# B383 spec 台账（2026-09-28）

| # | 事实/读数 | 出处 |
|---|---|---|
| 1 | 双 WARN 两处：请求侧 `PauseWaiting` 落空 `adapter.go:1634-1637`，应答侧 `Resume` 落空 `adapter.go:751-763`；`permTiming` 登记先于 PauseWaiting 判定（`adapter.go:1619-1627`），一次落空必成对 | grep + 读码 |
| 2 | H3 链路：`acceptForeign` `adapter.go:1432-1457`——子会话只放行 `permission.asked`（:1436-1448），其余丢弃（:1450-1456）→ 子会话工具段永不开 → 双落空；归属处 INFO 已带 `is_child`（:1610-1611） | 读码 |
| 3 | spike3/spike5 解析（SSE `data:` 前缀剥离后）：各含恰好 1 条 `permission.asked`（seq 11，前有 4 条 part.updated）、单会话集合（spike3 `ses_020cb20e6ffe…`、spike5 `ses_020c76668ffe…`）、无子会话 | python3 解析 testdata |
| 4 | 本机 `~/.handoff/agentd.log`（49MB，mtime 2026-09-28）grep「未找到对应工具等待窗口」「权限请求已归属会话」均 0 命中 → 夜班 WARN 在远端执行机，`is_child` 关联取证不可达 | grep 本机日志 |
| 5 | 被拒空回合链路：`mapIdle` `adapter.go:2199-2219`——`takeTurnRejected` 非空即发射 question（:2211-2213），文本由 `rejectedTurnQuestion`（:944-956）合成；`noteRejected` 登记 :771-789。该 question 是 B21 反挂死 P0 的设计行为，非 opencode 原生 | 读码 |
| 6 | `handleQuestion` 建 `kind=ask` 工单 `manager.go:2888-2965`；`taskIsOpenCode` :436、`noteDenyGuidanceUnlessInBand` :3719。**更正（审计）**：opencode 拒绝理由是死存储——`RespondPermission` 丢参（`adapter.go:702`）、API 只 POST response（`api.go:549`）、guidance 唯一消费点被 `!taskIsOpenCode` 门住（`manager.go:2896-2901`），`DenyReasonInBand` 仅 claudecode/agy 实现；初记「opencode 走 in-band」不成立 | 读码 + 审计 |
| 7 | B57② 复用：hook `manager.go:2029`、调用 `:2708`、语义注释 `:2552`；`FindReuse` 实体 `authority.go:244-261`（只 allow、只同任务、fail-closed） | 读码 |
| 8 | `writeCommands` `writeargs.go:44-53`：tee/cp/mv/ln/install/dd/gofmt/git——无 rm、无 cat | 读码 |
| 9 | `judgeCommand` `blacklist.go:133` 整串匹配不看 scope；`builtinBlacklist` :38、`execWrapperRx` :68、`evalRx` :77 | 读码 |
| 10 | `InScope` `path.go:41-68` 三基准 Workdir/TaskDir/TaskTmpDir + `resolveExistingPrefix` 软链防护 | 读码 |
| 11 | `Paths` 越界入口（opencode external_directory）`permgate.go:180-238` | 读码 |
| 12 | 自指令现判据 `selfcmd.go` `judgeSegment`：命令位置判据（4ded5bba1），`-c/-e/-E` 旗标残留 fail-closed | 读码 |
| 13 | codegraph 存在（`codegraph/best.json` 等）但本轮以 grep/读码定位（跨包链路需多域拼图，图查询收益低——记一笔，不属覆盖债） | ls |
| 14 | 放弃的尝试：远端执行机日志取证（is_child 关联）——不可达，spec 按「代码链路 + 入库样本」定案，真机验证排 acceptance 轮 | 判断 |
| 15 | 独立审计（Explore 子代理，SPEC-NEEDS-FIX 五条已全部并入）：A/B 节 PASS（事实骨架与 H3 链路全对上）；C/D/E CONCERN——对账路径同形缺口（`reconcile.go:219-237`）、VoidReason 缺位（`manager.go:3487-3491` 审计谎言风险）、台账 item 6 更正、§5 措辞（未导出符号）、WP1 方案 2 超卡标注 | 审计报告 2026-09-28 |
