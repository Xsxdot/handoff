# agentd service lifecycle ledger

- 2026-08-20：基线 `go test ./...` 已执行；已有失败为 `TestDispatchAutoRegisterSurvivesMissingLocalAgentd`、`TestRegisterDegradesWhenLocalAndNoTarget`、`TestRegisterFailsWhenNoLocalAndNoTarget`，原始报错均为本机 agentd 的 `project add` 返回 404，未归因到本计划。
- 2026-08-20：Task 1 开始；先添加跨平台契约测试，尚未提交。
- 2026-08-20：Task 1 双裁决通过；spec 符合：Manager 新增 Stop/Restart、Status 新增 Disabled、ErrNotInstalled 可由 errors.Is 判别、三平台仅落显式报错桩、两处 fake 补齐；代码质量：gofmt 无输出、`go build ./...` 通过、`go test ./internal/service/` 通过。Task 1 commit 范围：`internal/service/service.go`、`internal/service/service_test.go`、三平台桩、`cmd/service_test.go`、`desktop/internal/shell/lifecycle_test.go`、本 ledger；根 `cmd` 与 desktop 全包仍受基线 404、缺 frontend embed、只读 `/tmp` 失败影响。
- 2026-08-20：Task 2 双裁决通过；spec 符合：launchd 的 Start/Stop/Restart、plist 安装判据、Disabled 两种格式、轮询复核、PID 变化复核与 Install enable 全部实现并由 `TestLaunchd*` 覆盖；代码质量：gofmt 无输出、`go build ./...` 与 `go test ./internal/service/` 通过。Task 2 commit 范围：`internal/service/launchd.go`、`internal/service/launchd_test.go`、本 ledger。
