# agentd target client pool 执行 ledger

- Task 1 完成；范围：`internal/client/client.go`、`internal/client/update.go`、`internal/client/poisoned_test.go`；定向测试通过，`gofmt -l internal/client/` 为空；全包回归原始失败：`TestCursorRootFallsBackToCwdWhenHomeUnwritable ... 根 = ".../.handoff/cursors", want ".../.handoff/cursors"（应降级到 cwd）`；`TestCursorRootErrorNamesBothPaths ... 两处都不可写时必须报错，不得静默`。
- Task 2 完成；范围：`internal/relay/dialer.go`、`internal/relay/dialer_test.go`；定向测试、relay 全包测试通过，`gofmt -l internal/relay/` 为空；双裁决通过。
- Task 3 完成；范围：`internal/client/client.go`、`internal/targetclient/targetclient.go`、`internal/targetclient/targetclient_test.go`；targetclient 全包测试通过；client 回归原始失败仍为 `TestCursorRootFallsBackToCwdWhenHomeUnwritable ... 应降级到 cwd` 与 `TestCursorRootErrorNamesBothPaths ... 两处都不可写时必须报错，不得静默`；双裁决通过。
- Task 4 完成；范围：`internal/targetclient/pool.go`、`internal/targetclient/pool_test.go`；Pool 定向测试与 `-race` 通过，`gofmt -l internal/targetclient/` 为空；双裁决通过。
- Task 5 完成；范围：`internal/targetclient/warm.go`、`internal/targetclient/warm_test.go`、`internal/targetclient/pool.go`、`internal/targetclient/targetclient.go`；Warm 定向测试、全包 `-race` 通过，`gofmt -l internal/targetclient/` 为空；双裁决通过。
- Task 6 完成；范围：`cmd/root.go`、`cmd/target_client_test.go`；定向测试、cmd 全包测试通过，`gofmt -l cmd/` 为空；双裁决通过。
- Task 7 修复轮 1；范围：`internal/agentd/pool_wiring_test.go`；原计划测试传 nil Store，实际原始失败为 `panic: runtime error: invalid memory address or nil pointer dereference`，栈落在 `store.(*Store).SetEventHook` / `agentd.(*Server).registerEventFrameHook`；测试改为使用临时 Store，未改变生产代码。
- Task 7 完成；范围：`internal/agentd/server.go`、`internal/agentd/pool_wiring_test.go`、`cmd/agentd.go`；定向测试、`go build ./...`、`go test ./internal/agentd/ ./cmd/` 通过，`gofmt -l internal/agentd/ cmd/` 为空；双裁决通过。
