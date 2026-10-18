# 共享审批链 spec 台账

- 2026-10-19 开工：worktree `agitated-bun-cd484f`，分支 `claude/agitated-bun-cd484f`，基线 main `c6619013`，工作树干净。`handoff card move B437 spec` 成功（status=spec）；`handoff card bind B437` → `{"ok":true}`。首次 `card show` 撞 PG TLS EOF（经 socks5 代理），重试即通，瞬时网络问题。
- 2026-10-19 事实调查（Explore 子代理，只读）：`ApproverConfig` 在 `internal/config/config.go:212-229`（Executor 有序表 / Model / Models / Timeout / Blacklist）；`validate` `:623-665`；启动一次装配 `cmd/agentd.go:162,168,207,641-676`，不热加载；运行入口 `Manager.handlePermission` `internal/orchestration/manager.go:2067-2109`（非 opencode）与 `internal/approval/client.go:318`（opencode）；逐候选 `Approver.Decide` `internal/orchestration/approver.go:201-249`、`decideWithCandidate` `:256-319`，时限按候选计；全败 → `escalatePermission`，连续 3 次 → `approver_disabled`（`manager.go:647,2529-2544`）；审批路径无任何机器概念（`internal/executor/oneshot_cap.go:40-47`）。
- 2026-10-19 读码自核：`OneShotReq` 只有 Prompt/Model/Workdir/HomeDir/Env/Limits，审批提示词模板 `approver.go:387-394` 只含任务背景与权限原文，`decideWithCandidate` 不传 Workdir → 裁决不依赖发起机文件系统，可原样在他机执行。env 取自 `a.env.For(name)`（本机 env 文件），远端执行自然用远端 env。
- 2026-10-19 设备表：`internal/ledger/store.go:405-413` devices（name PK、target、instance_id、attrs JSONB）；`attrs` 读口 `DeviceAttrs` 无调用方；跨机池 `targetclient.Pool.For(name)` 经 `agentd.DeviceTableSource`。对端鉴权 `Server.auth` 比对本机 token（`internal/agentd/server.go:1056-1066`）。约 140 条路由无 OneShot 代跑端点。
- 2026-10-19 账本已有版本化 KV `registry(kind,id,version,seq,body)`（`internal/ledger/store.go:399-404`、`internal/ledger/registry.go`，CAS 写、账本不解释 body，载体/小队在用）→ 共享链落此原语，不新建表。
- 2026-10-19 控制台：B160 把 `Approver.*` 列为安全链不进设置页（`docs/superpowers/specs/2026-08-19-b160-general-settings-design.md:58`）；审批配置未经账本同步。
- 2026-10-19 卡 note 顺带观察核实：`cd <wt> && go test … > "$TMPDIR/x.log"` 在 `internal/permgate/cdbase.go:70-73` 无法解析重定向目标 → `permgate.go:240-243` 硬 Escalate，不经审批链；roadmap 已有条目（B439 协调者）。判：与 B437 改动面不重叠，不并入。
- 2026-10-19 写 spec r1 草稿 `docs/superpowers/specs/b437.md`，定级 L3、不扇出；待用户批准。
- 2026-10-19 提交 spec r1：`git commit` → `262c312a docs(b437): spec r1 草稿——共享审批链跨多机逐环裁决（L3，待批准）`；随后 amend 收进本行。OOS 三条「本期不做」落 `docs/roadmap.md`。
