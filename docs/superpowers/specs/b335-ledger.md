# B335 台账

- 2026-09-04 对话冻死：禁点名；origin 交接；未发布保留跨机锁。
- 定级 L1。基线 24aab70d。
- `go test ./internal/ledgerstep/ ./internal/ledger/` 绿；`TestSquadNode*` 与 `TestViaTemplate*` 绿；`go build ./...` 绿。
- `cmd.TestServePermissionHookDenyWithReasonAndStep0` 本机 unix socket bind 抖动，与本卡无关。
