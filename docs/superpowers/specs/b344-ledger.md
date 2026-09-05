# B344 台账

- 2026-09-05 用户要做远端协调者终端；不做 TUI auto bind。
- 2026-09-05 定级 L2，本会话 grok 本地实现。
- 2026-09-05 测试先红：尚未接线；关 tab 未调远端删除。接线后 `go test ./internal/agentd/ -run TestOpenCoordinatorTUI` 绿。变异还原「尚未接线」后 OpenRemotePty 红。未用 checkout。
