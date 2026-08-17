# 原型站说明

真实前端（`web/`）的形态镜像基准站。页面清单、导航结构、布局骨架与真实控制台对齐，
内容为代表性占位（B93 任务）。fork 副本做交互细化时以本目录为起点。

真实前端只有两条路由（工作台 `/` 与设置 `/settings`），看板与工单是工作台内的弹层，
不单独成页；中央工作台的三种 tab（终端/文件/TUI）中，TUI tab 是任务会话主视图。

| 页面 | 对应功能 | 来源路由 | 确认状态 |
|------|---------|---------|---------|
| index.html | 工作台（三栏 + TUI tab 对话式形态 + 看板/工单弹层） | / | 已确认 |
| pages/settings.html | 设置页（开发机/常规/Env） | /settings | 未确认 |

TUI tab 的对话式重构（方案 A：单滚动会话流 + 事件内联 + 审阅右滑栏 + composer +
ctx/累计用量页头）经 fork 副本 `tui-redesign/` 走查确认，真实前端落地并真机验收后
已于 2026-08-17 回流进 index.html；同批回流的还有确认之后新增的形态：连续工具行
折成「执行了 N 步操作」、工具名中文化、运行中指示、加载更早/回合跳转的边界提示。
fork 副本不入库，已完成使命。

镜像基准：web/src/app/shell/Shell.tsx（三栏）、workbench/TuiTab.tsx 与
task/{TuiHeader,ConversationStream,ToolCard,streamGroups,ReviewSidePanel,Composer,
DebugDrawer}.tsx（中央 TUI）、settings/SettingsPage.tsx。生成日期 2026-08-17。
