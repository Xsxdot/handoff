# B314 台账

- 2026-09-03 用户：B310 后 `go test` auto-allow 有结果；`git`/`go run` 审批 allow 后冻 ACTIVE、无工单。问是否停 4 道。不 reopen B310。
- 根因：B310 只对 auto-allow 审计重放回写；`tk.Answer != nil` 的审批工单重放仍跳过 Respond。双 hooks 第二次连 sock 永远等。
- 2026-09-03 四道挂死任务：**停**（hook 86400s / stall 2h）。linux-01 charter 不动。未部署。
- 2026-09-03 首红 `go test ./internal/agentd -count=1 -run TestAnsweredTicketReplayStillResponds`：`answered replay responses = [], want [step_2:once]` / `want [step_2:reject]`，退出 1。命令用 `git status && git rev-parse HEAD`（白名单连接符拒）避免误走 AutoAllow。
- 2026-09-03 `handlePermission` replay 补 `respondAnsweredTicketReplay`（`gateDecision` → `RespondPermission`）后同测试 `ok`。`go test ./internal/agentd -count=1` → `ok … 126.983s`；`go build ./...` 退出 0。
- 2026-09-03 变异：去掉 `m.respondAnsweredTicketReplay(...)` 调用（全仓唯一 1 处）；`go build ./internal/agentd` 0；同测试再红 `want [step_2:once]` / `want [step_2:reject]`；还原绿。
- 2026-09-03 finish 新鲜：`go build ./...` 0。`go test ./internal/agentd -count=1` 一次红 `TestPtyWSAttachedBacklogBytesKeyPresent`（`backlog_bytes = 162，期望 0`，PTY 新会话提示符，与本卡无关）；单测复跑 + 整包再跑 `ok … 95.291s`。无视图 diff（未导出方法，与 B310 同形）。用户裁本地合 main。
