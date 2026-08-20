# 工作台 A 组执行账本

本文件记录各 task 的实际验证结果、修复轮次与提交范围；结论只写已亲自执行的命令结果。

- Task 1 修复轮 1：测试夹具由 `newTestStore` 调整为现有 `seedStore`，因为 `mk` 依赖默认工作流；原始失败为 `建卡取工作流 "bug": ... 记录不存在`。范围：`internal/ledger/cards_test.go`。
- Task 1 完成：`go test ./internal/ledger/ -count=1` 通过；`grep -rn '\.Subtree(' --include='*.go' .` 仅命中 `cmd/card_wait.go`、账本内部定义及测试。提交范围：Task 1 代码、测试、注释与本账本文件。
- Task 2 完成：后端 `go test ./internal/agentd/ -count=1` 通过（`ok ... 102.064s`）；前端 `npx vitest run` 通过（81 files、826 tests）；详情附加查询失败降级带 `Warn`，子任务区空时不渲染。测试夹具因仓库没有计划所称 `newLedgerEnv` 而补建并复用。提交范围：Task 2 后端 API、测试、前端类型/区块/测试与本账本文件。
- Task 3 完成：后端 `go test ./internal/agentd/ -count=1` 通过（`ok ... 104.106s`）；前端 `npx vitest run` 通过（81 files、828 tests）；验收事件固定 `verified=true`，空/空白证据后端 400，未知卡 404，成功日志仅记录证据字节数。提交范围：Task 3 验收 API、测试、前端 API/表单/三态 chip 与本账本文件。
