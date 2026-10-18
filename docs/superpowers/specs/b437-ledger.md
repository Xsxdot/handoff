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
- 2026-10-19 批准：协调会话 local_30aaf694（用户全权授权）批准 r1，附修订「连接失败与裁决时限分开：他机环拨号独立短时限、快速失败；单环 timeout 只约束连上后等裁决；审计区分不可达/超时」。已并入 spec 成 r2（实现决定·时限、S3）。

## 真机验收（finish，2026-10-19，隔离实例）

被验对象（第一轮）：cards/B437-charter-3 @ d119ac80（go build，darwin 版 ad-hoc codesign）。
隔离实例：mini-b437（Mac mini 100.91.215.29:17777，~/.handoff-b437test）、l01-b437（linux-01 100.81.114.119:17777，/root/.handoff-b437test）、
mbp-b437（macbook-pro 100.91.173.63:17777，~/.handoff-b437test）、old-b437（linux-01 :17778，main c6619013 旧版二进制，/root/.handoff-b437old）、
hole-b437（仅设备表行，addr 100.64.0.250:17777 黑洞）。账本 = 测试库 handoff_b437_test（专属账号 handoff_b437，已登记 credentials.md）。
sync.auto=false、terminal.auto=false。共享账本 / mirror_lease 未触碰。在役 agentd 前后对照见文末。

| 项 | 结果 | 证据要点 |
|---|---|---|
| S1 写链校验 | 通过 | `approver set` 未知机器 nosuch → 「第 1 环的机器 "nosuch" 不在设备表；可用机器名: l01-b437, mini-b437」 |
| S1 一处配、处处见、不重启生效 | 通过 | Mac 写 v1，linux-01 `approver show` 同链版本 1；linux-01 写 v2/v3，Mac show 同版本；l01-b437 未重启，下一次裁决按 v2/v3 走 |
| S2 他机裁决 | 通过 | 任务在 l01-b437（opencode），链首环 mbp-b437/codex：approver_decision `machine=mbp-b437 executor=codex decision=escalate elapsed_ms=9665`，干净裁决即停，升级人工 |
| 端点直测 | 通过 | mbp-b437 `/api/approver/decide` codex → approve 9740ms；无 token 401；executor=nosuch → error/no_executor |
| S3 停机转下一环 + S4 本机环 | 通过 | mbp 停机：`机器不可达` 5ms → mini-b437/claude `exit status 1`(internal) → l01-b437/codex 本机环 escalate 7974ms |
| S3 旧版 404 | 通过 | old-b437（旧二进制）→ `对端不支持` 0ms |
| 连接时限（黑洞） | 通过但发现分类缺陷 | hole-b437 elapsed 恒 10000ms（拨号上限 10s，未等 60s 裁决时限）；**4 个并发请求中 2 条判「超时」2 条判「机器不可达」**——DecideRemote 以 errors.Is(err, DeadlineExceeded) 判超时，拨号超时也命中。已并入 implement 修复轮（cards/B437-charter-6） |
| S3 连续 3 次全败停用 | 通过 | 链 [hole, old, mini-claude]：第 3 次全败后 `approver_disabled {"reason":"连续 3 次裁决失败，审批链已停用（后续权限直接升级人工协调者）"}`；全败升级理由「审批者调用失败」 |
| S6 启动 WARN | 通过 | l01-b437 本地加 approver.executor 后重启：WARN「本地审批链键已被共享链取代，可删除」keys=approver.executor/model/models/timeout |
| clear 回落本地链 | 通过 | `approver clear` → 「已清除共享审批链，各机下次裁决回落本地链」；下一请求 approver_decision 无 machine 字段、executor=codex（本地链） |
| S6 账本不可达 → last-good | 未验（单测覆盖） | 隔离实例无法在不影响同机其它连接的前提下单独掐断到测试库的连接；TestSharedChainReadTimeoutUsesLastGood 覆盖 |
| relay 冷隧道（§9-6） | 未验 | 无授权在 relay 部署；隔离实例无法占 relay 节点名（同 B439） |
| 资源量级（§9-7） | 未验 | 隔离实例无真实负载 |

搭建偏差（如实）：Mac mini 上 claude 本身未登录（`claude -p` → Not logged in），mini-b437 的 claude 环恒 `exit status 1`，被当作「对端执行器失败」环使用；正面他机裁决改由 macbook-pro 隔离实例（codex 已登录）承担。载体 cl-mini/cx-l01/oc-l01 经隔离实例 API 登记；default_carrier 行直接写测试库 registry（同 B439 做法，仅测试库）。

### 第二轮（修复构建 cards/B437-charter-6 @ b35b6eb5；l01-b437 与 mbp-b437 换新二进制，均为隔离实例）

| 项 | 结果 | 证据要点 |
|---|---|---|
| 黑洞分类回归 | 通过 | 链 [hole-b437, l01-b437]，同一任务 4 个并发权限请求：hole 环 4/4 判「机器不可达」elapsed 10000/10001/10000/10000ms；随后 l01 本机环 codex 裁决 |
| 真超时 | 通过 | 链 timeout 3s、首环 mbp-b437/codex（对端裁决约 9s）→「超时」elapsed 3000ms → 转下一环 |
| S2 他机裁决（新构建） | 通过 | 链 [mbp-b437, l01-b437] 60s：`machine=mbp-b437 executor=codex escalate 8687ms`，干净裁决即停 |
| 残余观察 | 记录 | 本机环在链 timeout=3s 下 `context deadline exceeded` elapsed 7592ms：本机 OneShot（codex）取消后进程收尾约 4.6s 才返回，本机环审计理由沿用原始错误串（未归一为「超时」）。属既有本机路径语义（S4「同今天」），不阻塞，落 roadmap |
- 2026-10-19 finish：review r2（6448cb61）pass。PG 往返补跑：`LEDGER_TEST_PG_DSN=<测试库 handoff_b437_test> go test ./internal/ledger -run Registry -count=1 -v` → `--- PASS: TestPGRegistryCASAndSeq (0.62s)`、`ok github.com/Xsxdot/handoff/internal/ledger 1.620s`；真机中 Mac 写入、linux-01 读出同一 approver_chain JSONB 行即 PG 真实往返。
