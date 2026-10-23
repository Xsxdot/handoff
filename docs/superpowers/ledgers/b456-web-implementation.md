# B456 前端实现台账

## 偏离与自主决定

按已批准的 `docs/superpowers/plans/b456-plan.md` 与 spec r4 实现前端 consumer，没有改变冻结的状态词表、服务边界或数据所有权。将组件与样式拆到 `SessionRoleStatusBar.tsx` 和 `session-role-status.css`，是计划授权范围内的文件组织决定。待处理事项只以 `ticket_refs` 作候选，并逐 task 读取现有 `fetchTaskDetail` 后按 task/ticket 精确匹配；没有添加 API 或第二份待办状态机。系统实际观测到执行人身份无法处理当前会话权限的额外分支仍由后端状态和当前读取权限决定，前端不自造 assignee。

## 实现范围

- `SessionDetail.role_status` 增加 TypeScript 只读契约；会话 composer 上方加入紧凑角色状态条、逐级压缩/折叠和全角色查看入口。
- 详情弹层按结论、原因、可执行操作、最近五条动态、折叠技术细节排列；键盘 Esc/Tab、焦点恢复均可用。
- 票据候选通过既有 task detail API 核验，复用 `TicketsPanel` 和 `replyTicket`。只对当前成功核验的 pending 票据计数；提交成功转到下一张，提交失败保留当前项；刷新失败保留只读旧内容和草稿，CTA 计数清零，恢复读取后沿同 ticket key 重核。
- 接续只通过会话消息 `@card` 发送请求，并明确提示“已发送”不代表已接手。归档、会话状态源读取失败时禁用写操作；状态读取失败不继续显示绿色状态。
- `SessionRoleStatusBar.test.tsx` 使用后端生成的 `src/api/testdata/SessionDetailB456.json` 验证真实 Go JSON 契约，包括 recent、ticket refs 与可选 since 缺省。

## 验证

使用 Node 24：`PATH=/Users/xushixin/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin:$PATH`。

- `npm test -- --run src/app/rooms/SessionRoleStatusBar.test.tsx src/app/rooms/SessionTab.test.tsx`：2 个测试文件、22 项测试通过。
- `npm run typecheck`：`tsc -b` 通过。
- `npm run build`：Vite production build 通过；有现有 bundle 超过 500 kB 的提示。

未进行浏览器/视觉验收；由主会话按既定要求核对。构建产物及浏览器交互验收不据此宣称通过。

## 审查修正（F1–F3）

- 将 role status 样式中的 `hsl(var(--…))` 改为直接消费 `index.css` 的完整 `oklch()` 变量，并恢复 r4 弹层的 520px 上限。
- 状态图标为“需要你”加入对话气泡 SVG；名称改为“协调者 · 卡号”和“执行者 · 短任务号”，头像角标仅显示末两位。未观察到协调者时显示明确提示。
- 移除 chips 横向滚动，改为可换行并用实际 chips 宽度逐级压缩；所有异常 chip 不折叠。狭窄布局也执行压缩；无可操作待办时保留“需要你” chip。折叠集合中的异常和待处理角色使用各自颜色标记。
- 待处理状态改为逐 task 核验：局部 GET 失败只禁用失败 task 的旧快照，不影响其他已核验 task 的 CTA/答复；轮询发起时保留最近核验成功集，明确失败后才从可操作集合剔除。草稿由现有 TicketsPanel 按 task/ticket key 保留。
- 详情/工单分别使用独立 dialog ref；轮询替换详情数据不重置焦点，Esc 在 document 捕获阶段调用对应关闭逻辑并恢复焦点，避免继续触发会话抽屉快捷键。
- 反例覆盖新增：独立 task 读取失败时其他 task 的工单仍可用；新一轮轮询期间已核验 CTA 不闪退；打开详情时轮询不抢焦点，Esc 关闭当前弹层并阻断下层监听。

修正后验证：`SessionRoleStatusBar.test.tsx`、`SessionTab.test.tsx` 与 `TicketsPanel.test.tsx` 共 3 个文件、33 项测试通过；`npm run typecheck` 与 `npm run build` 通过（仍有 bundle 体积提示）；`git diff --check` 通过。仍待主会话真实浏览器视觉验收。

## 提示文案兼容

`TicketsPanel` 的布尔 `disabled` 调用继续显示既有“已断开，暂不能作答”提示；逐票禁用回调显示“部分事项暂不能作答”。同步修正文档注释，说明全局工单弹层与会话角色状态入口共用 `TicketsPanel`，事件行仍不可操作。验证：`npm test -- --run src/app/task/TicketsPanel.test.tsx`，1 个文件、9 项测试通过。

## 暗色状态色

从 r4 原型补入 `html.dark .role-status` 的状态色和弹层阴影覆盖，不覆写全局主题变量。验证：Node 24 `npm run build` 与 `git diff --check` 通过；保留 bundle 体积提示。

## 同状态排序

同状态角色按有效、非未来的 `since` 从早到晚排列；缺失、非法、零值或未来时间排在已知时间之后，并按角色 ID 稳定排序。实际 DOM 顺序反例测试通过：`npm test -- --run src/app/rooms/SessionRoleStatusBar.test.tsx`，1 个文件、6 项测试通过。

## 最近动态色彩

动态中的失败项固定使用异常文字色与异常图标色，待处理项固定使用待处理文字色与图标色，不再随当前详情角色的状态色变化；普通项维持灰色。Node 24 `npm run build` 通过，保留既有 bundle 体积提示。

## “需要你处理”悬停说明

该按钮的 `title` 从当前可操作、已核验工单关联角色去重生成，只使用可见角色名，不包含 raw identity。Node 24 `npm run build` 与 `git diff --check` 通过。

## 状态读取失败文案

无成功投影时显示首次读取失败和“尚无成功观测”，不再同时声称服务不支持或保留了成功读数；已有投影后读取失败时将角色降为未知并说明保留最后成功观测。错误原因允许窄屏换行。验证：相关 2 个测试文件 28 项通过，Node 24 `npm run typecheck` 与 `git diff --check` 通过。

## 窄屏状态条行数

≤650px 下普通 lead 保持单行，CTA 与摘要并排并允许摘要截断；异常/来源错误提示可折行，状态条内的观测时间隐藏（详情仍显示完整时间），不修改全局触点最小高度。Node 24 `npm run typecheck`、`npm run build` 与 `git diff --check` 通过。

## F10 弹层层叠修正

角色详情与待处理工单抽屉现通过 React portal 挂到 `document.body`，脱离会话 pane 的局部层叠上下文；仍由各自 `.role-status` wrapper 提供状态色变量与暗色覆盖，沿用既有 dialog ref、焦点约束、Esc 关闭及关闭后焦点归还逻辑。行为测试断言两种弹层均挂在 body 根层。

验证：`SessionRoleStatusBar.test.tsx` 与 `SessionTab.test.tsx` 共 2 个文件、28 项测试通过；Node 24 `npm run typecheck`、`npm run build`、`git diff --check` 通过。Build 保留已有 bundle 大于 500 kB 提示。浏览器复验由主会话完成。

## F11 嵌套弹层焦点归还

角色详情与工单抽屉分别保存其触发元素；从“全部角色”菜单打开详情时，详情关闭后焦点归还到菜单入口。弹层切换仅在新顶层打开时自动聚焦，不在工单关闭时将焦点重置到详情关闭按钮；关闭工单恢复到详情内的入口，随后关闭详情恢复到状态条原始入口。新增连续两次 Esc 的行为反例。

验证：`SessionRoleStatusBar.test.tsx` 与 `SessionTab.test.tsx` 共 2 个文件、29 项测试通过；Node 24 `npm run typecheck`、`npm run build` 与 `git diff --check` 通过。Build 保留既有 bundle 大于 500 kB 提示。
