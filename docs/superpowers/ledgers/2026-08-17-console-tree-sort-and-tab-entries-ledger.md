# 控制台目录行排序与三条新入口执行记账

| 时间 | Task | 轮次 | 结果 | Commit 范围 | 验证 |
|---|---|---:|---|---|---|
| 2026-08-18 | Task 1 | 0 | 完成；spec 符合性与代码质量双裁决通过 | `HEAD^..HEAD`（本 task 提交） | `go test ./internal/agentd/ ./internal/proto/`、`cd web && npm run test -- src/api/contract.test.ts`、`gofmt -l .`、`git diff --check` |
| 2026-08-18 | Task 2 | 0 | 完成；spec 符合性与代码质量双裁决通过；补齐既有 Workspace 测试夹具的 `created_at` | `HEAD^..HEAD`（本 task 提交） | `go test ./internal/agentd/ ./internal/proto/`、`cd web && npm run test -- src/api/contract.test.ts && npm run typecheck`、`gofmt -l .` |
| 2026-08-18 | Task 3 | 0 | 完成；spec 符合性与代码质量双裁决通过 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test -- src/app/tree/sortWorkspaces.test.ts && npm run lint && npm run typecheck` |
| 2026-08-18 | Task 4 | 0 | 完成；spec 符合性与代码质量双裁决通过；同步既有 GlobalTickets 测试夹具 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test -- src/app/overlay/useGlobalTickets.test.ts src/app/overlay/TicketsOverlay.test.tsx && npm run lint && npm run typecheck` |
| 2026-08-18 | Task 5 | 0 | 完成；spec 符合性与代码质量双裁决通过；同步 ProjectTree 既有调用夹具 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test && npm run lint && npm run typecheck` |
| 2026-08-18 | Task 6 | 0 | 完成；spec 符合性与代码质量双裁决通过；同步 WorkbenchPage/测试 API 夹具 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test && npm run lint && npm run typecheck` |
| 2026-08-18 | Task 7 | 0 | 完成；spec 符合性与代码质量双裁决通过；终态口径按 board/columns.ts 校准 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test -- src/app/workbench/TaskPickerDialog.test.tsx && npm run typecheck` |
| 2026-08-18 | Task 8 | 0 | 完成；spec 符合性与代码质量双裁决通过；删除 PICK_HINT/awaiting/hint/onBack，接通任务选择器并同步空态快捷键文案 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test && npm run lint && npm run typecheck`；定向 `src/app/workbench/ src/app/shell/` 160 tests PASS |
