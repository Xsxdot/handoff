# 控制台：目录行排序 + 三条新入口

日期：2026-08-17
分支：`claude/tui-feature-optimization-398a03`
状态：已评审通过，待实现

## 0. 这份 spec 解决什么

W4 控制台上线后暴露的四个形态问题，彼此独立，合成一份是因为它们都落在同一批文件上（左栏树 / 中央工作台 / 右下角浮窗），分开做会互相踩。

| # | 问题 | 现状 |
|---|------|------|
| 1 | 左栏目录行顺序是 `git worktree list` 的原始序，与"我现在该看哪个"无关 | [`ProjectTree.tsx:417`](../../../web/src/app/tree/ProjectTree.tsx) 直接 `loc.workspaces.map` |
| 2 | 打开任务 TUI 只有左栏点击一条路 | 空白 tab 选"打开任务 TUI"后只给一句指路文案 |
| 3 | 空白 tab 的"打开文件"点了不干事（只指路），而真正缺的是"新建文件" | [`WorkbenchPage.tsx:45`](../../../web/src/app/workbench/WorkbenchPage.tsx) 的 `PICK_HINT` |
| 4 | 右下角悬浮框只能开终端，没有随手记东西的地方 | [`HomeDock.tsx`](../../../web/src/app/homedock/HomeDock.tsx) |

四项都要动前端，其中 1 和 4 各需要一处后端改动。

### 非目标

- 不动看板弹层、工单弹层、设置页
- 不改 `/api/tasks/{id}/file`（CLI `handoff fetch` 的既有契约）
- 不做文件模糊搜索选择器（右栏文件树已是文件选择器，且它一直在屏上）
- 不支持把目录行拖进中央区（"基准目录全局唯一"这条不变式的代价太大，见 §3.4）

---

## 1. 目录行排序

### 1.1 排序键

目录行（`Workspace`）在其机器节点下按**三级降序**排列：

```
工单数 ↓ → 任务数 ↓ → 创建时间 ↓ → path ↑
```

末位的 `path` 升序不是排序意图，是**稳定性兜底**：前三个键全等时若不给确定次序，`Array.prototype.sort` 在不同引擎上的结果可能不同，行会随每次 2.5s 任务流心跳无缘无故重排。

**主工作树（`is_main`）恒排第一，不参与上述排序。** 它不是一个任务分支，是这个项目在这台机器上的家。让它被别的分支的工单顶下去，用户对"仓库主目录在第一行"的肌肉记忆当场失效，而这条记忆的价值高于"主目录也参与排序"带来的信息量。

### 1.2 计数口径

| 键 | 口径 |
|----|------|
| 工单数 | 该目录下 `waiting_answer` 任务的 `pending_tickets` 条数之和 |
| 任务数 | 该目录下 `running` + `waiting_answer` + `waiting_review` 的任务条数 |
| 创建时间 | `Workspace.created_at`（新增，见 §1.3） |

任务归集到目录用现有的 [`tasksOfWorkspace`](../../../web/src/app/tree/ProjectTree.tsx)：`work_dir` 为空串表示原地模式，归到 `is_main` 目录。**工单归集必须用同一条口径**，否则同一个任务的工单和任务数会落在不同的行上。

任务数刻意不含已结束任务：排序要回答的是"我现在该看哪个"，历史堆积会让一个跑完 30 个任务的老分支永远压在第一位。

### 1.3 后端：`Workspace.created_at`

`proto.Workspace` 新增字段：

```go
// CreatedAt 是这个工作树被建出来的时间。零值 = 取不到（见下）。
//
// 取法分两种：
//   - 主工作树：stat <path>/.git
//   - 链接工作树：stat <主仓库 git 公共目录>/worktrees/<名>/gitdir
//
// 为什么链接工作树不 stat 工作树目录本身：那个目录的 mtime 会随着往里写代码
// 变化，排出来的是"最近动过"而不是"什么时候建的"。gitdir 这个文件由
// git worktree add 写一次之后就不再动，是唯一稳定的创建时间证据。
//
// 取不到时留零值而不是报错：整棵项目树不该因为一个 stat 失败就 500，
// 前端把零值当"最旧"处理即可（spec §8 的诚实展示在这一层的兑现）。
CreatedAt time.Time `json:"created_at"`
```

实现落在 [`probeWorkspaces`](../../../internal/agentd/workspaceprobe.go)：`git worktree list --porcelain` 已经给出每个工作树的 path 与是否主工作树，在组装 `proto.Workspace` 时补一次 stat。stat 失败只 Debug 不 Warn——探测期间工作树被 `git worktree remove` 掉是正常竞态。

**契约三处必须同步改**，缺一处对应侧的契约测试就会红：

1. `internal/proto/projects.go` 的结构体
2. `web/src/api/types.ts` 的 `Workspace` 接口
3. `web/src/api/testdata/*.json`（由 `TestContractFixtures -update` 生成，**不手写**）

### 1.4 前端

新纯函数文件 `web/src/app/tree/sortWorkspaces.ts`：

```ts
export interface WorkspaceMetrics {
  tickets: number
  tasks: number
  createdAt: string   // RFC3339Nano；空串 = 取不到，视为最旧
}

// sortWorkspaces 返回排好序的新数组，不改入参。
export function sortWorkspaces(
  list: Workspace[],
  metricsOf: (ws: Workspace) => WorkspaceMetrics,
): Workspace[]
```

把 metrics 做成回调而不是让这个函数自己去算，是为了让它能在测试里用手写数字驱动，不需要造一整棵项目树加一批任务。

工单表由 `useGlobalTickets` 派生。它现在返回 `items: {ticket, task}[]`，新增：

```ts
// byWorkDir 是「目录绝对路径 → 挂起工单张数」。
// 空 work_dir 的任务不进这张表——它们归主目录，而这里不知道谁是主目录。
// 归集主目录那一步由 ProjectTree 做（它手上有 ws.is_main）。
byWorkDir: Map<string, number>
```

`ProjectTree` 新增 prop `ticketsByDir: Map<string, number>`，由 Shell 从 `tickets.byWorkDir` 传下去。

### 1.5 排序不改的东西

- 项目行、机器行顺序保持后端返回序（稳定，不随任务状态跳）
- "已结束"分组仍钉在该机器节点的最后
- "未归属"分组仍钉在整棵树的最后
- 搜索过滤后仍走同一套排序（`filtered.projects` 的目录也排）

---

## 2. 任务选择器弹层

### 2.1 与看板的分工

看板（`BoardOverlay`）是**纵览全局**：全屏、按状态分栏、带筛选条。选择器是**给当前这个 tab 选一个**：小弹层、一个搜索框、一个列表。两者都能开 TUI，但意图不同，共用一个会让"我只是想在这个 tab 里开个任务"变成一次全屏导航。

看板保持一字不改。

### 2.2 组件

新文件 `web/src/app/workbench/TaskPickerDialog.tsx`。

**放 workbench/ 而不是 overlay/**：它的生命周期属于一个具体的 tab（选完就变成那个 tab 的内容），而 overlay/ 下的两个弹层是 Shell 级的全局弹层。

```ts
export interface TaskPickerDialogProps {
  open: boolean
  base: BaseDir           // 当前基准，用于定范围
  tree: ProjectTreeResp   // 用于把 base 解析回它所属的 project_id
  tasks: Task[]           // 全量任务流
  onPick: (taskId: string) => void
  onClose: () => void
}
```

**范围**：`base` 所属项目的全部任务，跨机器，含已结束。解析路径是 `base.path → 找到包含它的 location → project_id`，然后 `tasks.filter(t => t.project_id === thatId)`。

**布局**：
- 顶部搜索框，按任务名 / `plan_summary` 子串过滤（与左栏搜索同一口径：`toLowerCase().includes`）
- 「进行中」区：非终态任务，按 `updated_at` 倒序
- 「已结束」区：终态任务，按 `updated_at` 倒序，默认**折叠**，标题上带条数

已结束默认折叠的理由与左栏"已结束"分组一致：历史堆积（实测单机 60 条）会把正在做的活挤出视口。

**每行显示**：状态圆点（复用 `StateDot` + `stateTone`）、任务名、所在分支/目录短名。带上目录短名是必需的——这个弹层的存在理由就是"打开**别的分支**的任务"，不显示分支等于让用户在一堆同名任务里猜。

**空态**：「这个项目下还没有任务」。不显示一个空列表——空白会被当成加载失败。

**键盘**：Esc 关闭，↑↓ 移动选中项，Enter 确认。与既有弹层（`Overlay.tsx`）的 Esc 行为一致。

### 2.3 接线

`BlankTab` 的 `onPick('tui')` 不再进 `awaiting` 状态，改为让 `WorkbenchPage` 开这个弹层，记下是哪个 tab 在等：

```ts
// picking 记「哪个空白 tab 正在选任务」。null = 弹层关闭。
const [picking, setPicking] = useState<{ group: number; tabId: string } | null>(null)
```

选中后 `api.setContent(group, tabId, { kind: 'tui', taskId })`——原地把空白 tab 变成 TUI tab，位置与 id 都不动。`setTabContent` 已有的去重分支会处理"这个任务已经在别的 tab 里开着"：激活那个，关掉这个空白 tab。

从空组面板（一个 tab 都没有）选"打开任务"时，先 `api.open({kind:'blank'}, undefined, group)` 开一个空白 tab 承接，再走同一条路——与现在 `startFromEmpty` 的做法一致。

### 2.4 净删除

这个弹层让下面这些东西失去存在理由，一并删掉：

- `WorkbenchPage.PICK_HINT`（整个常量）
- `WorkbenchPage` 的 `awaiting` state 与 `back()` 函数
- `BlankTab` 的 `hint` / `onBack` 两个 prop 及其分支渲染
- 对应的测试断言

那套指路文案存在的唯一理由是"没有选择器"。留着它等于同一件事有两个说法。

---

## 3. 拖拽分屏

### 3.1 拖源

左栏任务行（含「已结束」分组里的、「未归属」分组里的）加 `draggable`，`onDragStart` 写：

```ts
e.dataTransfer.setData('text/handoff-task', task.id)
e.dataTransfer.effectAllowed = 'copy'
```

**自定义 MIME 而不是 `text/plain`**：中央区的投放区只认这一个类型，从别处（浏览器地址栏、桌面文件、编辑器选中文本）拖进来时不会被误判成一次任务拖放。

拖动不影响点击：HTML5 拖放只在真的拖起来之后才吞掉 click。

### 3.2 投放区

`WorkbenchPage` 的每个 `<section>` 内加一层投放覆盖，按鼠标横向位置分三区：

| 区域 | 范围 | 行为 |
|------|------|------|
| 左边缘 | 该栏左侧 25%，且不超过 120px | 在这一栏**左边**插入新栏，任务开在新栏 |
| 右边缘 | 该栏右侧 25%，且不超过 120px | 在这一栏**右边**插入新栏，任务开在新栏 |
| 中间 | 其余 | 在这一栏开一个 tab |

25% 与 120px 两个上限同时生效（取小），因为栏可以被拖得很宽——一个 800px 宽的栏，25% 是 200px 的边缘区，会让"我只是想在这栏开个 tab"频繁误触发分屏。

**已到 `MAX_GROUPS`（3 栏）时边缘区退化成中间区**：不高亮、不分屏，落下去就是在这栏开 tab。不做"置灰 + 提示"——拖放过程中弹一句话没地方放，而一次落空的拖拽比一次"落在了这栏"更让人困惑。这与既有的"分屏按钮到上限置灰"不冲突：按钮是常驻控件，说得起话；投放区是瞬时的。

**视觉反馈**：dragover 时边缘区画一条 3px 的强调色竖条（预示新栏落在哪一侧），中间区给整栏一圈内描边。

### 3.3 前置修复：把组下标从 Shell 的 state 里拔掉

`splitGroup` 现在只能往末尾追加，[`tabs.ts:265`](../../../web/src/app/workbench/tabs.ts) 的注释写明了原因：Shell 的 `closingPty` / `closingDirtyFile` 把 `(组下标, tabId)` 存进了 state，中间插入会让所有后续组的下标 +1，于是确认弹层里存着的下标指向别的栏——点"确认关闭"关掉的是另一栏的 tab。

**修法是拔病根，不是绕开它**：

1. `closingPty` 与 `closingDirtyFile` 删掉 `group` 字段，只留 `tabId`（及各自已有的其他字段）
2. `useWorkbench` 暴露一个 `closeById(tabId: string)`，内部在 `wb.groups` 里按 id 反查所在组再关
3. 确认回调改调 `closeById`

`tabId` 在整个 workbench 内唯一（`nextTabId` 保证），所以反查是确定的。这样组下标就只在一次事件内活着，不再跨越用户思考的时间。

做完这一步才能加：

```ts
// splitGroupAt 在 index 处插入一个空栏并聚焦它；已到 MAX_GROUPS 时原样返回。
// index 会被夹到 [0, groups.length]。
export function splitGroupAt(wb: Workbench, index: number): Workbench

// splitGroup 保留为末尾追加的包装，⌘D 与面包屑分屏按钮的行为一字不变。
export function splitGroup(wb: Workbench): Workbench {
  return splitGroupAt(wb, wb.groups.length)
}
```

`useWorkbench` 相应新增 `splitAt(index: number)`。

### 3.4 跨基准拖放

拖的任务不属于当前选中目录时（拖 A 目录的任务落到显示着 B 目录的工作台上）：**工作台整体切到 A，边缘投放区退化成"在末尾新开一栏"**。

理由：投放时算出的组下标是在 **B 的** tab 组里算的，切到 A 之后 A 有自己的一套组（`byBase` 那张 Map），那个下标已经不指任何东西。硬要保留位置语义就得先切基准、等重渲染、再重新命中投放区——那是两帧之后的事，而拖放在落下的那一刻就要给出结果。

中间区在跨基准时也退化成"开在 A 的焦点组"。

判据是 `targetBase.key !== api.base?.key`。`targetBase` 由拖源在 dragstart 时一并写入 dataTransfer（左栏手上有 `workspaceBase(project, machine, ws)`），中央区不需要反查树。所以 dataTransfer 实际写两个键：

```ts
e.dataTransfer.setData('text/handoff-task', task.id)
e.dataTransfer.setData('text/handoff-base', JSON.stringify(base))  // 未归属任务为 'null'
```

未归属任务（`base` 为 null）落下时按"用当前基准开"处理，与现在点它的行为一致。

---

## 4. 空白 tab：打开文件 → 新建文件

### 4.1 面板

`PICK_ITEMS` 变成：

| 种类 | 标签 | 快捷键 |
|------|------|--------|
| `terminal` | 新终端 | ⌘T |
| `newfile` | 新建文件 | ⌘N |
| `tui` | 打开任务 | ⌘⇧A |

`PickKind` 的 `'file'` 改名为 `'newfile'`——它的语义变了（从"打开一个已有的"变成"造一个新的"），沿用旧名字会让 `hotkeyOf` 那段代码骗人。`hotkeyOf` 里 `⌘⇧O` 那条替换成 `⌘N`（`n` 且不带 shift）。

home 基准下仍只留终端（现有过滤逻辑不变：home 不在文件接口白名单里）。

### 4.2 新建流程

```
fetchWorkspaceDir(base.path, '')        列一次基准目录根
  → 挑第一个不冲突的 untitled-N.md（N 从 1 起）
  → createWorkspaceEntry(base.path, '', name, 'file', base.machine)
  → setContent(group, tabId, { kind: 'file', rel: name })
  → 通知右栏文件树刷新
```

**先列举再命名**，不是"从 1 开始建、撞 409 就 +1"：后者在已经有 untitled-1..9 的目录里会打出 9 个 409，服务端日志里全是拒绝记录，排障时看着像出了故障。

列举之后 `createWorkspaceEntry` **仍可能 409**（另一个客户端同时建了同名文件）。此时不重试，把 agentd 的报错原文原样显示在空白 tab 上——这是真实的并发冲突，用户再点一次就好，静默重试会掩盖"有别人在动这个目录"这个事实。

**文件建在基准目录根**，不是右栏文件树当前展开的目录。理由：空白 tab 不知道右栏的展开状态，而"当前目录"在这个上下文里唯一无歧义的解释就是基准目录。

**扩展名固定 `.md`**：得选一个，`.md` 在编辑器里无害且是记东西最常见的格式。

### 4.3 右栏刷新

`useDirEntries` 已有 `refresh`（[`FileTree.tsx:371`](../../../web/src/app/files/FileTree.tsx) 的刷新按钮在用）。Shell 需要把它提到能被 WorkbenchPage 触发的位置：在 Shell 里持有一个 `fileTreeNonce` state，`FileTree` 收一个 `refreshKey` prop，nonce 变化时重拉。

不做的选择：让 WorkbenchPage 直接持有 FileTree 的 ref。那会把中央区和右栏焊死，而它们现在互不认识。

---

## 5. 悬浮框：新建临时文件

### 5.1 后端：scratch 白名单

**目录**：`<DataDir>/scratch`，agentd 启动时 `MkdirAll`（0700）。

`s.scratchRoot()` 返回该目录的绝对路径；**建目录失败时返回空串并 Warn**，此后闸门那一支恒不命中、`StatusResp.ScratchRoot` 为空、前端入口不渲染。整条链路对"草稿区不可用"是收敛的：没有任何一处会拿着空路径去发请求。agentd 不因为草稿区建不出来就启动失败——那是个附属功能，不该拖垮派发。

**上报**：`proto.StatusResp` 新增 `ScratchRoot string \`json:"scratch_root,omitempty"\``；`proto.Machine` 同样新增，由 `localMachine()` 直填、`probeRemote()` 从对端 StatusResp 投影——与 `pty_supported` / `reveal_supported` 完全同一条路。

`omitempty` + 前端按"缺席 = 这台机器不支持临时文件"处理：老 agentd 不发这个字段，入口不渲染。这与能力位的三态纪律略有不同（那里 `null` 要放行），因为这里缺的不是一个"能不能"的判断，而是一个**路径**——没有路径就没法发请求，放行会得到一次必然 400。

**白名单闸门**：`resolveWorkspace` 开头加一支：

```go
// scratch 草稿区是这道闸门的第二个入口。它不是 git 工作树，也不在
// project_locations 表里，所以下面按登记表比对的两段都命中不了它。
//
// 放在最前面短路：这是一次纯字符串比较，比读一次数据库便宜，而草稿区的
// 请求频率与工作树同量级（浮窗里每敲一次保存就是一次 PUT）。
if root := s.scratchRoot(); root != "" && filepath.Clean(path) == root {
    return root, true
}
```

已核实 `ListDir` 对 git 只是**尽力而为**：`Ignored` 字段查不出来时（目录不是仓库）一律按未忽略返回并打日志（[`projects.go:151`](../../../internal/proto/projects.go) 的字段注释写明了这条）。所以 scratch 不是 git 仓库不会让任何一个现有端点出错，`dir` / `file` / `entry` 六个端点全部可直接复用，**零新端点**。

写文件路径上的既有护栏（`.git` 拒写、符号链接拒写、二进制拒写、1MB 上限、`base_sha256` 前置条件）对 scratch 一体适用，不需要另开一套。

### 5.2 前端

**`HomeTab` 加种类**：

```ts
export interface HomeTab {
  id: string
  kind: 'terminal' | 'file'   // 新增
  // seq 只对 terminal 有意义（标题里的 'bash · home N'）。file tab 仍分配一个
  // 但不显示——让两种 tab 共用一个只增不减的计数器，比给 file 单开一个计数器
  // 再解释"为什么 home 3 后面跟着 home 1"简单。
  seq: number
  sessionId?: string          // 仅 terminal
  rel?: string                // 仅 file：scratch 根下的文件名
  draft?: string              // 仅 file：未保存草稿，与中央 file tab 同理由（切 tab 不丢）
  baseSha?: string            // 仅 file：草稿所基于的那一版哈希
  machine: string
}
```

`useHomeDock` 新增 `newFile(rel: string)`，与 `newTerminal` 同构（建 tab、激活、开浮窗），以及 `setDraft(id, draft, baseSha)` 供 `FileTab` 回写草稿。

草稿必须寄存在 tab 上而不是组件 state 里，理由与中央 file tab 逐字相同：浮窗同时只渲染激活 tab，切走即卸载。

**入口位置：浮窗 tab 条现有 `+` 的旁边，不是 FAB。**

FAB 现在是"一次点击直达终端"，中间那层清单面板是被**特意删掉**的（[`HomeDock.tsx:25`](../../../web/src/app/homedock/HomeDock.tsx) 有完整论证：面板是同一批终端的第二套清单，挡在第一套前面，用户要点两次才拿得到终端）。把"选终端还是选文件"塞回 FAB 就是把那张面板请回来。

改成 tab 条上两个图标：`TerminalSquare` 开终端、`FilePlus` 开临时文件。两个都是一次点击直达。

**命名**：与 §4.2 同一套 `untitled-N.md` 规则，同样先 `fetchWorkspaceDir(scratchRoot, '')` 列举再挑名字。

**关闭**：文件 tab 的 × 干净时直接关——scratch 里的文件**关了还在磁盘上**，下次能再打开。有未保存草稿时才确认。

确认弹层的**文案与 `destructive` 取向复用**中央区那份，但**不能复用 `closingDirtyFile` 这个 state**：§3.3 之后它的确认回调调的是 `wb.closeById`，而浮窗 tab 根本不在 `wb` 里（`useHomeDock` 与 `useWorkbench` 刻意完全独立）。新开一个 `closingDirtyHome: { id: string; rel: string } | null`，确认时调 `dock.closeTab(id)`——与既有的 `closingPty` / `closingHome` 那一对同构（那两个也是因为同一条独立性而分开存在的）。

**恢复**：`usePtyRestore` 不变（它只恢复 PTY 会话）。scratch 文件 tab 刷新即丢，与中央 tab 组一致——文件本身在磁盘上没丢，重开一次即可。

### 5.3 FileTab 需要一个非工作树的 BaseDir

`FileTab` 收 `base: BaseDir`，而 `BaseDir.kind` 现在是 `'workspace' | 'home'`。scratch 两个都不是。

**新增 `'scratch'` 这一支**，并造一个从机器能力位派生的常量：

```ts
// scratchBase 把一台机器的 scratch 根做成 BaseDir，供浮窗里的文件 tab 用。
// 它**不进** useWorkbench 的 byBase：草稿区不是可选中的基准目录，
// 左栏点不到它，面包屑也不显示它。
export function scratchBase(root: string, machine: string): BaseDir
```

新增一支就要审所有分支判断，共三处，全部保持现状即可：

| 位置 | 判断 | scratch 的正确落点 |
|------|------|-------------------|
| `Shell` 渲染右栏 | `base.kind === 'workspace'` | 不渲染（scratch 从不是选中基准，走不到这里） |
| `BlankTab` 过滤项 | `base.kind === 'home'` | 走不到（浮窗不渲染 BlankTab） |
| `BlankTab` 基准提示 | `base.kind === 'home'` | 同上 |

即：`'scratch'` 只在 `FileTab` 内部被用到（它只读 `base.path` 与 `base.machine` 发请求）。**不把它伪装成 `'workspace'`**——那是一句会在半年后骗到人的谎。

---

## 6. 测试

### 6.1 新增纯函数测试

| 文件 | 覆盖 |
|------|------|
| `sortWorkspaces.test.ts` | 三级键各自生效、主工作树置顶、全等时按 path 稳定、空 createdAt 当最旧 |
| `tabs.test.ts`（补充） | `splitGroupAt` 插首/插中/插尾、到上限原样返回、`sizes` 与 `groups` 等长不变式 |
| `useGlobalTickets.test.ts`（补充） | `byWorkDir` 归集正确、空 work_dir 不进表 |

### 6.2 新增组件测试

| 文件 | 覆盖 |
|------|------|
| `TaskPickerDialog.test.tsx` | 范围限于当前项目、已结束默认折叠、搜索过滤、Esc 关闭、空态文案 |
| `BlankTab.test.tsx`（改） | 三项内容、⌘N 触发新建、`hint`/`onBack` 相关断言删除 |
| `WorkbenchPage.test.tsx`（改） | 拖放三个区各自的结果、到上限时边缘退化、跨基准退化 |
| `ProjectTree.test.tsx`（改） | 目录行按新序渲染、主工作树置顶 |
| `HomeDock.test.tsx` / `HomeWindow.test.tsx`（改） | 两个新建图标、文件 tab 标题、脏草稿确认 |

### 6.3 Go 侧

| 文件 | 覆盖 |
|------|------|
| `workspaceprobe_test.go`（补充） | 主/链接工作树各自取到 created_at、stat 失败留零值不报错 |
| `workspacefiles_test.go`（补充） | scratch 根命中白名单、scratch 下建/读/写/列举全通、scratch 外的路径仍被拒 |
| `contract_fixture_test.go` | 跑 `-update` 重新生成 fixture |

### 6.4 真实验收（实现完成后由审核者做）

1. 重启 agentd，控制台打开
2. 左栏：主工作树在第一行；有工单的分支排在无工单的前面
3. 空白 tab：⌘N 建出 `untitled-1.md` 并可编辑保存，右栏文件树立刻看得见
4. 空白 tab：⌘⇧A 弹出选择器，能选到别的分支的任务和已结束的任务
5. 从左栏拖一个任务到中央区右边缘 → 分出新栏并在其中打开
6. 右下角浮窗：新图标建出临时文件，落在 `<DataDir>/scratch/` 下，重启 agentd 后文件还在

---

## 7. 改动清单

### 后端（Go）

| 文件 | 改动 |
|------|------|
| `internal/proto/projects.go` | `Workspace.CreatedAt`；`Machine.ScratchRoot` |
| `internal/proto/status.go` | `StatusResp.ScratchRoot` |
| `internal/agentd/workspaceprobe.go` | 组装 Workspace 时补 stat |
| `internal/agentd/workspacefiles.go` | `resolveWorkspace` 加 scratch 分支；`scratchRoot()` 辅助 |
| `internal/agentd/machines.go` | `localMachine` / `probeRemote` 投影 ScratchRoot |
| `internal/agentd/server.go`（或启动处） | 启动时 MkdirAll scratch |

### 前端（TS）

| 文件 | 改动 |
|------|------|
| `web/src/api/types.ts` | `Workspace.created_at`；`Machine.scratch_root`；`StatusResp.scratch_root` |
| `web/src/app/tree/sortWorkspaces.ts` | **新增** |
| `web/src/app/tree/ProjectTree.tsx` | 目录行排序；任务行 draggable；新 prop |
| `web/src/app/overlay/useGlobalTickets.ts` | `byWorkDir` |
| `web/src/app/workbench/TaskPickerDialog.tsx` | **新增** |
| `web/src/app/workbench/tabs.ts` | `splitGroupAt` |
| `web/src/app/workbench/useWorkbench.ts` | `splitAt`、`closeById` |
| `web/src/app/workbench/WorkbenchPage.tsx` | 投放区；选择器接线；删 PICK_HINT/awaiting |
| `web/src/app/workbench/BlankTab.tsx` | 三项改版；删 hint/onBack |
| `web/src/app/homedock/useHomeDock.ts` | `HomeTab.kind`、`newFile` |
| `web/src/app/homedock/HomeDock.tsx` / `HomeWindow.tsx` | tab 条两个图标；按种类分发 |
| `web/src/app/data/useMachineCaps.ts` | `scratchRoot(machine)` |
| `web/src/app/shell/Shell.tsx` | 组下标拔除；文件树 nonce；新接线 |
| `web/src/app/files/FileTree.tsx` | `refreshKey` prop |

---

## 8. 已知取舍

| 取舍 | 代价 | 为什么接受 |
|------|------|-----------|
| 主工作树不参与排序 | 主目录堆了 10 张工单也不会被顶到眼前 | 它本来就在第一行，用户第一眼就看得到 |
| 排序不含已结束任务 | 跑完很多任务的活跃分支不因此加权 | 排序回答"现在看哪个"，不是"哪个历史悠久" |
| 到 3 栏时边缘投放退化 | 用户可能以为拖失败了 | 拖放过程中没地方放提示；落在这栏比落空好 |
| 跨基准拖放丢失位置语义 | 拖到左边缘却出现在最右 | 旧组下标在新基准里无意义；两帧之后才能给结果的交互不成立 |
| scratch 文件 tab 刷新即丢 | 要重新打开 | 文件在磁盘上没丢；与中央 tab 组同一条口径 |
| 临时文件固定 `.md` | 想写 `.sh` 得改名 | 右栏/浮窗都有改名入口；给个选扩展名的弹层不值这一步 |
