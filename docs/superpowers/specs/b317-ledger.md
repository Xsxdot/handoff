# B317 台账

- 2026-09-03 B316 任务 `681a7e9f`：`permission_auto_allow` 4 条（git status/log、ls）；其余 `permission_request` + `ticket_answered allow`。`approver_disabled`：连续 3 次失败。
- 2026-09-03 linux-01 `agentd.log`：`approver.executor=opencode` model 空 → `kimi-for-coding-highspeed`。三次失败 `cause=exit status 1` `You've reached your 5-hour usage limit` elapsed ~2.8s。不是进程死、不是 one-shot 映射崩。
- 2026-09-03 用户：只读进白名单；审批者改 Codex 默认模型。`go run` 不扩。
- 2026-09-03 红：`TestOneShotArgs` 未知执行者 codex；`TestJudgeSafeCommandTable/git-grep` 等 Consult；`TestPermTextAndRequestFindByNameSearchDirectory` Tool=other；`TestApproverDecisionErrorRecordsCause` reason 空超时。实现后触及包绿。变异四靶（git-grep id / SearchDirectory / Err→reason / case codex）均可编译且对应测试红。linux-01 配置未改：现网二进制无 OneShotArgs(codex)，先合 main 再改 `approver.executor` 并重启。
