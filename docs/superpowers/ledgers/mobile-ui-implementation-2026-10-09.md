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

## GPT-6-Luna 接力复核增量（2026-10-10）

- 先查阅批准故事 plan、dispatch baseline、mobile-complete README/visual-qa/gallery，并用 view_image 复核 `01-progress.png` 与 `05-execution.png`；目标仍是三入口生产 UI，不以图稿数据造假。
- M2 compact 列表：标题、显式「需要你处理 N」和固定 44px 新建图标同排；事件流健康另行呈现，不压缩主动作。CardItem 主行可打开详情，needs 原文限制两行预览，完整请求不删除；迁移放入折叠操作。
- M2 compact 详情：头部展示卡号、真实状态与完整标题；当前动作、挂卡需要/裁决、关联任务优先；验收、事件历史、附件、关系等收于默认折叠证据区；标题/优先级/迁移和协调者操作放入低频折叠区域。裁决选项仍只填草稿，compact 答复为多行文本并需显式提交；项目裁决同样多行、明确提交。
- 本轮文件：`web/src/app/cards/{CardsPage.tsx,CardsPage.test.tsx,CardItem.tsx,CardDrawer.tsx,CardDrawer.test.tsx}` 与 `web/src/app/shell/mobile-ui.css`。CSS 是继承未跟踪文件，仅保留移动 compact 样式。
- 新鲜聚焦：`npm test -- --run src/app/cards/CardsPage.test.tsx src/app/cards/CardDrawer.test.tsx --reporter=dot --silent`，2 files / 91 tests passed。
- 稳定最终代码下 `npm run typecheck` fresh exit 0；`npm run build` fresh exit 0（2001 modules）。Rollup 提示 JS chunk 约 1.22 MB，构建成功。
- 全量测试 fresh：144 files，142 passed / 2 failed；1796 tests，1766 passed / 30 failed。仅失败文件 `src/app/files/FileTree.test.tsx` 与 `src/app/tree/ProjectTree.test.tsx`，均为桌面所有权默认目录展开/样式类预期；未在移动树修桌面源/测试。报告给 root 由组合候选复验。
- 浏览器 viewport 由 root 独占，本轮没有新鲜视觉截图或 Console 核验；不得将上述自动化结果视为视觉验收。

## GPT-6-Luna 2026-10-10 compact finish increment

- M4 automation compact now has local `载体 / 小队` tabs. The active category alone renders; each collapsed row has an explicit accessible name, left-aligned name and one-line machine/CLI or squad/member metadata, a right-side real carrier status/version and chevron. Carrier model remains in expanded details; CRUD actions remain behind expanded details and nested actions disclosure. No API or persisted state changed.
- Env machine chooser already had the dedicated `mobile-env-machine-bar` hook; compact CSS now explicitly forces one horizontal, non-wrapping scroll row and prevents chips shrinking, addressing the fresh 390 screenshot where macbook-pro wrapped.
- Restore status remains part of the mounted WorkbenchPage and restoring lock. When its workbench underlay is covered by compact navigation, only that underlay status is visually hidden; settings and cards stay navigable and the workbench remains mounted/inert under the cover.
- Regressions: SchedulingPage now asserts tabs, explicit accessible names, category isolation, and hidden model until expansion; Shell restoring test asserts the status does not appear over mobile navigation. Fresh focused scheduling + shell: 2 files / 145 tests passed. Fresh Env/settings/scheduling/cards focused: 6 files / 147 tests passed.
- Fresh `npm run typecheck` passed. Fresh `npm run build` passed (2001 modules; Vite reports the existing ~1.22 MB JS chunk size warning).
- Fresh full `npm test -- --run --reporter=dot --silent`: 144 files; 141 passed / 3 failed; 1799 tests; 1768 passed / 31 failed. Failures are one desktop FileTree class assertion, one stale Shell new-session selector, and 29 desktop ProjectTree structure/location expansion assumptions. This is not full-suite pass evidence; desktop owner/root has the detailed combined-tree failures.
- Root owns the live browser and will capture fresh 390/360 automation, Env, card and restore states after synchronizing these files. This worker did not operate the browser; no new visual pass is claimed.

## 2026-10-10 final unknown-state and card hierarchy increment

- `QueuePanel` now receives `hasSnapshot` from `CardsPage`'s existing `queuePoll.data`. Compact count is `…` before the first response, `— · 未确认` when unavailable without a snapshot, `N · 未确认` when showing a stale snapshot, and `0` only for a confirmed empty queue. Desktop count rendering remains its existing numeric count. `QueuePanel.test.tsx` covers loading, expired/disconnected unknown, stale, and confirmed-empty cases; `CardsPage.test.tsx` proves the consumer transitions from unknown to loaded empty.
- Compact CardDrawer places its return control first in the header. Needs text spans the full content column, with the existing clear action on its own row. Terminal execution states are grouped under a closed `历史执行 N` disclosure using the shared task live-state classifier; unlinked rows stay visible as unknown. Summary prefers the real task name, otherwise uses the first eight ID characters with full ID in the title and expanded details. Status is non-wrapping and metadata occupies a secondary line. Desktop paths remain unchanged.
- Compact needs action now has a dedicated page scope and a higher-specificity width rule that overrides the generic 44px mobile button rule; it retains at least 112px for the label/count, and the flag icon cannot shrink.
- A compact sessions deep link now shows `正在读取会话功能…` while the existing ledger capability probe is pending, then renders the real enabled/disabled result. Added Shell test uses a one-shot pending probe to avoid affecting other cases.
- Fresh `npm test -- --run src/app/cards/QueuePanel.test.tsx src/app/cards/CardsPage.test.tsx src/app/cards/CardDrawer.test.tsx src/app/shell/Shell.test.tsx --reporter=dot --silent`: 4 files / 227 tests passed. Fresh `npm run typecheck` passed. Fresh `npm run build` passed (2001 modules; existing Vite ~1.22MB chunk warning). After replacing a direct status import with the shared task classifier, the focused terminal-history test passed and typecheck was rerun successfully.
- Root will perform the combined full suite and live 360 screenshot check; this worker did not use the browser and makes no visual-pass claim.
