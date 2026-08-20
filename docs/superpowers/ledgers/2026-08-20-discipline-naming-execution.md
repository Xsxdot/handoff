# 纪律块具名化执行账本

任务：3b39b1a9-7e54-4c43-8314-5322259e9200
分支：feat/discipline-naming

## 进度

| 时间 | 范围 | 结果 |
|---|---|---|
| 2026-08-20 | Task 1，c982b56b..c982b56b | spec/质量双裁决通过；`go test ./internal/discipline/ -count=1` 通过；`For` 未改，review 源文逐字复制。 |
| 2026-08-20 | Task 1 修复轮，c982b56b..c982b56b（amend） | agentd 既有 Builtins 数量断言从 2 修正为 3，定向纪律测试通过；重新裁决通过。 |
| 2026-08-20 | Task 2，f4dc5005..f4dc5005 | spec/质量双裁决通过；`go test ./internal/store/ -count=1` 与 `go test ./internal/agentd/ -count=1` 均通过；纯存储字段无新增日志。 |
| 2026-08-20 | Task 3，813b78b3..813b78b3 | spec/质量双裁决通过；定向纪律测试与 `go test ./internal/agentd/ -count=1` 均通过；三个调用点统一走 `resolveDisciplineFor`。 |
| 2026-08-20 | Task 4，a7a587a6..a7a587a6 | spec/质量双裁决通过；定向/全量 ledger 测试与 `go build ./...` 均通过；老路径映射成功/未知均 Warn。 |
| 2026-08-20 | Task 5，337ccdb6..337ccdb6 | spec/质量双裁决通过；cmd/agentd/ledger 通过，定向 client Dispatch 通过；四包命令中 client 基线失败：`TestCursorRootFallsBackToCwdWhenHomeUnwritable`、`TestCursorRootErrorNamesBothPaths`。红线 grep 无输出。 |
| 2026-08-20 | Task 6，e68bf002..e68bf002 | 删除前 review 副本逐字 diff 无输出；4 条红线 grep 均无输出；build/vet 通过；全量测试并行运行失败，单包复核失败：client `TestCursorRootFallsBackToCwdWhenHomeUnwritable`、`TestCursorRootErrorNamesBothPaths`，config `TestLoadStripUpdateDoesNotBlockOnSaveFailure`，grok `TestSyncAuthKeepsTaskCopyWhenWriteFails`；cmd 单包复核通过。 |
| 2026-08-20 | Task 7，f8a8fd09..HEAD | 格式/静态门：`gofmt -l . | grep -v '^web/'` 无输出、`git diff --check`/`go build ./...`/`go vet ./...` 退出 0；全量 `go test ./... -count=1` 共 38 包，35 个 `ok`、3 个失败包（client/config/grok），原始失败行见下。前端门：`npm install` 退出 0，`npx tsc --noEmit` 退出 0，Vitest 81 文件/824 测试全通过。终审红线均无输出，web 无改动；spec/质量双裁决通过，`Resolver.For` 语义未改、无 `fmt.Printf`，无待修项。 |
| 2026-08-20 | Task 7 修复轮，9e2a861a..HEAD | 按复核恢复 `internal/ledger/templates.go` 与测试中的老文件名完整字面量，恢复 `ledgers/2026-08-19-b156.1-handoff.md` 的历史原文。`gofmt` 无输出，`git diff --check` 通过，`go test ./internal/ledger -count=1` 通过；`go test ./...` 本次退出 1，失败仍为 client/config/grok 环境敏感用例，原始失败行见下。红线 grep 现命中两处正当例外：`internal/ledger/templates.go` 及其迁移测试夹具的老路径 basename 映射负责识别旧文件名；`docs/superpowers/ledgers/` 保存当日历史事实，不应为审计改写。该 grep 规则与这两项职责冲突，已如实记录，不以拆分字面量规避。 |
| 2026-08-20 | 补测修复轮，2ed5958c..HEAD | 新增三条常驻守门：`TestResumeTaskReparsesPersistedDisciplineName` 覆盖落盘名字/空名恢复，`TestContinueReparsesPersistedDisciplineName` 覆盖 waiting_review 续接恢复，`TestByNameIgnoresExecutorDisable` 覆盖机器级显式关闭不影响点名路径；定向测试通过，`gofmt` 无输出，`go build ./...` 与 `go vet ./...` 退出 0。`go test ./...` 本轮退出 1，新增测试所在 agentd/discipline 包通过；原始失败行：`cursordir_test.go:57: 根 = ".../001/.handoff/cursors", want ".../002/.handoff/cursors"（应降级到 cwd）`、`cursordir_test.go:95: 两处都不可写时必须报错，不得静默`、`config_test.go:414: 回写应失败，磁盘上仍须留着 update 段:`、`authsync_test.go:263: 写回失败应返回错误`。 |

### Task 7 全量测试失败原文

```text
cursordir_test.go:57: 根 = "/root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestCursorRootFallsBackToCwdWhenHomeUnwritable3152678052/001/.handoff/cursors", want "/root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestCursorRootFallsBackToCwdWhenHomeUnwritable3152678052/002/.handoff/cursors"（应降级到 cwd）
cursordir_test.go:95: 两处都不可写时必须报错，不得静默
config_test.go:414: 回写应失败，磁盘上仍须留着 update 段:
perm_test.go:56: newPermServer: 裁决 socket 路径过长（114 字节，上限 107）: /root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestPermServerAskThenRespond3743307715/001/perm.sock——把 DataDir 配到更浅的位置
perm_test.go:91: newPermServer: 裁决 socket 路径过长（116 字节，上限 107）: /root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestPermServerRespondUnknownID2608486731/001/perm.sock——把 DataDir 配到更浅的位置
perm_test.go:108: newPermServer: 裁决 socket 路径过长（115 字节，上限 107）: /root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestPermServerReRegisterSameID727009731/001/perm.sock——把 DataDir 配到更浅的位置
resume_test.go:89: Resume 应判活并续读: alive=false err=裁决 socket 路径过长（115 字节，上限 107）: /root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestResumeContinuesFromOffset3814848401/001/perm.sock——把 DataDir 配到更浅的位置
authsync_test.go:263: 写回失败应返回错误
```

### Task 7 修复轮 Go 全量测试原始失败行

```text
cursordir_test.go:57: 根 = "/root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestCursorRootFallsBackToCwdWhenHomeUnwritable2700848085/001/.handoff/cursors", want "/root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestCursorRootFallsBackToCwdWhenHomeUnwritable2700848085/002/.handoff/cursors"（应降级到 cwd）
cursordir_test.go:95: 两处都不可写时必须报错，不得静默
config_test.go:414: 回写应失败，磁盘上仍须留着 update 段:
perm_test.go:56: newPermServer: 裁决 socket 路径过长（114 字节，上限 107）: /root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestPermServerAskThenRespond2731821359/001/perm.sock——把 DataDir 配到更浅的位置
perm_test.go:91: newPermServer: 裁决 socket 路径过长（115 字节，上限 107）: /root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestPermServerRespondUnknownID854345050/001/perm.sock——把 DataDir 配到更浅的位置
perm_test.go:108: newPermServer: 裁决 socket 路径过长（115 字节，上限 107）: /root/.handoff/tasks/3b39b1a9-7e54-4c43-8314-5322259e9200/tmp/TestPermServerReRegisterSameID811191459/001/perm.sock——把 DataDir 配到更浅的位置
authsync_test.go:263: 写回失败应返回错误
```
