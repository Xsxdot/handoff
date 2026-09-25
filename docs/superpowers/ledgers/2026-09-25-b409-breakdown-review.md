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

## B409.1 计划节点追加记录（2026-09-25）

- 本机 subagent 完成 L3 重档子卡计划 `docs/superpowers/plans/2026-09-25-b409-1-plan.md`；协调者按 `charter:plan` 复核目标、改动边界、真实调用路径、B380 C-2/B156.2 适用契约及验收责任。计划通过单卡审查并挂入 B409.1 `plan` 附件；卡 note seq 21585 记录审查和阻塞门。计划把三档合成增长矩阵的固定业务状态、实际传输行/字节观测及 U1 与 S1/U7 的性能责任边界写明；并明确 B409.2–B409.7 计划未齐稿、独立计划集审计未通过前，不派发任何 implement。B409.1 仍留在 `plan`，不代表允许开始实现。
- 复核命令：`codegraph --repo . resolve --doc docs/superpowers/plans/2026-09-25-b409-1-plan.md` → exit 0，两个源码锚均为 `moved`；`git diff --no-index --check -- /dev/null docs/superpowers/plans/2026-09-25-b409-1-plan.md` → exit 1（新文件有差异，无空白诊断）；未运行 Go 测试，因为本节点只交计划、不实现代码。CLI `handoff card update B409.1 --attach plan:docs/superpowers/plans/2026-09-25-b409-1-plan.md` 成功；`handoff card note B409.1 ...` 成功，seq 21585。未改变卡状态、未派发实现、未部署。
- 此记录与计划同批提交。提交后按 `charter:plan` 仅 amend 一次收入本次提交原始输出；不追写 amend 后的新 hash。
- 提交命令与原始输出：`git commit -m "docs: plan B409.1 open ticket read model"` → `[codex/session-reliable-mentions df4a9493] docs: plan B409.1 open ticket read model`；`2 files changed, 89 insertions(+)`，新增计划文件。随后仅 amend 一次将该历史输出纳入同批提交；不追记 amend 后的新 hash。
- 后续子卡 plan 回退记录：B409.2、B409.3、B409.4、B409.5 均因 `origin` 缺少 `codex/session-reliable-mentions` 而无法取得已批准输入，按相同本机 subagent 回落规则继续 plan 节点；卡 note 分别 seq 21586、21587、21588、21591。B409.2 计划在协调者要求补齐 refresh worker 关停/cancel 边界后复核中；B409.3 与 B409.4 已审查，B409.5 正在撰写。实现派发门仍由全计划集独立审计控制。
- B409.3 计划已完成并经协调者审查：`docs/superpowers/plans/2026-09-25-b409-3-plan.md`；卡上 plan 附件挂接成功，review note seq 21590。核对了 U3 → `runCardWait` → `encodeCardWaitSnapshot` → `Store.OpenTickets`/`NeedsReasons` 路径、快照 JSON 字段与 B380 C-2/B156.2 Follow 边界；计划通过，卡留在 `plan`。作者自检 `codegraph resolve --doc` exit 0；未运行测试（plan 节点）。
- 提交命令与原始输出：`git commit -m "docs: plan B409.3 card wait snapshot"` → `[codex/session-reliable-mentions 37b61f33] docs: plan B409.3 card wait snapshot`；`2 files changed, 81 insertions(+)`，新增 plan。随后只 amend 一次收入本次实际提交输出，不追记 amend 后的新 hash。
- B409.4 计划已完成并经协调者审查：`docs/superpowers/plans/2026-09-25-b409-4-plan.md`；附件挂接成功，review note seq 21593。复核 session list/detail、room page、member unread 的真实生产链，以及 B0、B156.2、B358、B374 边界；计划通过，卡保持 `plan`。协调者新跑 `codegraph --repo . resolve --doc ...` exit 0（无坏锚）和 `git diff --no-index --check -- /dev/null ...` exit 1（新文件差异、无空白诊断）；未运行测试（plan 节点）。
- 提交命令与原始输出：`git commit -m "docs: plan B409.4 session read paths"` → `[codex/session-reliable-mentions 6ec36af0] docs: plan B409.4 session read paths`；`2 files changed, 83 insertions(+), 1 deletion(-)`，新增 plan。随后仅 amend 一次收入原始输出，不追记 amend 后的新 hash。
