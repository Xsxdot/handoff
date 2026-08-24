# B239 breakdown 轮台账（2026-08-25）

卡：B239（把「认领」一分为二：归属锁 + 运行锁）；分支：cards/B239-charter-2。
本档按纪律边干边追加；每行一个已确立的事实/跑过的命令/做出的判断。

## 上游状态位核对

- [fact] spec `docs/superpowers/specs/2026-08-24-b239-claim-lock-split.md:4` 头部「状态：**已批准** —— 2026-08-24 用户批准」——开工核对一致。
- [fact] contract `docs/superpowers/specs/b239-contract.md:3-8` 头部「contract 轮冻结稿（2026-08-25）……随本提交冻结……交棒：breakdown」——开工核对一致。
- [fact] git log HEAD=c7565808「docs(b239): freeze claim lock split contract」，即 contract 冻结提交。

## 图与子系统

- [fact] `codegraph/best.json` 顶层域（parent 为空）12 个：d_orchestration/d_gateway/d_workspace/d_execution/d_sessions/d_transport/d_protocol/d_ledger/d_cli/d_web/d_policy/d_maintenance。**无 d_coordination 域**——spec/contract 行文里的「d_coordination（internal/agentd 与 cmd）」在图上分属 d_gateway（控制门面，type=boundary）与 d_cli（协调者命令面，type=logic）。有图以图为准 → 本拆解子系统 id 用 d_ledger / d_gateway / d_cli。此为边界澄清，需回写契约修订记录。
- [fact] best.json 类型读数：d_ledger=logic、d_gateway=boundary、d_cli=logic。
- [fact] `codegraph/target.json` contracts：`d_gateway→d_ledger` entries=["ledger.Store","ledgerstep.StepRunner"]、`d_cli→d_ledger` entries=["ledger.Store"]，与契约 §6 冻结面一致。
- [fact] `codegraph/diffs/cards-B239-charter.json` 记录 Ticket 0 六新符号（RunLock 模型 + 五 Store 方法）+ StepRunner 字段变更（Session 收窄、RunHolder 新增），base=84af7380。

## 现状代码逐条复核（对照契约 §1.1 表）

- [fact] `internal/ledger/runlock.go` Ticket 0 空壳在：RunLock 类型 :15-21、五方法签名 :26/:32/:38/:44/:49，方法体直返零值。
- [fact] `internal/ledgerstep/runner.go:32` RunHolder 字段已在；:26-28 Session 注释已收窄为归属身份。
- [fact] `internal/ledgerstep/runner.go:63-67` nodeFor 失败直接 return（无落卡）；`:92-95` Session 空直接 return；`:97` ClaimDriver；`:102-108` defer ReleaseCard；`:80-84` 纯人工列提前返回不认领。契约引用的行号 59-63/88-92/93/98-104 有 ±4 行漂移，符号全部命中。
- [fact] `internal/ledger/move.go:145` ClaimCard(id,to,expect,session) 四参签名；`:155` 经 moveCardTx 转状态；`:158-159` 写 driver_session+driver_heartbeat_at。
- [fact] `internal/ledger/move.go:26-35` moveCardTx 的终态拒绝（ErrBadState）住在转移路径里——解耦后 ClaimCard 必须显式补回这层拒绝（契约 §2.1 规则 2 的现状依据，已亲读确认）。
- [fact] moveCardTx 第 4 参是事件 actor；今天 ClaimCard 把 session（带 pid）当 actor 传进去，认领的状态转移事件署名带 pid——解耦后该通路整体消失。
- [fact] `internal/ledger/move.go:175-201` ReleaseCard：UPDATE 带 `AND driver_session = ?`，0 行时记日志返回 nil（:188-191 静默 no-op），doc 注释 :170 明写「非持有者调用是无操作而不是报错」。
- [fact] `internal/ledger/tasks.go:94-117` ClaimDriver；`:122-145` TakeoverCard 无条件覆盖 + EvDriverTakeover payload{from,to}。
- [fact] 生产代码 ClaimDriver/ReleaseCard/ClaimCard/TakeoverCard 全部调用点（grep 非测试）：runner.go:97/:103、card_driver.go:26/:50、card_dispatch.go:213/:240。无其他生产消费者。
- [fact] 测试触及面（grep *_test.go 含上述符号或 AcquireRunLock）：internal/ledger/move_test.go、tasks_test.go、internal/ledgerstep/runner_test.go。
- [fact] `cmd/ledgercli.go:47-54` ledgerActor()=`cli:<user>@<host>`；`:60-62` ledgerSession()=`<actor>#<pid>`。
- [fact] `cmd/card_driver.go:17` release 用 ledgerSession()；`:41` takeover 用 ledgerSession()+ledgerActor()；`:31` 成功打印 {"ok":true}（release 非持有者今天也走到这里=假成功）。
- [fact] `cmd/card_dispatch.go:208-210` 守卫判 Status==StatusDoing；`:213` ClaimCard(id, StatusDoing, card.Status, ledgerSession())；`:236-242` 失败回滚 MoveCard 回原列 + ReleaseCard。
- [fact] `cmd/card_node.go` --step 路径把 Actor 发给 agentd（契约引 :40；本轮未逐字复读该行，符号 CardStepReq.Actor 在 internal/proto/cardstep.go:23 确认存在）。【补记：见下方 08-25 二次核】
- [fact] `internal/agentd/cardstep.go:45-46` 生产唯一 StepRunner 构造点（grep 非测试仅此一处），Session=req.Actor；RunHolder 尚未装配（Ticket 0 只加字段）；`:59-62` go func 后台编排；`:80-100` 在飞 map。
- [fact] `internal/agentd/ledgerapi.go:186-196` 徽标判据 `view.Status == ledger.StatusDoing` + LatestTaskStates 最新 failed；`:446` fallback `req.Actor = "web:" + r.RemoteAddr`（带端口）。
- [fact] `internal/agentd/ledgerapi.go` 其余 `web:`+RemoteAddr 出现于 :89/:367/:386/:522/:542/:693——都是事件 actor 审计署名，不是锁身份；契约只收窄 :446 一处，范围冻结不动其余。
- [fact] `internal/ledger/store.go:40-43` now 可注入时钟字段；`:45-48` timeNow() 回退 time.Now；PG DDL card_run_locks 在 :252-256、SQLite 在 :318-322（PK card_id REFERENCES cards(id)，两方言同构）——契约写的 250-253/316-319 有 2~3 行漂移，表结构事实一致。
- [fact] `internal/ledger/events.go:163` AddComment(cardID,body,kind,actor)、`:264-277` MarkNeedsHuman(cardID,reason,actor)（reason 必填，空串显式报错）——落卡三件套既有形状确认。
- [fact] `cmd/card_wait.go:98` st.Follow(...) 只跟事件流——「判据是 card wait 看得见」的通道依据。
- [fact] 项目缺陷族清单在 docs/superpowers/specs/2026-08-21-handoff-instantiation-checklist.md §3：通用五族 + 序列化边界 + 枚举白名单两条设问 + webview 第六族候选（触及 Web 且涉及浏览器 API 才必答）。§5.1 补判据：单包 ≥40 文件且无子包须显式回答能否圈出有界文件集（internal/agentd 61 文件命中）。

## 判断与放弃

- [判断] 本轮为 charter 流 breakdown 节点：产出物只有 docs/superpowers/specs/b239-breakdown.md；红线=不写实现代码、不建卡、不派发。轻档（spec 选档复核冻死）→ 子卡按单轮 implement 的序贯单元拆，不做跨执行器扇出。
- [放弃] 不调 handoff CLI 写命令；只允许只读 graph 子命令。charter v9 states 读数维持契约 §1.4 的「引上游读数」标注，不复验。
- [fact]【补记】cmd/card_node.go:40 亲读确认 `Actor: ledgerSession()`；ledgerSession() 生产调用点全集=card_dispatch.go:213/217/240、card_driver.go:17/41、card_node.go:40，与契约 §2.1 名单一致；另有 cmd/card_dispatch_test.go:460-461 断言 wire actor==ledgerSession()，实现轮须随迁（入 U4 有界文件集）。
- [fact] 生产代码 StepRunner{} 构造点全集=internal/agentd/cardstep.go:45（grep 非测试唯一）；测试侧 internal/ledgerstep/runner_test.go。
- [fact] moveCardTx 第 4 参是事件 actor 且不触碰 driver_session；今天 ClaimCard 传 session 进去 → 认领的状态转移事件署名带 pid；解耦后该通路整体消失（move.go:26-72 亲读）。
- [fact] events.go AddComment :163、MarkNeedsHuman :264-277（reason 必填空串显式报错）；card_wait.go:98 st.Follow 只跟事件流。
- [fact] store.go PG card_run_locks DDL :252-256、SQLite :318-322（PK card_id REFERENCES cards(id)，两方言同构）；now 注入字段 :40-43、timeNow :45-48。契约引用行号漂移 2~3 行，结构事实一致。
- [判断] 契约路由为 contract → breakdown →（单轮）implement，无 plan 节点：契约中「归 plan」的三处（TTL 常量位置、RunHolder 精确构造、续租 gate 达成机制）由本拆解钉定或列为待拍板岔口，不由实现自由发挥。

## 出稿与自检

- [fact] 拆解稿写入 docs/superpowers/specs/b239-breakdown.md：待拍板岔口 3 条（徽标取数落点 / RunHolder 构造形态 / 续租循环驱动源）、子卡 U0–U4 五张四段式 + 收尾核验、缺陷族逐族作答、真机清单 7 条。
- [cmd] handoff graph resolve --doc docs/superpowers/specs/b239-breakdown.md → exit 0；anchors=12：ok×10、moved×2（StepRunner.Run→runner.go:60、ledgerSession→ledgercli.go:60，均为再锚定非坏锚）、坏锚 0。首次运行 anchors=0 是因为符号锚未加反引号/用了短形，已修。
- [判断] 契约文档回写边界澄清修订记录（§二澄清一、二），结论均不退回 contract。
