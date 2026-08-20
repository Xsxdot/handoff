# 原型站说明

真实前端（`web/`）的形态镜像基准站。页面清单、导航结构、布局骨架与真实控制台对齐，
内容为代表性占位。fork 副本做交互细化时以本目录为起点。

## 路由

真实前端的路由都挂在 `web/src/app/shell/Shell.tsx` 的 `<Routes>` 下。看板与工单是
工作台内的 `<dialog>` 弹层，不单独成页；中央工作台的三种 tab（终端/文件/TUI）中，
TUI tab 是任务会话主视图。

| 页面 | 对应功能 | 来源路由 | 确认状态 |
|------|---------|---------|---------|
| index.html | 工作台（三栏 + TUI tab 对话式形态 + 看板/工单/偏好/建树弹层 + home 悬浮窗） | `*` | 已确认 |
| pages/settings.html | 设置页（开发机/执行纪律/常规/Env/**更新**） | `/settings` | 已确认 |
| （未建页） | 任务深链：按任务 id 直接打开它的 TUI | `/tasks/:id` | — |
| （无需页） | `/machines` → 重定向到 `/settings` | `/machines` | — |
| pages/board.html | 工作项账本看板（统一任务卡：看板/列表双视图、「需要你」就地筛选、详情抽屉含并入区/验收/Timeline、合并跟随可拆回） | `/cards` | 已确认 |
| pages/flows.html | 流程管理页（工作流状态形状 + gate、派发模板 + 纪律块版本，均不可变版本化） | `/flows` | 已确认 |
| （未建页） | 代码图页（后端调用图：左树 320px + 中间竖向焦点子图自适应 + 右详情 340px；单击聚焦、⌘+单击并集、层级默认上下 2 级、拖动平移 + ⌃滚轮缩放、后退/快进） | 规划中（spec `docs/superpowers/specs/2026-08-19-codegraph-design.md`） | 确认中 |

工作项账本两页的形态基准经 fork 副本 `workbench-ledger/`（`pages/board.html`、
`pages/flows.html`）走查确认（2026-08-19）。原则性约定：卡的一切信息只在详情抽屉
一处看，不另开面板；裁决/等人合一为「需要你」就地筛选；保真信号（驱动/镜像/冲突）
默认沉默、仅异常时上浮。真实页面（`/cards`、`/flows`）已按该副本开发并对照验收通过，副本已回流入本基准站
（2026-08-19，B156.1）。

TUI tab 的对话式重构（方案 A：单滚动会话流 + 事件内联 + 审阅右滑栏 + composer +
ctx/累计用量页头）经 fork 副本 `tui-redesign/` 走查确认，真实前端落地并真机验收后
已于 2026-08-17 回流进 index.html；同批回流的还有确认之后新增的形态：连续工具行
折成「执行了 N 步操作」、工具名中文化、运行中指示、加载更早/回合跳转的边界提示。
fork 副本不入库，已完成使命。

设置页的两处新形态（新增「执行纪律」分区：按机器编辑纪律块文件正文、内置两版只读可
另存；开发机详情内新增 executor→纪律块文件映射控件，三档下拉）经 fork 副本
`discipline-config/` 走查，已于 2026-08-19 确认形态，**该副本即真实页面的验收基准**。
状态待真实前端落地并对照验收通过后再推进为「已确认」。

更新形态（B166）经 fork 副本 `desktop-update/` 走查确认（2026-08-19），真实页面实现后
对照通过，形态已于 2026-08-20 回流进本基准站（副本可弃）。回流时**没有整份覆盖**：
`index.html` 在 fork 之后被左栏偏好的改动更新过，整份拷贝会把基准打回旧版，所以只搬了
提示框那一段；`原型脚手架`（切换三种提示的那条黑色胶囊）**不进基准**——真实产品里没有它。
原确认内容：
① 控制台右下角提示框接管原来挂在菜单栏图标上的三条动态提示（有新版 / 有更新待应用 /
上次同步失败）。只有「有新版」有真动作（下载 + 校验 + 打开安装包），主按钮就地转进度条、
完成后文案变「已在访达中打开」；另两条没有可点的补救，主按钮就是「知道了」。与 home 悬浮窗
共用右下角时整摞上移让位。② 设置页新增「更新」分区，三块为桌面应用（下载安装包）/ 同步状态
（提示框消失后的常驻落点，**不给「立即应用」按钮**——重开一次桌面端即等效）/ 执行机版本与
一键升级，本机那行标「随桌面应用一起更新」且不给按钮；③ 托盘只剩「打开控制台」与
「退出（agentd 继续运行）」，图标换成标志。设计见 spec
`docs/superpowers/specs/2026-08-19-desktop-update-surface-design.md`。真实页面开发后对照
该副本验收，通过后推进为已确认。副本还顺带把设置页外壳从旧的 `.footer` 换成 index.html
现用的 `.dock`——那是基准漂移的修复，回流时一并带回。

代码图页的形态基准经 fork 副本 `codegraph/`（`pages/codegraph.html`，读真实扫描数据
`data/graph.json`）走查确认（2026-08-19），迭代收敛为「树+图」主形态：左侧调用树导航、
中间竖向焦点子图（上游在上/焦点居中/下游在下，BFS 最短路分层）、右侧详情常显；
交互为单击换焦点、⌘/Ctrl+单击并集多选（chips 可单独移出）、层级下拉默认上下 2 级、
空白拖动平移 + ctrl/⌘ 滚轮以光标为不动点缩放、◀▶ 焦点历史后退/快进。时序图保留为
辅助视角；地铁图/调用树/节点图为过程试验品，不进真实功能。真实页面开发后对照该副本
验收，通过后推进为已确认。

## 镜像基准

`web/src/app/` 下：`shell/Shell.tsx`（三栏与路由）、`tree/{ProjectTree,TreePrefsMenu,NewWorktreeDialog}.tsx`
（左栏）、`board/{BoardPage,columns}.ts(x)`（看板）、`homedock/`（home 悬浮窗）、
`workbench/TuiTab.tsx` 与 `task/{TuiHeader,ConversationStream,ToolCard,streamGroups,
ReviewSidePanel,Composer,DebugDrawer}.tsx`（中央 TUI）、`settings/SettingsPage.tsx`。

**本轮全量刷新日期：2026-08-18（B131）。** 上一版生成于 2026-08-17。

## 覆盖边界（2026-08-18 核对，B131 刷新后）

base 是**快照，不是全量镜像**。对照时不要把「base 里没有」读成「真实前端没有」——
那正是 desktop-console 退役前反复咬人的那种错。

本轮补上的（上一版的四个缺口已全部纳入）：

| 区域 | 真实前端来源 | base |
|------|------------|------|
| 左栏顶部搜索（⌘K 聚焦，过滤项目/机器/任务） | `tree/{ProjectTree,search}.ts(x)` | ✅ 已覆盖 |
| 显示偏好菜单（隐藏无活跃任务的工作树／排序方式／逐项目勾选） | `tree/TreePrefsMenu.tsx`、`treePrefs.ts` | ✅ 已覆盖 |
| 机器行 hover 动作入口（新建工作树／更多） | `tree/ProjectTree.tsx` | ✅ 已覆盖 |
| 新建工作树弹层（开发目录／新建分支或检出已有／分支名／基线） | `tree/NewWorktreeDialog.tsx` | ✅ 已覆盖 |
| 看板卡片干预态（琥珀左竖条；`waiting_answer` 的「等你答复」只出现一次） | `board/{BoardPage,columns}.ts(x)` | ✅ 已覆盖 |
| 看板四列与状态机映射（等待执行／进行中／Review／完成，failed 视觉区分且不带干预标记） | `board/columns.ts`（vitest 钉死） | ✅ 已覆盖 |
| 底部图标区（看板／工单／设置／home／添加项目并列） | `shell/Shell.tsx` | ✅ 已覆盖 |
| home 基准终端悬浮窗（tab 条 + 新建 + 收起保留会话） | `homedock/` | ✅ 已覆盖 |
| 桌面壳面包屑 | `shell/Shell.tsx` 的 `<Breadcrumb>` | ✅ 已覆盖（薄壳里不画这一行，base 按浏览器形态画） |

仍未覆盖的：

| 区域 | 真实前端来源 | base |
|------|------------|------|
| 添加项目向导（多步：选机器／填 origin／落点校验） | `projects/AddProjectWizard.tsx` | ❌ 未覆盖（底部图标区只有入口） |
| 终端 tab 与文件 tab 的内部形态 | `workbench/{TerminalTab,FileTab}.tsx` | ❌ 未覆盖（tab 条有，内容只画了 TUI tab） |
| 设置页三块的字段级形态 | `settings/SettingsPage.tsx` | ⚠️ 页在，内容为占位，确认状态「未确认」 |

**刷新时机**：本项目不设常设的同步机制（B131 评估后明确不做）。约定是——
**要 brainstorm 前端形态之前跑一次刷新**，而不是定期刷。理由是漂移的代价按次收，
而这份资产每周只用一两次，维持一套「什么时候刷、由谁刷、怎么判定漂移」的流程
比它要防的问题更贵。

## 同级目录的状态

- `desktop-console/` —— **已退役**（2026-08-18，B106），历史资产，**不是形态权威**。
  与真实前端已知三处不符，详见该目录 `AGENTS.md` 顶部的退役说明。
- `w4b-timeline/` —— 功能 fork 副本，非基准。

注：`prototypes/.gitignore` 已声明「只有 `base/` 入库，其余子目录是 fork 副本不入库」。
上面两个目录是该声明之前就跟踪进来的遗留文件，**只做状态标注、不取消跟踪**——
`desktop-console/AGENTS.md` 里有「Confirmed product and visual decisions」的决策记录，
untrack 会把它埋进历史，考古成本反而更高。
