# E2E 手动验证清单（真实 opencode）
前置：executor 机装 opencode 并配好模型凭证；`handoff agentd --executor=opencode` 已起。
- [ ] SPIKE-1（spec 风险#1）：手动 `opencode serve` + curl 建会话发 prompt，抓 /event SSE 原始样本：
      确认 permission 事件类型名/字段、回合结束（idle）事件类型名 —— 对照调整 adapter 映射
- [ ] SPIKE-1b（spec 风险#1 续，/event 重放语义）：**重连时是否会收到断连前的历史事件？**
      实测：订阅 /event 收到若干事件后断开连接（或直接重启消费进程），重连后观察首条事件的
      messageID/seq —— 若事件带序号，顺带确认序号的单调性与断点衔接方式。**若证实会重放历史**，
      列出两类危害并启用「水位线应急方案」（见文件末尾注意事项）：
      (a) 重复 permission_request / question 事件重复入库，审核者重复收到、重复应答；
      (b) 重放的旧 result 事件重新驱动 handleResult → completed → Adapter.Stop，
          杀死真实存活的 tmux 执行器会话 —— 数据丢失
- [ ] SPIKE-2（spec 风险#2）：验证 OPENCODE_CONFIG 环境变量注入配置生效（permission.bash=ask 真的会问）；
      不生效则切 fallback：写 repo 内 opencode.json + gitignore
- [ ] dispatch → wait 被 permission_request 唤醒 → approve → 执行继续 → completed → diff 有内容
- [ ] deny 链路：reply --deny 后 executor 收到 reject 并调整做法
- [ ] 提问链路：executor 输出 {"ask":...} → wait 唤醒 → reply --answer → 下一回合收到原文
- [ ] 审批挂起过夜：permission 不答搁置 8h 后 approve —— opencode 侧等待不超时、流程继续（替代原 hook 长阻塞 spike）
- [ ] continue：回发修改指令 → 同一 session 续接 → 二轮 completed diff 含新改动
- [ ] 断网演练：wait 期间关 Wi-Fi 3min 恢复 → 自动重连并收到期间积压事件
- [ ] 恢复演练：杀掉审核者会话 → 新会话 tasks+attach 重建现场 → 处理 pending 后流程走通
- [ ] agentd 重启：任务执行中重启 agentd → RecoverOnStartup 重连 SSE，流程不中断；
      断言（结合 SPIKE-1b 结论）：
      - 重启后无重复 permission_request / question 事件（对照重启前的消息记录逐条核对）
      - 重启前已完成的旧 result 不得重新驱动 completed → Stop：真实存活的执行器
        会话必须保持存活（`tmux ls` 仍见会话 + attach 看 render.log 继续滚动）
      - 若 SPIKE-1b 证实 /event 重放历史 → 本条必须在「水位线应急方案」落地后通过，
        否则按数据丢失风险阻塞验收
- [ ] agentd 重启挂起自愈：权限门（permission_request）pending 期间重启 agentd，
      不依赖 /event 重放，直接 `handoff reply --ticket <id> --approve` →
      断言 executor 收到 "once" 恢复执行（tmux 侧 `tail -f <taskDir>/render.log`
      确认继续滚动，wait 收到后续事件）——验证 reply 无等待者时的 RelayAnswer 自愈中继
- [ ] tmux attach 能看到 render.log 实况滚动
- [ ] 远程演练（可选功能）：devbox 上起 agentd，本机 --target devbox 跑通上述主链路

注意（水位线应急方案 —— contingency，仅当 SPIKE-1b 证实 /event 重放历史时实施）：
在 serve.json 中持久化 last-seen-message 水位线（last_seen_seq）；RecoverOnStartup 恢复订阅时
跳过 seq ≤ 水位线的重放事件：permission/question 去重不入库，旧 result 直接丢弃、不驱动
completed→Stop。实现前先在 SPIKE-1b 确认事件是否携带可排序序号，不携带则以 messageID 比对。
此方案不在当前 MVP 实现范围内，由 SPIKE-1b 结论决定是否立项。
