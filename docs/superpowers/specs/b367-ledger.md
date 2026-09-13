# B367 台账

- 2026-09-13 建卡 B367。用户复现：点设置、顶栏换组换 tab 再回来，TUI 花屏划不动。
- 审过 `fix/web-tab-unmount-regression`（`d86cf8db5`）：只补 group `min-w-0`，验证脚本只走设置往返，不覆盖换组。
- 现状读数：`WorkbenchPage.tsx` `renderGroup` 后台组 `pointer-events-none absolute -left-[10000px]`。HomeWindow 已禁止这两样。B270 spec 写「不把画布标成看不见」；B280 spec 写不用 `pointer-events-none`。
- 分流：L1。跟 HomeWindow 对齐 keep-alive CSS，不改 PTY / TerminalTab 建连。
- 红：`npx vitest run src/app/workbench/WorkbenchPage.test.tsx` 后台组断言红，`className` 含 `pointer-events-none absolute -left-[10000px]`。
- 绿：同文件 21 passed；`Shell.test.tsx` 52 passed。`npm run typecheck` 通过。
- 变异（改语义、命中唯一、先编译）：后台组加回 `pointer-events-none` → 对应用例红；组 class 去掉 `min-w-0` → 对应用例红。均已还原。
- 真机 WKWebView：本会话未验，需用户在桌面端走顶栏换组 + 进设置再回来。
- 2026-09-13 用户：`bash · main (2)` / sq 本机仍划不动。现场：组 1 是 linux-01 | sq 分屏，组 2 终端 keep-alive 叠在同一块 z-0。z-0 WebGL 抢走滚轮。补：后台组 `pointer-events-none`+`inert`；滚轮跟指针走、不跟键盘焦点走。WorkbenchPage 21 + TerminalTab 59 绿。
