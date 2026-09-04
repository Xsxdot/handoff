# B272 spec 台账

- 2026-09-04：开卡。`handoff card show B272` 待办；`handoff card move B272 spec`。标题「把本机的文件或者图片拖到远程机器的终端的时候，把文件传过去」。无附件、无 note。
- 2026-09-04：现状读数：桌面壳原生拖放只把本机绝对路径 `term.input` 进终端（`web/src/app/lib/desktopFileDrop.ts`、`TerminalTab.tsx` registerFileDropTarget）；WKWebView 能拿到 HTML5 `File` 内容但没有路径。`PUT /api/workspaces/file` 只改已存在文本、拒二进制、1 MiB。PTY `cwd` 只记开会话时的值，不跟踪后续 `cd`。本机→远端转发体上限 `forwardBodyLimit = 1 MiB`（`internal/agentd/forward.go`）。B300 已裁定剪贴板不并入本卡。
- 2026-09-04：用户裁定落点不走 shell cwd（防污染工作区），用远端 `$HOME/.handoff/drop/`。
- 2026-09-04：用户裁定同名不覆盖，改为 `photo.png` → `photo-2.png`，终端插入实际写成的路径。
- 2026-09-04：用户裁定单文件上限 32 MiB；更大拒绝并提示、不插路径。
- 2026-09-04：对话中冻结（未经逐条点名、用户未反对）：只收 basename；目录拖入本期不做；多文件逐个传；失败不插路径、可见错误；不自动清理收件箱；远程传文件走 HTML5 `File`（桌面壳+浏览器），本机终端仍走原生路径插入；桌面壳不读盘。
- 2026-09-04：用户裁定跨机判定为「文件所在电脑 ≠ PTY 所在电脑」，含浏览器直连远端 agentd；本机桌面壳+本机终端仍只插真实路径。
- 2026-09-04：对话冻结推论：32 MiB + 失败可见 → 进行中也要可见；本机浏览器+本机终端不在本卡补路径；多文件同机串行落盘防抢名；其它转发路径保持 1 MiB。
- 2026-09-04：spec 落盘 `docs/superpowers/specs/b272.md`，定级 L3 轻档，状态待用户批准。OOS 后续项写入 `docs/roadmap.md`。
- 2026-09-04：独立审查（子 agent，只读）。裁决：零上下文缺项、接缝合法性缺项、架构法第五条违、不能批准。Critical：① 32 MiB 内容 vs 体/编码未定（对照 `FileWriteReq` JSON string、`forwardTo` `LimitReader` 截断）；② 跨机 1 与 2 可同时真，先走 2 会把文件写在 UI 机。Important：不覆盖无原子性、中断孤儿、缝 1 导出门面层、双响测试未锁、缝 3 调用方写成 WorkbenchPage（实为 `Shell.tsx:728/850`）、目录拖入失败面。Minor：TUI 未点名、备注「壳始终 loopback」过满（壳用 `cfg.Listen`）。现状读数主段属实。
- 2026-09-04：协调者核验后回写 spec（Critical 1–2 + Important 3–8 + Minor 9–10）。状态仍待用户批准。
- 2026-09-04：用户批准并授权自主推进。回写 spec 头部。`card bind` 因本会话无席位身份失败（普通终端）。`card move B272 contract`。工作区有无关原型删除，远程 dispatch 会拒，本会话落地。
- 2026-09-04：分支 `cards/B272-charter`。`dropdir.Put` 测试全绿；`TestDrop*` HTTP/转发全绿；vitest TerminalTab+contract 109 passed。
- 2026-09-04：提交 `git commit` → `[cards/B272-charter a2e8dd972] feat(drop): 拖本机文件到远程终端写入 ~/.handoff/drop（B272）`，`20 files changed, 1283 insertions(+), 2 deletions(-)`。本条随一次 amend 收进口（amend 换 hash，不回填追认）。

