# Handoff 需求总账

## Backlog

| ID | 标题 | 状态 | 优先级 | Spec | 原型/流程图 | 验收 | 变更痕迹 | 备注 |
|----|------|------|--------|------|------------|------|---------|------|
| B1 | 二期：审批链/executor 选择/dispatch 扩展/可观测性 | 🔨 doing | 高 | [spec](specs/2026-08-08-handoff-approver-dispatch-observability-design.md) | — | — | — | 领于 08-08，plan 见 plans/2026-08-08-handoff-phase2.md，已派发 devbox 真实执行（任务 41db0d4d） |
| B2 | Claude Code adapter（任务级五动作全链路） | 💡 idea | 高 | — | — | — | — | 来源：二期 spec §4.4 范围外单独立项；挂载走 headless + permission MCP/hooks，需 spike |
| B3 | grok adapter（预授权降级模式） | 💡 idea | 低 | — | — | — | — | 来源：二期 spec §4.4；缺程序化审批挂载点，与审批链不契合，优先级低 |
| B4 | 远程 target 派发前代码同步保证 | 💡 idea | 中 | — | — | — | — | 来源：08-08 devbox 真实测试——远程仓库落后 2 提交需手动 push+pull；方向：dispatch 前校验/自动同步远程仓库基准（如比对 commit hash 拒发或代跑 git pull） |
| B5 | 任务停止/取消命令（handoff stop） | 💡 idea | 中 | — | — | — | — | 来源：08-08 真实测试——废弃 running 任务只能 ssh 杀 tmux 会话，缺 CLI 一等入口；应停 executor、置 failed/aborted、可选清理任务分支 |
| B6 | 权限描述截断导致审核者盲批 | 💡 idea | 高 | — | — | — | — | 来源：08-08 真实测试——permTextLimit 把长命令（heredoc 脚本等）截断，审核者只能看到开头就得批准，安全门形同虚设；方向：工单存全文、CLI 提供 `handoff show --ticket` 看完整命令，或超长命令强制附摘要+全文路径 |
| B7 | agentd 侧 PATH 继承与工具链探测 | 💡 idea | 中 | — | — | — | — | 来源：08-08 真实测试——agentd 继承的 PATH 缺 go，executor 满盘找工具链浪费多轮；方向：agentd 启动时以登录 shell 解析 PATH，或任务级配置可注入 PATH |
| B8 | --worktree 归属校验接受仓库子目录 | 💡 idea | 低 | — | — | — | — | 来源：08-08 二期审阅 P2-1——git-common-dir 向上查找使 /repo/internal/sub 被当作 worktree 接受，实际改的是主仓 HEAD 且把审阅面收窄到子目录；方向：加比对 --show-toplevel 与入参（EvalSymlinks 后）相等 |
| B9 | 审批者裁决输出的 nonce 防伪 | 💡 idea | 中 | — | — | — | — | 来源：08-08 二期审阅 P2-4——权限原文由被监管的 executor 产生（不可信）且被插进审批 prompt，若 CLI 回显 prompt 或模型复述，构造的 {"decision":"approve"} 会被采信；方向：prompt 要求回显随机 nonce、解析校验匹配，或只取 stdout + 权限原文走 stdin |
| B11 | attach 无参列表的建议命令丢 --target | 💡 idea | 低 | — | — | — | — | 来源：08-08 二期终验——非 TTY 降级打印 `handoff attach <id>` 不带 --target，对远程任务照抄会打到本机 agentd（先 404 再 attach 本机不存在的会话）；顺带：任务查不到时那条 ERROR 日志噪音也该降级 |
| B10 | workspace git 调用无超时 | 💡 idea | 中 | — | — | — | — | 来源：08-08 二期审阅 P2-6——PrepareWorkspace/RemoveManagedWorktree 全部 context.Background()，worktree add 遇网络文件系统/hook/credential 交互会挂死并拖住 dispatch 的 HTTP handler；方向：PrepareWorkspace(ctx, req) 从 r.Context() 透传 + 分钟级上限 |
