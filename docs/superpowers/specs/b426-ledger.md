# B426 台账——现状读数与排查记录

> 记法：每行 = 日期 + 命令/读数原文 + 结论指针。spec 正文只留结论，原始读数在此。

## 2026-10-07 会话内排查（协调者本机，main @95f90fa4）

- `handoff card list`（18 张现役卡）：状态分布 待办7 / acceptance6 / finish3 / spec2 —— 五锚点字面量中 进行中/待审阅/已完成/终止 **零张**（问题③根因的分布证据）。
- `curl -w time_total http://127.0.0.1:7777/api/...`（本机 agentd，console ticket 换会话 cookie，测后 cookie 已删）：
  - `/api/cards` 0.131s / 0.123s / 0.149s / 0.114s / 0.142s，10KB，18 张卡
  - `/api/flows` 0.185s，13KB；`/api/ledger/health` 0.190s；`/api/queue` 0.016s；`/api/decisions?open=1` 0.008s
- `~/.handoff/config.yaml` dsn：`postgres://handoff:***@100.84.251.46:54322/handoff` —— 账本库在远端（tailscale IP）。
- `nc -z 100.84.251.46 54322`：连接成功，总耗时 0.022s —— 裸 TCP RTT ≈22ms；`/api/cards` ≈150ms 与 7 次顺序 DB 往返吻合。
- `internal/ledger/derived.go:39-110`（`listCards`）：主 SELECT 后顺序执行 allRelations / allStatuses / needsMap / baseFrozenMap / openDecisionCount / allParents 共 6 条派生查询。
- `internal/relay/dialer.go:267-340`（`ensureTunnel`）+ `:346`（`DialContext`）：单条常驻 WSS + E2E + yamux，每 HTTP 连接仅开新流——relay 无每请求握手，排除 relay 结构性放大。
- `internal/mobilecore/proxy.go:27-58`（`newReverseProxy`）：回环反带与桌面同源走 relay 隧道，不注入凭据。
- 历史性能卡：B374（房间 attach 刷新风暴）、B409（全账本读路径分域与投影性能，已完成）、B413（下钻 transition 饿死，已完成）——本轮为 B409 后残余形态。
- `handoff workflow show charter`：`Def.board.columns = ["代办","沟通中","进行中","审核中","结束"]`，`state_to_column` 含 spec→沟通中、plan/implement/breakdown/contract→进行中、review/acceptance/integrate/图对账→审核中、finish/已完成/终止→结束；「代办」错字在账本工作流定义内。
- `web/index.html:7`：viewport meta 无 `interactive-widget` 键。
- `mobile/android/app/src/main/AndroidManifest.xml:38-41`：WebviewActivity 无 `windowSoftInputMode`；`res/values/themes.xml` 仅背景色，无软键盘配置。
- 形态权威：`prototypes/mobile-app/pages/mobile-projects.html`（项目卡流：apphead+图标+名称+›+位置chips）、`pages/mobile-cards.html`（apphead 卡+记一张 / needs-strip / 五列 filter chips / 单列卡流；注③「状态词表不变=五锚点」的假设被实测分布证伪）。
- 关键代码读数（file:line，2026-10-07 @95f90fa4）：`web/src/app/rooms/SessionSidebar.tsx:78-106`（compact 两段筛选）；`web/src/app/cards/CardsPage.tsx:269`（status 严格等值过滤）、`:439-488`（compact 头三行 min-h-11）、`:501-518`（桌面 header 同组五锚点 chips——同一 bug 双端存在）；`web/src/app/cards/statusVocab.ts:9`（CARD_STATUSES 五锚点）；`web/src/app/cards/QueuePanel.tsx:61`（大边框盒）；`web/src/app/workbench/TabBar.tsx:129`（组标签条 h-11）；`web/src/app/workbench/WorkbenchPage.tsx:397-423`（窗格标题行 + ⋯）；`web/src/app/rooms/SessionTab.tsx:142-154`（compact 群聊|详情 header）、`:86`（registerSessionDetailOpener→setDrawerOpen，compact 无消费者）；`web/src/app/shell/Shell.tsx:1286-1317`（mobile-detail-bar）；`web/src/app/rooms/SessionChat.tsx:297-311`（footer composer）；`web/src/app/tree/ProjectTree.tsx:534`（compact 复用桌面树）。

## 2026-10-07 plan 节点复核（执行者，main @95f90fa4）

- 台账上行「关键代码读数」全部 12 处 file:line 逐条对照真码复核：**全部吻合，无与 spec 相悖的事实**。要点补记：
  - `SessionSidebar.tsx:85/:89` compact chips onClick 带 `if (!needsOnly)` / `if (needsOnly)` 防翻转——S1 toggle 语义要翻转它，且 :79-83 注释同步改写（B369.10 岔口 5 旧语义与新裁决相左）。
  - `columns.ts:12` `DEFAULT_BOARD_COLUMNS=['代办','沟通中','进行中','审核中','结束']`（错字在词表内，本卡不改）；`:20` 未知状态→「进行中」；`:60-65` `mergedLayoutFor` 未知流退默认映射；`:67` `boardColumnFor`；`:99-102` `cardsInColumn` 带 `!card.following` 折叠语义——S3 chips 过滤换源**不得**套它（现状过滤不排 following）。
  - `CardsPage.tsx:217-236` 既有 `boardLayout`/`layoutForWorkflow`/`cardLayoutResolver`/`displayedColumns`（单流视图取该流列序、合并视图恒默认五列）——S3 chips 换源与桌面看板同源已具备，零新映射。
  - `Shell.tsx:1192` compact 项目 tab 挂载点渲染 `{projectTree ?? …}`（projectTree=桌面 ProjectTree 唯一实例 :897 起）+ MobileProjectDetail 覆盖层 :1204——S2 只换前者。
  - `Shell.tsx:1159` compact 覆盖层让位条件 `compact && !nav.detail`；WorkbenchPage 有 singleFocus 先例（Shell 按 viewport 下传 prop、组件不自读视口）——S5 判据沿同款通道。
  - `web/package.json` `test` = `vitest run`、`typecheck` = `tsc -b`——验收命令确认。
  - `mobile/android/app/src/main/AndroidManifest.xml:40-41` WebviewActivity 声明确认无 windowSoftInputMode（台账记 38-41 为 activity 元素区间，行号口径一致）。
  - `projectColor.ts` 现产出 `text-project-N` 文字色类（FNV-1a 按 id 哈希、CLASSES 字面量数组 + index.css --project-N 对齐硬约束）——S2 图标底色若加 bg 类须同步 index.css 组数。
- 产出物：`docs/superpowers/plans/b426-plan.md`（S1-S6 一轮实现，工作分支建议 b426-mobile-walkthrough）。

## 2026-10-07 implement 节点（执行者，分支 b426-mobile-walkthrough）

### Step 0 开工基线

- `git checkout -b b426-mobile-walkthrough main` → `95f90fa4`（与 plan 基线一致）。
- `cd web && npm test`（vitest run）原文末段：
  ```
  Test Files  140 passed (140)
       Tests  1709 passed (1709)
  ```
  基线全绿成立，开工。

### Step 1（S1 会话筛选行）

- 红先于实现：`npx vitest run src/app/rooms/SessionSidebar.test.tsx` → `1 failed | 12 passed`，失败原因 = select（session-project-filter）不在 session-filter-chips 行内（功能缺失，非 typo）。
- 实现：`SessionSidebar.tsx` compact 两段（chips 行 + 项目筛选行）合一——「⚑需要你 N」toggle chip（`onClick={onToggleNeeds}` 直 toggle，删 `if (!needsOnly)`/`if (needsOnly)` 防翻转守卫）+ 项目 select + 计数右对齐；「全部」chip 删除；:79-83 旧注释改写为 toggle 新语义。桌面 `!compact` 段零改动。
- 绿：`npx vitest run src/app/rooms/SessionSidebar.test.tsx` → `13 passed (13)`，含承重断言「S1：筛选一行三元素（needs-count/项目/计数）；点已选「需要你」取消回全部；无「全部」chip」与桌面反例锁（toggle 行原样、chips 行不渲染）原样通过。

### Step 2（S3 状态 chip 归看板五列，双端）

- 红先于实现：`npx vitest run src/app/cards/CardsPage.test.tsx` → `7 failed`（chip 行仍渲染五锚点词表：card-status-沟通中 等列名 testid 不存在、期望文本撞车），失败原因均为功能缺失。
- 实现（CardsPage.tsx）：
  - compact 行 2 与桌面 header chips 两处 `CARD_STATUSES.map` → `displayedColumns.map`（chips 列序与桌面看板同源；label 逐字跟列名，「代办」照写）；aria-pressed / toggle / testid 结构原样。
  - `filtered` 过滤换 `boardColumnFor(card.status, cardLayoutResolver(card)) === statusFilter`（真实解析链 mergedLayoutFor/layoutForWorkflow），**未套 cardsInColumn**（避开其 `!card.following` 折叠语义，plan 判据）；deps 补 cardLayoutResolver。
  - 移除 statusVocab import（noUnusedLocals 开启）；statusFilter 状态注释与两处 chips 段注释改写为新语义。
- 波及与修复（按 plan §6 预警）：
  - 既有用例「选中工作流时按其看板映射渲染五列」`findByText('收集')` 撞单流 chips 新列名——列头断言收紧 `selector: 'section header span'`（改期望不删断言）。
  - 台账勘误：本执行者测试首写 card-status-待办——列名是现行词表「**代办**」（错字在账本工作流定义内），已修正；词表错字本卡不改。
- 绿：`npx vitest run src/app/cards/` → `Test Files 11 passed (11) / Tests 137 passed (137)`，含必含断言：
  - 「必含①：工作流状态卡归入语义桶——charter 流 spec 卡点「沟通中」chip 后可见（桌面 header chips）」（走真实 flows mock + mergedLayoutFor，非 mock 布局）
  - 「必含①（compact 同断言）」
  - 「必含②：未知流兜底——flows 未含该卡 workflow 且状态非映射串 → 归「进行中」chip」（正反两面：进行中可见、代办不可见）
  - 「点已选 chip 取消回全部（toggle 原样保留）」
  - 双端 chips 同断言成立 = 同 bug 同修证据；`grep card-status-|CARD_STATUSES` 退出 cards 域外零引用。

### Step 3（S4 卡页头部收敛）

- 红先于实现：`npx vitest run src/app/cards/CardsPage.test.tsx src/app/cards/QueuePanel.test.tsx` → `5 failed | 38 passed`（行 3 三件仍在主面、抽屉无筛选区、QueuePanel 无 compact 分支=功能缺失）。首跑另有 2 处测试笔误（CardsPage.test 未导入 userEvent），属测试代码问题非实现问题，补 import 后收口。
- 实现：
  - `CardsPage.tsx`：compact 头部三行收敛两行——行 1 = 工作项+健康指示+⚑需要你(ml-auto)+＋新建+从浏览器打开；行 3 整块删除；`drawerFilters`（项目/工作流/搜索三件，testid `card-drawer-filters`）经新 prop `compactFilters` 注入 CardDrawer；QueuePanel 传 `compact`。
  - `CardDrawer.tsx`：新增 `compactFilters?: ReactNode` 内容槽，渲染于 header 之下、滚动区之外（aside flex-col，固定顶部）；桌面不传不渲染，aside 类串与块序逐字节不动。
  - `QueuePanel.tsx`：新增 `compact?: boolean`——compact 容器 `shrink-0 border-b px-3 py-1.5` 细横条；缺省桌面 `mx-4 mt-2 rounded-lg border bg-background p-3` 大盒逐字节不动；展开 state/aria-expanded/列表渲染不变。
- 执行者裁量（plan §3 Step 3 授权）：「从浏览器打开」落 **行 1 尾**——它是页面级控制（打开当前 /cards 页），放卡抽屉里语义错位（会被读成"打开这张卡"）；仅桌面薄壳 UA 渲染，390px 移动面不受影响。
- 形态对照（S4 ↔ `mobile-cards.html`）：行 1 = apphead（标题左、＋新建右）✓；行 2 chips 对应原型 .filter 单行——**词表偏离原型注③（五锚点）**：按用户 2026-10-07 裁决改看板五列（台账 plan 节已记原型注③被实测证伪）；原型无行 3 次级控件 → 主面已撤出 ✓；原型无排队大盒 → 细横条 ✓。
- 绿：`npx vitest run src/app/cards/` → `Test Files 11 passed (11) / Tests 141 passed (141)`，含承重断言：
  - 「compact 主面无行 3 三件控件；+ 新建升行 1；从浏览器打开保留（行 1 尾）」
  - 「抽屉顶部筛选区三件生效：设筛选 → 关抽屉 → 背后列表 filtered 反映；重开抽屉筛选保留」（select beta → 关抽屉 → alpha 卡消失 beta 卡在场 → 重开值仍 beta）
  - 「QueuePanel compact 细横条：收缩态无大边框盒；展开仍列完整队列」（CardsPage 集成 + QueuePanel 单测双面）
  - 桌面既有断言原样通过（桌面 header 逐字节未动的证据）。

### Step 4（S2 项目页对齐原型）

- 红先于实现（新缝符号双段）：①测试先行 → `Failed to resolve import "./MobileProjectList"`（编译红，核实非拼写错）；②落空壳（return null）→ `5 failed (5)`（断言红，证断言有牙）；③实现 → 绿。
- 实现：
  - 新组件 `web/src/app/tree/MobileProjectList.tsx`（tree 域，与 MobileProjectDetail 同域）：apphead（「项目」+「＋添加项目」）+ 项目卡流（图标=名称前两字符+哈希底色块、名称、›；第二行位置 chips）。纯投影、零请求、不识 router。
  - `projectColor.ts` 新增 `projectColorVar(projectId)`（返回 `var(--project-N)`，同一 FNV-1a 哈希族）；图标底色经 inline style `color-mix(... 18%, white)` 消费——**不走 Tailwind bg 类**，理由与「拼类名 v4 静态扫描静默失效」的坑写进组件与函数注释，零 index.css 同步负担。
  - `ProjectTree.tsx` 仅一处 `export` 关键字：`locationActiveCount` 导出供 MobileProjectList 复用（tasksOfWorkspace + running/waiting_answer/waiting_review 口径同源，plan 明令不另立第二套；行为零变化，ProjectTree.test 98 测原样绿）。
  - `Shell.tsx:1192` 挂载点：`{projectTree ?? …}` 换 `<MobileProjectList projects={treeState.data.projects} machines={treeState.data.machines} tasks={tasks} onOpenProject={nav.setProject} onAddProject={setWizardOpen 通道}>`；MobileProjectDetail 覆盖层与 relative 容器保留（B369.10 保挂载手法不动）；桌面 aside 消费零改动。
  - 测试笔误修正两次：图标首两字符是 'ha'（plan 权威=前两字符；原型 mock 'ho' 与其项目名不自洽，不跟）；chips 断言改 testid+textContent（getByText 只匹配直接文本节点）；组件内分隔点用显式字符串段（JSX 吞换行缩进空白）。
- **ProjectTree compact 特判裁定：保留**（依据落账）：S2 后 grep 确认 ProjectTree 唯一挂载点=Shell:897（`!compact` 门内消费），compact prop 恒 false 成死参数；但移除需改写 ProjectTree 内 43 处 compact 消费点及关联测试用例，虽渲染输出不变，但违反 plan §2「桌面 aside ProjectTree 逐字节不动」的验收承诺、churn/回归风险远超收益。两种裁定 plan 均授权，选保留；死参数清理留待专门卡。
- 形态对照（S2 ↔ `mobile-projects.html`）：apphead（标题 flex-1 + 右上「＋添加项目」pill）✓；卡=白底圆角块（top 行：36px 圆角图标块+名称+›）✓；第二行位置 chips（绿点/灰点+机器名+「N 活跃」amber/「离线」）✓；零活跃裸机器名（原型卡三）✓。
- Shell.test compact 项目族 18 用例随语义更新（改期望不删断言意图）：下钻助手改 `openMobileProjectDetail`/`openWorkspaceTerminal`（卡→详情→wt 卡双动作）；离线用例改「卡 chip 离线 + 详情 pill disabled」承接不降级只读语义；:1585 改写为 S2 反例锁（树轨/worktree 计数/⌘K 搜索框/页脚/流程代码图死按钮全部不在场）；「工作项」行钮能力随树复用退役（原型形态无此钮，卡页走底栏），单独用例锁缺席；「浏览器返回键一致性」按详情层新路径验证 POP 落 `/?tab=projects&project=p1`。
- 绿：`npx vitest run src/app/shell/ src/app/tree/` → `Test Files 20 passed (20) / Tests 374 passed (374)`。

### Step 5（S5 会话房间头部收敛）

- 红先于实现：`npx vitest run src/app/rooms/SessionTab.test.tsx` → `4 failed | 14 passed`（受控 paneDetail/无 tablist/详情头部/注册表投递 = 功能缺失）。
- 实现：
  - **判据与通道**：`sessionRoom = compact && nav.detail && focusedSessionId !== null`（focusedSessionId 从 Shell 既有 focusedTabOf 投影取）；WorkbenchPage 新 prop `sessionRoom` 下传（singleFocus 同款通道，组件不自判 nav/视口）；SessionTab 的 paneDetail **上提 Shell**（裁定：roomDetailOpen 真值源在 Shell——三条约束「⋯仅群聊态渲染/详情返回仅回群聊/任一时刻只渲染一条 header」要求 Shell 知道详情态，内部态+上报会让出房间再进时头部与内容失同步）。
  - **Shell**：房间头部（群聊态）= ‹返回（分派复用同一 detailBack 回调）+ 房间标题 + ⋯（经 openSessionDetail 既有注册表投递，不新开缝）；详情态（roomDetailOpen=true）整个让位给 SessionTab 自渲染头部；既有「返回条+裁决横幅」容器原样保留给终端/任务下钻。roomDetailOpen 在焦点会话变化/出房间时重置（防「头部说群聊、内容在详情」失同步）。Shell.tsx 期间 2 处 JSX 括号配错（tsc 即红），当轮修正。
  - **SessionTab**：「群聊|详情」tablist 删除；详情面板自渲染头部（sticky，左上返回→onPaneDetailChange(false)、无 ⋯）；注册表回调 compact→上报 true/桌面→setDrawerOpen(true) 原样；Esc→上报 false；paneDetail 受控、缺省 false 兼容既有调用点。
  - **WorkbenchPage**：sessionRoom 为真不渲染 TabBar 组标签条与窗格标题行（含拖拽柄/⋯/窗格切换/×）；缺省 false 桌面与终端/任务下钻逐字节不动。已知代价（房间内无多 tab 切换条，切 tab 先出房间）写进组件注释。
- 测试改写（改期望不删断言意图）：SessionTab.test 两态 describe 改受控语义（tablist 缺席/详情头部/注册表投递/Esc/草稿跨切换存活）；`role=tab` 断言在会话房间失效（TabBar 收敛的形态代价）——改由「房间头部在场」证明会话 tab 已开（其判据即焦点 tab 为会话）；from= 深链用例澄清两段路径：首击落在返回条（焦点 tab 未重建为会话→非房间分支），openOrFocus 重建后才是房间分支。
- 录得既有债务：**main @95f90fa4 的 `tsc -b` 本就不绿（15 个 TS 错误）**——SessionChat.test 8（historyExpired 缺参）、SessionSidebar expired prop 缺声明 2、SessionTab.test Duplicate ApiError 2、Shell.test/其他 3；vitest 不跑类型检查故测试全绿。本分支同清单 15 个（SessionTab.test 的 detailPanel 未使用已修），**对 main 零新增**；词表外债务留协调者裁决（本卡不修，修了会扩 diff 面）。
- 绿：`npx vitest run src/app/rooms/ src/app/workbench/ src/app/shell/ src/app/tree/` → `Test Files 50 passed (50) / Tests 825 passed (825)`，含 S5 承重断言（会话房间仅一条 header/⋯进详情/详情返回回群聊不出房间/header 返回出房间/终端下钻 chrome 反例四件套）。
