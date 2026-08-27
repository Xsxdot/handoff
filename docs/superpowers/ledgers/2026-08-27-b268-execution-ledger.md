# B268 执行台账

- 2026-08-27：工作树 `/Users/sycm/.handoff/worktrees/b38cb45c`，分支 `cards/B268-charter`，HEAD `f3a62dab`，工作区干净。附件 `docs/superpowers/specs/b268.md` / `docs/superpowers/plans/b268-plan.md` 不在本分支；从 `main:6912a6d0` 只读取出正文，未切分支、未合并。
- 2026-08-27：节点定位 implement。读 using-charter / implement / instrumenting-code / defect-families / product-backlog / handoff。未调用 handoff CLI。
- 2026-08-27：`cd web && npx vitest run ...` 第一次失败：`Cannot find package '@tailwindcss/vite'`。`web/node_modules` 不存在。按 CI 跑 `npm --prefix web ci`，原始输出 `added 287 packages ... found 0 vulnerabilities`。
- 2026-08-27：在仓库根跑 vitest 得到 42 红（`document is not defined` / `Cannot find package '@/components/ui/button'`）。这是工作目录错了，不是基线红。`cd web && npx vitest run src/app/workbench/terminalWheel.test.ts src/app/workbench/terminalInput.test.ts src/app/workbench/TerminalTab.test.tsx src/app/tree/ProjectTree.test.tsx` 原始收口：`Test Files  4 passed (4)` / `Tests  101 passed (101)` / `Duration  1.23s`。基线绿，可以叠行为。
- 2026-08-27：Task 1 Step 2 换 `terminalWheel.test.ts` 后跑 `npx vitest run src/app/workbench/terminalWheel.test.ts`：`10 failed | 2 passed`，失败为 `wheelForcesSelection is not a function` / `altBufferWheelReports is not a function`（符号缺席，非 typo）。
- 2026-08-27：空壳落地后再跑：`9 failed | 3 passed`，失败全是 AssertionError（Mac Option 期望 true 得 false；SGR 期望 SGR_PIXELS；报告期望序列得空串）。断言红成立。
- 2026-08-27：按 plan 实现 `terminalWheel.ts` 后再跑：`1 failed | 11 passed`。失败是横滑：`deltaX=-160, cellWidth=8` 实发 20 格，plan 期望 10 格；随后 `deltaX=16` 实发 2 格，期望 1 格。实测 `first ticks 20 eq20 true` / `second ticks 2 eq2 true`。判断：plan 把纵滑 `cellHeight=16` 的除法套到横滑；公式与纵滑同一条，改测试期望为 repeat(20)/repeat(2)，不改公式。
- 2026-08-27：横滑期望修正后再跑 `terminalWheel.test.ts`：`12 passed`。
- 2026-08-27：删 `altBufferWheelSgr` 后 `TerminalTab.test.tsx` 现有竖直滚轮用例红：`TypeError: altBufferWheelSgr is not a function`。再加构造选项 / 划词放行 / 横滑三条，再跑：`4 failed | 27 passed`。红因分别是缺 `macOptionIsMeta`、仍调旧入口、`deltaY===0` 把横滑放行。
- 2026-08-27：接上新 handler + Terminal 选项后再跑 `terminalWheel.test.ts` + `TerminalTab.test.tsx`：`43 passed`。
- 2026-08-27：Task 2 先扩 `key()` 并加 mac 键三用例，跑 `terminalInput.test.ts`：`2 failed | 14 passed`。⌘← 得 `[]` 而非 `['\x01','\x05']`；⌘K `clear` 被调 0 次。Ctrl+K 负例已绿。非 typo。扩 `attachCustomKeyEventHandler` 后再跑：`16 passed`。
- 2026-08-27：Task 3 加「焦点在 xterm textarea 时 ⌘K 不抢搜索」，跑过滤该名：`AssertionError: expected <input> to be <textarea>`。改 `onKey` 后再跑整文件：`54 passed`。
- 2026-08-27：`cd web && npm run typecheck` 第一次失败：`TerminalTab.tsx(227,35): error TS2559: Type 'Terminal' has no properties in common with type '{ _core?: ... }'`。调用处改为 `mouseEncodingOf(term as Parameters<typeof mouseEncodingOf>[0])`。再跑 `npm run typecheck` 退出码 0，无输出。
- 2026-08-27：⌘K 变异：锚 `logTermFix(label, '⌘K 清屏', '')\n        term.clear()` 命中 count=1，替换为 `term.input('\\x0c')`。`npm run typecheck` 仍退出 0。`vitest -t '⌘K 调用 clear'` 红：`expected "clear" to be called 1 times, but got 0 times`。探针 `MUTATION_ONDATA ["\f"]`，onData 收到 `\x0c`。触及四文件：`1 failed | 112 passed`，失败仅该条。已恢复 `term.clear()`，探针日志已删。
- 2026-08-27：恢复后 `cd web && npm run typecheck` 退出 0；触及四文件 `113 passed`。`go build ./...` 退出 0、无输出。未改 Go，未跑全仓 Go 测试（移位到集成节点）。
- 2026-08-27：收尾自审：划词放行走 `logTermWheelBypass`；滚轮上送走 `logTermWheel`；⌘←/⌘→/⌘K 走 `logTermFix`。无 `console.log`。`altBufferWheelSgr` 仓库内零命中。真机清单未跑，标未验证。
- 2026-08-27：`cd web && npm test`（子系统 vitest 全量）原始收口：`Test Files  109 passed (109)` / `Tests  1136 passed (1136)` / `Duration  13.19s`，退出码 0。
