# Handoff 需求总账

## Backlog

| ID | 标题 | 状态 | 优先级 | Spec | 原型/流程图 | 验收 | 变更痕迹 | 备注 |
|----|------|------|--------|------|------------|------|---------|------|
| B1 | 二期：审批链/executor 选择/dispatch 扩展/可观测性 | 🔨 doing | 高 | [spec](specs/2026-08-08-handoff-approver-dispatch-observability-design.md) | — | — | — | 领于 08-08，plan 见 plans/2026-08-08-handoff-phase2.md，已派发 devbox 真实执行（任务 41db0d4d） |
| B2 | Claude Code adapter（任务级五动作全链路） | 💡 idea | 高 | — | — | — | — | 来源：二期 spec §4.4 范围外单独立项；挂载走 headless + permission MCP/hooks，需 spike |
| B3 | grok adapter（预授权降级模式） | 💡 idea | 低 | — | — | — | — | 来源：二期 spec §4.4；缺程序化审批挂载点，与审批链不契合，优先级低 |
| B4 | 远程 target 派发前代码同步保证 | 💡 idea | 中 | — | — | — | — | 来源：08-08 devbox 真实测试——远程仓库落后 2 提交需手动 push+pull；方向：dispatch 前校验/自动同步远程仓库基准（如比对 commit hash 拒发或代跑 git pull） |
| B5 | 任务停止/取消命令（handoff stop） | 💡 idea | 中 | — | — | — | — | 来源：08-08 真实测试——废弃 running 任务只能 ssh 杀 tmux 会话，缺 CLI 一等入口；应停 executor、置 failed/aborted、可选清理任务分支 |
