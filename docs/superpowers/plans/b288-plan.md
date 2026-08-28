# B288 实现计划：工作台五处修整 + 顶部 chrome

读者：零上下文执行者。工作目录：本分支工作树。spec：`docs/superpowers/specs/b288.md`
（先读，含硬规则与 Out of Scope）。**样式真源是两份原型文件，数值一律对照移植，
本计划不复述像素值**（声明见「占位符扫描」节）。

- 命令（都在 `web/` 下跑，包管理器是 **npm**，锁文件 package-lock.json）：
  单文件测试 `npx vitest run src/app/...`；全量 `npm test`；类型 `npm run typecheck`。
  基线（commit 0145fa6dd）上这两条全绿，每个 task 动手前先跑对应单文件确认绿，
  再写红测试。依赖未装时先 `npm install`。
- 提交纪律：每个 task 一个 commit，消息 `feat(B288): <task 标题>` 或
  `test(B288): ...`；红绿 task 先测试后实现同一提交。
- 禁改：`prototypes/`、`DesktopTitleBar.tsx` 样式、后端 Go 代码、wire 类型。
- 全部新 UI 文案中文，与现状一致。

## Consumes / Produces 总表（跨 task 签名，逐字一致）

```ts
// 新增 web/src/app/lib/taskName.ts
export function taskDisplayName(t: { name: string; plan_summary: string }): string
// 返回 t.name，空则 t.plan_summary，再空则 '（无名称）'

// 修改 web/src/app/workbench/tabs.ts（T1）
export function tabTitle(
  c: TabContent,
  baseLabel: string,
  taskName?: (taskId: string) => string | undefined,
): string
// tui：taskName?.(c.taskId) 非空 → 原样返回；否则回退 `TUI · ${c.taskId.slice(0, 8)}`

// 修改 web/src/app/tree/archived.ts（T6a）
export function archivedKey(projectID: string): string          // 参数从两个变一个
export function archivedTasks(tasks: Task[]): Map<string, Task[]> // 不再收 tree 参数
// Map 键=projectID，值=该项目全部 isTerminalState 任务（保持任务流原顺序）

// 修改 web/src/app/workbench/useWorkbench.ts（T7）
focusTab: (b: BaseDir, group: number, tabId: string) => void
// 实现：mutate((w) => activateTab(w, group, tabId), b) —— 先切基准再激活，一次事件

// 修改 web/src/app/workbench/TabBar.tsx（T2）
TabBarProps 新增 taskName?: (taskId: string) => string | undefined

// 修改 web/src/app/tree/ProjectTree.tsx（T6b）ProjectTreeProps 新增
openItems: Array<{
  key: string          // `${baseKey}\x1f${tabId}`
  kind: 'tui' | 'terminal' | 'file'
  name: string         // 展示名（tui=任务原名，terminal/file=tabTitle 结果）
  taskId?: string      // kind==='tui' 时必填
  machine: string      // '' = 本机
  base: BaseDir
  group: number
  tabId: string
}>
focusedTaskId: string | null      // 焦点窗格是 tui 时的 taskId，否则 null
onFocusOpenItem: (item: OpenItem) => void
onOpenTerminalAt: (base: BaseDir) => void

// 修改 web/src/app/shell/Breadcrumb.tsx（T3）
export function breadcrumbSegments(base: BaseDir, tail?: string): string[]
// tail 非空时**替换**第三段（目录名），否则维持 [projectName, machine|'本机', label]
```

---

## T1 任务名解析进标题（红绿）

**Files**：新增 `web/src/app/lib/taskName.ts`、`web/src/app/lib/taskName.test.ts`；
改 `web/src/app/workbench/tabs.ts`、`web/src/app/workbench/tabs.test.ts`。

1. 基线复核：`cd web && npx vitest run src/app/workbench/tabs.test.ts` 全绿。
2. 红：tabs.test.ts 追加用例（照抄文件内既有 describe 风格）：
   - `tabTitle({kind:'tui',taskId:'f22ed520abc'}, 'handoff', () => '审 B264')` === `'审 B264'`
   - resolver 返回 `undefined` / 空串 → `'TUI · f22ed520'`
   - 不传 resolver → `'TUI · f22ed520'`（现状回归）
   - terminal/file 用例带上 resolver，标题与不带时逐字相等（不回归）
3. taskName.test.ts（纯函数表驱动）：`{name:'审 B264', plan_summary:'x'}` → `'审 B264'`；
   `{name:'', plan_summary:'摘要'}` → `'摘要'`；两个都空 → `'（无名称）'`。
4. 绿：按 Consumes 总表实现。tabTitle 的 tui 分支是唯一改动点；
   `web/src/app/lib/taskName.ts` 文件头写职责+边界（不认识 React、不做数据获取）。
5. 范围：只跑上述两个测试文件。提交。

## T2 TabBar chrome 1:1 + 类型图标 + 标题注入

**Files**：`web/src/app/workbench/TabBar.tsx`、`web/src/app/workbench/WorkbenchPage.tsx`、
新增 `web/src/app/workbench/TabBar.test.tsx`；资产复制
`prototypes/base/assets/dispatch-task.png` → `web/src/assets/dispatch-task.png`。

1. 资产：`mkdir -p web/src/assets && cp prototypes/base/assets/dispatch-task.png web/src/assets/`；
   TabBar 里 `import dispatchTaskUrl from '../../assets/dispatch-task.png'`
   （Vite 原生支持 png import，基线上 FileTab/其他处已有图片导入先例，typecheck 会证）。
2. 样式移植（**打开 `prototypes/base/pages/project-tree-option-1.html` 对照
   `.workspace-tabbar` / `.workspace-tab` / `.workspace-tab-surface` /
   `.workspace-tab-close` / `.workspace-tab-add` 的 CSS 逐条移植为 Tailwind
   arbitrary values**）：容器高度、底色、水平滚动隐藏滚动条、tab 内边距与
   9px 间距、激活 tab 的加粗与药丸面（圆角、底色、`calc(100%-12px)` 高、
   内层 9px 内边距）、tab 间 20px 高 1px 短分隔线（原型是 ::after 伪元素——
   Tailwind 行内做不了伪元素，**改为 tab 之间的真实 `<span>` 兄弟元素**，
   `data-testid="tab-sep"`，非激活 tab 后才渲染，行尾 + 钮前没有）、
   关闭钮颜色与 hover、+ 钮宽度与 hover。字号字重逐条对照。
3. 图标映射：`tui` → `<img src={dispatchTaskUrl} className="size-[15px]" alt="" />`；
   `terminal` → lucide `Terminal`；`file` → lucide `FileText`；`blank` → lucide `Plus`。
   图标外包 `<span className="tab-icon" aria-hidden>` 结构对应原型的 15px 槽位。
4. 标题：`tabTitle(t.content, baseLabel, taskName)`；props 增加透传的
   `taskName`（WorkbenchPage 用 `tasks` 构建：
   `const taskName = useCallback((id: string) => { const t = tasks.find(x => x.id === id); return t ? taskDisplayName(t) : undefined }, [tasks])`）。
5. 行为保留（逐条，不许丢）：`role="tablist"`、`role="tab"` + `aria-selected`、
   关闭钮 `aria-label={'关闭 ' + title}`、mousedown `preventDefault`（TUI 焦点保护，
   照抄现状注释）、+ 的 `IconMenu` 菜单与全部菜单项、分屏钮（`ml-auto` 保留，
   按 + 钮同款样式重绘、`disabled` 语义保留）、激活切换回调。
6. 红→绿（新文件 TabBar.test.tsx，setup 照抄 `WorkbenchPage.test.tsx` 的
   render/base 构造方式，声明见占位符扫描节）断言逐条：
   - tui tab 显示任务原名（经 taskName resolver），无 resolver 时回退 `TUI ·` 前缀
   - tui 激活 tab 渲染 `img`（src 含 `dispatch-task`）；terminal tab 渲染终端图标
     （lucide 类名断言）；file tab 渲染文件图标
   - 激活 tab 存在 `data-testid="tab-surface"` 药丸元素，非激活 tab 没有
   - 3 个 tab 时 `tab-sep` 数量为 2
   - 关闭钮与分屏钮存在、aria-label 正确
7. 范围：TabBar.test.tsx + WorkbenchPage.test.tsx（回归）。日志/注释：TabBar
   文件头更新职责描述；标题回退逻辑注释说明为什么 resolver 可缺席。
   提交。

## T3 面包屑 chrome + 第三段跟焦点内容

**Files**：`web/src/app/shell/Breadcrumb.tsx`、`web/src/app/shell/Shell.tsx`、
`web/src/app/shell/Shell.test.tsx`（或新增 Breadcrumb.test.tsx，若现状无则新建）。

1. `breadcrumbSegments(base, tail?)`：tail 非空时返回
   `[projectName, machine|'本机', tail]`，否则现状三段。DesktopTitleBar 调用处
   **不传 tail**（薄壳不动）。
2. 行渲染 1:1 `workspace-context`（对照原型 CSS）：28px 高、13px、行高 1、
   `#7c7c7c`、白底、下边框、`0 18px` 内边距、单行省略、整行 `title` 属性带全文；
   段间分隔从 ChevronRight 图标改为**字面 `' / '` 文本**（原型如此）。
   `tone='titlebar'` 分支逐字不动。
3. Shell 计算 tail：`wb.base` 存在时取 `wb.groups[wb.active]` 的 activeId 对应 tab，
   content 非 blank 则 `tabTitle(content, base.label, taskName)`（与 T2 同一个
   resolver，提到 Shell 层构建向下传），blank 或无激活 tab 则 tail 不传。
4. 零交互红线：整行不新增任何可点元素（现状 `Breadcrumb.tsx:6-14` 注释是承重
   约束，保持）。
5. 断言逐条：`breadcrumbSegments` 三段/tail 替换单测；渲染测试断言 `' / '` 分隔
   文本存在、tail 出现、title 属性含全文。
6. 范围：Breadcrumb 相关测试文件 + Shell.test.tsx 回归。提交。

## T4 拖放落点 1:1 + 拖动期事件畅通（红绿）

**Files**：`web/src/app/workbench/WorkbenchPage.tsx`、`WorkbenchPage.test.tsx`、
`web/src/app/tree/ProjectTree.tsx`（只加 data 属性）。

1. 基线复核：WorkbenchPage.test.tsx 里既有的拖放用例（`drop-left/right/center`
   testid）全绿。
2. `dragging` 状态：组件内 `useState`；`useEffect` 挂 window 三监听：
   - `dragstart`：`(e.target as HTMLElement | null)?.closest?.('[data-drag-task]')`
     非空 → `setDragging(true)`
   - `dragend` 与 `drop`：`setDragging(false)`
   清理函数照常规。jsdom 可直接 `dispatchEvent(new Event('dragstart', {bubbles:true}))`
   （target 上带 data 属性的元素）。
3. 内容层放行：每个窗格的内容容器（`relative min-h-0 flex-1 overflow-hidden` 那层）
   在 `dragging` 为真时追加 `pointer-events-none`——对应原型
   `body.dragging .pane-body { pointer-events: none }`，杜绝 xterm canvas 吃事件。
4. 落点视觉 1:1（对照原型 `.pane.drop-*::after` 六条规则，数值逐条照抄）：
   - left：`absolute inset-y-0 left-0 w-1/2` + 半区底色 `rgba(37,99,235,0.32)` +
     内边条 `shadow-[inset_4px_0_0_#2563eb]`
   - right：镜像（`right-0` + `shadow-[inset_-4px_0_0_#2563eb]`）
   - center：`absolute inset-[18%]` + 2px `#2563eb` 描边（`outline outline-2 outline-[#2563eb]`）+
     底色 `rgba(37,99,235,0.18)`
   - 预览层 z 提到 `z-20`，自身保持 `pointer-events-none`
   - `data-testid={`drop-${zone}`}` 保留（既有测试依赖）
5. ProjectTree.tsx 三处任务行（普通任务行、已结束子行、未归属行——grep
   `DRAG_TASK_MIME` 的 dragstart 源）的可拖按钮加 `data-drag-task="1"`。
6. 红→绿断言逐条：dragstart 带属性元素 → 内容容器含 `pointer-events-none`；
   dragend 后类名消失；三种 zone 的预览类含对应任意值类
   （`bg-[rgba(37,99,235,0.32)]` / `inset-[18%]`）。
7. 范围：WorkbenchPage.test.tsx。提交。

## T5 列宽不横滑（红绿）

**Files**：`web/src/app/workbench/WorkbenchPage.tsx`、`web/src/app/workbench/tabs.ts`、
`tabs.test.ts`。

1. 红：tabs.test.ts 新用例——sizes `[1,1]`、minRatio 使 `min*2 > left+right` 仍能
   分配（如 delta 把一栏压到 0.05、min=0.45）：现状早退返回原对象（断言会变），
   新行为返回**重分配后的 sizes**（两栏都被夹到可行解，不抛错、长度不变）。
2. 绿：删掉 `resizeGroups` 里 `if (min * 2 > left + right) return wb` 早退分支
   （现状 `tabs.ts:349`），保留两条单侧夹紧；同步改那段「交给容器横向滚动」的
   注释为「交给容器压缩（overflow 被裁掉，横滚不存在）」。
3. WorkbenchPage 最外容器（`relative flex h-full min-h-0 bg-border`）加
   `overflow-hidden`——横滚从容器层面不存在；列的 `flexGrow/ flexBasis:0 /
   min-w-0` 机制不动。
4. 断言：容器类含 `overflow-hidden`（WorkbenchPage.test.tsx）；T5 的纯函数用例绿。
5. 范围：tabs.test.ts + WorkbenchPage.test.tsx。提交。

## T6a 「已结束」口径：项目内全部终态（红绿）

**Files**：`web/src/app/tree/archived.ts`、新增 `web/src/app/tree/archived.test.ts`、
`web/src/app/tree/ProjectTree.tsx`（调用点随签名更新，结构改造在 T6b）。

1. 红：archived.test.ts 表驱动（夹具照抄 ProjectTree.test.tsx 里现成的
   tree/tasks 构造，声明见占位符扫描节）：
   - completed 任务 work_dir 在树上 → 收进 Map（现状不收——红）
   - completed 任务 work_dir 不在（orphan）→ 收进（现状回归）
   - failed 同收；running/waiting_* 不收；project_id==='' 不收
   - 同项目两台机器的终态归进同一个键；值内顺序 = 任务流原顺序
   - 键值经 `archivedKey(projectID)` 一致
2. 绿：按 Consumes 总表改签名与实现；`archived.ts` 文件头注释同步重写
  （为什么不再看目录：口径改为「任务组只留未终态 + 已打开」，终态一律进已结束，
   不论目录在不在）；`ARCHIVED_LABEL` / `ARCHIVED_TITLE` 保留，
   TITLE 文案改为「已完成 / 已失败的任务」。
3. ProjectTree 现调用点（`archived.get(aKey)`、`archivedKey(project_id, machine)`）
   更新为新签名；机器维度的分组渲染留到 T6b 一起动。
4. 范围：archived.test.ts + ProjectTree.test.tsx 回归。提交。

## T6b 左栏层级重绘（本计划最大 task）

**Files**：`web/src/app/tree/ProjectTree.tsx`（主体）、`ProjectTree.test.tsx`（大改）、
`web/src/app/shell/Shell.tsx`（新 props 接线与 T7 合并做）、
资产 `prototypes/base/assets/dispatch-task.png` 已在 T2 复制，直接 import。

**样式真源**：结构对照 `prototypes/b288-workbench-ux/index.html` 的 `renderTree()`
（项目头 → 「任务」peer-group（live 行 + 已结束行）→「目录」peer-group（机器行 →
工作树子行））；行样式与顶部小标题对照 `prototypes/base/pages/project-tree-option-1.html`
的左栏 CSS（`.proj-*` / `.sec-head` / 任务行 / `.mach-*` / 子行 / `is-open` /
`is-selected`）。**数值逐条对照移植，两份原型冲突时以 option-1 为准。**

渲染结构（自上而下，替换现有 机器行→目录行→任务行 嵌套）：

1. 项目行：现有按钮改造——折叠箭头移到行**右侧**（计数之后，原型 `chev` 位置），
   项目名加粗；右侧簇 = 进行中计数（`countsForProject` 的 running+pending，
   带 dispatch-task 图标，样式对照 option-1 项目行）+ 既有 `+`（添加项目）与
   `TreePrefsMenu`。`data-testid="project-count"` 保留。
2. 「任务」小标题行（原型 sec-head；`data-testid="task-group-head"`）。
3. 任务组行序：**已打开项在前**（Shell 给的 `openItems` 顺序），其后是非终态且
   未打开的任务（任务流原序）。同一任务既打开又非终态 → 只出现一次（open 行）。
   行构成对照原型方案 2：类型图标槽（tui/打开任务 → dispatch-task.png；打开终端 →
   Terminal；打开文件 → FileText；未打开的普通任务 → dispatch-task.png）+ 状态点
  （`StateDot`，tone 用现有 `stateTone`）+ 名称 + 右侧机器簇（绿点 `StateDot
   tone='active'` + 机器名，`''`→「本机」）。搜索过滤任务名与机器名（谓词与
   `search.ts` 的任务匹配一致——`filterTree` 内部谓词若未导出则导出复用，
   Produce：`export function taskMatchesQuery(t: Task, q: string): boolean`）。
   行点击：open 项 → `onFocusOpenItem(item)`；普通任务 → `onOpenTask(base, t.id)`
   （base 解析沿现状 `findBaseOfTask` 链）。行可拖拽，dragstart 照现状写入
   `DRAG_TASK_MIME` + `DRAG_BASE_MIME`，并带 `data-drag-task="1"`（T4）。
   双态：`is-open`（在 openItems 中）与 `is-selected`（`t.id === focusedTaskId`），
   样式对照 option-1 两个类的底色差。`data-testid="open-item-row"` / 行内
   `data-testid="open-item-name"`。
4. 「已结束」行：任务组尾部（`aTasks` 来自 T6a 新口径，项目维度），右侧计数 +
   箭头在右；默认收起、搜索命中自动展开、`prefs.hideArchived` 语义保留；
   子行 = 终态任务行（同上构成，机器名在右），`data-testid="archived-row"` 保留。
5. 「目录」小标题行（`data-testid="dir-group-head"`）。机器行：绿点（可达性——
   `locationProblem` 为空才绿，断连时红点 + 既有「已断开」徽标与原因行保留）+
   机器名 + 右侧箭头（在**右**）+ 悬停动作簇（对照原型 hover-actions）：终端钮
   （Terminal 图标，`title="打开主目录终端"`，主目录不存在时隐藏）→
   `onOpenTerminalAt(workspaceBase(project, machine, mainWs))`；新建工作树钮
   （Plus 图标，走既有 `setWorktreeTarget`）。既有右键菜单原样挂机器行。
   `data-testid="machine-row"` 保留。
6. 机器展开 → 工作树子行（缩进、紧凑；对照 option-1 目录子行）：`sortWorkspaces`
   与 `splitIdleWorkspaces`（含「已隐藏 N 个目录」行）原样保留；子行点击 =
   `onSelectDir(base)` 选中（选中态样式对照 option-1 `is-selected`）；子行悬停
   终端钮 → `onOpenTerminalAt(base)`。子行不再列任务（任务已上移任务组）。
   `data-testid="workspace-row"`、`hidden-dirs-row` 保留。
7. 未归属分组：保留在树尾，行构成并入第 3 条的任务行样式。空态文案保留。
8. 功能保留清单逐条自检（搜索/⌘K、偏好、右键、dock、未归属、断连原因、拖拽、
   确认弹层、建树弹层）——这些的测试若因结构迁移断言失效，按新结构改写断言，
   不许删测试躲重构。
9. 测试（ProjectTree.test.tsx 改写，setup 照抄该文件现有 renderProjectTree 助手）：
   - 层级：项目行存在且箭头在计数之后（DOM 顺序断言）；「任务」「目录」两个
     小标题行存在
   - 任务组：未终态任务出现；completed/failed 不在任务组、只在已结束子行；
     已结束默认收起（子行不在文档流）、点击展开、`archived-row` 计数正确
   - 已打开项：openItems 渲染在前、名字用注入的 name、点击触发
     onFocusOpenItem(item)、tui 打开项与同 id 任务只一行且带 is-open 态
   - 焦点态：focusedTaskId 命中行带 is-selected
   - 机器行：悬停动作两钮存在性（主目录存在/不存在两 case）、断连机器保留
     「已断开」徽标与原因文本、右键菜单仍弹
   - 工作树子行：点击 onSelectDir、隐藏目录行保留、idle 折叠保留
   - 搜索：命中任务名/机器名过滤、全空空态文案、⌘K 聚焦（既有用例不回归）
10. 范围：ProjectTree.test.tsx + archived.test.ts。日志/注释：ProjectTree 文件头
    重写层级说明（项目 → 任务组/目录组，任务不再挂目录下，为什么）；新回调
    props 各一行注释。提交。

## T7 Shell 接线 + focusTab

**Files**：`web/src/app/workbench/useWorkbench.ts`、`web/src/app/shell/Shell.tsx`、
`Shell.test.tsx`、`WorkbenchPage.tsx`（taskName resolver 上移 Shell 的收尾）。

1. `focusTab`：按 Consumes 总表实现（接口注释写清为什么存在：open 的去重键对
   无会话终端是 null，`open` 会开出第二个终端而不是聚焦，所以聚焦必须走
   activateTab 语义）。
2. Shell 构建 `openItems`（useMemo，依赖 `wb.byBase`/`baseDirs`/`wb.base`/`tasks`）：
   遍历 `Object.entries(byBase)`——**当前基准在最前**（保持组序、组内 tab 序），
   其余按对象键序；每个 tab 产一行；`name`：tui → `taskDisplayName(task)`
   （tasks 里找不到 → `TUI · id8`），terminal/file → `tabTitle(content, base.label,
   taskName)`；`machine: base.machine`。
3. `focusedTaskId`：`wb.base` → `wb.groups[wb.active]` → activeId 的 tab，
   content.kind==='tui' → taskId，否则 null。
4. `onFocusOpenItem`：`wb.focusTab(item.base, item.group, item.tabId)`；
   `onOpenTerminalAt`：`wb.openTerminal(base)`。两个回调各一行注释。
5. T2 的 taskName resolver 从 WorkbenchPage 上移到 Shell 构建、经 props 下传
   （WorkbenchPage 保留对 TabBar 的透传），T3 的 tail 用同一个 resolver。
6. 断言（Shell.test.tsx，setup 照抄该文件现有 harness）：openItems 传给 ProjectTree
   的内容与顺序（当前基准在前）；focusTab 点击链路（渲染树里点 open 行 →
   激活的 tab 变化）；面包屑 tail 随激活 tab 变化。
7. 范围：Shell.test.tsx + useWorkbench 相关（persist/restore 回归）。提交。

## T8 收尾全量

`cd web && npm test && npm run typecheck && npm run lint` 全绿；`npm run build` 成功。
逐条过一遍「功能保留清单」。分批未竟事项清零后，最终提交（如有零星修复）。

## 五项检查结论

1. **缺陷族**：拖放（dragging 状态泄漏 → dragend/drop 双保险 + 组件卸载清理）；
   已结束（空 Map 不渲染分组行——沿用现状「取不到就不渲染」）；命名（任务被删
   后 resolver 返回 undefined → 回退现状格式，不抛错）；resize 极端容器（夹紧后
   允许低于 MIN_PANE，不再早退，单测锁）；⌘K 与 xterm 焦点让位逻辑不动。
2. **序列化边界**：本卡零新增持久化/wire 字段（树 props 是 React 内存对象）；
   persist/restore 既有 roundtrip 测试不回归即可，无新增投影。
3. **上下文预算**：每个 task 圈定 ≤4 个源文件 + 对应测试，T6b 最大
   （ProjectTree.tsx 928 行 + 其测试），独立成 task。
4. **类型标注**：web 前端，无边界型子系统 task；真机清单统一放验收节。
5. **接缝覆盖**：spec 六缝 → T1(tabTitle)、T6a(archivedTasks)、T4(dropZoneAt
   消费面)、T2(TabBar)、T6b(ProjectTree)、T3(breadcrumbSegments)——每缝至少一支
   缝级断言（上文各红绿/断言条目），每支测试入口都在缝上；无内部锁。

## 占位符扫描（声明）

- 组件测试（T2/T3/T4/T6b/T7）未贴完整 JSX 断言代码：**套用「照抄既有 harness」
  正当出口**——T2 照抄 `WorkbenchPage.test.tsx`、T6b 照抄 `ProjectTree.test.tsx`、
  T7 照抄 `Shell.test.tsx` 的渲染 setup；每支测试的断言已逐条列全（上文编号），
  每条可独立判 pass/fail。纯函数缝（T1/T5/T6a）的用例给了完整输入输出。
- 样式数值不写入本计划：**指认拷贝源**（两份原型文件的对应 CSS 选择器），
  spec 硬规则「一比一对照原型代码」即数值真源，这是本卡的刻意设计而非缺口。

## 派发前自审

- 无「由协调者执行」的验收步骤混入实现 task；真机对照（截图 vs 原型）是验收
  节点动作，不在本计划 task 内。

---

## 修订（2026-08-29，T2 执行轮裁决）

T2 的「内容 tab 条」实现被协调者否决：卡基线（B281/B285 后）中央模型已是
组/列/格（基线 tabs.ts：组标签条、列内两格、五区 DropZone、组拖动 DRAG_GROUP_MIME、
新建/关闭组），plan 初版把 main 上的旧形态当成了基线。T2 重做为：**组标签条换皮**——

- TabBar 恢复基线 props（groups / activeGroupId / onActivateGroup / onCloseGroup /
  onNew / onNewLauncher / launchers / terminalUnavailable / onNewGroup / onMoveGroup），
  另加 `taskName` resolver。
- 样式 1:1 option-1 chrome：44px 条、激活组药丸面、非末尾组后的短分隔线、
  每组关闭钮、行尾 + = `onNewGroup`（基线语义）、组拖动（DRAG_GROUP_MIME 的
  dragstart/dragover/drop 处理器原样保留）。
- 图标按组焦点内容种类：tui→dispatch-task.png、terminal→Terminal、file→FileText、
  空组→Plus。
- 问题 1 的落点补全：autoName 组的命名走 taskDisplayName（tui 焦点任务原名），
  解析不到才回退 TUI·id8——组名、窗格头、左栏已打开行同源一致。
- T4 落点样式补全 top/bottom 两区（五区全部 1:1 原型）。
- 恢复被删的 launchers.test 组拖动用例及其它组语义测试（以
  `git show f770304b0:<file>` 为基线参照，仅样式断言按新皮改写）。
- T1/T3/T5/T6a/T6b/T7 的产出保留；T7 的 focusTab/openItems 对齐组模型语义
  （聚焦 = 激活所在组 + 焦点该格）。
