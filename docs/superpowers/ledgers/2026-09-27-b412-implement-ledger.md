# B412 实现节点台账（charter:implement）

> 卡 B412 · 分支 `cards/B412-charter-2` · 基线 HEAD `5e975edc`（plan 节点产出）
> 纪律：TDD（先红后绿）、变异自验、命令必须亲跑到结果才落结论。
> 临时文件一律落 `$TMPDIR`（= /root/.handoff/tmp/341d4c0c）。

## L1 环境与基线（本节点亲跑）

- `git status --short` → 空（工作树干净）；`git branch --show-current` → `cards/B412-charter-2`；HEAD `5e975edc`。
- `codegraph --repo . sym CardsPage / cardsInColumn / normalizeBoardLayout` → 三条均命中 `d_web_cards`，anchor ok（CardsPage.tsx:82、columns.ts:56、columns.ts:24）。
- `ls web/node_modules` → 不存在；`npm ci`（web/）→ 成功（输出尾部 `To address all issues, run: npm audit fix`）。
- `npx vitest run src/app/cards`（web/，改动前基线）→
  `Test Files  10 passed (10)` / `Tests  103 passed (103)` / `EXIT=0`。
- `npx tsc -b`（web/，改动前基线）→ 见 L2。

## L2 基线静态检查（改动前）

- `npx tsc -b`（web/）→ `TSC_EXIT=0`。
- `npm run lint`（web/）→ `✖ 27 problems (0 errors, 27 warnings)`，`LINT_EXIT=0`（27 条 warning 为存量）。

## L3 T1 红锚（缝 3）：既有符号可编译、断言红

- 动作：把 plan §6 T1 的 `describe('B412 合并视图按各流当前版本看板配置归列')` 整段追加到
  `web/src/app/cards/CardsPage.test.tsx` 末尾（只用既有生产符号，不引新符号）。
- `npx vitest run src/app/cards/CardsPage.test.tsx -t 'B412'` → 原文：
  `TestingLibraryElementError: Unable to find an element with the text: spec 卡.`
  失败点 `src/app/cards/CardsPage.test.tsx:468`（`within(columnSection('沟通中')).getByText('spec 卡')`），
  DOM 里「沟通中」列内容为 `（空）`——spec 卡此刻落「进行中」。
  `Tests  1 failed | 18 skipped (19)`。
- 判定：断言红（非编译红、非 typo），失败原因 = 功能缺失（合并视图不读流 board 映射）。

## L4 放弃的尝试（TDD 违规自纠）

- 先手改了 `columns.ts`（直接落 T2 的三个新导出 + 签名扩展），随后才想起来缝 1/2 的测试还没写——
  违反「无失败测试不写生产代码」。处置：`git checkout -- web/src/app/cards/columns.ts` 整体回滚，
  改走「先测试红 → 空壳 → 断言红 → 实现」。回滚后 `git status` 只剩 T1 测试与本台账。

## L5 T2 缝 1/2：先红后绿

- 动作序：改 `columns.test.ts` import（加 `DEFAULT_BOARD_COLUMNS`/`mergeIntoDefaultColumns`/
  `mergedLayoutFor` + `import type { BoardLayout, CardLayoutResolver }`）并追加
  `describe('B412 合并视图按各流当前版本 board 归列')` 五条用例 → 此时 `columns.ts` 已回滚为空。
- 首红（编译红形态）：`npx vitest run src/app/cards/columns.test.ts` →
  `TypeError: mergedLayoutFor is not a function`（`columns.test.ts:104`），5 failed / 12 passed。
  已核实是符号缺席、非拼写错（名字与 plan §5 Produces 逐字一致）。
- 落空壳（行为=现状「不读流配置」：`mergeIntoDefaultColumns` 只 `{...layout}`、
  `mergedLayoutFor` 只 `defaultBoardLayout([card.status])`；同时落 `CardLayoutResolver`/
  `WorkflowBoardLookup` 类型与 `cardsInColumn`/`visibleColumns` 签名扩展）→ **断言红**：
  `AssertionError: expected '进行中' to be '沟通中'`、
  `expected [ '收集', … ] to deeply equal [ '代办', … ]`、
  `expected [] to deeply equal [ 'Bs' ]` → `Tests  3 failed | 14 passed (17)`。
  （同轮 `npx tsc -b` 报 `columns.ts(58,3): error TS6133: 'lookup' is declared but its value is never read`
  ——空壳期的未用参，实现落地后消失。）
- 实现（三导出的真实体）→ `npx vitest run src/app/cards/columns.test.ts` →
  `Test Files  1 passed (1)` / `Tests  17 passed (17)`；`npx tsc -b` → `TSC_EXIT=0`。

## L6 T3 接线（CardsPage.tsx）：T1 转绿

- 按 plan 改动五~九落地：import 补 `DEFAULT_BOARD_COLUMNS`/`mergedLayoutFor` + 类型 import；
  `fetchFlows` 成功路径加 `cards.flows.loaded`；新增 `layoutForWorkflow`/`cardLayoutResolver`/
  `displayedColumns`（合并视图恒默认五列）/`missingWorkflows`+`cards.board.workflow.missing` Warn；
  看板渲染与 `nodeTag` 改用 `cardLayoutResolver`；抽屉 `boardLayout={selectedCard ? cardLayoutResolver(selectedCard) : undefined}`。
- **偏差 1（测试侧，必改否则撞车）**：plan T1 原样附带的 `columnSection` 用裸
  `screen.getByText(name)`，取「进行中」时撞上状态词表 chip 行的同名词 →
  `TestingLibraryElementError: Found multiple elements with the text: 进行中`（`CardsPage.test.tsx:435`）。
  改为 `screen.getByText(name, { selector: 'section header span' })` 后再 `closest('section')`
  （只认列头列名，chip 行不在 `section header span` 下）。属测试取元素方式修正，断言本身一字未改。
- 修后：`npx vitest run src/app/cards/CardsPage.test.tsx -t 'B412'` → `Tests  1 passed | 18 skipped (19)`。
- 目录全量：`npx vitest run src/app/cards` → `Test Files  10 passed (10)` / `Tests  109 passed (109)`
  （基线 103 + 新增 6）；`npx tsc -b` → `TSC_EXIT=0`；`npm run lint` → `0 errors, 27 warnings`（与基线同数）。

## L7 T4 不误伤回归 + 变异复验

- 全量：`npm test`（web）→ `Test Files  135 passed (135)` / `Tests  1473 passed (1473)` / `TEST_EXIT=0`；
  `npx tsc -b` → `TSC_EXIT=0`；`npm run lint` → `✖ 27 problems (0 errors, 27 warnings)`、`LINT_EXIT=0`（warning 数与基线一致）。
- **变异 1（合并视图退回默认布局）**：
  - 先按 plan 原文把合并分支改 `() => boardLayout`：`npx tsc -b` →
    `CardsPage.tsx(15,92): error TS6133: 'mergedLayoutFor' is declared but its value is never read`，
    **MUT_TSC=2 编译不过 → 该发不算数**（vitest 不做类型检查，测试虽红但属无效读数）。
  - 替换为可编译等价变异：合并分支改 `(card) => mergedLayoutFor({ ...card, workflow: '' }, layoutForWorkflow)`
    （语义=强制查不到流，退回默认映射；两个标识符仍在用）。改前断言 `count(old)==1` → `1`。
  - `npx tsc -b` → `MUT_TSC=0`；`npx vitest run src/app/cards/CardsPage.test.tsx -t 'B412'` →
    `TestingLibraryElementError: Unable to find an element with the text: spec 卡.`、`Tests  1 failed`（与 T1 基线红同形）。
  - 回滚备份文件 → `npx tsc -b` `RESTORE_TSC=0`、用例 `Tests  1 passed`。
- **变异 2（mergeIntoDefaultColumns 不收敛五列）**：
  - 先按 plan 原文想整句换 `return { ...layout }`：命中唯一断言 **失败**
    （`count(columns: [...DEFAULT_BOARD_COLUMNS], …) == 2`，`defaultBoardLayout:21` 与
    `mergeIntoDefaultColumns:49` 各一处）——若照改会打中第一处、测错地方。未落盘。
  - 换长锚（带 `state_to_column[state] = DEFAULT_BOARD_COLUMNS.includes(column) …` 前文）→ `count=1`。
  - 变异体：`return { ...layout, state_to_column }`（保留收敛循环，只去掉列集合收敛；
    plan 原文的 `{...layout}` 会连带让 `state_to_column` 变量未用 = 编译不过，属同一类无效发）。
  - `npx tsc -b` → `MUT2_TSC=0`；`npx vitest run src/app/cards/columns.test.ts` →
    `× 映射目标不在默认五列的状态落「进行中」（D3）`、`× 合并视图列集合恒默认五列，单流视图列集合用该流自身列名`
    → `Tests  2 failed | 15 passed (17)`（与 plan 预言的两条一致）。
  - 回滚 → 目录全量 `Tests  109 passed (109)`、`git status` 只剩四个正式改动文件 + 本台账。

## L8 图覆盖债（本节点）

- `codegraph --repo . sym CardsPage / cardsInColumn / normalizeBoardLayout` → 均命中 `d_web_cards`（基线图）。
- **未命中**：`sym mergedLayoutFor` → `符号 "mergedLayoutFor" 不在图中（图未覆盖或名字有误）`
  ——本卡新增符号不在基线图里，调用面回落 grep 复核（`CardsPage.tsx` 与 `columns.test.ts` 各 1 处）。
- **未命中**：`who-calls cardsInColumn` → `outputEdges=0`（只回焦点本身，无调用边）；
  `flow d_web_cards` → `符号 "d_web_cards" 不在图中`。与 plan §图覆盖债同源：baseline 图对本域
  TS 函数缺 call/flow 边，记为既有图覆盖债，非本卡引入。

## L9 收口自审

- 错误分支日志：flows 失败沿用既有 `setFlowsError` 横幅；新增 `cards.board.workflow.missing`（Warn，含流名列表）；
  成功路径 `cards.flows.loaded`（含 workflows/withBoard 计数）。纯逻辑层 `columns.ts` 按 plan 显式声明不记日志。
- 文件头/导出注释：`mergeIntoDefaultColumns`、`WorkflowBoardLookup`、`mergedLayoutFor`、
  `CardLayoutResolver`、`asLayoutResolver` 均带「参数/返回/为什么」；`columns.ts` 文件头职责不变。
- 触及包测试绿、全量编译过、全量测试过、lint 无新增 error（见 L7）。
- 与 plan §5 Interfaces 一致：Produces 四条逐字落地；Consumes 既有签名一字未改。
- 未验证项：真机清单（plan §11）归协调者，本节点未跑。

## L10 提交事实（历史读数）

- `git add web/src/app/cards/{CardsPage.tsx,CardsPage.test.tsx,columns.ts,columns.test.ts} docs/superpowers/ledgers/2026-09-27-b412-implement-ledger.md && git commit -m "fix(B412): …"` → 原文：
  `[cards/B412-charter-2 0ffa146e] fix(B412): 看板合并视图按各流当前版本看板配置归列——columns 逐卡解析 + CardsPage 接线 + 缝1/2/3 测试`
  ` 5 files changed, 328 insertions(+), 10 deletions(-)`
  ` create mode 100644 docs/superpowers/ledgers/2026-09-27-b412-implement-ledger.md`
- 随后把本台账（含本节）并入同批提交：`git add docs/superpowers/ledgers/2026-09-27-b412-implement-ledger.md && git commit --amend --no-edit`。
  amend 会换 hash——收口判据是 `git status` 干净，不是文件里的 hash 等于 HEAD。
