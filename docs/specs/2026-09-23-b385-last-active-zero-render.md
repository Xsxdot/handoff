# B385 spec：会话详情「最后活跃」是恒零值字段——换接账本可证实读数

状态：已批准（2026-09-23，群主 session:15 两段裁决：v1 选②补描述性规格；真机验收退回后改选 B——事件推导，不改代码除外） / 级别：L1（spec+plan 同文件双附件） / 日期：2026-09-23

> **修订链**：v1（提交 c6995b7e，已成孤儿提交，链在卡 session:15 seq 19983/19985）按群主
> 「选②」补了描述既成事实的规格并声明「验收可收口」。当晚真机验收**退回**：会话详情所有
> 成员的「最后活跃」恒为「未记录」，有过活动的人也查不到时刻。群主改选 **B**（事件推导）。
> 本文是 v2 修订版：定级改判、方案改写，前端既有修复保留。批准即回写本头部。

## 问题陈述

两波症状、同一根因：

- **第一波（09-19，卡上 09-19 comment + 截图）**：真机走查（无头 Chrome 390×844，部署版
  b4c539e786a5）打开会话抽屉，成员「最后活跃」渲染成 **739877 天前**——Go `time.Time`
  零值经 `Date.parse` 被当有效值算相对时间。前端修法已落地：`4ec97de4`
  （`format.ts#isUnrecordedTime` 纪元判据 + `memberStatusText` 渲染「未记录」）。
- **第二波（09-23 晚，真机验收退回）**：假读数没了，但**所有**成员、**永远**显示
  「未记录」——有过活动的人也查不到时刻。等于字段死着。

根因（复核于 2026-09-23，实库直查）：`last_active` 的唯一填充源是
`internal/collab/sessions.go#memberStatus`，它只读 `driver_leases` 表
（经 `s.lc.DriverLease`）。而 B189（merge `15dfc51f8`「废除驱动租约的 TTL 过期语义」）
废掉了租约心跳/续期——`RenewDriverLease` 生产零调用方（`docs/roadmap.md:647` 记债
「租约三法零生产消费方」，本轮 grep 复核：生产 0 命中，仅测试用）。线上库直查
（2026-09-23 19:05，PG 100.84.251.46）：**driver_leases rows = 0**。表恒空 →
`exists=false` → `memberStatus` 恒返回（last_active, 零值）→ 全员「未记录」。

契约面旧账：B358 契约（`docs/superpowers/specs/b358.md:85`）写明成员状态「只报可证实的」，
并声明「driver_leases 三法零生产消费」这笔已知债在本期核销——但 `memberStatus`
落地时仍接在死表上，债没核销，只是变成恒零值的 wire 字段。

## 级别与档位

**L1**。定级两问：① 单子系统（`d_collab` 投影面；`d_web` 零改动）；② 不动契约层——
wire 字段形状与「零值=无」语义不变（`internal/proto/sessions.go:61` 注释逐字保留），
只换填充源。L1 两判据：plan 增量为零（实现决定即本文「实现决定」三行，plan 只会复述）；
验收一眼可核（真机开抽屉：有活动者见真实时刻、无活动者见「未记录」）。
**验收不跳快道**：群主明示「验收仍归人」（2026-09-23 裁决），acceptance 照走人工列。

## 方案（已批准，群主裁决 B：账本推导）

`memberStatus` 弃读死表，`last_active` 改从**账本里能证实的事件**推导；不动 B189；
前端零值文案「未记录」保持不变（`4ec97de4` 保留，零前端改动）。

**读数语义（冻结）**：成员/席位的「最后活跃」= 该身份在本会话范围内最近一次可证实动作
的账本时刻（`ev.CreatedAt`，同刻取最大）。

- **范围**（沿用 timeline 归属判据 §9 同族）：会话房间消息（事件载荷 `room` == 本会话 id）
  ∪ 本会话各卡的账本事件（`ev.CardID` ∈ 本会话当前 Cards）。卡移出会话后其历史不再计入
  ——与 `sessionTimeline` 现行判据一致。
- **动作词表**：一切账本事件（卡事件与会话消息）都算可证实动作，**除了**：
  `agentd` 系统组件书写的事件（actor 恒为系统标识，如指针行、wake_round 机器行）——
  系统不是成员，不算任何人的活动。
- **身份匹配**：actor 与成员/席位身份精确相等为主；唯一直读侧归一——旧传输脸
  `cli:<名字>@<主机>` / `web:<名字>@<主机>` 的本地段与某 `user:<名字>` 成员名**精确相等**
  时归一为该成员（群主 CLI 发言的 actor 是 `cli:sycm@sycmdeMacBook-Air.local`，成员名是
  `user:sycm`；不归一则群主自己的发言永远不算自己的活动，验收必再挂）。
  该归一**只用于本读数推导的展示面**；权力面（写权限、成员名单、@ 路由）仍按
  `internal/proto/identity.go` 决定 8 fail-closed，旧脸回落禁令不受影响。
- **席位**：席位成员身份（`cli:<cli>#<session_id>`）与卡事件/会话消息的 actor 精确相等
  即可命中（如 `driver_seat_bound`/`driver_takeover` 的 actor=席位自称、席位身份的会话
  发言与卡 comment）。
- **状态词表不动**：有读数报 `last_active`+真实时刻；无读数报 `last_active`+零值
  （前端「未记录」）。`working`/`listening` 本卡不接——那是租约语义，归 B189 裁决范围。

### 弃选与理由

- **A 恢复租约心跳（接唤醒回合/协调者心跳）**：`docs/roadmap.md:240` 明载 B189 裁决
  「若将来要恢复覆盖到别的死法，必须先推翻 B189 的裁决，不能顺手加回 TTL」；且租约只能
  覆盖持卡席位，盖不住 `user:`/`agent:` 纯成员——群主明确否（「A 翻 B189 也只覆盖持卡
  席位，盖不住 user: 和 agent: 成员，验收过不了」）。否。
- **精确匹配不归一**：群主本人经 CLI 发言的 actor 是旧脸，成员名单里是 `user:sycm`——
  不归一则有活动的人仍「未记录」，验收过不了。否。

## 用户故事

1. US1：成员/席位有过可证实活动（发过言、坐过卡、落过卡事件）时，会话详情显示真实的
   「最后活跃 N 分钟/小时/天前」。
2. US2：成员无任何可证实话动时，显示「最后活跃 未记录」；永不出现计算出来的天数
   （`4ec97de4` 行为保留）。

## 契约语义与接缝

- 语义（既有，本卡 reaffirm 不改）：`proto.SessionMember.LastActive` 零值=无记录；
  消费方把零值渲染为「未记录」，不得计算相对时间。本卡只改**填充源**，wire 不动。
- 接缝清单（符号 + 调用方）：
  1. 新建事件推导符号（collab 包内，读 `[]proto.LedgerEvent` + 会话 → 成员身份→时刻表）
     ← `sessionMembers` 调用（`internal/collab/sessions.go:222` 一带）；导出面 = collab
     包内非导出函数即可（无跨包调用方，contract/plan 核）。
  2. 新建旧脸归一符号（读侧，`cli:<名字>@<主机>`/`web:<名字>@<主机>` → `user:<名字>`）
     ← 推导符号内调。
   3. `memberStatus`（`internal/collab/sessions.go:260`，存量）← `sessionMembers`
      （`:234`/`:251` 两处调用）——本卡改写其函数体；实现落地时为携带会话范围推导读数
      增参 `lastActive time.Time`（review-1 minor 记录，协调者 2026-09-23 追认：返回形状
      `(string,time.Time)` 不变，调用方 `sessionMembers` 同文件内单遍扫描后传入，无跨包
      面变化）。

## 实现决定

1. `memberStatus` 数据源换成上缝 1 的事件推导（单遍扫描；`SessionDetail` 已持有全量
   事件流，`internal/collab/sessions.go:84`）；停止调用 `s.lc.DriverLease`（恒空死表）。
2. 上缝 2 归一 helper 按方案词表实现；未列出的 actor 形态一律不归一（宁窄勿宽）。
3. 前端零改动；`proto` wire 零改动；`driver_leases` 三法本卡不删（消费方清零后
   roadmap:647 那条债仍在账上，处置归独立卡）。

## 测试决定（接缝清单）

- 缝 1 缝级测试（collab sessions_test）：成员发言/席位卡事件给真实时刻；范围外事件
  （别的会话、已移出卡）不计；同刻取最大；零值穿透。
- 缝 2 表驱动：`cli:sycm@host`→`user:sycm` 命中；本地段不等不归一；席位脸精确匹配；
  `agentd` 不算活动。
- 回归：`go test ./internal/collab/...` 全绿；web 侧既有 48 条不涉改动。

## Out of Scope（必写）

- 恢复租约心跳 / 推翻 B189：永不做在本卡（roadmap.md:240 红线）。
- `driver_leases` 三法（`RenewDriverLease`/`DropDriverLease`/`AllDriverLeases`）零消费
  后的删除或另接用途：本期不做，roadmap:647 债行继续挂着，另开卡处置。
- `working`/`listening` 精确态（租约语义）：本期不接，词表保留。
- 前端任何改动（含 `memberStatusText`）：零改动。
- 09-19 comment 的截图 `~/.handoff/notes/wt-6-drawer.png`：历史证据，不入库。

## 备注

- 台账：根因取证过程（grep 命中、实库 0 行直查、B189 溯源）已落卡账
  （session:15 seq 20023、卡 B385 2026-09-23 晚 comment），不另立台账文件。
- 合并目标：本卡工作分支以 `cards/B385-charter-1`（基于工作线
  `cards/B233.1-charter-7`）为起点；finish 时合回工作线，合并归人。
