# TUI tab 对话式重构执行 ledger

任务：0909257a-648f-4e8e-a81b-c3851e36a48a
分支：feat/tui-conversational-redesign-2
基线：9373cef7
计划：用户消息中的 TUI tab 对话式重构 Implementation Plan

## 进度

- 2026-08-17 初始化：Task 1–10 待完成；Task 11 按计划留给审核者真机执行。
- 2026-08-17 Task 1 完成，commit 228f6bf4（待本行 amend 固化）。spec/质量双裁决通过：Frame/TS 增加 instructions，BeginTurn 日志只记 instructions_len，8 个 adapter 调用点与既有测试已同步；`gofmt -l .` 无输出；`go test ./internal/executor/... -v -run 'TestBeginTurn'` PASS；`go test ./...` 首跑出现原始失败 `TestApproverConcurrentTaskEndOnlyAudits: 应留 approver_decision 审计事件`，重跑 PASS；`npm run typecheck` 首跑因 `sh: tsc: command not found` 未执行，按现有 lockfile `npm ci` 后 PASS。
- 2026-08-17 Task 2 完成，commit 37f4804b（待本行 amend 固化）。spec/质量双裁决通过：Branches 只列本地 refs/heads，新增 byTask 路由、默认基准与三点日志，TS BranchesResult/client 已对齐；`go test ./internal/agentd/ -run TestBranches -v` PASS；`go test ./internal/agentd/` PASS；`npm run typecheck` PASS；`gofmt -l .` 无输出。
- 2026-08-17 Task 3 第 1 轮未裁决：新增测试与实现契约冲突——测试要求 `files[0].lines[0]` 为 hunk，但 DiffLine 注释要求保留 `index/---/+++/Binary` 头部为 ctx；当前实现保留头部，`npx vitest run src/app/task/diff.test.ts` 仅「行类型标注正确」失败。等待协调者裁决，不提交。
- 2026-08-17 Task 3 第 2 轮修复完成，commit 121799fb：按协调者裁决跳过 `index `、`--- `、`+++ ` 三类纯噪声头部，保留其余头部为 ctx；`npx vitest run src/app/task/diff.test.ts` PASS（5 tests）。spec/质量双裁决通过。
- 2026-08-17 Task 4 完成，commit 0cf7a504：事件白名单映射与未知事件原样透出，delivery trailer best-effort 提取且失败不吞正文；`npx vitest run src/app/task/eventPhrase.test.ts src/app/task/delivery.test.ts` PASS（7 tests）；spec/质量双裁决通过。
