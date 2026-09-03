# B315 台账

- 2026-09-03 用户：补丁链质疑；确认 skip-permissions + matcher `*` 为一扇门。写文件 canary 合 main 后隔离 HOME 再做，不碰生产 agentd。
- 2026-09-03 `agyArgv` 恒带 `--dangerously-skip-permissions`；PreToolUse matcher `*`；gate 只写任务 HOME。`permTextAndRequest`：`write_file` 当写，读工具带路径，未识别 `Other`。
- 2026-09-03 `go test ./internal/executor/agy -count=1 -skip 'TestAdapterRespondPermission|TestPermServer'` → `ok … 1.452s`（跳过的是既有 macOS perm.sock sun_path）。`go build ./...` 0。
- 2026-09-03 变异：去掉 argv 中 skip-permissions；`go build ./internal/executor/agy` 0；`TestAgyArgv` 红 `want … --dangerously-skip-permissions`。还原。matcher `"*"`→`"run_command"`；`TestWriteTaskEnv` 红 `matcher 必须是 *`。还原绿。
- 2026-09-03 linux-01 隔离实例（未碰生产 pid 1966371 :7777）：源码 rsync `/tmp/handoff-b315-src`，二进制 `/tmp/b315-iso/bin/handoff`，agentd `127.0.0.1:17777` pid 2288244，HOME=`/tmp/b315-iso/home`，datadir=`/tmp/b315-iso/data`，无生产 ledger DSN。用户 `~/.gemini/config/hooks.json` 全程不存在。
  - 任务 `8604058a` `--executor agy`：argv 含 `--dangerously-skip-permissions --add-dir <worktree>`；HOME hooks matcher=`*`；worktree 无 `.agents/`。
  - `write_to_file` CANARY.txt：13:51:50.732 收到 step_30 → 13:51:50.735 `allow`（范围内自动放行，无工单）；文件存在，内容 `b315-write-ok`。第二次 step_32 同路径再 allow（agy 自己打两次，不是双文件 hook）。
  - skip-permissions 下 hook deny 仍有效：`ps aux | grep b315` → `tool call denied by pre-tool hook` + 协调者 reason。
  - 范围内 `list_dir` 当时未映射路径 → 升级工单（已补 `DirectoryPath`）。越界 `view_file /tmp/b315-iso/config.yaml` 正确升级。
  - 隔离 agentd 已停，17777 gone，生产仍 1966371。
- 2026-09-03 finish 合 main 前新鲜测试（本机 macOS）：`go build ./...` 0；`go test ./internal/executor/agy -count=1 -skip 'TestAdapterRespondPermission|TestPermServer'` → `ok … 1.208s`（跳过的是既有 perm.sock sun_path 107）。全量 `go test ./...` 另有三条与本卡无关的红：`cmd.TestServePermissionHookDenyWithReasonAndStep0` 同 sock 路径；`grok.TestWriteTaskEnvOmitsModelSectionWhenEmpty` 读本机 `~/.grok` 把 default 写成 grok-4.6；`hostapi.TestWakeHome*` 全量负载下 1s 超时、隔离复跑绿。本卡未改这些包。
