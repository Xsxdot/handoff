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

未开始。
