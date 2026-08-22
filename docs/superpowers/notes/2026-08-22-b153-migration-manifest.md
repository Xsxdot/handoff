# B153 存量迁移清单：backlog.md 活跃行 → 卡账本

> 2026-08-22。依据 [spec](../specs/2026-08-22-b153-backlog-to-cards.md)。
> 本文件是**迁移前落盘的执行清单**，末尾附执行后的核对结果。
> 迁移操作对象是本机真实账本 `~/.handoff/ledger.db`。

## 一、清单是怎么算出来的

活跃行必须**跨血脉取并集**——单条分支只有局部视图，正是撞号事故的根因
（记忆 `backlog-diverged-across-branches`）。取了三份：

| 来源 | 提交 | 号段 max | 表里 B 号个数 |
|---|---|---|---|
| `main:docs/superpowers/backlog.md` | `573149364` | 172 | 182 |
| `handoff/web-console:docs/superpowers/backlog.md` | `2710515c2` | 163 | 173 |
| 本分支 `docs/superpowers/backlog.md`（工作副本） | `779288c34` | 170 | 180 |

**全局 max B = 172**（`main` 侧的 B172）→ `card min-b 172`。

活跃状态 = 💡 idea、📋 specced、🔨 doing、📦 epic。
不迁：✅ done(已验/未验/真机验)、🗄️ shelved、🚫 已评估·不做、🔀 已并入、🧊 已知延后。

**实测与 spec 快照的偏差（spec §备注已授权「以当时 md 为准」）**：

- **📋 specced 一行都没有**。spec 写「specced 5 行」，三份 md 里该状态字面量一次都没出现——
  写 spec 的那份读数不成立。实际活跃行按状态是 idea 14 / doing 2 / epic 1。
- **🔨 doing 是 2 行不是 7 行**（B156.1、B166）。

## 二、两条判断题（机械规则会判错，此处按事实判）

### B163：不迁

`handoff/web-console` 侧是 💡 idea（「Manager 的 Targets 运行期配置未热读」），
`main` 与本分支侧是 ✅ done(已验)（「跨机镜像跟随活配置」，标题在做的过程中被改写过）。
任务给的机械规则是「同 B 号以 web-console 版为准」，照此会把一条**已经做完并合入**的活
当 idea 迁进账本。git 侧事实站在 main 一边：`779288c34 merge(b163)`、`fc5189325`、
`e4dbf95a9` 三个提交都在本分支祖先链上。→ **判 done，不迁**。
web-console 那行是分叉后没跟上的陈行，冻结时按 done 收口（协调者在合并阶段处理另一条血脉）。

「以 web-console 为准」这条规则的适用范围是**两侧同为活跃行、内容有出入**时选哪份文本；
它不能把「一侧已终态、另一侧陈旧」判成活跃。

### B153：不迁

B153 就是本轮这件事本身，本分支收尾即把它的 md 行改为 ✅ done。
把它同时导入成一张 triage 待办卡，等于账本与冻结 md 互相打脸。→ **归 done，不迁**。

## 三、迁移清单（17 张卡，project 一律 `handoff`）

落点列说明：triage 三态 `待办 → 定性中 → 已定性`；**领活池 = 已定性**。

| # | B 号 | md 状态 | 优先级 | 落点 | 附件 / 备注动作 |
|---|---|---|---|---|---|
| 1 | B82 | 💡 idea | 低 | 待办 | 指针 note |
| 2 | B90 | 💡 idea | 低 | 待办 | 指针 note |
| 3 | B154 | 💡 idea | 中 | 待办 | 指针 note |
| 4 | B155 | 💡 idea | 高 | 待办 | 指针 note |
| 5 | B156 | 📦 epic | 高 | 待办（父卡） | `spec:docs/superpowers/specs/2026-08-18-workbench-blueprint-design.md` |
| 6 | B156.1 | 🔨 doing | 高 | 已定性 | `spec:…2026-08-18-workbench-phase1-design.md` + 在进行中 note（领于 08-19） |
| 7 | B156.2 | 💡 idea | 中 | 待办 | 指针 note |
| 8 | B156.3 | 💡 idea | 中 | 待办 | 指针 note |
| 9 | B156.4 | 💡 idea | 中 | 待办 | 指针 note |
| 10 | B159 | 💡 idea | 中 | 待办 | 指针 note |
| 11 | B162 | 💡 idea | 低 | 待办 | 指针 note |
| 12 | B166 | 🔨 doing | 高 | 已定性 | `spec:…2026-08-19-desktop-update-surface-design.md` + 在进行中 note |
| 13 | B168 | 💡 idea | 中 | 待办 | 指针 note |
| 14 | B169 | 💡 idea | 中 | 待办 | 指针 note |
| 15 | B170 | 💡 idea | 中 | 待办 | 指针 note |
| 16 | B171 | 💡 idea | 中 | 待办 | 指针 note |
| 17 | B172 | 💡 idea | 低 | 待办 | 指针 note |

标题逐字取 md 的「标题」列。每张卡落一条指针 note：
「案情见冻结 md 对应行（docs/superpowers/backlog.md，B×××）」——长备注不搬，
md 冻结后原文不会漂移，指针足够，搬运只会失真（spec §富文本备注的去向）。

**epic 父卡 B156 为什么停在「待办」而不是「已定性」**：spec 的状态映射表给 epic 的
落点只写了「父卡」，没给列。领活池 = 已定性，而父卡是容器不是可领条目——放进领活池
会让 Entry 3 把一个四期蓝图推荐成「下一个可以干的活」。故停在待办，蓝图 spec 照挂。

**B166 的原领取日期**：md 里没有显式「领于」字样，最早活动痕迹是 08-19（一期已验收并
合入那条）。note 里如实记为「领取日期 md 未显式记录，最早活动 08-19」。

## 四、执行顺序

1. `card min-b 172`
2. 逐条 `card import <B号> <标题> --project handoff --priority <高|中|低> --source backlog.md`
3. specced/doing 卡：先 `card update --attach spec:<仓内相对路径>`，再 `card move <id> 已定性`
   （实测 `MoveCard` 只校验「目标态在钉住版本的 States 内」，不强制沿 next 边逐跳，
   故一跳即可——move.go 的包注释写明「一期不限制转移方向」）
4. doing 卡补 `card note`
5. 每卡补指针 note
6. `card list --all` 核对总数

---

## 五、执行结果

2026-08-22 在本机真实账本 `~/.handoff/ledger.db` 上执行完毕，全部命令 rc=0，
stderr 无 error/warn（只有 INFO 与既有的「模板用了废弃字段 discipline_path」提示，
与本轮无关）。

`card min-b 172` 已落。`card list --all` **实得 17 张，与清单逐条一致**：

| 落点 | 张数 | B 号 |
|---|---|---|
| triage 待办 | 15 | B82、B90、B154、B155、B156（epic 父卡）、B156.2、B156.3、B156.4、B159、B162、B168、B169、B170、B171、B172 |
| triage 已定性（领活池） | 2 | B156.1、B166 |
| **合计** | **17** | 与清单第三节 17 行一一对应 |

按原 md 状态统计：💡 idea 14 张、🔨 doing 2 张、📦 epic 1 张。

抽验 `card show`（B156 / B156.1 / B166 / B82）：

- 三张带 spec 的卡附件都在，路径为仓内相对路径（`docs/superpowers/specs/…`）。
- `card_created` 事件负载带 `"imported": true, "import_source": "backlog.md"`
  ——导入来源标注生效。
- B156.1 的 `parent` = `B156`，父卡 timeline 有四条「创建子卡 B156.x」留痕，
  点号子卡与自动建的子卡零差别。
- B156.1 / B166 各有 `status_moved 待办→已定性`、一条「存量迁入时已在进行中」note、
  一条指向冻结 md 的指针 note。
- 全部 17 张钉的都是 `triage v1`。

迁移后账本里最大顶层号 = B172，min_b = 172 → **下一个自动分配号是 B173**。

## 六、全局 CLAUDE.md §3.2 的改法

有写权限，**已直接改**（`/Users/xushixin/.claude/CLAUDE.md:74`）：

```diff
-| **product-backlog** | 记需求、加 backlog、排期、领活、我接下来做什么、从零规划新项目 | 上游需求总账：沉淀需求→排期→领取调度，交棒 `charter:spec` |
+| **product-backlog** | 记需求、加 backlog、排期、领活、我接下来做什么、从零规划新项目 | 上游需求总账**（载体是 handoff 卡账本 `handoff card`，不是 markdown）**：建卡沉淀→定性→从 triage「已定性」领活，交棒 `charter:spec` / `charter:plan` |
```

触发词一列未动（原说法已覆盖「记需求 / 加 backlog / 领活 / 我接下来做什么」）。
同文件 §3.2 规则区第 86 行那条（「先调 product-backlog，它管需求总账与领取调度」）
在卡口径下仍然逐字成立，未改。

## 七、留给协调者的两件事

1. **另一条血脉的冻结**：`handoff/web-console` 侧 `docs/superpowers/backlog.md`
   也要加冻结标注，并把该侧陈旧的 B163 行按 done 收口。冻结不落全就没冻住。
   注意该分支的 backlog.md **第 220–225 行有一处未解决的合并冲突标记**
   （`<<<<<<< HEAD` / `>>>>>>> claude/b160-general-settings`），冻结时顺手收掉。
2. **B82 / B90 的号在冻结 md 里各有两行**（header 记的「真撞号」六个号之二）：
   迁进账本的是活跃的那一行——B82 取 w4 线「编辑草稿服务端化」，B90 取 main 线
   「CLI 界面英文化」；另一行是**另一件事且已 done**，只留在冻结 md 里。
   查 B82/B90 的历史时必须连「线」列一起看，账本里的那张卡只对应其中一件事。
3. **记忆 `backlog-diverged-across-branches` 待补**一段「冻结后取号纪律作废，
   新号一律账本分配」（spec §实现决定列出，本轮未做——记忆文件不在本仓内）。
