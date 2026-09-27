# B409.7 (U7) 最终验收台账：S5 方言/投影恢复 + S1–S5 最终增长验收

> 状态：执行中（实现执行者记录）。本文件只记录 U7 隔离验收证据，不宣称 B409
> 父故事完成——最终归档仍需协调者本机/`linux-01` 真机与 Web 三组首屏读数。
> 计划：`docs/superpowers/plans/2026-09-25-b409-7-plan.md`；批准基准：B409 r2。

## 0. 依赖门确认（开工时记录）

- 分支 `codex/session-reliable-mentions`，开工 HEAD `0a6403bd`，工作树干净。
- U1–U6 均已独立 review 并集成：U1 `757279de`；U2/U3 `a620922a`；U4 `79450802`/`8b4e3ce2`；U5 `838e224d..822e87ca`；U6 `0aadd824..0a6403bd`。
- U5 有界候选读契约已冻结（`2026-09-25-session-bounded-candidate-read-contract.md`）。
- S3 跨机重挂证据已由协调者交付（`2026-09-26-b409-5-acceptance.md`：真实 CLI × 本机+linux-01 × 共享隔离 PG，停听补收/不回放/游标推进/30 次超时）——依赖门满足，未重做。
- 隔离 PG：PostgreSQL 16.15，容器 handoff-b409-pg @ 127.0.0.1:59849，专用可丢弃库 `handoff_b409_test`（协调者确认；DSN 不入档）。
- 全计划集审计：协调者确认通过后派发（派发单记录）。

## 1. 步骤 2：跨方言金样与独立 oracle

- 金样：同一逻辑事件集（2 卡 + 主/空两会话 + 8 条房间消息覆盖 mentions/reply/by_system 超集行 + 消费标记 + 5 条工单生命周期镜像含幂等重放 + 交付游标），全部经真实生产写路径（`ImportCard`/`CreateSession`/`RecordRoomMessage`/`RecordMessageConsumed`/`AppendMirroredEvent`/`AdvanceSessionDeliveryCursor`）分别写入 SQLite（t.TempDir()）与专用 PG。
- 独立 oracle：`replayB409U7Oracle` 只读权威 `card_events` 原始行重放，不经任何生产投影/读代码；逐项断言生产 `OpenTickets`/`OpenTicketCounts`/`RoomMessagesBeforeContext`（含排他 before 边界）/`SessionMessageCandidates`（fromSeq 排他、toSeq 包含）/`SessionDeliveryCursor`/投影水位与 mirror watermark。
- 冻结金样钉住：未决工单恰 2 条；memberA 候选恰 {身份@, reply→memberA 作者, by_system 带@ 超集行} 三条；memberB 恰 {@} 一条；普通发言/无寻址指针不混入。
- 空/失败/取消不混淆：真实空房间与无提及成员候选 0 行无错；canceled ctx 返回 `context.Canceled`；已关闭存储返回错误。
- 结果：`TestB409U7CrossDialectGoldenAndOracle` postgres/sqlite 双 PASS。逐方言读回字节留痕（PG JSONB 渲染字节 > SQLite 原文，语义等值、字节差异如实记录不静默）：
  - postgres：room_rows=8 room_payload_bytes=819 tickets=2 ticket_payload_bytes=50
  - sqlite：room_rows=8 room_payload_bytes=764 tickets=2 ticket_payload_bytes=48

## 2. 步骤 3：旧库升级、rebuild 幂等、中途失败恢复

- pre-projection schema fixture：`internal/ledger/testdata/b409-preprojection-schema-{pg,sqlite}.sql`，逐字提取自 revision `a7983e9c`（`1f28372b` 引入 open_ticket_projection 之前的父提交）的 `ddlStatements`；未从任何生产库复制 DDL。
- 升级：旧库形态（21 表、无投影表）+ 旧写路径格式 canonical 行（镜像 envelope `{"node","attempt","task_type","payload"}`）→ 真实 `ledger.Open`/`ensureSchema` 升级 → 投影/计数/水位（ledger_seq=5）/房间历史/候选读/交付游标全部与 oracle 一致，canonical 事件无损（6 行）。PG 腿使用专用库内独立 schema 命名空间（search_path DSN），用后 DROP；SQLite 腿用临时文件。
  - `TestB409U7PreProjectionUpgradeSQLite` PASS；`TestB409U7PreProjectionUpgradePostgres` PASS。
- rebuild：两次显式 rebuild 行集/水位/open_count 完全一致（幂等）；`TestB409U7RebuildIdempotentAndMidwayFailureRecovery` 两方言 PASS。
- 中途失败恢复：数据库触发器注入确定性失败（仅测试内建/拆，生产无开关；安装前先拆同名残留保证幂等）。失败后：显式 rebuild 报错可见；投影与水位与失败前快照逐字节一致（事务原子回滚，无半成品）；删空投影后读者得到显式错误而非合法空；故障拆除后 `OpenTickets` 自动追赶重建，从权威事件补齐到基线快照与 oracle 结果。
- 追加期中断：`TestB409U7MirrorAppendAtomicOnProjectionFailurePG` PASS——投影触发器失败时权威事件与投影同事务回滚（canonical 三元组 0 行、水位不变）；故障拆除后同参数追加成功且投影同步。SQLite 同语义由既有 `TestAppendMirroredEventProjectionFailureRollsBackEvent` 覆盖。

## 3. 步骤 4：三阶段隔离 PG 增长矩阵（数据库实测，run7 终采）

seed 说明：金样目标经真实写路径建立；批量镜像经 SQL 批插，envelope 顶层键集与真实 `AppendMirroredEvent` 持久格式逐键对照（`assertB409U7EnvelopeParity` 每次运行钉住）。既有 helper 已按 plan 修正（去掉顶层 `b409_run` 键，run 标签移入内层 payload；Wave 0 基准 body 470→510 尺寸补偿），统计/清理过滤改为 `COALESCE(payload->>'b409_run', payload->'payload'->>'b409_run')`。

实测（run 级原始行见 `evidence/2026-09-25-b409-final/seed-phase{1,2,3}.log`）：

| 阶段 | 总事件 | task_mirrored 行 | mirror payload bytes | 总 payload bytes | 按 type |
|---|---:|---:|---:|---:|---|
| ① baseline | 21,021 | 9,342 | 7,048,844 | 7,780,053 | card_created=2, room_message=253, session_created=2, task_mirrored=9,342, unrelated_event=11,422 |
| ② +unrelated | 41,021 | 9,342（不变） | 7,048,844（不变） | 9,148,947 | unrelated_event=31,422 |
| ③ mirror 双倍 | 50,363 | 18,684 = 2×9,342 | 14,100,947 = 2×7,048,844 | 16,201,050 | 同上，镜像行/字节各自 ≥2×① |

- 阶段③分母按阶段①报告；金样镜像（5 行）计入分母且翻倍增量已补齐（先因分母遗漏差 5 行被红，修正后通过）。
- 禁混淆检查：类型集合中不存在 `mirror_event`/`task_mirrored_event`；`task_mirrored` 分母精确来自 GROUP BY type。
- 限域读探针（真实 Store 读路径，rows/bytes 三阶段逐项相等，不随无关事件线性增长）：房间历史 200 行/19,451 B；OpenTickets 2 行/66 B；OpenTicketCounts 2 卡；SessionMessageCandidates 2 行/292 B。
- Wave 0 `TestRoomHistoryPostgresPerformance` 回归 PASS（矩阵常量对齐后）；房间历史 100 样本 p95 三阶段均 <2s（阶段② p95=1.58ms 量级）。
- EXPLAIN 断言放宽说明：U5 引入 `idx_room_messages_cardless_seq` 后优化器在两个等价 room_message 局部索引间自行选择；断言改为「room_seq/cardless_seq 之一且不退化全表扫」，仍钉住有界读路径（测试内注释留档）。

## 4. 步骤 5：HTTP API 与 CLI（真实 agentd + 真实 CLI，隔离端口 17877，run7 终采）

环境：`CGO_ENABLED=0 go build -trimpath -tags embedweb`，SHA `0a6403bd`；PG 16.15；隔离库 `handoff_b409_test`；agentd 端口 17877（生产 7777 未触碰）；DSN/token 只进 /tmp 配置。三档经 `TestB409U7PGSeedPhase` 增量 seed，agentd 全程不重启。

HTTP 数值目标（每场景首请求 + 5 预热 + 100 计入样本，nearest-rank；完整原始样本 `evidence/2026-09-25-b409-final/http-samples.tsv`（1,212 行 = 12 场景 × 101）与 `http-stats.json`）：

| 阶段 | 场景 | 首请求 ms | p50 ms | p95 ms | max ms | 错误 | response bytes | p95≤2s |
|---|---|---:|---:|---:|---:|---:|---:|---|
| ① | /api/cards | 16.40 | 16.42 | 23.76 | 30.35 | 0 | 953 | ✓ |
| ① | /api/sessions | 20.90 | 17.72 | 22.77 | 29.38 | 0 | 984 | ✓ |
| ① | /api/rooms?limit=50 | 16.93 | 16.10 | 22.99 | 39.23 | 0 | 751 | ✓ |
| ① | /api/rooms/{主会话}/messages?limit=200 | 13.02 | 11.35 | 16.04 | 21.56 | 0 | 41,410 | ✓ |
| ② | 同上四场景 | 10.63–20.87 | 11.09–16.63 | 16.89–25.91 | 21.24–43.80 | 0 | 同上恒定 | ✓×4 |
| ③ | 同上四场景 | 13.32–22.98 | 11.53–17.61 | 18.61–25.55 | 21.13–38.65 | 0 | 同上恒定 | ✓×4 |

- 12/12 场景 0 错误、p95 全部 ≤26ms（门槛 2s）；response bytes 三档逐字节恒定（cards 953 / sessions 984 / rooms 751 / messages 41,410）——无关事件 +20,000 与镜像翻倍都没有让限域读路径的返回规模增长。
- Store 阶段 rows/bytes 取自同一批真实 HTTP 请求贯穿的 agentd U6 诊断日志（`agentd-stage-lines.log`，1,263 行 rows_returned/payload_bytes 记录；agentd 的 slog 经 logx.Setup 落 datadir/agentd.log，已复制为 `agentd-full.log`），未另造独立 100 样本。
- 功能级（描述性，`api-functional.txt`）：`/api/sessions/{fixture}` 200（775B/15.7ms）；不存在会话 404；空会话 messages 200 `{"messages":[]}`（真实空 16B）；无 token 401；`/api/inbox` 200 带 1 条 mention 命中（`origin=mention`，区别于合法空）；客户端中断探针因端点 15ms 内完成未触发取消（该请求完成于 --max-time 0.05 内），取消语义的承重证据仍在 U6（锁等待取消 `canceled/failed_stage=sql` + `TestRoomHistoryPostgresCancelsBlockedRead`）。

CLI 故事功能（真实 `handoff` 进程 × 隔离 config，`cli-story.txt`）：

- `card list --json`：2 行卡 JSON（rows_returned=2 日志同证）。
- `card wait <卡A> --timeout 4s`：首行完整 `card_snapshot` JSON（含 `actionable` 未决工单 `u7-matrix-open-a`），随后空等 124 退出。
- `session list --json` / `session detail session:1 --json`：成员含 user:sy 与两个 agent 成员。
- `room list`：列卡房间 ×2 + project + global；`room read session:1`：200 行 `#<seq>\t<actor>\t<正文>` 升序。
- `room inbox`（经 agentd HTTP）：1 条 mention 命中。
- `session wait <wait 成员> --timeout 5s`：首个完整 `session_backlog` 行恰 2 hits（seq 255/256，@ 寻址命中、他人消息不命中），exit 0。
- 重挂 `--timeout 400ms`：exit 124 无 stdout（已交付不回放）；idle 成员空等 exit 124 无 stdout（空积压不输出）。
- `session wait <idle 成员> --timeout 5s` 30 次空等：**30/30 exit 124、最大 5,097ms ≤ 6,000ms**（`session-wait-30x.tsv`）。
- Web 三组真实浏览器首屏读数（/cards 卡片列表、工作台 SessionSidebar 会话列表、消息首屏）：**留交协调者**（U7 执行者无法做浏览器交互）。

## 5. 变异自验（三发，均保留编译、先验命中唯一、红/绿闭环）

| 编号 | 变异 | 位置 | 变异下测试 | 结果 |
|---|---|---|---|---|
| M1 | rebuild 水位比较去掉：`ledgerSeq != currentSeq` 从 needsRebuild 判据删除 | `internal/ledger/ticketprojection.go` | `TestB409U7CrossDialectGoldenAndOracle/sqlite` | **FAIL**（金样 5b 探针：直插一条新未决工单镜像后读——滞后投影未追赶，2≠3）；还原后回绿 |
| M2 | unrelated 计入 mirror 分母：`typ == EvTaskMirrored` → `typ != ""` 累加 | `internal/ledger/rooms_history_perf_test.go`（collectB409U7StageStats） | `TestB409U7PGFinalGrowthMatrix` | **FAIL**（阶段②镜像量不得变化：rows 21021→41021 被当场咬住）；还原后回绿 |
| M3 | 房间历史排他游标破坏：`AND seq < ?` → `AND seq <= ?` | `internal/ledger/rooms.go` | `TestB409U7CrossDialectGoldenAndOracle/sqlite` | **FAIL**（排他游标语义破坏：before=7 返回 seq=7）；还原后回绿 |

- M1 的靶子是金样新增的 5b 探针：绕过投影维护直插一条未决工单镜像，下一次 `OpenTickets` 必须显式追赶——水位比较被去掉后 `ensureOpenTicketProjection` 不再重建，滞后投影被当作完整最新返回。
- 事故记录（M2 还原环节）：首次还原误用 `git checkout -- rooms_history_perf_test.go`，把该文件**未提交的 U7 矩阵改动整体回滚**（变异还原必须走逆补丁）。处置：从工作记录完整重建该文件改动 → `go vet` + Wave 0 回归 + 三阶段矩阵 + 带 DSN 全包测试复跑全绿 → 用正向/逆向补丁重做 M2（红/绿复现）。事故波及的 run6 harness 采数无效，已弃用并以 run7 重采。

## 5a. 测试三段律

- 编译全量：`go build ./...` + `go vet ./internal/ledger/ ./internal/collab/ ./internal/agentd/ ./cmd/` → exit 0。
- 触及包局部（SQLite）：`env -u LEDGER_TEST_PG_DSN go test ./internal/ledger/ -count=1` → exit 0。
- PG 定向：`LEDGER_TEST_PG_DSN=<隔离库> go test ./internal/ledger/ -count=1 -timeout 30m` → exit 0（29.9s）。
- 集成全量：`env -u LEDGER_TEST_PG_DSN GOCACHE=… go test -p 1 ./... -count=1` → exit 0（62+ 包 ok）。
- `codegraph --repo . check` → `fails: []`。

## 5b. 图覆盖债

U7 新增测试符号（b409_final_acceptance_test.go 的金样/oracle/升级/重建测试、rooms_history_perf_test.go 的矩阵与 seed-phase 入口）未入代码图，留父卡 recon 按既有惯例偿还；未预制 nodes/edges，也未改 best/target。

## 6. 边界声明

- 只写专用可丢弃库 `handoff_b409_test` 与 `t.TempDir()` SQLite；未触碰生产/共享账本（100.84.251.46）、本机 ledger.db、linux-01。
- U1–U6 生产文件、internal/proto、部署配置零修改；改动全部在 plan §3 文件边界内。
- 本台账与全部证据不含 DSN、凭据、token；消息正文均为合成夹具文本。
- 当前性能数据不外推为 2026-09-23 20:00–22:00 故障的历史根因。
