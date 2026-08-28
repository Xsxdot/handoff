# B281 执行节点台账

- 2026-08-28：当前分支为 `cards/B281-charter-5`，HEAD 为 `c9cefe28`（父提交包含 `6fb38d67`）；`git status --short --branch` 初始仅输出分支信息，无工作区改动。
- 2026-08-28：按计划运行 `npm test -- --run src/app/workbench/tabs.test.ts src/app/workbench/paneDrop.test.ts`；原始输出为 `sh: 1: vitest: not found`，退出码 127。
- 2026-08-28：按 `web/package-lock.json` 执行 `npm ci --ignore-scripts`；原始输出为 `added 290 packages, and audited 291 packages in 3s`、`found 0 vulnerabilities`，退出码 0；依赖未纳入提交。
- 2026-08-28：恢复依赖后重跑最小基线；原始结果为 `Test Files  2 passed (2)`、`Tests  18 passed (18)`，退出码 0。
- 2026-08-28：先改 `tabs.test.ts` 的跨列拖动断言、补 `ProjectTree.test.tsx` 的已打开项 MIME 断言并补 Shell 拖放回归；触及测试运行结果为 `3 failed | 103 passed (106)`：`placeSource` 仍返回 2 列、已打开行仍为 `draggable="false"`，Shell 断言定位到旧 pane 节点未看到更新文本；该红测发生在生产实现修改之前。
- 2026-08-28：实现空源列移除、已打开项 `DRAG_TAB_MIME`、Shell 任务按项目/机器/路径筛选及 `GroupDivider` 注释修正；重跑 `tabs.test.ts`、`ProjectTree.test.tsx`、`Shell.test.tsx` 原始结果为 `Test Files  3 passed (3)`、`Tests  106 passed (106)`，退出码 0。
- 2026-08-28：为文件抽屉选择器先撤回筛选实现并运行碰撞回归，原始结果为 `1 failed | 33 skipped (34)`，断言实际选中了错误任务的 diff；恢复按 `project_id + machine + work_dir` 的最小实现后重跑同一用例，原始结果为 `Test Files  1 passed (1)`、`Tests  1 passed | 33 skipped (34)`，退出码 0。
- 2026-08-28：执行 `npm run typecheck`；原始输出只有 npm 脚本启动行，无错误，退出码 0。
- 2026-08-28：执行计划列出的 12 个布局/树/文件/壳/账本测试文件；原始结果为 `Test Files  12 passed (12)`、`Tests  194 passed (194)`，退出码 0。
- 2026-08-28：第一次变异探针先跑了行为测试后才补编译检查，顺序不符合变异纪律，读数作废并恢复原文；未据此下结论。
- 2026-08-28：重新将唯一命中的 `columnIndex--` 取反为 `columnIndex++`，`rg` 命中行 378；先执行变异态 `npm run typecheck`，退出码 0 且无错误；再跑唯一相关测试，原始结果为 `1 failed | 13 skipped (14)`，报 `workbench.place.invalid_target`；再跑 3 个触及文件，原始结果为 `1 failed | 106 passed (107)`；随后恢复原实现。
- 2026-08-28：补充源列仍有另一格的 seam 测试后，未改生产代码先跑该测试，原始结果为 `1 failed | 14 skipped (15)`，实际把 B 错放到原源列并覆盖 A；改为让 `removeTabAt` 返回是否移除列、仅在确实移除时递减目标索引后，`-t placeSource` 原始结果为 `Test Files  1 passed (1)`、`Tests  3 passed | 12 skipped (15)`，退出码 0。
- 2026-08-28：最终实现后执行 `npm run typecheck`，原始输出只有 npm 脚本启动行，退出码 0；再执行计划列出的 12 个测试文件，原始结果为 `Test Files  12 passed (12)`、`Tests  195 passed (195)`，退出码 0。
- 2026-08-28：最终边界条件变异的第一发删除 `sourceColumnRemoved &&`，编译失败，原始错误为 `src/app/workbench/tabs.ts(367,7): error TS6133: 'sourceColumnRemoved' is declared but its value is never read.`，按纪律不计数；换为可编译的 `!sourceColumnRemoved` 后，类型检查退出 0，唯一边界行为测试原始结果为 `1 failed | 14 skipped (15)`，3 个触及文件原始结果为 `1 failed | 106 passed (107)`，随后恢复原实现。
- 2026-08-28：恢复最终实现后再次执行 `npm run typecheck`，退出码 0；再次执行计划列出的 12 个测试文件，原始结果为 `Test Files  12 passed (12)`、`Tests  195 passed (195)`，退出码 0；`git diff --check` 退出码 0，`GroupDivider.tsx` 中无 `resizeGroups` 命中。
- 2026-08-28：执行 `git commit -m "fix B281 workbench drag and diff scope"` 成功，原始输出为 `[cards/B281-charter-5 e96d9ec1] fix B281 workbench drag and diff scope`；随后为将提交事实纳入本台账而 amend 同一提交。
