# B310 台账

- 2026-09-03 用户截图：38c9c82d 等三任务 `go test` auto-allow 后 run_command ACTIVE；以为无 permission-hook。不 reopen B309。
- 2026-09-03 linux-01 agentd pid 1870539 `db4d6e99+dirty1`：
  - hook **在**：`handoff permission-hook --sock …/38c9c82d-…/perm.sock` pid 1870923 `wchan=ep_poll`，unix ESTAB 到 agentd fd。
  - HOME 与 worktree `.agents/hooks.json` 各一份相同 `handoff-safety-gate`。
  - `out.jsonl` 停在 step_2 ACTIVE `go test -v -run TestPurgeKeepsMessageExactlyAtCutoff ./internal/core/retention/`；`init.cwd` 已是 worktree；agy.log 空。
  - agentd：10:56:40.220 收到 `step_2` → auto-allow go-test → 10:56:40.228 `自动放行已回传` / `已下发权限裁决 allow`；10:56:40.248 **第二次** 收到 `step_2`，无第二次回传。无「发生重连」（第一次 Respond 已清 pending）。无「裁决目标不存在」。
  - 任务 `state=running pending=0`。af9bed0c / 49d6c7e6 同形。
- 2026-09-03 首红 `go test ./internal/agentd -count=1 -run TestSafeCommandPermissionAuditsOnceWithoutTicket`：`replayed responses = [safe-1:once], want two once deliveries`，退出 1。
- 2026-09-03 `handlePermission` replay 路径补 `respondAutoAllowReplay` 后同测试 `ok`。`go test ./internal/agentd -count=1` → `ok … 91.831s`；`go build ./...` 退出 0。
- 2026-09-03 变异：去掉 `respondAutoAllowReplay` 调用；`go build ./internal/agentd` 0；同测试再红 `want two once`；还原绿。
