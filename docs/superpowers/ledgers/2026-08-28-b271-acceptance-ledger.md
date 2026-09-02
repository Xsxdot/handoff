# B271 acceptance 台账

- 对象：`cards/B271-charter-2` @ `390fb8a69`，加上验收夹具补丁。
- 审查 `node:review` pass，findings 空。implement minor：`cmd/init_test.go` `/tmp/handoff.yaml` 只读，与 B273/B276 同族环境失败，不修。
- 无代码图视图 diff，跳过图对账。
- 2026-08-28 协调者复跑（Mac 工作树）：`go test ./internal/config ./internal/store ./internal/ledgermirror -count=1` 退出 0；`go test ./internal/ledgerstep -run 'TestViaTemplate|TestStepRunner|TestRunnerLocal|TestBuildPrompt' -count=1` 退出 0；`go test ./cmd -run 'Test(TargetEndpoint|BareDispatch|CardDispatch|TargetClient|NamedTarget)' -count=1` 退出 0。
- 2026-08-28 首轮 `go test ./internal/agentd -run 'Test(CardStep|Mirror|LocalStep|WSOutOfOrderPublishNotDropped|Local)'` 红：`TestCardStepReturns202` 等 400，原文 `agentd 地址不含主机名（原始地址 "http://")`。原因：空 target 探本机 Status，`newLedgerEnv`/`newTestAgentdEnv` 的 Listen 为零。linux-01 implement 只修了 `newNoPTYLedgerEnv`，没修共享夹具。
- 2026-08-28 夹具补丁：`newTestAgentdEnvWithCfg` 回填 httptest Listen；`newLedgerEnv` 挂最小 Manager 并写入 `review` 测试纪律。补后 `go test ./internal/agentd -run TestCardStep -count=1` 退出 0，`ok ... 2.012s`。再跑上列合集全绿，`internal/agentd` `ok 1.923s`。
- 2026-08-28 `go build ./...` 退出 0。
- 变异（均先 `go build` 过，对应缝级测试转红后 `git checkout -- .`）：
  - M1 空 target 再拒发 → `TestViaTemplateEmptyTargetIsLocal` FAIL `目标机未定`
  - M2 `isLocalTarget` 恒 false → `TestMirrorLocalTargetUsesLocalSource` FAIL `一秒内没有从本机 Store 镜像 permission_request`
  - M3 `remoteMachineNames` 不跳过自机 → `TestMirrorDiscoverOnceSkipsSelfTarget` FAIL `本机 target ListTasks 请求数 = 1, want 0`
  - M4 恢复 WorkBranch 空串短路 → `TestViaTemplateSelfAliasContinuesLocalWorkBranch` FAIL `上次目标机 ""，本次目标机 ""`
  - M5 CLI 空 target 再拒 → `TestTargetClientEmptyAndConfiguredSelf` FAIL `未指定目标机`
  - M6 乱序迟到改 continue → `TestWSOutOfOrderPublishNotDropped` FAIL `乱序迟到事件被静默丢弃`（90s）
- 真机：`TestLocalStepTransportUsesLocalClient`、`TestLocalStepDisciplineProbesStatus`、`TestMirrorLocalTargetUsesLocalSource`、`TestTargetClientEmptyAndConfiguredSelf` 走真实 httptest HTTP/WS/Store。生产 agentd 仍是旧二进制，本卡不重装；本机 `card dispatch --step` 无 `--target` 要等本机 agentd 换成这版才生效。
- 用户 2026-08-28 老样子授权合 main 并 push。
