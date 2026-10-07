# B426 实现计划——移动端走查修复一批（S1-S6，一轮实现）

- **卡**：B426（L2，plan → implement → review → acceptance → finish）
- **spec**：`docs/superpowers/specs/b426.md`（已批准：用户 2026-10-07 三项裁决 + 派发走子 agent）；现状读数台账 `docs/superpowers/specs/b426-ledger.md`
- **基线**：main @95f90fa4（web 1709 测 / go 64 包全绿）
- **读者假设**：执行者零上下文。本计划给出输入位置、每步的承重断言与完成标准，边界内的实现判断保留给执行者。
- **计划范围声明**：本轮 S1-S6 全做，一轮实现；性能项已拆 B427 不做；S6 真机项挂起待用户设备（实现轮交配置核对 + web 全绿）。

## 0. 事实基线（plan 节点复核结论，2026-10-07）

台账全部 file:line 读数已逐条对照 main @95f90fa4 真码复核，**无与 spec 相悖的事实**。计划依赖的关键现状（均已证实，非假设）：

- S3 的归桶机制与 chips 同源列序**已存在且 CardsPage 已在消费**：`web/src/app/cards/columns.ts` 导出 `DEFAULT_BOARD_COLUMNS`（= `['代办','沟通中','进行中','审核中','结束']`，含「代办」错字，本卡不改词表）、`mergedLayoutFor`（未知流退默认映射、未知状态落「进行中」，columns.ts:60-65 + :20）、`boardColumnFor`（:67）；`CardsPage.tsx:230-236` 已有 `cardLayoutResolver` 与 `displayedColumns`（单流视图取该流列序、合并视图恒默认五列——与桌面看板同源）。
- S1 现状 compact 两段筛选在 `SessionSidebar.tsx:84-106`；其 :85 现注释「点已选中的 chip 不再翻转」——S1 用户裁决是 toggle，**该注释随实现同步改写**，不许留矛盾注释。
- S5 判据所需数据 Shell 已持有：`compact && nav.detail`（Shell.tsx:1159 覆盖层让位条件、:1286 起 mobile-detail-bar）；焦点 tab 是否会话可从 `wb.wb` 组树判（tab.content.kind === 'session'）。WorkbenchPage 有 singleFocus 先例（Shell 按 viewport 下传 prop、组件不自读视口，WorkbenchPage.tsx:47-56）——S5 判据沿同款通道下传。
- S2 挂载点：`Shell.tsx:1192`（`nav.tab === 'projects'`）现渲染 `{projectTree ?? …}`（projectTree 是桌面 ProjectTree 唯一实例，Shell.tsx:897 起）+ MobileProjectDetail 覆盖层（:1204）。S2 只换前者，后者保留。
- S4 抽屉：`CardDrawer.tsx` 已有 compact 分支（:275 起、:688 行式块序）；S4 的筛选区加在 compact 分支顶部，绑定 CardsPage 既有 state（project/workflow/search 住 CardsPage，不随抽屉卸载——「关闭后筛选保留」由 state 住所天然满足）。
- 测试入口：`cd web && npm test` = `vitest run`（web/package.json）；`npm run typecheck` = `tsc -b`。
- S6 两处落点：`web/index.html:7`（viewport meta 无 interactive-widget）、`mobile/android/app/src/main/AndroidManifest.xml:40-41`（WebviewActivity 无 windowSoftInputMode）。

## 1. 交付目标

本轮交付 spec §4 全部六个故事，全部改动 compact 分支收口，**唯一双端例外是 S3 的 chip 组**：

| 故事 | 一句话 | 做到哪里算完 |
|---|---|---|
| S1 | compact 会话筛选收一行（需要你 toggle + 项目 + 计数），删「全部」chip | 组件断言过 + 桌面筛选行零改动 |
| S2 | compact 项目 tab 换原型卡流（tree 域新组件），零新端点 | 组件断言过 + 与 `mobile-projects.html` 形态对照 + 桌面 ProjectTree 零改动 |
| S3 | 卡状态 chip 以看板五列为准（双端同组 chips 同修） | 含「归桶」「未知流兜底」两条必含断言 + 桌面其余零改动 |
| S4 | 卡页行 3 撤出主面（筛选进抽屉顶部）、排队中收细横条 | 组件断言过 + 筛选关抽屉后保留 |
| S5 | 会话房间只留一条 header（返回+标题+⋯），⋯进详情态 | 组件断言过 + 终端/任务下钻 chrome 回归反例 |
| S6 | viewport meta + AndroidManifest 两处配置 | 配置逐字核对 + web 全绿；真机挂起 |

不做（Out of Scope，spec §8）：B427 性能优化、「代办」错字词表、iOS 壳键盘专项、移动项目搜索、深色模式。

## 2. 改动边界

**工作分支**：`b426-mobile-walkthrough`（从 main @95f90fa4 拉，全程在分支上，合并方式归人）。

**文件集**（预期触达；超出此集的触达先对照决策权限 §5）：

| 域 | 文件 | 故事 |
|---|---|---|
| rooms | `web/src/app/rooms/SessionSidebar.tsx`（compact 筛选段） | S1 |
| cards | `web/src/app/cards/CardsPage.tsx`（chips 源与过滤、头部三行、抽屉筛选区）、`QueuePanel.tsx`（细横条）、`CardDrawer.tsx`（compact 顶部筛选区） | S3/S4 |
| tree | 新组件 `web/src/app/tree/MobileProjectList.tsx`（命名自定）+ `ProjectTree.tsx`（仅 compact 特判裁定，见 Step 4） | S2 |
| workbench | `web/src/app/workbench/WorkbenchPage.tsx`（TabBar/窗格标题行渲染条件） | S5 |
| shell | `web/src/app/shell/Shell.tsx`（S2 项目 tab 挂载点、S5 房间 header 与判据下传） | S2/S5 |
| rooms | `web/src/app/rooms/SessionTab.tsx`（compact tablist 删除、详情态头部、opener 语义） | S5 |
| 配置 | `web/index.html`、`mobile/android/app/src/main/AndroidManifest.xml` | S6 |
| 测试 | 上述各域既有 `*.test.tsx(x)` 对应更新 | 全部 |

**须遵守的架构方向与既有契约**：

- 零 wire 变化、零新端点、零壳绑定面变化；全部消费既有 `/api` 投影（S2 数据从 `useProjectTree` 的 `ProjectTreeResp` + tasks + `MachineStatus` 投影，Shell 均已持有）。
- S3 只**消费** `columns.ts` 既有函数族，不新增第二套映射；chips 列序 = `displayedColumns`（CardsPage.tsx:234-236，与桌面看板同源）。
- 「桌面端除 S3 chips 外零改动」是验收承诺：桌面 header（CardsPage.tsx:493-518 除 chips 段）、桌面 `!compact` 筛选行（SessionSidebar.tsx:61-77）、桌面 aside ProjectTree、桌面会话抽屉（SessionTab `!compact` 分支）全部逐字节不动。
- WebviewActivity 只加一行 manifest 属性，themes.xml 不动、不加 JS 兜底（spec §5 明令）。

## 3. 工作顺序

每步 = 一个可独立验证的可检查结果；每步收口跑一次 `cd web && npm test` 局部文件，全部步骤完成后跑全量（三段律沿用 implement：编译全量 / 测试局部 / 集成全量）。

**Step 0 开工基线**：拉分支；跑 `cd web && npm test` 确认 1709 全绿基线成立。若基线不绿，停下回报协调者（阻塞全部后续步骤，不得带病开工）。

**Step 1（S1 会话筛选行）** —— rooms 域，先做最独立的小步。
`SessionSidebar.tsx` compact 段（:84-106）两段合一段：⚑需要你 N（toggle chip）+ 项目 select + N 个会话（右对齐）；删「全部」chip；:85 与 :89 的 `if (!needsOnly)` 防翻转改为直接 toggle（点已选「需要你」= 取消回全部）。改写 :79-83 注释为新语义。
- 承重断言：compact 下一条筛选行同时含 `needs-count` / 项目筛选 / `session-total` 三个元素；「全部」chip 不再渲染；点「需要你」两次 needsOnly 走 true→false；`!compact` 桌面筛选行既有断言原样通过。

**Step 2（S3 状态 chip 归看板五列，双端）** —— cards 域。**这是唯一动桌面 JSX 的步。**
compact 行 2（CardsPage.tsx:452-469）与桌面 header chips（:501-518）两处：`CARD_STATUSES.map` 换 `displayedColumns.map`；过滤（:269）从 `card.status === statusFilter` 换 `boardColumnFor(card.status, cardLayoutResolver(card)) === statusFilter`。两点判据钉死：
- **不套 `cardsInColumn`**：它带 `!card.following` 折叠语义（columns.ts:101），chips 过滤现状不排 following，换源不得引入行为差。
- statusFilter 语义从「状态字面量」变「列名」；`data-testid="card-status-<label>"` 与 `aria-pressed` 结构保持，label 逐字跟列名（「代办」照写，不改词表）。点已选 chip 取消回全部的 toggle（:459-463）原样保留。
- 承重断言（spec §6 必含前两条）：①「工作流状态卡归入语义桶」——charter 流 status=`spec` 的卡点「沟通中」chip 后可见（走 `mergedLayoutFor`/`layoutForWorkflow` 真实解析链，不用 mock 布局顶替）；②「未知流兜底进行中」——flows 未含该卡 workflow（lookup 返回 undefined）且状态为非映射串的卡归「进行中」chip；③点已选 chip 取消回全部；④桌面 header chips 同断言成立（同 bug 同修的证据）；⑤既有按五锚点字面量断言的用例随语义更新期望值（改期望不删断言；快照型断言红了属预期，按新语义修）。

**Step 3（S4 卡页头部收敛）** —— cards 域，与 Step 2 同文件相邻。
- compact 行 3（:472-488）撤出主面：项目 select、工作流 select、搜索 input 三件移进 `CardDrawer` compact 分支顶部筛选区（绑同一组 CardsPage state——「关闭后筛选保留」由 state 住所天然满足，不新造持久化）。
- `+ 新建` 移 compact 行 1（spec §2 S4 行 1 清单：工作项+健康指示+⚑需要你+＋新建；原型 apphead「＋记一张」同位）。
- 行 3 的「从浏览器打开」（:477-487）spec 未点名：**保留能力**，落点（行 1 尾 / 抽屉顶部次行）执行者裁量并留注释，不得无删。
- `QueuePanel.tsx:61` 大边框盒收细横条：收缩态单行高（⧗ 排队中 N + 箭头），既有展开 state / aria-expanded / 队列列表渲染行为不变。
- 承重断言：compact 主面不再渲染行 3 三件次级控件；开抽屉可见三件且改值作用于背后列表（设筛选→关抽屉→列表 filtered 反映）；筛选在抽屉关闭后保留；QueuePanel 收缩态单行 + 展开仍列队列；桌面 header 段逐字节不动（既有桌面断言原样通过）。

**Step 4（S2 项目页对齐原型）** —— tree 域独立组件 + Shell 挂载点。
- 新组件（tree/，与 MobileProjectDetail 同域）：形态按 `prototypes/mobile-app/pages/mobile-projects.html`——apphead（「项目」+「＋添加项目」）+ 项目卡流（图标=名称前两字符+底色块、名称、›；第二行位置 chips=机器名 · N 活跃 / 离线）。纯投影呈现：项目=`ProjectNode[]`；位置=locations（机器名经 `machineLabel`）；离线=`MachineStatus.ok === false`（`locationProblem` 同源判据）；N 活跃=`tasksOfWorkspace` + running/waiting_answer/waiting_review 口径（与 MobileProjectDetail `isAwaitingUs` 同源，不另立第二套口径）。
- 底色：`projectColor.ts` 既有哈希取色；若需 bg 类（现产出 text-project-N），新增类须同步 `index.css --project-N` 组数（CLASSES 注释的硬约束），Tailwind v4 拼类名静默失效的坑写进组件注释。
- 交互：点项目卡 → 既有 `nav.setProject` 下钻 MobileProjectDetail（其 props/行为零改动）；＋添加项目 → Shell 既有 `onAddProject` 通道（添加项目向导）。
- `Shell.tsx:1192` 挂载点：`{projectTree ?? …}` 换新组件；MobileProjectDetail 覆盖层与 relative 容器结构保留（B369.10 的保挂载手法不动）；桌面 aside 的 ProjectTree 消费（`!compact` 门）零改动。
- **ProjectTree compact 特判裁定**（spec §5 授权 plan 裁定）：S2 后 grep ProjectTree 内 `compact` 消费点——若无其余 compact 挂载点，compact prop 成死参数，可移除（连带其测试用例）；若仍有消费者则保留。两种裁定都合法，裁定结果与依据落台账。
- 承重断言：compact 项目 tab 渲染项目卡流（项目名、位置 chips 含活跃数与离线态、›）；点卡进 MobileProjectDetail（既有下钻断言链）；＋添加项目开向导；ProjectTree 的 ⌘K 搜索框 / 树轨 / worktree 计数 /「流程与代码图暂未适配移动端」页脚不出现在 compact 项目 tab。

**Step 5（S5 会话房间头部收敛）** —— shell + workbench + rooms，动渲染条件的一步，回归反例必写。
- 判据（spec §5）：**compact && nav.detail && 焦点 tab 内容 kind === 'session'**。数据 Shell 已持有；通道沿 singleFocus 先例由 Shell 下传 prop（WorkbenchPage 不自读视口/不自判 nav）。
- Shell：会话房间下 `mobile-detail-bar`（:1286-1317）换成房间 header = ‹返回（出房间回会话列表，既有 detail-bar 返回语义复用）+ 房间标题 + ⋯更多（右侧）；⋯经 `openSessionDetail` 既有注册表投递，不新开缝。
- SessionTab：compact「群聊|详情」tablist（:142-154）删除；:86 注册回调改语义——compact 下 ⋯ 投 `setPaneDetail(true)`（桌面投 `setDrawerOpen(true)` 原样）；详情态自渲染头部（左上返回 → `setPaneDetail(false)` 回群聊、不渲染 ⋯）；Esc 关详情态既有行为保持。paneDetail 住所（SessionTab 内部 + 上报，或上提 Shell）执行者裁量，约束三条：⋯仅群聊态渲染、详情态返回仅回群聊不出房间、房间任一时刻只渲染一条 header。
- WorkbenchPage：判据为真时不渲染 TabBar（:475 组标签条）与窗格标题行（:397 起，含 ⋯ 与窗格切换钮）；判据为假一切原样。
- 防回归（终端/任务下钻，spec §6 风险位）——各一条反例断言：焦点 tab 为终端/任务时 TabBar、窗格标题行、task-verdict-banner、mobile-detail-bar 返回语义全部照旧；焦点 tab 为会话时这些 chrome 消失且仅房间 header 存在。
- 承重断言：会话房间仅一条 header；⋯进详情态；详情左上返回回群聊（不出房间）；header 返回出房间回会话列表；已知代价（房间内无多 tab 切换条，切 tab 先出房间）写进组件注释，与 spec §2 S5 记明一致。

**Step 6（S6 键盘遮挡，两处配置收尾）**
- `web/index.html:7` viewport meta 追加 `interactive-widget=resizes-content`（并入现有 content，不另起 meta）。
- `mobile/android/app/src/main/AndroidManifest.xml:40-41` WebviewActivity 声明加 `android:windowSoftInputMode="adjustResize"`。
- 不加 visualViewport JS 兜底（spec §5 明令）；themes.xml 不动；iOS 壳不加代码。
- 无单测接缝（spec §6）：证据 = 两处逐字核对原文落台账 + `cd web && npm test` 全绿 + typecheck 过。真机项挂起待用户设备，最终验收补（acceptance 节点责任）。

## 4. 验收方式

**命令**（本计划的验收步骤没有需要驱动派发系统自身的，全部可由执行者完成）：

```bash
cd web && npm test          # vitest run——1709 基线延长线，S1-S6 全绿
cd web && npm run typecheck # tsc -b
go build ./...              # 兜底；本卡预期零 Go diff
```

**每故事至少一条可失败断言**（必含项汇总；断言写法随既有测试惯例）：

| 故事 | 必含断言 |
|---|---|
| S1 | 一行筛选（三元素并存）；点已选需要你取消回全部；无「全部」chip；桌面行零改动 |
| S2 | 项目卡流渲染（图标/名称/›/位置 chips 含活跃数与离线态）；点卡进详情；＋添加项目可用；桌面树四件套（⌘K/树轨/worktree 计数/页脚）不出现在 compact |
| S3 | 「工作流状态卡归入语义桶」+「未知流兜底进行中」两条必含（真实解析链）；点已选取消；双端 chips 同断言 |
| S4 | 主面无行 3 三件控件；抽屉顶部三件生效；关抽屉筛选保留；QueuePanel 细横条可展开 |
| S5 | 房间仅一条 header；⋯进详情；详情返回回群聊；header 返回出房间；终端/任务下钻 chrome 反例 |
| S6 | 无单测接缝——配置逐字核对（原文落台账）+ web 全绿；真机项挂起 |

**证据保存**：每步的命令与原始输出边干边追加台账 `docs/superpowers/specs/b426-ledger.md`（plan 节点已开「plan 节点复核」节，implement 沿用该文件）。S6 的两处配置核对以 `sed -n` 原文摘录为证。形态对照证据：S2/S4 对照 `prototypes/mobile-app/pages/mobile-projects.html` / `mobile-cards.html`（形态权威），对照结论落台账。

**成功判据的行为口径**（防计数漂移）：验收钉行为不钉计数——「桌面上能点出归列正确的 chip」「房间里只看得见一条 header」「键盘上方可见输入框（真机）」；不以「改了 N 个文件 / 新增 N 行」为完成依据。

## 5. 决策权限

**执行者自定**（留注释与台账即可）：

- 组件内部命名与拆分（含 S2 新组件名；建议 `MobileProjectList.tsx`，非强制）。
- S5 paneDetail 的住所（SessionTab 内部上报 / 上提 Shell），受 §3 Step 5 三条约束。
- S4「从浏览器打开」按钮的保留落点；S2 ProjectTree compact 特判的去留裁定。
- testid 演进（如 `card-status-<列名>`、行 3 testid 的去留）——改 testid 必须同步对应测试。
- compact 头部/卡流的类名与布局细节（形态权威是原型 HTML，像素级对齐执行者裁量）。

**停下升级协调者**（碰到即停，不得先斩后奏）：

- 凡需**动桌面 JSX**（S3 双端 chips 之外）——桌面零改动是验收承诺。
- 凡需**动 API**：新端点、wire 字段、壳绑定面变化；S2 投影若发现既有 `/api` 数据不够（如活跃数口径缺数据），停下上报，不得加端点。
- 凡触**账本词表「代办」错字**（columns.ts `DEFAULT_BOARD_COLUMNS`、Go 侧工作流定义）——chip 标签逐字跟随现行列名，词表修正另立卡。
- 发现 S6 必须**加 visualViewport JS 兜底**才能成立——spec 明令不加，真机不够立新卡。
- Step 0 基线不绿、或 S3 改动导致无法在不删断言的前提下收口——停下回报。

## 6. 待验证假设与风险

| 假设/风险 | 状态 | 动作与阻塞范围 |
|---|---|---|
| vitest 能渲染各组件 compact 分支 | 高置信：B369.8/B369.10 系列已留 compact 用例 | Step 0 基线跑测即验证；不成立则阻塞全部步骤，回报协调者 |
| S3 双端改动波及桌面快照型断言 | spec §6 已预警 | 红了按新语义改期望值，属预期；无法收口则升级 |
| S5 动 Shell/WorkbenchPage 渲染条件致终端/任务下钻回归 | spec §6 已预警 | Step 5 反例断言钉死；review 节点复看 |
| S6 manifest 逐字加行的壳侧构建 | 无 CI 构建接缝（真机挂起同因） | 证据=逐字核对；用户真机走查若报壳异常，回本卡走 debug 流 |
| S2 位置 chips 数据够用（MachineStatus.ok + tasks 投影） | 已核对类型与既有消费，非假设 | 若实现中发现口径缺口 → §5 升级项，不加端点 |
