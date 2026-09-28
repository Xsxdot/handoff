# B409.5 (U5/S3) 实现台账——收件箱消费与 session wait 限域恢复

> 执行者实现台账（charter:implement 结论纪律）。逐事实追加：命令、原始读数、
> 放弃的尝试、判断。最终报告以本台账为取证底稿。

## 一、实现前核实（动手前源码复核，plan §已核实生产链）

- `cmd/session.go#runSessionWait`（基线 8b4e3ce2）：backlog 与 follow 均为
  `st.EventsFromAsc(nil, scan, 500)` 全流读 + `sessionWaitMatch` 逐行判定
  （IsSessionRoom → MessageWakeTargets）。确认为本卡要替换的物理读面。
- `internal/collab/service.go`：`Pending`/`Mentions`/`Consume`/`consumeRoomMentions`
  均经 `room.ReadAllEvents(lc, 0)` 全流读后在 collab 过滤；`Consume` 为定位
  cardID 做全流线性扫描。
- 席位写路径盘点（契约第 8 条）：全仓 `driver_session` 写点恰三处——
  `internal/ledger/binding.go#Store.BindSeat`(:60)、`#Store.RebindSeat`(:121)、
  `#Store.clearSeatTx`(:264)；clearSeatTx 的调用方为 `ClearSeat`(:283) 与
  终态路径 `internal/ledger/cards.go#CloseCard`(:711)。与有界候选读契约
  §源码核验锚点一致。
- U4/U5 seam 清点（plan 闸核实）：U4 已落公开方法 =
  `RoomMessagesBeforeContext`/`RoomMessageSnapshotsContext`/
  `SessionProjectionEventsContext`/`LatestNeedsEventsContext`（范围投影读）。
  U5 新增公开方法 = `MentionCandidatesContext`/`CardRoomUserMessagesContext`/
  `ConsumedMessageSeqsContext`/`EventBySeq`（recipient-address 候选/消费标记/
  点读）+ ledger.Store 直读面 `SessionMessageCandidatesContext`/`SeatRevision`。
  两组过滤键与结果语义不同，无共同新增方法或 DTO——seam 闸通过，未落成同一
  共享公开 seam。
- `sessionMessageMentions`（CLI 正文 @ 编译）逐条对照 r2 金样与桌面
  `web/src/app/rooms/sessionModel.ts#extractSessionMentions`（冻结语义基准）：
  两侧同一语法（Go 空白分词 + `@` 前缀 + 统一记法/卡号校验）。**发现**：
  `@user:sy,` 在两侧都被寻址为字面身份 `user:sy,`——名字语法 `[^:]+` 允许
  标点，共享金样如此；`B233.16。`（尾随句号）两侧都拒绝。CLI 无需改动
  （判断：与桌面金样一致即合规；「不修剪尾随标点」的落点是标点随 token
  原样进入寻址键，而不是把带标点 token 判为非法）。
- codegraph 图覆盖债沿用 plan 记录：wait/send/cursor Store 方法/当前
  Pending/Mentions/Consume 实现按真实源码复核，图中未命中或 moved。

## 二、设计判断

- **席位一致性选 revision 复读，不选页内锁**（契约第 6–17 条二选一）：
  1. 锁方案需要把「账本候选读」与「collab 判定读」（经 client 接口缝、
     GetCard 逐 mention 读卡）圈进同一把锁——collab 经接口缝访问账本，
     CLI 侧无法横跨两个组件持锁；且 SQLite 单写者锁会阻塞一切写路径。
  2. revision 是单一单调量：`ledger_meta` 键 `seat_revision`，在三个席位
     写路径同事务读-增-写（nextSessionSeqTx 同款）；候选查询前读、该页全部
     `MessageWakeTargets` 判定后复读（空页也复读），不等即丢弃重读。
  3. 版本单调不复用（契约第 9 条）⇒ A→member→A 的 ABA 必改版本被发现；
     集合值比较发现不了（拍板记录明确拒绝）。
  4. 多余递增安全（清座幂等重入也递增）：只会多重读一页，不漏检。
- **进程内扫描水位与持久交付游标分离**（契约第 31/32 条）：页确认后扫描
  水位可跨到页上界（穷尽页）或末候选 seq；持久游标只在 stdout 成功后推进
  到已输出最大命中 seq。`sessionWaitPage` 返回 (hits, next) 分离两者。
- **候选读刻意不做 SQL 过滤的行**（超集契约）：by_system/pointer、
  project:/global 等非会话无卡房间、reply 跨房间行——把寻址/房间规则复制进
  SQL 会违反契约第 20 条，金样测试逐行钉住这些行必须出现在候选里。
- **CLI/桌面 token 金样**：不修 `sessionMessageMentions`——桌面金样语法
  允许名字含标点（见一）。补金样断言把该边界钉住（TestSessionMessageMentionsGoldens）。
- **测试缝**：`sessionWaitBeforeQuery`（版本读后、查询前）、
  `sessionWaitBeforeJudge`（查询后、判定前）、`sessionWaitBeforeAdvance`
  （stdout 成功后、游标推进前），生产恒 nil。ABA 护栏需要「查询前」翻转：
  A→member（查询前）→A（判定前）使页前后集合值相等——集合值比较实现会
  接受该页且丢消息，revision 实现丢弃重读后交付。
- 源码守卫 `TestSessionWaitSourceGuard` 更新为沿委托链
  runSessionWait→sessionWaitRun→sessionWaitPage→sessionWaitMatch 逐层检查
  （判定只许落 sessionWaitMatch 的 MessageWakeTargets；链上全部函数保留
  kind 门控/mentions 直读 AST 禁令）。

## 三、命令与读数（追加式）

- `go test ./internal/ledger/ -run 'TestSeatRevision|TestSessionMessageCandidates|
  TestMentionCandidates|TestCardRoomUser|TestConsumedMessage|TestEventBySeq'`：
  首轮编译红（新缝符号未定义，TDD 首红）→ 实现后 SQLite `ok`；
  `TestPGSeatRevisionSameSemantics`+`TestPGSessionMessageCandidatesSameGolden`
  带 LEDGER_TEST_PG_DSN `ok`（PG 16.15 / handoff_b409_test）。
- `go test ./internal/collab/...`：rewiring 后 `ok`（既有 B156.2 语义金样全绿）。
- `go test ./cmd/ -count=1`：`ok`（含既有 backlog/follow/timeout 全部金样 +
  新增席位护栏 3 例 + 游标失败 2 例）。54.6s。
- 隔离 PG 探活：`current_database()=handoff_b409_test`，PostgreSQL 16.15
  (Debian 16.15-1.pgdg13+)，初始 0 事件。

## 四、变异自验（两段判定：先确认编译过，再数红；还原后回绿）

| # | 变异点 | 变异内容 | 编译 | 变异后红 | 还原后绿 |
|---|--------|----------|------|----------|----------|
| M1 | `cmd/session.go#sessionWaitPage` | `if revAfter != revBefore` → `... && false`（席位一致性守卫短路） | ✅ | `TestSessionWaitABARebindDetectedAndRedelivered`、`TestSessionWaitOneWayRebindStillDeliversOnce` FAIL（重读次数/下界断言） | ✅ 两测试 ok |
| M2 | `cmd/session.go` 积压输出条件 | `len(backlog.Hits) > 0` → `>= 0`（空积压也输出+推进） | ✅ | `TestSessionWaitDefaultRecoversBacklogAndDoesNotRepeat`、`TestSessionWaitEmptyPageStillVerifiesSeat` FAIL | ✅ 两测试 ok |
| M3 | `internal/ledger/seat_revision.go` | `current+1` → `current`（版本永不递增） | ✅ | `TestSeatRevisionIncrementsOnEverySeatWritePath`、`TestSeatRevisionNotReusedAfterABA` FAIL | ✅ ok |
| M4 | `internal/ledger/message_candidates.go` | 删除 mentions∋member 的 SQL 过滤（`query += " AND " + mentionMatchExpr` 移除） | ✅ | `TestMentionCandidatesBoundedRead` FAIL（ledger 金样）+ `TestPendingGroupMentionAndConsume` FAIL（collab 全链路） | ✅ 两包 ok |

- M4 先行尝试（collab 侧 Pending 过滤循环中删 `MentionsMember`）变异存活——
  该 Go 侧检查对账本缝而言已是纵深防御冗余（SQL 已保证 mentions∋member），
  真正守卫在 ledger 缝；遂改打 ledger 缝过滤本身（M4），全链路两处变红。
  collab 侧检查保留：它是 B156.2 裁决链在门面内的显式表达，代价为零值检查。

## 五、第 8 步：单机端到端等价链路（真 CLI 二进制 × 两进程 × 共享 PG）

- 证据原文：`docs/superpowers/evidence/2026-09-26-b409-u5/e2e-single-machine.md`
  （完整 transcript）。
- 链路：`go build` 真 binary → 首挂空等（124，无积压无推进）→ listener 停止，
  生产者（同 DB）建会话并投递 @agent:e2e-u5 ×2 + @agent:other-b ×1 → 同 member
  重挂：backlog 恰 2 hits（冻结格式、@他人不在、无效 token 不寻址）exit 0 →
  再挂不回放（124）→ 另一进程 `--follow` 常驻 + 生产者实时投递：两行冻结
  `SessionWake`（@他人不叫醒）→ 空闲超时 124。
- 持久游标直读：`session_delivery_cursors.last_seq = 140132`（恰为最后一条
  已输出命中 seq；两轮 E2E 各自验证）。
- 跨机器重挂验收：留 S3 故事级验收（协调者）。同机两进程共享同一 PG 与同一
  member 身份，游标/账本链路与跨机一致；差异仅在进程宿主机。

## 六、插曲与残余债

- **共享 PG 前缀冲突插曲**：首轮夹具用 `Project: "p"`，自动前缀 P 被
  `TestPGSmokeEndToEnd` 的项目 `pg-smoke-临时数据` 需要时冲突（共享库跨测试
  残留）。修复：U5 PG 夹具项目改 `u5`（前缀 U，同项目幂等复用）并显式
  `Workflow: "charter"`（共享库已有多条工作流，缺省解析被拒）；清理专用库
  内 project p/u5 的残留卡与前缀行后，`TestPGSmokeEndToEnd` 恢复通过。
  教训：专用可丢弃库上的夹具必须自带「可重入」卫生（唯一项目名/显式前缀/
  清理回填）。
- `room.ReadAllEvents`（B156.2 全流读助手）在 collab 生产路径已无调用方
  （Pending/Mentions/Consume/consumeRoomMentions 全部改走限域读），现存调用
  仅测试文件；保留不删——删除属 room 包语义清理，超出本卡边界。
- `--timeout 5s` 一次性空等样本（真 CLI × 共享 PG，5 次）：5.136 / 5.083 /
  5.085 / 5.080 / 5.085 s，全部 ≤6s、exit 124。30 次口径留 S3 故事级验收。

## 七、遗留与移交

- 跨机器真机重挂验收：留协调者（S3 故事级验收）；本卡做单机两 CLI 进程 +
  共享 PG 等价链路（见台账 §三后续记录）。
- 图覆盖债：新增 ledger seam（SessionMessageCandidatesContext/SeatRevision/
  MentionCandidatesContext/CardRoomUserMessagesContext/ConsumedMessageSeqsContext/
  EventBySeq）、cmd 扫描器分层、collab 限域改接——recon 按真实关系回填。
