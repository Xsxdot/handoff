# B442 控制台恢复新建会话入口（bug 流，spec 即 plan）

状态：用户裁决「恢复新建会话入口」（2026-10-19）。

## 偏离与自主决定

- 入口位置自定：桌面放左栏底部「归档会话」上方（与它同为会话类全局入口）；移动端放工作台「⋯」菜单「添加项目」之后。未改 B429 收起的左栏 tab 结构（那是已批准的 IA）。
- 两支旧 Shell 用例（B358.9 建会话）改为点可见那颗按钮：它们原本点的是 `hidden` 面板里的按钮——jsdom 不算 Tailwind 的 `hidden`，用例在真机不存在的世界里绿着。

## 现象

桌面端与移动端都找不到新建会话的地方。

## 根因

B429（70da3632）把桌面左栏「会话 | 任务」tablist 设为 `hidden`、会话并进项目树；「＋ 新建会话」只留在 SessionSidebar 头部。之后进入 SessionSidebar 的唯一入口是「归档会话」（`openSessionList(true)`），而 `archivedOnly` 时新建按钮禁用 → 桌面无入口。移动端只有「⋯ → 全部协作会话 → ＋ 新建会话」三跳。

## 修法

Shell 收一个 `openCreateSession`（会话列表两处 `onCreate` 一并改用它，审查 I3）；`ProjectTree` 与 `MobileWorkspace` 各加可选 `onCreateSession`，账本开启时由 Shell 传入，渲染「新建会话」入口，打开既有 `NewSessionDialog`。

## 验收

- 红→绿：`Shell.test.tsx`「B442 新建会话入口可见」两支（只认祖先链无 `.hidden` 的按钮）；改前两支均 `expected 0 to be greater than 0`。
- 全量同 B441。
- 真机：生产控制台桌面宽度左栏底部可见「新建会话」，点开建会话对话框；手机工作台「⋯」菜单可见「新建会话」。
