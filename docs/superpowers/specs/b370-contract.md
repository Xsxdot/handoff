# B370 契约增量：镜像身份闸降级判定（能判定才拦）

**上游状态：已批准**（源 spec `docs/superpowers/specs/b370.md`，头部状态行「已批准」（2026-09-14，用户原话「批准，进 contract」））
**级别：L3；档位：轻档**　**卡：B370**　**基线分支：`cards/B233.1-charter-7`**　**本分支：`cards/B370-charter-4`**
**冻结状态：本提交随 `codegraph/target.json`、`codegraph/diffs/cards-B370-charter-4.json` 与 Ticket 0 骨架同批冻结**
**架构形态：按子系统分域的平铺领域包，无横向 controller/service/dao 分层**（沿用 `codegraph/best.json`）
**台账：** `docs/superpowers/ledgers/2026-09-14-b370-contract-ledger.md`

本契约是 B349 冻结正文（`docs/superpowers/specs/b349.md:79`「不得猜成当前尝试」）的**显式修订**：把身份闸的判定方向从「缺身份即拒绝」改为「能证明迟到才拦」，并把判定正文从两处消费点收成一处共享实现。本契约只冻结接缝断言与跨子系统决定；Ticket 0 落共享符号、reason 枚举与两处调用点接线，三分支降级语义与两份旧快照扫描的退役留给 implement。

## 1. 上游状态位与边界

- 上游 spec 头部状态行已回写「已批准」，本节点开工核对通过（台账 §1 条目 3）。
- 本卡修订的是 B349 冻结的消费规则正文，不改 wire、不改事件类型、不改 HTTP/前端契约、不扩 `mirrorSkip`、不把判定下沉成账本派生查询（B349 P3 保留）。
- 流向不变：镜像按现 `mirrorSkip` 全量落账；`source_target`/`source_task`/`source_seq` 仍是列上权威身份；envelope 身份仍是投影。

## 2. 现状查证

### 2.1 已查证签名与类型

下表是本轮对现状代码的事实查证；代码出处使用符号锚（括号内行号只是本轮读数）。

| 接缝 | 现状代码事实 | 本卡冻结后的精确形状 |
| --- | --- | --- |
| 闸的两处实现 | `cmd/card_wait.go#cardWaitEventActionable`（`:240`）内含 `cardWaitCurrentWorkflowAttempt`（`:195`）；`internal/agentd/wakeconsumer.go#Server.acceptsCurrentWorkflowAttempt`（`:110`）内含 `Server.currentWorkflowAttempt`（`:57`） | 判定正文抽到 `internal/client` 一处；两处消费点解码 envelope 后调用共享符号（形状见 §3） |
| CLI 消费点 | `cmd/card_wait.go#cardWaitEventActionable`（`:240`）：`func cardWaitEventActionable(st *ledger.Store, ev ledger.Event) (bool, error)` | 签名不变；`EvTaskMirrored` 分支解包后填 `client.WakeGateEvent` 调 `client.JudgeMirroredWake`，再走 `client.WaitDeliveryPolicy` |
| CLI 主缝 | `cmd/card_wait.go#runCardWait`（`:56`）：`func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration) error` | 签名不变；事件所属 `e.CardID` 仍是查快照的卡号 |
| 自动化消费点 | `internal/agentd/wakeconsumer.go#Server.acceptsCurrentWorkflowAttempt`（`:110`）：`func (s *Server) acceptsCurrentWorkflowAttempt(ev proto.LedgerEvent) (bool, error)` | 签名不变；改为调 `client.JudgeMirroredWake(s.ledger, client.WakeGateEvent{...})`，把 `Deliver` 翻成布尔出口 |
| 自动化主缝 | `internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`（`:302`）：`func (s *Server) consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error)` | 签名不变；被拦仍记 `seen` 并推进游标，不打死消费循环 |
| 账本事件实体 | `internal/ledger/types.go#Event`（`:160`）有 `SourceTarget string`、`SourceTask string`、`SourceSeq int64` | 签名不变；source 三列仍是权威身份 |
| 派发快照 | `internal/ledger/events.go#DispatchSnapshot`（`:115`）有 `Target`、`TaskID`、`Node`、`Attempt` | 签名不变；当前身份只取最新合格 `EvDispatched` 快照 |
| 事件读面 | `internal/ledger/events.go#Store.EventsFromAsc`（`:63`）：`func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error)` | 签名不变；共享实现经它分页读到尾 |
| 任务策略 | `internal/client/delivery.go#WaitDeliveryPolicy`（`:23`）：`func WaitDeliveryPolicy(t proto.EventType) bool` | 签名不变；仍在身份闸通过后调用，不复制策略集合 |
| 组装点 | `internal/agentd/server.go#Server.SetupAutomation`（`:980`）`func (s *Server) SetupAutomation(st *ledger.Store)`；`cmd/agentd.go#setupLedger`（`:474`）先 `srv.SetLedger(lst)`（`:485`）再 `srv.SetupAutomation(lst)`（`:489`） | 签名不变；`s.ledger` 与 `autoLedger` 在同装配序列落地（生产与 `SetupAutomationForTest` 均先注入 ledger） |

### 2.2 依赖与对侧常量查证

- `ledger.EvDispatched`、`ledger.EvTaskMirrored` 的生产者/消费者活跃，字面值不变。
- `client.WaitDeliveryPolicy` 仍是任务类型可动作性的唯一事实源；身份闸通过后才调用它，不在 ledger/mirror/CLI 复制任务类型集合。
- **依赖边事实**（`go list`）：`internal/client` 当前不依赖 `internal/ledger`；`internal/ledger` 的依赖闭包不含 `internal/client`（无环）；`cmd` 依赖 `internal/client` 与 `internal/agentd`；`internal/agentd` 依赖 `internal/client`、`internal/ledger`；全仓无 `agentd→cmd` 反向依赖。
- **图机制事实**（`codegraph/check.go` v0.10.0）：`dead-contract` 只认**视图内**活跃 call/implements/组装点豁免边；无 `--view` 时视图 = 纯 `baseline.json`；`entry` 按被调方容器 Label 匹配。

依赖库既成行为也是本契约的一部分：

| 行为 | 依赖源码出处 | 冻结影响 |
| --- | --- | --- |
| 账本 asc 读以 `fromSeq` 排他、升序、每页上限 500 | `internal/ledger/events.go#Store.EventsFromAsc`（`:63`） | 共享快照扫描必须分页到尾，不因 500 截断误判「无当前快照」 |
| `json` 对 `*string` 区分键缺失与空串 | Go `encoding/json` 未设字段保持 `nil` | envelope `node`/`attempt` 用 `*string` 区分「旧写入者缺键」与「新写入者空值」 |

## 3. 精确契约形状

### 3.1 共享实现位置与导出面

共享判定与快照取数落 `internal/client`（与 `WaitDeliveryPolicy` 同包），新增方向 `d_transport→d_ledger`。导出面冻结为：

```go
// internal/client/wakegate.go

type WakeGateReason string

const (
	WakeGateCurrentAttempt             WakeGateReason = "current_attempt"
	WakeGateStaleAttempt               WakeGateReason = "stale_attempt"
	WakeGateEmptyEnvelopeIdentity      WakeGateReason = "empty_workflow_identity"
	WakeGateSnapshotNoWorkflowIdentity WakeGateReason = "snapshot_without_workflow_identity"
	WakeGateSnapshotNotFound           WakeGateReason = "snapshot_not_found"
	WakeGateMissingSourceTask          WakeGateReason = "missing_source_task" // 保留给 implement：身份非空且缺 source_task 的独立 reason
)

type WakeGateDecision struct {
	Deliver bool
	Reason  WakeGateReason
}

// WakeGateEvent 是两处消费点喂给身份闸的事件最小投影。
// cmd 用 ledger.Event、agentd 用 proto.LedgerEvent，各自解码 envelope 后填本结构。
type WakeGateEvent struct {
	CardID       string
	Seq          int64
	Node         *string
	Attempt      *string
	TaskType     string
	SourceTask   string
	SourceTarget string
	SourceSeq    int64
}

func CurrentWorkflowAttempt(st *ledger.Store, cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error)

func JudgeMirroredWake(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error)
```

`CurrentWorkflowAttempt` 语义（B349 保留）：从 `st.EventsFromAsc([]string{cardID}, from, 500)` 按 `Seq` 升序分页读到尾，只接受 `EvDispatched` 且 `Node == node && Node != "" && Attempt != "" && TaskID == Attempt` 的快照，最大事件 seq 的一条为当前身份；`found=false` 表示无合格快照。

### 3.2 判定正文（B370「能判定才拦」）

`JudgeMirroredWake` 返回 `WakeGateDecision`，`Deliver=true` 表示该 `task_mirrored` 可进入类型表判断。判定正文：

**A. 身份非空**（envelope `node` 与 `attempt` 均存在且非空）——按 B349 正文保留：

1. 存在该事件所属卡、该 node 的当前合格快照。
2. `snapshot.TaskID == snapshot.Attempt`。
3. `envelope.attempt == snapshot.Attempt`。
4. `ev.SourceTask == snapshot.Attempt`。
5. `ev.SourceTarget == snapshot.Target`（两边都空算匹配）。

五项全真 → `Deliver=true, Reason=WakeGateCurrentAttempt`；否则 `Deliver=false`。`!found`、旧 attempt、`source_task` 或 `source_target` 不匹配都落 `WakeGateStaleAttempt`。缺 `source_task` 自然无法满足第 4 项，闭集拒绝（`Deliver=false`）。

**B. 身份缺失或为空**（缺 `node`/`attempt` 键，或键值对为空串）——**改用 source 列匹配**（本卡标题）。三条降级分支：

1. **能定位该 task 的派发快照，且快照带工作流身份** → 与 source 列比对：`ev.SourceTask == snapshot.Attempt && ev.SourceTarget == snapshot.Target` 相等 = 放行（`Reason=WakeGateCurrentAttempt`）；不等 = 丢（`Reason=WakeGateStaleAttempt`，防迟到保留）。
2. **能定位快照，但快照无工作流身份**（裸派发 / 旧派发 / 旧写入者）→ 无从判迟到 = 放行（`Reason=WakeGateSnapshotNoWorkflowIdentity`）。
3. **找不到该 task 的派发快照** → 无从判迟到 = 放行（`Reason=WakeGateSnapshotNotFound`，相对 B349 的 `!found` 反转）。

定位规则：`node` 非空时按 `(cardID, node)` 用 `CurrentWorkflowAttempt` 定位；`node` 缺失或空时无从按节点定位，以 `ev.SourceTask` 在卡上匹配合格快照。分支 2/3 的 reason 区分只服务可观测性（§3.3）。

**C. reason 可观测**：判定结果必须携带 `reason`（枚举）；两个消费点按 reason 落日志——放行与拦截都留痕。

### 3.3 消费点接线

- `cmd/card_wait.go#cardWaitEventActionable`：`EvTaskMirrored` 分支解包 envelope 后调 `client.JudgeMirroredWake(st, client.WakeGateEvent{...})`；`Deliver=false` 不 Encode，`Deliver=true` 再调 `client.WaitDeliveryPolicy`。解码失败/缺 `task_type`/`payload` 仍带上文返回错误，不静默降级成审计。
- `internal/agentd/wakeconsumer.go#Server.acceptsCurrentWorkflowAttempt`：解包后调 `client.JudgeMirroredWake(s.ledger, client.WakeGateEvent{...})`，把 `Deliver` 翻成布尔出口；被拦仍走既有 `seen` + 游标推进路径。

## 4. 依赖方向、组装点与预算

- **新增方向 `d_transport→d_ledger`**：`internal/client` 读 `ledger.Store.EventsFromAsc` 与 `ledger.DispatchSnapshot`。`target.json` 显式声明，`entries` = `["ledger.Store", "ledger 实体"]`，`legacyBudget` = 0（一律经这两个公开面，不再扩大）。
- `d_cli→d_transport` 与 `d_gateway→d_transport` 各补 `client（包级函数）` entry，承载 `JudgeMirroredWake` 调用边；预算不变（12 / 9）。
- `d_transport→d_protocol` 沿用既有方向（`WaitDeliveryPolicy` 入参为 `proto.EventType`），不改。
- 组装点不变（`main.go`、`internal/agentd/server.go`、`cmd/agentd.go`）；**不新增** agentd→cmd 反向依赖。
- `best.json` 结构树不变：新符号复用既有容器 `k_client_fn` / `k_client_model`（`d_transport_channel`），无 `containersAdded`。

## 5. 原子冻结清单

每条是一支可独立判 pass/fail 的断言。**Ticket 0 状态**列标注本提交是否已满足；未满足者逐条落 §8 欠账。

### 5.1 共享符号形状（Ticket 0 已满足）

1. `internal/client` 导出类型 `WakeGateReason`。
2. `WakeGateReason` 是字符串类型（`type WakeGateReason string`）。
3. 常量 `WakeGateCurrentAttempt` 值等于 `"current_attempt"`。
4. 常量 `WakeGateStaleAttempt` 值等于 `"stale_attempt"`。
5. 常量 `WakeGateEmptyEnvelopeIdentity` 值等于 `"empty_workflow_identity"`。
6. 常量 `WakeGateSnapshotNoWorkflowIdentity` 值等于 `"snapshot_without_workflow_identity"`。
7. 常量 `WakeGateSnapshotNotFound` 值等于 `"snapshot_not_found"`。
7a. 常量 `WakeGateMissingSourceTask` 值等于 `"missing_source_task"`（Ticket 0 声明，implement 消费）。
8. `internal/client` 导出类型 `WakeGateDecision`，含字段 `Deliver bool`。
9. `WakeGateDecision` 含字段 `Reason WakeGateReason`。
10. `internal/client` 导出类型 `WakeGateEvent`，含字段 `CardID string`。
11. `WakeGateEvent` 含字段 `Node *string`。
12. `WakeGateEvent` 含字段 `Attempt *string`。
13. `WakeGateEvent` 含字段 `TaskType string`。
14. `WakeGateEvent` 含字段 `SourceTask string`。
15. `WakeGateEvent` 含字段 `SourceTarget string`。
16. `WakeGateEvent` 含字段 `Seq int64`。
17. `WakeGateEvent` 含字段 `SourceSeq int64`。
18. `internal/client` 导出 `func CurrentWorkflowAttempt(st *ledger.Store, cardID, node string) (ledger.DispatchSnapshot, bool, error)`。
19. `internal/client` 导出 `func JudgeMirroredWake(st *ledger.Store, ev WakeGateEvent) (WakeGateDecision, error)`。

### 5.2 `CurrentWorkflowAttempt` 行为（implement 满满足）

20. 只接受 `Node != "" && Attempt != "" && TaskID == Attempt` 的 `EvDispatched` 快照。
21. 同 node 多快照时取最大事件 seq 的一条。
22. `TaskID != Attempt` 的派发快照不作为当前身份。
23. 分页读到尾，不因首页 500 上限漏掉尾部合格快照。
24. 无合格快照时 `found=false`。

### 5.3 身份非空路径（B349 保留；Ticket 0 已接线）

25. envelope `node` 非空是进入本路径的必要条件。
26. envelope `attempt` 非空是进入本路径的必要条件。
27. `envelope.attempt == snapshot.Attempt` 才可能放行。
28. `snapshot.TaskID == snapshot.Attempt` 才可能放行。
29. `ev.SourceTask == snapshot.Attempt` 才可能放行。
30. `ev.SourceTarget == snapshot.Target` 才可能放行。
31. 当前快照 `Target` 与 `source_target` 同为空时放行。
32. 当前快照 `Target` 与 `source_target` 一空一非空时拦截。
33. `source_seq` 不参与身份比较（不相等不单独否决）。
34. 身份非空但为旧 attempt 时拦截（防迟到）。
35. 缺 `source_task` 的身份非空镜像拦截且不终止消费循环。

### 5.4 身份缺失/为空降级路径（B370 新语义；implement 满满足）

36. 缺 `node`/`attempt` 键的镜像，其 source 列等于当前派发时交付。
37. 键在但为空串的镜像，其 source 列等于当前派发时交付。
38. 身份为空且快照无工作流身份时交付。
39. 身份为空且无该 task 派发快照时交付（反转 B349 的 `!found`）。
40. 身份为空、能定位带身份快照、且 source 列不等时拦截（branch 1「不等=丢」）。
41. 拦截路径的判定结果 reason 为 `WakeGateStaleAttempt`。
42. 放行路径的判定结果 reason 非空。

### 5.5 消费点接缝（穿调用方，禁止只测 helper）

43. `card wait` 对身份非空且等于当前派发的可动作 `task_mirrored` 输出一行原始 `ledger.Event`。
44. `card wait` 对身份非空但为旧 attempt 的 `task_mirrored` 不输出。
45. `card wait` 用事件所属卡取快照（`--subtree` 不用 wait 根卡）。
46. `card wait` 在身份闸通过后才调用 `WaitDeliveryPolicy`。
47. `wakeconsumer` 对身份非空且等于当前派发的可动作 `task_mirrored` 产生一次唤醒。
48. `wakeconsumer` 对身份非空但为旧 attempt 的 `task_mirrored` 不唤醒。
49. `wakeconsumer` 被拦的 `task_mirrored` 记 `seen` 且消费循环继续。
50. 卡原生事件（`needs_*`/decision/真人 room message）不走身份闸。

## 6. 三重闸门拍板记录

只记录同时满足「难逆转、无上下文会惊讶、真取舍」的决定：

1. **共享判定落 `internal/client`，与 `WaitDeliveryPolicy` 同包，代价是新增方向 `d_transport→d_ledger`。** 这会跨 `cmd`（d_cli）、`internal/agentd`（d_gateway）与传输包三个子系统，并新增一条冻结的跨域依赖边；后人看到传输包读账本会觉得是分层倒置，容易顺手搬回消费点或另建包；被否方案是新建独立包（如 `internal/wakegate`，归 d_gateway，需新容器 + d_cli→d_gateway entry）与把判定下沉到 ledger。用户裁决选 A（落 `internal/client`）。明确不做独立包与 ledger 派生查询。
2. **降级方向定为「能判定才拦」：身份缺失/为空时按 source 列匹配，只在能证明迟到时才拦。** 这是对 B349 冻结正文的反转，直接改变 `card wait` stdout 与自动化唤醒面；后人只看到「缺身份」会本能保守拒绝；被否方案是「一律放行（不查快照）」与「保持 fail-closed 只加告警」。明确不做一律放行、不做只告警不修。
3. **source 三列是降级路径的权威匹配面，不把 source 复制进 envelope。** 降级判定改用 `source_task`/`source_target` 与当前快照比对；后人看到 envelope 已有 node/attempt，容易把降级也建在 payload 上；被否方案是往 envelope 加身份字段（B233.6 已冻「列是权威身份」）。明确不做 envelope 身份双写。
4. **共享判定与快照取数同符号族一处承载，两份旧快照扫描退役。** 这会同时改动两个子系统的消费点内部取数；后人保留两份近重复扫描会重新漂移；被否方案是两处各留一份扫描、共享符号只做纯比较。明确不做双份扫描。退役动作按用户裁决留 implement 轮。

## 7. Ticket 0、可执行冻结与图

### 7.1 本提交 Ticket 0

- **已落** `internal/client/wakegate.go`：`WakeGateReason` 及六个常量、`WakeGateDecision`、`WakeGateEvent`、`CurrentWorkflowAttempt`、`JudgeMirroredWake`（编译通过）。
- **已落** 两处消费点接线：`cmd/card_wait.go#cardWaitEventActionable`、`internal/agentd/wakeconsumer.go#Server.acceptsCurrentWorkflowAttempt` 调用共享符号；非空身份路径按 B349 正文实现，使 `d_transport→d_ledger` 的调用边成为视图内活跃边。
- **骨架期保守结果**：`JudgeMirroredWake` 对「身份缺失或为空」返回 `Deliver=false, Reason=WakeGateEmptyEnvelopeIdentity`（与旧行为等价），故两处消费点既有测试保持绿；§5.4 的三分支放行语义由 implement 激活（台账 §3 条目 15–19）。
- **两份旧拷贝退役不在本提交**：`cardWaitCurrentWorkflowAttempt` 与 `Server.currentWorkflowAttempt` 暂时保留（未被调用，Go 允许），随 implement 退役。

### 7.2 可执行冻结

- 命中的是**编译期签名**与**消费点接缝行为**，非哈希/密钥派生/编码格式金样本。签名绑定由编译与 `codegraph resolve --doc` 锚校验锁定；行为由 §5.5 穿缝测试承接（部分待 implement）。
- 无哈希、密钥派生金样本：写明「无命中」，不用「未审」代替。

### 7.3 图产出

- `codegraph/target.json`：新增方向 `d_transport→d_ledger`（entries `ledger.Store` / `ledger 实体`，预算 0）；`d_cli→d_transport`、`d_gateway→d_transport` 各补 `client（包级函数）` entry（预算不变）。**随本提交冻结。**
- `codegraph/diffs/cards-B370-charter-4.json`：5 新符号（`m_client_WakeGateReason` / `m_client_WakeGateDecision` / `m_client_WakeGateEvent` / `n_client_CurrentWorkflowAttempt` / `n_client_JudgeMirroredWake`）、2 改动符号、8 新边。**随本提交冻结。**
- `best.json` 不变（复用既有容器）。

## 8. 已知红

- `cmd/graph_gate_test.go#TestRepoContractGate` 跑**无视图**基线：`codegraph/baseline.json` 未含 B370 符号与 `d_transport→d_ledger` 活跃边，故本提交后该测试报 `dead-contract d_transport→d_ledger`——这是**合并前过渡态**，非新问题（同 B229/B353 先例，台账 §3 条目 14、§5 条目 21）。
- 不在本分支重扫 `baseline.json` 的理由（实测）：工作树重扫产物会让既有视图 `cards-B358-charter.json` 的 `nodesDeleted` 有 11 项在新基线中不存在（`ValidateDiff` 失败），即重扫会连锁破坏其他未 absorb 视图。`absorb`（分支合并回主线后执行）统一重扫后本方向转绿，不在本分支生命周期内（台账 §5 条目 21）。
- `codegraph --view cards-B370-charter-4 check` 本轮 fails 空、退出码 0（台账 §3 条目 13）；这是本分支的契约对照判据。

## 9. 本节点欠账

1. `JudgeMirroredWake` 的降级三分支（§5.4 条目 36–42）在本节点暂返回保守 `Deliver=false`（等价旧行为）；三分支放行语义待 implement，且必须有一支能变红的穿缝测试锁住（禁止「已实现但零测试」）。
2. 两份旧快照扫描（`cmd/card_wait.go#cardWaitCurrentWorkflowAttempt`、`internal/agentd/wakeconsumer.go#Server.currentWorkflowAttempt`）尚未退役，待 implement 删除；删除后由 `CurrentWorkflowAttempt` 唯一取数。
3. §5.2 的 `CurrentWorkflowAttempt` 独立行为测试（分页到尾、TaskID!=Attempt 拒绝等）尚未在本节点落测试；现状由 `TestB349AutomationSourceIdentity` 的子用例间接覆盖，implement 需补直测。
4. Ticket 0 以外无「已实现但零测试」的行为：本提交对「身份缺失或为空」未改变旧行为，对非空身份路径沿用既有测试。

## 10. 移交 plan 附区

以下是查证期确立的实现级事项，不占冻结清单条目；plan 吸收后须在区头标注「已由 plan〈文档〉吸收（日期）」并销区：

- 降级分支的定位细节（`node` 空时以 `source_task` 在卡上匹配合格快照的具体扫描形态与日志字段）；分支 2/3 的 reason 落点与日志等级。
- 两份旧扫描退役的删除顺序与调用点参数透传；`cmd` 侧现有 B353/B349 夹具补 `RecordDispatch` 的具体顺序。
- reason 枚举在两个消费点的日志文案与字段名；`--subtree` 事件卡取数的参数透传。
- `JudgeMirroredWake` 对 `s.ledger == nil` 的处理（Ticket 0 返回错误；implement 复核是否需与 `autoLedger` 装配纪律对齐）。

## 11. 本轮法定核对

- 契约增量文档：本文件；每个冻结签名均有现状代码出处。
- 目标图：`codegraph/target.json` 与本契约同提交冻结；`best.json` 不变；分支视图 diff 同提交落盘（5 符号 / 8 边）。
- `codegraph validate` 本轮退出码 0；`codegraph --view cards-B370-charter-4 check` 本轮 fails 空、退出码 0；无视图 `check` 因 baseline 未重扫报 `dead-contract d_transport→d_ledger`，如实记 §8。
- Ticket 0 编译：本轮 `go build ./...` 退出码 0；`go vet ./internal/client/ ./internal/agentd/ ./cmd/` 退出码 0（台账 §4）。
- 可执行冻结：命中编译期签名与接缝行为；无哈希/密钥派生命中。
- 三重闸门：已记录 4 项；无其它命中。

## 12. breakdown 核对修订记录

以下澄清由 breakdown 节点（`docs/superpowers/specs/b370-breakdown.md`，2026-09-14）在契约增量核对中做出；结论均为「不退回 contract」，只留痕不改冻结语义。

- **2026-09-14（breakdown 核对）：「有派发但无工作流身份」与「无派发」的区分属包内实现面，不属导出契约面。** 冻结的 `CurrentWorkflowAttempt(st, cardID, node)` 契约只承诺返回 `(snapshot, found)` 二态；实现分支 2/3 reason 区分所需的第三态（有 `EvDispatched` 但无合格身份）以 `internal/client` 包内**未导出**取数承载，导出面一个符号不增不删，不产生新跨域边。若裁决要改导出面，须退回 contract 重冻。
- **2026-09-14（breakdown 核对）：`node` 缺失/为空时「定位」用该卡最新合格快照而非以 `source_task` 反查该任务自身快照。** 这是为同时满足 §5.4 条目 36/37（空身份应交付）与 B349 回归 `TestB2336StaleAttemptDoesNotWake`（其空身份 `task-empty` 事件须不唤醒）而必须取的分支——见 breakdown §0 F1，仍待协调者拍板确认。
- **2026-09-14（breakdown 核对）：§3.2 A 末句「缺 `source_task` 落 `WakeGateStaleAttempt`」与 §3.1 常量 `WakeGateMissingSourceTask` 注释「身份非空且缺 source_task 的独立 reason」不自洽。** 两值都在冻结枚举内，选哪个不退回 contract；breakdown §0 F3 请协调者拍板，以便实现与契约字面对齐。
