# B469 四卡实施计划（r1.4 已拍板）

## 偏离与自主决定（协调者已核定）

- r1.3 原批准基线仍有效；r1.4 把已授权裁决 B469 #33153/#33182、B463 #33181 和独立审查 #33180/#33179 转成下一段修复计划。**独立 Sol 出稿后，根协调者已核定全部 r1.4 与 I5 精确 wire（2026-10-23）。** 用户已裁决的确认级别、未知 kind 游标和 S3 独立合并资格不再重复请用户批准。
- S1 的“可信独立人类确认”硬门取消：cookie 或 CLI 主令牌上的显式规则管理动作与现行 reply 同级；不建 nonce/新 secret/WebAuthn。执行者无权用模型 approve 建/放宽规则，管理面人工门禁、actor/设备/有效期/撤销审计仍在。
- I5 选择执行机 **task creation source generation** 的窄增量列表、持久每机水位与原有每 task 事件回放；不用 `created_at` 时间水位，避免同 timestamp/时钟回退漏任务。源侧建议只在 `CreateTask` 同事务写一条 task ID→自增 seq 的窄索引，旧任务迁移时一次回填；目标侧复用 ledger `registry` 的 CAS JSON 保存每源机水位，无新增 PG 表/3→4 升版。通知地址未变不进写事务。
- 协调者已核定 I5 准确增量发现 wire 与源 task 生命周期/迁移边界；按 [S3 contract v1.1](b469-s3-contract.md) 施工。持久 scalar 水位不足以恢复进程重启丢失的未出票订阅，启动须从 0 有界分页重建；稳态用持久水位增量发现。桌面原生端按用户决定暂留未验。

- 状态：**r1.3 已拍板；r1.4 已由根协调者拍板（含 I5 精确 wire）（2026-10-23）**。旧依据 B469 #33059；本轮用户裁决 B469 #33153/#33182、B463 #33181。审查修复见[本轮台账](b469-r14-ledger.md)，原计划证据见[原台账](b469-plan-ledger.md)。
- 输入：[spec r1.4](../specs/b469-approval-batch-spec.md)；审查快照 `c93fbb627`，执行前刷新事实。所有工作由 Codex 本会话推进，无房间/远程派发。

## 当前实施状态（2026-10-23，非最终验收）

- U2/S2 本地实现提交 `e24d13605`，技术审查与触及验证见 [B463 review 台账](b463-review-ledger.md)。
- U3/S3 本地实现提交 `8cdff7fcc`，新增 wire 按 [S3 contract v1](b469-s3-contract.md) 由协调者核定；实际双机基础链、离线补投与驻场换责见 [S3 runtime 台账](b469-s3-runtime-ledger.md)。卡席位 push/pull/祖先、缺归属重试和显式 session wait 的双机联合矩阵已通过；默认游标跨CLI进程不重收也已实测；不能将代码提交等同整批最终通过。
- P0 证明原票等待与协调准入释放可并存；用户 #33153 选择与现行 reply 同级的显式确认，S1 已解阻，尚未开工。
- 用户明确「保持全 Codex，桌面项暂留未验」；不将 print/CLI wait 当作真实桌面 OS 验收。审查快照 `c93fbb627` 已 push；尚未合并或升级，动作门继续有效。

## 本轮最薄修复单元与验收（先于 U1/U4）

S2 与 S3 的旧技术审查结果均为 fail；已有测试通过仅是基线读数。以下每单元交付变更、可变红的反例、日志/意图注释和证据，执行者先写失败断言，再改生产路径，协调者独立复审。单元共享源文件时串行施工；S3 对 S1/S4 无契约硬依赖。具体源码边界见[r1.4 台账](b469-r14-ledger.md)。

| 单元 | 有界修改与完成断言 | 责任/依赖 |
|---|---|---|
| I1 / S2 | `internal/orchestration/approver_context.go` 与 `internal/permgate/credential.go` 的同源政策区分**完整命令形态**的变量名投影和实际值读取，不凭子串 `cut` 放行；已验证只读 `--help` 按只读裁决。现状 `credSegments/Words` 丢管线结构、`credEnvDump` 对 env/printenv 皆 hit、`SecretEnvCommandsText` 让模型把任意 env 转人工，三处须一起核。原 B461 13 条 help 与 B453 env 原票逐条记证；隔离真实模型跑 `env \| cut -d= -f1 \| sort`、`compgen -e` 和 help。`printenv` 无参、`env` 裸调用及 `printenv SECRET` 实际输出值，`cut -f2`、后接 `printenv`/`tee` 或重定向的旁路、动态 shell/危险 wrapper 保持门禁。fixture 绿不算真实模型通过；不扩静态 allowlist。 | Luna 实现 S2；独立复审；协调者供隔离真实模型实验。与 I2 可同一 S2 修复提交，判据分别裁决。 |
| I2 / S2 | 从真实 `Decide→decideLocalChain→decideWithCandidate` 入口核每支日志：本地候选 `attempt.Err/final.Err`、env resolver cause、Invoke permission/cause/reply.Text、nonce permission 均脱敏，不记录 permission/cause/output 原文；默认 Warn 级别观察开始/结果/超时/跳过理由，含 codex 环。真实入口注入凭据哨兵，成功/失败/超时及 `error_kind` 断言和 m3c 日志级别变异锁住；日志可关联 request/ring，不泄正文。 | Luna 实现 S2，独立日志审查。 |
| I3 / S3 | `cmd/session.go` 的候选 SQL、wire notice.Target、当前归属 target 各有**本层入口**的独立反例：候选查询不枚举 other；wire 目标不匹配不交付；旧 ownership_version/current target 不匹配不交付。端到端真实账本 other→sy 通知在 sy 的 `session wait` 无输出、无 delivered，合法 target 可收且 cursor 正确；只有同时拆掉三层防线才要求整条消费者测试转红，不要求任删一层都漏件。 | Luna 实现 S3；先于 S3 review。 |
| I4 / S3 | `internal/agentd` 用真实 `store` 的 permission/question 待答票驱动通知投递，断言 delivery 后仍 pending、未调用 `AnswerTicket`；合法回复只通过原票 CAS 生效。注入错误的 allow 调用应转红。 | Luna 实现 S3；与 I3 独立判决。 |
| I5 / S3 | `internal/store` 的 `CreateTask` 同事务记录单调创建 seq，旧任务一次回填；`internal/agentd`/`internal/client`/`internal/proto` 提供有界源任务发现页。`internal/ledgermirror` 复用 ledger registry CAS JSON 存每机水位（不可借用 mirror_cursors 的事件健康 seq）；**所有非终态 task 即使无 origin/当前无票也订阅**，终态仍有待答票亦补拉，不能沿用旧过滤。重启从 0 有界重建未出票订阅，稳态按水位增量，每 task 原有事件继续回放。地址/状态/来源/退避事实未变，`PutTaskTicketNotice` 进入写事务前返回，`updated_at` 不变；已 delivered 可周期只读核归属但不每 10s 写。测无 origin 任务先 running 后 question、同 timestamp/时钟回退、跨页/断连/终态未答、重启旧活动 task、静止零写。 | Luna 实现 S3；准确 wire 与迁移已核定，按 S3 contract v1.1 施工；证伪源事实再上报。不得造通用 bus/新 PG 表；若确须 PG DDL，先评估 schema 3→4。 |
| I6 / S3 | `task_ticket_notice` 未知/非法 kind 与畸形 payload：修 `internal/proto/delivery.go#ValidateTaskTicketNotice` 的旧“cursor 不推进”注释与调用方错误分类；`cmd/session.go`、card wait、agentd 自动化/print/desktop 各跳过该条，Warn(kind/seq/source，无正文)，推进游标，后续合法通知仍消费；未寻址/别人的 target 不唤醒。`internal/agentd/taskticketnotice_consumer_test.go` 旧“非法即报错”测试须改成端到端坏→好两条反例和日志泄露哨兵，不能仅把 mapper 错吞掉。 | Luna 实现 S3；按 B435 r1.4 与 S3 contract v1.1；不能以忽略错误但不推进 cursor 结案。 |
| I7 / S3 | 从真实 `POST /api/tasks` JSON 经 agentd、store 到 `GET /api/tasks/{id}` 投影断言 `origin_session` 精确往返；缺席旧任务保持 unknown，非法非空 400。删去任何一处手写投影应转红。 | Luna 实现 S3；跨语言 web DTO 另核缺失/零值。 |

**Minor 逐项收口**：S2 在 `Gate.Judge` 真入口给命令替换、进程替换、if、函数体内真实 handoff 正例及危险反例补锁；AST 中 `arg.Lit` 不可直接代表双引号动态参数，`sh -c` 判定按实际 option token 而非 `strings.Contains(word,"c")`，不把修法变成完整 shell 解释器。`echo "$(handoff …)"`、`"handoff" dispatch`、`H=handoff; $H` 若解析事实不能证明可静态安全，保持 consult，逐条在交回物记录事实和原因，不把它们静默算通过。S3 的 desktop 门铃失败重扫遵守持久/内存退避；retry 源读失败用 503、保留状态；事件搜索分页越过前 10000 条；`ListActiveTaskTicketNotices` 增源 task 过滤并按调用点使用；若保留 `retry_after` 列则真实写入/按到期重试，否则同批删 wire/列的虚假承诺并重审契约；`setCardNoticeDelivery` 仅版本冲突记版本变化，其余错误记真实 cause。证据 JSON 含对应提交 SHA、入库 manifest 列命令/环境/脱敏文件 hash；web 新状态/重试 UI 补具体原型版本、确认状态和实际差异记录，未确认时不得声称形态已验收。agentd 81 非测试文件/Server 462 方法的竖切债入 roadmap，不趁本轮重构。

**阶段门**：I1–I2 与 Minor 核清后 S2 独立复审；I3–I7 与 S3 Minor 核清后由协调者用隔离 `LEDGER_TEST_PG_DSN` 跑 PG 及双机关键回归并安排独立复审。测试 DSN 只从专用 0600 配置注入进程，证据只记库别名/结果，不打印 DSN。S3 可先于 S1/S4 单独合并的资格由上述证据触发；合并本身与机队升级另按动作门执行，schema 2→3 不能混版。之后 S1/B458 按用户 C 开工，S4/B470 随 U1/U4，不抢写这两条全套实现细节。

## 稿首批准基线与实施待审

spec/plan r1.3由#33059最终批准拍板；r1.4 依据#33153/#33182/#33181修订，已由协调者核定。原顺序P0→U2/S2与U3/S3→U1→U4不变，先处理上述 review 修复；S3 达成独立合并条件时可提前收口。准确新增wire仍须按下文冻结程序核定，不得越过已批准边界。

1. **原工单载体与等待准入**：复用任务原 permission/question 工单；在任务发起/关联会话处记录显式唯一审批责任归属及版本，不广播resident，不纳入组织者。卡路由完整保留自身push→自身、pull→card wait、空座沿父卡找push祖先；否则任务发起会话驻场者按print续轮或桌面门铃+session wait；否则可见needs_human给人。归属只管通知，不限制合法回答者，原CAS保留。未挂卡任务执行机侧发布/镜像与独立投递记录一并交付。等人事实持久化后退出回合、释放准入，不占满 30min 回合/槽位。P0 验证现有 Runner 回合实际何时结束；失败阻塞 S3，不换成 hostapi 自身权限桥。
2. **解析器**：推荐固定版本 `mvdan.cc/sh/v3/syntax` 解析受支持 POSIX/bash 输入，按 AST 判命令位置、参数、替换、重定向；不继续扩充手写分隔器。未知 shell/不支持语法/截断保守走原门禁。新增依赖版本/许可证及实际输入方言在 U2 开工前核实；若该库不支持真实输入，回报证据再拍板。规范化是规则匹配的**硬依赖**，不是共享文件施工排序。
3. **规则管理确认**：CLI 主令牌可得到 `user:<console_user>`（`internal/agentd/identity.go:34-60`），不能证明独立人类在场；用户 #33153 明知并接受这个与现行 reply 同级的边界。控制台 cookie 或 CLI 主令牌上的显式规则管理动作就是本期确认，创建/延长不另加 nonce、secret 或 WebAuthn。模型 approve 不能创建/放宽规则；执行者触发审批控制面仍须人工门禁。管理动作记录规则草案/来源、actor/设备、期限、撤销和不可变审计；“本任务相同请求批准”不写规则。
4. **生命周期与复用**：采纳“新裁决必见撤销、已原生投递不追杀”；到期与撤销同等处理。规则派生 grant 复用前读取来源版本/有效期/撤销，读取失败不得复用。已批准“本任务相同请求批准”继续现有同任务同指纹人工复用且不加新门禁并以“本任务相同请求批准”明确提示；独立人工 grant 不被某条规则撤销连带抹除。未来授权与传输重放幂等分开。
5. **分类、审计、UI**：采用 spec 有限分类及求值顺序；普通 push/部署是本期新增分类，不能靠现有黑名单 Reason 猜。registry 保存当前快照；规则 CAS 与不可变事件同事务，历史/命中/去重待办由事件投影。UI 只在现有工单加入显式保存确认、历史/原因/撤销；实施前按 prototyping-in-brainstorm 从 `prototypes/base/` 分支展示并确认形态。
6. **冻结**：已拍板的语义原子断言；新增 wire 的准确字段/枚举/错误与投影清单由首段真实消费者形成后提交协调者审查。现有 `keysclient.Runner.Launch/Resume` 无 ctx，`internal/keysclient/keysclient.go:60-71` 与 `server.go:1581`；`hostapi.Host.RunTurn` 已冻结。S3 不预定改签名；任何影响 launch/resume、卡席位及驻场共用路径须重新审查并分别回归。

## 目标、责任与行为闭环

只交 spec r1.4 的 S1–S4。本轮先修审查 I1–I7，B463 codex 无日志、help/env 名误升，B470 四类原因/去重/历史/命中/有效期仍为本期后续；旧 agy 专项暂缓。hostapi 自身权限桥不再是交付项。

| 责任与类型 | 有界范围 / 真实消费者 | 事实归属与依赖 |
|---|---|---|
| 权限/审批（逻辑型） | `internal/permgate/`、`internal/approval/`；Authority.Judge/FindReuse、任务权限链 | 唯一分类/规则语义，不复制到 CLI/UI |
| 任务编排（逻辑型） | `internal/orchestration/approver*.go`、既有 handlePermission/AnswerTicket 路径 | 原工单、原生回复、单次投递与人工复用来源 |
| 账本（逻辑型，远端存储为边界） | `internal/ledger/registry*.go`、事件写入/读取公开面 | 当前规则版本 + 不可变审计；事务/CAS，不解释授权 |
| 收件/会话（边界型） | `internal/agentd/wakeconsumer.go`、`residentpush.go`、`residentwake.go`、`internal/ledgermirror/`、`cmd/session.go`、执行机侧工单发布/任务发起与归属入口、`internal/keystone/` 的既有消费面 | 显式归属/唯一责任、push/pull投递；不读取审批内部，不广播驻场 |
| 管理入口（边界型） | agentd/proto 审批/工单 API、cmd/reply 与规则命令、web API/TicketsPanel 及直接调用者 | 与现行 reply 同级的显式确认、草案/查看/撤销/历史；不本地匹配 |

这些是有界责任，非按目录强造子系统/子卡；规则线B须等U2规范化；P0后S3与U2并行，实际共享文件再串行施工。Manager/server 大对象仅改本用例组装点，圈不住上下文则停段提出竖切还债。组装点绑定具体实现，其他消费者经契约；审批不反查会话/任务内部。

| 故事 | 触发者 → 权威事实/载体 → 消费者 → 可见结果 → 实现责任 |
|---|---|
| S2 | adapter/审批候选环 → AST/单环结果 → Gate/审批链 → grep/help/env名合理判定、失败可解释 → 权限/审批执行者 |
| S1 | 人工工单确认 → 规则版本/确认来源 → 下一任务权限 → 保存/命中/撤销 → 管理+审批+任务执行者 |
| S4 | 任务权限/规则变更 → 原工单与裁决事件 → 工单详情/去重投影 → 四类原因/历史/有效期可见 → 审批+账本+管理执行者 |
| S3 | 房间唤起执行者 → 原工单+显式归属版本 → 合法卡席位或任务发起会话驻场者，均无则人本人 → 收件/回答回原执行者或needs_human → 收件+任务执行者 |

各线执行者负责其证据；协调者负责全部故事集成、真机与跨机回归。本 task 中驱动派发系统自身的实验由协调者执行，不派发。

## 顺序与可检查交付

### P0：隔离承重实验（既有部分已跑；余项随相应故事复核）

只用**测试账本、隔离 agentd/配置/HOME、隔离工作目录**，两机实验也连同一个独立测试账本；禁止生产共享账本、生产 Handoff 配置、部署或 push。真实模型判词测试可由协调者复用本机已有授权通道，单一必要认证值仅注入隔离进程且不回显；其余隔离边界见本段末尾。先确认 DSN/端口/实例标识与测试数据可清理，保存脱敏配置摘要。

- 复现真实任务由房间执行者发起后产生 permission/question 工单，追踪 task/permission/ticket/session/room/归属/席位及 seq；分别验证 push、pull(card wait)、空座/缺归属和换责任。若真实入口未保留发起关系，先提出最薄新增契约，不能从 resident 列表猜。
- 验证收到工单的短回合、等待人时持久化/退出、LaunchAdmit释放、重启/超时/重复事件。分别辨明 task 等待与协调者回合30min界限，不用 mock 证明外部CLI已暂停。
- 只为理解现存共享承载，记录真实 CLI 版本/config、launch/resume 的回合结束/取消；opencode auto/HOME 可全放行，若不产生权限事件只记录未触发，不能宣称权限链通过；本期无需新增 hostapi 自身权限握手。
- 核现行 cookie/主令牌确认来源与账本事务追加事件能力；人工同任务/规则grant 的现有实际来源链；采样 codex 环开始/结果/跳过及 help/env名原权限原文，禁止读取实际 env 值。

产物：原始脱敏事件/版本/时序/失败矩阵。归属/准入实验失败阻塞 S3；规则管理同级确认与事务审计失败阻塞规则写入；不阻塞纯 S2。真实模型判词实验可复用本机已有授权模型通道，但 Handoff 的测试 PG/agentd/HOME/工作区全部隔离；由协调者仅向隔离子进程注入所需单一认证值，不复制整个 authstore、不落盘、不入 argv、不回显密钥，不改在役服务。实验只送合成安全样本，不读取实际环境变量值。实验证据不足不能用 loopback 夹具结案。

### 线 A / U2：先交 S2（B463）

- 在真实命令输入与依赖核实后固定 AST 解析边界；修复段首 `do` 与 `$(...)` 内 `grep -c` 被误认执行包装器的成因，不写“含grep即安全”例外。遍历替换/管道/重定向全部执行节点，未知继续原门禁。
- 原样例来自 B463 #30192：`grep -c "handoff $w" README.md`；采集真实完整循环，含段首do、命令替换。对照真 `handoff` 变更、`sh -c`/eval、嵌套危险命令、未知语法；测试穿过 adapter→Authority→Gate，不只测 helper。
- 给每个审批候选环在默认Warn级别下可见记录开始、approve/escalate、解析失败、超时、跳过/停用及原因，关联请求/环/模型/耗时；codex 不执行也有明确理由。日志脱敏，不记完整环境值/凭据。
- help/env表驱动收集B461原13张help工单与B453原env名命令（原改名二进制样本为`"$TMPDIR/handoff-fc" <子命令> --help`；完整权限原文、分类、期待结果）；B461台账14/29仅首张完整载荷，缺失12张必须从原任务证据恢复或隔离重现，不伪称已具备。B453原命令同样先取原载荷，取不到列阻塞项；已知危险反例`./deploy.sh --help`不能仅因help后缀放行，`env | cut -d= -f2`读取值必须保持原门禁。env只列名称与读值严格分开，禁止把 env 全输出当列名称；保留已有非人工路径，不把所有 Consult 硬改人工。真实配置隔离运行证明当前环结果，旧agy根因不需本期复现。隔离多环链显式包含codex候选，分别制造前环approve、前环escalate与故障/超时，断言codex执行结果或跳过理由在默认Warn日志可见；新进程不设HANDOFF_LOG_LEVEL，参照hostapi wakeRoundLogLevel策略，不能把全局默认改Info。

交付检查点：S2 成功/反例、日志原文与真实调用链证据。U2仅减少误判的自指令命中，不扩静态白名单或缩黑名单/包装器门禁；跑permgate全量回归并做一次能令相关测试变红的变异。S2自身review/finish完成后可独立合入主线，不等S1/S3/S4；实际合并/推送仍须用户授权。U2 的规范化结果是线 B 规则匹配**数据/契约硬依赖**；共享文件串行另标施工排序。

### 线 B / U1：S1 + 任务侧 S4

硬依赖：U2 稳定规范化；P0 现行 cookie/主令牌确认来源与事务结果。CLI 自身权限桥无依赖，S1 不再等待独立人类在场证明。

- 实现 spec 分类表，收集完整限制集；永不可覆盖集先于规则。按完整性/AST→分类事实→不可覆盖→最新规则/有效期/撤销→条件与覆盖全集→剩余静态/模型/人工求值，所有入口同一道权威。普通push按可证明仓库/远端/ref；deploy首版规则覆盖为空；`scripts/build-deploy.sh`仅构建，按普通命令走原审批；upgrade与任何替换handoff/agentd二进制属于永不可规则覆盖的审批控制面，不通过字符串“prod”推断；未知目标不靠规则补猜。
- 最小规则包含条件、主体/范围、显式管理动作来源、时间与撤销版本，未知版本/字段 fail-closed。cookie 或 CLI 主令牌管理动作按 #33153 可确认，不要求另证人类在场；审批控制面（规则管理、handoff reply、handoff approver set/clear、handoff upgrade及任何替换handoff/agentd二进制的动作）与账本/handoff凭据不能被规则授权。
- 复用 registry CAS 当前状态，并在同一事务追加创建/撤销不可变审计；追加失败整个变更失败。裁决/命中/投递事件有 request/ticket/主体/规则版本/期限/原因/关联 seq；历史从事件投影而非 UPDATE 后快照伪造。
- 修 `Authority.FindReuse` 与 grant 写入真实转换链：规则派生来源撤销/过期即不得复用，读错也不得；人工来源的同任务同指纹复用依稿首裁决，UI/CLI显示真实语义。测试同任务、跨任务、不同主体及撤销竞争，不能只测新请求首次 Judge。
- 原工单“本任务相同请求批准”/保存规则明确分开；保存成功和原生回复失败分别可见；幂等重试不重复建规则。四类原因、缺失字段/未覆盖限制/候选故障/需人依据均可见；同请求与相关事实版本更新原待办，不反复新建升级。保留历史原因与命中/未命中版本。
- 补现有工单UI最小原型确认，接真实管理 API；CLI/UI共用校验与显式确认语义，不写客户端真相。
- 原例用安全文件 `nl -ba <fixture> | sed -n '1,20p'`：“本任务相同请求批准”不建规则；显式保存→后续命中→撤销/到期→同任务也不借规则grant复用；改变路径/机器/子命令/含危险替换不得误命中。持续至撤销须显式确认，过期边界用可控时钟。
- 故障/并发：规则读错/损坏/未知字段、CAS冲突、审计写失败、保存与投递中断、重复请求、时钟边界、撤销提交与新Judge、旧缓存/断连。至少一条穿过CLI/UI→proto JSON→agentd→approval→ledger→任务消费者，区分缺失/空/零值。

日志/注释：规则管理进入/退出、存储前后、评估原因/命中版本、复用来源校验、投递结果；说明来源信任边界、事务与已投递不追杀。日志脱敏并关联原请求。

### 线 C / U3：S3 原执行者工单收件

硬依赖 P0 归属/准入承重通过；**不硬依赖线B**；自有工单发布/镜像、投递幂等与审计记录，不依赖持久规则可信确认。P0后与U2并行。wakeconsumer/agentd组装共享文件为施工排序。

- 真实发起路径登记最薄关联与唯一审批责任归属/版本，经任务/会话公开面读写；不在 approval 域读 resident 内部。核卡席位与显式驻场责任按先完整保留resolveWakeCard自身push/自身pull/空座找push祖先，给不出接收人才发起会话驻场者、再人本人处理，不选全部房间成员。
- 未挂卡任务执行机侧发布/镜像工单，维护独立投递记录；新增“寻址系统通知”kind工单通知关联执行机/task/ticket。`cmd/session.go:565-568`现只收房间消息，因此必须补通知生产、候选解码/匹配、游标与字段处理，并接residentpush/residentwake，不声称沿用就会收到原工单。工单创建后以ticket/request+归属版本登记投递；挂卡合法push唤醒、pull待办订阅/card wait各走原契约；驻场print走会话寻址续轮，桌面只门铃+session wait，pull不起回合不等于丢件。卡空座/非法先按resolveWakeCard查push祖先，仍无接收人才回退任务发起会话驻场者，仅按优先级全部候选均不可达时可见needs_human给人，并有人工重绑/重试入口；不会无日志continue失踪。
- 等人持久化后结束短回合并释放准入；原 task 工单仍待答。重启恢复未答、重放不双通知、换责任更新通知去向，不撤合法回答权；原工单回答CAS/幂等保证只有一次原生回复生效。
- 回归卡席位 Wake 与驻场续轮两个消费者；不改 Runner 无ctx签名、不把 ctx 临时塞进其他字段。若现存取消/30min行为不能满足，先呈变更契约提案，禁止用无限占槽等待掩盖。
- 真隔离发起/工单/收件/回答链覆盖permission和question、push/pull、缺归属/空座、换席位、重启、重复、失败与超时；验证无广播/无伪task/无新增协调者自身工单。日志以ticket/room/session/归属版本/投递状态关联。

### U4：四卡分别验收与收口

协调者负责，不新增功能。在测试账本+隔离实例做两机创建/命中/撤销/到期/断连/审计/收件，实际用PG就验PG，SQLite不替代PG；单机证据不能宣称跨机完成。按触及范围跑 Go/web 检查并按 implement 三段律阶段收口；本计划不预报测试通过。S1/S2/S3/S4各有独立判决，未验项列清。记录旧needs_human #32980及孤儿会话 `ses_e988da52bffee6OP6dTT91n308`：由协调者只读核现状，经支持入口清理/说明，文档修订不能声称已关闭。

## 需冻结的外部承诺（已拍板语义）

新增跨进程面由真实消费者形成准确schema后审查；一条单独可判pass/fail，新增准确wire未完成审查不能作为后续共同基线。

1. 分类枚举包含spec五类可覆盖限制；普通push/deploy不映射成既有黑名单ID。
2. 永不可覆盖集命中不被规则改写出口。
3. 多限制请求只有全部所需条件/覆盖满足才可规则授权。
4. 未知/缺失必需条件不能自动授权。
5. 本任务相同请求批准不写规则。
6. 模型 approve 不得创建或放宽规则；创建/延长须经控制台 cookie 或 CLI 主令牌的显式规则管理动作，按现行 reply 同级确认，不额外要求可信人类在场证明（#33153）。
7. 规则保存失败不批准本次请求。
8. 保存成功、原生投递失败分别报告。
9. 规则变更与不可变审计同事务提交。
10. CAS冲突不能静默覆盖。
11. 撤销提交后开始的新裁决不使用旧规则。
12. 到期规则不授权新裁决。
13. 规则来源grant的FindReuse复核撤销与期限；人工来源沿现行复用不收紧。
14. 已投递原生动作不因撤销追杀。
15. 四类原因在wire/展示可区分且未知原因保守处理。
16. 同请求/事实版本去重不丢待办及历史。
17. 原任务工单承载回答，通知收件人按显式优先级确定，合法回答权不受其限制。
18. push唤醒与pull待办订阅保持各自契约。
19. 卡路由自身push→自身、pull→card wait、空座沿父卡找push祖先；该路由无接收人才走驻场/人回退。
20. 等人事实落盘后释放回合准入。

既有契约：`keysclient.Runner` Launch/Resume（无ctx）、`hostapi.Host.RunTurn`、proto审批上下文与registry CAS；未计划修改这些现存签名。新增分类/规则/原因/归属DTO、未知枚举处理、CLI/web手写投影与错误白名单在第一段真实wire前列全并端到端断言，不能两端各测替代。

## 风险、真机与决策权限

| 缺陷族 | 反例与责任 |
|---|---|
| 生命周期 | U1保存/投递之间崩溃不混事实；U3等待/重启/换责任不丢工单、不占槽、不双回复 |
| 静默失败 | 单环未执行与执行失败区分；无归属/存储/投递故障有可行动待办，不能报成功 |
| 跨平台 | AST输入方言与路径/进程取消行为核实际目标；未运行平台不能泛称通过 |
| 假绿 | U2真实生产组合、U1原nl例生命周期、U3真实发起/收件；夹具不证明外部暂停/恢复 |
| 门禁与TOCTOU | 永不可覆盖先行、全部管理口同级显式确认；撤销/过期穿过FindReuse；不可变审计失败阻止提交 |
| 序列化与枚举 | 所有proto/map/CLI/web转换链测缺失/空/零/未知；新分类/原因/归属枚举穿既有校验器 |
| 安全属性 | 显式确认/隔离/撤销新鲜性/一次投递各有能变红测试，review选择实际变异 |

未验证真机清单：隔离真实CLI与候选环日志、任务发起关联、原permission/question收件、push/pull、等待准入/30min边界、launch/resume共用回归、两机测试账本撤销/过期/断连、实际后端及平台。不得以单元测试替代。

执行者可选界内私有命名、测试夹具、logger与可逆拆分；不可自改范围/本期与 reply 同级的确认信任、有效期与复用/分类覆盖/责任归属/等待准入/共享契约/UI形态。失败携证据只阻塞相应线并回协调者，不绕门。每段交回首节“偏离与自主决定”，随后证据/未验/日志注释/卡记录。r1.3已依#33059拍板；r1.4已由根协调者核定，新增偏离仍须协调者审核。

图覆盖债：本checkout无 `codegraph/`，本轮按源码定位；无已有图未命中符号。文档自检不等于实现或验收通过。

## r1.3 已批准实施补充（随 r1.4 修复继续有效）

- S3执行机侧从原任务工单产生处发布/订阅，未挂卡也可可靠补发；复用传输契约但不得伪挂卡。独立投递记录以机器/task/ticket/来源seq与通知归属版本幂等，不以规则事件为开工前提。实施/集成责任仍为收件+任务线，线B受阻不阻塞S3。
- 桌面驻场目标机消费会话消息并按本机门铃；非本机`residentwake`零转交是现状，不是跨机已解决。验证目标机离线/重连补发、信未落盘重试、信已落盘但opener失败、session wait游标与重复铃，超限保留可见待办/needs_human。print驻场以显式会话寻址进入现有residentpush续轮，分别验证print/desktop，不套卡push/pull。
- 有效期以账本事务的权威UTC判定时间为准，客户端/执行机时钟不可延长授权；取不到权威时间/新鲜版本不按规则允许。用两机正负时钟偏移、到期边界、断连、续裁决测试，人工同任务相同请求批准保持现状。
- 数据归属/允许依赖以spec r1.3落位三问后的第一版表为准，首次wire前核对：approval拥有规则/期限/撤销/来源语义；orchestration+执行机store拥有原工单与回答CAS；执行机发布/ledgermirror负责事件运输；会话/通知领域拥有显式发起关联与通知投递记录；ledger持久化不解释授权。管理面经公开能力，禁止跨内部读写。

冻结补充：未挂卡工单也从执行机可靠发布/镜像；寻址系统通知关联原ticket，session wait候选/print/desktop都可消费；通知归属不改变AnswerTicketApplied的CAS及同答案幂等。

### r1.3新增原子冻结与验收

21. 审批控制面（规则管理、handoff reply、approver set/clear、handoff upgrade及任何替换handoff/agentd二进制动作）永不可被规则覆盖。
22. resolveWakeCard空座沿父卡找push祖先的原契约保持；不能将空座直接降到驻场/needs_human。
23. B435冻结26/31条按本期契约变更修订为“寻址系统通知只唤醒被寻址者，其余system/by_system/pointer系统结构行仍不唤醒”；session wait、print续轮、桌面门铃同口径，不以普通EvRoomMessage唤醒系统行。

新增通知kind从生产→proto/JSON→账本→匹配→session wait/print/desktop逐环穿过既有枚举与过滤；正例只到指定地址，反例为未寻址系统行、普通by_system/pointer、别人的通知均不唤醒。桌面仍由目标机读共享事实按本机铃，不能变为远程门铃。以上语义已同步[B435现役契约 r1.4 修订](../specs/b435-contract.md)，覆盖历史26/31与C-202/匹配表；S3 源码已接线但 I3–I7、PG 与桌面原生端未验收。事件 type 字面值 `task_ticket_notice`、票种 `gate`/`ask`；未知/非法 kind 按 v1.1 警告、跳过并推进游标。

路由实现必须区分“pull已有card wait接收人”与“确无接收人”：origin合法pull虽不启动回合且resolveWakeCard返回空，仍视为已收口，不得回退驻场；只有完整卡路由确无接收通道才继续回退。寻址系统通知无显式目标时收件为空集，不使用默认收件人。审批控制面二进制例外按真实写入/替换目标与效果识别，覆盖任何工具/路径，不限命令名upgrade。
