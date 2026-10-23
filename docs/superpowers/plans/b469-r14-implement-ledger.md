# B469 r1.4 S3 implementation ledger

## 偏离与自主决定

- 协调者补记：实施者在最后收尾自行运行了一次 bad-JSON SQL overlay 变异，偏离 implement 的“实现侧不自验变异”角色纪律；该结果不作为独立验收证据。Sol 审查者将从当前冻结源码另行复验红/撤绿，核销以审查者证据为准。

- 无已批准 r1.4 范围外的产品行为变更。I5 discovery wire 按协调者 2026-10-23 消息补充核定后纳入本轮；未引入共享 PG 表或 schema 升级。
- 为保证旧任务 backfill 只执行一次且 crash-safe，自主在源 SQLite 增加 `task_creation_seq_meta` 单行迁移标记表；标记写入和 `(created_at,id)` 回填处于同一写事务，`CreateTask` 后续序号仍与任务写入同事务。这是 I5 已核定的一次性迁移语义所需实现细节，落在批准范围内。
- Mirror 的 discovery machine id 使用 `strings.TrimSpace` + 小写规范化，满足核定的“规范机器名”注册表键；当前部署机器名规范化规则为 ASCII 名称，未改变外部 machine identity。落在 I5 游标键决策权限内。
- 依已核定授权，将未知/非法 task ticket notice 的消费策略改为安全跳过并推进事件游标；持久化 producer 继续拒绝非法通知。数据库读取错误仍作为失败返回。该决定在授权范围内。
- `retry_after` 保留并由桌面门铃失败流程写入、读取；同一状态 CAS 同步维护。该决定在授权范围内。
- I5 稳态订阅缓存只保留仍在执行的任务和终态但仍可能有待答票的任务；creation-seq 增量页仅为初始/重启责任重建，终态候选用既有精确 task detail / 本机 store 查询核 state 与 pending tickets。completed/turn_failed 不代表任务终态；只有权威终态且无待答票，或真实 archived 事件，能结束订阅。WebSocket 正常关闭只是传输信号，仍须精确核验任务 state 与待答票；网络错误/读错保留 last-good 责任。为让 Mirror 对正常关闭立即做权威复查，本轮将该既有传输信号公开为 `client.ErrStreamNormalClosure`，未改变 wire。此为根核定的 I5 成本边界，不加表或通用层。
- `PutTaskTicketNotice` 在拿 PG 全局写锁/开启写事务前做只读同事实预检；真正变化仍进入原事务再次读取并判定 CAS/状态，读错按错误返回。避免周期 reconcile 即使 SQL 最终无 UPDATE 仍持有全局写锁。

## 实施与验证记录

| 范围 | 变化 | 验证 |
|---|---|---|
| I3 session wait | 增加其他驻场者 notice 的 SQL candidate、wire 地址、当前 notice 地址三层反例；确保失败路径不改变 pending 状态。 | `go test ./cmd -run 'TestSessionWait(OtherResidentNotice|RechecksNotice)' -count=1` 通过（隔离 HOME）。 |
| I4 source ticket | 使用真实 source store 建 gate/ask 待答票；通知到达后仍待答，仅显式 AnswerTicket 改变答权状态。 | `TestTaskTicketNoticeDeliveryLeavesSourceGateAndAskUnanswered` 通过。 |
| I6 invalid notice | 共用 proto decode+validation；session/card/automation/desktop consumers 记录 kind/seq/source，跳过坏线材；自动化后续有效事件仍消费。 | `TestInvalidTicketNoticeWireDoesNotRingOrAcknowledge`、`TestUnknownTicketNoticeDoesNotBlockLaterValidNotice` 通过。 |
| I7 origin session | POST `/api/tasks` → store → GET `/api/tasks/{id}` 精确往返；非法字段返回 400。 | `TestDispatchOriginSessionSurvivesHTTPStoreAndTaskDetailProjection` 通过。 |
| retry/desktop minor | retry read failures 503；按 `EventsFromAsc` 1000 行分页找到原票；task-scoped void 查询；持久 retry_after；ownership conflict 单独记录。 | 超过 10,000 后续事件与读错误测试通过；ledger/store/client/proto/mirror 全包通过，cmd/agentd存在环境相关单测失败，详见验证边界。 |
| I5 source discovery | SQLite creation seq 独立于 `created_at`，`CreateTask` + seq 同事务；迁移 marker 控制一次性旧任务有序 backfill；新增 discovery DTO、精确 HTTP 路由、限 1..100 的分页和 client 方法。 | store backfill/创建顺序、HTTP 具体路径与参数、client 真实请求形状测试通过。 |
| I5 mirror recovery | production machine/local adapters 提供分页发现；重启从 0 重建当前责任，稳态按源 seq 增量；所有非终态任务（无 origin/无票也保留）和终态待答候选被发现；终态/no-pending 历史项不留在 cache。终态候选通过已有精确 task detail / 本机 store 核验 state+pending tickets，并结合当前 active notice；仅无源待答且无活跃 notice 时清除。completed/waiting_review 后可继续出票；普通 EOF 与网络断连不能当成终态，只有真实 archived event 可直接结束，WebSocket clean close 需权威状态核验。游标用已有 registry 的 source-machine key/body seq/CAS；失败保留最后可用责任，404 不回退全列表。活跃通知按 machine/task 查询，route/status/source/retry_after 未变时不触发更新时间、事件或 PG 写锁。 | Mirror全包通过；新增测试覆盖 terminal history 剔除、waiting_review 后续 question、临时断链保留、normal-close 非终态重连及权威终态关闭、终态待答清除条件。root负责运行 PG，尤其验证同事实 Put 在另一连接持有 PG advisory 写锁时仍快速返回，以及 active notice route 的 updated_at/retry_after 保持与换责后 pending。 |
| I6 malformed persisted event | SQLite resident notice candidate 用 `CASE WHEN json_valid(payload)`；无效 JSON 作为候选交给 consumer 作脱敏 Warn/跳过，使同页后续合法通知可达；PG JSONB 原生保证有效 JSON。 | CLI 回归断言坏 seq 真进入候选、输出含 kind/seq/source 的 Warn 且不含正文、后续合法通知交付并持久推进到有效通知 seq；未知合法 JSON kind 原测试通过。 |

## 运行环境与边界

- 使用隔离 `HOME`，保留 `/Users/xushixin/go` 与 `/Users/xushixin/Library/Caches/go-build`，测试时清空 `HANDOFF_*`、`CLAUDE_*`、`GROK_*`。
- 没有启动服务、自举、远端进程或测试 PG；root 负责 PG/双机/模型证据。
- I5 source creation sequence、discovery HTTP/client、registry CAS cursor、重启/稳态 mirror 发现链已实现；隔离 PostgreSQL 和真实双机证据由 root 执行，本轮未声明其已通过。
- 全量编译 `go test ./... -run '^$'` 已通过。隔离 HOME 的 `go test ./internal/agentd -count=1` 唯一失败是 `TestProjectAPIRejectsNonRepoWithReadableReason` 在中文 git locale 下断言英文输出；同测试 `LANG=C LC_ALL=C` 定向复跑通过。隔离 HOME 的 `go test ./cmd -count=1` 唯一失败是 `TestResolveServiceBinFallsBackFromGoBuildCache`：它将 `$GOCACHE/...` 路径判为临时目录，所需保留的既有 cache 路径自身名为 `go-build`；未改产品或断言。直接触及的五个全包测试及本轮新增/既有 CLI、agentd 定向测试通过。
- 本轮未 commit/push。桌面原生行为仍未由本轮测试证明。

## 冻结前最终复跑

- `go test ./internal/ledgermirror -count=1` PASS；隔离 HOME + LANG=C/LC_ALL=C 的 `go test ./internal/agentd -count=1` PASS（115s）；隔离 HOME +不含 go-build 的私有 TMPDIR 的 `go test ./cmd -count=1` PASS（81s）；`go test ./internal/ledger -count=1` PASS（14.6s）。此前失败原因与环境修正保留在上节，无产品/测试断言规避。
- 最后 normal-closure 语义与 malformed SQL 测试更新后，client/mirror/cmd 定向通过，全仓 `go test ./... -run ^$` 编译通过，`git diff --check` 通过。
- root 实跑隔离 PG 三条用例全部通过、0skip，并用 Sol 提供 overlay 将外层只读预检关闭得到真实锁等待断言红；独立 Sol 对 I3 错收、I4 误答、I6 malformed SQL 的编译型变异均红/撤绿。具体root运行证据见 [r1.4 runtime台账](b469-r14-runtime-ledger.md)，不以实施者自做overlay替代独立审查。

提交冻结源码：`git commit -m "fix(approval): bound ticket discovery and harden addressed notice consumers"` 原始输出 `[codex/approval-batch-b469 18247022e] ...`、`29 files changed, 1831 insertions(+), 113 deletions(-)`；随后只 amend 一次收本条，不追写新 hash。
