# 侧栏任务列表：顺序固定、分支名、状态圆点、终态 30 分钟保留

状态：**已获用户批准**（2026-08-29，用户在会话中逐条点名方案与配色，授权照做；
本 spec 是该口头的书面化，无追加设计）。
定级 **L2**（单子系统 web，不动跨子系统契约；Task.branch 线格式已有，零协议改动）。
plan 增量并入本文「实现决定」段（用户逐条点名到改法粒度，另立 plan 只会复述），
review / acceptance 照走。

## 问题陈述（现状读数 @ c11574713）

1. **打开一个任务，整个列表顺序就变**。两个重排源：
   - Shell 把「当前选中基准」的已打开行_partition 到最前
     （`web/src/app/shell/Shell.tsx:495-497`），打开任务会 `wb.select` 切基准，
     已打开行当场重排；
   - ProjectTree 把全部已打开行整组提到任务组最前
     （`web/src/app/tree/ProjectTree.tsx:641-670`），被打开的任务从任务流行原位
     跳到顶部，其余行整体下移。
2. **派发任务行认不出谁是谁**。charter 派发的任务 name 为空，行名回退
   plan_summary 开头——所有派发的提示词都以「你是 charter 流程的节点执行者。」
   开头（现状读数 `web/src/app/tree/ProjectTree.tsx:390`、
   `web/src/app/lib/taskName.ts:9-11`）。而任务线格式本就带 `branch`
   （`internal/proto/proto.go:227`，派发时 `task.Branch = ws.Branch`
   `internal/agentd/manager.go:980`，charter 卡分支形如 `cards/B285-review-2`），
   界面却没用它。
3. **行右侧圆点无状态语义**。TaskRow 对所有行硬编码绿色
   `StateDot tone="active"`（`web/src/app/tree/ProjectTree.tsx:1156`）——
   运行中、等待、已完成的任务行，连接着与断开的终端行，全部一个颜色。
4. **已完成的任务从任务组消失**。B288 起终态一律进「已结束」
   （`web/src/app/tree/archived.ts:6-9`），刚跑完的任务瞬间沉入折叠分组，
   用户还开着它的 tab 盯 diff，列表里却已经找不到它。
5. 终端行、文件行的圆点也不该是「恒绿」：终端应表达连接状态，文件应表达
   只读/已编辑/冲突/已删除。

## 方案

### 1. 顺序固定（同一目录内，行序不随打开/聚焦变化）

- 任务组行的顺序基准改为**显式 created_at 降序**。勘误（2026-08-29 真机读数）：
  原方案假定「任务流原序 = 服务端 created_at DESC」——那是一条单机查询的读数
  （`internal/store/store.go:416`），而侧栏消费的是 `scope=all` 跨机聚合响应，
  其同项目内行序实为新旧交错（04:10 → 07:46 → 02:55 …），且会随任务状态迁移
  漂移。前端显式按 created_at 排序后，行序只由恒定字段决定：打开、聚焦、
  状态迁移都不再挪动其他行，最新的任务在最上。
- 已打开的 tui 任务**不再置顶**：在任务流原位渲染成「已打开态」行
  （is-open 底色、点击聚焦 tab、拖拽携带 tab MIME）；普通任务行不再因它打开而
  上移。一行任务在列表里只有一个位置，打开前与打开后一致。
- 终端 / 文件已打开行保留在任务组最前（它们不是任务流成员，无「原位」可言）；
  去掉 Shell 的「当前基准置顶」分区后，其顺序 = 组序 × 列序 × 格序（即打开
  顺序），点击、切换目录都不再重排。
- 已打开 tui 的 taskId 在任务流里找不到（任务刚派发尚未进流、或任务已删）时，
  行追加在任务列表末尾，保持可见不闪烁。
- 项目行排序维持既有偏好（`treePrefs`，默认按活跃数）不动：它随任务状态迁移，
  不随「打开」动作变化，不在本次投诉范围。

### 2. 派发任务行显示分支名

- 命名口径（真机数据核对后收紧，2026-08-29）：`有效名 → branch → plan_summary →
  （无名称）`。其中「有效名」= name 非空**且不是 plan_summary 的前缀**——后端
  派发无名无 plan 文件名时会拿 prompt 前 20 字当名（现状读数
  `internal/agentd/manager.go:488 deriveName`，charter 提示词开头千篇一律），
  这种名恰好是 plan_summary 的开头，**可判定**；判定命中时界面改用分支
  （`cards/B285-review-2`）。真机核对：现存全部 bench-* / b293-* 好名均非摘要
  前缀，不受影响；「对卡 …」行全部命中，改显分支。
- 口径收口在 `taskDisplayName` 单点；ProjectTree 的内联 taskName、search.ts 的
  taskText 两处重复定义一并改为引用它。左栏行、tab 标题、面包屑同源同值自动生效
  （B288 建立的「同一口径」原则顺延）。
- 根因侧（后端 deriveName 对 prompt-only 派发取前 20 字）不动：改派发命名属
  账本/派发协议语义，另行立卡。

### 3. 状态圆点按行类着色（沿用 StateDot 既有基调 token，不发明新颜色）

| 行类 | 圆点语义 | 映射 |
|---|---|---|
| 任务行 / 已结束子行 | 任务状态 | `stateTone(state)`：running 绿、waiting_answer / waiting_review 琥珀（即用户说的「之前用的颜色」，token `bg-state-intervention`）、pending 空心、completed 灰、failed 红 |
| 终端行 | PTY 连接状态 | 连接正常绿（`active`），断开 / 判死 / shell 已退出红（`failed`） |
| 文件行 | 文件状态 | 干净（只读未动）绿、已编辑（有草稿）琥珀、冲突红、已删除灰（`done`） |
| 机器行 | 可达性 | 维持现状（可达绿 / 断连红），不改 |

- 终端连接状态的来源是**第一手事实**：TerminalTab 本就持有 WS 状态机
  （`api/pty.ts` 的 `onStatus`: connecting/open/closed，及 dead / exit 判定），
  加一条上报缝逐 tab 上抛，Shell 聚合后投影进 OpenItem。**不轮询**
  `/api/pty/sessions`（弃选：那是对每台远端机的新增周期请求，而组件内已有
  更新鲜的状态）。未上报前（会话建立中的一瞬）按连接显示，不闪红。
- 文件状态：已编辑直接由 TabContent.draft 判（Shell 已持有，草稿寄存缝现成）；
  冲突（保存 409 / 打开时草稿基线过期）与删除（读取 404）由 FileTab 上报——
  它是唯一知道这两件事的组件。文件 tab 被切走会卸载（keep-alive 只保终端组，
  `web/src/app/workbench/WorkbenchPage.tsx:341-346`），侧栏显示**最后上报值**，
  重新打开即刷新——agentd 无文件变更推送通道，这是既定取舍，不是新欠账。
- 文件态优先级：已删除 > 冲突 > 已编辑 > 干净（可分辨的最高严重级胜出）。

### 4. 终态任务 30 分钟保留

- 判据（纯函数 `recentlyCompleted`）：终态（completed / failed）且
  `now - updated_at < 30min`。updated_at 即终态迁移时间（任务终态后不再跳动，
  该读数见 `internal/proto/proto.go` Task.UpdatedAt 语义）。
- 满足「30 分钟内终态」**或**「tui tab 仍打开」的终态任务，留在任务列表原序
  位置（灰 / 红点标识终态），不进「已结束」；「已结束」分组同步排除这些行，
  不双列。30 分钟过后自然沉入「已结束」。
- 用户点名的场景（已完成 + tab 打开）被两个条件的并集覆盖；非打开的刚完成
  任务也获得同样的缓冲，规则只有一条，无例外表。

### 5. 数据缝：OpenItem 扩展

- `OpenItem` 增加可选 `tone?: StateTone`，只由 Shell 为 terminal / file 行计算
  注入（tui 行的圆点由 ProjectTree 从任务流取 `stateTone`——它本就持有任务）。
- Shell 新增 tab 级行状态表（tabId → 终端连接 / 文件问题），由 TerminalTab /
  FileTab 的上报缝写入，随 openedItems 变化修剪已关 tab 的残值。

## 用户故事

1. 我同时派发了 5 张 charter 卡，打开左栏不用点开任何一行就能靠
   `cards/Bxxx` 分支名认出每个任务；行首圆点告诉我哪张在跑（绿）、哪张在等工单
   （琥珀）。
2. 我点开一个任务看进度，列表里其他行的位置一格都不动。
3. 一个任务跑完了，我还在它的 tab 里看交付说明——它还在列表原位，灰点标识
   已完成；半小时后我早关了 tab，它沉进「已结束」。
4. 一台机器断网了，我开着的终端行圆点变红，不用点进去就知道这条终端连不上了。
5. executor 改了我打开着的配置文件并保存，我这边一保存就撞冲突——文件行圆点
   变红提醒我这件事还没处理。

## 实现决定（决策级落点，内部符号归 implement）

- 顺序：Shell 去分区；ProjectTree 合并渲染（已打开 tui 原位 + 孤儿追加 +
  终端/文件行置前）。
- 命名：`taskName.ts#taskDisplayName` 扩链，消灭两处内联重复。
- 圆点：`TaskRow` 接 tone 入参；MachineRow 不动；色调映射用 `StateDot` 既有
  DOT_CLASS token，零新 CSS。
- 上报缝：TerminalTab 增连接回调、FileTab 增文件问题回调，均为可选 prop
  （现有调用点 HomeDock / 测试不传时行为不变）。
- 保留窗口：`archived.ts` 导出 `recentlyCompleted`（含 30min 常量），
  ProjectTree 消费。

## 测试决定（接缝清单）

| 缝 | 符号 + 调用方 | 断言 |
|---|---|---|
| 命名投影 | `taskDisplayName` ← ProjectTree 行 / Shell resolver / search 谓词 | 表驱动：name / branch / plan_summary / 全空四档 |
| 终态保留窗 | `recentlyCompleted` ← ProjectTree 任务组与已结束过滤 | 表驱动：终态 29min/31min、非终态、非法时间 |
| 树渲染 | ProjectTree.test.tsx | 打开任务不改变行序；圆点 tone 按状态/行类；30 分钟内终态在任务组且不在已结束；孤儿已打开 tui 追加末尾 |
| Shell 投影 | Shell.test.tsx | openItems 顺序 = 打开顺序（改写原「当前基准置顶」断言）；终端/文件 tone 进 OpenItem |
| 终端上报 | TerminalTab.test.tsx | open→connected；closed/dead/exit→断开；connecting 不上报 |
| 文件上报 | FileTab.test.tsx | 409/过期草稿→conflict；404→deleted；解决→ok |

假缝自查：全部符号有生产调用方（表列即出处），无「为测而抽」。

## Out of Scope

- 未归属分组里已打开任务的重复渲染隐患（打开基准落到某项目时可能双列）——
  现状既有，观察后再立卡。
- 文件状态的磁盘轮询 / watcher——agentd 无推送通道，等协议侧有事件再议。
- projectSort 的「活跃数」排序档、目录行排序——有偏好菜单，不在本次投诉内。
- HomeDock 浮窗 tab 的圆点——浮窗不在侧栏投影里。
- 终端行连接状态对「机器可达性」的区分——断网与判死对用户是同一件事：连不上。

## 备注

- 形态基准延续 B288：颜色一律用 `StateDot` 现有 token（用户点名「反正是之前
  用的颜色」即 `bg-state-intervention` 琥珀）。
- 30 分钟为用户口头给定值，落为常量；若将来要可配，归设置页另立卡。

## 追加轮（2026-08-29 二次优化，用户口头点名两条，随本 spec 归档）

1. **已打开行悬停 × 快速关闭**：悬停终端行、已打开 tui 行（含已完成的派发
   任务）、文件行时，行右侧滑出 ×，点击关闭对应 tab。
   - 覆盖面按「行 = tab」判定而不是按用户枚举的三类：所有已打开行（终端 /
     文件 / tui，运行中或终态）都带 ×——它们都是打开着的 tab，关闭只是收起
     视图（tui 关 tab 不杀任务）；普通任务行与已结束子行没有 tab，不给 ×。
   - 关闭走与窗格 × **同一条** `beforeCloseTab` 守卫（Shell.closeOpenItem →
     wb.closeById）：带会话的终端先确认「关闭并终止」、脏草稿文件先确认
     「不保存，关闭」——左栏 × 是另一个入口，不是另一条规则。
   - × 是行 button 的兄弟节点（button 不能嵌套），点击不触发行聚焦；悬停期间
     机器名淡出让位，状态圆点保留。
2. **排序口径统一**：任务组已是 created_at 降序（正文 §1）；「已结束」子行与
   「未归属」行此前仍吃任务流原序（archived.ts 注释里「任务流按 created_at
   降序」的前提对 scope=all 聚合不成立，是 §1 勘误的漏网处），一并改用同一个
   模块级 `createdDesc` 排序键。新测试钉住两处行序。
