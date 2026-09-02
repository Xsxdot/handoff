# B288 验收台账（2026-08-29）

执行位置：工作树 `~/.handoff/worktrees/manual/B288`（cards/B285-review-2 @ 32489903b）。
真机环境：本机 agentd 127.0.0.1:7777（用户在跑的真实实例）+ 工作树 vite dev（5174），
浏览器经 `/console?ticket=` 兑换登录，真实数据验收。

## 1. 复跑（新鲜证据，协调者本人跑）

- `npm test` → `Test Files 112 passed (112)` / `Tests 1136 passed (1136)`（02:40:58）
- `npm run typecheck` → `tsc -b` 零错误
- `npm run lint` → `✖ 23 problems (5 errors, 18 warnings)`，与基线 f770304b0 完全一致
  （api/pty.ts、NodeEditor.test、terminalHostResponse ×3，均非本卡文件，零新增）
- `npm run build` → `tsc -b && vite build` `✓ built in 2.04s`

## 2. 变异复验（三刀三红，还原回绿）

| 变异 | 打点 | 转红 |
|---|---|---|
| A：tui 忽略任务名解析器（resolved 置 undefined） | `tabs.ts#tabTitle` 缝 | tabs.test 1 红 + TabBar.test 1 红 |
| B：终态判定漏掉 failed（isTerminalState 只认 completed） | `archived.ts#isTerminalState/archivedTasks` 缝 | archived.test 3 红 + ProjectTree.test 1 红 |
| C：终端窗格落点浅蓝遮罩降为暗色 | WorkbenchPage drop 预览（问题 2 主场景） | WorkbenchPage.test 1 红 |

还原后对应文件复跑全绿（27 passed）。

## 3. 真机验收（行为事实，逐条）

1. **任务原名**：点开真实任务 bench-agent-connectors-wave2 →
   - 组标签新组显示任务原名（ evaluate 读 selected tab textContent 原文）；
   - 窗格头「bench-agent-connectors-wave2  agent-connectors-wave2」（截图）；
   - 面包屑第三段 = 「agent-connectors-wave2 / 本机 / bench-agent-connectors-wave2」（截图）；
   - 左栏已打开行与任务行去重为一行、原名、选中态（截图）；
   - 回退路径同样真机验证：合成投掷两个不存在的 taskId（把任务名误当 id 传入）→
     组标签/已打开行显示 `TUI · bench-b1` 式回退名 + TuiTab「任务不存在」诚实报错——
     这两个假 tab 是验收探针，已当场关闭，工作台状态还原（19 组、bash·B199 单格）。
2. **拖放落点**：切到黑底终端组，合成 dragover（DRAG_TASK_MIME）→
   `drop-left` 预览出现，类值与原型逐字一致（半区 + `shadow-[inset_4px_0_0_#2563eb]` +
   终端变体 `bg-[rgba(147,197,253,0.5)]`）；截图：黑底上浅蓝半区遮罩 + 左缘 4px 蓝条，
   对比清晰。dragend 复位已验。
3. **列宽不横滑**：同一黑底组内右缘连投两次生成三列 → 截图三列**等宽压进 1280 窗口**、
   无横向滚动（真机截图为证；earlier 几何读数 488×3 系误抓隐藏组容器，作废）。
4. **终态进已结束**：handoff 项目任务组只剩已打开项（bash·main(16) mac-02 等），
   「已结束」行计数 **523**，子行带机器名；搜索期间自动展开（旁路生效）。
5. **左栏层级**：项目行加粗 + 进行中计数 + 右侧箭头；「任务」「目录」小标题分组；
   机器行绿点 + 右箭头；工作树子行紧凑缩进；与 option-1 结构一致（多张截图比对）。
6. **顶部 chrome**：面包屑 28px 灰字条 + 44px 组标签条（激活药丸面、短节奏分隔线、
   类型图标、每组关闭钮、行尾 + 新建标签组），截图与 option-1 对照成立。

## 4. 环境异常记录（非产品缺陷）

- 验收中途 IAB 标签页出现一次会话搁浅（合成事件后输入与点击无响应、无任何
  console/window 错误），reload 后恢复且此后全程正常；未复现于正常操作路径，
  判定为自动化环境的偶发搁浅，不构成本卡缺陷。
- 验收对本机 agentd 的副作用：开/关了一个真实任务的 TUI 组、两次合成投掷产生的
  两个假 tab（已关）、bash·B199 组内的分列（已还原单列）。落盘的工作台状态已还原。

## 5. 结论

复跑 ✅ 变异 ✅ 真机五条 + 顶部 chrome ✅ 功能保留清单（review 已核）✅。
**验收通过。**

## 6. 图对账（recon，2026-08-29）

- 改动面：f770304b0..HEAD 的 web 源码（基线不含 test 文件，视图只收源码符号）。
- 补建 `codegraph/diffs/cards-B285-review-2.json`：nodesAdded 4
  （taskDisplayName / taskMatchesQuery / OpenItem / OpenedSearchItem）、
  nodesModified 9（tabTitle、archivedKey、archivedTasks、breadcrumbSegments、
  Breadcrumb、BreadcrumbSegments、WorkbenchApi+focusTab、TabBarProps+taskName、
  ProjectTreeProps+openItems/focusedTaskId/onFocusOpenItem/onOpenTerminalAt）。
  修改节点从基线原节点打补丁，schema 与既有视图一致。
- `codegraph validate`：12 个 issues 全部为 `[decl d_*]` 基线既有（有无本视图均为
  12，本视图零新增）；`unscannedEntries: 6` 亦为基线既有。
- 抽查：`sym taskDisplayName` / `sym taskMatchesQuery` 命中（anchor ok），
  `sym tabTitle --view` 显示修改后签名。focusTab 以 WorkbenchApi 字段记录
  （字段不进 sym 索引，按 schema 放 model.fields）。
- **裁决：pass**。只动了 codegraph/diffs/，未触 baseline/target/best。

## Finish（合并与回灌，2026-08-29）

- **合并**：用户拍板本地合并。合并前分支树新鲜全量 1136 绿（npm test，112 文件）。
  main 与分支已分叉（main 侧有 B283 悬浮窗终端 tab 累积修复，分支切出后合入），
  5 个文件两侧都动过：restore.ts / restore.test.ts / useWorkbenchSync.ts /
  TerminalTab.tsx / Shell.tsx。
- **解冲突原则**：以分支重写后的全局 workbench 结构为基，把 B283 三段恢复语义
  重新移植上去——①机器扇出门控 prune（原逐行跳过改为逐 tab 谓词，
  `pruneDeadSessions` 加可选 `keep` 参数，按 `tab.base.machine` 判归属）；
  ②外来悬浮窗 tab 一次性清除（`purged` 统计、activeId 兜底、windowOpen 收窗）；
  ③外来机器 home 会话不收编。Shell.tsx / TerminalTab.tsx 的 main 侧注释勘误
  （会话跨 agentd 重启存活）逐处保留。
- **合并后全量**：npm test 1145 绿（1136 + 9 条 B283 用例移植到全局结构，含新增
  「本机 tab 不受门控」一条）；typecheck 零错；build 过。合并提交 3a4e6e845。
- **图回灌**：absorb `cards-B285-review-2`（+4 ~9，基线 3640 节点）→ 补
  `merge-b283-port` 视图记录合并解冲突增量（machineOkSet 新增 + 4 节点修改，
  absorb 后基线 3641 节点 @3a4e6e845，meta 刷到 HEAD）。validate 终验 12 issues
  / unscannedEntries 6，与分支轮逐项相同（全 `[decl d_*]` 基线既有，零新增）；
  `check` 无违规。
- **图覆盖债（承自 B281 轮，非本卡引入）**：B281 重写恢复管道时改名的函数
  （liveSessionIds→liveIds、collectUsedSessionIds→usedSessionIds、
  countTerminalsWithSession→countSessions、orphanTerminal→orphanContent）在
  baseline 仍留旧名节点；本次 merge-b283-port 已把 buildRestore /
  pruneDeadSessions / RestoreInput / RestoreResult / machineOkSet 五节点刷到
  合并后现状，改名四对的旧节点留待下一轮图扫描统一收编。
