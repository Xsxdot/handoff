# agentd target client pool 执行 ledger

- Task 1 完成；范围：`internal/client/client.go`、`internal/client/update.go`、`internal/client/poisoned_test.go`；定向测试通过，`gofmt -l internal/client/` 为空；全包回归原始失败：`TestCursorRootFallsBackToCwdWhenHomeUnwritable ... 根 = ".../.handoff/cursors", want ".../.handoff/cursors"（应降级到 cwd）`；`TestCursorRootErrorNamesBothPaths ... 两处都不可写时必须报错，不得静默`。
