# B74 + B75 设计：不许静默降级——假完成与游标失写

> 日期：2026-08-12
> 覆盖 backlog：B74（工具调用被截断时残留标记漏进正文，提问回合被误判为完成）、B75（客户端游标目录硬编码在 $HOME，codex 当审核者时无法授权）
> 交付拆分：一份 spec，三个 plan（B75 / 探针 / B74），见 §7
> **编号已改（08-12 合 main 时）**：本文与三份 plan 全篇沿用 B74 / B75，但合入 main 时发现这两个号已被另外两条需求占用（B74=Web 控制台左栏搜索框、B75=看板卡片橙色标记）。backlog 上本组已改号为 **B78（假完成）/ B79（游标）**。文中保留旧号不做全文替换——改号发生在设计与实现都已完成之后，替换掉会让commit 信息、plan 正文与本文互相对不上；对照关系记在这里即可。

---

## 1. 为什么这两条合一份 spec

它们表面无关——一个在 agentd 侧判回合，一个在客户端侧写文件。合一份是因为**同一个失败模式**：

**handoff 在信息不足时，选择了一个看起来正常的结果，而不是让不足本身可见。**

- B74：模型没有宣布完成，handoff 拿 git 实况替它宣布了。审核者看到的是一条「已完成」，看不到「这条完成是 handoff 推的」。
- B75：游标读不出来（权限被拒、内容损坏）时静默当成「首次」退回 0；写不进去时虽有告警，但只是逐条告警、事件照常交付、游标永不推进——审核者看到的是一串重复告警加一串重复事件，没有任何一条告诉他该怎么办。

两处的修法因此是同一条原则：**信息不足必须显形，且要显形在会被看见的地方。** 不是多打日志——是让结果本身带上「这是推的」或者「这次没写成」。

这条原则在本 spec 里被两节反复引用，不再重述。

---

## 2. 证据

### 2.1 B74 的原始现场（2026-08-12，审核者亲历）

executor 为 opencode。Task 1–4 落得干净；执行者在 Task 5 抛出一个 A/B/C 三选一等审核者裁决，任务却直接进了「完成」。事件流里能看到 `</｜｜DSML｜｜parameter>` 这段工具调用标记漏进了正文，回合被当成终结。

**该现场的日志与事件流已丢失，无法复核。** 因此本设计不依赖对该现场的任何细节还原（见 §3.4 为什么不做词法层清洗）。

### 2.2 代码链路（审核者本地读码核实，未采信执行者自述）

opencode 的回合终结与分类链路：

1. 终结信号是 `session.status` 的 `properties.status.type == "idle"`（`internal/executor/opencode/adapter.go` `mapSessionStatus`），经 `idleGrace`（1500ms）静默去抖后才算数。同时到达的 `session.idle` 与顶层 idle/busy 事件被显式忽略，防重复分类。
2. `mapIdle` 分类：空回合有两条专门分支（被拒终止 → question；零文本 → `result{OK:false}`），非空回合走 `turn.ParseTrailer`。
3. `turn.ParseTrailer`（`internal/executor/turn/protocol.go`）两级提取：**主档只看最后一个非空行**，从该行第一个 `{` 起解一个 JSON 值，解不出立刻 `break` 不向前扫；**兜底档**扫全文取最后一个「trim 后以 `{` 开头」的行。主档只放宽末行是 B48 刻意设的上限，避免正文中间复述协议样例被当成结论（grok 踩过）。
4. 判 `none` → `fallbackClassify`：`hasNew` 为真则 `result{OK:true}`，branch/commit 取 git 实况，summary 取正文尾 200 字；否则转 question。

**关键事实：实时事件流上没有「回合是怎么结束的」这个信息。** `message.updated` 只携带 `info.id` / `info.role`（`mapMessageUpdated` 的结构体只解这两个字段）。而 `SessionMessage` 上确实有 `Finish` / `ErrorName` / `ToolStatus` / `CompletedMS`（`internal/executor/opencode/api.go`，字段注释记着实测取值），`reconcile.go` 用它们跑一张六行判定表——**但这套真值今天只在 agentd 重连对账时用，实时回合终结完全没碰。**

**全仓对截断/畸形工具调用标记零处理。** `antml` / `function_calls` / `invoke` / `sanitize` / 闭合标签模式 grep 均无命中。现有防线只有两类，都不防「工具流残渣落进 text part」：

- 结构性：opencode 的 part 类型闸门（`mapPartUpdated` 先登记类型再过滤，`mapPartDelta` 把未知类型增量暂存待类型揭晓）。
- 词法性：grok 的 `bodyBuf` / `renderBuf` 分流（`internal/executor/grok/adapter.go`），但它防的是推理流复述协议样例。

### 2.3 频率实测（2026-08-12，devbox agentd.log，08-08 起 76384 行）

| 现象（日志原文关键词） | 次数 |
|---|---|
| `回合未输出协议 trailer`（`fallbackClassify` 触发） | **112** |
| `兜底判定无新提交`（→ 转 question） | 53 |
| **差值 = 有新提交 → handoff 替模型宣布完成** | **59** |
| `idle 但回合无文本` | 26 |
| `回合因权限被拒终止` | 10 |
| `本回合已通过 question 工具提问` | **0**（⚠️ 这一格是**日志级别造成的假象**，见下方订正） |

对照：同机 `handoff status` 快照为 completed 79 / failed 52。

**读法与其边界**：112 与 59 是**回合**计数，一个任务可跨多回合触发多次；79 是当前快照而非同期累计。因此**不能**把 59/79 当作「74% 的完成是假的」——两者不同分母。可以断定的是量级：`fallbackClassify` 的宣布完成路径**不是罕见兜底，是常走的主路**（四天 59 次）。

~~**第二个结论**：`本回合已通过 question 工具提问` 为 0，说明 B49 的 `askedViaTool` 去重从未触发，opencode 执行者的提问历史上全部走 trailer、原生 question 工具一次都没被用过。~~ 这直接影响探针 S4 的形状，也意味着「§2.1 那次截断的是 question 工具调用」这个还原**证据不足，本设计不采用**。

**订正（2026-08-12，探针 S4 实测推翻上面划掉的那句）**——原文保留不删，修订痕迹本身是证据：

- **推翻的理由是这条判据本身不成立，不是计数错了。** `本回合已通过 question 工具提问` 这个字符串只出现在 `internal/executor/opencode/adapter.go:1658`，是一条 **`a.log.Debug`**，且只打在「因原生提问已发生而抑制 trailer 提问工单」这一条分支上。agentd 跑在 INFO 级别，这行**永远不会写进日志**。审核者本机 `agentd.log` 里 DEBUG 行数为 **0**（28734 INFO / 1605 WARN / 85 ERROR），devbox 同理。**0 次是日志级别的假象，不是行为证据。**
- **反向事实**：同一次探针 S4 派发抓到的 opencode 原始样本里，原生提问工具**确实触发了**——`"tool":"question"` 出现 2 次、`question.asked` 事件 1 次（样本 `internal/executor/opencode/testdata/probe-s4-opencode.jsonl`，断言见同包 `replay_probe_test.go`：该场景产出的 question 带 `que_` 前缀的原生请求 id，其余三个场景为空）。
- **可用的判据**是原始样本里的事件，不是这条 Debug 日志：opencode 看 `question.asked`，grok 看 `_x.ai/ask_user_question` 请求。
- **未受影响的部分**：本节最后那半句（「§2.1 那次截断的是 question 工具调用」这个还原证据不足、本设计不采用）**继续成立**——它依据的是「§2.1 现场已丢」，与本条判据无关。
- **顺带的教训**：拿「日志里某串出现 0 次」推「该行为从未发生」之前，必须先确认那条日志的级别打不打得出来。这是本 spec 唯一一处被自己的探针推翻的推论。

### 2.4 B75 的现状

- `cursorPath` 硬编码 `$HOME/.handoff/cursor-<taskID>`（`internal/client/client.go`），是唯一的路径计算点。无 flag、无 env、无 XDG。`--config` 只影响配置文件加载，**不影响游标目录**（`cmd/root.go` 的持久 flag 只有 `--agentd` / `--target` / `--config`）。客户端全程只认一个环境变量 `HANDOFF_LOG_LEVEL`。
- `cursorPath` 的注释写明它**故意**不跟 `DataDir` 走，理由是「游标是审核者侧本地状态，必须扛得住 DataDir 搬家」。本设计保留该决策（见 §4.1）。
- `readCursor` 的**全部**失败路径都是 Debug：路径不可用、文件不存在、内容非法一视同仁退回 0。其中「文件不存在」确属正常首次，但**权限被拒**与**内容损坏**被当成同一件事静默吞掉。
- `writeCursor` 失败**已有 Warn**（`WaitEvent`、`FollowEvents`、对账 fast-forward 三处调用点均已告警到 stderr，默认级别 Info 下可见）。问题不是看不见，而是**看见了也没用**：事件照常交付、游标永不推进，审核者收到的是一串重复告警加一串重复事件，没有任何一条指出下一步该做什么。
- `writeCursor` 用 `os.CreateTemp(dir, "cursor-<task>-*.tmp")` + `Rename`，**临时文件创建在游标所在目录里**。这决定了任何重定向必须是**目录粒度**，单文件软链无效。
- 游标文件名只按 taskID，**不带 target/agentd 命名空间**，多机同 ID 会共用一个文件。
- 实测：审核者本机 `~/.handoff/` 下已堆积 **98 个** `cursor-*` 文件，从无回收。
- 审核者本机 codex 配置为 `sandbox_mode = "workspace-write"`，无 `writable_roots` 覆盖。该模式可写 cwd + `$TMPDIR` + `/tmp`，`$HOME` 整体不可写。

**软链方案已排除**：在 `~/.handoff/` 下创建软链本身就是一次该目录的写操作，正是沙箱不给的权限。handoff 在沙箱内无法在 `~/.handoff/` 留下任何东西，软链也不例外。

---

## 3. B74 设计

### 3.1 主决策：`fallbackClassify` 不再宣布完成

`hasNew` 为真时，从 `result{OK:true}` 改为 `result{OK:false}`。

**理由不是 §2.3 的频率，而是翻转几乎不要钱。** `OK:true` 与 `OK:false` 落到**同一个状态**——都经 `handleResult` → `transitToReview` 进 `waiting_review`（`internal/agentd/manager.go`），而 `done` 与 `continue` 在该状态下都合法。所以翻转**不给审核者增加任何一次操作**：动作完全一样，看一眼，`done` 或 `continue`。

变的只有那条事件的类型与文案：从一条「已完成，摘要如下」——它在邀请审核者不看 diff 就 `done`——变成一条「有新提交，但模型未按纪律宣布完成」——它要求审核者看一眼。这正是 §1 那条原则要的性质，代价为零。

`!hasNew` 与 git 查询失败两条分支**维持现状不动**（转 question）：没有提交时正文往往就是模型在提问，把全文交审核者 reply 是对的。

**施加范围是四个 executor 全部，不是 opencode 一处。** 已逐一核实，四处都是「`hasNew` 即宣布完成」的同构逻辑：

| executor | 位置 |
|---|---|
| opencode | `internal/executor/opencode/adapter.go` `fallbackClassify` |
| claudecode | `internal/executor/claudecode/adapter.go` `fallbackClassify` |
| grok | `internal/executor/grok/adapter.go` 的 `if hasNew` 分支 |
| codex | `internal/executor/codex/adapter.go` 的 `if hasNew` 分支 |

只改一处等于换个 executor 假完成照旧。四处的判定语义相同，但**代码不共享**（各 adapter 各写一份，共享的只有 `turn.GitTurnStatus`）。plan 阶段需决定是四处平行改，还是把这段判定上提到 `turn` 包共用——倾向后者：判定规则是协议层的事，四份副本各自漂移正是 B74 这类问题的温床。

### 3.2 git 实况必须保留为结构化字段

`failedPayload` 今天只有 `FailReason` 一个字段（`internal/agentd/manager.go`），而 `completedPayload` 有 `Branch` / `CommitHash` / `Summary`。若不处理，翻转会把 git 实况从结构化字段降级成一段自由文本，审核者与任何下游都无法再结构化地取用。

**修法**：给 `failedPayload` 增加 `Branch` / `CommitHash` 两个 `omitempty` 字段，`executor.Result` 在 `OK:false` 时照常携带它们，`handleResult` 透传。`omitempty` 是必需的——绝大多数 failed 事件（executor 崩溃、看门狗判死）没有 git 实况，不该在 payload 里出现空字段。

`FailReason` 文案要求：必须同时包含 (a) 判定依据「回合结束但未输出协议 trailer」、(b) git 实况 `分支@commit`、(c) 正文尾部片段。三者缺一，审核者就得回去翻日志。

### 3.3 作废工单的理由必须诚实

`handleResult` 的 `!OK` 分支无条件调用 `voidTicketsWithAudit(m.st, taskID, "executor 已终结", m.log)`。该文案来自「executor 已死」的原始场景，但**在本设计新增的场景里 executor 活着**，只是没写 trailer。

这条不诚实**今天就已存在**：零文本回合那条 `FailReason` 明写「executor 仍在线，可 continue 续接重试」，却被同一次调用标记成「executor 已终结」。

**修法**：作废理由改为由 result 侧提供，`handleResult` 透传而非硬编码。executor 真死的路径继续传「executor 已终结」，回合纪律类失败传对应的真实原因。这不改变作废行为本身（回合已结束，挂起工单无论如何都该作废），只让审计记录说真话。

### 3.4 不做词法层清洗

不清洗 `</｜｜DSML｜｜...>` 一类残留标记。两条理由：

1. **无法验证有效性**：§2.1 的现场已丢，无从证明清洗能改变那次的判定。
2. **即使清洗成功也不改变结论**：清掉残留标记后，正文里依然没有协议 trailer，`ParseTrailer` 照样判 `none`，照样进 `fallbackClassify`。清洗只让日志好看，不改变任何判定路径。

### 3.5 证据层：加不加由探针决定，规则现在定

「证据层」指：在 `fallbackClassify` 触发时，去问 executor「最后那条消息到底怎么结束的」（opencode 侧即复用 `api.go` 的 `SessionMessage` 与 `reconcile.go` 已有的判定维度），把「截断 / 工具报错 / 被 abort」写进故障报告文案。

**决策规则**（探针结果回来后直接套用，不需要再讨论）：

| 探针结果（逐 executor 判定） | 处置 |
|---|---|
| 该 executor 在 S3 下的事件层信号**能与 S1 区分** | 给它加证据层，把异常写进 `FailReason` |
| **不能区分** | 不加 |
| **S3 未复现** | 不加 |

「不能区分」与「未复现」都归入不加，理由相同：**缺证据时不写推测性文案，那会制造虚假的确定性**——审核者读到「疑似截断」而系统其实并不知道，比读到「模型未按纪律宣布完成」更糟。这两种情况下，§3.1 的策略翻转就是全部防线，而它本身已经足够安全（不再有假完成）。

证据层是**纯增量**：它只让故障报告更具体，不改变 §3.1 的判定结果。因此它不构成 §3.1 落地的前置。

#### 实测结论（2026-08-12，探针已跑完）

探针记录与全部原始数据见 `docs/superpowers/probes/2026-08-12-turn-end/README.md`（15 行结果表 + 样本入库清单）。按上表规则逐 executor 套用：

| executor | S3 是否复现 | 信号能否与 S1 区分 | 处置（规则套出来的） |
|---|---|---|---|
| **opencode** | 复现：一次超长 `write` 调用在传输层被截断，JSON 解析失败 | **能**。两个取值：`step-finish` 的 `reason:"unknown"`（tokens/cost 全 0）与 tool part 被翻成 `tool:"invalid"`（`state.input.error` = `Invalid input for tool write: JSON parsing failed: ... Unterminated string`）。两者在 S1/S2/S4 三份基线样本里出现次数均为 **0**，已由 `internal/executor/opencode/replay_probe_test.go` 的反向断言固定 | **加证据层** |
| **grok** | **未复现**（2 次尝试）：模型主动绕开超长调用，全程最大帧 101 KB 且是 `available_commands_update` 样板帧；bash 结果里的 `truncated` 字段全为 `false` | 不适用 | **不加** |
| **codex** | **未复现，且原因不同——它没被截断**：20000 行 / 1.88 MB 的写入一次调用完整落盘，单帧最大 700 KB，收尾 `turn/completed` `status:"completed"`、`error:null` | 不适用 | **不加** |
| **claudecode** | **未测：环境阻塞**。本机 OAuth 过期（`Failed to authenticate: OAuth session expired and could not be refreshed`），`assistant` 帧 model=`<synthetic>`、输出 token 全 0，**模型根本没运行**，重试 1 次结果相同 | 未测 | **待补测后再定**。注意：这一格**不是**「未复现」——按本节规则「未复现」会直接推出「不加」，而这一格没有任何行为证据可供推理，把它记成未复现就是用环境故障冒充实测结论 |

**一句话**：只有 opencode 一家在事件层留下了可判别于 S1 的截断信号，因此**证据层只在 opencode 落地**，grok / codex 保持 §3.1 的策略翻转作为唯一防线（本节已说明这本身就足够安全），claudecode 待鉴权恢复后补测。

**顺带得到、比 S3 更普遍的一条**：S2（不改不提交）在四家上全部被判成 `question`，而其事件层与 S1 **逐帧同形**——判定差异完全来自 git 状态而非事件流。这从正面印证了 §3.1 的判断：trailer 缺失时事件层给不出任何帮助，兜底只能靠仓库副作用，所以「有新提交」绝不足以支撑「宣布完成」。

---

## 4. B75 设计

### 4.1 布局：`<游标根>/cursors/<agentd>/<taskID>`

`<agentd>` 由 **agentd 地址**推出（`baseURL` 的 host:port，`:` 与其它路径不安全字符折成 `_`，如 `100.73.238.21_7777`、`127.0.0.1_7777`）。

**为什么用地址而不是 `--target` 名字**（本条在写 plan 时订正，原设计写的是 target 名）：

1. **地址是身份，名字只是本机别名。** 两个 target 名指向同一台 agentd 时，按名字分篓会把同一批任务的游标分裂成两份；改个名字则让已有游标全部失联。按地址分篓两种情形都不出问题。这与 `resolveProject` 里「projectID 是身份、名字只是引用」的既有判断同源。
2. **零签名改动。** `Client` 只持有 `baseURL`，不知道 target 名；`client.New(addr, token)` 有十余个调用点，塞 target 名要逐个改。地址已经在手里。

代价：目录名不如 target 名好读，且 agentd 换端口会让旧游标失联。后者可接受——换端口本就是换了一个 agentd 实例，游标重来一次的成本是重放一次事件。

一步做掉三件事：命名空间隔离（多机同 ID 不再串）、给出一个可被整体处置的目录、让「清掉某台机器的全部游标」变成删一个目录。

保留 `cursorPath` 原注释的决策：**游标根不跟 `DataDir` 走**，理由（审核者侧本地状态须扛得住 DataDir 搬家）依然成立。本设计只是让这个根可降级。

### 4.2 游标根：三级确定性降级

1. `~/.handoff/` —— 缺省，与今天一致。
2. 不可写 → `<cwd>/.handoff/` —— **向 stderr 打一行**：`游标目录 ~/.handoff 不可写，已改用 <path>`。一行，不是 Debug 日志：审核者必须知道游标换了地方。
3. 两处都不可写 → **响亮失败**：退出并说明两个路径都试过、各自的错误是什么。**不再静默退回游标 0。**

**探测方式是真写一次**（在候选目录创建并删除一个探测文件），不是查权限位——沙箱的拒绝不体现在 mode 上。

**为什么 `<cwd>` 是对的降级目标**：codex `workspace-write` 下可写的是 cwd + `$TMPDIR` + `/tmp`（§2.4）。cwd 即审核者的项目目录，**同一项目跨 session 稳定**，所以游标能续上；`$TMPDIR` 会被清理，续不上等于没修。选 cwd 意味着零 per-session 配置——这是本条需求的核心诉求（审核者原话：codex 的沙箱每次不一样，不可能每次手工配）。

**不做的事**：不加 `--cursor-dir` / `HANDOFF_CURSOR_DIR`（自动降级已覆盖诉求，多一条优先级链就多一个可错面：审核者忘带 flag 时会静默从游标 0 重放）；不做软链（§2.4 已排除）；不动 `--config` 与 DataDir 的关系。

### 4.3 拆除静默降级

这是 B75 真正咬人的地方，不是路径本身。

- **读失败分两类**：文件不存在 = 正常首次，退 0 且不报（维持现状）；其它错误（权限被拒、内容损坏）**必须报到 stderr**，因为它们意味着「游标存在但用不了」，而后果是静默重放旧事件。这是读侧唯一真正静默的地方。
- **写失败**：按 §4.2 三级降级。写侧本来就有 Warn（§2.4），所以这里要修的不是音量而是**可操作性**——降级让写入真正成功；若三级都不成立则响亮失败并说清两个路径各自的错误，而不是继续逐条告警、让审核者面对一串无从下手的重复。
- **降级只判定一次**：游标根在客户端首次用到时解析一次并缓存，不每次写都重探。否则每条事件都要多两次文件系统探测，且那行降级提示会被打印 N 次——把一条有用的信息淹成噪音，正是本节要消灭的形态。

### 4.4 回收

- **任务终结即删**：客户端观察到 `archived` 事件时删掉该任务的游标文件（`Manager.Done` 归档时发该事件）；`handoff done` 返回成功后再删一次兜底。两条通道幂等（删不存在的文件不是错误）。
- **TTL 清扫**：`mtime` 超过 30 天的游标文件顺带清掉，沿用 `sweepStaleCursorTemps` 已有的「顺手扫、失败只 Debug」形状。这条负责收拾所有「任务没走 `done` 就没人管了」的漏网。TTL 清扫失败只打 Debug 是**允许的例外**：它是尽力而为的卫生工作，失败不影响任何正确性。
- **旧布局一次性清除**：新布局上线时，把 `~/.handoff/` 下平铺的 `cursor-*` 文件（含 `.tmp`）一次性删掉。**不做迁移**——保住的东西是「已归档任务的游标」，本来就该删；代价是极少数仍在 `waiting_review` 的老任务下次 `wait` 会重放一次历史事件。

---

## 5. 探针设计

### 5.1 目的

**只回答一个问题：S3（截断）在事件层有没有可判别的信号。** §3.1 的主决策不依赖探针，§3.5 的证据层依赖它。

### 5.2 矩阵

四个真实 executor（opencode / claudecode / grok / codex；`fake` 不算），四个场景，**串行**：

| | 诱发方式（写进派发给 executor 的 plan 正文） | 量什么 |
|---|---|---|
| S1 | 改一个文件、`git add` 并 commit，然后用自然语言说一句「做完了」，禁止输出任何 JSON | 现行 policy 判成什么；这是 §2.3 那 59 次的可控复刻 |
| S2 | 什么都不改、不提交，用自然语言描述打算怎么做，然后结束回合，禁止 JSON | 现行 policy 判成什么 |
| S3 | 发一个参数极长的工具调用（如提问正文重复某句话至数万字），求撞输出上限使其被截断 | **事件层有没有区别于 S1 的信号** |
| S4 | 走原生提问通道提问 | 原生通道与 trailer 判定是否一致 |

**claudecode 无原生提问通道**（grep 无 `askedViaTool`，`internal/executor/claudecode/` 下无 question 翻译），S4 对它不适用。**合计 15 次派发，不是 16。**

每次派发量三样：(a) 事件层能拿到哪些信号（原始字节归档）、(b) handoff 当前判成了什么（question / result OK / result !OK）、(c) 任务落到哪个状态。

### 5.3 硬约束

- **串行，绝不并行**。B73 的整机 fork 瘫痪就是并行 executor 顶穿 `kern.maxprocperuid`。每次派发前查 devbox 进程余量，不足则停。
- **跑在专用沙箱仓库**，每次 `--new-branch --new-worktree`，不碰任何真实项目。
- **每场景归档原始字节流**，入库为 `internal/executor/<x>/testdata/<scenario>-<executor>.jsonl` + 回放测试。这是既有规矩：`replay_spike_test.go` 的头注释写死了理由——样本留在本机等于结论无法从任何 clone 复核，上游一改协议没有任何东西会变红。
- **S3 诱不出来是允许的结果**，如实记「未复现」，不编。「未复现」在 §3.5 的规则里有明确后果（不加证据层），不是无信息。
- 探针**只观测不修改判定逻辑**。探针期间跑的是当前生产代码路径。

---

## 6. 测试策略

**B74**

- 兜底判定的三条分支（`hasNew` / `!hasNew` / git 出错）各一条单元测试，断言事件类型与 payload 字段。`hasNew` 那条断言 `OK:false` 且 branch/commit 出现在结构化字段里。**四个 executor 各覆盖一遍**——若判定上提到 `turn` 包共用，则共用逻辑一份测试 + 四处各一条接线测试。
- `handleResult` 的 `!OK` 路径断言 `failedPayload` 携带 branch/commit，以及作废理由来自 result 而非硬编码。
- **变异锚**（本仓库既有做法）：把兜底判定改回 `OK:true`，上述测试必须变红；还原后变绿。**四处逐一变异**，确保没有哪一处是测试覆盖不到的。审核者本地独立复现，不采信执行者自述。
- 探针产出的 `testdata/*.jsonl` 回放测试。

**B75**

- 三级降级各一条：可写 → 用规范位置；规范位置不可写 → 用 cwd 且 stderr 有那一行；两处都不可写 → 返回错误且错误里含两个路径。
- 读失败分类两条：不存在 → 退 0 不报；权限错误 → 报 stderr。
- 命名空间：两个不同 agentd 地址的同 taskID 游标互不干扰；同一地址的不同写法（含/不含 scheme）折算到同一个篓。
- 回收三条：`archived` 事件后文件消失；`done` 兜底幂等（连删两次不报错）；TTL 扫掉超期文件、不碰未超期的。
- 测试改 `HOME` 与 cwd 到临时目录（现有 `backlog_internal_test.go` 已是这个做法），不污染真实环境。

**共同**：`go build ./...` + `go vet ./...` + `gofmt -l .`（无输出）+ `go test ./... -count=1` 全绿；`go test -race` 覆盖 `./internal/agentd/ ./internal/executor/opencode/ ./internal/client/ ./cmd/`；`GOOS=windows GOARCH=amd64 go build ./...` 通过。

---

## 7. 交付拆分

```
                          ┌─ B75 游标（不依赖探针）────────────→ plan B75 ──→ 实现
本 spec ──────────────────┤
                          └─ 探针（15 次派发）─→ plan 探针 ─→ 跑 ─→ 实测判定表
                                                                    │
                                                    （回填 §3.5）────┘
                                                                    ↓
                                                               plan B74 ──→ 实现
```

- **plan B75** 与 **plan 探针** 可并行推进：零文件冲突（前者动 `internal/client/`，后者只读 + 增 `testdata/`）。
- **plan B74** 的 §3.1/§3.2/§3.3 三项不依赖探针，可与上述并行；§3.5 的证据层等探针结果按规则套用。若探针结论为「不加」，plan B74 就只有前三项。
- 每个 plan 的实现类 task 必须包含「加关键节点日志」与「加注释」两个 step（`instrumenting-code` 的硬要求）。

---

## 8. 验收标准

| # | 标准 | 判据 |
|---|---|---|
| 1 | 无 trailer 的回合不再产生 completed 事件，**四个 executor 全覆盖** | 单元测试 + 四处变异锚各自变红/变绿 |
| 2 | git 实况在 failed 事件里仍是结构化字段 | `failedPayload` 断言 |
| 3 | 作废工单的审计理由与实际情形一致 | `handleResult` 测试 |
| 4 | 游标在 `$HOME` 不可写时仍能持久化，且审核者被告知 | 三级降级测试 + stderr 断言 |
| 5 | 游标写/读失败不再静默 | 读失败分类测试 |
| 6 | 多 agentd 同 taskID 不再串游标 | 命名空间测试 |
| 7 | 任务归档后游标被回收 | 回收三条测试 |
| 8 | 探针 15 次派发全部有归档样本，S3 结论明确（复现 / 未复现） | 探针 plan 的产出物清单 |
| 9 | 真机验收：codex 沙箱内 `handoff wait` 游标能推进，不再重复吐旧事件 | 审核者在 codex 沙箱里实跑，非执行者自述 |

---

## 9. 风险与已知边界

- **§3.1 的翻转会让历史上那 59 次/四天的路径全部变成 failed 事件。** 这是设计意图，不是回归。若审核者体感上被打扰，正确的应对是让 executor 守纪律（补 prompt），而不是把假完成放回来。
- **探针的 S3 依赖供应商行为**，不可控。未复现是可接受结局，但意味着「截断在事件层长什么样」这个空白继续留着——应回填进 backlog 的「待验证的空白」一节。
- **§4.2 的 cwd 降级把游标绑定到项目目录**。审核者在两个不同目录里 `wait` 同一个任务时，会各自持有一份游标。这是可接受的：两处都能推进，最坏结果是各自重放一次。
- 本 spec 不处理 `ParseTrailer` 本身的宽容度（主档只看末行、兜底档只认 `{` 开头）。§3.1 落地后，`ParseTrailer` 判错的后果从「假完成」降级为「多一次人工确认」，不再值得为它冒扩大误吞面的风险。
