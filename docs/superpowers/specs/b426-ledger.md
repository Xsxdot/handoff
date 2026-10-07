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
