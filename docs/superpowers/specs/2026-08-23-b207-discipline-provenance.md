# B207：纪律块的来源、组装与漂移

> 状态：待用户批准 · 级别 **L2** · 档位不适用（L2 无选档）
> 来源：B200 的决定性实验（根因取证在 B200 卡上）

## 问题陈述

派发时执行者读到的纪律块正文，来自执行机本地 `<DataDir>/discipline/<角色名>.md`。
今天这些文件是**手工组装的快照**：某人把 charter 仓的 `skills/<节点>/SKILL.md` 去掉
frontmatter，再拼上附录（架构法、缺陷族法），拷进目录。这个过程：

- **没有生成器**——仓库里零处引用 charter 源（已全量 grep 确认），组装步骤只存在于某次
  会话的记忆里；
- **没有溯源**——文件里不记录它取自哪个 charter 提交、哪几个 skill、什么时候；
- **没有漂移检测**——charter 仓演进后无人知晓，也无从查；
- **没有跨机一致性**——每台机器各存各的，同一个角色名可以在两台机器上解析出两份不同
  的正文。

代价已实测（2026-08-23）：`charter-plan.md` 与 `charter-review.md` 停在「## 红线」节
加入之前的版本，各丢了整节。那两节正是禁令本身（plan：「本节点的产出物是计划文档，
不是实现代码」；review：「绝不按 plan 文档重新实现一遍再审自己写的」）。后果是 plan 节点
连续 4 轮直接写实现代码、review 轮重做被当成另一个缺陷追了很久。补回红线节后重派，
产出立刻变成 941 行纯 plan 文档、零实现代码（对照实验取证在 B200）。

止血已做（两机同指纹，备份在各机 `~/.handoff/discipline/.bak-20260823/`），但机制没修：
`breakdown` / `implement` / `integrate` 三份至今两机指纹不同，且没有任何地方能看出这一点。

## 现状事实（本轮工作树读数，交 contract/plan 复核）

| 事实 | 出处 |
|---|---|
| 角色名解析优先命中覆盖文件，未命中才用内置块，两者都无则报错 | `internal/discipline/resolver.go#Resolver.ByName` |
| 内置角色块共 6 份，含 `plan-writing` / `review` / `implement` / `spec-draft` / `finishing` | `internal/discipline/discipline.go#builtinByName` |
| 内置 `plan-writing` 开篇即「你这一轮的产出是一份实现计划，不是代码」 | `internal/discipline/builtin/plan-writing.md` |
| `charter-v4.json` 的 plan 节点点名 `charter-plan`，于是内置那份**从未被用上** | `deploy/workflows/charter-v4.json:53` |
| Resolver 无状态、每次派发重新读盘（改文件不必重启 agentd） | `internal/discipline/resolver.go#Resolver` 类型注释 |
| 分发通道已存在且支持转发到具名机器，但只有控制台在用、无 CLI | `internal/agentd/discipline.go#handleDisciplineFileWrite`（`PUT /api/discipline/file?name=&machine=`）；`forwardIfRequested` 在同文件 |
| 仓库内不存在任何引用 charter 源的生成/同步代码 | 全量 grep `charter/skills`、`skills/charter`，命中 0 |

## 方案

### 方案 D（推荐）：职责切开——handoff owns 角色契约，charter owns 方法论

纪律块在**解析时**由两段拼成：

1. **角色契约段**（handoff 自有，内置、随二进制分发、不可被覆盖文件替换）：
   「你这一轮的产出是什么、不许做什么」。就是今天 `builtin/plan-writing.md` 那类短文。
2. **方法论段**（charter 自有，可选，走覆盖文件）：「怎么把这件事做好」。就是今天
   charter-\*.md 的内容。

覆盖文件从「替换」降级为「追加」。于是**方法论怎么漂移，角色契约都还在**——本次这个
失败类被结构性排除，而不是靠「记得同步」。

取舍：改了 `ByName` 的既有语义（今天覆盖文件是完全替换）。需要一条迁移路径，且要回答
「想完全替换内置契约的人怎么办」——留一个显式出口（如覆盖文件带一行 front-matter 声明
`replace-role-contract: true`），凡红线必留正当出口。

### 方案 A：加生成器 + 漂移检测（`handoff discipline sync`）

一份清单声明「哪个角色名 = 哪个 charter skill + 哪些附录」，命令按清单生成、比对各机
（走既有 `GET /api/discipline/file`）、推送（走既有 `PUT`），`--check` 模式只报不改、
有漂移则非零退出，可挂 CI。生成的文件头写入溯源（charter 提交号、组成、生成时间）。

取舍：正确性仍依赖「有人记得跑」。它把漂移**变得可见**，但没有让丢失禁令这件事变得
不可能——今天的事故在方案 A 下会被更早发现，不会被杜绝。

### 方案 B：干脆废掉副本

`charter-v4.json` 改点内置角色名（`plan-writing` / `review` / …），删掉 charter-\*.md。
没有副本就没有漂移。

取舍：丢掉方法论内容（架构法、缺陷族法、四项检查等 100~200 行/块），那些是真有价值的。
本次事故恰恰证明内置契约够用**来防越轨**，但不足以指导做好。**不推荐**。

**推荐 D，并把 A 作为 D 之后的补充**：D 管「禁令不会丢」，A 管「方法论保持新鲜且各机
一致」。两者不互斥，先做 D（防的是已发生的事故类），A 视需要再排。

## 用户故事

1. 作为协调者，我改了 charter 仓的 plan 节点正文，希望执行机下次派发就能用上，且能查到
   现在各机跑的是哪一版。
2. 作为协调者，即使某台机器的方法论文件是三周前的旧版，我也要确信 plan 节点不会去写实现
   代码——禁令不依赖同步是否及时。
3. 作为协调者，我要能一条命令看出哪台机器的哪个角色块与权威源不一致，并选择推平。
4. 作为使用者，我想完全用自己的一套纪律替换内置契约时，要有显式且留痕的做法，而不是
   靠「覆盖文件恰好会替换」这个隐式行为。

## 测试决定（接缝清单）

最高可测缝是 **`Resolver.ByName` 的返回值**——它是「解析出什么正文」的唯一收口点，
角色契约段是否恒在、覆盖文件如何参与、显式替换出口是否生效，全部可在这一个缝上断言，
不需要起 agentd、不需要真派发。

一条真机确认（不进单测）：改完后重派一次 plan 节点，产出仍是 plan 文档。
判据与 B200 的对照实验同款。

## Out of Scope

- **不动 charter 仓**。charter 是权威源，本卡只解决 handoff 侧怎么消费它。
- **不做纪律块的版本回滚 / 灰度**。YAGNI，今天连「现在是哪一版」都答不出，先解决这个。
- **不给分发通道做鉴权改造**。复用既有 `PUT /api/discipline/file`，它已有的鉴权就是它的鉴权。
- **不顺手同步 breakdown / implement / integrate 三份的两机差异**（本期不做、后续要做）：
  手工再同步一次只会把病往后拖一轮，等本卡的机制落地后统一推平。已记入 roadmap。
- **不改内置块的正文**。本卡只改「怎么组装、怎么分发、怎么发现漂移」，不改各角色说什么。

## 备注

`contract` 与 `implement` 没有「## 红线」节是**正确的**——它们的权威源本来就没有这一节。
排查时别把它当漂移一起「修」。
