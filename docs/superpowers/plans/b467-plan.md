# B467 实现计划：忽略账号、弹层月额度不换行、底栏去名、去「使用详情」

- 状态：**r1 已拍板（2026-10-23）**：主会话裁决 Q1–Q13 全部采纳推荐；Q14 换版届时由用户点头；Q15 采纳（测试库按全局规范经脚本建，不打印凭据）；SC1、SC2 已回写 spec r1.1。
- 输入：`docs/superpowers/specs/b467.md` **r1 已批准**（岔口①「忽略」取代界面「删除」、② 载体登录下拉隐藏已忽略账号）。L3 未扇出、单卡；执行形态丙（plan 本会话、implement 派 runner 小队按段派发、review 本会话 subagent、真机与浏览器验收主会话）。
- 基线：分支 `cards/B467-charter-1` @ `0c71baef`（含 main `8cac2bcc`；生产全队 `50333816`）。
- 原型（形态基准，不入库）：`prototypes/b467-accounts/`。与 `prototypes/base/` 的差异即本卡形态：`pages/accounts.html`（卡上「忽略」、页底「已忽略」折叠区与恢复、拒绝/确认文案）、`shared/accounts.js`（`ignored`/`ignore`/`restore`、底栏段 `title` 无 `.sb-name`、弹层去「使用详情」）、`shared/styles.css`（`.up-main` 376→392px）。
- 台账：`docs/superpowers/specs/b467-ledger.md`「## plan」节是本稿全部读数（文件:行）与放弃方案的原始记录。
- 读者：零上下文执行者。故事编号沿用 spec S1–S10。标「**未验证，需真机**」的结论不得写成测试夹具，汇总见 §8.3。

---

## 待拍板（出稿者推荐，主会话裁决）

> **裁决（2026-10-23 主会话）**：Q1–Q13 **采纳推荐**；Q14 **采纳**，S10 前全队换版需用户点头；Q15 **采纳**（端口起前核空闲）。

每条：**荐** + 理由（+ 被否方案）。标「**需用户**」的必须回人，其余主会话可定。

**数据与接缝**

1. **Q1 「已忽略」存在哪里（spec 点名的对策题）。荐：独立 registry kind `account_ignore`，id = 账号 id，body `{at, by}`；行在 = 已忽略，恢复 = 删行。账号定义行（`kind=account`）body 一个字节不改。**
   - 事实：账号定义行有三个写者，全部是「读整行 → 改一处 → 整行写回」：人编辑 `Service.Put`（`accounts.go:68-103`，`next := cur`）、发现补 label `backfillLabel`（`discover.go:230-258`，`next := cur`）、发现新建 `ensureAccount`（`discover.go:194-225`，expect=0）。生产 `50333816` 的 `Account` 结构（`types.go:53-65`）不认识新字段，`json.Unmarshal` 丢掉、`json.Marshal` 写回即抹掉 → 悄悄恢复。registry 是通用 KV、不解释 body、无 kind 白名单（`internal/ledger/registry.go:1-6,31-77`；B464 加 `account_login`/`discovery_run` 时也没动账本，`types.go:20-25`）。
   - 理由：旧二进制根本不认识 `account_ignore` 这个 kind，不会读也不会写它——「旧版本改写定义行丢字段」这条失败路径在结构上不存在，不靠换版时序兜底；发现（Reconcile）也从不碰它，「发现与忽略竞态」同样结构性消失。代价：读面多一次 `List(account_ignore)`（行数 = 被忽略的账号数，个位数）。
   - 被否 (a)：字段放进 `Account` + 「全队同批换版」硬要求。新二进制里所有测试全绿，只有混版本窗口里旧机改名/补 label 时无声丢失，没有任何测试能变红（见 §11 拍板记录）；且「同批」在三台机器上做不到原子（升级是逐台的）。
   - 被否 (b)：字段放进登录行。忽略是账号级（spec 永不做：登录级忽略），且登录行由发现频繁合并写。
   - 仍需全队换版的部分（非数据安全，是行为完整）：旧二进制看不到忽略标记 → 旧控制台照常显示它、旧机探测器照常探它。S3/S10 的验收前提是三台都换上本版（Q14）。
   - 与 spec 字面差异：spec「账号定义记『已忽略』及时间、操作机器」→ 实为「与账号定义同 id 的独立行」，仍归账号领域、仍只由账号域写（落位①不变）。见 SC1。

2. **Q2 忽略/恢复走哪个写入口。荐：专用子资源 `PUT /api/accounts/{ref}/ignore`（忽略）与 `DELETE /api/accounts/{ref}/ignore`（恢复），无请求体、无 `expect`、幂等，成功都回 `200 AccountView`。**
   - 理由：Q1 下忽略写的不是账号定义行，借 `PUT /api/accounts/{ref}?expect=` 会把「账号行版本」与「忽略行」两种 CAS 混在一个入口；幂等语义下 `expect` 没有保护对象（重复忽略 = 无变化）。行在共享账本，任一机都可写、不转发（同 `PUT /api/logins/{id}/proxy` 先例，`account_api.go:73-114`）。
   - 被否：`AccountInput` 加 `ignored` 布尔（要求 expect、与改名同体，「缺席 vs false」又多一处要分辨）。

3. **Q3 GET 投影的字段形状。荐：`AccountView` 增可缺席键 `ignored: {"at": RFC3339, "by": "<机器名>"}`；缺席 ⇔ 未忽略，没有 `false` 形态。** 一个键承载「是否 + 何时 + 谁」，前端判据唯一（`a.ignored !== undefined`），不存在「缺席与 false」两种「未忽略」写法。被否：`ignored` 布尔 + `ignored_at`/`ignored_by` 三键（三键可不一致）。

4. **Q4 不加新状态值。荐：`state` 词表不变（仍是额度推导 ok|low|out|fail|none|na），已忽略只由 `ignored` 键表达；CLI 表格状态列遇 `ignored` 改印「已忽略」（spec S6）。** 理由：`state` 流经 `Blocks()`、前端 `byProblem`/`statusPhrase`/`dispatchHint`、CLI `accountStateLabel` 等多处 switch（缺陷族「枚举新值过既有白名单」），新值要逐处登记，收益为零。已忽略账号的 `probe_login` 缺席；`state` 仍按冻结的旧读数推导（不改写成「探测失败 · 没有可用登录」）。

5. **Q5 调度准入遇到「载体所选登录属于已忽略账号」怎么判。荐：按「未选账号登录」放行 + Warn（与 `admission.go:99-100,111-114,125-130`「登录或账号已删 → 放行」同一先例）。** 这个状态只会经两条窄路出现：忽略与载体保存并发（TOCTOU，§10）、或旧二进制在混版本窗口里保存了载体。已忽略账号不再探测，读数冻结——继续按冻结读数判，会让一个冻在「低额度」的账号永久挡住载体；拒绝派发则把一个两端都已拦过的边角态变成派发故障。被否：照冻结读数判；判不可用。

6. **Q6 载体保存校验的范围。荐：只要所选登录的账号已忽略就拒（含「登录没变、只改模型/并发」的编辑），400，文案「账号 <名> 已忽略，先在账号页恢复」。** 理由：spec S5 只说「选了已忽略账号的登录 → 拒」；「未改登录也拒」逼人在编辑时就把边角态（Q5）修掉，规则一句话说得清。`PutCarrier` 只有人经 `PUT /api/squads/carriers/{name}` 一个调用方（`schedapi.go:144`），不会误伤系统内部写。被否：只在登录变化时校验（规则多一条分支，边角态长期留存）。

7. **Q7 存量载体自动迁移（B464 §5）跳过已忽略账号的登录。荐（必须做，门禁绕过族）：** `writeMigration` 不经 `PutCarrier`（`migrate.go:142-166`），Q6 的校验管不到它；而它每轮发现后都会跑（`cmd/agentd.go:878-884`）。规则：同 HOME 兼容登录里若有属于已忽略账号的，先剔除；剔除后恰一条 → 规则 1；多条 → 规则 2；**剔除后为零且剔除前非零 → 不写（留作未选，原因「同 HOME 的兼容登录属已忽略账号」）**，不落到规则 3 的 no_account——人忽略一个账号不等于替人选了「无账号」。

8. **Q8 `DELETE /api/accounts/{ref}`（API 保留、界面无入口）对忽略行的处置。荐：不连带删忽略行。** 账号 id 由身份确定性导出（B464 L1），删除后下一轮发现以同一 id 重建——忽略行仍在，账号重建出来即是已忽略，与「忽略是对这个身份的决定」一致。读面只给存在的账号挂忽略标记，孤儿忽略行不展示。被否：连带删（删除一次就把忽略也抹了，又回到第 1 条反馈）。

**前端**

9. **Q9 前端过滤点。荐：在 Shell 级唯一数据源 `store.ts` 入口处分区——快照 `accounts` 只含未忽略账号，新增 `ignored` 列表。** 底栏、使用量弹层、「刷新所有账号」、移动端节、载体登录下拉、账号页分组全部读 `accounts`，不改一行就排除了已忽略；只有账号页「已忽略」区读 `ignored`。理由：spec 列了五处消费点（缺陷族「前端各处过滤遗漏」），逐处加 filter 漏一处就是 bug；分区在入口一处。被否：`model.ts` 提供 `visibleAccounts()` 让各处调用。

10. **Q10 弹层三窗口同行的改法。荐：`.up-main` 宽 376→392px（原型取值，已经浏览器核验 offsetTop 同为 121）。** 读数：内容区 329px，三窗口最小 3×102 + 2×14 = 334px（`accounts.css:71,85,91,96-98`）。392 → 内容区 345px，余 11px。被否：收 `.q-inline` 间距 14→11（余 1px，字体回退或「5h」标签宽 1px 就再换行，且 `.q-inline` 被移动端与账号页复用）。

11. **Q11 「已忽略」区的行内容。荐：照已确认原型——名字、身份、N 条登录、机器、「X 前忽略」、「恢复」；不逐条列登录状态。** spec S2 后半「『已忽略』区里它的登录状态照常更新」的验收改由 `handoff account list`（已忽略账号照列、其下登录行带状态）与 API `logins[].state/changed_at` 观察。若主会话认为页面必须可见登录状态，回 spec/原型加形态，不由执行者自加。

12. **Q12 新增登录向导识别结果落在已忽略账号上。荐：结果行「已挂到账号 X」后追加「（已忽略，可在本页底部恢复）」**（前端按 `ignored` 列表判，`LoginWizard.tsx:216-218`），否则用户登录完找不到账号。A12 响应不改。被否：不提示。

**分段与环境**

13. **Q13 两段串行。荐：段 A（后端 + wire + CLI + 契约链路）先完成并合入卡分支，段 B（前端组件）以其为基线再派。** 段 B 对段 A 是契约硬依赖（`web/src/api/accounts.ts` 的类型与 `ignoreAccount/restoreAccount` 由段 A 提供，`testdata/AccountsResp.json` 由段 A 的 Go fixture 生成）。被否：按冻结条目并行（两段同改 `web/src/api/` 的冲突与回归面不值得省的那一轮时间）。

14. **Q14 S10 生产验收的换版（需用户）。** 合入推送后三台（linux-01、mac-mini、macbook-pro）全部换到本版，才做 S10（旧机探测器不认忽略，会继续探 opencode-go-54ce）。Q1 方案下不要求「同批原子」，逐台升级期间不丢数据；换版命令沿用现行做法（`build-deploy.sh --pack` + `handoff upgrade --now --from`），换版本身需用户点头。

15. **Q15 真机环境（需告知用户）。荐：** 新测试库 `handoff_b467`（linux-01 PG，主会话按全局 CLAUDE.md §4 用脚本建、凭据只经脚本 → 配置，不打印，登记「已分配」）；mac-mini 上起隔离实例 `t467-mini`（分支二进制，独立 datadir/端口/配置，端口起前核空闲，荐 7797）；混版本检查另起 `t467-old`（`50333816` 二进制，同测试库，荐 7798）。隔离实例的发现**只读扫描**真实主 HOME 与 `~/.handoff/home/*`（读身份、不改任何文件）；探测会以真实凭据调各家只读 usage 接口（B464 同款）。在役 7777 全程不动。

## spec 更正（交主会话，plan 不改 spec）

| 编号 | spec 原文位置 | 事实 | 影响 |
|---|---|---|---|
| SC1 | 方案①「账号定义记『已忽略』及时间、操作机器」；实现决定「加字段不改 DDL」 | Q1 荐：记在与账号定义同 id 的独立 registry 行 `account_ignore`，账号定义行不改 | 若采纳 Q1，spec 该句改为「账号领域记一条与账号同 id 的『已忽略』记录」；仍不改 DDL、不升 `ledgerSchemaVersion` |
| SC2 | S10「opencode-54ce」 | 生产账本里的名字是 `opencode-go-54ce`（2026-10-23 `handoff account list`：opencode-go、state fail、2 条登录在 linux-01/macbook-pro、无载体引用） | S10 操作对象按实名 |

---

## 0. 交付目标

| 故事 | 本轮做到 |
|---|---|
| S1 忽略 | 账号页卡上「忽略」取代「删除」；确认框；被引用拒绝点名载体 + 链到「设置 · 自动化」；已忽略账号从分组、底栏、弹层、移动端、载体下拉消失；页底「已忽略（N）」 |
| S2 不回来 | 发现照常扫、照常更新登录行，从不写忽略行；删除后重建仍是已忽略（Q8） |
| S3 不探测 | 定时扫描跳过；立即探测（页面、CLI、API、转发链）409 并说明已忽略 |
| S4 恢复 | 「已忽略」区「恢复」→ 删忽略行 → 回原分组；下一轮扫描即到期探测 |
| S5 载体保存拒绝 | `PutCarrier` 拒（Q6）；存量迁移不选（Q7）；准入边角态放行（Q5） |
| S6 CLI | `account list` 照列、状态列「已忽略」；`--json` 带 `ignored` |
| S7 弹层 | `.up-main` 392px，三窗口同行 |
| S8 底栏 | 段内去名字，`title` = 名字 |
| S9 弹层菜单 | 只留「管理账号…」 |
| S10 生产 | 全队换版后由主会话陪用户做（§8.3 R9） |

**留待后续（finish 落 roadmap）**：spec OOS 两条（opencode-go key 与 OAuth 归并、CLI 忽略/恢复子命令）。

## 1. 触及子系统与类型

| 子系统 / 领域 | 类型 | 本轮角色 |
|---|---|---|
| 编制域·账号领域 `internal/scheduling/accounts` | 逻辑型 | 忽略行的读写（Ignore/Restore，引用拒绝复用删除同一份载体引用查询）、读面挂 `Ignored`、已忽略不选可探登录 |
| 编制域 `internal/scheduling` | 逻辑型 | 载体保存拒绝（Q6）、存量迁移剔除（Q7）、准入放行（Q5） |
| agentd 账号工作 `internal/agentd/accountwork` | 逻辑型 | 探测器扫描跳过、ProbeNow 拒绝 |
| agentd 网关 `internal/agentd`（account_api.go） | 逻辑型；对外 HTTP 面 | 两条新路由、立即探测 409、`accountView` 投影 `ignored` |
| wire `internal/proto` + 契约 fixture + `web/src/api` | 逻辑型（契约面） | §11 条目 |
| CLI `cmd/account.go` | 逻辑型 | 表格状态列「已忽略」 |
| 控制台 `web/src/app/accounts`、`web/src/app/settings/SchedulingPage.tsx` | 逻辑型（对面是浏览器，形态验收走真机浏览器） | 数据源分区、忽略对话框、已忽略区、底栏去名、弹层宽度与菜单 |

派卡资格：本卡不扇出（spec 定 L3 未扇出、单卡按段派发），各段文件集见 §6，可一条路径规则写出。架构法第三条读数：`internal/scheduling/accounts` 现 9 个非测试源文件，本卡 +1（`ignore.go`）；`internal/agentd` 不新增源文件（路由与投影落在既有 `account_api.go`）——不触发升格必答题。

## 2. 故事映射与行为闭环

### 2.1 故事 → 实现责任 → 真实消费者 → 集成/回归负责人

| 故事 | 实现责任（段） | 真实消费者 | 集成/回归负责人 |
|---|---|---|---|
| S1 | 账号域 Ignore + 引用拒绝（A）；API 路由（A）；忽略对话框、已忽略区、store 分区（B） | 浏览器账号页；底栏/弹层/移动端/载体下拉经 store | 段 A 执行者（API 竖切测试）；段 B 执行者（组件测试）；主会话浏览器 R4/R7 |
| S2 | 账号域：Reconcile/ensureAccount/backfillLabel 不写忽略行；读面按 id 挂（A） | 各机发现器 → 账本 → GET | 段 A（账号域单测）；主会话 R1/R8 |
| S3 | 读面不选可探登录（A）；探测器 scan/ProbeNow（A）；API probe 409（A） | 探测器节拍、`account probe`、页面立即探测（已忽略账号界面上不出现该按钮） | 段 A（真实 Prober + 内存账本）；主会话 R2 |
| S4 | Restore（A）；恢复按钮（B） | 浏览器；探测器下一轮 | 段 A/B；主会话 R3 |
| S5 | `checkCarrierLogin`、迁移、准入（A） | `PUT /api/squads/carriers/{name}`、每轮发现后的迁移、派发准入 | 段 A（scheduling 单测 + API 竖切）；主会话 R5 |
| S6 | `renderAccountTable` 状态列；`--json` 继承投影（A） | 主会话/skill/协调者读 CLI | 段 A（cmd 单测）；主会话 R6 |
| S7 | CSS 宽度（B） | 浏览器 | 主会话浏览器 R7（jsdom 量不到布局，见 §10 假绿族） |
| S8 | StatusBar 段结构（B） | 浏览器 | 段 B（组件测试断言段内无名字文本、title 为名字）；R7 |
| S9 | StatusBar 菜单（B） | 浏览器 | 段 B；R7 |
| S10 | — | 生产 | 主会话 R9（换版需用户点头） |

### 2.2 行为闭环（触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任）

| # | 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 实现责任 |
|---|---|---|---|---|---|
| V1 | 用户点「忽略」→ 确认 | `account_ignore/<账号 id>` 行（`{at,by}`），写前经载体引用查询 | GET 投影 `ignored` → store 分区 | 分组里消失、「已忽略（N）」+1；底栏/弹层/移动端/下拉无它 | 段 A Ignore + 路由；段 B 对话框 + 分区 |
| V2 | 同上，但有载体引用 | 载体行 `login`（经 `LoginReferrers`，`admission.go:174`） | API 409 `{error, carriers}` | 对话框「不能忽略 X」点名载体、链到 `/settings?section=automation` | 段 A 拒绝；段 B 对话框 |
| V3 | 发现节拍 / 「立即发现」 | 登录行（照常合并写）；忽略行不被触碰 | GET | 已忽略账号不回到分组；`account list` 里其登录状态随实况变 | 段 A（Reconcile 不变 + 单测锁） |
| V4 | 探测器 30s 扫描 | 读面 `Ignored` 非空 → 无可探登录 | 探测器 | 读数行 `last_attempt_at` 不再前进；日志无该账号的探测写入 | 段 A prober |
| V5 | `handoff account probe <名>` / API probe | 同上 | API handler | 409，文案含「已忽略」；CLI 退出 1 | 段 A handler |
| V6 | 用户点「恢复」 | 删 `account_ignore/<id>` | GET；探测器 | 回原分组；≤ 一个扫描周期后被探测（`last_attempt_at` 前进） | 段 A Restore；段 B 按钮 |
| V7 | 经 API/CLI 保存载体选了已忽略账号的登录 | 忽略行 | `PutCarrier` | 400「账号 X 已忽略，先在账号页恢复」，载体行不变 | 段 A scheduling |
| V8 | 发现后存量迁移 | 忽略行 | `MigrateCarrierLogins` | 不把未选载体迁到已忽略账号的登录 | 段 A migrate |
| V9 | 用户看底栏 / 打开弹层 | — | StatusBar | 段内只有图标 + 读数、悬停见名；三窗口同行；菜单仅「管理账号…」 | 段 B |

## 3. 现状事实（基线 0c71baef，执行者开工前不必重核；台账 §plan 有原始读数）

**账号域**
- 账号定义：`accounts.Account`（`internal/scheduling/accounts/types.go:53-65`）以 JSON 存 registry `kind=account`（`types.go:21`），id 不进 body；四类 kind 常量 `types.go:20-25`。
- 定义行三个写者：`Service.Put`（`accounts.go:68-103`，读现值 `next := cur` 后整行 CAS）、发现新建 `ensureAccount`（`discover.go:194-225`，expect=0 竞争即归并）、发现补 label `backfillLabel`（`discover.go:230-258`，只在现值无 label 时整行 CAS，冲突跳过）。删除 `Delete`（`delete.go:61-98`）：名下登录被引用 → `*ReferencedError`；否则删定义行 + 登录行 + 读数行。
- 读面：`List`（`accounts.go:246-289`）/`Get`（`:217-243`）→ `newView`（`:294-325`）挂登录、引用载体、可探登录；支持探测但无可探登录时就地改成 `fail` + `no_login`（`:322-324`）。`ProbeLogin` 选择 `:350-366`。`Assess`（`:592-606`）给准入用，不查载体引用。
- 引用查询：账号域经 `SetReferrers`（`delete.go:37`）注入编制域 `LoginReferrers`（`admission.go:174`），组装在 `cmd/agentd.go:850-854`。
- registry 无 kind 白名单、不解释 body（`internal/ledger/registry.go:1-6,31-77`；门面 `internal/ledger/api/registry.go:29-58` 透传）。

**API 与 wire**
- 路由 `account_api.go:64-71`：GET/PUT/DELETE `/api/accounts/{ref}`、POST `/{ref}/probe`、PUT `/api/logins/{id}/proxy`。`AccountsClient` 方法集 `:42-52`（注释写明「多一个即越界」——本卡加两个，注释同步）。
- PUT 可改字段只有 `name`/`interval_minutes`/`warn_percent`/`low_percent`（`proto/scheduling.go:371-376`、`accountFromInput` `account_api.go:260-275`）。
- 唯一手写投影 `accountView`（`account_api.go:379-411`）→ `proto.AccountView`（`proto/scheduling.go:211-234`）。删除拒绝体 `proto.AccountDeleteRefusal{error,carriers}`（`:342-345`）。
- 立即探测 handler `account_api.go:284-375`：先判 na（`:296-301`），再找可探登录所在机（`:302-314`），本机交 `ProbeNow`，他机一跳转发。
- 契约 fixture：`internal/proto/contract_fixture_test.go:71,1004-` 生成 `web/src/api/testdata/AccountsResp.json`（`go test ./internal/proto -run <fixture 测试名> -update`，开关 `:36`），前端 `web/src/api/contract.test.ts:931-` 断言键缺席。

**探测器**
- `Prober.scan`（`accountwork/prober.go:156-197`）：跳 na → `probeLoginOf`（`:200-211`，读面 `ProbeLogin`）→ 本机 + 到期才探；扫描周期 30s（`:36`）。`ProbeNow`（`:276-328`）：na → `ErrProbeUnsupported`；无可探登录 → `ErrNoProbeLogin`；错误哨兵 `:45-55`。使用方接口 `ProberAccounts`（`:65-70`）只读 `View`，本卡不改方法集。

**编制域**
- 载体保存 `PutCarrier`（`scheduling.go:237-`）→ `checkCarrierLogin`（`:341-372`：存在、同机、kind 兼容）。唯一调用方 `PUT /api/squads/carriers/{name}`（`agentd/schedapi.go:111-148`）。
- 准入 `usability`（`admission.go:102-145`）：登录不存在/账号不存在 → 放行 + Warn（`:111-114,125-130`）；登录已失效 → 不可用；`Assess` 状态 `Blocks()` → 不可用。
- 存量迁移 `MigrateCarrierLogins`（`migrate.go:43-82`）→ `decideMigration`（`:85-105`）→ `writeMigration`（`:142-166`，直接 `repo.Put`，不经 `PutCarrier`）；每轮发现后调用（`cmd/agentd.go:878-884`）。

**CLI**
- `cmd/account.go`：`list` 表格（`renderAccountTable` `:169-189`，状态列取 `accountStateLabel` `:157-159`）、`--json` 直接 `Encode(AccountView)`（`:53-61`）；`probe` 走 `client.ProbeAccount`（`internal/client/accounts.go:35-50`），非 200 → `httpError` 带服务端原文 → 退出 1（B453 F12）。

**前端**
- 数据源 `store.ts`：唯一拉取者 `useAccountsSource`（`:53-70`），快照 `accounts`/`discovery`（`:22-30`），回执合入 `mergeAccount` 按 name（`:42-47`），`remove`（`:112-114`）。
- 账号页 `AccountsPage.tsx`：删除对话框 `DeleteDialog`（`:101-144`，拒绝体 409 carriers 读法 `:115-119`）、卡上「删除」按钮 `:204`、分组 `:317-341`、对话框挂载 `:343`。
- 移动端 `MobileAccounts.tsx:49-79`（读 `accounts`，页头计数 `:64`）。
- 底栏 `StatusBar.tsx`：段内名字 `:214`（`<span className="sb-name">`），菜单「使用详情」`:238`、「管理账号…」`:239`，头注 `:11`；「刷新所有账号」与弹层列表都读 `sorted = byProblem(quotaAccounts(accounts))`（`:99`）。Shell 只有一处 `onManage` 路由（`shell/Shell.tsx:1842`），去掉「使用详情」不需要改 Shell。
- 载体登录下拉 `SchedulingPage.tsx`：`loginsOn` 读 `accounts`（`:191-194`）；下拉 `:502-509`，当前值不在选项里时保留（`keep`，`:492,508`）；载体详情账号栏 `CarrierAccount`（`:73-`）。
- 新增登录向导结果行 `LoginWizard.tsx:216-218`。
- 弹层 CSS：`.up-main` 宽 `accounts.css:71`；行内边距 `:85,87`；`.q-inline` 间距 `:91`；窗口 `.qi`/`.qi-l`/`.qi .qbar`/`.qi .q-pct` `:95-98`；底栏名字样式 `.sb-seg .sb-name` `:55`。
- 文档：`README.md:429`、`README.zh-CN.md:245`、`skills/handoff/SKILL.md:131-132` 描述 `account list` 列与 `--json`。

**生产读数（2026-10-23 只读 `handoff account list --json`，版本 50333816）**：13 个账号，全部定义行 version=1（上线以来无人编辑、无补 label）；`opencode-go-54ce` 无载体引用，可直接做 S10。

## 4. 数据模型（Q1 采纳时）

- 新 registry kind `account_ignore`（常量放 `types.go`，与另四个 kind 并列），id = 账号 id，body `{"at": <UTC 时间>, "by": <入口机自名，空则 "local">}`。
- 写者唯一：账号域 `Ignore`/`Restore`（新文件 `internal/scheduling/accounts/ignore.go`）。
  - `Ignore(ref, by)`：解析 ref → 读完整读面（`Get`，含引用载体）→ 有引用返回 `*ReferencedError`（复用删除的类型与 `ErrReferenced`，gateway 统一回 409 `{error,carriers}`；错误文案须说「不能忽略」而不是「删除」——执行者可给 `ReferencedError` 加动作字段或另写一个同形错误，二选一自定）→ `Put(account_ignore, id, 0, body)`；CAS 冲突（已存在）= 已忽略，返回现状、不改 at/by。
  - `Restore(ref)`：解析 ref → `Get(account_ignore, id)`；不存在 = 已恢复（幂等）；存在则按版本删，冲突重读一次（并发两人同时恢复）。
  - 两者返回写后读面 `View`。账号不存在 → `ErrNotFound`。
- 读面：`View` 增 `Ignored *IgnoreMark`（类型名执行者定）；`List`/`Get` 多读一次 `List(account_ignore)` 按 id 挂；已忽略 → `ProbeLogin` 置空、**不**走「无可探登录 → fail/no_login」改写（`accounts.go:322-324`），`State` 保持 `Derive` 结果。坏行（解码失败）按「已忽略、at 未知」处理并 Warn——宁可多藏一个账号，也不让坏行悄悄恢复它（与读数行「坏行按空」的取舍相反，理由：忽略是人的决定，读数是可重建缓存）。
- 准入：`Assessment` 增 `Ignored bool`（或同名指针），`Assess` 多读一次 `Get(account_ignore, id)`；`usability` 按 Q5 放行 + Warn。
- 账号定义行、登录行、读数行、发现结果行的 body 与写者一律不变（§11 L13）。不改 DDL、不升 `ledgerSchemaVersion`。

## 5. 改动边界

- 架构方向：忽略的规则全在账号领域；编制域、探测器、gateway 只读 `View.Ignored`/`Assessment` 或调 `Ignore/Restore`，不直接读写 `account_ignore` 行（第五条：规则归数据所有者）。gateway 只做解码、调用、投影、错误翻译。
- 不触碰：发现器扫描逻辑（`accountwork/discoverer.go`、`internal/quota`）、登录行合并写、删除语义（除 Q8 明确不连带）、`DELETE /api/accounts/{ref}` 端点本身、`/api/logins/*`、账本 DDL、原型目录。
- 已冻结且沿用：B464 L1–L10、A1–A12、C1–C2、W1–W3（`docs/superpowers/plans/b464-plan.md` §11）；B453 F10（凭据值不出现）、F12（`account probe` 退出码）。本卡只做加法（§11）。

## 6. 工作顺序与分段

### 段 A：账号域 + 编制域 + 探测器 + API/wire + CLI（先派）

**允许改动的文件集**（路径规则：下列文件及其同目录 `*_test.go` 新增/修改）：
- `internal/scheduling/accounts/{types.go,accounts.go,delete.go,ignore.go(新)}` + 测试
- `internal/scheduling/{scheduling.go,migrate.go,admission.go}` + 测试（新测试文件可叫 `b467_ignore_test.go`）
- `internal/agentd/accountwork/prober.go` + 测试
- `internal/agentd/account_api.go` + 测试（新测试文件可叫 `b467_ignore_api_test.go`）
- `internal/proto/scheduling.go`、`internal/proto/contract_fixture_test.go`、`web/src/api/testdata/AccountsResp.json`（只经 `-update` 生成，不手改）、`web/src/api/accounts.ts`（类型 + `ignoreAccount`/`restoreAccount` 两个调用）、`web/src/api/contract.test.ts`
- `cmd/account.go`、`cmd/account_test.go`
- `README.md`、`README.zh-CN.md`、`skills/handoff/SKILL.md` 中 `account list` 的那几行（补「已忽略账号照列、状态列『已忽略』、`--json` 带 `ignored`」）

**顺序（每步先红后绿）**：
1. **账号域忽略行**（承重假设：忽略不被发现与定义行写者改回）。红：① `Ignore` 后 `List`/`Get` 的 `View.Ignored` 非空、`ProbeLogin` 为空、`State` 不被改成 fail；② 有载体引用时 `Ignore` 返回 `errors.Is(err, ErrReferenced)` 且载体名正确、忽略行不存在；③ `Ignore` 两次幂等（at 不变）；④ `Restore` 后 `Ignored` 为 nil、再 `Restore` 幂等；⑤ 对已忽略账号跑 `Reconcile`（含新登录、登录消失 → gone、补 label 三种情形）后仍已忽略，且登录状态照常变（S2 两半）；⑥ `Put` 改名后仍已忽略；⑦ `Delete` 后再 `Reconcile` 重建账号 → 仍已忽略（Q8）；⑧ 坏的忽略行按已忽略。用真实 `accounts.Service` + 内存 registry（`accounts_test.go:23` 已有 `memRegistry`）。
2. **编制域三处**。红：① `PutCarrier` 选已忽略账号的登录 → `ErrInvalid`，文案含「已忽略，先在账号页恢复」与账号名；未改登录的编辑同样被拒（Q6）；② `MigrateCarrierLogins`：同 HOME 唯一兼容登录属已忽略账号 → 不写、载体仍未选（Q7），另有正常登录时选正常那条；③ `usability`：载体所选登录属已忽略账号且冻结读数为 low → 放行（Q5），并有 Warn。
3. **探测器**。红：真实 `Prober` + 内存账本 + 假 `QuotaProber`（`accountwork_test.go:233` 同款）：已忽略账号到期也不调 quota；`ProbeNow` 返回新哨兵（如 `ErrIgnored`）。恢复后同一 `scan` 立即探到（S4 后半）。
4. **API + wire**。红：经 `httptest` 走真实 handler：`PUT /api/accounts/{ref}/ignore` → 200 且**原始 JSON**（`map[string]any` 解码，不是 struct）里 `ignored.at`/`ignored.by` 在场；未忽略账号原始 JSON 无 `ignored` 键；有引用 → 409 体 `{error,carriers}`、error 含「不能忽略」；`DELETE …/ignore` → 200 无 `ignored`；`POST …/probe` 已忽略 → 409 含「已忽略」；载体 PUT 选已忽略登录 → 400。然后 `proto` 加类型、fixture 样本加一个已忽略账号（踩 `ignored` 在场）并保留一个未忽略账号（踩缺席），`-update` 重生成 JSON，`contract.test.ts` 用 `hasOwnProperty` 断言两种形态，`web/src/api/accounts.ts` 加类型与两个调用。
5. **CLI**。红：`renderAccountTable` 对带 `ignored` 的 `AccountView` 状态列印「已忽略」，其余列照旧；`--json` 行含 `ignored`。
6. **文档**三处同步。

**段 A 验收命令**：
```
gofmt -l internal cmd            # 无输出
go vet ./...
go test ./internal/scheduling/... ./internal/agentd/... ./internal/proto/... ./cmd/...
(cd web && npx vitest run src/api/contract.test.ts && npx tsc -b)
go test ./...                    # 收尾全量（三段律第三段）；环境敏感用例见下
```
环境敏感：`internal/agentd` 有两支用例只在 `CLAUDE_CODE_SESSION_ID` 存在或嵌套工作树下变红，非回归（用户记忆 handoff-env-sensitive-tests），交回物里注明即可。

### 段 B：控制台（段 A 合入卡分支后派）

**允许改动的文件集**：
- `web/src/app/accounts/{store.ts,store.test.tsx,AccountsPage.tsx,AccountsPage.test.tsx,MobileAccounts.tsx,MobileAccounts.test.tsx,StatusBar.tsx,StatusBar.test.tsx,LoginWizard.tsx,accounts.css,model.ts,model.test.ts,testAccounts.ts}`
- `web/src/app/settings/{SchedulingPage.tsx,SchedulingPage.test.tsx}`（预计只改测试；若下拉需提示边角态再动源文件）
- 不改 `web/src/api/**`（段 A 所有）；若发现段 A 的类型或调用不够用，停下升级，不自改。

**顺序**：
1. **store 分区**（承重：一处排除、各处生效）。红：发布含一个 `ignored` 账号的快照后，`useAccounts().accounts` 不含它、`ignored` 含它；`apply(view)` 对忽略/恢复回执在两表之间移动（按 id）。
2. **消费点回归**（写成「去掉分区就会红」的真实组件测试，不测 helper）：`UsageStatusBar` 无该账号的段与弹层行、「刷新所有账号」不探它；`MobileAccountsSection` 无该行且计数不含；`SchedulingPage` 载体登录下拉无该账号的登录。
3. **底栏与弹层**（S8/S9）：段内无 `.sb-name`、段的文本不含账号名、`title` = 账号名；菜单只有「管理账号…」；删 `.sb-seg .sb-name` 样式；`.up-main` 392px；头注 `:11` 同步。
4. **账号页**（S1/S4）：卡上「忽略」替换「删除」；对话框（文案照原型 `pages/accounts.html` 忽略段：标题「忽略账号 X？」/「不能忽略 X」，说明文字、按钮「取消」「忽略」/「关闭」；拒绝时点名载体并给「设置 · 自动化」链接）；成功 → `apply`；页底 `<details>` 默认收起「已忽略（N）」+ 说明 + 行（照原型）+「恢复」；N=0 时整节不渲染；恢复成功在页面给一句「已恢复：X 回到列表，下一轮由持有正常登录的机器探测。」。去掉 `DeleteDialog` 与 store `remove`（若再无调用方）；`deleteAccount` API 函数保留（API 保留不动）。
5. **向导提示**（Q12）。

**段 B 验收命令**：
```
cd web && npx vitest run && npx tsc -b && npx eslint .
```

### 段 C：真机与浏览器验收（**本段由主会话执行，不派发**）
见 §8.3。

## 7. 依赖

- 段 B → 段 A：**契约/数据硬依赖**（段 B 用段 A 产出的 `web/src/api/accounts.ts` 类型与调用、`AccountsResp.json` fixture）。
- 段 C → 段 A + 段 B：硬依赖（验收对象）。
- S10（R9）→ 合入推送 + 全队换版：硬依赖 + 需用户点头。

## 8. 验收

### 8.1 机内断言（执行者，摘要；完整红点见 §6 各步）

- 成功：忽略 → 读面 `Ignored` + 投影 `ignored{at,by}`；恢复 → 键缺席；探测器不探；载体保存拒；CLI 印「已忽略」；前端各消费点不见它；底栏段无名、弹层仅「管理账号…」。
- 关键反例：被引用不能忽略（忽略行不产生）；发现/改名/补 label/删除重建都不恢复；未忽略账号的原始 JSON 无 `ignored` 键（不是 `null`、不是 `{}`）；恢复后同一轮扫描能探到（不是永久卡住）；迁移不把载体迁到已忽略登录、也不改成 no_account。
- 序列化链路：至少一条测试穿过 `accounts.View → accountView → JSON`（handler 级原始 JSON 断言），一条穿过 `proto → testdata JSON → TS`（contract.test.ts）；两端各自单测不算。

### 8.2 运行入口
- Go：`go test ./internal/scheduling/... ./internal/agentd/... ./internal/proto/... ./cmd/...`，收尾 `go test ./...`。
- 前端：`cd web && npx vitest run && npx tsc -b && npx eslint .`。
- 浏览器：隔离实例的控制台 URL（主会话起实例后给出），browser-worker 执行 R4/R7。

### 8.3 真机清单（**本节由主会话执行，不派发**；隔离 agentd + 测试账本，在役 7777 全程不动）

R0 环境：按 Q15 建 `handoff_b467`、起 `t467-mini`（分支二进制）；`handoff account discover` 指向隔离实例，确认真实账号进了测试账本；把一个有三窗口读数的 opencode-go 账号与一个无载体引用的账号记下作样本。验后停实例、删库、删 datadir，回报回滚。

| # | 故事 | 步骤 | 判据 | 证据 |
|---|---|---|---|---|
| R1 | S1/S2 | 浏览器忽略无引用账号 Y → `handoff account discover`（隔离）跑两轮 | Y 不回分组；`account list` 状态列「已忽略」；Y 的登录行与实况一致；`discovery_run.at` 前进 | 截图 + CLI 输出 |
| R1b（可选） | S2 后半 | 若测试期间 Y 名下有登录状态真实变化（如某 HOME 被用户退出登录） | `account list` 里该登录状态随之变、Y 仍已忽略 | CLI 输出；无真实变化则以段 A 单测为准，标「真机未覆盖」 |
| R2 | S3 | 忽略前把 Y 的间隔设 5 分钟；忽略后等 ≥ 6 分钟 | `last_attempt_at` 不变；隔离实例日志无 Y 的「写入账号探测结果」；`handoff account probe Y` 退出 1、文案含「已忽略」 | 日志片段 + CLI |
| R3 | S4 | 浏览器恢复 Y | 回原分组；≤ 60s 内 `last_attempt_at` 前进 | 截图 + CLI |
| R4 | S1 拒绝 | 隔离实例上建载体选 Y 的登录 → 点忽略 | 对话框「不能忽略 Y」点名该载体、链接到设置·自动化；Y 未被忽略 | 截图 |
| R5 | S5 | 忽略 Y 后 `curl -X PUT …/api/squads/carriers/t467-c?expect=0` 选 Y 的登录 | 400，文案「账号 Y 已忽略，先在账号页恢复」 | curl 输出 |
| R6 | S6 | `handoff account list` / `--json` | 状态列「已忽略」；JSON 行含 `ignored.at/by`，其他账号无该键 | CLI 输出 |
| R7 | S7/S8/S9 | 浏览器默认字号：弹层详细模式、底栏、菜单 | 三窗口账号 5h/周/月 三个 `.qi` 的 `offsetTop` 相同；底栏段内无名字文本、`title` 为名字；菜单只有「管理账号…」；控制台无 error | browser-worker 回报 + 截图 |
| R8 | Q1 声明 | 起 `t467-old`（50333816）连同一测试库：经它的 API 改名已忽略的 Y（`PUT /api/accounts/Y?expect=v` 体 `{name}`）| `t467-mini` 的 GET 里 Y 仍有 `ignored`；（预期差异）`t467-old` 的 GET 里 Y 显示为普通账号——记入换版须知 | 两边 JSON 片段 |
| R9 | S10 | 合入推送、用户点头后三台换版；用户在生产控制台忽略 `opencode-go-54ce`；等两轮发现 + ≥ 一个探测间隔 | 不回来；`account list` 已忽略；读数不再更新 | 主会话记录 |

**未验证，需真机**：Q1「旧二进制不触碰 `account_ignore` 行」由代码事实推得（旧版不认识该 kind），行为证据是 R8；S7 只能浏览器量（R7）；S2「真实凭据仍在的 HOME 上不回来」是第 1 条反馈的根因场景，夹具证明不了（R1）。

## 9. 决策权限

执行者可自行决定（交回物「偏离与自主决定」逐条说明）：私有函数/类型命名与文件内组织；`ReferencedError` 扩字段还是另写同形错误（§4）；`Assessment` 用 bool 还是指针；测试文件名与夹具组织；前端 store 内部实现（按 id 移动的写法）；对话框与已忽略区的 DOM 结构与 class 名（文案与形态以原型为准）；日志措辞（字段须含账号 id/名、by、结果）。

须停下升级主会话（远端发工单）：
- 任何 §11 冻结条目的形状或语义变化（含加状态值、`ignored` 改成布尔、改路由/方法/状态码、`by` 取值规则）；
- 要改账号定义行/登录行/读数行 body，或要让发现器/删除碰 `account_ignore` 行；
- 文件集之外的改动（含 `web/src/api/**` 之于段 B、`internal/quota`、`discoverer.go`、账本）；
- 用户可见文案与 spec「实现决定」不一致，或形态与原型不一致；
- 测试揭示 §3 某条现状事实不成立（例如还有第四个账号定义行写者）。

## 10. 缺陷族（`defect-families` 逐族设问 → 验收责任）

| 族 | 设问与结论 | 落到 |
|---|---|---|
| 生命周期中断 | 忽略写到一半宿主重启？忽略是单行单写，要么在要么不在；无内存状态、无孤儿资源。忽略时恰有在飞探测：该次结果照写读数行，无害（读数是缓存，账号已不展示）。 | 无额外断言，因为单行原子写 |
| 静默失败 / 误导报错 | 被引用拒绝必须点名载体（409 体）；控制台连到不认新路由的旧 agentd 会 404/405——控制台与本机 agentd 同二进制，正常不出现，出现时原文显示错误、不报成功。「报成功但没做」窗口：Ignore 先查引用后写，写失败即返回错误，前端只在 200 后移动。 | 段 A 步骤 4 的 409 断言；段 B 对话框只在成功后 `apply` |
| 跨平台 | 无文件系统/进程改动；CSS 在窄视口 `max-width: calc(100vw - 16px)` 时 <408px 仍会换行——紧凑档（phone/pad）不挂底栏（`StatusBar.tsx:12`），可接受。 | R7 在桌面默认字号判 |
| 假红 / 假绿 | S7 布局 jsdom 量不到 → 不写 vitest 宽度断言冒充验收，只在 R7 量 `offsetTop`。前端过滤测试必须渲染真实消费组件读 store（不测 `model` helper），这样删掉 store 分区会红（review 的变异复验点）。API 投影测试用原始 JSON map，struct 解码会把「缺席」与零值混为一谈。 | §6 段 A 步骤 4、段 B 步骤 2 |
| 门禁绕过 | 「已忽略」这道门的全部入口：探测（scan、ProbeNow、API handler、转发——已忽略无可探登录，停在入口机）、载体保存（`PutCarrier`）、**存量迁移（`writeMigration` 绕过 `PutCarrier`，Q7）**、准入（Q5 边角态）。TOCTOU：忽略查引用与载体保存查忽略之间有窗口（两行无跨行事务）——人为并发操作，接受；结果态由 Q5 定义（放行 + Warn），载体编辑时 Q6 会要求改掉。 | 段 A 步骤 2、3、4 各一支断言 |
| 序列化边界 | 链路：`account_ignore` body → `View.Ignored` → `accountView`（手写投影）→ `proto.AccountView` JSON → `AccountsResp.json` → TS `AccountView.ignored?` → store 分区；CLI `--json` 直接编码 proto，继承同一键。缺失与「零值」：只有对象在/不在两态（Q3），断言「未忽略时键不存在」。 | §8.1 序列化链路两条 |
| 枚举新值过白名单 | 新 registry kind：账本无白名单（已核 `ledger/registry.go`）；不加状态值（Q4），`Blocks()`/前端 switch/CLI 词表不受影响。 | 无，因为不新增枚举值 |
| 承重安全属性 | 「忽略不会被发现/改名/补 label/删除重建改回」是本卡唯一承重属性：段 A 步骤 1 ⑤⑥⑦ 各一支能变红的测试；混版本那一半只有真机 R8。 | 段 A 步骤 1、R8 |
| CAS 并发 | 两人同时忽略：expect=0 一方冲突 → 视为已忽略返回现状；同时恢复：删冲突重读、不存在即成功；忽略与恢复交错：最后写者胜，界面下一次轮询对齐。 | 段 A 步骤 1 ③④ |
| 发现与忽略竞态 | Q1 下发现从不读写忽略行，不存在竞态（若改选字段方案，`backfillLabel` 与忽略会互顶，需另加断言）。 | 段 A 步骤 1 ⑤ |
| 前端各处过滤遗漏 | spec 五处 + 「刷新所有账号」+ 移动端计数：全部读 store `accounts`，分区一处（Q9）；账号页只有已忽略区读 `ignored`。载体详情 `CarrierAccount` 遇已忽略账号（边角态）找不到账号对象，只显示名字——可接受，不另做。 | 段 B 步骤 1、2 |

## 11. 需冻结的外部承诺

**入口判据成立**：共享账本 body（所有版本 agentd 读写同一批行，混版本）、控制台 ↔ agentd 的 HTTP 面（段 A 产出、段 B 消费，两个独立工作单元）、CLI `--json`（主会话/skill 依赖）。复用不重述：B464 L1–L10、A1–A12、C1–C2、W1–W3；B453 F10、F12。依赖的既成行为：registry CAS 版本从 1 起、`expect=0` = 必须不存在（`ledger/registry.go:29-77`）。

**共享账本 body**
- **L11**：registry kind `account_ignore` 的 id = 被忽略账号的 id（与 `kind=account` 行同 id）；行在 ⇔ 已忽略。
- **L12**：`account_ignore` body 键恰为 `at`（UTC RFC3339）与 `by`（写入时入口 agentd 自名，自名为空写 `local`）。
- **L13**：`kind=account`、`account_login`、`account_reading`、`discovery_run` 的 body 形状与写者不因本卡改变。
- **L14**：`account_ignore` 行只由账号域 `Ignore`/`Restore` 写；发现归并（Reconcile 及其 `ensureAccount`/`backfillLabel`）、`Put`、`Delete` 都不写也不删它。

**账号 HTTP API（producer agentd；consumer 控制台、CLI）**
- **A13**：`AccountView` 增可缺席键 `ignored`，形状 `{"at": "<RFC3339>", "by": "<机器名>"}`；未忽略时键缺席（无 `null`、无 `{}`、无布尔形态）。
- **A14**：`state` 词表不变；已忽略账号的 `probe_login` 缺席。
- **A15**：`GET /api/accounts` 的 `accounts` 数组包含已忽略账号（不另开数组）。
- **A16**：`PUT /api/accounts/{ref}/ignore`（无体、无 `expect`）→ `200 AccountView`（含 `ignored`）；已忽略再调 → 200，`ignored.at/by` 不变。
- **A17**：A16 遇名下任一登录被载体引用 → `409 {"error": "...", "carriers": [...]}`（与 `AccountDeleteRefusal` 同形），`error` 含「不能忽略」，不产生忽略行。
- **A18**：`DELETE /api/accounts/{ref}/ignore` → `200 AccountView`（无 `ignored`）；未忽略时同样 200。
- **A19**：A16/A18 账号不存在 → 404。
- **A20**：`POST /api/accounts/{ref}/probe` 对已忽略账号 → 409，`error` 含「已忽略」。
- **A21**：`PUT /api/squads/carriers/{name}` 所选 `login` 属已忽略账号 → 400，`error` 含「账号 <账号名> 已忽略，先在账号页恢复」。

**CLI（producer `handoff`；consumer 主会话/skill/协调者）**
- **C3**：`handoff account list --json` 每行形状 = A13 扩展后的 `AccountView`（已忽略行带 `ignored`）。
- **C4**：`handoff account list` 表格中已忽略账号照列，状态列印「已忽略」。
- **C5**：`handoff account probe <已忽略账号>` 退出 1，错误输出含服务端「已忽略」原文（F12 不变）。

**拍板记录**（难逆转、无上下文会惊讶、真取舍）：
- **忽略标记存独立行而不是账号定义字段**（L11/L13，Q1）。被否：`Account` 加字段 + 全队同批换版。反过来写，新二进制的全部单测、契约测试、浏览器验收都绿；只有混版本窗口里旧机改名或补 label 时无声抹掉标记、账号悄悄回来——正是本卡第 1 条反馈的形状，且没有任何测试会红（只有 R8 这类真机混版本检查能看见）。
- **删除账号不连带删忽略行**（L14，Q8）。被否：连带删。反过来写所有测试同样绿，但「删除 → 发现重建」会把忽略一并抹掉。
- **不新增 `ignored` 状态值**（A14，Q4）。被否：`state = "ignored"`。反过来写，前端/CLI/准入的每个 `state` switch 都要登记新值，漏一处即通道分裂，单测只覆盖登记过的那几处。

## 12. 自审

- 故事覆盖：S1–S10 均在 §0、§2.1 有责任与验收；S7/S10 明确只能真机。
- 待验证假设有动作与阻塞范围：Q1 混版本声明 → R8（失败则阻塞 S10 换版方案，回主会话）；S2 真实 HOME → R1；S7 → R7。
- 决策权限写清（§9）；待拍板在稿首；行为闭环 V1–V9 五格完整；依赖分清硬依赖（§7，无施工排序伪装）；真机清单已汇总（§8.3）；冻结节有结论（L11–L14、A13–A21、C3–C5 + 三条拍板记录）。
- 本稿不含实现代码；驱动派发系统自身的步骤（段 C、R9 换版）标注主会话执行、不派发。
