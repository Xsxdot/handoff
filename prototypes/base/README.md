# 原型站说明

真实前端（`web/`）的形态镜像基准站。页面清单、导航结构、布局骨架与真实控制台对齐，
内容为代表性占位（B93 任务）。fork 副本做交互细化时以本目录为起点。

真实前端只有两条路由（工作台 `/` 与设置 `/settings`），看板与工单是工作台内的弹层，
不单独成页；中央工作台的三种 tab（终端/文件/TUI）中，TUI tab 是任务会话主视图。

| 页面 | 对应功能 | 来源路由 | 确认状态 |
|------|---------|---------|---------|
| index.html | 工作台（三栏 + TUI tab 当前形态 + 看板/工单弹层） | / | 未确认 |
| （fork: tui-redesign/index.html） | TUI tab 对话式重构（方案 A：单滚动会话流 + 事件内联 + 审阅右滑栏 + composer + ctx/累计用量页头） | /（TUI tab） | 确认中 |
| pages/settings.html | 设置页（开发机/常规/Env） | /settings | 未确认 |

镜像基准：web/src/app/shell/Shell.tsx（三栏）、workbench/TuiTab.tsx（中央 TUI 现状）、
settings/SettingsPage.tsx。生成日期 2026-08-17。
