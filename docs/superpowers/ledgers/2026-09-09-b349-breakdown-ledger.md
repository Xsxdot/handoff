# B349+B352 breakdown 台账

日期：2026-09-09
卡：B349（B352 并入）
节点：charter breakdown
工作分支：cards/B349-charter-2
开工 HEAD：543f47c4 contract(B349): freeze source identity contract

## 过程与判断

1. 执行形态：handoff executor 出稿。本轮只写法定 breakdown 与本台账；不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新的 executor。
2. 上游状态：spec 文件头为已批准；contract 文件头写明上游 spec 已批准且本提交冻结。本稿不改上游冻结文件。
3. 用户补充约束已落拆解：L3 轻档不扇出；B352 cursor 与 B349 身份闸同一份拆解；P3 保持闸在消费者、ledger 不揽当前 attempt 派生查询。
4. Ticket 0 已在当前 HEAD：Go wire DTO、Facade/HTTP 投影和对应穿缝测试已存在；本轮身份闸、card wait 快照扫描、automation cursor 仍未实现。本稿明确把 Ticket 0 当已完成前置，不把实现欠账写成已生效。
5. 本稿不新增接缝。契约 #1–#50 已逐条归属 T0–T4；任何新字段、事件、HTTP、ledger 派生 API、共享 cursor 或第二策略源必须退回 contract。
6. 2026-09-09 协调者拍板：F1–F6 全部维持冻结，无新岔口。头部回写已拍板。
6. 当前生产 card wait B353 可动作镜像夹具没有匹配的 RecordDispatch；本稿把“夹具先有当前派发快照”列为 T2 的假绿防线，不把旧测试夹具结果外推为 B349 行为。
7. 缺陷族已逐单元回答：生命周期/状态机中断、静默失败/误导报错、跨平台假设、假红/假绿测试、门禁绕过，以及序列化边界、枚举白名单、承重安全属性。外部执行器、Keystone、PG/relay、真实重启与跨平台行为统一写入真机清单并标注“未验证，需真机”。

## 工作树与基线命令

命令：git status --short --branch

原始输出：

    ## cards/B349-charter-2

命令：git log -6 --oneline --decorate

原始输出：

    543f47c4 (HEAD -> cards/B349-charter-2, cards/B349-charter) contract(B349): freeze source identity contract
    0d9673ed (origin/cards/B233.1-charter-7) docs(B349): 批准消费正确性 spec
    f2129a25 docs(B353): CHANGELOG Unreleased
    b6c087b6 merge(B353): 唤醒面分类表并入功能线
    c5c75087 (origin/cards/B353-charter-5, cards/B353-review-2, cards/B353-charter-5) fix(B353): close wake consumer review gaps
    5440ebdc (origin/cards/B353-charter-4, cards/B353-charter-4) fix(B353): filter card wake events

命令：wc -l docs/superpowers/specs/b349.md docs/superpowers/specs/b349-contract.md docs/superpowers/reviews/b349-spec-review.md

原始输出：

    148 docs/superpowers/specs/b349.md
    255 docs/superpowers/specs/b349-contract.md
    204 docs/superpowers/reviews/b349-spec-review.md
    607 total

## 代码图命令与原始输出

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --help

原始输出摘要：退出码 0；命令列出 domains、context、sym、resolve、validate、check
等子命令。

命令：jq -r '.domains | to_entries[] | select((.value.parent // "") == "") | [.key,.value.type,.value.label] | @tsv' codegraph/best.json

原始输出：

    d_orchestration	logic	任务编排
    d_gateway	boundary	控制门面
    d_workspace	boundary	项目与工作区
    d_execution	boundary	任务执行
    d_sessions	boundary	终端会话
    d_transport	boundary	跨机连接
    d_protocol	logic	协议契约
    d_ledger	logic	卡片账本
    d_collab	logic	协作房间
    d_cli	logic	协调者命令面
    d_web	logic	Web 控制台
    d_policy	logic	运行策略与配置
    d_maintenance	boundary	安装与换版
    d_scheduling	logic	编制调度
    d_keystone	logic	协调者 Keystone

命令：jq -r '.containers | to_entries[] | select(.key=="k_proto_model" or .key=="k_ledger_api_Facade" or .key=="k_ledger_Store" or .key=="k_agentd_fn" or .key=="k_agentd_Server" or .key=="k_cmd_fn") | [.key,.value] | @tsv' codegraph/best.json

原始输出：

    k_agentd_Server	d_gateway
    k_agentd_fn	d_orchestration
    k_cmd_fn	d_cli
    k_ledger_Store	d_ledger
    k_ledger_api_Facade	d_ledger
    k_proto_model	d_protocol

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym m_proto_LedgerEvent

原始输出摘要：命中 m_proto_LedgerEvent，域 d_protocol，文件 internal/proto/ledger.go，
line 119；基线字段为 Seq/CardID/Type/Actor/Payload/CreatedAt。

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_agentd_ledgerEventWire

原始输出摘要：命中 n_agentd_ledgerEventWire，文件 internal/agentd/ledgerapi.go，
line 112，签名 func ledgerEventWire(event ledger.Event) proto.LedgerEvent。

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_ledger_Store_EventsFromAsc

原始输出摘要：命中 n_ledger_Store_EventsFromAsc，域 d_ledger，文件 internal/ledger/events.go，
line 63，签名 func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error)。

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_ledger_Store_Follow

原始输出摘要：命中 n_ledger_Store_Follow，域 d_ledger，文件 internal/ledger/follow.go，
line 18，签名 func (s *Store) Follow(ctx context.Context, members func() ([]string, error), fromSeq int64, pollInterval time.Duration, onEvent func(Event) error) error。

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym n_cmd_cardWaitEventActionable

原始输出（失败，按纪律保留原文）：

    Error: 符号 "n_cmd_cardWaitEventActionable" 不在图中（图未覆盖或名字有误）；近似候选: []
    Usage:
      codegraph sym <符号名或节点 id> [flags]
    ...
    exit status 1

判断：cardWaitEventActionable 以源码符号锚查证，列为图覆盖债；不把图缺席当作代码缺席。

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_protocol --max-tokens 3000、context d_ledger、context d_orchestration、context d_cli、context d_gateway

原始输出摘要：五条命令均完成；各自日志含 graph context completed domain=...，
对应 domain 分别为 d_protocol、d_ledger、d_orchestration、d_cli、d_gateway。输出
因 max-tokens=3000 显示 assemble result truncated，因此只采用 domain/type/container
映射和已点名源码入口，不把截断 chain 当完整依赖结论。

第一次尝试将 context 输出写到 /tmp：

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_protocol --max-tokens 3000 >/tmp/b349-context-protocol.json

原始输出（失败，按纪律保留原文）：

    /bin/bash: line 1: /tmp/b349-context-protocol.json: Read-only file system

随后改写到任务临时目录成功；不把失败归因成 codegraph 失败。

## 源码查证命令

命令：rg -n '^(type LedgerEvent|func \(f \*Facade\) EventsFromAsc|func eventWire|func ledgerEventWire|func \(s \*Server\) currentWorkflowAttempt|func \(s \*Server\) acceptsCurrentWorkflowAttempt|func automationWakeEvent|func \(s \*Server\) consumeAutomationEventsOnce|func cardWaitEventActionable|func runCardWait|func \(s \*Server\) SetupAutomation|func \(s \*Server\) StartAutomation|type DispatchSnapshot|func \(s \*Store\) EventsFromAsc|func \(s \*Store\) RecordDispatch|func \(s \*Store\) Follow|func \(.*WaitDeliveryPolicy)' internal cmd

原始输出：

    cmd/card_wait.go:56:func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration) error {
    cmd/card_wait.go:195:func cardWaitEventActionable(ev ledger.Event) (bool, error) {
    internal/agentd/ledgerapi.go:111:func ledgerEventWire(event ledger.Event) proto.LedgerEvent {
    internal/agentd/server.go:2554:func (s *Server) SetupAutomation(st *ledger.Store) {
    internal/agentd/scheddrain.go:51:func (s *Server) StartAutomation(ctx context.Context) {
    internal/ledger/follow.go:18:func (s *Store) Follow(ctx context.Context, members func() ([]string, error),
    internal/ledger/events.go:63:func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error) {
    internal/ledger/events.go:115:type DispatchSnapshot struct {
    internal/ledger/events.go:150:func (s *Store) RecordDispatch(cardID string, snap DispatchSnapshot) error {
    internal/agentd/wakeconsumer.go:54:func (s *Server) currentWorkflowAttempt(cardID, node string) (attempt string, found bool, err error) {
    internal/agentd/wakeconsumer.go:97:func (s *Server) acceptsCurrentWorkflowAttempt(ev proto.LedgerEvent) (bool, error) {
    internal/agentd/wakeconsumer.go:133:func automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error) {
    internal/agentd/wakeconsumer.go:211:func (s *Server) consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error) {
    internal/ledger/api/api.go:81:func (f *Facade) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error) {
    internal/ledger/api/api.go:147:func eventWire(ev ledger.Event) proto.LedgerEvent {
    internal/ledger/mirror.go:37:func (s *Store) AppendMirroredEvent(cardID string, ev MirroredEvent) (bool, error) {
    internal/proto/ledger.go:120:type LedgerEvent struct {

结论：当前身份闸返回 attempt string，只核 envelope；card wait 只按
WaitDeliveryPolicy；cursor 仍由 Server 内存字段承载；Store 已读出 source 三列。

## 当前 Ticket 0 实测命令

命令：go test ./internal/ledger/api -run '^TestFacadeEventsFromAscPreservesSourceIdentity$' -count=1

原始输出：

    ok  	github.com/Xsxdot/handoff/internal/ledger/api	0.093s

命令：go test ./internal/agentd -run '^TestCardDetailProjectsMirroredSourceIdentity$' -count=1

原始输出：

    ok  	github.com/Xsxdot/handoff/internal/agentd	0.212s

命令：go test ./internal/proto -count=1

原始输出：

    ok  	github.com/Xsxdot/handoff/internal/proto	0.004s

结论：以上三条是本回合亲跑到的 Ticket 0 投影/协议结果；不据此宣称身份闸或
cursor 已实现。

## 收口规则

本台账与法定 breakdown 同批提交。提交命令与原始输出在提交发生后追加；按纪律
只 amend 一次收进同批提交，不把 amend 后 hash 回写台账再追逐。

## 文档锚点与提交前门禁

命令：go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b349-breakdown.md

原始输出：

    {
     "anchors": [
      {"ref":"internal/proto/ledger.go#LedgerEvent","file":"internal/proto/ledger.go","line":119,"anchor":"ok","nodeId":"m_proto_LedgerEvent"},
      {"ref":"internal/ledger/api/api.go#Facade.EventsFromAsc","file":"internal/ledger/api/api.go","line":81,"anchor":"ok","nodeId":"n_ledger_api_Facade_EventsFromAsc"},
      {"ref":"internal/ledger/api/api.go#eventWire","file":"internal/ledger/api/api.go","line":147,"anchor":"moved"},
      {"ref":"internal/ledger/events.go#Store.EventsFromAsc","file":"internal/ledger/events.go","line":63,"anchor":"ok","nodeId":"n_ledger_Store_EventsFromAsc"},
      {"ref":"internal/ledger/events.go#DispatchSnapshot","file":"internal/ledger/events.go","line":115,"anchor":"ok","nodeId":"m_ledger_DispatchSnapshot"},
      {"ref":"internal/ledger/follow.go#Store.Follow","file":"internal/ledger/follow.go","line":18,"anchor":"ok","nodeId":"n_ledger_Store_Follow"},
      {"ref":"internal/agentd/wakeconsumer.go#automationWakeEvent","file":"internal/agentd/wakeconsumer.go","line":133,"anchor":"moved","nodeId":"n_agentd_automationWakeEvent"},
      {"ref":"internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce","file":"internal/agentd/wakeconsumer.go","line":211,"anchor":"moved","nodeId":"n_agentd_Server_consumeAutomationEventsOnce"},
      {"ref":"internal/agentd/server.go#Server.SetupAutomation","file":"internal/agentd/server.go","line":2554,"anchor":"moved","nodeId":"n_agentd_Server_SetupAutomation"},
      {"ref":"internal/agentd/scheddrain.go#Server.StartAutomation","file":"internal/agentd/scheddrain.go","line":51,"anchor":"ok","nodeId":"n_agentd_Server_StartAutomation"},
      {"ref":"cmd/card_wait.go#runCardWait","file":"cmd/card_wait.go","line":56,"anchor":"moved","nodeId":"n_cmdrunCardWait"},
      {"ref":"cmd/card_wait.go#cardWaitEventActionable","file":"cmd/card_wait.go","line":195,"anchor":"moved"},
      {"ref":"internal/client/delivery.go#WaitDeliveryPolicy","file":"internal/client/delivery.go","line":23,"anchor":"moved"}
     ]
    }

结论：法定拆解稿中的现状代码锚点均已解析为 ok 或 moved；未出现 vanished。

审阅记录：先以重叠的 `sed -n '1,155p'` 与 `sed -n '150,450p'` 拼接查看文档，
误把重叠输出看成 DAG 重复；随后尝试删除重复段的 apply_patch，原始失败信息为：

    apply_patch verification failed: Failed to find expected lines in /root/.handoff/worktrees/c18264b3/docs/superpowers/specs/b349-breakdown.md

再以 `nl -ba ... | sed -n '145,170p'` 复核，确认文件实际只有一段 DAG，未发生文件改动。

提交记录（历史读数；随后只 amend 一次）：

命令：git commit -m "docs(B349): 出稿消费身份闸拆解提案"

原始输出：

    [cards/B349-charter-2 6703c264] docs(B349): 出稿消费身份闸拆解提案
     2 files changed, 723 insertions(+)
     create mode 100644 docs/superpowers/ledgers/2026-09-09-b349-breakdown-ledger.md
     create mode 100644 docs/superpowers/specs/b349-breakdown.md

随后按纪律只 amend 一次，将本条提交记录收回同批台账；不把 amend 后 hash 回写本文件。
