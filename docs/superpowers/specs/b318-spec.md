# B318 工作项页空白处滚动会带动整页

状态：**已落地**（`origin/main` @ `4921cb296`，2026-09-03 已推）
定级 **L1**
卡：B318
日期：2026-09-03
基线：`origin/main` @ `91ffc4a9d`（修复提交以其为 rebase 起点）

## 问题陈述

用户 2026-09-03 截屏：工作项页（`/cards`）在空白处滚轮，整页（含左栏项目树、顶栏）一起滚下去，底下露出大块白。以前没有房间会话时不这样。

根因：B275 把 `RoomPanel` 挂成 `/cards` 右侧 360px 常驻栏，中央容器从纵向 flex 改成横向。Flex 子项默认 `min-height: auto`，高度下限等于内容固有高度。会话列表一长，右侧栏就把整列撑破根节点的 `h-dvh`。根节点没有 `overflow: hidden`，窗口自己出滚动条。看板列被一起拉高后列内反而没什么可滚，空白处滚轮冒泡到窗口。

左栏同一条纪律早已写在 `Shell`：`min-h-0` 才能让内部 `overflow-y-auto` 生效。常驻房间栏漏了这一环。

分流：一两行小修，不升格。

## 方案

中央容器与常驻 `aside` 补 `min-h-0`，会话列表在栏内滚，不再撑页。不改房间数据流、不改浮动面板几何。

弃选：给根节点加 `overflow: hidden`（会把别的溢出一起掐死，掩盖高度链断点）。

## 用户故事

1. `/cards` 挂着常驻房间栏时，窗口不因会话列表变长而出现整页滚动。
2. 左栏底部入口仍钉在窗口底；房间栏会话列表在栏内滚动。
3. 非 `/cards` 页的浮动房间面板行为不变。

## 测试决定

缝：`Shell` 在 `/cards` 下房间栏父级 class；`RoomPanel` 常驻栏 class。jsdom 不测像素，钉 `min-h-0`。

## 实现决定（即 plan）

1. `web/src/app/shell/Shell.tsx`：`/cards` 横向中央容器加 `min-h-0`。
2. `web/src/app/rooms/RoomPanel.tsx`：常驻 `aside` 加 `min-h-0`。
3. `Shell.test.tsx` / `RoomPanel.test.tsx` 各钉一条 class 回归。

## Out of Scope

- 永不做：用根节点 `overflow: hidden` 掩盖高度链断裂。
- 本期不做：房间栏宽度/收起交互改版；其它整页路由的高度链普查。
