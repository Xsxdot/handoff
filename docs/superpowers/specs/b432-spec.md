# B432 — 移动端：iOS 系统推送「需要你」（APNs MVP）

**定级：L2**（新 REST+store + iOS 壳 APNs；事件旁路挂既有账本写成功点；不动 bind/配对信封——碰了抬 L3）

## 问题

杀进程 / 切走后，iOS 上「需要你处理」类待办到不了系统栏。现网无推送骨架（B369 把 APNs/FCM 标 OOS）；前台靠轮询/`/api/inbox` 角标，「需要你提醒」只是站内开关。

产品定稿（2026-10-10 产品与方案群）：**MVP = 系统推送(B) + 只 iOS(APNs) + 事件收窄「需要你」**；站内铃铛可顺带对齐但不挡；安卓中国区厂商强达另卡。

## 验收

1. **何时推**：仅 iOS；仅当产生「需要你处理」类待办（与现工作台/工作项「需要你」同源），且用户未在前台已读该条——前台已打开对应面则不重复弹系统横幅。
2. **点进落哪**：点通知进 App，落到该条「需要你」对应会话/卡（无深链则落工作台「需要你处理」入口）；角标与未处理数一致。
3. **站内铃铛**：本刀**不挡、不做必达**——有现成 inbox/角标就对齐「需要你」计数即可；没有也不为铃铛单开范围。权限拒绝/无 token 时静默降级站内，不报假送达。

## Out of scope

- 安卓 / FCM / 中国区厂商通道 / 聚合推送 / 强达(C)
- 自建长连当强达替代
- 改 bind / 配对信封 / `Session.DeviceName` 挂 token
- 为站内铃铛单开新范围（仅对齐既有则顺带）
- 事件源收窄到仅 `needs_human`（禁止；见下）

## 架构边界

- **扇出只在协调机 agentd**：壳只做权限申请、APNs token 上报、点通知深链。
- **新表 `push_devices`**：不把 token 写进 `Session.DeviceName`。
- **事件源**（对齐产品「需要你」）：inbox 三源 `decision` / `ticket` / `mention`（+ 可选会话 `needs_human`）；**别只挂** `needs_human`。前台已读不弹，靠 inbox/会话计数去重。
- **挂点**：扇出钩在既有账本写成功点旁路；不重造「需要你」判定。
- **定级两问**：跨子系统面 = agentd 新 REST+store + iOS 壳 APNs（正交 webview）；契约层 = 新设备登记端点 + 内部 Fanout，**不动** bind/配对 → **L2**。

## 契约面

### 1. 设备登记（对外 REST）

`POST` / `DELETE` `…/push/devices` — **DeviceRegistration**：

| 字段 | 说明 |
|---|---|
| `member` | 成员 |
| `device_id` | 设备 id |
| `platform` | 本 MVP 固定 `ios` |
| `apns_token` | APNs device token |
| `auth_session_id` | 可选 |
| `updated_at` | 更新时间 |

谁写：iOS 壳在获权后上报；谁读：agentd 扇出查表。无 token / 拒权 → 静默降级站内，不报假送达。

### 2. 内部 Fanout（agentd → APNs，不对外）

| 字段 | 说明 |
|---|---|
| `event_type` | 事件类型（对齐 inbox 源） |
| `member` | 目标成员 |
| `title` | 通知标题 |
| `card_id` | 可选卡 id |
| `ref_id` | 引用 id |
| `deep_link` | 点进深链；无则 UI 落工作台「需要你处理」 |

## 工作流

L2：plan → implement → review → acceptance → finish。本节点写 plan（实现切片、测试入口、风险）；不写实现代码。
