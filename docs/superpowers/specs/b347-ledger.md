# B347 台账

- 2026-09-05 定级 L1。目标：解决两处「本地」口径不一致（CanonicalTarget 认本机别名折空；normalizeCoordinatorSpec 与 ResolveSessionRef 支持空 HOME 展开为主 HOME 绝对路径）。
- 2026-09-05 基线确立：当前分支 `cards/B347-charter`，基线 commit `bb9ce6cac409bb3393cadf3b43412f703c4cb3f3`。`codegraph --repo . sym CanonicalTarget` 命中 `internal/agentd/server.go#Server.CanonicalTarget`。`codegraph --repo . check` 校验通过退出码 0。
- 2026-09-05 TDD 声明接缝红：新增 `TestCanonicalTargetLocalMachineAliases`（覆盖 `Server.CanonicalTarget` 对本机别名与远端名归一）、`TestNormalizeCoordinatorSpecEmptyHome`（覆盖 `normalizeCoordinatorSpec` 空 HOME 展开）、`TestCoordinatorSessionRefResolverEmptyHomeOnlineCarrier`（覆盖 `ResolveSessionRef` 已上线空 HOME 载体恢复主 HOME 与无上线载体报错）。
  运行 `go test ./internal/agentd/ -run 'TestCanonicalTargetLocalMachineAliases|TestNormalizeCoordinatorSpecEmptyHome|TestCoordinatorSessionRefResolverEmptyHomeOnlineCarrier' -count=1` 退出码 1；原始输出：
  `--- FAIL: TestCanonicalTargetLocalMachineAliases (0.00s)`
  `    cardstep_local_test.go:199: CanonicalTarget("本机") = "本机", want ""`
  `    cardstep_local_test.go:199: CanonicalTarget("local") = "local", want ""`
  `--- FAIL: TestNormalizeCoordinatorSpecEmptyHome (0.00s)`
  `    coordinator_home_test.go:384: normalizeCoordinatorSpec(HomeDir="") 失败: 协调者 SessionSpec 缺少 HomeDir`
  `--- FAIL: TestCoordinatorSessionRefResolverEmptyHomeOnlineCarrier (6.67s)`
  `    coordinator_home_test.go:425: 已上线空 HOME 载体 ResolveSessionRef 失败: 协调者小队 coord 没有已上线载体可恢复 HOME`
  确认红原因为三处功能缺失：CanonicalTarget 未折叠 scheduling.IsLocalMachine 别名；normalizeCoordinatorSpec 拦截空 HomeDir；ResolveSessionRef 对已上线但 HomeDir 为空的载体误判为未恢复 HOME。
- 2026-09-05 实现与绿灯：
  1. `internal/agentd/server.go`：`Server.CanonicalTarget` 增加 `|| scheduling.IsLocalMachine(name)` 归一为空串。
  2. `internal/agentd/coordinator_home.go`：`normalizeCoordinatorSpec` 将空 HomeDir 视为主 HOME（~）并走 `hostapi.ExpandHomePath` 展开为绝对路径。
  3. `internal/agentd/coordinator_home.go`：`coordinatorSessionRefResolver.ResolveSessionRef` 遍历小队成员时，将已上线载体的空 HomeDir 作为合法主 HOME 补齐为 `~` 并展开为绝对路径，仅当小队中无任何 online 载体时才报错。
  运行 `go test -v ./internal/agentd/ -run 'TestCanonicalTargetLocalMachineAliases|TestNormalizeCoordinatorSpecEmptyHome|TestCoordinatorSessionRefResolverEmptyHomeOnlineCarrier' -count=1` 退出码 0，测试全绿。全包测试 `go test ./internal/agentd/...` 退出码 0。
- 2026-09-05 变异自验：
  1. 变异缝 1：将 `CanonicalTarget` 恢复为仅 `s.IsSelfTarget(name)`，`grep -c` 命中唯一（1），`go test -c ./internal/agentd -o /dev/null` 确认变异编译通过；测试命中 2 处断言失败（`CanonicalTarget("本机") = "本机", want ""` 与 `CanonicalTarget("local") = "local", want ""`）。恢复后测试全绿。
  2. 变异缝 2：将 `normalizeCoordinatorSpec` 改回空 HomeDir 报错，`grep -c` 命中唯一（1），`go test -c ./internal/agentd -o /dev/null` 确认变异编译通过；测试命中断言失败（`normalizeCoordinatorSpec(HomeDir="") 失败: 协调者 SessionSpec 缺少 HomeDir`）。恢复后测试全绿。
  3. 变异缝 3：在 `ResolveSessionRef` 中去除空 HomeDir 补 `~` 的逻辑，`grep -c` 命中唯一（1），`go test -c ./internal/agentd -o /dev/null` 确认变异编译通过；测试命中断言失败（`已上线空 HOME 载体 ResolveSessionRef 失败: 展开卡 card-test 的协调者 attach HOME "": hostapi: 目标 HOME 路径不能为空`）。恢复后测试全绿。
  证明三处测试均具备有效阻拦能力（非摆设）。全量编译 `go build ./... && go test -run=^$ ./...` 与架构图检查 `codegraph --repo . check` 均通过。
- 2026-09-05 提交落地：执行 `git add docs/superpowers/specs/b347-ledger.md internal/agentd/cardstep_local_test.go internal/agentd/coordinator_home.go internal/agentd/coordinator_home_test.go internal/agentd/server.go && git commit -m "fix(B347): fold local machine aliases and empty home for coordinator"` 生成初始提交 `[cards/B347-charter f2858579] fix(B347): fold local machine aliases and empty home for coordinator`；随后通过 `--amend` 将台账收口合入。
