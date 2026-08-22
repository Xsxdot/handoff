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

未开始。

## Task 3 · 收口

未开始。
