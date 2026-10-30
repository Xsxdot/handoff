# 移动 UI 接力实现账本（2026-10-09）

范围：仅移动 Web 树 `/Users/xushixin/.codex/worktrees/mobile-workspace-ui/handoff`；HEAD 基线 `3e52eb462b7e0213db4c4b8988db91dd3083e84c`，基线 tree `9b0407fb957eed373e4847da895f5160f856e4a9`。不含已有 native/settings 与此前 UI 工作。

## 本轮新增/修正

- `MobileWorkspace` 搜索现在同时匹配项目/协作会话、打开资源标题与目录、已打开 PTY 的 shell/path、未打开 PTY 的 shell/path。资源命中会保留并展开所属项目。未打开且不属于任何项目工作树的 live home/scratch/未匹配 PTY 进入「其他工作」并保持可恢复；未知关联不被强行归属。
- 工作台 needs count 在 cards 与 decisions 两条 Shell 数据流都已到达前传 `null`，加载期间隐藏 count，不用 `0` 代替未知。
- 移动项目标题行 CSS 让 disclosure、加号、目录均固定 44×44；项目名称按钮 flex 占余宽、左对齐并截断。
- 文件抽屉目录切换通过 `directoryChoicesFor` 按稳定的 machine+workspace path 找唯一 ProjectNode，再提供其目录；同名项目不混入，无匹配/多重匹配时保留当前目录单选。
- 新增 MobileWorkspace 搜索/资源祖先/PTy 未归属/未知 count 回归，以及 Shell 同名项目目录候选测试。
- `MobileWorkspace` 的项目名称与目录 disclosure 分为独立按钮：名称主操作进入项目详情，箭头只管展开；保持资源项目映射、机器+目录身份、Shell 单点轮询与独立记忆的既有实现。
- SettingsHub 常规入口与 GeneralPage 子页去重内层标题，避免设置页出现重复标题。
- `useMobileNav.normalize`：账本禁用且缺省路径 `/` 时，补 canonical replace 到 `/?tab=projects`；此前项目 tab 默认已从 sessions 改为 projects，这条逻辑仍沿用授权的禁用账本回退规则。
- 对应测试：移动工作台项目主操作、导航 normalize、设置标题；Shell 的项目位置按需展开、移动会话入口新路径及 active workbench group 新建内容助手。

## 沿用且已确认的实现面

接力前已存在三入口 MobileTabBar、URL 导航 hook、MobileWorkspace/项目详情、cards/detail/settings/rooms 的 compact 呈现、加载/断线缓存/过期门控，以及设置 Env 脱敏等代码。它们属于既有工作树基线，不列为本轮增量。桌面 agent 已从本树同步 Shell 与移动依赖；其 ProjectTree/FileTree/WorkbenchPage 桌面专属修改仍由桌面树负责。

## 测试及其他验证

- `npm run typecheck`：本轮 fresh 通过。
- `npm test -- --run src/app/shell/Shell.test.tsx --reporter=dot --silent`：125/125 通过（第 7 轮 fresh）。
- 聚焦 `MobileWorkspace.test.tsx` + `Shell.test.tsx`：本轮 fresh 135/135；`MobileWorkspace` 的新增用例经历红测后 9/9 通过。
- 预览：本轮 CUA 截图在 390×844 查看的是身份失效降级页，Console 记录 API 401；root 接手 IAB 视口与 authenticated 浏览器 QA，移动侧不再触碰共享窗口。详见 [移动 UI QA](../../design/mobile-workspace/implementation-qa/mobile-ui-qa-2026-10-09.md)。
- `npm run build`：本轮 fresh 通过（2001 modules；Vite 对 1.2MB JS chunk 给体积 warning）。
- `npm test -- --reporter=dot --silent`：144 files，141 passed/3 failed；1788 tests，1756 passed/32 failed。失败均在共享桌面 `FileTree.test.tsx`、`ProjectTree.test.tsx` 和 `Shell.test.tsx`，移动侧不修改桌面所有权测试；未将全量记为通过。

## 未决缺口

- authenticated 390/360 交互截图与真实数据 Console 需 root 浏览器复核；当前新鲜截图只证明 390×844 的 expired 降级布局。
- 11 组 45 个状态未完成逐屏实际实现对照；测试不能代替这一视觉证据。
- 全量测试 32 项桌面归属失败待桌面组合树修复并复测；本树 typecheck/build 与 owned focused tests 通过。
- 不触发真实审批、会话消息或持久化创建；这些实际生产副作用不属于本轮验证。
