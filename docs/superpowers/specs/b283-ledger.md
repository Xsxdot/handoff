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
