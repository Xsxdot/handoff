# B358 contract 补签名轮台账（2026-09-12）

**卡**：B358（L3 重档）｜**节点**：contract（增补轮 v2）｜**执行者**：contract 节点 subagent
**触发**：breakdown 出稿轮三条退回项 R1/R2/R3（`docs/superpowers/specs/b358-breakdown.md` §0 P1–P3 裁决行、§2.3；台账 `2026-09-12-b358-breakdown-ledger.md`），协调者拍板退回 contract 补签名。
**分支**：`cards/B233.1-charter-7`（开工 HEAD `a921d1df`，契约 v1 冻结于 `ef8257bb`）
**产出物**：`docs/superpowers/specs/b358-contract.md`（就地增补）、`internal/ledger/types.go`、`internal/ledger/binding.go`、`internal/ledger/binding_test.go`、`internal/proto/sessions.go`、`internal/proto/sessions_fixture_test.go`、`codegraph/diffs/cards-B358-charter.json`、本台账。

---

## 一、现状查证（补签轮新增读数，逐条带出处）

- `internal/ledger/events.go#appendEvent`（`:22`）：签名 `(tx, sink, cardID, typ, actor string, payload any) (int64, error)`；同事务落事件 + PG pg_notify；「禁止绕过它裸 INSERT」。R2 的席位事件走它。
- `internal/ledger/binding.go#Store.BindSeat`（`:28-64`）：注释明言「不落事件也不写 driver_carrier」；mutate 事务内读卡→判空→UPDATE cards 两列；**无 appendEvent 调用**。R2 的改动点。
- `internal/ledger/binding.go#Store.RebindSeat`（`:105-108`）：同事务 `s.appendEvent(tx, sink, id, EvDriverTakeover, identity, map[string]string{"from":…, "to":…})`——actor=identity（席位自称）、载荷 {from,to}。R2 的同形样板。
- `internal/ledger/types.go:48-92`：事件常量唯一定义点；B358 会话四事件已在。R2 在此 +1。
- `EvStatusMoved` 载荷三处生产：`move.go:67`（普通转移 `{from,to}`）、`cards.go:709`（CloseCard 终止 `{from,to,reason}`）、`cards.go:730`（ReviveCard `{from,to,reason:"复活"}`）。R3 的 `{to}` 判定依据：收口判定值域 = `已完成(StatusDone)` / `终止(StatusClosed)`（均带 payload 可判）；`reason` 键仅终止/复活携带。
- `internal/ledger/follow.go:13-19`：`Store.Follow` 明文「含 card_id 为空的项目级事件**不在内**——多路 wait 只关心子树」。会话房间消息是无卡事件（`RecordRoomMessage` 落 `CardID=""`），物理上进不了 card wait 的 Follow 流。这是 R1 通道形态选型的决定性事实。
- `internal/ledger/events.go#Store.EventsFromAsc`（`:63` 起）：「cardIDs 空 = 全流（含项目级）」——无卡事件可经全流读拿到，wakeconsumer 的消费循环正是这么读的（`internal/agentd/wakeconsumer.go:234` `EventsFromAsc(nil, from, 500)`）。
- `internal/agentd/wakeconsumer.go#automationWakeEvent`（`:144-146`）：首行 `ev.CardID == ""` 直接不唤醒——外部半边今天零唤醒，未读照记（`RecordRoomMessage` 事件本身进流、游标照推）。
- `internal/collab/sessions.go#MessageWakeTargets` / `#AddressesCard`（`:127` / `:145`）：冻结的寻址判定唯一入口，签名无成员集合。R1 的命中判定复用，不在 cmd/agentd 重造。
- `cmd/card_wait.go#runCardWait`（`:56` 起）：card wait 起点=`st.MaxSeq()`（`:78`）、超时 124（`ExitTimeout`，`:178-182`）、stdout 逐事件 `json.Encoder` 一行一个（`:155`）、一次性默认 + `--follow` + `--timeout` 三 flag（`:369-372`）。R1 的 CLI 约定样板。
- `cmd/room.go#roomServiceFor`（`:33`）/ `#openRoomService`（`:39`）：CLI 侧 collab.Service 唯一组装点；`session wait` 归 P7 已立的 `session` 命令族，组装走同一点。
- `internal/collab/service.go` + `internal/collab/cursor`：未读数投影 = `unreadByRoom(events, cursors)`（ListSessions 内），游标介质 `room-cursors.json`。R1 载荷的未读数用同一投影。
- 图覆盖探针：`codegraph sym Store.BindSeat` → **在基线图**（`n_ledger_Store_BindSeat`，summary=「…不落事件…」——R2 后须 nodesModified 更新）；`sym RoomMessage` 在图；`sym SessionDetail` 不在图（近似候选 opencode.sessionDetail，不相关）。`codegraph resolve --doc docs/superpowers/specs/b358-contract.md` 开工读数：**8 ok / 11 moved / 0 坏**。

---

## 二、本轮判断与放弃的尝试

- **R1 通道形态判定（定调依据）**：用户 2026-09-12 探讨定调「外部会话走事件流订阅」（breakdown §0 P1 裁决行）。落到代码只有两条路：a) 扩 `Store.Follow` 让无卡事件进多路 wait——放弃：follow.go:13-19 的排除是 B156.3 的刻意设计（执行器对群聊零感知），动它 = 扇出禁令在执行器半边破口，且 PG LISTEN 行为面变大；b) 独立的会话维度 wait（`handoff session wait <member>`），轮询全流读 + 复用冻结寻址入口过滤——选定：增量最小（不碰 Follow、不碰 keystone、不建外部会话注册表），且与 P7 已立的 `session` 命令族同族。机内事实：wakeconsumer 已按全流读消费，订阅通道读侧同形。
- **R1 不实现通道本体**（协调者指令）：本轮只落契约签名 + 冻结条目；CLI 命令、载荷 DTO 落码、wakeconsumer 分流改接、金样本全部归 S3 外部半边（§8 欠账逐条列）。
- **R2 事件命名**：`EvDriverSeatBound = "driver_seat_bound"`。对侧查执：与 `EvDriverTakeover`（换绑）配对，`driver_` 前缀沿用卡上席位域既有词根；proto 侧 timeline kind `SessionEventSeatBound="seat_bound"`（契约 v1 已冻结）是其消费面。
- **R2 载荷**：`{to: identity}`（镜像 `EvStatusMoved` 终止事件的 `{to}` 单键形；无 from——初始坐下没有旧席位）。actor=identity，与 RebindSeat 同形。
- **R3 收口判定值域**：`to ∈ {已完成, 终止}`（`ledger.StatusDone`/`StatusClosed`）→ timeline kind=card_closed。依据：spec §4.3「卡收口或终止」、用户故事 9「卡何时收口」；普通列间转移（待办/进行中/待审阅）不是结构事实，不进 timeline。终止的 `reason` 在 payload 里，Detail 透传归 S2（不二次解释）。

（以下随轮次继续追加。）

---

## 三、R2 执行记录（席位入群事件）

- 改动：`internal/ledger/types.go` +1 常量 `EvDriverSeatBound = "driver_seat_bound"`（含不唤醒注释）；`internal/ledger/binding.go#BindSeat` 闭包收 `sink`，同 mutate 事务 `appendEvent(id, EvDriverSeatBound, identity, {"to": identity})`，注释同步更新。
- 新测试：`internal/ledger/binding_test.go#TestBindSeatFallsDriverSeatBoundEventInSameTransaction` + 帮手 `countSeatBoundEvents`。断言：恰一条、card_id/actor、payload 恰 {to} 一键、换绑不误落、CAS 冲突与空身份拒绝路径零事件。
- 编译期问题一笔：BindSeat 闭包原为 `_ *eventSink`，落事件需收 sink——改签名即过，无设计影响。
- 本轮命令与原文：
  - `go test ./internal/ledger -run 'TestBindSeat' -count=1` → 首轮 `undefined: sink` FAIL（编译错）→ 修后 `ok`（三测 PASS）。
  - 变异验证：临时删掉 appendEvent 调用 → `FAIL`；还原 → `ok`。测试可变红成立。
- 既有测试面排查：全仓 21 处测试用 BindSeat 做夹具，均只数 EvDriverTakeover 专项计数（countTakeoverEvents），无全量事件计数断言；wakeconsumer 对 `driver_seat_bound` 走 default 分支零唤醒（结构事件），预期全量绿（见 §七全量证据）。

---

## 四、R3 执行记录（卡收口 timeline kind）

- 改动：`internal/proto/sessions.go` 词表 +1 常量 `SessionEventCardClosed = "card_closed"`（插入 needs_human 与 archived 之间，词表注释写明载体与判定：仅 `EvStatusMoved` payload `to ∈ {已完成,终止}` 记行，普通列间转移不进 timeline；终止 reason 随 payload 透传）。
- 金样本：`internal/proto/sessions_fixture_test.go` + `TestSessionTimelineKindVocabulary`——timeline kind 全词表逐值冻结（8 值），消费方 switch 对齐义务写在注释。DTO 键集不受常量影响，原四测不动。
- 本轮命令与原文：
  - `go test ./internal/proto -run 'TestSession' -count=1 -v` → 4 测 PASS `ok`。
  - 变异验证：改字面量为 `card_closed_MUTATED` → `FAIL`；还原 → `ok`。
- 操作事故一笔（不改结果，只记过程）：首次变异验证后误用 `git checkout internal/proto/sessions_fixture_test.go` 还原，把新增测试一并回滚（工作树该文件当时未提交）。复核发现后重写测试、改用 /tmp 副本还原，复跑绿。教训：未提交文件的还原不走 git checkout。

---

## 五、收尾（协调者接手，2026-09-12 16:5x）

- 执行者 subagent 于收尾阶段被平台并发限额终止（与 B361 原型变体 agent 并行所致）；R1/R2/R3 的文档与落码在其生前已完成（§一–§四），全量证据与提交由协调者接手完成。
- `go build ./...` → 退出 0。
- `go test ./internal/ledger/... ./internal/collab/... ./internal/proto/... -count=1` → 全 `ok`（法定范围）。
- `go test ./internal/agentd/... -count=1` → 1 FAIL：`TestLegacyNodeEventSequenceUnchanged`（scheddispatch_test.go:531，golden 第 2 条漂移：got=dispatched / want=comment 挂账）。经临时 worktree 在补签前基线 `a921d1df` 复跑**同样红**——分支既有失败，与本轮改动无关（本轮只触 ledger/proto 席位与词表，golden 锁派发事件序列）。另立缺陷卡跟踪。
- codegraph validate/check/resolve 由协调者补跑（见提交后账）。
