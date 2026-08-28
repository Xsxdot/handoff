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
- T6b 完成：ProjectTree 全量重绘。层级=项目行(加粗名+dispatch-task 计数+右侧箭头)
  →「任务」小标题组(已打开行在前/未终态任务/已结束行，行=图标槽+状态点+名+机器簇)
  →「目录」小标题组(机器行绿点+右箭头+悬停终端/新建工作树钮+紧凑子行+左轨线缩进)。
  ProjectTreeProps：removed openedItems/onOpenItem/onOpenDirectoryTerminal；added
  openItems/focusedTaskId/onFocusOpenItem/onOpenTerminalAt(Shell 最小接线，T7 收口)。
  search.ts 新增导出 taskMatchesQuery；filterTree 的 openedItems 参数收窄为
  OpenedSearchItem(base/name/machine/detail)。
  测试：ProjectTree.test 重写 62 支(层级 DOM 序/任务组构成/已结束收起+计数/已打开项
  在前+双态/机器行悬停两 case/搜索/⌘K/dock/偏好/右键/建树)，search.test 2 支夹具
  改新形状，Shell.test 1 支按任务原名改写。tree+shell 187/187 绿，typecheck 绿。
- T6b 偏离记录：
  1) plan Consumes 表 OpenItem.group 类型 number 与 activateTab(groupId: string)
     矛盾——取 string（group id），activateTab 才能直接消费。
  2) OpenItem 增加可选 detail 字段（plan 形状没有）：保住「按文件相对路径搜到
     已打开行」的既有搜索能力（search.ts openedText 原本读 content.rel）。
  3) plan「列的 min-w-0 机制不动」类问题同款：plan 声称行样式类 .proj-*/.mach-*
     在 option-1 里，实际它们是 b288-workbench-ux 的类；按 plan 冲突规则
     「两原型冲突以 option-1 为准」在两者都定义的类（task-row、子行）上取 option-1，
     仅 b288 定义的类（archive-row、mach-row）取 b288。
  4) 工作树子行去掉 Home/GitBranch 图标（option-1 目录子行无图标）；行上
     RowCounts 计数控件全部移除（spec §5 明言），计数保留给排序/折叠内部使用；
     「隐藏文件夹数量」偏好测试改写为「行上不再渲染计数控件」。
  5) 项目行悬停「工作项/代码图」既有入口保留（out-of-scope 弃选的是原型新增项，
     移除现存入口属于功能回退，超出本卡授权）。
- T7 完成：useWorkbench 新增 focusTab(b, group, tabId)（select + activateTab 一次事件，
  接口注释写明为什么不用 open）；Shell 的 onFocusOpenItem 改走 focusTab，
  onOpenTerminalAt 改走 wb.openTerminal(base)（不再 select 基准、不再强制新组）；
  openItems 排序收紧为「当前基准的项在最前，其余按组序」；taskName resolver 上移
  Shell（taskNameResolver）经 props 下传 WorkbenchPage（窗格标题+标签条继续透传）。
  plan 偏离：T7 文中 wb.byBase/Object.entries(byBase) 在现模型不存在——按语义
  改为在 wb.groups 摊平结果上按 base.key 分组排序；OpenItem.group 取 string。
  Shell.test 新增 2 支（openItems 顺序+focusTab 链路、tail 随激活 tab 变化），
  1 支标题更新；Shell 41/41 绿，persist/restore/useWorkbenchSync/useWorkbench 29/29 绿。
- T8 收尾全量（2026-08-29）：
  npm test → 112 files / 1130 tests 全绿。
  npm run typecheck → tsc -b 零错误。
  npm run lint → 23 problems（5 errors, 18 warnings）——与基线 f770304b0 完全一致
  （api/pty.ts no-case-declarations、flows/NodeEditor.test prefer-const、
  workbench/terminalHostResponse no-control-regex ×3），本卡零新增。
  npm run build → tsc -b + vite build 成功（chunk >500kB 警告为基线既有）。
  功能保留清单逐条核过：搜索/⌘K、树偏好菜单、机器行右键菜单、未归属分组、
  底部 dock、任务拖拽（含 data-drag-task 放行链路）、断连徽标与原因、
  确认弹层（终端会话/草稿文件，标签条关闭钮也过确认闸）、新建工作树弹层、
  计数内部使用（排序/折叠）——全部有测试支撑且绿。
  真机截图对照原型属验收节点动作，不在本 executor 范围。
## 裁决与重做（2026-08-29，T2 执行轮）

- 协调者裁决：首轮 T2「摊平内容 tab 条」否决。原因：初版 spec/plan 侦察读了 main 旧形态
  （目录为组），把卡基线（B281/B285 后）的组/列/格中央模型误判为 B264 未来结构。
  以修订版 spec/plan（92ef249dd）为准重做，其余产出（T1/T3/T5/T6a/T6b、chrome 资产）保留。
- T2-rev 完成：TabBar 恢复基线 props 全套（onActivateGroup/onCloseGroup/onNew/onNewLauncher/
  onNewGroup/onMoveGroup）+ taskName；组拖动 DRAG_GROUP_MIME dragstart/dragover/drop 与
  多窗格告警原样恢复；option-1 chrome（44px/药丸面/非末尾组分隔线/tab-add 行尾 +=
  新建标签组）；图标按组焦点内容（tui→dispatch-task.png/terminal/file/空组→Plus）。
- 问题 1 落点：autoName 组标签在**显示层**推导为焦点内容名（tabTitle + taskName resolver，
  tui=任务原名、解析不到回退 TUI·id8），不改布局模型、持久化组名不变——基线 tabs.ts
  的 autoName 只有赋值没有改名路径，显示层推导是「组名/窗格头/左栏已打开行同源一致」
  且零持久化增量的最小落点（偏离说明：协调者猜测改名在布局迁移函数里，实际不在）。
- 组关闭钮放回标签旁（基线结构，可访问名=「关闭 <组标签>」精确匹配），不放药丸面内
  ——保住基线 launchers.test 逐字恢复（getByRole('tab', { name: '组 2' })）。
  每组「新建内容」IconMenu 原样保留。
- WorkbenchPage 恢复 moveGroup/tabCount 与基线 TabBar 消费；closeTabById 移除
  （标签条关闭钮语义回到「关闭整组」，无确认闸——基线语义）。
- Shell 的机器行终端钮恢复基线行为（select + openOrFocus → 独立新组），onOpenTerminalAt
  接回；Shell.test：空初始组现在渲染为「组 1」标签（计数 1→2 按基线改回），关闭钮
  断言全部瞄准窗格头（组关闭钮同名歧义消除）。
- 红绿：launchers.test 基线逐字恢复（红：组拖动告警）+ TabBar.test 重写 8 支（红 6 支）
  → 绿；workbench 235/235、shell 50/50、tree+shell+workbench 424/424，typecheck 绿。
- T4-rev 完成：落点预览五区全部 1:1 原型——top（inset 0 0 50% 0 + inset 0 4px 0 #2563eb）、
  bottom（inset 50% 0 0 0 + inset 0 -4px 0 #2563eb）补全；另实现原型 .pane.pane-term.drop-*
  变体（终端窗格遮罩 rgba(147,197,253,0.5)，问题 2「黑底终端看不出落点」的主场景）。
  WorkbenchPage.test 新增 2 支（top/bottom 断言、终端窗格浅蓝断言），红→绿 19/19。
- T8-rev 收尾全量：npm test → 112 files / 1135 tests 全绿；npm run typecheck → tsc -b
  零错误；npm run lint → 23 problems（5 errors, 18 warnings）与基线一致（本卡零新增）；
  npm run build → 成功（✓ built in 2.06s，chunk 警告为基线既有）。
  重做后组语义核对：组标签条激活/关闭/拖动/新建、autoName 组显焦点任务原名、
  五区落点 1:1、T1/T3/T5/T6a/T6b/T7 产出保留。未 push，未跑 handoff。
