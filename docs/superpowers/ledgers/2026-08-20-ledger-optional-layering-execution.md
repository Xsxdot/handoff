# 账本可选化与命令分层执行账本

职责：记录本次实现计划各 task 的双裁决、修复轮次、提交范围与亲自执行的验证结果。
边界：只记录本分支证据；附录中的真机验收由协调者执行，不在本账本代替记录。

- 基线：`d9b8c436`（计划提交）；当前分支 `feat/ledger-optional-layering`，开工前工作树干净。
- Task 1 / 修复轮 0：先写 `TestLedgerDisabledByDefault`；受限沙箱原始错误为 `go: writing go.mod cache: ... read-only file system`，提升权限后按预期翻红：账本库被打开并生成 `ledger.db`，测试报 `账本未启用时 card add 应报错`。实现 `LedgerConfig.Enabled`、`openLedger` 门禁与测试基座显式开关后，定向两测实际通过。
- Task 1 / 完成裁决：spec 符合性通过（默认 false、未知键清单含 `ledger{enabled,dsn}`、CLI 未启用含「账本未启用」且不建库、既有测试基座显式开启）；代码质量通过（门禁有 `slog.Warn` 与 `config_dsn_set`、字段和基座 why 注释齐全、`git diff --check` 通过）。验证：`go test ./cmd/ -run 'TestLedgerDisabledByDefault|TestOpenLedgerFallbackSQLite' -count=1`、`go test ./cmd/ -count=1` 实际通过。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
