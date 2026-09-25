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

## B409.4 unread 复审与 B409.5 计划（2026-09-25）

- 协调者复审 B409.4 时发现 `ListSessionsContext` 在 session 循环内重复执行 `unreadByRoom(events, cursors)`，对同一 events 重复折叠。计划作者已修订 `docs/superpowers/plans/2026-09-25-b409-4-plan.md`：改为循环外一次 room→unread 聚合，并加入结果等价与重复扫描反例。计划仍为 `plan`；卡 note seq 21594 记录了修订，原 plan 附件路径不变。
- B409.5 本机 subagent 完成 `docs/superpowers/plans/2026-09-25-b409-5-plan.md`。协调者复核 CLI 正文寻址、Pending/Mentions/Consume、共享 cursor、backlog/follow 输出时序、stdout 成功后推进、无逐条 ack、错误/cancel/timeout、多机恢复、wakeconsumer 边界及 U7 最终验收责任；计划单卡审查通过并挂附件，B409.5 note seq 21596，卡仍在 `plan`。
- B409.5 明确 implement 阻塞：B358 §3.8/§4.6 的订阅全流物理读取、session reliable-mentions contract 的 `(cursor, MaxSeq]` 全流分页与 B409 r2 的限域候选读要求不一致；须在实现前完成最小 contract delta。另需核 U4/U5 是否共享新增 `LedgerClient` seam，若共享先冻结接缝；全七计划集独立审计通过前所有实现派发关闭。没有修改冻结契约、没有测试或实现。
- 复核命令：B409.4 与 B409.5 的 `codegraph --repo . resolve --doc ...` 均 exit 0，`anchors: []`；B409.4 `git diff --check -- docs/superpowers/plans/2026-09-25-b409-4-plan.md` exit 0；B409.5 `git diff --no-index --check /dev/null docs/superpowers/plans/2026-09-25-b409-5-plan.md` exit 1（新文件差异，输出为空、无空白诊断）。未运行测试，因当前仍为 plan 节点。
- B409.6 与 B409.7 远端 plan 同样无法取得 origin 不含的本机批准分支，故转本机 subagent 仅撰写计划；卡 notes seq 21597/21598 留痕。没有派发实现，未改变节点状态。
- 本次文档提交命令与原始输出：`git commit -m "docs: review B409.4 and plan B409.5"` → `[codex/session-reliable-mentions 39f32277] docs: review B409.4 and plan B409.5`；`3 files changed, 98 insertions(+), 1 deletion(-)`，新增 B409.5 plan。随后仅 amend 一次收入原始提交输出；不追记 amend 后的新 hash。

## B409.2 计划复审（2026-09-25）

- 协调者复核 `/api/cards` 的实际路径 `handleCardsList → unlinkedSummary → Store.AllTaskLinks → projectTaskLinks("")`，确认 key 摘要只用 Target/TaskID，却随旧实现扫描全局 `EvDispatched` 投影。B409.2 计划因此修订为新增 context-aware、key-only 的 additive Store read；不调用 `projectTaskLinks`，保留既有 `AllTaskLinks()` 与全部旧消费者语义。计划加入隔离 PostgreSQL 下固定链接键、增加至少 10,000 条无关 dispatched 历史并测量 driver 实际 rows/列值 bytes 的验收，以及取消/超时关停验证。
- 当前 B409.4/5 计划没有复用此 key-only Store seam；B409.2 计划仍要求独立计划集审计重新核对，发现共享后先冻结契约。计划已通过单卡复审并挂到 B409.2，card note seq 21600；卡仍为 `plan`，依赖 U1 review/集成与全套计划审计。
- 复核命令：`codegraph --repo . resolve --doc docs/superpowers/plans/2026-09-25-b409-2-plan.md` exit 0，3 个源码锚均为 `ok`；`git diff --no-index --check /dev/null docs/superpowers/plans/2026-09-25-b409-2-plan.md` exit 1（新文件差异，输出为空、无空白诊断）。未运行测试，因为计划节点不实现代码。
- 本计划提交命令与原始输出：`git commit -m "docs: plan B409.2 async target summary"` → `[codex/session-reliable-mentions 7b326f59] docs: plan B409.2 async target summary`；`2 files changed, 83 insertions(+)`，新增 B409.2 plan。随后仅 amend 一次收入原始提交输出；不追记 amend 后的新 hash。

## B409.6 S4 诊断计划（2026-09-25）

- 本机 subagent 完成 `docs/superpowers/plans/2026-09-25-b409-6-plan.md`。协调者按 S4/r2 复核 request/CLI 总耗时、pool acquire、SQL/rows/bytes、collab 投影、U2/U4 后台 target、context cancel、敏感日志边界与真实调用入口；计划复用 Wave 0 的 DB.Conn pool wait 计时及历史查询日志字段，不建立通用 metrics 子系统。单卡审查通过并挂到 B409.6，card note seq 21602；卡保持 `plan`。
- 计划明确依赖 U1–U5 稳定实现/review；U5 的 B358/session wait contract delta 未冻结时 U6 implement 也阻塞。S4 故障诊断样本与 U7 三档全性能/方言验收区分；不把当前诊断外推成 20:00–22:00 历史故障根因。
- 复核命令：`codegraph --repo . resolve --doc docs/superpowers/plans/2026-09-25-b409-6-plan.md` exit 0，`anchors: []`；`git diff --no-index --check /dev/null docs/superpowers/plans/2026-09-25-b409-6-plan.md` exit 1（新文件差异、输出为空、无空白诊断）；相关 agentd/collab/CLI 生产入口 `rg` 复核 exit 0。未运行测试，因为仍是 plan 节点。
- 本计划提交命令与原始输出：`git commit -m "docs: plan B409.6 diagnostics"` → `[codex/session-reliable-mentions 372f1bdf] docs: plan B409.6 diagnostics`；`2 files changed, 111 insertions(+)`，新增 B409.6 plan。随后仅 amend 一次收入原始提交输出；不追记 amend 后的新 hash。

## B409.7 S5/最终验收计划（2026-09-25）

- 本机 subagent 完成 `docs/superpowers/plans/2026-09-25-b409-7-plan.md`。协调者复核 PG/SQLite golden、旧库升级、投影重建/重复重建/中断恢复、水位定义归属、隔离三阶段 seed、每故事生产入口、U5 真实跨机重挂证据、U6 诊断和本机/`linux-01` 最终验收责任。初稿将 p95/100 样本门扩大到 CLI；协调者要求按批准 r2/breakdown 精确限于 HTTP 与对应 Web 首屏。修订后其余 CLI/API 走故事功能验收及描述性计时，保留唯一 CLI 数值门 `session wait --timeout 5s` 30 次各 ≤6s。另修正已存在 U6 plan 被误报缺失的问题。
- 单卡复审通过并挂到 B409.7，card note seq 21604；卡仍在 `plan`。计划中的硬阻塞为七计划集独立审计、U5 的 B358/session reliable-mentions contract delta、U1–U6 实现 review/集成、确认可丢弃 PG 库及 U5 跨机器故事证据；不部署、不写真实/共享账本种子，协调者负责授权的本机和 `linux-01` UI/真机验收。
- 复核命令：`codegraph --repo . resolve --doc docs/superpowers/plans/2026-09-25-b409-7-plan.md` exit 0；store/open、web fetchCards 锚为 `ok`，handleCardsList 与 runCardWait 为 `moved`，未覆盖项回落源码并记录图债。`git diff --no-index --check /dev/null docs/superpowers/plans/2026-09-25-b409-7-plan.md` exit 1（新文件差异、输出为空、无空白诊断）。未运行测试，仍为 plan 节点。
- 本计划提交命令与原始输出：`git commit -m "docs: plan B409.7 final matrix"` → `[codex/session-reliable-mentions cfcf83c6] docs: plan B409.7 final matrix`；`2 files changed, 110 insertions(+)`，新增 B409.7 plan。随后仅 amend 一次收入原始提交输出；不追记 amend 后的新 hash。

## B409.1–B409.7 独立计划集审查修订（2026-09-25）

- GPT-6-Sol 独立审查者以干净提交 `a4f9fed41b7bb561b9c1acacdaacc54cfbd4da12` 审查七份计划，判定计划集暂不通过 implement 派发门：目标轴有一项 Important，U7 将工作台 SessionSidebar 列表和打开房间消息首屏合写，未分别锁定独立 100 样本与 p95 门槛；架构/证据轴计划层面通过，Critical/Minor 均无。它确认 U5 的 B358/session-v1 全流物理读与 r2 有界候选读冲突是计划已识别的实现硬门，不属于遗漏；当前证据不能证明 U4/U5 必然共享新 LedgerClient 方法，若实现前清点发现共享，再冻结最小 seam。
- 按审查意见修订 `docs/superpowers/plans/2026-09-25-b409-7-plan.md` 第 58、62、81 行：Web 明确拆成 `/cards` 卡片列表、`/` SessionSidebar 会话列表、`/` 已选中目标房间消息首屏三组；每组在每个数据档分别记首请求、5 次预热、100 次计入样本、独立 p50/p95/max/错误/response bytes，分别判 p95 ≤2s。为消息首屏预先打开并选中 fixture tab，刷新计时以导航开始为准，避免把列表与消息等待合成单个时长。B409.7 卡保持 plan，note seq 21605 记录首轮发现、修订与复审待完成；implement 派发门仍关闭。
- 新鲜校验：`git diff --check` exit 0；`codegraph --repo . resolve --doc docs/superpowers/plans/2026-09-25-b409-7-plan.md` exit 0，4 个锚均解析、无坏锚。未运行测试，因为本轮只修 Charter plan，不实现代码。修订后待独立审查者按新提交重新审计；在收到 PASS 前不派发 implement。
- 修订提交命令与原始输出：`git commit -m "docs: clarify B409 Web acceptance samples"` → `[codex/session-reliable-mentions 8617d1fd] docs: clarify B409 Web acceptance samples`；`2 files changed, 9 insertions(+), 3 deletions(-)`。随后按 Charter 仅 amend 一次收入该历史输出；不追记 amend 后 hash。

- GPT-6-Sol 独立审查者按修订后干净 HEAD `9b2ed8aa5f1094c228b19e0fbc27c57a5ee27151` 完成全套复核：**计划集 PASS**，目标轴与架构/证据轴均通过；首轮 U7 Important 已关闭，Critical/Important/Minor 剩余均无。确认工作台当前预选 tab 可随刷新恢复，修订里的计时方案可执行；其余维度沿用首轮 PASS。审查报告确认计划集 PASS 不解除 U5 读面 contract delta，也不解除各子卡自身依赖门。B409.7 note seq 21606 留痕；没有改卡状态。
- 复核实际命令：`git rev-parse HEAD` exit 0，SHA=`9b2ed8aa5f1094c228b19e0fbc27c57a5ee27151`；`git status --short` exit 0 且为空；`git diff a4f9fed..HEAD -- docs/superpowers/plans/2026-09-25-b409-7-plan.md` exit 0（仅 Web 样本分组措辞）；`git diff --check a4f9fed..HEAD` exit 0。没有运行实现测试，当前只完成计划审查。
- 计划集 PASS 记录提交命令与原始输出：`git commit -m "docs: record B409 plan-set audit pass"` → `[codex/session-reliable-mentions 1b3a0c8e] docs: record B409 plan-set audit pass`；`1 file changed, 3 insertions(+)`。随后按 Charter 仅 amend 一次收入该历史输出；不追记 amend 后 hash。

## B409.1 S1-U1 实现（2026-09-25）

- B409.1 的 `implement` 远端派发因 origin 缺少本机已批准分支再次拒绝；清除 `needs_human` 后依授权在当前工作树 inline 执行，卡从 `plan` 移入 `implement`。卡 note seq 21611 留痕，未推送分支、未触及 agentd 部署。
- 红测证据：新增 `TestOpenTicketProjectionMatchesCanonicalReplay` 后，旧实现运行 `go test ./internal/ledger/ -run '^TestOpenTicketProjectionMatchesCanonicalReplay$' -count=1` 退出 1，确切失败 `no such table: open_ticket_projection`；旧状态机基线 `go test ./internal/ledger/ -run 'Test(OpenTicketCounts|OpenTicketsTerminalMirrorClosesAll|OpenTicketsTerminalBeforeAnswerDoesNotResurrect)' -count=1` 退出 0（`ok github.com/Xsxdot/handoff/internal/ledger 0.757s`）。
- 实现新增 PG/SQLite 同构的未决工单投影与水位；`AppendMirroredEvent` 在既有幂等事务内维护开单、单票答复、任务作废和 `completed/failed/archived` 终态；旧库初始化/显式恢复原子重放 `card_events`。明细只读取当前投影 payload，计数 SQL 聚合不取正文；投影行数和正文 ticket_id 异常会报错。无完整 source target/task 身份的历史坏行从投影读取/水位中排除，兼容 B349 wakeconsumer 已忽略的畸形 identity。
- 新鲜行为覆盖：独立直接重放 oracle、跨 card/target/task 同 ID 隔离、创建 payload、重复来源事件、答复/作废/终态、旧库升级、重复重建、缺行恢复、投影写失败时 canonical append 回滚及 structured rows/bytes 观测。三档 SQLite 增长回归实测：阶段 1 为 9,341 总事件、9,337 mirror、6,473,800 mirror payload bytes；阶段 2 加 20,000 无关账本事件，总 29,341、mirror 数据不变；阶段 3 为 38,678 总事件、18,674 mirror、13,009,700 mirror payload bytes。三阶段业务状态均为同一 100 条未决票据；明细读恒为 100 行 / 2,100 payload bytes，计数恒为 4 聚合行 / 0 payload bytes。
- 变异自验：只在工作树临时把增量投影分支的 `completed` 终态改成无匹配值；`go test ./internal/ledger/ -run '^TestOpenTicketsTerminalMirrorClosesAll/completed$' -count=1 -v` 编译通过并按行为断言失败（completed 后 q1 仍可见）。随即恢复源码；`go test ./internal/ledger/ -run '^TestOpenTicketsTerminalMirrorClosesAll$' -count=1` 退出 0（0.970s），源码 diff 无残留。
- 实际命令与结果：`go test ./internal/ledger/...` 退出 0（最终轮 `internal/ledger` 4.185s、`internal/ledger/api` cached）；`go test ./internal/agentd -run '^TestB349AutomationSourceIdentity$' -count=1` 退出 0（1.262s）；`git diff --check` 退出 0。完整 `go test ./...` 在实现阶段曾提前运行作跨包回归检查：前两轮分别抓到空 payload envelope 与缺 `source_task` 的历史镜像兼容问题，修复后第三轮只剩 `TestHostGuardAllowsLoopbackAndConfigured` 临时回环端口 `can't assign requested address`；该用例独立复跑退出 0，之后完整套件复跑退出 0。全量测试仍须在 Charter integration/finish 节点新鲜复跑；最终全量发生在最后一个空切片兼容小改前，改后触及包与 B349 均已重跑。
- PostgreSQL 尚未验证：`TestOpenTicketProjectionPostgresReplayAndRebuild` 因未设置 `LEDGER_TEST_PG_DSN` 明确 skip，当前环境也无 `pg_isready`。未写入任何 PG/共享账本样本；U1 不可报告双方言验收完成。未证明任何测试结果是 2026-09-23 20:00–22:00 历史不可用的根因。
- 当前实现尚待 `charter:review` 独立双轴审查；B409.1 的 review/accept 未完成，U2/U3 依赖尚未放行。
- 实现提交原始命令与输出：`git commit -m "feat: add rebuildable open ticket projection"` → `[codex/session-reliable-mentions 9c211d46] feat: add rebuildable open ticket projection`；`7 files changed, 1031 insertions(+), 89 deletions(-)`，新增 `taskstate_projection_test.go` 与 `ticketprojection.go`。随后仅 amend 一次收入该原始输出；不追记 amend 后的新 hash。

- 独立审查发现的 U1 阻塞及修复：历史 room-history 镜像可同时有有效 `source_target/source_task`、空 `card_id`；旧账本升级重放曾将 NULL 扫入 Go `string`，导致 `Open` 的 schema 初始化失败。投影水位、重放和测试 oracle 现统一过滤 `card_id IS NOT NULL`；升级回归夹具包含上述历史行，并断言它不能生成卡工单且旧库可打开。先行红测实际失败为 `Scan error on column index 1, name "card_id": converting NULL to string is unsupported`，修复后 `go test ./internal/ledger/ -run '^(TestOpenTicketProjectionUpgradeReplaysExistingEvents|TestOpenTicketProjectionGrowthKeepsRowsAndPayloadBounded)$' -count=1` 退出 0。
- 独立审查还指出原增长矩阵把自动全量重建排除在测量值之外。矩阵现将 baseline 扩至 21,270 条总事件，并在各阶段计时/读观测之前显式重建投影；每个明细/计数查询均断言其日志不含全量重建事件。新鲜 `go test -v ./internal/ledger/ -run '^TestOpenTicketProjectionGrowthKeepsRowsAndPayloadBounded$' -count=1` 退出 0：阶段 1 为 21,270 总事件、9,337 mirror、6,473,800 mirror payload bytes；阶段 2 为 41,270 总事件；阶段 3 为 50,607 总事件、18,674 mirror、13,009,700 mirror payload bytes。三阶段明细读均为 100 行 / 2,100 payload bytes，计数读均为 4 行 / 0 payload bytes；每次 hot read 未发生投影全量重建。原先记录的 9,341/29,341/38,678 总数来自较小 baseline 且样本未排除自动重建，已由本条更正，不再作为性能验收证据。
- 修复提交原始命令与输出：`git commit -m "fix: ignore cardless mirrors in ticket projection"` → `[codex/session-reliable-mentions 82b8c463] fix: ignore cardless mirrors in ticket projection`；`3 files changed, 38 insertions(+), 5 deletions(-)`。本提交随后仅 amend 一次收入原始提交输出；不追记 amend 后新 hash。
- GPT-6-Sol 独立 fresh review 对当前干净 HEAD `8865403c5e2e734a0174d1c9b005b83c37dbeb0d` 的裁决：U1 代码目标/实现差异 PASS；Charter 架构与证据轴因 PG 必验腿未运行而 FAIL（证据未齐，不是已确认代码缺陷）；Critical/Minor 均无。审查确认 NULL `card_id` 水位/重放/oracle 一致过滤、旧库 Open 回归通过；增长矩阵样本外重建和热读无重建断言通过。实际复审 `go test ./internal/ledger/... -count=1` 退出 0；PG 定向测试用例 SKIP（`LEDGER_TEST_PG_DSN` 未设置），因此 DDL、JSONB、事务/重建及双方言一致仍未验。不能迁卡到 accepted，也不能宣称 U1 完整 PASS；U2/U3/U7 等相关验收仍受该缺口影响。
- 尝试将 fresh review 结果写入 B409.1 卡 note，`handoff card note B409.1 ...` 退出 1；为避免回显后端连接信息，命令输出被抑制。卡状态没有因此改变，也未直接改共享账本；待 CLI 可用时补写审查结论和状态。
- 审查证据提交原始命令与输出：`git commit -m "docs: record B409.1 review evidence"` → `[codex/session-reliable-mentions a28cc78f] docs: record B409.1 review evidence`；`1 file changed, 2 insertions(+)`。本提交随后仅 amend 一次收入原始提交输出；不追记 amend 后新 hash。

- U1 PostgreSQL 证据补齐（2026-09-25）：检测到本机 Docker Engine 可用并有 PostgreSQL 16 镜像；新建 `--rm` 临时容器，只使用数据库名 `handoff_b409_test`，没有连接共享账本。设置 `LEDGER_TEST_PG_DSN` 后先运行 `go test ./internal/ledger -run '^TestOpenTicketProjectionPostgresReplayAndRebuild$' -count=1 -v` → exit 0，`TestOpenTicketProjectionPostgresReplayAndRebuild` PASS；随后 `go test ./internal/ledger/... -count=1` → exit 0，`internal/ledger` 6.109s、`internal/ledger/api` 0.772s。容器已 stop，`docker ps -a --filter name=handoff-b409-pg-20260925` 无结果。此证据关闭先前“PG 测试跳过”的环境缺口；独立审查已确认的 U1 源码/增长矩阵 verdict 与卡/故事状态尚未通过 ledger CLI 更新，本记录不自行推进卡状态。

## U5 contract 校验与依赖闸更新（2026-09-25）

- GPT-6-Sol 定向复审 PASS：最终接受候选页使用一致席位状态；`ClearSeat`/`clearSeatTx` 图缺失说明准确，`CloseCard` 调用由当前源码核实；U5 plan 的结构化日志与意图注释步骤符合计划阶段纪律。无新的行为复审或测试结论。
- Fresh `codegraph --repo . resolve --doc docs/superpowers/specs/2026-09-25-session-bounded-candidate-read-contract.md` exit 0；`docs/superpowers/plans/2026-09-25-b409-5-plan.md` resolve exit 0；`git diff --check` exit 0。此前计划集 audit PASS 已登记于上方；实现仍须遵守 B409.4→B409.5 账本阻塞边。
- 当前依赖快照来自 `handoff card list --project handoff --json`：B409.1 为 `review`；B409.2/.3 受 B409.1 阻塞；B409.4 受 B409.2/.3 阻塞；B409.5 受 B409.4 阻塞。不得因计划集 audit PASS 而跳过故事依赖和逐卡 review/accept。
