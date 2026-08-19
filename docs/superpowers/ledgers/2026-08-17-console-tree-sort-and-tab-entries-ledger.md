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
| 2026-08-18 | Task 9 | 0 | 完成；spec 符合性与代码质量双裁决通过；新增边缘/中间投放纯函数，三处任务行拖源与跨基准投放接线 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test && npm run lint && npm run typecheck`；拖放定向 41 tests PASS；`git diff --check` |
| 2026-08-18 | Task 10 | 0 | 完成；spec 符合性与代码质量双裁决通过；新增文件先列举挑号、中央 file tab 接线、错误原文与右栏根层刷新 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test && npm run lint && npm run typecheck`；Task 10 定向 74 tests PASS；`git diff --check` |
| 2026-08-18 | Task 11 | 0 | 完成；spec 符合性与代码质量双裁决通过；浮窗支持临时文件 tab、草稿寄存、scratch 能力隐藏入口与关闭确认分流 | `HEAD^..HEAD`（本 task 提交） | `cd web && npm run test && npm run lint && npm run typecheck`；Task 11 定向 33 tests PASS；`git diff --check` |
| 2026-08-18 | Task 12 | 1 | 终审修复通过；本机 `localMachine` 在 `fillFromStatus` 后补回 scratch_root，避免本机能力投影丢失 | `HEAD^..HEAD`（终审修复提交） | `go test ./internal/agentd/ ./internal/proto/`、`gofmt -l .`、`git diff --check` |
