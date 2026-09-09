# B351 远端孤儿回收与重试改名：契约冻结

状态：**本提交随 `codegraph/target.json`、本分支视图、Ticket 0 骨架与台账冻结**

上游 spec：`docs/superpowers/specs/b351.md`，头部状态为「已批准」（2026-09-09）。本契约不重新裁决 spec 语义，只把已批准语义落成可编译的接缝、数据形状与对账断言。

冻结物：

- 本文件；
- `codegraph/target.json` 中既有 `d_cli → d_transport`、`d_gateway → d_transport` 的 B351 注记；
- `codegraph/diffs/cards-B351-charter.json`；
- Ticket 0 的 `DispatchCompensator`、`Store.RecordDispatchRound`、`Client.StopAndReclaim` 空壳；
- `docs/superpowers/ledgers/2026-09-09-b351-contract-ledger.md`。

架构形态：**按子系统分域的平铺领域包，无横向 controller/service/dao 分层**。本卡不改 `codegraph/best.json` 的结构树；账本计数留在 `d_ledger`，补偿策略由 `d_ledgerstep` 注入，真实 client 选路仍由 CLI 与 agentd 组装点负责。

## 1. 现状签名与依赖行为

以下均为本轮在当前工作树查到的代码事实。图查询中标成 `moved` 的节点以源码符号锚为准；行号是本轮文档生成时的定位。

| 接缝/事实 | 当前签名或行为 | 现状出处 |
|---|---|---|
| 共享派发入口 | `func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)` | `internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`（第 139 行） |
| 传输注入 | `type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, baseCommit string, err error)` | `internal/ledgerstep/dispatch.go#Transport`（第 51 行） |
| 现有本地落账 | `func (s *Store) RecordDispatchAndLinkTask(cardID string, snap DispatchSnapshot) (int64, int64, error)` | `internal/ledger/events.go:167`（当前代码；该符号未被 baseline 图覆盖） |
| 非审阅轮计数 | `func (s *Store) PurposeRounds(cardID, purpose string) (int, error)`；当前只遍历 `TasksOf(cardID)` 后按 `link.Purpose` 计数 | `internal/ledger/events.go#Store.PurposeRounds`（第 610 行）、`internal/ledger/tasks.go#Store.TasksOf`（第 48 行） |
| 审阅轮计数 | `func (s *Store) ReviewRounds(cardID string) (int, error)`，当前委托 `PurposeRounds(cardID, PurposeReview)` | `internal/ledger/events.go#Store.ReviewRounds`（第 626 行） |
| 写闸错误 | `ErrWriteGateClosed` 是现有 sentinel；ViaTemplate 在 Transport 成功后检查 `req.WriteGate` | `internal/ledgerstep/node.go#ErrWriteGateClosed`（第 21 行）、`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`（第 367-373 行） |
| 主动中止 | `func (c *Client) Stop(ctx context.Context, taskID string) (worktreeRemoved bool, err error)`；只接受 HTTP 200，响应体 `worktree_removed` 缺失按 false | `internal/client/client.go#Client.Stop`（第 1119 行） |
| worktree 回收 | `func (c *Client) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)`；POST body 是 `map[string]bool{"force": force}` | `internal/client/client.go#Client.Reclaim`（第 600 行） |
| 统一 HTTP 请求 | `func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error)` 使用 `http.NewRequestWithContext`，ctx 取消/超时原样返回；非 ctx 传输错误包装为既有 `ErrUnreachable` 或 `ErrTunnelDisconnected` | `internal/client/client.go#Client.do`（第 382-429 行） |
| HTTP client 默认 | `NewWithWSTiming` 构造 `http.Client{Transport: ...}`，未设置 `Timeout`；拨号层使用既有 `dialTimeout` | `internal/client/client.go#NewWithWSTiming`（第 228-261 行） |
| Stop 服务端 | `handleStop` 调 `s.mgr.Stop(r.Context(), taskID)`，成功 HTTP 200；`writeManagerError` 把任务不存在映射 404、`store.ErrBadTransit` 映射 409 | `internal/agentd/server.go#Server.handleStop`（第 1729 行）、`internal/agentd/server.go#Server.writeManagerError`（第 2169 行） |
| Reclaim 服务端 | `handleReclaim` 解码 `force` 后调 `s.mgr.Reclaim(r.Context(), taskID, body.Force)`；成功 HTTP 200，任务不存在 404，状态/脏树/非 managed 等拒绝 409 | `internal/agentd/server.go#Server.handleReclaim`（第 968 行）、`internal/agentd/server.go#Server.writeReclaimError`（第 994 行） |
| Stop 的资源语义 | `Manager.Stop` 停 executor 并落 failed，但不删除 worktree；现有注释明确 `worktreeRemoved=false` | `internal/agentd/manager.go#Manager.Stop`（第 1824 行） |
| Reclaim 的资源语义 | `Manager.Reclaim` 只回收 managed worktree 资源；force 允许脏树强删，不删除任务分支，不改任务状态 | `internal/agentd/reclaim.go#Manager.Reclaim`（第 252 行） |
| CLI 派发组装 | `func targetClient(target string) (*client.Client, func(), error)`：空目标走 `LocalEndpoint` + `client.New`，非空目标走 `newTargetClientNamed` | `cmd/card_dispatch.go#targetClient`（第 194 行） |
| CLI transport | `func cliTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (string, string, error)`，经 `dispatchTransportWithOpts` 使用同一目标选路 | `cmd/card_dispatch.go#cliTransport`（第 120 行） |
| agentd 节点组装 | `func (s *Server) startCardStep(cardID string, req proto.CardStepReq) error` 构造 `ledgerstep.Dispatcher`，Transport 为 `s.stepTransport` | `internal/agentd/cardstep.go#Server.startCardStep`（第 47 行） |
| agentd transport | `func (s *Server) stepTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (string, string, error)` 先归一 target，再调用 `s.clientForTarget` 与 `Client.Dispatch` | `internal/agentd/cardstep.go#Server.stepTransport`（第 314 行） |
| agentd 目标选路 | `func (s *Server) clientForTarget(target string) (*client.Client, error)`：本机/空目标走 local client，远端走 Pool | `internal/agentd/server.go#Server.clientForTarget`（第 421 行） |
| `--step` 排除项 | `func runStepDispatch(cmd *cobra.Command, id, node string) error` 是直接 POST `CardStep` 的 CLI 路径，不是 bare card dispatch 的 Dispatcher 组装点 | `cmd/card_node.go#runStepDispatch`（第 122 行）；spec §4.2 已明确排除 |

### 1.1 常量与状态码执法

本卡不新增 HTTP 路径、wire 状态名、事件类型或错误码。对侧事实如下：

- Stop 的 404/409 由 `handleStop` → `writeManagerError` 发出；client 当前把非 200 统一成含状态码的私有 `httpStatusError`。
- Reclaim 的 404、409 由 `handleReclaim` → `writeReclaimError` 发出；client 的 404 会额外 GET `/api/reclaim`，双 404 才消费为既有 `ErrReclaimUnsupported`，任务不存在的真实 404 保留为 HTTP 错误；409 消费为既有 `*ReclaimRejected`。
- `ErrWriteGateClosed` 由 `ViaTemplate` 产生、既有 ledgerstep 测试消费；`ledger.EvDispatched` 与 `ledger.EvComment` 是既有事件类型。本卡不把失败尝试伪装成任何一个事件。
- `store.ErrBadTransit` 是 agentd Manager.Stop 产生、server 错误映射消费的现有 sentinel，不复制一份“已终态”常量。

上述状态码/常量都有真实生产发出者与消费点；没有把零使用的死常量当事实源。

### 1.2 图覆盖债

本轮亲自执行 `codegraph --repo . sym Store.RecordDispatchAndLinkTask` 与 `sym n_ledger_Store_RecordDispatchAndLinkTask`，两次均返回原文 `Error: 符号 "..." 不在图中（图未覆盖或名字有误）；近似候选: []`。因此 `RecordDispatchAndLinkTask` 在本文件只用 `internal/ledger/events.go:167` 的源码定位，不把缺失图节点冒充符号锚；下游若需图查询，应先补该现状节点或沿源码查证。

## 2. Ticket 0 精确签名

Ticket 0 只落空壳与字段镜像，不接线可观测行为；其本轮源码证据如下。

```go
// internal/ledgerstep/dispatch.go#DispatchCompensator
type DispatchCompensator func(ctx context.Context, target, taskID string) error

// internal/ledgerstep/dispatch.go#Dispatcher
Compensate DispatchCompensator

// internal/ledger/dispatch_rounds.go#Store.RecordDispatchRound
func (s *Store) RecordDispatchRound(cardID, purpose string) error

// internal/client/compensate.go#Client.StopAndReclaim
func (c *Client) StopAndReclaim(ctx context.Context, taskID string) error
```

Ticket 0 的两个方法分别返回包内 `...Unwired` sentinel，**不发 HTTP、不写账本**；这是空壳而不是成功实现。`Dispatcher.Compensate == nil` 在空壳期保持现状行为，接线由 implement 完成。新增方法的签名是实现节点的跨包窄缝：ledgerstep 只接收回调，client 内部才有权解释现有 `httpStatusError`、`ErrReclaimUnsupported` 与 `ReclaimRejected`。

## 3. 冻结清单

以下每条都是可独立判定 pass/fail 的接缝断言；内部函数命名、SQL 遍历顺序和日志文字不属于冻结清单。

### 3.1 失败尝试与轮次账本

1. 只有 `Transport` 返回 `err == nil` 后，才允许为该次尝试写失败轮次事实。
2. `Transport` 返回错误时，不写失败轮次事实。
3. 每次 Transport 成功而本地写闸关闭的尝试，写一条 `card_id × purpose` 失败轮次事实。
4. 每次 Transport 成功而 `RecordDispatchAndLinkTask` 失败的尝试，写一条 `card_id × purpose` 失败轮次事实。
5. 写闸关闭的失败尝试不追加 `EvDispatched`。
6. 写闸关闭的失败尝试不写入 `card_tasks`。
7. 本地快照/挂账失败的尝试不追加 `EvDispatched`。
8. 本地快照/挂账失败的尝试不写入 `card_tasks`。
9. 失败轮次事实不追加任何 ledger event。
10. 失败轮次事实不写入 `card_tasks`。
11. 失败轮次事实写入失败时，原始写闸或账本错误仍是 ViaTemplate 的返回错误。
12. `PurposeRounds(cardID, purpose)` 的结果等于 `card_tasks` 中该卡该 purpose 行数与失败轮次表中该卡该 purpose 行数之和。
13. `ReviewRounds(cardID)` 继续委托同一 additive 口径，并计入 review 失败轮次。
14. 失败轮次计数不读取 `CountRounds` 的回合/人工 reset 事件。
15. 失败轮次计数不读取 `EvDispatched` 的反向事件扫描。
16. 失败轮次表中的一行只表达一次失败尝试，不携带 task identity、target、branch 或 actor。
17. `card_dispatch_rounds.card_id` 为非空字符串并引用 `cards(id)`。
18. `card_dispatch_rounds.purpose` 为非空字符串。
19. `card_dispatch_rounds.created_at` 为非空时间值。
20. PostgreSQL 与 SQLite 都创建同名 `card_dispatch_rounds` 表。
21. PostgreSQL 与 SQLite 都为 `card_dispatch_rounds(card_id, purpose)` 提供查询索引。
22. `RecordDispatchRound` 复用 Store 的 `mutate` 写事务和 `Store.timeNow` 时钟。
23. 失败轮次落账事务只写失败轮次表，不触发 event listener。

### 3.2 补偿回调与原始错误

24. `Dispatcher.Compensate` 的回调参数顺序固定为 `ctx, target, taskID`。
25. 回调收到的 `target` 是本次 Transport 实际使用的归一目标。
26. 回调收到的 `taskID` 是本次 Transport 返回的 task id。
27. 写闸关闭后，先落一条失败轮次事实，再调用非 nil 补偿回调。
28. 本地快照/挂账错误后，先落一条失败轮次事实，再调用非 nil 补偿回调。
29. 每个满足补偿条件的失败尝试至多调用一次补偿回调。
30. 补偿回调返回错误时只记录带 card、target、task 和原始错误上下文的日志。
31. 补偿回调返回错误时不替换 ViaTemplate 的原始写闸/账本错误。
32. `Compensate == nil` 时仍写失败轮次事实并记录缺少补偿钩子的日志。
33. `Compensate == nil` 时不 panic。
34. 失败补偿不进入 `Transport`，不创建第二个 task。
35. 补偿不调用 branch delete、全机扫描或新的命令/HTTP 入口。

### 3.3 Stop/Reclaim 既有 client 语义

36. `Client.StopAndReclaim` 使用收到的同一个 `ctx` 调用 Stop。
37. Stop 返回 nil 时，`StopAndReclaim` 调用 Reclaim。
38. Stop 返回 HTTP 409 时，`StopAndReclaim` 调用 Reclaim。
39. Stop 返回 HTTP 404 时，`StopAndReclaim` 返回 nil。
40. Stop 返回非 404/409 错误时，`StopAndReclaim` 不调用 Reclaim。
41. Reclaim 调用的 force 参数固定为 true。
42. Reclaim 返回 HTTP 404 时，`StopAndReclaim` 返回 nil。
43. Reclaim 返回既有 `ErrReclaimUnsupported` 时，`StopAndReclaim` 将过旧对端视作补偿软成功。
44. Reclaim 返回非 404 错误时，`StopAndReclaim` 返回该补偿错误。
45. Stop 返回 HTTP 409 且 Reclaim 返回 HTTP 404 时，`StopAndReclaim` 返回 nil。
46. Stop 返回 nil 且 Reclaim 返回非 404 错误时，`StopAndReclaim` 返回该补偿错误。
47. Stop/Reclaim 的 HTTP 方法与路径仍是既有 `POST /api/tasks/{id}/stop` 与 `POST /api/tasks/{id}/reclaim`。
48. 补偿请求不新增请求体字段；Reclaim 继续发送 `{"force":true}`。
49. 补偿不设置新的 client 超时；ctx deadline/cancel 仍由 `Client.do` 透传。
50. `StopAndReclaim` 不删除任务分支。
51. `StopAndReclaim` 不改变任务状态机之外的账本状态。

### 3.4 两处生产组装

52. bare card dispatch 的 `ledgerstep.Dispatcher` 注入非 nil `Compensate`。
53. bare card dispatch 的补偿闭包用 `targetClient(target)` 选择 client。
54. bare card dispatch 的补偿闭包调用选中 client 的 `StopAndReclaim(ctx, taskID)`。
55. bare card dispatch 补偿闭包调用 `targetClient` 返回的 cleanup。
56. agentd `startCardStep` 的 `ledgerstep.Dispatcher` 注入非 nil `Compensate`。
57. agentd 节点补偿闭包用 `Server.clientForTarget(target)` 选择 client。
58. agentd 节点补偿闭包调用选中 client 的 `StopAndReclaim(ctx, taskID)`。
59. agentd 节点补偿闭包不自行复制 target Pool/relay 选路规则。
60. `cmd/card_node.go#runStepDispatch` 不新增 Dispatcher 补偿装配。
61. 两处生产补偿都使用 ViaTemplate 已解析的 target，不从 task id 反查另一台机器。

### 3.5 重试分支命名

62. 非 review purpose 的首个派发仍使用无后缀的 `<prefix>/<card>-<purpose>`。
63. 非 review purpose 的后续派发使用 `<prefix>/<card>-<purpose>-<PurposeRounds(card,purpose)+1>`。
64. review purpose 使用 `<prefix>/<card>-review-<ReviewRounds(card)+1>`。
65. 失败轮次在本地挂账失败后计入下一次同 purpose 分支编号。
66. 失败轮次在写闸关闭后计入下一次同 purpose 分支编号。
67. 传输失败不增加下一次分支编号。
68. `CountRounds` 的人工 reset 不减少 `PurposeRounds` 或 `ReviewRounds` 的派发历史计数。

## 4. 三重闸门拍板记录

**拍板 1：补偿策略放在 ViaTemplate 的失败出口，由 Dispatcher 注入，不放进 Transport。** 该决定难逆转：CLI 与 agentd 两个组装点都必须提供真实目标选路和 cleanup；无上下文时后人会自然把回收塞进 `Transport` 或 `Store.RecordDispatchAndLinkTask`，但那会漏掉写闸关闭分支、把账本事务和远端副作用混在一起；取舍是拒绝在 Transport 内做“传输成功后的本地账本补偿”，也不让 ledger 包 import client。显式不做：Transport 失败不补偿，`runStepDispatch` 不复制一套补偿入口，ledger 不主动反调 Stop/Reclaim。

**拍板 2：失败轮次使用独立的 count-only 表并与 `card_tasks` 相加，不写 `card_tasks`、不造失败 event。** 该决定难逆转：schema、轮次查询、重试命名和失败路径测试共同依赖这条数据形状；无上下文时“把失败 task 也挂进 card_tasks”看起来省表，但会把不存在的本地任务伪装成已挂账派发；取舍是接受一次新表迁移，换取失败事实不进入正常 task 投影、不被事务回消。显式不做：不保存 task id/target/branch，不通过 `CountRounds` 或 EvDispatched 反向扫描推导，不增加全机扫描。

**拍板 3：补偿动作固定为 Stop 后 Reclaim(force=true)，并在 client 内收口 404/过旧软成功。** 该决定难逆转：它跨 ledgerstep、client、CLI 与 agentd 两个组装点；无上下文时仅调用 Stop 会留下 Stop 明确保留的 managed worktree；取舍是增加一个 client 内部复合接缝，换取两处组装不复制私有 HTTP 状态码判定。显式不做：不调用 ForceReclaim，不删除分支，不新增端点；Stop 404 不再发第二个 reclaim 请求，Stop 409 才继续 Reclaim。

无其他同时命中三重闸门的决定。步骤顺序“先记失败轮次、再补偿”已由清单第 27/28 条冻结；它不另造一条拍板记录。

## 5. 依赖方向、预算与图

`codegraph/target.json` 只更新既有方向注记：

- `d_cli → d_transport`：bare card dispatch 的补偿经既有 `targetClient` 与 `client.Client.StopAndReclaim`；预算仍 10；
- `d_gateway → d_transport`：agentd `startCardStep` 的补偿经既有 `Server.clientForTarget` 与 `client.Client.StopAndReclaim`；预算仍 9；
- 不声明 `d_ledger → d_transport`。`DispatchCompensator` 是 ledgerstep 的注入类型，ledger/ledgerstep 不 import client；
- 不改 `best.json` 结构树，不新增领域，不改 `d_cli → d_ledger` 或 `d_gateway → d_ledger` 预算。

Ticket 0 新符号随本提交写入 `codegraph/diffs/cards-B351-charter.json`；下游查询新符号须使用 `--view cards/B351-charter`。视图只记录三个新增符号与 `Dispatcher` 字段变更，不伪造尚未接线的生产调用边。

## 6. Ticket 0 边界、可执行冻结与移交 plan

Ticket 0 已落：

- `DispatchCompensator` 类型和 `Dispatcher.Compensate` 字段；
- `Store.RecordDispatchRound` 的签名与未接线 sentinel；
- `Client.StopAndReclaim` 的签名与未接线 sentinel；
- 三个新符号的本分支视图记录。

Ticket 0 未落：失败轮次 DDL/查询、ViaTemplate 失败接线、两处生产闭包、Stop/Reclaim HTTP 调用、重试命名变化。以上行为必须由下游 implement 落地并以能变红的测试锁定；不得把空壳返回的 `...Unwired` 当作真实补偿成功。

本卡没有哈希、密钥派生或编码格式的新冻结向量；因此无金样本测试条目。Stop/Reclaim 的 JSON `force=true` 是既有请求形状，本轮不复制一份 wire 金样本。

### 移交 plan 附区

以下是查证期为实现节点整理的实现级落点，不计入冻结清单；plan 吸收后应在此节头部标注“已由 plan〈文档〉吸收（日期）”：

- `card_dispatch_rounds` 的 PG/SQLite DDL 放在 `internal/ledger/store.go#ddlStatements` 两个方言分支，并由 `ensureSchema` 幂等创建；查询使用 `(card_id, purpose)` 索引；
- `RecordDispatchRound` 先以 `getCardTx` 验证卡存在，再用 Store 的 `mutate` 插入一行，时间用 `s.timeNow()`，不经 `appendEvent`；
- `PurposeRounds` 保持已有 `TasksOf` purpose 计数，再加 `card_dispatch_rounds` 计数；`ReviewRounds` 不改名、不另造查询；
- `ViaTemplate` 在 WriteGate 失败出口和 `RecordDispatchAndLinkTask` 失败出口各调用一次共同的“记录失败轮次 + 补偿 + 原始错误返回”路径；去除现有“远端由真机项回收”的过期注释；
- `Client.StopAndReclaim` 在 client 包内用 `errors.As` 判定现有 `httpStatusError` 的 404/409，并把 `ErrReclaimUnsupported` 作为过旧软成功；它不改变 Stop/Reclaim 的原方法；
- CLI 与 agentd 闭包都复用各自现有 cleanup；defer/错误日志策略不得让 cleanup 错误替换原始账本错误。

## 7. 本轮法定核对与交棒欠账

1. 契约增量文档：本文件已落盘；现状签名均带源码文件与符号锚。
2. 目标图：`codegraph/target.json` 已更新；本提交同时冻结。Ticket 0 视图 diff 已落盘。
3. Ticket 0 编译：本轮交棒前运行 `go build ./...`，以本轮输出为准。
4. 金样本：本卡无哈希/密钥/编码新增冻结，故无金样本测试。
5. 三重闸门：第 4 节已记录全部命中决定；无其他命中。

本轮全量测试欠账（交棒时显式携带）：`go test ./... -count=1` 退出码 1；单独重跑 `go test ./cmd -run '^TestRepoContractGate$' -count=1` 仍报告 `d_execution→d_orchestration` 的 `dead-contract` 与 `dead-interface`（`ApprovalClient`）两条原始违规。该失败不改变本卡已通过的 `go build ./...`、三包定向测试、视图 `validate` 与文档锚点核验结果；是否在后续节点处理由协调者认领。

交棒对象：`breakdown`。下游必须先读取本文件与 spec，再按移交 plan 吸收实现级落点；若实现需要改变上述签名、表语义、依赖方向或状态码处置，必须重新走 contract 与审核。
