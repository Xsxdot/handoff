# TUI tab 对话式重构执行 ledger

任务：0909257a-648f-4e8e-a81b-c3851e36a48a
分支：feat/tui-conversational-redesign-2
基线：9373cef7
计划：用户消息中的 TUI tab 对话式重构 Implementation Plan

## 进度

- 2026-08-17 初始化：Task 1–10 待完成；Task 11 按计划留给审核者真机执行。
- 2026-08-17 Task 1 完成，commit 228f6bf4（待本行 amend 固化）。spec/质量双裁决通过：Frame/TS 增加 instructions，BeginTurn 日志只记 instructions_len，8 个 adapter 调用点与既有测试已同步；`gofmt -l .` 无输出；`go test ./internal/executor/... -v -run 'TestBeginTurn'` PASS；`go test ./...` 首跑出现原始失败 `TestApproverConcurrentTaskEndOnlyAudits: 应留 approver_decision 审计事件`，重跑 PASS；`npm run typecheck` 首跑因 `sh: tsc: command not found` 未执行，按现有 lockfile `npm ci` 后 PASS。
- 2026-08-17 Task 2 完成，commit 37f4804b（待本行 amend 固化）。spec/质量双裁决通过：Branches 只列本地 refs/heads，新增 byTask 路由、默认基准与三点日志，TS BranchesResult/client 已对齐；`go test ./internal/agentd/ -run TestBranches -v` PASS；`go test ./internal/agentd/` PASS；`npm run typecheck` PASS；`gofmt -l .` 无输出。
