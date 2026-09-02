# B305 台账

- 2026-09-02 linux-01 agy 1.1.24 strace print-mode：open `$HOME/.gemini/config/hooks.json`（ENOENT）、`antigravity-cli/hooks.json`、`$HOME/.gemini/hooks.json`；workspace `.agents/hooks.json` 从未出现。`hooks_manager loaded 0 named hooks from 0 hooks.json file(s)`。
- 同日：`HOME=/tmp/fake` + `$HOME/.gemini/config/hooks.json` → `loaded 1 named hooks`；`appDataDir=/tmp/fake/.gemini/antigravity-cli`；用户 `~/.gemini/config/hooks.json` 仍不存在。
- 同日：`--dangerously-skip-permissions` 日志 `auto-approving all tool permissions`；deny hook 开火但 `uname -s` 仍输出 `Linux`。
- 同日：`toolPermission: always-proceed` 同样 ignore deny，uname 跑出。
- 同日：`permissionOverrides` + `decision:allow` 仍 `Print mode: soft-denying tool confirmation RunCommand`。
- 同日：`permissions.allow: ["command(*)"]` 下 deny / exit 1 / 空 stdout 均拦不住 uname。
- 同日：`command(echo)` + deny → `tool call denied by pre-tool hook`；`command(git status)` + deny → 同样被拦。
- 同日：`command(uname)` + deny → 仍输出 `Linux`（原生例外，本卡 allow 清单不收录）。
- 同日：`command(go)` + deny → `tool call denied by pre-tool hook: go-deny`；`command(go)` + allow → `go env GOVERSION` 输出 `go1.26.1`。
- 同日：B304 隔离 task `aa208c7b` symlink 全局 hooks 后：工单 → approve `echo hello-b304-agy`（命中用户 settings.allow）completed；复合 echo+git 被原生 soft-deny。symlink 已拆。
