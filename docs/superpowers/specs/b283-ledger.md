# B283 spec 台账

- 2026-08-28 开卡：工作树 `~/.handoff/worktrees/manual/B283`，分支 `fix/B283-float-terminal-dup`（基点 origin/main `994311da0`；本地 main 落后 origin/main 35 个提交，本地无独有提交）。
- 2026-08-28 真机取证：`~/.handoff/handoff.db`（本机 agentd，pid 69995，端口 7777）dock 快照 4 个 tab，全部 id==sessionId（UUID 收编形状）、machine=mac-02、seq 1..4 递增、windowOpen=false。
- 2026-08-28 真机取证：`scope=all` 会话列表里这 4 个会话 created_at 为 11:17:57.331/.348/.361/.372（41ms 区间），pid 6968/6969/6973/6975 连号，cols/rows 全为 166×44（浮窗尺寸）——一次挂载风暴，非用户逐次点击。
- 2026-08-28 本机 agentd.log：「终端会话扇出失败」日志 114 行（text/JSON 双格式各记一次）＝ **57 个事件**：win-b37 34 行（连接拒绝）、mac-02 21 行、linux-01 2 行——收编来源机器 mac-02 自身也扇出失败过，链路反而被强化。本地 base_kind=home 会话建立记录 6 条（08-15、08-18×2、08-20×2、08-26），均不在当前活会话列表。
- 2026-08-28 排查 note 落卡时写「PTY 会话是内存态，agentd 重启即死」——错误，被 Shell.tsx 陈旧注释与文案误导。用户指正后核实：ptyreclaim.go 头注（不参与崩溃/升级重启）、survive_test.go `TestSurviveAgentdClientRestart`（会话与滚屏跨 agentd 客户端存活）、启动扫描 sessdir 认领。卡上已补 kind=更正 note。
- 2026-08-28 红色回路：`web/src/app/workbench/b283-redloop.test.ts`（工作树内未提交）。vitest run 结果：1 failed——open2 dock.tabs 期望 1 实际 2（S1 live 且不在 used 集合 → 被收编）。回路机制确认。
- 2026-08-28 用户裁决设计边界：悬浮窗 home 终端不跨机同步（本机面）；中央区 tab 跨机同步照旧（同步面）。
- 2026-08-28 勘误备注：工作树安装依赖用 npm ci（web/ 有 package-lock.json，无 pnpm-lock）；临时 ticket cookie 已删除。
- 2026-08-28 独立审查（用户点名派子 agent，按 spec/review/defect-families 纪律）：总判「修订后再批」，无 Critical；Important I1–I4、Minor M1–M7 共 11 条，r1 全数吸收进 spec。审查人亲手读码复证根因链四环节、只读 sqlite/grep 复证真机取证（114 行＝57 事件即其更正）、亲手跑红色回路失败形态一致。审查文件：`docs/superpowers/reviews/b283-spec-review.md`。未核实项（审查文件已标）：mac-02 侧 41ms 四连发/pid 连号为远端运行时状态，本机不可独立取证，采信台账。

## plan 节点（2026-08-28）

- 2026-08-28 plan 出稿开始：出稿人亲手读 `web/src/app/workbench/restore.ts`、`persist.ts`、`useWorkbenchSync.ts`、`web/src/app/homedock/dockPersist.ts`、`useHomeDock.ts`、`web/src/api/types.ts`、`web/src/app/shell/Shell.tsx`（217/270/301/655）、`web/src/app/workbench/TerminalTab.tsx`（8/616/379）、`restore.test.ts`、`persist.test.ts`、`dockPersist.test.ts`、`useWorkbenchSync.test.ts`、`Shell.test.tsx`（工作树 HEAD `91680a494`）。图覆盖债：本节点未跑 codegraph——核对对象全是 spec/审查点到行的引用，直接读码 + 跑测复核（照 b286 先例记债）。
- 2026-08-28 判据基线跑（web/ 下）`npx vitest run src/app/workbench/b283-redloop.test.ts`：**1 failed**，原始失败行 `AssertionError: expected [ { id: 'h1', …(4) }, …(1) ] to have a length of 1 but got 2`（`b283-redloop.test.ts:58`，open2 把活着的 S1 当孤儿收编 → tab 1→2）。
- 2026-08-28 判据基线跑（web/ 下）`npx vitest run src/app/workbench src/app/homedock`：Test Files **1 failed | 22 passed (23)**；Tests **1 failed | 358 passed (359)**。唯一 failed 即红色回路；restore.test.ts（14 条）、persist.test.ts、dockPersist.test.ts、useWorkbenchSync.test.ts 等全绿，是各 task 跑红/跑绿步骤的基线参照。
- 2026-08-28 服务端事实亲手复核：`internal/agentd/pty_api.go:186-241` —— `ptySessionsAll` 构造 `Machines` 恒以 `{Name:"", Ok:true}` 领衔（189 行），远端失败只写该机器行 error（HTTP 仍 200），会话行 machine 戳与 machines 行同循环盖章。「本机行恒 ok=true」成立，无第二赋值点。
- 2026-08-28 关键签名决定：① `RestoreInput` 增 `machines?: MachineStatus[]`（对应 `PtySessionsResp.machines` 的可选形状，`types.ts:730`）；② `RestoreResult` 增 `purged: number`（方案2 清除的外来 tab 计数，进 `useWorkbenchSync` 的 console.debug，acceptance 对照「外来 tab 消失属预期」用）；③ `pruneDeadSessions` / `pruneDeadDockSessions` **签名都不改**——门控落在 restore.ts 两个调用处（workbench 侧按 base.machine 决定是否调用；dock 侧把扇出缺席机器 tab 的 sessionId 并进 live 副本再调用，归属按 `tab.machine`）。
- 2026-08-28 关键语义决定：`machine===''`（本机）在 ok 集合里无条件置位——任务指令「本机恒 ok」的实现形态：门控表里恒有本机行，prune 逻辑本身无本机特判分支，与 spec「本机会话不设特殊分支」一致；machines 缺席/查不到的机器一律按不 ok 保守保引用。该读法下既有 restore.test.ts 全部夹具（全本机）零改动保持绿。
- 2026-08-28 放弃的尝试（门控读法甲）：`''` 不置位、纯按 machines 查表——machines 缺席时连本机引用都保，语义更保守，但既有 restore.test.ts 的剥引用用例会集体翻红（每条都要补 machines 夹具），且与任务指令「本机恒 ok」字面冲突。弃。
- 2026-08-28 放弃的尝试（门控进 prune 签名）：给两个 prune 函数加 machineOk 参数——workbench 门控按整行 base.machine 均匀成立，调用处直接跳过调用比传参简单，且任务指令把门控钉在「调用处」。弃。
- 2026-08-28 放弃的尝试（红色回路原样搬）：原夹具 dock tab `machine='mac-02'`，方案2 无条件清除外来 tab 后该 tab 在 open1 即消失，「保引用」断言在 dock 面写不出来。转正夹具改为「外来 tab（承载清除断言）＋本机 tab machine=''（承载保引用反转断言，其会话在名单里）」；「两次打开不增长」不变式保持，open2 的 `adopted=0` 断言兼验方案1。
- 2026-08-28 关键次序决定：restore.ts ② 内 prune（门控）先于 purge——purge 无条件删整 tab，最终 tabs 与次序无关，但 `pruned` 统计的诚实性依赖这个次序（「外来 tab 被清是整 tab 走、不是剥引用留壳」→ pruned=0，可与基线的 pruned=1 区分）。清除命中 activeId 显式置 null 交给既有兜底（restore.ts:225）重指；kept 为空时 windowOpen 收 false（decode 出来本来就空的退化现场一并兜住——closeTab 写不出那种形状；既有用例对该形状不断言 windowOpen，保持绿）。
- 2026-08-28 计划落盘：`docs/superpowers/plans/b283-plan.md`——Task1 machines 入参＋中央区门控；Task2 home 收编仅本机（方案1）；Task3 悬浮窗门控＋存量清除（方案2＋方案3 悬浮窗半）；Task4 红色回路转正；Task5 话术订正六处。出稿自审时纠正一处 task 次序错误：清除用例断言 `adopted===0`／`tabs 长 0` 依赖方案1 在位，方案1 必须先于清除 task，DAG 定为 1→2→3→4（5 独立并行）。
