# B345 台账

- 2026-09-05 真机：PUT agy home_dir="" 后 POST detect → 400 `cli 与 home_dir 必填`。grok 本机空 HOME detect 成功 online。workaround：agy/codex home_dir=`~`。
- 2026-09-05 定级 L1。派 runner（agy@linux-01）实现，兼验 B341 系统 push origin。
- 2026-09-05 TDD 声明缝红：新增 `TestHostWakeAllowsEmptyHomeDirForMainHome` 与 `TestCarrierDetectRemoteWithEmptyHomeDir` 覆盖 `handleHomeWake` 与远端检测空 HOME 路径。运行 `go test ./internal/agentd/ -run 'TestHostWakeAllowsEmptyHomeDirForMainHome|TestCarrierDetectRemoteWithEmptyHomeDir' -count=1` 退出码 1；原始输出：
  `--- FAIL: TestHostWakeAllowsEmptyHomeDirForMainHome (0.16s)`
  `    hostprobe_test.go:234: 空 home_dir 唤起失败: 400 {"error":"cli 与 home_dir 必填"}`
  `--- FAIL: TestCarrierDetectRemoteWithEmptyHomeDir (0.33s)`
  `    hostprobe_test.go:296: 远端空 HOME 载体检测失败: 400 {"error":"cli 与 home_dir 必填"}`
  确认红原因为 handleHomeWake 拦截空 home_dir。
- 2026-09-05 实现与绿灯：`internal/agentd/hostprobe.go` `handleHomeWake` 仅拦截空 `cli`，放行空 `home_dir` 并原样转交 `WakeHome`。运行 `go test ./internal/agentd/ -run 'TestHostWakeAllowsEmptyHomeDirForMainHome|TestCarrierDetectRemoteWithEmptyHomeDir' -count=1` 退出码 0，测试全绿。
- 2026-09-05 变异自验：将 `handleHomeWake` 变异为 `if in.CLI == "" || in.HomeDir == ""`，`go test -c ./internal/agentd -o /dev/null` 确认变异编译通过且命中唯一；执行测试命中 2 红（TestHostWakeAllowsEmptyHomeDirForMainHome 与 TestCarrierDetectRemoteWithEmptyHomeDir 均报 400 失败），证明断言有效。恢复绿灯后重跑测试退出码 0，全量编译 `go build ./... && go test -run=^$ ./...` 与 `codegraph --repo . check` 均通过。
- 2026-09-05 提交落地：执行 `git add docs/superpowers/specs/b345-ledger.md internal/agentd/hostprobe.go internal/agentd/hostprobe_test.go && git commit -m "fix(B345): allow empty home_dir on host wake for main home fallback"` 生成初始提交 `[cards/B345-charter a9157de5] fix(B345): allow empty home_dir on host wake for main home fallback`；随后通过 `--amend` 将台账收口合入。

