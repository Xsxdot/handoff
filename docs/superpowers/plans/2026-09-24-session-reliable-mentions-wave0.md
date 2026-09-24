# 会话定向消息可靠续收 Wave 0 计划

批准基准：[r2](../specs/2026-09-24-session-reliable-mentions.md)；前置冻结：[投递契约 v1](../specs/2026-09-24-session-reliable-mentions-contract.md)，提交 `1fa7194a`。本轮竖切目标：同一账本中，一台机停监听期间收到的定向消息，在另一台机重挂后以一条 backlog 摘要补出；已输出的摘要不重复。

1. **红窗**：在 `internal/ledger` 写共享游标缺行/单调/重开测试；在 `cmd/session_test.go` 将旧“流尾起点”断言替换为 backlog 断言，并测一次性重挂去重、显式 `--since` 不读写持久水位、stdout 失败不推进。先跑局部测试确认行为红。
2. **账本实现**：PG/SQLite 同名 schema；Store 读/单调推进 API。写失败必须向 CLI 传播，记录 member/seq/cause，不记录消息正文或连接凭据。
3. **CLI 竖切**：启动时 `MaxSeq` 定积压边界，按 `EventsFromAsc` 升序分页扫描、`MessageWakeTargets` 唯一寻址；引用条沿用 `buildSessionWake` 的解析。积压聚合一条 `session_backlog`，写 stdout 成功后写最高命中水位；之后实时循环单条 `SessionWake`，同样写后落水位。`--since` 保留只读手工回放。
4. **Wave 0 验收**：局部 Go 测试、CLI 真 SQLite 进程重启测试、PG 能连接时做异机同 DSN 验证；前端既有小修单独跑 typecheck/test/build。记录所跑命令与限制，代码图 check 与图覆盖债。若 PG 不可用，不能声称异机真链已验，留给集成。

后续故事：`--follow` 积压摘要与正文 @ 解析在同一 CLI 读写链补齐；桌面 token 金样本和 CLI 相同。UI 假空态修复另走已有 debug 红绿链，不依赖此 Wave 0。旧 B358 流尾测试必须随外部契约有意识更新，不能机械保持。
