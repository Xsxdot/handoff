# B456 后端实现记录

## 偏离与自主决定

- 本轮按计划 r1.3 核销 I1–I4，并按主会话后续明确批准的最小事件契约实现 I5：复用现有 session event stream 记录 `resident_round` start/end/fail，不增表、端点或 wire 字段。round ID 关联三个阶段；claim/lease 不作为运行状态，只有最近 start 且没有后续 terminal event 才显示 running。
- I2 以一次 card-scoped CTE 计算 latest dispatch、当前 task raw 镜像与 lifecycle 镜像，替换历史镜像逐行相关子查询；窗口固定最多 `limit+6`，超出标记 `truncated`。解释计划只覆盖隔离 review PostgreSQL schema。
- 新增 opt-in delivery failure harness 模式仅由 `B456_BROWSER_HARNESS_DELIVERY_RETRY=1` 开启：fake adapter 第一次 permission 投递注入一次错误，真实 `delivery_failed` 被本地 event poller 观察后清除该错误。答复已写入，因此不是同票重试或待处理票据；恢复通过既有 `/api/tasks/{taskID}/resume` 重投已答复事项。默认 harness 行为不变，不执行外部命令。
- 按主会话冻结澄清从 plan r1.2 收口到 r1.3：远端 mirror snapshot 只有与同 task ledger lifecycle 时间可比且更新时才可覆盖；`FetchedAt` 不作跨时钟排序。未更改 r4 的六态和冻结 wire。
- 按主会话 F8 裁决，票据候选超过 500 时不增加 wire/分页；collab SessionDetail 直接返回可行动错误“会话待处理事项超过本次读取上限，请先通过全局待处理入口处理或拆分会话”，HTTP 走既有 unavailable/error 路径。票据截断不再混入 `MoreActivity`。
- opt-in harness 的 P1 采用真实 manager/fake executor `Dispatch`，真实 permission/question/Finish producer 与 HTTP reply；P2 completed、P3 wake-failure 仍是明确注明的 ledger/mirror fixture。无生产 agentd、线上端口或 PG 被启动/触碰。

## 完成内容

- `internal/ledger` 增加限定当前会话 card IDs 的有界事实查询，并沿 `ledger/api` 与 `collab/client` 暴露。普通 comment 在 SQL 层按 `wake_round:` 排除；固定保留最新 dispatch、当前席位绑定、当前 dispatch 原始镜像和最新 lifecycle 镜像；task lifecycle 额外保留只关联当前 `(target, task)`，不会按历史 task 分区无界增长。超出常规窗口显式 `truncated`。
- 未决票据只读权威 `open_ticket_projection`，按卡范围查询且只返回引用。超出 500 项则 SessionDetail 失败关闭，避免漏票造成不可达动作；`MoreActivity` 仅表示动态截断。
- `collab` 纯投影处理 resident 作者、executor/协调者独立性、空 target 精确关联、生命周期状态、wake 当前席位/dispatch 边界、票据和事件摘要。普通 progress 仅作 recent activity，不改变生命周期基准。`delivery_failed`、`turn_failed`、`failed` 显示公开原因摘要并截断，不读取完整执行输出。
- I1 修正把未被更新 lifecycle 取代的 `delivery_failed` 作为硬异常；task `running` snapshot 不包含答复投递结果，不能覆盖该异常。只有同 task 更新 lifecycle 或新派发/任务事实才可解除。真实 `/api/sessions/{id}` 测试覆盖 permission → delivery_failed → ticket_answered → completed + `waiting_review` 与 running snapshot 仍异常的反例。
- I5 在首轮 `LaunchForResident` 与续轮 `WakeResident` 实际调用前写 start，返回后以同 round ID 写 end/fail；失败 Pointer 保留原人类可读信息。collab 只按当前驻场 identity 读取该会话的有限 round window，并按最新关联回合投影状态与摘要；读失败沿既有 SessionDetail 错误边界返回。桌面驻场没有该回合来源仍是 unknown。
- I5 当前席位边界由 `SessionMember.ResidentSatAt`（`json:"-"`）传给纯投影：只查询当前 `session+identity`，且仅当前 `push` 驻场身份的 round facts 参与状态；`CreatedAt < SatAt` 的旧席位事件被排除，相等时间允许参与。为限定每次查询，新建双方言部分索引 `idx_resident_round_session_seq`，DDL 在每次 schema ensure 中以新索引名创建，使既有库也会补建；读窗和 start/terminal 幂等查询都用 `type='resident_round'` 常量匹配 partial-index predicate。
- 新增上述 PG 索引后按项目机制将 `ledgerSchemaVersion` 从 2 升到 3，保留 1/2 历史指纹并追加 v3 指纹 `cf4412170e9255dfaa882c8f4a31259f9205963ca4fcb22519c9f17e959d0642`。隔离 PG 回归把旧 marker 降到 2 并移除索引，再经正式 `Open` 验证 marker=3、索引重新安装、search_path 仍限于专用 schema。
- I4 账本测试使用两张真实卡分别写入不同 target/task/seq/ticket，验证双卡查询和两个单卡查询的隔离；硬窗口测试验证最新 dispatch、wake、seat boundary、当前 task raw mirror、最新 lifecycle mirror 在 `limit+6` 内保留且明确截断。
- I2 SQLite 增长回归在同一卡 bulk-seed 500/2000/5000 条 raw mirror（seed 不计入计时），真 query 用时分别 6.745/24.208/60.739ms，输出数量保持≤106并保留最新 raw 与 completed lifecycle；`EXPLAIN QUERY PLAN` 对三规模均无 `CORRELATED`，原始输出 `/tmp/b456-r2-sqlite-growth.log`。测试对单次查询设置宽松 3 秒上限，挡住 25 秒级相关子查询退化。
- agentd 组装本机 manager task snapshot；只有本机 `GetTask` 明确 `ErrNotFound` 时读单条既有 mirror snapshot，不调用全量列表或远端 fanout，并校验 target。`Task.UpdatedAt` 仅用于同 task snapshot 与 ledger lifecycle 冲突排序，不作为 since 或心跳；快照无法可靠排序时不复活旧状态。
- `internal/store`/`orchestration` 增加既有 `mirror_tasks` 的单 task 只读 Get；`SessionRoleBasis` 和排序元数据均为 `json:"-"`。真实 mux HTTP JSON 生成共享金样 `web/src/api/testdata/SessionDetailB456.json`，其中包含 roles、recent、ticket_refs 和缺省 since。
- opt-in browser harness 默认 Skip，启用后使用临时 SQLite/httptest，manifest 权限 0600。真实 P1 task 由 `/api/tasks/{taskID}` 读到全文 pending permission 后保持未答，供浏览器操作；fake 脚本顺序为 permission gate #1、question、permission gate #2、Finish；浏览器先批准第一门、回答 question，再拒绝第二门并填理由，确保拒绝理由的真实引导消息不会被下一道 question 消费；harness 通过 `Manager.EventsFromAsc` 从本地 events 表读取并镜像至 ledger，发布 manifest 前的真实 `SessionDetail` 断言确认 executor `recent` 与 `ticket_refs` 均含当前 permission。其它可视角色的种子等级记录于 manifest。
- 增加 `TestSessionRoleTicketsPostgresTwinIsCardScoped`，在同一隔离 schema 通过真实 `AppendMirroredEvent` 建投影，然后双卡与单卡读取；activity PG twin 使用同一 schema，插入 500/2000 行并对 CTE 运行 `EXPLAIN (ANALYZE, BUFFERS)`。原始输出 `/tmp/b456-r2-pg-final.log`：500 行执行计划 `Execution Time: 3.455 ms`，2000 行 `9.502 ms`；API query 分别约 37ms/27ms（包含测试驱动和扫描）。计划无 `SubPlan`，CTE/扫描 loops 为固定节点扫描，不再有每历史行重复 dispatch 子计划。测试清理自己的 `b456_role_*` schema，不删数据库或写 public schema。

## 红绿与验证证据

- F7 唯一承重变异已执行，不再重复：临时关闭远端 snapshot 新旧闸，测试可编译但 `TestSessionRoleStatusHTTPKeepsLifecycleTimeAcrossProgress` 失败，断言实际 executor=`running`、coordinator=`unknown`，而预期 `idle/waiting` 且保留原 since。原始 stdout：`/tmp/b456-f7-red.log`。立即恢复源码闸后同测试通过，stdout：`/tmp/b456-f7-green.log`（`ok .../internal/agentd 0.581s`）。主会话之后只做稳定实现 fresh green，不执行额外 mutation。
- F8 红绿承重入口：真实 SQLite mirror event→`open_ticket_projection` 写入 501 个 question ticket；真实 `/api/sessions/{id}` 返回 HTTP 500 并包含“超过本次读取上限”和“全局待处理入口”。`go test ./internal/agentd -run 'TestSessionRoleTicketLimitFailsClosedOverHTTP|TestSessionRoleStatusHTTPWire|TestSessionRoleStatusHTTPKeepsLifecycleTimeAcrossProgress' -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 1.178s`。
- I1 原始红绿：旧实现先将真实 HTTP 测试 `TestSessionRoleDeliveryFailureRecoveryHTTP` 投影成 `exception`（旧 delivery_failed 覆盖较新 completed）；修正后通过，并新增 `completed/waiting_review` 后新派发 running 的同一 HTTP 测试。旧红输出未单独保存；当前绿原始输出 `/tmp/b456-i1-http-green.log`。
- I2 SQLite/Go 定向：`go test ./internal/ledger ./internal/collab` → ledger、collab 均通过；新增承重窗口/卡隔离断言在其中。三包测试命令曾运行 `go test ./internal/ledger ./internal/collab ./internal/agentd`，agentd 整包以 `FAIL` 退出但当时未保存 stdout，不能据此宣称整包通过；独立定向 B456 HTTP 测试随后通过。
- I3 opt-in PostgreSQL：`LEDGER_TEST_PG_DSN` 从 `/tmp/b456-pg-dsn` 读入进程环境（未打印 DSN），只允许 `handoff_b456_review_` 数据库并创建/清理 `b456_role_*` schema。`go test ./internal/ledger -run '^TestSessionRole(ActivityPostgresTwinAndExplain|TicketsPostgresTwinIsCardScoped)$' -count=1 -v` → 两项 PASS（输出 `/tmp/b456-r2-pg-final.log`）。
- retry-mode harness opt-in smoke：`B456_BROWSER_HARNESS=1 B456_BROWSER_HARNESS_DELIVERY_RETRY=1 B456_BROWSER_HARNESS_HOLD_SECONDS=1 ... go test ./internal/agentd -run '^TestB456BrowserHarness$' -count=1 -v` → PASS（`/tmp/b456-retry-harness-smoke.log`）。这只证明 fixture 启动、manifest、真实 pending 断言和 poller 可用，不代表浏览器已执行 resume 恢复。
- genuine producer two-gate harness smoke:`B456_BROWSER_HARNESS=1 B456_BROWSER_HARNESS_HOLD_SECONDS=1 B456_BROWSER_HARNESS_MANIFEST=/tmp/b456-session-status-smoke.json go test ./internal/agentd -run '^TestB456BrowserHarness$' -count=1 -v` → `PASS`。更早一轮 smoke 因断言等待 `completed`（manager 正确产出 `waiting_review`）失败，修正为生产状态后通过；不存在用户数据影响。
- 早期 retry-mode browser harness 已由主会话完成并关闭。现场真实 permission answer 落账后，真实 delivery_failed 使票据不再 pending；主会话通过已有 `/api/tasks/{taskID}/resume` 恢复投递，再完成 question/permission 流程。现场证明了已观察的恢复链，不是同票仍 pending 的 retry；新 I1 实现仍需主会话 fresh browser 复验失败保持异常。
- 新增 I5 真实入口验证：resident launch HTTP 在 Launch 阻塞时为 running、返回后 idle；resident resume真实consumer失败为 exception、同身份后续真实成功回合恢复为 idle；带同身份旧 push fail 的 desktop 当前席位仍 unknown。`TestB444SessionDetailResidentDriverKeys` 覆盖桌面unknown反例，定向输出包含在 `/tmp/b456-r2-targeted-green.log`。
- resident round 专项 DB 核验：SQLite `TestSessionResidentRoundQueryUsesSessionScopedIndex` 的真实 `EXPLAIN QUERY PLAN` 命中专用部分索引；同 session 插入 120 条其它驻场 identity 的新活动后，当前 identity 查询仍只返回自己的两条回合事实。PG isolated companion 插入 1000 条其它 session/identity round 事件，旧签名版本的计划曾以 `Bitmap Index Scan on idx_resident_round_session_seq` 命中索引（0.111ms，`/tmp/b456-r2-resident-pg.log`）；最终 SQL 已再加 identity 条件，该 companion fresh rerun 等主会话执行，避免把旧输出冒充最终签名的证据。PG 新 companion 只扩测 resident-round 读窗，不重复前两项 activity/ticket PG twin；前两项主会话复跑原始计划在 `docs/superpowers/ledgers/b456-pg-explain-r2.log`。
- 这一轮后端定向 fresh：`go test ./internal/ledger ./internal/ledger/api ./internal/collab ./internal/agentd -run 'TestSessionRole|TestResidentRound|TestB444SessionCreateResidentFailureThenRetry|TestB444ResidentResumeFailurePointsOnce|TestB444SessionDetailResidentDriverKeys|TestB456ResidentRoundHTTPStartThenEnd|TestB456' -count=1` → 四包 PASS，原始输出 `/tmp/b456-r2-targeted-final.log`。主会话的 `go test ./...` fresh 与浏览器复验尚待完成。
- 增加 dispatch 最近动态后，Go HTTP JSON 金样按真实 mux 重新生成：`B456_UPDATE_FIXTURE=1 go test ./internal/agentd -run '^TestSessionRoleStatusHTTPWire$' -count=1` → PASS；共享 fixture 仅更新 `web/src/api/testdata/SessionDetailB456.json`。
- 全量 Go 首次复跑另外发现的答复摘要旧断言已更新为中性事实“工单答复已记录”；`go test ./internal/collab ./internal/ledger -run 'TestTaskMirrorRecentSummariesKeepTicketAndFailureMeaning|TestSchemaVersionPinsDDL' -count=1` → PASS。旧 `TestSchemaVersionPinsDDL` 输出了新增 DDL 的指纹 `cf441217...d0642`，该预期失败原始输出未保存；追加版本 pin 后测试转绿。
- PG marker upgrade 隔离回归：`LEDGER_TEST_PG_DSN` 仅从 `/tmp/b456-pg-dsn` 注入进程；`TestB456PostgresSchemaVersionTwoInstallsResidentRoundIndex` 在 `handoff_b456_review_*` 专用库的 `b456_role_*` schema 将 marker=2 且缺索引，再调用正常 `Open`，断言 marker=3、index 存在和 `current_schema` 未越界。PASS，原始 `/tmp/b456-r2-schema-upgrade-pg.log`。
- 本轮最终 `go build ./...` 与主会话 fresh 全量复验仍待主会话执行；未运行全仓库复核或新 mutation。

## 未验证与边界

- 需要主会话在 retry mode 有效 hold 内完成浏览器复验；opt-in 现场不是生产环境。P1 脚本由真实 manager/fake producer运行，permission/question/deny-reason 边界是真实 producer/HTTP；P2 completed、P3 wake failure 为 fixture，不能将全部卡片称作生产端到端。
- I5 的 round event 证明的是最后观测到的回合阶段，不宣称进程此刻仍存活。进程在 start 后崩溃、且没有明确 terminal/interruption 事实时，读面会继续报告“最近观测到开始、未观察到结束”，不会按 lease/TTL 自动归为故障。event query 限定 session 并最多读取固定窗口，截断会进入 MoreActivity；查询失败不静默成功。首轮/续轮记录的 start/end/fail 仅覆盖生产调用返回边界。
- 主会话需要对稳定代码执行 fresh Go build/tests、PG（本次 isolated PG twin 已有结果）及浏览器验收；当前记录是待复审交接，不是最终通过声明。
