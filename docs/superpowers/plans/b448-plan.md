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

---

## 增补 r1：S7 名额持有机（已拍板 2026-10-20）

> **拍板（协调者，2026-10-20）**：Q-r1-1 甲（`StartAutomation` 先同步对账再起循环，失败中止启动；结构保证、T9 锁顺序）；Q-r1-2 甲（按键扣 `min(max(0, holders[self]−owned[key]), count)`，保留跨重启仍存活的执行者任务名额）；Q-r1-3 甲（`cfg.Name`，空则 `os.Hostname()`，与镜像 holder 同口径）；Q-r1-4 甲（仅解码失败中止，写失败记 Error 跳过该键）；Q-r1-5 甲（观察面不暴露 holders，wire/web 零改）。spec 方案段那句前提已同批修订为「扣除本机名下、且不属于本机仍存活任务的占用」。

- 状态：**待拍板**。出稿者是 plan subagent，拍板者是主会话协调者。本节只做增补，不改 §0–§8（前一轮已交付）。
- 输入：`docs/superpowers/specs/b448.md` 末节「修订 r1」，2026-10-20 已批准。内容是新故事 S7，以及 S6 失败子句的改写。
- 基线：分支 `cards/B448-charter-1` @ `30b21c5d`。本节引用的代码事实见末尾「r1 核实读数」。

### r1 待拍板

> 每条给推荐项（**荐**）和理由。Q-r1-1 与 Q-r1-2 推翻了 spec 方案段的一句前提：「启动时本进程尚无任何在途回合，故本机名下的全部占用都是上一进程遗留」。这句话在基线代码里有两处不成立。拍板后，spec 那句措辞要不要同批修订，由协调者决定。

1. **Q-r1-1 对账必须先于本进程的第一次准入。**
   - 现状：`setupLedger` 在 `cmd/agentd.go:758` 调 `srv.StartAutomation(ctx)`，自动化循环 goroutine 立即开跑首轮。首轮里，事件唤醒走 `AdmitSeatCarrier`，清队走 `LaunchAdmit` / `Select`。启动对账却更晚，在 `cmd/agentd.go:360` 经 `srv.RecoverOnStartup` 调到 `server.go:1387`。
   - 后果：对账跑之前，本进程可能已经以本机名准入了新回合。如果按 spec 扣掉「本机名下全部占用」，会把这个活着的回合也扣掉；它日后归还时再减一次，计数偏低，导致超发。
   - HTTP 不受影响：真实 handler 在 `bootGate.Ready`（`cmd/agentd.go:401`）之后才上线，晚于两者。
   - **荐 甲**：把对账做成 `StartAutomation` 的同步前置。
     - `StartAutomation` 在 `automationStartOnce` 内先同步跑对账，成功后才起循环 goroutine；签名改为返回 error。解码失败时 `setupLedger` 返回错误，走 `abortBoot`。
     - `Server.RecoverOnStartup` 不再调对账，只做 executor 恢复。
     - 理由：启动期唯一的准入者就是自动化循环。把对账钉在它的入口上，顺序由结构保证，不依赖 cmd 里两行调用的先后，而且能写单测（T9）。
     - 提前对账是安全的：对账读的是本机非终态任务集，`RecoverOnStartup` 只把任务迁到 waiting_review、不迁终态（`watchdog.go:596-602`），不会改变这个集合。
   - 乙：只调 cmd 层顺序，`setupLedger` 在 `StartAutomation` 前显式调 `srv.ReconcileOccupancy()`。改动更小，但顺序只靠 cmd 两行维持，没有测试能锁住。
   - 丙：持有者名带启动纪元（`<机器名>#<boot id>`），对账扣「本机名、非本纪元」的占用，完全不依赖顺序。代价是 executor 任务跨重启存活时，要把它的持有从旧纪元迁到新纪元，否则它日后 Release 时扣不到自己名下。复杂度明显更高，也偏离 spec「记机器名」。不荐。
2. **Q-r1-2 跨重启仍存活的 executor 任务，名额要保留。**
   - 现状：执行者任务在 agentd 重启后不会终结。`RecoverOnStartup` 探活成功就 resume；探活失败也只迁到 waiting_review。名额要等协调者 `done` / `stop` 时，经 `releaseTaskCarrierOccupancy` 才归还（`handlers.go:933-937,982-986,993-1025`）。
   - 这些名额的持有者就是本机，因为准入发生在本机 `handleDispatch`（见承重事实 2）。按 spec 字面扣掉「本机名下全部」，会误扣它们；之后 `done` 再减一次，同样计数偏低、超发。
   - **荐 甲**：按键扣减。
     - `owned[key]` = 本机任务表里非终态、带 Carrier 的任务，按 `OccupancyKeys(task.Squad, task.Carrier)` 计数。是计数，不是今天的集合。
     - 设 `h = holders[self]`，扣减量 `d = min(max(0, h − owned[key]), count)`；写回 `count −= d`，`holders[self] = min(h, owned[key])`。
     - `owned` 用的键与 `releaseTaskCarrierOccupancy` 归还时用的键同源（都取 `task.Squad` / `task.Carrier`），扣与还对称，不会重复扣。
     - 旧版本准入的任务不在 holders 里，会让 `owned` 偏大、扣得偏少。这是安全方向。
   - 乙：按 spec 字面扣光 `holders[self]`。会误扣存活任务，否。
3. **Q-r1-3 持有者名的来源。**
   - **荐 甲**：用 `Service.selfName`，即 `cfg.Name`（`cmd/agentd.go:753` 注入）；自名为空时回退 `os.Hostname()`。
     - 这与账本镜像 holder 的回退规则同口径（`cmd/agentd.go:772-775`）。
     - 持有者记的是「哪个 agentd 进程族写的」，与载体登记的 machine 别名无关，不受 local / 本机 / hostname 等别名形态影响。
     - 自名为空只出现在无 DSN 的单机账本，那里 `sched_running` 本来就只有本机写，hostname 足够。
   - 乙：自名为空时不记持有，退化成今天的「无主、保留」。单机重启后照样泄漏，S7 在单机上不成立。
4. **Q-r1-4 对账写失败（CAS 重试耗尽、Put 报错）时怎么办。**
   - **荐 甲**：只有解码失败阻断启动（沿用现状，带 key / version）。CAS 耗尽或写错误时，记一条 Error 日志（带 key / version / 拟扣数 / cause），跳过这把键继续启动，遗留留到下次重启。理由：写失败的后果就是今天的现状（泄漏保留），为一次瞬态争用把整个 agentd 挡在门外，代价大于收益。
   - 乙：任何失败都阻断启动。
5. **Q-r1-5 观察面要不要暴露 holders。** 现状：`GET /api/squads` 的 `running` 视图只有 key 与 count（`proto/scheduling.go:50-60`、`web/src/api/scheduling.ts:38-39`）。
   - **荐 甲**：不暴露。观察面 wire、契约夹具、前端都不改。理由：spec 没要求；持有分布只服务对账；少一个跨语言投影面。
   - 乙：`RegistryRunningView` 加 `holders`，前端展示。要走契约夹具和 web 投影，属于范围外扩。

### r1.0 交付目标

本轮交付 S7，并完成 S6 失败子句的改写。

- **要达成的结果**：
  - 回合中途被杀的 agentd 重启后，本机遗留的名额归还，计数回到真实在途数；
  - 他机名下的占用不动；
  - 旧格式计数（无持有分布）原样保留；
  - 计数记录损坏时，启动报错并带 key / version。
- **覆盖范围**：全部准入路径，即卡首轮、驻场首轮与续轮、转交收件端、执行者任务派发。它们写计数都经过 `scheduling` 的同一组写者，所以一并覆盖。
- **留待后续**（spec 已列 Out of Scope）：机器永久下线后其名下占用的清理；孤儿 CLI 子进程的回收。

### r1.1 触及子系统与类型

| 子系统 | 类型 | 本轮责任 |
|---|---|---|
| `internal/scheduling`（调度领域） | 逻辑型 | 计数 body 增加持有分布；`casCount` / `stepRunning` / `readCount` 保留并维护 holders；新增本机遗留归还方法，扣减规则只在这里定义 |
| `internal/orchestration`（启动编排） | 逻辑型 | `ReconcileSchedRunning` 从只读改为「算 owned 计数，再调调度域归还」；删掉它自带的 body 解码副本 |
| `internal/agentd`（组装与生命周期） | 逻辑型 | `SchedulingClient` 增加归还方法；按 Q-r1-1 甲，`StartAutomation` 先对账后起循环并返回 error，`RecoverOnStartup` 不再对账 |
| `cmd/agentd.go`（组装点） | 逻辑型 | 处理 `StartAutomation` 返回的 error：失败时 `setupLedger` 返回错误 |
| 共享账本 `registry` 表（`sched_running` 行） | 边界型 | 多版本 agentd 跨机共读共写同一 body；机内只验形状与混版本金样本，行为验收见 r1.5 真机清单 |

不扇出，单工作单元。

### r1.2 故事映射与行为闭环

| 故事 | 实现责任 | 真实消费者 | 集成/回归负责人 |
|---|---|---|---|
| S7 本机遗留归还 | scheduling（规则）+ orchestration（owned）+ agentd（时序） | agentd 启动期的自动化循环，以及之后的一切准入 | 执行者（机内 T1–T10）；协调者（R7′） |
| S6 失败子句（改写） | 同上 | 同上 | 同上 |

行为闭环（触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任）：

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 实现责任 |
|---|---|---|---|---|
| 本机任一准入成功 | `sched_running/<key>` body：同一次 CAS 里 `count+1`、`holders[self]+1` | 本机下次启动对账 | 重启前已死回合的名额被扣回 | scheduling `acquire` / `AdmitCarrier` / `casCount` |
| 本机回合结束归还 | 同一行：`count−1`，`holders[self]−1`（下限 0，归零删键） | 后续准入（读 count） | 名额可再用 | scheduling `Release` / `stepRunning` |
| agentd 被杀后重启 | 本机任务表 + 本行 holders | 对账（先于自动化首轮） | 本机键的 `count` 降为真实在途数；`GET /api/squads` 的 running 读数下降；下一次叫机器人不再 409 | scheduling 归还方法 + orchestration owned + agentd 启动序 |
| 他机 B 准入，或旧二进制写入 | 本行 `holders[B]`，或 holders 被旧写者整体丢弃 | A 的启动对账 | B 的条目与无主部分原样保留；`d` 只来自 `holders[A]` | scheduling 归还方法 |

### r1.3 改动边界

**只改**以下文件及其所在包的测试：

- `internal/scheduling/scheduling.go`：计数 body 类型；`readCount` / `casCount` / `stepRunning`；`AdmitCarrier` 的计数写；新增归还方法。
- `internal/scheduling/registry_read.go`：`RunningCounts` 改用同一 body 类型解码，对外仍只投影 count。
- `internal/orchestration/watchdog.go`：`ReconcileSchedRunning` 及其注释。
- `internal/agentd/scheduling_client.go`：接口加一个方法。
- `internal/agentd/scheddrain.go`：`StartAutomation`。
- `internal/agentd/server.go`：`RecoverOnStartup`。
- `cmd/agentd.go`：处理 `StartAutomation` 的 error。

**架构方向**：

- 扣减规则只在 scheduling 定义（spec 落位①）。
- orchestration 只负责从本机任务表算 `owned`，经使用方定义的窄接口交给调度域。接口形如 `interface{ ReclaimLocalOccupancy(owned map[string]int) (Report, error) }`，命名由执行者定。
- orchestration 不再直接解码或改写 `sched_running`。今天 `watchdog.go:451-455` 有一份独立的 body 解码副本，本轮删除。
- 不新增 DDL，不升 `ledgerSchemaVersion`（spec 已定）。

**不改**：

- `proto.RegistryRunningView` / `SquadsResp` 与 web 契约夹具（Q-r1-5 甲）；
- B448 前一轮冻结的 F1–F37；
- 转交端点与 `runOnCoordinatorCarrier` 的名额语义（F32 / F33 保持）。

**改动面为什么能收住**：全仓写 `sched_running` 的只有 `scheduling.go:1226` 与 `:1253` 两处 `Put(kindRunning…)`。所以持有维护只需改这两个写函数，以及调用它们的 `acquire` / `AdmitCarrier` / `Release`。

### r1.4 工作顺序

1. **先钉混版本金样本（承重假设）**。写测试证明两点：
   - 旧解码器（`struct{Count int "json:\"count\""}`，与 `registry_read.go:90`、`watchdog.go:453`、`scheduling.go:1213,1237` 同形）解新 body，count 正确；
   - 新解码器解旧 body `{"count":3}`，得到 count=3、holders 为空。

   这是「旧二进制读新 body 不坏」的唯一机内证据。若失败，阻塞后续全部步骤，回到 plan。
2. scheduling：body 类型与三个计数函数的持有维护（T1–T3）。
3. scheduling：归还方法与扣减规则（T4–T8）。
4. orchestration + agentd：owned 计数、窄接口、`StartAutomation` 前置对账、`RecoverOnStartup` 去对账。既有 `recover_occupancy_test.go` 的五条用例迁到新入口，断言不变（T9、T10）。
5. 全量测试：按 implement 三段律。
6. 交协调者跑真机 R7′。

### r1.5 验收方式

**机内断言（执行者）**。全部打真实账本门面（`SetupAutomationForTest` 环境，或 scheduling 包的真实 Registry 实现），以便穿过真实的 body 编解码；不能只用内存 fake。

- **T1 持有维护**：两个 `Service` 共用一个 Registry，自名分别为 A、B。A 准入 2 次、B 准入 1 次、A 归还 1 次。断言成员键与载体键两行的 body 都是 `count=2, holders={A:1,B:1}`。`AdmitCarrier`（无成员键）同样记持有。
- **T2 旧 body 上的新写**：直写旧 body `{"count":3}`，A 准入一次，得到 `count=4, holders={A:1}`。
- **T3 新 body 给旧读者（金样本）**：
  - 新 body 用旧形结构解码，count 正确；
  - `RunningCounts()` 与 `GET /api/squads` 的 `running[].count` 不变，响应里没有 holders 字段（Q-r1-5 甲）。
- **T4 基本对账**：`count=3, holders={A:2,B:1}`，自名 A，本机无任务。对账后得到 `count=1, holders={B:1}`，version+1。
- **T5 owned**：同 T4，但本机有一条非终态任务（含 waiting_review）落在该键上。只扣 1，得到 `count=2, holders={A:1,B:1}`。终态任务不计入 owned。
- **T6 无可扣**：body 无 holders，或 `holders[self]=0` 时，不写、version 不变。既有 B23310 / B23311 五条用例的语义原样保留。
- **T7 上限**：`count=2, holders={A:5}`。对账后 `count=0`，holders 中不再有 A；计数任何时候都不为负。
- **T8 并发 CAS**：Registry 包装层在对账写时注入一次 `ErrCASConflict`，期间 B 并发准入一次。断言重试后两者的增减都保留：`count` = 原值 − d + 1，`holders[B]` 增 1。另注入重试耗尽：按 Q-r1-4 甲，启动不失败，日志带 key / version。
- **T9 时序（竖切）**：
  - 设置：载体 `max_concurrency=1`；账本里预置 `count=1, holders={self:1}`，模拟上一进程被杀；launch 队列里有一条协调者拉起请求。
  - 断言：调 `StartAutomation` 后，fake Runner 收到 Launch，即首轮准入成功、没有 409。
  - 变异：把对账挪到循环首轮之后，该用例必须变红。
- **T10 解码损坏**：body 不是 JSON 时，`StartAutomation`（Q-r1-1 甲）返回带 key 的错误，循环不启动。
- **变异两处**（供 review 复核）：
  - 删去 owned 扣减 → T5 变红；
  - `casCount` 不保留他机 holders → T1 变红。

**运行入口**：

```
go test ./internal/scheduling/ ./internal/orchestration/ ./internal/agentd/ -count=1
go test ./... -count=1
```

全量按 implement 三段律跑。memory 里记的两条环境敏感测试，按既有口径处理。

**真机清单（本节由协调者执行，不派发）**

- **R7′ 执行机被杀后计数归还**。沿用 R1 靶子（`t448-linux` / `t448-mac`，PG 测试库），两台隔离实例都换成本轮构建。**不碰在役 7777，不碰生产账本。**
  1. **准备**：把 `t448-oc` 的 `max_concurrency` 设为 1。另外预置两行，与本机行合起来覆盖「本机 / 他机 / 无主」三类：
     - 他机行：可以在 `t448-mac` 上登记载体 `t448-oc-mac` 并让它跑一个在途驻场续轮；也可以用 SQL 插一行 `carrier/t448-foreign`，body 为 `{"count":1,"holders":{"t448-mac":1}}`。
     - 无主行：用 SQL 插一行 `carrier/t448-legacy`，body 为旧格式 `{"count":1}`。
  2. **在途读数**：从 mac-mini 隔离控制台叫机器人（或建会话），首轮转交到 `t448-linux` 执行。在途时读测试库，断言 `squad/t448-coord/t448-oc` 与 `carrier/t448-oc` 两行都是 `count=1, holders={"t448-linux":1}`。存 SQL 读数。
  3. **杀进程**：在 linux-01 上 `kill -9` 隔离实例（不是 7777），然后重启它。
  4. **重启后断言**：
     - `t448-oc` 的两把键都是 `count=0`，holders 里没有 `t448-linux`；
     - 他机行与旧格式行的 body、version 都不变；
     - linux 实例日志里有对账行，带 key、扣减数、保留数；
     - B444 启动清扫照常请下在途驻场行，并落「被重启打断」指针行（保留原 R7 断言）。
  5. **名额可再用**：再叫一次机器人，结果是成功，不是 409「协调者并发已满」；回合结束后计数为 0。
  6. **可选 R7′b（混版本）**：把 mac-mini 实例换成基线 `b9e68472` 的构建，让它对 `t448-oc-mac` 准入并归还一次。断言 holders 被丢、count 正确；再重启 linux 实例，断言该行不被改写。
  - **收尾**：手工清掉被杀实例遗留的孤儿 opencode 进程。这不在本卡范围，只是清场。

### r1.6 决策权限

**执行者可自行定**：

- body 的 Go 类型名；
- holders 为空时是省略还是写空对象（冻结条目 F39 已把两者定为语义等价）；
- 归还方法的名字、签名与报告结构；
- orchestration 窄接口的形状；
- 日志措辞与测试文件组织；
- `recover_occupancy_test.go` 用例迁到新入口的方式（断言语义不得放宽）。

**须升级**：

- 拍板结果以外的任何扣减口径；
- 修改 `count` 的语义或准入判据；
- 让 orchestration 或 agentd 直接写 `sched_running`；
- 修改 `/api/squads` 的 wire；
- 新增 DDL；
- 触动 F1–F37；
- 发现某条准入与归还不在同一台机（推翻承重事实 2）。

交回物首节「偏离与自主决定」要对照本节逐条说明。

### r1.7 缺陷族风险

- **并发 CAS**：对账与他机的准入或归还会同时改同一行。所有写都必须是「读 body → 改 count 与 holders → 带 version Put → 冲突就重读重算」，不得拿旧读数回写。acquire 半程回滚为什么用带符号减法，`scheduling.go:1088-1099` 的注释已写明理由。由 T8 锁定。
- **混版本**：旧写者（`scheduling.go:1225,1252`，body 为 `map{"count"}`）每写一次，就丢掉全部 holders，包括他机条目。退化方向是「无主保留」，只会少扣、不会多扣。全队换版后，新准入会重新积累 holders。由 T2、T3、T6 与 R7′b 锁定。
- **误清他机**：`d` 只来自 `holders[self]`，前提是自名在共享账本内唯一。两个 agentd 用同一 `name` 挂同一账本时，会互相扣对方的占用。这是 B439 自名唯一性的既有前提，本轮不新增防护，只在对账日志里打印 self 名，便于排障。真机靶子用 `t448-*` 专名和测试库（Q5 甲），避开了这种情况。
- **重复扣**：对账扣掉的名额若日后又被 Release 一次，计数偏低，导致超发。两道防线：
  - owned 与 Release 用同一来源的键（Q-r1-2 甲），由 T5 锁定；
  - 对账先于本进程的首次准入（Q-r1-1 甲），由 T9 锁定。

  另有一种情形：旧版本准入、新版本归还时，减到的 `holders[self]` 本不含这一名额，结果是少扣，属于安全方向。
- **改名**：`cfg.Name` 改名后重启，旧名下的持有会被当成他机、永久保留。与「机器永久下线」同归 roadmap，不在本轮。

### r1.8 需冻结的外部承诺

**结论：要冻结。** `sched_running` 的 body 不经 HTTP，但它存在共享账本里，由不同机器上、不同版本的 agentd 进程并发读写，是持久化的数据契约。账本本身不解释 body（`ledger/registry.go:5,18`），兼容性全靠读写两端的代码，因此按跨进程 wire 对待。条目接续前一轮编号：

- **F38**（形状）：body 是 JSON 对象。`count` 为非负整数，原名、原义不变。新增可选字段 `holders`：一个对象，键是 agentd 自名，值是正整数。
- **F39**（缺失 ≡ 空）：`holders` 缺失、为 `{}`、为 `null`，三者同义，都表示「无已知持有者」，都不触发对账写。
- **F40**（准入判据不变）：准入与满员判断只读 `count`，不读 `holders`。
- **F41**（旧读者兼容）：只认识 `count` 的解码器解新 body，count 正确且不报错（金样本测试，即 T3）。
- **F42**（新读者兼容）：新解码器解旧 body `{"count":N}`，得到 `count=N` 与空 holders（T2）。
- **F43**（保留他人条目）：新写者每次写都保留 `holders` 里非自名条目的原值。
- **F44**（准入、归还同写）：准入在同一次 CAS 里把 `count` 与 `holders[self]` 各加 1。归还在同一次 CAS 里把 `count` 减 1（下限 0），`holders[self]` 大于 0 时也减 1，减到 0 时删键。
- **F45**（持有者名）：`self` 是写者 agentd 的 `cfg.Name`，为空时取 `os.Hostname()`（Q-r1-3 甲）。
- **F46**（对账只动自己）：对账写只改 `count` 与 `holders[self]`，其余键值语义不变。
- **F47**（对账扣减量）：`d = min(max(0, holders[self] − owned[key]), count)`，写后 `holders[self] = min(holders[self], owned[key])`。若 `d = 0` 且 `holders[self]` 不变，则不写，version 不变。
- **F48**（对账时序）：对账完成之前，本进程不发生任何 `sched_running` 准入写（Q-r1-1 甲，由 T9 锁定）。
- **F49**（损坏阻断）：body 解码失败时启动报错，带 key 与 version（沿用现状）。

**拍板记录**（三重闸门）：

- **Q-r1-2 甲**（按 owned 扣，不扣光本机名下）：
  - 难逆转：误扣会经后续 Release 造成超发；
  - 无上下文的人会惊讶：spec 字面写的是「全部」；
  - 反过来写，除 T5 外没有任何既有测试会变红。
- **Q-r1-1 甲**（对账前置到 `StartAutomation`）：反过来写、保持现在的启动顺序，既有测试全绿，但生产启动窗口里会误扣。

### r1 核实读数（基线 `30b21c5d`）

1. **计数写者**：
   - 全仓对 `sched_running` 的写只有两处：`casCount`（`scheduling.go:1224-1228`）和 `stepRunning`（`scheduling.go:1231-1262`），body 都是 `map[string]int{"count":N}`。`readCount`（`:1205-1220`）只解 `count`。
   - `acquire`（`:1100-1174`）经 `casCount` 做两级递增（`:1135,:1146`），半程回滚走 `stepRunning(-1)`（`:1148`）。
   - 经 `acquire` 准入的有：`admitInto`（`:1054`）、`AdmitFrozen`（`:683`，acquire 在 `:749`）、`AdmitSeatCarrier`（`:787`，acquire 在 `:828`）、`LaunchAdmit`（`:844`）、`Admit`（`:531`）。
   - `AdmitCarrier`（`:420`）直接调 `casCount`（`:445`）。`Release`（`:879-906`）走 `stepRunning(-1)`。`Select`（`:568`）只读、不占名额。
   - `orchestration/watchdog.go:451-503` 只 `List`、不写。
2. **准入机 = 归还机**（承重；静态已核实）：
   - 协调者路径：准入与 defer 归还都在同一函数、同一进程内。
     - `runOnCoordinatorCarrier`：准入在 `scheddrain.go:447-451`，`defer release()` 在 `:457-464`；
     - 续轮 `wakeCoordinatorRoundID`：准入在 `:667`，紧接着 `defer releaseSchedulingBinding`；
     - 驻场续轮：准入在 `residentpush.go:259`，defer 在 `:263`。
   - 转交路径：受理端先还后转（F32）；收件端在本端准入、本端归还（F33）。
   - 执行者路径：
     - `scheddispatch.go:79` 只 `Select`、不占名额，冻结身份经 HTTP 发到目标机；
     - 目标机 `handleDispatch` 的 `AdmitFrozen` / `prepareReceiverBinding` 在本机准入（`handlers.go:539`、`receiver_bind.go:71-73`），若 `!IsSelfMachine(binding.Target)`，defer 归还并拒绝（`handlers.go:566-573,585-592`）；
     - 任务只由 `handleDispatch` 经 `mgr.Dispatch` 建在本机 SQLite store（`handlers.go:593`；`store.CreateTask` 的唯一生产调用在 `orchestration/manager.go:986`）；
     - 终态归还 `releaseTaskCarrierOccupancy` 只在本机 `handleDone` / `handleStop` 读完本机 store 后调用（`handlers.go:875,933-937,951,982-986`）；
     - 远端镜像任务在独立的 `mirror_tasks` 表，`ListTasks` 不含它们（`store/mirror.go`、`store.go:452-454`）。
   - 结论：不存在跨机归还路径，持有者可以按「写者自名」记。
   - **未验证，需真机**的行为事实只有一条：CLI 的 `done` / `stop` 总是路由到任务所在机。若被路由到他机，结果是 404，不会误还。R7′ 不依赖这条事实。
3. **本机名**：`Service.selfName` 字段在 `scheduling.go:167-178`，`SetSelfName` 在 `:186`；组装点在 `cmd/agentd.go:753`，取 `cfg.Name`。`scheduling.New` 在 `cmd/agentd.go:734`。账本镜像 holder 的回退规则（自名为空用 hostname）在 `cmd/agentd.go:772-775`。
4. **启动序**：
   - `setupLedger`（调用在 `cmd/agentd.go:314`，定义在 `:654`）里先 `SetScheduling`（`:755`），再 `StartAutomation`（`:758`）；
   - 回到主流程后才调 `RecoverOnStartup`（`:360`），它经 `server.go:1387` 调 `ReconcileSchedRunning`；
   - 最后 `bootGate.Ready`（`:401`）。
   - `StartAutomation`（`scheddrain.go:54-71`）的 goroutine 先跑 `sweepInterruptedResidents`（不准入），再进 `automationLoop`；循环首轮立即 `runAutomationPass`，事件唤醒和清队都可能在这里准入。**这证伪了 spec「启动时尚无在途回合」的前提**（见 Q-r1-1）。
   - `RecoverOnStartup` 只把任务迁到 waiting_review、不迁终态（`watchdog.go:596-602`）。
   - 现行 owned 是集合，不是计数（`watchdog.go:473-481`）。
5. **混版本**：
   - 所有读者都用只含 `Count` 的匿名结构加 `json.Unmarshal`，未知字段被忽略：`registry_read.go:90-95`、`scheduling.go:1213-1219,1237-1242`、`watchdog.go:453-455,491`，测试夹具 `recover_occupancy_test.go:20-22` 也一样。
   - 旧写者会整体重写 body，丢掉 holders（见第 1 条）。
   - 账本不解释 body（`ledger/registry.go:5,18,24`）。
6. **观察面**：
   - 投影链：`RunningCounts`（`registry_read.go:80-99`）经 `SchedulingClient.RunningCounts`（`scheduling_client.go:49-51`），投影为 `proto.RegistryRunningView{Key,Count}`（`proto/scheduling.go:50-60`）。
   - 前端类型在 `web/src/api/scheduling.ts:38-39`，契约测试在 `web/src/api/contract.test.ts:637`。
   - 这条链只取 count，新字段不影响解码；按 Q-r1-5 甲不投影 holders。
7. **既有测试**：
   - `internal/agentd/recover_occupancy_test.go` 的五条用例（`:77,:99,:120,:151,:184`）经 `srv.RecoverOnStartup` 测对账，夹具都是只含 count 的 body，按 F39 不触发写。
   - Q-r1-1 甲把对账移出 `RecoverOnStartup` 后，这五条要改调新入口，断言不变。
   - 调用 `StartAutomation` 的测试文件只有一个。
