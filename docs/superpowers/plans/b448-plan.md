# B448 实现计划：协调者首轮拉起按载体机转交

- 状态：**已拍板（2026-10-19）**（出稿者：plan subagent；拍板者：主会话协调者；Q1–Q5 全取推荐项，裁决见下）
- 输入：`docs/superpowers/specs/b448.md`（已批准，L3，单卡一轮不扇出，高难度，主会话亲自盯）
- 基线：分支 `cards/B448-charter-1` @ `b9e68472`
- 读者：零上下文执行者。文末「核实读数」列出本稿引用的代码事实及出处，动手前可复核。

---

## 待拍板（已拍板 2026-10-19）

> **裁决汇总**：Q1 甲（`card rebind --launch` 一并转交——同走 `runOnCoordinatorCarrier` 共用骨架，留下就得保留 spec 要删的「指向本机载体」提示；协调者认定为 S1 的同入口变体，属范围内补足，不另立故事，终验收时与 S1 同列核销）；Q2 甲（两个端点 `.../coordinator/first-round` 与 `.../resident/first-round`）；Q3 甲（无回执时受理机重读驻场行、席位空才落指针行；回执带 `pointed`）；Q4 甲（只延长两个新收件端点的写超时与出站上限至 ≥30m；用户面三端点 11m 问题落 roadmap）；Q5 甲（测试对用专名 `t448-linux` / `t448-mac`，主机名判据留给 fleet 生产复核）。


> 每条给出推荐项（**荐**）和理由。

1. **Q1 「叫机器人换绑」（`card rebind --launch`）是否一并转交。** `handleCoordRebind`（`coordapi.go:235`）走的是 `launchCoordinatorRoundForRebind` → `launchCoordinatorRoundWithExpect`（`scheddrain.go:242-281`），和叫机器人共用同一个 `runOnCoordinatorCarrier` 骨架。spec 的 S1–S6 没有点名换绑。
   - **荐 甲**：一并转交。卡转交请求多带 `expect`（换绑 CAS 前值），收件端用 `RebindSeatMode(expect)` 落座。理由有三：转交判定收口在共用骨架上（spec「收口点」），排除换绑就得在骨架里专门开一个「换绑仍 fail-closed」的口子；spec 要求删掉「请把协调者小队的成员指向本机载体」这句提示，如果换绑仍然拒绝，这句提示就还得留着；换绑与首轮的落座代码本来就是同一个函数 `bindLaunchedCoordinator`。超出 S 列表的部分需要协调者确认，是否要回人确认由协调者定。
   - 乙：换绑遇到远端载体仍然拒绝，改用新文案「换绑暂不支持远端载体，请在 <机器> 上执行」。wire 少一个字段，但骨架里要多一个分支。
2. **Q2 端点形态。**
   - **荐 甲**：两个端点，各挂在自己的资源路径下：`POST /api/cards/{id}/coordinator/first-round` 和 `POST /api/sessions/{id}/resident/first-round`。这和续轮转交 `/api/cards/{id}/coordinator/wake` 同形；老对端对这两个新路径一律回 404，判据单一。
   - 乙：一个带类型的端点，例如 `POST /api/coordinator/first-round {kind:card|resident, id}`。少一条路由，但请求体里多一个判别字段，收件端的错误映射也要按 kind 分叉。
3. **Q3 发出之后没拿到 HTTP 回执（读写中断、超时）时的会话指针行归属。** `client.do` 把「拨不通」和「读写中断」都归成 `ErrUnreachable`（`client.go:500-514`），受理机分不清对端到底执行了没有。
   - **荐 甲**：受理机重读共享账本里这场会话的驻场行。驻场位为空，就由受理机补一条指针行（覆盖「根本没送到」）；驻场位非空（在途或已坐），受理机不写，指针归执行机。代价是一个极少见的情形：对端失败、已请下并已落指针行，但回执丢了。这时会多出一条重复指针行，可以接受。卡路径没有指针行，只按文案报错。spec Out of Scope 已经明确「结果找回」后续再做，本条不负责合成成功。
   - 乙：没拿到回执时受理机一律不写指针行。缺点是对端不可达（S2 的关键失败）时会话里一条指针都没有，不满足 S2。
   - 丙：解析 `net.OpError.Op=="dial"`，区分「拨号失败」与「发出后中断」。这样会依赖 Go 错误形状和 relay 隧道的错误形状（relay 走 `ErrTunnelDisconnected`），脆弱，否。
4. **Q4 写超时。** agentd 的 `http.Server.WriteTimeout` 是 11 分钟（`cmd/agentd.go:531`），首轮回合的上界是 30 分钟（`hostapi.DefaultTurnTimeout`，`server.go:1539`）。执行机的回执如果超过 11 分钟才写，会写失败，受理机收到的就是「发出后中断」。
   - **荐 甲**：只在两个新收件端点上用 `http.NewResponseController(w).SetWriteDeadline(now + coordWakeTurnTimeout + 余量)` 放宽写截止；受理机出站 ctx 设 `coordWakeTurnTimeout + 余量` 作为上界。三个入口端点（launch / sessions / resident/launch）不改，与今天本机路径对齐，满足 spec「不得比本机路径更早放弃」。入口自身超过 11 分钟丢回执是今天就存在的潜伏问题，落 roadmap。
   - 乙：三个入口端点也一并放宽。这能顺手修掉今天本机路径的潜伏问题，但超出 spec 范围，而且会改变 Web 和 CLI 的可见行为（等更久）。
5. **Q5 真机靶子的设备名。**
   - **荐 甲**：两台隔离 agentd 用专用设备名 `t448-linux`（linux-01）和 `t448-mac`（mac-mini），载体登记的 `machine` 用 `t448-linux`。测试库自带一张设备表，名字与生产不重名，即使配错也不可能寻址到生产 7777。代价是主机名判据那条路（`IsLocalMachine` 的 hostname 分支）不在隔离靶子里覆盖，改由 fleet 生产复核覆盖。
   - 乙：用真实机器名 `linux-01` / `mac-mini`。

---

## 0. 交付目标

本轮交付 spec S1–S6 全部故事，单卡一轮，不扇出：

| 故事 | 本轮做到 |
|---|---|
| S1 叫机器人跨机 | 受理机选定载体后发现载体在远端：归还名额 → 转交到载体机 → 载体机冻结准入 → 首轮 → push 席位与承载（含 B446 自坐兜底）→ 受理机按今天的 HTTP 映射回给调用方 |
| S2 建会话 / 叫驻场者跨机 | 会话和拉卡在受理机完成（共享账本）；驻场者首轮转交到载体机，载体机执行 B444 的「坐下 → 拉起 → 补写 push_session」以及失败处置（请下在途行 + 指针行） |
| S3 CLI 三条命令 | 不改 CLI：`card coordinate`、`session create --squad`、`session resident launch` 都只打本机 agentd 的 HTTP（`cmd/card.go:506`、`cmd/session.go:792`），随 S1/S2 自动生效 |
| S4 本机载体 | 不经转交，行为逐字不变；本机载体相关的既有测试零改 |
| S5 防环 | 带转交头的请求，载体仍不在本机 → 拒绝，不发第二跳 |
| S6 名额 | 受理机在转交前已归还名额；名额只在执行机占用、执行机归还；执行机名额满时回给调用方「协调者并发已满」409，文案与本机路径相同；launch_queue 出队转交时，执行机报名额满要能被识别为 `ErrNoSlot`，从而按既有规则延后回填 |

若 Q1 取甲，换绑也纳入转交；若取乙，换绑遇远端载体仍拒绝，换用新文案。

**留待后续（落 roadmap，本轮不做）**：转交后回执丢失时的「结果找回」（spec Out of Scope）；入口端点 11 分钟写超时的潜伏问题（Q4 甲）；续轮唤醒收件端 `handleCoordWake` 不检查转交头的问题（见 §7 观测项）。

## 1. 触及子系统与类型

| 子系统 | 类型 | 本轮角色 |
|---|---|---|
| agentd 编排层（`internal/agentd`） | 逻辑型（两个 Server 共用一个账本，测试可闭环） | 转交判定、受理端出站、执行端收件、错误映射；本卡主体 |
| agentd HTTP 客户端（`internal/client`） | 逻辑型 | 两个新出站方法，以及老对端 404 哨兵 |
| wire DTO（`internal/proto`） | 逻辑型（契约面） | 新增请求、回执、错误体类型 |
| 跨机传输（设备表客户端池、relay、Tailscale 直连） | **边界型** | 不改代码，只消费；长请求能否撑过 30 分钟只能真机验证 |
| scheduling / ledger / collab / keystone | 不改 | 只经既有门面消费：`LaunchAdmit`、`AdmitSeatCarrier`、`Release`、`BindSeatMode`、`SitResident` 等 |

单卡、单工作单元，不派子卡，所以不核派卡资格四条。

## 2. 故事映射与行为闭环

### 2.1 故事 → 实现责任 → 真实消费者 → 集成/回归负责人

| 故事 | 实现责任 | 真实消费者 | 集成/回归 |
|---|---|---|---|
| S1 | 骨架远端分支、卡转交出站、卡收件端 | Web「叫机器人」、`POST /api/cards/{id}/coordinator/launch` | 执行者：竖切测试 V1；协调者：真机 R2 |
| S2 | 驻场转交出站、驻场收件端、指针归属（Q3） | Web 项目行建会话、`POST /api/sessions`、`POST /api/sessions/{id}/resident/launch` | 执行者：V2；协调者：R3、R4 |
| S3 | 无新增代码 | CLI 三条命令 | 协调者：真机 R5 |
| S4 | 骨架本机分支保持原样 | 全部本机入口 | 执行者：既有测试零改且全绿 |
| S5 | 收件端本机判定 + 拒绝 | 设备表不一致的两机 | 执行者：V5 |
| S6 | 受理端先归还名额；执行端冻结准入并持有名额；`no_slot` 映射回 `ErrNoSlot` | 入口 409、launch_queue 延后回填 | 执行者：V3、V6；协调者：R6 |

### 2.2 行为闭环

| 触发者 | 权威事实 / 载体 | 消费者 | 可观察结果 | 实现责任 |
|---|---|---|---|---|
| 人在 A 机控制台点「叫机器人」，小队载体 X 在 B 机 | A：`LaunchAdmit` 选出 X → 立即 `Release`；B：`AdmitSeatCarrier(squad,X)` 计数 +1，回合结束 −1；`cards.driver_*` + `seat_bearings.machine=B`（共享账本） | A 的 launch handler → Web / CLI | HTTP 200 `{woke:true, session_id}`，卡详情显示 push 席位、承载在 B；回合结束后两机计数都是 0 | `scheddrain.go` 骨架 + 卡收件 handler + `client` |
| B 上的席位随后收到卡事件 | `seat_bearings.machine=B`（由 B 写入） | 既有唤醒消费循环（A 转交给 B，或 B 自己认领） | 续轮在 B 执行（既有路径，本卡不改，真机复核闭环） | 无新代码，真机 R2 第 4 步 |
| 人在 A 机项目行建会话，驻场小队载体在 B 机 | `sessions` / `session_cards` 由 A 写；`session_residents(push, carrier_machine=B)` 由 B 写，先在途、后补写 push_session | A 的 `createProjectSession` → `SessionCreateResp.resident` | 会话立即出现；驻场者显示 `push · X@B`；首轮能发言 | 驻场转交 + 驻场收件 handler |
| B 不可达 / B 是老版本 / B 名额满 / B 判定载体不在本机 | 无写入（或 B 已回滚自己的写入）；双方计数都是 0 | 入口 handler | 文案点名机器与原因（§8 F21–F24）；会话里恰好一条指针行（Q3）；卡不落席位 | 受理端错误映射 + 指针归属 |
| B 在首轮途中重启 | `session_residents` 在途行，`carrier_machine=B` | B 的启动清扫 `sweepInterruptedResidents` | 在途行被请下，并落「被重启打断」指针行（既有逻辑，前提是行属于 B） | 无新代码；V2 断言 carrier_machine=B 让该前提成立；真机 R7 |
| launch_queue 在 A 出队，载体在 B | 同 S1 | A 的 `drainQueuesOnce` | B 名额满时 `errors.Is(err, scheduling.ErrNoSlot)` 为真 → 延后回填，不停清队 | `no_slot` 映射回哨兵 |

## 3. 改动边界

**有界文件集**（新文件拆分属于执行者权限，见 §6）：

- `internal/proto/`：新增 DTO（建议新文件 `firstround.go`）——`FirstRoundTransferReq`、`CoordinatorFirstRoundResp`、`ResidentFirstRoundResp`、`FirstRoundTransferError` 和 kind 词表常量。形状以 §8 为准，名字可调。
- `internal/client/coordinator.go`（或 `sessions.go`）：新增 `CoordinatorFirstRound(ctx, card, req)`、`ResidentFirstRound(ctx, session, req)`；新增哨兵 `ErrFirstRoundUnsupported`（404，与 `ErrApproverUnsupported` 同款，`coordinator.go:18-48`）；非 2xx 解码成带 `Status/Kind/Message/SessionID/Pointed` 的类型化错误。发起方负责 `MarkForwarded`，这两个方法本身不重试。
- `internal/agentd/scheddrain.go`：
  - `runOnCoordinatorCarrier`（`:375-417`）是收口点。现在是 `LaunchAdmit` 加 `defer Release`，改成：
    - 准入策略二选一：受理时用 `LaunchAdmit`；收件时用冻结的 `AdmitSeatCarrier(squad, carrier)`。
    - 远端分支：受理时**先显式归还名额**，再调调用方给的「转交」回调；收件时（已转交来的）返回 not_local，不出站。
    - 本机分支（`:401-416`）一字不动。
    - 删掉 `:394-398` 的 fail-closed 文案。
  - `launchCoordinatorRoundWithExpect`（`:246-281`）向骨架提供卡的转交回调，并新增一个收件变体：带冻结的 squad/carrier（以及 Q1 甲的 expect），在本机的卡锁内重新检查席位占用，再走 `bindLaunchedCoordinator` 原样（含 `adoptSelfSeatedCoordinator`）。
- `internal/agentd/coordapi.go`：注册卡收件路由，包 `withLedger(withCoordinator(...))`，与 `:48` 的 wake 同形；收件 handler 可以放进新文件 `firstround.go`。`handleCoordLaunch` 和 `handleCoordRebind` 的错误映射（`:123-159`、`:265-297`）**不改**——受理端把收件端的 kind 还原成既有的类型化错误，现有 switch 照常命中。
- `internal/agentd/sessioncreate.go`：`launchSessionResident`（`:210-288`）向骨架提供驻场转交回调；新增收件变体，带冻结的 squad/carrier，在本机会话锁内重读会话、归档状态、驻场位，再走 `:240-279` 的闭包原样；`pointResidentLaunchFailure` 的调用（`:283`）改成按 Q3 和回执里的 `pointed` 决定写不写。
- `internal/agentd/ledgerapi.go`：在 `:74` 旁注册驻场收件路由。**不包 `requireConsoleIdentity`**：执行机可能没配 `console_user`（`roomsapi.go:52-61`），而 agentd 之间的信任由 Bearer 令牌承担，与 wake 同口径。
- 测试：新文件 `internal/agentd/b448_*_test.go`；`internal/client` 的出站方法单测。**允许改写的既有测试只有一条**：`TestCoordLaunchRejectsRemoteCarrier`（`coordapi_test.go:651-689`）。它锁定的是被本卡推翻的行为，改写成「远端载体、对端不可达 → 502 点名机器，席位为空，两级计数为 0」，计数断言保留。
- **不改**：`cmd/`、`web/`、`internal/scheduling`、`internal/ledger`、`internal/collab`、`internal/keystone`、`cmd/agentd.go` 的 WriteTimeout。

**架构方向**：只走 agentd → client 出站（既有方向，wake 先例）；不新增 scheduling / ledger / collab 的对外依赖；执行机身份沿用 `scheduling.IsLocalMachine || s.IsSelfTarget`（B441）；目标地址经 `s.clientForTarget`（`server.go:645-664`）解析，不自己拼 URL。

**契约夹具**：新 DTO 只在 agentd 之间流转，Web 不消费，**不进** `web/src/api/testdata`。先例：`CoordinatorWakeReq` 与 `ApproverDecideReq` 都不在 `contract_fixture_test.go` 里。序列化边界由 agentd 的真实 HTTP 测试锁定（§5 V4）。

## 4. 工作顺序

1. **先验承重假设（阻塞后续一切）**：在测试里搭两个 `Server` 共用一个 `*ledger.Store`。A 的 `cfg.Targets={"linux-01": B.ts.URL}`，B 的 `cfg.Name="linux-01"`。用一个 echo 端点证明三件事：
   - A 经 `clientForTarget("linux-01")` 能打到 B；
   - B 上 `s.IsSelfTarget("linux-01")` 为真，A 上为假；
   - 两边 `SetupAutomationForTest` 之后，scheduling 计数读到的是同一份。

   参照 `newNoPTYLedgerEnvWithTargets`（`ledgerapi_test.go:194-217`）。搭不起来就停下回报协调者，不要改用 mock 对端，因为那样 V1 就退化成 `TestB389TransferPostsOnceWithBatchSeqs` 那种假对端。
2. 落 `proto` DTO 和 `client` 出站方法，写 V4（序列化边界），产出 §8 F1–F10 的断言。
3. 改造骨架：两种准入策略加远端分支。**先跑 S4 回归**：`coordapi_test.go`、`b444_*`、`b446` 相关、`scheddrain_test.go` 全部零改且全绿，再继续。
4. S1：卡转交与卡收件，写 V1、V3、V5、V6，改写 `TestCoordLaunchRejectsRemoteCarrier`。若 Q1 取甲，换绑一并做。
5. S2：驻场转交与驻场收件，加指针归属（Q3），写 V2 和 V2 的反例。
6. 超时（Q4）与两端日志，写 V7。
7. 按 implement 三段律跑全量，提交交回物（首节写「偏离与自主决定」）。真机 R1–R8 由协调者执行，**不派发**。

## 5. 验收方式

### 机内断言（执行者）

两个 Server 竖切测试一律走**真实 HTTP**，打 A 的入口端点，fake Runner 只装在 B 上。A 上也装一个 fake Runner，用来断言它零调用。

- **V1（S1 成功）**：载体 X 只在 B。打 A 的 `POST /api/cards/{id}/coordinator/launch`，断言：
  - 200 `woke=true`，`session_id` 等于 B 的 fake Runner 返回的值；
  - B 的 Runner 恰好 1 次 Launch，A 的 Runner 0 次；
  - 账本里 `driver_source=coordinate`、`driver_mode=push`、`seat_bearings.machine` 为 B 的名字、`carrier=X`；
  - 回合结束后 `squad/<队>/X` 和 `carrier/X` 两级计数都是 0；
  - 若 Q1 取甲，换绑同样跑一遍，带 expect。
- **V1 反例（B446 兜底在执行机生效）**：B 的 Runner 在回合里先自坐 pull（参照 `seatStealingRunner`，`coordapi_test.go:691-707`，改成自坐本回合会话）→ 200，席位改写成 coordinate/push。
- **V1 反例（席位被他人占）**：B 回合里被他人抢座 → A 入口返回 409，体里带 `session_id`；needs_human **恰一条**，由 B 写，A 不追加。
- **V2（S2 成功）**：打 A 的 `POST /api/sessions`（带 project/squad）。断言：
  - 响应 `resident.identity=agent:<队>`、`carrier=X`、`carrier_machine` 为 B 的名字；
  - 驻场行 `push_session` 已补写；
  - B 的 Runner 收到一次 `LaunchForResident`；两机计数为 0。
  - 再打 `POST /api/sessions/{id}/resident/launch`（另一场空驻场位的会话），同样断言。
- **V2 反例**：
  - (a) B 回合失败 → A 响应 `resident.error` 非空，驻场位为空，会话里**恰一条**指针行；
  - (b) B 不可达（A 的 target 指向一个已关闭的端口）→ 指针行恰一条，由 A 写，文案点名机器；
  - (c) 会话已有驻场者 → resident/launch 返回 409，零指针行。
- **V3（S6 名额）**：
  - B 侧把 X 的名额占满（`MaxConcurrency=1`，先手动 acquire 一次）→ A 入口返回 409，文案与本机路径相同（「协调者并发已满（小队 …）」，`coordapi.go:144-147`）。
  - 同样条件下走 launch_queue：A 的 `drainQueuesOnce` 对该请求得到 `errors.Is(err, scheduling.ErrNoSlot)==true`，请求被回填（`scheddrain.go:146-152`）。
  - 转交途中断言 A 侧计数为 0：在 B 的 Runner.Launch 里读计数，值为 1 且只来自 B 的那次准入；A 在调出站之前已归还名额。
- **V4（序列化边界，穿过真实编解码）**：
  - (a) 截获 A 发出的原始请求字节（B 前面挂一层透传 handler），断言 JSON 里**一定有** `squad`、`carrier` 两个键，并带 `X-Handoff-Forwarded: 1`。
  - (b) 直接向 B 发原始 JSON：缺 `carrier` 键 → 400，`kind=invalid`，错误信息提到「缺失」；`"carrier":""` → 400，`kind=invalid`，错误信息提到「为空」。这两条是独立断言，分别区分缺失与零值；`squad` 同样两条。
  - (c) 无转交头 → 400 `kind=invalid`。
  - 以上三项均断言零写入、零计数、Runner 零调用。
- **V5（S5 防环）**：B 的设备表或配置里，X 的机器不是 B 自己（让 `IsSelfTarget` 为假），A 认为 X 在 B。断言：
  - A 入口返回 502，文案「转交到本机的协调者首轮，但载体 X 不在本机（设备表不一致？）」；
  - B 零出站：B 的 target 池指向一个计数 handler，调用数为 0；
  - 两机计数为 0。
- **V6（老对端）**：A 的 target 指向一个对新路径回 404 的 handler → A 入口返回 502，文案「<机器> 的 agentd 版本过旧，不支持首轮转交，请升级」，零席位、零计数。驻场路径另断言指针行恰一条。
- **V7（超时与日志）**：
  - 把 `coordWakeTurnTimeout` 测试覆盖成秒级。B 收件 handler 的写截止 ≥ 该值，用一个比 server 级 WriteTimeout 更长的回合证明：测试 server 的 WriteTimeout 设得比回合短，回执仍能完整送达。
  - A 出站 ctx 的截止 ≥ 该值。
  - 两端各有一行转交日志，字段含 `accepted_by`、`handled_by`、`carrier`、`card` 或 `session`，用捕获的 slog handler 断言。
- **S4 回归**：本机载体相关既有测试零改全绿，唯一例外是上面列出的那条改写。

### 运行入口

```
go test ./internal/agentd/ -run 'B448|CoordLaunch|CoordRebind|B444|B446' -count=1
go test ./internal/client/ ./internal/proto/ -count=1
```

之后按 implement 三段律跑全量。已知环境敏感用例见 memory「handoff env-sensitive tests」，不算回归。

### 真机清单（**本节由协调者执行，不派发**）

前置：协调者在 linux-01 的 PG 18 上新建临时测试库和专属账号，并登记到 `credentials.md` 的「已分配」表。凭据不进计划、不进对话。**绝不碰在役 7777，绝不碰生产账本。**

- **R1 靶子搭建**：
  - linux-01 起隔离 agentd：独立端口（例如 7794）、`HOME`、datadir、config，`name=t448-linux`（Q5 甲）。
  - mac-mini 起隔离 agentd：独立端口、`HOME`、datadir、config，`name=t448-mac`。
  - 两者的 ledger DSN 都指向该测试库。
  - 设备表登记两台，地址用 Tailscale：linux-01 是 `100.81.114.119:<port>`。
  - 载体 `t448-oc`：machine 是 `t448-linux`，CLI opencode，模型 `opencode/deepseek-v4.1-flash-free`，HOME 用隔离目录。
  - 协调小队 `t448-coord` 的成员只有 `t448-oc`。
  - 证据：两端 `handoff status`；`squad list`。
- **R2 S1**：在 mac-mini 隔离实例的控制台（SSH 转发或本机浏览器）对一张测试卡点「叫机器人」。断言：
  1. 返回成功；
  2. 卡显示 push 席位，承载机器为 `t448-linux`；
  3. linux-01 实例的日志有「执行机」转交行，mac-mini 有「受理机」转交行，两行能按卡号对上；
  4. 再给卡打一条评论或镜像事件，续轮在 linux-01 执行，闭环；
  5. 回合结束后测试库里 `squad/t448-coord/t448-oc` 和 `carrier/t448-oc` 计数为 0。
- **R3 S2**：在 mac-mini 控制台项目行建会话（带一张卡）。断言：会话立即出现；驻场者显示 `push · t448-oc@t448-linux`；首轮发言可见；驻场行 `carrier_machine=t448-linux`。
- **R4 S2 失败**：停掉 linux-01 隔离实例，在 mac-mini 建会话。断言：`resident.error` 点名 `t448-linux`；会话里指针行恰一条；会话和卡都保留；恢复实例后 `resident launch` 重试成功。
- **R5 S3**：在 mac-mini 用 CLI（`--target` 指向隔离实例）跑 `card coordinate`、`session create --squad`、`session resident launch`，三条都成功。
- **R6 S6**：把 `t448-oc` 的 `max_concurrency` 设为 1，并行发两次叫机器人（两张卡）。断言一个成功、一个 409「协调者并发已满」，结束后计数为 0。
- **R7 执行机重启**：一次驻场者首轮在途时重启 linux-01 隔离实例（不是 7777）。断言：启动清扫请下在途行，并落「被重启打断」指针行；mac-mini 侧报错。
- **R8 防环与老版本**：
  - 把 mac-mini 隔离实例换成基线 `b9e68472` 构建，让它当执行机（载体改登记到 `t448-mac`）。从 linux-01 叫机器人，得到「版本过旧」文案。
  - 构造设备表不一致：linux-01 实例 config 的 name 临时改掉，再从 mac-mini 叫机器人，得到防环文案，且 linux-01 无出站。
- **R9 PG 腿（顺带清 B444 残余）**：在同一测试库对应的 DSN 下跑：
  ```
  LEDGER_TEST_PG_DSN=<测试库> go test ./internal/ledger/ -run 'TestB444|TestPGOpen' -count=1 -v
  LEDGER_TEST_PG_DSN=<测试库> go test ./internal/agentd/ -run PG -count=1
  ```
  PG 用例会建表或改 schema，必须用测试库，不得用生产库。
- **R10 长回合传输（边界型，未验证）**：让 linux-01 的载体首轮超过 11 分钟（简报里要求 sleep），从 mac-mini 叫机器人，验证 Tailscale 直连（或 relay，看设备表登记方式）下回执能送达。失败时记录现象：是写超时还是隧道空闲断开。
- **R11 生产复核**：通过后上 fleet，从 mac-mini 控制台叫机器人，载体在 macbook-pro。按 memory「Real-machine tests use isolated agentd」，先报告回滚方式再动在役服务。
- 收尾：停隔离实例；删测试库或保留（由协调者记账）；证据（日志片段、截图、SQL 读数）存进台账。

## 6. 决策权限

**执行者可自行决定**（交回物「偏离与自主决定」逐条对照本表）：

- 私有函数与文件拆分：新建 `firstround.go`，骨架用回调还是用策略结构体传准入方式。
- DTO 的 Go 类型名。**JSON 键名、路径、kind 词表、状态码以 §8 为准，不可改。**
- 区分缺失与空的实现手法：指针字段、`json.RawMessage` 或两遍解码，任选。
- 写截止与出站截止的余量（≥ `coordWakeTurnTimeout` 即可）。
- 日志措辞与级别。转交行必须带 §5 V7 列的字段，级别要保证缺省日志级别下看得见（参照 `wakeRoundLogLevel`，`server.go:1542-1545`）。
- 收件端是先读载体判定本机再准入，还是先准入再判定（前提是 not_local 时零计数）。

**必须升级给协调者**：

- 任何 §8 冻结条目的改动。
- 改动 `cmd/`、`web/`、scheduling / ledger / collab / keystone 包。
- 改动三个入口端点的状态码或文案。spec「实现决定」规定的三条新文案除外，它们是必改项。
- 除 `TestCoordLaunchRejectsRemoteCarrier` 之外，删除或改写任何既有测试的断言。
- 改动 `cmd/agentd.go` 的 server 级超时。
- 发现执行机还需要受理机提供请求里没有的信息（例如 workdir 必须由受理机给）。
- 承重假设（§4 第 1 步）不成立。
- 发现 spec 的某条故事在现有门面下做不到。

## 7. 缺陷族风险（与本卡相关的）

| 风险 | 在本卡的具体形态 | 验收责任 |
|---|---|---|
| 名额泄漏（任一侧） | 受理端在 `defer` 里才归还，转交 30 分钟期间一直占着一份，对端还要再占一份；执行端 not_local 或 invalid 早退时漏了归还 | 受理端出站前显式 `Release`（V3 在回合中读计数）；收件端每条早退路径计数为 0（V4、V5） |
| 重试导致双重执行 | 调用方超时后人重试，而第一次仍在 B 上跑（Runner 不读 ctx，`keystone.go:221-229`） | B 的进程内锁（`coordinatorLock`，`server.go:458`）串行同卡和同会话；收件端在锁内重查占用；卡侧 `BindSeatMode` CAS、驻场侧 `ErrResidentOccupied` 由账本兜底（V2c）；出站只发一次（F25） |
| 成环 | 两机设备表不一致导致 A→B→A | 收件端判定非本机即拒，不出站（V5、F26） |
| 版本偏斜 | 老执行机不认新路径；老受理机根本不转交（仍是旧的 fail-closed，可以接受） | 404 → 版本过旧（V6、R8）；新端点业务上绝不回 404（F20） |
| 部分失败：远端已落座但回执丢失 | B 已落席位或驻场行，A 收到中断，向人报 502 | 文案说明「可能仍在该机执行或已完成，请刷新查看」；驻场侧按 Q3 指针归属避免误导性的「未能拉起」；结果找回落 roadmap |
| 锁作用域跨机 | 进程内锁只在本进程有效；A 和 B 各持各的。两台不同受理机对同一张卡的并发，如果选到不同机器上的载体，不受任何锁约束 | 账本 CAS 兜底：第二个人拿到 409 加 needs_human（与今天两台机各自本机拉起的行为同族，不新增风险类）；A 持锁跨越转交不会死锁，因为 B 不回转 |
| 超时不匹配 | server 级 WriteTimeout 11 分钟 < 回合 30 分钟；client 无全局超时（`client.go:327-329`），超时全靠 ctx | Q4 甲；V7；R10 |
| 驻场指针重复或缺失 | A 和 B 都写指针行，或都不写 | 回执 `pointed` 字段 + Q3 规则；V2a、V2b 断言恰一条 |
| 执行机缺控制台身份 | 收件端如果包了 `requireConsoleIdentity`，没配 `console_user` 的 linux-01 就会 403 | 收件路由不包该守卫（§3）；R3 用 linux-01 无 `console_user` 的配置跑 |
| 启动清扫归属 | 驻场行 `carrier_machine` 若写成受理机的名字，B 重启时不会清扫它 | 由 B 写（`residentCarrierMachine` 在 B 执行）；V2 断言 `carrier_machine=B`；R7 |

**观测项（不在本卡范围，落 roadmap）**：续轮收件端 `handleCoordWake`（`coordapi.go:172-232`）不检查 `isForwarded`。如果 B 的账本承载显示另一台机，`wakeCoordinatorRoundRaw` 会再转交一跳（`scheddrain.go:557-570`）。共享账本下承载只有一份，概率低，但这与本卡给首轮加的防环不对称。

## 8. 需冻结的外部承诺

**入口判据成立**：本卡新增 agentd↔agentd 的跨进程 wire。混版本的两台 agentd 会互相调用，所以下列条目冻结。producer 和 consumer 已标注；「受理端」= 发起转交的 agentd（经 `internal/client`），「收件端」= 载体机 agentd 的新 handler。每条是一个独立的 pass/fail 断言。

**端点与头**

- **F1**（producer 受理端 / consumer 收件端）：卡转交 = `POST /api/cards/{id}/coordinator/first-round`；新版收件端对合法请求不回 404。
- **F2**（同上）：驻场转交 = `POST /api/sessions/{id}/resident/first-round`；新版收件端对合法请求不回 404。（若 Q2 取乙，F1、F2 合并重写。）
- **F3**（producer 受理端）：每个转交请求都带 `X-Handoff-Forwarded: 1`。
- **F4**（consumer 收件端）：不带该头的请求 → 400 `kind=invalid`，零写入、零计数、零回合。
- **F5**（consumer 收件端）：鉴权同其它 `/api/*`（Bearer）；不要求控制台身份。

**请求体** `{"squad","carrier","accepted_by"?,"expect"?}`

- **F6**（producer）：`squad` 键恒在（无 omitempty）。
- **F7**（producer）：`carrier` 键恒在（无 omitempty）。
- **F8**（consumer）：缺 `squad` 键 → 400 `kind=invalid`。
- **F9**（consumer）：`squad` 为空串 → 400 `kind=invalid`，错误信息与「缺失」不同。
- **F10**（consumer）：缺 `carrier` 键 → 400 `kind=invalid`。
- **F11**（consumer）：`carrier` 为空串 → 400 `kind=invalid`，错误信息与「缺失」不同。
- **F12**（consumer）：`accepted_by` 可缺，只进日志；缺失不影响执行。
- **F13**（Q1 甲才有；producer 卡换绑）：换绑请求带 `expect`，值为非空的旧席位身份；首轮请求不带该键。
- **F14**（Q1 甲才有；consumer）：带 `expect` 时以 `RebindSeatMode(expect)` 落座；不带时以 `BindSeatMode` 落座。
- **F15**（consumer）：收件端用请求里的 `squad`/`carrier` 做 `AdmitSeatCarrier`，不调 `LaunchAdmit`，也不重新选载体。

**成功回执**

- **F16**（producer 收件端，卡）：200 的体是 `CoordinatorLaunchResp` 的全部既有键（`woke` 恒在）加 `handled_by`（执行机名，非空）。
- **F17**（producer 收件端，驻场）：200 的体是 `SessionResidentOutcome` 的既有键（`identity`、`carrier`、`carrier_machine` 非空）加 `handled_by`。
- **F18**（consumer 受理端）：卡入口 200 体与本机路径同形（`woke`、`session_id`），不外泄 `handled_by`；驻场入口的 `resident` 同形。

**错误回执**：体统一为 `{"error":string,"kind":string,"session_id"?:string,"pointed"?:bool}`。

- **F19**（consumer 受理端）：未知 `kind` 或无 `kind`（例如 503 未装配）→ 入口 502，原文透出。
- **F20**（producer 收件端）：新端点在业务上绝不回 404；卡不存在 → 409 `kind=not_found`；会话不存在 → 409 `kind=no_room`（即 F26，受理端还原为 `collab.ErrNoRoom` → 404，与本机路径 `sessionsapi.go` 一致）。〔修订 r1，2026-10-20：独立 review I-3 澄清，原文「卡或会话不存在 → not_found」与 F26 重叠；协调者裁决取 no_room。〕
- **F21**（consumer 受理端）：对端回 404 → `ErrFirstRoundUnsupported` → 入口 502，文案「<机器> 的 agentd 版本过旧，不支持首轮转交，请升级」。
- **F22**：`kind=no_slot`，状态 409 → 受理端错误满足 `errors.Is(err, scheduling.ErrNoSlot)`，并且 `errors.As(*coordinatorAdmissionError)` 成立。
- **F23**：`kind=admission_rejected`，状态 400 → 受理端为非 NoSlot 的 `*coordinatorAdmissionError` → 入口 400。
- **F24**：`kind=cas_conflict`，状态 409 → 受理端错误满足 `errors.Is(err, ledger.ErrCASConflict)`。
- **F25**：`kind=seat_conflict`，状态 409，带非空 `session_id` → 受理端为 `*coordinatorSeatConflict` → 入口 409，体里带 `session_id`；needs_human 由收件端写，受理端不写。
- **F26**：`kind=resident_occupied` / `read_only` / `no_room`，状态均为 409 → 受理端分别满足 `errors.Is(collab.ErrResidentOccupied / ErrReadOnly / ErrNoRoom)`。
- **F27**：`kind=not_local`，状态 409 → 受理端入口 502，文案为防环文案；收件端零出站、零计数。
- **F28**：`kind=round_failed`，状态 502 → 受理端入口 502，透出收件端原因。
- **F29**：`kind=invalid`，状态 400 → 受理端入口 502。受理端发出不成形的请求是受理端自己的缺陷，不能伪装成用户的请求错误。

**行为承诺**

- **F30**（producer 收件端，驻场）：`pointed=true` 当且仅当收件端已在该会话落了失败指针行。
- **F31**（consumer 受理端，驻场）：收到 HTTP 回执时，`pointed` 为假才由受理端写指针行；没收到回执时按 Q3 规则处理（驻场位为空才写）。
- **F32**（producer 受理端）：调出站之前，本次 `LaunchAdmit` 的名额已经归还。
- **F33**（consumer 收件端）：冻结准入占的名额在 handler 返回前归还，所有出口都如此。
- **F34**（producer 受理端）：出站 ctx 的截止 ≥ `coordWakeTurnTimeout`。
- **F35**（producer 收件端）：回执的写截止 ≥ `coordWakeTurnTimeout`，覆盖 server 级 11 分钟。
- **F36**（producer 受理端）：每次拉起尝试只发一次转交，失败不自动重试。
- **F37**（consumer 收件端）：载体不在本机（`!IsLocalMachine && !IsSelfTarget`）时绝不发起任何出站请求。

**依赖库既成行为（已核，见核实读数）**：

- `client.do` 不设全局超时，靠 ctx（`client.go:327-329,426-470`）。
- ctx 取消或超时**不**归为 `ErrUnreachable`（`client.go:452-456`）。
- 读写中断归为 `ErrUnreachable`；走 relay 时归为 `ErrTunnelDisconnected`（`client.go:457-467`）。
- `httpError` 截取 4096 字节错误体（`client.go:481-497`）。中文错误体超长会被截断，kind 解析要先于截断，或者确保错误体短于上限。
- Go 1.26 的 `http.Server.WriteTimeout` 在 handler 执行前设定截止，`cmd/agentd.go:514-519` 的注释记载了这一实测。

**拍板记录候选**（满足三重闸门）：

- **Q3**：反过来写（受理端一律写指针，或一律不写），不会有任何既有测试变红，但会造成重复或缺失的指针行。
- **F20**（新端点业务上禁回 404）：反过来写，测试不会变红，但老对端会被误判成「卡不存在」，或者卡不存在会被误判成「版本过旧」。
- **F29**（invalid 映射成 502 而不是 400）：反过来写同样不会变红，却把受理端自己的缺陷伪装成用户的错误。

拍板后在本节补记被否方案。

---

## 核实读数（出稿时在基线 `b9e68472` 上核对）

- `runOnCoordinatorCarrier`：`internal/agentd/scheddrain.go:375-417`。`LaunchAdmit` 在 `:378`；`defer s.releaseSchedulingBinding(releaseKey, binding)` 在 `:384`（**回合结束才归还**）；远端 fail-closed 判据 `!scheduling.IsLocalMachine(carrier.Machine) && !s.IsSelfTarget(carrier.Machine)` 在 `:394`，文案在 `:395-396`；spec 组装与规范化在 `:401-414`；`launch(...)` 在 `:416`。
- `launchCoordinatorRound` / `ForRebind` / `WithExpect`：`scheddrain.go:238-281`。卡锁 `coordinatorLock(card)` 在 `:251`；席位占用判定在 `:258-268`；`resolveCoordinatorSquad` 在 `:269`；调骨架在 `:273`。锁覆盖整个骨架，含回合。
- `bindLaunchedCoordinator`：`scheddrain.go:283-329`。`LaunchForCard` 在 `:288`；`BindSeatMode` / `RebindSeatMode` 加 `coordinatorBearing`；撞席时走 `adoptSelfSeatedCoordinator`（`:313`）；仍失败则 `MarkNeedsHuman`（`:321`），并返回 `coordinatorSeatConflict`。
- `adoptSelfSeatedCoordinator`：`scheddrain.go:335-356`，以本回合身份作 expect，`RebindSeatMode` 成 coordinate/push。
- `coordinatorBearing`：`scheddrain.go:676-684`，`Machine = carrier.Machine`（登记名）。
- launch_queue 出队调 `launchCoordinatorRound(ctx, req.Card, "coordinate")`：`scheddrain.go:147`；`errors.Is(launchErr, scheduling.ErrNoSlot)` 时延后回填，在 `:148-152`。ctx 是自动化循环的 ctx，没有超时。
- `transferCoordinatorWake`：`scheddrain.go:434-465`。经 `clientForTarget(machine)` 取客户端，`target.MarkForwarded().CoordinatorWake(ctx, …)` 在 `:445`，ctx 透传调用方，无额外超时。
- 续轮：远端承载判定在 `scheddrain.go:557`，转交在 `:569`；本机分支 `AdmitSeatCarrier(squad.Name, bearing.Carrier)` 在 `:577`，`defer` 归还在 `:582`。这是冻结准入加执行机持有名额的先例。
- `releaseSchedulingBinding`：`scheddrain.go:730-744`。`Release` 后调 `kickAutomation`；失败只记日志。
- 错误类型：`coordinatorAdmissionError`（`scheddrain.go:28-37`，有 `Unwrap`）、`coordinatorLookupError`（`:39-45`）、`coordinatorSeatConflict`（`:47-51`）。
- `handleCoordLaunch`：`internal/agentd/coordapi.go:101-166`，错误映射在 `:123-159`：seatErr → 409 带 `session_id`；`ErrCASConflict` → 409；无小队 → 400；歧义 → 409；`ErrNoSlot` → 409「协调者并发已满」（`:144-147`）；其它准入错误 → 400；默认 → 502。
- `handleCoordWake`：`coordapi.go:172-232`。只校验 seat 与 events；`card.DriverSession != req.Seat` 时回 409（`:189`）；执行失败回 502；回执带 `HandledBy: s.localMachineName()`。**没有 `isForwarded` 检查。**路由在 `:48`，包 `withLedger(withCoordinator(...))`。
- `handleCoordRebind`：`coordapi.go:235-300`，同一套映射，调 `launchCoordinatorRoundForRebind`（`:265`）。
- `launchSessionResident`：`internal/agentd/sessioncreate.go:210-288`。
  - 会话锁 `coordinatorLock(residentClaimKey(sessionID))` 在 `:216`；锁内读会话、归档状态、驻场位，已有则返回 `ErrResidentOccupied`。
  - 调骨架在 `:240`：闭包里依次 `residentCarrierMachine` → `SitResident(push)`（`:249`）→ `LaunchForResident`（`:260`）→ `SetResidentPushSession`（`:269`）；失败时 `ForgetResident` 加 `standInflightResident`。
  - 骨架返回任何错误都会 `pointResidentLaunchFailure`（`:283`）。
  - 调用方：`createProjectSession`（`:187`，失败进 `resident.error`）和 `handleSessionResidentLaunch`（`sessionsapi.go:190`）。
- `handleSessionResidentLaunch`：`sessionsapi.go:170-207`。先 `requireConsoleIdentity`（`:171`）；映射：lookup → 400，`ErrNoRoom` → 404，`ErrReadOnly` / `ErrResidentOccupied` / 准入错误 → 409，默认 → 502。路由在 `ledgerapi.go:74`；`POST /api/sessions` 在 `ledgerapi.go:64`。
- `requireConsoleIdentity`：`roomsapi.go:52-61`，未配 `console_user` 时回 403（fail-closed）。
- 启动清扫：`sweepInterruptedResidents` 在 `sessioncreate.go:331-356`，只处置 `carrier_machine` 是本机的行（`:344`），用 `TryLock` 与在途拉起互斥（`:361`）；指针行由 `pointResidentLaunchFailure`（`:392-401`）经 `rooms.Pointer` 写。
- `collab.Service.SitResident`：`internal/collab/sessions.go:296-321`。已有驻场者时由账本层返回 `ErrResidentOccupied`（`sessions.go:282`；PG 用例见 `ledger/b435_resident_write_pg_test.go:26`），可跨机兜底。
- 防环头：`forwardedHeader = "X-Handoff-Forwarded"` 与 `isForwarded` 在 `internal/agentd/forward.go:35,43`；客户端 `MarkForwarded` 在 `internal/client/client.go:366-375`。
- `clientForTarget`：`internal/agentd/server.go:645-664`。规范名为空则走本机直连，否则走 `s.pool.For(canonical)`。`IsSelfTarget`：`server.go:554-567`（自名，或配置 target 指向本机 listen）。
- `scheduling.IsLocalMachine`：`internal/scheduling/scheduling.go:216-223`（空、local、本机、hostname）。
- `AdmitSeatCarrier`：`scheduling.go:787-841`。校验 squad 存在且 `Role == RoleCoordinator`（否则 `ErrRoleMismatch`）、carrier 是小队成员（否则 `ErrRoleMismatch`）、载体 `Status == StatusOnline`（否则 `ErrNoHealthy`）；再走与 `LaunchAdmit` 相同的 `acquire`（两级计数，成员 `MaxConcurrency` 与载体 `MaxConcurrency`），满员时 `errMemberFull` 转成 `ErrNoSlot`。
  - `LaunchAdmit`（`:844-876`）经 `admitInto`（`:1054` 起）遍历成员，跳过非在线载体，全满给 `ErrNoSlot`，无在线成员给 `ErrNoHealthy`。
  - 两者的角色、成员策略、载体上限口径相同；差别在于冻结准入针对单个载体：离线即 `ErrNoHealthy`，非成员即 `ErrRoleMismatch`。
  - `Release`：`scheduling.go:879-906`，幂等，两级计数减一。
- `client.CoordinatorWake`：`internal/client/coordinator.go:69-88`；`ErrApproverUnsupported` 的 404 哨兵先例在 `coordinator.go:18-48`；`CoordinatorLaunch` 在 `client/squads.go:80-95`；`SessionResidentLaunch` 在 `client/sessions.go:52-67`；`decodeWire` 在 `client/squads.go:22-31`（普通 `json.Unmarshal`，不拒未知字段）。
- `client.do`：`client.go:426-470`。ctx 取消或超时原样返回，其它无响应的失败包成 `ErrUnreachable`，relay 时为 `ErrTunnelDisconnected`。`httpError` 在 `client.go:481-497`，截取 4096 字节。共享 `http.Client` 的 `Timeout` 恒为 0（`client.go:327-329,345-347`）。
- server 级超时：`cmd/agentd.go:526-534`，`WriteTimeout = workspace.RunCmdTimeout + 1min`，`RunCmdTimeout` 为 10 分钟（`internal/workspace/gitworkspace.go:107`）。回合上界 `coordWakeTurnTimeout = hostapi.DefaultTurnTimeout`（`server.go:1539`），值为 30 分钟（`internal/hostapi/driver.go:47`）；`readyCtx` 在 `server.go:1550`。
- Runner 不读 ctx：`keystone.LaunchForCard` 收 ctx 但不使用（`internal/keystone/keystone.go:221-229`）；`LaunchForResident` 同样（`keystone/resident.go:61-70`）；接口是 `Launch(spec, prompt)`（`internal/keysclient/keysclient.go`）。所以调用方断开后回合照跑。
- `coordinatorLock`：`server.go:458-469`，进程内 map，按卡号或 `resident:<会话>` 分锁。
- DTO：`CoordinatorLaunchResp` 在 `internal/proto/scheduling.go:115-121`（`woke` 无 omitempty）；`CoordinatorWakeReq/Resp` 在 `proto/ledger.go:137-153`；`SessionResidentOutcome` 在 `proto/sessions.go:257-262`；`SessionResidentLaunchReq` 在 `proto/sessions.go:281-283`。
- 契约夹具：`internal/proto/contract_fixture_test.go` 生成 `web/src/api/testdata`；`CoordinatorWakeReq`、`ApproverDecideReq` **不在**夹具里（agentd 之间 DTO 的先例）。
- 既有测试：
  - `TestCoordLaunchRejectsRemoteCarrier` 在 `coordapi_test.go:651-689`，锁定远端载体被拒，本卡要改写；
  - `TestB389TransferPostsOnceWithBatchSeqs` 在 `scheddrain_test.go:476-535`，用假对端加 `newNoPTYLedgerEnvWithTargets`，是转交测试的先例；
  - `seatStealingRunner` 在 `coordapi_test.go:691-707`；
  - 测试 env 构造在 `ledgerapi_test.go:194-217`。
- PG 门控：`LEDGER_TEST_PG_DSN`。用例有 `TestB444ResidentPushMigrationPG`、`TestB444SessionHomeProjectMigrationPG`、`TestB444ResidentSeatExclusionPG`（`internal/ledger/b444_*`），以及 `TestPGOpenSkipsSchemaWhenVersionCurrent`、`TestPGOpenFreshSchemaWritesMarker`（`ledger/schema_version_pg_test.go`）；agentd 侧有 `unlinked_summary_test.go:453`。
- CLI：`card coordinate` 调 `cl.CoordinatorLaunch(cmd.Context(), …)`（`cmd/card.go:506`）；`session resident launch` 调 `cl.SessionResidentLaunch`（`cmd/session.go:792`）。都只打本机 agentd。
- roadmap 残余条目：`docs/roadmap.md:951`（「建会话 / 叫机器人只接受本机载体」），finish 时销账。
- Go 版本：`go.mod` 为 `go 1.26.1`，可用 `http.NewResponseController`（Go 1.20+）。仓内目前没有使用先例。
- **未验证，需真机**：
  - Tailscale 直连或 relay 隧道能否让一个静默 11–30 分钟的 HTTP 请求存活（R10）；
  - mac-mini 与 linux-01 在测试设备表里走直连还是 relay，取决于登记方式（R1 记录）。
