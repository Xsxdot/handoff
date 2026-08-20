# 工作台 Web 写操作执行 ledger

- Task 1 完成（spec/质量双裁决通过）：新增卡片建卡、PATCH 元信息/验收判据、附件增删 API；补齐 PATCH/DELETE 测试工具与覆盖测试。commit range: 3e45274..HEAD（本 task commit）。
- Task 1 验证：定向 agentd 测试通过；变异测试恢复后通过；`go build ./...`、`go vet ./...`、`gofmt -l .` 通过。`go test ./... -count=1` 失败项原文摘要：`internal/client` 的 `TestCursorRootFallsBackToCwdWhenHomeUnwritable`/`TestCursorRootErrorNamesBothPaths`，`internal/config` 的 `TestLoadStripUpdateDoesNotBlockOnSaveFailure`，`internal/executor/grok` 的 `TestPermServerAskThenRespond`/`TestPermServerRespondUnknownID`/`TestPermServerReRegisterSameID`/`TestSyncAuthKeepsTaskCopyWhenWriteFails`。
- Task 2 完成（spec/质量双裁决通过）：补齐 PATCH/DELETE/PUT JSON 底座，新增卡片与工作流客户端方法、节点/工作流类型、线格式测试；`npx vitest run src/api/ledger.test.ts` 与 `npm run lint && npm run typecheck && npm test` 通过（93 files/947 tests）。commit range: 93bf488f..HEAD（本 task commit）。
- Task 3 完成（spec/质量双裁决通过）：新增建卡对话框与 CardsPage「+ 新建」入口，创建成功后刷新并打开抽屉；定向测试先红后绿，全量前端回归通过（94 files/951 tests）。commit range: 3eecab96..HEAD（本 task commit）。
