# B409 contract 台账：房间历史限域读

日期：2026-09-24
卡：B409「全账本读路径分域与投影性能：卡片、会话、房间与等待」
规格：`docs/superpowers/specs/2026-09-24-ledger-read-performance.md`（r2，用户已批准）
基线：`codex/session-reliable-mentions` @ `186bd7ba1f0eaa1d1d2e91e264167a5474f47d57`

## 节点裁决

本轮只冻结 Wave 0 所需的 S2 房间历史读取接缝。实现保持 `d_collab` 拥有房间/会话业务语义，`d_ledger` 拥有追加事件及限域存储查询；请求仍经会话侧 `LedgerClient` 接口，不增加跨域方向，也不让 collab 直接访问 SQL。精确签名、历史窗口、排他游标、排序、空结果、错误传播及取消语义已增补到 `docs/superpowers/specs/b156.2-contract.md` §3.4.1，并把旧 A.5 的十万行门槛改为已关闭。

## 现状证据

- `codegraph who-calls n_collab_Service_History` 显示 `GET /api/rooms/{id}/messages` 与 `room read` 都进入 `collab.Service.History`。该节点当前锚为 `moved`；直接核对源码后确认 HTTP handler 调用 `HistoryContext(r.Context(), ...)`、CLI 调用 `History(...)` 包装入口，两者共用用户可见历史语义。
- 源码核实 `internal/collab/service.go#HistoryContext` 先调 `room.ReadAllEventsContext(lc, 0)`，再在 collab 中筛 `room_message`、房间 ID 和 `beforeSeq`，最后截尾。这使房间首屏取决于全账本事件量。对应 helper 暂无有效 codegraph 符号，见图覆盖债。
- `codegraph sym n_ledger_Store_EventsFromAsc` 显示底层查询仅按 `seq > fromSeq`，可选按 `card_id` 筛选，升序取页；它没有消息类型或房间范围条件。调用方逐页拉取时会把不相关 payload 一并传回应用。
- 源码核实 `internal/collab/room/room.go#RoomIDOf`：非空 `card_id` 是卡房间身份，否则由 `RoomMessage.Room` 提供 session/全局房间身份。新查询必须覆盖该规则下的历史事件，不能只覆盖新写入。
- 已批准 spec 的本次共享 PG 样本约 21,270 个事件；完整事件流客户端耗时 12.08 秒，而 PostgreSQL 单页执行约 0.19ms。证据支持“读出并处理超出展示范围的数据”是房间首屏的主要已证路径；不据此断言数据库网络或连接池是独立根因。

## 冻结的外部行为

1. 新增 `LedgerClient.RoomMessagesBeforeContext(ctx, roomID, beforeSeq, limit)`。它仅返回该房间的 `room_message`，按 `beforeSeq` 排他限界并返回最近 `limit` 条，最终仍按 seq 升序；读取受 context 取消。
2. `collab.Service.HistoryContext` 沿用现有 `beforeSeq`、默认 200 条、升序截尾与空房间返回空切片的外部语义，但改为调用限域账本读；它把非正 `limit` 规范为 200，账本方法要求正数并拒绝非法直接调用。SQL/索引/投影细节留给 Wave 0 plan/implement。
3. 空房间与不存在房间仍返回空切片；发生账本读取错误时必须保留错误并走现有错误映射。不得将失败折叠为成功的空消息列表。
4. PostgreSQL 与 SQLite 对同一事件金样返回一致的 seq 集、顺序、上界、默认数量与错误语义。辅助索引/投影必须能从 `card_events` 重建。
5. `EventsFromAsc` 与 `Store.Follow` 保持现有全流/游标和无卡事件行为。Pending、Mentions、Consume、Unread、card wait 和 session wait 尚未由本次冻结；按批准 spec 在对应故事前另行修约。

## Codegraph 核对

- `codegraph check --repo . --stale`：`fails=[]`，warning 包含既有 legacy、budget-raised、best-dangling、oversized-package 与 prefix-family 项；本次只扩展已声明的 `d_collab → d_ledger` `LedgerClient` 接口，不新增方向或契约边，故 target 不变。
- `codegraph validate --repo .`：退出失败，当前 baseline 已有两项完整性问题：`cards-B272-charter` 引用不存在的基线领域 `d_coordination_api`；`cards-B374-charter` 重复新增已存在的 `k_collab_model`。本次未改这些视图。
- 图覆盖债：`codegraph sym n_collab_Service_HistoryContext` 与 `n_collab_room_ReadAllEventsContext` 未命中，`codegraph resolve --doc` 把 helper 锚报告为 `moved`；按规则回落核对 `internal/collab/service.go#HistoryContext`、`internal/collab/room/room.go#ReadAllEventsContext` 真源码。`RoomIDOf` 可查询，但节点锚也为 `moved`。本次不补无关图节点。

## 后续交棒

- Wave 0 plan 选定能满足等价语义、PG/SQLite 同构、历史回建与 p95 验收的账本查询实现；不得改变本台账冻结的调用方语义。
- 实现及 fresh review 后对 S2 的真 CLI/内置浏览器路径做 Wave 0 验收，并完成查询/页面端到端采样与承重变异。
- S3 开始前修订 B358 全流 session wait 与已批准 r2 会话续收契约：保留 `(cursor, MaxSeq]`、升序命中输出、共享水位、断线补收和无逐条确认；只替换低效读取机制，不改变投递保证。

## S3 / U5 会话候选读契约增量（2026-09-25）

- 新鲜源码复核：`codegraph --repo . sym Store.EventsFromAsc` 退出 0，明确该账本 API 的 `cardIDs=[]` 会执行全流 `seq > fromSeq ORDER BY seq LIMIT`；`codegraph sym collab.Service.MessageWakeTargets`、`Service.Mentions` 与 session cursor 符号未命中，按纪律回落读取当前 `cmd/session.go#runSessionWait`、`sessionWaitMatch`、`internal/collab/sessions.go#MessageWakeTargets` / `replyAuthorOf`、`internal/ledger/session_delivery_cursor.go` 源码。当前 backlog 与 follow 均逐页调用 `EventsFromAsc(nil, ...)`；最终寻址仍由 `MessageWakeTargets` 决定。
- 由于 B358 §3.8 / §4.6 条 49 与已批准 r2 / U5 的有界恢复读面冲突，新增 [`2026-09-25-session-bounded-candidate-read-contract.md`](../specs/2026-09-25-session-bounded-candidate-read-contract.md)：候选集合必须完整覆盖直接身份 mention、当前席位卡 mention、有效同房间 reply；最终判定不复制；候选页升序和有界；扫描水位与已交付持久游标分离；数据库以选择性地址键读取，错误可见，CLI JSON 与 no-ack 交付语义不变；若派生投影则与 canonical append 原子并可重建。未冻结方法名/签名/DTO/SQL，也未新建跨域方向或 Ticket 0。
- U4/U5 seam 按当前计划所需结果语义清点：U4 的 room/session/card 投影查询与 U5 的 recipient-address 候选/消费/seq 点读无共同新增 LedgerClient 语义，不造全用途接口；实际最终签名若收敛成相同公开 seam，实施前仍停下单独冻结。U5 plan 已引用该增量，并将候选查询的执行计划/返回 rows 与 payload bytes 纳入增长核验。
- 自检实际运行：`codegraph --repo . resolve --doc docs/superpowers/specs/2026-09-25-session-bounded-candidate-read-contract.md` 退出 0，9 个锚解析（`Store.EventsFromAsc`、`ResolveDelivery` 为 `ok`，其它当前源码锚为 `moved`）；首次用 receiver-qualified 形式 `Store.SessionDeliveryCursor` 造成 resolve `vanished`，改为实际方法名后通过。`codegraph --repo . resolve --doc docs/superpowers/plans/2026-09-25-b409-5-plan.md` 退出 0（无锚）；`git diff --check` 退出 0。文档没有新增代码、跨域边或 wire，因此无 Ticket 0、无金样本运行测试。新契约与计划修订待独立 `charter:review` / `charter:plan` 复审。
- 契约冻结提交原始命令与输出：`git commit -m "docs: freeze bounded session candidate reads"` → `[codex/session-reliable-mentions f63d8898] docs: freeze bounded session candidate reads`；`3 files changed, 95 insertions(+), 11 deletions(-)`。本提交随后仅 amend 一次收入原始提交输出；不追记 amend 后新 hash。
