# B358 台账（spec 阶段）

> 每确立一个事实追加一行。裁决变更回写 spec 正文；本文件留过程与原始读数。

## 一、用户裁决（2026-09-12 产品对话，逐条）

1. **组织模型**（用户原话）：董事长（人）→ CEO（主 agent）→ 总监（协调者）→ 团队（执行者）。主 agent 与 handoff 保持联系；handoff 叫起协调者推进 spec 之后的节点；协调者处理不了的问主 agent，主 agent 处理不了的问人；人能看到主 agent 与协调者的往来记录。主 agent 有两种形态：外部启动的（Claude / Codex 桌面端）与内部的（handoff 自己的对话面）。
2. **房间锚点翻转**：不再按卡开房间，改成"人 / 主 agent 开一场会话"，先聊需求与目标，再建卡或把卡拉进来。
3. **会话内是群聊**：可并行多张卡、可同时多个协调者在群里；协调者之间也用 `@`；**不分 kind，各自以自己名义说话**；群主可以是人也可以是主 agent，"处理不了的事 `@` 群主"。
4. **`@` 不是待办对象**：`@` 只是提醒"你有一条消息"；`needs_human` 依然存在，展示为**会话列表的标签**（会话里的卡有 needs_human，列表就出标签）。
5. **卡进群与配人分开**：拉卡进群只建讨论面；配不配协调者、谁配，仍在卡上按 B307 的三颗按钮。
6. **升级边界按三档**：协调者自决填补级；`@` 主 agent 仅在推翻级（影响执行走向、要动商定的东西）；人掌三道人工门。**主 agent 定时巡场看有没有跑偏，不接收每一个动作**——推是例外、拉是常态。

## 二、现状读数（原始出处）

- 账本在 PG：`~/.handoff/config.yaml` 的 `ledger.dsn`。`handoff room list` → `count=326`（本机账本房间数），其「活」列 **全部 false**。
- `handoff room inbox` → `count=0`。
- 抽查房间历史（`handoff room read <卡号>`）：
  - `B233.14` / `B233.16` / `B233.13` → **空**（这三张是当时最近在跑的卡）；
  - `B233` → 仅一条，actor = `cli:sycm@…`，正文是人对人写的交接备注。
- 纪律对比：`grep -c -iE "room|房间" skills/handoff/SKILL.md` → 14（协作房间纪律节在 `:578` 起）；`grep -rn -E "room|房间" ~/.agents/skills/` → **0**；`grep -rl -E "room|房间" ~/.handoff/discipline/` → **0**。
- 卡状态分布（同期）：`已完成 63 / 待办 38 / 终止 12 / implement 2 / finish 2`。

## 三、读码结论（现状事实，带符号锚）

- 房间是卡事件流的投影，**无房间表、无成员表**；消息落 `card_events`，群消息为无卡事件（`payload.room`）。
- `internal/collab/room/room.go#VerifyWriter`：协调者类 kind 仅本卡席位可写；`relay` 仅本卡或一级父卡席位；**群房间仅 `user` kind 可写**。
- `internal/collab/room/room.go#KindAllowed`：kind 白名单 7 种（escalation / deviation / closing / relay / reply / user / pointer）。
- `internal/agentd/wakeconsumer.go#automationWakeEvent`（:144）：首行 `if ev.CardID == "" { return false }` → **群消息不唤醒任何人**（@ 在群面无效果）。
- `internal/agentd/wakeconsumer.go#decodeHumanRoomMessage`（:201）：`kind == user && !by_system` → 唤醒本卡协调者，**`mentions` 未被读取**（广播形状）。
- `internal/proto/seat.go`：席位来源只有 `SeatSourceBind`（坐下）/ `SeatSourceCoordinate`（叫机器人）两种；席位是卡级 `(cli, session_id)`。
- `handoff decision open`（无 `--card`）= 项目级裁决，`decision list`（缺省）= 未答复收件箱 —— 是今天唯一的"无卡升级"通道。
- 已知债（`docs/roadmap.md` 记）：`RoomSummary.Live` 生产恒 false；`driver_leases` 三法零生产消费；`driver_carrier` 无 wire 字段。

## 四、北极星与批准（2026-09-12）

- 用户要求先看原型与流程图，并指定二者为**本卡的北极星**：形态 = `prototypes/b358-session-groups/pages/sessions.html`；过程 = `prototypes/b358-session-groups/pages/flow.html`。
- 载体流程按 `prototyping-in-brainstorm`：fork 自 `prototypes/base/`；IM 视觉语言取自 `b275-frontend-proto/`（base 未回流 B275，故不重复造视觉）。走查要点 W1–W6 见副本 README。
- 用户走查定稿（原话「我觉得可以，就这么定吧」）→ spec 头部状态回写为**已批准**；副本确认状态登记进 `prototypes/base/README.md`（置「确认中」，回流在 finish）。
- 入库决定（用户：「至于 git，随卡入库吧」）：`git add -f prototypes/b358-session-groups`；**不复制** `data/`（1.7MB codegraph 演示数据）与 `pages/codegraph.html`。
- 形态追加裁决（同日，见卡上对话与 `pages/sessions.html` 修订）：① **会话页是一等公民页面，不是工作台 tab**——不占 tab 条、不挂面包屑、不带右栏文件树，整块中央区归它（与工作项页同构）；理由是 tab 条语义是「我现在在干的几件事」（可多开/可关/可分屏），而家必须位置稳定，且会话跨卡跨机器、不合 tab 的「项目位置 × 工作树」绑定。② **左栏保留项目树**（全局导航），会话列表为中央区第一栏；未选「会话列表接管左栏」与「左栏可折叠」两个备选。

## 五、与既有文档的关系

- 被取代：B156.2 §4（卡:房间 1:1）、§5（kind 白名单与书写者矩阵）、§7 注意力面部分语义。
- 沿用：B156.3（协调者拉起/唤醒/小队）、B275（前端 IM 三态面板形态）、B307（三颗按钮与席位身份、平级不写对方卡房间）。
- 未决（本卡 OOS，落 roadmap）：主 agent 内部对话面、成员状态心跳、多人时代、历史翻页、富文本。
