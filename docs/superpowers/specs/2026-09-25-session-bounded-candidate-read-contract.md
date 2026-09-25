# 会话监听有界候选读取：B358 / r2 契约增量

> 状态：契约增量，基于 B409 r2 与 session reliable-mentions r2，2026-09-25 冻结。
> 基线提交：`8865403c5e2e734a0174d1c9b005b83c37dbeb0d`。
> 生产者：`ledger.Store` 的会话消息候选读；`collab.Service.MessageWakeTargets` 的既有寻址判定。
> 消费者：`cmd/session.go#runSessionWait` 的一次性、`--follow` 与显式 `--since` 订阅。

## 冻结理由与适用范围

B358 §3.8 冻结 `EventsFromAsc(nil, cursor, 500)` 全流读取，§4.6 条 49 再次要求会话订阅走全流读取。已批准的 session r2 则将默认起点改为共享交付游标、恢复停机期间未交付的定向消息，并增加积压摘要；B409 U5 还要求避免把无关账本历史搬入等待进程。现状 `cmd/session.go#runSessionWait` 对恢复窗口和 follow 循环都调用 `Store.EventsFromAsc`，随后逐行调用 `sessionWaitMatch`，该函数先用 `room.IsSessionRoom` 限定房间，再以 `collab.Service.MessageWakeTargets` 做最终命中判定。`Store.EventsFromAsc` 的空 `cardIDs` 明确代表全流（`internal/ledger/events.go#Store.EventsFromAsc`）。

本增量**只替换会话订阅的物理读面**。它不改变用户可见 CLI/JSON、寻址结果、交付语义、超时、`Store.Follow` 或 wakeconsumer。它不指定具体 LedgerClient 方法名、参数、DTO、SQL 或索引实现；这些留给 U5 plan，并继续执行 U4/U5 共享 seam gate。

本轮源码核验锚点：`cmd/session.go#runSessionWait`、`cmd/session.go#sessionWaitMatch`、`internal/collab/sessions.go#Service.MessageWakeTargets`、`internal/collab/sessions.go#Service.replyAuthorOf`、`internal/collab/room/room.go#IsSessionRoom`、`internal/collab/room/delivery.go#ResolveDelivery`、`internal/ledger/events.go#Store.EventsFromAsc`、`internal/ledger/session_delivery_cursor.go#SessionDeliveryCursor`、`internal/ledger/session_delivery_cursor.go#AdvanceSessionDeliveryCursor`。其中 codegraph 对 `Store.EventsFromAsc` 命中；其余符号未命中或为 moved，均以当前源码复核并记入 U5 图覆盖债。

## 冻结清单

1. `session wait` 的候选枚举不得逐页读取全局 `card_events` 事件流再在 CLI/collab 丢弃无关事件。
2. 会话消息候选读必须返回所有在本次读取对应状态下可能由 `MessageWakeTargets` 定向到订阅 member 的 `room_message`，不得漏掉有效命中。
3. 候选范围必须覆盖 `mentions` 中经 `ResolveDelivery` 去首尾空格后等于 member 的身份。
4. 候选范围必须覆盖 `mentions` 中经 `ResolveDelivery` 去首尾空格、再由现有 `MessageWakeTargets` 可解析为 member 当前席位的卡号。
5. 候选范围必须覆盖 `reply_to` 指向同一房间、且作者经 `ResolveDelivery` 去首尾空格后等于 member 的消息。
6. 每条候选仍须通过 `room.IsSessionRoom` 检查后才可进入命中判定。
7. 每条会话候选仍须由唯一的 `collab.Service.MessageWakeTargets` 判定确实命中订阅 member 后才可输出。
8. 候选读不得在 CLI、ledger 或新 helper 中复制一份最终寻址规则。
9. 会话订阅不得按会话成员列表广播。
10. 候选分页使用账本 `seq` 严格升序。
11. 每页候选范围以排他下界、包含上界限定。
12. 续页不得跳过尚未返回的候选。
13. 续页不得重复同一候选行。
14. 首次 backlog 固定在启动时捕获的 `MaxSeq` 上界内，覆盖 `(last_delivered_seq, startup_max_seq]`；没有持久交付游标时 `last_delivered_seq=0`。
15. backlog 完成后，`--follow` 从本次进程已扫描的上界继续捕获更高 `MaxSeq`。
16. backlog 与 follow 的候选范围连续衔接，不跳过两者边界间的消息。
17. 进程内候选扫描水位可跨过未命中候选或无候选的已查询范围。
18. 进程内扫描水位与持久 `session_delivery_cursors.last_seq` 分离。
19. 持久交付游标只在定向命中成功写入 stdout 后推进到该输出覆盖的最大命中 seq。
20. 仅扫描候选或无命中不得推进持久交付游标。
21. 显式 `--since N` 继续从 `seq>N` 读，不读取或写入持久交付游标。
22. 显式 `--since N` 也使用同一有界候选读语义。
23. 候选查询、引用查询或 `MessageWakeTargets` 依赖读失败时，命令返回可见错误。
24. 上述读取失败不得输出伪造的空 backlog。
25. 上述读取失败不得推进持久交付游标。
26. 查询只把候选消息及组装既有 wake 所需的引用行返回应用。
27. 固定有效候选集时，增加无关房间、卡、结构事件或 mirror 历史不得线性增加查询返回行数。
28. 固定有效候选集时，增加无关房间、卡、结构事件或 mirror 历史不得线性增加查询返回 payload bytes。
29. 数据库执行路径须用地址候选键与 seq 范围选择读取，不得每轮物理枚举全部事件 payload。
30. 若使用派生候选投影，canonical `card_events` 写入与投影维护须在同一事务原子提交。
31. 若使用派生候选投影，投影须可从 canonical 事实幂等重建。
32. PostgreSQL 与 SQLite 对相同金样本返回相同候选 seq、排序和上下界语义。
33. PostgreSQL 与 SQLite 对相同金样本返回相同错误与取消语义。
34. backlog 的既有单行摘要形状、实时 `SessionWake` 行形状、stdout 成功即交付与游标失败可重投等 session r2 承诺保持不变；不增加 `ack` 或处理完成回执。
35. `Store.Follow` 继续排除无卡事件；会话订阅仍不扩展 `Store.Follow`。
36. wakeconsumer 边界不变。

## 被替代的既有条文

- B358 §3.8 中规定 `EventsFromAsc(nil, cursor, 500)` 全流物理读取的句子，由本增量第 1–33 条替代。
- B358 §4.6 条 49 中“订阅通道读侧走 `EventsFromAsc` 全流读”的物理读取承诺，由本增量第 1–33 条替代；同条 `Store.Follow` 语义不变的部分保留。
- B358 §3.8 / §4.6 关于缺省从流尾开始的旧行为，已由 session reliable-mentions contract v1 第 1 条替换；本增量不再重复改变默认起点。
- session reliable-mentions contract v1 的“`(cursor, MaxSeq]` 全流分页扫描”由本增量第 10–33 条替代；范围、排他/包含边界及最终命中语义不变，仅候选物理读取收窄。

## 兼容边界与验收

- 一次性 wait、follow、显式 since 的退出状态、超时与 stdout JSON 保持 session reliable-mentions r2 contract 的既有金样。
- 使用显式 @、卡号当前席位 @、有效同房间回复、无效回复、无寻址消息、结构消息、卡席位变化、错误、取消、分页边界和跨页多命中建立独立金样。
- 同一固定候选集分别增加无关账本行；观察实际返回 rows/payload bytes，并以 PostgreSQL `EXPLAIN (ANALYZE, BUFFERS)` 与 SQLite `EXPLAIN QUERY PLAN` 核实执行计划使用地址候选键和 seq 范围，不把对全表事件 payload 的每轮扫描藏在数据库过滤中。
- 真机共享 PG、CLI 跨机器断线重挂、浏览器及 B409 三档增长仍按 U5/U7 计划验收；本契约冻结不代替这些行为证据，也不证明 2026-09-23 20:00–22:00 事故根因。

## 拍板记录

命中三重闸门：选择有界候选读而非保留全流读，跨 collab/ledger/CLI 且若未来改回会重新打开历史传输成本；没有本增量时后续执行者会自然沿用 B358 明文的 `EventsFromAsc(nil, ...)`，很难从 U5 的产品故事推断这是故意不做的优化；取舍是账本需维护或索引可查询的寻址事实，并验证席位映射与回复的完整候选覆盖，而全流方案实现更简单但每次等待都随无关账本增长。用户已批准的 r2 交付结果不变。

被否方案：保留全流分页并只优化 CLI 过滤（仍传输全部 payload）；把最终寻址复制进 ledger/CLI（破坏唯一判定入口）；只查显式 mentions 而忽略有效 `reply_to` 或当前卡席位（会漏消息）。

## 图、Ticket 0 与移交

- 不新增领域方向或组装点：现有 `d_cli → d_collab` 与 `d_collab → d_ledger` 接缝继续使用；本轮不冻结方法名、参数或 DTO，因此无 Ticket 0、无新符号视图 diff。
- 无 wire/编码变更；backlog JSON 金样继续由 session r2 contract 及其现有消费方测试约束。本轮无需新协议金样运行测试。
- 移交 U5 plan：将本增量作为 U5 的物理读基准；在实现前完成 U4/U5 实际新增 LedgerClient 方法/DTO 清点。若相同新增 seam 被两个工作单元消费，先独立冻结那个最小接口 contract；若没有则不造共享接口。
