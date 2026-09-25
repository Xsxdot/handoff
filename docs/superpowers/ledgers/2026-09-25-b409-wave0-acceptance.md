# B409 Wave 0 验收记录：S2 房间消息首屏限域读

**状态**：S2 / Wave 0 阶段验收通过（2026-09-25；独立实现 review PASS，I1/I2 已关闭）；仅覆盖 S2，不代表 B409 的 S1–S5 全部完成。

**工作树**：`codex/session-reliable-mentions`，S2 实现与首批验收证据提交 `0703b2eb1675b2229a5f4124998683d2de9dd02f`（tree `92698d7855956899a2c36b8d64c536c38836d338`）。

## 结果

S2 已把会话历史读取限制为目标房间最近 200 条，在数据库侧应用房间、事件类型、游标与条数边界，向客户端按 seq 升序返回。PostgreSQL 与 SQLite 金样、取消传播、HTTP/CLI 真实入口、空态与错误态均已验证。性能专项中 Store、HTTP 与 Codex 内置浏览器三组测量的 p95 均低于批准的 2 秒目标。

数据库根因不能仅靠 S2 的一次性能结果断言为全部解决：其余卡片列表、待办/提及/未读、`card wait`、`session wait` 与远端摘要仍在 B409 S1–S5 的其他故事范围内。

## 数据与性能

PG 性能专项使用本机隔离 PostgreSQL 16 容器与专用数据库 `handoff_b409_test`；内置浏览器最终验收使用独立克隆库 `handoff_b409_ui_recheck`。夹具标记为 `b409-wave0-acceptance-20260925`，只含合成数据。原始日志：

- PG 性能、行数、字节数与 `EXPLAIN`：`/private/tmp/b409-pg-perf-corrected-20260925.log`
- PG 语义与阻塞查询取消：`/private/tmp/b409-pg-semantics-cancel-20260925.log`
- HTTP 每次响应与样本：`/private/tmp/b409-http-corrected-{baseline,unrelated,doubled}-20260925.json`
- 内置浏览器的原始样本、数据库阶段规模、DOM 状态和错误夹具审计：[2026-09-25-b409-wave0-ui-evidence.json](2026-09-25-b409-wave0-ui-evidence.json)

| 数据阶段 | 总事件 / 总 payload 字节 | 镜像行 / 镜像 payload 字节 | Store p95 | HTTP 首次 | HTTP p50 / p95 / max | HTTP 响应字节 | 浏览器 p50 / p95 / max |
|---|---:|---:|---:|---:|---:|---:|---:|
| 基准 | 21,270 / 7,317,325 | 9,337 / 6,376,064 | 5.081 ms | 11.782 ms | 2.034 / 5.626 / 6.159 ms | 84,966 | 163.361 / 174.137 / 176.477 ms |
| 增加 20k 无关事件 | 41,270 / 8,926,219 | 9,337 / 6,376,064 | 5.269 ms | 12.172 ms | 2.851 / 7.032 / 15.153 ms | 84,966 | 163.746 / 173.053 / 185.853 ms |
| 镜像行与 payload 翻倍 | 50,607 / 15,302,283 | 18,674 / 12,752,128 | 5.147 ms | 10.168 ms | 1.991 / 5.569 / 8.866 ms | 84,966 | 164.192 / 175.125 / 266.543 ms |

表中 163–175 ms 为较早 Playwright 浏览器汇总，原始样本数组未持久化，因此作为历史结果保留，不承担本轮最新构建门槛。

最新构建在 Codex 内置浏览器重新采样：每阶段 5 次预热、100 次串行重载，计时至新可访问性树同时出现选中会话标题与首个回复按钮。首轮 100 样本复查后来被独立审查发现用了 `mirror_event`，与计划和 `rooms_history_perf_test.go` 要求的真实 `task_mirrored` 夹具不符；该组完整保留在 JSON 的 `invalid_fixture_recheck_100` 并标为不计验收。随后仅在隔离克隆库按性能测试 helper 重建三阶段数据并重跑，数组及复算指标保存在 `latest_build_recheck_100`：

| 最新构建 UI 阶段 | 事件总数（含元数据） | fixture payload 字节 | `task_mirrored` 行 / payload 字节 | UI p50 / p95 / max | 成功 / 错误 |
|---|---:|---:|---:|---:|---:|
| 基准 | 21,272 | 7,317,325 | 9,337 / 6,376,064 | 406.5 / 436 / 458 ms | 100 / 0 |
| 增加 20k 无关事件 | 41,272 | 8,926,219 | 9,337 / 6,376,064 | 406 / 435 / 515 ms | 100 / 0 |
| `task_mirrored` 行与 payload 翻倍 | 50,609 | 15,302,283 | 18,674 / 12,752,128 | 412.5 / 496 / 509 ms | 100 / 0 |

本轮 UI 验收均使用 `task_mirrored`；最终 SQL 核验 `mirror_event=0`。JSON 持久化了每阶段 5 个预热值、100 个计时值及其首条消息序号。内置浏览器最终截图：[session-final.jpg](evidence/2026-09-25-b409-wave0/session-final.jpg)。三阶段原始浏览器回包保存在同目录。克隆会话共 250 条消息，界面读取最近 200 条；UI 首条回复按钮为 `回复 #341874`。最终克隆库的 CLI 输出后来确认来自旧安装版，已从证据目录移除，不作为当前限域查询的证明。

## 语义与用户入口

- PG 与 SQLite 对最近窗口、升序、排他 `beforeSeq`、房间隔离及空结果遵守同一金样。
- PG 阻塞读取在实际锁等待期间收到 context cancel 后退出；日志见 PG 语义/取消记录。
- `GET /api/rooms/{id}/messages` 基准、+20k、翻倍阶段均返回 200 条、seq 升序且唯一；消息响应大小均为 84,966 bytes。
- collab 日志在账本方法返回处立即停止 `ledger_call_ns`，独立计量 payload 字节统计的 `assembly_ns`，并记录整体 `elapsed_ns`；HTTP handler 在响应写出后记录端到端耗时。取消/失败日志保留账本调用耗时与总耗时。
- 较早隔离 PG CLI `room read session:1` 退出码 0，输出 200 条，seq `172653–172852` 严格递增。原始 stdout/stderr 留在 `/private/tmp`，仓内只保存序号摘要和筛选后的结构化日志：[summary](evidence/2026-09-25-b409-wave0/room-read-current-cli-summary.json)、[stderr](evidence/2026-09-25-b409-wave0/room-read-current-cli.stderr)。stderr 含 `database` 开库字段、新 Store 查询开始/完成及 collab 完成日志，证明该次运行经过当前限域查询路径；原始 CLI 二进制 build ID 未留存，故不伪称有独立二进制身份记录。最终克隆库 CLI 样本 `341874–342073` 来自旧安装版 `handoff 3390cd2f3e7c`，仍调用旧全流路径，且 stderr 曾含连接位置；原始 stdout/stderr 已删除并排除验收。
- 较早真机浏览器重测显示 200 个回复按钮，seq `172653–172852`；真空会话显示“（还没有消息）”，输入框可见且发送按钮禁用。本轮内置浏览器实际打开隔离实例 `http://127.0.0.1:7788/` 的 `B409 性能验收会话`；最终截图可见连续消息卡片，重载后可访问性树持续出现标题与 `回复 #341874`。
- 删除隔离测试库中的 `card_events` 表名（改名）后，页面进入“消息暂时无法读取”并显示查询错误；恢复表名并刷新后错误消失。该验证只对隔离测试数据库执行。
- 空消息与查询错误从不合并成同一个 UI 状态。

## 验证与剩余门

- 聚焦 Go 房间历史/HTTP/collab/agentd/CLI 测试通过；Go 全包编译通过。
- `npm --prefix web test -- --run`：135 个文件 / 1,476 个测试通过；`npm --prefix web run typecheck` 与 `npm --prefix web run build` 通过。
- `scripts/build-deploy.sh` 对最新源码完成，产物 `/private/tmp/handoff-b409-agentd-final-20260925`；embedweb 自检通过。隔离验收 agentd 已用此产物重启在 7788，专用 PG 临时配置只在隔离运行期间保存在 `/private/tmp`，验收后回收。
- 合法 under-fetch 变异将房间窗口限制从 200 改为 100 时，`TestRoomHistoryReturnsNewestWindowInAscendingOrder` 失败（200 实得 100）；恢复实现后测试通过。
- `codegraph check --repo . --stale` 退出码 0、`fails: []`。`codegraph validate --repo .` 退出码 1，仍报告两个既有图视图问题：B272 引用缺失 baseline domain `d_coordination_api`；B374 把现有 container `k_collab_model` 列为 new。`codegraph sym/who-calls cmd/agentd.go#setupLedger` 均报告符号不在图中；本次启动日志修复已手动检查源代码，记为图覆盖债。
- 首次串行全量 Go 测试在不相关 PTY 用例 `TestPtyWSAttachedBacklogBytesKeyPresent` 失败：shell 在 WS attach 前写入启动输出，实际 `backlog_bytes=160`，用例却假定必为 0。修复仅重整测试：把零值字段序列化断言移到 `internal/proto`，HTTP 集成测试只核对字段存在。固定用例 `-count=10` 在修复前复现 1 次、修复后通过；该修复没有生产代码变化。
- 最新计时边界修正后的全量 Go 测试曾通过，原始日志 `/private/tmp/b409-go-full-serialized-final-20260925.log`。随后发现 `cmd/agentd.go` 的 `setupLedger` 在数据库打开失败时把原始 DSN 写入错误日志。新增 `TestSetupLedgerFailureDoesNotLogDSNCredentials` 先红（记录显示完整 DSN 和测试口令），修复为只记录 `dsn_configured` 布尔值后定向测试转绿；测试输出保存在 [dsn-redaction-test.log](evidence/2026-09-25-b409-wave0/dsn-redaction-test.log)。暴露值只属于临时隔离数据库角色，重试前已轮换角色口令、清空临时日志；未使用或改动 7777 的凭据。修复后重新运行 `go test -p 1 ./... -count=1`，退出码 0。
- 当前隔离构建在按 helper 重建的真实夹具上，于内置浏览器完成三阶段各 100 次重载，全部 300 个样本成功；原始数组及阶段数据库核验在 UI evidence JSON 的 `latest_build_recheck_100`。最终 clone 有 50,609 行事件（含两条元数据）、18,674 条 `task_mirrored`、0 条 `mirror_event`。该 clone 的 CLI 输出不计入当前代码验收；有效 CLI 证据为上文较早的 `172653–172852` 样本。
- `127.0.0.1:7777` 原 agentd 仍监听；使用 `handoff console --print-url` 签发并兑换本机一次性 ticket 后，内置浏览器恢复到 `http://127.0.0.1:7777/cards`，截图可见看板。该会话登录恢复不需要重启原服务。
- 最终复审期间，`7788` 隔离实例使用独立配置、数据目录和克隆数据库 `handoff_b409_ui_recheck`；`7777` 原 agentd 保持原进程和状态。独立 review PASS 后已保存截图和原始样本，随后停止隔离实例并回收克隆数据库、配置及数据目录；原数据库与原 agentd 未触碰。

## 独立复审与阶段裁决

- **独立复审：实现轴 PASS（2026-09-25）**。GPT-6-Sol 子 agent 对照已批准 spec r2、Wave 0 计划及 B156.2 §3.4.1/§3.5 复核实现、冻结边界、测试与真机证据；代码未发现阻塞问题。发现两项 Important 交棒缺口：I1 被测实现未提交；I2 最新 clone CLI 样本错误归属旧二进制且原始 stderr 含连接位置。关闭记录见下方。
- 审查者逐项核对三阶段 100 个 UI 原始样本、各 5 次预热与 0 错误，独立复算 p50/p95/max 为 406.5/436/458 ms、406/435/515 ms、412.5/496/509 ms；SQL 复核最终克隆库为 50,609 行（其中夹具 50,607）、`task_mirrored=18,674` / 12,752,128 bytes、`mirror_event=0`；截图可见目标会话和 `#341874` 起的消息。审查者也确认较早 CLI 证据走过当前 Store/Service 查询日志；旧 clone CLI 输出不被计入。
- 审查者在最新源码上重跑聚焦 Go 测试（exit 0）；代码图 `check --stale` 为 0 fail。全量 Go suite 使用协调者此前对相同工作树的运行记录（exit 0），审查者未重复执行。`codegraph validate` 的 B272/B374 两条视图问题是既有问题，不属于本次 finding。
- **首轮阶段裁决：待封版**。当时实现尚未提交；后续关闭记录见下节。最终 clone 的旧 CLI stderr 已删除，替代为不含连接 URL 的当前路径摘要及筛选日志。被测 UI 构建曾运行于隔离 `7788`，隔离数据库及临时凭证已回收；原 `7777` agentd 未重启且仍可用。

## Important findings 关闭与最终阶段裁决（2026-09-25）

- **I1 关闭：被测实现与证据已提交。** S2 源码、测试、Wave 0 计划及首批验收证据同属提交 `0703b2eb1675b2229a5f4124998683d2de9dd02f`，tree `92698d7855956899a2c36b8d64c536c38836d338`；`git status --short` 为空。独立审查后没有修改被测代码。提交后按同一 SHA 新鲜运行 `go test ./cmd ./internal/ledger ./internal/ledger/api ./internal/collab ./internal/agentd -run 'TestSetupLedgerFailureDoesNotLogDSNCredentials|TestRoomHistory|TestRoomMessagesEndpoint' -count=1`，五个包均 exit 0。全量 `go test -p 1 ./... -count=1`、Web 1,476 测试、typecheck 与 build 对应同一代码树的既有新鲜记录；本次不重复全量套件。
- **I2 关闭：CLI 证据归属已纠正并脱敏。** 最终 clone 的旧安装版 CLI 原始 `room-read.stdout/stderr` 已从提交材料删除；旧版序号不得再作为本次路径证明。早先 CLI 的原始输出仅留在 `/private/tmp`，仓内只保留 `[room-read-current-cli-summary.json](evidence/2026-09-25-b409-wave0/room-read-current-cli-summary.json)` 与筛选后的 `[room-read-current-cli.stderr](evidence/2026-09-25-b409-wave0/room-read-current-cli.stderr)`：200 条、seq `172653–172852` 严格递增，包含当前 Store 查询开始/完成和 collab 完成日志。原始 CLI build ID 未留存，已明确标为未知，不冒称可核对的二进制 SHA。对提交证据目录扫描 `postgres://` 与旧 `target=` 字段，均无命中。
- **最终阶段裁决：Wave 0 通过，仅限 S2。** 被测提交 SHA、PG/SQLite、HTTP/CLI、内置浏览器、取消/空/错误和承重变异证据已可取得；两项 Important 已关闭。B409 S1–S5 完整目标、昨晚 20:00–22:00 的整体故障根因、linux-01 部署和父卡最终验收仍未完成。

## 边界

此记录只判定 B409 S2 Wave 0。B409 批准 spec r2 的完整 S1–S5、远端摘要陈旧标注（**超过 30 秒显示陈旧及观测时间；摘要不能驱动当前数量或筛选；没有 5 分钟隐藏上限**）、session wait 恢复、Linux `agentd` 部署及最终父故事验收仍待后续完成。
