# B353–B355 唤醒消费面收口实现计划

状态：执行计划
卡：B353（B354、B355 并入；不另建实现卡）
定级：L3 轻档
法定产出：`docs/superpowers/plans/b353-plan.md`
事实台账：`docs/superpowers/ledgers/2026-09-09-b353-plan-ledger.md`
有效实现基线：`cards/B233.1-charter-7 @ 48f2736a`
当前执行分支：`cards/B353-charter-3`；实现者只在当前分支修改，不切分支、不改 git 配置、不 push。

本计划只落实已批准 spec `docs/superpowers/specs/b353.md`、已冻结 contract `docs/superpowers/specs/b353-contract.md` 与已拍板 breakdown `docs/superpowers/specs/b353-breakdown.md`。实现不新增 wait 命令、HTTP/WS 字段、事件枚举、账本写入口、常驻订阅进程或第二事件分类表；`mirrorSkip` 不扩面，过滤只发生在既有应用消费点。

## 0. 基线证据、图覆盖与硬边界

### 0.1 动手前已真实复核的判据

下列命令均在当前工作树未改实现的基线运行；实现者开始对应 task 前须重跑该 task 的最小命令。当前测试命令中没有 B353 新测试，所以 `-race` 基线的 `[no tests to run]` 不是行为通过证据。

| 范围 | 命令 | 基线原始结果 |
|---|---|---|
| client 唯一策略与 wait/follow | `go test ./internal/client -run 'Test(B353|WaitDeliveryPolicy|WaitEvent|Follow)' -count=1` | 退出 0；`ok  github.com/Xsxdot/handoff/internal/client  7.871s` |
| card wait 存量终态接缝 | `go test ./cmd -run '^TestCardWaitSubtreeExitsWhenAllDone$' -count=1` | 退出 0；`ok  github.com/Xsxdot/handoff/cmd  2.155s` |
| agentd 存量自动化接缝 | `go test ./internal/agentd -run 'Test(Automation|B2336)' -count=1` | 退出 0；`ok  github.com/Xsxdot/handoff/internal/agentd  3.216s` |
| 三链组合基线 | `go test ./internal/client ./internal/ledger ./internal/agentd ./cmd -run 'Test(B353|WaitDeliveryPolicy|Follow|CardWait|Automation)' -count=1` | 退出 0；四包分别为 `4.954s`、`1.046s`、`2.175s`、`2.171s` |
| 构建 | `go build ./...` | 标准输出为空，退出 0 |
| race 命令可运行性 | `go test -race ./internal/client ./internal/ledger ./internal/agentd ./cmd -run 'Test(B353)' -count=1` | 退出 0；四包均输出 `[no tests to run]`，只证明基线命令可运行 |
| 文档/计划门禁 | `git diff --check` | 标准输出为空，退出 0 |

上一节点台账还记录了 `go test ./cmd` 的既有 `TestRepoContractGate` 失败：`dead-contract` / `dead-interface` 指向 `d_execution→d_orchestration` 的无关声明。本计划的 B353 组合命令不匹配该测试名；实现者若扩展到 `go test ./cmd`，必须原样记录该失败，不把它归因于本卡。

### 0.2 代码图先查结论与源码权威

仓内有 `codegraph/`，本节点已用已安装 `codegraph` 查询；图解析结果与源码以现状为准，图的 `moved` 不表示符号不存在。

| 入口/领域 | 图结果与源码锚点 | 实现约束 |
|---|---|---|
| `d_cli` | `codegraph context d_cli` 返回 `truncated=false`，但 `fociTruncated.total=86, shown=5`；`cmd/card_wait.go#runCardWait` 命中，现状 `func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error`（`:48`）；`who-calls` 命中 `cardWaitCmd.RunE`，并报告 5 个未扫描入口 | 以 `cmd/card_wait.go` 源码锁 CLI 入口；不以 context 邻域当流程图 |
| `d_transport` | `Client.FollowEvents` 命中 `internal/client/client.go:1751`，flow `degraded=false`、29 steps，caller 是 `cmd/wait.go#runFollow` | `FollowEvents` 签名不变；策略仍在 client 消费点 |
| `d_ledger` | `Store.Follow` 命中 `internal/ledger/follow.go:18` | `fromSeq` 排他、每轮动态 members、500 条批量、2 秒生产轮询均不变 |
| `d_orchestration` | `automationWakeEvent` 命中 `internal/agentd/wakeconsumer.go:130`；flow `degraded=true`、`steps=[]`，`who-calls` 只有自身节点并报告 5 个未扫描入口；`consumeAutomationEventsOnce` 同样 `degraded=true`、`steps=[]` | 两个 wakeconsumer 方法按源码核对；不得把 chain/who-calls 结果冒充流程图 |
| `d_keystone` / `d_protocol` | contract 已核对 `keystone.Service.Decide`、`proto.EventType`、`proto.RoomMessage`；`codegraph resolve` 均为 `ok`/`moved` | 复用现有 `WakeTaskTerminal`、`WakeTicket`、`WakeMessage` 与既有 DTO，不新增类型/字段 |
| 策略 | `WaitDeliveryPolicy` 未命中独立图节点；生产实现是 `internal/client/delivery.go#WaitDeliveryPolicy`，`internal/client/client.go#isDeliverable` 只是包内别名 | 三链只调用这一策略；不在 cmd、wakeconsumer、mirror 包复制七项假集合 |

### 0.3 现状依赖行为（必须按出处实现）

- `internal/ledger/follow.go:20-21` 将 `pollInterval <= 0` 归一为 `2*time.Second`；`:55` 每轮 `EventsFromAsc(ids, cursor, 500)`，`:59-63` 先回调再推进排他 cursor；PG `:24-45` 的 `LISTEN card_events` 只作唤醒铃，`:65-70` ticker 始终兜底。实现只取消 `Store.Follow` 的 ctx，不在存储层加过滤或 timer。
- `internal/client/client.go:1651-1659` 的 `waitOnce` 在 `streamOnce` 收到每帧后以 `isDeliverable` 判定；`:1751-1801` 的 `FollowEvents` 先以任意帧更新 `lastFrame/fromSeq`，再过滤，且 `all=true` 绕过过滤。`internal/client/delivery.go:23-34` 的七项假集合是唯一策略。
- `internal/proto/proto.go:42-126` 是任务事件词表；`delivery_failed` 的注释（`:61-66`）规定其唤醒协调者执行 `resume`，而 `approver_decision`、`permission_auto_allow` 等注释规定仅审计。`internal/proto/rooms.go:24-35` 的 `RoomMessage.BySystem` 为 `omitempty`，所以生产 JSON 缺少该键时等价于显式 false；显式 `null` 不得在新增判断中伪装成明确的 false。
- `internal/ledgermirror/mirror.go:169-173` 的 `mirrorSkip` 只包含 `progress`、`approver_decision`、`approver_disabled`；本卡不得修改它。
- `internal/agentd/wakeconsumer.go:222-260` 先过 task_mirrored 当前 attempt 闸，再映射事件；`:263-280` 按卡合并，`keystone.Decide` attach 暂缓时不推进 cursor；失败升级、自生 `needs_human` 去重和 cursor 语义继续保留。
- contract §4 已从依赖源码核对 `pgx/v5@v5.10.0/conn.go:413-430` 的 `WaitForNotification(ctx)`、`coder/websocket@v1.8.15/read.go:20-49,60-61,88-107` 的单消息读取/控制帧/32768 字节默认上限，以及 `internal/client/client.go:49-54,1497-1519` 的 10 秒拨号与 1–60 秒重连退避；本机测试不把这些边界外推为 PG/跨机真机结论。

### 0.4 有界文件集

实现者只能修改下面文件与本计划台账；不因目录内相邻代码而扩成整个包。已列生产文件若现状已满足行为，可以只加测试或保持不变，但不能用“顺手重构”扩大范围。

```text
cmd/card_wait.go
cmd/card_wait_test.go

internal/client/delivery.go
internal/client/client.go
internal/client/client_test.go
internal/client/execution_test.go
internal/client/follow_test.go

internal/agentd/wakeconsumer.go
internal/agentd/wakeconsumer_test.go

skills/handoff/SKILL.md
README.md
```

`internal/ledger/follow.go`、`internal/ledger/types.go`、`internal/ledgermirror/mirror.go`、`internal/keystone/keystone.go`、`internal/proto/proto.go`、`internal/proto/rooms.go` 是本卡的消费依赖和不变性核对文件；除非实现者发现编译所需的既有签名事实与本计划矛盾，否则只读核对，不改。`Store.Follow`、`EventType`、`RoomMessage` 的既有测试通过 T2/T3 的真实调用方接缝被覆盖。

## 1. 跨 task 接缝与精确接口

### 1.1 Consumes（已有签名，逐字符保持）

```go
// internal/client/delivery.go
func WaitDeliveryPolicy(t proto.EventType) bool

// internal/client/client.go
func (c *Client) WaitEvent(ctx context.Context, taskID string, all bool) (*proto.Event, error)
func (c *Client) FollowEvents(ctx context.Context, taskID string, all bool, idle time.Duration, onEvent func(*proto.Event) error, onBacklog func(*BacklogSummary) error) error

// internal/ledger/follow.go
func (s *Store) Follow(ctx context.Context, members func() ([]string, error), fromSeq int64, pollInterval time.Duration, onEvent func(Event) error) error

// internal/agentd/wakeconsumer.go
func automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error)
func (s *Server) consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error)

// internal/keystone/keystone.go
func (s *Service) Decide(ev WakeEvent) Decision
```

### 1.2 Produces（本卡新增/改变的进程内行为，不进 wire）

```go
// cmd/card_wait.go：私有调用面，供 cardWaitCmd.RunE 唯一调用。
func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration) error

// cmd/card_wait.go：私有事件分类；返回 error 时必须携带 seq/card/type 上下文。
func cardWaitEventActionable(ev ledger.Event) (bool, error)

// cmd/card_wait.go：follow idle 控制器；不创建持久进程或订阅。
func startCardWaitIdle(parent context.Context, idle time.Duration) (ctx context.Context, noteActivity func(), stop func(), timedOut func() bool)
```

`cardWaitCmd` 新增的唯一 CLI 输入是既有 `card wait` 子命令的 `--follow bool` flag；不新增命令或输出字段。`WaitDeliveryPolicy`、`WaitEvent`、`FollowEvents`、`Store.Follow`、`automationWakeEvent`、`consumeAutomationEventsOnce` 和 `Service.Decide` 的签名均不改。

### 1.3 唯一分类表

任务事件的假集合严格为：`progress`、`approver_decision`、`approver_disabled`、`tickets_voided`、`ticket_answered`、`permission_auto_allow`、`permission_reuse`。集合外每个已有 `proto.EventType` 均为可动作，包括 `delivery_failed`、`stalled`、`approval_dropped`、`archived`、`resource_pressure`、`task_proc_pressure`。卡原生可动作集合为 `needs_human`、`needs_cleared`、`decision_opened`、`decision_answered` 和真人 `room_message`；`status_moved` 只触发终态检查，不输出/不唤醒；其它卡事件只保留审计。

## 2. DAG 与实现任务

```text
T0 冻结物/基线核验
 └──> T1 任务 wait/follow 消费策略
        ├──> T2 card wait 过滤与退出模型
        └──> T3 wakeconsumer/Keystone 映射
               └──> T4 三链穿缝回归 + skill/README 同步
T2 ────────────────────────────────┘
```

T1 的策略在本基线已经有 `WaitDeliveryPolicy`、`WaitEvent`、`FollowEvents` 路径；因此 T1 的生产实现可能是零改动，必须用新接缝测试证明而不是为了“有代码变更”重写。T2 与 T3 只能在 T1 的唯一策略测试和源码核对完成后开始。T4 由实现者跑机内收口命令；真实跨机/宿主清单由协调者的验收节点执行，不在本计划中伪造结果。

### Task 0：冻结物与可执行边界核验

#### 文件范围

- 只读：`docs/superpowers/specs/b353.md`、`docs/superpowers/specs/b353-contract.md`、`docs/superpowers/specs/b353-breakdown.md`、`codegraph/best.json`。
- 过程记录：`docs/superpowers/ledgers/2026-09-09-b353-plan-ledger.md`。

#### Interfaces

- Consumes：contract §§1–5 的精确签名和冻结 #1–#48；breakdown §3.2 的 T1–T4；best 顶层领域 `d_cli`、`d_transport`、`d_ledger`、`d_orchestration`、`d_keystone`、`d_protocol`。
- Produces：给 T1/T2/T3/T4 的有界文件集、唯一策略表、测试入口清单；不产生运行时代码或新符号。

#### 步骤

1. 在实现分支上运行 `git status --short --branch`，确认仍是当前分支且工作树只包含实现者随后允许的本卡文件；若发现其它改动，保留原始输出并停止扩写，不覆盖用户改动。
2. 运行 `codegraph resolve --repo . --doc docs/superpowers/specs/b353-contract.md` 与 `codegraph resolve --repo . --doc docs/superpowers/specs/b353-breakdown.md`，确认所有 file#Symbol 为 `ok` 或 `moved`；未命中只记图覆盖债并回到源码，不改写符号名。
3. 对照 §1.3 的 7 项假集合和卡原生集合，逐条映射到 T1、T2、T3；若某个实现要求新增事件/字段/命令，停止该实现并把需求退回 contract，不能在 T2/T3 私自扩面。
4. 记录 T0 结论到台账后进入 T1；T0 不跑全量测试，也不修改 `codegraph/`、spec 或 contract。

### Task 1：任务 wait/follow 复用唯一 `WaitDeliveryPolicy`

#### 文件范围

- 生产核对/必要注释：`internal/client/delivery.go`、`internal/client/client.go`。
- 声明缝测试：`internal/client/client_test.go`、`internal/client/follow_test.go`、`internal/client/execution_test.go`。

#### Interfaces

- Consumes：`WaitDeliveryPolicy(proto.EventType) bool`；`Client.WaitEvent(ctx context.Context, taskID string, all bool) (*proto.Event, error)`；`Client.FollowEvents(ctx context.Context, taskID string, all bool, idle time.Duration, onEvent func(*proto.Event) error, onBacklog func(*BacklogSummary) error) error`；现有 `pushEvents`、`newTestClientEnv`、`newTestEnv.createPendingTask` harness。
- Produces：不新增导出接口；`all=false` 的两个真实调用方只把策略为真的事件交给调用者，`all=true` 的 `FollowEvents` 仍交付策略为假的审计帧；收到任意 WS 帧（包括被过滤帧）仍刷新 idle/fromSeq。若源码已满足，不改生产函数体，只补 regression tests 和完整注释。

#### 基线判据与测试范围

动手前已跑：`go test ./internal/client -run 'Test(B353|WaitDeliveryPolicy|WaitEvent|Follow)' -count=1`，退出 0，原始读数见本计划 §0.1 与台账第 12 条。实现后只跑 `go test ./internal/client -run 'TestB353|TestWaitDeliveryPolicy|TestWaitEvent|TestFollow' -count=1` 与 `go test ./internal/client -run 'TestB2336MirrorWatermarkIsIndependentFromWaitEvent' -count=1`；不在本 task 跑全仓。

#### 步骤

1. 先在 `internal/client/execution_test.go` 加策略矩阵缝的附加锁。测试入口必须仍包含真实 `WaitEvent`/`FollowEvents` 测试；helper 矩阵不能替代调用方测试。完整矩阵代码如下：

   ```go
   func TestB353WaitDeliveryPolicyFrozenSet(t *testing.T) {
       falseTypes := []proto.EventType{
           proto.EventTypeProgress,
           proto.EventTypeApproverDecision,
           proto.EventTypeApproverDisabled,
           proto.EventTypeTicketsVoided,
           proto.EventTypeTicketAnswered,
           proto.EventTypePermissionAutoAllow,
           proto.EventTypePermissionReuse,
       }
       trueTypes := []proto.EventType{
           proto.EventTypePermissionRequest,
           proto.EventTypeQuestion,
           proto.EventTypeCompleted,
           proto.EventTypeFailed,
           proto.EventTypeTurnFailed,
           proto.EventTypeStalled,
           proto.EventTypeDeliveryFailed,
           proto.EventTypeApprovalDropped,
           proto.EventTypeArchived,
           proto.EventTypeResourcePressure,
           proto.EventTypeTaskProcPressure,
       }
       for _, typ := range falseTypes {
           if got := client.WaitDeliveryPolicy(typ); got {
               t.Errorf("WaitDeliveryPolicy(%q)=true, want false", typ)
           }
       }
       for _, typ := range trueTypes {
           if got := client.WaitDeliveryPolicy(typ); !got {
               t.Errorf("WaitDeliveryPolicy(%q)=false, want true", typ)
           }
       }
   }
   ```

2. 在 `internal/client/client_test.go` 从真实 `newTestClientEnv` → SQLite `store.AppendEvent` → httptest agentd WS → `Client.WaitEvent` 写一条 `permission_auto_allow`，再写一条 `delivery_failed`；断言返回 `delivery_failed` 的 seq/type，且 cursor 写到该 seq。该测试的完整调用骨架如下，任务创建必须照抄现有 `TestWaitEventSkipsProgress` 的 `env.createPendingTask`/时间字段 harness，不另造 server：

   ```go
   func TestB353WaitEventDeliversDeliveryFailedAfterAudit(t *testing.T) {
       env := newTestClientEnv(t)
       taskID := env.createPendingTask(t)
       if _, err := env.st.AppendEvent(taskID, proto.EventTypePermissionAutoAllow,
           map[string]any{"rule": "safe-command"}); err != nil {
           t.Fatalf("AppendEvent audit: %v", err)
       }
       failed, err := env.st.AppendEvent(taskID, proto.EventTypeDeliveryFailed,
           map[string]any{"ticket_id": "ticket-1"})
       if err != nil {
           t.Fatalf("AppendEvent delivery_failed: %v", err)
       }
       ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
       defer cancel()
       got, err := client.New(env.ts.URL, env.token).WaitEvent(ctx, taskID, false)
       if err != nil {
           t.Fatalf("WaitEvent: %v", err)
       }
       if got.Seq != failed.Seq || got.Type != proto.EventTypeDeliveryFailed {
           t.Fatalf("WaitEvent = seq=%d type=%q, want seq=%d type=%q",
               got.Seq, got.Type, failed.Seq, proto.EventTypeDeliveryFailed)
       }
       if got.Payload == nil || string(got.Payload) == "null" {
           t.Fatalf("delivery_failed payload 被丢失: %s", got.Payload)
       }
   }
   ```

3. 在 `internal/client/follow_test.go` 复用已存在的 `pushEvents`，新增下面的真实 WS JSON 回归；同一组事件先验证 `all=false`，再验证 `all=true`，不能把 stream handler 换成直接调用策略：

   ```go
   func TestB353FollowFiltersAndPreservesAllMode(t *testing.T) {
       evs := []proto.Event{
           {Seq: 1, TaskID: "t-b353", Type: proto.EventTypeProgress, Payload: json.RawMessage(`{"n":1}`)},
           {Seq: 2, TaskID: "t-b353", Type: proto.EventTypeApproverDecision, Payload: json.RawMessage(`{"decision":"approve"}`)},
           {Seq: 3, TaskID: "t-b353", Type: proto.EventTypeApproverDisabled, Payload: json.RawMessage(`{"reason":"fail-closed"}`)},
           {Seq: 4, TaskID: "t-b353", Type: proto.EventTypeTicketsVoided, Payload: json.RawMessage(`{"count":1}`)},
           {Seq: 5, TaskID: "t-b353", Type: proto.EventTypeTicketAnswered, Payload: json.RawMessage(`{"ticket_id":"q1"}`)},
           {Seq: 6, TaskID: "t-b353", Type: proto.EventTypePermissionAutoAllow, Payload: json.RawMessage(`{"rule":"safe-command"}`)},
           {Seq: 7, TaskID: "t-b353", Type: proto.EventTypePermissionReuse, Payload: json.RawMessage(`{"fingerprint":"f1"}`)},
           {Seq: 8, TaskID: "t-b353", Type: proto.EventTypeDeliveryFailed, Payload: json.RawMessage(`{"ticket_id":"q1"}`)},
           {Seq: 9, TaskID: "t-b353", Type: proto.EventTypeStalled, Payload: json.RawMessage(`{"last_seq":8}`)},
           {Seq: 10, TaskID: "t-b353", Type: proto.EventTypeApprovalDropped, Payload: json.RawMessage(`{"ticket_id":"q1"}`)},
           {Seq: 11, TaskID: "t-b353", Type: proto.EventTypeArchived, Payload: json.RawMessage(`{"note":"done"}`)},
           {Seq: 12, TaskID: "t-b353", Type: proto.EventTypeFailed, Payload: json.RawMessage(`{"reason":"stop"}`)},
       }
       filteredServer := pushEvents(t, evs, func(*websocket.Conn) {})
       var filtered []proto.Event
       if err := client.New(filteredServer.URL, "").FollowEvents(t.Context(), "t-b353", false, 0,
           func(ev *proto.Event) error { filtered = append(filtered, *ev); return nil }, nil); err != nil {
           t.Fatalf("FollowEvents all=false: %v", err)
       }
       if len(filtered) != 5 {
           t.Fatalf("all=false count=%d, want 5", len(filtered))
       }
       if got := []int64{filtered[0].Seq, filtered[1].Seq, filtered[2].Seq, filtered[3].Seq, filtered[4].Seq};
           !reflect.DeepEqual(got, []int64{8, 9, 10, 11, 12}) {
           t.Fatalf("all=false seq=%v, want [8 9 10 11 12]", got)
       }
       allServer := pushEvents(t, evs, func(*websocket.Conn) {})
       var all []proto.Event
       if err := client.New(allServer.URL, "").FollowEvents(t.Context(), "t-b353", true, 0,
           func(ev *proto.Event) error { all = append(all, *ev); return nil }, nil); err != nil {
           t.Fatalf("FollowEvents all=true: %v", err)
       }
       if len(all) != len(evs) {
           t.Fatalf("all=true count=%d, want %d", len(all), len(evs))
       }
       for i := range evs {
           if all[i].Seq != evs[i].Seq || all[i].Type != evs[i].Type ||
               string(all[i].Payload) != string(evs[i].Payload) {
               t.Fatalf("all=true event[%d]=%+v, want %+v", i, all[i], evs[i])
           }
       }
   }
   ```

   该代码沿用 `follow_test.go` 已有 imports (`reflect` 需加入) 和 `pushEvents` 的临时 HOME/WS harness；`failed` 是正常终结，保证两次验证都会收束。已有 `TestFollowIdleCountsProgressFrames` 继续保留并作为“过滤帧刷新 idle”的接缝锁。
4. 若生产核对发现 `waitOnce` 或 `FollowEvents` 出现第二份白名单，最小修复是删除副本并让调用点继续走 `isDeliverable` → `WaitDeliveryPolicy`；在 `waitOnce`、`FollowEvents` 的过滤、cursor/重连、交付和错误分支保留现有结构化日志，新增日志必须带 `task`、`seq`、`type` 和 `cause`，不得用 `fmt.Print`。若函数体无须改动，保留现有日志/注释，不为测试制造生产 diff。
5. 重新运行 `go test ./internal/client -run 'TestB353|TestWaitDeliveryPolicy|TestWaitEvent|TestFollow' -count=1`；预期退出 0。策略矩阵、`WaitEvent`、`FollowEvents` 和 `all=true` 均必须从声明缝进入；只测 helper 的绿色不能交付 T1。

#### T1 验收

- 七项假集合逐项 false；集合外至少 `delivery_failed`、`stalled`、`approval_dropped`、`archived`、`resource_pressure`、`task_proc_pressure` 逐项 true。
- `WaitEvent(..., false)` 跳过审计帧并实际返回 `delivery_failed`；`FollowEvents(..., false)` 不回调假集合；`FollowEvents(..., true)` 回调相同审计帧；WS/store JSON 原始 payload 未被改写。
- `FollowEvents` 的任意帧 idle 语义、cursor seq 递进、断线重连和已有 mirror watermark 隔离测试不回退。
- T1 不改 `WaitDeliveryPolicy` 七项成员、不新增策略函数、不改 HTTP/WS 流传输过滤。

### Task 2：`card wait` 只输出可动作事件并对齐一次/持续退出模型

#### 文件范围

- 生产：`cmd/card_wait.go`。
- 声明缝测试：`cmd/card_wait_test.go`；测试基座固定复用 `cmd/ledgercli_test.go#runLedgerCLI`、`ledger.Open`、现有 `TestCardWaitSubtreeExitsWhenAllDone` 的临时 SQLite 账本和 goroutine 写事件方式。

#### Interfaces

- Consumes：`openLedger() (*ledger.Store, error)`；`(*ledger.Store).GetCard(id string) (ledger.Card, error)`；`(*ledger.Store).MaxSeq() (int64, error)`；`(*ledger.Store).Subtree(cardID string) ([]string, error)`；`(*ledger.Store).Follow(ctx context.Context, members func() ([]string, error), fromSeq int64, pollInterval time.Duration, onEvent func(ledger.Event) error) error`；`client.WaitDeliveryPolicy(proto.EventType) bool`；`ledger.Event` 原始 `json.RawMessage` payload；`proto.RoomMessage`。
- Produces：`func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration) error`；`card wait --follow` flag；stdout 每行仍是原始 `ledger.Event` JSON；默认模式首个可动作事件成功 Encode 后 0 退出；follow 模式连续 Encode 到当前成员全 `StatusDone`/`StatusClosed`；正 timeout 分别是默认总时长/follow 空闲上限；超时仍为 `ExitTimeout` 124。

#### 基线判据与测试范围

动手前已跑：`go test ./cmd -run '^TestCardWaitSubtreeExitsWhenAllDone$' -count=1`，退出 0，原始读数见 §0.1。实现后本 task 只跑 `go test ./cmd -run '^TestB353CardWait|^TestCardWaitSubtreeExitsWhenAllDone$|^TestWaitRejectsCardFlag$' -count=1`；不跑全仓。

#### 步骤

1. 先扩 `cardWaitCmd.RunE` 的调用形状和命令帮助：在现有 flag 变量旁增加 `var cardWaitFollow bool`，增加 `--follow` bool，调用 `runCardWait(cmd, args[0], cardWaitSubtree, cardWaitFollow, cardWaitTimeout)`；`--subtree` 语义不动；负 timeout 仍在打开账本前拒绝。`init` 的完整变更形状为：

   ```go
   var cardWaitFollow bool

   func init() {
       cardWaitCmd.Flags().BoolVar(&cardWaitSubtree, "subtree", false,
           "扩展到子树（后代 + 并入成员，动态）")
       cardWaitCmd.Flags().BoolVar(&cardWaitFollow, "follow", false,
           "持续输出可动作事件，全部成员终态才退出")
       cardWaitCmd.Flags().DurationVar(&cardWaitTimeout, "timeout", 0,
           "超时（如 2h）；默认=等待总时长，--follow=空闲上限，到点以 124 退出")
   }
   ```

   `RunE` 必须调用 `runCardWait(cmd, args[0], cardWaitSubtree, cardWaitFollow, cardWaitTimeout)`。文件头与 `runCardWait` 注释必须写清默认一次一挂、follow 长挂、stdout 只出可动作原始事件的边界。
2. 在 `cmd/card_wait.go` 放置下面的私有分类代码。它只复用 client 策略，不复制任务假集合；`task_mirrored` 仅解包 `task_type`，输出仍是输入的原始 `ledger.Event`。JSON 缺失、损坏或 payload 为空时返回含 seq/card/type 的错误，不能静默当作审计：

   ```go
   func cardWaitEventActionable(ev ledger.Event) (bool, error) {
       switch ev.Type {
       case ledger.EvNeedsHuman, ledger.EvNeedsCleared,
           ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
           return true, nil
       case ledger.EvRoomMessage:
           var msg proto.RoomMessage
           if err := json.Unmarshal(ev.Payload, &msg); err != nil {
               return false, fmt.Errorf("card %s room_message seq=%d type=%s 解码: %w",
                   ev.CardID, ev.Seq, ev.Type, err)
           }
           var fields map[string]json.RawMessage
           if err := json.Unmarshal(ev.Payload, &fields); err != nil {
               return false, fmt.Errorf("card %s room_message seq=%d type=%s 字段解码: %w",
                   ev.CardID, ev.Seq, ev.Type, err)
           }
           if raw, present := fields["by_system"]; present &&
               bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
               return false, nil
           }
           return msg.Kind == proto.RoomMsgUser && !msg.BySystem, nil
       case ledger.EvTaskMirrored:
           var envelope struct {
               TaskType string          `json:"task_type"`
               Payload  json.RawMessage `json:"payload"`
           }
           if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
               return false, fmt.Errorf("card %s task_mirrored seq=%d type=%s 解包: %w",
                   ev.CardID, ev.Seq, ev.Type, err)
           }
           if envelope.TaskType == "" || len(envelope.Payload) == 0 {
               return false, fmt.Errorf("card %s task_mirrored seq=%d 缺 task_type/payload",
                   ev.CardID, ev.Seq)
           }
           return client.WaitDeliveryPolicy(proto.EventType(envelope.TaskType)), nil
       default:
           return false, nil
       }
   }
   ```

   `by_system` 缺失按现有 `omitempty` user JSON 的 false 语义处理；显式 false 也可动作；显式 null 不可动作；非法类型由 `json.Unmarshal` 返回错误。测试必须同时构造缺失、false、true、null，不能只构造 Go 零值。
3. 把 `Store.Follow` 回调改成“先判断终态 status_moved，再分类，再 Encode”的顺序。下面是回调的完整控制形状；`follow` 使用 `runCardWait` 参数，`noteActivity`、`checkDone`、`enc`、`allDone` 使用现有函数局部变量，不改 `Store.Follow` 签名：

   ```go
   err = st.Follow(ctx, members, start, 2*time.Second, func(e ledger.Event) error {
       noteActivity()
       slog.Debug("card wait 收到账本事件", "card", e.CardID, "seq", e.Seq,
           "type", e.Type, "follow", follow)

       if e.Type == ledger.EvStatusMoved {
           done, err := checkDone()
           if err != nil {
               slog.Error("card wait 终态检查失败", "card", cardID, "seq", e.Seq,
                   "type", e.Type, "cause", err)
               return err
           }
           if done {
               slog.Info("card wait 成员全部终态", "card", cardID, "seq", e.Seq,
                   "follow", follow)
               return allDone
           }
           return nil
       }

       actionable, err := cardWaitEventActionable(e)
       if err != nil {
           slog.Error("card wait 事件分类失败", "card", e.CardID, "seq", e.Seq,
               "type", e.Type, "cause", err)
           return err
       }
       if !actionable {
           slog.Debug("card wait 过滤审计事件", "card", e.CardID, "seq", e.Seq,
               "type", e.Type)
           return nil
       }
       if err := enc.Encode(e); err != nil {
           slog.Error("card wait 输出事件失败", "card", e.CardID, "seq", e.Seq,
               "type", e.Type, "cause", err)
           return err
       }
       slog.Info("card wait 已输出可动作事件", "card", e.CardID, "seq", e.Seq,
           "type", e.Type, "follow", follow)
       if !follow {
           return allDone
       }
       return nil
   })
   ```

   默认模式和 follow 模式都复用现有 `allDone` 哨兵：默认模式代表“首个可动作 Encode 成功”，follow 模式只在当前成员全终态的 `status_moved` 触发；switch 分支按 `follow` 区分两种正常收尾日志。审计事件不输出、不结束，但仍已由 `Store.Follow` 推进 seq；`status_moved` 自身永不 Encode。
4. 为 follow timeout 使用下列完整的取消/复位形状，不把 `Store.Follow` 改成另一个读循环，也不遗留 goroutine：

   ```go
   func startCardWaitIdle(parent context.Context, idle time.Duration) (
       ctx context.Context, noteActivity func(), stop func(), timedOut func() bool,
   ) {
       if idle <= 0 {
           return parent, func() {}, func() {}, func() bool { return false }
       }
       ctx, cancel := context.WithCancel(parent)
       activity := make(chan struct{}, 1)
       expired := make(chan struct{})
       done := make(chan struct{})
       go func() {
           defer close(done)
           timer := time.NewTimer(idle)
           defer timer.Stop()
           for {
               select {
               case <-ctx.Done():
                   return
               case <-activity:
                   if !timer.Stop() {
                       select { case <-timer.C: default: }
                   }
                   timer.Reset(idle)
               case <-timer.C:
                   close(expired)
                   cancel()
                   return
               }
           }
       }()
       noteActivity = func() {
           select { case activity <- struct{}{}: default: }
       }
       stop = func() {
           cancel()
           <-done
       }
       timedOut = func() bool {
           select { case <-expired: return true; default: return false }
       }
       return ctx, noteActivity, stop, timedOut
   }
   ```

   `runCardWait` 在 `follow && timeout > 0` 时使用该 ctx；其它模式保持 `context.WithTimeout` 总时长语义。`Store.Follow` 返回 `context.Canceled` 且 `timedOut()` 为 true 时转成 `&exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 空闲超时")}`；命令 ctx 取消、账本读取失败、分类失败、Encode 失败原样非零返回。`defer stop()` 必须在调用 Follow 后执行，确保 idle goroutine 已退出。`Store.Follow` 返回 `allDone` 后，若 `!follow` 记录“首个可动作已输出”并返回 nil；若 `follow` 则记录“成员全部终态”并返回 nil，不能把默认首动作误报成全员终态。

   `runCardWait` 在调用 `Store.Follow` 前后的上下文设置与收尾分支使用下面的完整形状；`follow` 是函数参数，`timeout` 是该调用的输入：

   ```go
   ctx := cmd.Context()
   noteActivity := func() {}
   stopIdle := func() {}
   timedOut := func() bool { return false }
   if follow && timeout > 0 {
       ctx, noteActivity, stopIdle, timedOut = startCardWaitIdle(ctx, timeout)
       defer stopIdle()
   } else if timeout > 0 {
       var cancel context.CancelFunc
       ctx, cancel = context.WithTimeout(ctx, timeout)
       defer cancel()
   }

   err = st.Follow(ctx, members, start, 2*time.Second, onEvent)
   switch {
   case errors.Is(err, allDone):
       if follow {
           slog.Info("card wait 成员全部终态，follow 退出", "card", cardID)
       } else {
           slog.Info("card wait 首个可动作事件已输出，一次性退出", "card", cardID)
       }
       return nil
   case follow && errors.Is(err, context.Canceled) && timedOut():
       return &exitCodeError{code: ExitTimeout,
           err: fmt.Errorf("wait --card 空闲超时")}
   case errors.Is(err, context.DeadlineExceeded):
       return &exitCodeError{code: ExitTimeout,
           err: fmt.Errorf("wait --card 超时")}
   default:
       return err
   }
   ```

   `onEvent` 即步骤 3 的完整回调；实现者把它绑定到 `st.Follow` 的第 5 个参数，不另造读循环。`stopIdle` 只在 follow idle controller 被启用时等待其 goroutine 退出。
5. 在 `cmd/card_wait_test.go` 按允许的既有 harness 例外复用 `runLedgerCLI`、`ledger.Open`、`AddComment`、`RecordRoomMessage`、`OpenDecision`/`AnswerDecision`、`MarkNeedsHuman`/`ClearNeedsHuman`、`AppendMirroredEvent`，不得直接调用 `cardWaitEventActionable` 作为唯一入口。新增测试入口至少包括：

   - `TestB353CardWaitDefaultEmitsFirstActionAndExits`：在 wait 起点之后写 `comment`、`dispatched`、`acceptance_recorded`、`permission_auto_allow` 的 `task_mirrored`，断言 stdout 为空且进程仍阻塞；随后写 `needs_human`，断言命令返回 nil、stdout 恰一行、该行 `json.Unmarshal` 后 seq/type/payload 与原 `ledger.Event` 完全相同；再写另一条可动作事件，断言默认模式没有第二行。
   - `TestB353CardWaitFollowFiltersAndContinues`：使用 `--follow --timeout 2s`，依次写 `needs_human`、`needs_cleared`、`decision_opened`、`decision_answered`、真人 `room_message`、系统 `room_message`、comment、dispatched、acceptance_recorded、策略为假的与为真的 `task_mirrored`；断言五类卡侧可动作 + 策略为真的 mirror 各一行，审计/系统消息为零行，且过滤事件仍能从 `EventsFromAsc` 读取。
   - `TestB353CardWaitStatusMovedOnlyChecksTerminal`：非终态 `status_moved` 不输出且不退出；所有当前成员转为 `StatusDone` 或 `StatusClosed` 后，follow 和默认两种模式均返回 nil，终态迁移事件本身不占 stdout 行；`--subtree` 复用现有 `TestCardWaitSubtreeExitsWhenAllDone` 的动态成员写法并断言新子卡仍被成员重算纳入。
   - `TestB353CardWaitTimeoutModes`：默认模式只写审计事件并让总时长到期，错误必须 `errors.As` 为 `*exitCodeError` 且 code=124；follow 模式只写审计事件，任意新账本事件会刷新 idle，连续无新事件才 124。
   - `TestB353CardWaitSerializationBoundaries`：原始 `task_mirrored` envelope 分别使用缺失/空串/非空 `task_type`、缺失/JSON `null`/对象 `payload`；room payload 分别使用 `by_system` 缺失、false、true、null；断言缺失 `task_type`/payload 和非法 JSON 非零并带 seq/card/type，缺失/false user 字段的正常 legacy JSON 可动作，true/null 不动作，stdout 原始 event payload 不改写。
   - `TestB353CardWaitHelpHasFollow`：通过 `runLedgerCLI(t, dir, "card", "wait", "--help")` 断言帮助含 `--follow` 与 `--subtree`；既有 `TestWaitRejectsCardFlag` 继续通过，证明没有把 `--card` 加回任务 wait。

   以上断言逐条列全且全部从 `cardWaitCmd`/`runCardWait` 入口构造；由于 CLI 测试必须复用包级 cobra flag/reset 和临时 SQLite 配置，允许照抄 `cmd/ledgercli_test.go#runLedgerCLI` harness，不能把该例外降级为 helper 内部锁。
6. 在 `cmd/card_wait.go` 的入口、账本打开/卡不存在、成员/MaxSeq 错误、事件分类错误、Encode 错误、终态收尾、timeout/ctx 取消分支保留或补齐结构化 `slog`，字段至少含 `card`、`subtree`、`follow`、`timeout`、`seq/type`（适用时）与 `cause`；成功输出和正常收尾分别有 Info。新文件头/导出函数注释规则在本 task 适用；`runCardWait` 私有函数仍写完整参数/退出语义注释。禁止 `fmt.Print` 作为日志。
7. 运行 `go test ./cmd -run '^TestB353CardWait|^TestCardWaitSubtreeExitsWhenAllDone$|^TestWaitRejectsCardFlag$' -count=1`；预期退出 0。再运行 `git diff --check`；预期无输出、退出 0。

#### T2 验收

- `card wait` 的每个 stdout 行均是原始 `ledger.Event` JSON；可动作/审计/系统消息/终态 status_moved 的正反断言均从 CLI+Store.Follow 接缝进入。
- 默认首个成功 Encode 退出 0；follow 连续输出到成员全终态；默认正 timeout 是总时长，follow 正 timeout 是任意账本事件之间的空闲上限；超时 code=124。
- `--subtree` 动态成员、`fromSeq` 排他、2 秒 Store.Follow 轮询和账本审计保留均不变。

### Task 3：wakeconsumer 与既有 Keystone WakeKind 对齐

#### 文件范围

- 生产：`internal/agentd/wakeconsumer.go`。
- 声明缝测试：`internal/agentd/wakeconsumer_test.go`；复用 `newNoPTYAutomationEnv`、`createCoordCard`、`prebindConsumerSession`、`appendMirroredForConsumer`、`appendWorkflowDispatchForConsumer`、`appendWorkflowMirroredForConsumer`、`appendUserMessage`、`appendPointerMessage`、`queueTraceRunner.snapshot`。

#### Interfaces

- Consumes：`proto.LedgerEvent`；`mirroredTaskTypeAndPayload`；`client.WaitDeliveryPolicy(proto.EventType) bool`；`(*Server).acceptsCurrentWorkflowAttempt`；`(*Server).currentWorkflowAttempt`；`(*ledger.Store).EventsFromAsc([]string, int64, int)`；`(*keystone.Service).Decide(keystone.WakeEvent) keystone.Decision`；`(*Server).wakeCoordinatorRound(ctx, card string, evs []keystone.WakeEvent) (keystone.RoundResult, error)`。
- Produces：`automationWakeEvent` 仍返回 `(keystone.WakeEvent, bool, error)`；`task_mirrored` 先解包 `task_type` 并调用唯一 `WaitDeliveryPolicy`，策略 false 返回 `yes=false`；`permission_request`/`question` → `WakeTicket`；其它策略 true task type（至少 `delivery_failed`、`stalled`、`approval_dropped`、`archived`）→ `WakeTaskTerminal`；`needs_human`、`needs_cleared`、`decision_opened`、`decision_answered` → `WakeTaskTerminal`；真人 room → `WakeMessage`；审计/系统 room/status_moved/其它卡事件不唤醒。消费批量、attempt 闸、attach 暂缓、seen/cursor、失败升级和自生 needs 去重保持原语义。

#### 基线判据与测试范围

动手前已跑：`go test ./internal/agentd -run 'Test(Automation|B2336)' -count=1`，退出 0，原始读数见 §0.1。实现后本 task 只跑 `go test ./internal/agentd -run '^TestB353Automation|^TestAutomation|^TestB2336' -count=1`；不跑全仓。

#### 步骤

1. 在 `wakeconsumer.go` 引入 `internal/client`，只在现有 `task_mirrored` 分支的 `taskType` 解包之后插入策略判定。生产映射必须保持下面的完整形状：

   ```go
   case ledger.EvTaskMirrored:
       taskType, payload, err := mirroredTaskTypeAndPayload(ev)
       if err != nil {
           return keystone.WakeEvent{}, false, err
       }
       if !client.WaitDeliveryPolicy(proto.EventType(taskType)) {
           s.log.Debug("task_mirrored 因任务消费策略过滤", "seq", ev.Seq,
               "card", ev.CardID, "type", ev.Type, "task_type", taskType,
               "reason", "delivery_policy_false")
           return keystone.WakeEvent{}, false, nil
       }
       kind := keystone.WakeTaskTerminal
       if proto.EventType(taskType) == proto.EventTypePermissionRequest ||
           proto.EventType(taskType) == proto.EventTypeQuestion {
           kind = keystone.WakeTicket
       }
       return keystone.WakeEvent{
           Kind: kind,
           Card: ev.CardID,
           Summary: fmt.Sprintf("%s: %s", taskType,
               truncateRunes(string(payload), 400)),
       }, true, nil
   case ledger.EvNeedsHuman, ledger.EvNeedsCleared,
       ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
       return keystone.WakeEvent{
           Kind: keystone.WakeTaskTerminal,
           Card: ev.CardID,
           Summary: fmt.Sprintf("%s: %s", ev.Type,
               truncateRunes(string(ev.Payload), 400)),
       }, true, nil
   ```

   这里的 default-to-`WakeTaskTerminal` 是 contract 的“假集合外全部可动作”在卡侧 task mirror 的落点；不得只列 `delivery_failed` 两条，也不得把 `permission_auto_allow` 送进 default。
2. 保留当前 attempt 闸在 `automationWakeEvent` 前；缺失/空 `Node`/`Attempt` 仍只留审计。`automationWakeEvent` 的 room 分支必须继续返回损坏 JSON error，且用下面的字段边界规则：room JSON 缺 `by_system` 是旧 `omitempty` user false，按 false 处理；显式 false 按 false 处理；显式 true 或 null 不唤醒；非 bool/损坏 JSON 返回含 event seq 的 error。`RoomMessage.Kind` 非 user 也不唤醒。为使“缺失”和显式 `null` 不被 Go bool 零值混淆，在 `wakeconsumer.go` 加入 `bytes` import，并把 room 解码收口为下面的完整私有函数，再由 `automationWakeEvent` 唯一调用：

   ```go
   func decodeHumanRoomMessage(ev proto.LedgerEvent) (proto.RoomMessage, bool, error) {
       var msg proto.RoomMessage
       if err := json.Unmarshal(ev.Payload, &msg); err != nil {
           return proto.RoomMessage{}, false,
               fmt.Errorf("事件 %d 的 room_message 解码失败: %w", ev.Seq, err)
       }
       var fields map[string]json.RawMessage
       if err := json.Unmarshal(ev.Payload, &fields); err != nil {
           return proto.RoomMessage{}, false,
               fmt.Errorf("事件 %d 的 room_message 字段解码失败: %w", ev.Seq, err)
       }
       if raw, present := fields["by_system"]; present &&
           bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
           return msg, false, nil
       }
       if msg.Kind != proto.RoomMsgUser || msg.BySystem {
           return msg, false, nil
       }
       return msg, true, nil
   }
   ```

   其中缺失 `by_system` 与显式 false 均保留真人 user 的动作语义；显式 true、显式 null、非 user kind 均不动作；非法 bool 类型由第一次 `json.Unmarshal` 返回 error。`automationWakeEvent` 的 `EvRoomMessage` 分支调用该函数，error 原样返回，`yes` 为 true 时只把 `msg.Body` 截断到既有 400 rune 摘要。不得把 room payload 投影成新的 stdout 或 Keystone 字段。
   `EvRoomMessage` 分支的完整替换形状如下：

   ```go
   case ledger.EvRoomMessage:
       msg, yes, err := decodeHumanRoomMessage(ev)
       if err != nil {
           return keystone.WakeEvent{}, false, err
       }
       if !yes {
           return keystone.WakeEvent{}, false, nil
       }
       return keystone.WakeEvent{
           Kind: keystone.WakeMessage,
           Card: ev.CardID,
           Summary: truncateRunes(msg.Body, 400),
       }, true, nil
   ```

3. 在消费调用方而非只在 helper 中加关键日志：task mirror 策略过滤必须 Debug 记录 `seq/card/type/task_type/reason=delivery_policy_false`；映射 error 必须 Error 记录 `seq/card/type/cause`（现有分支保留）；`yes=true` 进入 pending、attach 暂缓、wake 成功、wake 失败和 cursor/seen 推进必须各有上下文日志。成功路径不得静默，禁止 `print`。
4. 在 `wakeconsumer_test.go` 追加下列声明缝测试；全部通过 `consumeAutomationEventsOnce` 与 fake runner/Keystone 入口，不以直接调用 `automationWakeEvent` 作为唯一证据：

   - `TestB353AutomationUsesWaitDeliveryPolicy`：同一卡、当前 attempt 下写 `progress`、`approver_decision`、`approver_disabled`、`tickets_voided`、`ticket_answered`、`permission_auto_allow`、`permission_reuse` 七种策略 false mirror，断言 `processed==0`、runner 无 Resume、事件仍存在且 automation cursor/seen 前进；再写 `delivery_failed`、`stalled`、`approval_dropped`、`archived`，断言四条均进入一次合并 Resume briefing。测试必须使用 `appendMirroredForConsumer` 让真实 envelope JSON 经过 `mirroredTaskTypeAndPayload`。
   - `TestB353AutomationMapsCardActionEvents`：同一卡写 `needs_human`、`needs_cleared`、`decision_opened`、`decision_answered`、真人 `room_message`；另写系统 room、`comment`、`dispatched`、`acceptance_recorded`、非终态与终态 `status_moved`。断言前五类进入同一次 Resume 且 briefing 含各自 payload，系统/审计/status_moved 不进入，终态 status_moved 只触发已有 `closeCoordinatorTabIfTerminal` 路径。
   - `TestB353AutomationRoomJSONBoundaries`：通过真实 ledger event payload 逐条覆盖 `by_system` 缺失、false、true、null、非法字符串；缺失/false user 唤醒，true/null 不唤醒，非法 JSON/类型返回 error；断言原始 payload 仍可从账本读取。
   - `TestB353AutomationMalformedEnvelopeFailsAtConsumerSeam`：用 `appendMirroredForConsumer(..., typ="", ...)` 产生缺失 `task_type` 的真实 envelope，调用 `consumeAutomationEventsOnce`，断言返回 error 且 error 同时含卡号、事件 seq、`task_mirrored`，runner 无 Resume；该错误不能被当成合法审计跳过。
   - 复跑并保留 `TestB2336StaleAttemptDoesNotWake`、`TestAutomationAttachDefersAndThenWakes`、`TestAutomationCursorRewindIsIdempotent`、`TestAutomationFallbackResumeRebuildFailure`、`TestAutomationWakeFailureAdvancesCursor`；这些是当前 attempt、attach、cursor、失败升级和自激防护的反例锁，不能删或改成 helper 测试。

   上述测试复用现有 agentd harness 是法定例外：harness 的真实装配形态（SQLite ledger + `SetupAutomation` + fake runner + prebound Keystone session）已在 `internal/agentd/wakeconsumer_test.go` 定义；计划逐条给出入口与 pass/fail 断言，执行者照抄夹具，不创建第二套环境。
5. 运行 `go test ./internal/agentd -run '^TestB353Automation|^TestAutomation|^TestB2336' -count=1`；预期退出 0。再运行 `git diff --check`；预期无输出、退出 0。

#### T3 验收

- `task_mirrored` 七项 false 与策略外 true 在真实 `consumeAutomationEventsOnce` 到 fake Keystone/runner 的结果一致；`delivery_failed` 的可观察结果是进入协调者 wake/resume briefing，不是被当作无事件。
- 卡原生四种 needs/decision、真人 room 的 WakeKind 全部锁住；系统 room、审计、status_moved 与未列事件不唤醒。
- attempt 闸、attach 暂缓不推进 cursor、seen/cursor 去重、同卡合并、失败升级和自生 needs 不自激仍通过既有反例。
- `mirrorSkip` 文件不改；audit 事件仍存在于 card ledger；Keystone 不读 transport 流、不新增持久事实。

### Task 4：三链真实穿缝回归、文档同步与交付门

#### 文件范围

- 文档：`skills/handoff/SKILL.md`、`README.md`。
- 回归入口：T1 的 `internal/client/*_test.go`、T2 的 `cmd/card_wait_test.go`、T3 的 `internal/agentd/wakeconsumer_test.go`；本 task 不创建第四套 helper 测试。

#### Interfaces

- Consumes：T1 的 `WaitDeliveryPolicy` × `WaitEvent`/`FollowEvents`；T2 的 `cardWaitCmd`/`runCardWait` × `Store.Follow`；T3 的 `consumeAutomationEventsOnce` × `Service.Decide`/fake runner；现有操作文档中的 wait/card wait 文字。
- Produces：两份文档对同一行为使用同一套术语：Claude Code/grok 使用单条 `card wait --follow`；opencode/Codex 使用默认一次一挂；审计事件仍可由 show/全量历史对质；`delivery_failed` 动作是 `handoff resume <task>`；`card wait` 的 `--timeout` 默认/ follow 语义明确；不暗示 Codex/opencode 有后台 follow。

#### 基线判据与测试范围

动手前已跑组合命令并退出 0：`go test ./internal/client ./internal/ledger ./internal/agentd ./cmd -run 'Test(B353|WaitDeliveryPolicy|Follow|CardWait|Automation)' -count=1`；race 命令在新测试未存在的基线只输出 `[no tests to run]`，见 §0.1。T4 动手后只跑下面明确列出的组合/race/文档命令；全量测试由协调者统一执行，不归任何单 task。

#### 步骤

1. 在 `skills/handoff/SKILL.md` 更新现有 wait 过滤列表，从“四类”改为完整七项：`progress`、`approver_decision`、`approver_disabled`、`tickets_voided`、`ticket_answered`、`permission_auto_allow`、`permission_reuse`；明确“任务流的集合外全是可动作，含 `delivery_failed`/`stalled`/`approval_dropped`/`archived`/压力告警”。任务侧 `wait --follow` 的现有语义不改。
2. 在 `skills/handoff/SKILL.md` 用下列完整文字替换外层 card wait 命令和“一次工作流只挂一次”段落；保留 `--subtree` 动态成员说明和“不要接管道”告诫，但改掉现状的“裸 card wait 长挂/只跳 mirrorSkip 三项”：

   ```markdown
   handoff card wait <id> [--subtree] [--follow] [--timeout 3h]
   ```

   - `card wait` 跟卡或动态子树的账本单流，stdout 只出逐行原始 `ledger.Event` JSON；过滤只发生在消费点，`comment`、`dispatched`、`acceptance_recorded`、审批链/自动审批审计和系统房间指针仍留在账本，`show` 可对质。
   - 默认模式收到第一条可动作事件并成功写出后退出 0；只收到审计事件时继续等。`--follow` 才持续输出多条可动作事件，直到当前成员全部 `已完成`/`终止`；`status_moved` 只触发终态检查，不作为 stdout 唤醒行。
   - 可动作卡事件是 `needs_human`、`needs_cleared`、`decision_opened`、`decision_answered`、真人 `room_message`，以及 `task_mirrored` 解包后经任务 `WaitDeliveryPolicy` 判为真的 task event。任务假集合七项与 `handoff wait` 同值；`delivery_failed` 会醒来，按任务处置表执行 `handoff resume <task>`。
   - 默认 `--timeout` 是等到可动作事件或终态收尾的总时长；`--follow` 的 `--timeout` 是空闲上限，任意新账本事件（含被过滤审计事件）都会刷新它；超时退出 124。
   - 有后台 Monitor 的 Claude Code/grok：只挂一条 `handoff card wait --follow <id> --timeout 3h`。没有后台唤醒的 opencode/Codex：使用不带 `--follow` 的一次性 `handoff card wait <id> --timeout 5m`，返回后处置，再挂下一条；不要用 `show`+`sleep`、shell 大循环、子 agent 或 `write_stdin` 轮询冒充 follow。
   - 卡 wait 与 task wait 不是两张分类表；同一工作流只在选择 follow 的 harness 上长挂 card wait，不再叠加第二条 task 级订阅来补审计噪声。两次默认 wait 之间的偶发订阅真空是已接受的后续项，不在本卡创建常驻订阅者。
   ```

3. 在 `README.md` 的命令表 `handoff wait <task>` 行后加入以下完整行，并把事件说明段改成明确任务/卡两侧消费点；不新增命令：

   ```markdown
   | `handoff card wait <id>` | Follow a card or dynamic subtree ledger; default prints the first actionable event and exits, `--follow` keeps the subscription until all current members are terminal | `--subtree`; `--follow`; `--timeout <duration>` (one-shot = total budget, `--follow` = idle budget) |
   ```

   将 README 现有 `progress and approval-chain audit events...` 段替换为：

   ```markdown
   Task wait/follow filters the same seven audit types at the application consumer:
   `progress`, `approver_decision`, `approver_disabled`, `tickets_voided`,
   `ticket_answered`, `permission_auto_allow`, and `permission_reuse`. Every other
   existing task event—including `delivery_failed`, `stalled`, `approval_dropped`,
   `archived`, and pressure alerts—is actionable; `delivery_failed` means run
   `handoff resume <task>`.

   Card wait applies that task policy after unpacking `task_mirrored` and additionally
   wakes for `needs_human`, `needs_cleared`, `decision_opened`, `decision_answered`,
   and real user room messages. Comments, dispatch snapshots, acceptance records,
   system room messages, and `status_moved` remain ledger audit facts: they do not
   produce a wake line, and filtering never removes them from `show` history.
   ```

   README 仍保留已有 `wait --follow` 的任务语义、`delivery_failed` 排障行和四种 harness 差异；新增 card wait 行必须说明 opencode/Codex 默认一次一挂，不得把 card wait 文案写成“无 flag 永久长挂”。
4. 运行三链组合回归：

   ```text
   go test ./internal/client ./internal/ledger ./internal/agentd ./cmd -run 'Test(B353|WaitDeliveryPolicy|Follow|CardWait|Automation)' -count=1
   ```

   预期四包退出 0；输出必须包含 T1 policy/wait/follow、T2 card stdout/exit、T3 automation consumer 的正反测试。若某包没有匹配测试，应以原始 `[no tests to run]` 记录并补测试，不得把命令本身当完整验收。
5. 运行并取得真实 race 结果：

   ```text
   go test -race ./internal/client ./internal/ledger ./internal/agentd ./cmd -run 'Test(B353)' -count=1
   ```

   预期四包退出 0，且不出现 data race；测试必须实际覆盖 card follow 单回调/seq 顺序、过滤帧仍前进、同卡 wake 合并和 seen/cursor 去重。实现前的 `[no tests to run]` 不可复用为实现后的 pass。
6. 运行文档/图门禁：

   ```text
   rg -n 'card wait .*--follow|一次一挂|一次工作流只挂一次|delivery_failed|permission_auto_allow|decision_answered' skills/handoff/SKILL.md README.md
   git diff --check
   codegraph resolve --repo . --doc docs/superpowers/plans/b353-plan.md
   ```

   `rg` 必须能定位两类 harness、完整过滤集合、`delivery_failed`→`resume` 与 `decision_answered`；不得残留把裸 card wait 说成永远长挂的旧段。`git diff --check` 必须无输出退出 0；resolve 的每个 file#Symbol 必须是 `ok` 或 `moved`。执行者必须把实际原始输出追加台账。
7. T4 只负责机内回归与文档，不能把以下项目写成通过：真实 executor 产生事件、真实 `delivery_failed` 回复后 `resume`、PG LISTEN/跨机 relay/WS 关闭码与重启回放、agentd/协调者/executor 重启清理、Claude Code/grok Monitor 逐行唤醒、Linux/macOS/Windows shell/pipe 缓冲与真实宿主行为。它们交给协调者的真机验收清单，状态写“未验证，需真机”。

#### T4 验收

- 文档命令和行为表只说一张消费分类表；没有第二个 whitelist、第三条 wait 命令或子 agent follow 方案。
- 组合测试、race 测试、`rg`、`git diff --check`、plan resolve 均真实运行并原样入台账；未运行或失败不能填 pass。
- README 与 skill 的 `card wait` 退出/过滤/timeout/harness 文字与 T2/T3 实际行为逐字一致。

## 3. 五项计划自审

### 3.1 缺陷族对抗审查

1. **生命周期/状态机中断：** T2 的 idle controller 由 `Store.Follow` ctx 驱动，默认首动作、follow 终态、ctx 取消和 timeout 都走 `stop()` 等待 goroutine 退出；不创建持久进程/任务/PG 常驻订阅。WS/PG/跨机/重启仍“未验证，需真机”。
2. **静默失败/误导报错：** T1 的 WS/JSON 错误沿 client 返回；T2 的 envelope/room 解码、账本读取、Encode、timeout 均非零且含 task/card/seq/type 上下文；T3 的 map error 不会记成已唤醒；`delivery_failed` 明确进入 wake/resume。
3. **跨平台假设：** 本卡只改 Go CLI/client/agentd 消费和 Markdown，不新增 HOME、进程组、webview、权限或路径语义；stdout pipe/Monitor、PG/relay/真实 executor 行为列真机未验证。
4. **假红/假绿：** T1 穿过真实 store/WS→Wait/Follow，T2 穿过 CLI→Store.Follow→Encoder，T3 穿过 ledger→consume→Keystone/runner；每个正向断言有审计/系统/假策略反例；race 锁 seq/cursor/合并而非私有 switch 形状。
5. **门禁绕过：** 不新增写 ticket、直接执行 resume、旁路写卡或第二事件总线；当前 attempt、attach、seen/cursor、Keystone Decide、failure escalation 原闸保留。
6. **序列化边界：** 任务 source JSON→proto.Event→WaitDeliveryPolicy；task mirror envelope `task_type`/`payload`→cmd/wakeconsumer；RoomMessage JSON→card/wake；ledger.Event→stdout Encoder；skill/README 文本投影，均有真实边界断言或文档 rg 断言。
7. **枚举新值过白名单：** 无新增 enum；逐处核对七项任务假集合、mirrorSkip 三项、card event switch、wakeconsumer task type 映射、Keystone 三种既有 WakeKind。
8. **承重安全属性：** T1 cursor/seq 单次交付，T2 follow seq 不重复、审计不输出、Encode 成功才退出，T3 同卡批次一次 wake、seen/cursor 不重 launch、旧 attempt/attach/失败升级反例均有测试。
9. **webview/宿主差异：** 无 `d_web` 生产改动；终端逐行唤醒和 Monitor 现场行为不由 Go 单测外推，保持真机未验证标记。

### 3.2 序列化边界与测试对照

| 边界 | 产生端/消费端 | 计划断言 |
|---|---|---|
| task event JSON → `proto.Event` → policy | `internal/store`/WS → `Client.WaitEvent`/`FollowEvents` | T1 `TestB353WaitEventDeliversDeliveryFailedAfterAudit`、Follow 全量/过滤测试；审计 payload 与 seq 保留 |
| mirror envelope JSON → `task_type`/`payload` | `ledger.AppendMirroredEvent` → cmd/T3 decoder | T2 缺失/空/非空字段断言；T3 `appendMirroredForConsumer` 真实 envelope + policy/briefing 断言 |
| RoomMessage JSON → human/system predicate | `RecordRoomMessage` → card wait/wakeconsumer | T2/T3 覆盖 by_system 缺失、false、true、null、非法类型；缺失与 false 的 legacy 兼容含义被显式断言 |
| `ledger.Event` → stdout JSON | `Store.Follow` → `json.Encoder` | T2 逐行 unmarshal 与原 event seq/type/payload 等值；审计事件账本读取仍存在 |
| CLI/事件名文字 → harness 使用者 | skill/README | T4 `rg` 同时定位 `--follow`、一次一挂、七项 audit、`delivery_failed`→`resume`、`decision_answered` |

本卡没有新增字段；“字段缺失 vs 零值/null”通过 raw JSON boundary fixtures 明确分辨，不通过“两端各自有测试”替代穿缝回归。

### 3.3 接缝覆盖双向清单

| 声明缝 | 缝级测试入口 | 正/反行为 |
|---|---|---|
| `WaitDeliveryPolicy × Client.WaitEvent` | `TestB353WaitEventDeliversDeliveryFailedAfterAudit` | 假集合不返回，`delivery_failed` 返回且 payload/seq 保留 |
| `WaitDeliveryPolicy × Client.FollowEvents` | `TestB353FollowFiltersAndPreservesAllMode`（复用 `pushEvents`） | false 过滤、true 全量、任意帧刷新 idle |
| `cardWaitCmd/runCardWait × Store.Follow × stdout` | `TestB353CardWaitDefaultEmitsFirstActionAndExits`、`TestB353CardWaitFollowFiltersAndContinues`、`TestB353CardWaitStatusMovedOnlyChecksTerminal` | 可动作输出/审计不输出/默认一次/ follow 终态/原始 JSON |
| `cardWaitCmd × timeout/flag` | `TestB353CardWaitTimeoutModes`、`TestB353CardWaitHelpHasFollow` | 总时长 vs idle、124、flag 形状 |
| `consumeAutomationEventsOnce × automationWakeEvent × Keystone` | `TestB353AutomationUsesWaitDeliveryPolicy`、`TestB353AutomationMapsCardActionEvents` | task policy、卡原生 WakeKind、同卡合并、审计不 wake |
| `task_mirrored × current attempt/attach/cursor` | 既有 `TestB2336StaleAttemptDoesNotWake`、`TestAutomationAttachDefersAndThenWakes`、`TestAutomationCursorRewindIsIdempotent` 与 T3 新测试 | 闸、暂缓、去重、失败升级 |

反向覆盖：T1/T2/T3 的每一支测试入口均在上表某条声明缝上；上表每条声明缝至少有缝级断言。策略矩阵、decode boundary 属于附加内部锁，不能顶替这些入口。

### 3.4 用户故事归属

| spec 用户故事 | 具体 task/断言 |
|---|---|
| grok/Claude 用 `card wait --follow`，自动审批/comment 不逐行醒，提问/权限/`delivery_failed`/needs/decision/真人消息会醒 | T2 `TestB353CardWaitFollowFiltersAndContinues`；T3 `TestB353AutomationMapsCardActionEvents`；T4 skill/README harness 文案 |
| 小队自动化遇 `delivery_failed` 会醒去 resume | T1 policy + T3 `TestB353AutomationUsesWaitDeliveryPolicy`；T4 事件表/排障文案 |
| opencode/Codex 默认一次一挂，skill 不暗示后台 follow | T2 `TestB353CardWaitDefaultEmitsFirstActionAndExits`/timeout；T4 skill/README 一次一挂文字 |

## 4. 未验证、需真机的边界清单

以下不是本机计划的通过判据，协调者验收时逐条标“未验证，需真机”，完成现场命令后再改状态：

1. 真实 agentd/真实 executor 产生 question、permission_request、`delivery_failed`、stalled、completed、turn_failed、failed、archived、压力告警，核对三条链收到正确 source task/seq。
2. 真实 reply 送达失败后 `delivery_failed` 唤醒协调者并由 `handoff resume` 恢复，工单/等待态/进程不被伪成功消耗。
3. Claude Code/grok 的真实 Monitor 挂 `card wait --follow` 逐行唤醒；opencode/Codex 默认一次一挂；审计事件不打断主会话。
4. 直连/relay 断开恢复、agentd/协调者/executor 重启后的 WS/PG/SQLite seq 回放、cursor/seen 去重、进程/临时资源回收。
5. PG LISTEN 只作铃声、ticker 兜底；真实 WS 关闭码、单消息读限、重连退避与 idle 跨重连。
6. Linux/macOS/Windows 目标上 stdout pipe/缓冲、shell、权限、网络与宿主 Monitor 不改变逐行 JSON、0/124 退出和 Keystone wake。
7. 并发产生同卡 mirror/decision/room/needs 并切换 attach 时，真实消费者不重复 launch、不跳过当前 attempt、不把自生 needs_human 变成自激唤醒。

## 5. 计划自检与允许的 harness 例外

- spec 故事三条已在 §3.4 逐条落到 task/测试；冻结 #1–#48 已按唯一策略、card wait、wakeconsumer 三组落到 T1–T3。
- 占位符扫描无命中；每个实现 task 都有精确文件集、精确签名、基线命令、最小测试范围、日志/注释动作和 pass/fail 断言。
- T2 允许复用 `cmd/ledgercli_test.go#runLedgerCLI` 与已有 ledger 夹具，因为 cobra 包级 flag、临时 config/SQLite、Store.Follow 并发写入的形态只能从既有 harness 构造；本计划逐条列出测试入口与断言，未用该例外隐藏测试骨架。
- T3 允许复用 `internal/agentd/wakeconsumer_test.go` 的真实 SQLite ledger + `SetupAutomation` + fake runner/Keystone harness；本计划逐条列出事件、入口和观察结果，内部 helper 只能附加不能顶替 `consumeAutomationEventsOnce`。
- T4 的三链组合、race、rg、diff-check、resolve 是实现者执行的验收步骤，不派发给其它 executor；真实跨机/宿主清单由协调者执行。
- 计划层跨卡独立审计（冻结物逐条、A/B Produces↔Consumes 逐字符、spec 故事归属）在本计划齐稿后由协调者完成；本节点不派发审计子任务、不调用 handoff CLI。
