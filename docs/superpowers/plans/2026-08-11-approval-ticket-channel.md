# 审批工单通道整顿（B57② + B58 + B50）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让审核者不再为同一件事被反复叫醒、不再面对答不掉的幽灵工单、`--deny --reason` 的原因真的送到模型手里。

**Architecture:** 三块改动全部落在 `internal/agentd/manager.go` 的权限/工单路径上，各自有一处最小的外围配套：B57② 需要 `internal/store` 加一列指纹并提供复用查询；B58 需要 `executor.AdapterEvent` 加一个可选 id 字段并由 opencode adapter 填；B50 纯在 manager 内部完成，不动五动作契约。三块彼此无代码冲突，但共用同一次真机验收。

**Tech Stack:** Go 1.x、SQLite（`modernc.org/sqlite`）、标准库 `log/slog`、`crypto/sha256`

## Global Constraints

- **不改 `Adapter` 接口的任何方法签名**——`RespondPermission(ctx, taskID, permID, decision string) error` 一个字不动（spec §2.4、§6）。
- **「非 allow 一律 reject」的安全语义不变**（spec §5.1）。本计划只新增旁路信息，不改任何裁决判据。
- **只复用 allow，不复用 deny；不跨任务复用**（spec §3.3、§3.4）。
- **日志用 `m.log` / `a.log`（`log/slog`），禁止 `fmt.Printf`**；错误分支必须带上下文与 cause。
- 新文件必须有文件头注释（职责 + 边界）；新增导出方法必须有 doc 注释；非显然分支必须有「为什么」的中文注释。
- SQLite 迁移一律「单列 `ALTER TABLE … ADD COLUMN` + 容忍 `duplicate column`」，照 `tickets.delivered_at` 既有写法（`internal/store/store.go:113`）。
- 每个 task 结束前跑该包的测试；全部 task 完成后跑公共门槛（Task 6）。
- 本项目惯例：逐 task 派发 devbox 执行，每 task 独立提交。

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/proto/proto.go` | 修改 | `Ticket` 加 `Fingerprint` 字段；新增三个事件类型常量 |
| `internal/store/store.go` | 修改 | 建表加 `fingerprint` 列 + 迁移；`CreateTicket` 写入该列；新增 `FindReusableGrant` |
| `internal/store/store_test.go` | 修改 | 指纹写入回读、复用查询四条件 |
| `internal/agentd/manager.go` | 修改 | `permFingerprint` / `reuseDecision` / `approvePermission` 加 source / `handleQuestion` 三岔 / `gateDecision` 双返回值 / `denyGuidance` 挂起消费 |
| `internal/agentd/manager_test.go` | 修改 | 复用、三岔、guidance 的白盒测试 |
| `internal/executor/executor.go` | 修改 | `AdapterEvent` 加 `QuestionID` |
| `internal/executor/opencode/adapter.go` | 修改 | 三处 question emit 填 `QuestionID` |
| `internal/executor/opencode/question_test.go` | 修改 | emit 带原生 id |

不新建文件——三块改动都是既有路径上的增量，独立成文件反而把「权限中介」这件事拆到两个地方。

---

## Task 1: store 层指纹列与复用查询

**Files:**
- Modify: `internal/proto/proto.go`（`Ticket` 结构体，约 :151-163）
- Modify: `internal/store/store.go`（建表 :94-97、迁移 :113-118 附近、`CreateTicket` :544）
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: 无（本计划的第一个 task）
- Produces:
  - `proto.Ticket.Fingerprint string`（json tag `fingerprint`）
  - `func (s *Store) FindReusableGrant(taskID, fingerprint string) (*proto.Ticket, error)` —— 返回同任务、同指纹、`answer` 严格等于 `"allow"`、`delivered_at` 非空的最近一条 gate 工单；无匹配返回 `(nil, nil)`

- [ ] **Step 1: 写失败测试——指纹随工单落库并可回读**

追加到 `internal/store/store_test.go`：

```go
// TestTicketFingerprintRoundTrip 验证 gate 工单的 fingerprint 列落库并可回读，
// 且未填该列的旧式工单回读为空串（旧库兼容）。
func TestTicketFingerprintRoundTrip(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	withFP := &proto.Ticket{
		ID: "t1:p1", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate","permission":"bash: ls"}`),
		Fingerprint: "abc123", CreatedAt: time.Now().UTC(),
	}
	if _, err := s.CreateTicket(withFP); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	got, err := s.GetTicket("t1:p1")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.Fingerprint != "abc123" {
		t.Fatalf("Fingerprint = %q, 期望 abc123", got.Fingerprint)
	}

	noFP := &proto.Ticket{
		ID: "t1:q1", TaskID: "t1", Kind: "ask",
		Request: json.RawMessage(`{"kind":"ask","question":"x"}`),
		CreatedAt: time.Now().UTC(),
	}
	if _, err := s.CreateTicket(noFP); err != nil {
		t.Fatalf("CreateTicket(ask): %v", err)
	}
	got2, err := s.GetTicket("t1:q1")
	if err != nil {
		t.Fatalf("GetTicket(ask): %v", err)
	}
	if got2.Fingerprint != "" {
		t.Fatalf("ask 工单 Fingerprint = %q, 期望空串", got2.Fingerprint)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run TestTicketFingerprintRoundTrip -v`
Expected: 编译失败，`proto.Ticket` 没有 `Fingerprint` 字段

- [ ] **Step 3: 给 proto.Ticket 加字段**

在 `internal/proto/proto.go` 的 `Ticket` 结构体末尾（`DeliveredAt` 之后）加：

```go
	// Fingerprint 是 gate 工单的裁决指纹：权限描述全文的 sha256 十六进制串。
	// 它让「审核者是不是已经就同一件事表过态」成为一次索引查询而不是全表扫文本。
	// ask 工单不参与复用，留空。
	Fingerprint string `json:"fingerprint"`
```

- [ ] **Step 4: 建表加列 + 旧库迁移 + CreateTicket 写入**

`internal/store/store.go` 的 `tickets` 建表 DDL 末尾加列：

```sql
  delivered_at TIMESTAMP, fingerprint TEXT NOT NULL DEFAULT '')
```

紧跟 `tickets.delivered_at` 那段迁移之后，照同样写法加一段：

```go
	// 迁移：为旧库补 tickets.fingerprint 列（B57②）。
	//
	// why 容忍 duplicate column：SQLite 无 ADD COLUMN IF NOT EXISTS，也不支持
	// 一次加多列，只能逐条 ALTER；列已存在时报 duplicate column 属预期。
	// 旧库里既有工单的 fingerprint 为默认空串——空指纹永不参与复用
	// （FindReusableGrant 对空 fingerprint 直接返回无匹配），旧数据不会被误当先例。
	if _, err := db.ExecContext(context.Background(),
		`ALTER TABLE tickets ADD COLUMN fingerprint TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		db.Close()
		return nil, fmt.Errorf("迁移 tickets.fingerprint: %w", err)
	}
```

`CreateTicket` 的 INSERT 补上该列：

```go
	res, err := s.db.ExecContext(context.Background(), `
INSERT OR IGNORE INTO tickets (id, task_id, kind, request, answer, created_at, answered_at, fingerprint)
VALUES (?, ?, ?, ?, NULL, ?, NULL, ?)`,
		tk.ID, tk.TaskID, tk.Kind, string(tk.Request), fmtTime(tk.CreatedAt), tk.Fingerprint)
```

`GetTicket` 与 `PendingTickets` 的 SELECT 列表补 `fingerprint` 并扫进 `tk.Fingerprint`（`GetTicket` 用普通 `string` 变量即可，列有 NOT NULL DEFAULT）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/store/ -run TestTicketFingerprintRoundTrip -v`
Expected: PASS

- [ ] **Step 6: 写复用查询的失败测试（四条件）**

```go
// TestFindReusableGrant 钉住复用的四个条件：同任务、同指纹、answer 严格等于
// "allow"、delivered_at 非空。任一不满足都必须查不到——查到就等于静默放行。
func TestFindReusableGrant(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	mk := func(id, taskID, fp string) {
		t.Helper()
		if _, err := s.CreateTicket(&proto.Ticket{
			ID: id, TaskID: taskID, Kind: "gate",
			Request:     json.RawMessage(`{"kind":"gate","permission":"bash: go build"}`),
			Fingerprint: fp, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("CreateTicket(%s): %v", id, err)
		}
	}

	// 命中：同任务同指纹、已 allow、已送达
	mk("t1:p1", "t1", "FP")
	if err := s.AnswerTicket("t1:p1", "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	if err := s.MarkTicketDelivered("t1:p1"); err != nil {
		t.Fatalf("MarkTicketDelivered: %v", err)
	}
	got, err := s.FindReusableGrant("t1", "FP")
	if err != nil {
		t.Fatalf("FindReusableGrant: %v", err)
	}
	if got == nil || got.ID != "t1:p1" {
		t.Fatalf("命中用例返回 %+v，期望 t1:p1", got)
	}

	// 不命中之一：跨任务
	if got, err := s.FindReusableGrant("t2", "FP"); err != nil || got != nil {
		t.Fatalf("跨任务不应命中，得到 %+v err=%v", got, err)
	}
	// 不命中之二：指纹不同
	if got, err := s.FindReusableGrant("t1", "OTHER"); err != nil || got != nil {
		t.Fatalf("异指纹不应命中，得到 %+v err=%v", got, err)
	}
	// 不命中之三：指纹为空（旧库工单）
	if got, err := s.FindReusableGrant("t1", ""); err != nil || got != nil {
		t.Fatalf("空指纹不应命中，得到 %+v err=%v", got, err)
	}

	// 不命中之四：answer 是 deny
	mk("t1:p2", "t1", "FPDENY")
	if err := s.AnswerTicket("t1:p2", "deny: 太危险"); err != nil {
		t.Fatalf("AnswerTicket(deny): %v", err)
	}
	if err := s.MarkTicketDelivered("t1:p2"); err != nil {
		t.Fatalf("MarkTicketDelivered(deny): %v", err)
	}
	if got, err := s.FindReusableGrant("t1", "FPDENY"); err != nil || got != nil {
		t.Fatalf("deny 不应命中，得到 %+v err=%v", got, err)
	}

	// 不命中之五：已 allow 但未送达
	mk("t1:p3", "t1", "FPUNDELIVERED")
	if err := s.AnswerTicket("t1:p3", "allow"); err != nil {
		t.Fatalf("AnswerTicket(undelivered): %v", err)
	}
	if got, err := s.FindReusableGrant("t1", "FPUNDELIVERED"); err != nil || got != nil {
		t.Fatalf("未送达不应命中，得到 %+v err=%v", got, err)
	}
}
```

> 送达标记的方法是 `store.MarkTicketDelivered(id)`（`internal/store/store.go:679`），manager 侧的封装是 `markDelivered(taskID, ticketID)`（`manager.go:2069`）。

- [ ] **Step 7: 跑测试确认失败**

Run: `go test ./internal/store/ -run TestFindReusableGrant -v`
Expected: 编译失败，`FindReusableGrant` 未定义

- [ ] **Step 8: 实现 FindReusableGrant**

```go
// FindReusableGrant 查同任务、同指纹、已被审核者批准且已送达的 gate 工单。
//
// 参数：
//   - taskID: 任务 id（复用严格限制在任务内，见 spec §3.4）
//   - fingerprint: 权限描述全文的 sha256；空串直接返回无匹配
//
// 返回：
//   - 命中时返回该工单；无匹配返回 (nil, nil)；查询出错返回错误
//
// 注意：
//   - answer 必须**严格等于** "allow"——gate 的翻译规则就是严格相等，
//     这里放宽（如 LIKE 'allow%'）会让 "allowed once, then never" 之类的
//     人工笔误变成一张长期通行证
//   - delivered_at 必须非空：应答落库但中继失败的工单不构成有效先例，
//     executor 侧那次请求根本没收到批准
func (s *Store) FindReusableGrant(taskID, fingerprint string) (*proto.Ticket, error) {
	if fingerprint == "" {
		return nil, nil
	}
	var id string
	err := s.db.QueryRowContext(context.Background(), `
SELECT id FROM tickets
WHERE task_id = ? AND fingerprint = ? AND kind = 'gate'
  AND answer = 'allow' AND delivered_at IS NOT NULL
ORDER BY created_at DESC LIMIT 1`, taskID, fingerprint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询任务 %s 可复用裁决: %w", taskID, err)
	}
	return s.GetTicket(id)
}
```

- [ ] **Step 9: 跑测试确认通过**

Run: `go test ./internal/store/ -v`
Expected: 全包 PASS（含既有用例，验证加列没破坏 GetTicket/PendingTickets 的扫描）

- [ ] **Step 10: 加关键节点日志**

`store` 包不持有 logger（既有约定：store 是纯数据层，日志由调用方打）。**本 task 不加日志**，可观测性由 Task 2 在 manager 侧承担（复用命中/未命中都会打）。这条是刻意的，不是遗漏——在 store 里打日志会让每次查询都产生噪音，而调用方才知道这次查询意味着什么。

- [ ] **Step 11: 加注释**

- `proto.Ticket.Fingerprint` 字段注释（Step 3 已含）
- 迁移段的「为什么容忍 duplicate column + 空指纹永不复用」注释（Step 4 已含）
- `FindReusableGrant` 的 doc 注释与两条 why（Step 8 已含）

确认这三处都在，且注释讲的是**为什么**不是**做了什么**。

- [ ] **Step 12: 提交**

```bash
git add internal/proto/proto.go internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): 工单加裁决指纹列与复用查询（B57②）"
```

---

## Task 2: manager 侧裁决复用

**Files:**
- Modify: `internal/proto/proto.go`（事件类型常量）
- Modify: `internal/agentd/manager.go`（`escalatePermission` :1335、`approvePermission` :1622、`consultApprover` :1580 的调用点）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: `proto.Ticket.Fingerprint`、`Store.FindReusableGrant(taskID, fingerprint string) (*proto.Ticket, error)`（Task 1）
- Produces:
  - `proto.EventTypePermissionReuse EventType = "permission_reuse"`
  - `func permFingerprint(text string) string` —— 权限描述全文的 sha256 十六进制串
  - `func (m *Manager) reuseDecision(taskID string, ev executor.AdapterEvent, ticketID string) bool`
  - `approvePermission` 签名变为 `(taskID, ticketID, permID, permission, reason, source string)`，`source` 取 `"approver"` 或 `"reuse"`

- [ ] **Step 1: 写失败测试——同指纹第二次自动放行**

追加到 `internal/agentd/manager_test.go`：

```go
// TestPermissionReuseSkipsSecondTicket 验证 B57②：同一任务内同一权限描述
// 第二次到达时不再建工单、不再叫醒审核者，而是复用首次的人工批准自动放行。
func TestPermissionReuseSkipsSecondTicket(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	permText := "external_directory: /Users/x/go/pkg/mod/github.com/coder/websocket@v1.8.15/*"

	// 第一次：升级人工 → 审核者批准 → 送达
	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: permText,
	}, "T1:p1")
	if err := st.AnswerTicket("T1:p1", "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	m.markDelivered("T1", "T1:p1")

	// 第二次：同一文案、不同 perm id
	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p2", Text: permText,
	}, "T1:p2")

	// 断言 1：没有新的挂起工单
	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("复用后仍有 %d 张挂起工单，期望 0：%+v", len(pending), pending)
	}

	// 断言 2：落了 permission_reuse 审计事件
	evs, err := st.EventsFromAsc("T1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	var reuse int
	for _, e := range evs {
		if e.Type == proto.EventTypePermissionReuse {
			reuse++
		}
	}
	if reuse != 1 {
		t.Fatalf("permission_reuse 事件 %d 条，期望 1", reuse)
	}

	// 断言 3：批准真的回传给了 executor
	if perms := ad.permsRec(); len(perms) == 0 || perms[len(perms)-1] != "p2:once" {
		t.Fatalf("RespondPermission 实参 = %v，期望末条 p2:once", perms)
	}
}

// TestPermissionReuseIgnoresDeny 验证只复用 allow：首次被拒后，同文案的第二次
// 仍然升级人工。自动重复拒绝会静默掐死回合，方向与 deny 原因下发正好相反。
func TestPermissionReuseIgnoresDeny(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	permText := "bash: rm -rf /tmp/x"

	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: permText,
	}, "T1:p1")
	if err := st.AnswerTicket("T1:p1", "deny: 太危险"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	m.markDelivered("T1", "T1:p1")

	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p2", Text: permText,
	}, "T1:p2")

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "T1:p2" {
		t.Fatalf("deny 之后第二次应照常出单，得到 %+v", pending)
	}
}
```

> 测试用到的既有设施都已在仓库中：`chanAdapter.permsRec()` 返回 `permID:decision` 形式的调用记录（`manager_test.go:119`），`chanAdapter.sendsRec()` 返回 `Send` 实参（`:126`），事件按升序取用 `store.EventsFromAsc(taskID, fromSeq, limit)`（`store.go:475`）——**不要**新造 `lastPermDecision` / `ListEvents` 这类不存在的辅助。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestPermissionReuse' -v`
Expected: 编译失败（`proto.EventTypePermissionReuse` 未定义），或断言失败「复用后仍有 1 张挂起工单」

- [ ] **Step 3: 加事件类型常量**

`internal/proto/proto.go`：

```go
	// EventTypePermissionReuse 表示一次权限请求命中了本任务内**同一权限描述**的
	// 既有人工批准，被自动放行而没有再次叫醒审核者（B57②）。
	// 复用必须留痕，否则「我明明没批过这个」将无从对质。
	EventTypePermissionReuse EventType = "permission_reuse"
```

- [ ] **Step 4: 实现指纹与复用判定**

`internal/agentd/manager.go`，紧邻 `gateDecision` 附近：

```go
// permFingerprint 计算权限描述全文的裁决指纹（sha256 十六进制串）。
//
// 为什么取哈希而不是原文：权限描述可长达 64KB，原文不适合做索引键；
// 而复用要求的是「一字不差的同一件事」，哈希恰好表达这个语义。
func permFingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
```

`escalatePermission` 与 `approvePermission` 建工单时都填上 `Fingerprint: permFingerprint(ev.Text)`（`approvePermission` 用其 `permission` 入参）。

新增复用判定：

```go
// reuseDecision 检查本次权限请求是否命中本任务内既有的人工批准；命中则自动
// 放行并返回 true，调用方不得再走升级人工那套。
//
// 参数：
//   - taskID/ev/ticketID: 与 escalatePermission 同源
//
// 返回：命中并已自动放行为 true；未命中（含查询失败）为 false
//
// 注意：
//   - 查询失败按未命中处理（fail-closed 到「照常问人」）——多问一次是噪音，
//     错误地复用是安全事故，两个方向的代价不对称
//   - 只复用 allow、只在同任务内复用：见 spec §3.3/§3.4
func (m *Manager) reuseDecision(taskID string, ev executor.AdapterEvent, ticketID string) bool {
	fp := permFingerprint(ev.Text)
	prior, err := m.st.FindReusableGrant(taskID, fp)
	if err != nil {
		m.log.Warn("查询可复用裁决失败，照常升级人工", "task", taskID,
			"ticket", ticketID, "fingerprint", fp[:8], "cause", err)
		return false
	}
	if prior == nil {
		m.log.Debug("无可复用裁决，升级人工", "task", taskID,
			"ticket", ticketID, "fingerprint", fp[:8])
		return false
	}
	m.log.Info("命中既有人工批准，自动放行不再叫醒审核者", "task", taskID,
		"ticket", ticketID, "prior_ticket", prior.ID, "fingerprint", fp[:8],
		"perm_chars", len([]rune(ev.Text)))
	// 只入库不 Publish：照 approver_decision 的先例——自动放行没有人需要被唤醒，
	// 但审核者经 show 必须能看到「这条是复用工单 X 的裁决放行的」
	if _, err := m.st.AppendEvent(taskID, proto.EventTypePermissionReuse, permissionReusePayload{
		TicketID: ticketID, PriorTicketID: prior.ID,
		Fingerprint: fp[:8], Permission: permEventText(ev.Text),
	}); err != nil {
		m.log.Error("追加 permission_reuse 事件失败", "task", taskID,
			"ticket", ticketID, "cause", err)
		// 审计事件失败不阻断放行：executor 正阻塞等应答，为一条审计把它挂死
		// 是更坏的结果；Error 日志已留痕
	}
	m.approvePermission(taskID, ticketID, ev.PermissionID, ev.Text,
		"复用工单 "+prior.ID+" 的人工批准", "reuse")
	return true
}
```

payload 类型（放在 `permissionPayload` 等一组结构体旁边）：

```go
// permissionReusePayload 是 permission_reuse 事件的 payload。
type permissionReusePayload struct {
	TicketID      string `json:"ticket_id"`
	PriorTicketID string `json:"prior_ticket_id"`
	Fingerprint   string `json:"fingerprint"`
	Permission    string `json:"permission"`
}
```

- [ ] **Step 5: 接入 escalatePermission 并给 approvePermission 加 source**

`escalatePermission` 的**第一行**（早于 `transitBestEffort`）：

```go
	// 复用判定必须早于任何状态迁移（spec §3.2）：先落 waiting_answer 再放行回迁
	// running，会让任务状态凭空抖动一次，resumeIfIdle 的判定面也跟着变复杂。
	if m.reuseDecision(taskID, ev, ticketID) {
		return
	}
```

`approvePermission` 签名末尾加 `source string`，首行日志改为：

```go
	m.log.Info("权限自动批准", "task", taskID, "ticket", ticketID,
		"perm", permID, "source", source, "reason", truncateRunes(reason, 80))
```

同函数内后续三条日志（创建工单失败 / 应答失败 / 已送达）同样带上 `"source", source`。`consultApprover` 里那个唯一调用点传 `"approver"`。

**为什么加 source 而不是复用原日志**：原文案写死「审批者自动批准」，复用路径打出来是假的——排障时会把人引向审批链去查一个根本没发生的裁决。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestPermissionReuse' -v`
Expected: 两个用例都 PASS

- [ ] **Step 7: 跑全包回归**

Run: `go test ./internal/agentd/`
Expected: PASS。若既有的审批者用例因 `approvePermission` 签名变化而编译失败，补上 `"approver"` 实参即可，不要改用例断言。

- [ ] **Step 8: 加关键节点日志**

自查这四点都在（Step 4/5 的代码已含，此步是核对）：

- 进入判定：未命中打 Debug（带 fingerprint 前 8 位），避免每次权限请求都刷 Info
- 命中：Info，带 `prior_ticket` —— 「凭哪一次批准放行的」必须可追溯
- 错误分支：查询失败 Warn + cause，审计事件失败 Error + cause
- 成功路径：`approvePermission` 末尾的「已送达」Info 带 `source=reuse`，复用不是静默的

- [ ] **Step 9: 加注释**

- `permFingerprint` 的 doc + 「为什么哈希不是原文」
- `reuseDecision` 的 doc + 「查询失败为什么按未命中」+ 「审计失败为什么不阻断放行」
- `escalatePermission` 插入点的「为什么必须早于状态迁移」
- `approvePermission` 的 `source` 参数在 doc 注释里说明取值与含义

- [ ] **Step 10: 提交**

```bash
git add internal/proto/proto.go internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): 任务级裁决复用，同一权限描述不再反复叫醒审核者（B57②）"
```

---

## Task 3: question 事件的稳定 id（契约与 adapter 侧）

**Files:**
- Modify: `internal/executor/executor.go`（`AdapterEvent` :145-154）
- Modify: `internal/executor/opencode/adapter.go`（`mapQuestionAsked` :1357、`replyPendingQuestion` :517 与 :528）
- Test: `internal/executor/opencode/question_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `executor.AdapterEvent.QuestionID string` —— executor 侧提问请求的原生 id；空表示该 executor 没有原生 id

- [ ] **Step 1: 写失败测试——三处 emit 都带 QuestionID**

追加到 `internal/executor/opencode/question_test.go`（package `opencode`，同文件已有一组 `mapQuestionAsked` 内测，装配方式照抄 `TestMapQuestionAskedEmitsTicketAndKeepsTurn`）：

```go
// TestMapQuestionAskedCarriesRequestID 验证 question.asked 转出的事件带上 opencode
// 的原生请求 id——manager 靠它做工单幂等，缺了就会在 agentd 重启后出第二张单。
func TestMapQuestionAskedCarriesRequestID(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"

	props := []byte(`{"id":"que_ff048094","sessionID":"ses_a","questions":[
		{"question":"选哪个超时","header":"超时","options":[{"label":"5000ms"}]}]}`)
	a.mapQuestionAsked(r, props)

	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" {
		t.Fatalf("事件 = %+v ok=%v，期望一条 question", ev, ok)
	}
	if ev.QuestionID != "que_ff048094" {
		t.Fatalf("QuestionID = %q，期望 que_ff048094", ev.QuestionID)
	}
}
```

> 用的都是该文件既有的辅助：`newTestAdapter(t)`、`a.newRun(taskID, dir, dir)`、`drainOne(r)`。不要新造装配。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestQuestionAskedCarriesRequestID -v`
Expected: 编译失败，`AdapterEvent` 没有 `QuestionID` 字段

- [ ] **Step 3: 给 AdapterEvent 加字段**

`internal/executor/executor.go` 的 `AdapterEvent`，紧跟 `PermissionID` 之后：

```go
	// QuestionID 是 Type=question 时 executor 侧提问请求的**原生**稳定 id
	// （如 opencode 的 que_xxx）。manager 按其派生 ticket id 使其幂等——
	// 没有它，agentd 重启后 executor 重放同一个提问会产出第二张工单，
	// 而旧工单永远答不掉（B58）。
	//
	// 留空表示该 executor 没有原生 id（claudecode/codex/grok 的 trailer ask
	// 就是这种），manager 退回 uuid，行为与今天一致。
	QuestionID string
```

- [ ] **Step 4: 三处 emit 都填上**

`mapQuestionAsked` 的 emit：

```go
	a.emit(r, executor.AdapterEvent{
		Type: "question", QuestionID: qa.ID, Text: turn.ClampQuestion(text),
	})
```

`replyPendingQuestion` 里的两处重发 emit 同样填 `QuestionID: reqID`。

**为什么重发也要填**：重发用的是同一个 reqID，填上之后由 manager 统一判定「这是重放还是重发」（Task 4 的三岔）。adapter 侧自作主张留空，等于把这个判断分散到两个地方——manager 那份判据还在，只是永远不会被触发，下一个人读代码时无从判断哪份才作数。

同时更新 `mapQuestionAsked` 的函数头注释：原文写着「question 事件不带幂等 id 给 manager，SSE 重放的去重只能在本层做」，这句已经过期。改为说明 `seenQuestionIDs` 现在只负责**进程内**的 SSE 重放去重，跨进程（agentd 重启）的幂等由 manager 按 `QuestionID` 承担。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -v`
Expected: 全包 PASS

- [ ] **Step 6: 确认另三家不受影响**

Run: `go test ./internal/executor/...`
Expected: PASS。claudecode/codex/grok 的 question emit **一行都不改**——它们的 trailer ask 没有原生 id，留空退回 uuid 正是设计。

- [ ] **Step 7: 加关键节点日志**

`mapQuestionAsked` 既有的「收到 executor 提问，转工单交审核者」Info 已带 `request` 字段（即 `qa.ID`），本 task 无需新增日志。两处重发 emit 的 Warn 也已带 `request`。**核对确认后不加冗余日志**——同一件事打两遍会让 `search_logs` 的计数失真。

- [ ] **Step 8: 加注释**

- `AdapterEvent.QuestionID` 字段注释（Step 3 已含）
- `mapQuestionAsked` 函数头注释的过期段落订正（Step 4）
- 两处重发 emit 旁一句「为什么重发也填同一个 reqID」

- [ ] **Step 9: 提交**

```bash
git add internal/executor/executor.go internal/executor/opencode/adapter.go internal/executor/opencode/adapter_test.go
git commit -m "feat(executor): question 事件带上原生请求 id（B58 前置）"
```

---

## Task 4: handleQuestion 建单三岔

**Files:**
- Modify: `internal/agentd/manager.go`（`handleQuestion` :1771-1800）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: `executor.AdapterEvent.QuestionID`（Task 3）
- Produces: 无新导出符号；`handleQuestion` 的建单行为变更

- [ ] **Step 1: 写失败测试——三岔全覆盖**

```go
// TestQuestionTicketIdempotentOnReplay 验证 B58：带原生 id 的提问重放（agentd
// 重启后 executor 重发同一个 request）不产生第二张工单。
func TestQuestionTicketIdempotentOnReplay(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ev := executor.AdapterEvent{Type: "question", QuestionID: "que_ff", Text: "选哪个？"}

	m.handleQuestion(context.Background(), "T1", ev)
	m.handleQuestion(context.Background(), "T1", ev) // 重放

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("重放后有 %d 张挂起工单，期望 1：%+v", len(pending), pending)
	}
	if pending[0].ID != "T1:que_ff" {
		t.Fatalf("工单 id = %q，期望 T1:que_ff", pending[0].ID)
	}

	// 事件也只该有一条 question——重放不该再唤醒审核者一次
	evs, err := st.EventsFromAsc("T1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	var qn int
	for _, e := range evs {
		if e.Type == proto.EventTypeQuestion {
			qn++
		}
	}
	if qn != 1 {
		t.Fatalf("question 事件 %d 条，期望 1", qn)
	}
}

// TestQuestionReissueAfterAnswerCreatesNewTicket 钉住三岔的第三条：opencode 的
// 「答复没对上选项 → 重发工单」用的是同一个 reqID。若无脑幂等，审核者答错一次
// 之后就再也答不了，任务停在 waiting_answer 直到 stall 超时——比 B58 本身严重。
func TestQuestionReissueAfterAnswerCreatesNewTicket(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ev := executor.AdapterEvent{Type: "question", QuestionID: "que_ff", Text: "选哪个？"}

	m.handleQuestion(context.Background(), "T1", ev)
	if err := st.AnswerTicket("T1:que_ff", "5000ms"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}

	// 折算失败，adapter 用同一个 reqID 重发
	m.handleQuestion(context.Background(), "T1", executor.AdapterEvent{
		Type: "question", QuestionID: "que_ff", Text: "上一次答复没能对上选项。选哪个？",
	})

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("重发后挂起工单 %d 张，期望 1（新单）：%+v", len(pending), pending)
	}
	if pending[0].ID == "T1:que_ff" {
		t.Fatal("重发复用了已答工单的 id，审核者将无法作答")
	}
}

// TestQuestionWithoutIDFallsBackToUUID 验证无原生 id 的 executor（claudecode /
// codex / grok 的 trailer ask）行为不变：每次提问都是一张新单。
func TestQuestionWithoutIDFallsBackToUUID(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ev := executor.AdapterEvent{Type: "question", Text: "选哪个？"}

	m.handleQuestion(context.Background(), "T1", ev)
	m.handleQuestion(context.Background(), "T1", ev)

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("无 id 的两次提问应出两张单，得到 %d 张", len(pending))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestQuestion' -v`
Expected: `TestQuestionTicketIdempotentOnReplay` 失败（挂起工单 2 张），另两个可能已通过

- [ ] **Step 3: 实现三岔**

改写 `handleQuestion` 的建单段：

```go
	// 工单 id 优先用 executor 的原生提问 id 派生（taskID:questionID），与 gate
	// 工单同构、天然幂等：agentd 重启后 executor 重放同一个 request 时，
	// CreateTicket 直接返回 created=false，不会产出第二张永远答不掉的单（B58）。
	// executor 没有原生 id 时退回 uuid——问题没有天然稳定 id，回答一次即终结。
	ticketID := uuid.NewString()
	if ev.QuestionID != "" {
		ticketID = taskID + ":" + ev.QuestionID
	}
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "question")
	req, _ := json.Marshal(ticketRequest{Kind: "ask", Question: ev.Text})
	created, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "ask",
		Request: req, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		m.log.Error("创建提问工单失败", "task", taskID, "ticket", ticketID, "cause", err)
		m.transitBestEffort(taskID, proto.TaskStateRunning, "提问工单创建失败回滚")
		return
	}
	if !created {
		prior, gerr := m.st.GetTicket(ticketID)
		switch {
		case gerr != nil:
			m.log.Error("提问工单已存在但读取失败，按重放跳过", "task", taskID,
				"ticket", ticketID, "cause", gerr)
			return
		case prior.Answer == nil:
			// 重放：agentd 重启后 executor 重发了同一个仍未作答的 request。
			// 不建单、不发第二条事件（审核者已经被叫醒过一次），但必须重挂
			// waiter——新 agentd 实例里没有任何 goroutine 在等这张单
			m.log.Info("提问重放，复用既有工单并重挂等待", "task", taskID, "ticket", ticketID)
			go m.waitQuestion(ctx, taskID, ticketID)
			return
		default:
			// 重发：旧单已答，但 executor 又问了一次（opencode 的「答复没对上
			// 选项」路径用的是同一个 reqID）。此时**必须**新开一张单——复用已答
			// 工单的 id 会让审核者再也答不了，任务停在 waiting_answer 到 stall
			m.log.Info("提问重发（旧单已答），另开新工单", "task", taskID,
				"prior_ticket", ticketID)
			ticketID = uuid.NewString()
			if _, err := m.st.CreateTicket(&proto.Ticket{
				ID: ticketID, TaskID: taskID, Kind: "ask",
				Request: req, CreatedAt: time.Now().UTC(),
			}); err != nil {
				m.log.Error("创建重发提问工单失败", "task", taskID, "ticket", ticketID, "cause", err)
				m.transitBestEffort(taskID, proto.TaskStateRunning, "提问工单创建失败回滚")
				return
			}
		}
	}
```

其后的 `AppendEvent` / `go m.waitQuestion` / `m.hub.Publish` 保持原样。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestQuestion' -v`
Expected: 三个用例全 PASS

- [ ] **Step 5: 跑全包回归**

Run: `go test ./internal/agentd/`
Expected: PASS

- [ ] **Step 6: 加关键节点日志**

核对这五点（Step 3 代码已含）：

- 重放分支 Info：「提问重放，复用既有工单并重挂等待」——这条是 B58 修复生效的现场证据，真机验收要 grep 它
- 重发分支 Info：带 `prior_ticket`，说明为什么另开新单
- 读旧单失败 Error + cause
- 两处建单失败 Error + cause（含回滚状态）
- 成功路径沿用既有的 question 事件与 Publish，不额外加

- [ ] **Step 7: 加注释**

- 建单段顶部「为什么优先用原生 id 派生」
- 重放分支「为什么不发第二条事件但要重挂 waiter」
- 重发分支「为什么必须新开一张单」——这条最重要，它防的是比 B58 更严重的挂死

- [ ] **Step 8: 提交**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "fix(agentd): 提问工单按原生 id 幂等，重启重放不再产生幽灵单（B58）"
```

---

## Task 5: deny 原因下发

**Files:**
- Modify: `internal/agentd/manager.go`（`gateDecision` :1715、`waitPermission` :1734、`RelayAnswer` :1153、`handleQuestion`、`Manager` 结构体的任务级状态 map 附近 :121）
- Modify: `internal/proto/proto.go`（事件类型常量）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: 无（`handleQuestion` 的改动叠在 Task 4 之上）
- Produces:
  - `func gateDecision(answer string) (decision, reason string)` —— **签名变更**，原为单返回值
  - `proto.EventTypeDenyGuidanceRelayed EventType = "deny_guidance_relayed"`
  - `proto.EventTypeDenyGuidanceDropped EventType = "deny_guidance_dropped"`
  - `func (m *Manager) noteDenyGuidance(taskID, reason string)` / `func (m *Manager) takeDenyGuidance(taskID string) string`

- [ ] **Step 1: 写失败测试——gateDecision 双返回值**

```go
// TestGateDecisionParsesReason 表驱动钉住 gate 应答的翻译：只有严格 "allow"
// 放行，其余一律 reject；reason 从 deny/deny: 前缀后取余文。
func TestGateDecisionParsesReason(t *testing.T) {
	cases := []struct {
		name, answer, wantDecision, wantReason string
	}{
		{"批准", "allow", "once", ""},
		{"批准带空白", "  allow  ", "once", ""},
		{"裸拒绝", "deny", "reject", ""},
		{"带原因", "deny: 改用 go build ./...", "reject", "改用 go build ./..."},
		{"带原因无空格", "deny:改用 go build", "reject", "改用 go build"},
		{"任意文本", "看着办", "reject", ""},
		{"空串", "", "reject", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, r := gateDecision(c.answer)
			if d != c.wantDecision || r != c.wantReason {
				t.Fatalf("gateDecision(%q) = (%q,%q)，期望 (%q,%q)",
					c.answer, d, r, c.wantDecision, c.wantReason)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestGateDecisionParsesReason -v`
Expected: 编译失败，`gateDecision` 返回单值

- [ ] **Step 3: 实现 gateDecision 双返回值**

```go
// gateDecision 把审核者对 gate 工单的应答翻译成回传 executor 的裁决与可选原因。
//
// 参数：answer 为审核者应答原文（CLI 侧 --approve → "allow"、
// --deny [--reason r] → "deny" 或 "deny: r"，见 cmd/reply.go）
//
// 返回：
//   - decision: "once"（严格等于 "allow" 时）或 "reject"（其余一律）
//   - reason: 拒绝原因；无原因或批准时为空串
//
// 注意：
//   - 「非 allow 一律 reject」是安全语义，本函数新增 reason 返回值**不改变**它——
//     原因是给模型看的旁路信息，不参与裁决
func gateDecision(answer string) (decision, reason string) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "allow" {
		return "once", ""
	}
	rest := strings.TrimPrefix(trimmed, "deny")
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
	if rest == trimmed {
		// 前缀根本不是 deny（如审核者手工 POST 了任意文本）：照旧 reject，
		// 但那段文本不是「拒绝原因」，不下发给模型
		return "reject", ""
	}
	return "reject", rest
}
```

两个调用点（`waitPermission`、`RelayAnswer`）改为接双返回值。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestGateDecisionParsesReason -v`
Expected: 七个子用例全 PASS

- [ ] **Step 5: 写失败测试——guidance 挂起与消费**

```go
// TestDenyGuidanceRelayedOnNextQuestion 验证 B50：带原因的拒绝，其原因在下一条
// question 到达时被 Send 给 executor，且该分支不建工单、不落 waiting_answer。
func TestDenyGuidanceRelayedOnNextQuestion(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	m.noteDenyGuidance("T1", "改用 go build ./...")
	m.handleQuestion(context.Background(), "T1", executor.AdapterEvent{
		Type: "question", Text: "上一步操作因权限被拒而终止了本回合",
	})

	// 不建工单
	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("guidance 分支不应建工单，得到 %+v", pending)
	}
	// 不落 waiting_answer（否则就是「等你回答却零挂起工单」的死形态）
	task, err := st.GetTask("T1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateRunning {
		t.Fatalf("状态 = %q，期望保持 running", task.State)
	}
	// 原因真的发给了 executor
	sends := ad.sendsRec()
	if len(sends) != 1 || !strings.Contains(sends[0], "改用 go build ./...") {
		t.Fatalf("Send 记录 = %v，未包含拒绝原因", sends)
	}
	// 落了审计事件
	evs, err := st.EventsFromAsc("T1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	var relayed int
	for _, e := range evs {
		if e.Type == proto.EventTypeDenyGuidanceRelayed {
			relayed++
		}
	}
	if relayed != 1 {
		t.Fatalf("deny_guidance_relayed 事件 %d 条，期望 1", relayed)
	}
}

// TestDenyGuidanceConsumedOnce 验证取走式：guidance 只抑制一条 question，
// 第二条正常出单。常驻会让后续真提问被永久吞掉，任务停在 running 无人知晓。
func TestDenyGuidanceConsumedOnce(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	m.noteDenyGuidance("T1", "改用别的办法")
	m.handleQuestion(context.Background(), "T1", executor.AdapterEvent{Type: "question", Text: "问题一"})
	m.handleQuestion(context.Background(), "T1", executor.AdapterEvent{Type: "question", Text: "问题二"})

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("第二条 question 应正常出单，挂起工单 %d 张", len(pending))
	}
}
```

> `chanAdapter.sendsRec()` 已存在（`manager_test.go:126`），直接用，不要新造钩子。`truncateRunes(s, n)` 在 package agentd 里已有（`server.go:1308`）。

- [ ] **Step 6: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDenyGuidance -v`
Expected: 编译失败，`noteDenyGuidance` 未定义

- [ ] **Step 7: 加事件类型常量**

```go
	// EventTypeDenyGuidanceRelayed 表示审核者拒绝时给出的原因已作为一条消息
	// 下发给 executor（B50）。
	EventTypeDenyGuidanceRelayed EventType = "deny_guidance_relayed"
	// EventTypeDenyGuidanceDropped 表示拒绝原因没能下发——回合在下一条提问到达前
	// 就终结了。审核者据此知道要用 continue 自己把话带上。
	EventTypeDenyGuidanceDropped EventType = "deny_guidance_dropped"
```

- [ ] **Step 8: 实现挂起—消费**

`Manager` 结构体加字段（与 `apFails`/`apDisabled` 同一组任务级状态，复用同一把 `apMu`）：

```go
	//   - denyGuidance：审核者拒绝时给出的原因，等下一条 question 到达时下发
	//     （取走式，见 takeDenyGuidance 的 why）
	denyGuidance map[string]string
```

`NewManager` 里初始化它（与相邻 map 一致）。

```go
// noteDenyGuidance 登记一条待下发的拒绝原因。
//
// 参数：taskID 为任务 id；reason 为审核者给出的原因（空串直接忽略）
//
// 为什么不立刻 Send：executor 收到 reject 会当场终结回合（opencode 实测），
// 此刻发消息会撞上正在终结的回合，而回合终结时 adapter 还会补一条兜底提问——
// 审核者刚说完怎么改，又被问一遍「请给出下一步指令」。挂起到下一条 question
// 到达时再下发，正好用那次机会开新回合。
func (m *Manager) noteDenyGuidance(taskID, reason string) {
	if strings.TrimSpace(reason) == "" {
		return
	}
	m.apMu.Lock()
	m.denyGuidance[taskID] = reason
	m.apMu.Unlock()
	m.log.Info("登记待下发的拒绝原因", "task", taskID,
		"reason", truncateRunes(reason, 80))
}

// takeDenyGuidance 取走任务挂起的拒绝原因（读后即清）。
//
// 返回：挂起的原因；没有则为空串
//
// 为什么必须取走式：原因的生命周期是「从这次拒绝到下一条提问」。常驻会让后续
// 的真提问被永久吞掉，任务停在 running 无人知晓——与 askedViaTool 同一个坑。
func (m *Manager) takeDenyGuidance(taskID string) string {
	m.apMu.Lock()
	defer m.apMu.Unlock()
	r := m.denyGuidance[taskID]
	delete(m.denyGuidance, taskID)
	return r
}
```

`waitPermission` 与 `RelayAnswer` 的 gate 分支：拿到 `(decision, reason)` 后，**在 `RespondPermission` 成功之后**调 `m.noteDenyGuidance(taskID, reason)`。放在成功之后是因为没送达的拒绝不会终止回合，提前登记会让下一条无关提问被误吞。

`handleQuestion` 的**最开头**（早于任何状态迁移与建单）：

```go
	// 拒绝原因优先下发（B50）：审核者刚说完该怎么改，此刻 executor 的任何提问
	// 都应先收到那条指令。收到后若仍要问，它会再发一次 question——那时 guidance
	// 已消费，正常出单。
	//
	// 这里刻意不区分「被拒终止的兜底提问」与「模型真的在问问题」：manager 没有
	// 可靠判据（文本前缀匹配一改文案就失效）。吞错的代价是**模型**的一个回合，
	// 漏抑制的代价是**审核者**的一个回合——后者正是本条要消灭的东西。
	if guidance := m.takeDenyGuidance(taskID); guidance != "" {
		m.relayDenyGuidance(ctx, taskID, guidance)
		return
	}
```

```go
// relayDenyGuidance 把审核者的拒绝原因作为一条普通消息下发给 executor，开新回合。
//
// 注意：
//   - **不得触碰状态机**：本分支不建工单，落 waiting_answer 会造出「等你回答却
//     零挂起工单」的死形态（reply/continue/done 三条路全封死）。任务保持 running
//   - Send 失败只记 Error + 审计事件：executor 此刻没有在等任何应答，
//     发不出去不会让任何东西挂死，审核者可用 continue 自己把话带上
func (m *Manager) relayDenyGuidance(ctx context.Context, taskID, guidance string) {
	text := "你请求的操作已被审核者拒绝。原因：" + guidance +
		"\n请据此调整做法后继续，不要重复发起同一请求。"
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("下发拒绝原因：解析执行者失败", "task", taskID, "cause", err)
		m.appendGuidanceDropped(taskID, guidance, err)
		return
	}
	actx, acancel := unaryCtx(ctx)
	defer acancel()
	if err := ad.Send(actx, taskID, text); err != nil {
		m.log.Error("下发拒绝原因失败", "task", taskID, "cause", err)
		m.appendGuidanceDropped(taskID, guidance, err)
		return
	}
	if _, err := m.st.AppendEvent(taskID, proto.EventTypeDenyGuidanceRelayed,
		denyGuidancePayload{Reason: guidance}); err != nil {
		m.log.Error("追加 deny_guidance_relayed 事件失败", "task", taskID, "cause", err)
	}
	m.log.Info("拒绝原因已下发，executor 将据此开新回合", "task", taskID,
		"reason", truncateRunes(guidance, 80))
}
```

`appendGuidanceDropped` 落 `EventTypeDenyGuidanceDropped` 事件（payload 带 reason 与 cause 文本），并打一条 Warn。

任务终态清理：在既有的 `clearApproverState`（Done 归档与 handleResult 回合结束都会调）里一并 `delete(m.denyGuidance, taskID)`，若删除时原因还在，落一条 `deny_guidance_dropped` 事件说明「回合已终结，原因未下发」。这样「审核者说的话去哪了」在任何路径下都有答案。

- [ ] **Step 9: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestGateDecision|TestDenyGuidance' -v`
Expected: 全 PASS

- [ ] **Step 10: 跑全包回归**

Run: `go test ./internal/agentd/`
Expected: PASS

- [ ] **Step 11: 加关键节点日志**

核对这六点（Step 8 代码已含）：

- 登记原因：Info，带截断后的 reason
- 下发成功：Info「拒绝原因已下发，executor 将据此开新回合」——真机验收 grep 这条
- 解析执行者失败：Error + cause
- Send 失败：Error + cause
- 原因被丢弃（回合终结前没机会下发）：Warn + 事件
- 审计事件追加失败：Error + cause

- [ ] **Step 12: 加注释**

- `gateDecision` 的 doc 注释与「reason 不参与裁决」
- `noteDenyGuidance` 的「为什么不立刻 Send」
- `takeDenyGuidance` 的「为什么必须取走式」
- `handleQuestion` 入口的「为什么不区分提问类别」
- `relayDenyGuidance` 的「不得触碰状态机」——这条是最容易被后来者好心改坏的地方

- [ ] **Step 13: 提交**

```bash
git add internal/proto/proto.go internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): --deny --reason 的原因下发给 executor（B50）"
```

---

## Task 6: 公共门槛与真机验收

**Files:** 无代码改动（发现问题则回到对应 task 修）

- [ ] **Step 1: 编译与静态检查**

```bash
go build ./... && go vet ./... && gofmt -l .
```

Expected: 三条全过，`gofmt -l .` **无输出**

- [ ] **Step 2: 全量测试**

```bash
go test ./...
```

Expected: 全绿

- [ ] **Step 3: 竞态检测**

```bash
go test -race ./internal/agentd/ ./internal/store/ ./internal/executor/opencode/
```

Expected: 全绿。`denyGuidance` 走 `apMu`、`FindReusableGrant` 走 SQLite，两处都可能在这里暴露问题。

- [ ] **Step 4: 真机验收——一次 opencode 派发同时观测三条**

在 devbox 上用**隔离实例**（独立端口 + 独立 DataDir + 独立仓库，照 B31/B49 的做法，不碰 7777 与 `~/.handoff`）派发一个会读 go module cache 的任务。三条观测点：

| 条目 | 操作 | 期望 |
|---|---|---|
| B57② | 批准一次 `external_directory: <mod cache 路径>` 工单 | 后续同路径请求**不再出工单**；`handoff show` 里有 `permission_reuse` 事件，日志有「命中既有人工批准，自动放行不再叫醒审核者」 |
| B58 | 任务停在提问时重启 agentd | 重放**不产生第二张单**；`pending_tickets` 全程只有一个 id；日志有「提问重放，复用既有工单并重挂等待」；答复后 `pending_tickets` 清空 |
| B50 | 对一个权限工单 `reply --deny --reason "改用 X"` | 模型**换了做法**而非原样重发；`show` 里有 `deny_guidance_relayed`；日志有「拒绝原因已下发，executor 将据此开新回合」 |

- [ ] **Step 5: 回填 backlog**

三行（B57 / B58 / B50）按证据填 `验收` 列并转 `✅ done(已验)` 或 `✅ done(未验)`：真机三条都实测通过才写 `已验`，写明具体证据（事件名、日志原文、工单 id），不要写「测试通过」这种无法追溯的话。项目全部条目无原型/流程图，自动免除对照。

---

## Self-Review

**Spec 覆盖**：

| spec 节 | 对应 task |
|---|---|
| §3.1 指纹列与迁移 | Task 1 Step 3-4 |
| §3.2 判定点早于状态迁移 | Task 2 Step 5 |
| §3.3 复用四条件、只复用 allow | Task 1 Step 8 + Task 2 Step 1 |
| §3.4 任务级作用域 | Task 1 Step 8（SQL 的 `task_id = ?`）+ Step 6 跨任务用例 |
| §3.5 permission_reuse 只入库不 Publish | Task 2 Step 3-4 |
| §3.6 并发不加锁 | Task 2 Step 4（无锁实现，Task 6 Step 3 用 -race 兜底） |
| §4.1 QuestionID 契约 | Task 3 Step 3 |
| §4.2 建单三岔 | Task 4 Step 3 |
| §4.3 第三岔必须有独立测试 | Task 4 Step 1 的 `TestQuestionReissueAfterAnswerCreatesNewTicket` |
| §4.4 重放分支重挂 waiter | Task 4 Step 3 的 `prior.Answer == nil` 分支 |
| §5.1 gateDecision 双返回值 | Task 5 Step 3 |
| §5.3 挂起—消费、不触碰状态机 | Task 5 Step 8 |
| §5.5 终态清空与审计 | Task 5 Step 8 末段 |
| §7.1 单测四面 | Task 1/2/4/5 的测试步 |
| §7.2 真机验收 | Task 6 Step 4 |

**类型一致性**：`FindReusableGrant` 在 Task 1 定义、Task 2 调用，签名一致；`gateDecision` 的双返回值在 Task 5 内部定义并同步两个调用点；`approvePermission` 的 `source` 参数在 Task 2 Step 5 一次改完签名与唯一调用点；`AdapterEvent.QuestionID` 在 Task 3 定义、Task 4 消费。

**已核对的既有设施**（计划里用的都是仓库中的真名，不要另造）：`store.MarkTicketDelivered`（`store.go:679`）、`store.EventsFromAsc`（`store.go:475`）、`store.AppendEvent(taskID, typ, payload)`（`store.go:391`）、`manager.markDelivered`（`manager.go:2069`）、`manager.clearApproverState`（`manager.go:1592`）、`unaryCtx`（`manager.go:1130`）、`permEventText`（`manager.go:380`）、`truncateRunes`（`server.go:1308`）、`chanAdapter.permsRec/sendsRec`（`manager_test.go:119/126`）、`newTestManager`/`mustCreateTask`（`manager_test.go:133/163`）、`newTestAdapter`（`opencode/reconcile_internal_test.go:40`）。

**唯一需现场对齐的一处**：Task 3 测试里 opencode run 的装配写法（`newRunForTest` / 事件通道字段名）——照同目录既有 `map*` 内测的实际写法抄。
