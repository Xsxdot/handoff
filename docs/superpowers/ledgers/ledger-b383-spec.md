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
| 16 | **用户裁决（2026-09-28）：v1 暂不批准**；方向 A=b、B=a 支持；五条阻塞：①分级按代码图三顶层域改 L3 补契约语义；②WP2 多造了提取器责任——RedirectTargets 已存在，真实缺口是 heredoc 正文与 cd 相对路径，rm 不与重定向共用提取器；③A=b 对账收紧——row4 ToolStatus=error 双义不可全标被拒，VoidReasonTurnDiscipline 不适合有意拒绝；④B=a 只收可证明安全子集（git checkout -- 写工作树、模块缓存 Paths 先被范围门拦、psql -c 非笼统只读），需写明维持人工清单；⑤H3 降级待验证假说，用新鲜样本定修复范围 | 用户消息 2026-09-28 |
| 17 | v2 复核读数：`RedirectTargets` redirect.go:48-104（`> >> >| n> &>`，边界明写不处理 heredoc/不跟 cd :8，已知 cd 相对写误放行残余 :15-17）；`judgeBash` 落点循环 permgate.go:242-249 先于 `safeCommandID` :267；VoidReason 两档 executor.go:105-110；reconcile row4 reconcile.go:168-169 双义 + classifyReconciled :223-233 补 question；零文本 result 缺 VoidReason adapter.go:2230-2234；指纹版本盐 fingerprint.go:41-43 + manager.go:452；codegraph/best.json 顶层 15 域，本卡跨 d_policy/d_execution(_adapters/_contract)/d_orchestration | 读码 |
| 18 | roadmap 落账三条（跨任务记忆、executor 只读基准、拒绝理由送达）→ docs/roadmap.md 队列 4-6；v2 提交后推送 cards/B383-charter | 落账 |
