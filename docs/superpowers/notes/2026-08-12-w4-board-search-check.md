# W4 看板干预态 + 左栏搜索 走查记录

实现计划：`docs/superpowers/plans/2026-08-12-w4-board-intervention-and-sidebar-search.md`（Task 1–9，9 个提交）。
Spec：`docs/superpowers/specs/2026-08-12-w4-board-intervention-and-sidebar-search-design.md`。
分支：`feat/w4-board-search`，基于 `850ae61a`（W4 集成分支）。

> 执行者无浏览器。spec §12 里需要肉眼看页面的判据（第 1、3、5、9 条）如实标
> 「未验（无浏览器）」，不猜「通过」。其余判据由对应测试用例背书。

## 1. spec §12 十条验收判据逐条结论

| # | 判据 | 结论 | 背书 |
|---|---|---|---|
| 1 | 与 `implementation-task-board-final.png` 并排看：Review 列两张卡片状态文字均为琥珀「圆点 + 文字」 | **未验（无浏览器）** | 结构由 `TaskCard` + `TaskState` 落地，className 断言见下：BoardPage.test.tsx「状态用圆点 + 文字呈现，文案与状态对得上」 |
| 2 | `waiting_answer` 卡片上「等你答复」只出现一次 | 已验 | BoardPage.test.tsx「waiting_answer 的「等你答复」只出现一次」（B75 回归防线） |
| 3 | `waiting_answer`/`waiting_review` 卡片带琥珀边框 + 左侧竖条；`failed` 红色区分且不带琥珀 | **未验（无浏览器）** | className 断言：BoardPage.test.tsx「两个干预态都带卡片级干预标记」「failed 保持红色区分，且不带干预标记」 |
| 4 | 任务详情页顶栏两个干预态 Badge 为琥珀色 | 已验 | columns.test.ts「详情页 Badge：两个干预态改用 intervention 档」+ badge.test.tsx「intervention 变体用琥珀实色背景 + 白字」；`TaskHeader.tsx` 零改动（spec §4.3） |
| 5 | 左栏搜索框 + 「项目 N」；四类字段（项目/机器/目录/任务名）各能命中，命中项祖先链自动展开 | **未验（无浏览器）** | 过滤逻辑由 search.test.ts 十条契约覆盖（四类命中、祖先链可见、子树裁剪） |
| 6 | 「项目 N」跟随过滤；零结果出空态文案 | 已验 | ProjectTree.test.tsx「零结果时出空态文案，N 归 0」「搜项目名：N 仍是 1，整棵子树可见」「渲染搜索框与「项目 N」，N 默认是项目总数」 |
| 7 | `⌘K` 聚焦搜索框，`Esc` 清空并失焦 | 已验 | ProjectTree.test.tsx「⌘K 聚焦搜索框」「Ctrl+K 同样聚焦（非 mac）」「输入框内 Esc 清空并失焦」「单独按 k 不聚焦（不劫持普通输入）」 |
| 8 | 清空搜索后，此前手动折叠的节点仍折叠 | 已验 | ProjectTree.test.tsx「清空搜索后，此前手动折叠的节点仍是折叠的」（钉 spec §5.5 旁路而非清空） |
| 9 | 左栏任务行圆点颜色与该任务在看板上的基调一致 | **未验（无浏览器）** | 左栏：ProjectTree.test.tsx「左栏任务行的圆点跟随任务状态」；看板：StateDot.test.tsx + BoardPage.test.tsx |
| 10 | 回归全绿 | 已验 | 见下第 3 节，四条命令全绿 |

PTY 合并后补验（spec §6）：终端聚焦时按 `⌘K` 不会把焦点抢到左栏搜索框——**待 `feat/w4-pty-terminal` 合并后真机确认**。

## 2. 改动面（相对本分支基线 `8a06cb6`，9 个提交）

```
web/src/app/board/BoardPage.test.tsx   （新增）
web/src/app/board/BoardPage.tsx
web/src/app/board/StateDot.test.tsx    （新增）
web/src/app/board/StateDot.tsx         （新增）
web/src/app/board/columns.test.ts
web/src/app/board/columns.ts
web/src/app/tree/ProjectTree.test.tsx
web/src/app/tree/ProjectTree.tsx
web/src/app/tree/search.test.ts        （新增）
web/src/app/tree/search.ts             （新增）
web/src/components/ui/badge.test.tsx   （新增）
web/src/components/ui/badge.tsx
web/src/index.css
```

红线核对：`git diff --name-only 8a06cb6..HEAD -- '*.go' internal/ cmd/` 无输出；变更文件不含
`web/src/app/shell/Shell.tsx`、`web/src/app/workbench/` 下任何文件、`web/src/api/` 下任何文件、
`internal/proto/` 下任何文件；`web/src/api/testdata/*.json` 零改动。与 PTY 并行线冲突面为零。

## 3. 回归命令原文输出

在 `web/` 下执行：

```
$ npx vitest run
 Test Files  35 passed (35)
      Tests  304 passed (304)
   Start at  00:16:10
   Duration  3.23s (transform 1.17s, setup 2.92s, import 3.06s, tests 3.84s, environment 15.07s)

$ npm run typecheck
> tsc -b
（无错误输出）

$ npm run lint
✖ 10 problems (0 errors, 10 warnings)
（10 条 warning 均为既有：react-refresh/only-export-components 在既有导出上、
react-hooks/exhaustive-deps 在既有 useMemo 上，本批未新增）

$ npm run build
transforming...
✓ 1887 modules transformed.
rendering chunks...
computing gzip size...
dist/index.html                   0.47 kB │ gzip:   0.32 kB
dist/assets/index-BpXBl0BY.css   33.19 kB │ gzip:   6.85 kB
dist/assets/index-B0UrSWwE.js   382.73 kB │ gzip: 117.45 kB
✓ built in 963ms
```

## 4. 已知偏离与说明

- **Task 6 一处测试适配**：`getByText('项目')` 是 exact-match，「项目 N」小标题写成一整个
  div 混排文字与计数 span 时匹配不到（文本是「项目 1」）。处理：把小标题拆成两个独立
  `<span>`，文案仍是「项目」二字，未改文案迁就测试（符合计划 Task 6 Step 4 的既定修法方向）。
- **`main` 对比基线说明**：计划 Task 9 写的是 `git diff --stat main`，但本分支基于
  `850ae61a`，`main` 已落后多个集成分支，该对比把既有历史也算了进来。红线核对改用分支
  基线 `8a06cb6..HEAD`，结果如上第 2 节。
