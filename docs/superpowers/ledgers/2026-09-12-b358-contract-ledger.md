# B358 contract 轮台账（2026-09-12）

卡：B358（会话即工作单元：房间锚点从卡翻转到会话）；分支：cards/B358-charter。
按纪律边干边追加；每行一个已确立的事实 / 跑过的命令与原始输出 / 做出的判断。
本台账与产出物同批提交（收入 contract 提交）。

## 上游状态位核对

- [fact] spec `docs/superpowers/specs/b358.md:5` 头部「**状态**：**已批准**（用户 2026-09-12：「我觉得可以，就这么定吧」……）」——开工核对一致，无需回写。
- [fact] 有效基线分支 `cards/B233.1-charter-7` @ `94246fc8`（`git merge-base HEAD cards/B233.1-charter-7` 输出 `94246fc8a2acc7996c254cb6b473c7c9b5aef225`）。
- [fact] 本卡分支 `cards/B358-charter`，HEAD 起点 `966654cf`（含 spec 与两支北极星入库）。工作分支只在当前分支工作，不切分支。

## 现状读数（本轮对工作树复核，带符号锚）

- [fact] `internal/collab/room/room.go#Resolve`（:55）今天只认卡房间（GetCard 命中）与旧群房间（project:<name>/global）；会话房间无解析。`#VerifyWriter`（:102）按 kind 分权限的书写者矩阵仍在（B358 显式废止按 kind 分权限的取向，但本轮 Ticket 0 只补会话语义、不删旧矩阵——删旧矩阵属越过空壳的可观测行为，列入交棒欠账）。
- [fact] `#KindAllowed`（:154）kind 白名单 7 种仍在；B358 §4.2 要废止 kind 白名单，同属交棒欠账。
- [fact] `internal/agentd/wakeconsumer.go#automationWakeEvent`（:144）首行 `if ev.CardID == "" { return false }`——群/会话消息今天谁都不醒（spec §4.3 现状读数复核一致）。
- [fact] `#decodeHumanRoomMessage`（:201）`kind == user && !by_system` → 唤醒本卡协调者，`mentions` 在唤醒路径里没被读（广播形状，spec §4.3 复核一致）。
- [fact] `internal/proto/seat.go#ValidateSeat`（:20）/`#ParseSeatIdentity`（:53）席位身份形如 `cli:<cli>#<session_id>`，来源 bind|coordinate；席位权威在 `cards.driver_session`（`internal/ledger/binding.go#BindSeat`/`#RebindSeat`）。
- [fact] `internal/ledger/store.go` 两方言 DDL 在 `ddlStatements(pg bool)`；`ensureSchema` 逐语句执行，CREATE IF NOT EXISTS 幂等，加列走 ALTER 容忍 duplicate column。`TestDDLDialectParity`（internal/ledger/ddl_parity_test.go:26）逐表名比对两方言，往任一加表必须同步另一支。
- [fact] `internal/ledger/events.go#appendEvent`（:19）是落事件唯一入口；`Store.mutate`（store.go:156）写事务包裹。事件类型词表在 `internal/ledger/types.go:44` 起。
- [fact] `internal/ledger/api/api.go#eventWire`（:147）与 `internal/agentd/ledgerapi.go#ledgerEventWire`（:111）是两处事件直通投影；`var _ client.LedgerClient = (*Facade)(nil)`（:28）是接口满足的编译期执法。
- [fact] `internal/collab/client/client.go#LedgerClient`（:26）是会话侧出站接口，接口归使用方；扩方法必须由 Facade 实现（编译期红）。
- [fact] `cmd/card_wait.go#runCardWait`（:56）默认订阅口径；`room send`（cmd/room.go:118）走 `collab.Service.Send`；`room list`（:49）走 `ListRooms`。
- [fact] `web/src/api/rooms.ts` 是 TS 契约镜像；金样本双侧在 `internal/proto/rooms_fixture_test.go` 与 `web/src/api/rooms.test.ts`（引 `web/src/api/testdata/RoomsFixture.json`）。
- [fact] 图覆盖债：`codegraph sym collab.Service` / `sym collab.Service.Send` / `sym room.Resolve` / `sym wakeconsumer.go` 均报「不在图中」（近似候选空）；`sym VerifyWriter`、`sym RoomMessage`、`sym RoomSummary`、`sym Service.ListRoomsForMember` 命中。会话相关符号以源码锚为准。
- [fact] 图三闸基线读数：`codegraph validate` 报 122 个完整性问题（全是既有 `cards-B233.13-charter` 等视图的陈旧引用 + 1 条跨文件调用边门控）；本轮加 `cards-B358-charter` 视图后仍 122、且 0 条来自本视图。`codegraph check` 基线 6 处 fail（1 dead-interface `OrchestrationClient` + 5 over-budget），全为既有、不由本卡新增。

## 落码与命令证据

- [fact] 落 `internal/proto/sessions.go`：Session/SessionMember/SessionCard/SessionSummary/SessionNode/SessionTimelineEvent/SessionDetail + 三个结构事件载荷 DTO；`internal/proto/rooms.go#RoomMessage` 增 `ReplyTo int64`（投递寻址的隐式半边）。
- [fact] 落 `internal/ledger/types.go`：四个会话结构事件常量 EvSessionCreated/EvSessionArchived/EvSessionCardJoined/EvSessionCardLeft。
- [fact] 落 `internal/ledger/sessions.go`：Session 实体 + Store.CreateSession/GetSession/ListSessions/ArchiveSession/JoinCardToSession/LeaveCardToSession/AddSessionMember/SessionOfCard；两方言加 sessions/session_cards 两表与唯一索引 uq_session_cards_card。
- [fact] 落 `internal/ledger/api/api.go`：Facade 八个会话直通镜像 + translateNotFound（ledger.ErrNotFound → client.ErrNotFound）。
- [fact] 落 `internal/collab/client/client.go`：LedgerClient 八个会话方法 + `ErrNotFound` 使用方哨兵。
- [fact] 落 `internal/collab/room/delivery.go`：`ResolveDelivery`/`IsAddressed` 纯函数——签名里没有成员集合，扇出形状写不出来。
- [fact] 落 `internal/collab/sessions.go`：Service.CreateSession/ListSessions/SessionDetail/ArchiveSession/JoinCard/LeaveCard/WakeTargets/MessageWakeTargets/AddressesCard（带会话成员/状态/节点/timeline 投影）。
- [fact] 落 `internal/collab/sessions_test.go`：直通竖切（真 SQLite → Facade → Service：开会话 → 拉卡进群 → 无卡群消息落账 → History 读回 → 列表/详情投影）+ 投递寻址 + 归档只读 + 纯函数寻址。
- [fact] 落 `internal/collab/room/delivery_gate_test.go`：源码级扇出守卫（ResolveDelivery 形参无成员集合语义、函数体同时引用 Mentions 与 ReplyTo；全包不得出现 broadcastToFanout 类函数）。
- [fact] 命令：`go build ./...` → 退出码 0（当轮原文）。
- [fact] 命令：`go test ./internal/collab/...` → `ok github.com/Xsxdot/handoff/internal/collab 4.762s`；`ok .../internal/collab/room 0.002s`（当轮原文）。
- [fact] 命令：`go test ./internal/ledger/...` → `ok .../internal/ledger 21.849s`；`ok .../internal/ledger/api 1.200s`（当轮原文）。
- [fact] 命令：`codegraph validate --view cards-B358-charter` → 本视图 0 issue；`codegraph check --view cards-B358-charter` → fails=6（与基线逐条相同）、warns=140。
- [fact] 落 `internal/proto/sessions_fixture_test.go`：Go 侧 wire 金样本（`reply_to` omitempty 零值/非零/往返、SessionSummary/SessionDetail 键集、成员状态词表）。命令：`go test ./internal/proto/ -run 'TestRoomMessageReplyTo|TestSession' -count=1` → 四条 PASS。
- [fact] 落 `codegraph/diffs/cards-B358-charter.json`：39 个新增符号（会话 DTO / Store 会话法 / Service 会话法 / room 投递纯函数 / Facade 直通）+ 2 个修改（RoomMessage 增 ReplyTo、LedgerClient 增会话法）。
- [fact] `codegraph/target.json` 三条既有方向注记补 B358 说明；无新方向、无预算变化。

## 判断

- [judgment] 档位：**重档**（合同级已定：会话模型重做 × 账本域 × 控制面 × CLI × 控制台，远超流程固定成本且可并行）。重档法定步骤：Ticket 0 骨架 + 直通竖切 + 冻结提交。竖切已落在 collab 库缝（夹具直调真 SQLite，主缝形态）。
- [judgment] 契约边界：本节点只落「会话语义 + 投递寻址 + 账本会话存储 + 源码守卫」的骨架与可观测纯函数。旧的 kind 白名单/书写者矩阵/卡房间 Resolve 的**删除**、wakeconsumer 唤醒路径改接寻址、HTTP/CLI/控制台接线，均属越过空壳的可观测行为，逐条列入交棒欠账——它们不能靠空壳冒充完成。
## 提交事实

- [fact] 冻结提交（本批产出随此提交冻结）：
  `git commit -m "contract(B358): 会话即工作单元——会话语义/投递寻址/账本会话存储冻结"`
  → `dc75627e33fc2ff2514cb8296518abac52fae7d2 contract(B358): 会话即工作单元——会话语义/投递寻址/账本会话存储冻结`（原始输出；本台账与产出物同批 amend 收口，amend 会换 hash——历史读数如实记录，不把 amend 后 HEAD chase 回写）。
