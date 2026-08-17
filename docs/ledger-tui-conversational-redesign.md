# TUI tab 对话式重构执行 ledger

任务：0909257a-648f-4e8e-a81b-c3851e36a48a
分支：feat/tui-conversational-redesign-2
基线：9373cef7
计划：用户消息中的 TUI tab 对话式重构 Implementation Plan

## 进度

- 2026-08-17 初始化：Task 1–10 待完成；Task 11 按计划留给审核者真机执行。
- 2026-08-17 Task 1 完成，commit 685e84fb。spec/质量双裁决通过：Frame/TS 增加 instructions，BeginTurn 日志只记 instructions_len，8 个 adapter 调用点与既有测试已同步；`gofmt -l .` 无输出；`go test ./internal/executor/... -v -run 'TestBeginTurn'` PASS；`go test ./...` 首跑出现原始失败 `TestApproverConcurrentTaskEndOnlyAudits: 应留 approver_decision 审计事件`，重跑 PASS；`npm run typecheck` 首跑因 `sh: tsc: command not found` 未执行，按现有 lockfile `npm ci` 后 PASS。
- 2026-08-17 Task 2 完成，commit 90e2de76。spec/质量双裁决通过：Branches 只列本地 refs/heads，新增 byTask 路由、默认基准与三点日志，TS BranchesResult/client 已对齐；`go test ./internal/agentd/ -run TestBranches -v` PASS；`go test ./internal/agentd/` PASS；`npm run typecheck` PASS；`gofmt -l .` 无输出。
- 2026-08-17 Task 3 第 1 轮未裁决：新增测试与实现契约冲突——测试要求 `files[0].lines[0]` 为 hunk，但 DiffLine 注释要求保留 `index/---/+++/Binary` 头部为 ctx；当前实现保留头部，`npx vitest run src/app/task/diff.test.ts` 仅「行类型标注正确」失败。等待协调者裁决，不提交。
- 2026-08-17 Task 3 第 2 轮修复完成，commit 6e8a3053：按协调者裁决跳过 `index `、`--- `、`+++ ` 三类纯噪声头部，保留其余头部为 ctx；`npx vitest run src/app/task/diff.test.ts` PASS（5 tests）。spec/质量双裁决通过。
- 2026-08-17 Task 4 完成，commit c050e878：事件白名单映射与未知事件原样透出，delivery trailer best-effort 提取且失败不吞正文；`npx vitest run src/app/task/eventPhrase.test.ts src/app/task/delivery.test.ts` PASS（7 tests）；spec/质量双裁决通过。
- 2026-08-17 Task 5 第 1 轮完成，commit 6c923147：新增统一 MetaRow/EventChip/UserInstructionBlock/DeliverySummaryCard，turn 块承载 instructions，ThinkingBlock/ToolCard 改为元数据行；目标测试 PASS（48 tests）。
- 2026-08-17 Task 5 第 1 轮修复：typecheck 首跑报 `frames.test.ts(6,10): error TS2300: Duplicate identifier 'buildBlocks'`，移除新增的重复 import；修复后 task 全测 PASS（113 tests）、`npm run typecheck` PASS；lint 0 errors、仓库既有 10 warnings。
- 2026-08-17 Task 6 第 1 轮完成，commit fa69d075：ConversationStream 接管唯一滚动区、prepend 补偿、回合锚点、交付卡与流内提示；目标测试初次因标题 emoji 与精确文本查询同节点失败，拆分展示节点后修复；task 全测 PASS（118 tests）、`npm run typecheck` PASS、lint 0 errors/既有 10 warnings。
- 2026-08-17 Task 7 第 1 轮完成，commit 952326fa：UsageChip/TuiHeader 实现两行页头、ctx/累计账目弹出、回合下拉与动作状态；首测仅因现有 `formatTokens(200000)` 实际输出 `200.0k` 而非计划示例 `200k` 失败，断言按既有格式化函数对齐；task 全测 PASS（122 tests）、`npm run typecheck` PASS、lint 0 errors/既有 10 warnings。
- 2026-08-17 Task 8 第 1 轮完成，commit 79ff726f：DiffView 按文件分组着色并在解析失败时裸文本回退，ReviewSidePanel 接入 branch/diff/run/file；首测发现总计与文件组各有 `+1`，将脆弱 `getByText` 改为精确 `getAllByText`；task 全测 PASS（126 tests）、`npm run typecheck` PASS、lint 0 errors/既有 10 warnings。
- 2026-08-17 Task 9 第 1 轮 RED：按计划新增 Composer 交互测试；`npx vitest run src/app/task/Composer.test.tsx` 原始失败为 `Failed to resolve import "./Composer" ... Does the file exist?`。
- 2026-08-17 Task 9 第 1 轮完成，commit e82fd37b：Composer 按 AdvanceActions 状态机实现续发/完成/停止/恢复与强制收口，Enter 发送、Shift+Enter 换行、断线禁用且保留输入；6 tests PASS；spec/质量双裁决通过。
- 2026-08-17 Task 10 第 1 轮 RED：按计划新增 DebugDrawer 测试；`npx vitest run src/app/task/DebugDrawer.test.tsx` 原始失败为 `Failed to resolve import "./DebugDrawer" ... Does the file exist?`。
- 2026-08-17 Task 10 第 1 轮完成，commit 范围为本分支最终总装提交（含本 ledger，哈希以 `git log HEAD` 为准）：DebugDrawer 默认展示封顶原始事件，原始正文页签按需挂 RenderPanel；TuiTab 总装 ConversationStream/TuiHeader/ReviewSidePanel/Composer/DebugDrawer，清理六个旧件并迁移 EventChip 测试；DebugDrawer 2 tests PASS，`npm run typecheck` PASS，`npm test` 56 files/551 tests PASS，`npm run lint` 0 errors/10 existing warnings，`go test ./...` PASS；spec/质量双裁决通过。
- 2026-08-17 Task 11 按计划跳过：Task 11 留给审核者真机执行。
