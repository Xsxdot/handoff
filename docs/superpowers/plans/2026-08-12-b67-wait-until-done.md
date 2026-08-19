# B67 静默等待依赖任务归档 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `handoff wait` 增加 `--until-done` 门闩：不碰审核者 cursor、不输出中间事件，只在真实 `archived` 到达时输出一行事件 JSON；依赖失败立即非零退出。

**Architecture:** 以当前 `main+B68` 为主线，先精确复用 W3a Task 8 的无 cursor 单连接原语 `StreamEventsOnce`。`internal/client.WaitArchived` 负责“权威快照 → 从最新 seq 订阅 → 内存水位 → 退避重连”的全部可靠性；`cmd/wait.go` 只负责 flag、总超时、退出码、通知和 stdout 契约。agentd、store、proto 均不改。

**Tech Stack:** Go 1.24、Cobra、`net/http` / `httptest`、`github.com/coder/websocket`、结构化 `log/slog`、现有 `proto.Event` / `client.AttachInfo` 契约。

## Global Constraints

- 实现基线必须同时包含 B68（当前分支已有）与 W3a Task 8（提交 `529a950f`）；不得复制另一套 WS 或 cursor 逻辑。
- 命令固定为 `handoff wait <task> --until-done`；不新增顶层命令，不通用化为任意 `--until <event>`。
- `--until-done` 只等待，不自动派发、执行、回答、continue 或 done 后续任务。
- stdout：成功严格一行原始 `archived` `proto.Event` JSON；等待、失败、超时均不得输出事件或人读文本。
- 退出码：归档 `0`；依赖 failed / 鉴权 / 任务不存在 / 协议异常 `1`；总等待超时 `124`。
- `--timeout` 在本模式是整体总时限，不能被中间帧或重连重置。
- `--until-done` 与 `--follow` 互斥；`--notify` 只在归档成功时触发。
- 不读写 `~/.handoff/cursor-<task>`；网络层只维护本进程内 `from_seq`。
- `completed` 事件不是成功；只有 B68 的真实 `archived` 才算成功。
- 权威状态 `completed` 却找不到 `archived` 时返回兼容性/数据错误，禁止合成假事件。
- 临时 HTTP/WS 故障按 `1s → 2s → … → 60s` 退避；401/403/404/1008 等永久错误立即返回。
- 不新增第三方依赖，不修改 agentd/store/proto，不触发远程分支自动同步。
- 新文件顶部写中文职责与边界；导出方法/错误有中文注释；复杂分支解释“为什么”。
- 使用结构化 `slog`，禁止 `fmt.Printf`/`log.Printf` 充当日志；正常等待默认安静，循环日志只用 Debug。
- 每个实现任务完成前必须自检关键日志、错误上下文、成功可观测性、文件头与导出注释。

---

## File Map

| 文件 | 职责 |
|---|---|
| `internal/client/client.go` | 复用 W3a 的 `StreamEventsOnce`；让 HTTP 状态错误可分类；保留现有 WS/HTTP 公共原语 |
| `internal/client/mirrorstream_test.go` | W3a 原有“逐帧交付且无 cursor”契约测试 |
| `internal/client/wait_archived.go` | 新增 B67 专用快照分类、终态错误、无 cursor 等待与退避编排 |
| `internal/client/wait_archived_test.go` | 覆盖终态快照、中间事件、竞态、重连、cursor 隔离、永久错误与取消 |
| `cmd/wait.go` | 新 flag、冲突校验、总超时、退出码、通知和单行 JSON 写出 |
| `cmd/wait_until_done_test.go` | CLI 线格式、退出码、总超时、flag 冲突与写出失败 |
| `README.md` | 面向操作者记录 B67 用途与契约 |
| `skills/handoff/SKILL.md` | 面向 agent 审核者记录依赖门闩用法与边界 |
| `docs/superpowers/backlog.md` | 实现完成且有证据后回填 plan/验收；执行阶段不要提前标 done |

---

### Task 1: 把 W3a 无 cursor 单连接原语带入 B68 基线

**Files:**
- Modify: `internal/client/client.go`（`streamOnce` 后新增 `StreamEventsOnce`）
- Create: `internal/client/mirrorstream_test.go`

**Interfaces:**
- Consumes: 当前分支的 `Client.streamOnce(ctx, taskID, fromSeq, readDeadline, onFrame)`。
- Produces: `func (c *Client) StreamEventsOnce(ctx context.Context, taskID string, fromSeq int64, onEvent func(proto.Event) error) error`，供 Task 2 的 `WaitArchived` 使用。

- [ ] **Step 1: 验证 B68 已在当前基线、W3a 原语尚未进入**

Run:

```bash
git merge-base --is-ancestor e00a43af HEAD
rg -n 'EventTypeArchived|DoneNote' internal/proto/proto.go
! rg -n 'func \(c \*Client\) StreamEventsOnce' internal/client/client.go
```

Expected: 第一条退出 `0`；第二条能看到 `EventTypeArchived` 与 `DoneNote`；第三条退出 `0`（尚无该方法）。若第三条失败，说明执行基线已经带入 W3a，跳过 cherry-pick，直接从 Step 3 验证现有实现。

- [ ] **Step 2: 精确复用 W3a Task 8 提交**

Run:

```bash
git cherry-pick 529a950f
```

Expected: 新增 `StreamEventsOnce` 与 `mirrorstream_test.go`，没有 agentd/store/proto 变更。该提交本身即本步骤的 commit，不另造重复提交。

- [ ] **Step 3: 运行原语契约测试**

Run:

```bash
go test -count=1 ./internal/client -run '^TestStreamEventsOnceDeliversAndNoCursor$'
```

Expected: PASS；测试收到 question 与 progress 两帧，HOME 下没有 `cursor-t1`。

- [ ] **Step 4: 把镜像专用文案收敛为通用无 cursor 原语文案**

将 `StreamEventsOnce` 的 Debug 日志与注释从“镜像事件流”改为“无 cursor 事件流”，保留签名和行为：

```go
// StreamEventsOnce 建立一次无 cursor 事件流连接，把收到的每一帧交给 onEvent，
// 直到连接断开、回调收手或 ctx 取消。它不读写 cursor，也不自行重连。
//
// 调用方必须持有自己的 fromSeq 与重连策略：事件镜像把水位落在数据库，
// B67 的归档门闩只把水位留在当前进程；两者都不能污染审核者 cursor。
func (c *Client) StreamEventsOnce(ctx context.Context, taskID string, fromSeq int64,
    onEvent func(proto.Event) error) error {
    c.log().Debug("无 cursor 事件流建立", "addr", c.baseURL,
        "task", taskID, "from_seq", fromSeq)
    return c.streamOnce(ctx, taskID, fromSeq,
        func() time.Time { return time.Time{} }, onEvent)
}
```

同时把 `streamOnce` 内两条纯生命周期日志从 Info 降为 Debug，字段不变：

```go
c.log().Debug("WS 连接建立", "addr", c.baseURL, "task", taskID, "from_seq", fromSeq)
defer func() {
    conn.CloseNow()
    c.log().Debug("WS 连接关闭", "addr", c.baseURL, "task", taskID)
}()
```

原因：B67 正常等待必须默认安静；真正需要 Info/Error 的事件交付、终态和断线结论都在上层调用方已有日志。保留 Debug 仍满足可观测性，又不会让每次健康建连污染 CLI stderr。

- [ ] **Step 5: Add logging at key points**

- 入口 Debug 必须带 `addr`、`task`、`from_seq`；底层连接建立/关闭也保持 Debug。
- 不新增逐帧 Info/Warn；逐帧分诊属于调用方。
- 连接拨号/关闭与错误继续复用 `streamOnce` 的结构化日志，不复制第二套。

- [ ] **Step 6: Add intent comments**

- 保留 `mirrorstream_test.go` 文件头的职责/边界。
- 导出方法注释必须点明：无 cursor、单连接、不重连、调用方持有水位。
- 内联注释解释零 read deadline 的原因，不写“调用 streamOnce”式表面注释。

- [ ] **Step 7: 运行相关包测试并提交通用化改动**

Run:

```bash
gofmt -w internal/client/client.go internal/client/mirrorstream_test.go
go test -count=1 ./internal/client
git diff --check
```

Expected: 全部 PASS，`git diff --check` 无输出。

```bash
git add internal/client/client.go internal/client/mirrorstream_test.go
git commit -m "refactor(client): 通用化无 cursor 事件流原语"
```

---

### Task 2: 实现 `Client.WaitArchived` 的快照、事件流与重连闭环

**Files:**
- Modify: `internal/client/client.go`（HTTP 状态错误改为可分类类型；`isPermanent` 扩展）
- Create: `internal/client/wait_archived.go`
- Create: `internal/client/wait_archived_test.go`

**Interfaces:**
- Consumes: Task 1 的 `StreamEventsOnce`；现有 `Attach(ctx, taskID) (*AttachInfo, error)`；`Client` 的可注入 WS timing。
- Produces:
  - `var ErrDependencyFailed error`
  - `var ErrArchivedEventMissing error`
  - `func (c *Client) WaitArchived(ctx context.Context, taskID string) (*proto.Event, error)`

- [ ] **Step 1: 写 HTTP 错误分类与终态快照的失败测试**

在 `wait_archived_test.go` 顶部使用 `package client`（白盒测试私有状态分类），加入文件头与以下测试：

```go
// wait_archived_test.go 验证 B67 归档门闩的快照、事件与重连契约。
//
// 职责：覆盖真实 archived、failed、兼容性错误、竞态、重连和 cursor 隔离。
// 边界：只使用 httptest HTTP/WS，不启动真实 agentd、不改变真实 HOME。
package client

func TestHTTPStatusErrorIsPermanent(t *testing.T) {
    for _, code := range []int{400, 401, 403, 404} {
        err := &httpStatusError{op: "任务详情", code: code, body: "拒绝"}
        if !isPermanent(err) {
            t.Errorf("status %d 应是永久错误", code)
        }
    }
    if isPermanent(&httpStatusError{op: "任务详情", code: 500, body: "故障"}) {
        t.Fatal("500 可能瞬时恢复，不应判永久")
    }
}

func TestClassifyArchivedSnapshot(t *testing.T) {
    archived := proto.Event{Seq: 12, TaskID: "t1", Type: proto.EventTypeArchived,
        Payload: json.RawMessage(`{"note":"完成"}`)}
    cases := []struct {
        name string
        snap *AttachInfo
        wantSeq int64
        wantArchived bool
        wantErr error
    }{
        {"已归档返回原事件", snapshot("t1", proto.TaskStateCompleted, archived), 12, true, nil},
        {"失败立即返回", snapshot("t1", proto.TaskStateFailed), 0, false, ErrDependencyFailed},
        {"completed 缺 archived", snapshot("t1", proto.TaskStateCompleted), 0, false, ErrArchivedEventMissing},
        {"活任务从最新水位续拉", snapshot("t1", proto.TaskStateRunning,
            proto.Event{Seq: 9, TaskID: "t1", Type: proto.EventTypeProgress}), 9, false, nil},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, seq, err := classifyArchivedSnapshot("t1", tc.snap)
            if !errors.Is(err, tc.wantErr) {
                t.Fatalf("err=%v want errors.Is(_, %v)", err, tc.wantErr)
            }
            if seq != tc.wantSeq { t.Fatalf("fromSeq=%d want %d", seq, tc.wantSeq) }
            if (got != nil) != tc.wantArchived {
                t.Fatalf("archived=%+v wantArchived=%v", got, tc.wantArchived)
            }
            if got != nil && (got.Seq != 12 || got.Type != proto.EventTypeArchived ||
                string(got.Payload) != `{"note":"完成"}`) {
                t.Fatalf("必须返回原始 archived，got=%+v", got)
            }
        })
    }
}
```

测试辅助 `snapshot` 必须构造真实 `AttachInfo`，RecentEvents 保持升序：

```go
func snapshot(id string, state proto.TaskState, events ...proto.Event) *AttachInfo {
    return &AttachInfo{
        Task: proto.TaskView{Task: proto.Task{ID: id, State: state}},
        RecentEvents: events,
        PendingTickets: []proto.Ticket{},
    }
}
```

- [ ] **Step 2: 运行测试确认 RED**

Run:

```bash
go test -count=1 ./internal/client -run 'TestHTTPStatusErrorIsPermanent|TestClassifyArchivedSnapshot'
```

Expected: FAIL，至少包含 `undefined: httpStatusError`、`undefined: ErrDependencyFailed` 或 `undefined: classifyArchivedSnapshot`。

- [ ] **Step 3: 让 HTTP 状态错误可判别且保持原错误文本**

在 `internal/client/client.go` 的 `permanentError` 附近新增：

```go
// httpStatusError 保留 agentd 非 2xx 响应的状态码，让长驻调用能区分
// “重试可能恢复”的 5xx 与“请求本身不会自愈”的 4xx。
type httpStatusError struct {
    op   string
    code int
    body string
}

func (e *httpStatusError) Error() string {
    return fmt.Sprintf("%s: 状态码 %d: %s", e.op, e.code, e.body)
}
```

修改 `isPermanent`：

```go
var he *httpStatusError
if errors.As(err, &he) && isPermanentStatus(he.code) {
    return true
}
```

修改 `httpError` 的返回值，日志与错误字符串保持不变：

```go
body := strings.TrimSpace(string(b))
return &httpStatusError{op: op, code: resp.StatusCode, body: body}
```

- [ ] **Step 4: 写 `wait_archived.go` 的职责头、错误与快照分类**

```go
// wait_archived.go 实现 B67 的依赖任务归档门闩。
//
// 职责：用权威快照与无 cursor 事件流等待真实 archived，维护进程内水位并重连。
// 边界：不读写审核者 cursor，不交付中间事件，不改变远端任务状态。
package client

var (
    // ErrDependencyFailed 表示依赖任务在本次等待期间进入 failed。
    ErrDependencyFailed = errors.New("依赖任务已失败")
    // ErrArchivedEventMissing 表示任务已 completed，但 B68 archived 事件缺失。
    ErrArchivedEventMissing = errors.New("任务已归档但 archived 事件缺失")
)

func classifyArchivedSnapshot(taskID string, snap *AttachInfo) (*proto.Event, int64, error) {
    var fromSeq int64
    if n := len(snap.RecentEvents); n > 0 {
        fromSeq = snap.RecentEvents[n-1].Seq
    }
    if snap.Task.State == proto.TaskStateFailed {
        return nil, fromSeq, fmt.Errorf("%w: task=%s", ErrDependencyFailed, taskID)
    }
    for i := len(snap.RecentEvents) - 1; i >= 0; i-- {
        if snap.RecentEvents[i].Type == proto.EventTypeArchived {
            ev := snap.RecentEvents[i]
            return &ev, fromSeq, nil
        }
    }
    if snap.Task.State == proto.TaskStateCompleted {
        return nil, fromSeq, fmt.Errorf("%w: task=%s", ErrArchivedEventMissing, taskID)
    }
    return nil, fromSeq, nil
}
```

- [ ] **Step 5: 写终态快照、逐帧分诊、cursor 隔离与总取消测试**

在同一测试文件增加一个可复用的 HTTP/WS server helper：

```go
func newWaitArchivedServer(t *testing.T,
    attach func() *AttachInfo,
    stream func(*websocket.Conn, *http.Request),
) *httptest.Server {
    t.Helper()
    h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch {
        case r.URL.Path == "/api/tasks/t1":
            w.Header().Set("Content-Type", "application/json")
            if err := json.NewEncoder(w).Encode(attach()); err != nil {
                t.Errorf("encode attach: %v", err)
            }
        case r.URL.Path == "/ws/events":
            conn, err := websocket.Accept(w, r, nil)
            if err != nil { t.Errorf("accept: %v", err); return }
            defer conn.CloseNow()
            stream(conn, r)
        default:
            http.NotFound(w, r)
        }
    })
    ts := httptest.NewServer(h)
    t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
    return ts
}

func writeWSEventE(conn *websocket.Conn, ctx context.Context, ev proto.Event) error {
    b, err := json.Marshal(ev)
    if err != nil { return err }
    return conn.Write(ctx, websocket.MessageText, b)
}

func writeWSEvent(t *testing.T, conn *websocket.Conn,
    ctx context.Context, ev proto.Event) {
    t.Helper()
    if err := writeWSEventE(conn, ctx, ev); err != nil {
        t.Errorf("write event seq=%d type=%s: %v", ev.Seq, ev.Type, err)
    }
}
```

加入以下具体用例：

```go
func TestWaitArchivedAlreadyArchivedReturnsOriginalEvent(t *testing.T) {
    want := proto.Event{Seq: 12, TaskID: "t1", Type: proto.EventTypeArchived,
        Payload: json.RawMessage(`{"note":"已验收"}`)}
    ts := newWaitArchivedServer(t,
        func() *AttachInfo { return snapshot("t1", proto.TaskStateCompleted, want) },
        func(*websocket.Conn, *http.Request) { t.Fatal("已归档不应建 WS") })
    got, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
    if err != nil || got.Seq != want.Seq || string(got.Payload) != string(want.Payload) {
        t.Fatalf("got=%+v err=%v, want 原始 archived", got, err)
    }
}

func TestWaitArchivedSkipsIntermediateEventsAndDoesNotTouchCursor(t *testing.T) {
    home := t.TempDir()
    t.Setenv("HOME", home)
    archived := proto.Event{Seq: 15, TaskID: "t1", Type: proto.EventTypeArchived,
        Payload: json.RawMessage(`{"note":"done"}`)}
    frames := []proto.Event{
        {Seq: 11, TaskID: "t1", Type: proto.EventTypeQuestion},
        {Seq: 12, TaskID: "t1", Type: proto.EventTypePermissionRequest},
        {Seq: 13, TaskID: "t1", Type: proto.EventTypeCompleted},
        {Seq: 14, TaskID: "t1", Type: proto.EventTypeProgress},
        archived,
    }
    ts := newWaitArchivedServer(t,
        func() *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
        func(conn *websocket.Conn, r *http.Request) {
            for _, ev := range frames { writeWSEvent(t, conn, r.Context(), ev) }
        })
    got, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
    if err != nil || got.Seq != archived.Seq { t.Fatalf("got=%+v err=%v", got, err) }
    if _, err := os.Stat(filepath.Join(home, ".handoff", "cursor-t1")); !os.IsNotExist(err) {
        t.Fatalf("B67 不得创建 cursor: %v", err)
    }
}

func TestWaitArchivedFailedBeforeAndDuringStream(t *testing.T) {
    t.Run("before", func(t *testing.T) {
        wsCalled := false
        ts := newWaitArchivedServer(t,
            func() *AttachInfo { return snapshot("t1", proto.TaskStateFailed) },
            func(*websocket.Conn, *http.Request) { wsCalled = true })
        _, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
        if !errors.Is(err, ErrDependencyFailed) || wsCalled {
            t.Fatalf("err=%v wsCalled=%v", err, wsCalled)
        }
    })
    t.Run("during", func(t *testing.T) {
        ts := newWaitArchivedServer(t,
            func() *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
            func(conn *websocket.Conn, r *http.Request) {
                writeWSEvent(t, conn, r.Context(), proto.Event{
                    Seq: 7, TaskID: "t1", Type: proto.EventTypeFailed,
                    Payload: json.RawMessage(`{"error":"boom"}`),
                })
            })
        _, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
        if !errors.Is(err, ErrDependencyFailed) {
            t.Fatalf("err=%v want ErrDependencyFailed", err)
        }
    })
}

func TestWaitArchivedContextDeadlineIsTotal(t *testing.T) {
    ts := newWaitArchivedServer(t,
        func() *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
        func(conn *websocket.Conn, r *http.Request) {
            ticker := time.NewTicker(10 * time.Millisecond)
            defer ticker.Stop()
            var seq int64
            for {
                select {
                case <-r.Context().Done(): return
                case <-ticker.C:
                    seq++
                    if err := writeWSEventE(conn, r.Context(), proto.Event{
                        Seq: seq, TaskID: "t1", Type: proto.EventTypeProgress,
                    }); err != nil { return }
                }
            }
        })
    ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
    defer cancel()
    started := time.Now()
    _, err := New(ts.URL, "").WaitArchived(ctx, "t1")
    if !errors.Is(err, context.DeadlineExceeded) {
        t.Fatalf("err=%v want DeadlineExceeded", err)
    }
    if elapsed := time.Since(started); elapsed > time.Second {
        t.Fatalf("总时限被帧重置，elapsed=%v", elapsed)
    }
}
```

`writeWSEvent` 必须 marshal 完整 `proto.Event` 并用 `t.Errorf` 留下 server goroutine 错误；持续 progress 的正常断连则使用返回 error 的 `writeWSEventE` 收口，不能把客户端主动取消误报成测试失败。

- [ ] **Step 6: 运行新增等待测试确认 RED**

Run:

```bash
go test -count=1 ./internal/client -run 'TestWaitArchived'
```

Expected: FAIL with `(*Client).WaitArchived undefined`。

- [ ] **Step 7: 实现 `WaitArchived` 的完整循环**

按以下结构实现；不要调用 `WaitEvent` / `FollowEvents`：

```go
func (c *Client) WaitArchived(ctx context.Context, taskID string) (*proto.Event, error) {
    backoff := c.wsInitialBackoff
    c.log().Debug("等待归档开始", "addr", c.baseURL, "task", taskID)

    for attempt := 1; ; attempt++ {
        if err := ctx.Err(); err != nil {
            c.log().Debug("等待归档结束：上下文取消", "task", taskID, "cause", err)
            return nil, err
        }

        c.log().Debug("等待归档读取权威快照", "task", taskID, "attempt", attempt)
        snap, err := c.Attach(ctx, taskID)
        if err != nil {
            if ctx.Err() != nil { return nil, ctx.Err() }
            if isPermanent(err) {
                c.log().Error("等待归档失败：快照永久错误",
                    "task", taskID, "attempt", attempt, "cause", err)
                return nil, err
            }
            c.log().Debug("等待归档快照暂时失败，退避重试",
                "task", taskID, "attempt", attempt, "backoff", backoff, "cause", err)
            if err := waitArchivedRetry(ctx, &backoff,
                c.wsInitialBackoff, c.wsMaxBackoff, false); err != nil {
                return nil, err
            }
            continue
        }
        terminal, fromSeq, err := classifyArchivedSnapshot(taskID, snap)
        if err != nil {
            c.log().Error("等待归档结束：快照终态失败",
                "task", taskID, "from_seq", fromSeq, "cause", err)
            return nil, err
        }
        if terminal != nil {
            c.log().Debug("等待归档完成：快照已有 archived",
                "task", taskID, "seq", terminal.Seq)
            return terminal, nil
        }

        var archived *proto.Event
        var failed *proto.Event
        started := time.Now()
        streamErr := c.StreamEventsOnce(ctx, taskID, fromSeq, func(ev proto.Event) error {
            fromSeq = ev.Seq // 只在内存推进；绝不调用 writeCursor
            switch ev.Type {
            case proto.EventTypeArchived:
                copy := ev
                archived = &copy
                return errStopStream
            case proto.EventTypeFailed:
                copy := ev
                failed = &copy
                return errStopStream
            default:
                return nil
            }
        })
        lived := time.Since(started)

        if archived != nil {
            c.log().Debug("等待归档完成：收到 archived",
                "task", taskID, "seq", archived.Seq)
            return archived, nil
        }
        if failed != nil {
            err := fmt.Errorf("%w: task=%s seq=%d payload=%s",
                ErrDependencyFailed, taskID, failed.Seq, string(failed.Payload))
            c.log().Error("等待归档结束：依赖任务失败",
                "task", taskID, "seq", failed.Seq, "cause", err)
            return nil, err
        }
        if ctx.Err() != nil { return nil, ctx.Err() }
        if errors.Is(streamErr, errArchived) {
            // 正常 close 只是线索，不是成功证据；立即回快照找真实 archived。
            c.log().Debug("等待归档连接正常关闭，回查归档事件",
                "task", taskID, "from_seq", fromSeq)
            continue
        }
        if streamErr == nil {
            err := fmt.Errorf("归档事件流无终态却正常结束: task=%s from_seq=%d", taskID, fromSeq)
            c.log().Error("等待归档协议异常", "task", taskID, "cause", err)
            return nil, err
        }
        if isPermanent(streamErr) {
            c.log().Error("等待归档失败：事件流永久错误",
                "task", taskID, "from_seq", fromSeq, "cause", streamErr)
            return nil, streamErr
        }
        c.log().Debug("等待归档事件流暂时断开，退避重连",
            "task", taskID, "attempt", attempt, "from_seq", fromSeq,
            "backoff", backoff, "cause", streamErr)
        if err := waitArchivedRetry(ctx, &backoff,
            c.wsInitialBackoff, c.wsMaxBackoff,
            lived >= c.wsStableAfter); err != nil {
            return nil, err
        }
    }
}
```

`waitArchivedRetry` 实现为以下确定逻辑：健康连接先把 backoff 复位为 initial（不能硬编码包级常量，以保留测试注入）、ctx 可取消等待、非健康失败后倍增并封顶。

```go
func waitArchivedRetry(ctx context.Context, backoff *time.Duration,
    initial, max time.Duration, healthy bool) error {
    if healthy { *backoff = initial }
    delay := *backoff
    timer := time.NewTimer(delay)
    defer timer.Stop()
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-timer.C:
    }
    if !healthy {
        next := delay * 2
        if next > max { next = max }
        *backoff = next
    }
    return nil
}
```

- [ ] **Step 8: 加确定性的快照—订阅竞态与重连测试**

竞态测试必须让顺序可控，不能靠 `time.Sleep` 猜：

```go
func TestWaitArchivedReplaysArchiveBetweenSnapshotAndSubscribe(t *testing.T) {
    snapshotRead := make(chan struct{})
    allowWS := make(chan struct{})
    archived := proto.Event{Seq: 11, TaskID: "t1", Type: proto.EventTypeArchived,
        Payload: json.RawMessage(`{"note":"race"}`)}
    ts := newWaitArchivedServer(t,
        func() *AttachInfo {
            select { case <-snapshotRead: default: close(snapshotRead) }
            return snapshot("t1", proto.TaskStateRunning,
                proto.Event{Seq: 10, TaskID: "t1", Type: proto.EventTypeProgress})
        },
        func(conn *websocket.Conn, r *http.Request) {
            <-allowWS
            if got := r.URL.Query().Get("from_seq"); got != "10" {
                t.Errorf("from_seq=%s want 10", got)
                return
            }
            writeWSEvent(t, conn, r.Context(), archived)
        })
    go func() { <-snapshotRead; close(allowWS) }()
    got, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
    if err != nil || got.Seq != 11 { t.Fatalf("got=%+v err=%v", got, err) }
}
```

增加三条明确断言：

```go
func TestWaitArchivedReconnectsThenFindsArchived(t *testing.T) {
    var wsCalls atomic.Int32
    archived := proto.Event{Seq: 2, TaskID: "t1", Type: proto.EventTypeArchived}
    ts := newWaitArchivedServer(t,
        func() *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
        func(conn *websocket.Conn, r *http.Request) {
            if wsCalls.Add(1) == 1 {
                _ = conn.Close(websocket.StatusGoingAway, "restart")
                return
            }
            writeWSEvent(t, conn, r.Context(), archived)
        })
    cli := NewWithWSTiming(ts.URL, "", time.Millisecond, 5*time.Millisecond, time.Millisecond)
    got, err := cli.WaitArchived(t.Context(), "t1")
    if err != nil || got.Seq != 2 || wsCalls.Load() < 2 {
        t.Fatalf("got=%+v err=%v wsCalls=%d", got, err, wsCalls.Load())
    }
}

func TestWaitArchivedPermanent401DoesNotRetry(t *testing.T) {
    var calls atomic.Int32
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls.Add(1)
        http.Error(w, "bad token", http.StatusUnauthorized)
    }))
    t.Cleanup(ts.Close)
    _, err := NewWithWSTiming(ts.URL, "bad", time.Millisecond,
        5*time.Millisecond, time.Millisecond).WaitArchived(t.Context(), "t1")
    if err == nil || !isPermanent(err) || calls.Load() != 1 {
        t.Fatalf("err=%v permanent=%v calls=%d", err, isPermanent(err), calls.Load())
    }
}

func TestWaitArchivedRetriesTransientAttachFailure(t *testing.T) {
    var attaches atomic.Int32
    archived := proto.Event{Seq: 4, TaskID: "t1", Type: proto.EventTypeArchived}
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/api/tasks/t1" {
            if attaches.Add(1) == 1 {
                http.Error(w, "temporary", http.StatusInternalServerError)
                return
            }
            _ = json.NewEncoder(w).Encode(snapshot("t1", proto.TaskStateRunning))
            return
        }
        conn, err := websocket.Accept(w, r, nil)
        if err != nil { t.Errorf("accept: %v", err); return }
        defer conn.CloseNow()
        writeWSEvent(t, conn, r.Context(), archived)
    }))
    t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
    cli := NewWithWSTiming(ts.URL, "", time.Millisecond, 5*time.Millisecond, time.Millisecond)
    got, err := cli.WaitArchived(t.Context(), "t1")
    if err != nil || got.Seq != 4 || attaches.Load() != 2 {
        t.Fatalf("got=%+v err=%v attaches=%d", got, err, attaches.Load())
    }
}

func TestWaitArchivedNormalCloseWithoutEventRechecksSnapshot(t *testing.T) {
    var attaches atomic.Int32
    archived := proto.Event{Seq: 3, TaskID: "t1", Type: proto.EventTypeArchived}
    ts := newWaitArchivedServer(t,
        func() *AttachInfo {
            if attaches.Add(1) == 1 { return snapshot("t1", proto.TaskStateRunning) }
            return snapshot("t1", proto.TaskStateCompleted, archived)
        },
        func(conn *websocket.Conn, _ *http.Request) {
            _ = conn.Close(websocket.StatusNormalClosure, "task archived")
        })
    got, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
    if err != nil || got.Seq != 3 || attaches.Load() < 2 {
        t.Fatalf("got=%+v err=%v attaches=%d", got, err, attaches.Load())
    }
}
```

- [ ] **Step 9: Add logging at key points**

- 入口 Debug：`addr`、`task`。
- 每次 Attach 外部调用前 Debug；永久错误 Error；瞬时错误 Debug（已确认的静默门闩契约优先，避免默认 stderr 刷屏）。
- WS 临时断线 Debug 带 `attempt/from_seq/backoff/cause`；永久错误 Error。
- failed / missing archived / 协议异常 Error 带 task、seq、cause。
- archived 成功 Debug 带 task、seq；stdout 仍由 CLI 负责，client 不打印。
- 不逐条记录被跳过的中间事件。

- [ ] **Step 10: Add intent comments**

- `wait_archived.go` 文件头写职责和“不碰 cursor/不改状态”边界。
- 两个导出错误与 `WaitArchived` 写中文 doc comment，包含参数、返回、总 ctx 与 cursor 隔离。
- 正常 close 分支解释“close 不是成功证据，必须回查真实事件”。
- 回调里的内存 `fromSeq` 注释解释为何不能写 cursor。
- HTTP 状态错误类型注释解释为何长驻等待必须区分 4xx/5xx。

- [ ] **Step 11: 运行 client 全量与 race 测试**

Run:

```bash
gofmt -w internal/client/client.go internal/client/wait_archived.go internal/client/wait_archived_test.go
go test -count=1 ./internal/client
go test -race -count=1 ./internal/client
git diff --check
```

Expected: 全部 PASS；race 0；diff check 无输出。

- [ ] **Step 12: 提交 client 闭环**

```bash
git add internal/client/client.go internal/client/wait_archived.go internal/client/wait_archived_test.go
git commit -m "feat(client): 静默等待真实归档事件"
```

---

### Task 3: 给 `handoff wait` 接入 `--until-done` 契约

**Files:**
- Modify: `cmd/wait.go`
- Create: `cmd/wait_until_done_test.go`

**Interfaces:**
- Consumes: Task 2 的 `Client.WaitArchived`、`ErrDependencyFailed`、`ErrArchivedEventMissing`；现有 `ExitTimeout=124`、`notifyEvent`、`TargetEndpoint`。
- Produces: `handoff wait <task> --until-done`，成功 stdout 单行 JSON；内部 `runUntilDone` 与 `writeEventLine`。

- [ ] **Step 1: 写 flag 冲突、成功线格式和失败退出测试**

新测试文件使用 `package cmd` 并写职责/边界文件头。先加无需 WS 的终态快照测试 server：

```go
func terminalWaitServer(t *testing.T, state proto.TaskState,
    events ...proto.Event) *httptest.Server {
    t.Helper()
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/api/tasks/t1" { http.NotFound(w, r); return }
        _ = json.NewEncoder(w).Encode(client.AttachInfo{
            Task: proto.TaskView{Task: proto.Task{ID: "t1", State: state}},
            PendingTickets: []proto.Ticket{}, RecentEvents: events,
        })
    }))
    t.Cleanup(ts.Close)
    return ts
}

func runWaitUntilDoneCLI(t *testing.T, serverURL string,
    extraArgs ...string) (stdout, stderr string, err error) {
    t.Helper()
    resetFlags(t)
    addr := strings.TrimPrefix(serverURL, "http://")
    configPath = writeTestConfig(t,
        "listen: \""+addr+"\"\ntoken: \"test-token\"\n")
    targetName = ""
    rootCmd.PersistentFlags().Lookup("agentd").Changed = false
    waitUntilDone, followFlag, notifyFlag, waitTimeout = false, false, false, 0
    t.Cleanup(func() {
        waitUntilDone, followFlag, notifyFlag, waitTimeout = false, false, false, 0
        rootCmd.SetArgs(nil)
        rootCmd.SetOut(nil)
        rootCmd.SetErr(nil)
    })

    args := []string{"wait", "t1", "--until-done"}
    args = append(args, extraArgs...)
    rootCmd.SetArgs(args)
    var out, errOut bytes.Buffer
    rootCmd.SetOut(&out)
    rootCmd.SetErr(&errOut)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    err = ExecuteContext(ctx)
    return out.String(), errOut.String(), err
}
```

加入：

```go
func TestWaitUntilDoneRejectsFollowBeforeNetwork(t *testing.T) {
    resetFlags(t)
    followFlag, waitUntilDone = true, true
    t.Cleanup(func() { followFlag, waitUntilDone = false, false })
    err := waitCmd.RunE(waitCmd, []string{"t1"})
    if err == nil || !strings.Contains(err.Error(), "--follow") ||
        !strings.Contains(err.Error(), "--until-done") {
        t.Fatalf("应在网络前拒绝冲突 flag: %v", err)
    }
}

func TestWaitUntilDoneOutputsExactlyArchivedJSON(t *testing.T) {
    archived := proto.Event{Seq: 21, TaskID: "t1", Type: proto.EventTypeArchived,
        Payload: json.RawMessage(`{"note":"上游完成"}`)}
    ts := terminalWaitServer(t, proto.TaskStateCompleted, archived)
    stdout, stderr, err := runWaitUntilDoneCLI(t, ts.URL)
    if err != nil || ExitCode(err) != 0 { t.Fatalf("err=%v stderr=%q", err, stderr) }
    if strings.Count(strings.TrimSpace(stdout), "\n") != 0 {
        t.Fatalf("stdout 必须严格一行: %q", stdout)
    }
    var got proto.Event
    if err := json.Unmarshal([]byte(stdout), &got); err != nil { t.Fatalf("json: %v", err) }
    if got.Seq != 21 || got.Type != proto.EventTypeArchived ||
        string(got.Payload) != `{"note":"上游完成"}` {
        t.Fatalf("got=%+v", got)
    }
}

func TestWaitUntilDoneFailedHasEmptyStdout(t *testing.T) {
    ts := terminalWaitServer(t, proto.TaskStateFailed)
    stdout, _, err := runWaitUntilDoneCLI(t, ts.URL)
    if !errors.Is(err, client.ErrDependencyFailed) || ExitCode(err) != ExitFailure || stdout != "" {
        t.Fatalf("err=%v code=%d stdout=%q", err, ExitCode(err), stdout)
    }
}

func TestWaitUntilDoneMissingArchivedFails(t *testing.T) {
    ts := terminalWaitServer(t, proto.TaskStateCompleted)
    stdout, _, err := runWaitUntilDoneCLI(t, ts.URL)
    if !errors.Is(err, client.ErrArchivedEventMissing) || ExitCode(err) != ExitFailure || stdout != "" {
        t.Fatalf("err=%v code=%d stdout=%q", err, ExitCode(err), stdout)
    }
    if !strings.Contains(err.Error(), "升级") || !strings.Contains(err.Error(), "archived") {
        t.Fatalf("错误缺处置方向: %v", err)
    }
}
```

- [ ] **Step 2: 写总超时与写出错误测试**

```go
func TestWaitUntilDoneTimeoutIsTotalDespiteProgress(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/api/tasks/t1" {
            _ = json.NewEncoder(w).Encode(client.AttachInfo{
                Task: proto.TaskView{Task: proto.Task{ID: "t1", State: proto.TaskStateRunning}},
                PendingTickets: []proto.Ticket{}, RecentEvents: []proto.Event{},
            })
            return
        }
        if r.URL.Path != "/ws/events" { http.NotFound(w, r); return }
        conn, err := websocket.Accept(w, r, nil)
        if err != nil { return }
        defer conn.CloseNow()
        ticker := time.NewTicker(10 * time.Millisecond)
        defer ticker.Stop()
        var seq int64
        for range ticker.C {
            seq++
            b, _ := json.Marshal(proto.Event{Seq: seq, TaskID: "t1",
                Type: proto.EventTypeProgress})
            if err := conn.Write(r.Context(), websocket.MessageText, b); err != nil { return }
        }
    }))
    t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
    started := time.Now()
    stdout, _, err := runWaitUntilDoneCLI(t, ts.URL, "--timeout", "120ms")
    if ExitCode(err) != ExitTimeout || stdout != "" {
        t.Fatalf("err=%v code=%d stdout=%q", err, ExitCode(err), stdout)
    }
    if elapsed := time.Since(started); elapsed > 2*time.Second {
        t.Fatalf("progress 错误续命，elapsed=%v", elapsed)
    }
}

type failingWriter struct{}
func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteEventLinePropagatesWriterError(t *testing.T) {
    err := writeEventLine(failingWriter{}, &proto.Event{Type: proto.EventTypeArchived})
    if err == nil || !strings.Contains(err.Error(), "write failed") {
        t.Fatalf("写出失败必须上抛: %v", err)
    }
}
```

- [ ] **Step 3: 运行 CLI 测试确认 RED**

Run:

```bash
go test -count=1 ./cmd -run 'TestWaitUntilDone|TestWriteEventLine'
```

Expected: FAIL，包含 `undefined: waitUntilDone`、`undefined: writeEventLine` 或命令不认识 `--until-done`。

- [ ] **Step 4: 增加 flag、冲突校验和专用运行分支**

在包级 flag 区加入：

```go
// waitUntilDone 开启 B67 依赖门闩：静默忽略中间事件，只等真实 archived。
var waitUntilDone bool
```

在 `RunE` 读取 task 后、`TargetEndpoint` 前校验：

```go
if followFlag && waitUntilDone {
    return fmt.Errorf("--follow 与 --until-done 不能同时使用：前者交付审核事件，后者只等归档")
}
```

初始化日志后，优先进入：

```go
if waitUntilDone {
    return runUntilDone(cmd, taskID, addr, token)
}
if followFlag {
    return runFollow(cmd, taskID, addr, token)
}
```

注册 flag 并更新 timeout help：

```go
waitCmd.Flags().BoolVar(&waitUntilDone, "until-done", false,
    "静默等待 handoff done；成功只输出 archived，failed 立即失败")
```

- [ ] **Step 5: 实现总时限、退出码和严格单行输出**

```go
func runUntilDone(cmd *cobra.Command, taskID, addr, token string) error {
    ctx := cmd.Context()
    if waitTimeout > 0 {
        var cancel context.CancelFunc
        ctx, cancel = context.WithTimeout(ctx, waitTimeout)
        defer cancel()
    }
    slog.Debug("等待依赖任务归档", "task", taskID, "addr", addr,
        "timeout", waitTimeout.String())
    ev, err := client.New(addr, token).WaitArchived(ctx, taskID)
    if err != nil {
        switch {
        case waitTimeout > 0 && errors.Is(err, context.DeadlineExceeded):
            slog.Error("等待依赖任务归档超时",
                "task", taskID, "timeout", waitTimeout.String(), "cause", err)
            return &exitCodeError{code: ExitTimeout, err: fmt.Errorf(
                "等待任务 %s 归档超时（%s）", taskID, waitTimeout)}
        case errors.Is(err, client.ErrDependencyFailed):
            slog.Error("依赖任务失败，归档门闩终止", "task", taskID, "cause", err)
            return fmt.Errorf("依赖任务 %s 已失败: %w", taskID, err)
        case errors.Is(err, client.ErrArchivedEventMissing):
            slog.Error("任务已完成但缺归档事件", "task", taskID, "cause", err)
            return fmt.Errorf("任务 %s 已归档但缺 archived 事件；请升级对端 agentd 或检查事件数据: %w",
                taskID, err)
        default:
            slog.Error("等待依赖任务归档失败", "task", taskID, "cause", err)
            return err
        }
    }
    if notifyFlag { notifyEvent(ev) }
    if err := writeEventLine(cmd.OutOrStdout(), ev); err != nil {
        slog.Error("输出归档事件失败", "task", taskID, "seq", ev.Seq, "cause", err)
        return err
    }
    slog.Debug("依赖任务已归档", "task", taskID, "seq", ev.Seq)
    return nil
}

func writeEventLine(w io.Writer, ev *proto.Event) error {
    b, err := json.Marshal(ev)
    if err != nil { return fmt.Errorf("序列化事件: %w", err) }
    if _, err := fmt.Fprintln(w, string(b)); err != nil {
        return fmt.Errorf("写出事件: %w", err)
    }
    return nil
}
```

把一次性 wait 与 follow 的事件输出也改用 `writeEventLine`，消除原有 `Fprintln` 写错被忽略的问题；现有 stdout 线格式不变。

- [ ] **Step 6: Add logging at key points**

- `runUntilDone` 入口 Debug 带 task/addr/timeout。
- timeout、failed、missing archived、其他失败均 Error，带 task/cause；missing 带处置方向。
- stdout 写失败 Error 带 task/seq/cause。
- 成功 Debug 带 task/seq；机器可读成功结果仍是 stdout 事件。
- 不记录中间事件，不启动 follow 的 stalltimeout 告警 goroutine。

- [ ] **Step 7: Add intent comments**

- 更新 `cmd/wait.go` 文件头职责，增加 `--until-done` 总时限与 cursor 隔离边界。
- `waitUntilDone`、`runUntilDone`、`writeEventLine` 写中文注释。
- 冲突校验注释解释两种模式消费契约相反，不能组合。
- timeout 注释解释为何 progress 不能续命。

- [ ] **Step 8: 运行 cmd 全量与既有 wait 回归**

Run:

```bash
gofmt -w cmd/wait.go cmd/wait_until_done_test.go
go test -count=1 ./cmd
go test -race -count=1 ./cmd
git diff --check
```

Expected: 新旧 wait 测试全部 PASS；race 0；diff check 无输出。

- [ ] **Step 9: 提交 CLI 接入**

```bash
git add cmd/wait.go cmd/wait_until_done_test.go
git commit -m "feat(cmd): wait 增加 until-done 归档门闩"
```

---

### Task 4: 同步文档、做变异验证与全仓验收

**Files:**
- Modify: `README.md`
- Modify: `skills/handoff/SKILL.md`
- Modify after verified implementation: `docs/superpowers/backlog.md`
- Verify: all files from Tasks 1–3

**Interfaces:**
- Consumes: Task 3 的最终 CLI 契约。
- Produces: 操作者与 agent 可执行说明、竞态测试有效性证据、全仓验证证据；backlog 仍在实现阶段保持 `🔨 doing`，只有验收完成后才由收尾流程改 done。

- [ ] **Step 1: 在 README 加“等待另一个任务归档”小节**

必须包含以下命令与语义，不另造别名：

```markdown
### 等待另一个任务归档

`handoff wait <完整任务 ID> --until-done --timeout 3h` 是依赖门闩：等待期间
不输出 question、permission_request、completed 等中间事件，也不推进审核者的
cursor。只有审核者执行 `handoff done` 后，它才输出一行 `archived` 事件并退出 0。

- `0`：已归档，stdout 是原始 archived JSON（payload.note 为归档说明）
- `124`：总等待时间到，任务尚未归档
- `1`：依赖任务 failed、鉴权/任务 ID/协议错误

它不会自动派发后续任务。原任务仍必须由自己的审核者处理工单、审阅 completed，
并显式 done；不要用本命令替代 `wait --follow` 的审核订阅。
```

- [ ] **Step 2: 在 handoff skill 的 agent 会话等待章节补依赖门闩**

紧跟现有 Monitor 说明增加：

```markdown
### 另一个会话只等本任务归档

后续会话依赖当前任务真正审核归档时，使用一条持久 monitor：

    handoff wait <完整 task-id> --until-done --timeout 3h

它不会消费审核者 cursor，也不会把 question/permission/completed 送进后续会话；
只在 `handoff done` 产生 `archived` 后输出一次。退出 0 才能开后续工作，124 表示
本轮等待到期，其他非零表示依赖失败或配置错误。它只负责唤醒，不自动 dispatch。
```

同时在事件分诊表加入 `archived`：仅 `--until-done` 把它作为成功信号；普通 follow 收到后也会随连接正常结束。

- [ ] **Step 3: Add logging at key points（全功能机械自检）**

逐项用 `rg` 和代码审阅确认：

```bash
rg -n '等待归档|归档门闩|无 cursor 事件流' internal/client cmd
rg -n 'fmt\.Printf|log\.Printf|console\.log' internal/client/wait_archived.go cmd/wait.go
```

必须确认：

- 每次外部 Attach/WS 边界有 Debug 或底层已有结构化日志；
- 永久失败、failed、missing archived、协议错误带 task/seq/cause；
- 瞬时重试只 Debug，不污染静默等待；
- 成功有 stdout archived + Debug task/seq；
- 第二条 `rg` 对本次新增路径无新增匹配（既有匹配如有，必须证明不在本次代码）。

- [ ] **Step 4: Add intent comments（全功能机械自检）**

确认：

- `wait_archived.go`、两个新测试文件有职责/边界文件头；
- `StreamEventsOnce`、`WaitArchived`、两个导出错误有中文注释；
- 正常 close 回查、快照—订阅窗口、内存水位不写 cursor 均有 why 注释；
- `cmd/wait.go` 文件头包含 `--until-done` 的总时限与不自动编排边界。

- [ ] **Step 5: 运行竞态测试的 GREEN 基线**

Run:

```bash
go test -count=1 ./internal/client -run '^TestWaitArchivedReplaysArchiveBetweenSnapshotAndSubscribe$'
```

Expected: PASS。

- [ ] **Step 6: 做回归钉变异验证**

临时在 `WaitArchived` 调 `StreamEventsOnce` 前把局部水位推进一格：

```go
fromSeq++ // TEMP mutation: 错误跳过紧邻 archived
```

Run:

```bash
go test -count=1 ./internal/client -run '^TestWaitArchivedReplaysArchiveBetweenSnapshotAndSubscribe$'
```

Expected: FAIL（超时或拿不到 seq=11）。若仍 PASS，测试没有真正覆盖快照—订阅窗口，必须先修测试。

立即用 `apply_patch` 删除这行临时 mutation，再运行同一命令，Expected: PASS。禁止把 mutation 提交；禁止用 `git checkout --` 覆盖其他人的改动。

- [ ] **Step 7: 跑完整验证矩阵**

Run:

```bash
gofmt -w internal/client/client.go internal/client/wait_archived.go internal/client/wait_archived_test.go cmd/wait.go cmd/wait_until_done_test.go
git diff --check
go test -count=1 ./internal/client ./cmd
go test -race -count=1 ./internal/client ./cmd
go test -count=1 ./...
go vet ./...
go build ./...
```

Expected: `gofmt` 后无额外意外 diff；`git diff --check` 无输出；所有 test/race/vet/build 退出 `0`。

- [ ] **Step 8: 检查改动边界与提交文档**

Run:

```bash
git diff --stat main...HEAD
git diff --name-only main...HEAD
```

Expected: 只出现 File Map 中列出的 client/cmd/docs/skill 文件，以及本分支已提交的 B67 spec/plan；不得出现 agentd/store/proto 或 Web 文件。

```bash
git add README.md skills/handoff/SKILL.md
git commit -m "docs: 说明 until-done 依赖门闩"
```

- [ ] **Step 9: 真机验收（隔离 agentd/DataDir）**

按 spec §8.4 执行并保存命令与原始输出：

1. 活任务：挂 `--until-done`，制造 question/permission/completed，确认 stdout 始终空；`done --note` 后只出现 archived。
2. 迟到任务：先 done，后启动门闩，立即返回同一 archived。
3. 失败任务：stop/自然失败后门闩立即退出 `1` 且 stdout 空。
4. 每条前后对 `~/.handoff/cursor-<task>` 做 checksum；必须不存在或字节完全一致。

不得碰生产 7777 或真实 DataDir；隔离实例要使用临时端口与 `mktemp -d` DataDir，完成后只清理本次创建的明确路径。

- [ ] **Step 10: 回填 backlog 证据（仅验收后）**

只修改 `docs/superpowers/backlog.md` 的 B67 行：

- Spec 后追加 `[plan](plans/2026-08-12-b67-wait-until-done.md)`；
- 验收列写入 test/race/vet/build 与三条真机原始结论；
- 状态按证据写 `✅ done(已验)`；若真机证据缺失，必须写 `✅ done(未验)`，不得伪造。

```bash
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B67 记录验收证据"
```

---

## Final Review Checklist

- [ ] 目标：只等真实 `archived`，没有自动编排或通用事件表达式。
- [ ] 架构：client 持有可靠性，cmd 只做 CLI；agentd/store/proto 未变。
- [ ] cursor：新 client/CLI 测试与真机 checksum 均证明不读写审核者 cursor。
- [ ] 竞态：快照—订阅回归钉做过 RED（mutation）→ GREEN。
- [ ] 输出：成功 stdout 严格一行；失败/超时 stdout 空；人读信息只在 stderr。
- [ ] 退出码：0/124/1 与 spec 一致。
- [ ] 失败：启动前/等待中 failed 都立即返回；completed 不冒充 done。
- [ ] 兼容：completed 缺 archived 明确报错，不合成事件、不死等。
- [ ] 重连：临时错误退避，4xx/1008 永久错误不重试，总 ctx 不被重置。
- [ ] 日志：关键入口、边界、错误、成功均可检索；瞬时循环只 Debug；无 printf 日志。
- [ ] 注释：新文件头、导出方法/错误、竞态与正常 close 分支均解释 why。
- [ ] 验证：gofmt、diff check、client/cmd、race、全仓 test、vet、build 全部有本轮原始输出。
- [ ] 文档：README 与 handoff skill 同步，明确不替代审核订阅。
- [ ] backlog：只有证据齐全后才从 `🔨 doing` 转 done。
