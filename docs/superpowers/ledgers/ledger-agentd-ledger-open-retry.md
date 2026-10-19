# 台账：agentd 账本打开失败无自愈 / 重启连接峰值（2026-10-19，charter:implement 排查节）

## 偏离与自主决定

本活无 spec/plan（协调会话直接下发的 bug 修复，走 implement 排查节），以下按「对照任务简报」列出；均未取得 plan 决策权限授权，交协调者裁决。

1. **对现象的纠正**：简报说「打开账本库失败后不再重试、进程在没有账本的状态下继续运行」。实证是：首个实例在 53300 后**退出**（launchd 10s 后拉起），第二个实例**卡死**在 ensureSchema 的 `ALTER TABLE cards` 锁等待里——零日志、未监听 7777、未自登记，直到人工重启。修复同时覆盖两段（瞬时错误重试 + 有界打开）。
2. **启动期可观察性的实现方式（自行拍板）**：没有做「先对外服务、账本后台晚挂载」——那会让约 100 处无同步读取的 `s.ledger` 等字段与挂载写竞争，并违背既有的「RecoverOnStartup 先于对外服务」约束。改为**提前绑端口 + 启动闸**：DataDir 锁与装配之后先 `sd.Begin` 占端口，启动期一切请求回 503 + 当前阶段；账本挂载与启动恢复完成后 `bootGate.Ready` 原子切换。代价：绑端口从 RunE 末尾提前到 setupLedger 之前（lock 处注释同步改写）；启动期对端（镜像/探活）看到 503 而不是 refused。
3. **启动闸不鉴权**（自行拍板）：它只回 503 与阶段文案；文案经 `ledger.OpenErrorReason` 只含 SQLSTATE 与服务端报文，不含连接串/主机/账号。
4. **连接上限取 8**（自行拍板）：`PGMaxOpenConns=8`、MaxIdle 2、IdleTime 5m，单值通用于 agentd 与 CLI。镜像持有者 517 个 goroutine 改为在 8 条连接上排队；若镜像轮次延迟变得可感，可上调或给镜像做并发闸——未实测镜像吞吐。
5. **lock_timeout 只加在建库阶段**（自行拍板）：建库用独立短命小池（lock_timeout 10s / statement_timeout 60s / MaxOpen 2，建完即关），运行期池不设 lock_timeout，mutate 的写锁等待语义不变。代价：每次 Open 多一次建连握手。
6. **重试成功日志用 WARN**（自行拍板）：默认日志级别过滤 INFO，用 WARN 让「重试 WARN → 恢复」在默认日志里首尾成对。
7. **改了既有测试** `TestSetupLedgerFailureDoesNotLogDSNCredentials`：127.0.0.1:1 拒连现属瞬时错误会无限重试，改为注入 sleep 在首次退避时取消 boot ctx；断言不变。
8. **CLI 不重试**：`ledgercli.go` 照旧 fail 快（简报只要求 agentd 重试）；CLI 只获得连接上限、connect_timeout 与建库 lock_timeout（原本可能无限挂住，现在至多约 10s 后报错）。
9. **越界接触**：真机 A 中隔离实例按测试库夹具残留的 devices 行向在役 linux-01 agentd 发了 12 次只读 GET，全部 401，无状态变化（详见流水）。
10. **未提交**：会话规则要求「任务明说才提交」，简报未明说，改动留在 worktree `.claude/worktrees/ledger-open-retry`（分支 `fix/ledger-open-retry`，基于 628d9311）未 commit；台账随同未提交。

## 结论

- 根因①（无自愈）：PG 打开路径没有任何时间上界——pgx 无 connect_timeout、Ping 无 ctx、ensureSchema 的 `ALTER TABLE cards`（AccessExclusiveLock）与 `pg_advisory_xact_lock` 无 lock_timeout。任一会话持 cards 锁或写锁时 Open 无限挂住且零日志；之前的一次 53300 则直接退出进程。
- 根因②（连接峰值）：`database/sql` 池无 MaxOpenConns（默认无上限），镜像持有者启动时 517 个任务 goroutine 并发读库，单进程即可打穿 max_connections=90；mac-mini 第二次启动与 macbook-pro 的风暴同秒（13:04:33 CST = 14:04:33 JST）。未取证的加剧因素：风暴期间排队的 ALTER 挡住其后所有 cards 读（锁护航），持连接等待进一步耗尽名额——当时持 cards 锁的是哪个会话已无法复原。
- 全队最坏连接数（修复后）：agentd 每台 ≤ 8（不开 LISTEN）；每个并发 CLI ≤ 8（实际串行 1~2）+ `card wait --follow` 1 条 LISTEN；任一进程打开时另有 ≤ 2 条建库短连接（返回前关闭）。3 台机器 = 24 + CLI；按库上可用约 67（90 − 3 保留 − 约 20 supabase 自用）计，余约 43 给 CLI，约 20 个并发 CLI 才会触顶。
- 残余/后续：①每次 CLI 调用仍执行 ensureSchema 全套 DDL（每次取 cards 排他锁，全队锁护航源）——应按 schema 版本号跳过已就绪的 DDL；②`handoff status` 在账本**已启动但 S2 挂载门拦下**（缺身份）时仍看不出账本未挂载（StatusResp 无 ledger 字段）；③既有红 `TestB439DevicesPG`、`TestPGSmokeEndToEnd`（基线同红）。

## 事实流水

- 第 0 步身份：本机即 mac-mini（~/.handoff/config.yaml name: mac-mini），在役 agentd pid 4616 起于 14:06:12（人工重启）；修复在 worktree `.claude/worktrees/ledger-open-retry`，分支 `fix/ledger-open-retry`，基于 628d9311。
- mac-mini agentd.log：`14:04:25 ERROR 打开账本库失败 … FATAL: sorry, too many clients already (SQLSTATE 53300)`，紧接 cobra `Error: 打开账本库: …` ——进程**退出**了（setupLedger 返回 err → RunE 返回），并非「带病继续运行」。
- launchd plist `KeepAlive=true`；`14:04:33` 新进程起（只打两行 std log INFO：relay egress / 出网代理），此后到 14:06:12 人工重启前**零日志**。
- 共享账本 devices 表（只读查询，经 linux-01 psql）：mac-mini updated_at = 05:06:27Z（= 14:06:27 JST，人工重启那次），14:04:33 那个实例从未自登记。
- linux-01 agentd.log `05:04:34Z` 探 mac-mini `100.91.215.29:7777` 得 **connection refused** ——14:04:33 实例没走到 HTTP 监听（监听在 setupLedger 之后）。结论：第二个实例**卡死在账本打开阶段**（Ping / ensureSchema），无超时、无日志。
- 连接峰值来源：macbook-pro（镜像持有者）agentd.log `13:04:3x CST`（= 14:04:3x JST，与 mac-mini 第二次启动同秒）出现 1469 条 too many clients，涉及 517 个不同 task 的「读取镜像 watermark 失败」——镜像子系统每任务一 goroutine 并发读库。
- 连接池：账本是 `database/sql` + `pgx/v5/stdlib`（store.go `sql.Open("pgx", dsn)`），PG 分支**未设 SetMaxOpenConns**——database/sql 默认 0 = 无上限（不是 pgxpool 的「按 CPU 核数」；pgxpool 在本仓无使用）。并发 N 个查询就开 N 条连接。另有 `Follow` 的 LISTEN 裸连接（pgx.Connect，CLI `card wait` 用）。
- 共享库 `max_connections=90`、`superuser_reserved_connections=3`；稳态 pg_stat_activity：supabase 自身约 20、handoff 各机合计约 6。
- ensureSchema 每次 Open（含**每次 CLI 调用**）都执行 `ALTER TABLE cards ADD COLUMN …`（取 AccessExclusiveLock，即使列已存在也先取锁）与 `pg_advisory_xact_lock(915601)`，库上无 lock_timeout / statement_timeout，pgx 无 connect_timeout ——任一长事务持 cards 锁或写锁时 Open 无限等待；ALTER 排队期间还会挡住其后所有 cards 读（锁护航）。这是 14:04:33 卡死的头号候选（另一候选：TLS 握手无超时）。
- 嵌套取连接审计（cap 后会死锁的形状）：awk 扫描 internal/ledger 全部 `s.mutate(func` 闭包与 rows 循环内对 `s.db.` / 非 tx 方法的调用，零命中。
- 测试库：linux-01 PG 建 `handoff_b440_test` / 账号 `handoff_b440`（CONNECTION LIMIT 40），已登记 credentials.md「已分配」表。
- 红灯（空壳 open_errors.go：PGMaxOpenConns=8 / schemaLockTimeout=10s / IsTransientOpenError 恒 false）：`go test ./internal/ledger -run 'TestPGOpenBoundedWhenCardsLocked|TestPGPoolCapped|TestIsTransientOpenError' -count=1`（LEDGER_TEST_PG_DSN=handoff_b440_test）→ 三红：`cards 被锁时 Open 超过 30s 仍未返回（无界等待）`（持锁事务 Rollback 的同一秒 Open 才继续：日志 14:25:26 `cards.driver_carrier 列已存在`）；`MaxOpenConnections=0，期望 8`；分类表 11 条瞬时用例全判 false。挂死假设获确定性复现。
- 绿灯（store.go openPG：pgx.ParseConfig + connect_timeout 缺省 10s；建库专用小池 RuntimeParams lock_timeout=10s/statement_timeout=60s、MaxOpen 2，建完即关；运行期池 MaxOpen 8 / MaxIdle 2 / IdleTime 5m；Follow 的 LISTEN 改 pgx.ConnectConfig(s.pgCfg)）：同三测全绿，`TestPGOpenBoundedWhenCardsLocked (10.59s)`、`TestPGPoolCapped (1.20s)`。
- ledger 包 PG 全量（`-p 1`，DSN=handoff_b409_test——各 PG 套件的库名护栏只认它，credentials.md 登记为 LEDGER_TEST_PG_DSN 专用、可随时重建；跑前该库 pg_stat_activity=0）：基线 628d9311（scratchpad 临时 worktree）与本分支**同为** `TestB439DevicesPG`（attrs 往返 `{"k": "v"}` 格式差）+ `TestPGSmokeEndToEnd`（自动前缀 P 被既有项目 "p" 占用）两红，余全绿——两红为既有，非本修引入。（首轮在 handoff_b440_test 上跑，护栏拒库名致 18 红，属环境，不计。）
- agentd 侧红灯（空壳 BootGate / ledgerBoot 字段未消费）：`go test ./cmd -run 'TestSetupLedger(RetriesTransient|FailsFast|GivesUp)'` → `attempts=0，期望 3`、`err=<nil> attempts=0`、`得 <nil>`；`go test ./internal/agentd -run TestBootGate` → `解析状态响应: EOF`。
- 绿灯：cmd/agentd.go `openLedgerWithRetry`（瞬时退避 1s×2 封顶 30s ±20% 抖动；WARN 只在第 1/2/4/8… 次；配置性 ERROR 一次即返回）+ `ledgerBoot` 注入缝；internal/agentd/bootgate.go；orchestration.Shutdown 拆 `Begin`/`Done`（Serve = Begin+wait，既有测试不改）；RunE 改为 DataDir 锁与装配之后、setupLedger 之前 `sd.Begin(启动闸)`，账本挂载+启动恢复后 `bootGate.Ready(srv.Handler())`。
- 既有测试改动：`TestSetupLedgerFailureDoesNotLogDSNCredentials` 的 127.0.0.1:1 拒连现属瞬时→会无限重试；改为注入 sleep 首次退避即取消 boot ctx，仍走真实打开失败路径、仍断言日志不含 DSN/密码且含 dsn_configured=true。其余 setupLedger 调用点只补 `ledgerBoot{}`。
- 触及包：`go test ./cmd ./internal/agentd ./internal/orchestration ./internal/ledger -count=1` → orchestration/ledger ok；cmd 红 `TestSessionSendActorResolution`（`env -u CLAUDE_CODE_SESSION_ID -u CLAUDECODE` 复跑 ok——记忆中已知的会话环境敏感测试）；agentd 红 `TestNoDirectHttptestServers`（bootgate_test 用了 httptest.NewServer，改 testhttp.NewServer 后 ok）。
- 真机 A（隔离 agentd：scratchpad/iso，HOME/DataDir 独立、127.0.0.1:17790、name iso-ledger-retry、--executor fake、DSN=handoff_b440_test；分支构建 628d9311+未提交改动）：linux-01 上起 40 个 `select pg_sleep(40)` 占满 handoff_b440 的 CONNECTION LIMIT 40（pg_stat_activity=40），14:42:42 起 agentd。14:42:48 `handoff status` → `状态码 503: {"error":"agentd 未就绪（账本未挂载：共享账本打开失败，第 3 次重试（SQLSTATE 53300 too many connections for role \"handoff_b440\"），约 4s 后再试）"}`。agentd.log WARN 只有 attempt 1/2/4（3 被限频）。占位会话 ~14:43:22 释放，14:43:52 status 可用；devices 行 iso-ledger-retry updated_at 13:43:49+08；稳态 handoff_b440 连接 2（idle）。
- 发现：重试成功日志原为 INFO，默认级别下不可见 → 改 WARN（与重试 WARN 同级，事故有始有终）。
- **越界接触留痕**：测试库 devices 有 b439 测试夹具残留行 `linux-01 → 100.81.114.119:7777`（真实在役 agentd 地址）与 `mac-02`。隔离实例据此向在役 linux-01 发了只读 GET（/api/previews ×10、/api/tasks ×2），全部 401 未授权（夹具行无有效 token），未改变在役机任何状态。发现后即停隔离实例、删两行夹具。教训：隔离实例接测试库前先清 devices 表非自身行。
- 真机 B（同隔离实例，修复构建）：linux-01 上一会话 `begin; lock table cards in access share mode; select pg_sleep(30)`，14:44:50 起 agentd。14:45:04 status → `agentd 未就绪（账本未挂载：共享账本打开失败，第 1 次重试（SQLSTATE 55P03 canceling statement due to lock timeout），约 1s 后再试）`；WARN attempt 1/2 cause `建账本 schema: 迁移 cards.driver_carrier: … lock timeout`；14:45:19 WARN `账本挂载重试成功，瞬时故障已恢复 attempts=3 elapsed=27s`；14:45:20 status 可用。
- 真机 C（DSN 密码错）：14:45:37 起，14:45:38 exit=1，`Error: 打开账本库: 连接账本库: … password authentication failed … (SQLSTATE 28P01)`；端口已释放；bad.out 与 agentd.log 中 "wrong-password" 命中 0。
- 对照（基线 628d9311 构建、同隔离配置、同持锁）：14:45:50 起，14:46:10 `nc -z 127.0.0.1 17790` → refused、agentd.log 0 行；PG 侧 handoff_b440 会话 `active | Lock | ALTER TABLE cards ADD COLUMN driver_carrier TEXT`。**与 mac-mini 14:04:33 实例的现场（零日志、7777 refused、devices 未刷新）同形**——根因证实：ensureSchema 的 ALTER 无界锁等待。
- 收尾：最终代码 `go vet ./...` 通过；`env -u CLAUDE_CODE_SESSION_ID -u CLAUDECODE go test ./cmd ./internal/agentd ./internal/orchestration ./internal/ledger ./internal/ledgermirror -count=1` 全 ok。linux-01 上 `DROP DATABASE handoff_b440_test`、`DROP ROLE handoff_b440` 成功，credentials.md 登记行已删（grep 命中 0）；隔离实例已停、17790 端口已释放；scratchpad 基线 worktree 已移除。
