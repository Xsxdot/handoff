# B320 工作项页滚动仍带动整页（B318 只修了高度）

状态：**已落地（源码）**
定级 **L1**
卡：B320
日期：2026-09-04
发现自：B318 验收后真机仍复现

## 问题陈述

B318 给常驻房间栏补了 `min-h-0`。用户说没有任何变化。无头 Chrome 对 v0.4.0 控制台 `/cards` 实测：

- `document.scrollWidth=22486`，`clientWidth=1440`（高度已经等于视口 813）
- `main` 宽 21670px；常驻栏在 `x=22126`，视口里看不见
- 空白处横滑 `deltaX=2400` 后 `scrollLeft=2400`，根节点 `x=-2400`，左栏一起走

根因：工作台常驻在 `main` 里不卸载。flex 子项默认 `min-width:auto`，按终端/画布固有宽度撑开 `main`。B318 只处理了交叉轴（高度），主轴（宽度）仍漏。

## 方案

`main` 加 `min-w-0 overflow-hidden`；壳 `h-dvh` 与常驻 `aside` 加 `overflow-hidden`。不改工作台常驻策略（B270）。

## 实现决定（即 plan）

1. `Shell.tsx`：根、中央列、`main`、工作台包装切断溢出。
2. `RoomPanel.tsx`：常驻 aside `overflow-hidden`（浮动面板本来就有）。
3. `Shell.test.tsx` / `RoomPanel.test.tsx` 钉类名。

## 验收

无头 Chrome：`scrollWidth===clientWidth`，常驻栏 `x+width` 落在视口内，横滑后 `scrollLeft===0` 且左栏 `x` 不变。源码已验；生产要重编 agentd/桌面端，v0.4.0 二进制仍是旧前端。
