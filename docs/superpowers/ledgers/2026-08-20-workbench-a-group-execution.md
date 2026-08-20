# 工作台 A 组执行账本

本文件记录各 task 的实际验证结果、修复轮次与提交范围；结论只写已亲自执行的命令结果。

- Task 1 修复轮 1：测试夹具由 `newTestStore` 调整为现有 `seedStore`，因为 `mk` 依赖默认工作流；原始失败为 `建卡取工作流 "bug": ... 记录不存在`。范围：`internal/ledger/cards_test.go`。
- Task 1 完成：`go test ./internal/ledger/ -count=1` 通过；`grep -rn '\.Subtree(' --include='*.go' .` 仅命中 `cmd/card_wait.go`、账本内部定义及测试。提交范围：Task 1 代码、测试、注释与本账本文件。
- Task 2 完成：后端 `go test ./internal/agentd/ -count=1` 通过（`ok ... 102.064s`）；前端 `npx vitest run` 通过（81 files、826 tests）；详情附加查询失败降级带 `Warn`，子任务区空时不渲染。测试夹具因仓库没有计划所称 `newLedgerEnv` 而补建并复用。提交范围：Task 2 后端 API、测试、前端类型/区块/测试与本账本文件。
- Task 3 完成：后端 `go test ./internal/agentd/ -count=1` 通过（`ok ... 104.106s`）；前端 `npx vitest run` 通过（81 files、828 tests）；验收事件固定 `verified=true`，空/空白证据后端 400，未知卡 404，成功日志仅记录证据字节数。提交范围：Task 3 验收 API、测试、前端 API/表单/三态 chip 与本账本文件。
- Task 4 基线：`go test ./cmd/ ./internal/ledgerstep/ -count=1` 两包通过；verbose 统计 247 条 PASS，关键用例含 `TestCardDispatchClaimAndSnapshot`、`TestCardDispatchFailureReleasesLease`、四条纪律块派发回归，以及 `TestReviewStepPassAndFailLoop`、`TestMergeStepDecision`。搬迁后同范围仍 247 条 PASS，`go test ./cmd/ ./internal/ledgerstep/ ./internal/ledger/ -count=1` 与 `go build ./...` 均通过。提交范围：`internal/ledgerstep/dispatch.go`、`runner.go`、`dispatch_test.go`，CLI 装配与测试搬迁，以及本账本文件。
- Task 5 修复轮 1：测试辅助未登记 `demo` 项目导致首次环节启动原始失败 `卡 B1 的项目 "demo" 未在本机登记，先 handoff project add: 项目 demo: 记录不存在`，且回调重复关闭 channel 触发 `panic: close of closed channel`；补齐测试项目登记并让测试回调幂等。范围：`internal/agentd/cardstep_test.go`。
- Task 5 完成：`go test ./internal/agentd/ -run '^TestStartCardStep' -count=1 -race` 通过；全包 `go test ./internal/agentd/ -count=1 -race` 首次原始失败为 `TestWatchdogRefiresStalledAfterReply`（`无活动后 stalled 应保持 2 条，实际 3（二次告警刷屏）`），再次原命令通过（`ok ... 122.070s`）。双裁决结论：spec 符合——项目路径走登记、环节仅 review/merge、占位后异步、同卡互斥且完成释放；质量符合——共享状态由互斥锁保护，生产/失败/结束日志齐备，无 `fmt.Printf`。提交范围：`internal/agentd/cardstep.go`、`cardstep_test.go`、`server.go` 与本账本文件。
