# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。

---

# B100 实现计划：把「回合失败」与「执行失败」在事件层面分开

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增事件类型 `turn_failed` 表示「回合失败但任务仍在 `waiting_review`」，
让 `wait --follow` 不再把它误判为任务终结。

**Architecture:** 只动事件类型，不动状态机。`handleResult` 的失败分支改发新类型；
其余三个 `failed` 生产者保持不变；两个消费端按新类型分流。

**Tech Stack:** Go 1.26；前端 `web/`（Vite + TS + vitest）。

**设计依据：** `docs/superpowers/specs/2026-08-15-b100-turn-failed-event-design.md`
（**开工前完整读一遍**，尤其 §1.1 的四生产者表与 §5 的风险）。

## Global Constraints

- **不改任务状态机**：状态一个不加、迁移规则一条不改。
- **不改任何 `fail_reason` 文案**：改了会掩盖「客户端不许嗅探文案」是否被遵守。
- **不得引入按 `fail_reason` 文案判断的分支**（验收会检索）。
- `EventTypeTurnFailed` 的字面值固定为 `"turn_failed"`，不得改名。
- **两边已有的日志调用一条都不能丢**；新增分支按 `instrumenting-code` 补日志。
- 提交信息前缀 `feat(b100):` / `fix(b100):`。
- **不合并进任何长期分支，不 `git push` 到 `w4-delivery`/`main`**，只交你自己的任务分支。
- **不动 `~/.handoff`**（那是这台机器正在服役的数据目录）。
- 本次不做 B101 / B105，发现的新问题记进 ledger，不顺手修。

---

### Task 1: 新增事件类型常量

**Files:**
- Modify: `internal/proto/proto.go`

**Interfaces:**
- Produces: `proto.EventTypeTurnFailed EventType = "turn_failed"`

- [ ] **Step 1: 加常量与注释**

在 `EventTypeFailed` 之后加：

```go
	// EventTypeTurnFailed 表示**一个回合**失败了，而任务**仍然活着**——
	// handleResult 在发这条之前已经把任务迁到 waiting_review，协调者一个
	// continue 就能接着干。
	//
	// 为什么必须与 EventTypeFailed 分开而不是共用一个类型加个字段：
	// 客户端要据此决定「要不要收流、要不要报任务终结」，而这是一个封闭取值的
	// 判断，不能靠 fail_reason 的散文去猜（那是十来处各自措辞、改一句文案就能
	// 静默改掉客户端行为的东西）。分成两个类型还有一个好处：**旧客户端遇到未知
	// 类型会当普通事件继续跟随**，于是它不再假终态退出——bug 对旧 CLI 自动消失。
	//
	// 与 EventTypeCompleted 的关系：两者是**同一个状态迁移**（都进 waiting_review），
	// 所以消费端对它俩的行为必须一致，只是一个成功一个失败。
	EventTypeTurnFailed EventType = "turn_failed"
```

- [ ] **Step 2: 编译**

Run: `go build ./...`
Expected: 通过。

- [ ] **Step 3: Commit**

```bash
git add internal/proto/proto.go
git commit -m "feat(b100): 新增 turn_failed 事件类型——回合失败但任务仍在 waiting_review"
```

---

### Task 2: `handleResult` 改发 `turn_failed`

**Files:**
- Modify: `internal/agentd/manager.go`（`handleResult` 的 `!OK` 分支，现在发 `proto.EventTypeFailed`）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: `proto.EventTypeTurnFailed`（Task 1）

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/manager_test.go` 加（沿用该文件既有的 Manager 构造与 store 桩写法，
**不要自己另造一套** —— 照抄邻近用例如 `TestHandleResultSweepsProcsOnFail` 的搭建方式）：

```go
// TestHandleResultEmitsTurnFailedOnTurnFailure 钉死 B100 的正身：回合失败落的是
// turn_failed 而不是 failed，且任务此刻是 waiting_review（活着，可 continue）。
func TestHandleResultEmitsTurnFailedOnTurnFailure(t *testing.T) {
	// …构造 manager 与一个 running 任务（照抄邻近用例）…
	m.handleResult(taskID, executor.AdapterEvent{
		Type:   "result",
		Result: &executor.Result{OK: false, FailReason: "回合异常终止: boom"},
	})
	evs, err := st.Events(taskID, 0)
	if err != nil {
		t.Fatalf("读事件失败: %v", err)
	}
	last := evs[len(evs)-1]
	if last.Type != proto.EventTypeTurnFailed {
		t.Fatalf("回合失败应落 turn_failed，实际 %s", last.Type)
	}
	if last.Type == proto.EventTypeFailed {
		t.Fatal("回合失败落成 failed，会让 follow 误判任务终结")
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("读任务失败: %v", err)
	}
	if task.State != proto.TaskStateWaitingReview {
		t.Fatalf("回合失败后任务应在 waiting_review，实际 %s", task.State)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -count=1 -run TestHandleResultEmitsTurnFailedOnTurnFailure ./internal/agentd/`
Expected: FAIL，报「回合失败应落 turn_failed，实际 failed」。

- [ ] **Step 3: 改实现**

把 `handleResult` 里 `!OK` 分支的

```go
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeFailed,
			newFailedPayload(r.FailReason, r.Branch, r.CommitHash))
```

改成

```go
		// 类型是 turn_failed 而不是 failed：上面 transitToReview 已经把任务迁到
		// waiting_review，它**没有终结**。发 failed 会让 wait --follow 打出
		// 「任务已失败」并以 0 退出，而此时任务好端端等着审（B100 两次真机实测）。
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeTurnFailed,
			newFailedPayload(r.FailReason, r.Branch, r.CommitHash))
```

payload 构造函数**不换**（`newFailedPayload` 继续用），只换类型。

- [ ] **Step 4: 跑测试确认通过 + 全包回归**

Run: `go test -count=1 ./internal/agentd/`
Expected: 全 PASS。**若有既有用例因此变红，不要改测试去迁就实现——先判断那条
用例断言的是不是「回合失败落 failed」这个已被本次推翻的旧契约**；是的话按新契约
改断言并在 ledger 里记一行，不是的话说明实现改错了。

- [ ] **Step 5: 补一条反向用例，防止改过头**

```go
// TestStopEmitsFailed 防止 Task 2 改过头：协调者主动中止仍然是**任务终结**，
// 必须继续落 failed，否则 follow 再也收不了流。
func TestStopEmitsFailed(t *testing.T) {
	// …构造 manager 与一个 running 任务…
	if _, err := m.Stop(context.Background(), taskID); err != nil {
		t.Fatalf("stop 失败: %v", err)
	}
	evs, _ := st.Events(taskID, 0)
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeFailed {
			found = true
		}
		if e.Type == proto.EventTypeTurnFailed {
			t.Fatal("stop 落了 turn_failed，它是任务终结不是回合失败")
		}
	}
	if !found {
		t.Fatal("stop 没有落 failed 事件")
	}
	task, _ := st.GetTask(taskID)
	if task.State != proto.TaskStateFailed {
		t.Fatalf("stop 后任务应是 failed，实际 %s", task.State)
	}
}
```

Run: `go test -count=1 -run 'TestStopEmitsFailed|TestHandleResultEmitsTurnFailed' ./internal/agentd/`
Expected: 两条都 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(b100): handleResult 的回合失败改发 turn_failed，Stop 仍发 failed"
```

---

### Task 3: follow 不再对 `turn_failed` 收流

**Files:**
- Modify: `internal/client/client.go`（follow 循环里判 `proto.EventTypeFailed` 收流那一处）
- Test: `internal/client/client_test.go`（沿用该文件既有的假 WS 服务端写法）

- [ ] **Step 1: 写失败的测试**

```go
// TestFollowDoesNotStopOnTurnFailed 是 B100 的正身：回合失败不是任务终结，
// follow 必须继续跟随，后续事件仍要能投递到调用方。
func TestFollowDoesNotStopOnTurnFailed(t *testing.T) {
	// …起一个假 WS 服务端，依次推 turn_failed(seq=1) 与 progress(seq=2)…
	var got []proto.EventType
	err := c.Follow(ctx, taskID, func(ev *proto.Event) error {
		got = append(got, ev.Type)
		return nil
	})
	// 收到两条才算没被 turn_failed 掐断
	if len(got) != 2 {
		t.Fatalf("follow 在 turn_failed 之后被掐断了，只收到 %v", got)
	}
}

// TestFollowStopsOnFailed 防止 Task 3 改过头：真终态仍要收流。
func TestFollowStopsOnFailed(t *testing.T) {
	// …假服务端依次推 failed(seq=1) 与 progress(seq=2)…
	var got []proto.EventType
	_ = c.Follow(ctx, taskID, func(ev *proto.Event) error {
		got = append(got, ev.Type)
		return nil
	})
	if len(got) != 1 || got[0] != proto.EventTypeFailed {
		t.Fatalf("failed 之后 follow 应收流，实际收到 %v", got)
	}
}
```

（假服务端的具体搭建照抄该文件里既有的 follow 用例，**不要新造框架**。）

- [ ] **Step 2: 跑测试确认第一条失败**

Run: `go test -count=1 -run 'TestFollowDoesNotStopOnTurnFailed' ./internal/client/`
Expected: FAIL。

- [ ] **Step 3: 改实现**

原处的注释与判断整体替换为：

```go
			if ev.Type == proto.EventTypeFailed {
				// 只有**任务终结**才收流。回合失败走 turn_failed，它与 completed
				// 是同一个状态迁移（都进 waiting_review），所以行为也必须与
				// completed 一致——投递、不收流。
				//
				// 旧实现在这里把回合失败也收了流，还打「任务已失败」并以 0 退出，
				// 而任务其实好端端等着审（B100）。更糟的是它与 completed 行为相反，
				// 两个后果完全相同的事件走了两条路。
				c.log().Info("follow 结束：任务已终结", "task", taskID, "seq", ev.Seq)
				return errStopStream
			}
```

- [ ] **Step 4: 跑测试确认两条都通过**

Run: `go test -count=1 ./internal/client/`
Expected: 全 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/client/client.go internal/client/client_test.go
git commit -m "fix(b100): follow 只对 failed 收流，turn_failed 与 completed 同款继续跟随"
```

---

### Task 4: 自动同步触发集加上 `turn_failed`

**Files:**
- Modify: `cmd/wait.go`（`autoSyncAfterWait` 里 `ev.Type != completed && != failed` 那处）
- Test: `cmd/wait_test.go`（若无该文件则新建，沿用 `cmd` 包既有测试写法）

- [ ] **Step 1: 写失败的测试**

```go
// TestAutoSyncTriggersOnTurnFailed：回合失败同样要把代码拉回本地——这正是
// autoSyncAfterWait 自己注释里写的理由「失败恰恰是最需要把代码拉到本地翻的时候」。
func TestAutoSyncTriggersOnTurnFailed(t *testing.T) {
	// …构造一个 turn_failed 事件，断言 autoSyncAfterWait 不会在类型判断处提前返回…
}
```

若该函数不易直接测（它内部要建 client），**把类型判断抽成一个纯函数**再测：

```go
// shouldAutoSync 判断这类事件要不要触发自动同步。
//
// 为什么 turn_failed 也要：任务此刻在 waiting_review，协调者马上就要 diff 审代码，
// 而失败恰恰是最需要把代码拉到本地翻的时候。
func shouldAutoSync(t proto.EventType) bool {
	return t == proto.EventTypeCompleted || t == proto.EventTypeFailed || t == proto.EventTypeTurnFailed
}
```

然后测这个纯函数的三真一假（`progress` 为假）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -count=1 -run TestAutoSync ./cmd/`
Expected: FAIL。

- [ ] **Step 3: 改实现，让 `autoSyncAfterWait` 调用 `shouldAutoSync`**

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -count=1 ./cmd/`
Expected: 全 PASS。

- [ ] **Step 5: Commit**

```bash
git add cmd/
git commit -m "feat(b100): 自动同步触发集加入 turn_failed"
```

---

### Task 5: 穷举所有 `EventTypeFailed` 读取点并逐个判定

**Files:**
- Modify: 检索结论决定（至少含 `internal/agentd/mirror.go`；`web/` 若有事件类型枚举也算）
- Create: `docs/superpowers/notes/2026-08-15-b100-ledger.md`（ledger，含本 task 的完整清单）

**这是本计划最容易漏掉的一环，也是 spec §5 点名的唯一真风险：漏改一处消费端
= 一条静默消失的事件，且不会编译失败。**

- [ ] **Step 1: 穷举检索**

Run:
```bash
grep -rn "EventTypeFailed\|'failed'\|\"failed\"" internal/ cmd/ web/src/ | grep -v _test.go
```

- [ ] **Step 2: 逐个判定并写进 ledger**

对**每一个**命中点，在 ledger 里写一行：`文件:行号 | 它在做什么 | 改 / 不改 | 为什么`。
**少列一个即为本 task 未完成。**

判定口径：
- 「这里在判断**任务是否终结**」→ **不改**（`turn_failed` 本来就不是终结）；
- 「这里在判断**要不要唤醒/展示/镜像这条事件**」→ **要改**，把 `turn_failed` 一并纳入；
- 「这里在做**终态归档/清理**」→ **不改**，并在 ledger 里写明「turn_failed 不触发它是对的」。

- [ ] **Step 3: 专门看 `backlog_summary` 的对账**

`turn_failed` **既不进 `actionable`**（它不是待办工单）**也不能让对账把任务当死**。
在 ledger 里写明现有代码是否已经满足这两条；不满足就改，并补一条用例。

- [ ] **Step 4: 前端**

若 `web/` 里有事件类型到中文文案的映射，加一条 `turn_failed → 回合失败`。
没有就在 ledger 里写明「web/ 无事件类型枚举，无需改」——**要有结论，不能沉默**。

- [ ] **Step 5: 跑全量**

Run: `go build ./... && go vet ./... && go test -count=1 ./...`
Expected: 0 FAIL。
Run: `cd web && npx vitest run && npx tsc -b && npx vite build`
Expected: 0 error。（`web/` 有改动时才需要跑，没改动也建议跑一次确认没被带坏。）

- [ ] **Step 6: 检索确认没有文案嗅探**

Run: `grep -rn "fail_reason" internal/client cmd/ | grep -v _test.go`
Expected: 没有任何按文案内容判断分支的代码（只读取/透传是允许的）。把结论写进 ledger。

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(b100): 穷举 failed 读取点并逐个判定，补 ledger"
```

---

### Task 6: 文档

**Files:**
- Modify: `README.md`（事件表/事件分诊相关章节）

- [ ] **Step 1: 在事件说明处加 `turn_failed` 一行**

要写清三件事：它表示回合失败、任务仍在 `waiting_review`、协调者可 `continue` 续接。
并说明 `failed` 现在**专指任务终结**。

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs(b100): README 事件表补 turn_failed，并说明 failed 已收窄为任务终结"
```
