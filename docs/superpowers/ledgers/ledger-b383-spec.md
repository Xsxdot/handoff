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
| 19 | **用户裁决（2026-09-28）：v2 仍有五处承重缺口**：①heredoc 规则不安全——未引用定界符时正文中的命令替换仍会执行（用户本机实验），只能豁免「明确不会执行正文」的受限形态；②原始 rm 场景没闭环——`cd "$TMPDIR" && rm -rf verify` 的相对删除目标无判据（v2 rm 规则只认临时目录路径、cd 规则只覆盖相对写）；③域归属仍错——adapters/contract 是 d_execution 子域（best.json parent）、approval 归 d_orchestration，轻档按每子系统总工作量判，VoidReason 扩档不构成 contract 节点前置；④A=b 只解决实时路径——对账无法归因时第二张 ask 依旧，需可靠归因或显式接受缩减；⑤零复现不能核销双 WARN 故事，只能记未验证 | 用户消息 2026-09-28 |
| 20 | v3 复核读数：best.json 容器映射 `k_approval_* → d_orchestration`、`k_permgate_* → d_policy`、`d_execution_adapters/contract` parent=d_execution（:82 起）；**对账归因哨兵实证**——`testdata/session_rejectedend.json` 被拒工具 part error 文案固定为 "The user rejected permission to use this specific tool call."（opencode 原生，版本漂移风险已记，退化方向=安全侧回兜底 ask）；归因实现前提 = SessionMessage 现只带 state.status（api.go:446），需扩带工具 part error 文案（内部模型扩展） | 读码 + 夹具 |
| 21 | v3 裁定落法：heredoc 豁免=引用定界符 ∧ 非执行消费者（cat/tee）；cd 解释基准写与删共用；对账走哨兵归因不接受缩减；H1 修复=本卡承诺（结构性成因，验收=合成回放红→绿，零复现只记未验证）；H2 仍超卡增量取证证实才纳入 | 落账 |
| 22 | **v4 复审反例**：`cat <<'EOF' \| bash` 中 quoted heredoc 仍作为下游解释器的脚本执行；故不能只凭引用定界符和前段 cat 就剔除正文。v4 改为整条命令/所有段的可证明数据形态，管道至解释器一律升级 | 安全 shell 实验（只输出标记）+ 命令语义复核；卡事件 22610 |
| 23 | **v4 复审反例**：`TMPDIR=/tmp; cd "$TMPDIR"` 的 cd 实际进 `/tmp`，因此 `cd "$TMPDIR" && rm -rf verify` 仅在 `$TMPDIR` 可证明仍绑定任务注入值时才可放行；同名赋值/重绑定、命令替换、子 shell、未知目录变化 fail-closed | 安全 shell 实验（只读 `pwd`）+ `internal/permgate/path.go:39-68`、`redirect.go:8-17`；卡事件 22610 |
| 24 | **v4 复审反例**：PostgreSQL 官方 psql 文档说明 `-c` 可接受单条反斜杠元命令；`\!` 执行 shell 命令。故 v3 `psql` 旗标内容不算命令位置候选的通用放宽不安全；v4 仅放宽经验证的 rg/grep 纯搜索参数，`psql -c` 保持人工门 | https://www.postgresql.org/docs/current/app-psql.html（-c Options、`\!` Meta-Commands），`internal/permgate/blacklist.go:58-81`；卡事件 22610 |
| 25 | **v4 复审反例**：`RespondPermission` 成功后才 `noteRejected`（`adapter.go:740-789`），而 SSE idle 可先消费空回合（`:2199-2234`）；`clearTurn`（`:2376-2380`）不清 `turnRejected`。锁保护单次访问不足以保证归属；v4 加 API/idle 交错、失败、迟到、跨回合与恰一次终局不变量 | 读码；卡事件 22610 |
| 26 | H2 序 B/C 的合成回放已被独立评审测成双 WARN；v3 又声明无条件禁止成对 WARN，却把 H2 留作可选，范围与 S1 冲突。v4 将 H1/H2 均列必交，现场取证只用于夜班归因，不用于缩减已证反例 | 卡事件 16677、22610；v3/v4 对照 |
| 27 | `session_rejectedend.json` 只有会话消息及 tool part 的英文 error，未记录 opencode 版本和权限请求 ID；`api.go:446` 当前仅拷最后 tool part status。v4 把无第二 ask 的对账承诺限定于新鲜实测版本的同一 terminal tool part 哨兵，失配须回 spec 而非假报通过 | `python3` 读 `internal/executor/opencode/testdata/session_rejectedend.json`；`api.go:390-465`、`reconcile.go:145-237` |
| 28 | L3 选档改重档：`d_policy` 内含四组相互牵连的安全判据/对抗测试，总量超过约 70 分钟固定成本；Wave 0 选 S3 拒绝回合竖切，先证 API/idle 因果与对账归因，再批次扇出。当前无新增对外承诺；冻结仍按独立工作单元共同依赖条件触发 | `charter:spec` 定级与选档判据；v4 方案判断 |
