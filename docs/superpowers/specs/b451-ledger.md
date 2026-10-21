# B451 台账

## spec（2026-10-21，主会话）

- 卡：`handoff card add` → B451（charter v15，待办）；`card move B451 spec`；note seq 29058 记来源。
- 分支：`git checkout -b cards/B451-charter-1 origin/main`（基线 6de4b8e8）。
- 生产读数（linux-01，任务 c01a8fb9，E21 implement，opencode deepseek-v4.1-flash）：`handoff show` 最近 100 事件 = permission_auto_allow 48、permission_request 21、ticket_answered 25、approver_decision 5；permission_request 理由 20 条为「人工清单：内联 Python（标准输入脚本）」；人工答复间隔 8–12s；13:19 起 1 张待答。opencode 库同会话工具计数 bash 82 / write 9 / edit 7 / read 2，工具错误 0。
- 代码读数：人工清单判据 `internal/permgate/manualgate.go:48-82`；调用点 `internal/permgate/permgate.go:292-298`（在 `judgeCommand` 之前）；Consult 出口 `internal/orchestration/manager.go:2090-2108`；审批者收 `ev.Text` 全文 `manager.go:2366`；审批者 escalate 走 `escalatePermission`（无理由）`manager.go:2440`；提示词模板 `internal/orchestration/approver.go:703-712`。
- 受影响测试：`internal/permgate/manualgate_test.go`、`internal/permgate/selfcmd_test.go:185`、`internal/orchestration/permgate_wire_test.go:352`（无复用腿）与 `:390`（复用腿）。
- 共享审批链现状：`handoff approver show` → 版本 1，linux-01/agy → linux-01/codex gpt-6-luna，单环 1m。
