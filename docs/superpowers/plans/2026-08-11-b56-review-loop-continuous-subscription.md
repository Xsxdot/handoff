# 审核回路的持续订阅（B56）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让审核者对一个任务的事件订阅在会话存续期间连续不断（`wait --follow`），并把「当前有几个人在听」变成 agentd 可查询、`handoff status` 可见的事实（`watchers`）。

**Architecture:** 三条互相独立的改动线。①**读侧**：`Hub` 已有的 `subs` 表暴露出订阅数（`Watchers`），经 `proto.TaskView` 与 `proto.ActiveTask` 上到 `/api/tasks` 与 `/api/status`，`handoff status` 据 §3.3 的写死判据打「⚠ 无人值守」。②**流侧**：`client` 把「建一次连接、读帧」抽成 `streamOnce`，一次性 `wait` 与新增的 `FollowEvents` 共用它；follow 不在首个事件后返回，`--timeout` 改为跨重连的**绝对空闲期限**。③**收尾**：`done` 归档不产生任何事件，跟随端无从得知「没有下文了」，故 `Hub.CloseTask` 关闭该任务全部订阅，WS 以正常关闭码收尾，客户端据此退出 0。

**Tech Stack:** Go 1.x（标准库 + `github.com/coder/websocket` + `github.com/spf13/cobra` + `log/slog`），SQLite（本计划**无**数据库变更）。

## Global Constraints

以下约束逐条来自 spec，适用于**每一个** task：

- **不修 B52**：执行侧的子会话权限归属是独立缺陷，本计划一行都不碰 `internal/executor/`。
- **`wait` 默认行为一字不改**：`--follow` 是可选开关；不带它时的输出、退出码、cursor 语义与今天完全一致。任何改动若让既有 `wait` 行为变化，即为实现错误。
- **线格式只增不改不删**：`/api/tasks`、`/api/tasks/{id}`、`/api/status` 只允许新增 JSON 键；老客户端解码不得破。
- **`watchers` 不落库**：它是 `Hub` 的瞬时运行态，不得写进 SQLite，不得加进 `proto.Task`。
- **异常判据写死**：`watchers == 0` 只在 `pending` / `running` / `waiting_answer` 三个状态算异常；`waiting_review` 与终态**不算**。
- **空闲以「任何帧」为准**：包含被客户端过滤掉的 `progress`。只有 progress 流入时**不得**触发超时。
- **日志一律走 `slog`**，禁止 `fmt.Printf` 作为日志手段；`wait` 的 stdout 是「每事件一行 JSON」的契约，任何人读信息一律走 stderr。
- **公共闸门**（每个 task 的提交前都要过）：`gofmt -l .` 无输出、`go build ./...`、`go vet ./...`、`go test ./... -count=1` 全绿；涉及 `internal/agentd` / `internal/client` / `cmd` 的改动另跑 `go test -race` 对应包。

---

### Task 1: `Hub.Watchers` —— 把订阅数读出来

**Files:**
- Modify: `internal/agentd/hub.go`（在 `unsubscribe` 之后、`Publish` 之前插入）
- Test: `internal/agentd/hub_test.go`（追加）

**Interfaces:**
- Consumes: 无（本任务是整条链的起点）
- Produces: `func (h *Hub) Watchers(taskID string) int` —— Task 2 与 Task 3 都直接调它。

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/hub_test.go`：

```go
// TestWatchersCountsSubscribers 验证 Watchers 精确反映当前订阅数：
// 未订阅为 0、订阅后逐个累加、取消后逐个回落、全部取消后归零。
//
// 为什么这个数字必须干净：handoff status 的「⚠ 无人值守」直接以它为判据，
// 多算一个（内部订阅者虚高）就是漏报，少算一个就是误报。
func TestWatchersCountsSubscribers(t *testing.T) {
	hub := agentd.NewHub()

	if n := hub.Watchers("t-watch"); n != 0 {
		t.Fatalf("未订阅时 Watchers = %d, want 0", n)
	}
	_, cancel1 := hub.Subscribe("t-watch")
	if n := hub.Watchers("t-watch"); n != 1 {
		t.Fatalf("一个订阅者时 Watchers = %d, want 1", n)
	}
	_, cancel2 := hub.Subscribe("t-watch")
	if n := hub.Watchers("t-watch"); n != 2 {
		t.Fatalf("两个订阅者时 Watchers = %d, want 2", n)
	}
	// 别的任务不受影响：hub 按 taskID 分表，串号会让整条判据失效
	if n := hub.Watchers("t-other"); n != 0 {
		t.Fatalf("其他任务的 Watchers = %d, want 0", n)
	}

	cancel1()
	if n := hub.Watchers("t-watch"); n != 1 {
		t.Fatalf("取消一个后 Watchers = %d, want 1", n)
	}
	cancel2()
	cancel2() // 重复取消幂等，不得把计数减成负数
	if n := hub.Watchers("t-watch"); n != 0 {
		t.Fatalf("全部取消后 Watchers = %d, want 0", n)
	}
}

// TestWatchersConcurrent 验证并发订阅/取消/读取下 Watchers 不数据竞争。
// 单跑无意义，价值在 -race 下（见本 task 的 Step 4）。
func TestWatchersConcurrent(t *testing.T) {
	hub := agentd.NewHub()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, cancel := hub.Subscribe("t-race")
			_ = hub.Watchers("t-race")
			cancel()
		}()
	}
	wg.Wait()
	if n := hub.Watchers("t-race"); n != 0 {
		t.Fatalf("并发收尾后 Watchers = %d, want 0", n)
	}
}
```

`hub_test.go` 的 import 块需补 `"sync"`。

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestWatchers' -count=1
```

预期：编译失败，`hub.Watchers undefined (type *agentd.Hub has no field or method Watchers)`。

- [ ] **Step 3: 写最小实现**

在 `internal/agentd/hub.go` 的 `unsubscribe` 之后插入：

```go
// Watchers 返回当前订阅该任务事件流的连接数。
//
// 参数：
//   - taskID: 任务 ID
//
// 返回：
//   - 订阅者数量；无人订阅或任务不存在均返回 0（两者对本层等价）
//
// 为什么这个数字可以直接当「有几个审核者在听」用：全仓 Subscribe 只有一个调用点
//（/ws/events 的处理器），没有任何内部订阅者混在里面。若将来新增了内部订阅者，
// 这条结论就不再成立，必须同步修改本注释与 status 的判据。
//
// 注意：
//   - 走 Hub 现有的 mu，与 Subscribe/unsubscribe/Publish 互斥；返回的是调用瞬间
//     的快照，调用方不得假设它在返回后仍然成立
func (h *Hub) Watchers(taskID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[taskID])
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/agentd/ -run 'TestWatchers' -count=1 && go test -race ./internal/agentd/ -run 'TestWatchers' -count=1
```

预期：两条均 PASS，`-race` 无 DATA RACE 报告。

- [ ] **Step 5: 加关键节点日志**

本函数**不打日志**，这是刻意的：它是纯读快照，会被 `status`（每次列出全部活跃任务）与 `/api/tasks`（每个任务一次）高频调用，逐次打点会把 agentd.log 淹掉。订阅数变化的可观测性已由 `Subscribe` / `unsubscribe` 现有的两条 Debug 承担（`"事件订阅"` / `"取消事件订阅"`，均带 `subscribers` 字段）。

把这条理由写进上面 Step 3 的注释里（追加到「注意」块）：

```go
//   - 本方法刻意不打日志：它是高频纯读，订阅数变化已由 Subscribe/unsubscribe
//     的 Debug 日志覆盖，这里再打一遍只会把真正的线索淹掉
```

- [ ] **Step 6: 加注释**

确认 Step 3 的实现已包含：导出方法的完整 doc（参数/返回/why/注意）。`hub.go` 是既有文件，其文件头注释需补一句职责——把顶部 `// 职责：` 块的第一条改为：

```go
//   - 按 taskID 维度做事件实时扇出（Subscribe/Publish/Watchers），供 HTTP/WS 层推送
```

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
```

```bash
git add internal/agentd/hub.go internal/agentd/hub_test.go && git commit -m "feat(b56): Hub 暴露 Watchers，把订阅数变成可查询的事实"
```

---

### Task 2: `proto.TaskView` —— 任务接口带上 `watchers`

**Files:**
- Modify: `internal/proto/proto.go`（`Task` 与其 `Workdir()` 方法之后）
- Modify: `internal/agentd/server.go:217-231`（`handleListTasks`）、`:233-283`（`handleGetTask` 与 `taskDetail`）
- Modify: `internal/client/client.go:106-110`（`AttachInfo`）、`:257-271`（`ListTasks`）
- Modify: `cmd/attach.go:175`（`printAttachSuggestions` 形参）
- Test: `internal/proto/proto_test.go`（新建）、`internal/agentd/watchers_test.go`（新建，**白盒 `package agentd`**）

**Interfaces:**
- Consumes: `Hub.Watchers(taskID) int`（Task 1）
- Produces:
  - `proto.TaskView{ proto.Task; Watchers int \`json:"watchers"\` }`
  - `func (c *Client) ListTasks(ctx) ([]proto.TaskView, error)` —— 返回类型由 `[]proto.Task` 改为 `[]proto.TaskView`
  - `client.AttachInfo.Task` 类型由 `proto.Task` 改为 `proto.TaskView`

- [ ] **Step 1: 写失败的测试**

新建 `internal/proto/proto_test.go`：

```go
// proto 包测试：钉住 TaskView 的线格式兼容性。
package proto_test

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestTaskViewWireCompatible 验证 TaskView 的 JSON 与 Task **逐键兼容**：
// 字段提升后 Task 的每一个键都在原位，只多出一个 watchers。
//
// 缺陷形态：若有人把 Watchers 写成具名字段（如 `Task proto.Task`）而非嵌入，
// 线格式会变成 {"task":{...},"watchers":0}，所有老客户端当场解不出任务。
func TestTaskViewWireCompatible(t *testing.T) {
	task := proto.Task{ID: "t1", Name: "n1", State: proto.TaskStateRunning}
	view := proto.TaskView{Task: task, Watchers: 2}

	var fromTask, fromView map[string]any
	b1, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("序列化 Task: %v", err)
	}
	b2, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("序列化 TaskView: %v", err)
	}
	if err := json.Unmarshal(b1, &fromTask); err != nil {
		t.Fatalf("反序列化 Task: %v", err)
	}
	if err := json.Unmarshal(b2, &fromView); err != nil {
		t.Fatalf("反序列化 TaskView: %v", err)
	}
	for k, want := range fromTask {
		got, ok := fromView[k]
		if !ok {
			t.Errorf("TaskView 丢了 Task 的键 %q", k)
			continue
		}
		if !jsonEqual(got, want) {
			t.Errorf("键 %q: TaskView = %v, Task = %v", k, got, want)
		}
	}
	if len(fromView) != len(fromTask)+1 {
		t.Errorf("TaskView 的键数 = %d, want %d（Task 的键 + watchers 一个）",
			len(fromView), len(fromTask)+1)
	}
	if fromView["watchers"] != float64(2) {
		t.Errorf("watchers = %v, want 2", fromView["watchers"])
	}
}

// TestTaskViewDecodesIntoOldTask 验证老客户端（只认 proto.Task）解 TaskView 不破。
func TestTaskViewDecodesIntoOldTask(t *testing.T) {
	b, err := json.Marshal(proto.TaskView{
		Task:     proto.Task{ID: "t1", Name: "n1", State: proto.TaskStateRunning},
		Watchers: 3,
	})
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	var old proto.Task
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatalf("老客户端解码失败: %v", err)
	}
	if old.ID != "t1" || old.State != proto.TaskStateRunning {
		t.Errorf("老客户端解出的任务不对: %+v", old)
	}
}

// jsonEqual 比较两个 encoding/json 解出的任意值。
func jsonEqual(a, b any) bool {
	ba, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ba) == string(bb)
}
```

服务端拼装的断言新建 `internal/agentd/watchers_test.go`。**必须是白盒 `package agentd`**：要直接摸 `srv.hub` 和处理器，黑盒的 `agentd_test` 摸不到。复用本包既有脚手架 `newTestServerWithManager`（`regression_round2_test.go:41`，返回 `*Server, *Manager, *store.Store`，白盒共用一个 hub）与 `createRunningTask`（`manager_test.go:552`），**不要新造一套 server 启动脚手架**：

```go
// watchers_test.go —— watchers 运行态从 hub 上到 API 的白盒测试。
//
// 职责：钉住 /api/tasks 与 /api/tasks/{id} 的 watchers 取自 hub 实时订阅数，
// 以及 Manager.Status / Manager.Done 与订阅表的联动。
//
// 边界：不验 CLI 渲染（那在 cmd/status_test.go），不验线格式兼容（在 internal/proto）。
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestListTasksCarriesWatchers 验证 /api/tasks 的每个任务都带 watchers，
// 且数值来自 hub 的实时订阅数而不是恒 0。
func TestListTasksCarriesWatchers(t *testing.T) {
	srv, _, st := newTestServerWithManager(t)
	const id = "task-watch-list"
	createRunningTask(t, st, id)

	if got := listWatchers(t, srv, id); got != 0 {
		t.Fatalf("无人订阅时 watchers = %d, want 0", got)
	}
	_, cancel := srv.hub.Subscribe(id)
	defer cancel()
	if got := listWatchers(t, srv, id); got != 1 {
		t.Fatalf("一个订阅者时 watchers = %d, want 1", got)
	}
	cancel()
	if got := listWatchers(t, srv, id); got != 0 {
		t.Fatalf("取消订阅后 watchers = %d, want 0", got)
	}
}

// TestGetTaskCarriesWatchers 验证任务详情接口同样带 watchers。
func TestGetTaskCarriesWatchers(t *testing.T) {
	srv, _, st := newTestServerWithManager(t)
	const id = "task-watch-detail"
	createRunningTask(t, st, id)
	_, cancel := srv.hub.Subscribe(id)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	srv.handleGetTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var detail struct {
		Task proto.TaskView `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("解码详情: %v", err)
	}
	if detail.Task.Watchers != 1 {
		t.Errorf("详情 watchers = %d, want 1", detail.Task.Watchers)
	}
	if detail.Task.ID != id {
		t.Errorf("详情 task.id = %q, want %q（字段提升没生效？）", detail.Task.ID, id)
	}
}

// listWatchers 调 handleListTasks 并取出目标任务的 watchers。
func listWatchers(t *testing.T, srv *Server, taskID string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleListTasks(rec, httptest.NewRequest(http.MethodGet, "/api/tasks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var views []proto.TaskView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("解码任务列表: %v", err)
	}
	for _, v := range views {
		if v.ID == taskID {
			return v.Watchers
		}
	}
	t.Fatalf("任务列表里没有 %s", taskID)
	return 0
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/proto/ ./internal/agentd/ -run 'TaskView|Watchers' -count=1
```

预期：`undefined: proto.TaskView`。

- [ ] **Step 3: 写最小实现**

`internal/proto/proto.go`，在 `Workdir()` 方法之后插入：

```go
// TaskView 是 Task 的 API 视图：任务本体 + 不落库的运行态。
//
// 为什么用嵌入而不是给 Task 加字段：Watchers 是 agentd 内 Hub 的瞬时状态，
// 与任务的持久身份无关。加进 Task 会让存储层背一个它不该知道的概念，迟早有人
// 把它写进 SQLite；嵌入则让存储结构保持纯粹，同时 JSON 字段提升后线格式与旧版
// 逐字节兼容——只多一个 watchers 键，老客户端解码不受影响。
//
// 注意：Watchers 是服务端应答那一刻的快照，不做任何时效承诺。
type TaskView struct {
	Task
	// Watchers 是当前订阅该任务事件流的连接数（几个审核者在听）。
	// 0 不一定是异常：waiting_review 与终态本来就不需要有人盯，判据见
	// handoff status 的 unattended。
	Watchers int `json:"watchers"`
}
```

`internal/agentd/server.go` 的 `handleListTasks`，把 `writeJSON` 之前改为：

```go
	if tasks == nil {
		// 空列表序列化为 [] 而非 null，保证客户端解码出的始终是数组
		tasks = []proto.Task{}
	}
	// 拼装 API 视图：附上「有几个人在听」这条只有 hub 知道的运行态
	views := make([]proto.TaskView, 0, len(tasks))
	unattended := 0
	for _, t := range tasks {
		w := s.hub.Watchers(t.ID)
		if w == 0 && !isTerminalState(t.State) && t.State != proto.TaskStateWaitingReview {
			unattended++
		}
		views = append(views, proto.TaskView{Task: t, Watchers: w})
	}
	s.log.Info("任务列表完成", "tasks", len(views), "unattended", unattended)
	writeJSON(w, http.StatusOK, views)
```

`taskDetail` 与 `handleGetTask`：

```go
type taskDetail struct {
	Task           proto.TaskView `json:"task"`
	PendingTickets []proto.Ticket `json:"pending_tickets"`
	RecentEvents   []proto.Event  `json:"recent_events"`
}
```

```go
	writeJSON(w, http.StatusOK, taskDetail{
		Task:           proto.TaskView{Task: *task, Watchers: s.hub.Watchers(taskID)},
		PendingTickets: pending,
		RecentEvents:   events,
	})
```

`internal/client/client.go`：

```go
type AttachInfo struct {
	Task           proto.TaskView `json:"task"`
	PendingTickets []proto.Ticket `json:"pending_tickets"`
	RecentEvents   []proto.Event  `json:"recent_events"`
}
```

```go
func (c *Client) ListTasks(ctx context.Context) ([]proto.TaskView, error) {
	// ...（函数体只改这两行）
	var tasks []proto.TaskView
	// ...
}
```

`cmd/attach.go:175` 的形参：

```go
func printAttachSuggestions(w io.Writer, tasks []proto.TaskView) {
```

`cmd/wait.go:133` 与 `cmd/pull.go:36` 的 `&info.Task` 改为 `&info.Task.Task`（`syncTaskBranch` 收的是 `*proto.Task`，嵌入后要取内层）。

- [ ] **Step 4: 运行测试确认通过**

```bash
go build ./... && go test ./internal/proto/ ./internal/agentd/ ./internal/client/ ./cmd/ -count=1
```

预期：全 PASS。若 `cmd/attach_test.go` 等既有测试构造了 `[]proto.Task` 字面量，一并改为 `[]proto.TaskView{{Task: proto.Task{...}}}`——**只改类型，不改断言**。

- [ ] **Step 5: 加关键节点日志**

- `handleListTasks`：把原来那条只在入口的 `s.log.Info("任务列表请求", ...)` 保留，并在出口补上 Step 3 里那条 `"任务列表完成"`（带 `tasks` 与 `unattended` 两个字段）。**为什么出口也要打**：成功路径静默是「查不出到底跑没跑」的根源；`unattended` 这个计数还顺带把「昨晚那种没人听的任务有几个」写进了 agentd 自己的日志，事后复盘不必再靠客户端。
- `handleGetTask`：在既有的入口 Info 之后、`writeJSON` 之前补一条：

```go
	s.log.Info("任务详情完成", "task", taskID, "state", task.State,
		"pending", len(pending), "watchers", watchers)
```

（把 `s.hub.Watchers(taskID)` 的结果先存进局部变量 `watchers` 再用于两处。）

- [ ] **Step 6: 加注释**

- `proto.TaskView` 的类型 doc 与 `Watchers` 字段注释（Step 3 已含，确认落到位）。
- `handleListTasks` 的函数 doc 追加一句边界：

```go
// handleListTasks 返回全部任务（created_at 降序）及其实时订阅数，供 tasks 命令展示。
//
// 注意：watchers 取自 hub 的瞬时状态、不落库；它只回答「此刻有几个连接在听」，
// 不回答「该不该有人听」——那条判据在 status 侧（unattended）。
```

- `taskDetail.Task` 字段上方补一行 why：

```go
	// Task 用 TaskView 而非 Task：多带一个 watchers，且因字段提升线格式不变
```

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
```

```bash
git add internal/proto/ internal/agentd/server.go internal/client/client.go cmd/attach.go cmd/wait.go cmd/pull.go && git commit -m "feat(b56): 任务接口带上 watchers（TaskView 嵌入，线格式只增一个键）"
```

---

### Task 3: `handoff status` 的「⚠ 无人值守」与 `stall_timeout` 外露

**Files:**
- Modify: `internal/proto/status.go`（`ActiveTask` 加 `Watchers`，`StatusResp` 加 `StallTimeout`）
- Modify: `internal/agentd/status.go`（`probeActive` 填 `Watchers`，`Status` 填 `StallTimeout`）
- Modify: `cmd/status.go`（`renderStatus` 的活跃行、新增 `unattended`）
- Test: `cmd/status_test.go`（追加，`package cmd`）、`internal/agentd/watchers_test.go`（追加，Task 2 建的白盒文件）

**Interfaces:**
- Consumes: `Hub.Watchers(taskID) int`（Task 1）
- Produces:
  - `proto.ActiveTask.Watchers *int \`json:"watchers,omitempty"\`` —— **指针**，nil = 对端没给（未知）
  - `proto.StatusResp.StallTimeout string \`json:"stall_timeout,omitempty"\`` —— `time.Duration.String()` 形式（如 `"2h0m0s"`），空串 = 未知；Task 6 的 WARN 读它
  - `func unattended(a proto.ActiveTask) bool`（`cmd` 包内）

- [ ] **Step 1: 写失败的测试**

追加到 `cmd/status_test.go`：

```go
// TestUnattendedJudgement 钉死 §3.3 的异常判据。
//
// 为什么必须写死而不是「watchers==0 就报警」：waiting_review 等审核者裁决，
// 挂几天都正常，把它算进来这条标记就会天天亮，变成没人再看的狼来了。
func TestUnattendedJudgement(t *testing.T) {
	zero, one := 0, 1
	cases := []struct {
		name     string
		state    string
		watchers *int
		want     bool
	}{
		{"running 无人听 = 异常", "running", &zero, true},
		{"pending 无人听 = 异常", "pending", &zero, true},
		{"waiting_answer 无人听 = 异常", "waiting_answer", &zero, true},
		{"waiting_review 无人听 = 正常", "waiting_review", &zero, false},
		{"running 有人听 = 正常", "running", &one, false},
		{"对端没给 watchers = 不下结论", "running", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := unattended(proto.ActiveTask{State: c.state, Watchers: c.watchers})
			if got != c.want {
				t.Errorf("unattended(%s, %v) = %v, want %v", c.state, c.watchers, got, c.want)
			}
		})
	}
}

// TestRenderStatusMarksUnattended 验证活跃任务行在存活结论之后追加标记，
// 且只对该标记的三个状态出现。
func TestRenderStatusMarksUnattended(t *testing.T) {
	zero := 0
	var buf bytes.Buffer
	renderStatus(&buf, "127.0.0.1:7777", proto.BuildInfo{}, &proto.StatusResp{
		TaskCounts: map[string]int{"running": 1, "waiting_review": 1},
		Active: []proto.ActiveTask{
			{ID: "aaaaaaaa-1", Name: "跑着的", State: "running",
				Executor: "opencode", Live: proto.LiveAlive, Watchers: &zero},
			{ID: "bbbbbbbb-1", Name: "等审的", State: "waiting_review",
				Executor: "opencode", Live: proto.LiveAlive, Watchers: &zero},
		},
	})
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var running, review string
	for _, l := range lines {
		if strings.Contains(l, "跑着的") {
			running = l
		}
		if strings.Contains(l, "等审的") {
			review = l
		}
	}
	if !strings.Contains(running, "⚠ 无人值守") {
		t.Errorf("running + watchers=0 未标记无人值守: %q", running)
	}
	if strings.Contains(review, "⚠ 无人值守") {
		t.Errorf("waiting_review 不该标记无人值守: %q", review)
	}
	if !strings.Contains(running, "executor 存活") {
		t.Errorf("标记不得顶掉既有的存活结论: %q", running)
	}
}
```

追加到 Task 2 建的 `internal/agentd/watchers_test.go`（白盒，`m.cfg` 可直接改写，故不必给 `newTestManager` 加参数）：

```go
// TestStatusCarriesStallTimeout 验证 /api/status 把 agentd 自己的 stalltimeout
// 报出来——这是 wait --follow 判断「--timeout 会不会抢在 stalled 前面」的唯一依据。
//
// 刻意设成 90m 而不是默认的 2h：默认值恒等于零值之外的另一个常数，测不出
// 「到底是读了配置还是写死了」。
func TestStatusCarriesStallTimeout(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	m.cfg.StallTimeout = 90 * time.Minute
	resp, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.StallTimeout != "1h30m0s" {
		t.Errorf("StallTimeout = %q, want %q", resp.StallTimeout, "1h30m0s")
	}
}

// TestStatusCarriesWatchers 验证活跃任务带上订阅数，且是指针（老 agentd 缺字段
// 与「确实是 0」必须可区分）。
func TestStatusCarriesWatchers(t *testing.T) {
	m, st, hub, _ := newTestManager(t)
	const id = "task-status-watch"
	createRunningTask(t, st, id)

	resp, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(resp.Active) != 1 {
		t.Fatalf("活跃任务数 = %d, want 1", len(resp.Active))
	}
	if resp.Active[0].Watchers == nil || *resp.Active[0].Watchers != 0 {
		t.Fatalf("无人订阅时 Watchers = %v, want 指向 0 的指针", resp.Active[0].Watchers)
	}
	_, cancel := hub.Subscribe(id)
	defer cancel()
	resp, err = m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.Active[0].Watchers == nil || *resp.Active[0].Watchers != 1 {
		t.Fatalf("一个订阅者时 Watchers = %v, want 指向 1 的指针", resp.Active[0].Watchers)
	}
}
```

本文件的 import 需补 `"time"`。

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./cmd/ ./internal/agentd/ -run 'Unattended|StallTimeout' -count=1
```

预期：`undefined: unattended`、`resp.StallTimeout undefined`。

- [ ] **Step 3: 写最小实现**

`internal/proto/status.go`，`ActiveTask` 加字段：

```go
	// Watchers 是当前订阅该任务事件流的连接数（几个审核者在听）。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd），与「确实是 0」
	// 是两回事。猜一个 0 就是在制造假阳性——与 Live 三态用 unknown 而不猜死
	// 是同一条纪律：一条会说谎的诊断命令比没有更糟，因为你会信它。
	Watchers *int `json:"watchers,omitempty"`
```

`StatusResp` 加字段：

```go
	// StallTimeout 是 agentd 看门狗判定「卡住」的空闲阈值，形如 "2h0m0s"
	//（time.Duration.String()）。空串 = 对端未提供。
	//
	// 为什么要外露：wait --follow 的 --timeout 若不大于它，两个计时器同时到点时
	// 客户端的 124 会抢在 agentd 的 stalled 前面退出进程，把一次带 last_seq 和
	// idle 时长的**诊断**降级成一句「我没收到东西」——审核者拿到的信息严格更少。
	StallTimeout string `json:"stall_timeout,omitempty"`
```

`internal/agentd/status.go`，`Status()` 的 `resp` 字面量加一行：

```go
		StallTimeout:    m.cfg.StallTimeout.String(),
```

`probeActive` 的循环里，`at` 构造之后、探活之前：

```go
		// watchers 取自 hub 的瞬时订阅数：这是「有没有人在听这个任务」的唯一真相
		// 来源，昨晚 f7d07ece 空转 7h43m 时它一直是 0，只是从没有人问过
		w := m.hub.Watchers(t.ID)
		at.Watchers = &w
```

`cmd/status.go`，`renderStatus` 的活跃行改为：

```go
	for _, a := range st.Active {
		line := fmt.Sprintf("  %s  %s  %s  %s  %s",
			short8(a.ID), a.Name, a.State, a.Executor, liveText(a))
		if unattended(a) {
			// 追加而不是替换：executor 活着但没人听，与 executor 死了是两个独立结论，
			// 昨晚的现场正是「存活 + 无人值守」这一格
			line += "  ⚠ 无人值守"
		}
		fmt.Fprintln(w, line)
	}
```

新增函数（放在 `liveText` 之后）：

```go
// unattended 判断一个活跃任务是否处于「该有人听却没人听」的异常状态。
//
// 参数：
//   - a: status 响应里的一个活跃任务
//
// 返回：
//   - true 仅当：对端给出了 watchers（非 nil）、其值为 0、且状态属于
//     pending / running / waiting_answer 三者之一
//
// 为什么判据写死而不做成配置：这三个状态里事件随时会来，没人听等于事件掉地上；
// 而 waiting_review 是在等审核者裁决，挂几天都正常，本就不需要有人盯着。把它
// 也算进来，这条标记会天天亮，一周之内就没人再看它了——误报是诊断标记最贵的
// 失败模式。终态同理。
func unattended(a proto.ActiveTask) bool {
	if a.Watchers == nil || *a.Watchers > 0 {
		return false
	}
	switch proto.TaskState(a.State) {
	case proto.TaskStatePending, proto.TaskStateRunning, proto.TaskStateWaitingAnswer:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./cmd/ ./internal/agentd/ -count=1 && go test -race ./internal/agentd/ -count=1
```

预期：全 PASS。

- [ ] **Step 5: 加关键节点日志**

`internal/agentd/status.go` 的 `Status()` 出口，把既有那条 `"状态聚合完成"` 补上无人值守计数：

```go
	unattended := 0
	for _, a := range resp.Active {
		if a.Watchers != nil && *a.Watchers == 0 &&
			a.State != string(proto.TaskStateWaitingReview) {
			unattended++
		}
	}
	m.log.Info("状态聚合完成", "tasks", len(tasks), "active", len(active),
		"executors", len(names), "unattended", unattended)
```

**为什么值得占一个字段**：这条日志留在 agentd 自己的盘上，事后复盘「那几个小时到底有没有人在听」不必再依赖客户端当时打了什么。

`cmd/status.go` 侧**不加日志**：status 是人读命令，结论已在 stdout；再往 stderr 打一遍会让正常输出看着像出了错（与本文件既有的「404 降到 Debug」是同一条理由）。

- [ ] **Step 6: 加注释**

- `ActiveTask.Watchers` / `StatusResp.StallTimeout` 的字段注释（Step 3 已含）。
- `unattended` 的完整 doc（Step 3 已含）。
- `renderStatus` 里「追加而不是替换」那条 inline why（Step 3 已含）。
- `probeActive` 里 watchers 取值处的 why（Step 3 已含）。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
```

```bash
git add internal/proto/status.go internal/agentd/status.go cmd/status.go cmd/status_test.go internal/agentd/ && git commit -m "feat(b56): status 标记无人值守活跃任务，并外露 stalltimeout"
```

---

### Task 4: 归档时关闭订阅 —— 让 `done` 对跟随端可见

**Files:**
- Modify: `internal/agentd/hub.go`（`Watchers` 之后加 `CloseTask`）
- Modify: `internal/agentd/server.go:1083-1104`（排空器）、`:1222-1241`（实时写循环）
- Modify: `internal/agentd/manager.go`（`Done`，`transit` 成功之后）
- Test: `internal/agentd/hub_test.go`（黑盒）、`internal/agentd/ws_regression_round2_test.go`（追加，白盒，那里有现成的 `newWSTestEnv`）、`internal/agentd/watchers_test.go`（追加）

**Interfaces:**
- Consumes: 无
- Produces: `func (h *Hub) CloseTask(taskID string) int`（返回被关闭的订阅数）；WS 侧新增的收尾语义：**任务归档 → 服务端以 `websocket.StatusNormalClosure` + 原因 `"task archived"` 关闭连接**。Task 5 的客户端据此判终态。

**背景（实现前必读）：** `Manager.Done` 走的是 `m.transit(taskID, proto.TaskStateCompleted, "done")`，而 `transit` **只改状态、不追加任何事件**。所以归档在事件流上是完全无声的——跟随中的客户端会一直挂着，直到空闲超时打出一个会误导人的 124。这是本任务存在的全部理由。

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/hub_test.go`：

```go
// TestCloseTaskClosesAllSubscribers 验证 CloseTask 关闭该任务全部订阅、
// 返回关闭数、不误伤别的任务，且随后的 cancel 幂等不 panic（不得二次 close）。
func TestCloseTaskClosesAllSubscribers(t *testing.T) {
	hub := agentd.NewHub()
	ch1, cancel1 := hub.Subscribe("t-done")
	ch2, cancel2 := hub.Subscribe("t-done")
	chOther, cancelOther := hub.Subscribe("t-live")
	defer cancelOther()

	if n := hub.CloseTask("t-done"); n != 2 {
		t.Fatalf("CloseTask 返回 %d, want 2", n)
	}
	for name, ch := range map[string]<-chan proto.Event{"ch1": ch1, "ch2": ch2} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("%s 归档后仍收到事件", name)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 未被关闭", name)
		}
	}
	if n := hub.Watchers("t-done"); n != 0 {
		t.Fatalf("归档后 Watchers = %d, want 0", n)
	}
	// 别的任务不受影响
	hub.Publish(proto.Event{Seq: 9, TaskID: "t-live", Type: proto.EventTypeProgress})
	select {
	case ev := <-chOther:
		if ev.Seq != 9 {
			t.Fatalf("t-live 收到的事件不对: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("t-live 的订阅被误伤")
	}

	// 连接收尾时的 defer cancel 必须是空操作：通道已不在表中，重复 close 会 panic
	cancel1()
	cancel2()
	if n := hub.CloseTask("t-done"); n != 0 {
		t.Fatalf("重复 CloseTask 返回 %d, want 0", n)
	}
	// Publish 到已归档任务不得 panic（向已关闭通道发送）
	hub.Publish(proto.Event{Seq: 10, TaskID: "t-done", Type: proto.EventTypeProgress})
}
```

追加到 `internal/agentd/ws_regression_round2_test.go`（复用该文件的 `newWSTestEnv` / `dialWS` / `seedTask`，见 `:59` `:76` `:91`）：

```go
// TestWSClosesNormallyOnArchive 验证订阅被 hub 关闭（任务归档）时，
// 服务端以 StatusNormalClosure 收尾，而不是把连接晾着。
//
// 缺陷形态：transit 只改状态不发事件，跟随端拿不到任何「没有下文了」的信号，
// 会一直挂到空闲超时——那是一个会把审核者引向「agentd 失联」的假线索。
func TestWSClosesNormallyOnArchive(t *testing.T) {
	env := newWSTestEnv(t)
	const id = "task-archive-ws"
	env.seedTask(t, id)
	conn := env.dialWS(t, id, 0)

	// 等订阅真正建立：websocket.Dial 在 Accept 后即返回，服务端的 Subscribe
	// 在其后异步执行（本文件 :164 已记过这条时序）
	waitWatchers(t, env.srv.hub, id, 1)
	env.srv.hub.CloseTask(id)

	for {
		_, _, err := conn.Read(context.Background())
		if err == nil {
			continue // 归档前排队的事件，读干净
		}
		if got := websocket.CloseStatus(err); got != websocket.StatusNormalClosure {
			t.Fatalf("归档关闭码 = %v, want %v（err=%v）",
				got, websocket.StatusNormalClosure, err)
		}
		return
	}
}

// waitWatchers 轮询等待订阅数达到期望值（订阅是异步建立的）。
func waitWatchers(t *testing.T, hub *Hub, taskID string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Watchers(taskID) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 watchers=%d 超时，当前 %d", want, hub.Watchers(taskID))
}
```

追加到 `internal/agentd/watchers_test.go`（Manager 侧的接线，与上面的 WS 收尾分开验——一个证明「关了订阅 WS 会正常收尾」，一个证明「done 真的会去关」）：

```go
// TestDoneClosesEventSubscriptions 验证 done 归档时关闭该任务的全部事件订阅。
//
// 为什么要单独验这条：WS 那侧只证明「订阅一关就正常收尾」，不证明 done 会去关。
// 少了这根接线，归档仍然对跟随端无声。
func TestDoneClosesEventSubscriptions(t *testing.T) {
	m, st, hub, _ := newTestManager(t)
	const id = "task-done-close"
	createRunningTask(t, st, id)
	if err := st.UpdateTaskState(id, proto.TaskStateWaitingReview); err != nil {
		t.Fatalf("推到 waiting_review: %v", err)
	}
	ch, cancel := hub.Subscribe(id)
	defer cancel()

	if err := m.Done(context.Background(), id); err != nil {
		t.Fatalf("Done: %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("归档后订阅通道仍在投递事件")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("归档未关闭订阅通道：跟随端拿不到「没有下文了」的信号")
	}
	if n := hub.Watchers(id); n != 0 {
		t.Errorf("归档后 Watchers = %d, want 0", n)
	}
}
```

本文件 import 需补 `"context"`。`store.UpdateTaskState`（`internal/store/store.go:321`）会校验迁移合法性，`createRunningTask` 落的是 `running`，`running → waiting_review` 合法，无需绕过它。

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/agentd/ -run 'CloseTask|OnArchive' -count=1
```

预期：`hub.CloseTask undefined`。

- [ ] **Step 3: 写最小实现**

`internal/agentd/hub.go`，`Watchers` 之后：

```go
// CloseTask 关闭该任务的全部事件订阅并从表中摘除。
//
// 参数：
//   - taskID: 已终结（归档）的任务 ID
//
// 返回：
//   - 被关闭的订阅数；无人订阅返回 0
//
// 为什么需要它：done 归档只改任务状态、不追加任何事件，事件流上完全无声。
// 跟随中的客户端（wait --follow）因此拿不到「没有下文了」的信号，会一直挂到
// 空闲超时——而那个超时的语义是「agentd 可能失联」，把一次正常归档报成了故障。
// 关闭订阅让 WS 处理器以正常关闭码收尾，客户端据此正常退出。
//
// 注意：
//   - 与 unsubscribe 共用同一把 mu，且 unsubscribe 以「通道是否还在表中」为准，
//     连接随后 defer cancel 时找不到自己的通道即静默返回，不存在二次 close
//   - 关闭后 Publish 该任务的事件是空操作（表里已无订阅者），不会向已关闭通道发送
func (h *Hub) CloseTask(taskID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	subs := h.subs[taskID]
	if len(subs) == 0 {
		return 0
	}
	for _, ch := range subs {
		close(ch)
	}
	delete(h.subs, taskID)
	h.log.Info("任务归档，关闭其全部事件订阅", "taskID", taskID, "closed", len(subs))
	return len(subs)
}
```

`internal/agentd/server.go` 的 `handleEvents`。① 变量块加 `archived`：

```go
	var (
		liveMu      sync.Mutex
		live        []proto.Event
		overflow    bool
		archived    bool // 订阅被 hub 关闭（任务归档），与「本连接自己结束」区分
		drainNotify = make(chan struct{}, 1)
	)
```

② 排空器的 `!ok` 分支：

```go
			case ev, ok := <-ch:
				if !ok {
					// 通道被关闭有两种可能：连接结束时 defer cancel 关的（此时主循环
					// 已在退出路上，下面这个标记没人读，无害），或 hub.CloseTask 关的
					//（任务归档）。后者必须让主循环知道，好以正常关闭码收尾
					liveMu.Lock()
					archived = true
					liveMu.Unlock()
					notifyDrain()
					return
				}
```

③ 实时写循环的 `drainNotify` 分支，在 `over` 判断之后补：

```go
				if arch {
					s.log.Info("任务已归档，以正常关闭码结束事件流", "task", taskID,
						"sent", sent, "last_written", lastWrittenSeq)
					if cerr := conn.Close(websocket.StatusNormalClosure, "task archived"); cerr != nil {
						// 对端可能已经走了；关闭码送不到不改变结论，如实记一笔即可
						s.log.Debug("WS 归档关闭失败", "task", taskID, "err", cerr)
					}
					return
				}
```

同时把该分支的快照取值改为一次性取三个：

```go
			liveMu.Lock()
			pending := live
			live = nil
			over := overflow
			arch := archived
			liveMu.Unlock()
```

**顺序硬约束**：先 `writeLiveBatch(pending)` 把归档前排队的事件写完，再判 `arch` 关闭。反过来会在归档瞬间吞掉最后一批事件。

`internal/agentd/manager.go` 的 `Done`，在 `m.clearApproverState(taskID)` 那行之后插入：

```go
	// 归档对事件流是无声的（transit 只改状态、不追加事件），跟随中的 wait --follow
	// 无从得知「没有下文了」。关掉订阅，让 WS 以正常关闭码收尾
	if n := m.hub.CloseTask(taskID); n > 0 {
		m.log.Info("done 关闭事件订阅", "task", taskID, "closed", n)
	}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/agentd/ -count=1 && go test -race ./internal/agentd/ -count=1
```

预期：全 PASS，`-race` 干净。

- [ ] **Step 5: 加关键节点日志**

已在 Step 3 内落位，逐条确认：
- `Hub.CloseTask` 成功路径一条 Info（`taskID` + `closed`）——**状态变更必须留痕**，且 0 订阅时不打，避免每次 done 都刷一条无信息量的行。
- `handleEvents` 归档收尾一条 Info（`task` + `sent` + `last_written`）：这是「这条连接总共送出去多少」的收口；关闭失败降 Debug（对端已走不是异常）。
- `Manager.Done` 一条 Info（`task` + `closed`）：把「归档时有几个人正在听」记进 agentd 的盘。

- [ ] **Step 6: 加注释**

- `Hub.CloseTask` 的完整 doc（Step 3 已含）。
- `archived` 变量的行内说明、排空器 `!ok` 分支的两种可能、`Done` 里的 why（Step 3 已含）。
- `handleEvents` 的函数头 doc 追加一条「注意」：

```go
//   - 任务归档（done）时 hub 会关闭本连接的订阅，此处以 StatusNormalClosure +
//     "task archived" 收尾。客户端据这个关闭码区分「归档」与「断线」——断线要
//     重连，归档要退出，两者搞混就是无限重连一个已经结束的任务
```

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
```

```bash
git add internal/agentd/ && git commit -m "feat(b56): done 归档时关闭事件订阅，WS 以正常关闭码收尾"
```

---

### Task 5: `client.FollowEvents` —— 持续订阅

**Files:**
- Modify: `internal/client/client.go:694-805`（`WaitEvent` / `waitOnce`，抽出 `streamOnce`；新增 `FollowEvents`）
- Test: `internal/client/follow_test.go`（新建）

**Interfaces:**
- Consumes: 服务端归档时的 `websocket.StatusNormalClosure`（Task 4）
- Produces:
  ```go
  var ErrIdleTimeout = errors.New("空闲超时：期间未收到任何帧")

  func (c *Client) FollowEvents(ctx context.Context, taskID string, all bool,
      idle time.Duration, onEvent func(*proto.Event) error) error
  ```
  返回 `nil` = 任务终结（`failed` 事件或归档关闭）；`ErrIdleTimeout` = 空闲到点；其余为永久失败/ctx 错误。Task 6 按这三类分退出码。

- [ ] **Step 1: 写失败的测试**

新建 `internal/client/follow_test.go`：

```go
// follow_test.go —— FollowEvents 的行为契约测试。
//
// 职责：钉住「持续交付」「空闲以任何帧为准」「终态识别」三条，用自控 WS 端点，
// 不起真 agentd——被测的是客户端这一侧的语义。
//
// 边界：不验重连退避（那由 ws_backoff_test 覆盖），不验 cursor 落盘细节。
package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// pushEvents 起一个把给定事件依次推给客户端的 WS 端点，推完按 after 收尾。
func pushEvents(t *testing.T, evs []proto.Event, after func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for _, ev := range evs {
			b, err := json.Marshal(ev)
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), websocket.MessageText, b); err != nil {
				return
			}
		}
		after(c)
	}))
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

// TestFollowDeliversEveryEvent 验证 follow 不在首个事件后返回：
// 三条可动作事件必须逐条交付，且 progress 被过滤掉不交付。
func TestFollowDeliversEveryEvent(t *testing.T) {
	evs := []proto.Event{
		{Seq: 1, TaskID: "t1", Type: proto.EventTypeQuestion},
		{Seq: 2, TaskID: "t1", Type: proto.EventTypeProgress},
		{Seq: 3, TaskID: "t1", Type: proto.EventTypeCompleted},
		{Seq: 4, TaskID: "t1", Type: proto.EventTypeStalled},
		{Seq: 5, TaskID: "t1", Type: proto.EventTypeFailed},
	}
	ts := pushEvents(t, evs, func(c *websocket.Conn) { <-make(chan struct{}) })

	var got []int64
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t1", false, 0,
		func(ev *proto.Event) error {
			got = append(got, ev.Seq)
			return nil
		})
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil（failed 事件是正常终结）", err)
	}
	want := []int64{1, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("交付 seq = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("交付 seq = %v, want %v", got, want)
		}
	}
}

// TestFollowDoesNotExitOnCompleted 验证 completed **不**终结跟随：
// 那只是一轮结束，continue 之后还有事件。
//
// 缺陷形态：把 completed 当终态会让 follow 在每轮结束时退出，真空原样回来。
func TestFollowDoesNotExitOnCompleted(t *testing.T) {
	evs := []proto.Event{
		{Seq: 1, TaskID: "t1", Type: proto.EventTypeCompleted},
		{Seq: 2, TaskID: "t1", Type: proto.EventTypeQuestion},
	}
	done := make(chan struct{})
	ts := pushEvents(t, evs, func(c *websocket.Conn) { <-done })
	t.Cleanup(func() { close(done) })

	seen := make(chan int64, 4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		_ = client.New(ts.URL, "").FollowEvents(ctx, "t1", false, 0,
			func(ev *proto.Event) error { seen <- ev.Seq; return nil })
	}()
	for _, want := range []int64{1, 2} {
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("交付 seq = %d, want %d", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("未等到 seq %d —— follow 可能在 completed 后就退出了", want)
		}
	}
}

// TestFollowIdleCountsProgressFrames 是 §2.2 的核心断言：
// **只有 progress 帧流入时不得触发空闲超时。**
//
// 缺陷形态：把空闲定义成「距上一次可交付事件」，一个健康的长跑任务（8f7a4f18
// 连跑 15 小时只有 progress）会每隔 --timeout 无故 124 一次，兜底变噪音。
func TestFollowIdleCountsProgressFrames(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		// 每 50ms 推一条 progress，持续到测试取消；空闲阈值设 300ms
		for i := 1; ; i++ {
			b, _ := json.Marshal(proto.Event{
				Seq: int64(i), TaskID: "t1", Type: proto.EventTypeProgress})
			if err := c.Write(r.Context(), websocket.MessageText, b); err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}))
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	err := client.New(ts.URL, "").FollowEvents(ctx, "t1", false,
		300*time.Millisecond, func(*proto.Event) error { return nil })
	if errors.Is(err, client.ErrIdleTimeout) {
		t.Fatal("只有 progress 流入时触发了空闲超时：空闲口径把过滤掉的帧漏算了")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FollowEvents = %v, want context.DeadlineExceeded（测试自己收尾）", err)
	}
}

// TestFollowIdleTimeoutWhenNoFrames 验证完全无帧时按空闲超时退出。
func TestFollowIdleTimeoutWhenNoFrames(t *testing.T) {
	ts := pushEvents(t, nil, func(c *websocket.Conn) { <-make(chan struct{}) })
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t1", false,
		300*time.Millisecond, func(*proto.Event) error { return nil })
	if !errors.Is(err, client.ErrIdleTimeout) {
		t.Fatalf("FollowEvents = %v, want ErrIdleTimeout", err)
	}
}

// TestFollowExitsOnArchive 验证服务端以正常关闭码收尾时 follow 正常退出（nil），
// 而不是把它当断线无限重连一个已经结束的任务。
func TestFollowExitsOnArchive(t *testing.T) {
	evs := []proto.Event{{Seq: 1, TaskID: "t1", Type: proto.EventTypeQuestion}}
	ts := pushEvents(t, evs, func(c *websocket.Conn) {
		_ = c.Close(websocket.StatusNormalClosure, "task archived")
	})
	n := 0
	err := client.New(ts.URL, "").FollowEvents(t.Context(), "t1", false, 0,
		func(*proto.Event) error { n++; return nil })
	if err != nil {
		t.Fatalf("FollowEvents = %v, want nil（归档是正常终结）", err)
	}
	if n != 1 {
		t.Fatalf("交付 %d 条, want 1", n)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/client/ -run 'TestFollow' -count=1
```

预期：`undefined: client.ErrIdleTimeout`、`cli.FollowEvents undefined`。

- [ ] **Step 3: 写最小实现**

`internal/client/client.go`。① 包级哨兵（放在既有错误定义附近）：

```go
// ErrIdleTimeout 表示 follow 期间空闲超过约定时长——期间**一帧都没收到**
//（含被过滤掉的 progress）。
//
// 为什么它值得一个独立哨兵：它与「任务停滞」不是一回事。任务停滞由 agentd 的
// 看门狗诊断并作为 stalled 事件送达（带 last_seq 与 idle 时长）；本错误只说明
// 连接侧一片死寂，第一嫌疑是 agentd 失联而不是任务卡住。
var ErrIdleTimeout = errors.New("空闲超时：期间未收到任何帧")

// errStopStream 是 streamOnce 的内部哨兵：onFrame 返回它表示「本次连接的使命
// 已完成」，按正常结束处理而非错误（一次性 wait 用它在首个可动作事件后收手）。
var errStopStream = errors.New("stream stopped by callback")

// errArchived 是内部哨兵：对端以 StatusNormalClosure 关闭，表示任务已归档。
var errArchived = errors.New("任务已归档")
```

② 把 `waitOnce` 的连接+读循环抽成 `streamOnce`（**拨号段一字不改地搬过去**）：

```go
// streamOnce 建立一次 WS 连接并把收到的每一帧交给 onFrame，直到 onFrame 收手或连接结束。
//
// 参数：
//   - fromSeq: 断线续拉起点（服务端据此补发历史事件）
//   - readDeadline: 每次 Read 前调用一次，返回本次读取的**绝对**截止时刻；
//     返回零值表示不设。为什么是绝对时刻而不是时长：空闲要跨重连累计，
//     每次连接都从头计时等于让一个反复断连的对端永远超不了时
//   - onFrame: 每收到一帧调用一次（**含 progress**，过滤由调用方做）
//
// 返回：
//   - nil: onFrame 返回 errStopStream
//   - errArchived: 对端以 StatusNormalClosure 关闭（任务已归档）
//   - ErrIdleTimeout: 读取超过 readDeadline 且外层 ctx 未取消
//   - permanentError / 其他: 拨号或读取失败，由调用方决定是否重连
func (c *Client) streamOnce(ctx context.Context, taskID string, fromSeq int64,
	readDeadline func() time.Time, onFrame func(proto.Event) error) error {
	// ——以下拨号段与原 waitOnce 完全一致（scheme 换算、Bearer 头、dialTimeout、
	//    永久状态码判定、连接建立/关闭日志），照搬不改——
	// ...

	for {
		readCtx, cancelRead := ctx, context.CancelFunc(func() {})
		if readDeadline != nil {
			if dl := readDeadline(); !dl.IsZero() {
				readCtx, cancelRead = context.WithDeadline(ctx, dl)
			}
		}
		_, b, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			// 顺序要紧：先分辨「是我们自己设的空闲期限到了」（外层 ctx 仍活着），
			// 再分辨归档，最后才当普通断线交给外层重连
			if ctx.Err() == nil && readCtx.Err() != nil {
				return ErrIdleTimeout
			}
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return errArchived
			}
			return fmt.Errorf("WS 读取: %w", err)
		}
		var ev proto.Event
		if err := json.Unmarshal(b, &ev); err != nil {
			// 服务端推了非事件 JSON：按连接异常处理，交给外层重连（数据已由 store 兜底）
			return fmt.Errorf("WS 事件反序列化: %w", err)
		}
		if err := onFrame(ev); err != nil {
			if errors.Is(err, errStopStream) {
				return nil
			}
			return err
		}
	}
}
```

③ `waitOnce` 改写成 `streamOnce` 的薄封装（**外部行为一字不变**）：

```go
func (c *Client) waitOnce(ctx context.Context, taskID string, fromSeq int64, all bool) (*proto.Event, error) {
	var got *proto.Event
	err := c.streamOnce(ctx, taskID, fromSeq, nil, func(ev proto.Event) error {
		if !all && ev.Type == proto.EventTypeProgress {
			return nil // progress 不唤醒（why 见 WaitEvent doc 注释）
		}
		c.log().Info("wait 事件返回", "task", taskID, "seq", ev.Seq, "type", ev.Type)
		got = &ev
		return errStopStream
	})
	if err != nil {
		return nil, err
	}
	return got, nil
}
```

**一处必须核对的行为等价性**：正常关闭码现在返回 `errArchived` 而不是原来的 `"WS 读取"` 包装错误。`isPermanent(errArchived)` 必须为 **false**，否则一次性 `wait` 遇到对端正常关闭会不再重连——那是行为改变。实现后跑 `go test ./internal/client/ -count=1`，`ws_backoff_test.go` 里对端正是用 `StatusNormalClosure` 收尾，它绿就证明等价性成立。

④ 新增 `FollowEvents`：

```go
// FollowEvents 持续订阅任务事件流，逐条交给 onEvent，直到任务终结或出错。
//
// 与 WaitEvent 的区别只有一条：不在首个事件后返回。这条区别是本设计的全部理由
// ——一事件一退出意味着每两个事件之间必然有一段无人订阅的真空，而「回合结束后
// 记得重挂」是需要每轮重做的人工动作，漏一次即永久断链。
//
// 参数：
//   - all: false 时过滤 progress（与 WaitEvent 同义）
//   - idle: 空闲上限，0 表示不设。**空闲以「收到任何帧」为准，包含被过滤掉的
//     progress**——一个健康的长跑任务可以数小时只有 progress，用可交付事件计时
//     会让它周期性无故超时。这个计时跨重连累计
//   - onEvent: 每条**可交付**事件调用一次；返回非 nil 立即终止跟随并原样返回该错误
//
// 返回：
//   - nil: 任务终结（收到 failed 事件，或对端归档关闭连接）
//   - ErrIdleTimeout: 空闲超过 idle
//   - ctx.Err() / 永久失败（401、任务不存在）: 原样返回
//
// cursor 语义（与 WaitEvent 的差别，取舍已在 spec §2.4 记录并接受）：
//   - cursor 仍只在**交付**事件时推进，但「交付」不再等价于「审核者看过了」
//     ——事件可能在审核者正忙时流入。此刻会话若崩溃，该事件不会再重放
//   - 接受这个回退的理由：事件流本就不是权威，工单在 agentd 侧持久，
//     pending_tickets 才是权威清单。醒来先 show 这条纪律因此从建议变成必须
//   - 断线续拉起点（fromSeq）则按**任何帧**推进：已经收到的帧没有再补发的必要，
//     它与 cursor 的分叉是有意的，且分叉方向安全（cursor 永远更保守）
func (c *Client) FollowEvents(ctx context.Context, taskID string, all bool,
	idle time.Duration, onEvent func(*proto.Event) error) error {
	fromSeq := c.readCursor(taskID)
	lastFrame := time.Now()
	// readDeadline 与 onFrame 都只在 streamOnce 的读循环里被同一个 goroutine 调用，
	// lastFrame 无需加锁
	readDeadline := func() time.Time {
		if idle <= 0 {
			return time.Time{}
		}
		return lastFrame.Add(idle)
	}

	c.log().Info("follow 开始", "addr", c.baseURL, "task", taskID,
		"from_seq", fromSeq, "idle", idle.String())

	backoff := c.wsInitialBackoff
	for attempt := 1; ; attempt++ {
		start := time.Now()
		err := c.streamOnce(ctx, taskID, fromSeq, readDeadline, func(ev proto.Event) error {
			lastFrame = time.Now()
			fromSeq = ev.Seq
			if !all && ev.Type == proto.EventTypeProgress {
				return nil
			}
			if werr := c.writeCursor(taskID, ev.Seq); werr != nil {
				// cursor 写失败不吞事件：先把事件交给审核者（宁可下次重投，不可这次丢）
				c.log().Warn("cursor 写盘失败", "task", taskID, "seq", ev.Seq, "cause", werr)
			}
			c.log().Info("follow 事件交付", "task", taskID, "seq", ev.Seq, "type", ev.Type)
			if err := onEvent(&ev); err != nil {
				return err
			}
			if ev.Type == proto.EventTypeFailed {
				// failed 是任务终态；completed 不是——那只是一轮结束，continue 之后还有事件
				c.log().Info("follow 结束：任务已失败", "task", taskID, "seq", ev.Seq)
				return errStopStream
			}
			return nil
		})
		lived := time.Since(start)

		switch {
		case err == nil:
			return nil // onEvent 侧收手（failed）
		case errors.Is(err, errArchived):
			c.log().Info("follow 结束：任务已归档", "task", taskID)
			return nil
		case errors.Is(err, ErrIdleTimeout):
			c.log().Error("follow 空闲超时", "addr", c.baseURL, "task", taskID,
				"idle", idle.String(), "last_frame", lastFrame.Format(time.RFC3339))
			return err
		case ctx.Err() != nil:
			return ctx.Err()
		case isPermanent(err):
			c.log().Error("follow 永久失败，不再重试", "addr", c.baseURL, "task", taskID, "cause", err)
			return err
		}

		// 断线：与 WaitEvent 同一套退避（复位判据是「连接活够 wsStableAfter」，why 见那里）
		c.log().Info("follow 连接断开，等待后重连", "addr", c.baseURL, "task", taskID,
			"attempt", attempt, "next_backoff_seconds", int(backoff.Seconds()), "cause", err)
		if idle > 0 && time.Since(lastFrame) >= idle {
			// 重连期间同样在空闲：不检查这里，一个反复拒连的对端会让 follow 永远超不了时
			c.log().Error("follow 空闲超时（重连期间）", "task", taskID, "idle", idle.String())
			return ErrIdleTimeout
		}
		if lived >= c.wsStableAfter {
			backoff = c.wsInitialBackoff
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if lived < c.wsStableAfter {
			backoff *= 2
			if backoff > c.wsMaxBackoff {
				backoff = c.wsMaxBackoff
			}
		}
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/client/ -count=1 && go test -race ./internal/client/ -count=1
```

预期：新增 5 条 `TestFollow*` 全 PASS，且 **`ws_backoff_test.go` 与既有全部 client 测试保持绿**（这是 `waitOnce` 重构未改变行为的证据）。

- [ ] **Step 5: 加关键节点日志**

已在 Step 3 内落位，逐条确认（**这些日志是「为什么没唤醒」的唯一线索点**）：
- 进入 `FollowEvents`：`"follow 开始"`，带 `addr` / `task` / `from_seq` / `idle`。
- 每条交付：`"follow 事件交付"`，带 `seq` / `type`。
- 三条终结路径各一条：失败终态（Info）、归档（Info）、空闲超时（Error，带 `last_frame` 时刻——它直接回答「最后一次听到动静是什么时候」）。
- 断线重连：`"follow 连接断开，等待后重连"`，带 `attempt` / `next_backoff_seconds` / `cause`。
- 永久失败：Error，带 `cause`。
- **不打**的：过滤掉的 progress 帧（高频，会淹掉真线索；它的存在已通过「没有触发空闲超时」间接可见）。

- [ ] **Step 6: 加注释**

- `ErrIdleTimeout` / `errStopStream` / `errArchived` 三个哨兵各自的 why（Step 3 已含）。
- `streamOnce` 的完整 doc，重点是 `readDeadline` 为什么用绝对时刻（Step 3 已含）。
- `FollowEvents` 的完整 doc，含 cursor 语义那一段取舍（Step 3 已含）。
- `waitOnce` 保留原 doc，并补一句：

```go
// 注意：
//   - 本函数现在是 streamOnce 的薄封装，外部行为与重构前逐字节一致；
//     正常关闭码在 streamOnce 里被识别为 errArchived，它不是永久失败，
//     WaitEvent 仍按断线重连处理（ws_backoff_test 钉住了这条等价性）
```

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
```

```bash
git add internal/client/ && git commit -m "feat(b56): client 增加 FollowEvents，一次订阅持续交付"
```

---

### Task 6: `handoff wait --follow`

**Files:**
- Modify: `cmd/wait.go`（`--follow` 开关、follow 分支、空闲超时与 stalltimeout 的 WARN、退出码）
- Test: `cmd/wait_test.go`（新建）

**Interfaces:**
- Consumes: `client.FollowEvents` / `client.ErrIdleTimeout`（Task 5）、`proto.StatusResp.StallTimeout`（Task 3）
- Produces: CLI 契约 `handoff wait --follow <完整 task-id> [--timeout 3h]`，退出码 0 / `ExitTimeout` / 其他非 0

- [ ] **Step 1: 写失败的测试**

新建 `cmd/wait_test.go`：

```go
// wait_test.go —— wait --follow 的 CLI 层契约测试。
//
// 职责：钉住空闲超时与 stalltimeout 的告警判据（纯函数），以及 --timeout
// 负值仍被拒绝这条既有行为在 follow 下不退化。
//
// 边界：事件交付/终态识别在 internal/client 的 follow_test 里验，这里不重复。
package cmd

import (
	"strings"
	"testing"
	"time"
)

// TestIdleTimeoutWarning 钉死 §2.2 的硬约束：--timeout 必须大于 stalltimeout。
//
// 为什么：两者都在测「多久没动静」，但 stalled 是 agentd 带着 last_seq 和 idle
// 时长给出的**诊断**，124 只是客户端说「我没收到东西」。同时到点时 124 会抢先
// 退出进程，把一次已诊断清楚的停滞降级成一次连接超时——信息严格更少。
func TestIdleTimeoutWarning(t *testing.T) {
	cases := []struct {
		name      string
		idle      time.Duration
		stall     time.Duration
		wantWarn  bool
	}{
		{"小于 stalltimeout：告警", time.Hour, 2 * time.Hour, true},
		{"等于 stalltimeout：告警（同时到点 124 会抢先）", 2 * time.Hour, 2 * time.Hour, true},
		{"大于 stalltimeout：不告警", 3 * time.Hour, 2 * time.Hour, false},
		{"未设 --timeout：不告警", 0, 2 * time.Hour, false},
		{"对端未提供 stalltimeout：不告警（不拿未知当结论）", time.Hour, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := idleTimeoutWarning(c.idle, c.stall)
			if (got != "") != c.wantWarn {
				t.Fatalf("idleTimeoutWarning(%s, %s) = %q, wantWarn=%v",
					c.idle, c.stall, got, c.wantWarn)
			}
			if c.wantWarn && !strings.Contains(got, c.stall.String()) {
				t.Errorf("告警未点名对端的 stalltimeout: %q", got)
			}
		})
	}
}

// TestFollowRejectsNegativeTimeout 验证 --follow 下负时长同样被拒绝，
// 不因为走了新分支就把这条既有防线漏掉。
func TestFollowRejectsNegativeTimeout(t *testing.T) {
	t.Cleanup(func() { followFlag = false; waitTimeout = 0 })
	followFlag = true
	waitTimeout = -5 * time.Second
	err := waitCmd.RunE(waitCmd, []string{"任意-id"})
	if err == nil || !strings.Contains(err.Error(), "--timeout") {
		t.Fatalf("负时长应被拒绝并点名 --timeout，实际: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./cmd/ -run 'TestIdleTimeoutWarning|TestFollowRejects' -count=1
```

预期：`undefined: idleTimeoutWarning`、`undefined: followFlag`。

- [ ] **Step 3: 写最小实现**

`cmd/wait.go`。① 新增开关：

```go
// followFlag 为 true 时持续订阅：事件逐行流出，不在首个事件后退出。
//
// 为什么需要它：一次性 wait 的「一事件一退出」让每两个事件之间必然存在一段
// 无人订阅的真空，而「回合结束后记得重挂」是要每轮重做的人工动作——漏一次
// 就是永久断链（08-11 实撞：f7d07ece 的 wait 退出后空转 7 小时 43 分）。
var followFlag bool
```

② `RunE` 改为按分支走（**一次性路径一字不改**）：

```go
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		if waitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", waitTimeout)
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		slog.SetDefault(logx.Setup("cli", ""))
		if followFlag {
			return runFollow(cmd, taskID, addr, token)
		}
		// ——以下一次性路径与改动前完全一致——
		ctx := cmd.Context()
		if waitTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, waitTimeout)
			defer cancel()
		}
		// ...（原样保留）
	},
```

**关键**：follow 分支**不得**把 `waitTimeout` 套成 ctx deadline——那会让它变回「总时长上限」，而契约是空闲超时。

③ follow 主体：

```go
// runFollow 持续订阅任务事件流，每条事件单行输出到 stdout，直到任务终结。
//
// 参数：
//   - taskID: 完整 UUID（agentd 精确匹配，不做前缀补全）
//
// 返回：
//   - nil: 任务终结（failed 或已归档），退出 0
//   - ExitTimeout 的 exitCodeError: 空闲超过 --timeout
//   - 其他错误: 鉴权失败 / 任务不存在 / 连接永久失败
//
// 注意：
//   - stdout 严格是「每事件一行 JSON」，任何人读信息一律走 stderr——上层
//     （Monitor）按行解析，多打一行说明就会打断它
func runFollow(cmd *cobra.Command, taskID, addr, token string) error {
	cli := client.New(addr, token)
	// 异步核对 --timeout 与对端 stalltimeout：status 要逐个探活，最坏 10 秒，
	// 不能让一句告警把开始跟随这件事拖后
	go warnIfTimeoutBelowStall(cmd.Context(), cli, waitTimeout)

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
		})
	switch {
	case err == nil:
		slog.Info("follow 正常结束：任务已终结", "task", taskID)
		return nil
	case errors.Is(err, client.ErrIdleTimeout):
		slog.Error("follow 空闲超时：期间未收到任何帧（含 progress）",
			"task", taskID, "timeout", waitTimeout.String())
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf(
			"follow 空闲超时（%s）：期间一帧都没收到。agentd 的 stalled 本应先到，"+
				"先跑 handoff show 看任务状态，再怀疑 agentd 是否失联", waitTimeout)}
	default:
		return err
	}
}

// warnIfTimeoutBelowStall 核对本次 --timeout 是否会抢在 agentd 的 stalled 前面，
// 是则打一条 WARN。
//
// 注意：
//   - 全部失败路径静默（Debug）：这是锦上添花的提醒，取不到对端状态不该影响跟随
//   - 单独设 15 秒时限：status 端要逐个探活，不能挂在这里
func warnIfTimeoutBelowStall(ctx context.Context, cli *client.Client, idle time.Duration) {
	if idle <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st, err := cli.Status(ctx)
	if err != nil {
		slog.Debug("取不到对端 stalltimeout，跳过 --timeout 核对", "cause", err)
		return
	}
	stall, err := time.ParseDuration(st.StallTimeout)
	if err != nil {
		slog.Debug("对端 stalltimeout 无法解析，跳过核对", "raw", st.StallTimeout, "cause", err)
		return
	}
	if msg := idleTimeoutWarning(idle, stall); msg != "" {
		slog.Warn(msg)
	}
}

// idleTimeoutWarning 判断 follow 的空闲超时是否会盖过 agentd 的停滞诊断。
//
// 参数：
//   - idle: 本次 --timeout；<=0 表示不设上限
//   - stall: 对端 agentd 的 stalltimeout；<=0 表示未知
//
// 返回：
//   - 需要告警时返回完整告警文案，否则返回空串
//
// 为什么「相等」也要告警：两个计时器同时到点时，客户端的 124 会抢在 agentd 的
// stalled 事件前面退出进程——而 stalled 是带着 last_seq 与 idle 时长的诊断，
// 124 只是一句「我没收到东西」。让前者盖住后者，等于主动把信息量调低。
func idleTimeoutWarning(idle, stall time.Duration) string {
	if idle <= 0 || stall <= 0 || idle > stall {
		return ""
	}
	return fmt.Sprintf(
		"--timeout %s 不大于对端 stalltimeout %s：两者同时到点时空闲超时会抢在 "+
			"agentd 的 stalled 诊断前面退出，把一次已诊断的任务停滞降级成一次连接超时。"+
			"建议设为大于 %s（如 %s）", idle, stall, stall, stall+time.Hour)
}
```

④ 注册开关，并更新 `--timeout` 的帮助文案：

```go
	waitCmd.Flags().BoolVar(&followFlag, "follow", false,
		"持续订阅：每条事件单行输出，任务终结（failed/归档）才退出")
	waitCmd.Flags().DurationVar(&waitTimeout, "timeout", 0,
		"超时（如 3h）；默认不设上限。一次性模式=等不到事件的总时长上限，"+
			"--follow 模式=空闲上限（期间一帧都没收到，含 progress），到点退出非 0")
```

⑤ 文件头注释补 `--follow`（见 Step 6）。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./cmd/ -count=1 && go test -race ./cmd/ -count=1
```

预期：全 PASS。

- [ ] **Step 5: 加关键节点日志**

已在 Step 3 内落位，逐条确认：
- 两条终结路径：正常结束 Info（带 `task`）、空闲超时 Error（带 `task` + `timeout`）。**成功路径也要打**——静默成功是「到底跑没跑」这类猜谜的根源。
- WARN 核对的两条失败路径均为 Debug（取不到对端状态不是异常，不该污染 stderr）。
- 事件级日志由 `client.FollowEvents` 承担（Task 5），本层不重复打，否则同一条事件在 stderr 上出现两遍——B51 修的正是这个形态。

- [ ] **Step 6: 加注释**

`cmd/wait.go` 文件头的「职责」块追加一条：

```go
//   - --follow：持续订阅同一任务的事件流，每条事件单行输出，直到任务终结
//     （failed 事件或被 done 归档）。此模式下 --timeout 的语义是**空闲**上限
//     ——距上一次收到任何帧（含被过滤掉的 progress）的时长，且跨重连累计
```

并追加一条「边界」：

```go
//   - 不覆盖「审核者会话被关闭」：Monitor 是会话级的，会话没了订阅就没了，
//     本命令给不出任何补救（spec §7.2 明确接受的边界）
```

其余（`followFlag` / `runFollow` / `warnIfTimeoutBelowStall` / `idleTimeoutWarning`）的 doc 已在 Step 3 内。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
```

```bash
git add cmd/wait.go cmd/wait_test.go && git commit -m "feat(b56): wait --follow 持续订阅，--timeout 改为空闲上限"
```

---

### Task 7: 改写审核者纪律（skill）与 README

**Files:**
- Modify: `skills/handoff/SKILL.md`（回合形态一节整体替换；红旗表加两条；新会话接管一节）
- Modify: `README.md`（`wait` 用法与退出码语义）

**Interfaces:**
- Consumes: Task 6 交付的 CLI 契约（`--follow`、退出码）、Task 3 交付的 `watchers` 展示
- Produces: 无代码接口

**为什么这一步不能省**：`skills/handoff/SKILL.md` 现在**明文规定**旧形态（「每一轮 = 一条后台命令，内容就是一条裸的 wait」「处理完必须重新挂」）。不改它，下一个会话照旧按旧纪律办事，前六个 task 全部白做。

- [ ] **Step 1: 先读现状，定位要替换的段落**

```bash
grep -n "裸的 wait\|重新挂\|重挂\|124\|pending_tickets" skills/handoff/SKILL.md
```

把命中的行号记下来——下面每一处都要处理，漏一处就会留下一条自相矛盾的纪律。

- [ ] **Step 2: 替换回合形态**

把「每一轮 = 一条后台命令 / 处理完必须重新挂」整段替换为：

```markdown
### 订阅：开一次，活到会话结束

    Monitor({
      command: "handoff wait --follow <完整 task-id> --timeout 3h",
      description: "handoff <任务名> 事件流",
      persistent: true
    })

事件作为通知逐条流入本会话，**没有「重挂」这个动作**。

- `--timeout` 是**空闲**上限（距上一次收到任何帧，含不唤醒的 progress），
  必须**大于**对端 agentd 的 stalltimeout（默认 2h），故取 3h。设小了，
  客户端的超时会抢在 agentd 的 stalled 诊断前面退出——把一条带 last_seq 的
  诊断换成一句「我没收到东西」。
- **follow 进程退出本身就是信号**，必须看退出码：
  - `0`：任务已终结（failed 事件或被 done 归档）→ 进入终态处置
  - `124`：空闲 3 小时一帧都没收到 → **可疑**。正常情况下 agentd 的 stalled
    会先到；先 `handoff show`，再怀疑 agentd 失联
  - 其他非 0：鉴权失败 / 任务不存在 / 连接永久失败 → 看 stderr 按排障表办，
    **不要盲目重开**（401、404 重开一百次还是同样的结果）
```

- [ ] **Step 3: 替换「醒来之后」与「新会话接管」两处纪律**

醒来之后：

```markdown
醒来 → `handoff show <完整 task-id>` → 处置。**没有重挂这一步。**

`show` 是权威，事件只是唤醒信号——`--follow` 下这条比以前更要紧：事件可能在
你正忙时流入，cursor 已经推进而你还没看。**任何处置前先 show，以 `state` +
`pending_tickets` 为准。**
```

新会话接管：

```markdown
接管一个已有的现场：

1. `handoff tasks` —— 每行任务 JSON 现在带 `watchers`（有几个连接在听）
2. 给每个 `watchers == 0` 的**活跃**任务（`pending` / `running` /
   `waiting_answer`）补开一条 follow Monitor。`waiting_review` 不用补：
   它在等你裁决，挂几天都正常
3. `handoff show` 逐个清 `pending_tickets`

`handoff status` 会把同一结论直接标在活跃任务行上：`⚠ 无人值守`。
```

- [ ] **Step 4: 红旗表加两条**

```markdown
| 「Monitor 退出了，再开一个就行」 | 先看退出码。401 / 404 重开一百次也是同样的结果 |
| 「事件流进来了，直接按它处置」 | 事件是唤醒信号，`show` 是权威。`--follow` 下 cursor 会跑在「已读」前面，这条比以前更要紧 |
```

- [ ] **Step 5: 更新 README**

在 `wait` 的用法处补 `--follow` 与退出码表：

```markdown
    # 一次性：等到下一个可动作事件就退出（派发后等第一个事件适用）
    handoff wait <完整 task-id>

    # 持续订阅：每条事件单行输出，任务终结才退出（审核回路的常规形态）
    handoff wait --follow <完整 task-id> --timeout 3h

`--timeout` 在两种模式下语义不同：一次性模式是「等不到事件的总时长上限」，
`--follow` 模式是「空闲上限」——距上一次收到任何帧（含不唤醒的 progress）的
时长，跨重连累计。**`--follow` 下它必须大于 agentd 的 `stalltimeout`（默认 2h）**，
否则客户端超时会抢在服务端的停滞诊断前面退出；设小了 handoff 会打一条 WARN。

| 退出码 | 含义 |
|--------|------|
| 0 | 一次性：等到事件；`--follow`：任务已终结（failed 或被归档） |
| 124 | 超时（一次性：总时长；`--follow`：空闲） |
| 其他非 0 | 鉴权失败 / 任务不存在 / 连接永久失败 |
```

- [ ] **Step 6: 自查一致性**

```bash
grep -n "重新挂\|重挂\|裸的 wait" skills/handoff/SKILL.md README.md
```

预期：**无输出**。有输出即说明还有一处旧纪律没改，回 Step 2。

- [ ] **Step 7: 提交**

```bash
git add skills/handoff/SKILL.md README.md && git commit -m "docs(b56): 审核者纪律改为持续订阅，删除重挂动作"
```

---

## 收尾验收（全部 task 完成后）

- [ ] **公共闸门**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1 && go test -race ./internal/agentd/ ./internal/client/ ./cmd/ -count=1
```

- [ ] **跨平台构建**（沿用全仓惯例）

```bash
GOOS=windows GOARCH=amd64 go build ./...
```

- [ ] **变异检查**（不采信「测试写了就有用」）

逐条注入、确认对应测试翻红、恢复后转绿：

| 注入 | 应翻红的测试 |
|------|------------|
| `Hub.Watchers` 恒返回 0 | `TestWatchersCountsSubscribers` |
| `FollowEvents` 的空闲计时改为「只在可交付事件时重置 lastFrame」 | `TestFollowIdleCountsProgressFrames` |
| `FollowEvents` 把 `completed` 也当终态 | `TestFollowDoesNotExitOnCompleted` |
| `unattended` 去掉 `waiting_review` 的排除 | `TestUnattendedJudgement` / `TestRenderStatusMarksUnattended` |
| `Manager.Done` 里的 `hub.CloseTask` 删掉 | `TestWSClosesNormallyOnArchive` |

每轮恢复后 `git diff --exit-code` 必须退出 0。

- [ ] **真机验收**（devbox，用隔离实例，勿动 7777 那个生产 agentd）

1. 派一个真任务，全程挂 `handoff wait --follow <id> --timeout 3h`（Monitor `persistent: true`）
2. `handoff tasks` / `handoff status` 观察 `watchers` 走完 **0 → 1 → 0**，且
   `⚠ 无人值守` 只在 0 且状态属于三态之一时出现、`waiting_review` 期间不出现
3. 跨至少两个回合（`continue` 一次）：确认 follow **没有**在第一个 `completed`
   处退出，第二轮事件继续流入同一条订阅
4. `handoff done` 归档：确认 follow 进程退出码为 **0**（不是挂到超时）
5. spec §9 的两个待观察项，本轮一并记录结论：Monitor 是否触发高频自动停止（#1）、
   同时开 5 条 Monitor 是否被限流（#2）——把观察到的实际现象回填进 spec §9

- [ ] **回填 backlog**

按 `product-backlog` 的证据门把 B56 从 `🔨 doing` 推到 `✅ done(已验)` 或
`✅ done(未验)`，`验收` 列写真实命令与结果，`原型/流程图` 为 `—` 自动免除对照。

---

## 自查记录（写完计划后按 writing-plans 的 Self-Review 跑过一遍）

**Spec 覆盖：** §2.1 契约 → Task 5+6；§2.2 空闲口径与 stalltimeout 硬约束 →
Task 3（外露）+ Task 5（口径）+ Task 6（WARN）；§2.3 退出码 → Task 6；§2.4 cursor
取舍 → Task 5 的 `FollowEvents` doc 明写；§3.1 `Watchers` → Task 1；§3.2 `TaskView`
→ Task 2；§3.3 判据 → Task 3 的 `unattended`；§3.4 展示 → Task 2（tasks）+ Task 3
（status）；§4 skill 改写 → Task 7；§5 错误语义 → Task 5 的四条 switch 分支；
§6 测试矩阵 → 各 task 的 Step 1 + 收尾的变异检查与真机验收；§8 交付物表八个文件
全部有归属。

**§9 三个空白的处置：** #1（Monitor 自动停止阈值）与 #2（并发条数上限）在真机验收
第 5 项观察并回填；**#3（归档时 follow 如何得知）在写计划时查清了**——`Manager.Done`
走 `transit`，而 `transit` 只改状态不追加事件，归档在事件流上完全无声，现有
`done` 路径**不关** WS 连接。故本计划把它从「待确认」升级为一个完整的 Task 4，
并选了「服务端关订阅 + 正常关闭码」而不是「补发一条归档事件」——后者要动事件
类型枚举与落库，代价大得多，且会让一个纯粹的状态变更混进事件流。
