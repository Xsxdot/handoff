# B373 台账

- 2026-09-15 用户裁决：OpenCode 做仓内 monitor 插件；Command Code 改 skill 用不带 `--follow` 的一次性 wait，每收到工单/事件即退出。卡 B373，定级 L1 快道。
- 2026-09-15 分支 `feat/b373-opencode-monitor` 从 origin/main `41a284746` 起。不碰 `fix/web-tab-unmount-regression` / `docs/superpowers/specs/b358.8.md`。
- 2026-09-15 落点：`plugins/opencode-monitor/handoff-monitor.ts`；`internal/skill/plugin.go` `InstallPlugin`/`PluginStatus`；`cmd/skill.go` 状态与 install 追加插件行；`main.go` go:embed；skill/README 分支；不改 `opencode.json`；无 `.config/opencode` 不造目录。
- 2026-09-15 插件：stdout 按行叫醒、250ms 合并、busy 排队、`promptAsync` 后记 busy、stderr 只记日志、Unix 进程组杀树、会话删除/dispose 静默停。
- 2026-09-15 `go test ./internal/skill/ ./cmd/ -count=1` 首次 cmd 红：`TestServePermissionHookDenyWithReasonAndStep0` `listen unix …/perm.sock: bind: invalid argument`。与 B373 无关，长测试名 + `t.TempDir()` 顶过 macOS sun_path 104。该测试改用已有 `shortSockDir`。重跑整包绿：`ok internal/skill 0.541s`、`ok cmd 9.815s`。`go build ./...` 退出 0。
- 2026-09-15 变异 1：`InstallPlugin` 缺 OpenCode 时 `StateSkipped`→`StateInstalled`（命中 1、`go build ./...` 0）。`TestInstallPluginSkipsWhenOpenCodeMissing` 红 `state=installed，期望 skipped`。已还原。
- 2026-09-15 变异 2：SKILL.md `每次收到一个事件`→`每次收到两个事件`（命中 1、编译过）。`TestSkillTextBranchesWaitModes` 红 `Command Code 必须写明每次收到事件就退出`。已还原。
- 2026-09-15 提交：`git commit` 退出 0，stdout `[feat/b373-opencode-monitor 0caaa9f83] feat(B373): OpenCode monitor 插件 + Command Code 一次性 wait`，15 files changed, 737 insertions(+), 17 deletions(-)。随后 amend 一次收进本行。
- 2026-09-15 review（独立子代理）：Important-1 SKILL 把「一次性 card wait 每事件退出」写成纪律，但 `card wait` 跟流直到终态、无 `--follow`。Important-2 flush 非 busy 失败立刻重入可 livelock。已修：任务级 wait 写每次收到事件退出；card wait 无行叫醒时改走任务级 wait；插件 dead set + 失败不立刻 flush。`go test ./internal/skill ./cmd -run TestSkill|TestInstallPlugin|TestPluginStatus` 绿。
