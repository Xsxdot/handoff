# B409 breakdown 与 S1 摘要契约审议台账

日期：2026-09-25
父卡：B409「全账本读路径分域与投影性能：卡片、会话、房间与等待」
批准基准：`docs/superpowers/specs/2026-09-24-ledger-read-performance.md` r2
已验阶段：S2 Wave 0，证据见 `docs/superpowers/ledgers/2026-09-25-b409-wave0-acceptance.md`

## 协调者裁决

- S1 采用 U1 ledger 提供侧、U2 API/Web、U3 `card wait` 三个有界工作单元；先完成 U1，再由 U2/U3 并行消费，最终仍以 S1 故事集成验收。
- U4（S2 列表/未读）与 U5（S3 收件箱/wait）保持独立故事单元。当前无证据要求它们必然共用新增 LedgerClient 方法；plan 查实若跨单元共享新公开接缝，则在首个消费者实现前冻结最小契约，否则不造空 contract。
- S1 的外部摘要形状冻结在 `docs/superpowers/specs/b409-contract.md`：保留 `count/tasks/unknown_targets`，新增 `status/observed_at`；只有完整、30 秒内的 `latest` 可驱动当前数量和筛选。30 秒后展示陈旧观测及年龄，不设隐藏期限。
- 远端部署授权核对：`handoff card show B409` 的事件 seq 21326 记录用户已授权本机和 `linux-01` 更新部署，并明确 UI 验收不经 SuperDev。Wave 0 本轮只使用隔离验收 agentd，未部署 `linux-01`；最终真机 acceptance 阶段按既有授权执行。
- 用户问及 5 分钟陈旧值的理由后，按已批准 r2 澄清：**没有 5 分钟隐藏上限**。保留历史观测用于区分“旧数据”和“从未有数据”，同时通过 `status` 禁止陈旧值参与当前计数/筛选。

## 图与文档核对

- `codegraph sym` 命中：`Store.OpenTicketCounts`、`Server.handleCardsList`、`Server.unlinkedSummary`、`Service.ListSessions`、`Service.ListRooms`、`Service.Unread`、`runCardWait`、Web `UnlinkedSummary`、`UnlinkedRow`、`unlinkedOnly`；现状锚状态为 `moved` 或 `ok`。
- `codegraph sym` 未命中：`Store.OpenTickets`、`Store.RoomMessagesBeforeContext`、`runSessionWait`、Shell 局部值 `unlinkedTaskIds`。已按真源码回落定位并在 breakdown 中登记为图覆盖债；未伪造图节点。
- `codegraph --repo . resolve --doc docs/superpowers/specs/b409-breakdown.md` → exit 0；现有符号锚均解析为 `moved`。
- `codegraph --repo . resolve --doc docs/superpowers/specs/b409-contract.md` → exit 0；Go 锚为 `moved`，Web 已登记锚为 `ok`。
- `git diff --check` → exit 0。
- 本节点没有修改 `codegraph/target.json`、`best.json` 或分支视图；无新依赖方向、组装点或预算。没有建立 Ticket 0；协议由现有 `/api/cards` additive JSON 面表达。
- 提交命令与原始输出：`git commit -m "docs: freeze B409 ledger breakdown and summary contract"` → `[codex/session-reliable-mentions bbb6f2d2] docs: freeze B409 ledger breakdown and summary contract`；`3 files changed, 278 insertions(+)`。随后只 amend 一次，把该历史输出收入同批提交；不追记 amend 后的新 hash。

## 当前边界

本台账和两份文档完成协调者审议与冻结；S1–S5 除 S2 Wave 0 外仍未实现/验收。未创建子卡、未派发实现，不能据此宣称 B409 或生产 20:00–22:00 故障已整体解决。

## 子卡扇出追加记录（2026-09-25 12:29）

上段记录了拆解提交当时的状态。随后按 `product-backlog` 的 L3 重档流程完成子卡扇出：`handoff card list --project handoff --json` 在操作前未找到 B409 子卡；`card split` 新建下列七张，随后统一设为高优先级、挂父 spec/contract/breakdown 与适用既有契约，并从 `待办` 移到 `plan`。

| 子卡 | 工作单元 | 当前状态 |
|---|---|---|
| B409.1 | S1-U1 ledger 未决工单读模型 | plan |
| B409.2 | S1-U2 /api/cards 与 Web 远端摘要状态 | plan |
| B409.3 | S1-U3 card wait 建连快照 | plan |
| B409.4 | S2-U4 session/room 列表与未读投影 | plan |
| B409.5 | S3-U5 收件箱消费与 session wait 限域恢复 | plan |
| B409.6 | S4-U6 账本读路径诊断闭环 | plan |
| B409.7 | S5-U7 PG/SQLite 投影重建与增长矩阵验收 | plan |

实际阻塞边（`handoff card link <blocker> <blocked>`，每条 exit 0）：B409.1→B409.2、B409.1→B409.3、B409.2→B409.4、B409.3→B409.4、B409.4→B409.5、B409.5→B409.6、B409.6→B409.7。各子卡已写入节点中立的验收判据；B409.5 另挂 B156.2、B358 与 session reliable-mentions 冻结物，B409.4/B409.7 挂 B156.2。

父卡已挂 `b409-contract.md` 和 `b409-breakdown.md`，`handoff card move B409 integrate --expect implement` exit 0，当前父卡状态 `integrate`。尚未派发 plan 或 implement：S2 Wave 0 实现仍在本地工作树未提交，先完成独立 review 并将已验基线提交后再按依赖派发。此时没有部署 `linux-01`。

该追加记录提交命令与原始输出：`git commit -m "docs: record B409 child card fanout"` → `[codex/session-reliable-mentions 94127aee] docs: record B409 child card fanout`；`1 file changed, 18 insertions(+)`。随后只 amend 一次，把该历史输出收入同批提交；不追记 amend 后的新 hash。

## S2 Wave 0 封版与计划派发回退（2026-09-25）

- S2 源码、测试及脱敏证据已提交 `0703b2eb1675b2229a5f4124998683d2de9dd02f`；独立 GPT-6-Sol 实现 review PASS，I1（缺可取得被测提交）与 I2（旧 CLI 样本归属/连接位置材料）按 `docs/superpowers/ledgers/2026-09-25-b409-wave0-acceptance.md` 关闭。提交后聚焦 Go 测试 exit 0。父卡 B409 的 note seq 21575 记录验收完成（仅 S2）。
- B409.1 `plan` 首派 seq 21577 因远端默认 `main` 不含父 spec 被拒；按 handoff skill 设显式基线 `codex/session-reliable-mentions` 并清掉 `needs_human` 后重派，第二次仍因 `origin` 没有该本机分支而失败（seq 21581）。没有推送分支。
- 按全局执行规则改走本地 subagent，仅由其撰写 `docs/superpowers/plans/2026-09-25-b409-1-plan.md`，计划审查/验收前 B409.1 保持 `plan`，不派发实现；卡 note seq 21583 记录了回退理由。当前其他子卡仍在 `plan` 且受 DAG 阻塞，未部署 `linux-01`。

本次追加的提交命令与原始输出：`git commit -m "docs: record B409 Wave 0 review closure"` → `[codex/session-reliable-mentions dd7a1e0a] docs: record B409 Wave 0 review closure`；`1 file changed, 6 insertions(+)`。随后仅 amend 一次，把该历史输出收入同批提交；不追记 amend 后的新 hash。
