# TUI tab 对话式重构设计

日期：2026-08-17
状态：已确认（形态经可点击原型走查，见 §9）
前置：W4 shell calibration spec §2.3（TUI tab 定位）、W4b（结构化回合时间线）

## 0. 背景与病灶

TUI tab 是任务会话的主视图，现状是「四个盒子的仪表盘」：回合时间线（内部固定
384px 滚动区）、事件流、审阅取证、推进任务表单垂直堆叠。真机使用后确认四个病灶：

1. **嵌套滚动**：页面滚动 + 时间线内滚 + 事件流内滚 + diff 内滚，四层滚动条互相
   打架；主内容（会话正文）在大屏上只有一个 384px 的小窗口。
2. **无主次**：会话正文（主角）和原始事件流平起平坐，且信息重复——时间线里有
   EventMark，事件流又把同样的事件再列一遍。
3. **机器数据裸奔**：`#23 progress`、原始 type 名、截断的 JSON payload、无着色的
   diff、模型报工的 JSON trailer 原样展示。
4. **推进动作是表单不是对话**：与 spec「TUI 的终局是一个 agent」（W4 shell
   calibration §2.3 末段）背离。

本设计把 TUI tab 重构为「一个 agent 会话」：会话流当主角，事件人话化内联，审阅
取证右滑，推进动作变对话式 composer。

## 1. 已确认决策（brainstorm 澄清结论）

| 问题 | 结论 |
|---|---|
| 核心场景 | **盯进度为主**——实时会话流当主角，review 也要顺 |
| 事件流面板 | **取消**。关键事件人话化内联进会话流；原始事件收进调试抽屉 |
| 审阅取证 | **右侧分栏滑出**（waiting_review 时），非 review 态不占空间 |
| diff 呈现 | **按文件分组 + 增删着色**，自写 unified diff 解析器，不引库 |
| 视觉基调 | **跟随控制台主题**（shadcn token），靠层级/留白/字体分主次，不做暗色岛 |
| token/context 展示 | 补回页头常驻（现状 compact TaskHeader 把 usage/cumulative 裁掉了） |
| 基准分支 | **下拉选择**仓库分支，不手填（需后端接口，§6） |

## 2. 布局设计

整个 tab 分三段：薄页头 / 主区（会话流 + 审阅栏）/ composer。

### 2.1 页头（两行）

- **第一行（身份 + 动作）**：任务名（truncate）、状态徽章；右侧「审阅栏」开合
  按钮（仅 waiting_review 显示）与「调试」入口。
- **第二行（遥测，12px 浅色）**：`executor · 实际模型名 · 回合 N ▾ · 已运行时长
  · ▬ ctx X/Y`。
  - **回合 N 本身是跳转下拉**（列出各回合 + 起因 + 时刻，点击锚点跳转），不单设
    按钮；锚点只覆盖已加载范围的纪律保留（下拉底部注一行）。
  - **ctx 小表**：迷你进度条 + `ctx 41.2k / 200k`。executor 没报 `context_window`
    时退化为只显绝对值（前端不猜分母，沿用现有纪律）。点开弹出完整账目：
    「当前占用」（Usage）与「累计消耗」（Cumulative：输入/缓存命中/输出/合计/
    花费及可信度 hint）两口径。数据已在 Task 对象，无后端改动。
  - 断线/实时徽章仍在页头（现有 wsStatus 逻辑不变）。

### 2.2 会话流（唯一滚动区）

- **整个 tab 只有这一个滚动容器**；正文限宽 760px 居中。
- 现有逻辑全部保留：跟随滚动（stickBottom + 40px 阈值）、加载更早 + prepend
  滚动补偿、坏行提示、帧上限提示——提示样式改为流内元数据行，不再是独立横幅盒。
- **非正文元素统一为一套「元数据行」视觉语言**：左对齐、12px、muted 色、同一
  左轨、行首小符号区分类型；展开箭头悬停才显示。会话里带「表面」（背景/边框）
  的只有三样：审核者气泡、模型正文、交付摘要卡。
  - `◇` 生命周期事件（人话化，见 §4 eventPhrase）
  - `▸` 思维链：`思维链 · N 字` 一行折叠，展开为左边线引文块
  - `●` 工具行：状态点（成功绿 / 失败红 / 进行中琥珀脉冲）+ 工具名 + 参数/结果
    摘要，展开看输出（等宽、上限高度内滚）；未配对工具卡按任务态判定
    「进行中/未返回」的现有逻辑保留
  - `⚠` 权限请求（琥珀色）：摘要 + 裁决结果；全文仍只在工单面板，两区不混用
- **回合分隔线**：`── 回合 N · 起因 · 时刻 ──`，起因映射沿用（dispatch=派发、
  send=续发指令，未知原样显示）。
- **审核者指令气泡**：send 回合起点显示审核者 continue 指令原文，右对齐圆角气泡
  （width:fit-content + 右锚），身份行「审核者 · 时刻」。数据源见 §6.1；旧帧无
  指令原文时只显示回合分隔线（向后兼容）。
- **交付摘要卡**：对回合最后一个 text 块做报工 trailer JSON 提取（best-effort），
  解析出 branch/commit/summary 等字段渲染成卡片；解析失败原样当正文。不改协议。
- **原始正文切换取消**：原 RenderPanel（render.log 流）移入调试抽屉（§2.5）。

### 2.3 审阅栏（右滑分栏）

- waiting_review 进入时自动滑出；可手动收起/展开（开合状态本 tab 内记住）；
  宽约 44%（min 400px / max 620px），栏内自己滚动。
- 三个子 tab：
  - **改动**：基准分支**下拉选择**（默认「自动推导（默认分支）」，选中即重算）；
    统计行（相对分支 · N 个文件 · +A −B）；diff 按文件折叠分组（文件头带 ±行数），
    行级着色（add 绿 / del 红 / hunk 灰）。解析器自写（§4）。
  - **跑命令**：命令输入 + 运行；退出码徽章（0 绿 / 非 0 红 / 124 超时注记）+
    等宽输出。语义不变（非零退出也是 200）。
  - **读文件**：路径输入 + 等宽内容展示。
- 错误纪律不变：agentd 错误原文透出。

### 2.4 composer（对话式收口）

- 常驻底部，与会话流同宽（760px 居中）。上方一行状态提示语（人话说明当前能做
  什么），输入框圆角边框、内嵌工具条：
  - 左：**停止任务**（红字弱化按钮；终态隐藏）
  - 右：**完成任务**（waiting_review）+ **↑ 续发修改**（主按钮，waiting_review）
- Enter 发送 = continue，Shift+Enter 换行；发送后反馈「已续发指令，任务回到
  running」。
- done / stop 二次确认弹窗保留（不可逆操作纪律）。
- 状态联动见 §5；断线时禁用但保留已填内容（现有行为）。
- resume（waiting_answer）与 force 选项出现在提示行位置，替代当前表单区块。

### 2.5 调试抽屉

- 页头「调试」打开的弹层，两个子 tab：
  - **原始事件流**：现 EventsPanel 的原始列表（#seq / type / payload 摘要 / 时刻）
    整体迁入，实时追加与封顶丢旧逻辑保留。
  - **原始正文**：现 RenderPanel（render.log 流）整体迁入，按需连接（打开才挂流，
    关闭即断）。
- 存在理由不变：四家 adapter 帧质量不齐平，原始数据是区分「渲染错了」还是
  「采集错了」的关键证据。日常不可见。

## 3. 组件结构

| 组件 | 来源 | 动作 |
|---|---|---|
| `TuiTab` | 现有 | 重排为 页头/主区/composer 骨架；持审阅栏开合状态 |
| `ConversationStream` | TimelinePanel 重构 | 唯一滚动区；原滚动/加载逻辑平移 |
| `ThinkingBlock` | 现有 | 改元数据行折叠样式 |
| `ToolCard` | 现有 | 改元数据行样式 |
| `TextBlock` | 现有 | 正文化（无盒） |
| `UserInstructionBlock` | 新增 | 审核者指令右对齐气泡 |
| `EventChip` | EventMark 重构 | 人话化元数据行 |
| `DeliverySummaryCard` | 新增 | trailer 提取渲染 |
| `UsageChip` | 新增（逻辑自 TaskHeader 迁移） | 页头 ctx 小表 + 账目弹出 |
| `ReviewSidePanel` | ReviewPanel 重构 | 右滑栏 + DiffView |
| `DiffView` | 新增 | 按文件分组着色渲染 |
| `Composer` | AdvanceActions 重构 | 对话式输入 + 动作工具条 |
| `DebugDrawer` | 新增 | 收纳原始事件列表 + RenderPanel |
| `EventsPanel` | 现有 | 从 TuiTab 删除（原始列表逻辑迁入 DebugDrawer） |
| `RenderPanel` | 现有 | 移入 DebugDrawer，不再是时间线切换视图 |

TaskHeader 的 compact 分支被新页头取代；完整版 TaskHeader（若仍有使用处）不动。

## 4. 数据流与新纯函数

- `useTaskSession` / `useFramesStream` / `useRenderStream` **全部不动**。
- 新增纯函数（各配单测）：
  - `parseUnifiedDiff(text): FileDiff[]`——unified diff → 文件组（路径、±统计、
    行数组及类型）。只处理标准 `diff --git` 输出；解析失败整体回退裸文本展示。
  - `eventPhrase(ev): { text, level } | null`——事件 → 人话 + 显示级别；白名单
    映射（派发、权限请求/裁决、回合结束、失败、停止等），白名单外的生命周期
    事件保持原文小签展示，**不吞**；纯噪声（与帧内容重复的 progress）返回 null
    不进流。
  - trailer 提取函数——text 块尾部 JSON 的探测与解析。

## 5. 状态机联动

| 任务态 | 审阅栏 | composer |
|---|---|---|
| running | 隐藏 | 输入禁用，提示「回合结束后可下指令」；停止可用 |
| waiting_review | 自动滑出（可收起） | 可发送；完成任务可用 |
| waiting_answer | 隐藏 | 提示行显示恢复执行 + 强制收口（resume/force） |
| completed / failed | 隐藏 | 只读提示（终态说明 + done_note 若有）；全部动作隐藏 |
| 断线 / 会话失效 | 保持 | 禁用但保留已填内容；Banners 沿用 |

## 6. 后端配合（两项，均小改）

### 6.1 turn_start 帧带指令原文

现状 `proto.Frame` 的 turn_start 只有 `Reason`（dispatch/send）。新增可选字段
`Instructions`：send 时写入 continue 指令原文（四个 adapter 的写帧处 + proto 定义）。
前端旧帧无此字段时只渲染回合分隔线——向后兼容，不迁移旧数据。

### 6.2 分支列表接口

新增只读接口 `GET /api/tasks/{id}/branches`：返回任务仓库的本地分支名列表
与推导出的默认基准。供审阅栏「改动」的基准下拉用。失败时下拉退化为只有
「自动推导」一项，diff 功能不受影响。

## 7. 错误处理

全部沿用现有纪律，无新增错误面：agentd 错误原文透出、帧坏行计数提示、帧上限
提示、run 退出码语义、断线/会话失效 Banners。新增面的失败路径：diff 解析失败回
退裸文本；trailer 解析失败回退正文；分支接口失败退化下拉；旧帧无 Instructions
只显分隔线。**所有回退都是降级展示，不吞数据、不报错打断。**

## 8. 测试

- 纯函数单测：`parseUnifiedDiff`（多文件/单文件/空 diff/非法输入回退）、
  `eventPhrase`（白名单内/外/噪声过滤）、trailer 提取（合法/非法/无 trailer）。
- 组件测试迁移改造：TimelinePanel → ConversationStream、EventsPanel →
  DebugDrawer、ReviewPanel → ReviewSidePanel、AdvanceActions → Composer；
  TuiTab 骨架与状态联动（§5 表逐态）。
- 后端：turn_start Instructions 写入（send 路径）、branches 接口的 handler 测试。

## 9. 验收基准

形态已经可点击原型确认（`prototypes/tui-redesign/`，fork 自 `prototypes/base/`；
确认状态已记入 `prototypes/base/README.md`，确认中）。真实页面开发完成后对照该
原型形态验收；fork 副本不入库，若届时已被清理，以本 spec §2 + base/README.md
的记录为准。

原型走查中确认的三处细节修正一并纳入基准：页头两行不挤、会话流元数据行统一
视觉语言、审核者气泡靠右、基准分支下拉选择。

## 10. 范围外

- 不绑 task 的 agent 会话（spec §2.3 记录的终局方向，本期不做）
- diff 语法高亮 / 并排视图（引库的事，将来再议）
- 事件流回放、原型 fork 回流 base（后者归 finishing-a-development-branch）
- TicketsPanel 相关（已收敛到全局工单弹层，不在 TUI tab）
