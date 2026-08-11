# opencode 子会话审批归属（B52）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 opencode 的 subagent 发起的权限请求正确归属回父任务，产出工单并把审核者的裁决送回子会话，任务不再静默挂死。

**Architecture:** 在 opencode 适配层内部做归属判定——收到陌生会话的 `permission.asked` 时调 `GET /session/{id}` 取 `parentID`，等于本任务会话即认亲；认亲结果缓存在 `runState` 的两张内存表里，一张供入向过滤与工单标注，一张供出向应答选对会话。包外零改动。

**Tech Stack:** Go；`net/http` + `encoding/json`（现有 `API.do` 路径）；`log/slog`（`a.log` / `a.log()`）；标准库 `testing` + `httptest`（现有 `newFakeServer` / `startFakeRun` 脚手架）。

**Spec:** [docs/superpowers/specs/2026-08-11-opencode-subagent-permission-design.md](../specs/2026-08-11-opencode-subagent-permission-design.md)

## Global Constraints

- 改动只允许落在 `internal/executor/opencode/` 包内。`internal/executor/executor.go` 的 `AdapterEvent`、manager、store、CLI 一律不改。
- **不得取消或放宽会话校验**。校验防的是跨任务串台，正确解法是把子会话归属回父任务。
- 日志一律用 `a.log`（adapter）/ `a.log()`（api），**禁止** `fmt.Printf`。
- 新增导出方法必须有参数/返回/注意事项的文档注释；非显然分支必须有中文「为什么」注释。
- 归属判定**不向上递归**：opencode 自己给 subagent 下了 `permission:[{task,*,deny}]`，嵌套深度恒为 1。
- **只缓存认亲成功的结果**，失败结果不入缓存。
- **不得持 `sessMu` 跨 HTTP 调用**。锁序固定 `turnMu → sessMu`，不得反向。
- 认亲失败一律 fail-closed 且不静默：Warn + 一条 `progress` 事件后丢弃。
- 新超时常量 `ownershipTimeout = 5 * time.Second`，不复用 `unaryTimeout`（30s）。

---

### Task 1: API 层取会话详情

**Files:**
- Modify: `internal/executor/opencode/api.go`
- Test: `internal/executor/opencode/api_test.go`

**Interfaces:**
- Consumes: 现有 `(*API).do`、`(*API).httpError`、`(*API).log`
- Produces: `ownershipTimeout` 常量；`sessionDetail{ID, ParentID, Title string}`（包内类型）；`(*API).GetSession(ctx context.Context, sessionID string) (sessionDetail, error)`

- [ ] **Step 1: 写失败的测试**

在 `internal/executor/opencode/api_test.go` 末尾追加。注意该测试文件是**外部测试包**（`package opencode_test`），`sessionDetail` 未导出，所以只断言 `GetSession` 的可观察行为——通过导出方法拿到的值。若 `GetSession` 返回未导出类型导致外部包无法声明变量，用 `:=` 接收即可（Go 允许使用未导出类型的值，只是不能显式写类型名）。

```go
func TestGetSessionParsesParentAndTitle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/session/ses_child" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"id":"ses_child","parentID":"ses_parent","title":"Run probe curl command (@general subagent)"}`)
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	d, err := api.GetSession(context.Background(), "ses_child")
	if err != nil {
		t.Fatalf("GetSession 失败: %v", err)
	}
	if d.ParentID != "ses_parent" {
		t.Fatalf("parentID=%q，期望 ses_parent", d.ParentID)
	}
	if d.Title != "Run probe curl command (@general subagent)" {
		t.Fatalf("title=%q，期望带 subagent 标记的标题", d.Title)
	}
}

func TestGetSessionEmptyIDDoesNotHitServer(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	if _, err := api.GetSession(context.Background(), ""); err == nil {
		t.Fatal("空会话 id 应当直接报错")
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("空会话 id 不应触达服务端，实际请求 %d 次", n)
	}
}

func TestGetSessionNon2xxReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer ts.Close()

	api := opencode.NewAPI(ts.URL, testPassword)
	if _, err := api.GetSession(context.Background(), "ses_child"); err == nil {
		t.Fatal("非 2xx 应当返回错误")
	}
}
```

若 `api_test.go` 尚未 import `sync/atomic`，加上。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/executor/opencode/ -run TestGetSession -v`
Expected: 编译失败，`api.GetSession undefined`

- [ ] **Step 3: 加超时常量**

在 `api.go` 的 const 块里（`unaryTimeout` 之后）加：

```go
	// ownershipTimeout 是子会话归属判定（GetSession）的超时上限。
	// 为什么不用 unaryTimeout（30s）：本调用在 SSE 事件回调里同步执行，会阻塞
	// 本任务的事件流。30s 是按「一次人工应答的最长合理等待」定的，用在热路径上
	// 太长；5s 足够一次本机 HTTP 往返（serve 就在 127.0.0.1 上），且此刻任务
	// 本来就在等这个审批，短暂阻塞不额外损失什么。
	ownershipTimeout = 5 * time.Second
```

- [ ] **Step 4: 加响应体类型与方法**

在 `api.go` 的 `sessionListItem` 定义之后加：

```go
// sessionDetail 是 GET /session/{id} 的响应体形状。
//
// 只取三个字段：id 供自校验，parentID 是把子会话归属回父任务的唯一依据，
// title 供工单标注「这条审批来自哪个子 agent」。响应体里还有 directory /
// agent / permission 等字段，本层用不上就不入结构——多解析一个字段就多一处
// 会随 opencode 版本漂移的耦合面。
type sessionDetail struct {
	ID       string `json:"id"`
	ParentID string `json:"parentID"`
	Title    string `json:"title"`
}

// GetSession 取单个会话的详情，用于把子会话归属回父任务。
//
// 参数：
//   - ctx: 上下文；调用方负责叠加 ownershipTimeout
//   - sessionID: 目标会话 id
//
// 返回：
//   - sessionDetail: 会话详情
//   - err: sessionID 为空、请求失败、非 2xx、响应解析失败时非 nil，此时详情为零值
//
// 注意：
//   - sessionID 为空直接返回错误，不触达服务端：拿空 id 拼出的 "/session/" 只会
//     换来一个 404，白白占掉一次超时预算
//   - 本方法在 SSE 事件回调里同步调用（见 adapter.resolveChildSession），
//     阻塞的是本任务的事件流——超时必须用 ownershipTimeout 而非 unaryTimeout
func (a *API) GetSession(ctx context.Context, sessionID string) (d sessionDetail, err error) {
	if sessionID == "" {
		return sessionDetail{}, fmt.Errorf("查询会话详情：会话 id 为空")
	}
	start := time.Now()
	path := "/session/" + sessionID
	a.log().Info("opencode 查询会话详情", "path", path, "session", sessionID)
	defer func() {
		if err != nil {
			a.log().Error("opencode 查询会话详情失败", "path", path, "session", sessionID, "cause", err)
		} else {
			a.log().Info("opencode 会话详情已取得", "path", path, "session", sessionID,
				"parent", d.ParentID, "elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return sessionDetail{}, fmt.Errorf("查询会话详情请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return sessionDetail{}, a.httpError("查询会话详情", resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return sessionDetail{}, fmt.Errorf("解析会话详情: %w", err)
	}
	return d, nil
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run TestGetSession -v`
Expected: 三个用例全 PASS

- [ ] **Step 6: 核对日志与注释**

对照自查，缺哪补哪：
- 入口 Info（带 `path` / `session`）✅ Step 4 已含
- 失败 Error（带 `cause`）✅
- 成功 Info（带 `parent` / `elapsed_ms`，不静默）✅
- `sessionDetail` 有「为什么只取三个字段」注释 ✅
- `GetSession` 有参数/返回/注意事项文档注释 ✅
- `ownershipTimeout` 有「为什么不是 30s」注释 ✅
- 全文件无 `fmt.Printf`：`grep -n 'fmt.Printf' internal/executor/opencode/api.go` 应无输出

- [ ] **Step 7: 提交**

```bash
git add internal/executor/opencode/api.go internal/executor/opencode/api_test.go
git commit -m "feat(opencode): 加 GetSession 取会话 parentID，为子会话归属做准备"
```

---

### Task 2: 认亲——把子会话的审批请求收下来

**Files:**
- Modify: `internal/executor/opencode/adapter.go`
- Test: `internal/executor/opencode/adapter_test.go`

**Interfaces:**
- Consumes: Task 1 的 `(*API).GetSession`、`ownershipTimeout`、`sessionDetail`
- Produces: `runState` 新字段 `childSessions map[string]string` / `permSession map[string]string` / `sessMu sync.RWMutex`；`(*Adapter).resolveChildSession(r *runState, sessionID string) (title string, ok bool)`；`(*Adapter).emitOwnershipFailure(r *runState, sessionID, reason string)`

- [ ] **Step 1: 扩展假服务端，支持 GET /session/{id}**

在 `adapter_test.go` 的 `fakeServer` 结构体里加字段：

```go
	children         map[string]childSession // 子会话 id -> 详情
	sessionGets      []string                // 收到的 GET /session/{id} 顺序
	sessionGetStatus map[string]int          // 一次性故障注入：子会话 id -> 要返回的状态码
```

在 `permCall` 定义附近加类型：

```go
// childSession 是假服务端持有的子会话详情（GET /session/{id} 的应答素材）。
type childSession struct {
	parent string
	title  string
}
```

`newFakeServer` 的初始化改为：

```go
	fs := &fakeServer{
		sessionID:        "sess-1",
		lines:            make(chan string, 64),
		children:         map[string]childSession{},
		sessionGetStatus: map[string]int{},
	}
```

在 handler 的 `case r.Method == http.MethodGet && r.URL.Path == "/event":` **之前**插入新分支：

```go
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/session/") &&
			!strings.Contains(strings.TrimPrefix(r.URL.Path, "/session/"), "/"):
			child := strings.TrimPrefix(r.URL.Path, "/session/")
			fs.mu.Lock()
			fs.sessionGets = append(fs.sessionGets, child)
			// 一次性故障注入：用完即清，供「负结果不缓存」用例对同一 child 先失败后成功
			if code, bad := fs.sessionGetStatus[child]; bad {
				delete(fs.sessionGetStatus, child)
				fs.mu.Unlock()
				w.WriteHeader(code)
				return
			}
			d, ok := fs.children[child]
			fs.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprintf(w, `{"id":%q,"parentID":%q,"title":%q}`, child, d.parent, d.title)
```

再加三个辅助方法（放在 `perms()` 附近）：

```go
// addChild 登记一个子会话，供 GET /session/{id} 应答。
func (fs *fakeServer) addChild(id, parent, title string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.children[id] = childSession{parent: parent, title: title}
}

// failNextSessionGet 让下一次 GET /session/{id} 返回指定状态码（一次性）。
func (fs *fakeServer) failNextSessionGet(id string, code int) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.sessionGetStatus[id] = code
}

// sessionGetCount 返回针对某个子会话的 GET /session/{id} 次数。
func (fs *fakeServer) sessionGetCount(id string) int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	n := 0
	for _, got := range fs.sessionGets {
		if got == id {
			n++
		}
	}
	return n
}
```

再加一个带任意会话 id 的事件构造器（现有 `permissionAskedEvent` 写死 `sess-1`）：

```go
// permissionAskedEventFrom 构造一条来自指定会话的 permission.asked 事件。
func permissionAskedEventFrom(sessionID, id, perm, command string) string {
	return sseLine(map[string]any{
		"type":      "permission.asked",
		"sessionID": sessionID,
		"properties": map[string]any{
			"id": id, "sessionID": sessionID, "permission": perm,
			"patterns": []string{command},
			"metadata": map[string]any{"command": command},
			"tool":     map[string]any{"messageID": "msg-1", "callID": "call-1"},
		},
	})
}
```

- [ ] **Step 2: 写失败的测试**

在 `adapter_test.go` 末尾追加四个用例：

```go
// TestChildSessionPermissionProducesTicket 覆盖 B52 的核心修复：子会话发来的
// permission.asked 必须认亲成功并产出工单（修复前被 acceptForeign 丢弃，任务挂死）。
func TestChildSessionPermissionProducesTicket(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0001"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "Run probe curl command (@general subagent)")
	fs.push(permissionAskedEventFrom("sess-child", "perm-child-1", "bash", "curl https://example.com"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-child-1" {
		t.Errorf("PermissionID=%q，期望 perm-child-1", ev.PermissionID)
	}
	if !strings.Contains(ev.Text, "curl https://example.com") {
		t.Errorf("权限描述=%q，应含命令文本", ev.Text)
	}
}

// TestChildSessionOwnershipCached 认亲结果必须缓存：同一子会话连发两条审批，
// GET /session/{id} 只能被调一次，否则每条事件都在热路径上做一次网络 I/O。
func TestChildSessionOwnershipCached(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0002"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "子任务")
	fs.push(permissionAskedEventFrom("sess-child", "perm-a", "bash", "echo a"))
	fs.push(permissionAskedEventFrom("sess-child", "perm-b", "bash", "echo b"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	waitEventType(t, ch, "permission")
	waitEventType(t, ch, "permission")
	if n := fs.sessionGetCount("sess-child"); n != 1 {
		t.Fatalf("GET /session/sess-child 调用 %d 次，期望 1 次（认亲结果应缓存）", n)
	}
}

// TestOwnershipFailureEmitsProgress 认亲失败必须不静默：无工单，但要有一条
// progress 事件让审核者在 handoff show 的事件历史里看得见。
func TestOwnershipFailureEmitsProgress(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0003"
	fs := newFakeServer(t)
	// 不登记该子会话 → GET /session 返回 404
	fs.push(permissionAskedEventFrom("sess-unknown", "perm-x", "bash", "echo x"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	deadline := time.After(5 * time.Second)
	sawProgress := false
	for !sawProgress {
		select {
		case ev := <-ch:
			if ev.Type == "permission" {
				t.Fatalf("认亲失败却产出了工单: %+v", ev)
			}
			if ev.Type == "progress" && strings.Contains(ev.Text, "sess-unknown") {
				sawProgress = true
			}
		case <-deadline:
			t.Fatal("等待认亲失败的 progress 事件超时")
		}
	}
}

// TestOwnershipNegativeResultNotCached 认亲失败不得入缓存：首次 500、二次正常，
// 第二条审批必须能成功产出工单。缓存了负结果，一次网络抖动就把这个子会话永久拉黑。
func TestOwnershipNegativeResultNotCached(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0004"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "子任务")
	fs.failNextSessionGet("sess-child", http.StatusInternalServerError)
	fs.push(permissionAskedEventFrom("sess-child", "perm-fail", "bash", "echo first"))
	fs.push(permissionAskedEventFrom("sess-child", "perm-ok", "bash", "echo second"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-ok" {
		t.Fatalf("PermissionID=%q，期望 perm-ok（首条因 500 丢弃，第二条应重新认亲成功）", ev.PermissionID)
	}
	if n := fs.sessionGetCount("sess-child"); n != 2 {
		t.Fatalf("GET /session/sess-child 调用 %d 次，期望 2 次（负结果不得缓存）", n)
	}
}
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./internal/executor/opencode/ -run 'TestChildSession|TestOwnership' -v`
Expected: `TestChildSessionPermissionProducesTicket` 与 `TestChildSessionOwnershipCached`、`TestOwnershipNegativeResultNotCached` 因「等待 permission 事件超时」FAIL；`TestOwnershipFailureEmitsProgress` 因等不到 progress 事件 FAIL

- [ ] **Step 4: 加 runState 字段**

在 `adapter.go` 的 `runState` 结构体里，`lastEventAt` 之前加：

```go
	// childSessions / permSession 支撑子会话审批归属（B52）。
	//
	// childSessions: 已认亲成功的子会话 id → 会话标题。认亲一次即缓存——一个
	// 回合里同一子会话会连发多条事件，每条都发 HTTP 会把网络 I/O 塞进事件热路径。
	// permSession: permID → 该权限所属会话 id。应答必须发回请求所在的会话
	// （子会话的权限发给父会话 opencode 不认），而 RespondPermission 的入参只有 permID。
	//
	// why 不复用 turnMu：acceptForeign 在 mapEvent 里刻意排在 turnMu.Lock() 之前，
	// 就是为了不在持锁时做网络 I/O。锁序固定 turnMu → sessMu，不得反向。
	sessMu        sync.RWMutex
	childSessions map[string]string
	permSession   map[string]string
```

在 `runState` 的构造处（`startRun` 里初始化 `partSeen` / `partTypes` 等 map 的同一段）加：

```go
		childSessions: map[string]string{},
		permSession:   map[string]string{},
```

- [ ] **Step 5: 实现认亲与失败播报**

在 `adapter.go` 的 `acceptForeign` 之后加两个方法：

```go
// resolveChildSession 判定一个陌生会话是否是本任务派生的子会话。
//
// 参数：
//   - sessionID: 事件携带的会话 id（调用方保证非空）
//
// 返回：
//   - title: 子会话标题（供工单标注）；认亲失败时为空
//   - ok:    是否认亲成功
//
// 注意：
//   - 只缓存正结果。一次网络抖动导致的判定失败若被缓存，这个子会话就被永久
//     拉黑，它后续每一条审批都会被丢弃——那正是本次要修的故障形态
//   - 不持 sessMu 跨 HTTP 调用：查缓存与写缓存各自短暂加锁，网络 I/O 不在锁下。
//     同一个新子会话的两条事件并发到达会各发一次 GET /session，幂等且无害，
//     不值得为它加 singleflight
//   - 不向上递归找祖先：opencode 自己给 subagent 下了 permission:[{task,*,deny}]
//     （真机实测），嵌套深度恒为 1。真出现二级说明这条不变量被打破了，那是断言
//     失败，该报警而不是在事件热路径上做无界遍历
func (a *Adapter) resolveChildSession(r *runState, sessionID string) (title string, ok bool) {
	r.sessMu.RLock()
	title, cached := r.childSessions[sessionID]
	r.sessMu.RUnlock()
	if cached {
		a.log.Debug("子会话认亲命中缓存", "task", r.taskID, "child", sessionID)
		return title, true
	}

	a.log.Info("子会话认亲开始", "task", r.taskID, "event_session", sessionID, "own_session", r.session)
	start := time.Now()
	// ctx 不挂 run 的生命周期：超时已被 ownershipTimeout 收在 5s 内，
	// 而 Stop 会关掉 SSE 流，最坏情况只是多等这一次往返
	ctx, cancel := context.WithTimeout(context.Background(), ownershipTimeout)
	defer cancel()
	d, err := r.api.GetSession(ctx, sessionID)
	if err != nil {
		a.log.Warn("子会话认亲失败：查询会话详情出错，本条审批请求丢弃",
			"task", r.taskID, "event_session", sessionID, "cause", err)
		return "", false
	}
	switch {
	case d.ParentID == r.session:
		r.sessMu.Lock()
		r.childSessions[sessionID] = d.Title
		r.sessMu.Unlock()
		a.log.Info("子会话认亲成功", "task", r.taskID, "child", sessionID,
			"parent", d.ParentID, "title", d.Title, "elapsed_ms", time.Since(start).Milliseconds())
		return d.Title, true
	case d.ParentID == "":
		a.log.Warn("子会话认亲失败：该会话没有父会话，是与本任务无关的顶层会话，本条审批请求丢弃",
			"task", r.taskID, "event_session", sessionID, "own_session", r.session)
	default:
		a.log.Warn("子会话认亲失败：父会话不是本任务会话，opencode「subagent 不可再派生 subagent」"+
			"（task:deny）的不变量可能已被打破，本条审批请求丢弃",
			"task", r.taskID, "event_session", sessionID, "event_parent", d.ParentID, "own_session", r.session)
	}
	return "", false
}

// emitOwnershipFailure 把一次归属判定失败播报给审核者。
//
// 参数：
//   - sessionID: 判定失败的会话 id（可能为空串）
//   - reason:    失败原因短语，进 progress 文本
//
// why：丢弃一条审批请求意味着 opencode 在等一个永远不会到来的决策，而 serve
// 活着、看门狗不触发——只写日志的话审核者在 handoff 里完全看不见。progress
// 只入库不阻塞，是把这件事送到 handoff show 事件历史的最轻通道。
func (a *Adapter) emitOwnershipFailure(r *runState, sessionID, reason string) {
	if sessionID == "" {
		sessionID = "(未携带)"
	}
	a.emit(r, executor.AdapterEvent{
		Type: "progress",
		Text: "丢弃了一条无法归属的审批请求（" + reason + "，会话 " + sessionID +
			"）：opencode 可能在等一个看不见的决策，任务若卡住请 handoff attach 查看现场",
	})
}
```

- [ ] **Step 6: 改写 acceptForeign**

把 `adapter.go` 里整个 `acceptForeign` 函数（含其文档注释）替换为：

```go
// acceptForeign 裁决一条「会话 id 与本任务不符」的事件是否仍要处理，
// 并保证两个方向都不静默。
//
// 参数：
//   - sessionID: 从顶层或 properties 提取到的会话 id（"" 表示事件没带）
//
// 返回：
//   - true 表示继续按本任务的事件处理，false 表示丢弃
//
// why（三条实测结论，改这里之前先读）：
//   - opencode 会为 subagent/task 工具派生子会话，其权限请求带子会话 id。
//     GET /session/{id} 返回 parentID，**父子关系端点是存在的**——本函数早期
//     版本的注释断言「本层无法把子会话映射回任务」，那是错的，B52 已实测推翻：
//     子会话的 parentID 就等于本任务的会话 id
//   - 子会话被 opencode 自己下了 permission:[{task,*,deny}]，不能再派生 subagent，
//     所以嵌套深度恒为 1，比一次 parentID 就够，不需要向上遍历
//   - 每个任务起自己的 opencode serve（见 proc.go 的 freePort），所以 /event 的
//     「全服务器广播」实际是「本任务这一个 serve 的广播」，流上出现的陌生会话
//     本就只可能是本任务自己派生的子会话
//
// why 仍然保留校验（不能改成一律放行）：校验存在的理由是防止跨任务串台。
// 缺 sessionID 的任务级事件更不能 fail-open——一条无归属的 permission.asked
// 会被当成本任务的审批门，审核者的批准动作会发到错误的会话。
func (a *Adapter) acceptForeign(r *runState, ev sseEvent, sessionID string) bool {
	if !taskScopedEvents[ev.Type] {
		return true // 服务器级广播事件：本就不带会话，交给下游的 default 分支跳过
	}
	if ev.Type == "permission.asked" {
		if sessionID == "" {
			a.log.Warn("收到不带会话 id 的审批请求，无法归属，未产出工单",
				"task", r.taskID, "own_session", r.session,
				"properties", turn.TruncateRunes(string(ev.Properties), 200))
			a.emitOwnershipFailure(r, sessionID, "事件未携带会话 id")
			return false
		}
		if _, ok := a.resolveChildSession(r, sessionID); ok {
			return true
		}
		a.emitOwnershipFailure(r, sessionID, "认亲失败")
		return false
	}
	// 非审批事件即使来自已认亲的子会话也照旧丢弃：回合记账（lastAssistantMsgID
	// 水位、pendingDelta、mapIdle 分类）整套围绕单一会话的消息序列构建，交错
	// 第二个会话的消息会直接污染水位与空闲判定。子 agent 干了什么仍看得到——
	// 主 agent 会在自己的收尾正文里转述（四家 executor 探针均已实测）。
	a.log.Debug("收到其他会话事件，跳过", "task", r.taskID,
		"type", ev.Type, "session", sessionID)
	return false
}
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run 'TestChildSession|TestOwnership' -v`
Expected: 四个用例全 PASS

- [ ] **Step 8: 核对日志与注释**

自查，缺哪补哪：
- 认亲开始 Info（`task` / `event_session` / `own_session`）✅
- 认亲成功 Info（`child` / `parent` / `title` / `elapsed_ms`，成功路径不静默）✅
- 三种失败各一条独立 Warn 且带 `cause` 或判据字段，可 grep 区分 ✅
- 缓存命中走 Debug（热路径不刷屏）✅
- `runState` 新字段有「为什么不复用 turnMu」注释 ✅
- `resolveChildSession` / `emitOwnershipFailure` 有参数/返回/注意事项文档注释 ✅
- `acceptForeign` 的旧注释「没有可用的父子关系端点」**已删除**，换成三条实测结论：`grep -n '没有可用的父子关系端点' internal/executor/opencode/adapter.go` 应无输出
- 无 `fmt.Printf`：`grep -n 'fmt.Printf' internal/executor/opencode/adapter.go` 应无输出

- [ ] **Step 9: 提交**

```bash
git add internal/executor/opencode/adapter.go internal/executor/opencode/adapter_test.go
git commit -m "fix(opencode): 子会话审批请求认亲回父任务，不再静默丢弃"
```

---

### Task 3: 工单标注与应答路由

**Files:**
- Modify: `internal/executor/opencode/adapter.go`
- Test: `internal/executor/opencode/adapter_test.go`

**Interfaces:**
- Consumes: Task 2 的 `runState.childSessions` / `runState.permSession` / `runState.sessMu`
- Produces: 无新导出符号；`mapPermissionAsked` 写入 `permSession` 并给 `Text` 加前缀，`RespondPermission` 按 `permSession` 选会话

- [ ] **Step 1: 写失败的测试**

在 `adapter_test.go` 末尾追加两个用例：

```go
// TestChildPermissionTextCarriesSubagentPrefix 工单文案必须让审核者一眼看出
// 这条审批来自子 agent——子 agent 的越权和主 agent 的越权含义完全不同。
func TestChildPermissionTextCarriesSubagentPrefix(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0005"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "Run probe curl command (@general subagent)")
	fs.push(permissionAskedEventFrom("sess-child", "perm-p", "bash", "curl https://example.com"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	want := "[子 agent: Run probe curl command (@general subagent)] "
	if !strings.HasPrefix(ev.Text, want) {
		t.Fatalf("权限描述=%q，应以 %q 开头", ev.Text, want)
	}
	if !strings.Contains(ev.Text, "curl https://example.com") {
		t.Errorf("权限描述=%q，前缀之后应保留原描述", ev.Text)
	}
}

// TestRespondPermissionRoutesToChildSession 应答必须发回请求所在的子会话。
// 发给父会话 opencode 不认，审核者的批准落不了地，任务照样挂死。
func TestRespondPermissionRoutesToChildSession(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0006"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "子任务")
	fs.push(permissionAskedEventFrom("sess-child", "perm-r", "bash", "echo r"))

	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	waitEventType(t, ch, "permission")
	if err := ad.RespondPermission(context.Background(), taskID, "perm-r", "once"); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	calls := fs.perms()
	if len(calls) != 1 {
		t.Fatalf("权限应答请求 %d 次，期望 1 次", len(calls))
	}
	want := "/session/sess-child/permissions/perm-r"
	if calls[0].path != want {
		t.Fatalf("应答 path=%q，期望 %q（必须发往子会话，不是父会话）", calls[0].path, want)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/executor/opencode/ -run 'TestChildPermissionText|TestRespondPermissionRoutes' -v`
Expected: 前者 FAIL（`Text` 无前缀），后者 FAIL（path 是 `/session/sess-1/permissions/perm-r`）

- [ ] **Step 3: 在 mapPermissionAsked 里记会话并加前缀**

在 `mapPermissionAsked` 的匿名结构体里补一个字段（与 `ID` 同级）：

```go
		SessionID  string   `json:"sessionID"`
```

在 `r.permText[pa.ID] = text` 这一行**之前**插入：

```go
	// 子会话归属与标注（B52）。permSession 决定应答发往哪个会话；前缀让审核者
	// 一眼看出这条审批来自子 agent——子 agent 的越权与主 agent 的越权含义不同。
	//
	// why 前缀必须加在空描述兜底之后：前缀本身非空，先加前缀会让上面那段
	// 「描述为空就给兜底文本」的判空永远为假，真正空描述的请求就变成一条只有
	// 前缀的工单，审核者仍然看不到要批什么。
	permSess := pa.SessionID
	if permSess == "" {
		permSess = r.session
	}
	if permSess != r.session {
		r.sessMu.RLock()
		title := r.childSessions[permSess]
		r.sessMu.RUnlock()
		if title != "" {
			text = "[子 agent: " + title + "] " + text
		} else {
			// 认亲成功但标题为空（opencode 未给 title）：仍要标出来源，
			// 只是标不出是哪个子 agent
			text = "[子 agent] " + text
		}
	}
	r.sessMu.Lock()
	r.permSession[pa.ID] = permSess
	r.sessMu.Unlock()
	a.log.Info("权限请求已归属会话", "task", r.taskID, "perm", pa.ID,
		"session", permSess, "is_child", permSess != r.session)
```

- [ ] **Step 4: 在 RespondPermission 里按表选会话**

把 `RespondPermission` 里这一行：

```go
	if err := r.api.RespondPermission(ctx, r.session, permID, decision); err != nil {
```

替换为：

```go
	// 应答必须发回权限请求所在的会话：子会话的权限发给父会话 opencode 不认，
	// 审核者的批准落不了地，任务照样挂死（B52）
	sess := r.session
	r.sessMu.RLock()
	mapped, known := r.permSession[permID]
	r.sessMu.RUnlock()
	if known {
		sess = mapped
	} else {
		// 进程内表，agentd 重启后为空；此时只能退回父会话。若该权限来自子
		// agent，这次应答会被 opencode 拒（4xx），错误会经 httpError 一路回到
		// 审核者终端——是响的，不是静默的
		a.log.Warn("权限应答未在会话映射表里找到该 permID，退回父会话应答",
			"task", taskID, "perm", permID, "session", sess)
	}
	a.log.Info("权限应答选定会话", "task", taskID, "perm", permID,
		"session", sess, "from_map", known)
	if err := r.api.RespondPermission(ctx, sess, permID, decision); err != nil {
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run 'TestChildPermissionText|TestRespondPermissionRoutes' -v`
Expected: 两个用例 PASS

- [ ] **Step 6: 核对日志与注释**

自查：
- 归属登记有 Info（`perm` / `session` / `is_child`）✅
- 应答选会话有 Info（`session` / `from_map`），未命中有 Warn ✅
- 前缀顺序有「为什么必须在兜底之后」注释 ✅
- `title` 为空的降级分支有注释 ✅
- 未命中回退分支有「失败是响的」注释 ✅
- 无 `fmt.Printf`

- [ ] **Step 7: 提交**

```bash
git add internal/executor/opencode/adapter.go internal/executor/opencode/adapter_test.go
git commit -m "feat(opencode): 工单标注 subagent 来源，权限应答发回子会话"
```

---

### Task 4: 回归与真机 e2e 验收

**Files:**
- Modify: 无（纯验证；如发现回归则回到对应 Task 修复）

**Interfaces:**
- Consumes: Task 1-3 的全部产出
- Produces: 验收证据（测试输出、devbox 上的工单文案与文件）

- [ ] **Step 1: 跑包内全量回归**

Run: `go test ./internal/executor/opencode/... -count=1`
Expected: ok，全绿。`regression_group_a`、`regression_round2`、`replay_spike`、`reconcile_internal`、`resume_cold_internal` 是历史踩坑的固化，改会话过滤路径必须证明没碰坏它们。任一 FAIL 即停，回到对应 Task 修复，不得跳过。

- [ ] **Step 2: 跑全仓回归**

Run: `go test ./... -count=1`
Expected: ok，全绿

- [ ] **Step 3: 跑 vet**

Run: `go vet ./internal/executor/opencode/...`
Expected: 无输出

- [ ] **Step 4: 部署到 devbox 隔离实例**

隔离实例已在 devbox 上就绪：`/tmp/hfb52`，监听 `127.0.0.1:7893`，DataDir `/tmp/hfb52/data`，独立二进制与探针仓库，config 含 `env:` 段。

**红线（违反即中止）**：不得触碰 `~/.handoff/` 任何文件；不得停止/重启/覆盖 7777 端口上的 agentd（本机或 devbox，都持有活跃任务）；每条 CLI 命令都要带 `--config /tmp/hfb52/config.yaml` 与 `HOME=/tmp/hfb52`（`cursorPath` 硬编码自 `os.UserHomeDir()`，不改 HOME 会把 cursor 文件写进受保护的 `~/.handoff/`）；杀进程只按验证二进制的完整路径精确匹配，**绝不 `pkill -f agentd`**。

```bash
GOOS=darwin GOARCH=arm64 go build -o /tmp/handoff-b52 ./ && \
  scp /tmp/handoff-b52 sycm@100.73.238.21:/tmp/hfb52/handoff.new
```

先确认 7893 上跑的确实是隔离实例，**输出必须逐字是** `/tmp/hfb52/handoff --config /tmp/hfb52/config.yaml agentd`，不是就停下来查，不要继续：

```bash
ssh sycm@100.73.238.21 'ps -o pid=,command= -p $(lsof -ti tcp:7893 -sTCP:LISTEN)'
```

确认后替换二进制并重启（`kill` 只针对上一步核对过的那个 pid；不得使用 `pkill`）：

```bash
ssh sycm@100.73.238.21 'set -e
PID=$(lsof -ti tcp:7893 -sTCP:LISTEN)
ps -o command= -p $PID | grep -qx "/tmp/hfb52/handoff --config /tmp/hfb52/config.yaml agentd" || { echo "7893 上不是隔离实例，中止"; exit 1; }
kill $PID
for i in $(seq 1 10); do ps -p $PID >/dev/null 2>&1 || break; sleep 1; done
ps -p $PID >/dev/null 2>&1 && { echo "旧进程未退出，中止"; exit 1; }
mv /tmp/hfb52/handoff.new /tmp/hfb52/handoff
chmod +x /tmp/hfb52/handoff
cd /tmp/hfb52 && nohup /tmp/hfb52/handoff --config /tmp/hfb52/config.yaml agentd >> /tmp/hfb52/agentd.out 2>&1 &
sleep 3
lsof -nP -iTCP:7893 -sTCP:LISTEN'
```

最后确认 7777 未受影响（应仍有 `handoff-s` 在监听）：

```bash
ssh sycm@100.73.238.21 'lsof -nP -iTCP:7777 -sTCP:LISTEN'
```

- [ ] **Step 5: 跑 opencode 探针，验四条标准**

用已复现过缺陷的探针脚本 `/tmp/hfb52/probe-opencode.md` 派发一个 opencode 任务，记下派发时刻，然后：

1. 挂起工单数 **1**（修复前为 0）
2. 工单文案以 `[子 agent: ` 开头，含 `(@general subagent)`
3. 批准后 `/tmp/b52-probe-opencode.txt` 真实创建（非空），任务走到 `completed`
4. **本次运行期间**（按时间戳筛，不看累计数）agentd.log 新增的「不属于本任务会话」WARN 数 **0**

四条缺一不可。任一不达标即停，把现场（`agentd.log` 相关行、`render.log`、`handoff show` 输出）记下来，回到对应 Task 修复。

- [ ] **Step 6: 记录验收证据并提交**

把四条标准的实际输出（工单文案原文、文件字节数、任务终态、WARN 计数）贴进 backlog 的 B52 行 `验收` 列。

```bash
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B52 转 done，记录真机 e2e 验收证据"
```
