# B300 执行台账

- 2026-08-29：用户报告「本机开远程终端，grok/opencode 等 TUI 选中文字自动复制但到不了本机」，要求评估修法与是否并入 B272。
- 2026-08-29：`handoff card show B272` —— B272「把本机的文件或者图片拖到远程机器的终端的时候，把文件传过去」，charter 流待办列，无 spec。用户裁定剪贴板问题单独建卡、不与 B272 关联。
- 2026-08-29：`handoff card add` 建卡 B300（charter 待办，优先级中），`card note` 记初查结论；后 `card move B300 spec` 进 spec 列。
- 2026-08-29：grep `web/src/app/workbench/` 无 clipboard/copy 实现（负事实）；`TerminalTab.tsx:36` 引用 `desktopFileDrop` 仅为「拖文件插入路径」桌面壳通道，与剪贴板无关。
- 2026-08-29：grep `internal/ptyhost/wire/` 无 osc/escape/filter 逻辑（负事实）——输出字节流透传，OSC 52 可达浏览器。
- 2026-08-29：`web/package.json` 读数：`@xterm/xterm ^5.5.0`、`@xterm/addon-fit ^0.10.0`、`@xterm/addon-webgl ^0.18.0`——5.x 有 `parser.registerOscHandler`。
- 2026-08-29：`codegraph sym TerminalTab` 命中 `d_web` 域（`n_web_app_workbench_TerminalTab`，anchor `web/src/app/workbench/TerminalTab.tsx`），图覆盖无债。
- 2026-08-29：读 `web/src/api/pty.ts:102-135` —— `attached` 控制帧含 `since` / `truncated` / `backlog_bytes`（缺键表示旧服务端）；据此确认「积压重放门」可纯前端实现，无需动 wire 契约。
- 2026-08-29：读 `web/src/app/workbench/terminalHostResponse.ts` 全文——该文件管反方向（xterm 设备回包不上送），其注释佐证「切 tab / 重连会把环形缓冲整段重放进新的 xterm，历史转义序列会再次触发解析器行为」的机制，是重放门必要性的依据。
- 2026-08-29：读 `web/src/app/workbench/TuiTab.tsx` 头部——TuiTab 是任务对话帧流渲染（`useFramesStream`），非 xterm 终端，不涉剪贴板，划出范围。
- 2026-08-29：spec 定稿落 `docs/superpowers/specs/b300.md`，定级 L2，待用户批准。
- 2026-08-29：spec 头部回写批准判决（已批准，L2）后，执行 `git add docs/superpowers/specs/b300.md docs/superpowers/specs/b300-ledger.md && git commit -m "docs(spec): B300 终端 TUI 复制落到本机剪贴板（OSC 52）"`，输出 `2 files changed, 88 insertions(+)`；本条台账与 spec 同批 amend 收口（amend 换 hash，不回填追认）。
- 2026-08-29：plan 节点基线复核：`cd web && npm test` 实际 1181 passed（约 32s）；`npx vitest run src/app/workbench/TerminalTab.test.tsx` 实际 47 passed；`npm run lint` 实际存量 23 problems（5 errors, 18 warnings）——lint 判据取「不新增」。
- 2026-08-29：库事实查证：`web/node_modules/@xterm/xterm/typings/xterm.d.ts:1822` 确认 `registerOscHandler(ident, callback): IDisposable`；TerminalTab 挂载 effect 已有 `hostReply: 'drop-all'|'replay'|'live'` 重放状态机（`TerminalTab.tsx:252`、`:420-435`），OSC 门直接复用，不新造计数。
- 2026-08-29：plan 落盘 `docs/superpowers/plans/b300-plan.md`（三 task：解码纯函数 → 注册+三道门+失败提示 → 全量门禁；真机清单六条标注协调者执行）。执行位置变更声明：用户 2026-08-29 指示「直接全做完，需要派发的用子 agent」——后续 implement/review 以本地 subagent 执行，卡生命周期照走。
