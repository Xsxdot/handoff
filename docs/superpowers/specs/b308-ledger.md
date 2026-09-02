# B308 台账

- 2026-09-02 用户：登录通了，批次停在权限门。d6740f8c `pwd && git status`：approver_decision 后仍 `user denied`。linux-01 agentd `5915c7be+dirty1` 未重启。要求扩 allow 或改匹配，换生产二进制后说一声。
- 2026-09-02 任务 `d6740f8c` settings.allow 即 B305 名单，无 pwd。agy.log：`required the "command" permission … e.g. command(<target>)`。out.jsonl：`CommandLine: pwd && git status` → `user denied permission`。
- 2026-09-02 扫 `/root/.handoff/tasks/*/out.jsonl` 的 run_command：denied 6 条全部以 `pwd &&` 开头；`git status && …`、`ls -la …`、`find …` 未进 denied。结论：首词 `pwd` 不在名单，不是「复合命令整段匹配失败」。
- 2026-09-02 用户本条选定扩 allow。开卡 B308，不 reopen B305，不并进 B306。
- 2026-09-02 首红 `go test ./internal/executor/agy -count=1 -run TestNativeCommandAllowCoversCompoundFirstTokens`：`nativeCommandAllow 缺 command(pwd)`，退出 1。
- 2026-09-02 加入 `command(pwd)`/`command(cd)` 后同测试 `ok`。包测试跳过预存 macOS perm.sock sun_path：`go test ./internal/executor/agy -count=1 -skip 'TestAdapterRespondPermission|TestPermServer'` → `ok … 1.390s`。`go build ./...` 退出 0。
- 2026-09-02 变异：`command(pwd)`→`command(pwx)` 唯一处；`TestNativeCommandAllowCoversCompoundFirstTokens` 断言缺 pwd，退出 1；还原后 `ok`。
