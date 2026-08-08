# Handoff 需求总账

## Backlog

| ID | 标题 | 状态 | 优先级 | Spec | 原型/流程图 | 验收 | 变更痕迹 | 备注 |
|----|------|------|--------|------|------------|------|---------|------|
| B1 | 二期：审批链/executor 选择/dispatch 扩展/可观测性 | ✅ done(已验) | 高 | [spec](specs/2026-08-08-handoff-approver-dispatch-observability-design.md) | — | go build + gofmt + go vet + go test ./... 全绿（合并结果上重跑）、go test -race ./internal/agentd/ ./internal/executor/opencode/ 绿；attach 本机/远程 execve 路径真机实测通过；真实旧库迁移实测无损；无原型/流程图，自动免除对照 08-08 | — | 08-08 完成并合入 main（c89932a，25 提交）。由 handoff 自身派发 devbox/opencode 执行，三轮审核：外部审阅发现 2 P0 + 5 P1 + 4 P2 全部修复 |
| B2 | Claude Code adapter（任务级五动作全链路） | 📋 specced | 高 | [spec](specs/2026-08-08-handoff-claude-code-adapter-design.md) | — | — | 08-08 spike 实测定案：`--permission-prompt-tool` + 内置 stdio MCP server 挂权限门；stream-json 双向流跨回合存活；可视化对齐现状（tmux 两窗口，不自研 TUI）；继承 user/project settings 保 skills、任务级 deny/ask 收口 | 来源：二期 spec §4.4 范围外单独立项 |
| B3 | grok adapter（预授权降级模式） | 💡 idea | 低 | — | — | — | — | 来源：二期 spec §4.4；缺程序化审批挂载点，与审批链不契合，优先级低 |
| B4 | 远程 target 派发前代码同步保证 | 📋 specced | 中 | [spec §3](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | 08-08 定策略：自动 fetch（基线缺失才 fetch 再复查，仍缺失即拒发） | 来源：08-08 devbox 真实测试——远程仓库落后 2 提交需手动 push+pull |
| B5 | 任务停止/取消命令（handoff stop） | 📋 specced | 中 | [spec §4](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | 08-08 定终态：复用 failed + 事件写明原因，不新增 aborted 状态 | 来源：08-08 真实测试——废弃 running 任务只能 ssh 杀 tmux 会话，缺 CLI 一等入口 |
| B6 | 权限描述截断导致审核者盲批 | 📋 specced | 高 | [spec §5](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | 08-08 定方案：工单存全文、事件仍截断；连带把黑名单/审批者改为对全文匹配 | 来源：08-08 真实测试——permTextLimit 截断长命令，安全门形同虚设；黑名单扫截断版会漏掉命令尾部的 rm -rf |
| B7 | agentd 侧 PATH 继承与工具链探测 | 📋 specced | 中 | [spec §6](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | 08-08 定方案：启动时用登录 shell 解析 PATH 并合并，不新增配置项 | 来源：08-08 真实测试——agentd 继承的 PATH 缺 go，executor 满盘找工具链浪费多轮 |
| B8 | --worktree 归属校验接受仓库子目录 | 📋 specced | 低 | [spec §7](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | — | 来源：08-08 二期审阅 P2-1——git-common-dir 向上查找使 /repo/internal/sub 被当作 worktree 接受，实际改的是主仓 HEAD 且把审阅面收窄到子目录 |
| B9 | 审批者裁决输出的 nonce 防伪 | 📋 specced | 中 | [spec §8](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | — | 来源：08-08 二期审阅 P2-4——权限原文由被监管的 executor 产生（不可信）且被插进审批 prompt，构造的 {"decision":"approve"} 会被采信 |
| B10 | workspace git 调用无超时 | 📋 specced | 中 | [spec §9](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | — | 来源：08-08 二期审阅 P2-6——PrepareWorkspace/RemoveManagedWorktree 全部 context.Background()，worktree add 遇网络文件系统/hook/credential 交互会挂死并拖住 dispatch 的 HTTP handler |
| B11 | attach 无参列表的建议命令丢 --target | 📋 specced | 低 | [spec §10](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | — | 来源：08-08 二期终验——非 TTY 降级打印 `handoff attach <id>` 不带 --target，对远程任务照抄会打到本机 agentd；顺带 client.httpError 对 404 也打 ERROR，噪音该降级 |
| B13 | isTTY 把 /dev/null 当成终端 | 💡 idea | 中 | — | — | — | — | 来源：08-08 三期压测实测——isTTY 只判字符设备，而 /dev/null 正是字符设备；脚本按标准做法 `< /dev/null` 跑 `handoff attach` 时会走进交互分支，打完表格再报「读取选择」错误，非 TTY 降级路径在最该生效的场景失效。修法：改用 github.com/mattn/go-isatty（已在依赖图中，零新增模块），与 B11 同在 cmd/attach.go 可一并做 |
| B12 | 任务完成后本地自动同步远程任务分支 | 📋 specced | 中 | [spec §11](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | — | — | 来源：08-08 三期 brainstorm 用户追加——与 B4 构成闭环（B4 保证开工前远程不落后，B12 保证收工后本地不落后）；走 ssh 直连 fetch 任务分支，不动本地 main |
