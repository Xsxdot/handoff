# 需求 A 第二批实现 ledger（T4 grok / T5 opencode）

基线分支：`handoff/timing-batch2`。执行范围只含本计划的 T4、T5 与 Task 3
遗留修复；没有派发子任务、没有切换分支、没有 push。

## 基线

- 工作树：已确认是 `/root/.handoff/worktrees/d278cc18` 的链接 worktree，分支为
  `handoff/timing-batch2`，起始 HEAD 为 `ada1ff32`（计划文档提交，父提交
  `27212adb`）。
- `go test ./internal/executor/...` 实测：executor、codex、fake、grok、opencode、
  rawtap、turn 输出 `ok`；claudecode 失败。原始失败包括：
  `裁决 socket 路径过长（114/115/116 字节，上限 107）`、
  `TestResumeContinuesFromOffset` 的同类路径错误，以及
  `TestClaudeToolTimingPaired` 失败。该命令最终输出 `FAIL`；未将其记为本批目标包的失败。

## Task 1 · T4 grok

- RED：新增 `internal/executor/grok/timing_test.go` 后运行
  `go test ./internal/executor/grok/ -run 'TestGrokToolTiming|TestGrokUnknownToolStatus' 2>&1 | tail -20`。
  输出为预期编译失败：`a.reportTiming undefined`、`r.seg undefined`、
  `unknown field seg in struct literal of type runState`。
- 实现文件：`internal/executor/grok/adapter.go`、
  `internal/executor/grok/timing_test.go`。
- 实现内容：Segmenter 初始化与回合边界打点、`reportTiming`、grok 工具字段解析、
  终态映射、工具帧与耗时分段、帧写入前计时、未知状态保守跳过。
- `toolNames` 决策：未保留。实现完成后确认没有第二个读者；`tool_call.title` 已直接
  写入 Segmenter 的 Label 与 tool_call 帧，tool_call_update 不需要通过名称反查。
- GREEN：`go test ./internal/executor/grok/ -run 'TestGrokToolTiming|TestGrokUnknownToolStatus'`
  输出 `ok github.com/Xsxdot/handoff/internal/executor/grok 0.006s`。
- Task 1 双裁决：规格覆盖通过；代码质量的 `gofmt`、`git diff --check` 与
  `go test ./internal/executor/grok/ 2>&1 | tail -20` 均已实测通过，后者输出
  `ok github.com/Xsxdot/handoff/internal/executor/grok 1.537s`。
- commit 范围：`internal/executor/grok/adapter.go`、
  `internal/executor/grok/timing_test.go`、本 ledger；提交信息按计划为
  `feat(grok): 补工具帧与耗时打点`。

## Task 2 · T5 opencode

- RED：新增 `internal/executor/opencode/timing_test.go` 后运行
  `go test ./internal/executor/opencode/ -run 'TestOpencodeToolTiming|TestOpencodeErrorToolStatus' 2>&1 | tail -20`。
  输出为预期编译失败：`a.reportTiming undefined` 与 `r.seg undefined`。
- 修复轮次 1：补实现后目标测试首次编译失败，原始错误为
  `internal/executor/opencode/adapter.go:993:55: undefined: proto`；补入既有
  `internal/proto` import。
- 修复轮次 2：import 修复后目标测试原始失败为
  `timing_test.go:51: 一次工具调用应恰好产一条 tool 条目，实得 0 条`。
  根因是测试直接以空 `FrameWriter.Turn()==0` 开始 Segmenter；按既有契约，
  `ToolStart` 对回合 0 丢弃信号。两个场景补写 `frames.BeginTurn("dispatch", "")`，
  没有改生产生命周期。
- 修复轮次 3：opencode 全包首次回归的原始失败为
  `TestSessionReadyProgress: 首事件类型=usage，期望 progress`、
  `TestIdleEmptyTurnSkips: 首事件类型=usage，期望 progress`、
  `TestApprovedPermissionEmptyTurnEmitsFailedResult: ...实际 {Type:usage ...}`、
  `TestSessionIsolationUsesPropertiesSessionID: ... {Type:usage ...}`、
  `TestSendUnparsableAnswerRepromptsAndKeepsPending: ...期望重发 question 工单`、
  `TestSendCustomRejectedByServerRepromptsAndKeepsPending: ...期望重发工单并说明原因`。
  原因是新增的合法首回合/续接 Timing usage 事件先于业务事件；行为测试辅助现在
  跳过 `usage` 且 `Timing != nil`，业务断言未改变。
- 实现文件：`internal/executor/opencode/adapter.go`、
  `internal/executor/opencode/timing_test.go`，以及为新增正常 Timing 事件更新读取
  语义的 `adapter_test.go`、`reconcile_internal_test.go`、
  `regression_group_a_test.go`。
- 实现内容：Segmenter 与阶段表初始化、两处 BeginTurn 与 mapIdle EndTurn、usage
  静默日志分支、`reportTiming`、单次 tool_call/tool_result 去重、callID→part.id
  回落配对、state.input/output 解析、`completed/error` 映射与墙钟耗时。
- GREEN：`go test ./internal/executor/opencode/ -run 'TestOpencodeToolTiming|TestOpencodeErrorToolStatus'`
  输出 `ok github.com/Xsxdot/handoff/internal/executor/opencode 0.006s`；
  `go test ./internal/executor/opencode/ 2>&1 | tail -20` 输出
  `ok github.com/Xsxdot/handoff/internal/executor/opencode 17.965s`。
- Task 2 双裁决：规格覆盖通过；代码质量的 `gofmt -l internal/executor/opencode/ internal/executor/grok/`
  无输出，`git diff --check` 通过，opencode 全包回归通过。测试读取耗时事件的
  调整仅用于适配新增事件流，不改变原有业务结果断言。
- commit 范围：`internal/executor/opencode/adapter.go`、
  `internal/executor/opencode/timing_test.go`、
  `internal/executor/opencode/adapter_test.go`、
  `internal/executor/opencode/reconcile_internal_test.go`、
  `internal/executor/opencode/regression_group_a_test.go`；提交信息按计划为
  `feat(opencode): 补工具帧与耗时打点`。

## Task 3 · 收口

- 改动：`internal/executor/claudecode/start_ordering_test.go`。将 usage 事件过滤循环
  的 `time.After(10 * time.Second)` 移到循环外的 `deadline`，保留原 10 秒时长与
  既有断言；防止不停产 usage 时每次迭代重置死线。
- 四家一致性只读复核：
  - `grep -c "func (a \*Adapter) reportTiming" internal/executor/{claudecode,codex,grok,opencode}/adapter.go`
    输出四个文件各为 `1`。
  - `grep -n -A3 "frames.BeginTurn" ...` 目视确认四家 dispatch/send 两处均紧跟
    `a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))`。
  - `sed` 目视确认 claudecode `mapResult`、codex/grok `finishTurn`、opencode
    `mapIdle` 的函数体第一条执行语句均为 `a.reportTiming(r, r.seg.EndTurn())`。
  - `grep -nE "claude|codex|grok|opencode" internal/executor/turn/timing.go`
    无输出。
- `go test ./internal/executor/claudecode/ -run '^TestStartWritesPromptBeforeWaitingReady$'`
  输出 `ok github.com/Xsxdot/handoff/internal/executor/claudecode 0.245s`。
- 计划指定的
  `gofmt -l internal/ && go vet ./internal/executor/... && go build ./... && go test ./internal/executor/... 2>&1 | tail -20`
  已实测：gofmt 无输出，vet/build 无错误输出；尾部显示 claudecode 失败、其余
  executor 包 `ok`。不带管道掩码并使用短临时目录的
  `TMPDIR=/tmp/handoff-timing-batch2 go test ./internal/executor/...` 仍失败，原始
  失败包含 `perm_test.go: 裁决 socket 路径过长（114/115/116 字节，上限 107）`
  与 `resume_test.go: TestResumeContinuesFromOffset ... 裁决 socket 路径过长`，
  以及 `TestClaudeToolTimingPaired` 的 `FAIL`；codex、fake、grok、opencode、
  rawtap、turn 均为 `ok`。该失败未修改无关测试或生产代码。
- Task 3 双裁决：规格方面完成指定 timeout 修复与四家只读核对；质量方面目标
  claudecode 顺序测试通过、gofmt/vet/build 命令链无前置错误，剩余全包失败已按
  原始输出记账。
- commit 范围：`internal/executor/claudecode/start_ordering_test.go` 与本 ledger；
  已提交为 `b9b1578a test(claudecode): 固定启动顺序测试死线`。

## 整分支终审（相对 `ada1ff32`）

- `git diff --name-status ada1ff32..HEAD` 仅列出本计划的 ledger、grok adapter/test、
  opencode adapter/test 辅助、claudecode timeout test；无计划外生产包或配置变更。
- `git diff --check ada1ff32..HEAD` 无输出，终审时工作树干净。
- `go test -count=1 ./internal/executor/grok/ ./internal/executor/opencode/` 输出：
  `ok github.com/Xsxdot/handoff/internal/executor/grok 1.354s`、
  `ok github.com/Xsxdot/handoff/internal/executor/opencode 17.897s`。
- `go vet ./internal/executor/...` 与 `go build ./...` 均无错误输出并返回成功。
- `go test ./...` 实测返回失败；根包、cmd 及除 claudecode 外的 internal 包均输出
  `ok`。原始失败仍为 claudecode `TestPermServerAskThenRespond`/
  `TestPermServerRespondUnknownID`/`TestPermServerReRegisterSameID` 的
  `裁决 socket 路径过长（114/115/116 字节，上限 107）`、
  `TestResumeContinuesFromOffset` 的同类路径超限，以及
  `TestClaudeToolTimingPaired` 的 `FAIL`。终审没有把这个基线环境问题扩展成无关修复。
- 终审结论：未发现需要集中修复的计划内代码问题；以上全仓测试阻断已原样留账。

## 补覆盖轮：四家 adapter 回合边界

本轮基线：`e5d1edf1`。本轮不改任何 adapter 实现，只改四家的 timing 测试搭台，
让生产路径自己喂 BeginTurn/EndTurn；四个 timing 测试文件中已无直接
`seg.BeginTurn`/`seg.EndTurn` 调用（`rg` 实测无输出）。

### grok

- 改动：`internal/executor/grok/timing_test.go`。假 ACP WebSocket 经真实
  `Adapter.Send` 发起回合，通知经真实 `acpHandler` 分流，终局响应经真实
  `awaitTurn`/`finishTurn` 收尾；断言两个模型段、工具段及 turn 条目数量，覆盖
  BeginTurn 与 EndTurn 的静默缺口。未知状态测试也改走同一真实回合路径。
- 定向结果：`go test ./internal/executor/grok/ -run '^TestGrok(ToolTimingPaired|UnknownToolStatusIsNotTerminal)$'`
  输出 `ok github.com/Xsxdot/handoff/internal/executor/grok 0.013s`；整包
  `go test ./internal/executor/grok/ 2>&1 | tail -20` 输出 `ok ... 1.364s`。
- 变异：临时删除 `finishTurn` 的 EndTurn 上报，目标测试原始失败为
  `timing_test.go:59: ...实得 1 个 api 条目`、`:62: ...实得 3 个`，以及未知状态
  `:112: ...实得 2 个`；恢复实现后整包通过。之后又以
  `go test ./internal/executor/grok/` 复跑整包，命令尾部为 `FAIL`（变异仍被测试
  罩住）。
- 双裁决：规格上真实 Send/finishTurn 边界均被驱动，未增加实现行为；质量上
  gofmt 与 grok 整包通过。
- commit：`5fceb5c7 test(grok): cover adapter timing boundaries`。

### opencode

- 改动：`internal/executor/opencode/timing_test.go`。用现有 `startFakeRun` 驱动
  真实 startRun/BeginTurn；工具 part 仍经 mapPartUpdated，收尾经真实 mapIdle；
  新增事件排空 goroutine，避免阻塞 emit；断言 BeginTurn 与 mapIdle EndTurn 各自
  贡献模型段。未改变 `frameKind("tool")` 断言。
- 结果：`go test ./internal/executor/opencode/ 2>&1 | tail -20` 输出
  `ok github.com/Xsxdot/handoff/internal/executor/opencode 17.870s`。
- 变异：临时删除 `mapIdle` 的 EndTurn 上报，整包命令输出
  `TestOpencodeToolTimingPaired ... 实得 1 个 api 条目`、
  `TestOpencodeErrorToolStatus ... 实得 1 个 api 条目`，并以 `FAIL` 结束；恢复后
  整包通过。临时删除 startRun 的 BeginTurn 上报，定向 timing 命令原始失败为
  `一次工具调用应恰好产一条 tool 条目，实得 0 条` 与
  `真实 BeginTurn 与 mapIdle EndTurn 都应收尾模型段，实得 0 个 api 条目`；恢复后
  定向测试通过。
- 双裁决：规格上两条真实边界与阻塞事件通道均有测试；质量上 opencode 整包通过，
  没有修改 adapter 实现或旧 tool 文本跳过判据。
- commit：`cb97cd5e test(opencode): cover adapter timing boundaries`。

### claudecode

- 改动：`internal/executor/claudecode/timing_test.go`。改为 Unix 测试，通过现有
  假 shim/短目录和持久在线 fake claude 驱动真实 Start/BeginTurn；夹具消息经真实
  mapMessage，最终 result 经真实 mapResult/EndTurn；新增断言两个 api 段和四次
  turn 信号。未改生产代码。
- 结果：`go test ./internal/executor/claudecode/ -run '^TestClaudeToolTimingPaired$'`
  输出 `ok github.com/Xsxdot/handoff/internal/executor/claudecode (cached)`。
  整包回归仍失败，原始失败为 `TestPermServerAskThenRespond`、
  `TestPermServerRespondUnknownID`、`TestPermServerReRegisterSameID` 的
  `裁决 socket 路径过长（114/116 字节，上限 107）`，以及
  `TestResumeContinuesFromOffset` 的同类 `115 字节` 路径错误；本轮新的
  `TestClaudeToolTimingPaired` 通过。
- 变异：临时删除 mapResult 的 EndTurn 上报，整包尾部明确出现
  `TestClaudeToolTimingPaired`：`实得 1 个 api 条目`、`实得 3 个 turn 条目`，
  并以 `FAIL` 结束；恢复后定向测试通过。临时删除 Start 的 BeginTurn 上报，
  定向测试原始失败为 `至少应有一条 tool TimingEntry`；恢复后通过。
- 双裁决：规格上真实 Start/mapResult 边界均被驱动；质量上假执行者可回收、短路径
  避免了本测试自身的 socket 问题，既有全包环境路径问题未扩大修复。
- commit：`dadb522b test(claudecode): cover adapter timing boundaries`。

### codex

- 改动：`internal/executor/codex/timing_test.go`。保留已有真实 finishTurn/EndTurn，
  将测试运行态接入假 app-server WebSocket，通过真实 Adapter.Send 驱动 BeginTurn；
  工具帧与 clock 注入断言不变。
- 结果：`go test ./internal/executor/codex/ 2>&1 | tail -20` 输出
  `ok github.com/Xsxdot/handoff/internal/executor/codex 5.930s`。
- 变异：临时删除 Send 的 BeginTurn 上报，定向测试原始失败为
  `TestCodexToolTimingPaired: 至少应有一条 tool TimingEntry` 与
  `TestCodexTimingShapeMatchesClaude: 实得 api=0 tool=0 turn=0`；整包命令以
  `FAIL` 结束；恢复后定向测试通过。EndTurn 变异属于本轮前已有覆盖，未改生产路径。
- 双裁决：规格上真实 Send/finishTurn 两端覆盖完整；质量上 codex 整包正常回归通过。
- commit：`8c717531 test(codex): cover adapter timing boundaries`。

### 本轮四项变异验收与门禁

- opencode `mapIdle` EndTurn 删除：整包 `FAIL`，两条 timing 用例报告 api=1。
- grok `finishTurn` EndTurn 删除：整包 `FAIL`；定向用例报告 api/turn 数量不足。
- claudecode `mapResult` EndTurn 删除：整包 `FAIL`，新的 timing 用例报告 api=1、
  turn=3。
- codex Send BeginTurn 删除：整包 `FAIL`；timing shape 报 api=0/tool=0/turn=0。
- `gofmt -l internal/` 无输出；`go vet ./internal/executor/...` 成功；
  `go build ./...` 成功。
- `go test ./internal/executor/... 2>&1 | tail -20`：codex、fake、grok、opencode、
  rawtap、turn 输出 `ok`；claudecode 以 `FAIL` 结束。失败仍是上述既有 socket
  路径超限用例，非本轮 timing 测试；没有为此修改无关实现或夹具。
- 本轮没有遇到需要人决策的问题；没有派发子任务、切分支或 push。
