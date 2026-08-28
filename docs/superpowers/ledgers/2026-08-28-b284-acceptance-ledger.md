# B284 acceptance 台账

本文件记录 B284 acceptance 节点的实际命令输出、判断与提交；边界是本卡复核与台账，不修改 charter 升版实现。

- 2026-08-28：当前工作树为 `/root/.handoff/worktrees/3d14170f`，分支 `cards/B284-charter`，工作树初始干净，HEAD 为 `5d733488 test(b271): 空 target 探活后补齐共享 ledger 夹具`。
- 2026-08-28：注入的 `docs/superpowers/specs/b284.md` 不存在；`git show 00e8c5757` 返回 `fatal: ambiguous argument '00e8c5757': unknown revision or path not in the working tree.`；本轮不据此改升版配置。
- 2026-08-28：当前 `go.mod`、`go.sum`、`desktop/go.sum` 仍标记 `github.com/Xsxdot/charter/graph v0.9.0`，由 `rg` 实际输出确认。
- 2026-08-28：命令 `go list -m github.com/Xsxdot/charter/graph` 退出 0，原始输出：`github.com/Xsxdot/charter/graph v0.9.0`。
- 2026-08-28：命令 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --help` 退出 0；原始输出的 `Available Commands` 为 `absorb, chain, check, completion, context, contract, domains, entity, help, migrate, resolve, summary, sym, validate, version, views, who-calls`，不含 `flow` 或 `tree`。
- 2026-08-28：命令 `go run github.com/Xsxdot/charter/graph/cmd/codegraph flow --help` 退出 1；原始输出：`Error: unknown command "flow" for "codegraph"`、`Run 'codegraph --help' for usage.`、`exit status 1`。
- 2026-08-28：命令 `go build ./...` 退出 0；无标准输出。
- 2026-08-28：法定四条中第 1、2、3 条未通过，第 4 条通过；按本卡补充规则不能裁决 pass。
- 2026-08-28：`git diff --check` 退出 0 且无输出；提交前状态仅有本台账未跟踪。
- 2026-08-28：`git add docs/superpowers/ledgers/2026-08-28-b284-acceptance-ledger.md && git commit -m "docs(b284): record acceptance failure"` 退出 0，原始输出：`[cards/B284-charter 23126c6f] docs(b284): record acceptance failure`，提交后 `git status --short --branch` 仅输出 `## cards/B284-charter`，`git rev-parse HEAD` 输出 `23126c6f4fa73071d7380a560de1d7616ef5fdf2`。
