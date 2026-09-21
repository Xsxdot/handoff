# B353 breakdown 台账

日期：2026-09-09
卡：B353（并入 B354 / B355）
节点：charter breakdown
工作分支：`cards/B353-charter-2`
开工 HEAD：`fc943607`（`docs(B353): freeze wake consumer contract`）

## 过程

1. 执行形态：handoff executor 出稿，协调者拍板。本轮只写法定 breakdown 与本台账；不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新的 executor。
2. 上游状态核对：`docs/superpowers/specs/b353.md` 头部为“状态：已批准（2026-09-09，用户原话「推进吧」）”；`docs/superpowers/specs/b353-contract.md` 头部为“状态：已冻结（本提交冻结）”。本轮不以会话记忆替代文件状态。
3. 基线核对：当前分支为 `cards/B353-charter-2`；spec/contract 声明有效基线为 `cards/B233.1-charter-7 @ 48f2736a`，不越过该基线。
4. 项目缺陷族清单 `docs/superpowers/specs/2026-08-21-handoff-instantiation-checklist.md` 已实读：含通用五族、序列化边界、新枚举白名单、webview 候选族与单包 ≥40 文件的竖切判据；文件顶部未见“基线版本：charter@<commit>”标记。该治理债在 breakdown 中显式记录，不改写为已对齐；承重安全属性按注入纪律追加回答。

## 命令与原始读数

### 工作树

命令：`git status --short --branch`
原始输出：`## cards/B353-charter-2`

命令：`git log -1 --oneline --decorate`
原始输出：`fc943607 (HEAD -> cards/B353-charter-2, origin/cards/B353-charter, cards/B353-charter) docs(B353): freeze wake consumer contract`

### 代码图

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --help`
原始结论：命令退出 0，提供 `domains`、`context`、`sym`、`resolve` 等查询子命令；本轮后续均按该命令族查图。

命令：`jq -r '.domains | to_entries[] | select((.value.parent // "") == "") | "\\(.key)\\t\\(.value.label)\\t\\(.value.type)"' codegraph/best.json`
原始输出：

```text
d_orchestration	任务编排	logic
d_gateway	控制门面	boundary
d_workspace	项目与工作区	boundary
d_execution	任务执行	boundary
d_sessions	终端会话	boundary
d_transport	跨机连接	boundary
d_protocol	协议契约	logic
d_ledger	卡片账本	logic
d_collab	协作房间	logic
d_cli	协调者命令面	logic
d_web	Web 控制台	logic
d_policy	运行策略与配置	logic
d_maintenance	安装与换版	boundary
d_scheduling	编制调度	logic
d_keystone	协调者 Keystone	logic
```

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_cmdrunCardWait`
原始输出摘要：`anchor: ok`；`cmd/card_wait.go:48`；签名为 `func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error`；域 `d_coordination_cli`。

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_client_Client_FollowEvents`
原始输出摘要：`anchor: moved`；`internal/client/client.go:1751`；签名为 `func (c *Client) FollowEvents(ctx context.Context, taskID string, all bool, idle time.Duration, onEvent func(*proto.Event) error, onBacklog func(*BacklogSummary) error) error`；域 `d_transport_channel`。

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_ledger_Store_Follow`
原始输出摘要：`anchor: ok`；`internal/ledger/follow.go:18`；签名为 `func (s *Store) Follow(ctx context.Context, members func() ([]string, error), fromSeq int64, pollInterval time.Duration, onEvent func(Event) error) error`；域 `d_ledger`。

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_agentd_automationWakeEvent`
原始输出摘要：`anchor: moved`；`internal/agentd/wakeconsumer.go:130`；签名为 `func automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error)`；域 `d_coordination_task`。

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_agentd_Server_consumeAutomationEventsOnce`
原始输出摘要：`anchor: moved`；`internal/agentd/wakeconsumer.go:185`；签名为 `func (s *Server) consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error)`；域 `d_coordination_api`。

图覆盖债：`WaitDeliveryPolicy` 未命中独立代码图节点；contract 与源码均指向 `internal/client/delivery.go#WaitDeliveryPolicy`，本稿不把 `isDeliverable` 当第二策略源。

### 源码现状

命令：`nl -ba cmd/card_wait.go | sed -n '31,126p'`
原始读数：`cardWaitCmd.RunE` 只把 `cardWaitSubtree`、`cardWaitTimeout` 传给 `runCardWait`；`runCardWait` 当前对每个 `ledger.Event` 先 `enc.Encode(e)`，再仅对 `EvStatusMoved` 调 `checkDone`；当前没有 `--follow`。

命令：`nl -ba internal/client/delivery.go` 与 `rg -n 'WaitDeliveryPolicy|isDeliverable' internal/client/client.go`
原始读数：`WaitDeliveryPolicy` 将 progress、approver_decision、approver_disabled、tickets_voided、ticket_answered、permission_auto_allow、permission_reuse 判为 false，其余返回 true；`waitOnce` 与 `FollowEvents` 均经 `isDeliverable` 别名消费。

命令：`nl -ba internal/agentd/wakeconsumer.go | sed -n '129,182p'`
原始读数：`task_mirrored` 当前只把 completed/failed/turn_failed 映射为 `WakeTaskTerminal`、permission_request/question 映射为 `WakeTicket`，其它 task type default 不唤醒；room_message 仅 `RoomMsgUser && !BySystem` 唤醒；needs_human/needs_cleared 唤醒；status_moved 与其它卡事件不唤醒。

命令：`nl -ba skills/handoff/SKILL.md | sed -n '446,487p'` 与 `nl -ba README.md | sed -n '408,432p'`
原始读数：skill 当前把 `card wait` 描述为单流长挂到终态，并写成“一次工作流只挂一次”；README 任务事件表已列 delivery_failed、stalled 等，但没有本卡卡侧过滤/`--follow` 退出模型。

### 既有测试入口

命令：`rg --files cmd internal/ledger | rg 'card_wait|follow.*test|wakeconsumer|delivery'`
原始输出：存在 `cmd/card_wait_test.go`、`internal/client/follow_test.go`、`internal/client/execution_test.go`、`internal/ledger/follow_test.go`、`internal/agentd/wakeconsumer_test.go` 等入口。

命令：`rg -n 'TestFollowFiltersAuditEvents|TestFollowAllDeliversAuditEvents|TestAutomationEventMappingThroughConsumer|TestCardWaitSubtreeExitsWhenAllDone' ...`
原始读数：任务 follow 已有正/反过滤与 all=true 回归；wakeconsumer 已有真实账本到消费者的同卡合并/审计过滤回归；card wait 现有测试只覆盖子树终态退出与分层 flag 反例，未覆盖本卡新增行为。

命令：`go test ./cmd -run '^TestCardWaitSubtreeExitsWhenAllDone$' -count=1`
原始输出：`ok  	github.com/Xsxdot/handoff/cmd	2.430s`。

命令：`go test ./internal/client ./internal/ledger ./internal/agentd -count=1`
原始输出：`ok  	github.com/Xsxdot/handoff/internal/client	9.881s`；`ok  	github.com/Xsxdot/handoff/internal/ledger	19.080s`；`ok  	github.com/Xsxdot/handoff/internal/agentd	162.655s`。

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b353-breakdown.md`
原始输出：JSON 返回 14 个 anchors，全部为 `anchor: ok` 或 `anchor: moved`，包括 `WaitDeliveryPolicy`、`runCardWait`、`Client.WaitEvent`、`Client.FollowEvents`、`Store.Follow`、`automationWakeEvent`、`Server.consumeAutomationEventsOnce`、`Service.Decide`、`EventType`、`RoomMessage`；命令退出 0。

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b353-contract.md`
原始输出：JSON 返回 10 个 anchors，全部为 `anchor: ok` 或 `anchor: moved`；命令退出 0。

命令：`git diff --check`
原始输出：无输出，命令退出 0。

命令：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b353-breakdown.md`
原始输出：
```json
{
 "anchors": [
  {"ref":"internal/client/delivery.go#WaitDeliveryPolicy","file":"internal/client/delivery.go","line":23,"anchor":"moved"},
  {"ref":"cmd/card_wait.go#runCardWait","file":"cmd/card_wait.go","line":48,"anchor":"ok","nodeId":"n_cmdrunCardWait"},
  {"ref":"internal/client/client.go#Client.WaitEvent","file":"internal/client/client.go","line":1468,"anchor":"moved","nodeId":"n_client_Client_WaitEvent"},
  {"ref":"internal/client/client.go#Client.FollowEvents","file":"internal/client/client.go","line":1751,"anchor":"moved","nodeId":"n_client_Client_FollowEvents"},
  {"ref":"internal/ledger/follow.go#Store.Follow","file":"internal/ledger/follow.go","line":18,"anchor":"ok","nodeId":"n_ledger_Store_Follow"},
  {"ref":"internal/agentd/wakeconsumer.go#automationWakeEvent","file":"internal/agentd/wakeconsumer.go","line":130,"anchor":"moved","nodeId":"n_agentd_automationWakeEvent"},
  {"ref":"internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce","file":"internal/agentd/wakeconsumer.go","line":185,"anchor":"moved","nodeId":"n_agentd_Server_consumeAutomationEventsOnce"},
  {"ref":"internal/keystone/keystone.go#Service.Decide","file":"internal/keystone/keystone.go","line":86,"anchor":"moved","nodeId":"n_keystone_Service_Decide"},
  {"ref":"cmd/card_wait.go#cardWaitCmd","file":"cmd/card_wait.go","line":31,"anchor":"moved"},
  {"ref":"cmd/wait.go#waitCmd","file":"cmd/wait.go","line":75,"anchor":"moved"},
  {"ref":"cmd/wait.go#runFollow","file":"cmd/wait.go","line":235,"anchor":"ok","nodeId":"n_cmd_runFollow"},
  {"ref":"internal/client/client.go#isDeliverable","file":"internal/client/client.go","line":125,"anchor":"moved","nodeId":"n_client_isDeliverable"},
  {"ref":"internal/proto/proto.go#EventType","file":"internal/proto/proto.go","line":39,"anchor":"ok","nodeId":"m_proto_EventType"},
  {"ref":"internal/proto/rooms.go#RoomMessage","file":"internal/proto/rooms.go","line":24,"anchor":"ok","nodeId":"m_proto_RoomMessage"}
 ]
}
```
命令退出 0。

命令：`wc -l docs/superpowers/specs/b353-breakdown.md docs/superpowers/specs/b353-contract.md docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md`
原始输出：`291 docs/superpowers/specs/b353-breakdown.md`；`158 docs/superpowers/specs/b353-contract.md`；`106 docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md`；`555 total`。

## 判断

- 本卡不新增命令、HTTP 字段、事件类型、事件总线或镜像过滤；contract 冻结的唯一任务策略与卡侧消费点足以圈出有界文件集，不退回 contract。
- 触及的生产域提案为 `d_cli`（logic）、`d_transport`（boundary）、`d_ledger`（logic）、`d_orchestration`（logic）、`d_keystone`（logic 既有 WakeEvent/Decide 载体）与 `d_protocol`（logic 既有 EventType/RoomMessage 形状）；`d_execution` 与 `d_gateway` 只作为真实任务事件/WS 的边界依赖列真机清单，不派实现文件。
- 建议采用单张跨域实现子卡，内部按“任务 wait policy → card wait → wakeconsumer → docs/接缝回归”串行 DAG；这是提案，是否拆成多个外部子卡列入稿首待拍板，不在本轮自批。
- 未验证的真实行为集中在真机清单：真实 executor 产生事件、直连/relay/WS 重连、PG LISTEN 与 SQLite 轮询、agentd/协调者重启后的事件间隙、Keystone 实际唤醒与 resume、不同 OS/权限/进程组。夹具结果不外推。

## 收口

本台账随 `docs/superpowers/specs/b353-breakdown.md` 同批提交。提交命令与原始输出在提交发生后追加；按纪律不追写 amend 后 hash。

## 失败命令

命令：`git add docs/superpowers/specs/b353-breakdown.md docs/superpowers/specs/b353-contract.md docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md && git diff --cached --check && git diff --cached --stat && git status --short`

原始输出：
```text
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:3: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:4: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:5: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:6: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:20: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:23: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:28: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:31: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:52: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:55: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:58: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:61: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:64: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:71: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:74: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:77: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:80: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:85: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:88: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:91: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:94: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:97: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:100: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:103: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:106: trailing whitespace.
docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md:130: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:3: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:4: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:5: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:6: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:7: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:8: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:9: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:10: trailing whitespace.
docs/superpowers/specs/b353-breakdown.md:11: trailing whitespace.
```

## 门禁复跑

命令：`git add docs/superpowers/specs/b353-breakdown.md docs/superpowers/specs/b353-contract.md docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md && git diff --cached --check && git diff --cached --stat`

原始输出：`git diff --cached --check` 无输出并继续返回；统计输出为 `491 insertions(+)`、3 个文件；命令退出 0。

## 首次提交

命令：`git commit -m "docs(B353): 拆解唤醒消费面三链提案"`

原始输出：
```text
[cards/B353-charter-2 243c2f84] docs(B353): 拆解唤醒消费面三链提案
 3 files changed, 497 insertions(+)
 create mode 100644 docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md
 create mode 100644 docs/superpowers/specs/b353-breakdown.md
```

按纪律，下面只 amend 一次收进本台账；不把 amend 后 hash 回写到台账。

## 拍板

- 2026-09-09：协调者提案 P1-A / P2-A / P3-B；用户原话「同意」。
- P1-A：不 `card split`，T0–T4 在 B353 一轮实现。
- P2-A：skill 与 README 同批改。
- P3-B：项目缺陷族清单顶部基线行不在本卡补。
