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
- 2026-08-28：按补充规则抓取 `origin/cards/B284-charter` 并将当前分支对齐到 `6c30d49c`；复核确认 `go.mod` 为 `github.com/Xsxdot/charter/graph v0.10.0`、spec 存在。命令 `GOMODCACHE=/root/.handoff/tmp/e0eaac48/gomodcache GOSUMDB=off GOPROXY=direct go list -m github.com/Xsxdot/charter/graph` 退出 0，原始输出：`github.com/Xsxdot/charter/graph v0.10.0`。
- 2026-08-28：命令 `GOMODCACHE=/root/.handoff/tmp/e0eaac48/gomodcache GOSUMDB=off GOPROXY=direct go run github.com/Xsxdot/charter/graph/cmd/codegraph --help` 退出 0；原始输出包含 `Available Commands` 下的 `flow` 与 `tree`（完整输出见下）。
  ```text
  go: downloading github.com/Xsxdot/charter/graph v0.10.0
  go: downloading github.com/spf13/cobra v1.10.2
  go: downloading github.com/spf13/pflag v1.0.9
  查询仓库内的代码图（codegraph/*.json，本地只读）

  Usage:
    codegraph [command]

  Available Commands:
    absorb      把分支视图 diff 併入 baseline 并删除该 diff（分支合并回主线后执行）
    chain       焦点的下游调用链（多个焦点取并集）
    check       目标图契约对照：实际跨域边 ⊆ target.json 声明的契约面，违规即非零退出
    completion  Generate the autocompletion script for the specified shell
    context     装配一个最优树领域的声明、接口、主链与实体上下文（无 best.json 时降级为现状领域）
    contract    维护目标图中的跨领域契约
    domains     列出领域树（职责、成员统计、对外接口）
    entity      数据实体的投影链：typed/handroll 投影点 + 跨语言孪生（序列化边界四查入口）
    flow        一个方法怎么走——控制流步骤树，不是 chain 的调用图切片
    help        Help about any command
    migrate     将 v2 target.json 与 baseline.json 机械迁移到 v3 与 best.json
    resolve     校验 file#Symbol 符号锚，或批量检查文档（坏锚即非零退出）
    summary     图摘要（供会话开局注入：规模、领域数、查询子命令菜单）
    sym         单点符号查询：位置（已再锚定）、签名、字段、摘要、归属
    tree        调用树（缺省向下；--up 向上；--through/--from 卡住走廊）
    validate    校验基线与全部视图的引用完整性（--stale 加保鲜检查），问题即非零退出
    version     输出 codegraph 版本
    views       列出可用视图（codegraph/diffs/ 下的文件名）
    who-calls   谁调用了焦点——上游影响面（多个焦点取并集）

  Flags:
        --base string   棘轮基准 revision（缺省取默认分支 merge-base）
    -h, --help          help for codegraph
        --repo string   目标仓库根目录 (default ".")
        --stale         附带保鲜检测结果
        --view string   叠加的视图名（codegraph/diffs/<名>.json）

  Use "codegraph [command] --help" for more information about a command.
  exit_code=0
  ```
- 2026-08-28：命令 `GOMODCACHE=/root/.handoff/tmp/e0eaac48/gomodcache GOSUMDB=off GOPROXY=direct go build ./...` 第二次实跑退出 0；本次原始输出为空。首次同命令在工具等待窗口输出依赖下载，未返回明确退出码，故不作为结论；随后确认无构建进程并以本次明确退出 0 的结果为准。
- 2026-08-28：命令 `GOMODCACHE=/root/.handoff/tmp/e0eaac48/gomodcache GOSUMDB=off GOPROXY=direct go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . flow --help` 退出 0；原始输出：
  ```text
  一个方法怎么走——控制流步骤树，不是 chain 的调用图切片

  Usage:
    codegraph flow <节点 id 或名字> [flags]

  Flags:
    -h, --help   help for flow

  Global Flags:
        --base string   棘轮基准 revision（缺省取默认分支 merge-base）
        --repo string   目标仓库根目录 (default ".")
        --stale         附带保鲜检测结果
        --view string   叠加的视图名（codegraph/diffs/<名>.json）
  exit_code=0
  ```
