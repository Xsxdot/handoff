# B413 spec 台账

一行一事实：读数、命令与原始输出、判断、放弃的尝试。spec 正文只留结论，过程读数归这里。

- 2026-09-28 领卡：`handoff card show B413`——高优 bug 卡（待办→spec）。卡 note（2026-09-27，发现自 B369.9 验收）带完整根因证据链：①console.error 抓现行「Cannot update a component (Shell) while rendering a different component (ProjectTree)」，JS 栈顶 poll（usePoll.ts）→dispatchSetState；②触发器 macbook-pro 100.91.173.63 离线（ping 100% loss），agentd 扇出 `/api/pty/sessions?scope=all` 拖到 ~3s（agentd.log deadline_exceeded target_call_ns≈2s）；③A/B 实锤：同浏览器 5174 跑 B369.8 基线同样冻结，B369.9 diff 不是肇因。
- 2026-09-28 工作树：`git worktree add ~/.handoff/worktrees/manual/B413 -b fix/B413-poll-transition-starve origin/main`——基线 a86e6c35（B369 合入点）。本地 main 落后 origin/main 54 提交，出事代码面（B369 移动端紧凑工作台）只在远端，故分支基于 origin/main 开。
- 2026-09-28 `codegraph sym usePoll` 命中 `n_web_app_data_usePoll`（d_web_shell 域，web/src/app/data/usePoll.ts:28）。useMobileNav / MobileProjectDetail 未查图、直接读源（图覆盖债：无——spec 未引用两符号内部，仅确认导航写口与按钮落点）。
- 2026-09-28 读 `web/src/app/data/usePoll.ts`（现状读数）：续帧写入点 :85-97——`await request` 之后 `setData(v)` / `setDisconnected(...)` / `setErrorText(...)` / `setSessionExpired(true)` 全部同步落（默认 sync lane）。文件头三条实时性纪律：document.hidden 停表、断线保留最后数据、401 落终止态不再重试。
- 2026-09-28 grep usePoll 消费方（-l 原始清单）：Shell.tsx、useTasks.ts、useProjectTree.ts、useMachines.ts、usePreviews（经 Shell）、useUpdate.ts、BoardPage、CardsPage、CoordinatorPanel、SessionSidebar、SessionTab、MobileProjectDetail、MachinesPage、usePoll.test.ts。
- 2026-09-28 `web/package.json`：react ^19.1.0、react-dom ^19.1.0、react-router-dom ^7.18.2。`web/src/main.tsx` StrictMode + createRoot；`web/src/App.tsx:35` `<BrowserRouter>`（未传 `useTransitions`）。
- 2026-09-28 node_modules/react-router/dist/development/chunk-62JRHF6Z.mjs:10399-10407（现状读数，vendor 产物）：`BrowserRouter` 的 history listen 回调 `useTransitions === false ? setStateImpl(newState) : React.startTransition(() => setStateImpl(newState))`——缺省即 transition；`useTransitions` prop 由 App.tsx 未传，走缺省。
- 2026-09-28 流密度读数（现状读数）：Shell.tsx:218/219 cards+decisions 2500ms；:239 sessions `COLLAB_POLL_MS`=5000（rooms/constants.ts:4）；:247 ptySessions 30_000（`enabled: compact`，fetcher=`fetchPtySessions('all')`——离线拖慢的那条）；:280 launchers 30_000；useTasks.ts:7 `TASKS_INTERVAL`=2500；useProjectTree.ts:9 `TREE_INTERVAL`=30000。
- 2026-09-28 「在此打开终端」按钮：web/src/app/tree/ProjectTree.tsx:1033（aria-label），onClick `openTerminalAt(base)` → `openMobileDetail()` → `nav.enterDetail()` → `navigate()`（compact 下唯一写口，useMobileNav.ts:179-206）。
- 2026-09-28 测试命令：web `vitest run`（package.json:11）。桌面/compact 双路径既有结构面证据：tree 186 + Shell 106 全绿（b369.10 收口读数，卡 note 2026-09-27 引）。
- 2026-09-28 判断（三候选裁决，理由展开见 spec §3）：采纳候选 1——usePoll 全局口径，续帧写入包 `React.startTransition`；依据：transition-lane 更新与导航 transition 合并进同一次渲染、一次提交，不触发「sync 更新打断重启 transition」的饥饿回路；候选 2（关 transition / idle 排队）不消除干扰源或无 lane 语义；候选 3（agentd 快速失败）是延迟卫生非机制修复，落 roadmap。
- 2026-09-28 放弃的尝试：无（读码即定性，未写任何试验代码——硬门：批准前禁止实现动作）。
