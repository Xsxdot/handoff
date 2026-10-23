# B469 S3 最薄接缝（协调者核定 v1，2026-10-23）

## 偏离与自主决定

无实现。上一稿额外所有者CRUD、task_notification_owners及必填responsible_identity已被协调者否决，本稿删除。r1.3回退依据就是任务origin_session与该会话当前登记驻场者，不能另设绑定门槛；本稿不改变答权。协调者已依据下述P0补证核定v1，允许进入S3实现；后续验收缺项不得宣称通过。

## 已有事实与唯一新增发起字段

裸派发 `cmd/dispatch.go:334` → `internal/client/client.go:757,816` → `internal/agentd/handlers.go:453,640` → `internal/orchestration/contracts.go:26` 没有发起会话；proto.Task也无。新增唯一可选 `origin_session`，贯穿上述DTO、Task及执行机任务持久化；不重复room，不使用executor_session替代。缺席/空表示unknown，旧任务不回填；非法非空格式400 `invalid_origin_session`。新任务关联与CreateTask同次提交，启动在提交后。

origin_session只是发起事实。驻场责任由会话公开面返回的**当前登记resident**决定，不新增配置或所有者表；查询失败保留重试，不伪装无resident。机器路由/CLI session由该驻场公开承载事实取得，不在approval读其内部。

CLI生产：新增 `--origin-session`，缺省读 `HANDOFF_ORIGIN_SESSION`，flag优先；agentd在已知会话上下文的卡席位/驻场print组装点向现有SessionSpec/SessionRef.Env传递，经既有TurnRequest.Env到CLI子进程。hostapi签名不变。不得给驻场注入卡席位身份；无法知道origin的桌面/普通CLI仍unknown，可明确flag，不猜当前窗口。web现未找到裸POST生产者，不能虚构：卡派发服务端从卡的会话公开关联填入；Task TS详情投影字段；若以后有裸派发表单，从明确会话上下文传入，无上下文unknown。首次实施须点明真实生产调用点并验跨机转发JSON不丢字段。

## 路由与最薄通知wire

卡路径结果在通知组装点区分 `push/pull/none`：自身合法push→自身；origin合法pull→原card wait通道并收口；空座/非法继续祖先push，跳过pull祖先且保留循环保护。现有resolveWakeCard空字符串混合pull/none，不能直接用空值触发fallback。只有none→origin_session当前登记resident→人；无origin/无resident/不可达均有可见待办，不广播、不找最后发言者。

建议一个独立账本事件type `task_ticket_notice`，payload准确键：`source_machine`、`task_id`、`ticket_id`、`source_seq`、`ownership_version`、`target`（一个既有DeliveryAddress）、`ticket_kind`（gate/ask）。target.session是唯一房间引用，不再加room。无target不发寻址事件；原工单详情按machine/task/ticket读取，通知不是第二回答载体。未知kind/缺字段/解码错不上推游标；未知type不唤醒。

ownership_version由通知领域为该原工单分配单调整数，指向一次已解析的卡席位或当前resident地址快照；目标变化才加一。原工单首次为1，同地址重放不增；版本只控制通知替换/幂等，不限制合法答权。不新增责任CRUD。重试/源重放/收到席位或resident变更事件时通过公开面重读路由；新目标版本撤旧待办，旧目标机投递前重读最新版本，不能投递过时地址。需要跨进程CAS防止两个消费者各自改版本。

## 发布与持久化：先复用现有任务面

推荐最小实现A：复用已配置机器的任务列表及每task事件补拉/订阅，补齐**未挂卡且origin_session非空的任务**，并将无origin原工单保留在人可见任务列表。现有Mirror从AllTaskLinks生成挂卡订阅（mirror.go:340-356），新增独立origin任务订阅集合，不伪card_tasks、不伪挂卡；每task事件after_seq从执行机持久事件补发，不只依赖易失Publish。与既有挂卡集合去重。任务终态/归档后的关单事件补齐后才退订，重启从未收口记录恢复。

若A的现有任务列表不包含重启窗口内已归档任务，先补窄的列表筛选/分页恢复语义；不能仅扫当前running从而漏永久待办。只有核实该补强比新全局源更重时才用备选B：GET `/api/ticket-events?after_seq=&limit=` 读取执行机持久工单事件并带next_seq。B新增全局扫描API/客户端/游标/索引及来源枚举，成本高于A复用列表+每task公开事件，现无证据证明必须。不得先落B全套。

只建议一张通知领域持久表 `task_ticket_notices`，唯一键(source_machine,task_id,ticket_id)；列source_seq、ownership_version、target JSON可空、status、reason、last_error、retry_after、updated_at，以及恢复所需源游标（可复用既有镜像游标能力，首次实施核真实可复用处）。状态闭集 `pending/delivered/needs_human/superseded/answered`；当前记录CAS更新与不可变寻址事件同账本事务。来源去重以machine/task/ticket/source_seq，通知投递幂等再加ownership_version；无需额外task_ticket_sources和owners表。原工单、答案CAS仍归orchestration+执行机store。

没有target时照样保留这条原工单待办，status=needs_human，reason闭集 `origin_unknown/resident_missing/recipient_unreachable/delivery_failed`。任务详情/现有跨机任务列表的工单投影附 `notification` 可空对象（status/reason/ownership_version/last_error）；CLI show和web原工单面板展示，因此无卡有载体。重试复用原工单操作入口增加单按钮，不新增全套CRUD。冻结窄接口：`POST /api/tasks/{id}/tickets/{ticket}/notification/retry`，空 JSON 对象 `{}` 请求，成功 `200` 返回 `{notification:{status,reason?,ownership_version,last_error?}}`；由既有 `byTask` 路由到原任务源机，处理方忽略调用方传来的来源/目标/版本（body 不允许其他字段），从执行机任务、待答票、原 permission/question 事件和现有账本重新核实并解析。未知 task/ticket 返回 `404`；票已答/void 或不再 pending 返回 `409`，其中只有执行机权威票/事件确认已答或 void 才能把通知记录收为 answered；源 machine 暂不可达/读失败返回 `503` 且保留当前通知状态。接口不调用 Reply、不改变答权/CAS。web的“重试通知”在 needs_human、recipient_unreachable 或 delivery_failed 时可见，独立调用该接口后刷新现有详情；成功不等于目标机已承认投递。delivered只是通道确认，不等于人已读/批准。

## 消费者与目标机

task_ticket_notice需生产→proto/JSON→账本候选查询→解码/匹配→持久游标→session wait、card wait、residentpush print、residentwake desktop逐环接线。cmd/session.go:565当前只接EvRoomMessage，须补独立候选DTO，不能强解RoomMessage。明确target才匹配；未寻址、别人的target、普通system/by_system/pointer零唤醒。pull只原待办/card wait，不起print；不把pull当丢件。

print/desktop仅由当前目标归属机消费，投递前核resident/席位和ownership_version；其他机不能调用本机铃后记成功。desktop铃只提示，内容通过session wait；失败保留待办/重试。外部回合/门铃不与账本事件假称原子提交。原工单回答CAS、同答案幂等、合法答权保持。

## P0硬门与实现边界

已知源码：任务waiter无截止等人工（manager.go:2944-2959、3068），协调者另有30min（server.go:1573,1585,1628），LaunchAdmit回合返回才释放（scheddrain.go:470-479）；驻场准入也是defer（residentpush.go:259-263）。真实opencode fixture已完成短launch/resume，不证明原工单等待退出或准入归零。

契约核定后可实施项：origin传播、最小来源复用、通知单表CAS/事件、三态路由、无卡工单可见投影、寻址候选/游标接线。但开S3前仍须P0真实外部CLI从房间发任务产生permission/question，验证发起回合短时退出、LaunchAdmit/驻场准入归零、原任务仍待答、重启回答恢复；协调者补probe。双机同测试PG断连/补发/换责/目标机投递是后续真实验收硬门。不能以fixture、源码或30min耗尽结案。若需改Runner无ctx签名或hostapi承载，停段另呈契约，不实现自身权限桥。S2独立。

存储归属不变：任务origin属执行机任务事实；会话/通知域解析已有卡/驻场公开面并拥有通知记录；ledger仅持久化；approval不反查内部。新增范围限上述真实接缝，不建通用总线或所有者管理系统。图覆盖债：checkout无codegraph目录，按源码定位；未实现/验收。

## 协调者核定与P0补证

核定推荐A：origin_session单字段、现有当前resident作回退、一张通知表与task_ticket_notice独立事件；不增加所有者配置/CRUD。实施前补齐源码字段/枚举的跨语言roundtrip守卫；若现有任务列表/源游标不能满足未挂卡恢复，带证据回协调者，不静默落备选B。通知归属不改回答权，保持r1.3审批与路由边界。

真实隔离agentd+独立PG+真实opencode1.18.32+loopback协议provider补证（不是fake executor，不调用生产模型/凭据）：session创建4.666秒完成，真实驻场bash工具发起任务b525424b-a86f-4341-b872-9aa83bb2a219；原任务waiting_answer有1张ask，carrier/p0coord与squad/p0coordinators/p0coord计数均0，任务载体计数1。隔离agentd重启后仍waiting_answer/同工单；reply返回ok=true,relayed=true，原任务running/待办0；随后stop原任务、agent退出0。原始脱敏报告/tmp/b469-p0-runtime/live/report.json。该证据解除单机短回合/准入/待答重启基本阻塞；permission分支、真实模型行为、双机断连/换责/补发仍属于后续验收硬门，不计已通过。
