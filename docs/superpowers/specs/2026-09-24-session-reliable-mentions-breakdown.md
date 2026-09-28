# 会话 @ 寻址与断线续收：Breakdown 提案

> **状态：已拍板（2026-09-24）。**
> 批准基准：r2 spec（2026-09-24 用户批准）；前置契约 v1（Wave 0 计划记为冻结提交 `1fa7194a`）；Wave 0 被测基准 `3390cd2f3e7ca86c17d3a5b0841b7a7ac4c1deda`。
> S2 的 Wave 0 已通过。S1/S3/S4 的实现也已在 Wave 0 验收前进入该基准；这是已记录的流程顺序偏差，不在本文改写成合规顺序。

## 原待拍板项（均已裁决）

1. **剩余最终验收的记账方式**：裁决为不建独立验收卡。当前工作以 cardless 本地任务开始，S1/S3/S4 已有实现、没有待分派工程工作；最终回归由本地协调者负责，证据继续记在本计划与 incident ledger，不追建父卡或重复子卡，也不把验收伪装成实现卡。
2. **Linux 配置兼容残余**：裁决为保留 Roadmap 第 6 项，作为独立后续缺陷定性；本轮不改远端 `/root/.handoff/config.yaml`、不升级 Linux agentd，也不把它扩进 r2。若后续决定修复，应另走 bug/debug 流程并先补可复现配置加载测试。

## 1. 触及子系统与派卡资格

类型与结构按 `codegraph/best.json` 中 `parent` 为空的顶层域判定；子域不升格为独立子系统。以下逐项核架构法第一条的有界文件、可枚举契约、依赖 DAG、类型四条件。代码已存在，因此资格核对不等于建议派卡。

| 子系统（图 ID） | 类型 | 本故事的有界范围与契约面 | 依赖与派卡资格核对 |
|---|---|---|---|
| 协调者命令面 `d_cli` | 逻辑型 | `cmd/session.go`、`cmd/session_test.go`；对外面为 `session send` / `session wait` 参数及逐行 JSON 输出；入口 `cmd/session.go#runSessionWait`（源码同文件 `sessionWaitMatch`、`sessionSendCmd`、`sessionMessageMentions`） | 文件集明确；CLI 命令与 stdout 形状可枚举；沿既有 `d_cli→d_collab`、`d_cli→d_ledger`、`d_cli→d_protocol` 方向，无需新增方向；满足四条件。 |
| 协议契约 `d_protocol` | 逻辑型 | `internal/proto/sessions.go`；既有 `RoomMessage` / `SessionWake` 和本轮冻结的 `SessionBacklog` JSON 形状；入口 `internal/proto/sessions.go#SessionWake`，新 `SessionBacklog` 在同一源码文件 | 文件集明确；消息/摘要字段可枚举；消费方沿既有图边；满足四条件。此处复用/扩充既有协议域，不另建 DTO 包。 |
| 协作房间 `d_collab` | 逻辑型 | `internal/collab/sessions.go`、`internal/collab/room/delivery.go` 及对应测试；唯一寻址入口 `internal/collab/sessions.go#Service.MessageWakeTargets` / `ResolveDelivery` | 文件集明确；消费面为 collab service 与已有 `LedgerClient`；`d_collab→d_ledger` 仅走既有接口，目标图零直接调用预算；依赖 DAG 不变；满足四条件。 |
| 卡片账本 `d_ledger` | 逻辑型 | `internal/ledger/session_delivery_cursor.go`、PG/SQLite schema 与账本测试；Store 读水位及单调推进为接缝；入口 `internal/ledger/store.go#Store`，新游标操作定义在 `session_delivery_cursor.go` | 文件集明确；Store 操作可枚举；`d_cli→d_ledger` 是既有 `ledger.Store` 方向，`d_ledger→d_protocol` 既有；满足四条件。 |
| Web 控制台 `d_web`（S1 一致性测试侧） | 逻辑型 | 子域 `d_web_command` 的 `web/src/app/rooms/sessionModel.ts#extractSessionMentions` 及金样本测试；只负责与 CLI 共用正文 token 判定样本 | 文件集明确；接缝是现有浏览器消息发送形状；同属 `d_web` 内部子域，不拆成独立子系统卡；满足四条件但本次没有待实现工作。 |

本轮依赖方向只使用已有目标图：CLI 调用 collab 寻址入口、读写 ledger Store，并复用 protocol 类型；collab 到 ledger 仍经消费方定义的 `LedgerClient`，不穿透 ledger 内部。契约记录没有新增组装点、依赖方向或预算。`d_web` 的 token 金样本属于 S1 一致性覆盖，不改变后端投递权威。

### 相邻独立轨迹：会话页假空态与请求取消

这条轨迹来自原始“会话页面无消息/疑似查询阻塞”问题，独立于 r2 的 S1–S4。它涉及 `d_web` 的 `web/src/app/rooms/SessionChat.tsx#SessionChat` 加载/错误状态、`web/src/app/data/usePoll.ts#usePoll` 轮询，以及 `d_gateway`（边界型，浏览器 HTTP 请求）至 `internal/ledger/events.go` 的 `EventsFromAscContext` 历史读取取消链。已有实现和红绿记录在 incident ledger；不新增 r2 故事、不与 CLI @ 寻址或交付游标混派，也不以此证明昨晚 20:00–22:00 故障根因已查明。最终验收时作为单独回归轨迹核对：加载中、请求失败、重试恢复、组件卸载/超时取消后不继续占用查询。页面真机读数仍列在 §5。

## 2. 边界与冻结核对

- r2 已批准，contract v1 已冻结。契约明确：无 `--since` 时按共享 member 水位排他续读；显式 `--since` 只读回放、不改水位；stdout 写成功后才推进；读写错误不得伪装成空游标/成功；`session_backlog` 摘要字段和 `SessionWake` 实时行形状固定。
- 正文有效 `@agent:` / `@user:` / 卡号 token 由 CLI 自动并入既有 `mentions`，与显式 `--mention` 合并去重；无效 token 保留正文、不寻址；桌面与 CLI 使用同一组金样本。投递目标仍只由 `MessageWakeTargets` / `ResolveDelivery` 判定，身份逐字匹配，不做成员广播。
- 本期共享 cursor 是交付水位而非模型处理回执；无逐条 ack。stdout 已写出但模型随后因限额未处理不在承诺内；同一 member 多个主动监听器的唯一认领也不在承诺内。迟写失败可重投，使用方按 seq 去重。
- 不新增冻结对象：持久 Store 操作和 CLI 摘要语义已经在前置契约冻结；Web token 解析沿用既有 `RoomMessage.mentions`；不改 HTTP wire、组装点或会话消息投递判据。若实现复核发现需要新增独立共享接缝或对外承诺，应退回 contract 冻结，不在 breakdown/plan 临时扩边。
- `linux-01` 原配置含解析器不支持的 `approver.models`；当前 CLI 解析失败会落到相对路径 SQLite。Wave 0 为避免误测，使用临时最小 PG 配置通过；正式配置与远端 agentd 未改。Roadmap 第 6 项是唯一账面位置。本轮验收不得在未确认账本 DSN/会话可见性的情况下把本地空库结果当成 PG 证据。

## 3. 故事映射、工作单元与行为闭环

当前无待实现工程工作单元。下表把已有实现、Wave 0 基线、剩余最终验收责任连在一起；不创建重复实现子卡。由本地协调者负责集成证据汇总与最终 acceptance，证据记入本计划及 incident ledger。集成以实际 `3390cd2f` 为既成代码基线，按故事做新鲜回归；不重排提交、不伪造 Wave 0 前后的实现顺序。

| 故事/承重步骤 | 实现责任与现状 | 可复核验收及尚缺证据 | 最终集成/验收责任 |
|---|---|---|---|
| **S1：CLI 正文 @ 自动寻址，桌面与 CLI 一致；显式参数仍有效** | `d_cli` 提取完整 token 并合并参数；`d_web` 同一金样本；`d_protocol` 持有既有 mentions 字段；`d_collab` 唯一解析和投递。均已在 `3390cd2f`。 | 已有 CLI/TS 金样本、有效正文 @ 真 SQLite/PG 冒烟和定向 wait 证据；最终新鲜复跑金样本，再以真实 CLI→共享账本→目标 wait 核对 agent、user、卡号与显式参数组合。无效 @、邮件地址里的 @ 不得叫醒。 | 协调者汇总 S1 证据；不派重复实现卡。 |
| **S2：停机积压可补收；换机共用水位且不重交** | `d_ledger` 持久共享 cursor；`d_cli` 一次性 wait 恢复；`d_collab` 过滤唯一目标。Wave 0 已通过。 | 已验基线为 Mac 首次消费→停听期间入账→Linux CLI 重挂补收→Mac 重挂无重复，均在同一 PG；临时 CLI 与配置已清理，Linux agentd 未替换。Wave 0 记录见计划及 ledger。若候选代码/DB 接缝未变，引用该基线；若有变化则重跑异机链。 | Wave 0 已验基线；集成/最终验收负责人核验候选 SHA 与基线一致性。 |
| **S3：多条积压一条摘要，follow 后续逐条输出；断线恢复** | `d_cli` 启动扫描、摘要和 `--follow`；`d_protocol.SessionBacklog` 形状；`d_ledger` 水位。代码已在 Wave 0 前提交。 | 已有同机共享 PG 真 CLI smoke 覆盖单条积压、follow 实时一条、重复挂听无重交；全量 Go 测试和重启 SQLite CLI 测试通过。仍需真机补验多条积压排序/完整性、摘要后继续 follow、重挂摘要不重复；游标/输出失败反例用调用方可见断言验证。 | 协调者对 S3 新鲜回归负责；不造重复实现卡。 |
| **S4：无寻址/无效寻址/结构消息不叫醒；reply 与卡席位语义保持** | `d_collab.MessageWakeTargets`、`ResolveDelivery` 是目标权威；CLI wait 只消费目标；既有席位与 reply 解析不另造。实现已在 `3390cd2f`。 | 已有 focused Go 反例与承重变异记录。最终新鲜运行有效/无效 @、无寻址、结构消息、同房间有效 reply、跨会话 reply、空/现有卡席位矩阵；对负例用真实 CLI listener 超时及 PG 落账读回证明不叫醒/不广播。 | 协调者负责 S4 回归与行为结论；不造重复实现卡。 |

### 行为闭环

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 实现与回归责任 |
|---|---|---|---|---|
| CLI/桌面用户发送正文 @ | 共享账本 `RoomMessage.mentions` / `reply_to` | `collab.Service.MessageWakeTargets` → `session wait` member | 只有有效且逐字匹配的收件人收到 `SessionWake`；正文保留 | S1：`d_cli` + `d_web` 编译、`d_collab` 判定；协调者做发送到等待者的最终验证。 |
| 监听器停机，定向消息入账，随后在任一机器重挂 | 共享 PG 事件流与 `session_delivery_cursors(member,last_seq)` | CLI `runSessionWait` 启动恢复 | 从排他水位补出停机期间未交付的定向命中；另一机器读取同水位，不重复已交付消息 | S2：已验 Wave 0 基线；候选代码变动时协调者重跑异机链。 |
| 有多条启动积压，或 `--follow` 期间有新命中 | cursor + 启动 `MaxSeq` 边界 + 账本消息 | CLI backlog 输出与后续 `SessionWake` 消费循环 | 启动积压按升序聚为一行；follow 接着逐条输出；写 stdout 成功后推进覆盖水位 | S3：`d_cli`/`d_protocol`/`d_ledger` 已实现；协调者补多条与重挂真机验证。 |
| 无寻址、无效目标、结构事件、引用另一会话或空席位 | 消息类型/mentions/reply_to 与当前席位映射；唯一判据在 collab | `MessageWakeTargets` 与 CLI wait | 不广播、不跨房间误唤醒；只有契约规定的当前席位和有效目标可达 | S4：`d_collab` + CLI 消费；协调者完成反例矩阵和真实 wait 验收。 |

### 建议执行顺序（非子卡 DAG）

由于实现已存在，不形成工程子卡 DAG。最终验收按承重路径分批：①核对被测 SHA、fresh suites 与冻结契约；②合并/验收 S1 与 S4 的输入编译及目标过滤反例；③演示 S3 多命中摘要后连续 follow，并回归 S1/S4；④引用或在代码变化时重跑 S2 异机基线；⑤单列桌面假空态/取消轨迹后进行最终故事全集裁决。工程上不允许把「focused tests 全绿」替代每条真机行为事实。

## 4. 缺陷族对抗审查

按 Charter 五个通用族逐项核，另加与本接缝命中的序列化、枚举及承重安全属性：

| 缺陷族 | 相关风险与验收落点 |
|---|---|
| 生命周期 / 状态机中断 | stdout 成功与 cursor 写入之间崩溃允许重复交付；cursor 先行则会漏，契约禁止。重挂/重启须验证只从已落账水位续读。并发多个同 member 主动监听器不保证唯一认领，已明确超出本期。 |
| 静默失败 / 误导报错 | cursor 读取错误不得当作 0，推进失败必须报错/Warn，stdout 失败不推进；PG 断连不得假装超时即正常。`approver.models` 导致配置解析后静默改用 SQLite 是 roadmap 风险，验收须核实实际共享账本，不能在本提案顺手修。 |
| 跨平台假设 | S2 的 Mac/Linux 共用 PG 已真机通过；SQLite 重启测试不替代 PG。摘要输出、空白 token 解析、进程中断按系统支持的平台矩阵复验；未跑的平台不得宣称通过。 |
| 假红 / 假绿测试 | 断言消费方看到的 JSON、seq、水位与真实 listener 行为，不只测内部 helper。Wave 0 有真 PG 异机链和承重变异；S3 多命中和 S4 负例还需独立真机输出证据。fixture 不替代生产 CLI→collab→ledger 链。 |
| 门禁绕过 | 发送仍走既有 `collab.Service.Send` 白名单/书写者门；自动提取只写现有 mentions，不授予新投递权。`--since` 不改持久游标。反例核对无寻址及结构事件不广播，确保所有 wait 入口共用同一目标判据。 |
| 序列化边界 | `RoomMessage.mentions`、CLI 输出 `session_backlog`/`SessionWake`、`hits[].hit/referenced` 均须断言真实 JSON 字段、缺省键与引用形状。CLI 和 TS token 金样本逐例一致，不能只各自单测后推断跨边界一致。 |
| 枚举新值过既有白名单 | `session_backlog` 是 CLI stdout `type`，不是 ledger event kind；确认消费者不会把它送入事件 kind 白名单或误拒绝。既有 `RoomMessage.Kind` 词表不新增值；结构消息保持无 wake。 |
| 承重安全属性 | cursor 单调、按 member 隔离、写出后才推进均有 ledger/CLI 断言；已验异机结果锁了共享 cursor 行为。多 listener 唯一认领不属于本期保证，不能在结果里暗示支持。 |

## 5. 真机清单与图覆盖债

以下是最终 acceptance 的行为清单；完成前按「未验证，需真机」处理。已有证据明确列出，不把部分 smoke 扩大成全故事结论。

1. **S1 未验证，需真机（完整矩阵）**：真实 CLI 对共享 PG 发送有效 `@agent`、`@user`、`@卡号` 和显式 `--mention` 组合；另一独立 CLI listener 观察目标命中。真实 TS 页面使用相同金样本。无效 token 与正文 `email@...` 不生成 mentions、不叫醒。
2. **S2 已验 Wave 0**：现存证据是 Mac 首次消费、停听期间入账、Linux 临时 CLI 同 PG 补收、Mac 重挂无重复。只在候选 SHA、PG seam 或依赖变化时重跑；不可把临时配置测试解释成正式 Linux 配置兼容已解决。
3. **S3 未验证，需真机**：同一 member 产生至少两条积压；一次性 wait 输出一行且 `hits` 升序完整；follow 输出摘要后持续接新 `SessionWake`；新进程重挂不重复摘要。直接验证 stdout/cursor 错误路径的可见失败与可重投。
4. **S4 未验证，需真机**：共享 PG 下以真实 CLI 分别观察无 mentions、无效目标、结构消息、有效 reply、跨会话 reply、已有卡席位与空席位；核对只有有效目标收到唤醒，无成员广播。
5. **相邻 UI 轨迹（独立于 S1–S4）未验证，需真机**：桌面页面进入会话先显示加载态；查询失败显示可行动错误而非“无消息”；恢复后历史消息出现；组件卸载/超时后请求取消，轮询能重试且不遗留旧请求。昨晚故障时段没有请求级历史日志，根因仍未证实。
6. **环境风险核验**：在 Linux 测试前从命令输出和已知会话确认实际连接共享 PG。Roadmap #6 配置回退 SQLite 尚未修复；本轮不得碰正式配置或远端 agentd。

**图覆盖债**：spec 已记录 `runSessionWait`、`sessionSendCmd` 不在 `codegraph/best.json`；incident ledger 另记录新增 `Store.SessionDeliveryCursor` / `AdvanceSessionDeliveryCursor`、`SessionBacklog`、`MessageReference`、`EventsFromAscContext` 等未入图符号。`MessageWakeTargets` 可从图定位。引用现状代码用文件#符号锚；本提案不建议修改 best/target 或新增视图 diff。未命中符号不把代码关系推断成已入图事实。

## 出稿自检

- 四项齐全：顶层触及子系统及逻辑/边界类型、冻结与图边核对、S1–S4 映射及行为闭环、相关缺陷族判断与验收落点。
- 原待拍板分岔集中在稿首并已逐项给出裁决；S2 明确为 Wave 0 已通过，S1/S3/S4 的实现先于 Wave 0 的偏差照实保留。
- 真机清单汇总所有剩余行为事实；已跑的单条 PG smoke 与异机 S2 证据区分陈述。
- 当前无待实现代码，不造重复工程子卡；可圈定的范围、既有契约面与 DAG 逐子系统列明；最终验收证据由本地协调者收口。
- 桌面假空态/请求取消保持独立轨迹；`approver.models` 只列 roadmap 风险。
