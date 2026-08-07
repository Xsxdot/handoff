# E2E 手动验证清单（真实 opencode）
前置：executor 机装 opencode 并配好模型凭证；`handoff agentd --executor=opencode` 已起。
- [ ] SPIKE-1（spec 风险#1）：手动 `opencode serve` + curl 建会话发 prompt，抓 /event SSE 原始样本：
      确认 permission 事件类型名/字段、回合结束（idle）事件类型名 —— 对照调整 adapter 映射
- [ ] SPIKE-2（spec 风险#2）：验证 OPENCODE_CONFIG 环境变量注入配置生效（permission.bash=ask 真的会问）；
      不生效则切 fallback：写 repo 内 opencode.json + gitignore
- [ ] dispatch → wait 被 permission_request 唤醒 → approve → 执行继续 → completed → diff 有内容
- [ ] deny 链路：reply --deny 后 executor 收到 reject 并调整做法
- [ ] 提问链路：executor 输出 {"ask":...} → wait 唤醒 → reply --answer → 下一回合收到原文
- [ ] 审批挂起过夜：permission 不答搁置 8h 后 approve —— opencode 侧等待不超时、流程继续（替代原 hook 长阻塞 spike）
- [ ] continue：回发修改指令 → 同一 session 续接 → 二轮 completed diff 含新改动
- [ ] 断网演练：wait 期间关 Wi-Fi 3min 恢复 → 自动重连并收到期间积压事件
- [ ] 恢复演练：杀掉审核者会话 → 新会话 tasks+attach 重建现场 → 处理 pending 后流程走通
- [ ] agentd 重启：任务执行中重启 agentd → RecoverOnStartup 重连 SSE，流程不中断
- [ ] tmux attach 能看到 render.log 实况滚动
- [ ] 远程演练（可选功能）：devbox 上起 agentd，本机 --target devbox 跑通上述主链路
