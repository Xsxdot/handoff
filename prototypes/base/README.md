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

## 覆盖边界（2026-08-18 核对，B106）

base 是**快照，不是全量镜像**。今天它覆盖的是工作台与 TUI；下列区域尚未纳入。
对照时不要把「base 里没有」读成「真实前端没有」——那正是 desktop-console 退役前
反复咬人的那种错。

| 区域 | 真实前端现状 | base |
|------|------------|------|
| 左栏项目树搜索（含 ⌘K 聚焦、`filterTree`） | 有：`web/src/app/tree/{ProjectTree.tsx,search.ts}` | ❌ 未覆盖 |
| 看板卡片干预态（琥珀边框 + 左侧竖条） | 有：`web/src/app/board/{BoardPage.tsx,columns.ts}` | ❌ 未覆盖（base 里看板只有入口按钮 `openBoard`） |
| 左栏显示偏好菜单、新建工作树弹层、机器行 hover 入口 | 有（2026-08-18 新增） | ❌ 未覆盖 |
| 桌面壳面包屑、悬浮窗、看板入口下移到底部图标区 | 有（2026-08-18 新增） | ❌ 未覆盖 |

全量刷新**刻意推迟**到前端这一波改动收敛之后：08-18 当天前端就有十余个提交在改左栏与
桌面壳，此时刷 base 等于对着移动靶画像，当天就旧。已记为 backlog **B131**。

## 同级目录的状态

- `desktop-console/` —— **已退役**（2026-08-18，B106），历史资产，**不是形态权威**。
  与真实前端已知三处不符，详见该目录 `AGENTS.md` 顶部的退役说明。
- `w4b-timeline/` —— 功能 fork 副本，非基准。

注：`prototypes/.gitignore` 已声明「只有 `base/` 入库，其余子目录是 fork 副本不入库」。
上面两个目录是该声明之前就跟踪进来的遗留文件，本次**只做状态标注、不取消跟踪**——
`desktop-console/AGENTS.md` 里有「Confirmed product and visual decisions」的决策记录，
untrack 会把它埋进历史，考古成本反而更高。
