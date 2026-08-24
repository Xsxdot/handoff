# B230 出厂种子退场：handoff 不再预设方法论

- **状态**：**已批准**（用户 2026-08-24 口头批准，含追加要求：product-backlog skill 列语义对齐）
- **级别**：L2（单子系统 `d_coordination`，不动跨进程 wire 契约）
- **档位**：—（L2 无选档）
- **卡**：B230
- **日期**：2026-08-24

## 问题陈述

handoff 的出厂种子（`internal/ledger/workflows.go#EnsureDefaultWorkflows`、
`internal/ledger/templates.go#EnsureDefaultTemplates`）预置了 4 条工作流与 5 个模板。
它们全是**方法论零件**，且全部已退役或从未成为现役：

| 出厂流 | 现状 | 活卡 |
|---|---|---|
| `feature` | 退役 | 0（零卡引用） |
| `domain` | 退役（分域开发协议） | 0（零卡引用） |
| `bug` | 停用，B191 收尾后清零 | 0（26 张终态卡引用） |
| `triage` | 本期退役 | 27 张活卡 + 2 张终态 |

而**现役的 charter 执行流一个字节都不在代码里**：工作流定义 v7（12 列 4 门）与
`charter-default` 模板 v4 只活在本机 `~/.handoff/ledger.db`，7 个 `charter-*` 纪律块
只活在 `~/.handoff/discipline/`（由 charter 仓 `scripts/regen_discipline.py` 生成）。

两件事合起来是同一个病：**handoff 是通用派发引擎，charter 是建在它上面的方法论，
而种子把这条界线画错了**——它把退役的方法论焊进了产品出厂默认，同时让现役方法论
没有任何可复现记录。

本期只治前半句：把方法论从 handoff 的出厂默认里清出去。后半句（charter 三样东西
落进 charter 仓的可复现安装）归 charter 仓侧，另建卡。

## 方案

**出厂种子整体退场。** 两个 seed 方法删除，`handoff` 装完得到一个零工作流、
零模板的空账本；工作流与模板一律由用户 `workflow put` / `template put` 建立。

清空后的三个着落：

1. **`card add` 缺省流**（现 `internal/ledger/cards.go:144` 写死 `"triage"`）
   → 账本里**只有一条流时自动用它**；零条时报错指路（说明先建流）；多条时报错
   要求显式 `--workflow`。无歧义时不问，有歧义时不猜。
2. **`card dispatch --template` 缺省**（现 `cmd/card_dispatch.go:211` 写死
   `"feature-impl"`）→ 同构：缺省留空，唯一模板时自动取，零/多条时报错指路。
   （只影响不带 `--step` 的裸 dispatch；带 `--step` 时模板取自节点定义，不受影响。）
3. **`workflow list` 的「出厂三条恒在」**（`cmd/workflow.go:37,49` 硬编码
   `feature/bug/triage`）→ 删除，全量走 `ListWorkflowNames`。不删的话删掉 feature
   会让 `workflow list` 直接 `return err`。

### 弃选方案

- **把 charter 内置进种子**（初版推荐，已否决）：会把私人方法论变成产品出厂默认；
  且纪律正文的单一真源在 charter 仓，embed 就是第三份副本，正撞「纪律块副本是
  陈旧快照」的实测教训。
- **留 feature 一条当通用示例流**：它不依赖外部文件、装完即可跑通，是最弱的保留
  理由；但「出厂预设一条流」本身就是预设方法论，且 `feature-impl` 模板写死
  `Executor: opencode` + `BranchPrefix: cards`，等于预设了一台机器的装机情况。
- **留 feature-impl + review-generic 两个通用模板**：同上，写死 opencode 的配方
  不通用；且工作流清空后它们零引用者。
- **`card add` 新增配置项 `ledger.default_workflow`**：多一个配置面，且会与账本
  实情脉冲（配置指向一条已删的流），需要额外的一致性校验。

### charter 「已定性」列：不做（卡里的③已否决）

卡的 note 要求「charter v8 在 spec 后加『已定性』领活列」。**否决**，charter 列序
不变（仍 12 列，无需 v8）。理由是语义重复与实证：

- 「定性中」= 正在做 spec 节点，与 charter 的 `spec` 列**完全同义**；
- 「已定性」= spec 做完、下一节点未开始，是**等待态**，不对应任何节点。charter 里
  同类等待期（plan 做完等 implement）一律用「停在下一个节点的列上」表达，不专设列。
  加这一列等于在 charter 里引入第二套表达同一件事的写法。
- 实证（51 次从 triage 迁出）：出发列 `待办` 35 次 / `已定性` 13 次 / `定性中` 3 次。
  剔掉 bug 流（bug 入口不走 spec）后去 charter 的 27 次里，`待办` 12 : `已定性` 13。
  其中「`待办` → `charter.plan`」8 次——plan 列的门验 spec 附件，所以这些卡迁移时
  **已挂 spec，只是没人去动列位**。人手维护的列位一半时候没人维护；真判据是附件，
  而门已经在验它了。

取而代之：**spec 完稿当即按级 move 到下一节点列**（L1→`implement`、L2→`plan`、
L3→`contract`）。triage 三列的映射因此是：`待办`→`charter.待办`、
`定性中`→`charter.spec`、`已定性`→不再存在（换成跳执行列）。

代价（如实记）：领活池不再是可一眼扫的单一列，判据变成「执行列里未派发过的卡」
（`dispatched` 事件存否）。本期不为它加查询能力——现役 charter 活卡个位数，肉眼够用；
真需要了从 roadmap 取。

## 用户故事

1. 作为新装 handoff 的用户，我 `handoff card add` 时账本里零工作流，**得到一条说明
   要先建流的报错**，而不是把卡挂到一条不存在的流上。
2. 作为本机用户（账本里只有 charter 一条流），我 `handoff card add "x" --project handoff`
   不带 `--workflow`，卡**落进 charter 的首列**，日常手感与今天一致。
3. 作为有多条流的用户，我 `card add` 不带 `--workflow` 时**被要求显式指定**并看到可选
   流名，而不是被静默塞进某一条。
4. 作为用户，我 `handoff workflow list` 看到的是**账本里真实存在的流**，不含任何已删
   流名，也不会因为某条流不存在而整条命令失败。
5. 作为用户，我裸 `card dispatch`（不带 `--step`）不指定 `--template` 时，行为与
   `card add` 缺省流同构：唯一模板自动取，零/多条报错指路。
6. 作为协调者，我在本机把 triage 的 27 张活卡迁进 charter 后，`feature`/`domain`/
   `bug`/`triage` 四条死行从账本删除，**且历史终态卡的 `card show` 仍可读**。

## 实现决定

- **两个 seed 方法整体删除**（不是留空 map）：留一个空的 `EnsureDefaultWorkflows`
  会保留「出厂种子」这个概念面与它的 4 处调用点，而这个概念本期正是要取消的。
  调用点：`cmd/agentd.go:273,276`、`cmd/ledgercli.go:42,46`（源码注释里说的「11 处
  调用点」是历史读数，现况 4 处）。
- **缺省解析放 Store 层还是 CLI 层**：`card add` 的缺省流解析下沉到 `internal/ledger`
  （与现有写死点同层），因为 agentd 的 HTTP 建卡入口共用它——放 CLI 层会让两个入口
  的缺省行为分叉。`dispatch --template` 的缺省留在 `cmd`（那是 flag 缺省值，不是
  账本语义）。
- **删死行是运维步骤不是代码**：本期不写「删除出厂流」的迁移代码。种子退场后死行
  只是不再复活，删除由一次性 SQL 完成（备份已在
  `~/.handoff/backups/ledger-pre-workflow-cleanup-20260824.db`）。
- **迁移顺序钉死**：先迁 27 张活卡 → 再删 4 条死行。`MigrateCardWorkflow` 只解析
  **目标**流定义（`internal/ledger/workflows.go#MigrateCardWorkflow`），不解析源流，
  所以反序也能跑通；但先迁后删让每一步都可单独验，且失败时账本仍自洽。
- **历史卡的读取安全性**（现状读数，待 plan 复核）：`GetWorkflow` 在卡路径上只出现
  两处——`cards.go:146`（`card add`）与 `move.go:40`（`card move` 的 gate 判定）。
  `card show` / `card list` 不解析工作流，所以 bug 的 26 张与 triage 的 2 张终态卡
  在流定义删除后仍可读；它们已是终态，不会再 move。**plan 节点须复核 agentd HTTP
  侧与看板 UI 是否另有解析点。**

## 测试决定（接缝清单）

两个缝，一层一个：

1. **`internal/ledger` Store 层**（主缝）：缺省流解析的三态——零流报错、
   唯一流自动取、多流报错要求显式；以及「新开 Store 后账本零工作流零模板」。
2. **`cmd` CLI 层**：`workflow list` 在任意流集合下不失败（含空账本）；
   裸 `dispatch` 的 `--template` 缺省三态。

**存量测试面的分类改造**——20 个测试文件引用了 `"feature"` / `"domain"` /
`feature-impl` / `domain-*`，必须先分三类再动，不许无脑替换：

| 类别 | 处置 |
|---|---|
| 真依赖「种子里有这条流/模板」 | 改为测试内自建（`PutWorkflow` / `PutTemplate`）|
| 只是自建了一条**恰好叫** `feature` 的流 | 不动（与种子无关）|
| 断言「种子里有 N 条流/模板」 | 反转为断言零条 |

分类结果与每类的文件清单由 plan 节点落表；**implement 不得跳过分类直接批量改**。

## 伴随的文档对齐（`charter:finish` 节点执行，不在 implement）

列语义变了，卡驱动驾驶手册必须跟着改，否则手册与账本脉冲。改动落在
`~/.claude/skills/product-backlog/SKILL.md`（**skill 仓，不在 handoff 仓**，
派发的 executor fetch 不到，只能本地会话做）。

**为什么不在 implement 之前改**：`card add` 缺省流的代码未落地时，手册若已写
「缺省落 charter 待办」，而实际行为仍写死 triage，任何读到手册的会话都会静默错位。
所以本项排在 implement 通过之后、随 finish 的文档对齐一起做。

新语义三条（用户 2026-08-24 定）：

1. **建卡落 `待办`**（charter 首列，唯一流自动取，不再写 `triage`）；
2. **用户说「开卡」→ `card move <id> spec`**（这就是原「定性中」的动作，换了列名）；
3. **spec 完稿 → 按级 move 执行列**（原「move 已定性」消失，见本 spec「已定性列：不做」）。

逐处清单（行号为 2026-08-24 读数，`grep -n '定性中\|已定性\|triage'` 复核）：

| 处 | 现状 | 改成 |
|---|---|---|
| 12, 19, 29, 32 | 「聊透定性…从领活池领取」「移进已定性才能被领取」 | 领活池的新定义（见下） |
| 38–42 Overview 流程图 | 落 triage 待办 → move 已定性 → 从已定性推荐 → 跨流 | 落 charter 待办 → move spec → 按级 move 执行列 |
| 58 状态机「现役两条流」 | triage 收件箱 + charter 执行流 | **删 triage 一节**，只剩 charter 单流；`待办` 列即收件箱 |
| 69 「领取即跨流」 | 按级迁到 charter 流 | **删**——不再跨流，改为「按级 move 到执行列」 |
| 76, 316 合法路径 | `待办 → 定性中 → 已定性 →（跨流）执行流 → 已完成` | `待办 → spec → 执行列 → 已完成` |
| 88 Entry 1 | 「缺省就是 triage 待办」 | 「缺省就是 charter `待办`」＋注明账本多流时须显式 `--workflow` |
| 102 Entry 2 | `card move <id> 定性中`（可选） | `card move <id> spec`——**不再可选**，「开卡」就是这个动作 |
| 108 Entry 2 | `card move <id> 已定性` | 按级 move：L1→`implement`、L2→`plan`、L3→`contract` |
| 123, 146, 313, 315, 333 Entry 3 领活 | 「只扫 `--status 已定性` 这一列」 | 两处扫，就绪度优先：①执行列（contract/plan/implement）里**未派发过**的卡（已有 spec，可直接推）②`待办` 列（须先走 spec）。「随便挑一个」仍不许挑没 spec 的 |
| 130 Entry 3 | `workflow migrate --workflow charter --column …` | `card move <id> <执行列>`——同流内移列，不再 migrate |
| 303 交棒边界表 | 「从已定性推荐、跨流迁移」 | 「从执行列/待办推荐、同流移列」 |
| 338 Red Flag | 「领活后卡还留在 triage」 | 换成「spec 完稿后卡还留在 `spec` 列：忘了按级 move，领活时扫不到它」 |

Entry 2 的触发词要补「**开卡**」「开 B<id>」——这是用户实际的说法，手册里现在只有
「把 B2 聊透」「brainstorm 一下」。

## Out of Scope

**永不做**：

- 把 charter 工作流/模板/纪律块 embed 进 handoff 二进制（本期已论证否决，见弃选）。
- charter 加「已定性」列（已否决，见上）。
- 保留任何一条出厂流或出厂模板。

**本期不做、后续要做**（已落 `docs/roadmap.md`）：

- **charter 三样东西的可复现落点**：工作流定义 v7 + `charter-default` 模板 v4 +
  7 个 `charter-*` 纪律块，落进 charter 仓，与 `scripts/regen_discipline.py` 同源
  安装。**这是本期发现的真问题的另一半，另建卡，project=charter。**
- **卡里的④条件边**：定级落卡字段（`Card` 结构 `internal/ledger/types.go:98` 现无
  级别字段）、门按级差异化、跳边自动化。是行为变更且改到派发主路径，与本期的
  「缺省值对齐」不同类风险。
- **领活池查询能力**：`card list` 增「未派发过」筛选，让「执行列里未派发的卡」可
  一条命令列出（本期方案的已知代价）。
- **bug 流 26 张终态卡的归档去向**：流定义删除后它们的 `workflow_name` 指向一条不
  存在的流。本期靠「终态卡不再 move」兜住，长期要么保留一条 tombstone 定义，要么
  在读取路径上显式容忍。

## 备注

### 图覆盖债

`codegraph domains` 命中 `d_coordination`（含 `d_coordination_cli` 等 4 个子领域），
本次改动全部落在该顶层领域内——这是 L2 定级的依据。以下符号本次靠 grep 定位、
图未命中，记账待重扫消化：`EnsureDefaultWorkflows`、`EnsureDefaultTemplates`、
`MigrateCardWorkflow`、`Resolver.ByName`。

### 现状读数出处（待 contract/plan 对本轮工作树复核）

- 出厂工作流种子：`internal/ledger/workflows.go:242`（`defaults` map）
- 出厂模板种子：`internal/ledger/templates.go:226`
- `card add` 写死 triage：`internal/ledger/cards.go:144`
- `dispatch --template` 缺省：`cmd/card_dispatch.go:211`
- 「出厂三条恒在」：`cmd/workflow.go:37,49`
- 纪律块解析（缺文件且无内置 → 硬报错）：`internal/discipline/resolver.go:176`
- `Card` 无级别字段：`internal/ledger/types.go:98`
