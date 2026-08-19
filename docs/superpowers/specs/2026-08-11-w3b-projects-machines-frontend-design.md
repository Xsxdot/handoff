# W3b：项目与机器控制面——前端设计

> 状态：待评审
> 前置：W3a（[spec](2026-08-11-w3a-projects-machines-backend-design.md)）已合入本分支；W3a 依赖 B62（[spec](2026-08-11-repo-registration-normalization-design.md)）。
> 形态基准：`prototypes/desktop-console/`（已确认的原型，含 `AGENTS.md` 的产品决策与 `design-qa.md` 的逐轮对照记录）。
> 上游：[Web 控制台总方案](2026-08-11-web-console-master-design.md) §5；W3a（后端）另出 spec。

## 0. 范围：从原型里切一刀

W3b **不设计形态，只还原形态**。`prototypes/desktop-console/` 是一个完整的 Orca 式桌面工作台原型，经过四轮 design QA，其 `AGENTS.md` 记录了确认过的产品与视觉决策。W3b 的工作是把其中 **W3a 能供数的那一块**在 `web/` 里实现出来。

原型远大于本轮。三堆划分：

**本轮做（W3a 能供数）**
左栏项目树（含层级、聚合计数、点击语义）、任务看板的筛选栏与卡片元信息、开发机页（只读）、项目登记与注销。

**本轮不做（原型有、后端没有）**
执行者开关、自动审批器配置、重启 agent、打开终端、Env 文件管理、设置页、配对开发机。这些都需要新的写 API，明确不进 W3a/W3b。

**本轮不做（属后续）**
中央的 terminal / editor / browser 工作台与右侧文件树——那是 workbench 本体。

未做的功能**整块不渲染**，不留置灰入口（左栏齿轮、「配对开发机」按钮、「可用执行者」的开关等）。理由：置灰控件承诺"以后能用"，用户会反复点；缺一个按钮反而诚实。**例外**是「可用执行者」列表本身——`/api/machines` 已投影出 `executors`，以只读列表呈现，仅开关不渲染。

**只有两条写操作**：项目登记（`POST /api/projects`）与注销（`DELETE /api/projects/{name}`），均为 B62 已有端点。

## 1. 形态与路由

App shell 三段：顶部 tab 条（任务看板 / 开发机）+ 左栏项目树（常驻）+ 中央内容区。依据 `AGENTS.md`：「Global task board, machine/agent management, and settings replace the workbench content area while keeping the project overview visible.」

```
/            任务看板     内容区 = 四列看板
/machines    开发机       内容区 = 机器卡片列表 + 右侧详情
/tasks/:id   任务详情     内容区 = W2 的 TaskPage
```

`App.tsx` 当前是裸 `<Routes>`（两条路由，无外框）。引入 `<Shell>` 包住 `<Outlet>`，三条路由都嵌在里面。W2 的 `BoardPage` / `TaskPage` **内容不改**，只是从"整页"降级为"内容区"——它们本来就没画自己的外框，这一步很轻。

左栏底部原型有「+ 添加项目」与齿轮：添加项目保留（§5），齿轮不渲染。

## 2. 左栏项目树

### 2.1 层级与数据

层级照原型：**项目 → 机器 → 主目录/worktree → 任务**（`AGENTS.md`：「project → code location(s) → main/worktree directory → handoff tasks」）。

- 结构（项目 / 机器 / 目录）来自 `GET /api/projects/tree?scope=all`（W3a §3）；
- 任务节点来自看板同一份 `GET /api/tasks`，按 `project_id` + `machine` + `work_dir` 挂到对应目录下。

**机器这一层直接对应 W3a 的 location**：W3a §1.1 已定「一个项目在一台机器上最多一个 location」，且单机响应 `locations` 长度恒 ≤1。因此树是定深三层，一台机器下不需要再分组——W3b 可以依赖这条不变式。

### 2.2 聚合计数

项目行三个计数（`AGENTS.md`：「Project rows aggregate directory, running-task, and attention counts」）：

| 计数 | 定义 |
|---|---|
| 目录 | 该项目所有机器下的 workspace 总数 |
| 运行 | 该项目下 `running` 态任务数 |
| 待处理 | 该项目下 `waiting_answer` + `waiting_review` 任务数 |

机器行同理，只统计该机器下的。计数全部从**任务流**算（不从树），使其随 2.5s 轮询实时跳动，见 §6。

### 2.3 点击语义：左栏选中态就是看板筛选状态

本轮没有中央 workbench，原型中「点目录切换工作区」没有落点。因此：

- 点**项目** → 筛选设为「只这个项目」，导航到 `/`
- 点**机器** → 在项目基础上收窄到这台机器
- 点**目录** → 再收窄到这个工作树
- 点**任务** → 导航到 `/tasks/:id`

顶部的项目多选下拉编辑的是**同一个 state**——不是两套联动，是一个对象两个编辑入口。多选时左栏不高亮单项，改为显示选中计数。

该 state 的形状（唯一真相，两个入口都读写它）：

```ts
type BoardFilter = {
  projects: Set<string>   // project_id 集合；空集 = 不按项目筛（全部）
  machine: string | null  // 机器名（""=本机）；null = 不按机器筛
  workspace: string | null // 工作树路径；null = 不按工作树筛
  search: string          // 搜索框文本
  pendingOnly: boolean    // 「只看待处理」
}
```

写入规则：点项目 → `projects = {该 id}`，同时清空 `machine`/`workspace`（换项目后旧的机器/目录筛选没有意义）；点机器 → 保持 `projects`，设 `machine`，清 `workspace`；点目录 → 保持前两者，设 `workspace`；顶部下拉 → 只改 `projects`，若改后当前 `machine`/`workspace` 不再属于任一选中项目，一并清空。

各字段的编辑入口：`projects` 由左栏与顶部多选下拉共写；`machine` 由左栏与顶部「全部开发机」下拉共写；`workspace` **只有左栏能设**（顶部没有对应控件，原型也没有）；`search` / `pendingOnly` 只有顶部能设。

这样两个 UI 永远不会打架。代价是左栏失去「纯浏览而不影响右侧」的能力；在没有 workbench 的本轮，浏览本身没有别的落点，代价为零。**W4 引入 workbench 时这条要重新审**——届时点目录应切换工作区而非筛看板，本节需重写。

### 2.4 断开与失效的展示

- 机器不可达：节点保持可见，标「已断开」，**不可展开**（`AGENTS.md`：「A disconnected remote machine stays visible but cannot be opened」）。
- 工作树探测失败（目录被删）：机器节点在，其下标注 `probe_error` 的人话说明，不炸整棵树。
- 未归属任务（`project_id: ""`）：挂在树末尾的「未归属」分组下。详见 §8。

## 3. 看板改造

### 3.1 筛选栏

按原型补齐：搜索框、项目多选下拉（每项带任务数）、开发机下拉、「只看待处理」toggle、右侧任务总数。

**筛选全部在客户端做。** 看板已 2.5s 全量拉 `GET /api/tasks`，改走后端过滤只会让筛选变成一次网络往返、并与轮询节奏打架。W3a §3 的 `?project=` 参数留给 CLI，前端不使用。

搜索框匹配任务名、项目名、执行者名（原型 placeholder：「搜索任务、项目或执行者」）。

### 3.2 卡片元信息

按原型给每张卡片加三行：项目 / 工作树分支 / 机器。来源：

| 行 | 来源 |
|---|---|
| 项目 | `proto.Task.ProjectID`（W3a 注解）join 树得到显示名；空则「未归属」 |
| 工作树 | 已有的 `task.branch` |
| 机器 | `proto.Task.Machine`（W3a 注解），`""` 显示「本机」 |

### 3.3 不动的部分

四列与卡片级状态**一个不改**。W2 的 `src/app/board/columns.ts` 已用 vitest 钉死状态到列的映射，而原型的「approval, question, blocked, and failure are card-level intervention states rather than additional columns」与之完全一致。轮询机制、断线保留最后列表、401 落终止态等 W2 纪律全部保留。

## 4. 开发机页（只读）

内容区照原型：左侧机器卡片列表 + 右侧选中机器详情；顶部三个统计（台数 / 在线数 / 运行任务数）。

字段来源：

| 原型格子 | 来源 |
|---|---|
| 名称 / 地址 / 已连接·已断开 | `/api/machines` 的 `name` / `addr` / `reachable` |
| Agent 版本 | `version` |
| 延迟 | `probe_ms`（W3a §4 本轮补入） |
| 可用执行者 | `executors` + `default_executor`（同上），**只读列表，开关不渲染** |
| 运行任务数 | `active_tasks` |
| 项目目录数 | 前端按机器聚合 `GET /api/projects/tree?scope=all` |
| 断开原因 | `error`（W3a 保证 `reachable=false` 时必非空） |
| 操作系统 | 后端没有——**不渲染该格** |
| 最后心跳 | 前端记录上次探活成功时刻，显示相对时间 |

`name` 为 `""` 的条目是本机，显示「本机」，其 `probe_ms` 恒为 0（进程内直查），UI 对本机不显示延迟格。

不渲染：配对开发机、重启 agent、打开终端、执行者开关、Env 文件区。

## 5. 项目登记与注销

### 5.1 两步向导

照 `AGENTS.md`：「Adding a project first selects local and/or one remote development machine, with at least one selected, then configures a Git repository or existing directory for every selected location.」

1. **选位置**：本机 / 一台远程开发机 / 两者，至少选一个。远程候选来自 `/api/machines`；不可达的机器可选但给出提示（登记会失败，让用户自己决定要不要试）。至多一台远程——由 UI 单选强制，对应 ADR-0008。
2. **配来源**：为每个选中位置填 Git 地址与可选目录。

落到 API：**每个选中位置一次 `POST /api/projects`**。填了目录 = 带 `path`（登记已有目录）；只给 Git 地址 = 不带 `path`（由该机器 clone 到自己的 `repo_root/<name>`）。这正是 B62 定义的两种形态，W3b 原样透传。

远程位置的请求经 W3a §5 透明转发到该机器，浏览器只与本机 agentd 通信。

### 5.2 部分成功必须可见

多位置登记是**多次独立调用**，可能一成一败。UI 必须逐位置报告结果，成功的保留、失败的显示原因并允许重试。**不允许"整体失败"的笼统提示**——那会让用户不知道本机到底登记上了没有。

### 5.3 注销

`DELETE /api/projects/{name}`，二次确认复用 W2 的 `ConfirmDialog`。确认文案须说明这只解除登记、不删除磁盘上的代码。

## 6. 数据获取节奏

三条流，节奏不同：

| 流 | 节奏 | 用途 |
|---|---|---|
| `GET /api/tasks` | 2.5s（W2 已有，不改） | 看板卡片、左栏任务节点、**全部计数** |
| `GET /api/projects/tree?scope=all` | 30s + 写操作后立即失效重拉 | 树的**结构**（项目/机器/目录）、开发机页的目录数 |
| `GET /api/machines` | 15s，仅 `/machines` 可见时 | 开发机页 |

**树不进 2.5s 热路径**：它带 `git worktree list` 现场探测，每 2.5s 对所有 location 探一遍纯属浪费。结构慢刷不影响体感，因为所有运行态都来自任务流——绿点、计数、卡片都跟着 2.5s 跳。

W2 已有的实时性纪律对三条流一体适用：`document.hidden` 时停表、可见时立即补拉；断线保留最后数据并标注；401 停止轮询落终止态。

## 7. 视觉令牌迁移

`web/src/index.css` 从 128 行扩成原型那套令牌：Geist 字体、中性色板、7–9px 圆角、hairline 分隔、紧凑密度。原型的 `styles.css`（3389 行手写 CSS）**不整体移植**——W2 已建立在 Tailwind 4 + shadcn 上，后续 W4/W5 还要继续加，移植会让 W2 部分变成孤岛。做法是把原型的视觉决策翻译成 theme 令牌，组件继续用 shadcn，按截图逐屏对照。

字体自托管：`prototypes/desktop-console/src/assets/fonts/Geist-Variable.woff2` 复制进 `web/public/`，**不引 CDN**（agentd 托管的控制台可能跑在无外网环境）。

**W2 的 BoardPage / TaskPage 一并对齐**：换 theme 令牌后 shadcn 组件自动跟随（它们用的是 `bg-background` / `text-foreground` 这类语义 class），手工要动的只有密度与分隔线。不对齐反而要额外做样式隔离。

W2 的行为测试（`columns.test.ts`、`review.test.ts`、`TicketsPanel.test.tsx`）断言的是逻辑与文案，**不应因换皮变红**。若有测试因此变红，说明它测错了层，顺手修掉并在提交信息里说明。

## 8. 诚实展示（硬约束，非装饰）

这一节的每一条都是"宁可显示难看的真相，不显示好看的假象"：

- **机器不可达**：卡片与树节点都留在原位，标状态与原因原文。**绝不静默少一台**——W3a §5.3 把这个列为头号失败模式。
- **未归属任务**：`project_id` 为空的任务（多为 B62 之前、`repo_path` 指向 linked worktree 的遗留任务）挂在树末尾的「未归属」分组，看板卡片显示「未归属」。不静默丢弃、不硬塞进某个项目。
- **工作树探测失败**：显示 location 与 `probe_error`，不炸整棵树。
- **数据新旧**：跨机任务快照带 `fetched_at`（W3a §6.3），明显陈旧时在机器卡片标注。不让陈旧数据冒充实时。
- **部分成功**：§5.2 的逐位置报告。

## 9. 对原型的显式偏离

| 偏离 | 原因 |
|---|---|
| 本机目录**不提供 Finder 选择器**，与远程一样粘贴路径 | 浏览器里没有 Finder；File System Access API 出于安全**故意不返回真实路径**，拿到的句柄对 agentd 无用 |
| clone 默认路径**不硬编码** | 原型标 `~/.handoff/<project-name>`，B62 实际是 `repo_root/<name>`（默认 `~/.handoff/repos/`）。UI 留空时只提示「由该机器 clone 到它自己的 repo_root 下」 |
| 「操作系统」格不渲染 | `/api/status` 没有这个数据 |
| 未实现功能整块不渲染而非置灰 | §0 |
| 左栏点击=筛看板 | 本轮无 workbench，见 §2.3；W4 时须重审 |

## 10. 可观测性

前端没有结构化日志后端，纪律落在**错误的可见性**上：

- 任何请求失败都要有**用户可见**的降级展示（沿用 W2 的 `Banners`），不允许 `catch` 后静默；
- `console.error` 只用于开发期辅助，**不作为错误处理手段**——用户看不到 console；
- 三条轮询流各自的失败互不影响：机器探活失败不该让看板空掉；
- 新组件按项目规范写文件头注释（职责 + 边界）与非显然分支的「为什么」注释。`BoardPage.tsx` 顶部那段注释是本项目的范本。

## 11. 测试与验收

**逐屏对照**（视口 1440×1024，与 `design-qa.md` 同基准）：

| 屏 | 基准截图 |
|---|---|
| 左栏项目树 | `implementation-1440x1024-v6.png` |
| 看板与项目多选 | `implementation-board-project-multiselect.png` |
| 开发机页 | `implementation-machines.png` |
| 项目向导 | `implementation-project-location-selection.png`、`-local-finder.png`（本机改粘贴路径）、`-remote-path.png` |

**行为测试**（vitest + testing-library，沿用 W2 形态）：

- 筛选状态单一真相：左栏点击与顶部下拉互相推动，不产生第三种状态；
- 树的计数聚合：目录/运行/待处理三个数在给定任务集下的正确值；
- 断开机器渲染为可见不可展开；未归属分组出现且不吞任务；
- 登记向导按选中位置数发起正确次数的 `POST`，且一成一败时逐位置报告（§5.2）；
- 三条流的节奏与 `document.hidden` 停表行为。

**真机验收**：本机 + devbox 两台，树里两台都在、任务各自归位；断开 devbox 后机器卡片与树节点均标已断开且看板不空。

## 12. 交付物与前置

前置硬依赖：W3a 已合入（否则 `/api/projects/tree`、`/api/machines`、`proto.Task` 的两个注解字段都不存在，本 spec 全部落空）。

交付：`web/` 下的 app shell、项目树、看板筛选栏与卡片元信息、开发机页、项目向导，以及 §7 的令牌迁移与 W2 回调。不动 `internal/` 与 `cmd/`——若实现中发现后端缺数据，走 W3a 补充而非前端硬编码。
