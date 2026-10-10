# B431 — 桌面：管理工作树目录组关不掉 + 工作树菜单机器行可拖分屏（L1 · spec+plan 合体）

**定级：L1**（plan 增量为零；验收一眼可核；单子系统 web UI，不动契约）

## 问题

1. 项目标题行右侧终端 ▾ 菜单里点「管理工作树…」后，侧栏「目录」组只进不出，关不掉。
2. 同菜单里的机器行（本机 / linux-01 / …）无法拖到右缘形成分屏；用户期望与侧栏已展开机器/工作树行一致。

## 根因

1. `ProjectTree` 的 `locationMenus` 对「管理工作树…」只有 `Set.add(key)`，没有 toggle / 对称收起。
2. 终端机器 `ContextMenu` 机器行只有 `onSelect`，未进 `paneDrop` 拖停靠链；侧栏同款行已挂 `DRAG_DIR_MIME` + `DRAG_BASE_MIME`。菜单层本身不是 HomeDock 浮窗——本卡只给**机器行**补 MIME，不拖整张菜单、不拖「管理工作树…」文案项。

## 修法

1. 「管理工作树…」：`locationMenus` 有 key 则删、无则加（并保持展开 disclosure 与现行为一致）；验收开得开、关得掉。
2. `ContextMenuItem` 增可选 `draggable` / `onDragStart`；机器行复用侧栏同款 MIME 载荷（主工作区 base）；`paneDrop` 消费端不动。
3. `dragstart` **不要**立刻关菜单（会卸掉 drag source）；`dragend` 再关。

## 触及文件（预期）

- `web/src/app/shared/ContextMenu.tsx`（+ 既有单测）
- `web/src/app/tree/ProjectTree.tsx`（toggle + 机器行 drag）
- `web/src/app/shell/Shell.test.tsx`（或等价树测：toggle 关；菜单机器行 MIME）

## Out of scope

- B430（子行文案 / 终端可读标题）
- 拖整张菜单 / 拖「管理工作树…」文案项
- 新拖放系统或改 `paneDrop` 消费语义
- 用户自命名 / 最近命令

## 验收

1. 「管理工作树…」再点同项（或等价 toggle）能关目录组；只进不出不算过。
2. 菜单机器行拖到右缘可停成右栏，行为对齐侧栏机器/工作树行；相关测绿。
3. 本节点以纪律块为准。

## 三行 plan

1. ContextMenuItem 可选拖；「管理工作树」改 toggle；机器行挂既有 MIME，dragend 再关菜单。
2. 单测：目录组 toggle 关；菜单机器行 `DRAG_DIR_MIME`+`DRAG_BASE_MIME` 载荷对齐侧栏。
3. 相关 web 测绿；不碰 B430 / 契约。
