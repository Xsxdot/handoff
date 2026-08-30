# B302 台账

- 2026-08-30 用户：「建一张新卡自主推进」「终端 tab 启动各 agent TUI 后 Shift+回车不换行而是发送」。派发节点 linux-01 / codex。
- 建卡 B302，project=handoff，优先级高，move spec。
- 主仓在 cards/B294-breakdown，从 origin/main 开 worktree `~/.handoff/worktrees/b302-spec` 分支 `cards/B302-spec`。
- 根因读数：xterm Keyboard.ts case 13（约 L100-104）`result.key = ev.altKey ? ESC+CR : CR`，shiftKey 未参与。terminalInput.ts 文件头：Enter 留给 xterm。
- B268 已独占 customKeyEventHandler；本卡只在该槽加一条。B268 OOS 的 Kitty 协议 ≠ 单键 CSI u。
- 定级 L2：单子系统、不动契约；plan 非零；真机 TUI 一眼可核但机制+真机两扇门。
- 方案冻结：alt-screen 才拦截，序列 `ESC[13;2u`。用户授权自主推进，协调者批准。
