# B322 台账

- 2026-09-04 开卡：来源用户报障（重启后一百多个 tab）+ 本会话核根因。卡在 charter 待办。
- 2026-09-04 核根因：机制链大体对，「agentd 重启窗口误剥」不是这次翻倍主因。更正已落卡。证据：08:53 reclaim live=7，10:27 live=104；7→104 发生在 09:11–09:55 agentd 存活期间；每次恢复固定 9 个 cwd 再建；落盘 132 组中 130 组为单终端收编形状；fanout machines=3 为 `''`+`linux-01`+`local` 自扇出。
- 2026-09-04 用户「你自主推进处理吧，修复完自己建隔离实例和通过无头浏览器进行验证」→ spec 按已批准回写。定级 L2。
- 2026-09-04 坐下失败：CLI 无 `HANDOFF_SESSION_CLI`/`HANDOFF_SESSION_ID`，空座推进不绑席。
- 2026-09-04 分支 `fix/B322-restore-pump`（仓内，非 worktree）。
- 2026-09-04 implement：workspace 收编停；TerminalTab spawn 门；`ptySessionsAll` 跳过 IsSelfTarget。vitest 触及文件 7 passed / 136；Shell+WorkbenchPage 71 passed；`go test ./internal/agentd -run TestPtyFanoutSkipsSelfTarget` ok；`go build ./...` 绿；`tsc -b` 绿。
- 2026-09-04 变异：restore 收编条件改回 B283 形态 → restore.test 1 failed（adopted 2≠0），还原绿。TerminalTab `!shouldSpawn` 取反 → B322 两条红，还原绿。pty_api `IsSelfTarget` 加 `false &&` → machines 出现 local，还原绿。
- 2026-09-04 隔离实例无头验收：`node web/scripts/verify-b322-restore.mjs`（临时 datadir、:18777 agentd、:5174 vite、Chrome headless CDP）。打开#1/#2 groups=1、reopen=1、uniqueLive=0；machines 不含 local。建 workspace PTY 返回 400（resolvePtyBase，未注册项目），活孤儿组数断言跳过，由 restore 单测覆盖。

