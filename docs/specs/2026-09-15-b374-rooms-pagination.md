# B374 spec：房间列表强制分页 + attach 刷新限域 + 日志三件套

状态：已批准（2026-09-15，用户审批） / 级别：L3轻 / 日期：2026-09-15

## 问题陈述

`card wait` 事件流延迟、打开工作项与 IM 页面慢、房间发言慢。2026-09-15 实测根因链：

- `GET /api/rooms` 返回 366 个房间（358 card + 7 project + 1 global），耗时 6.8s。
- 每次列表触发 `enrichRoomAttachments` → `startRoomAttachRefresh`，每轮对约 800+ 远端挂账 fan-out 16 并发 Attach RPC（`internal/agentd/roomsapi.go:157`），每 task 两行 INFO。
- launchd plist 把 stdout+stderr 指向同一文件，`logx.Setup`（`internal/logx/logx.go:31`）又向同文件写 JSON：同一记录落盘两次。`agentd.log` 涨到 19.6G、增速约 100KB/s、磁盘 92%。
- 止血（`HANDOFF_LOG_LEVEL=warn` + 截断 + gc）后：日志增速→0，列表 6.8s→1.06s。剩余 1s 为真实 RPC fan-out 与全量组装成本。
- 旁证：镜像发现 `linux-01 context deadline exceeded`（事件流滞后的另一嫌疑，本期顺带排查）。

现状引用（读数，非落点）：`internal/agentd/roomsapi.go:54`（`handleRoomsList`）、`internal/agentd/roomsapi.go:212`（逐 task INFO）、`internal/collab/service.go:368`（`listRooms` 全量组装，终态卡房间仅标 `ReadOnly` 不剪枝）、`web/src/api/rooms.ts:142`（`fetchRooms` 解包 `rooms` 数组）。

## 级别与档位

**L3轻**。定级两问：① 改动跨 `d_gateway`（handler+刷新）/`d_collab`（collab 列表语义）/`d_web`（懒加载）三个子系统契约面；② 动跨进程 wire 契约 `GET /api/rooms`（外置桌面 app 同进程外消费），即便加参相容、强制分页也是行为 breaking。选档：单域切片均未超流程固定成本（约 70 分钟），→ 轻档。轻档不跳契约冻结：分页 wire 语义由 contract 节点冻结。

> 备注（B374 contract 勘误，2026-09-15）：上段原写 `d_sessions`（collab 列表语义）为误写——协作房间是 `d_collab`，`d_sessions` 是终端 PTY 回放域（见 `codegraph/best.json` 的 `k_collab_* → d_collab`）。本勘误只改域 id，不重开 spec 语义。

## 方案

### 采纳：D 服务端强制分页（用户 2026-09-15 拍板）

- `GET /api/rooms` 必分页：`limit` + `cursor`，默认 `limit=50`（contract 可调）；响应带 `next_cursor`/`has_more`；排序稳定（`LastActivity` 降序，建议 cursor 为复合游标，contract 拍板）。
- attach 投影与后台刷新**只覆盖本页返回房间**；未返回房间不触发远端 RPC。
- web 会话列表懒加载：首屏一页，向下滚动载下一页。
- 日志三件套同批：① `roomsapi.go` 高频 INFO（约 64/139/145/152/212/218 行）降 Debug；② 修双写（同一记录落盘一次）；③ `agentd.log` 按大小轮转（`logx` 包注释明确不管轮转，此处补上）。
- 镜像发现超时（`linux-01 context deadline exceeded`）本期排查定性：是刷新风暴的次生症状还是独立故障，结论落卡。
- b358 §4.4 修订：列表不再全量（旧房间形态、只读、历史可查语义不变，仅列表访问分页化）。
- 发版约束（硬）：agentd + web + 外置桌面 app **同批升级**；旧客户端命中新 agentd 时拿到可行动的升级提示，不得静默半页（阻断文案 vs 降级首屏由 contract 定，默认阻断提示）。

### 弃选与理由

- A 摘终态房间：与 b358 §4.4「旧卡房间语义不变、不删旧形态」冲突最深，需大修冻结契约；且用户要保留旧房间可达性。否。
- B 可选分页+默认全量：老客户端（当前全量轮询的桌面/web）风暴依旧，治标不治本。用户否。
- C 两期拆分：用户选一步到位；且分页与日志三件套同因（不修日志 IO，分页收益被吃掉一半——warn 实测已证）。否。

## 用户故事

1. US1：打开 IM/工作项列表，首屏 ≤2s（基线 6.8s/1.06s），滚动续载无闪断。
2. US2：`card wait` 不再被刷新风暴饿死；可动作事件秒级到达（管道禁令仍有效）。
3. US3：房间发言低延迟；已读回执不受列表刷新牵连。
4. US4：磁盘不再被日志吃光：默认 warn + 单写 + 轮转后，日志增速常态 <1KB/s。
5. US5：未升级的旧桌面端打开列表时看到明确升级提示，而非不明不白的半页数据。

## 契约语义与接缝（L3增段，定语义不定签名）

- 语义1：页是 `LastActivity` 降序稳定切片；翻页不丢不重（cursor 比较规则 contract 定）。
- 语义2：刷新域 = 本页返回集合；跨页不预取、不补刷。
- 语义3：日志默认级别 warn；INFO 为调试态；同一记录落盘恰一次。
- 语义4：旧客户端策略（阻断提示为默认；降级首屏为备选，contract 二选一冻结）。
- 接缝清单（符号 + 调用方）：
  1. `collab.Service.ListRoomsForMember` ← `agentd.Server.handleRoomsList`（存量，`internal/agentd/roomsapi.go:54` 可验）。
  2. `agentd.Server.startRoomAttachRefresh` ← `enrichRoomAttachments`（存量，同文件 `:151` 可验）。
  3. `logx.Setup` ← `cmd/agentd.go:78`（存量；图未覆盖，见备注图覆盖债）。
  4. `GET /api/rooms` wire 对面 → `web/src/api/rooms.ts#fetchRooms`（存量调用方）。
  5. 新建分页参数决议符号 ← `handleRoomsList` 调用，导出到 `d_gateway` 层（contract 核导出面）。
  6. 新建页裁剪符号（service 层）← `ListRoomsForMember` 内调（contract 核）。

## 实现决定

- cursor 建议复合游标（`LastActivity`+卡 ID），默认 `limit=50`：建议值，最终由 contract 拍板，spec 不写死签名。
- `roomAttachRefreshInterval/TTL` 本期不动（分页后 fan-out 已降一个量级；再动是优化项，进 roadmap）。
- 轮转建议 100MB×5 份：建议值，plan 落地。
- 托管默认值 `HANDOFF_LOG_LEVEL=warn`（plist 已改，2026-09-15 生效）保留。

## 测试决定

- handler 分页单测（页稳定、不丢不重、旧客户端提示分支）。
- service 页裁剪单测（含终态房间仍可达、只读标记不变）。
- web 懒加载组件测试（滚动续载、has_more 终止）。
- `logx` 双写回归：单记录落盘恰一次断言；轮转触发单测。
- 真机：以本机 366 房间账本复测，首屏 <2s；`card wait` 端到端延迟对照（基线见问题陈述）。

## Out of Scope（必写）

- 摘终态卡房间出列表：永不做在本期；作为后续项逐条落 `docs/roadmap.md`（b358 修订卡）。
- 外置桌面 app 本期只做升级提示与同批发版，不重写其列表实现。
- `tmp/` gc 策略、PG 账本形态不动（PG ping 9ms，非瓶颈）。
- 推迟项残余：`roomAttachRefreshInterval` 动态化 → roadmap。

## 备注

- 图覆盖债：`logx.Setup` 未入代码图（`codegraph sym` 无命中，已回落 grep 读码）；`listRooms` 须用全名 `n_collab_Service_listRooms` 查询。由后续重扫消化。
- 批准即回写本头部状态与日期，然后交棒 contract（L3 路由）。
