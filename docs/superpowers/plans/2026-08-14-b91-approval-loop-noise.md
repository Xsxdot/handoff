# B91 审批回路降噪 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 跨 gate kind 的同命令权限请求复用既有 allow 不再重复问人；`deny_guidance_dropped` 事件补 Publish 让 `wait` 能醒。

**Architecture:** 全部改动集中在 `internal/agentd/manager.go` 一个文件 + 其测试。指纹计算新增一个纯函数 `permFingerprintFor`（命令域 / 全文域二选一），三个既有触点换用它；两处 `deny_guidance_dropped` 落库点在 AppendEvent 成功后补 `m.hub.Publish(evt)`。不碰状态机、不碰工单契约、不碰 store 层（`FindReusableGrant` 的 SQL 原样）。

**Tech Stack:** Go；测试走 `internal/agentd` 包内白盒（真实 store + hub + chanAdapter 假 executor，基建函数 `newTestManager` 已存在）。

**Spec:** `docs/superpowers/specs/2026-08-14-b91-approval-loop-noise-design.md`

## Global Constraints

- **一行前端都不改**，改动只允许出现在 `internal/agentd/manager.go` 与 `internal/agentd/manager_test.go` 两个文件
- 日志用既有 `m.log`（slog），**禁止** `fmt.Printf`
- 注释中文，写「为什么」；所有新增/改签名的函数保持既有 doc comment 风格（参数/返回/注意）
- 指纹域隔离前缀是 `"cmd\x00"`（反斜杠-x-0-0 是 Go 字符串里的 NUL 字节转义，不是四个字符的字面量）
- 只复用 allow、只在同任务内复用、查询失败 fail-closed 照常问人——B57 既有裁决全部原样保留，不许顺手改
- 每个 task 完成即 commit；回归统一 `go test -count=1`（不吃缓存）

## 已核实的既有事实（实现时以此为准，不要再猜）

1. `permFingerprint(text)` 在 `manager.go:1802`，就是 `sha256 → hex`。它**保留不动**，新函数包着它用。
2. 三个换键触点：
   - `manager.go:1415`（`escalatePermission` 建单）：`Fingerprint: permFingerprint(ev.Text)`
   - `manager.go:1721`（`approvePermission` 建单）：`Fingerprint: permFingerprint(permission)`——该函数签名里**没有** `ev`，要加参数
   - `manager.go:1820`（`reuseDecision` 查询）：`fp := permFingerprint(ev.Text)`
3. `approvePermission` 全仓只有两个调用点，`ev` 都在作用域内：
   - `manager.go:1649`：`m.approvePermission(taskID, ticketID, ev.PermissionID, ev.Text, d.Reason, "approver")`
   - `manager.go:1846`：`m.approvePermission(taskID, ticketID, ev.PermissionID, ev.Text, "复用工单 "+prior.ID+" 的人工批准", "reuse")`
4. `deny_guidance_dropped` 两个落库点，都是 `if _, err := m.st.AppendEvent(...)` 丢弃返回的事件：
   - `clearApproverState`（`manager.go` ~1677，内联）
   - `appendGuidanceDropped`（`manager.go` ~1946，helper，`Send` 失败路径专用）
5. `AppendEvent` 返回 `(proto.Event, error)`；全文件既有广播形态是落库成功后 `m.hub.Publish(evt)`（如 `manager.go:982`）。
6. 测试基建：`newTestManager(t)` 返回 `(*Manager, *store.Store, *Hub, *chanAdapter)`；`mustCreateTask` 直接落库任务；`hub.Subscribe(taskID)` 返回 `(<-chan proto.Event, func())`；假 adapter 的 `ad.permsRec()` 记录 `RespondPermission` 实参（形如 `"p2:once"`）。样板：`TestPermissionReuseSkipsSecondTicket`（`manager_test.go:1643`）。
7. `executor.PermRequest{Tool, Command, Paths}` 在 `executor.go:103`；`executor.PermToolBash` / `executor.PermToolWrite` 常量存在。
8. 审批者自动批准的工单同样落 `answer='allow'` + `markDelivered`，**参与** `FindReusableGrant` 复用——所以 `approvePermission` 触点漏改会静默缩窄复用面，Task 2 有用例专门盯它。

---

### Task 1: `permFingerprintFor` 纯函数

**Files:**
- Modify: `internal/agentd/manager.go`（`permFingerprint` 旁边加新函数）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Produces: `func permFingerprintFor(ev executor.AdapterEvent) string`——Task 2 的三个触点都调它

- [ ] **Step 1: 写失败测试**

```go
// TestPermFingerprintForDomains 验证 B91 指纹换键的域规则：
// 有命令 → 命令域（跨 gate kind 相等）；无命令 → 全文域（B57 原行为）；
// 两域之间即使文本相同也永不相撞（cmd\x00 前缀隔离）。
func TestPermFingerprintForDomains(t *testing.T) {
	cmd := "rm node_modules && cd /w && git worktree remove /tmp/b89-base --force"

	extDir := executor.AdapterEvent{
		Text: "external_directory: " + cmd,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd, Paths: []string{"/tmp"}},
	}
	bash := executor.AdapterEvent{
		Text: "bash: " + cmd,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd},
	}
	if permFingerprintFor(extDir) != permFingerprintFor(bash) {
		t.Fatal("同命令的 external_directory 与 bash 形态指纹应相等（命令域）")
	}

	// 无命令（纯路径 edit 类、fail-closed 类）：维持全文域
	pure := executor.AdapterEvent{
		Text: "edit: probe.md",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{"probe.md"}},
	}
	if permFingerprintFor(pure) != permFingerprint("edit: probe.md") {
		t.Fatal("无命令时应退回全文指纹（B57 原行为）")
	}
	nilPerm := executor.AdapterEvent{Text: "看不懂的权限描述"}
	if permFingerprintFor(nilPerm) != permFingerprint("看不懂的权限描述") {
		t.Fatal("Perm 为 nil 时应退回全文指纹")
	}

	// 域隔离：全文域算出的指纹与命令域算同一串文本的指纹不同
	same := "echo hi"
	textDomain := executor.AdapterEvent{Text: same}
	cmdDomain := executor.AdapterEvent{Text: "bash: " + same, Perm: &executor.PermRequest{Command: same}}
	if permFingerprintFor(textDomain) == permFingerprintFor(cmdDomain) {
		t.Fatal("命令域与全文域相撞，cmd\\x00 前缀隔离失效")
	}
}
```

- [ ] **Step 2: 跑测试确认编译失败**

Run: `go test ./internal/agentd/ -run TestPermFingerprintForDomains -count=1`
Expected: FAIL（`permFingerprintFor` 未定义）

- [ ] **Step 3: 实现**

放在 `permFingerprint` 定义的紧下方：

```go
// permFingerprintFor 计算一次权限请求的裁决指纹，是所有建单/查询点的唯一入口。
//
// 域规则（B91）：
//   - Perm.Command 非空 → 命令域：sha256("cmd\x00" + command)。同一条命令被
//     opencode 以 external_directory 与 bash 两种 kind 各发一次时（双胞胎工单，
//     见 B91 spec §1.1），两次算出同一指纹，第二次得以复用首次的人工批准。
//   - 否则（纯路径的 edit/write 类、提取不出结构的 fail-closed 类）→ 全文域：
//     沿用 B57 的权限描述全文指纹，行为不变。
//
// "cmd\x00" 前缀做域隔离：命令域与全文域即使文本相同也永不相撞，杜绝
// 「某段权限描述全文恰好等于另一条命令文本」的伪命中。
//
// 注意：写入（建单）与查询（reuseDecision）必须都走本函数——两边规则不一致
// 会让复用静默失效，那正是 B91 要修的缺陷形态。
func permFingerprintFor(ev executor.AdapterEvent) string {
	if ev.Perm != nil && ev.Perm.Command != "" {
		return permFingerprint("cmd\x00" + ev.Perm.Command)
	}
	return permFingerprint(ev.Text)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestPermFingerprintForDomains -count=1`
Expected: PASS

- [ ] **Step 5: 注释自检**

纯函数无日志点（无 I/O、无错误分支）。确认 doc comment 说清了域规则、隔离前缀的为什么、写入/查询必须同函数的警告——上面代码块已含，照抄即可。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): permFingerprintFor 指纹域函数——命令域跨 gate kind 一致（B91）"
```

---

### Task 2: 三触点换键 + 跨 kind 复用

**Files:**
- Modify: `internal/agentd/manager.go:1415`（escalate 建单）、`:1721` 与签名 `:1714`（approvePermission）、`:1820`（reuseDecision）、两个调用点 `:1649` `:1846`
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: Task 1 的 `permFingerprintFor(ev)`
- Produces: `approvePermission(taskID, ticketID, permID, permission, fp, reason, source string)`——新增 `fp` 参数，指纹由调用方算好传入

- [ ] **Step 1: 写失败测试（三条）**

```go
// TestPermissionReuseAcrossGateKinds 验证 B91 主场景：external_directory 形态
// 的命令获人工 allow 且送达后，同命令的 bash 形态到达 → 自动放行零唤醒。
// 复刻 B89 任务 4356d318 seq 630/631 的真机形态。
func TestPermissionReuseAcrossGateKinds(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	cmd := "rm node_modules && cd /w && git worktree remove /tmp/b89-base --force"

	// 第一次：external_directory 形态升级人工 → 批准 → 送达
	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "external_directory: " + cmd,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd, Paths: []string{"/tmp"}},
	}, "T1:p1")
	if err := st.AnswerTicket("T1:p1", "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	m.markDelivered("T1", "T1:p1")

	// 第二次：bash 形态、同一条命令、不同 perm id
	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p2", Text: "bash: " + cmd,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd},
	}, "T1:p2")

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("跨 kind 复用未命中，仍有 %d 张挂起工单：%+v", len(pending), pending)
	}
	if perms := ad.permsRec(); len(perms) == 0 || perms[len(perms)-1] != "p2:once" {
		t.Fatalf("RespondPermission 实参 = %v，期望末条 p2:once", perms)
	}
}

// TestPermissionReuseCommandMismatch 验证只差一个字符的命令不复用、照常升级。
func TestPermissionReuseCommandMismatch(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "external_directory: rm -rf /tmp/a",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "rm -rf /tmp/a"},
	}, "T1:p1")
	if err := st.AnswerTicket("T1:p1", "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	m.markDelivered("T1", "T1:p1")

	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p2", Text: "bash: rm -rf /tmp/b",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "rm -rf /tmp/b"},
	}, "T1:p2")

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("不同命令不该复用，期望 1 张挂起工单，实得 %d", len(pending))
	}
}

// TestPermissionReuseFromApproverGrant 验证审批者（廉价模型）自动批准的工单
// 同样是复用先例——approvePermission 触点漏换指纹键会静默缩窄复用面（spec §7）。
func TestPermissionReuseFromApproverGrant(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	cmd := "go test ./..."
	ev1 := executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "external_directory: " + cmd,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd, Paths: []string{"/x"}},
	}
	// 审批者路径直接批准（建单+allow+送达一条龙）
	m.approvePermission("T1", "T1:p1", "p1", ev1.Text, permFingerprintFor(ev1), "低危命令", "approver")

	// bash 形态同命令到达 → 应命中复用
	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p2", Text: "bash: " + cmd,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd},
	}, "T1:p2")

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("审批者先例未参与复用，挂起工单 %d 张，期望 0", len(pending))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestPermissionReuseAcrossGateKinds|TestPermissionReuseCommandMismatch|TestPermissionReuseFromApproverGrant' -count=1`
Expected: FAIL——`TestPermissionReuseFromApproverGrant` 编译错（`approvePermission` 还没有 fp 参数）；改签名前先注释掉该用例跑前两条，`AcrossGateKinds` 应 FAIL 在「仍有 1 张挂起工单」（指纹不同、复用不命中），`CommandMismatch` 天然 PASS（现状本来就不复用）——它是防回归用例。

- [ ] **Step 3: 实现换键**

四处改动：

```go
// ① manager.go:1415（escalatePermission 建单）
		Fingerprint: permFingerprintFor(ev),

// ② manager.go:1820（reuseDecision 查询）
	fp := permFingerprintFor(ev)

// ③ approvePermission 签名加 fp 参数（doc comment 的参数段同步补一行：
//    fp 是调用方用 permFingerprintFor 算好的裁决指纹——本函数只收文本串、
//    拿不到 ev.Perm，不在函数内重新猜域）
func (m *Manager) approvePermission(taskID, ticketID, permID, permission, fp, reason, source string) {
	// …
		Fingerprint: fp,   // 原 permFingerprint(permission)

// ④ 两个调用点补实参
// manager.go:1649
		m.approvePermission(taskID, ticketID, ev.PermissionID, ev.Text, permFingerprintFor(ev), d.Reason, "approver")
// manager.go:1846
	m.approvePermission(taskID, ticketID, ev.PermissionID, ev.Text, permFingerprintFor(ev),
		"复用工单 "+prior.ID+" 的人工批准", "reuse")
```

- [ ] **Step 4: 跑新测试 + B57 既有用例回归**

Run: `go test ./internal/agentd/ -run 'TestPermissionReuse|TestPermFingerprint' -count=1 -v | tail -20`
Expected: 全 PASS。特别确认 `TestPermissionReuseSkipsSecondTicket` 与 `TestPermissionReuseIgnoresDeny`（纯 Text 事件，走全文域）不红——它们红了说明全文域回退被改坏。

- [ ] **Step 5: 日志与注释自检**

- `reuseDecision` 既有日志打 `fingerprint fp[:8]`，换键后照旧成立，不用动
- `approvePermission` doc comment 的参数列表补了 `fp` 一行（Step 3 ③）
- 无新增错误分支，无新增日志点需求

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): 权限复用指纹换命令域，跨 gate kind 双胞胎工单不再重复问人（B91）"
```

---

### Task 3: `deny_guidance_dropped` 两落点补 Publish

**Files:**
- Modify: `internal/agentd/manager.go`（`clearApproverState` 内联落库点 ~1677、`appendGuidanceDropped` ~1946）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: `hub.Subscribe(taskID) (<-chan proto.Event, func())`、`proto.EventTypeDenyGuidanceDropped`

- [ ] **Step 1: 写失败测试（两条路径各一）**

```go
// TestDenyGuidanceDroppedWakesOnTurnEnd 验证 B91：回合终结时挂着的拒绝原因
// 被丢弃，事件必须 Publish 唤醒 wait——只落库的话审核者拿着 reply 的
// {"ok":true} 永远不知道裁决空转了。
func TestDenyGuidanceDroppedWakesOnTurnEnd(t *testing.T) {
	m, st, hub, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ch, cancel := hub.Subscribe("T1")
	defer cancel()

	m.apMu.Lock()
	m.denyGuidance["T1"] = "别删，先 git mv 归档"
	m.apMu.Unlock()
	m.clearApproverState("T1")

	select {
	case e := <-ch:
		if e.Type != proto.EventTypeDenyGuidanceDropped {
			t.Fatalf("收到事件类型 %s，期望 deny_guidance_dropped", e.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clearApproverState 丢弃拒绝原因后没有 Publish，wait 不会醒")
	}
}

// TestDenyGuidanceDroppedWakesOnSendFailure 验证 Send 失败路径（helper）同样唤醒。
func TestDenyGuidanceDroppedWakesOnSendFailure(t *testing.T) {
	m, st, hub, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ch, cancel := hub.Subscribe("T1")
	defer cancel()

	m.appendGuidanceDropped("T1", "换个姿势重试", errors.New("send: broken pipe"))

	select {
	case e := <-ch:
		if e.Type != proto.EventTypeDenyGuidanceDropped {
			t.Fatalf("收到事件类型 %s，期望 deny_guidance_dropped", e.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("appendGuidanceDropped 没有 Publish，wait 不会醒")
	}
}
```

（`errors` 若未导入需补 import。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDenyGuidanceDroppedWakes -count=1`
Expected: 两条都 FAIL 在 2s 超时（只落库不广播）

- [ ] **Step 3: 实现**

两处把丢弃返回值改为接住并广播，注释写清语义升格的为什么：

```go
// clearApproverState 内联处（原 if _, err := ...）：
		// Publish 而不是只落库（B91）：这条事件是可操作唤醒——审核者拿到的
		// reply 返回是 {"ok":true}，不叫醒的话他永远不知道那句 reason 空转了，
		// 唯一的补救动作（把话写进 continue）也就无从发生。progress /
		// approver_decision 不唤醒的先例不适用：那些没有审核者动作可做，这条有。
		evt, err := m.st.AppendEvent(taskID, proto.EventTypeDenyGuidanceDropped,
			denyGuidancePayload{
				Reason: guidance,
				Cause:  "回合在拒绝原因下发前终结（Done/stop/result），未送达 executor",
			})
		if err != nil {
			m.log.Error("追加 deny_guidance_dropped 事件失败", "task", taskID, "cause", err)
		} else {
			m.hub.Publish(evt)
		}

// appendGuidanceDropped：
func (m *Manager) appendGuidanceDropped(taskID, guidance string, cause error) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeDenyGuidanceDropped,
		denyGuidancePayload{Reason: guidance, Cause: cause.Error()})
	if err != nil {
		m.log.Error("追加 deny_guidance_dropped 事件失败", "task", taskID, "cause", err)
	} else {
		// 同 clearApproverState 处的理由（B91）：可操作唤醒，不是纯审计
		m.hub.Publish(evt)
	}
	m.log.Warn("拒绝原因未下发：回合已终结，用 continue 自己把话带上",
		"task", taskID, "reason", truncateRunes(guidance, 80), "cause", cause)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestDenyGuidanceDroppedWakes -count=1`
Expected: PASS

- [ ] **Step 5: 日志与注释自检**

- 两处既有 Warn/Error 日志原样保留（错误分支带 task+cause 上下文 ✓）
- 新增注释解释「为什么 Publish」且点名与 progress 先例的区别（Step 3 已含）
- 成功路径不静默：Publish 本身无日志，但紧随的 Warn（拒绝原因未下发）就是这条路径的可见输出 ✓

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "fix(agentd): deny_guidance_dropped 补 Publish，审核者的 reason 被丢弃时 wait 能醒（B91）"
```

---

### Task 5: 路径域指纹（spec §3.5，08-14 派发中追加）

> **追加说明**：本 task 是任务派发后追加的（spec §3.5）。触发它的是这个任务
> **自己**的实况：seq 678 主 agent 批准了 `external_directory:
> /var/folders/…/T/opencode/*`，28 秒后 seq 679 子 agent 又问了同一个目录——
> 只因权限描述多了 `[子 agent: Task 1 审查（双裁决）]` 前缀。Task 1 的命令域
> 治不了它：这类请求 `Perm.Command` 为空，落回全文域，而全文含前缀。

**Files:**
- Modify: `internal/agentd/manager.go`（只改 `permFingerprintFor` 一个函数体）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: Task 1 的 `permFingerprintFor(ev)`；`executor.PermRequest{Tool, Command, Paths}`
- Produces: 无新签名。三个域的优先级固定为 **命令域 > 路径域 > 全文域**

`sort` 与 `strings` 在 `manager.go` 的 import 块里已存在，不要动 import。

- [ ] **Step 1: 写失败测试**

```go
// TestPermFingerprintForPathDomain 验证 B91 spec §3.5 路径域：同路径同 Tool
// 跨子 agent 前缀相等、Paths 顺序无关、Tool 不同则不等。
func TestPermFingerprintForPathDomain(t *testing.T) {
	dir := "/var/folders/xc/hpx9c9w153j7tvphw53lc8qr0000gn/T/opencode/*"

	// 主 agent 与子 agent 的同一个目录请求：Text 带前缀，Perm 相同
	main := executor.AdapterEvent{
		Text: "external_directory: " + dir,
		Perm: &executor.PermRequest{Tool: "external_directory", Paths: []string{dir}},
	}
	child := executor.AdapterEvent{
		Text: "[子 agent: Task 1 审查（双裁决） (@general subagent)] external_directory: " + dir,
		Perm: &executor.PermRequest{Tool: "external_directory", Paths: []string{dir}},
	}
	if permFingerprintFor(main) != permFingerprintFor(child) {
		t.Fatal("同路径同 Tool 应跨子 agent 前缀相等（路径域忽略 Text）")
	}

	// Paths 顺序无关
	ab := executor.AdapterEvent{Text: "x", Perm: &executor.PermRequest{Tool: "edit", Paths: []string{"/a", "/b"}}}
	ba := executor.AdapterEvent{Text: "y", Perm: &executor.PermRequest{Tool: "edit", Paths: []string{"/b", "/a"}}}
	if permFingerprintFor(ab) != permFingerprintFor(ba) {
		t.Fatal("Paths 顺序不同应算同一指纹（排序后拼接）")
	}

	// Tool 不同则不等：edit 与 external_directory 对同一路径含义不同
	edit := executor.AdapterEvent{Text: "z", Perm: &executor.PermRequest{Tool: "edit", Paths: []string{dir}}}
	if permFingerprintFor(edit) == permFingerprintFor(main) {
		t.Fatal("同路径不同 Tool 必须算不同指纹——写文件与越界目录授权不是一件事")
	}

	// 命令域优先于路径域：同时带命令与路径时走命令域
	both := executor.AdapterEvent{
		Text: "external_directory: rm -rf /x",
		Perm: &executor.PermRequest{Tool: "bash", Command: "rm -rf /x", Paths: []string{dir}},
	}
	onlyCmd := executor.AdapterEvent{
		Text: "bash: rm -rf /x",
		Perm: &executor.PermRequest{Tool: "bash", Command: "rm -rf /x"},
	}
	if permFingerprintFor(both) != permFingerprintFor(onlyCmd) {
		t.Fatal("有命令时必须走命令域，路径不参与")
	}

	// 三域互不相撞
	empty := executor.AdapterEvent{Text: "external_directory: " + dir}
	if permFingerprintFor(empty) == permFingerprintFor(main) {
		t.Fatal("全文域与路径域相撞，paths\\x00 前缀隔离失效")
	}
}

// TestPermissionReusePathAcrossSubagents 验证端到端：主 agent 批过的目录，
// 子 agent 再问时自动放行零唤醒。复刻任务 d912b23a seq 678/679 的真机形态。
func TestPermissionReusePathAcrossSubagents(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	dir := "/var/folders/xc/abc/T/opencode/*"
	perm := &executor.PermRequest{Tool: "external_directory", Paths: []string{dir}}

	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1",
		Text: "external_directory: " + dir, Perm: perm,
	}, "T1:p1")
	if err := st.AnswerTicket("T1:p1", "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	m.markDelivered("T1", "T1:p1")

	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p2",
		Text: "[子 agent: Task 1 审查（双裁决） (@general subagent)] external_directory: " + dir,
		Perm: &executor.PermRequest{Tool: "external_directory", Paths: []string{dir}},
	}, "T1:p2")

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("子 agent 的同目录请求未复用，挂起工单 %d 张，期望 0", len(pending))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestPermFingerprintForPathDomain|TestPermissionReusePathAcrossSubagents' -count=1`
Expected: FAIL——路径域还没实现，跨前缀两条断言都不成立

- [ ] **Step 3: 实现**

`permFingerprintFor` 函数体改为三域优先级，doc comment 同步补路径域一段：

```go
func permFingerprintFor(ev executor.AdapterEvent) string {
	if ev.Perm != nil {
		if ev.Perm.Command != "" {
			return permFingerprint("cmd\x00" + ev.Perm.Command)
		}
		if len(ev.Perm.Paths) > 0 {
			// 路径域：拷贝后排序，不原地改 ev.Perm.Paths——该切片来自 adapter，
			// 排序它会让调用方看到的顺序被本函数悄悄改掉
			paths := append([]string(nil), ev.Perm.Paths...)
			sort.Strings(paths)
			// Tool 必须进指纹：edit 与 external_directory 对同一路径含义不同
			// （写这个文件 vs 授权越界访问这个目录），裸路径合并等于把两种
			// 授权当成一件事。NUL 作分隔符——路径不可能含 NUL，杜绝
			// ["a","b/c"] 与 ["a/b","c"] 这类拼接歧义
			return permFingerprint("paths\x00" + ev.Perm.Tool + "\x00" + strings.Join(paths, "\x00"))
		}
	}
	return permFingerprint(ev.Text)
}
```

doc comment 的域规则段补一条（放在命令域与全文域之间）：

```
//   - Command 为空但 Paths 非空 → 路径域：sha256("paths\x00" + Tool + "\x00" +
//     排序后的 Paths)。治的是「同一个目录被每个子 agent 各问一次」——子 agent
//     前缀只加在 Text 上（adapter.go:1248），Perm 不带它，所以路径域天然忽略。
//     Tool 进指纹是硬要求，理由见函数体内注释。
```

- [ ] **Step 4: 跑测试 + 前四个 task 的用例全回归**

Run: `go test ./internal/agentd/ -run 'TestPerm|TestDenyGuidance' -count=1 -v | tail -25`
Expected: 全 PASS。特别确认 Task 1 的 `TestPermFingerprintForDomains` 里
「无命令时退回全文指纹」那两条断言——它们用的 `pure` 事件带 `Paths`，
**加了路径域后会改走路径域，这条断言必须同步改**：把 `pure` 的期望从
`permFingerprint("edit: probe.md")` 改为路径域算式，或把 `pure` 的 `Paths`
去掉只留 `Tool`。选后者更省事，但要在注释里写明「全文域现在只兜底
Perm 为 nil 或 Command/Paths 皆空」。

- [ ] **Step 5: 日志与注释自检**

纯函数无日志点。确认：函数体两处「为什么」注释（不原地排序、Tool 进指纹）
+ doc comment 的路径域规则段都在。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): 指纹加路径域，同目录跨子 agent 不再重复问人（B91 §3.5）"
```

---

### Task 4: 全量回归收口

**Files:** 无新改动；只跑命令与修格式

- [ ] **Step 1: 四件套**

```bash
go build ./... && go vet ./... && gofmt -l .
go test -count=1 ./...
```

Expected: build/vet 无错，`gofmt -l .` **无输出**（有输出就 `gofmt -w` 修掉后重跑），`go test` 全绿。

- [ ] **Step 2: 把真实数字写进交付说明**

包数、用例结果原文（不要复述「全绿」，贴输出尾部）。

- [ ] **Step 3: Commit（如 gofmt 有修动）**

```bash
git add -A && git commit -m "chore: gofmt（B91 收口）"
```

---

## 不在本 plan 内（审核者本地做）

- handoff skill 事件分诊表补 `deny_guidance_dropped` 一行——skill 文件在审核者机器 `~/.claude/skills/handoff/`，executor 碰不到
- spec §6 验收项 7 的真机复现——审核阶段由审核者在 mac-02 真机做
