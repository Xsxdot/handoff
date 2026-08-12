# W4 补课：看板干预态标记 + 左栏搜索设计

> 覆盖 backlog **B75**（看板卡片缺干预态橙色标记，且 `waiting_answer` 重复渲染两遍「等你答复」）
> 与 **B74**（左栏缺顶部搜索框与「项目 N」小标题）。
>
> 形态基准：`prototypes/desktop-console/`（`implementation-task-board-final.png` 是本 spec 的主要对照图）。
> 上游：`2026-08-12-w4-shell-calibration-design.md`（W4 外壳校准期）§3 左栏、§5.1 任务看板。

## 0. 这一期在补什么

W4 外壳校准期交付后的真机走查（plan Task 17）留下两笔账，性质不同：

- **B75 是计划漏了。** W4 spec §5.1 白纸黑字要求「干预态是**卡片级**标记而非额外列：`● 等待审批`、`● 等待 Review`（橙色）」，验收项 11 照抄了这条，但通读 plan 全文没有任何一个 task step 实现它，验收判「部分通过」。
- **B74 是 spec 从未收录。** 原型左栏顶部有搜索框与「项目 N」小标题，W4 spec 全文没提过它们。所以实现忠实于 spec，既不算 spec §8 那五处「用户裁决过的偏离」，也不该就地糊上去——它需要补设计，这份 spec 就是那个补。

两条合一份 spec 的理由：都只动 Web 前端、都以同一张原型截图为基准、且 B75 的色板工作（§2）正好是 B74 左栏任务行上色的前置。

## 1. 探明的事实（设计的出发点）

写这份 spec 前对原型源码与实现代码做了核对，四条结论直接决定了下面的设计。

### 1.1 原型的卡片状态不是 Badge，是「圆点 + 文字」

`prototypes/desktop-console/src/App.jsx` 的 `TaskBoardCard`：

```jsx
<div className="board-card-footer">
  <span className={`task-state ${tone}`}><StatusDot tone={dotTone} />{task.state}</span>
  <span><Bot size={12} />{task.executor}</span>
</div>
```

配套 CSS 是 `.task-state.attention { color: #a66c09 }` 与 `.status-dot.attention { background: var(--amber) }`——**没有填充胶囊**。W4 spec §5.1 写的「`● 等待审批`」里那个 `●` 是字面意思。

而实现用的是 shadcn `Badge`（填充胶囊）。所以 B75 不只是「`badge.tsx` 缺一个橙色档」，是**卡片的状态形态本身与原型不一致**。

### 1.2 原型有两层橙，不是一层

- **文字层**：`tone` 来自所在列，Review 列 tone 为 `attention`，因此该列**全体**卡片的状态文字都是琥珀色——`等待审批` 与 `等待 Review` 都算。
- **卡片层**：`.board-card.needs-attention { border-color: #e7bd75; box-shadow: inset 3px 0 #dda13d }`，由 mock 数据的 `attention: true` 驱动，截图里只有 T-005（等待审批）有，T-015（等待 Review）没有。

### 1.3 仓库里已有一条确立的「干预态」口径

`waiting_answer + waiting_review` 这个集合在三处独立出现，且完全一致：

| 位置 | 用途 |
|---|---|
| `web/src/app/board/filter.ts:100` | 看板「只看待处理」筛选 |
| `web/src/app/tree/counts.ts:30,41` | 左栏项目/机器行的 `pending` 计数 |
| `web/src/app/tree/ProjectTree.tsx` `wsCounts` | 目录行的 `运行·待处理` 计数 |

W4 spec §5.1 列出的两个干预态也正是这两个。

**因此：卡片级标记（§1.2 的第二层）给 `waiting_answer` 与 `waiting_review` 都上，不照抄原型只标一张卡。** 原型里 T-015 没有橙边是 mock 数据的取值，不是规则；跟着仓库已确立的口径走，比跟着 mock 数据走可靠。

### 1.4 原型左栏搜索的自身实现很寒碜，不能照抄

```js
const visibleProjects = useMemo(
  () => projects.filter((project) => project.name.includes(query.trim().toLowerCase())),
  [projects, query],
)
```

- placeholder 写「搜索项目、机器或任务」，实际**只过滤项目名**——文不对题。
- `⌘K` 是一个纯装饰的 `<kbd>`，`App.jsx` 全文没有任何键盘监听。
- 「项目 N」数的是 `visibleProjects.length`，即**过滤后**的数量，不是总数。

结论：计数口径照抄（过滤后），检索面与 `⌘K` **不照抄**——见 §5、§6。

## 2. 状态色板：先补语义 token

### 2.1 为什么先做这个

实现里的状态色现在是**各写各的**：

| 位置 | 写法 |
|---|---|
| `ProjectTree.tsx` 工单角标 | `bg-amber-500` |
| `MachinesPage.tsx:123` 不可达 | `text-amber-600` |
| `AddProjectWizard.tsx:206` 已登记 | `text-emerald-600` |

再加一档橙色只会变成第四种写法。所以先把状态色收进 `web/src/index.css` 的 token 体系——原型 `:root` 本就有 `--green / --amber / --red` 三个语义色，直接对齐。

### 2.2 新增 token

在 `:root` 与 `.dark` 成对新增，并在 `@theme inline` 暴露：

| token | 取值（light，来自原型） | 用途 |
|---|---|---|
| `--state-active` | `#18a86b`（原型 `--green`） | 运行中 / 已完成的圆点 |
| `--state-intervention` | `#e79b18`（原型 `--amber`） | 干预态圆点、卡片左竖条、卡片边框（降透明） |
| `--state-intervention-text` | `#a66c09`（原型 `.task-state.attention`） | 干预态**文字**色 |
| `--state-failed` | `#df554f`（原型 `--red`） | 失败圆点与文字 |

**为什么 intervention 要单独一个文字色**：`#e79b18` 当 7px 圆点足够醒目，但当 11–12px 小字对白底只有约 2.4:1 对比度，读不清。原型自己就分了这两个值。

**取值写法**：转 oklch 写入，与文件里既有的 `oklch(...)` 风格一致。

**必须同时在 `@theme inline` 注册**，键名加 `--color-` 前缀：

```css
@theme inline {
  --color-state-active: var(--state-active);
  --color-state-intervention: var(--state-intervention);
  --color-state-intervention-text: var(--state-intervention-text);
  --color-state-failed: var(--state-failed);
}
```

这样 Tailwind v4 会生成 `bg-state-intervention` / `text-state-intervention-text` / `border-state-intervention` 等工具类，**全文一律用这些工具类，不写 `bg-[var(--...)]` 的任意值形式**——只有 `box-shadow` 因无对应工具类才用任意值（§4.2）。

**暗色档**：`.dark` 必须成对给出（`index.css:11` 的注释说明本轮不做主题切换，但保留 shadcn 契约）。暗色档按同色相提亮，实现时须核对文字色对暗色背景 ≥ 4.5:1（WCAG AA 正文）。

**边框不立第四个 token**：用 `border-state-intervention/45` 从实色降透明得到——原型的 `#e7bd75` 本就是 `#dda13d` 的浅化。

### 2.3 顺带收敛

`ProjectTree.tsx` 工单角标的 `bg-amber-500` 换成 `--state-intervention`。理由：它就在本期要改的文件里，留着就是同一个左栏里两种橙。

`MachinesPage.tsx` / `AddProjectWizard.tsx` 的裸色**不动**——不在本期改动面内，属无关重构。

## 3. `columns.ts`：拆开视觉映射

### 3.1 现状与问题

`stateBadgeVariant` 一个函数同时被 `BoardPage.TaskCard`（看板卡片）与 `TaskHeader.tsx`（任务详情页顶栏）消费。这正是 B75 backlog 行里「改了两个页面一起变」被当作阻力的成因——而这两个面本就该用不同形态：看板卡片是密集列表，行内「圆点 + 文字」的视觉噪声本该低于胶囊；详情页顶栏只有一个状态，胶囊是恰当的。共用一个函数是当初的耦合失误，本期拆掉。

### 3.2 拆分后的契约

```ts
export type StateTone = 'idle' | 'active' | 'intervention' | 'done' | 'failed'

// stateTone 把状态映射成视觉基调，看板卡片消费。
export function stateTone(state: string): StateTone

// needsIntervention 报告任务是否处于干预态（等你动手）。
// 口径必须与 filter.ts 的 pendingOnly、counts.ts 的 pending 保持一致。
export function needsIntervention(state: string): boolean

// stateBadgeVariant 保留，仅任务详情页消费；干预态改返回新的 'intervention'。
export function stateBadgeVariant(state: string): 'default' | 'secondary' | 'destructive' | 'outline' | 'intervention'
```

### 3.3 映射表（硬契约，测试钉死）

| 状态 | `stateTone` | `needsIntervention` | 卡片呈现 | TaskHeader Badge |
|---|---|---|---|---|
| `pending` | `idle` | false | 灰点 · 灰字「等待执行」 | `secondary` |
| `running` | `active` | false | 绿点 · 灰字「进行中」 | `default` |
| `waiting_answer` | `intervention` | **true** | **琥珀点 · 琥珀字「等你答复」** | **`intervention`** |
| `waiting_review` | `intervention` | **true** | **琥珀点 · 琥珀字「Review」** | **`intervention`** |
| `completed` | `done` | false | 绿点 · 灰字「已完成」 | `secondary` |
| `failed` | `failed` | false | 红点 · 红字「失败」 | `destructive` |
| 未知状态 | `idle` | false | 灰点 · **原文透出** | `secondary` |

未知状态回退 `idle` 而不是丢弃，沿用 `stateToColumn` 的既有纪律——「宁可让新状态显眼地出现，也不让任务凭空消失」。**注意回退值刻意与 `stateToColumn` 不同**：`stateToColumn` 回退 `active`（让未知状态出现在「进行中」列，看得见），而 `stateTone` 回退 `idle`（灰点）。分列要显眼，染色不该乱染——一个没见过的状态被涂成绿色或琥珀色是在编造语义。两者共同保证的是「不消失」，不是「同一个取值」。

### 3.4 `isWaitingAnswer` 的去留

删掉重复徽章（§4.2）后 `isWaitingAnswer` 在生产代码里没有消费者了。**保留函数、保留 `columns.test.ts` 里钉它的用例**——它是状态机契约的一部分，不是为那个徽章而生的；`columns.ts` 文件头把该文件定义为「硬契约，vitest 钉死」，契约函数不因一处调用点消失而删除。

## 4. 看板卡片改造（B75 主体）

### 4.1 新组件 `web/src/app/board/StateDot.tsx`

```tsx
export function StateDot({ tone }: { tone: StateTone })   // 7px 圆点，纯呈现
export function TaskState({ state }: { state: string })   // 圆点 + 文案
```

放在 `app/board/` 而非 `components/ui/`：它消费 `columns.ts` 的领域映射，不是通用 UI 原语。`TaskHeader.tsx` 已有 `import ... from '../board/columns'` 的跨目录引用先例，左栏若要用同样从这里引。

`TaskState` 在 `intervention` 时文字取 `--state-intervention-text`，`failed` 时取 `--state-failed`，其余状态文字保持 `text-muted-foreground`（与原型一致：只有需要你注意的才染色）。

### 4.2 `BoardPage.TaskCard` 三处改动

1. **删掉 `{waitingAnswer && <Badge variant="destructive">等你答复</Badge>}`**（`BoardPage.tsx:163`）。

   这就是 B75 顺带钉住的既有缺陷：它与下一行的 `<Badge variant={stateBadgeVariant(task.state)}>{stateLabel(task.state)}</Badge>` 在 `waiting_answer` 时会渲染出**两个一模一样的红徽章**（`stateLabel('waiting_answer')` 也是「等你答复」）。

   **删这一个而不是删另一个**：留下的那行是全状态通用的，删了会让其余五个状态一起失去标记。

2. `<Badge variant={stateBadgeVariant(...)}>` 换成 `<TaskState state={task.state} />`，位置从卡片中部（与 executor 同行）挪到**卡片底部一行**，对齐原型的 `board-card-footer`。

3. **卡片容器**：`needsIntervention(state)` 为真时加干预态边框 + 左侧 3px 竖条
   （`border-state-intervention/45` + `shadow-[inset_3px_0_var(--state-intervention)]`——
   竖条无对应工具类，是全文唯一的任意值写法）；
   `failed` 保持现有 `border-destructive/40 bg-destructive/5` 不变。

   两者互斥——`failed` 不在干预态集合里（§3.3），不会同时命中。

### 4.3 `TaskHeader.tsx`

只有一处实际变化：`badge.tsx` 补 `intervention` 变体后，`stateBadgeVariant` 对两个干预态返回 `'intervention'`，`TaskHeader` 的两处调用点（:41、:51）不改代码即自动变橙。

`badge.tsx` 新增变体（填充胶囊，因此用实色背景 + 白字，与 `destructive` 同构）：

```
intervention: "border-transparent bg-state-intervention text-white shadow hover:bg-state-intervention/80"
```

### 4.4 左栏任务行圆点上色

原型左栏的任务行圆点是**有颜色**的（截图中「等待批准：更新快照」为橙点，「修复 DropPeer 并发测试」为绿点）；实现里是固定的 `bg-muted-foreground/40` 灰点（`ProjectTree.tsx` 任务行与未归属分组两处）。

改为 `<StateDot tone={stateTone(t.state)} />`。

**这一处字面上既不属于 B74 也不属于 B75**，作为顺带修正纳入，理由：不做的话，同一个任务在看板上标着琥珀、在左栏是灰点，两个面自相矛盾；而左栏正是 B74 要改的文件，边际成本近乎零。**用户在设计评审中已确认纳入。**

## 5. 左栏搜索（B74 主体）

### 5.1 落点：`ProjectTree.tsx`，`Shell.tsx` 零改动

搜索框、「项目 N」小标题、query 状态、过滤逻辑**全部落在 `ProjectTree.tsx` 内部**。

**为什么不放 `Shell.tsx`**：query 状态与过滤逻辑都属于树本身，上提到 `Shell` 只是无意义的 prop drilling。

**附带收益（重要）**：交接文档把 B74 与 PTY 那条线的冲突面标为「低但非零——`Shell.tsx` 横幅区（:87-95）相邻，预期一次手工合并」。按本设计我们根本不进 `Shell.tsx`，而 PTY 只改它的 `<aside>` 横幅段——**冲突面归零**。

### 5.2 结构

在 `ProjectTree` 的 return 中，「任务看板」按钮之下、项目列表之上：

```
[任务看板]                          ← 已有
[🔍 搜索项目、机器或任务      ⌘K]    ← 新增
项目 N                              ← 新增
[树]
[未归属]                            ← 已有
[底部三入口：添加项目 / 工单 / 设置]  ← 已有
```

顺序与原型一致（原型：window-nav → sidebar-search → sidebar-section-title → sidebar-tree）。

### 5.3 匹配口径

`q = query.trim().toLowerCase()`，四类字段各自 `toLowerCase().includes(q)`：

| 层级 | 字段 |
|---|---|
| 项目 | `project.name` |
| 机器 | `machineLabel(loc.machine)` —— `""` → 「本机」，所以搜「本机」也能命中 |
| 目录 | `dirLabel(ws)` —— 有 branch 用 branch，否则用路径末段 |
| 任务 | `taskName(t)` —— `name \|\| plan_summary \|\| '（无名称）'` |

四类都匹配是刻意的：placeholder 承诺了「项目、机器或任务」，只过滤项目名（原型的做法，见 §1.4）文不对题。

### 5.4 可见性规则

**一条规则，递归**：节点可见 ⟺ **自身命中** 或 **任一后代命中**。

自身命中时**整棵子树都显示**——搜项目名要能看到它下面的全部机器、目录、任务。

### 5.5 展开态

`q` 非空时**旁路 `collapsed` 集合**，可见路径一律展开。搜到了却折叠着等于没搜到。

注意是**旁路而非清空**：`collapsed` 状态原样保留，只是在 `q` 非空时不参与可见性判断。`q` 清空后用户手动折起来的布局原样回来——搜索不破坏布局。

### 5.6 「项目 N」

N = **过滤后可见的 `tree.projects` 数量**；`q` 为空时即项目总数。

**「未归属」分组不计入 N。** 它不是一个项目，是个收纳箱；算进去会出现「项目 3」但下面只有 2 个能展开的项目行，数字与眼睛看到的对不上。

跟随过滤是原型的行为（§1.4），也顺理成章：搜索时这行小标题自然成为「找到几个」的即时反馈。

### 5.7 未归属分组与空态

- `unassigned`（`project_id === ''` 的任务）按 `taskName` 过滤；`tree.unowned`（未登记为项目的目录名）按名字字符串过滤。两边都空则整个分组隐藏。
- **空态**：可见项目为 0 **且** 未归属分组也空时，树区域显示「没有匹配的项目或任务」。

  为什么要有空态：左栏搜到全白会像加载失败。原型的看板有「没有匹配的任务」的空态，左栏没有——这里补上。

## 6. `⌘K`

`useEffect` 在 `window` 上挂 `keydown`，**冒泡阶段，不用 capture**：

- `(e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k'` → `e.preventDefault()` + 聚焦输入框 + 全选已有文本
- 输入框内 `Escape` → 清空 `query` + `blur()`

**为什么是冒泡阶段而不是 capture**：这是**刻意的让位次序**。将来 PTY 终端拿到焦点时，xterm 会吞掉自己的按键；冒泡阶段监听意味着「任何调用 `stopPropagation` 的组件优先」——终端里按 ⌘K 不该把焦点抢到左栏去。

这条 why 必须写进代码注释。**并且是一条显式待复核项**：PTY 那条线合并后，须真机确认「终端聚焦时按 ⌘K 不会跳到左栏搜索框」，结果记进走查记录。

## 7. 可观测性与注释（`instrumenting-code`）

**已核实：`web/src/` 生产代码中 `console.*` 零命中，前端没有日志层，本期也不引入。**

因此 `instrumenting-code` 的义务在前端由**意图注释 + 测试**兑现，这是本 spec 的显式立场（写在这里，免得实现计划被判 plan failure）。以下四处必须有解释「为什么」的中文注释：

1. `columns.ts` —— 干预态口径为何必须与 `filter.ts` / `counts.ts` 三处一致（§1.3）
2. `ProjectTree.tsx` 可见性递归 —— 「自身命中则整棵子树显示」的理由（§5.4）
3. `ProjectTree.tsx` 旁路 `collapsed` —— 旁路而非清空的理由（§5.5）
4. `ProjectTree.tsx` `⌘K` 监听 —— 冒泡而非 capture 的让位次序（§6）

新建文件 `StateDot.tsx` 须有文件头职责与边界注释；新增导出函数 `stateTone` / `needsIntervention` 须有参数、返回、注意事项注释。

禁止 `console.log` 充当日志。

## 8. 测试

全部落在既有 vitest 套件里，**不引入新依赖**。

**`web/src/app/board/columns.test.ts` 追加**

- `stateTone` 六个状态各一次 + 未知状态回退 `idle`
- `needsIntervention` 只对 `waiting_answer` / `waiting_review` 为真——这条同时钉住「口径与 `filter.ts`、`counts.ts` 一致」
- `stateBadgeVariant` 对两个干预态返回 `'intervention'`

**`web/src/app/board/` 新增 `BoardPage` 用例**

- `waiting_answer` 卡片上「等你答复」**只出现一次**（直接钉死 B75 的重复徽章缺陷，防回归）
- 干预态卡片带干预态样式、`failed` 卡片不带干预态样式
- 每个状态的卡片文案与圆点基调对得上

**`web/src/app/tree/ProjectTree.test.tsx` 追加**

- 四类字段（项目名 / 机器名 / 目录名 / 任务名）各搜一次都能命中
- 搜任务名时其祖先链展开、无关兄弟枝隐藏
- 搜项目名时该项目整棵子树可见
- 「项目 N」跟随过滤变化，`q` 为空时等于总数
- 「未归属」分组参与过滤，且不计入 N
- 零结果时出空态文案
- `⌘K` 聚焦输入框；输入框内 `Escape` 清空并失焦
- `q` 清空后，此前手动折叠的节点仍是折叠的（钉住 §5.5 的「旁路而非清空」）

## 9. 改动面清单

| 文件 | 改动 |
|---|---|
| `web/src/index.css` | 新增四个状态 token（`:root` + `.dark` + `@theme inline`） |
| `web/src/components/ui/badge.tsx` | 新增 `intervention` 变体 |
| `web/src/app/board/columns.ts` | 新增 `StateTone` / `stateTone` / `needsIntervention`；`stateBadgeVariant` 干预态改档 |
| `web/src/app/board/StateDot.tsx` | **新建**：`StateDot` / `TaskState` |
| `web/src/app/board/BoardPage.tsx` | `TaskCard` 删重复徽章、换 `TaskState`、加干预态卡片样式 |
| `web/src/app/tree/ProjectTree.tsx` | 搜索框 + 「项目 N」+ 过滤 + `⌘K`；任务行圆点上色；工单角标换 token |
| `web/src/app/board/columns.test.ts` | 追加用例 |
| `web/src/app/tree/ProjectTree.test.tsx` | 追加用例 |
| `web/src/app/board/BoardPage.test.tsx` | **新建**（已核实当前不存在：`app/board/` 下只有 `FilterBar.test.tsx` / `columns.test.ts` / `filter.test.ts`） |

`web/src/app/task/TaskHeader.tsx` **不改代码**——它经由 `stateBadgeVariant` 自动变橙。

## 10. 与 PTY 并行线的冲突面

PTY 那条线（分支 `feat/w4-pty-terminal`，同基于 `850ae61a`）的改动面见
`docs/superpowers/notes/2026-08-12-w4-parallel-handoff.md` §2。逐条比对：

| 本期文件 | PTY 是否碰 | 结论 |
|---|---|---|
| `index.css` / `badge.tsx` / `columns.ts` / `StateDot.tsx` / `BoardPage.tsx` | 否 | 零冲突 |
| `ProjectTree.tsx` | 否 | 零冲突 |
| `Shell.tsx` | **是**（`<aside>` 横幅段） | **本期不进该文件**（§5.1），零冲突 |

**本期不触碰 `WorkbenchPage.tsx` 的 `renderContent` 签名**（交接文档 §2 的红线），也不触碰任何 Go 代码、`internal/proto/`、`web/src/api/`——因此不涉及契约 fixture 重生成。

**净结论：与 PTY 线冲突面为零。**

## 11. 本轮不做

- **命令面板（Command Palette）**。原型只画了输入框，没画面板；做它等于在这一期再发明一块没有形态基准的东西，而这一期的立身之本是「不发明，只还原」。`⌘K` 先做成聚焦快捷键——那个 `<kbd>` 在原型里按下去什么都不会发生，做成聚焦已经让它名副其实。等项目规模真的上来、有真实使用数据了再谈面板。
- **搜索结果高亮 / 模糊匹配 / 拼音首字母**。`includes` 够用。
- **搜索状态持久化**。刷新即清空。
- **看板顶部那个已有的搜索框**（`FilterBar`）不动，与左栏搜索互不相干。
- **`MachinesPage.tsx` / `AddProjectWizard.tsx` 的裸色收敛**。不在本期改动面内，属无关重构。
- **主题切换**。`.dark` 档按 shadcn 契约成对给出，但本轮仍不做切换（沿用 `index.css:11` 的既定立场）。

## 12. 验收判据

1. 与 `prototypes/desktop-console/implementation-task-board-final.png` 并排看：看板 Review 列两张卡片的状态文字均为琥珀色「圆点 + 文字」，结构能对上。
2. `waiting_answer` 的卡片上「等你答复」**只出现一次**。
3. `waiting_answer` 与 `waiting_review` 的卡片均带琥珀边框 + 左侧竖条；`failed` 卡片保持红色区分且不带琥珀。
4. 任务详情页顶栏两个干预态的 Badge 为琥珀色。
5. 左栏顶部有搜索框与「项目 N」；输入项目名 / 机器名 / 目录名 / 任务名四类各能命中，命中项祖先链自动展开。
6. 「项目 N」跟随过滤；零结果出空态文案。
7. `⌘K` 聚焦搜索框，`Esc` 清空并失焦。
8. 清空搜索后，此前手动折叠的节点仍折叠。
9. 左栏任务行圆点颜色与该任务在看板上的基调一致。
10. 回归全绿：`cd web && npx vitest run && npm run typecheck && npm run lint && npm run build`。

**PTY 合并后补验**（§6）：终端聚焦时按 ⌘K 不会把焦点抢到左栏搜索框。
