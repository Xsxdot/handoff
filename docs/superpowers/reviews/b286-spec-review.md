# B286 spec 审查（C7 / C8）

审查对象：`docs/superpowers/specs/b286.md`（状态：待独立审查）
对照台账：`docs/superpowers/specs/b286-ledger.md`
对照代码：工作树 `/Users/sycm/.handoff/worktrees/b286-step-review` 分支 `fix/b286-step-review`（与 `origin/main` @ `f8e252ef3` 同内容，外加未提交 spec/台账）
源卡：C7 / C8（并入 B286）
审查者：独立 subagent（charter spec 审查，只读，不改 spec/代码）
日期：2026-08-28

行号按当前工作树，会漂。

## 1. 总判

**修订后再批。**

方向对，协调者事先相信的四件事与活代码对齐：C7 剩下的是 202 之后 CLI 静默，不是缺模板 target；B271 已把空 target 收成本机，本卡不得再拒空 `--target`、不得写死 `linux-01`；C8 剩下的是 `purpose=review` 且 pass 时对 Diff 的机械闸，平台层台账矛盾已由 B229.7 拆掉；`POST /api/cards/{id}/step` 必须继续 202；两条互不相干，禁止抽公共框架。L2 成立，不抬 L3。

不能批的原因是 C7 按正文落地会做错题：短等「消费本机卡事件」没钉通道、没钉 POST 前水位、没钉只认本次新事件。零上下文实现者能合理写成「POST 后再 `MaxSeq` / 订 live WS / 扫卡上最后一条 `dispatched`」，三条都会在 charter 主路径上把上一节点的成功快照当成这次成功，或把本机快路径派发全部超时成「已受理」。这比今天的静默更坏——无人值守会带着假成功往下走。C8 闸点选对了，但 `Diff` 失败与白名单未过的落账/路由、以及存量「节点名 review 但 purpose 为空」的负例还没锁死，plan 会在 `RecordReviewVerdict` 前后分叉。最小补丁见文末。

## 2. Findings

### Critical

#### C1. C7 短等没钉通道和水位；三种合理读法里有两种会假成功或系统性超时

- **位置**：spec 方案 C7 第 2–4 点 `b286.md:71-75`、实现决定 `b286.md:122`、接缝 1 `b286.md:129-133`、故事 1–5 `b286.md:110-114`；活代码 `cmd/card_node.go:22-51`、`cmd/card_wait.go:48-111`、`internal/ledger/follow.go:13-19`、`internal/agentd/cardstep.go:82-86`、`internal/ledgerstep/dispatch.go:333-344`
- **事实**：今天 `runStepDispatch` 只 `LocalEndpoint` + `client.CardStep`，202 即打印「已受理」退 0。卡事件的生产消费通道已经在 `card wait`：`openLedger()` → `MaxSeq()` → `Store.Follow`（`fromSeq` **排他**）。`Follow` 注释写明从 `fromSeq` 之后开始。`dispatched` 快照是 ViaTemplate 在 Transport 成功之后才 `RecordDispatch` 的，本机路径经常在 HTTP 202 返回前或返回当拍就落账。
- **两种会做错题的读法**：
  1. **POST 成功后再记 `MaxSeq` 再 Follow**（抄 `runCardWait` 的「先 MaxSeq 再跟」但把 MaxSeq 放到受理之后）：本机 ViaTemplate 已经把 `dispatched` 写进库，排他水位恰好等于该 seq，短等窗口内看不到首态，20s 后走超时「已受理」退 0。故事 2/3（成功必须打出短号/本机）在默认本机路径上稳定假超时。
  2. **不跟本次水位，扫卡上最近一条 `dispatched`**：charter 主路径先进 `implement` 再 `--step review`。跨机/基线失败时卡上早已有实现轮快照。CLI 会把旧快照当成本次成功、退出 0，stderr 没有失败原文。这是 C7 要堵的无人值守丢活的**反相**——从静默失败变成假成功。
  3. **202 之后再订本机 `/ws/events` 或新开 HTTP 等首态**：与「不新增 HTTP 路径」对撞；快路径事件已经过去，同样超时。生产 `client.New` 默认 Transport 本卡明确不改，测试 mock 只回 202，这条路还会逼着改拨号。
- **正确读法**（正文没写）：与 `card wait` 同一条账本通道；**在 `CardStep` 之前**记下 `MaxSeq`；只认 `seq > watermark` 的 `dispatched` 或 reason 为「派发失败」的 `needs_human`；其它事件（comment、认领无事件、旧 needs_human）跳过。失败原文在 `haltForHuman` 的 comment 正文（`本节点派发失败：\n` + `err.Error()`），`needs_human` payload 只有 `reason`。
- **为什么承重**：C7 的产品名就是「命令退出码反映这次派没派出去」。水位写错，接缝 1 的三条形态可以在假注入下全绿，活路径仍错。`TestCardDispatchStepReturnsImmediately` 的 mock 本来就没有事件，超时形态盖不住假成功。
- **建议**：方案第 2 点和实现决定写死四句：① 通道 = `openLedger` + `Follow`（或等价的 `EventsFromAsc` 轮询），禁止新 HTTP、禁止 task WS；② POST 前取水位；③ 只认水位之后的两类首态；④ 失败打 comment 正文到 stderr。接缝 1 加：卡上已有一条旧 `dispatched` 时，注入水位之后的 `needs_human`（reason=派发失败）必须非 0 且不得打印旧短号。

### Important

#### I1. 「基线形态」不在 `DispatchSnapshot` 里；接缝还允许「本地 ref 或 origin 二者之一」

- **位置**：现状 `b286.md:42`、方案 C7 第 2 点 `b286.md:72`、故事 2 `b286.md:111`、接缝 1 `b286.md:132`；活代码 `internal/ledger/events.go:115-135`、`internal/ledgerstep/dispatch.go:236-240,333-339`
- **事实**：快照已有 `target` / `branch` / `base` / `base_commit` / `discipline_name` / `executor` / `purpose`。**没有** `local_base_branch`。`LocalBaseBranch` 只存在于 Transport 的 `DispatchOpts`，不下账本事件。CLI 若在 `dispatched` 落账后再调 `WorkBranch()`，第一次实现轮会变成「已经有工作分支」——实际这一拍走的是 origin 补拉路径（`localBaseBranch=false`），标签打反。
- **为什么承重**：方案把「本地 ref / origin」列为成功行必含字段，接缝却接受二者之一，测不准。零上下文实现者会从 `base` 名字猜，或 POST 后再读 `WorkBranch`，首轮实现稳定标错。
- **建议**：二选一写死：① `DispatchSnapshot` 增 `local_base_branch`（append-only JSON 键，不算新 HTTP 契约）；② 成功行不打这个标签，只打 `base` / `branch` / 短号。接缝按选定项锁，禁止「二者之一」。

#### I2. 故事要「新分支名」，方案要「起点分支名」，快照里是两个字段

- **位置**：方案 C7 第 2 点 vs 故事 2；`DispatchSnapshot.Branch` 是新分支，`Base` 是起点
- **事实**：实现轮首发 `branch=cards/<id>-implement`、`base=main`（或空 + `ResolveDefaultBase`）；有工作分支后续轮 `base=工作分支`、`branch` 可能挂号。审阅轮 `branch=cards/<id>-review-N`、`base=工作分支`。只打其中一个，协调者对不上「从哪起、开到哪」。
- **建议**：决议行两者都打，并写明键：新分支 = `branch`，起点 = `base`。空 `base` 允许打「无起点分支」之类，不要用「origin」冒充分支名（那是 I1 的形态标签）。

#### I3. 「基线不存在」列入必堵清单，但缺省 20s 短于执行机 `FetchTimeout=2min`

- **位置**：方案 C7 第 2–3 点 `b286.md:73-74`；`internal/agentd/workspace.go:923-925,1253`；ViaTemplate 本地拒（跨机、无工作分支）在 Transport 之前，远端「起点不存在」在 Transport 里
- **事实**：工作分支跨机、审阅缺工作分支、`EffectiveBaseBranch` 失败都在协调者进程内，远小于 20s。真正的「基线提交在任务仓库中不存在」会在执行机上 `fetch`，上限 2 分钟。20s 到点按正文退回「已受理」退出 0——这族无人值守丢活原样还在。
- **建议**：要么把「基线不存在」从本卡必堵清单拿掉，写进 OOS/roadmap（与「慢路径不阻塞」对齐）；要么短等下限跟 `FetchTimeout`，并解释为何阻塞协调者会话。不要两句话同时成立。

#### I4. C8 `Diff` 失败 vs 白名单未过：落账与路由仍能读成两种活法

- **位置**：方案 C8 第 2/4/5 点 `b286.md:91-98`；活代码 `internal/ledgerstep/node.go:199-202`（解析失败：不落裁决、`haltForHuman`）、`226-247`（先 `RecordReviewVerdict` 再 produces Diff）、`internal/ledgerstep/rounds.go:18-29`（`EvReviewVerdict` 才进 `CountRounds`）
- **事实**：现网 produces 闸在裁决落账**之后**。解析失败在落账**之前**转等人、不记回合。C8 把白名单未过收成 fail→`on_fail`（记回合），把 `Diff==nil`/报错收成 `haltForHuman`。第 5 点只说「先闸门再 `RecordReviewVerdict`，pass 布尔与路由一致」，没说 `haltForHuman` 那条还记不记 `pass=false`。记了会吃掉 `max_rounds=3`；不记才与解析失败同构。
- **为什么承重**：抄 produces 结构会先落 `pass=true` 再改口，正是第 5 点要禁的。把 Diff 失败也 `RecordReviewVerdict(false)` 再 `haltForHuman`，卡上同时有 fail 裁决和等人旗，看板上像回了 implement 又像要人盖章。
- **建议**：写死：
  1. 白名单未过：改内存 `verdict.Pass=false` → 记一条列出越界路径的普通评论 → `RecordReviewVerdict(..., false, ...)` → 走现有 fail 路由（含 `ClearNeedsHumanFrom`、`routeTo(OnFail)`）。**不** `haltForHuman`。
  2. `Diff==nil` 或 `Diff` 报错：不写 `review_verdict`，`haltForHuman("读取审阅改动失败", ...)`，回合数不增加。
  3. 接缝钉事件序：白名单未过的卡，`review_verdict.pass` 只能是 false，且不得先出现 true。

#### I5. C8 开关写的是 `Node.Override.Purpose`，存量测试节点名就是 `review` 且 purpose 为空、`Diff==nil`

- **位置**：方案 C8 第 1 点；接缝 2 `b286.md:136-141`；`deploy/workflows/charter-v4.json:94-108`（charter 审阅列：`name=review` + `override.purpose=review`，**无 produces**，`on_fail=implement`）；`internal/ledgerstep/node_test.go:24-64` `TestReviewStepPassAndFailLoop`（`Name: "review"`，无 Override，无 Diff）；`node_test.go:679-709` `TestNodeStepWithoutProducesDoesNotInvokeOutputHooks`
- **事实**：用 Override 而不用节点名，对 charter 是对的（B183；「图对账」列名不是 review）。但包内大量节点测试 `Name=review`、purpose 空、`Diff==nil`。实现者若按节点名或「凡 Verdict 且无 produces」下闸，这些测试变红，或被改成「先装配 Diff」而把 legacy 行为一并改掉。`dispatch.go` 的有效 purpose 是「节点覆盖优先、否则模板」；`RunOnce` 今天不读模板。只认 Override 则 `bug` 流里只引 `review-generic`、不写 override 的审阅列不会下闸。
- **建议**：保持 Override.Purpose（与 B183、本卡「不改 charter 仓」一致），接缝 2 加负例：`Name=review`、`Override.Purpose=""`、pass、`Diff==nil` → 仍 pass、不调 Diff。点名 `TestReviewStepPassAndFailLoop` / `TestNodeStepWithoutProducesDoesNotInvokeOutputHooks` 继续绿。若要把模板 purpose 也纳入，必须写清 `RunOnce` 怎么取模板、并改上述测试——那是加范围，本卡不要默默做。

#### I6. HTTP 同步拒绝不止 404/400/409 三族；探活已在 202 之前

- **位置**：现状 `b286.md:40`、方案第 3 点把「纪律探活」当成 202 后短等要避开的慢路径 `b286.md:74`；活代码 `internal/agentd/cardstep.go:45-61,132-174`、`ledgerapi.go:481-507`
- **事实**：`startCardStep` 在起 goroutine **之前**就 `ResolveNode` + `resolveStepDiscipline`（本机/远端 `Status`，超时 10s）。探活失败、能力位拒发、`clientForTarget` 失败走 handle 的 default **400**，根本不到 202。202 之后的异步段才是认领、运行锁、`ViaTemplate`。方案第 5 点「入口同步拒绝不变」方向对，但现状少写了这一族，第 3 点用「纪律探活不阻塞短等」当 20s 的理由是过期读数。
- **建议**：现状改成：同步拒绝 = 卡/节点解不开（404/400）、在飞 409、**纪律探活/拒发闸 400**。短等只覆盖 202 之后的 ViaTemplate 族。探活已计入 `CardStep` 的 HTTP 耗时，不要再写进超时理由。

#### I7. skill「改一句」盖不住已经腐烂的排障表；haltEntrypoint 仍是同一条 202 静默洞

- **位置**：方案 C7 第 6 点 `b286.md:77`；OOS `b286.md:169`；`skills/handoff/SKILL.md:409-410,441-445,607`
- **事实**：441 行仍是「提交后立刻返回（202 受理）」。607 行仍写「`--step` 是 202 受理，失败只进 agentd.log，卡上不留任何事件」。B239 之后认领被拒/运行锁被占已经 `haltEntrypoint` 落 comment + `needs_human`（`runner.go:252-264`），发生在 goroutine 里、202 之后。本卡 OOS 这族的 CLI 复述可以接受，但 607 行今天就已经是错的；C7 落地后 ViaTemplate 失败会非 0，认领失败仍超时「已受理」且卡上**有**事件，这行会把协调者指向已经不存在的「零事件 + 只看 agentd.log」。
- **建议**：仓内 skill 至少改两处：① 441 短等首态/超时才「已受理」；② 607 按 B239+C7 重写（认领失败看卡上 comment，不再说零事件）。OOS 入口失败族保持，但排障表不得继续教错。

#### I8. 接缝 1 的生产调用方写成了 `cmd/card.go`

- **位置**：`b286.md:129`；活代码 `cmd/card_dispatch.go:280-281` `return runStepDispatch(...)`。`cmd/card.go` 没有引用。
- **建议**：改成 `cardDispatchCmd` / `cmd/card_dispatch.go`。这是真缝，调用方写错会让 plan 去改不相关文件。

### Minor

#### M1. 现状「基线两条路仍在 `dispatch.go`」把执行机补拉算进了协调者文件

- **位置**：`b286.md:43`；`dispatch.go:236-240` 只置 `LocalBaseBranch` / `ResolveDefaultBase`；origin 补拉在 `internal/agentd/workspace.go`
- **建议**：改成「协调者侧两条旗；补拉发生在目标 agentd」。不挡批准。

#### M2. `TestCardDispatchStepReturnsImmediately` 并没有断言「已受理」这个词

- **位置**：现状 `b286.md:44`；`cmd/card_dispatch_test.go:499-528` 断言的是卡号、节点名「进行中」、`handoff card wait <id>`，以及不含 Outcome/task id
- **建议**：改写超时形态时把「已受理」真正锁住；成功形态明确不得再是纯受理句。

#### M3. 白名单前缀的边界（目录本身、`..`、重命名双路径）没写

- **位置**：方案 C8 第 3 点；`ChangedPaths`（`output.go:31-77`）会同时收入 `rename from` / `rename to`，并剥 `a/` `b/`
- **建议**：`strings.HasPrefix(path, prefix)` 且 prefix 带尾斜杠；路径等于 `docs/ledgers` / `docs/superpowers/ledgers` 也放过；`ChangedPaths` 的输出已是仓内相对 POSIX 路径，不要再做 OS 分隔符转换。重命名只要一侧越界即未过。未跟踪未提交文件本卡看不见（现网 Diff 通道如此），可一句 OOS。

#### M4. `base_commit` 短号：不足 7 位怎么办

- **建议**：空串 →「无 sha」；非空原样截到 `min(7, len)`。不挡批准。

#### M5. 决议行 stdout/stderr 并写

- **事实**：今天受理句在 stdout（`card_node.go:50`）。失败方案指定 stderr + 非 0。成功行若打 stderr，脚本只读 stdout 会以为还是静默受理。
- **建议**：成功决议 stdout；失败原文 stderr；超时「已受理，首态未到」stdout。

#### M6. 「flows 在 charter 仓」对本仓不完全

- **事实**：`deploy/workflows/charter-v4.json` 就是 review 节点的形状副本（purpose=review、无 produces、on_fail=implement）。本卡不改它是对的，现状应承认仓内有这份种子。

#### M7. 图覆盖债

- 与 spec 备注一致：本会话未跑 `codegraph` CLI。符号靠读码。不挡批准。

## 3. 定级：L2 成立，不抬 L3

独立判断与 spec 同结论。

- **不新开 HTTP、不把 step 202 改成等整节点、不改任务/工单状态机、不改 `from_seq` 开闭、不给模板写死机器名**：与代码对照成立。`handleCardStep` 成功支仍是 `StatusAccepted` + `{"ok":true}`（`ledgerapi.go:496-497`）。审阅 `Await` 会阻塞到回合终态（`node.go:130-132`），HTTP 扛不住——弃选成立。
- **C7 是 CLI 呈现层消费已有账本事件**。`DispatchSnapshot` 若加 `local_base_branch` 是账本事件 append-only 键，不进 `CardStepReq`，不是跨子系统 wire。
- **C8 是 `NodeStep.RunOnce` 内部闸**，读已有 `Override.Purpose` 与已注入的 `Diff`（`runner.go:82` 生产路径永远装配）。不新增节点类型字段。
- **两条不要抽框架**：CLI 短等与 NodeStep 闸没有共享抽象。合在一张卡只因为都在 handoff 仓、同一条无人值守回路。抽「节点质量」框架会把 CLI 超时语义和 review 白名单焊到一起，L3 才需要那种冻结——本卡不该付。
- **不是 L1**：水位、超时、白名单、Diff 失败闭、与 `RecordReviewVerdict` 的次序，plan 不会只复述三行。

不因「快照可能多一个键」或「CLI 开始 Follow 账本」抬 L3。`card wait` 已经 Follow。

## 4. 接缝清单：假缝禁令对，真缝覆盖不足

| 缝 | 符号 + 调用方 | 判定 |
|---|---|---|
| 1 `runStepDispatch` | ← `cmd/card_dispatch.go` `cardDispatchCmd`（**不是** `cmd/card.go`）；测试 `cmd/card_dispatch_test.go` | 真缝。无事件超时、失败非 0、空 target 打「本机」方向对。**漏水位/旧 dispatched 负例（C1）、漏超时注入点、基线形态「二者之一」过宽（I1）、调用方写错（I8）。** |
| 2 `NodeStep.RunOnce` | ← `StepRunner.Run`（`runner.go:249`）；测试 `node_test.go` 已注入 `Diff` | 真缝。purpose=review 的空 diff / 台账 / 越界 / implement 负例 / Diff 失败 / 落账布尔，方向对。假缝禁令（不要把白名单纯函数当接缝、不要重写 `ParseVerdict`/produces）合格。**漏 Name=review 且 purpose 空（I5）、漏白名单未过的事件序（I4）。** |

生产 `Diff` 调用方已经在：`StepRunner.diffNode` → `client.Diff` → `ChangedPaths`。C8 复用它不是假缝。

不要为 C7 新增「等首态」HTTP 当接缝。那会破坏 202 契约，也与「不改生产 `client.New` Transport」冲突。测试注入事件的方式：写本机 `ledger.db`（与 mock 202 HTTP 并存，`newCardStepCLIEndpoint` 已经是这种分裂夹具）。

超时注入：cmd 包内可测时长（现网有 `FetchTimeout` 这种包变量先例），缺省约 20s；`TestCardDispatchStepReturnsImmediately` 必须用短超时，否则 `go test ./cmd` 被 20s 拖死。

## 5. 二解（会改可观察行为的句子）

| 句子 | 读法 A | 读法 B | 必须以正文消掉 |
|---|---|---|---|
| 「消费本机卡事件（202 之后）」 | POST 前水位，Follow 新事件 | POST 后 MaxSeq / 扫全卡最后一条 dispatched / 订 WS | C1 |
| 「stdout/stderr 打一行决议」 | 成功也打 stderr | 成功 stdout（与今天受理句同槽） | M5 |
| 「起点分支名」vs 故事「新分支名」 | 只打 `base` | 只打 `branch` | I2 |
| 「有工作分支 → 本地 ref」 | 派发当时的 `LocalBaseBranch` | POST 后 `WorkBranch()` | I1 |
| 「reason 以『派发失败』这一族为准」+「失败原文」 | 打印 `reason` 三字 | 打印 comment 正文里的 ViaTemplate 原文 | C1 建议④ |
| 「先闸门再 RecordReviewVerdict」对 Diff 失败 | 也记 `pass=false` | 不记裁决，只 `haltForHuman` | I4 |
| 「`Node.Override.Purpose == review`」 | 只认覆盖 | 有效 purpose（模板兜底）或节点名 | I5 |
| 「没有事件源」 | mock HTTP 无事件 API | 测试夹具里其实有 ledger.db，只是没有新首态 | 写清：跟账本，无新首态即超时 |

## 6. 弃选：站得住

| 弃选 | 审查意见 |
|---|---|
| 模板/节点写死 `target=linux-01` | 站得住。活模板空 target 是机器中性；`TestViaTemplateEmptyTargetIsLocal` 已锁空串穿过 Transport/挂账/快照。 |
| CLI 缺 `--target` 拒发 | 站得住。回滚 B271。`canonicalCLITarget("")` 已返回空串本机（`card_dispatch.go:166-168`）。 |
| HTTP 改成等整个审阅回合 | 站得住。`Await` 分钟到几十分钟；`startCardStep` 注释同一理由。 |
| 只打日志不改退出码 | 站得住。无人值守看退出码。 |
| 允许 review 新增 `*_test.go` | 站得住。C1.11 就是这条。 |
| 只报不拒 | 站得住。 |
| 按节点名而不是 purpose | 站得住。B183：模板 purpose 与节点 purpose 会分叉；charter 审阅靠 override。 |
| 本卡改 charter-review skill / 合 review 分支当证据 | 站得住。文字已只读；合进去等于把越轨合法化。 |
| 两族抽公共框架 | 站得住。 |

与协调者事先判断一致：本卡不得拒绝空 `--target`，不得硬编码 `linux-01`。正文弃选已写，没有暗门把它们从后门放回来。

## 7. 现状读数核对（逐条）

| 现状句 | 活代码 | 结论 |
|---|---|---|
| `runStepDispatch` 拨本机 agentd，`CardStep` 成功只表示 202，stdout 固定已受理，退出 0 | `card_node.go:32-51`；`client.go:799-811` 只认 202 | 成立 |
| HTTP 装配成功 `{"ok":true}` 202；卡不存在 404、节点不在流 400、在飞 409 | `ledgerapi.go:481-507`；`cardstep.go:46-47` | **半成立**：另有探活/拒发闸同步 400（I6） |
| 异步段 goroutine 跑 StepRunner；ViaTemplate 失败 `haltForHuman`「本节点派发失败」 | `cardstep.go:82-86`；`node.go:180-185` | 成立。另有 202 后、ViaTemplate 前的 `haltEntrypoint`（认领/运行锁），本卡 OOS |
| `DispatchSnapshot` 已有 target/branch/base/base_commit/discipline_name/executor/purpose，CLI 不读 | `events.go:115-135` | 成立；**不够覆盖「基线形态」（I1）** |
| 基线两条路在 dispatch.go | 旗在 `dispatch.go:236-240`，补拉在 workspace | **措辞过满（M1）** |
| `TestCardDispatchStepReturnsImmediately` 锁已受理 + card wait，mock 只回 202 | `card_dispatch_test.go:499-528` | **半成立（M2）**：锁的是 wait 句，不是「已受理」字面；mock 无事件源成立 |
| B271 空 target = 本机，不再「目标机未定」 | `TestViaTemplateEmptyTargetIsLocal`；`CanonicalTarget`；`canonicalCLITarget("")` | 成立。C7 原题「缺 target 当场拒」作废，同意 |
| 仓内 skill 仍写 202 即返回 | `SKILL.md:441-445` | 成立；607 行更旧（I7） |
| review 节点 produces 空，`RunOnce` 只在 Produces 非 nil 时 Diff | `node.go:247`；`charter-v4.json:94-108` 无 produces | 成立 |
| `Override.Purpose` 已能区分 review | `types.go:171-180`；charter-v4 override.purpose=review | 成立 |
| `Diff` 已注入，produces 路径在用 | `runner.go:82,378-402` | 成立。生产路径即使无 produces 也装配了 Diff，只是 RunOnce 不调用 |
| 平台层落台账已删，测试名对 | `platform.go:16-19`；`TestComposeEnabledWithEmptyBaseOmitsLedgerFromPlatformLayer` | 成立 |
| linux-01 agentd `016aef7e` | 本工作树无该二进制 | 未独立核；白名单含台账目录的理由自洽，不挡 |
| `charter-default` target 空 | 类型可空；B271 测试锁空串本机；活模板在账本不在 git | 采信台账；本卡不得写 linux-01 成立 |

协调者「C7 剩余不是缺 target」：同意，且已被 `TestViaTemplateEmptyTargetIsLocal` 钉死。

## 8. Out of Scope

永不做六条（写死 linux-01、空 target 再当错、`--target 本机` 魔法名、HTTP 等整回合、允许改测试文件、合 review 分支）与活代码约束一致，合格。

本期不做、后续要做三条：

- 新 agentd 全员部署后撤台账白名单：合理。旧平台层仍可能逼执行者写台账。
- `--step` 打「领先 N 个提交」：对。那是执行机读数，20s 短等未必拿得到。
- 入口失败（认领/运行锁）是否在 CLI 复述 `haltEntrypoint` 原文：明确 OOS 可以，但必须修 skill 607（I7），否则文档继续把这族说成「卡上零事件」。

本卡不做 C11 / 重装 agentd / B281：合格。C11 在 charter 仓，两张 L2 拆开是对的。

## 9. 批准前最小补丁（只改 spec 正文，不是代码）

1. **C1（挡批准）**：写死短等通道 = 本机账本 `Follow`/`EventsFromAsc`；**POST 前**水位；只认水位之后的 `dispatched` 与 reason=`派发失败` 的 `needs_human`；失败 stderr 打 comment 正文。接缝 1 加旧 `dispatched` + 新派发失败的负例。禁止新 HTTP、禁止用卡上历史快照当本次成功。
2. **I1 + I2**：基线形态要么进快照键要么从成功行拿掉；新分支名与起点分支名都打，不要「二者之一」。
3. **I3**：基线不存在（执行机 fetch 族）要么移出本卡必堵清单，要么把超时与 `FetchTimeout` 对齐，消除 20s 条款的自相矛盾。
4. **I4**：写死 Diff 失败不落 `review_verdict`；白名单未过记 `pass=false` 走 `on_fail`，事件序不得先 true 后改口。
5. **I5**：接缝 2 加 `Name=review` 且 purpose 空的负例；点名两条存量测试继续绿。
6. **I6 + I8**：现状补上探活同步 400；接缝 1 调用方改成 `cmd/card_dispatch.go`。

I7（skill 607）、M1–M6 建议一并写入，不挡在 C1 修好之后的批准。

方向保持：HTTP 仍 202；空 target 仍本机；C7 在 CLI 短等首态；C8 在 `RunOnce` 用 purpose+Diff 机械拒 pass；两族不抽框架；不改 charter 仓。
