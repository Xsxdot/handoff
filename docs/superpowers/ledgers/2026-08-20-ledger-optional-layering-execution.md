# 账本可选化与命令分层执行账本

职责：记录本次实现计划各 task 的双裁决、修复轮次、提交范围与亲自执行的验证结果。
边界：只记录本分支证据；附录中的真机验收由协调者执行，不在本账本代替记录。

- 基线：`d9b8c436`（计划提交）；当前分支 `feat/ledger-optional-layering`，开工前工作树干净。
- Task 1 / 修复轮 0：先写 `TestLedgerDisabledByDefault`；受限沙箱原始错误为 `go: writing go.mod cache: ... read-only file system`，提升权限后按预期翻红：账本库被打开并生成 `ledger.db`，测试报 `账本未启用时 card add 应报错`。实现 `LedgerConfig.Enabled`、`openLedger` 门禁与测试基座显式开关后，定向两测实际通过。
- Task 1 / 完成裁决：spec 符合性通过（默认 false、未知键清单含 `ledger{enabled,dsn}`、CLI 未启用含「账本未启用」且不建库、既有测试基座显式开启）；代码质量通过（门禁有 `slog.Warn` 与 `config_dsn_set`、字段和基座 why 注释齐全、`git diff --check` 通过）。验证：`go test ./cmd/ -run 'TestLedgerDisabledByDefault|TestOpenLedgerFallbackSQLite' -count=1`、`go test ./cmd/ -count=1` 实际通过。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
- Task 2 / 修复轮 0：先追加 `TestLedgerHealthReportsDisabled`；按计划原始失败为 `health 应 200，实际 503 body={"error":"账本库未配置（config.ledger.dsn 或单机回退）"}`。随后将 health 路由移出 `withLedger`、未挂载回 `enabled:false`，并按 `ledger.enabled` 包住 agentd 账本库与镜像启动。
- Task 2 / 修复轮 1：双裁决时发现 `SetLedger` 的既有 doc 将 health 也概括为 503；收窄为「除 health 外 API 降级 503」，避免文档与恒 200 契约冲突。复跑 health 定向测试与 `git diff --check` 实际通过。
- Task 2 / 完成裁决：spec 符合性通过（未启用不开库不起镜像并记录 info/warn、启用路径保留原镜像日志与 defer 顺序、health 未启用恒 200 且启用附 `enabled:true` 与 `mirror`）；代码质量通过（health handler doc/why 注释、半配告警、`SetLedger` 文档与行为一致、gofmt 无输出）。验证：`go test ./internal/agentd/ -run TestLedgerHealth -count=1`、`go build ./...`、`go test ./internal/agentd/ -count=1` 实际通过（全包 104.121s）。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
- Task 3 / 修复轮 0：先按新命令改测试；原始失败同时出现 `unknown flag: --subtree`（`card wait` 尚未接入）与 `应报未知 flag，实际: 卡 B1: ledger: 记录不存在`（旧 `wait --card` 仍被接受）。搬运 `runCardWait` 原逻辑到 `cmd/card_wait.go`，删除 wait.go 的 card 变量/分支/flag/ledger import，并挂入 card 命令树后，定向两测实际通过。
- Task 3 / 完成裁决：spec 符合性通过（`card wait <id> [--subtree] [--timeout]` 逐事件输出、终态/超时逻辑原样保留、`wait --card` 变未知 flag、wait.go 无 `ledger` 命中）；代码质量通过（新文件职责/边界头注释、超时码 why 注释、`git diff --check` 通过）。验证：`go test ./cmd/ -run 'TestCardWait|TestWaitRejectsCardFlag' -count=1` 实际通过；`grep -c 'ledger' cmd/wait.go` 实际无输出并以退出码 1 表示零命中。cmd 全包实际失败，原始报错为 `TestDispatchAutoRegisterSurvivesMissingLocalAgentd ... 本机没有 agentd 时首次派发应当成功: 自动登记失败: 登记到本机: project add: 状态码 404: 404 page not found`；该失败未改动相关文件，留待终审 triage。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
- Task 4 / 修复轮 0：先追加三条 `TestCardAccept`；原始失败为 `card accept: unknown flag: --evidence`、无证据调用未报错、以及 `card accept --unverified: unknown flag: --unverified`。实现 `card_records.go` 的 accept 门面并挂入 card 命令树后，三测实际通过。
- Task 4 / 完成裁决：spec 符合性通过（缺省已验、已验强制非空证据、`--unverified` 可空、调用 `RecordAcceptance` 落 `acceptance_recorded` 事件且不推状态）；代码质量通过（进入 Info 只记 evidence 长度、错误 Error 带 card/verified/cause、成功 Info、文件头与 why 注释齐全、`git diff --check` 通过）。验证：`go test ./cmd/ -run TestCardAccept -count=1` 实际通过。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
