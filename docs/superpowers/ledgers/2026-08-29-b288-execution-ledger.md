# B288 执行台账（implement 节点）

- 执行者：charter implement 节点 subagent，无人值守（用户显式授权，回落原因见 spec 备注）。
- 工作树：/Users/xushixin/.handoff/worktrees/manual/B288，分支 cards/B285-review-2，基线 f770304b0。
- 包管理器 npm；测试 `npx vitest run <file>`；全量 `npm test` / `npm run typecheck` / `npm run lint` / `npm run build`。
- T1 完成：红（1 断言红 + taskName 模块缺席编译红）→绿 25/25，typecheck 绿；commit
- T2 完成：TabBar 重绘为内容 tab 条（groups 摊平为 tab：group 序→列序→行序），行尾
  + 钮=IconMenu（新建内容语义保留）；资产 dispatch-task.png 入 web/src/assets；
  WorkbenchPage 构建 taskName resolver 注入 TabBar 与窗格标题（spec §5 窗格标题同源）；
  TabBar 关闭钮经 closeTabById 走 onBeforeClose 确认闸（与窗格 × 同一闸）。
  测试：TabBar.test 6 支新支 + WorkbenchPage 回归 + Shell.test 12 支断言按新结构改写
  （'组 2' tab → /任务原名/；关闭钮两处入口 getAllByRole）。workbench 目录 225/225 绿。
- T2 偏离记录：
  1) plan「分屏钮 ml-auto 保留、disabled 语义保留」不落——分屏钮在基线代码中不存在
     （B285 bb7e0828a 删除并冻结「布局只由拖放产生」，Shell 有回归测试钉住），
     已批准原型 option-1 的 chrome 也没有分屏钮；按「以原型为基准」硬规则取原型。
  2) group 拖动（DRAG_GROUP_MIME/onMoveGroup）、每组独立关闭、行尾「新建标签组」随
     旧组 tab chrome 一并移除（原型无此形态，组模型重构归 B264）；
     launchers.test 的组拖动告警测试随特性移除，未保留空壳。
  3) plan 文字「非激活 tab 后才渲染 tab-sep」与自身断言「3 tab→2 sep」矛盾
     （激活 tab 在首/中时两条规则不相容）；取数得清的断言 + 原型（每 tab 后一条、
     行尾无），分隔线渲染在除最后一个 tab 外的每 tab 之后。
- T3 完成：breadcrumbSegments(base, tail?) 纯函数 + 行渲染 1:1 workspace-context（28px/13px/#7c7c7c/' / ' 分隔/title 全文），Shell 构建_crumbTaskName 与 tail（焦点 tab 非 blank）；DesktopTitleBar 不传 tail 未动。Breadcrumb.test 5 支新支全绿；Shell.test 2 支断言改写（深链/跳转到该任务 → 第三段=任务原名）。shell 48/48 绿，workbench 225/225 绿，typecheck 绿。
- T4 完成：dragging 状态（window dragstart 认 data-drag-task / dragend+drop 双保险复位）、pane-content 层拖动期 pointer-events-none、落点预览 left/right/center 数值 1:1 原型（top/bottom 留旧形态属 B264）、testid 改为 drop-<zone>、ProjectTree 三处任务行加 data-drag-task=1。WorkbenchPage.test 5 支新支/改写，75/75 绿，typecheck 绿。
- T5 完成：resizeColumns 删除 min*2>两栏之和 的整体早退（改注释说明夹紧语义），
  WorkbenchPage 最外容器加 overflow-hidden。偏离记录：plan 写「列的 min-w-0 机制不动」，
  实际基线列是 min-w-[240px] 硬下限——按 spec §3「列 flex 均分、min-width:0」与
  原型 .col{min-width:0} 改为 min-w-0，240px 下限改由拖拽 minRatio 夹紧承担
  （否则窄窗三列被裁切，违反验收场景）。tabs.test 2 支新支（三列夹紧 + 单侧夹紧保留）、
  WorkbenchPage 容器断言 1 支；workbench 232/232 绿，typecheck 绿。
- T6a 完成：archivedKey 一个参数、archivedTasks(tasks) 不再收 tree（终态全收、顺序=任务流原序、未归属不收）；ARCHIVED_TITLE 改「已完成 / 已失败的任务」；ProjectTree 调用点随签名更新且已结束子行排除已打开 tui（openSet 语义）；search.ts 的 archivedHit 升为项目级（项目可见性用），search.test 1 支断言按新结构改写。tree 136/136 绿，typecheck 绿。
