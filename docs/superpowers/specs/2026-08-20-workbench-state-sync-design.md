# 工作台状态同步（B 部分）设计

日期：2026-08-20
分支：`claude/pty-hosting-state-sync-eb0ff1`

## 0. 背景与范围

用户在桌面端于若干目录下开了终端 / 文件 / TUI tab，并做了分屏布局。切换目录再切回来、
或退出桌面端再进来，这些现场会丢失。目标是：**不管切目录还是退出应用，回来时一模一样。**

现状的两条根因：

- `web/src/app/workbench/useWorkbench.ts` 按基准目录分持 tab 组，但整份 `byBase` 只在内存里，
  包注释明确写着「不做持久化。tab 组存内存，刷新即丢」。
- 刷新后唯一能回来的是终端 tab，靠 `usePtyRestore` 从 `GET /api/pty/sessions` 重建。
  分屏布局、tab 顺序、文件 tab、TUI tab 全丢，恢复出来的终端还全挤在单栏里。

本 spec 只解决**状态持久化与恢复**。「PTY 托管到 agentd 进程之外」是独立的一份 spec（A），
两者唯一的耦合点是 `session_id`：B 把它存进布局，A 保证它跨 agentd 重启仍然有效。
接口就是这一个字段，可以完全并行设计、分别实现。

## 1. 关键决策

### 1.1 存服务端，不存 localStorage

状态落 agentd 的 SQLite，经 HTTP 接口读写。

理由：需求里包含「接下来可能做移动端」。落 localStorage 则两端各存各的，永远对不齐。
不做「服务端为真相 + localStorage 当离线缓存」的两层结构：缓存层只在 agentd 连不上时有价值，
而那时整个控制台本来就是空的（树、任务、文件全从它来），渲染出一个摆着 tab 但点什么都报错的
界面，比空白更糟。

状态存在**控制台连的那个 agentd**（协调者机器）的库里。布局里可以有指向远程机器目录的 tab，
tab 内记录机器名即可，不需要去远程机存任何东西。

### 1.2 存什么：只存「工作现场」

存：

```
当前选中的基准目录
每个基准目录 → { groups: [{ tabs, activeId }], active, sizes }
  tab.content: terminal → seq + sessionId + rel
               file     → rel
               tui      → taskId
               blank    → 原样存
```

`blank` 必须存：它是「还没选种类」的合法中间态，不存的话用户点了 `+` 还没选就退出，
回来会少一个 tab。

外加**右下角悬浮窗**（`useHomeDock`）的现场：

```
悬浮窗 → { tabs: HomeTab[], activeId, windowOpen, geom: {x,y,w,h}, maximized }
  HomeTab: id + kind + seq + sessionId + machine + rel
```

它必须单独存，因为它**不在 `byBase` 里**：`Shell.tsx` 把 `base_kind=home` 的会话路由进
悬浮窗而不是中央工作台，`useHomeDock` 的注释写明了理由——home 终端不挂在任何目录上，
塞进 `byBase` 就会跟着目录切换走。

`HomeTab` 的 `draft` / `baseSha` **剥掉不存**，与 §1.2 对文件草稿的决定一致。

**不存**这三样：

1. **文件 tab 的未保存草稿**（现落 localStorage，见 `fileDraft.ts`）。跨端同步草稿要处理
   「两端各改了一半」的合并，而现有 localStorage 层已能撑过刷新与误关。
2. **左栏偏好**（隐藏项目、排序、折叠，现落 localStorage）。那是「显示偏好」不是「工作现场」，
   搬它会把本 spec 扩成「用户设置云同步」。
3. **滚动位置 / 编辑器光标 / TUI 滚到哪一回合**。内容会变（文件被改、TUI 又来 200 条 frame），
   恢复不准比不恢复更让人困惑。

### 1.3 粒度：一个基准目录一行

不是整份 `byBase` 打包成一个大 blob。

理由是冲突面：整份一个 blob 时，手机在目录 A 开了个 tab，整份写回就把电脑在目录 B 的布局
一起覆盖了——两端明明在动不相干的东西。按目录分行后，只有「两端同时在同一个目录里操作」
才会撞上。切目录也只写一行，不必每次推全量。

### 1.4 冲突：最后写入者赢

不做版本号、不做 409 重放、不做合并。

这份数据是「工作现场」不是「文档」，丢掉的最坏后果是某一栏的 tab 没了，重开一下就有。
为它引入乐观并发控制，换来的是用户感知不到差别的正确性。

### 1.5 「当前选中目录」单独存，且不跨端跟随

它单独存一条，语义是「上次选的」，重开时恢复。**不做跨端实时跟随**：电脑上在 A 目录、
手机上翻到 B 目录是完全正常的两件事，让手机把电脑的视线拽走是纯粹的坏。

### 1.6 本期不做实时推送

agentd 有现成的 WS hub，接上不难，但会引入「另一端正在输入时 tab 被远端改掉」这类交互问题，
而在真正同时用两端之前，这些问题连形状都还看不清。

本期只在**应用启动那一次拉**。等移动端真做起来、真感到疼了再补推送。

## 2. 失效态处理

存下来的布局必然会过期：worktree 被 `done` 回收、任务归档、文件被删、PTY 会话随 agentd 重启没了。
`useWorkbench` 的注释里那句「持久化要处理『目录被删了但 tab 还在』这类失效态，本期不值得」，
说的就是这件事。四条规则正面回答它：

### 规则一：不做任何恢复期探活

每种 tab **已经**有自己的失效态——`TerminalTab` 收到 close 1008 会红字报出服务端给的真实原因
并给「重开一个终端」的出口，`FileTab` 读不到文件会报错，`TuiTab` 有 `LoadFailed`。
恢复时挨个校验，等于为了提前几百毫秒告诉用户一件他点进去自然会知道的事，代价是 N 次请求
外加跨机器探活的慢机拖累。而且 `WorkbenchPage` 只渲染激活 tab，未激活的 tab 连组件都不挂载。

### 规则二：死掉的 sessionId 就地抹掉，tab 留在原位

唯一的例外，因为它不需要新增任何请求——恢复流程本来就要拉一次 `GET /api/pty/sessions?scope=all`。

拿那份列表比对：布局里的 `sessionId` 不在列表里，或已带 `exit_code`，就把该字段抹掉，
tab 降级成「还没有会话的终端 tab」。用户切到它时，原来那一栏原地起一个新 shell。

这条与 A 无缝衔接：**A 做完之前**，agentd 重启后是「布局完整、终端是新的」；**A 做完之后**，
`sessionId` 大多数时候还活着，那一栏就是原来那个 shell，连滚屏内容都在。中间不改任何代码。

反方向也要管：列表里有活会话、布局里没有（另一台设备开的，或那行被淘汰了）。保留
`usePtyRestore` 现在的行为——工作树会话补到对应目录的最后一栏末尾，home 会话补进悬浮窗
（`dock.adopt`）。不补的话它就是个在后台跑着、界面上却看不见的 shell。

这条规则**同等适用于悬浮窗的 tab**：恢复出来的 `HomeTab.sessionId` 不在列表里就抹掉，
tab 留在原位，用户点到它时原地起一个新 shell。

### 规则三：只有「上次选中的目录」要校验一次

它决定用户一睁眼看到什么。那个目录已不在树上时，退回「未选中」态（即现在没选任何目录时的
空态），而不是摆出一栏点什么都报错的 tab。这次校验不发请求——项目树本来就要加载。

home 基准例外：它永远有效，不用等树。

### 规则四：50 行上限，按 updated_at 淘汰

每个 worktree 都会留一行，跑久了会攒到几百行。保留最近 50 个目录，超出的删。

**不做**「路径还在不在」的 GC：那要遍历文件系统、还要跨机器，成本远高于一行 JSON 的价值。

## 3. 架构分界

后端是一个**不解释内容的键值存储**，前端是**唯一懂布局形状的一方**。

`payload` 在后端就是一个 JSON 字符串：agentd 不解析、不校验结构、不认识什么叫「分屏」。
好处是以后布局里加字段（比如新增一种 tab）后端一行都不用改；代价是坏数据只能在前端拦——
而前端本来就有这个纪律（`treePrefs.isPrefs` 逐字段查类型，不信 `as`）。

## 4. 后端设计

### 4.1 `internal/store/workbench.go`

照现有 `CREATE TABLE IF NOT EXISTS` 的路子加两张表：

```sql
CREATE TABLE IF NOT EXISTS workbench_bases (
  base_key   TEXT PRIMARY KEY,
  payload    TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS workbench_singletons (
  key        TEXT PRIMARY KEY,   -- 'selected' | 'dock'
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
```

`workbench_bases` 是「多行、有上限、按 key 索引」的那一类；`workbench_singletons` 装的是
**整个控制台只有一份**的两样东西——当前选中目录、悬浮窗现场。两者形状不同（前者要淘汰、
后者永远两行封顶），所以不合表。

单例用 kv 表而不是两张 `CHECK(id = 1)` 的单行表：两个单例就已经让「每样一张表」显得
啰嗦，而将来若再多一个单例，kv 表不用改 schema。

方法：

- `ListWorkbench() ([]WorkbenchBase, map[string]string, error)` —— 全部行 + 全部单例
- `PutWorkbenchBase(key, payload string) error` —— upsert，写完就地裁到 50 行
- `DeleteWorkbenchBase(key string) error`
- `PutWorkbenchSingleton(key, value string) error` —— upsert
- `DeleteWorkbenchSingleton(key string) error`

上限淘汰做在 `PutWorkbenchBase` 里而不是定时任务：省一个后台 goroutine，而且「刚写完立刻裁」
的时机最准。裁剪 SQL 按 `updated_at DESC` 保留前 50。

沿本包既有纪律：叶子层错误 return 前不打日志，由调用方带上下文记录。

### 4.2 `internal/agentd/workbench_api.go`

三个端点，全部走 body 传参，不碰 path 转义（`base_key` 含 `/` 与 `@`）：

```
GET  /api/workbench/state          → { selected: string, dock: string, bases: [{base_key, payload, updated_at}] }
PUT  /api/workbench/state/base     ← { base_key, payload }   payload 为 null = 删除该行
PUT  /api/workbench/state/selected ← { base_key }             空串 = 无选中
PUT  /api/workbench/state/dock     ← { payload }              payload 为 null = 清空悬浮窗现场
```

`dock` 在 GET 响应里是字符串（没有现场时为空串），与 `payload` 同一条「后端不解析」的纪律。

`payload` 在线上是一个**字符串**（内容是前端序列化好的 JSON），不是嵌套对象。这与 §3 的
架构分界一致：后端不解析它，所以也不该让 JSON 解码器替它解析一遍。前端 `JSON.stringify`
后发出、`JSON.parse` 后消费。

`payload` 取 `null`（而非空串）表示删除该行。空串是一个合法但无意义的 payload，
用它当删除信号会让「前端 bug 发了个空串」静默变成「删掉用户的布局」。

鉴权走既有 `/api` 那一套，无新增。

`payload` 超过 256 KiB 拒 400。这不是防攻击（控制台会话在能力上本就等价于主令牌），
是防前端 bug——万一哪天有人把文件草稿塞进 `TabContent`，希望它当场 400 而不是把库撑大。

`base_key` 为空串拒 400。

### 4.3 proto 类型

`internal/proto` 加 `WorkbenchBase` / `WorkbenchStateResp` / `WorkbenchBaseReq` /
`WorkbenchSelectedReq` / `WorkbenchDockReq`，并在 `web/src/api/testdata/` 补对应 JSON，纳入既有 `contract.test.ts`。

## 5. 前端设计

### 5.1 `web/src/app/workbench/persist.ts`（纯函数，无 React 依赖）

- `PersistedBase` 形状，带 `v: 1`（将来改形状时判断要不要整份丢弃，同 `TreePrefs.v`）。
  它**同时装 `BaseDir` 元数据与 `Workbench`**：`kind` / `path` / `label` / `projectName` /
  `machine` 都要存。只存 `Workbench` 的话，恢复时拿着一个 key 却不知道它是哪台机器上的
  哪个目录、面包屑该写什么——而 key 本身（`path@machine`）不足以还原 `label` 与 `projectName`。
- `encodeBase(base: BaseDir, wb: Workbench): PersistedBase`
- `decodeBase(raw: unknown): { base: BaseDir; wb: Workbench } | null` —— 逐字段校验，
  坏数据整行丢弃并 warn，**绝不半信半疑地用一部分**
- `isEmptyWorkbench(wb): boolean` —— 所有组都没有 tab。**空 Workbench 编码为删除**
  （PUT `payload: null`），不存一行空记录：用户把一个目录的 tab 全关掉，就是不想再看见它，
  存一行空记录只会白占 50 行配额里的一格
- `pruneDeadSessions(wb: Workbench, liveIds: Set<string>): Workbench` —— 规则二
- `diffPayloads(prev, next): { changed: string[]; removed: string[] }` —— 比较两份
  「key → payload 字符串」，分出要写的与要删的

悬浮窗那一套**放在 `web/src/app/homedock/dockPersist.ts`**，不进本文件：悬浮窗与工作台是
两套互不认识的状态（`useHomeDock` 的边界注释写明了这条分界），合并会让工作台反过来依赖
`HomeTab`。它导出：

- `encodeDock(d: DockSnapshot): string` —— **剥掉每个 tab 的 `draft` / `baseSha`**
- `decodeDock(raw: string): DockSnapshot | null` —— 同款逐字段校验
- `pruneDeadDockSessions(tabs: HomeTab[], liveIds: Set<string>): HomeTab[]` —— 规则二用在悬浮窗上
- `clampGeom(g, vw, vh, inset): Geom` —— 恢复几何时按**当前**视口夹紧

另有一层纯函数 `web/src/app/workbench/restore.ts`，把「落盘状态 + 会话列表」合成一次可直接
灌入的恢复结果（抹死会话、补孤儿、夹几何）。抽出来的理由：这些判断全都不需要 React 也
不需要网络，留在 hook 里就只能靠 mock fetch 去测，而它们恰恰最该用表驱动逐条钉住。

### 5.2 `web/src/api/client.ts`

新增 `fetchWorkbenchState()` / `putWorkbenchBase(key, payload)` / `putWorkbenchSelected(key)`。

### 5.3 `web/src/app/workbench/useWorkbenchSync.ts`（取代 `usePtyRestore.ts`）

合并是必要的，不是顺手：水合必须**等两个请求都到齐**才能做一次。布局先到就摆出来、
会话列表后到再抹死 id，用户会看见终端 tab 闪一下。

```
Promise.all([fetchWorkbenchState(), fetchPtySessions('all')])
  → 用会话列表抹掉死 sessionId（规则二，工作台与悬浮窗各一遍）
  → 补上列表里有、状态里没有的孤儿会话（工作树 → byBase，home → 悬浮窗）
  → 一次性 hydrate（byBase + 悬浮窗一起）
```

`usePtyRestore.ts` 随之删除——它和布局恢复本来就是同一件事的两半，留着两个入口必然会有人
只改一边。它现有的两条纪律要原样承接进新 hook：`ranRef` 挡 StrictMode 的第二次 effect，
`cancelledRef` 与之配对管「结果还要不要」，两者都必须跨 effect run（用局部变量会让上一轮
cleanup 取消掉这一轮仍有效的请求，开发端 100% 恢复不出任何 tab）。

写回：监听 `byBase`，与「上次已落盘快照」`diffBases`，变了的行各自去抖 500ms 单独 PUT，
被删的行 PUT `payload: null`。悬浮窗同理——它整体是一个单例，变了就去抖 500ms PUT 一次。

不用「只监听当前基准」：`restoreTerminal` 会写**非当前**基准的行（恢复是后台动作，
故意不切走用户的选中态），只盯当前基准的话那些 tab 永远落不了盘。

500ms 去抖照着 `FileTab` 草稿层的既有取舍：拖分屏分隔条会连发几十次 resize。
**不挂 `beforeunload` 做 flush**——去抖窗口内丢掉的最坏情况是一次栏宽微调没存上。

`selected` 的写回同理：`base` 变化时去抖写一次。

### 5.4 `useWorkbench` 的改动

- 暴露只读的 `byBase`
- 新增 `hydrate(entries: Array<{ base: BaseDir; wb: Workbench }>, selectedKey: string)`
- 现有的十几个动作签名一个都不动

### 5.5 `useHomeDock` 的改动

- 新增 `hydrate(snapshot: DockSnapshot)`：一次性灌入 `tabs` / `activeId` / `windowOpen` /
  `geom` / `maximized`
- **必须把 `seqCounter` 与 `tabIdCounter` 两个 ref 一起播种**到已恢复 tab 的最大值。
  这两个计数器现在从 0 起，恢复出 `h1..h5` 之后再点「新建终端」会生成 `h1`——与已存在的
  tab 撞 id。`seq` 同理，会出现两个 `bash · home 3`。这条不做的话，功能看起来是好的，
  只在用户恢复后新建第一个 tab 时炸。
  注意 `adopt` 进来的孤儿会话其 `id` 是 **sessionId**（见 `Shell.tsx` 的调用），不匹配
  `h<n>` 形状——播种时只从匹配 `/^h(\d+)$/` 的 id 里取最大值，其余忽略。
- `hydrate` 后把 `placed` ref 置 true：恢复出来的几何就是用户上次摆的位置，
  不能被「第一次打开时按视口重摆」冲掉
- `windowOpen` 为 true 时**照实恢复**（浮窗直接是打开的）。这与 `adopt` 刻意不打开浮窗
  不冲突：`adopt` 收编的是「用户不知道存在的孤儿会话」，而恢复的是「用户上次亲手开着的窗」

几何恢复要过 `clampGeom`：上次在 27 寸屏上摆到 x=2000，这次在笔记本上打开，不夹紧就是
一个看不见的浮窗。夹紧规则复用 `setGeom` 里已有的那四条下界（`MIN_W` / `MIN_H` / `MARGIN` /
`topInset()`），再加上界 `x + w <= vw`、`y + h <= vh`。

### 5.6 `workspaceBase` 的 key 加机器维度（既有隐患，顺手修）

`tree/ProjectTree.tsx` 的 `workspaceBase()` 返回 `key: ws.path`，**不带机器名**。
而 `usePtyRestore.baseOfSession()` 里 home 基准是明确按 `~@machine` 分开的，注释还写了
「远端 home 与本机 home 必须分开：路径都叫『~』，但它们是两台机器上的两个目录」。
同一条道理对工作树成立，工作树这边却没做——两台机器上出现同路径的工作树时，它们的 tab 组
会撞进同一个 key 里混在一起。

今天这是内存态、影响面小；一旦落盘就被固化成主键，以后再改要迁移数据。因此在本 spec 内修：

```ts
key = machine ? `${path}@${machine}` : path
```

与 home 的 `~` / `~@machine` 完全同构。单机用户（`machine` 为空串）的 key 逐字节不变，
对现有行为零影响。`baseOfSession` 必须同步改，两边对不上就会出现「左栏点进这个目录，
恢复出来的终端却在另一个组里」。

## 6. 启动时序

`selected` 的恢复要等项目树，而树是异步的，所以水合分两步：

1. `byBase` 立刻灌入，但 `base` 保持 `null`
2. 树到位后校验 `selected` 还在不在树上：在就 `select` 过去，不在就停在未选中态（规则三）

第 2 步 `select` 用的 `BaseDir` **从项目树重新构造**（`workspaceBase(project, machine, ws)`），
不用 payload 里存的那份。理由：树上那份是当下的真相，`label` 会跟着分支改名一起变；
payload 里的是上次退出时的快照。用快照会让面包屑显示一个已经改掉的旧分支名。
（payload 里仍要存这些字段——`selected` 之外的目录不会走这条路径，它们的 tab 标题与
面包屑只能靠 payload 自己那份，见 §5.1。）

**悬浮窗不等树**：它不挂在任何目录上，第 1 步就能连同 `byBase` 一起灌入。

`selected` 只可能是工作树基准——home 走悬浮窗，不进 `byBase`，也就永远不会是 `selected`。

用户看到的是：启动 → 短暂空态 → 树到位的同一帧，目录选中、tab 与分屏一起出现。

## 7. 错误处理

| 情形 | 处置 |
|------|------|
| 拉不到状态 | 不吞，banner 报出原文，空布局启动（沿用 `usePtyRestore` 返回 `error` 让 Shell 展示的路子）。用户看到「什么都没了」时必须知道为什么 |
| PUT 失败 | `console.warn`，不重试、不弹层。下次布局变动自然会再写一次；为一次没存上的栏宽打断用户是过度反应 |
| `payload` 解析失败 | 那一行整个丢弃 + warn，其余行照常恢复 |
| `payload` 超长 / `base_key` 空 | 后端 400 |

## 8. 日志与注释（instrumenting-code）

- Go：`store` 层沿本包纪律不打日志（Open 与非法迁移两处例外之外）；`agentd` 层在三个 handler
  各打一条 Debug（key、payload 字节数、结果），400 分支打 Warn 带原因。淘汰真的删了行时
  打一条 Info（`裁剪工作台状态`，含删除条数）——这是「用户的旧目录悄悄消失」的唯一解释来源。
- TS：水合结束打一条 `console.debug`（恢复了几个目录、几个 tab、抹掉了几个死会话）。
  抹掉死 sessionId 与丢弃坏行各打一条 warn，带上 key。
- 每个新建文件写「职责 + 边界」文件头注释；每个导出函数写参数 / 返回 / 注意事项。

## 9. 测试

**Go**

- `store`：CRUD 往返；50 行上限淘汰（写第 51 行时最旧的那条消失）；单行表 `CHECK(id=1)` 约束；
  `DeleteWorkbenchBase` 幂等
- `agentd`：三个端点正常路径；400 三种（坏 JSON、超长 payload、空 base_key）；
  `payload: null` 触发删除；契约 testdata 纳入 `contract.test.ts`

**TS**

- `persist.ts`：编解码往返；坏数据各种形状（缺字段、类型不对、`v` 不认识、`sizes` 与 `groups` 不等长）
  一律返回 `null`；`pruneDeadSessions`（死 id 被抹、活 id 保留、无 sessionId 的 tab 不受影响）；
  `diffBases`（新增 / 变更 / 删除三类）
- `useWorkbenchSync`：两个请求都到齐才 hydrate（先到的那个不触发）；去抖只发一次 PUT；
  拉取失败时返回 error 且不 hydrate；StrictMode 下只跑一次
- `encodeDock` 剥掉 `draft` / `baseSha`；`clampGeom` 的四条下界与两条上界；
  `pruneDeadDockSessions`
- `useHomeDock.hydrate`：计数器播种（恢复 `h1..h5` 后 `newTerminal` 产出 `h6` 且
  `seq` 不撞）；`adopt` 进来的 sessionId 形 id 不参与播种；`placed` 被置 true
- `workspaceBase` / `baseOfSession`：带机器名与不带机器名两种 key 的回归，两处必须产出同一个 key

## 10. 明确不做

- 实时推送（一端改了另一端立刻看到）
- 文件草稿与左栏偏好上云
- 滚动位置 / 编辑器光标 / TUI 回合位置
- 冲突合并、版本号、乐观并发控制
- 移动端的渲染适配（分屏在小屏上怎么折叠是移动端那份 spec 的事，本 spec 只保证状态在那儿）

## 11. 已知取舍

桌面端与浏览器同时开在同一台机器上时，以**最后操作的那端**为准，另一端的布局变更可能被覆盖。
这是「不做实时推送 + 最后写入者赢」的直接后果，不是 bug。

`payload` 由后端当不透明字符串存储，因此**后端无法校验布局的结构正确性**。坏数据的唯一防线
是前端 `decodeBase` 的逐字段校验。这是 §3 那条架构分界的代价，接受。
