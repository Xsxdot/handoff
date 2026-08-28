# B283 实现审查（悬浮窗终端 tab 累积根治）

审查对象：分支 `fix/B283-float-terminal-dup`，范围 `git diff 54bcc5678..HEAD`（计划落稿 54bcc5678 之上 5 个实现/文档提交），HEAD `5f1b6a99a`。
工作树：`~/.handoff/worktrees/manual/B283`（审查时 `git status` 干净）。
对照物：spec `docs/superpowers/specs/b283.md`（r1）、plan `docs/superpowers/plans/b283-plan.md`、台账 `docs/superpowers/specs/b283-ledger.md`、spec 审查 `docs/superpowers/reviews/b283-spec-review.md`。
审查者：独立实现审查人（charter 流 review 节点，只读；与出稿人/实现者无关联）。一切以亲手读 diff、读码、跑测、变异为准；未采信任何一方自述。
日期：2026-08-28

## 1. 总判

**可合。**

五个 task 的全部计划代码块与实现逐字对上（机械 diff 验证，见 §2）；实现者自报的 2 条实质偏离均属实、均为执行层语法/时序问题、零行为差异，另有「其余照抄」经逐块机械比对证实。spec 方案 1/2/3/4 与 spec 审查实现层义务（I1/I2/M1/M4/M5）全部指到实现行。无 scope drift（文件集、新符号、删除项全部在计划红线内）。测试有牙经本人两次独立变异（位置与实现者不同）复验：编译过、看守卫用例真翻红、还原后逐字节一致。收口数字独立复跑吻合：typecheck exit 0、web 全量 1180 passed、lint 5 error 确在 diff 之外。无 Critical、无 Important；Minor 2 条记账不阻塞。

## 2. 逐条 plan 对账

机械比对方法：用 sed 抽取计划代码块与实现对应区段做 `diff`。结果：

| 计划块 | 实现位置 | 机械比对结果 |
|---|---|---|
| Task 1 RestoreInput 整体替换 | `restore.ts:57-70` | 逐字一致 |
| Task 1 machineOkSet | `restore.ts:121-135` | 逐字一致 |
| Task 1 buildRestore 开头 + ① 门控行（含注释） | `restore.ts:186,198-205` | 逐字一致 |
| Task 2 ③ 循环（守卫行 + 四行注释 + adopted++ 在守卫后） | `restore.ts:256-281` | 逐字一致（守卫 `if (s.base_kind === 'home' && s.machine !== '') continue` 在 263，`adopted++` 在 264） |
| Task 3 RestoreResult 整体替换（含 purged 字段注） | `restore.ts:72-90` | 逐字一致 |
| Task 3 ② 区整段替换 | `restore.ts:211-248` | 逐字一致 |
| Task 3 return 行 | `restore.ts:289` | 逐字一致 |
| Task 3 useWorkbenchSync 日志块 | `useWorkbenchSync.ts:131-138` | 唯一差异=偏离 B（引号），键文本含空格逐字一致 |
| Task 4 转正回路用例 + 头注「职责」行补句 | `restore.test.ts:34,341-369` | 逐字一致 |
| Task 1–3 全部测试用例（3+1+3 条）与共享夹具 | `restore.test.ts:36-59,220-340` | 逐字一致 |
| Task 5 六处替换 | `Shell.tsx:217-220,270-273,301-305,655`、`TerminalTab.tsx:8-9,616-618` | 六处逐字一致；锁子串「在服务端已经不存在了」在位（Shell.tsx:655） |
| Task 4 删除 `b283-redloop.test.ts` | 已删（60 行） | 属计划文件集 |

### 偏离核实（实现者自报 3 条）

- **偏离 1（夹具后移避 noUnusedLocals）——属实，零行为差异**。按提交核对：`baseM`/`machine()` 落 Task 1 提交 `8e39a6228`；`homeSess`/`dockRaw`/`HomeTab` import 落 Task 2 提交 `2c3251851`。Task 1 的三条用例确实只消费 `baseM`/`machine`，计划原文把四个夹具全钉在 Task 1 会触发 TS6133（noUnusedLocals），偏离理由成立；文件终态与计划一致（四夹具都在 `const VIEW` 之前），断言零改动。台账偏离记录与事实吻合。
- **偏离 2（日志键加引号）——属实，零语义差异**。计划 3(d) 代码块的裸标识符键 `清除的外来悬浮窗 tab`（含空格）按原样落地必然 TS1005——计划文本本身不可编译。实现 `'清除的外来悬浮窗 tab': r.purged`（`useWorkbenchSync.ts:134`）运行时键文本与计划逐字一致（含空格）。机械 diff 证实这是日志块唯一差异。
- **偏离 3（其余照抄）——属实**。上表全部「逐字一致」即其证据。

## 3. spec 义务兑现

| spec 条目 | 实现行 | 结论 |
|---|---|---|
| 方案 1：home 收编仅限本机 | `restore.ts:263` 守卫（`adopted++` 在其后，外来不计数） | 已兑现，Task 2 用例 + 两条既有本机收编用例双面锁 |
| 方案 2：存量外来 tab 一次性清除 | `restore.ts:237-244`（kept 过滤、activeId 显式置 null 239、windowOpen 收拢 244）；`dockPersist.ts` 未动、`DOCK_PERSIST_VERSION` 未 bump（不在 diff 内） | 已兑现；清除发生在合成层，decode 照旧收旧数据 |
| 方案 3：machines 入参 + 两处 prune 门控 | 入参 `restore.ts:65` + 传出 `useWorkbenchSync.ts:108`；门控表 `restore.ts:129-135`；中央区按 `decoded.base.machine`（204）；悬浮窗按 `t.machine` 并入 effectiveLive（224-230）；本机 `''` 无条件置位（130） | 已兑现，两处归属来源与 spec 审查 I2 的钉法一致 |
| 方案 4：话术订正六处 | `Shell.tsx:217/270/301/655`、`TerminalTab.tsx:8/616` | 已兑现，逐字与计划一致 |
| spec 审查 I1（activeId 悬空 + 空壳浮窗） | `restore.ts:239`（显式置 null）→ 既有兜底 `restore.ts:285-287` 重指；`restore.ts:244`（清空收窗） | 已兑现，两条专测锁（Task 3 第二、三条） |
| spec 审查 I2（归属来源不对称） | dock=`tab.machine`（226）、workbench=`base.machine`（204），注释均写明理由 | 已兑现 |
| spec 审查 M1（open1 断言反转） | `restore.test.ts:352` `toMatchObject({ id: 'h1', sessionId: 'H1' })` 保引用 | 已兑现，非原样搬 |
| spec 审查 M4（home@machine 分支保留） | `restore.ts:41-43` 未动（不在 diff 内）；`restore.ts:258-262` 注释写明保留理由 | 已兑现 |
| spec 审查 M5（1008 措辞分开） | `restore.ts:200-201,219` 写作「连接错误 / 1008 出口」两条路 | 已兑现 |

## 4. 维度化裁决表

| 维度 | verdict | 证据 |
|---|---|---|
| plan 覆盖完整性 | **通过** | §2 对账表：五个 task 全部代码块/断言/签名/注释逐字落地；偏离 2 条均属实且零行为差异。五项检查中的接缝覆盖表与实际测试一一对应（8 条新用例 + 转正回路全在 `restore.test.ts`，入口符号全是 `buildRestore`，无内部锁） |
| scope drift | **无** | `git diff 54bcc5678..HEAD --name-only` = 计划红线 5 文件 + 台账 + 删除的 redloop，无计划外文件；新符号仅 `machineOkSet`（私有）、`RestoreInput.machines`、`RestoreResult.purged`、② 区局部 `effectiveLive`/`kept`/`activeId`/`purged`——全部在计划代码块内；无新导出、无新依赖、无配置改动。台账追加行是 implement 节点法定义务 |
| 架构法合规 | **通过** | 判据收口 `buildRestore` 合成层一处（方案宣示）；两个 prune 函数签名未动（`persist.ts:203`、`dockPersist.ts:133` 均不在 diff 内），门控在调用处——与台账关键签名决定一致；`buildRestore` 生产调用方唯一（`useWorkbenchSync.ts:105`），两 prune 生产调用点唯一（restore.ts:204/229，grep 全仓核实），门禁无绕过面；纯函数层不碰 React/window，无分层穿透 |
| 测试有牙 | **已验（缝级，本人独立变异）** | 见 §6：变异 A（取反中央区门控）与变异 B（purge 过滤失效）均编译过、看守卫用例翻红；实现者自己的变异（收编守卫删右半）是第三处。三处门控（中央区门控 / purge / 收编守卫）各有至少一次变异翻红背书 |
| 日志与注释覆盖 | **通过** | 日志：`console.debug` 新增键文本与计划逐字一致（含空格，仅语法形态加引号）。注释：计划钉死的全部「为什么」注释照抄在位（machineOkSet 头注 121-128、RestoreInput.machines 字段注 61-64、① 门控注 199-203、② 两段注 217-223/231-236、③ 守卫注 258-262、purged 字段注 85-87、回路叙述注 restore.test.ts）；无一句复述「是什么」的废话注 |
| 序列化边界 | **本次无新字段跨边界** | `machines` 消费既有 wire 字段（`internal/proto/pty.go` machines 已在），契约夹具 `PtySessionsResp.json` 未动继续锁 wire↔TS；`purged` 只进 console.debug，不落盘不进渲染；dock 载荷编解码未动（不 bump 版本），既有 roundtrip 测试继续覆盖 |
| 冻结物触碰 | **无触碰** | spec/plan/契约内容未被本轮发现推翻或增补；两条执行偏离（夹具时序、日志键引号）是执行层语法/时序，不改语义，已按 implement 纪律落台账（b283-ledger.md:33,39）。计划文本本身未回写这两处（plan 3(d) 代码块仍不可编译、Task 1 仍含会触发 TS6133 的夹具步骤）——是否回写归协调者裁决，见 Minor-2 |

## 5. 对抗审查（缺陷族设问，逐项给结论与理由）

**通用五族**：

- **生命周期/状态机中断**：purge/门控发生在每次恢复、输入是服务端落盘原文，崩溃在写回前则下次对原文重做，无半态；写回收尾由既有 flush 闸门负责（本卡未触碰）。机器永久注销时中央区该机器引用永剥不了（保守，spec 表态过）——可显式重开，非孤儿。无风险。
- **静默失败/误导报错**：保住引用的 tab 挂载走 attach 失败路径（连接错误 / 1008），都有「重开」出口，不静默自建（`TerminalTab.tsx:379` 的 `if (!id)` 支不进，sessionId 在）。无「报成功但没做」窗口：purged/pruned/adopted 三个统计量语义见下。无风险。
- **跨平台假设**：纯 web TS 纯函数 + 字符串字面量，无平台差异面。无。
- **假红/假绿**：全部新用例入口符号是 `buildRestore`（缝 1），断言输出形状（sessionId 有无、tab 数、activeId、windowOpen、统计量）即调用方 hydrate 依赖的面；每条「不剥」都有配对「照常剥」（Task 1 第一条、Task 3 第一条）。三处变异翻红证实非假绿。无。
- **门禁绕过**：无新写路径；machines 是同一份扇出响应的只读投影，两处 prune 与收编判据读同一输入，无 TOCTOU；`buildRestore`/两 prune 的生产调用点各唯一（§4 grep 证据），无第二入口绕门。无。

**任务指定对抗项逐个推演**：

1. **门控过度拦截（本机真死会话还被剥吗）**：不被拦。`machineOkSet` 无条件把 `''` 置位（`restore.ts:130`），本机 base 行（204）与本机 dock tab（machine=''，不在 effectiveLive 增补循环命中范围）都按 ok 走正常 prune——Task 1 第一条、Task 3 第一条 + 14 条既有用例锁死；我的变异 A（取反）正是把这一面打红的实验。远端 ok=true 机器的真死照剥（同两条专测）。服务端前提独立复核：`pty_api.go:189` 恒以 `{Name:"", Ok:true}` 领衔（亲手读码）。
2. **purge/prune 统计语义互染**：不互染。`pruneDeadDockSessions` 只剥引用不删 tab（`dockPersist.ts:133-141` 头注+实现，亲手读码），故 `gated.length === d.tabs.length` 恒成立，`purged = d.tabs.length - kept.length` 恰等于 machine!=='' 的 tab 数；`pruned` 在 purge 之前结算（228-230），不受清除影响。唯一交叠形态——外来 tab 引用的会话真死且机器 ok：prune 剥引用（pruned+1）、随后整 tab 被清（purged+1），两个统计量各自如实计自己的事件，且计划「次序承重说明」显式钉了 prune 先行正是为此；Task 3 第二条 `pruned===0`（活着的外来会话）+ `purged===2` 双断言钉住区分度。
3. **activeId/windowOpen 边界形态**：全外来 → activeId 置 null + windowOpen false + tabs 空（专测）；部分外来且 activeId 指外来 tab → 置 null 后既有兜底（285-287）重指到 `h2`（专测）；activeId 指本机存活 tab → `kept.some` 命中、原值不动（Task 3 第一条）；decode 即空 → decodeDock 校验（`dockPersist.ts:111-112`）保证 activeId 悬空的 payload 直接整份丢弃进 null 分支，`tabs=[]` + `activeId=null` 合法载荷会命中 `kept.length===0` 收窗（计划文本显式声明的兜住范围，`closeTab` 写不出该形状，代码行本身被全外来专测覆盖）；activeId 原值 null 且有存活 tab → 既有兜底重指。无未处理形态。
4. **machines 形状**：undefined → 按空表（专测第三条；`useWorkbenchSync` 传 `sessResp.machines` 可为 undefined，全量绿）；空数组 → 与 undefined 同路（循环零次）；重复机器名（同名一 ok 一不 ok）→ ok=true 胜出（`ok.add` 单向），服务端契约每机器恰一行（`pty_api.go` 构造侧），形状生产不可达，取乐观向无孤儿风险；error 非空但 ok=true → 只读 `m.ok`，按「答上来了」处理，与契约（失败行 Ok=false）一致。均无风险。
5. **dock 侧 effectiveLive 的理论边界**：同一 sessionId 同时出现在外来 tab（机器不 ok）与本机 tab 上时，本机 tab 对真死会话的引用也会被保住。该形状要求两个 tab 共享一个会话——生产写路径不可能（会话 attach 单 tab；收编戳 `machine: s.machine` 与 tab.machine 同源），且方向保守（保引用可显式重开，非静默自建）。理论-only，不构成缺陷。
6. **越轨扫描**：新符号清单见 §4 scope drift 行——全部在计划代码块内；五提交逐个 `--stat` 核对，无计划外文件、无顺手改其他测试断言、`persist.ts`/`dockPersist.ts`/`useHomeDock.ts`/`types.ts`/Go 侧零触碰（均不在 diff）。

## 6. 亲手跑测记录（review 人本机，web/ 下）

| 步骤 | 命令 | 结果 |
|---|---|---|
| 缝级三文件 | `npx vitest run src/app/workbench/restore.test.ts src/app/workbench/useWorkbenchSync.test.ts src/app/homedock/dockPersist.test.ts` | **3 files / 41 passed**（变异前，全绿） |
| 类型 | `npm run typecheck`（tsc -b） | **exit 0** |
| 全量 | `npx vitest run` | **Test Files 109 passed (109) / Tests 1180 passed (1180)**——与实现者自报一致 |
| lint | `npm run lint` | 25 problems（5 errors, 20 warnings）；5 error 全在 `src/api/pty.ts`(1)、`src/app/flows/NodeEditor.test.tsx`(1)、`src/app/workbench/terminalHostResponse.ts`(3)——三者均不在 `git diff 54bcc5678..HEAD --name-only` 清单内，基线既有欠账，实现者自报属实 |
| 变异 A（我的，非实现者位置） | 取反中央区门控：`restore.ts:204` `machineOk.has(...)` → `!machineOk.has(...)` | 第一段判定 typecheck **exit 0**（编译过）；第二段 `npx vitest run restore.test.ts` → **5 failed \| 17 passed (22)**，翻红含看守卫的三条新用例（ok 照剥 / ok=false 不剥 / machines 缺席不剥）+ 2 条既有本机剥引用用例——门控行有牙 |
| 变异 B（我的） | purge 失效：`restore.ts:237` `const kept = gated.filter((t) => t.machine === '')` → `const kept = gated` | typecheck **exit 0**；restore.test.ts → **3 failed \| 19 passed (22)**，翻红的恰是看守 purge 的三条（全外来清除 / activeId 重指 / 红色回路 open1）——purge 有牙 |
| 还原 | 每次变异后 `git checkout -- restore.ts` | `git status --porcelain` 0 行、`git diff` 0 行、HEAD `5f1b6a99a`；三文件回跑 **41 passed** |

与实现者变异（Task 2 收编守卫删右半，台账 b283-ledger.md:41）合计：三条门控路径（中央区门控、purge 过滤、收编守卫）各有独立变异翻红证据。

## 7. Findings

### Critical

无。

### Important

无。

### Minor

1. **spec 状态行未随批准翻转**（diff 之外，spec 节点收尾欠账，不阻塞本卡）：`docs/superpowers/specs/b283.md:3` 仍写「状态：待用户批准」，而流内该 spec 已批准并进入实现。批准侧动作（roadmap 三条已落账 `docs/roadmap.md:467-471`，已核实）做完了，状态行漏翻。归 spec 节点补一行。
2. **计划文本未回写两条已发生的执行偏离**（冻结物层面记账，归协调者裁决）：plan Task 1 仍指示在 Task 1 一次加入四个夹具（照做会触发 TS6133）、plan 3(d) 日志键仍是含空格的裸标识符（照抄即 TS1005 不可编译）。两条偏离的终态与语义都无害且已落台账（b283-ledger.md:33,39），但零上下文执行者将来照 plan 重放会在同两处绊住。审查纪律限制审查者不得单方改 plan，记此供协调者定夺（回写 plan 或留台账为凭皆可）。

## 8. 结论

五维全过、无越轨、测试有牙经独立变异证实、收口数字复跑吻合。**可合**。两条 Minor 均不阻塞：Minor-1 归 spec 节点补状态行，Minor-2 由协调者裁决 plan 文本是否回写。acceptance 真机项按 plan 文末清单执行（升级首启外来 tab 消失属设计内，勿报回归）。
