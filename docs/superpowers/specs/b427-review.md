# B427 独立 review

## 第一轮：代码与自动化阶段（2026-10-07）

- 审对象：`babd141e..e3a969e6`；spec r1、plan p1。
- 审阅者：独立新上下文 `b427_review`，只读回合；协调者转录结果。
- 目标轴暂不通过；架构边界通过；2 项 Important 未核销。手机性能未验。

| 轴 | 维度 | 裁决与证据 |
| --- | --- | --- |
| 目标 | 故事与验收覆盖 | S1–S4 请求与消费路径成立；驾驶会话时序 R1、S5 可见过期 R2 欠缺；spec:38/50 手机仍未验。 |
| 目标 | 原型/流程基准差异 | 对照 B426 实际基线，chips、头部、抽屉筛选和队列保留；审阅者完整 CardsPage.test.tsx 40/40 通过；驾驶会话 R1。 |
| 目标 | Scope drift | 无未经授权新增；欠交 R1/R2、手机验证。无 SQL、端点、持久缓存或通用框架。rooms 仅继承类型与 fixtures 修正。 |
| 架构与证据 | 架构法与触及路径 | 通过第四/八/九条。Shell:1184 组装 PollState，CardsPage:183 关闭替代本地流，Drawer:364 复用详情响应，无新增父请求。graph check fails=[]，未重扫图。 |
| 架构与证据 | 测试有牙 | 审阅者 B427+ready 双跳15/15；核读变异编译绿、3红7绿、恢复10绿；核读协调者全量1731绿。R1原始红；R2原断言仅停表/留快照。 |
| 架构与证据 | 日志与注释 | 通过：来源、详情与钉版本 start/loaded/error 含状态码；CardsPage:165/335、CardDrawer:359 解释缓存与迟到守卫。 |
| 架构与证据 | 序列化边界 | 无新 wire 字段；既有 ledger API:318/371。 |
| 架构与证据 | 适用契约 | 既有 API 符合，无新增冻结对象。内部 props 组装；同源 API 无 target selector，当前所有者 lifetime 不需要泛化 source key。 |

### Findings

- **R1 Important，待修**：详情按 ID 提前呈现（CardsPage:358），Drawer:1047 按 driver_session 提供可点按钮，Shell:565 却把 sessions 首拉未知投影为 [] 并 missing 返回。原始隔离日志 `/private/tmp/b427-shell-session-isolated.log` 有 for_card_missing/无跳转；当前 ready 态断言不覆盖此边界。建议既有 readiness→disabled/可理解提示，无新请求/队列/计时器，补延迟首拉、ready 点击及返回语境。已落卡 seq22755。
- **R2 Important，待修**：usePoll:96 在401停表、保留data；CardsPage:449/460 不消费 sessionExpired，首拉401永久加载、快照401不明示过期。属基线消费缺口但违反本轮 S5。建议复用 SessionExpiredBanner，过期优先于首拉loading，真实 Shell DOM 覆盖首拉与快照后401。已落卡 seq22757。
- **M1 Minor，待修/不阻塞**：B426 helper 未独立设置 fetchCardDetail/fetchFlow Promise fixtures。审阅者 `CardsPage.test.tsx -t B426` 5 failed/4 passed、完整文件40/40通过；属于用例顺序依赖，非生产回归证据。

无 Critical。后续修复必须以新提交重新独立审阅，不以第一轮绿灯核销 findings。

## 第二轮：代码与自动化阶段通过（2026-10-07）

- 审对象：`77292b7e`，修复差异 `e3a969e6..77292b7e`；spec r1、plan p1。独立审阅者只读，协调者转录。
- **目标轴及架构/证据轴通过；R1/R2/M1 核销，无新 Critical/Important。真实手机性能未验，不能归档卡。**

| 轴 | 维度 | 裁决与证据 |
| --- | --- | --- |
| 目标 | 故事与验收覆盖 | 代码/自动化通过，S1–S5保留；Shell.test:2422/2454覆盖会话延迟/失败首拉和两类401。spec:50手机仍欠项。 |
| 目标 | 原型/流程基准差异 | B426基线对照，Drawer:1050行保留/disabled/尚未就绪；Shell.test:2440 ready后导航及返回卡。B426隔离9/9通过。 |
| 目标 | Scope drift | 无。Shell:565/CardsPage:212仅既有readiness/过期消费与fixtures，无新请求/队列/计时器/端点。 |
| 架构与证据 | 架构法与触及路径 | 通过。readiness归Shell，props经CardsPage传Drawer；PollState/Banner复用，无权威状态复制。Shell:1196、CardsPage:450。亲跑graph fails=[]、继承warn、未重扫。 |
| 架构与证据 | 测试有牙 | 审阅者亲跑B427+既有ready双跳23/23、B426隔离9/9。核读共享注入变异compile绿、真实DOM expected1/got2红、恢复绿；核读协调者全量141files/1739tests绿。 |
| 架构与证据 | 日志与注释 | 通过；Shell:564未知/过期guard与结构化not_ready日志，保留来源/详情/工作流日志。 |
| 架构与证据 | 序列化边界 | 无新wire字段，仅Drawer props:284内部seam。 |
| 架构与证据 | 适用契约 | 既有符合，无新冻结对象；CardsPage props:143落实原导航接缝，不改后端API或缓存口径。 |

- R1 Important **已核销**：入口与callback守卫；deferred/失败首拉不可执行，ready后恢复且返回卡语境成立。
- R2 Important **已核销**：卡/决策/任务过期均明示；首拉不永久loading，旧快照注明尚未确认；CardsPage:450、performance.test:143与真实Shell两态401。
- M1 Minor **已核销**：helpers:533/689完整Promise fixtures，B426过滤单跑9/9通过。
- 依据日志：`/private/tmp/b427-final-full-web.log`、`b427-shared-mutation-red.log`、`b427-shared-mutation-green.log`及ledger原始红绿记录。所有验证针对77292b7e源码；后续仅追加本文档。
