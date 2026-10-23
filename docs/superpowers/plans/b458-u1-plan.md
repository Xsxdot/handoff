# B458 / U1 首段实现计划（提案，待协调者拍板）

## 偏离与自主决定

出稿未改父 spec/plan，未写产品实现、未运行服务/PG/远端；只在 `/tmp` 的源码副本用隔离 SQLite task 观察生产 Judge，未建权限票。建议把原 U1 的 S1/S4 合段拆为首个规则生命周期竖切和后续解释/UI段，是交付顺序细化，待协调者拍板。原 `nl|sed` 的真实执行器请求形态仍未验证，不把判据 API 观测当真机出票事实。

## 待拍板岔口

1. **原例的条件/覆盖解释**：在 `/tmp/b458-gate-probe.oA0eXV` 的 `HEAD` archive 副本，以生产 `Gate.Judge` 和 `Authority.Judge`、Tool=Bash、Cwd/Workdir=副本根及安全 `README.md` 观测 `nl -ba <绝对路径> | sed -n '1,20p'`，两者均得 `Consult`、空 Rule、`黑名单未命中`。Authority 使用隔离 SQLite 任务范围，但未建权限票。这是判据 API 事实，不证明真实 executor 是否发票、AdapterEvent 原字段或完整限制提取。spec 五类是可覆盖的限制，未必是所有普通命令的总分类。若真实请求和完整 AST 证明无五类特殊限制，严格主体/操作/对象条件配 `covers=[]` 可能成立：覆盖全集为空但条件仍须非空、具体且全匹配，既有不可覆盖先行。这保留原例而不新增第六类；代价是普通命令授权面更广，必须特别锁住未知脚本/动态目标/包装器/替换/截断不能因空覆盖而获 allow。未知限制不能当空集。此解释待协调者拍板，不自批、不造伪工单。首段可先选已批准五类中一条真实待答且可证明条件的请求完成竖切，原例故事边界保留待澄清。
2. **首段边界**：建议先交 CLI 显式创建/查看/撤销一条有界规则、同事务审计、真实任务下一请求匹配、规则派生授权的到期/撤销复用阻断。UI 保存与规则管理形态尚待 `prototyping-in-brainstorm` 原型确认，后端和 CLI 可先做；S4 四类原因、历史视图、去重另接第二段，不据首段宣称 S1/S4 完成。
3. **账本窄接缝**：`internal/ledger/registry.go` 现有 `RegistryPut` 自己 `mutate`，`appendEvent(tx,sink,…)` 是事务内私有方法，门面只有独立 Put/Get/List/Delete。建议增「规则版本 CAS + 不可变事件 + 权威 UTC 时间」单次账本操作及门面，规则语义留 approval；不把所有 registry 写一概加事件。精确方法名与 wire 由协调者在首次跨进程实现前核定。

本稿只细化下一段可验证工作。输入：[spec r1.4](../specs/b469-approval-batch-spec.md)、[父 plan r1.4](b469-plan.md)，两者已批准；本稿仍是提案。工作位置 `codex/approval-batch-b469`，冻结源 `381911e18` 由协调者复核新鲜性。所有工作在 Codex；不派 handoff executor。本段不改永不可覆盖集合、已批准五类或 reply 信任边界。

## 故事闭环与责任

| 步骤 | 触发者 → 权威事实/载体 → 消费者 → 可观察结果 | 实现/集成责任 |
|---|---|---|
| 显式规则管理 | 主令牌 CLI（后续同级 cookie）→ approval 校验后的账本规则快照和同事务事件 → 管理读面 → 规则 ID/版本、actor/设备、主体/范围、起止时间、来源工单与撤销事实可查 | Luna：approval、agentd/CLI、ledger 窄方法；协调者审信任边界和线格式 |
| 下一请求 | 原执行者 permission → `permgate` 完整限制/不可覆盖判据 + approval 最新规则 → 原任务执行者 → 满条件才得到原生 allow；未覆盖仍走原静态/模型/人工链 | Luna：approval 与真实任务接线；协调者隔离原任务复验 |
| 复用失效 | 同任务后续请求 → `store` 已送达 grant 的来源 + 账本最新版本/权威 UTC → Authority 与 OpenCode Client → 撤销/过期/读错不复用规则 grant，独立人工 grant 仍按同任务同指纹复用 | Luna：两条生产路径与持久来源；协调者两机 PG 及变异复验 |

触及面：`internal/approval`、`internal/permgate` 为逻辑型审批域；`internal/ledger` 与 `internal/ledger/api` 为持久边界型；`internal/agentd`、`internal/client`、`cmd` 是 HTTP/CLI 边界型；`internal/orchestration`、`internal/store` 与 `internal/proto` 是任务消费/原工单线。各面有有界文件集和真实消费者；此段由一位 Luna 顺序实现，不人为拆独立子卡。agentd 的既有大包竖切债已在父计划，不能趁本段泛化重构。

硬依赖：U2 已通过的 AST/完整性规范化必须进入生产 `Gate.Judge` 入口；新规则条件只用它能够证明的目标事实。规则保存与不可变审计是单事务硬依赖；规则派生 grant 必须先有持久来源，复用端才可上线；权威账本 UTC/新鲜版本是规则授权硬依赖。S3 收件链无此硬依赖。CLI、UI 共享 agentd 管理能力；UI 原型未确认是 UI 施工前置，不阻塞 CLI/后端。共享 checkout、root 的 S3 双机验收和 PG 资源只是施工排序，不能写成数据依赖。

## 执行次序与交回物

1. **承重实验与红灯**：在隔离任务还原原例的真实权限载荷、当前 `Gate.Judge` 出口及完整限制集；按稿首岔口报告原例有无票、是否可证 `covers=[]`。再选一条获准分类的真实待答 permission，先写通过生产规范化、未知目标/字段、多限制、永不可覆盖反例的红灯断言。不得用字符串子串匹配替代 AST、不得把 deploy 覆盖从空扩开。
2. **最薄持久与管理接缝**：定义版本化规则值（仅已批准主体、操作/对象条件、项目或全局范围、来源、开始/截止、actor、设备、撤销）和明确的 JSON 版本；缺字段与显式零值区分，未知 schema 版本/字段不得授权。CLI 显式 `create/show/list/revoke` 经既有 `newTargetClient` 主令牌与 agentd `auth`、`resolveConsoleIdentity`；cookie 管理 API 与之共用服务器校验。无截止必须显式输入/显示“持续至撤销”，不把字段遗漏当永久。模型/执行者不得走隐式创建；管理动作仍经原人工审批控制门。CAS 版本冲突、重复请求与审计失败不得留下半个成功；撤销保留墓碑版本，不用 `RegistryDelete` 抹掉来源。
3. **一次真实规则裁决**：approval 先取完整限制和不可覆盖例外，再读最新规则/权威 UTC，并要求条件与限制全集相符；任一证明缺失、规则读错/到期/撤销则沿原链。命中写规则 ID/版本/期限、请求/工单关联与来源的裁决事实，再按原任务协议原生回传；不新造回答载体。保存规则与答原工单是两个事实：保存失败绝不批准，保存成功而原生回复失败分别可见并可恢复。
4. **复用两腿**：`internal/store/store.go:FindReusableGrant` 目前只按任务、指纹、allow、delivered 查询；`proto.Ticket` 没有来源。把来源穿过实际 grant 写入/读取转换，再令 `approval.Authority.FindReuse` 与 `approval.Client.tryReuse` 在规则来源时共同复核最新版本、撤销、权威 UTC 和不可覆盖判据；旧人工 grant 沿现行合同，不因规则撤销失效。传输重放只做幂等，不提升授权。读取失败一律回原审批链。
5. **日志与意图注释**：按仓内结构化 `slog` 在管理进入/结束、存储前后及错误、规则匹配/未命中原因、复用来源复核、原生投递成功/失败留可关联 task/ticket/rule/version 的脱敏日志；审计正文/凭据不入日志。新文件头说明责任边界，导出方法写语义注释，信任来源、事务和已投递不追杀处写 why 注释。交回首节必须是「偏离与自主决定」，其后给红绿、真实序列化证据、未验、日志/注释与卡证据。

## 验收与关键反例

Luna 先跑触及包红绿与必要的 Go/CLI 检查；断言从 CLI 请求 JSON → agentd 鉴权/解码 → approval → ledger 门面/事务 → 真实任务下一请求，覆盖字段缺失/空/零、未知版本与新枚举白名单。原任务 `nl|sed` 若实验可成立则做显式保存→下一请求命中→撤销/到期→同任务也不借旧规则 grant 复用；若实验不成立，只报告该事实，不宣称该示例验收。

反例必须会红：空条件、未知目标、路径/机器/子命令/ref 变化、多限制仅部分覆盖、黑名单/包装器/eval/截断/控制面/账本凭据、模型 approve 试建规则、未授权/缺 console_user、CAS 冲突、审计插入失败回滚、重复 create/revoke、读错或断连、规则刚到期、撤销提交后新 Judge、旧缓存与两机时钟偏移。独立人工批准的同任务同指纹复用保留；旧规则 grant 不得因新版本/撤销/到期继续放行。已交付原生执行者的动作不追杀。测试不以「有事件」替代实际回传，也不以 SQLite 通过宣称 PG 或双机通过。

协调者负责隔离 `LEDGER_TEST_PG_DSN` 的 PG 自举、两机有偏移时钟/断连/重连与真实原任务 permission 演练、挑一处承重变异复验、S1/S4 分别判决。仅记录脱敏环境别名、提交和证据 hash，不打印 DSN。若账本可靠 UTC 取法无法从现有实现成立，阻塞规则 allow 并回到协调者；不偷用 agentd/CLI 本机钟延长授权。

## 需冻结的新增共同接缝：精确候选 v1（全部待协调者拍板）

真实基座：`ledger.RegistryEntry{Kind,ID,Version,Seq,Body}`，`ledger/api.Facade.Put/Get/List/Delete` 只做当前快照，`ledger.Store.mutate` 与 `appendEvent(tx,sink,…)` 能同事务写事件；`proto.Ticket`、SQLite `tickets` 现在只有 fingerprint，没有来源。下面是首段候选，不假称已实现或批准。只针对第一条可证明的真实限制做 `condition.kind`，后续类别须扩枚举和正反用例，不能以一个抽象字符串开全工具覆盖。

候选当前快照 `registry.kind="approval_rule_v1"`、`id=<服务端生成 UUID>`、`Body` 严格 JSON：

```json
{"schema_version":1,"subject":{"executor":"codex","machine":"machine-id"},"scope":{"kind":"project","project_id":"project-id"},"condition":{"kind":"out_of_scope_write","tool":"write","operation":"write","object":{"machine":"machine-id","canonical_path":"/absolute/proven/path"}},"covers":["out_of_scope_write"],"source":{"task_id":"task-id","ticket_id":"ticket-id","actor":"user:name","device":"device-or-empty"},"valid_from":"UTC-from-ledger","expires_at":"UTC-or-null","revoked_at":null}
```

首段 `condition` 可改选 spec 五类中另一条**真实**可证明限制，但必须在开工前同步改此候选及断言；这不是让实现者自行扩类。`subject.executor/machine`、`scope.kind`（`project|global`，global 时 `project_id` 必须显式 `null`）、`condition.kind/tool/operation/object`、`covers`（去重且为已批准可覆盖限制）、`source.task_id/ticket_id/actor/device`、`valid_from`、`expires_at`、`revoked_at` 都要求键存在；显式 `null` 仅允许项目 ID、截止和撤销时间。`device=""` 仅主令牌无登录会话时合法，不能伪称设备证明。`covers=[]` 仅在首岔口拍板且完整 AST 证明无特殊限制时才可用；它仍要求明确完整命令形态与对象事实，未知脚本/动态目标/包装器/命令替换不匹配。未知 key、未知枚举、零值主体/对象、缺必需键 fail-closed。`Version` 只信 registry 列，Body 不复制可伪造的版本；`Seq` 是当前 registry 全表写序，不当事件 seq。

候选管理 HTTP（agentd 现役 `auth` 包住 `/api`，CLI `newTargetClient` 主令牌调用）：`POST /api/approval/rules` 用 `{request_id,expected_version:0,subject,scope,condition,covers,source_ticket_id,expires_at}`；`GET /api/approval/rules`、`GET /api/approval/rules/{id}` 读当前快照；`POST /api/approval/rules/{id}/revoke` 用 `{request_id,expected_version}`，保留墓碑。`expires_at` 键必有，显式 null 表示持续至撤销，CLI 必显示这句话并要求显式选择；延长规则另需显式管理动作、CAS 与审计，首段可在后续实施但不得靠覆盖旧值偷做。服务端从 `resolveConsoleIdentity` 注入 actor/device，不接受客户端自报这两个权威字段；来源工单须服务器查证而非盲信 `source_ticket_id`。成功返回 `{rule,version,event_seq,effective_at}`；`effective_at` 是账本事务 DB 权威 UTC，不能用 agentd/CLI 本机钟。列表/详情也带 registry version 和明确有效期/撤销。

候选错误面（响应 `{code,error}`，不回传敏感命令全文）：无凭据 `401 unauthorized`；凭据有效但 `console_user` 不可解析或管理门不允许 `403 forbidden`；字段缺失、未知版本/字段/枚举、空条件或永续选择未显式 `400 invalid_rule`；规则 ID 不存在 `404 rule_not_found`；`expected_version` 不符或同一 `request_id` 携不同内容 `409 cas_conflict` / `idempotency_conflict`；账本不可达、不能取得权威 UTC/新鲜快照 `503 authority_unavailable`；事务内审计/提交失败 `500 commit_failed`，且无规则版本变化。原 `reply` 的授权信任级不改；失败绝不宣称创建/撤销成功。

账本本进程门面候选：在 `internal/ledger` 加窄 `CommitApprovalRuleCAS(id, requestID, requestHash, expectVersion, actor, build(version, dbUTC) -> (body,auditPayload)) -> (RegistryEntry,eventSeq,effectiveAt,error)`，`internal/ledger/api` 同形转调，approval 负责纯函数构造/校验 JSON，账本只识别专用 kind 与事件类型 `approval_rule_changed_v1`。一次 `mutate` 内先看 `registry.kind="approval_rule_request_v1",id=requestID` 的提交回执：存在且 hash 相等即返回原 rule/version/eventSeq/UTC，hash 不等即幂等冲突；不存在才以规则 registry 的 `expectVersion` 做 CAS、从数据库取 UTC（PG 要核 `clock_timestamp()`；SQLite 用账本单机权威时源）、将**同一** UTC 交给 build 填 `valid_from`/`revoked_at`、写规则快照、`appendEventAt(tx,sink,"",eventType,actor,auditPayload,dbUTC)`，最后写请求回执 `{request_hash,rule_id,rule_version,event_seq,effective_at}`。回执只服务此用例，不是通用注册表框架；三写任一步失败全回滚，不新增 PG 表。返回注册表版本、全局事件 seq、UTC；事件 payload 至少 `{schema_version:1,action:"created|revoked",rule_id,rule_version,request_id,source_task_id,source_ticket_id,scope,actor,device,effective_at}`，事件不含凭据或原始命令正文。重复相同请求回原提交版本/seq，冲突返回 409。这里的 `build` 是同进程纯函数接缝，不经 HTTP 传函；若 review 判接口过宽，只可在 ledger 内收更窄已验证 DTO，不拆成 Put 后再 AppendEvent。

grant 来源候选 `proto.Ticket.GrantSource` 与 SQLite `tickets.grant_source` JSON 列，值严格为 `{"schema_version":1,"kind":"human|approver|rule|legacy","rule_id":null,"rule_version":null,"rule_expires_at":null}`；`rule` 时后三项均非空，其他 kind 必须为 null。新建/已答/人工 `AnswerTicket` 的**持久写入同事务**设来源，再经 `GetTicket`、待答列表、原任务回放、`FindReusableGrant` 实际序列化投影返回；不能仅加 proto 字段。候选选择**规则 allow 走既有「建单即已答」路径**：非 OpenCode 复用 `Manager.approvePermission` 的 `CreateAnsweredTicket → RespondPermission(once) → MarkTicketDelivered` 真实次序，OpenCode 复用 `Client.consult` 的 `CreateAnsweredTicket → adapter Acknowledge(AckDelivered)` 次序；规则来源在建单同事务写入。原生未送达不得被 `FindReusableGrant` 当先例；静态 `AutoAllow` 仍保留现有无票路径，不能借它写规则 grant。旧数据迁移先在功能启用前把所有既有票（含待答）明确回填 `legacy`，表示**上线前不可能由规则产生**，保留其原复用；新票无来源/未知 kind 一律不复用，不能按空值猜成人工。旧 `legacy` 与 `human` 沿现同任务/同指纹/已送达 allow，`approver` 沿既有模型 grant 合同；`rule` 每次须读最新规则 ID/版本、权威 UTC/期限/撤销及不可覆盖判据，不能因查询按时间只取第一票而漏掉更早独立人工 grant。两条现入口 `Authority.FindReuse` 与 `Client.tryReuse` 必须共用来源验证语义，OpenCode 的政策版本盐指纹保持现状。

各冻结断言单独验：① 未知 schema/条件键不授权；② 空条件不授权；③ 未知限制不是空覆盖；④ 多限制必须全集覆盖；⑤ 无截止只由显式 null 表示；⑥ CAS 与审计同成败；⑦ 撤销留下新版本及事件；⑧ 账本 UTC 与规则快照来自同一读/写事务；⑨ rule grant ID/版本/截止穿真实 ticket/事件链；⑩ rule grant 每次读新鲜来源；⑪ 无来源/未知来源不复用；⑫ 旧 legacy 与独立 human grant 保留现行同任务语义；⑬ 管理动作只从现行 cookie/主令牌明确入口到达，模型批准不写规则。每条测试要能单独红，不以两端 mock 取代真实 JSON 链。

**剩余技术核对与阻塞**：`clock_timestamp()` 与 SQLite 账本 UTC 的真实实现及读事务一致性、现有 SQLite 迁移位置、`EventType` 消费白名单、CLI/HTTP DTO 投影和 rule allow 用票或无票的真实形态均需在首个 wire 施工前由 Luna 给编译/回放证据并交协调者核定。本稿给了明确候选值和接口，但上述任一事实不成立即回到稿首裁决，不改 spec 边界硬凑。既有 `keysclient.Runner`、`hostapi.Host.RunTurn`、reply CAS 不重述。

图覆盖债：本 checkout 无 `codegraph/`，按真实源码定位；无图可命中的符号。未验证真机项已列上节，测试夹具不代替实际行为。
