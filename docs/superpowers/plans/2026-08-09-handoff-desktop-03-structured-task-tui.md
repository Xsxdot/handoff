# Handoff Desktop Structured Task TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 OpenCode、Claude Code、Grok 的公开执行现场归一化为六类持久化 TaskFrame，并在桌面提供不依赖 PTY 的统一 TaskTUI，完成审批、拒绝、回答、继续、停止和恢复闭环。

**Architecture:** Adapter 只产统一 `ExecutorEvent`；Manager 仍是 Task/Ticket/审批状态机唯一写入者；Task Recorder 把公开事件、交互和状态写成 per-task 单调 frame，并生成 render snapshot/artifact。桌面只对打开的任务加载 session snapshot + frame stream，按 reducer 构建 UI；所有命令经服务端 command ledger、ticket version 和权威回流裁决。

**Tech Stack:** Go 1.26、SQLite、现有 executor adapters、coder/websocket、Electron Main/Preload、React、Zustand、Zod、Vitest、Playwright。

## Global Constraints

- 计划 01、02 completion gate 全部通过后开始；Task 已有稳定 machine/workspace identity，Workspace 资源不可用门禁已工作。
- TaskTUI 不是 PTY，不创建 shell，不 attach tmux，不解析 ANSI，不按 executor 名分支渲染；桌面没有 OpenCode 原生 TUI 入口、开关或 fallback。
- 现有 `proto.Event` 继续服务 CLI wait/show/attach 唤醒语义；message/activity/file/todo 等高频内容只进 TaskFrame，不能让旧 wait 被高频事件唤醒。
- 不展示或持久化 `thinking_delta`、reasoning part、hidden chain-of-thought。只展示执行者明确标记为用户可见的 assistant text、工具动作和结果。
- 交互和 TaskState 的权威仍是 Manager/Ticket/Task；TaskFrame 是同一事务产生的显示事实，不建第二套审批状态机。
- 命令 UI 不乐观改 Task/Ticket；accepted 后等待 TaskFrame/TaskSummary 回流。并发冲突返回 409 + canonical result。
- 每个任务先写失败测试；完成前补结构化日志、职责/边界头注释、导出项文档和复杂时序原因注释。日志不含 prompt、回答全文、隐藏 reasoning、artifact 全文或 terminal/file 内容。

---

### Task 1: 定义 ExecutorEvent、六类 TaskFrame、artifact 和 command 持久化契约

**Files:**
- Create: `internal/taskview/model.go`
- Create: `internal/taskview/model_test.go`
- Create: `internal/taskview/payloads.go`
- Create: `internal/taskview/reducer.go`
- Create: `internal/taskview/reducer_test.go`
- Create: `internal/desktopapi/tasks.go`
- Create: `internal/desktopapi/tasks_test.go`
- Create: `internal/desktopapi/task_assembler.go`
- Create: `internal/desktopapi/task_assembler_test.go`
- Create: `internal/desktopapi/testdata/task-frame.json`
- Create: `internal/desktopapi/testdata/task-session.json`
- Create: `internal/desktopapi/testdata/task-command.json`
- Create: `internal/store/task_frames.go`
- Create: `internal/store/task_frames_test.go`
- Create: `internal/store/task_commands.go`
- Create: `internal/store/task_commands_test.go`
- Modify: `internal/store/desktop_schema.go`
- Modify: `internal/executor/executor.go`
- Create: `internal/executor/executor_test.go`
- Modify: `internal/proto/proto.go`

**Interfaces:**
- Consumes: existing `executor.AdapterEvent`, `proto.Task/TaskState/Ticket`, Plan 01 desktop DTO conventions, and SQLite store lifecycle.
- Produces: typed `executor.ExecutorEvent`, `taskview.Frame`, six payload types, `taskview.Apply(ViewState, Frame) (ViewState, ReduceResult, error)`, artifact metadata, task snapshots, task-command records, `desktopapi.TaskAssembler`, and golden desktop JSON contracts.

- [ ] 写 model 红灯测试，逐类 round-trip：`message`、`activity`、`file_change`、`todo_snapshot`、`interaction`、`task_state`；未知 kind/version 拒绝；`task_seq` 从 1 每任务单调；重复 event ID 幂等；跨任务 seq 独立。

- [ ] 写 reducer 红灯测试：message delta 追加到稳定 message ID；activity started→updated→completed/failed 原位更新；todo snapshot 原子替换；interaction requested→resolved 保留 canonical decision；task state 更新；重复/旧序号不改变结果；gap 返回 `NeedsSnapshot`。

- [ ] 写 store 红灯测试：frame + task mutation 同事务；snapshot 带 through_task_seq；artifact metadata 不把内容塞入 frame；command ID 唯一；相同 ID/相同 payload 返回现有记录；相同 ID/不同 payload 返回 COMMAND_CONFLICT；ticket version CAS 只有一个赢家。

- [ ] 运行红灯：

  ```bash
  go test ./internal/taskview ./internal/desktopapi ./internal/store ./internal/executor -run 'TaskFrame|Reducer|TaskCommand|TicketVersion|ExecutorEvent'
  ```

- [ ] 用强类型定义统一事件，不继续扩散 `Type string + Text`：

  ```go
  type ExecutorEvent struct {
      EventID string
      Kind ExecutorEventKind
      SessionID string
      Message *MessageEvent
      Activity *ActivityEvent
      FileChange *FileChangeEvent
      Todo *TodoSnapshotEvent
      Permission *PermissionEvent
      Question *QuestionEvent
      Result *Result
  }
  ```

  迁移期可保留 `type AdapterEvent = ExecutorEvent` 源码兼容别名，但所有 adapter 在本计划结束前必须改用 typed payload，别名不得成为长期双格式。

- [ ] TaskFrame 精确定义：

  ```go
  type Frame struct {
      EventID string `json:"event_id"`
      TaskID string `json:"task_id"`
      TaskSeq int64 `json:"task_seq"`
      Kind Kind `json:"kind"`
      PayloadVersion int `json:"payload_version"`
      Payload json.RawMessage `json:"payload"`
      ArtifactRef *string `json:"artifact_ref,omitempty"`
      CreatedAt time.Time `json:"created_at"`
  }

  type ViewState struct {
      TaskID string
      ThroughTaskSeq int64
      Messages map[string]MessagePayload
      Activities map[string]ActivityPayload
      FileChanges map[string]FileChangePayload
      Todos []TodoItem
      Interactions map[string]InteractionPayload
      TaskState TaskStatePayload
  }

  type ReduceResult struct {
      Applied bool
      NeedsSnapshot bool
  }

  func Apply(state ViewState, frame Frame) (ViewState, ReduceResult, error)
  ```

- [ ] Payload 必须有稳定 ID：Message `message_id`；Activity `activity_id/type/phase/title/summary/duration_ms`；FileChange `path/change/additions/deletions/diff_ref`；TodoSnapshot `items[{id,title,state}]`；Interaction `ticket_id/kind/phase/version/prompt/options/resolution`；TaskState `state/reason/actions`。

- [ ] `tickets` 增加 `version INTEGER NOT NULL DEFAULT 1`；`task_frames`、`task_snapshots`、`artifacts`、`task_commands` 使用幂等 migration。`task_commands` 保存 payload hash，不保存用户文本明文到日志；数据库仍可按业务需要保存 command payload。

- [ ] 修改 `proto.TaskState` 的 stalled 迁移与 CLI JSON 测试，确保计划 01 定义的状态现在会由 TaskFrame 使用；不得删除 `EventTypeStalled`。

- [ ] 扩展桌面 Zod contract 并用 Go golden fixture 双向验证。公开 schema 不含 executor raw event 或 reasoning 字段。

- [ ] `desktopapi.TaskAssembler` 先纯转换 Task/session/frame/artifact 的领域类型与 wire DTO，提供 `ToSession`、`ToFrame`、`ToArtifactMetadata`；assembler 测试覆盖六类 payload、可选 artifact 与未知版本，handler/peer adapter 禁止内联 JSON 字段映射。command 转换在 Task 4 定义 command 类型后加入。

- [ ] 新文件补职责/边界头注释、导出文档和“稀疏 Event 与高频 Frame 分流”“强类型 payload 防 renderer 猜 executor”的原因注释。Store leaf 只返回上下文错误；调用层打结构化日志。

- [ ] 运行绿灯：

  ```bash
  gofmt -w internal/taskview internal/desktopapi/tasks.go internal/desktopapi/tasks_test.go internal/desktopapi/task_assembler.go internal/desktopapi/task_assembler_test.go internal/store/task_frames.go internal/store/task_frames_test.go internal/store/task_commands.go internal/store/task_commands_test.go internal/store/desktop_schema.go internal/executor/executor.go internal/executor/executor_test.go internal/proto/proto.go
  go test ./internal/taskview ./internal/desktopapi ./internal/store ./internal/executor ./internal/proto
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/taskview internal/desktopapi internal/store internal/executor/executor.go internal/executor/executor_test.go internal/proto/proto.go
  git commit -m "feat: define structured task frame contracts"
  ```

### Task 2: 实现 Task Recorder、render snapshot、artifact 与 frame stream

**Files:**
- Create: `internal/taskview/recorder.go`
- Create: `internal/taskview/recorder_test.go`
- Create: `internal/taskview/snapshot.go`
- Create: `internal/taskview/snapshot_test.go`
- Create: `internal/artifact/store.go`
- Create: `internal/artifact/store_test.go`
- Create: `internal/artifact/reader.go`
- Create: `internal/artifact/reader_test.go`
- Create: `internal/agentd/task_frame_stream.go`
- Create: `internal/agentd/task_frame_stream_test.go`
- Create: `internal/agentd/task_artifact_server.go`
- Create: `internal/agentd/task_artifact_server_test.go`
- Modify: `internal/agentd/server.go`
- Modify: `cmd/agentd.go`

**Interfaces:**
- Consumes: Task 1 `ExecutorEvent`, frame/reducer/store types, Task identity, and existing executor event channels.
- Produces: `Recorder.RecordExecutorEvent/RecordInteraction/RecordTaskState/Flush/Session`, snapshot compaction, `artifact.Store.Put/OpenRange`, task session/artifact HTTP routes, and frame WS replay by `task_seq`.

- [ ] 写 Recorder 红灯测试：同 event ID 只写一次；message delta 按 100ms 或 8KiB 批量落盘而非 token 一帧；flush 保持顺序；大 activity output/diff 存 artifact，只在 frame 写摘要/ref；reasoning event 被丢弃并只记计数。

- [ ] 写 snapshot 红灯测试：每 200 frame 或显式终态生成快照；snapshot reducer 结果等于从 seq 1 replay；写 snapshot 与 through seq 一致；进程重启从最新 snapshot + 后续 frame 恢复。

- [ ] 写 artifact 安全测试：artifact ID 绑定 task；跨 task 读拒绝；offset/limit 有界；路径不可越 task artifact root；二进制 MIME/size 正确；日志不含内容。

- [ ] 写 WS 红灯测试：session snapshot 后 subscribe after through seq 无窗口丢失；frame 按 task_seq；只允许打开任务单独订阅；慢客户端断开并可 replay；未知 task 立即 RESOURCE_NOT_FOUND。

- [ ] 运行红灯：

  ```bash
  go test ./internal/taskview ./internal/artifact ./internal/agentd -run 'Recorder|Snapshot|Artifact|TaskFrameStream'
  ```

- [ ] Recorder 公开端口：

  ```go
  type Recorder interface {
      RecordExecutorEvent(context.Context, string, executor.ExecutorEvent) error
      RecordInteraction(context.Context, InteractionFact) (Frame, error)
      RecordTaskState(context.Context, TaskStateFact) (Frame, error)
      Flush(context.Context, string) error
      Session(context.Context, string) (SessionSnapshot, error)
  }
  ```

  Manager 权威事务将在 Task 3 使用专门的 store transaction；Recorder 不可自行修改 Ticket/Task。

- [ ] message batching 每任务串行，使用有界 timer/buffer；任务关闭/终态/agentd shutdown 必须 flush。activity 完整 stdout/stderr 超阈值后写 artifact，frame 只留 title/summary/exit/duration。

- [ ] Artifact 存 `DataDir/tasks/<task-id>/artifacts/<artifact-id>`，metadata 入 SQLite；写入先临时文件后 rename；读 API 支持 `offset/limit`，单次默认 256KiB、上限 1MiB。

- [ ] 注册：

  ```text
  GET /v1/tasks/{task_id}/session
  GET /v1/tasks/{task_id}/frames?after=<task_seq>  (WebSocket)
  GET /v1/tasks/{task_id}/artifacts/{artifact_id}?offset=<n>&limit=<n>
  ```

- [ ] session 响应包含 task projection、pending interactions、render snapshot、through_task_seq。renderer 不需读取 executor 原始日志。

- [ ] 日志记录 recorder batch/frame kind/count/task seq、snapshot through seq、artifact id/size/range、stream after/replay/slow close；reasoning 只记 `dropped_kind` 和数量，不记文本。

- [ ] 补齐职责头注释、导出文档和 batching/artifact/snapshot 无窗口语义的原因注释。

- [ ] 运行绿灯与 race：

  ```bash
  gofmt -w internal/taskview internal/artifact internal/agentd/task_frame_stream.go internal/agentd/task_frame_stream_test.go internal/agentd/task_artifact_server.go internal/agentd/task_artifact_server_test.go internal/agentd/server.go cmd/agentd.go
  go test -race ./internal/taskview ./internal/artifact ./internal/agentd
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/taskview internal/artifact internal/agentd cmd/agentd.go
  git commit -m "feat: record and replay structured task sessions"
  ```

### Task 3: 把 Manager/Ticket/TaskState 接到同一权威 TaskFrame 事务

**Files:**
- Create: `internal/store/task_facts.go`
- Create: `internal/store/task_facts_test.go`
- Modify: `internal/agentd/manager.go`
- Modify: `internal/agentd/manager_test.go`
- Modify: `internal/agentd/integration_test.go`
- Modify: `internal/agentd/watchdog.go`
- Modify: `internal/agentd/watchdog_test.go`
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`
- Modify: `internal/taskview/recorder.go`
- Modify: `cmd/agentd.go`

**Interfaces:**
- Consumes: Task 2 Recorder, existing Manager/Ticket state machine, watchdog, and CLI Event hub.
- Produces: transactional `CreateTaskFact`, `TransitTaskFact`, `OpenInteractionFact`, `ResolveInteractionFact`, and separate publications for CLI Event, TaskFrame, and TaskSummary.

- [ ] 写事务红灯测试：Task create 同时产生 pending task_state frame；状态迁移同时更新 Task、TaskSummary machine event 和 task_state frame；创建 Ticket 同时产生 requested interaction frame/version=1；回答 CAS 同时产生 resolved frame/version=2；任一写失败全部 rollback。

- [ ] 写 Manager 红灯测试：message/activity/file/todo 只交 Recorder，不追加 proto.Event；permission/question 仍创建 Ticket + proto.Event + waiting 状态，同时产生 interaction frame；result 仍进 waiting_review，同时产生 task_state frame；CLI wait 仍只被原事件唤醒。

- [ ] 写 watchdog 红灯测试：机器可达但超时的 Task 真正迁移到 `stalled` 并发 task_state frame；机器 unavailable 不触发 Task state；resume/stop 可从 stalled 收敛；同一次空闲不重复 frame/event。

- [ ] 运行红灯：

  ```bash
  go test ./internal/store ./internal/agentd ./internal/taskview -run 'TaskFact|InteractionFrame|ManagerRecords|StalledState'
  ```

- [ ] `task_facts.go` 提供 application-level atomic methods：

  ```go
  CreateTaskFact(ctx, task, pendingPayload) (taskFrame, machineEvent, error)
  TransitTaskFact(ctx, taskID, to, reason) (taskFrame, machineEvent, error)
  OpenInteractionFact(ctx, taskID, ticket, payload) (proto.Event, taskFrame, machineEvent, error)
  ResolveInteractionFact(ctx, ticketID, expectedVersion, answer, payload) (taskFrame, machineEvent, error)
  ```

  这些方法做持久化原子性，不做审批决策；合法迁移仍由 `proto.CanTransit` 防护。

- [ ] 修改 Manager 的 `handleEvent` 使用 `ev.Kind`。公开 frame 事件先由 Recorder 持久化；permission/question/result 走现有 Manager 决策，但持久化改用 task fact transaction，移除“先写一张表再补另一张”的缝隙。

- [ ] `transit` 使用 `TransitTaskFact`，返回后发布 CLI Event/TaskFrame/TaskSummary 对应 hub。Hub 仍分为三个独立通道，不能把 TaskFrame 发布到旧 Hub。

- [ ] watchdog 的 stalled 是真实 TaskState；只在 owner machine connected 且 executor 应工作时迁移。Machine 状态来自 controlplane lookup，不能把全机断线误写成 stalled。

- [ ] 日志覆盖 event kind、fact transaction、ticket/version、from/to state、frame seq、machine event seq；permission/question 只记录长度和稳定 ID，不记录全文。

- [ ] 补齐新文件头、导出文档和“同一事实必须一事务”“三条 hub 不混用”“断机不是 stalled”的原因注释。

- [ ] 运行绿灯与完整旧 CLI 回归：

  ```bash
  gofmt -w internal/store/task_facts.go internal/store/task_facts_test.go internal/agentd/manager.go internal/agentd/manager_test.go internal/agentd/watchdog.go internal/agentd/watchdog_test.go internal/store/store.go internal/store/store_test.go internal/taskview/recorder.go cmd/agentd.go
  go test -race ./internal/store ./internal/agentd ./internal/taskview
  go test ./internal/client ./cmd
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/store internal/agentd internal/taskview cmd/agentd.go
  git commit -m "feat: publish task state and interaction facts atomically"
  ```

### Task 4: 实现幂等 Task command service 和 Ticket version 冲突

**Files:**
- Create: `internal/taskcommand/service.go`
- Create: `internal/taskcommand/service_test.go`
- Create: `internal/agentd/task_command_server.go`
- Create: `internal/agentd/task_command_server_test.go`
- Modify: `internal/agentd/server.go`
- Modify: `internal/agentd/manager.go`
- Modify: `internal/agentd/manager_test.go`
- Modify: `internal/store/task_commands.go`
- Modify: `internal/store/task_facts.go`
- Modify: `internal/desktopapi/task_assembler.go`
- Modify: `internal/desktopapi/task_assembler_test.go`
- Modify: `internal/executor/executor.go`
- Modify: `internal/executor/fake/fake.go`
- Modify: `internal/executor/fake/fake_test.go`
- Modify: `internal/executor/opencode/adapter.go`
- Modify: `internal/executor/opencode/adapter_test.go`
- Modify: `internal/executor/claudecode/adapter.go`
- Modify: `internal/executor/claudecode/adapter_test.go`
- Modify: `internal/executor/grok/adapter.go`
- Modify: `internal/executor/grok/adapter_test.go`
- Modify: `internal/executor/grok/perm.go`
- Modify: `internal/executor/grok/perm_test.go`
- Modify: `cmd/agentd.go`

**Interfaces:**
- Consumes: Task 1 command ledger/ticket version, Task 3 fact transactions, and all concrete `executor.Adapter` implementations.
- Produces: `taskcommand.Command`, `(*taskcommand.Service).Execute(context.Context, taskID, Command) (CommandResult, error)`, `POST /v1/tasks/{task_id}/commands`, and command-aware Adapter `Send/RespondPermission` signatures.

- [ ] 写红灯测试：approve/reject/answer 必须有 ticket ID + expected version；两个并发命令只有一个 CAS 成功且 adapter 调一次；败者 409 COMMAND_CONFLICT 含 canonical ticket/command；相同 command ID/相同 payload 重试返回原结果；相同 ID/不同 payload 冲突。

- [ ] 写 continue/stop/resume 红灯测试：服务端每 Task 串行 dispatch；同 command ID 不重复调用 adapter；错误记录 delivered/failed canonical result；UI 可等待后续 frame；legacy `/reply` 仍工作并与新 command CAS 竞争同一 Ticket。

- [ ] 运行红灯：

  ```bash
  go test ./internal/taskcommand ./internal/agentd ./internal/store ./internal/executor/fake -run 'Command|Conflict|ExactlyOnce|LegacyReply'
  ```

- [ ] 定义请求：

  ```go
  type Command struct {
      CommandID string `json:"command_id"`
      Kind Kind `json:"kind"`
      TicketID string `json:"ticket_id,omitempty"`
      ExpectedVersion *int64 `json:"expected_version,omitempty"`
      Payload json.RawMessage `json:"payload,omitempty"`
  }

  type CommandResult struct {
      CommandID string `json:"command_id"`
      State CommandState `json:"state"`
      CanonicalTicket *proto.Ticket `json:"canonical_ticket,omitempty"`
      CanonicalTask *proto.Task `json:"canonical_task,omitempty"`
  }

  type CommandState string

  const (
      CommandAccepted CommandState = "accepted"
      CommandDelivered CommandState = "delivered"
      CommandFailed CommandState = "failed"
  )
  ```

  Kind 仅允许 `approve|reject|answer|continue|stop|resume`。

- [ ] `Service.Execute` 先验证/保留 command ledger，再按 Task 串行处理。Ticket command 的 CAS 与 answered fact 同事务；成功获得权威权后才调用 Manager relay。并发 loser 不调用 executor。

- [ ] command 映射固定：approve/reject → `RespondPermission`；answer/continue → `Send`；stop → `Stop`；resume → `Manager.ResumeTask`。每条路径都先校验 Task state/action，错误返回 canonical Task/Ticket；不得把 resume 偷换成新 dispatch。

- [ ] `TaskAssembler` 增加 `ToCommand(TaskCommandRequest) (taskcommand.Command, error)` 与 `ToCommandResult(taskcommand.CommandResult) TaskCommandResponse`；只转换 wire/领域字段，合法 action、Ticket version 和 Task state 仍由 `taskcommand.Service` 判断。assembler test 覆盖六种 kind、payload 原样、canonical Task/Ticket 和 conflict。

- [ ] 将 adapter 指令边界显式携带 command ID：`Send(ctx, taskID, commandID, text)`、`RespondPermission(ctx, taskID, commandID, permID, decision)`；各 adapter 在进程内维护 bounded delivered command set，并以持久化 command ledger 作为网络重试事实。不要把 command ID 拼进用户文本。

- [ ] 注册 `POST /v1/tasks/{task_id}/commands`。accepted 返回 `202` + command projection；同步 CAS 冲突返回 409；renderer 不从 HTTP 202 直接改 Task state。

- [ ] legacy `/api/tasks/{id}/reply` 读取当前 Ticket version 后走同一 service，使用服务端生成 command ID；旧 wire 不要求 expected version，但底层仍只有一个赢家。

- [ ] 日志记录 command/task/kind/ticket/version/accepted/conflict/delivery result，不记录 answer/continue 文本，只记长度和 hash 前缀。

- [ ] 补齐职责头注释、导出文档和“先取得 ticket CAS 权威再调 executor”“HTTP 202 不等于完成”的原因注释。

- [ ] 运行绿灯与 race：

  ```bash
  gofmt -w internal/taskcommand internal/agentd/task_command_server.go internal/agentd/task_command_server_test.go internal/agentd/server.go internal/agentd/manager.go internal/agentd/manager_test.go internal/agentd/integration_test.go internal/store/task_commands.go internal/store/task_facts.go internal/desktopapi/task_assembler.go internal/desktopapi/task_assembler_test.go internal/executor/executor.go internal/executor/fake/fake.go internal/executor/fake/fake_test.go internal/executor/opencode/adapter.go internal/executor/opencode/adapter_test.go internal/executor/claudecode/adapter.go internal/executor/claudecode/adapter_test.go internal/executor/grok/adapter.go internal/executor/grok/adapter_test.go internal/executor/grok/perm.go internal/executor/grok/perm_test.go cmd/agentd.go
  go test -race ./internal/taskcommand ./internal/agentd ./internal/store ./internal/desktopapi ./internal/executor/...
  go test ./internal/client ./cmd
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/taskcommand internal/agentd internal/store internal/desktopapi/task_assembler.go internal/desktopapi/task_assembler_test.go internal/executor cmd/agentd.go
  git commit -m "feat: execute idempotent task commands with ticket versions"
  ```

### Task 5: 归一化 OpenCode 真实事件为六类公开 ExecutorEvent

**Files:**
- Create: `internal/executor/opencode/normalize.go`
- Create: `internal/executor/opencode/normalize_test.go`
- Create: `internal/executor/opencode/testdata/task-frame-session.jsonl`
- Create: `internal/executor/opencode/testdata/reasoning-filter.jsonl`
- Modify: `internal/executor/opencode/adapter.go`
- Modify: `internal/executor/opencode/adapter_test.go`
- Modify: `internal/executor/opencode/api.go`
- Modify: `internal/executor/opencode/api_test.go`

**Interfaces:**
- Consumes: Task 1 `executor.ExecutorEvent` typed payloads and Task 2 Recorder artifact threshold.
- Produces: `opencode.Normalize(json.RawMessage) ([]executor.ExecutorEvent, error)` with stable event IDs and no reasoning parts; OpenCode Adapter emits only normalized events.

- [ ] 用捕获 fixture 先写红灯：公开 assistant message → message；tool search/read/edit/bash/test 生命周期 → stable activity；编辑 → file_change；todo 更新 → todo_snapshot；permission/question/result 保持控制事件；reasoning/thinking 不产公开 event；同 raw part 重放保持 EventID 稳定。

- [ ] 写集成红灯：adapter Events 顺序可被 Recorder 完整 replay；工具大输出落 artifact；旧 permission/ticket/result Manager 测试仍通过。

- [ ] 运行红灯：

  ```bash
  go test ./internal/executor/opencode ./internal/taskview ./internal/agentd -run 'Normalize|TaskFrame|Reasoning|Permission'
  ```

- [ ] `normalize.go` 是 OpenCode raw schema → `executor.ExecutorEvent` 的唯一映射点。stable EventID 由 provider session/event/part/tool IDs 组合；缺 provider ID 时使用可重放的内容位置，不使用随机 ID 吞掉重复检测。

- [ ] 公开函数固定为 `Normalize(raw json.RawMessage) ([]executor.ExecutorEvent, error)`；adapter 的 SSE decoder 先保留 provider event envelope 为 JSON，再只调用该函数，测试 fixture 与真实流走同一路径。

- [ ] assistant message 只接受 API 明确公开的 text part；reasoning part、thinking delta、内部 prompt、tool arguments 中的 secret 不进入 message。Activity title 使用工具名和安全摘要，完整输出交 Recorder artifact。

- [ ] 文件变化优先使用 provider 提供的 structured patch；缺失时只在明确 edit 成功后用 Git diff summary 补充，不能把任意日志文本猜成文件修改。

- [ ] adapter 记录 raw kind→normalized kind/count、unknown/dropped count、session/task，不记录 raw payload。新增文件补职责/边界、导出文档和 reasoning 过滤原因注释。

- [ ] 运行绿灯与现有 OpenCode 回归：

  ```bash
  gofmt -w internal/executor/opencode/normalize.go internal/executor/opencode/normalize_test.go internal/executor/opencode/adapter.go internal/executor/opencode/adapter_test.go internal/executor/opencode/api.go internal/executor/opencode/api_test.go
  go test ./internal/executor/opencode ./internal/taskview ./internal/agentd
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/executor/opencode
  git commit -m "feat: normalize OpenCode events for task TUI"
  ```

### Task 6: 用 fixture 归一化 Claude Code 与 Grok，并守住隐藏 reasoning 边界

**Files:**
- Create: `internal/executor/claudecode/normalize.go`
- Create: `internal/executor/claudecode/normalize_test.go`
- Create: `internal/executor/claudecode/testdata/task-frame-session.jsonl`
- Create: `internal/executor/claudecode/testdata/reasoning-filter.jsonl`
- Create: `internal/executor/grok/normalize.go`
- Create: `internal/executor/grok/normalize_test.go`
- Create: `internal/executor/grok/testdata/task-frame-session.jsonl`
- Create: `internal/executor/grok/testdata/reasoning-filter.jsonl`
- Create: `internal/executor/normalization_contract_test.go`
- Modify: `internal/executor/claudecode/adapter.go`
- Modify: `internal/executor/claudecode/adapter_test.go`
- Modify: `internal/executor/grok/adapter.go`
- Modify: `internal/executor/grok/adapter_test.go`

**Interfaces:**
- Consumes: Task 1 common ExecutorEvent contract and provider protocol fixtures.
- Produces: `claudecode.Normalize(json.RawMessage) ([]executor.ExecutorEvent, error)` and `grok.Normalize(method string, params json.RawMessage) ([]executor.ExecutorEvent, error)`, plus a cross-adapter normalization contract test.

- [ ] 写两套 fixture 红灯，要求同一语义得到相同六类 payload shape，而不是相同 raw 文本。Claude stream-json/Grok ACP 的 permission/question 继续走 Manager；reasoning/thought/tool-private fields 全部过滤。

- [ ] 写跨 adapter contract test：message/activity/file/todo/interaction/task_state 可由同一 reducer 消费；event ID 重放稳定；没有 `executor ==` UI 字段要求。

- [ ] 运行红灯：

  ```bash
  go test ./internal/executor/claudecode ./internal/executor/grok ./internal/executor -run 'Normalize|Contract|Reasoning'
  ```

- [ ] 各 adapter 只有 normalize 文件知道 provider schema；共享 `executor.ExecutorEvent` 不增加 Claude/Grok 专属字段。无法表达的 provider 信息放 artifact 或安全丢弃，不污染通用 payload。

- [ ] Claude 公开 `Normalize(raw json.RawMessage) ([]executor.ExecutorEvent, error)`；Grok ACP 公开 `Normalize(method string, params json.RawMessage) ([]executor.ExecutorEvent, error)`。adapter 收到协议事件后只调用对应入口，fixture 与真实 adapter 不得维护两套 mapper。

- [ ] Todo 缺失时不伪造 todo；file_change 缺失时不从 assistant 自述猜测；Activity phase 必须来自真实 provider lifecycle 或明确 process result。

- [ ] 日志只记录 provider raw kind、normalized kind、drop reason/count、task/session。补齐职责头注释、导出文档和“缺失事实不伪造”的原因注释。

- [ ] 运行绿灯与所有 adapter 回归：

  ```bash
  gofmt -w internal/executor/claudecode internal/executor/grok internal/executor/normalization_contract_test.go
  go test ./internal/executor/claudecode ./internal/executor/grok ./internal/executor
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/executor/claudecode internal/executor/grok internal/executor/normalization_contract_test.go
  git commit -m "feat: normalize Claude and Grok task events"
  ```

### Task 7: 扩展 peer、Main 和 preload 的 Task session/command transport

**Files:**
- Modify: `internal/peer/protocol.go`
- Modify: `internal/peer/client.go`
- Modify: `internal/peer/client_test.go`
- Modify: `internal/resourcegateway/router.go`
- Modify: `internal/agentd/peer_server.go`
- Modify: `internal/agentd/peer_server_test.go`
- Create: `desktop/src/main/handoff/task-client.ts`
- Create: `desktop/src/main/handoff/task-client.test.ts`
- Modify: `desktop/src/main/handoff/register-handoff-ipc.ts`
- Modify: `desktop/src/main/handoff/register-handoff-ipc.test.ts`
- Modify: `desktop/src/preload/handoff-api-types.ts`
- Modify: `desktop/src/preload/handoff.ts`
- Modify: `desktop/src/shared/handoff/contracts.ts`
- Modify: `desktop/src/shared/handoff/contracts.test.ts`

**Interfaces:**
- Consumes: Tasks 1–4 task session/artifact/command wire, Plan 01 peer routing, and Machine availability.
- Produces: peer capabilities `task_frames/task_commands/artifacts` and `window.handoff.tasks.session/subscribe/command/artifact` multiplexed by task ID.

- [ ] 写 peer 红灯测试：task owner local/remote 路由；remote unavailable 拒绝 session/artifact/command；frame WS 通过 local agentd 转发后保持 task_seq；command token/answer 不进公开 errors。

- [ ] 写 Main 红灯测试：打开任务才订阅；关闭最后一个同 task tab 后 unsubscribe；同 task 多 pane 共用一个 TaskClient session；snapshot reset 原子发给 renderer；command 202/409 canonical response 正确。

- [ ] 运行红灯：

  ```bash
  go test ./internal/peer ./internal/resourcegateway ./internal/agentd -run 'TaskProxy|TaskCommand'
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff src/preload)
  ```

- [ ] peer capabilities 增加 `task_frames=1`、`task_commands=1`、`artifacts=1`；缺核心 capability 时 TaskTUI 不开放，不 fallback attach。

- [ ] `window.handoff.tasks` 暴露 `session(taskId)`、`subscribe(taskId, after, callbacks)`、`command(taskId, command)`、`artifact(taskId, artifactId, range)`。renderer 不传 machine endpoint/token。

- [ ] Main 按 task ID multiplex 多 renderer subscriber，一个 upstream stream；每 subscriber 独立 cursor/status。最后 subscriber 关闭才释放 upstream；窗口销毁自动清理。

- [ ] 日志记录 task subscribe/snapshot/frame seq/command id/result/artifact range，文本 payload 只记长度。补齐职责头、导出文档和“打开才订阅”“多 pane 共用一 session”的原因注释。

- [ ] 运行绿灯：

  ```bash
  gofmt -w internal/peer internal/resourcegateway/router.go internal/agentd/peer_server.go internal/agentd/peer_server_test.go
  go test ./internal/peer ./internal/resourcegateway ./internal/agentd
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff src/preload)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  ```

- [ ] Commit:

  ```bash
  git add internal/peer internal/resourcegateway internal/agentd desktop/src/shared/handoff desktop/src/main/handoff desktop/src/preload
  git commit -m "feat: proxy structured task sessions to desktop"
  ```

### Task 8: 实现 TaskSessionStore、确定性 reducer 和 snapshot reset

**Files:**
- Create: `desktop/src/renderer/src/features/handoff/tasks/task-session-store.ts`
- Create: `desktop/src/renderer/src/features/handoff/tasks/task-session-store.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/tasks/task-reducer.ts`
- Create: `desktop/src/renderer/src/features/handoff/tasks/task-reducer.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/tasks/task-command-controller.ts`
- Create: `desktop/src/renderer/src/features/handoff/tasks/task-command-controller.test.ts`
- Modify: `desktop/src/renderer/src/features/handoff/workbench/workspace-session-store.ts`
- Modify: `desktop/src/renderer/src/features/handoff/workbench/workspace-session-store.test.ts`

**Interfaces:**
- Consumes: Task 7 `window.handoff.tasks`, Plan 02 task tabs, and golden TaskSession/Frame DTOs.
- Produces: pure `applySnapshot/applyFrame`, TaskSessionStore reference-counted open/close/reset actions, and TaskCommandController pending/canonical-conflict state.

- [ ] 写 reducer 红灯测试，与 Go reducer 使用同一 golden journey：重复 frame 幂等；旧序号拒绝；gap 停止 apply 并请求 snapshot；snapshot 原子替换；message/activity/file/todo/interaction/task_state 的最终 view model 完全一致。

- [ ] 写 store 红灯测试：一个 task 一个 session，多 pane 引用计数；关闭最后 pane 才 unsubscribe；Machine unavailable 清空现场可见性但保留 tab/layout；重连重新 session；Task tab 按 task ID 去重并先切所属 Workspace。

- [ ] 写 command controller 红灯测试：提交后只标 `submitting`，不改 ticket/state；收到 resolved frame 才完成；409 显示 canonical resolution；网络不确定可用同 command ID 重试。

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff/tasks src/renderer/src/features/handoff/workbench)
  ```

- [ ] reducer 是纯函数：`applySnapshot` 与 `applyFrame`；state 按稳定 ID 保存 messages/activities/fileChanges/todos/interactions/taskState；未知 optional payload version 返回 unsupported，不静默误渲染。

- [ ] TaskSessionStore 独立于 CatalogStore 和 Orca `useAppStore`；通过注入接口读取 task owner availability。关闭 tab 可丢 session 内容，重开从 agentd snapshot 恢复。

- [ ] command ID 在首次用户动作时 UUID 生成并跟 pending command 保存；重试复用。用户 answer/continue 原文只传 API，不进 localStorage 或 renderer logs。

- [ ] renderer 无 console logging；Task subscribe/command/reset 的结构化日志由 Task 7 Main client 记录，错误 UI 保留 Problem code/correlation ID。补齐职责头、导出文档和权威回流/引用计数/snapshot reset 原因注释。

- [ ] 运行绿灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff/tasks src/renderer/src/features/handoff/workbench)
  (cd desktop && corepack pnpm typecheck)
  ```

- [ ] Commit:

  ```bash
  git add desktop/src/renderer/src/features/handoff/tasks desktop/src/renderer/src/features/handoff/workbench
  git commit -m "feat: reduce handoff task frames into desktop sessions"
  ```

### Task 9: 实现统一结构化 TaskTUI 与交互控件

**Files:**
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/TaskTUI.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/TaskTUI.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/MessageTimeline.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/ActivityList.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/FileChangeList.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/TodoList.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/InteractionCard.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/TaskStatusBar.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/ArtifactViewer.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/task-tui/task-tui-fixture.test.tsx`
- Modify: `desktop/src/renderer/src/features/handoff/components/WorkbenchShell.tsx`
- Modify: `desktop/src/renderer/src/features/handoff/components/ProjectTree.tsx`
- Modify: `desktop/src/renderer/src/features/handoff/architecture-boundary.test.ts`
- Modify: `desktop/src/renderer/src/assets/main.css`

**Interfaces:**
- Consumes: Task 8 Task view model/command controller, Task 7 artifact reads, and Plan 02 editor-tab navigation.
- Produces: `TaskTUI` and six renderer sections driven only by view model + callbacks, with no PTY/executor-specific imports.

- [ ] 写 UI 红灯测试：六类 frame 都有可读表现；activity 可展开 artifact；permission 有批准一次/拒绝；question 有输入回答；waiting_review 提供 continue，并明确提示最终完成仍沿用现有 CLI `done` 流程（Phase 2 桌面 command contract 不伪造 `done`）；running/stalled 有 stop/resume；409 显示“已由另一窗口处理”。

- [ ] 写视觉结构测试：TaskTUI 使用 terminal-like 深色 editor surface、mono 主体、公开对话时间线和结构化卡片；不是 ASCII 假表格，也不渲染 xterm/canvas；不同 executor fixture 产生同一 DOM 结构，仅标签内容不同。

- [ ] 更新架构守卫：`task-tui` imports 不得含 `@xterm`、`Terminal`, `pty`, `ssh`, `opencode`, `claudecode`, `grok` adapter 或原生 agent launch。

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff/components/task-tui src/renderer/src/features/handoff/architecture-boundary.test.ts)
  ```

- [ ] `TaskTUI` 只接 view model + command callbacks。状态栏显示 executor/model/context（仅有权威数据时）、connection、task state；不推断“正在思考”。

- [ ] Activity 默认展示工具名、摘要、阶段、耗时和结果；完整输出按需请求 Artifact。FileChange 点击打开对应 Workspace 的 Editor/Diff tab；Todo 使用 snapshot 权威顺序。

- [ ] InteractionCard 提交后 disable 并显示处理中；只在 frame resolved 后消失/变历史。Answer/continue 输入支持 Enter 语义和 Esc 退出，危险 stop/reject 使用明确确认，不把 Cancel 标 destructive。

- [ ] ProjectTree task row attention 和右侧计数由 TaskSummary control event 更新；TaskTUI detail frame 不让 renderer 自己反推全局计数。

- [ ] 错误、空、loading、offline、incompatible、cursor reset 状态都可行动且不 overclaim。renderer 无 console logging；命令与 artifact 的结构化日志沿用 Task 7 Main client。补齐职责头、导出文档和“TaskTUI 不用 PTY/不按 executor 分支”的原因注释。

- [ ] 运行绿灯、类型和边界检查：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  ```

- [ ] Commit:

  ```bash
  git add desktop/src/renderer/src/features/handoff desktop/src/renderer/src/assets/main.css
  git commit -m "feat: render unified structured task TUI"
  ```

### Task 10: 用真实 OpenCode 和跨 adapter fixture 完成 TaskTUI checkpoint

**Files:**
- Modify: `desktop/tests/fixtures/fake-handoff-agentd.ts`
- Create: `desktop/tests/e2e/handoff-task-tui.spec.ts`
- Create: `desktop/config/scripts/run-handoff-task-e2e.mjs`
- Modify: `desktop/package.json`
- Create: `docs/superpowers/evidence/phase2-checkpoint-03.md`
- Create: `docs/superpowers/evidence/phase2-checkpoint-03-opencode.md`

**Interfaces:**
- Consumes: Tasks 1–9 structured task wire/UI and a real local OpenCode task.
- Produces: `pnpm test:e2e:handoff-task-tui`, fake frame/command journals, cross-adapter reducer proof, and checkpoint-03 automated/real-OpenCode evidence.

- [ ] 写 fake wire E2E：snapshot + 六类 frames；artifact 展开；approve/answer/continue/stop；两次相同 command ID 只一次 fake executor call；409 canonical UI；Task tab 去重；TaskTUI 无 terminal session creation request。

- [ ] 添加 `pnpm test:e2e:handoff-task-tui`，失败保存 screenshot/trace/frame journal/command journal；journal 只存 kind/ID/seq/length/hash，不存 message/answer 全文。

- [ ] 运行自动化红灯后实现 fixture 和脚本，直到通过：

  ```bash
  (cd desktop && corepack pnpm test:e2e:handoff-task-tui)
  ```

- [ ] 在本机真实 agentd 启动一个受控 OpenCode 任务，任务要求：读取文件、搜索、编辑、跑测试、更新 todo、触发一次安全权限请求或提问，然后进入 waiting_review。禁止用 fake 替代这一步。

- [ ] 桌面验证 message/activity/file_change/todo_snapshot/interaction/task_state 六类均来自真实 TaskFrame；完成一次审批/回答、一次 continue，并以一次 stop 或执行者自然终态收尾。保存 DB 查询证明 task_seq 单调、command 只投递一次、reasoning 未落 frame。

- [ ] 用 Claude Code/Grok 捕获 fixture 跑 contract 与桌面同一 reducer；本 checkpoint 不要求两者真实账号在线，但 fixture 必须来自真实协议样本并清除 secret。

- [ ] 真实运行的 agentd/adapter/recorder/Main 日志按 task/command/frame seq 关联；成功路径有记录，内容不泄漏。测试/证据文件补职责边界说明和证据采集原因。

- [ ] 运行完整 checkpoint：

  ```bash
  go test -race ./internal/taskview ./internal/taskcommand ./internal/artifact ./internal/agentd ./internal/executor/...
  go test ./...
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff src/renderer/src/features/handoff)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm test:e2e:handoff-task-tui)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  ```

- [ ] 两份 evidence 记录 commit、版本、命令、退出码、frame kind/seq 统计、command/ticket version、截图/trace、日志 correlation 和 reasoning-negative 查询。不得把真实 prompt/回答全文写进 evidence。

- [ ] Commit:

  ```bash
  git add desktop/tests/fixtures/fake-handoff-agentd.ts desktop/tests/e2e/handoff-task-tui.spec.ts desktop/config/scripts/run-handoff-task-e2e.mjs desktop/package.json docs/superpowers/evidence/phase2-checkpoint-03.md docs/superpowers/evidence/phase2-checkpoint-03-opencode.md
  git commit -m "test: prove structured task TUI checkpoint"
  ```

## Plan 03 Completion Gate

- [ ] 六类 TaskFrame 有强类型、事务、replay、snapshot、Go/TS reducer 和 UI 测试。
- [ ] `proto.Event`/CLI wait 没被高频 frame 改变语义；现有 CLI 回归全绿。
- [ ] permission/question/state 与 Ticket/Task 同一权威事务，无第二套审批状态机。
- [ ] 并发 Ticket 命令一个赢家、executor 一次调用；409 返回 canonical result。
- [ ] 真实 OpenCode 六类现场和完整交互闭环有证据；Claude/Grok fixture 使用同一 reducer。
- [ ] reasoning/thinking 不在 frame、artifact、renderer、日志或 evidence 中。
- [ ] 架构守卫证明 TaskTUI 不含 PTY、SSH、native TUI 或 executor-specific UI import。
- [ ] 当前 Go/desktop 验证全绿；日志与注释按 instrumenting-code 清单逐项复核。
