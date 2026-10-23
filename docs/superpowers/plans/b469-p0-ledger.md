# B469 P0 取证台账（2026-10-23）

## 偏离与自主决定

无。源码执行者只读取spec/plan r1.3及implement skill；本次只新增本台账和S3契约草稿，供协调者审查。未实现、启动服务、发房间或远程派发。准确wire仍待裁决，未自行冻结。

## 本轮证据与边界

协调者隔离实验产物 `/tmp/b469-p0-runtime/report.json` 已读取：专用PG，隔离HOME、端口56468，fake_executor=true，pg_startup_succeeded=true，process_exit=0且process_reaped=true；external_cli_verified=false。未读取/打印test-db.json中的DSN或密码。

报告证明Bearer身份与cookie身份均为user:b469-test-user；主令牌可无人工创建cookie（master_token_can_create_cookie_without_human=true）。源码支持该链：server.go:1124-1139、identity.go:38-58、authroutes.go:129-211。因此仅要求cookie+nonce仍不能证明独立人类确认；S1持久规则写入等待可信确认来源裁决。此报告不证明外部CLI权限链或短回合准入。

源码核验：

- cmd/dispatch.go:334-343、client/client.go:757-807、orchestration/contracts.go:26、proto/proto.go:226无origin session/审批责任字段；executor_session不能替代发起会话。
- orchestration/manager.go:2214-2238、3050-3058产生原permission/question工单及事件；ledgermirror/mirror.go:340-356从AllTaskLinks订阅，未挂卡没有这条可靠镜像入口。
- manager.go:2944-2959、3068-3073的人工waiter与server.go:1573-1585的协调者30min回合不同；scheddrain.go:470-479、residentpush.go:259-263在回合返回才释放准入。
- wakeconsumer.go:502-537保留origin pull收口、push自身/祖先、非法上走和循环保护；空字符串不能区分pull与无接收人。
- cmd/session.go:565-567只接EvRoomMessage；不能宣称原工单天然进入session wait。

## 提案与未验

精确提案见[b469-s3-contract.md](b469-s3-contract.md)，首个版本仅供协调者审查。origin_session单字段、通知责任版本、独立工单来源、无卡needs_human与寻址通知是契约后可实施项；不改变原回答CAS。

P0尚未实测：external CLI实际版本/权限与问题产生、LaunchAdmit及驻场执行准入短回合释放、人工等待退出、取消/30min、重启回答恢复、重复与换责、双机同测试PG和目标机投递。S3这些验收门仍未通过，不能用fake executor/源码/单机PG起停替代。可信确认来源不足阻塞S1规则写入，纯S2不受阻。

图覆盖债：checkout无codegraph目录，按源码取证。未运行测试；本轮文档不代表实现或验收完成。

## 协调者后续隔离实跑补证

已读取 `/tmp/b469-p0-runtime/cli-report.json`：真实opencode 1.18.32、独立HOME，loopback确定性协议provider fixture（无真实凭据/模型）。launch 2.537s、resume 1.932s均exit0、marker=true，同一session；这仅证明该fixture下真实CLI启动/同session续接可短时结束。real_model_verified=false，未产生并验证真实permission/question人工等待，不证明LaunchAdmit释放或重启等待恢复。

已读取 `/tmp/b469-p0-runtime/baseline-tests.log`：协调者报告targeted agentd通过；ledger整批不是全绿。`TestB435ResidentCandidatesPG`（日志412-414）及`TestB435SeatCandidatesPG`（430-432）失败原因为newB409PGStore精确库名保护拒绝 current_database()="handoff_b469_p0_74db30e9"，属于测试夹具环境前置守卫失败，未进入其候选行为断言；没有绕过守卫或改库名冒充行为通过。其他已成功B435 PG腿的结论限于对应测试，不替代这两条及S3真实链验收。

本补证更新前节“external CLI未实测”的范围：基本launch/resume已实跑fixture；真实工单等待/短回合准入、双机等硬门仍未验。本节点未亲自启动实验，仅据协调者交付的原始文件登记。

## 协调者契约收窄裁决

上一版新增所有者CRUD、task_notification_owners及必填responsible_identity被否决，已从契约草稿删除。origin_session + 当前登记resident就是显式fallback事实；无需人工另绑责任。通知归属版本由现有卡/驻场公开事实解析而来，只改变通知去向。新稿优先复用任务列表+每task公开事件补未挂卡订阅，只提一张原工单通知记录表；全局ticket-events仅作有证据才采纳的备选。所有wire仍待协调者核定，P0准入释放probe未补齐前不启动S3。

## 协调者复查与实验修正

首个agent报告的pg_startup_succeeded仅以identity端点响应为依据，不能证明自动化账本已装配。后续真实房间探针从503定位到隔离配置缺advertise，导致SetupAutomation未装配；现已在临时隔离配置补本机回环advertise，并以squads端点可用/载体detect和真实会话创建作为就绪证据。早前“PG启动”结论限于独立PG账号/连接与已跑PG测试，不能代替自动化装配。没有改在役配置。
真实房间探针的provider有无tools的后台摘要请求须与实际工具回合区分；最初脚本误把摘要请求当工具回合，已从provider请求元数据定位并修正。仅本地协议fixture发生该误配，无生产模型/账号调用。

## P0真实驻场任务链补证与S3开工裁决

当前原始报告：/tmp/b469-p0-runtime/live/report.json。真实opencode1.18.32驻场CLI经bash工具调用隔离API启动真实opencode任务，模型端仅loopback协议fixture，无生产账号或真实LLM。session创建4.666s；task=b525424b-a86f-4341-b872-9aa83bb2a219原ask待答，协调者两项准入均0、任务计数1。隔离agent重启后待答仍1；回答ok=true/relayed=true，任务running/待答0；stop清理、agent exit0。证明原工单待答与发起回合释放可以并存，失败原因是发起关联/发布路径缺失，非必须占30min等待。

协调者核定S3契约v1，单机基本P0允许S3实施。permission分支、真实模型、多机/断连/换责任仍是验收缺口，不把fixture等价成最终验收。S1人类确认不足仍阻塞，按已批plan不进入S1写入。
