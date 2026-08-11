# follow 积压对账 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `wait --follow` 在建立连接前先用 `show` 的权威快照对账，把 cursor 之后的积压折成**一行**摘要，而不是把几十条历史事件逐条推给审核者（stdout 每行 = 一次会话唤醒）。

**Architecture:** 纯客户端改动。`FollowEvents` 在每次建连前调一次 `Client.Attach` 拿快照，算出「错过多少 / 其中多少已失效 / 还欠哪些工单」，吐一行 `backlog_summary` 并把 cursor 直接推到当前水位——**积压事件根本不被拉取**。摘要内嵌完整待办工单原文，因此折叠不丢可操作信息。agentd 一行不改。

**Tech Stack:** Go 1.x（仓库现有版本）、`github.com/coder/websocket` v1.8.15、`net/http/httptest`、标准库 `encoding/json`。

**Spec:** [docs/superpowers/specs/2026-08-11-follow-backlog-summary-design.md](../specs/2026-08-11-follow-backlog-summary-design.md)

## Global Constraints

- **不改 agentd**。`internal/agentd/` 与 `internal/proto/` 一行不动（spec §2.3）。
- **不改起点语义**。不加 `--since=now` / `--from-seq`（spec §7）。
- **不加折叠阈值**。有积压就折叠，N=1 也折叠（spec §2.2、§7）。
- **stdout 严格每行一个 JSON 对象**。Monitor 按行解析；任何给人看的信息一律走 stderr。
- **日志用 `slog`（`c.log()` / `slog.X`），禁止 `fmt.Printf` 作为日志手段。**
- **`seq` 是全局 AUTOINCREMENT、跨任务共享**，单任务 seq 不连续：任何地方都不得用 `toSeq - fromSeq` 当条数，也不得用 seq 连续性判断缺口（spec §3.3）。
- **三个计数各自独立算，不做减法**（spec §3.2）。
- 中文注释；新文件写文件头（职责 + 边界），导出函数写文档注释。

## File Structure

| 文件 | 职责 |
|---|---|
| `internal/client/client.go`（改） | 新增 `isDeliverable` 谓词并接入两处流过滤；新增 `reconcileBacklog`；`FollowEvents` 签名加 `onBacklog` 并在每次建连前对账 |
| `internal/client/backlog.go`（新） | 摘要的线格式 `BacklogSummary` + 从快照算摘要的纯函数 `computeBacklog` / `ticketIDOf`。**无 I/O、无网络** |
| `internal/client/backlog_internal_test.go`（新） | `computeBacklog` 的表驱动测试（`package client`，因为被测函数未导出） |
| `internal/client/follow_test.go`（改） | 夹具扩成「HTTP 快照 + WS」双路由；新增对账相关的行为测试 |
| `cmd/wait.go`（改） | `runFollow` 传入 `onBacklog`：序列化摘要单行输出 + `--notify` 通知 |
| `skills/handoff/SKILL.md`（改） | 补一段「重连后你会先看到一行 backlog_summary」，并修正 cursor 语义段 |

---

### Task 1: 统一「可交付」谓词，让代码追上文档契约

> **可独立否决。** 这一条超出 spec 原始范围：它同时改变一次性 `wait` 在**重放路径**上的行为（交付的事件严格更少）。否掉此 task 时，Task 2 的 `computeBacklog` 改为内联判断 `ev.Type != proto.EventTypeProgress`，其余任务不受影响。

**背景：** handoff skill 写明「`progress` / `approver_decision` / `approver_disabled` 三类不会唤醒 `wait`」，但客户端只挡了 `progress`。后两类在服务端是「只入库不 Publish」，所以**实时流**里确实见不到；**但 WS 重放读的是 store**（`EventsFromAsc`），会把它们推给客户端。结果：一次重连交付的东西比实时流更多，多出来的全是审计噪音。

**Files:**
- Modify: `internal/client/client.go`（新增谓词；替换 `waitOnce` 与 `FollowEvents` 各一处过滤）
- Test: `internal/client/follow_test.go`

**Interfaces:**
- Produces: `func isDeliverable(t proto.EventType) bool`（包内未导出，Task 2 的 `computeBacklog` 会用它）

- [ ] **Step 1: 给 `pushEvents` 夹具加 HOME 隔离**

现有 follow 测试没有重定向 `HOME`，cursor 会写进真实 `~/.handoff/`——后续 task 的断言依赖 cursor，必须先隔离。改 `internal/client/follow_test.go` 的夹具，在函数体第一行加：

```go
func pushEvents(t *testing.T, evs []proto.Event, after func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	// cursor 落在 $HOME/.handoff/cursor-<task>：不重定向就会污染真实主目录，
	// 且上一轮遗留的 cursor 会让本轮的 from_seq 起点不确定
	t.Setenv("HOME", t.TempDir())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
```

- [ ] **Step 2: 写失败的测试**

追加到 `internal/client/follow_test.go`：

```go
// TestFollowFiltersAuditEvents 钉住可交付口径：approver_decision / approver_disabled
// 与 progress 一样不唤醒审核者。
//
// 为什么这条会退化：这两类在服务端只入库不 Publish，实时流本就见不到，
// 于是「客户端不过滤」长期没有症状——直到 WS 重放从 store 读出它们。
func TestFollowFiltersAuditEvents(t *testing.T) {
	evs := []proto.Event{
		{Seq: 1, TaskID: "t-audit", Type: proto.EventTypeApproverDecision},
		{Seq: 2, TaskID: "t-audit", Type: proto.EventTypeProgress},
		{Seq: 3, TaskID: "t-audit", Type: proto.EventTypeApproverDisabled},
		{Seq: 4, TaskID: "t-audit", Type: proto.EventTypeQuestion},
		{Seq: 5, TaskID: "t-audit", Type: proto.EventTypeFailed},
	}
	ts := pushEvents(t, evs, func(c *websocket.Conn) { <-make(chan struct{}) })

	var got []int64
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t-audit", false, 0,
		func(ev *proto.Event) error { got = append(got, ev.Seq); return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	want := []int64{4, 5}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("交付 seq = %v, want %v（审计类事件不该唤醒审核者）", got, want)
	}
}

// TestFollowAllDeliversAuditEvents 验证 all=true 不做任何过滤：排障时要看得到审计事件。
func TestFollowAllDeliversAuditEvents(t *testing.T) {
	evs := []proto.Event{
		{Seq: 1, TaskID: "t-all", Type: proto.EventTypeApproverDecision},
		{Seq: 2, TaskID: "t-all", Type: proto.EventTypeProgress},
		{Seq: 3, TaskID: "t-all", Type: proto.EventTypeFailed},
	}
	ts := pushEvents(t, evs, func(c *websocket.Conn) { <-make(chan struct{}) })

	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t-all", true, 0,
		func(*proto.Event) error { n++; return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	if n != 3 {
		t.Fatalf("all=true 交付 %d 条, want 3（不过滤）", n)
	}
}
```

注意：`FollowEvents` 此时还是 5 参数签名，Task 4 加第 6 个参数时这两条测试要补 `nil`。

- [ ] **Step 3: 跑测试确认失败**

```bash
go test ./internal/client/ -run 'TestFollowFiltersAuditEvents|TestFollowAllDeliversAuditEvents' -v
```

预期：`TestFollowFiltersAuditEvents` FAIL，报 `交付 seq = [1 3 4 5], want [4 5]`（approver 两类被交付了）。`TestFollowAllDeliversAuditEvents` 应当已经 PASS。

- [ ] **Step 4: 实现谓词并接入两处过滤**

在 `internal/client/client.go` 的 `isPermanent` 函数之后加：

```go
// isDeliverable 判定一个事件类型是否该唤醒审核者。
//
// 可交付 = 全部类型 − {progress, approver_decision, approver_disabled}。
//
// 为什么后两类也要挡：它们在服务端是「只入库不 Publish」（见 manager.go 追加
// approver_decision 处的注释），**实时流本就见不到**——所以客户端不过滤长期
// 没有症状。但 WS 重放读的是 store（EventsFromAsc），会把它们一并推来，于是
// 「重连交付的东西比实时流更多」，多出来的全是审计噪音；审批链裁决越密，
// 重连时的唤醒风暴越大。handoff skill 早已写明这三类不唤醒 wait，这里是让
// 代码追上契约。
//
// 注意：all=true 时调用方不使用本谓词，全量交付——排障需要看到审计事件。
func isDeliverable(t proto.EventType) bool {
	switch t {
	case proto.EventTypeProgress,
		proto.EventTypeApproverDecision,
		proto.EventTypeApproverDisabled:
		return false
	}
	return true
}
```

然后把两处过滤改掉。`waitOnce` 里（原 `client.go:863` 附近）：

```go
		if !all && !isDeliverable(ev.Type) {
			return nil
		}
```

`FollowEvents` 的 `onFrame` 里（原 `client.go:923` 附近）同样：

```go
			if !all && !isDeliverable(ev.Type) {
				return nil
			}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/client/ -v
```

预期：全部 PASS（含既有的 7 条 follow 测试）。

- [ ] **Step 6: 加关键节点日志**

被过滤掉的事件不打日志（高频，会淹没输出）。但**过滤口径本身**要在 follow 开始时可见一次——在 `FollowEvents` 已有的「follow 开始」Info 上补一个字段：

```go
	c.log().Info("follow 开始", "addr", c.baseURL, "task", taskID,
		"from_seq", fromSeq, "idle", idle.String(), "all", all)
```

`all` 是「为什么我没收到那条事件」的第一诊断点，必须能从日志读出来。

- [ ] **Step 7: 加注释**

已在 Step 4 的谓词文档注释里写全（含「为什么后两类也要挡」这条 why）。此外在两处调用点各留一行，说明口径来自同一个谓词：

```go
			// 口径与 waitOnce 共用 isDeliverable，避免两处漂移
```

- [ ] **Step 8: 提交**

```bash
git add internal/client/client.go internal/client/follow_test.go
git commit -m "fix(client): 统一可交付口径，审计类事件不再在重放路径唤醒审核者

approver_decision/approver_disabled 在服务端只入库不 Publish，实时流见不到，
但 WS 重放读 store 会推给客户端而客户端不过滤——一次重连交付的东西比实时流更多，
多出来的全是审计噪音。抽 isDeliverable 统一 waitOnce 与 FollowEvents 两处过滤，
让代码追上 skill 早已写明的契约。all=true 不受影响。

顺带给 follow 测试夹具加 HOME 隔离，cursor 不再污染真实主目录。"
```

---

### Task 2: 摘要线格式与计算（纯函数）

**Files:**
- Create: `internal/client/backlog.go`
- Test: `internal/client/backlog_internal_test.go`

**Interfaces:**
- Consumes: `isDeliverable(proto.EventType) bool`（Task 1）；`AttachInfo`（`internal/client/client.go` 既有类型，字段 `Task proto.TaskView` / `PendingTickets []proto.Ticket` / `RecentEvents []proto.Event`）
- Produces:
  - `const BacklogSummaryType = "backlog_summary"`
  - `type BacklogSummary struct{ Type, TaskID string; FromSeq, ToSeq int64; State proto.TaskState; Missed int; MissedTruncated bool; Stale int; Actionable []proto.Ticket }`
  - `func computeBacklog(taskID string, fromSeq int64, snap *AttachInfo) *BacklogSummary`（无积压返回 nil）
  - `func ticketIDOf(ev proto.Event) string`（非工单类或缺字段返回空串）

- [ ] **Step 1: 写失败的测试**

新建 `internal/client/backlog_internal_test.go`：

```go
// backlog_internal_test.go —— computeBacklog 的计算契约测试。
//
// 职责：钉住「三个计数各自独立算、不做减法」「seq 全局不连续」「截断判据」三条。
// 边界：不涉及网络与 cursor 落盘，那些在 follow_test.go 里覆盖。
package client

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// permEv 构造一条带 ticket_id 的 permission_request 事件。
func permEv(seq int64, ticketID string) proto.Event {
	p, _ := json.Marshal(map[string]string{"ticket_id": ticketID})
	return proto.Event{Seq: seq, TaskID: "t1", Type: proto.EventTypePermissionRequest, Payload: p}
}

func TestComputeBacklogNoBacklog(t *testing.T) {
	snap := &AttachInfo{RecentEvents: []proto.Event{permEv(100, "a")}}
	if got := computeBacklog("t1", 100, snap); got != nil {
		t.Fatalf("水位等于 cursor 时应无积压，got %+v", got)
	}
	if got := computeBacklog("t1", 100, &AttachInfo{}); got != nil {
		t.Fatalf("空事件窗口应无积压，got %+v", got)
	}
}

// TestComputeBacklogCountsIndependently 钉住 spec §3.2：三个计数各自独立。
// 夹具刻意让「断网前就欠着的老工单」（seq 90 ≤ cursor 100）出现在 actionable 里，
// 若实现写成 stale = missed - len(actionable) 会算错。
func TestComputeBacklogCountsIndependently(t *testing.T) {
	snap := &AttachInfo{
		Task: proto.TaskView{Task: proto.Task{State: proto.TaskStateWaitingAnswer}},
		PendingTickets: []proto.Ticket{
			{ID: "old", Kind: "gate"},  // seq 90，断网前就欠着
			{ID: "new1", Kind: "gate"}, // seq 109，间隙内且仍待处置
		},
		RecentEvents: []proto.Event{
			permEv(90, "old"),
			permEv(104, "done1"), // 已被审批链答掉
			permEv(109, "new1"),
			permEv(117, "done2"), // 已被审批链答掉
		},
	}
	got := computeBacklog("t1", 100, snap)
	if got == nil {
		t.Fatal("有积压却返回 nil")
	}
	if got.Missed != 3 {
		t.Fatalf("missed = %d, want 3（seq 104/109/117；90 在 cursor 之前）", got.Missed)
	}
	if got.Stale != 2 {
		t.Fatalf("stale = %d, want 2（done1/done2 已不在 pending）", got.Stale)
	}
	if len(got.Actionable) != 2 {
		t.Fatalf("actionable = %d, want 2（pending_tickets 全量，含断网前的 old）", len(got.Actionable))
	}
}

// TestComputeBacklogGlobalSeqNotContiguous 钉住 spec §3.3：seq 是全局
// AUTOINCREMENT，跨任务共享，ToSeq-FromSeq 不是条数。
func TestComputeBacklogGlobalSeqNotContiguous(t *testing.T) {
	snap := &AttachInfo{RecentEvents: []proto.Event{
		{Seq: 90, TaskID: "t1", Type: proto.EventTypeProgress},
		{Seq: 104, TaskID: "t1", Type: proto.EventTypeCompleted},
		{Seq: 109, TaskID: "t1", Type: proto.EventTypeStalled},
		{Seq: 117, TaskID: "t1", Type: proto.EventTypeCompleted},
	}}
	got := computeBacklog("t1", 100, snap)
	if got.Missed != 3 {
		t.Fatalf("missed = %d, want 3（不是 ToSeq-FromSeq=17）", got.Missed)
	}
	if got.ToSeq != 117 || got.FromSeq != 100 {
		t.Fatalf("区间 = [%d,%d], want [100,117]", got.FromSeq, got.ToSeq)
	}
}

// TestComputeBacklogSkipsAuditEvents 验证摘要计数与流过滤同口径。
func TestComputeBacklogSkipsAuditEvents(t *testing.T) {
	snap := &AttachInfo{RecentEvents: []proto.Event{
		{Seq: 101, TaskID: "t1", Type: proto.EventTypeProgress},
		{Seq: 102, TaskID: "t1", Type: proto.EventTypeApproverDecision},
		{Seq: 103, TaskID: "t1", Type: proto.EventTypeApproverDisabled},
		{Seq: 104, TaskID: "t1", Type: proto.EventTypeCompleted},
	}}
	if got := computeBacklog("t1", 100, snap).Missed; got != 1 {
		t.Fatalf("missed = %d, want 1（三类审计/进度事件不计）", got)
	}
}

// TestComputeBacklogTruncation 钉住 spec §3.5 的截断判据：窗口最旧一条仍晚于
// cursor ⇒ 无法证明覆盖了整个间隙 ⇒ 标 truncated。
func TestComputeBacklogTruncation(t *testing.T) {
	covered := &AttachInfo{RecentEvents: []proto.Event{permEv(100, "a"), permEv(104, "b")}}
	if computeBacklog("t1", 100, covered).MissedTruncated {
		t.Fatal("窗口最旧一条 seq=100 <= cursor，不该标截断")
	}
	truncated := &AttachInfo{RecentEvents: []proto.Event{permEv(104, "b")}}
	if !computeBacklog("t1", 100, truncated).MissedTruncated {
		t.Fatal("窗口最旧一条 seq=104 > cursor=100，必须标截断")
	}
}

// TestComputeBacklogShape 钉住线格式的两个固定值。
func TestComputeBacklogShape(t *testing.T) {
	snap := &AttachInfo{
		Task:         proto.TaskView{Task: proto.Task{State: proto.TaskStateFailed}},
		RecentEvents: []proto.Event{permEv(104, "a")},
	}
	got := computeBacklog("task-xyz", 100, snap)
	if got.Type != BacklogSummaryType {
		t.Fatalf("type = %q, want %q", got.Type, BacklogSummaryType)
	}
	if got.TaskID != "task-xyz" {
		t.Fatalf("task_id = %q, want task-xyz", got.TaskID)
	}
	if got.State != proto.TaskStateFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Actionable == nil {
		t.Fatal("actionable 必须是数组而非 nil：JSON 里 null 与 [] 对消费方是两回事")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/client/ -run TestComputeBacklog -v
```

预期：编译失败，`undefined: computeBacklog`、`undefined: BacklogSummaryType`。

- [ ] **Step 3: 实现**

新建 `internal/client/backlog.go`：

```go
// backlog.go —— follow 建连前「积压对账」的线格式与计算。
//
// 职责：
//   - 定义摘要行的线格式 BacklogSummary（stdout 每行一条，与事件行共用一条通道）
//   - 从 agentd 的权威快照（AttachInfo）算出摘要：错过多少条、其中多少已失效、
//     当前还欠哪些工单
//
// 边界：
//   - 无 I/O、无网络：快照怎么拿是 reconcileBacklog 的事，本文件只做纯计算
//   - 不决定摘要吐不吐、cursor 推不推——那是 FollowEvents 的编排
package client

import (
	"encoding/json"

	"github.com/xushixin/handoff/internal/proto"
)

// BacklogSummaryType 是摘要行的 type 取值。
//
// 为什么复用 type 这个 key 而不另起 kind：stdout 的既有契约是「每行一个带 type
// 的 JSON 对象」，上层按行解析。沿用 type 能让既有解析器读到一个不认识的取值
// 就跳过；换个 key 则会让它们撞上一个缺字段的对象。
//
// 注意：这是**客户端合成**的行，agentd 从不存这个事件类型——不要去 proto.EventType
// 里找它。
const BacklogSummaryType = "backlog_summary"

// BacklogSummary 是 follow 建立连接前对账得出的「你错过了什么」。
//
// 线格式（单行 JSON，stdout）：{"type":"backlog_summary","task_id":..,"from_seq":..,
// "to_seq":..,"state":..,"missed":..,"missed_truncated":..,"stale":..,"actionable":[..]}
type BacklogSummary struct {
	Type    string          `json:"type"`
	TaskID  string          `json:"task_id"`
	FromSeq int64           `json:"from_seq"`
	ToSeq   int64           `json:"to_seq"`
	State   proto.TaskState `json:"state"`

	// Missed 是间隙内可交付事件的条数。MissedTruncated 为 true 时语义降级为
	// 「至少 Missed 条」——快照的事件窗口没能覆盖到 cursor，剩下的数不出来。
	Missed          int  `json:"missed"`
	MissedTruncated bool `json:"missed_truncated"`

	// Stale 是间隙内工单已被消费（审批链答掉或被作废）的事件条数——补 reply 会 404。
	Stale int `json:"stale"`

	// Actionable 是当前仍待处置的工单**全量**，每张带完整 Request 原文，审核者
	// 可直接据此 reply --ticket <id>。
	//
	// 注意它**不限于间隙内**：断网前你就看见过、但一直没答的工单也在里面。
	// 那正是最需要知道的一类，也是 Stale 不能用减法算出来的原因。
	Actionable []proto.Ticket `json:"actionable"`
}

// computeBacklog 从权威快照算出积压摘要。
//
// 参数：
//   - taskID: 完整 UUID，原样写进摘要
//   - fromSeq: 本机 cursor 停在哪（0 表示本机从未交付过该任务的事件）
//   - snap: GET /api/tasks/{id} 的快照
//
// 返回：
//   - 摘要；**无积压时返回 nil**（快照事件窗口为空，或水位不超过 fromSeq）
//
// 注意：
//   - 三个计数各自独立算，不做减法（why 见 BacklogSummary.Actionable 的注释）
//   - seq 是全局 AUTOINCREMENT、跨任务共享，单任务 seq 不连续：ToSeq-FromSeq
//     不是条数，只能逐条遍历来数
func computeBacklog(taskID string, fromSeq int64, snap *AttachInfo) *BacklogSummary {
	if snap == nil || len(snap.RecentEvents) == 0 {
		return nil
	}
	// RecentEvents 按 seq 升序（EventsFrom 取最新窗口后翻回升序），末条即当前水位
	toSeq := snap.RecentEvents[len(snap.RecentEvents)-1].Seq
	if toSeq <= fromSeq {
		return nil
	}

	pending := make(map[string]struct{}, len(snap.PendingTickets))
	for _, tk := range snap.PendingTickets {
		pending[tk.ID] = struct{}{}
	}

	missed, stale := 0, 0
	for _, ev := range snap.RecentEvents {
		// 口径与流过滤共用 isDeliverable：数的是「本该唤醒审核者的事件」
		if ev.Seq <= fromSeq || !isDeliverable(ev.Type) {
			continue
		}
		missed++
		id := ticketIDOf(ev)
		if id == "" {
			continue
		}
		if _, still := pending[id]; !still {
			stale++
		}
	}

	// 归一化为空数组而非 nil：JSON 里 null 与 [] 对按行解析的消费方是两回事
	actionable := snap.PendingTickets
	if actionable == nil {
		actionable = []proto.Ticket{}
	}

	return &BacklogSummary{
		Type:    BacklogSummaryType,
		TaskID:  taskID,
		FromSeq: fromSeq,
		ToSeq:   toSeq,
		State:   snap.Task.State,
		Missed:  missed,
		// 判据是「窗口最旧一条仍晚于 cursor」——此时无法证明窗口覆盖了整个间隙。
		// 不用「窗口满 N 条」：客户端不知道 agentd 的 recentEventsLimit，写死会
		// 造成版本耦合，且服务端调小该值时会**漏报**截断（错在危险方向）。
		// 现判据的代价是偶尔虚报，错在安全方向：宁可少声称，不可多声称
		MissedTruncated: snap.RecentEvents[0].Seq > fromSeq,
		Stale:           stale,
		Actionable:      actionable,
	}
}

// ticketIDOf 取事件 payload 里的 ticket_id。
//
// 参数：
//   - ev: 任意事件
//
// 返回：
//   - 工单 ID；非工单类事件、payload 非法或缺该字段时返回空串
//
// 注意：ticket_id 是 permission_request / question 事件 payload 的既有线格式契约
//（服务端定义在 internal/agentd/manager.go 的 permissionPayload / questionPayload）。
// 此处只解这一个字段，不与服务端结构体耦合。
func ticketIDOf(ev proto.Event) string {
	switch ev.Type {
	case proto.EventTypePermissionRequest, proto.EventTypeQuestion:
	default:
		return ""
	}
	var p struct {
		TicketID string `json:"ticket_id"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		// payload 解不开不是致命错：摘要少数一条 stale，好过让整次对账失败
		return ""
	}
	return p.TicketID
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/client/ -run TestComputeBacklog -v
```

预期：6 条全 PASS。

- [ ] **Step 5: 加关键节点日志**

本文件**刻意不打日志**——它是纯函数，没有 I/O、没有外部调用、没有会失败的分支。日志归调用方 `reconcileBacklog`（Task 3），它拿着同一份结果打一行带全部计数的 Info。在文件头「边界」段落里把这个决定写明，避免后来者以为是漏了：

```go
//   - 不打日志：本文件是纯计算，可观测性由调用方 reconcileBacklog 承担
//     （它拿同一份结果打一行带全部计数的 Info）
```

唯一的静默分支是 `ticketIDOf` 的 payload 解析失败——它由调用方的 `stale` 计数体现，且已在函数内注释说明为什么不升级成错误。

- [ ] **Step 6: 加注释**

已随 Step 3 写全：文件头（职责 + 边界，含「不打日志」的理由）、`BacklogSummaryType` 的 why、`BacklogSummary.Actionable` 的「不限于间隙内」、`computeBacklog` 的文档注释 + 截断判据 why、`ticketIDOf` 的文档注释。自查这几处都在。

- [ ] **Step 7: 提交**

```bash
git add internal/client/backlog.go internal/client/backlog_internal_test.go
git commit -m "feat(client): 加 BacklogSummary 线格式与 computeBacklog 纯函数

从 show 的权威快照算出「错过多少 / 其中多少已失效 / 还欠哪些工单」。
三个计数各自独立算不做减法（存在断网前就欠着的老工单，减法会算错）；
seq 是全局 AUTOINCREMENT 不连续，条数只能逐条遍历；截断判据用「窗口最旧
一条仍晚于 cursor」而非「窗口满 N 条」，避免把服务端的 limit 写进客户端。"
```

---

### Task 3: `reconcileBacklog`——拿快照、吐摘要、推 cursor

**Files:**
- Modify: `internal/client/client.go`（在 `FollowEvents` 之前新增方法）
- Test: `internal/client/backlog_internal_test.go`（追加）

**Interfaces:**
- Consumes: `computeBacklog`（Task 2）、既有的 `Client.Attach` / `Client.writeCursor` / `Client.log`
- Produces: `func (c *Client) reconcileBacklog(ctx context.Context, taskID string, fromSeq int64, onBacklog func(*BacklogSummary) error) (next int64, terminal bool, err error)`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/client/backlog_internal_test.go`（同 `package client`，可直接构造 `Client`）：

```go
// snapServer 起一个只服务 GET /api/tasks/{id} 的假 agentd，返回给定快照。
// status 非 200 时返回该状态码（用于降级路径）。
func snapServer(t *testing.T, status int, snap *AttachInfo) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestReconcileBacklogEmitsAndAdvancesCursor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ts := snapServer(t, http.StatusOK, &AttachInfo{
		Task:           proto.TaskView{Task: proto.Task{State: proto.TaskStateWaitingAnswer}},
		PendingTickets: []proto.Ticket{{ID: "new1", Kind: "gate"}},
		RecentEvents:   []proto.Event{permEv(104, "done1"), permEv(109, "new1")},
	})

	var got *BacklogSummary
	next, terminal, err := New(ts.URL, "").reconcileBacklog(t.Context(), "tk", 100,
		func(s *BacklogSummary) error { got = s; return nil })
	if err != nil {
		t.Fatalf("reconcileBacklog = %v, want nil", err)
	}
	if terminal {
		t.Fatal("waiting_answer 不是终态，terminal 应为 false")
	}
	if next != 109 {
		t.Fatalf("next = %d, want 109（推到水位）", next)
	}
	if got == nil || got.Missed != 2 || got.Stale != 1 {
		t.Fatalf("摘要 = %+v, want missed=2 stale=1", got)
	}
	b, err := os.ReadFile(filepath.Join(home, ".handoff", "cursor-tk"))
	if err != nil {
		t.Fatalf("读 cursor: %v", err)
	}
	if strings.TrimSpace(string(b)) != "109" {
		t.Fatalf("cursor = %q, want 109", strings.TrimSpace(string(b)))
	}
}

func TestReconcileBacklogSilentWhenNoBacklog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts := snapServer(t, http.StatusOK, &AttachInfo{
		RecentEvents: []proto.Event{permEv(100, "a")},
	})
	called := false
	next, terminal, err := New(ts.URL, "").reconcileBacklog(t.Context(), "tk", 100,
		func(*BacklogSummary) error { called = true; return nil })
	if err != nil || terminal {
		t.Fatalf("err=%v terminal=%v, want nil/false", err, terminal)
	}
	if called {
		t.Fatal("无积压时不该吐摘要——那会给正常运行加噪音")
	}
	if next != 100 {
		t.Fatalf("next = %d, want 100（原样返回）", next)
	}
}

// TestReconcileBacklogDegradesOnAttachFailure 钉住 spec §4：对账失败退回逐条重放，
// 绝不因此中断 follow。404/401 的永久性由随后的 WS 握手判定，不在这里重复一套。
func TestReconcileBacklogDegradesOnAttachFailure(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Setenv("HOME", t.TempDir())
		ts := snapServer(t, status, nil)
		called := false
		next, terminal, err := New(ts.URL, "").reconcileBacklog(t.Context(), "tk", 100,
			func(*BacklogSummary) error { called = true; return nil })
		if err != nil {
			t.Fatalf("status=%d: reconcileBacklog = %v, want nil（降级不报错）", status, err)
		}
		if called || terminal || next != 100 {
			t.Fatalf("status=%d: 降级路径应原样返回 fromSeq 且不吐摘要，got next=%d called=%v terminal=%v",
				status, next, called, terminal)
		}
	}
}

// TestReconcileBacklogTerminalOnFailed 钉住 spec §4：积压被跳过后，终结判据必须
// 由 state 接上，否则 follow 会挂在一个死任务上。
func TestReconcileBacklogTerminalOnFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts := snapServer(t, http.StatusOK, &AttachInfo{
		Task:         proto.TaskView{Task: proto.Task{State: proto.TaskStateFailed}},
		RecentEvents: []proto.Event{permEv(104, "a")},
	})
	_, terminal, err := New(ts.URL, "").reconcileBacklog(t.Context(), "tk", 100,
		func(*BacklogSummary) error { return nil })
	if err != nil {
		t.Fatalf("reconcileBacklog = %v, want nil", err)
	}
	if !terminal {
		t.Fatal("快照 state=failed 时 terminal 必须为 true")
	}
}

// TestReconcileBacklogPropagatesCallbackError 验证 onBacklog 的错误原样上抛
//（stdout 写失败必须让 follow 停下，而不是继续跑一个没人看得见的循环）。
func TestReconcileBacklogPropagatesCallbackError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts := snapServer(t, http.StatusOK, &AttachInfo{
		RecentEvents: []proto.Event{permEv(104, "a")},
	})
	boom := errors.New("写 stdout 失败")
	if _, _, err := New(ts.URL, "").reconcileBacklog(t.Context(), "tk", 100,
		func(*BacklogSummary) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}
```

测试文件需补 import：`errors` / `net/http` / `net/http/httptest` / `os` / `path/filepath` / `strings`。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/client/ -run TestReconcileBacklog -v
```

预期：编译失败，`c.reconcileBacklog undefined`。

- [ ] **Step 3: 实现**

在 `internal/client/client.go` 的 `FollowEvents` 之前插入：

```go
// reconcileBacklog 在建立 WS 连接前对账：拿一次权威快照，判断本机 cursor 之后
// 是否积压了未读事件；有则吐一行摘要并把 cursor 直接推到当前水位。
//
// 参数：
//   - taskID: 完整 UUID
//   - fromSeq: 本机 cursor 当前停在哪
//   - onBacklog: 摘要的消费者（cmd 层写 stdout）；返回非 nil 立即上抛
//
// 返回：
//   - next: WS 应当使用的 from_seq——有积压时是当前水位，否则原样是 fromSeq
//   - terminal: 快照显示任务已 failed，调用方应吐完摘要后正常收尾（返回 nil）
//   - err: 仅当 onBacklog 报错；**网络/HTTP 失败一律不报错**（见下）
//
// 为什么积压事件不拉取而是直接跳过：摘要用的是权威快照，比逐条重放**信息更全**
// ——重放里混着已被审批链答掉的历史工单（补 reply 会 404），而 PendingTickets
// 只含真正还欠的，且每张带完整 Request 原文。
//
// 为什么 Attach 失败是降级而不是报错：摘要是优化不是正确性，对账失败就该退回
// 改动前的逐条重放，绝不能因此中断 follow。永久性（401/404）也不在这里判——
// Client.Attach 的错误是普通 fmt.Errorf 而非 permanentError，isPermanent 认不出
// 它；紧随其后的 WS 握手会用既有的、已被测试覆盖的路径判出同一个结论。判定
// 只留一处，不复制。
func (c *Client) reconcileBacklog(ctx context.Context, taskID string, fromSeq int64,
	onBacklog func(*BacklogSummary) error) (int64, bool, error) {
	c.log().Debug("follow 积压对账开始", "task", taskID, "from_seq", fromSeq)

	snap, err := c.Attach(ctx, taskID)
	if err != nil {
		c.log().Warn("follow 积压对账失败，退回逐条重放", "task", taskID,
			"from_seq", fromSeq, "cause", err)
		return fromSeq, false, nil
	}

	sum := computeBacklog(taskID, fromSeq, snap)
	if sum == nil {
		c.log().Debug("follow 积压对账完成：无积压", "task", taskID, "from_seq", fromSeq)
		return fromSeq, false, nil
	}

	// 有积压是「你离开期间发生了事」，是 Info 不是 Debug：它是排查「我错过了什么」
	// 的唯一线索行，必须带齐全部计数
	c.log().Info("follow 积压对账：有积压", "task", taskID,
		"from_seq", sum.FromSeq, "to_seq", sum.ToSeq, "missed", sum.Missed,
		"stale", sum.Stale, "actionable", len(sum.Actionable),
		"truncated", sum.MissedTruncated, "state", string(sum.State))

	if berr := onBacklog(sum); berr != nil {
		return fromSeq, false, berr
	}
	if werr := c.writeCursor(taskID, sum.ToSeq); werr != nil {
		// 不因写盘失败中止：下次对账会重新吐同一行摘要，重复一行无害；
		// 吞掉才危险
		c.log().Warn("对账后 cursor 写盘失败", "task", taskID, "seq", sum.ToSeq, "cause", werr)
	}
	return sum.ToSeq, sum.State == proto.TaskStateFailed, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/client/ -run TestReconcileBacklog -v
```

预期：5 条全 PASS。

- [ ] **Step 5: 加关键节点日志**

已随 Step 3 实现落位，逐项对照 `instrumenting-code` 的清单：

| 关键点 | 落位 |
|---|---|
| 进入关键操作（带入参） | `Debug("follow 积压对账开始", task, from_seq)` |
| 外部调用前后 | `Attach` 前的开始行 + 失败时的 Warn（带 cause） |
| 每个错误分支带上下文 | Attach 失败 Warn 带 `cause`；cursor 写盘失败 Warn 带 `seq` + `cause` |
| 状态变更 | cursor 推进由「有积压」的 Info 行覆盖（含 `from_seq`→`to_seq`） |
| 成功路径不静默 | 有积压 → Info 带全部计数；无积压 → Debug 明确说「无积压」 |

**为什么开始/无积压用 Debug 而结论用 Info**：对账挂在每次重连之前，agentd 长时间不可达时重连会反复触发；若开始行也是 Info，stderr 会被无意义的对账行淹没，而真正的信号（有积压）反而被埋掉。降级 Warn 保持 Info 级别是有意的——它与既有的「WS 连接断开，等待后重连」Info 行同频，不会额外放大。

- [ ] **Step 6: 加注释**

已随 Step 3 写全：方法文档注释（参数/返回/两条 why），以及 cursor 写盘失败分支的行内 why。自查这三处都在：「为什么积压事件不拉取」「为什么 Attach 失败是降级」「为什么写盘失败不中止」。

- [ ] **Step 7: 提交**

```bash
git add internal/client/client.go internal/client/backlog_internal_test.go
git commit -m "feat(client): 加 reconcileBacklog——建连前对账、吐摘要、推 cursor

拿一次权威快照判断 cursor 之后是否有积压；有则吐一行摘要并把 cursor 直接推到
水位，积压事件根本不拉取。Attach 失败一律降级回逐条重放，不在这里判永久性——
让紧随其后的 WS 握手用既有路径判，判定只留一处。快照 state=failed 时返回
terminal，接上被跳过的 failed 事件原本承担的终结判据。"
```

---

### Task 4: 接进 `FollowEvents`（首连与重连同一条路径）

**Files:**
- Modify: `internal/client/client.go`（`FollowEvents` 签名与循环）
- Modify: `internal/client/follow_test.go`（既有 7 条测试补 `nil` 参数 + 新增 2 条）

**Interfaces:**
- Consumes: `reconcileBacklog`（Task 3）
- Produces: `func (c *Client) FollowEvents(ctx context.Context, taskID string, all bool, idle time.Duration, onEvent func(*proto.Event) error, onBacklog func(*BacklogSummary) error) error`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/client/follow_test.go`。需要一个同时服务 HTTP 快照与 WS 的夹具：

```go
// snapAndPushServer 起一个既服务 GET /api/tasks/{id} 快照、又服务 /ws/events 的
// 假 agentd。每次 WS 连接会把握手带的 from_seq 记进 gotFromSeq。
//
// snaps 按连接次序取用：第 n 次 HTTP 快照请求取 snaps[min(n, len-1)]，
// 用于「第一次连接后积压、第二次连接前再对账」这类多阶段场景。
func snapAndPushServer(t *testing.T, snaps []*client.AttachInfo,
	evs []proto.Event, after func(*websocket.Conn)) (*httptest.Server, *[]string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var mu sync.Mutex
	var gotFromSeq []string
	nth := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := nth
		nth++
		mu.Unlock()
		if i >= len(snaps) {
			i = len(snaps) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snaps[i])
	})
	mux.HandleFunc("/ws/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotFromSeq = append(gotFromSeq, r.URL.Query().Get("from_seq"))
		mu.Unlock()
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for _, ev := range evs {
			b, merr := json.Marshal(ev)
			if merr != nil {
				return
			}
			if werr := c.Write(r.Context(), websocket.MessageText, b); werr != nil {
				return
			}
		}
		after(c)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts, &gotFromSeq
}

// TestFollowReconcilesBeforeConnect 验证首连前对账：吐一行摘要，且 WS 握手带的
// from_seq 是水位而不是 cursor——积压事件根本不被拉取。
func TestFollowReconcilesBeforeConnect(t *testing.T) {
	snap := &client.AttachInfo{
		Task:           proto.TaskView{Task: proto.Task{State: proto.TaskStateWaitingAnswer}},
		PendingTickets: []proto.Ticket{{ID: "new1", Kind: "gate"}},
		RecentEvents: []proto.Event{
			{Seq: 104, TaskID: "tk", Type: proto.EventTypePermissionRequest},
			{Seq: 109, TaskID: "tk", Type: proto.EventTypePermissionRequest},
		},
	}
	live := []proto.Event{{Seq: 200, TaskID: "tk", Type: proto.EventTypeFailed}}
	ts, fromSeqs := snapAndPushServer(t, []*client.AttachInfo{snap}, live,
		func(c *websocket.Conn) { <-make(chan struct{}) })

	var sums []*client.BacklogSummary
	var evSeqs []int64
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "tk", false, 0,
		func(ev *proto.Event) error { evSeqs = append(evSeqs, ev.Seq); return nil },
		func(s *client.BacklogSummary) error { sums = append(sums, s); return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	if len(sums) != 1 {
		t.Fatalf("摘要 %d 行, want 1", len(sums))
	}
	if sums[0].ToSeq != 109 || sums[0].Missed != 2 {
		t.Fatalf("摘要 = %+v, want to_seq=109 missed=2", sums[0])
	}
	if len(*fromSeqs) != 1 || (*fromSeqs)[0] != "109" {
		t.Fatalf("WS from_seq = %v, want [109]（积压不该被拉取）", *fromSeqs)
	}
	if len(evSeqs) != 1 || evSeqs[0] != 200 {
		t.Fatalf("交付事件 = %v, want [200]（只有实时那条）", evSeqs)
	}
}

// TestFollowNoBacklogIsSilent 验证无积压时行为与改动前逐字一致：一行摘要都不吐，
// from_seq 仍是 cursor。
func TestFollowNoBacklogIsSilent(t *testing.T) {
	snap := &client.AttachInfo{RecentEvents: []proto.Event{
		{Seq: 0, TaskID: "tk2", Type: proto.EventTypeProgress},
	}}
	live := []proto.Event{{Seq: 1, TaskID: "tk2", Type: proto.EventTypeFailed}}
	ts, fromSeqs := snapAndPushServer(t, []*client.AttachInfo{snap}, live,
		func(c *websocket.Conn) { <-make(chan struct{}) })

	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "tk2", false, 0,
		func(*proto.Event) error { return nil },
		func(*client.BacklogSummary) error { n++; return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	if n != 0 {
		t.Fatalf("吐了 %d 行摘要, want 0（无积压不该加噪音）", n)
	}
	if len(*fromSeqs) != 1 || (*fromSeqs)[0] != "0" {
		t.Fatalf("WS from_seq = %v, want [0]", *fromSeqs)
	}
}

// TestFollowTerminalOnFailedSnapshot 验证：积压被跳过后，failed 由快照 state 接住，
// follow 不会挂在一个死任务上。
func TestFollowTerminalOnFailedSnapshot(t *testing.T) {
	snap := &client.AttachInfo{
		Task:         proto.TaskView{Task: proto.Task{State: proto.TaskStateFailed}},
		RecentEvents: []proto.Event{{Seq: 104, TaskID: "tk3", Type: proto.EventTypeFailed}},
	}
	ts, fromSeqs := snapAndPushServer(t, []*client.AttachInfo{snap}, nil,
		func(c *websocket.Conn) { <-make(chan struct{}) })

	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "tk3", false, 0,
		func(*proto.Event) error { return nil },
		func(*client.BacklogSummary) error { n++; return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil（failed 是正常终结）", err)
	}
	if n != 1 {
		t.Fatalf("摘要 %d 行, want 1", n)
	}
	if len(*fromSeqs) != 0 {
		t.Fatalf("快照已是 failed，不该再建 WS 连接，got %v", *fromSeqs)
	}
}

// TestFollowNilOnBacklogSkipsReconcile 验证 onBacklog 为 nil 时**完全跳过对账**。
//
// 为什么必须是「跳过对账」而不是「丢弃摘要」：后者会让积压事件既不被交付、
// 又无人知晓——事件无声消失是本项目最不能接受的失败形态。
func TestFollowNilOnBacklogSkipsReconcile(t *testing.T) {
	snap := &client.AttachInfo{RecentEvents: []proto.Event{
		{Seq: 104, TaskID: "tk4", Type: proto.EventTypePermissionRequest},
	}}
	live := []proto.Event{{Seq: 200, TaskID: "tk4", Type: proto.EventTypeFailed}}
	ts, fromSeqs := snapAndPushServer(t, []*client.AttachInfo{snap}, live,
		func(c *websocket.Conn) { <-make(chan struct{}) })

	err := client.New(ts.URL, "").FollowEvents(t.Context(), "tk4", false, 0,
		func(*proto.Event) error { return nil }, nil)
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil", err)
	}
	if len(*fromSeqs) != 1 || (*fromSeqs)[0] != "0" {
		t.Fatalf("WS from_seq = %v, want [0]（未对账，起点仍是 cursor）", *fromSeqs)
	}
}
```

同时把 `follow_test.go` 里既有的 7 处 `FollowEvents(...)` 调用末尾补上 `, nil`。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/client/ -run TestFollow -v
```

预期：编译失败，`not enough arguments in call to ...FollowEvents`（补 nil 之前）；补完 nil 后新增的 4 条 FAIL。

- [ ] **Step 3: 实现**

改 `internal/client/client.go` 的 `FollowEvents`。签名加第 6 个参数：

```go
func (c *Client) FollowEvents(ctx context.Context, taskID string, all bool,
	idle time.Duration, onEvent func(*proto.Event) error,
	onBacklog func(*BacklogSummary) error) error {
```

在 `for attempt := 1; ; attempt++ {` 之后、`start := time.Now()` 之前插入：

```go
		// 每次建连前对账——首连与重连同一条路径。断网重连与「忘挂后补挂」是
		// 同一个问题的两种入口，不该有两套代码
		if onBacklog != nil {
			next, terminal, rerr := c.reconcileBacklog(ctx, taskID, fromSeq, onBacklog)
			if rerr != nil {
				return rerr
			}
			fromSeq = next
			if terminal {
				c.log().Info("follow 结束：对账时快照已是 failed", "task", taskID, "from_seq", fromSeq)
				return nil
			}
		}
```

并在文档注释的参数段补一条：

```go
//   - onBacklog: 每次建连前对账出的积压摘要的消费者。**传 nil 表示完全跳过对账**，
//     行为与改动前逐字一致——不能只是丢弃摘要却照样跳过积压，那会让事件无声消失
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/client/ -v
go build ./...
```

预期：`internal/client` 全部 PASS；`go build ./...` 会因 `cmd/wait.go` 还没补参数而失败——这是预期的，Task 5 修。若要在本 task 内保持可编译，先在 `cmd/wait.go` 的调用末尾补 `, nil`，Task 5 再换成真实实现。

- [ ] **Step 5: 加关键节点日志**

对账自身的日志在 Task 3 已落位。本 task 新增的唯一节点是「因快照终态而结束」，已在 Step 3 的 `terminal` 分支打 Info（带 `task` 与 `from_seq`）。这条不能省：它是「follow 明明还没收到 failed 事件却退出了」的唯一解释。

同时确认既有的两条日志仍覆盖成功路径：`follow 开始`（Info）与 `follow 事件交付`（Info，每条）。

- [ ] **Step 6: 加注释**

已随 Step 3 写全：循环内的「首连与重连同一条路径」why，以及 `onBacklog` 为 nil 的语义（连同「为什么必须是跳过对账而不是丢弃摘要」）。

- [ ] **Step 7: 提交**

```bash
git add internal/client/client.go internal/client/follow_test.go cmd/wait.go
git commit -m "feat(client): FollowEvents 每次建连前对账，首连与重连同一条路径

断网重连与「忘挂后补挂」是同一问题的两个入口，用一套代码覆盖。onBacklog 为
nil 时完全跳过对账（不是丢弃摘要——那会让事件无声消失）。快照 state=failed
时吐完摘要即正常收尾，接住被跳过的 failed 事件原本承担的终结判据。"
```

---

### Task 5: `cmd/wait.go` 接线与文档

**Files:**
- Modify: `cmd/wait.go`（`runFollow` 传真实 `onBacklog`；新增 `notifyBacklog`；文件头文档串）
- Modify: `skills/handoff/SKILL.md`

**Interfaces:**
- Consumes: `client.BacklogSummary` / `client.BacklogSummaryType`（Task 2）、`FollowEvents` 的 6 参数签名（Task 4）

- [ ] **Step 1: 写失败的测试**

`cmd` 包的既有测试形态是对着 `httptest` 起的真 agentd 跑。这里只验「摘要行确实落到 stdout 且是合法单行 JSON」，用一个假 agentd 就够。新建 `cmd/wait_backlog_test.go`：

```go
// wait_backlog_test.go —— runFollow 的摘要行输出契约。
//
// 职责：钉住「摘要走 stdout、单行、合法 JSON、type 为 backlog_summary」。
// 边界：不验对账逻辑本身（那在 internal/client 覆盖），只验 cmd 层的接线与输出通道。
package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

func TestBacklogSummaryLineIsSingleJSON(t *testing.T) {
	var sb strings.Builder
	sum := &client.BacklogSummary{
		Type: client.BacklogSummaryType, TaskID: "task-xyz",
		FromSeq: 100, ToSeq: 109, State: proto.TaskStateWaitingAnswer,
		Missed: 2, Stale: 1, Actionable: []proto.Ticket{{ID: "new1", Kind: "gate"}},
	}
	if err := writeBacklogLine(&sb, sum); err != nil {
		t.Fatalf("writeBacklogLine: %v", err)
	}
	out := sb.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("输出 = %q，必须恰好一行（Monitor 按行解析，多一行就多一次唤醒）", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("摘要行不是合法 JSON: %v（原文 %q）", err, out)
	}
	if got["type"] != client.BacklogSummaryType {
		t.Fatalf("type = %v, want %q", got["type"], client.BacklogSummaryType)
	}
	if _, ok := got["actionable"]; !ok {
		t.Fatal("摘要行必须带 actionable——那是审核者唯一能直接据以 reply 的字段")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./cmd/ -run TestBacklogSummaryLine -v
```

预期：编译失败，`undefined: writeBacklogLine`。

- [ ] **Step 3: 实现**

在 `cmd/wait.go` 里新增：

```go
// writeBacklogLine 把积压摘要作为**一行** JSON 写出。
//
// 参数：
//   - w: 目标（生产环境是 cmd.OutOrStdout()）
//   - sum: 对账结果
//
// 返回：
//   - 序列化失败时返回错误——写不出摘要就等于审核者不知道自己错过了什么，
//     必须让 follow 停下而不是继续跑一个没人看得见的循环
//
// 注意：严格一行。stdout 是「每行一个 JSON 对象」的契约，上层（Monitor）按行
// 解析，每一行都是一次会话唤醒——多打一行就多叫醒一次，这正是本功能要消灭的东西。
func writeBacklogLine(w io.Writer, sum *client.BacklogSummary) error {
	b, err := json.Marshal(sum)
	if err != nil {
		return fmt.Errorf("序列化积压摘要: %w", err)
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// notifyBacklog 为积压摘要发一条系统通知（--notify）。
//
// 为什么摘要也要通知：它正是「你离开期间发生了事」的那一次唤醒信号。漏掉它
// 等于把 --notify 在最需要它的场景（断网回来、补挂）悄悄关掉。
func notifyBacklog(sum *client.BacklogSummary) {
	if runtime.GOOS != "darwin" {
		slog.Debug("非 macOS，--notify 忽略", "task", sum.TaskID, "type", sum.Type)
		return
	}
	msg := fmt.Sprintf("任务 %s: 错过 %d 条，待处置 %d 张",
		id8(sum.TaskID), sum.Missed, len(sum.Actionable))
	script := "display notification " + strconv.Quote(msg) +
		" with title " + strconv.Quote("handoff")
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		slog.Warn("发送系统通知失败", "cause", err, "output", truncateBytes(string(out), 200))
	}
}
```

需要补 import `io`。

然后把 `runFollow` 里的 `FollowEvents` 调用补上第 6 个参数：

```go
	err := cli.FollowEvents(cmd.Context(), taskID, false, waitTimeout,
		func(ev *proto.Event) error {
			if notifyFlag {
				notifyEvent(ev)
			}
			b, merr := json.Marshal(ev)
			if merr != nil {
				return fmt.Errorf("序列化事件: %w", merr)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			// 每次遇到回合结束都同步一次：--follow 下一个任务会有多个 completed
			autoSyncAfterWait(cmd, addr, token, ev)
			return nil
		},
		func(sum *client.BacklogSummary) error {
			if notifyFlag {
				notifyBacklog(sum)
			}
			return writeBacklogLine(cmd.OutOrStdout(), sum)
		})
```

并在 `cmd/wait.go` 的文件头「职责」里补一条：

```go
//   - --follow 每次建连前先对账：本机 cursor 之后有积压时吐**一行** backlog_summary
//     （带 missed/stale/actionable），把 cursor 推到当前水位，积压事件不再逐条重放
//     ——stdout 每行是一次会话唤醒，逐条重放会把一次重连变成 N 次唤醒
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./cmd/ -run TestBacklogSummaryLine -v
go build ./... && go test ./... 2>&1 | tail -30
```

预期：全部 PASS，`go build ./...` 通过。

- [ ] **Step 5: 加关键节点日志**

cmd 层的日志纪律是「给人看的一律走 stderr」。此处新增两条：

- `notifyBacklog` 的非 macOS 分支 Debug、通知失败 Warn（已随 Step 3 落位，与 `notifyEvent` 同规格）
- `writeBacklogLine` 本身不打日志——它的失败以错误上抛，由 `runFollow` 末尾既有的错误分支统一处理；在这里补一行日志会与上抛的错误重复

对账本身的 Info/Warn 已在 Task 3 落位，cmd 层不重复。

- [ ] **Step 6: 加注释**

已随 Step 3 写全：两个新函数的文档注释（含「为什么严格一行」「为什么摘要也要通知」），以及文件头职责段的新增条目。

- [ ] **Step 7: 改 skill 文档**

改 `skills/handoff/SKILL.md` 三处：

其一，「cursor 语义」那一节，把「换机接管时本机没有 cursor，wait 从 seq 0 起把历史可动作事件重放一遍」改成：

```markdown
- 换一台机器接管时本机没有 cursor 文件。**`wait --follow` 会在建连前先对账**，
  把水位之前的一切折成一行 `backlog_summary`（带 `missed` / `stale` / `actionable`），
  而不是逐条重放。一次性 `wait`（不带 `--follow`）没有这个机制，仍会从 seq 0 起逐条重放。
```

其二，在「事件分诊表」之前加一小段：

```markdown
### 重连/补挂后的第一行：`backlog_summary`

`wait --follow` 每次建立连接前都会对账一次。本机 cursor 之后有积压时（断网重连、
忘挂之后补挂、换机接管），它先吐**一行**摘要再转入实时流：

    {"type":"backlog_summary","task_id":"…","from_seq":2489,"to_seq":2537,
     "state":"waiting_answer","missed":14,"missed_truncated":false,"stale":11,
     "actionable":[{"id":"…","kind":"gate","request":{…}}]}

怎么读：

- **`actionable` 是权威的「你还欠什么」**，每张带完整请求原文，可直接
  `reply --ticket <id>`。它**不限于间隙内**——断网前你就看见过、一直没答的也在里面。
- `stale` 是间隙里已被审批链答掉的工单数，补 `reply` 会 404，跳过即可。
- `missed_truncated` 为 `true` 时，`missed` / `stale` 的语义是「**至少**这么多」
  ——快照的事件窗口没覆盖到 cursor。此时 `actionable` 仍然精确。
- 摘要行**不是**事件，`agentd` 不存这个类型；它只在客户端合成。

积压事件不会再逐条推给你——那会让一次重连变成 N 次会话唤醒。要看被折叠掉的历史，
用 `handoff show`。
```

其三，在「红旗」表里加一行：

```markdown
| 「重连后没收到那 14 条 permission_request，是不是丢了？」 | 没丢。它们被折进了一行 `backlog_summary`，其中仍需处置的在 `actionable` 里，其余是已被审批链答掉的。 |
```

- [ ] **Step 8: 全量门禁**

```bash
go build ./... && go vet ./... && go test ./... 2>&1 | tail -30
go test ./internal/client/ -race -count=1
```

预期：build / vet 干净，全部包 PASS，`-race` 无告警。

- [ ] **Step 9: 提交**

```bash
git add cmd/wait.go cmd/wait_backlog_test.go skills/handoff/SKILL.md
git commit -m "feat(wait): --follow 把重连积压折成一行 backlog_summary

runFollow 接上 onBacklog：摘要单行写 stdout（严格一行——每行是一次会话唤醒，
这正是本功能要消灭的东西），--notify 时一并发通知。skill 补一节说明怎么读这一行，
并修正 cursor 语义段里「换机接管会逐条重放」的旧描述。"
```

---

## Self-Review

**1. Spec coverage**

| Spec 章节 | 落在哪 |
|---|---|
| §2.1 开场对账（读 cursor → Attach → 比水位 → 吐摘要 → 推 cursor → 带新 from_seq 连） | Task 3 实现 + Task 4 接入循环 |
| §2.1「首连与重连同一条路径」 | Task 4 Step 3（循环内对账）；测试 `TestFollowReconcilesBeforeConnect` + 重连场景由 `snapAndPushServer` 的多快照支持 |
| §2.2 摘要内嵌工单原文、不设阈值 | Task 2 的 `Actionable []proto.Ticket`（原样带 `Request`） |
| §2.3 不改 agentd | Global Constraints；无任何 task 触及 `internal/agentd/` |
| §2.4 换机接管落在同一路径 | Task 2 的截断处理 + Task 5 的 skill 文档 |
| §3.1 线格式与 `type` 复用 | Task 2 `BacklogSummaryType` + Task 5 `writeBacklogLine` |
| §3.2 三计数独立、不做减法 | Task 2 Step 3 实现 + `TestComputeBacklogCountsIndependently` |
| §3.3 全局 seq 不能做减法 | Task 2 `TestComputeBacklogGlobalSeqNotContiguous` |
| §3.4 `isDeliverable` 口径统一 | Task 1（可独立否决） |
| §3.5 截断判据 | Task 2 `TestComputeBacklogTruncation` |
| §4 六条边界 | Attach 失败/404/401 → Task 3 `TestReconcileBacklogDegradesOnAttachFailure`；`state=failed` → Task 3 + Task 4 两条测试；`state=completed` → 无需代码（照常连 WS，复用 B56 归档路径）；`RecentEvents` 空 → `TestComputeBacklogNoBacklog`；cursor 写盘失败 → Task 3 Step 3 的 Warn 分支 |
| §5 十条测试 | 1→Task 4；2→Task 4；3→Task 3；4/5→Task 2；6→Task 2；7→Task 2；8→Task 3+4；9→Task 4 夹具；10→Task 1+Task 2 |
| §6 日志与注释 | 每个 task 的 Step「加关键节点日志」「加注释」 |
| §7 明确不做 | Global Constraints |

**缺口检查**：spec §4 的「`state == completed`（已归档）」一行没有对应测试。这是有意的——它不引入任何新代码路径（对账不特判 completed，照常连 WS，由服务端的正常关闭码收尾），而该路径已被 B56 的 `TestFollowArchiveWithIdleSetIsNormal` 覆盖。不为「什么都没做」补一条空测试。

**2. Placeholder scan**：无 TBD / TODO / 「类似 Task N」/ 「补充适当的错误处理」。每个代码 step 都给了可直接粘贴的完整代码块。

**3. Type consistency**

- `isDeliverable(proto.EventType) bool` — Task 1 定义，Task 2 `computeBacklog` 内使用，名字一致。
- `computeBacklog(taskID string, fromSeq int64, snap *AttachInfo) *BacklogSummary` — Task 2 定义，Task 3 调用，参数序一致。
- `reconcileBacklog(ctx, taskID, fromSeq, onBacklog) (int64, bool, error)` — Task 3 定义，Task 4 按 `next, terminal, rerr` 三返回值接收，一致。
- `BacklogSummary` 字段名在 Task 2（定义）、Task 3（日志取 `sum.ToSeq` / `sum.Missed` / `sum.Stale` / `sum.MissedTruncated` / `sum.Actionable` / `sum.State`）、Task 4（测试断言 `ToSeq` / `Missed`）、Task 5（`notifyBacklog` 取 `Missed` / `Actionable` / `TaskID` / `Type`）四处一致。
- `writeBacklogLine(io.Writer, *client.BacklogSummary) error` — Task 5 Step 1 测试与 Step 3 实现签名一致（测试在 `package cmd` 内，可调未导出函数）。
- `FollowEvents` 的 6 参数签名 — Task 4 定义，Task 4 的 4 条新测试与 Task 5 的 `runFollow` 调用一致；Task 1 的 2 条测试写于签名变更前，Task 4 Step 1 已明确要求给既有调用补 `nil`。
