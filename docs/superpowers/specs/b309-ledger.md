# B309 台账

- 2026-09-03 用户停跑分：B308 后登录与复合命令已过；`pwd && git status` cwd 为 `agyhome/.gemini/antigravity-cli/scratch`，`fatal: not a git repository`。不 reopen B308。
- 2026-09-03 linux-01 任务 `5530b65b-2096-432e-b091-ca0533151746`，agentd pid 1861240 版本 `c51b6313+dirty1`：
  - `proc.json` `mark_root=/root/.handoff/worktrees/5530b65b`
  - `shim.log`：`dir=/root/.handoff/worktrees/5530b65b`
  - `out.jsonl` `init.cwd` 同为该 worktree
  - `run_command` 参数仅 `CommandLine: pwd && git status`（无 `Cwd`）；output 为 scratch 路径 + `fatal: not a git repository`
  - 随后 `find`/`ls` 任务目录、`view_file` `spec.json` → `user denied permission for read_file(...)`
  - `agy.log` 仅 headless `read_file` soft-deny 提示
  - `agy --help`：`--add-dir  Add a directory to the workspace (repeatable) (default [])`
- 2026-09-03 根因：`agyArgv` 不传 `--add-dir`。隔离 HOME 下工具工作区默认 scratch，与 OS cwd 脱钩。
- 2026-09-03 首红 `go test ./internal/executor/agy -count=1 -run TestAgyArgv`：`got [... --print-timeout 24h], want [... --add-dir /worktrees/T1]`，退出 1。
- 2026-09-03 `agyArgv` 在 RepoPath 非空时追加 `--add-dir` 后同测试 `ok … 0.447s`。包测试 skip perm.sock：`ok … 1.457s`。`go build ./...` 退出 0。
- 2026-09-03 变异：`--add-dir` 值改为 `/wrong`；`go build ./internal/executor/agy` 退出 0；`TestAgyArgv/有_RepoPath` 断言失败 `got --add-dir /wrong`，退出 1；还原后 `ok`。
