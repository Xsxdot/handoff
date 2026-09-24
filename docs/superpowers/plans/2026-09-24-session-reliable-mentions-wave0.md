# 会话定向消息可靠续收 Wave 0 计划

批准基准：[r2](../specs/2026-09-24-session-reliable-mentions.md)；前置冻结：[投递契约 v1](../specs/2026-09-24-session-reliable-mentions-contract.md)，提交 `1fa7194a`。本轮竖切目标：同一账本中，一台机停监听期间收到的定向消息，在另一台机重挂后以一条 backlog 摘要补出；已输出的摘要不重复。

1. **红窗**：在 `internal/ledger` 写共享游标缺行/单调/重开测试；在 `cmd/session_test.go` 将旧“流尾起点”断言替换为 backlog 断言，并测一次性重挂去重、显式 `--since` 不读写持久水位、stdout 失败不推进。先跑局部测试确认行为红。
2. **账本实现**：PG/SQLite 同名 schema；Store 读/单调推进 API。写失败必须向 CLI 传播，记录 member/seq/cause，不记录消息正文或连接凭据。
3. **CLI 竖切**：启动时 `MaxSeq` 定积压边界，按 `EventsFromAsc` 升序分页扫描、`MessageWakeTargets` 唯一寻址；引用条沿用 `buildSessionWake` 的解析。积压聚合一条 `session_backlog`，写 stdout 成功后写最高命中水位；之后实时循环单条 `SessionWake`，同样写后落水位。`--since` 保留只读手工回放。
4. **Wave 0 验收**：局部 Go 测试、CLI 真 SQLite 进程重启测试、PG 能连接时做异机同 DSN 验证；前端既有小修单独跑 typecheck/test/build。记录所跑命令与限制，代码图 check 与图覆盖债。若 PG 不可用，不能声称异机真链已验，留给集成。

后续故事：`--follow` 积压摘要与正文 @ 解析在同一 CLI 读写链补齐；桌面 token 金样本和 CLI 相同。UI 假空态修复另走已有 debug 红绿链，不依赖此 Wave 0。旧 B358 流尾测试必须随外部契约有意识更新，不能机械保持。

## Wave 0 验收交棒（2026-09-24，追加；批准基准未改）

- 阶段裁决：**Wave 0 通过**。本轮选定故事为 S2 的共享交付进度与跨机器恢复竖切；验收边界是同一 PostgreSQL 中“Mac 首次消费 → 停听时入账 → Linux CLI 重挂补收 → Mac 重挂不重复”。S1/S3/S4 与桌面会话页面不由本次 Wave 0 单独核销。
- 被测代码基准：`3390cd2f3e7ca86c17d3a5b0841b7a7ac4c1deda`；本机 agentd/CLI 版本 `3390cd2f3e7c`。Linux 临时 CLI SHA256 为 `e24a0163d339d88f147d54c742696e446816dbd2600d1743d9a53b2a414709d1`。跨机轨迹、独立 review、全量测试与承重变异结果记在 `docs/superpowers/ledgers/2026-09-24-session-incidents.md`。
- 分工草案与下一去向：进入 `charter:breakdown`，按 S1（正文 @ 编译及桌面金样本一致）、S3（积压摘要与 `--follow`）、S4（无效/无寻址/reply_to/卡席位边界）整理故事证据和最终验收清单；桌面假空态与取消超时链单列回归轨迹。实现已先于 Wave 0 验收完成的顺序偏差照实保留，不为补流程重排提交或虚构子卡完成历史。随后按可取得证据进入最终 acceptance；当前尚未最终归档、recon 或 finish。
- 运行边界：`linux-01` 原配置中的 `approver.models` 与新 CLI 不兼容，本次只用权限 `0600` 的临时最小账本配置跑异机 CLI，并已删除临时文件；正式配置和远端 agentd 未改。该兼容残余见 roadmap 队列第 6 项。

## 后续交棒（2026-09-24，breakdown 已拍板）

- Breakdown 提案 `docs/superpowers/specs/2026-09-24-session-reliable-mentions-breakdown.md` 已由协调者审阅并拍板；原列出的两项分岔均有裁决与理由，不建重复实现/验收卡，Linux 配置问题留在 roadmap 单独定性。
- Wave 0 的下一站 breakdown 已完成。当前下一站是最终 acceptance：按 breakdown §5 补核 S1 完整寻址矩阵、S3 多条积压摘要后继续 follow、S4 负例/reply/席位矩阵，并单列桌面假空态与取消轨迹；稳定的 `3390cd2f` / PostgreSQL 接缝可复用 S2 Wave 0 证据。完整最终验收尚未通过，不得 finish 或归档。

## 最终验收交棒（2026-09-24，r2 故事验证完成；原始查询性能残余未关闭）

- 被测代码仍为 `3390cd2f3e7ca86c17d3a5b0841b7a7ac4c1deda`；没有改动实现。新鲜定向复跑：`cmd` 会话等待/发送/回复 6 项通过（2.115s），`internal/collab` 寻址/取消/回复 5 项通过（0.880s），前端 `usePoll`、`SessionChat`、`SessionTab`、`sessionModel` 4 个测试文件共 58 项通过（1.71s）。
- S1/S3/S4 最终共享 PG 真 CLI 轨迹、UI 页面截图证据、临时数据回收、代码图结果和根因读数均在 `docs/superpowers/ledgers/2026-09-24-session-incidents.md`。S2 引用已通过的 Wave 0 异机基线。批准的 r2 故事 S1–S4 可裁为通过。
- 原始“会话页面打不开/像无消息”问题仍未完全收敛：实机页面曾先停在加载态，随后显示列表请求超时（15000ms），重试后恢复并显示消息；现有全事件顺序扫描造成 13–22s 读延迟，CLI `--timeout 5s` 也实测超过时限。昨晚 20:00–22:00 的专属根因没有请求级历史证据。此查询优化会新增账本/协作读缝，已转至 roadmap 队列第 1 项，需先经独立 Charter spec/契约批准，未在本轮改代码。
- Charter 阶段结论：批准的 r2 行为验收通过；原始故障的性能/过载残余仍开着，因此本任务不作整体完成、recon 或 finish 声明。下一去向为 roadmap 第 1 项的 spec 裁决。
