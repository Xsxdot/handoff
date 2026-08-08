# Handoff 需求总账

> 三期（B4–B18）公共验收证据：`go build ./...` + `go vet ./...` + `gofmt -l .`（无输出）+
> `go test ./...` 全绿 + `go test -race ./internal/agentd/ ./internal/executor/opencode/ ./internal/localsync/ ./cmd/` 全绿，
> **远程执行机与本地各独立跑一次**（08-09）。以下各行「验收」列只记该条**专属的真机验证**。
> 全部条目均无原型/流程图，自动免除对照。

## Backlog

| ID | 标题 | 状态 | 优先级 | Spec | 原型/流程图 | 验收 | 变更痕迹 | 备注 |
|----|------|------|--------|------|------------|------|---------|------|
| B1 | 二期：审批链/executor 选择/dispatch 扩展/可观测性 | ✅ done(已验) | 高 | [spec](specs/2026-08-08-handoff-approver-dispatch-observability-design.md) | — | go build + gofmt + go vet + go test ./... 全绿（合并结果上重跑）、go test -race ./internal/agentd/ ./internal/executor/opencode/ 绿；attach 本机/远程 execve 路径真机实测通过；真实旧库迁移实测无损；无原型/流程图，自动免除对照 08-08 | 08-09 三期压测修正：当时「attach 远程路径实测通过」只验了 execve 路径解析、未验成功的远程连接，实际远程 attach 从未可用（见 B17）——验收标准定松了 | 08-08 完成并合入 main（c89932a，25 提交）。由 handoff 自身派发 devbox/opencode 执行，三轮审核：外部审阅发现 2 P0 + 5 P1 + 4 P2 全部修复 |
| B2 | Claude Code adapter（任务级五动作全链路） | 📋 specced | 高 | [spec](specs/2026-08-08-handoff-claude-code-adapter-design.md) | — | — | 08-08 spike 实测定案：`--permission-prompt-tool` + 内置 stdio MCP server 挂权限门；stream-json 双向流跨回合存活；可视化对齐现状（tmux 两窗口，不自研 TUI）；继承 user/project settings 保 skills、任务级 deny/ask 收口 | 来源：二期 spec §4.4 范围外单独立项 |
| B3 | grok adapter（预授权降级模式） | 💡 idea | 低 | — | — | — | — | 来源：二期 spec §4.4；缺程序化审批挂载点，与审批链不契合，优先级低 |
| B4 | 远程 target 派发前代码同步保证 | ✅ done(已验) | 中 | [spec §3](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 真机两条路径都验：①本地有 2 个未推提交时派发 → 400 拒发，错误含 sha `32abad5e…` + 仓库路径 + `请先 git push` 提示；②远程落后时派发 → 日志「基线提交缺失，补拉远端」→ `git fetch --all --prune`（4.5s）→「补拉远端后基线提交已就位」→ 放行 08-09 | 08-08 定策略：自动 fetch（基线缺失才 fetch 再复查，仍缺失即拒发） | 来源：08-08 devbox 真实测试——远程仓库落后 2 提交需手动 push+pull |
| B5 | 任务停止/取消命令（handoff stop） | ✅ done(已验) | 中 | [spec §4](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 真机验证：running 任务 stop → 状态落 failed、tmux 会话 `handoff-6cfa341e` 消失、该任务 serve 进程退出（其他任务的 serve 不受影响）；重复 stop → 409 且日志为 WARN 非 ERROR 08-09 | 08-08 定终态：复用 failed + 事件写明原因，不新增 aborted 状态；08-09 追加：Stop 需自行清理 managed worktree（见 B15），CLI 提示文案随之改为按响应体 `worktree_removed` 打印 | 来源：08-08 真实测试——废弃 running 任务只能 ssh 杀 tmux 会话，缺 CLI 一等入口 |
| B6 | 权限描述截断导致审核者盲批 | ✅ done(已验) | 高 | [spec §5](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 单测覆盖三条契约：工单 request 存全文、事件 payload 截至 200 字并带截断标记、黑名单命中长命令**尾部**的 `rm -rf`（正是旧实现漏掉的那条）08-09 | 08-08 定方案：工单存全文、事件仍截断；连带把黑名单/审批者改为对全文匹配。刻意接受误升级变多（错误方向是「多叫醒审核者一次」，反方向是「漏放一条 rm -rf」） | 来源：08-08 真实测试——permTextLimit 截断长命令，安全门形同虚设 |
| B7 | agentd 侧 PATH 继承与工具链探测 | ✅ done(已验) | 中 | [spec §6](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 真机验证：故意用最小 PATH（`/usr/bin:/bin:/usr/sbin:/sbin`）启动 agentd，日志「已合并登录 shell 的 PATH」且 added 列表含 `/usr/local/go/bin` 08-09 | **08-08 方案被实测推翻并修正**：初稿 `$SHELL -l -c` 在 devbox 上一无所获——该机 PATH 追加写在 `.zshrc`（交互式才加载），而 `-l` 只 source `.zprofile`/`.zlogin`，修复会在它要解决的那台机器上恰好无效。改为 `-l -i -c` + 只取 stdout、不看退出码、stderr 丢弃 | 来源：08-08 真实测试——agentd 继承的 PATH 缺 go |
| B8 | --worktree 归属校验接受仓库子目录 | ✅ done(已验) | 低 | [spec §7](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 真实 git 仓库集成测试：仓库子目录被按 ErrBadWorkspaceReq 拒绝、真 worktree 仍被接受且 Managed=false 08-09 | — | 来源：08-08 二期审阅 P2-1——git-common-dir 向上查找使 /repo/internal/sub 被当作 worktree 接受 |
| B9 | 审批者裁决输出的 nonce 防伪 | ✅ done(已验) | 中 | [spec §8](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 表驱动单测：回显正确 nonce → approve 生效；nonce 错误 → 判无效；缺 nonce → 判无效（两者均 fail-closed 升级）08-09 | — | 来源：08-08 二期审阅 P2-4——权限原文由被监管的 executor 产生（不可信）且被插进审批 prompt |
| B10 | workspace git 调用无超时 | ✅ done(已验) | 中 | [spec §9](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 单测：已取消 ctx 下 PrepareWorkspace 与 RemoveManagedWorktree 均立即失败；真机日志可见 `timeout=2m0s` 字段 08-09 | — | 来源：08-08 二期审阅 P2-6——全部 context.Background()，worktree add 挂死会拖住 dispatch 的 HTTP handler |
| B11 | attach 无参列表的建议命令丢 --target | ✅ done(已验) | 低 | [spec §10](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 真机验证：非 TTY 输出为 `handoff attach <id> --target devbox`（本机任务不带）；4xx 日志确认降为 WARN（dispatch 400、stop 409 实测均为 WARN）08-09 | — | 来源：08-08 二期终验 |
| B12 | 任务完成后本地自动同步远程任务分支 | ✅ done(已验) | 中 | [spec §11](specs/2026-08-08-handoff-backlog-cleanup-design.md) | — | 真机端到端：远程任务完成 → wait 自动同步 → 本地 `已同步分支 handoff/46e84025（新增 2 个提交）`，且该输出走 stderr、stdout 仍只有单行事件 JSON；同步失败时（B17 未修前）wait 退出码仍为 0，符合「同步失败不阻断唤醒」设计 08-09 | — | 来源：08-08 三期 brainstorm 用户追加——与 B4 构成闭环 |
| B13 | isTTY 把 /dev/null 当成终端 | ✅ done(已验) | 中 | — | — | 真机验证：`handoff attach --target devbox < /dev/null` 正确走非 TTY 降级并打印建议命令（旧实现会打表格再报「读取选择」错误）08-09 | 08-09 三期压测中定位并直接修复，未单独出 spec；改用 github.com/mattn/go-isatty（原为间接依赖，零新增模块） | 来源：08-08 三期压测实测 |
| B14 | 探活档位切换的日志级别不对称 | ✅ done(已验) | 低 | — | — | 真机验证：round2 二进制启动（00:31:55）之后日志再无一条 INFO 级「探活降频」，此前约每 4 秒一条 08-09 | 08-09 三期压测中定位并直接修复，未单独出 spec；两档统一为 Debug | 来源：08-08 三期压测实测 |
| B15 | 落 failed 的任务永久泄漏 managed worktree | ✅ done(已验) | 高 | — | — | 真机验证两条泄漏路径均已堵住：①令 tmux 不在 agentd 的 PATH 中使 adapter.Start 失败 → 日志「managed worktree 已删除」、worktrees/ 下零残留；②`--new-worktree` 任务 stop → worktree 已删而分支 `handoff/c6418a7b` 保留 08-09 | 08-09 三期压测中定位并直接修复，未单独出 spec。实现改为 defer 统一补偿，并由执行者补上 `executorStarted` 守卫：executor 接管工作区后不再补偿删除（否则会把运行中的任务脚下抽空）——该权衡不在审核指令内，是执行者自行判断 | 来源：08-09 三期压测实测（实证残留 worktrees/0995f42e 与 /6cfa341e，已手工清理） |
| B16 | dispatch 500 掩盖 executor 启动失败真因 | ✅ done(已验) | 中 | — | — | 真机验证：tmux 缺失时 dispatch 报 `启动 executor 失败: tmux 启动 handoff-e0075fc4: exec: "tmux": executable file not found in $PATH`（此前为扁平「派发任务失败」）08-09 | 08-09 三期压测中定位并直接修复，未单独出 spec；状态码保留 500（agentd 侧环境问题），关键是回显可行动真因 | 来源：08-09 三期压测实测 |
| B17 | target 无法配置 ssh 用户名，远程 attach/pull 实际不可用 | ✅ done(已验) | 高 | — | — | 真机端到端：配置 `user: sycm` 后 `handoff pull` 换算出 `sycm@100.73.238.21:/Users/sycm/workspace/handoff` 并成功拉回分支（修复前报 Connection closed）08-09 | 08-09 三期压测中定位并直接修复，未单独出 spec；抽 `sshHostFromTarget` 为 attach/pull 唯一换算点，config.Target 加可选 `user`，README targets 示例同步补齐 | 来源：08-09 三期压测实测；连带修正 B1 的验收结论 |
| B18 | agentd 重启后 waiting_review 任务无法续接 | ✅ done(已验) | 高 | — | — | 真机端到端（本缺陷的完整闭环）：重启 agentd → 启动恢复日志「执行器存活，重建订阅继续消费 task=0126bf0a alive=true state=waiting_review, recovered=1」→ `handoff continue` 返回 `{"ok":true}` 并真的产出了一个提交（修复前为永久 500）08-09 | 08-09 三期压测中**由审核者亲身撞上**并直接修复，未单独出 spec：换二进制重启 agentd 后 round2 审阅指令发不进去，只能重新派发。修法①启动恢复纳入 waiting_review 探活重建但**不改状态**（executor 不在时也不追加 failed 事件，避免噪音）；②ErrTaskNotRunning 映射为带重派提示的 409 | 来源：08-09 三期压测实测 |

## 待验证的空白

- **分级审批链第 1 层（廉价模型审批者）至今零生产验证**：三期三轮任务共上百次工具调用，权限请求产生 **0 次**——二期第 0 层静态规则已放行编码任务的全部常规操作，审批者根本没轮到上场。它只在 webfetch / external_directory / 危险 bash 上触发，而那类请求本就该惊动人。是否真有存在价值，需专门构造会触发的场景才能回答。
