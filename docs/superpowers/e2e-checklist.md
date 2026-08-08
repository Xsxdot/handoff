# E2E 手动验证清单（真实 opencode）
前置：executor 机装 opencode 并配好模型凭证；`handoff agentd --executor=opencode` 已起。
- [x] SPIKE-1（spec 风险#1）：手动 `opencode serve` + curl 建会话发 prompt，抓 /event SSE 原始样本：
      确认 permission 事件类型名/字段、回合结束（idle）事件类型名 —— 对照调整 adapter 映射
      **结论（样本已入库：`internal/executor/opencode/testdata/spike3-events.jsonl`、
      `spike5-events.jsonl`，opencode 1.18.15 serve；由 `replay_spike_test.go` 原样重放，
      协议一变即变红）**：事件类型已对齐并
      已调整 adapter 映射（fix-A）——权限=permission.asked（properties.id 即 PermissionID，
      permission/patterns/metadata 拼描述；permission.replied 应答回显必须忽略）；回合结束
      主信号=session.status 的 status.type=idle（同现 session.idle 冗余，须去重防重复触发）；
      文本载体=message.part.updated（part.type=text 全量快照）+ message.part.delta
      （field=text 增量），message.updated 仅带 properties.info.role，只用于探测新回合开始。
      遗留验证项（SPIKE-1b 待做）：更长回合/多工具调用的 part 流（当前样本仅单工具单文本
      part）、断线重连后的事件重放语义。
- [x] SPIKE-1b（spec 风险#1 续，/event 重放语义）：**重连时是否会收到断连前的历史事件？**
      实测：订阅 /event 收到事件后断开，重连后观察首条事件（fix-J spike，opencode
      1.18.15 serve）——**结论：不重放**。重连只收到 server.connected / heartbeat，
      断连前已产出的 permission.asked 等历史事件不会补发；事件无 Last-Event-ID/序号
      可续拉。因此「水位线应急方案」不启用（无历史可跳过），但断连间隙本身会丢事件
      （见下方「断连间隙权限丢失」项）。
- [x] SPIKE-2（spec 风险#2）：验证 OPENCODE_CONFIG 环境变量注入配置生效（permission.bash=ask 真的会问）；
      不生效则切 fallback：写 repo 内 opencode.json + gitignore
      **本机 e2e 2026-08-08（opencode 1.18.15）通过**：任务目录 `opencode.json` 含 bash/edit=ask；
      真链路产出 `permission_request`：`bash: git status…`、`edit: hello.txt`、`bash: git commit…`。
- [x] dispatch → wait 被 permission_request 唤醒 → approve → 执行继续 → completed → diff 有内容
      **本机 e2e 2026-08-08 通过**：task `4de1e051-…`；3 次 permission 均 approve；
      completed commit `a872d85`；`hello.txt` 内容正确；diff 有新增文件。
- [~] deny 链路：reply --deny 后 executor 收到 reject 并调整做法
      **半通过（2026-08-08）**：task `a96ef1b4-…` 对首个 bash 门 `--deny`，agentd 日志
      `decision=reject` 且 `relayed=true`；随后 opencode 发 `idle` 且回合无文本
      （`idle 但回合无文本，跳过分类`），模型长时间无新权限/提问/完成——**reject 送达 OK，
      「调整做法」依赖模型行为，本轮未观察到**。
- [x] 提问链路：executor 输出 {"ask":...} → wait 唤醒 → reply --answer → 下一回合收到原文
      **远程 e2e 2026-08-08 通过**（task `778f36cf-…`，`--target devbox`，free model）：
      plan 强制先 `{"ask":"…A/B/C/D 自定义"}`；wait 收到 `type=question`；
      `reply --answer "自定义：你好 from handoff ask-e2e"` 后模型写 `greeting.txt` 为
      `你好 from handoff ask-e2e`（采纳自定义文案），commit `96c5d92`，completed → done。
- [ ] 审批挂起过夜：permission 不答搁置 8h 后 approve —— opencode 侧等待不超时、流程继续（替代原 hook 长阻塞 spike）
- [x] continue：回发修改指令 → 同一 session 续接 → 二轮 completed diff 含新改动
      **本机 e2e 2026-08-08 通过**：continue 改 hello 文案 → round2 commit `544ec77`；
      diff 显示 `(round2)`；同一 `executor_session` 续接。
- [~] 断网演练：wait 期间关 Wi-Fi 3min 恢复 → 自动重连（退避复位回 1s，见 P1-10a），
      重连后事件流恢复；**断连期间产生的事件不会积压补发**（SPIKE-1b 结论：无重放），
      若期间有权限请求产生，按下方「断连间隙权限丢失」项核对 Warn 日志
      **本机 e2e 2026-08-08 半通过**（task `2d84519e-…`，用 kill agentd ~20s 模拟断线）：
      wait 进程存活；日志见指数退避 1→2→4→8→16s 后 `WS 连接建立`（重连成功）。
      但若在 **permission 尚未落库** 时杀 agentd，恢复后任务可卡在 running 无事件
      （见下条 SPIKE-1b）——完整「断线后事件流无感恢复」未单独用 pf 挡端口验证。
- [x] 断连间隙权限丢失（P1-10b 降级方案，SPIKE-1b 结论的必然推论）：权限 pending 期间
      掐断 SSE（如 agentd 重启 / 网络抖动），重连后断言日志出现
      `SSE 断连已恢复：断连间隙的权限请求可能丢失`（Warn，带任务上下文）。
      该间隙内服务端产出的 permission.asked 无法自动补拉（fix-J spike 结论：
      /event 无重放；GET /session/{id}/message 的 tool part 只有 callID/state，
      无权限 id per_xxx；POST /session/{id}/permissions/{id} 要求真实 id，
      伪造即 404 PermissionNotFoundError）——opencode 会一直等这个看不见的决策
      直到看门狗判死。人工兜底：`tmux attach -t handoff-<id8>` 观察/介入，
      或重启任务。
      **本机 e2e 2026-08-08 实测确认风险形态**（task `2d84519e-…`）：dispatch 后 ~2s 杀
      agentd，仅 progress 落库；tmux+serve 仍活但无 permission 事件；恢复后 running
      空转直至人工 `tmux kill-session` → failed/waiting_review。与 SPIKE-1b 一致。
- [x] 恢复演练：杀掉审核者会话 → 新会话 tasks+attach 重建现场 → 处理 pending 后流程走通
      **本机 e2e 2026-08-08 强版本通过**（task `330a1f9a-…`）：`wait` 收到 permission 后不 reply，
      模拟失忆只靠 `tasks`+`attach` 读出 2 条 pending，reply 后走完 completed → done。
- [x] agentd 重启：任务执行中重启 agentd → RecoverOnStartup 重连 SSE，流程不中断；
      **本机 e2e 2026-08-08 通过**（task `679105d1-…`）：permission pending 时 kill agentd，
      tmux 会话仍在；重启后 state=waiting_answer、pending 未丢、事件无重复 permission 刷屏；
      无 waiter 时 reply 成功，后续 completed。
- [x] agentd 重启挂起自愈：权限门（permission_request）pending 期间重启 agentd，
      不依赖 /event 重放，直接 `handoff reply --ticket <id> --approve` →
      断言 executor 收到 "once" 恢复执行（tmux 侧 `tail -f <taskDir>/render.log`
      确认继续滚动，wait 收到后续事件）——验证 reply 无等待者时的 RelayAnswer 自愈中继
      **同上 task `679105d1-…` 一并覆盖**。
- [x] tmux attach 能看到 render.log 实况滚动
      **本机 e2e 2026-08-08 通过**：dispatch 后 `tmux ls` 见 `handoff-<id8>` 两窗；
      `done` 后会话被 adapter 回收。
- [x] 远程演练（可选功能）：devbox 上起 agentd，本机 --target devbox 跑通上述主链路
      **本机→远程 e2e 2026-08-08 通过**（task `cd1058f8-…`，executor `100.73.238.21`，
      opencode 1.18.13 + 免费模型 `opencode/big-pickle` via `HANDOFF_OPENCODE_MODEL`）：
      本机 `--target devbox` dispatch/wait/reply/done；3 次 permission 审批；
      远程仓库 `remote-hello.txt` 内容正确、commit `5e88029`、tmux 归档后回收。

注意（水位线应急方案 —— contingency，仅当 SPIKE-1b 证实 /event 重放历史时实施）：
在 serve.json 中持久化 last-seen-message 水位线（last_seen_seq）；RecoverOnStartup 恢复订阅时
跳过 seq ≤ 水位线的重放事件：permission/question 去重不入库，旧 result 直接丢弃、不驱动
completed→Stop。实现前先在 SPIKE-1b 确认事件是否携带可排序序号，不携带则以 messageID 比对。
此方案不在当前 MVP 实现范围内，由 SPIKE-1b 结论决定是否立项。
