# W4 home 基准终端浮窗 走查记录

实现计划：`docs/superpowers/plans/2026-08-14-w4-home-floating-terminal.md`（Task 1–6）。
Spec：`docs/superpowers/specs/2026-08-14-w4-home-floating-terminal-design.md`。
分支：`feat/b90-home-floating-terminal`，基于 `c61b0c5e`（计划 + spec 提交）。

> 执行者无浏览器、无真 PTY。凡是要肉眼在页面上看、或要真实终端会话才能验证的
> 判据（第 5、6、9 条）如实标「未验（无浏览器）」，不猜通过。其余判据由对应
> 自动化用例背书（见下第 1 节）。

## 1. spec §5 十条验收判据逐条结论

| # | 判据 | 结论 | 证据/背书 |
|---|---|---|---|
| 1 | 圆钮常驻右下角；有存活会话时带数字角标，无则不带 | 已验 | HomeDock.test.tsx「无会话时圆钮不带角标」「有会话时角标显示数量」；useHomeDock 状态由 useHomeDock.test.ts 7 用例背书 |
| 2 | 点圆钮出小面板，列出已开终端，每项带存活点，底部有「新终端 ⌘T」 | 已验 | HomeDock.test.tsx「点圆钮出面板，列出已开终端与「新终端」」；存活点为 HomeDock.tsx 中每项右侧 `<span aria-hidden>` 绿色圆点（结构断言） |
| 3 | 点列表任一项 → 浮窗打开并激活该终端；小面板收起 | 已验 | HomeDock.test.tsx「点清单某项 → activate 并收起面板」+ useHomeDock.test.ts「activate 会把收起的浮窗重新打开」 |
| 4 | 浮窗内 tab 条可切换、可新建、每 tab 可单独关闭 | 已验 | HomeWindow.test.tsx「只渲染激活 tab 的内容」「tab 上的 × 走 onKill，且不误触发激活」；「新终端」按钮在 HomeDock/HomeWindow（onNew）；切换由激活按钮调 onActivate 背书 |
| 5 | 拖标题栏浮窗移动；拉右下角改尺寸；尺寸变化后终端内容按新尺寸重排（stty size 与窗口一致） | **未验（无浏览器、需真 PTY）** | 拖动/拉伸几何由 HomeWindow.test.tsx「拖标题栏改位置」「拉右下角改尺寸」背书；尺寸→PTY 行列由 TerminalTab 既有 ResizeObserver（TerminalTab.tsx:143）+ term.onResize（:134）背书，实现未在拉伸回调额外 resize（见第 2 节 spec 更正 1）；「stty size 与窗口一致」需真终端，未验 |
| 6 | 点「收起」→ 浮窗消失、角标仍在；再点圆钮→列表还在→点进去输出连续、会话未断 | **未验（无浏览器、需真 PTY）** | 前半（收起不杀、角标仍在）由 useHomeDock.test.ts「收起浮窗不动 tabs」+ HomeDock.test.tsx「浮窗收起后角标仍在」背书；「输出连续、会话未断」需真终端，未验 |
| 7 | 点某个 tab 的 × → 该会话结束；关掉最后一个 → 浮窗消失、角标归零 | 已验 | 最后关掉自动收起：useHomeDock.test.ts「关掉最后一个：浮窗自动收起，activeId 归 null」；× 走「确认 → deletePtySession → 关 tab、失败留痕」由 Shell.tsx killPtySession 共用函数 + 代码审查背书（Task 4 审查确认，失败不关 tab、错误经 ConfirmDialog 呈现） |
| 8 | 中央工作区的 tab 条上从不出现 home 终端；切换左栏目录不影响浮窗 | 已验 | Shell.test.tsx「home 终端不进中央 tab 条」；「切换左栏目录不影响浮窗」由架构背书——useHomeDock 与 useWorkbench 完全独立（useHomeDock.ts 文件头职责注释） |
| 9 | 对端不支持 PTY 时圆钮不渲染（不是渲染一个点不动的按钮） | **未验（无浏览器、手上没有不支持 PTY 的对端）** | Shell.test.tsx「对端不支持 PTY 时不渲染圆钮」（mock pty_supported=false）背书逻辑；真机未验 |
| 10 | 前端四条全绿 | 已验 | 见下第 3 节，四条命令实际输出 |

## 2. 计划期发现的两处 spec 更正（给下一个读 spec 的人）

写计划（`c61b0c5e`）时核实代码后，spec 有两处旧估计要作废，spec §3.2 已在计划提交里一并更正。留档如下：

1. **spec §3.2「浮窗拉伸结束后要重新 resize 该 PTY」是错的。**
   `TerminalTab` 已有 `ResizeObserver`（TerminalTab.tsx:143）→ `fit.fit()`，且 :134 有
   `term.onResize` 上报服务端。只要 `TerminalTab` 渲染在浮窗内部，拉伸浮窗就会自动重算行列
   并上报，实现**没有**在拉伸回调里额外调 resize（额外调会和 ResizeObserver 打架，产生可见
   抖动）。以既有事实为准，spec 该句作废。

2. **spec §2.3「收起不杀」在实现上是免费的。**
   `TerminalTab` 的 cleanup 只断连接、不发 DELETE（TerminalTab.tsx:149），服务端会话继续跑。
   所以「收起浮窗 = 卸载 = 会话继续活着」不需要任何会话侧专门处理。杀会话只在 × 显式走
   `deletePtySession`（Shell.tsx 的 killPtySession，中央 tab 与浮窗共用）。

## 3. 回归原文 + 空 diff 证据

在 `web/` 下执行（本次实跑）：

```
$ npx vitest run
 Test Files  42 passed (42)
      Tests  391 passed (391)

$ npm run typecheck
> tsc -b
（无错误输出）

$ npm run lint
✖ 10 problems (0 errors, 10 warnings)
（10 条 warning 均为既有文件：BoardPage.tsx / codeText.tsx / ProjectTree.tsx / BlankTab.tsx /
 WorkbenchPage.tsx / badge.tsx / button.tsx 上的 react-refresh 与 hooks 告警；
 本次改动文件零告警）

$ npm run build
✓ built
（仅有既有 chunk 体积 warning：单 chunk 超 500 kB）
```

空 diff 证据（逐条贴命令与结果）：

```bash
$ git diff --stat -- '*.go' internal/ cmd/            # 无输出（Go 零改动）
$ git grep -n FloatingNewPane -- web/                 # 无输出（exit 1，web 源码零引用）
# 注：docs/superpowers/ 下的历史计划/规格文档里仍有 FloatingNewPane 字样，
# 那是已提交的历史文档（本计划自身就写了要退役它），不在源码改动范围
$ git diff -- web/src/app/workbench/WorkbenchPage.tsx | grep renderContent   # 无输出（renderContent 签名未变）
```

另：`web/src/app/workbench/usePtyRestore.ts` 零改动（`git diff` 空），其既有
`console.warn`（:79，恢复失败告警）不在本计划范围、未顺手改。本批未新增任何 `console.*`。

终审后按 Minor 记账修了三处（均为低风险一致性/健壮性，不影响十条验收结论）：grab 补
`pointercancel` 解绑、收起按钮 `onPointerDown` stopPropagation 防误拖、HomeDock 存活点绿
统一为 `#18a86b`。修复后四条命令仍全绿。

## 4. 改动面（相对分支基线 `c61b0c5e`，10 个提交：5 功能 + 5 ledger）

提交列表（`git log --oneline c61b0c5e..HEAD`，从旧到新）：

```
5e44b231 docs(ledger): B90 Task 1 完成记录
200c5c4a feat(web): home 终端状态 useHomeDock，与工作区 tab 完全独立
8338520f docs(ledger): B90 Task 2 完成记录
4fb1a248 feat(web): home 终端浮窗容器，可拖可拉伸，收起不杀会话
25d91713 docs(ledger): B90 Task 3 完成记录
988b843b feat(web): home 悬浮入口面板，圆钮角标反映存活会话数
dd846f71 docs(ledger): B90 Task 4 完成记录
88057397 feat(web): home 终端接入浮窗，退役 FloatingNewPane
9545459c docs(ledger): B90 Task 5 完成记录
f2c877ed fix(web): home 会话恢复到浮窗而非中央工作区
```

改动文件（`git diff --name-only c61b0c5e..HEAD`）：

```
docs/ledger-b90.md
web/src/app/homedock/HomeDock.test.tsx   （新增）
web/src/app/homedock/HomeDock.tsx        （新增）
web/src/app/homedock/HomeWindow.test.tsx （新增）
web/src/app/homedock/HomeWindow.tsx      （新增）
web/src/app/homedock/useHomeDock.test.ts （新增）
web/src/app/homedock/useHomeDock.ts      （新增）
web/src/app/shell/Shell.test.tsx
web/src/app/shell/Shell.tsx
web/src/app/workbench/FloatingNewPane.test.tsx  （删除）
web/src/app/workbench/FloatingNewPane.tsx       （删除）
```

`git diff --stat c61b0c5e..HEAD -- web/`：`10 files changed, 771 insertions(+), 117 deletions(-)`。

## 5. 走查后修正：小面板配色偏离

走查发现初版把入口小面板做成了深色（`bg-[#0b131e]` 一系），与用户确认过的原型基准
`prototypes/desktop-console/src/styles.css` 的 `.home-dock-panel`（白底 `#ffffff`、`1px solid var(--border)` 边框）不符。

已修正：小面板整棵子树 token 化（`bg-popover` / `text-popover-foreground` /
`text-muted-foreground` / `border-border` / `bg-accent` / `bg-state-active` / `bg-background`），
浮窗本体（HomeWindow，深色 `var(--terminal)` 系）与圆钮 FAB（`#10151b`）维持原型原样。

教训：走查记录漏记这处有意偏离本身就是问题——「以后有意偏离必须写进走查记录，不写就等于没发生」。本次把它补记进来。

修正后四条命令仍全绿（本次重跑，真实数字见下）：

```
$ npx vitest run
 Test Files  42 passed (42)
      Tests  391 passed (391)

$ npm run typecheck
> tsc -b
（无错误输出）

$ npm run lint
✖ 10 problems (0 errors, 10 warnings)
（10 条 warning 均为既有文件：BoardPage.tsx / codeText.tsx / ProjectTree.tsx /
 BlankTab.tsx / WorkbenchPage.tsx / badge.tsx / button.tsx 上的 react-refresh 与
 hooks 告警；本次改动文件零告警）

$ npm run build
✓ built in 1.06s
（仅有既有 chunk 体积 warning：单 chunk 792.50 kB 超 500 kB）
```
