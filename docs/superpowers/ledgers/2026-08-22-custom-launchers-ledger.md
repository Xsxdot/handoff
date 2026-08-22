# 需求 B：工作台自定义启动项执行 ledger

基线：`74fdb762` 之后的任务分支 HEAD `aba1248c`。

## 执行记录

- 2026-08-22：基线核验完成。工作区干净；`OpenOptions.InitCommand`、启动项契约类型、能力位类型均已存在。B1 开始；尚未提交。
- 2026-08-22：Task 1 B1 完成，改动 `internal/ptyhost/engine/engine.go`、`initcmd_test.go`。失败测试实测：`go test ./internal/ptyhost/engine/ -run TestInitCommand 2>&1 | tail -20` 返回 3 条非空命令失败、空命令守卫通过。实现后 `gofmt -l internal/ptyhost/` 无输出；`go test ./internal/ptyhost/engine/ 2>&1 | tail -30` 输出 `ok github.com/Xsxdot/handoff/internal/ptyhost/engine`；`GOOS=windows go build ./... && echo "windows 构建通过"` 输出 `windows 构建通过`。双裁决通过：spec 覆盖首字节/3 秒兜底/空命令兼容/Q4 原文加换行/无命令日志；代码质量通过：`firstOut` 只由 pump 观测、写入错误不告警、平台文件未改。Task 1 commit 范围：本行、B1 实现与测试。
- 2026-08-22：Task 2 B2 完成，改动 `internal/agentd/launchers_api.go`、`launchers_api_test.go`、`server.go`。失败测试实测：`go test ./internal/agentd/ -run TestLaunchers 2>&1 | tail -20` 在 handler 接入前返回各用例 404。实现后 targeted 输出 `ok github.com/Xsxdot/handoff/internal/agentd 2.163s`；完整 `go test ./internal/agentd/ 2>&1 | tail -10` 输出 `ok github.com/Xsxdot/handoff/internal/agentd 125.810s`；`git diff --check` 无输出。Task 2 测试采用既有 `newTestAgentdEnvWithCfg`/`doJSON` 搭台，未新造 HTTP 服务；双裁决通过：spec 覆盖 CRUD、五档校验、派生字段双向与忽略客户端字段、空列表、日志脱敏；代码质量通过：跨机复用 `forwardIfRequested`，GET 回非 nil 列表，错误保留真因。Task 2 commit 范围：本行、B2 handler/测试/路由清单。
