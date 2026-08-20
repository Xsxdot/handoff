# 工作台 A 组执行账本

本文件记录各 task 的实际验证结果、修复轮次与提交范围；结论只写已亲自执行的命令结果。

- Task 1 修复轮 1：测试夹具由 `newTestStore` 调整为现有 `seedStore`，因为 `mk` 依赖默认工作流；原始失败为 `建卡取工作流 "bug": ... 记录不存在`。范围：`internal/ledger/cards_test.go`。
- Task 1 完成：`go test ./internal/ledger/ -count=1` 通过；`grep -rn '\.Subtree(' --include='*.go' .` 仅命中 `cmd/card_wait.go`、账本内部定义及测试。提交范围：Task 1 代码、测试、注释与本账本文件。
