# B271 spec 审查（本机卡派发 / loopback 镜像 EOF）

审查对象：`docs/superpowers/specs/b271.md`（状态：待独立审查）  
对照台账：`docs/superpowers/specs/b271-ledger.md`  
对照 OOS：`docs/roadmap.md` 文末「来自 B271 spec」三条  
对照代码：工作树 `/Users/sycm/.handoff/worktrees/b271-loopback` 分支 `fix/b271-local-dispatch`（与 `main` @ `d319f92d2` 同内容，外加未提交 spec）  
源卡：B268  
审查者：独立 subagent（charter spec 审查，只读，不改 spec/代码）  
日期：2026-08-28

## 1. 总判

**修订后再批。**

方向对：本机身份应收成空串、两条镜像不得把本进程当远端、自动批的门与自订掐 WS 必须拆开修——这三件与代码和 B268 现场对齐。不能批的原因是两处按正文落地会做错题：① WorkBranch 现网不是「字符串不等即拒」，空串短路会让故事 3 的主路径（连续两次不带 `--target`）继续假跨机，且与 B192 冻结语义对撞；② 账本本机源「订本机 Hub」会让内部订阅者计入 `Watchers`，B56 无人值守判据虚高漏报。最小补丁见文末。

## 2. Findings

### Critical

#### C1. WorkBranch 空串短路被读成「字符串不等」；B192 冻结「空 = 不能证明同机」，本卡把它翻成「空 = 本机」却没改门、没锁 `""` vs `""`

- **位置**：spec 现状 `b271.md:65`、方案 §6 `b271.md:118-120`、故事 3 `b271.md:139`、接缝 2 `b271.md:167`；活代码 `internal/ledgerstep/dispatch.go:159-163`；冻结 `docs/superpowers/specs/2026-08-23-b192-contract.md:54-58,123` 与 `docs/superpowers/specs/2026-08-23-b192-breakdown.md:148-150`
- **事实**：跨机门不是「字符串不等即拒」，而是

  ```
  hasWorkBranch && (workInfo.Target == "" || workInfo.Target != target)
  ```

  上次目标机为空**无条件拒**，包括两边都是 `""`。B192 §2.1.3 明文：「历史快照 `Target == ""` 时不能证明同机」；矩阵「有工作分支但上次目标机不同（或目标机信息缺失）→ 不生成任务」。ViaTemplate 今天在空 target 处先失败（`dispatch.go:137-139`），所以成功快照通常写的是登记名；本卡方案 §1 要把挂账/快照归一成空串。归一之后下一节点不带 `--target` 就是 `""` vs `""`，现网第一条短路仍拒，文案会是「上次目标机 `""`，本次目标机 `""`」——故事 3 写的却是 `local` vs `""` 那种假跨机。接缝 2 只要求「`local` 与 `""` 不触发跨机拒发」，没有 `""` vs `""`。
- **为什么承重**：这是本卡产品名落地后的默认主路径（两次都不带 `--target`）。按现状读数去改「字符串比较」而保留空串短路，故事 3 红。B192 是已冻结契约，本卡把它的空串语义反转，必须显式废止/改写，不能靠「字段语义收口」一笔带过。
- **建议**：① 现状改成逐字引用 `dispatch.go:159` 的析取，不要写「字符串不等」；② 方案 §6 写死：新身份下 `""`、自机登记名互相等价，**去掉** `workInfo.Target == ""` 短路；一边自机一边真远端仍走今天跨机原文；③ 接缝 2 加 `""` vs `""` 不拒、Transport 被调用；④ 回写 B192 §2.1.3 / 矩阵「目标机信息缺失」行——历史缺字段快照怎么处置必须另写（见 I5），不得假装 B192 没冻过。

#### C2. 账本本机源「订本机 Hub」会变成内部订阅者，`Watchers` 虚高，无人值守漏报

- **位置**：spec 方案 §4 `b271.md:106`、实现决定 `b271.md:148`、接缝 3 `b271.md:169-174`；活代码 `internal/agentd/hub.go:51-62,109-111,166-177`；`internal/agentd/hub_test.go:190-194`；生产消费 `internal/agentd/status.go:142`、`internal/agentd/server.go:858,922`、`cmd/status.go:184,329-331`
- **事实**：Hub 包头与 `Watchers` 注释写明——全仓 `Subscribe` 只有 `/ws/events` 一个调用点，「没有任何内部订阅者混在里面。若将来新增了内部订阅者，这条结论就不再成立，必须同步修改本注释与 status 的判据。」`TestWatchersCountsSubscribers` 把「多算一个就是漏报」钉死。本机源按正文是 store 开区间重放 **+ 订本机 Hub**。实现成 `Hub.Subscribe` 后，凡挂在本机、被账本镜像盯着的 task，`Watchers` 恒 ≥ 1；节点 `WaitEvent` 断开之后仍 ≥ 1。`handoff status` 的「⚠ 无人值守」对这批本机卡派发任务漏报。接缝 3 只禁本机源再 `Publish`、用假 Hub 计数，不碰 `Watchers`。
- **为什么承重**：B56 的判据是「有几个协调者在听」，不是「有没有内部镜子」。按字面订 Hub，本卡修好 EOF 的同时把无人值守灯弄坏。Hub `Publish` 还对满缓冲走 `select-default` 丢事件（缓冲 16，`hub.go:21-25,171-175`），内部订阅者若同步写账本，还会在 Watchers 问题之外再丢 live 事件（见 I3）。
- **建议**：正文必须三选一写死，并加接缝：① Hub 增加不计入 `Watchers` 的内部订法，本机源只用它；② 本机源不订 Hub，按 store 水位轮询/等落库门铃，live 仍不回 Publish；③ 改 `Watchers` 过滤内部订阅并改 B56 测试。禁止「订 Hub」四个字不提 Watchers。接缝 3 加：本机源在飞时 `Watchers` 仍为 0（无真实 WS 协调者）。

### Important

#### I1. 节点运输/等待的本机客户端不在接缝清单里；只放行 ViaTemplate 仍会在 `pool.For("")` 上挂死

- **位置**：spec 方案 §5 `b271.md:110-116`、接缝清单 `b271.md:157-188`；`internal/agentd/cardstep.go:71-72,136-141,178-189`；`internal/ledgerstep/runner.go:381-431`；`internal/targetclient/pool.go:99-105`
- **事实**：`--step` 装配 `Clients: s.pool.For`，`awaitNode` / `diffNode` 用 ViaTemplate 回传的 `result.Target` 去 `Clients(target)`。空串归一后 `pool.For("")` 报「target 未在配置中登记」。`stepTransport` 同样 `pool.For(opts.Target)`。接缝 1 只锁 ViaTemplate 放行；接缝 5 锁 `resolveStepDiscipline` / CLI `targetClient`，**没有** `stepTransport` / `awaitNode` / `diffNode`。`resolveStepDiscipline` 今天空 target 跳过探活（`cardstep.go:136-141`），改成探本机后也要有本机客户端，不能 `pool.For("")`。
- **为什么承重**：故事 1 的 charter 主路径是 `--step`。挂账成功但节点等回合在 `Clients("")` 失败，卡停在 dispatched、`waitForTurnEnd` 走不到——形态接近 B268，只是 EOF 换成「未登记」。
- **建议**：接缝 5 扩成「空/自机 → 本机客户端」的生产调用方：`stepTransport`、`awaitNode`、`diffNode`、`resolveStepDiscipline`、CLI `targetClient` 各至少一支。禁止 `pool.For("local")` 当本机。

#### I2. 自机判定接缝没锁 B268 生产地址形态：`addr=http://127.0.0.1:7777` vs listen 无 scheme

- **位置**：spec 方案 §2 `b271.md:90-92`、接缝 2 `b271.md:163-167`；台账 `b271-ledger.md:7`；`internal/config/config.go:234-235`；`internal/config/listenclass.go:40-63`；`cmd/root.go:126-135`；`internal/targetclient/targetclient.go:53-54`
- **事实**：现场登记是 `targets.local.addr=http://127.0.0.1:7777`。`Listen` 一般是 `127.0.0.1:7777` / `0.0.0.0:7777`（无 scheme）。`ClassifyListen` 走 `SplitHostPort`，带 `http://` 会解析失败并归 loopback、原样返回。正文写「host:port 等于 listen 本身或 loopback 变体」，接缝例子却是无 scheme 的 `127.0.0.1:同端口` 与「等于 listen 本身」。字面字符串比会把这台机器判成非自机，任务镜像继续订 `local`，B268 原样还在。
- **为什么承重**：自机判定若在生产形态上假阴，本卡其余修法全落空。
- **建议**：写明 Addr 去 scheme 再取 host:port；listen 的 loopback 变体同样去 scheme 后比。接缝 2 夹具用 `http://127.0.0.1:<port>`（及 `localhost`、通配 listen）对无 scheme 的 listen → 自机。

#### I3. 本机源与 WaitEvent / 任务镜像抢的不是「同一条订阅」，而是 Hub 的慢订阅丢弃契约；正文没写 drain / 溢出 / 重订

- **位置**：spec `b271.md:106,148`；`internal/agentd/hub.go:7-9,21-25,61-62,171-175`；WS 侧对照 `internal/agentd/server.go:2079-2181`（排空器 + live 缓冲 + 越限断开重连）
- **事实**：Hub 只做实时扇出，不保证送达；满 16 槽丢事件打 Warn。`/ws/events` 另有 drain + 越限断开，靠 store 重放补。本机源若 `range` Hub 通道并同步 `AppendMirroredEvent`，progress 等仍会进通道（过滤在回调里），一阵 burst 就把 completed 丢掉；丢了没有溢出信号，也不会自动从 watermark 重订。`StreamEventsOnce` 注释（`internal/client/client.go:1566-1570`）刻意不碰磁盘 cursor，正是怕和 `handoff wait` 抢水位——本机源走 Hub 不抢 cursor，但抢缓冲。
- **为什么承重**：修掉二次 Publish 之后，卡流缺终态会变成偶发、难归因。接缝 3 的「假 Hub 计数」测不出丢。
- **建议**：写死本机源必须及时抽干（含 `mirrorSkip` 的类型）、溢出则按 store 水位重订；或改成不订 Hub。接缝 3 加：连续 >16 条 progress 之后一条 `permission_request` 仍要落到卡上。

#### I4. 账本 `Source` 现签名依赖 `machines.For`；空 target 放行后若仍 `For("")` 会在本机源之前失败

- **位置**：spec `b271.md:108`；`internal/ledgermirror/mirror.go:35-36,218-255,356-381`
- **事实**：`reconcile` 先 `registered[link.Target]`，再 `machines.For(link.Target)` 才 `subscribe`。`Source` 的第一个参数是 `*client.Client`。空串不在 `Names()` 里；自机登记名 For 会给出 loopback 客户端——本机源若仍走这条，等于又订了自己的 WS。正文说空串放到本机源、自机不走 WS，但没说 subscribe 在 For 之前分流、本机源允许 `c == nil`。
- **为什么承重**：实现按现签名「先 For 再 Source」会要么对 `""` 直接 continue，要么对 `local` 继续 DefaultSource。
- **建议**：写明：自机/空 target **不得** `For`、不得 `DefaultSource`；本机源在组装点注入，不拿 target 客户端。接缝 2「账本对自机挂账走本机源不走 WS」必须打到这条分流，不能只 mock Source 计数。

#### I5. 历史「缺 Target 字段」与新「空串 = 本机」会撞车，正文没切开

- **位置**：B192 `2026-08-23-b192-contract.md:48-58`；`internal/ledger/events.go:137-141,439-440`；spec 方案 §1 `b271.md:78-79`
- **事实**：B192 把 `WorkBranchInfo.Target` 空值定义为「历史快照没有目标机信息」。JSON 缺键解成 `""`。B192 之后 ViaTemplate 成功路径不会写空 target（先被 137-139 拒）。本卡开始把新快照的 `""` 当本机。若不切开，B192 之前的缺字段快照会在下一节点被当成「就在这台机器」，跨机产出被静默接续——正是 B192 规则三要禁的。
- **为什么承重**：空串一身二职。窗口可能不宽（B192 是 2026-08-23），但冻结语义还在，审查节点会抓「未回写」。
- **建议**：写死历史缺字段是否仍拒（保持 B192）还是一律视作本机（接受误接续）。不要只说「空串是产品名」。

### Minor

#### M1. 任务镜像二次 Publish 的因果链成立，但「30s 发现 tick 解释日志节奏」略混

- **位置**：spec `b271.md:55-58`；`internal/agentd/mirror.go:33,238-253,274-287`；`cmd/agentd.go:240`（`NewMirror(..., srv.Hub(), ...)`）
- **事实**：任务镜像与 `/ws/events` 共用同一 Hub。`onEvent` 在 `AppendMirrorEvent` 成功后 `hub.Publish(ev)`，`ev.Seq`/`TaskID` 是上游那条（JSON 拷贝，序号相同）。`writeLiveBatch`（`server.go:2090-2098`）对 `maxReplayed < seq <= lastWrittenSeq` 记乱序迟到并断开。自订会把**已经**由 manager 发布过的 seq 再送一遍，所有订该 task 的 WS 被掐。EOF 日志来自 `subscribe` 的断线分支（`mirror.go:252-253`），节奏跟事件到达有关；`mirrorDiscoveryTick=30s` 解释的是发现循环，不是每次 EOF。
- **建议**：把「日志键 `machine=` + 自订二次 Publish」与「tick 只解释发现周期」分开写。不挡批准。

#### M2. `charter-default` 空 target 仓内无种子，采信台账

- **位置**：spec `b271.md:39`；台账 `b271-ledger.md:6`；类型 `internal/ledger/templates.go:18`
- **事实**：审查未跑 `handoff template show`（只读、无该命令输出）。仓内 `deploy/workflows/charter-v4.json` 只引模板名。与 `TemplateDef.Target` 可空、ViaTemplate 把空当错误，逻辑自洽。
- **建议**：无。

#### M3. skill 里已有一句「目标机未定」指的是 CLI/agentd 版本 skew，不是空 target

- **位置**：`skills/handoff/SKILL.md:441-445`；spec `b271.md:150`
- **事实**：现文案：「CLI 与 agentd 必须同批升级（新 CLI 的覆盖项旧 agentd 不认，每次派发都以「目标机未定」失败）。」本卡要加的是「不要 `--target`、不要登记 loopback」。两句并置时，「目标机未定」会像同一病因。
- **建议**：新句不要复用「目标机未定」当空 target 的别名；版本 skew 那句留着。

#### M4. `ModelByTarget[target]` 在 target 归一成空串后改查 `""` 键

- **位置**：`internal/ledgerstep/dispatch.go:233-235`
- **事实**：有效 target 变空后，按名覆盖的模型表对不上 `local`。本卡范围外，几乎无生产键。
- **建议**：plan 里顺手点一句即可。

## 3. 定级：L2 成立，不抬 L3

独立判断与 spec 同结论，理由不同。

- **不新开 HTTP 路径、不改任务/工单状态机、不改 `from_seq` 开区间、不改 `mirrorSkip`**：与代码对照成立。`CardStepReq.Target` 空串已经在线上（`--step` 主路径 202 受理，失败在异步 ViaTemplate）。
- **跨顶层领域但不是新契约面**：任务镜像在 `k_agentd_Mirror → d_workspace`（`codegraph/best.json:134`），账本镜像在 `d_ledger`，CLI 在 `d_cli`，Hub 在 `d_orchestration`。改的是同一条身份规则的调用方，值得抽一份纯函数，不值得为这条缝付 L3 冻结成本（架构法第一条实操裁决）。
- **B192 是包内已冻语义，不是跨进程 wire**：`WorkBranchInfo` 不进 HTTP/JSON（B192 contract §4 末）。把它的空串含义从「缺失」改成「本机」是 **d_ledger 内冻结物回写**，必须做（C1），但不是「动跨子系统契约 → L3」。
- **不是 L1**：自机判定、本机源、Watchers、WorkBranch 等价，plan 不会只复述三行。

不因「空 Target 从拒发变本机」抬 L3。必须在 spec 里承认这是对 B192 空串语义的改写，而不是「不是新契约面」一句话打发。

## 4. 接缝清单：假缝不多，漏缝三处

| 缝 | 符号 + 调用方 | 判定 |
|---|---|---|
| 1 ViaTemplate | `Dispatcher.ViaTemplate` ← `StepRunner.dispatchNode` / 裸模板派发 | 真缝。空串放行是产品行为。 |
| 2 自机判定 | 纯函数内部锁 + 任务镜像发现 / 账本 reconcile / WorkBranch | 假缝禁令写对了。生产调用方真。**漏 `""` vs `""`（C1）、漏生产 Addr 带 scheme（I2）。** |
| 3 账本本机源 | ← `Mirror.subscribe`（账本） | 真缝，且不能用「跳过自机」负例冒充——写对了。**漏 Watchers（C2）、漏 burst 不丢（I3）、漏不得 For/WS（I4）。** |
| 4 任务镜像发现 | ← `Mirror.discoverOnce` | 真缝。`pool.Names()` 仍含 `local`、发现跳过自机，与升级/探活不冲突。 |
| 5 `resolveStepDiscipline` / `targetClient` | ← `--step` 与裸 dispatch | 真缝，但**覆盖不足（I1）**：`--step` 的活路径还经过 `stepTransport` / `awaitNode`，二者今天 `pool.For`。 |
| 6 二次 Publish 仍掐 WS | 回归锁 | 真缝（边界型：乱序迟到协议）。「不得为了让自订能用而删断开」写对了。 |

假缝：`from_seq` 开闭、`resume --force`、grok 是否发 `completed` 明确排除，合格。

自机判定单独导出且无生产调用方——正文已禁，合格。

## 5. 弃选：站得住

| 弃选 | 审查意见 |
|---|---|
| 改 `from_seq` 闭区间 | 站得住。开区间是不丢不重；闭区间会让远端镜像重放重复。根因是不该自订。 |
| 只删配置里的 `local` | 站得住。空 target 仍拒，下一轮再登记。 |
| `--target 本机` 魔法别名 | 站得住。身份是空串 + 地址，未登记继续拒。 |
| 自订但 `onEvent` 不再 Publish | 站得住。仍占一条自己到自己的 WS；本机任务不需要 `mirror_tasks` 副本。 |
| store 钩子兼写账本 | 站得住。钩子已用于 `frames.jsonl`（`internal/store/store.go:773-780`），同步打账本是跨库。 |
| 节点等待改成纯进程内 WaitEvent | 站得住（在 C2/I3 写清 Hub 契约的前提下）。B161 已钉 WS 不走代理；`client.New` 的 Transport `Proxy: nil`（`internal/client/client.go:257-267`）。自订拿掉后本机 `/ws/events` 与 `handoff wait` 同路。另造 WaitEvent 必漂移。 |
| `resume --force` 补发 `completed` | 站得住。见下节对齐。 |
| 自动放行出现在 `card wait` | 站得住。`approver_decision` 在 `mirrorSkip`（`ledgermirror/mirror.go:84-88`）；approve 路径根本不发 `permission_request`（`internal/agentd/approver_test.go:281-284`，`manager.go:2082-2084`）。 |

## 6. 与 B268 现场对齐；两件事没有混成一个修法

源卡现场（台账 + spec 问题陈述）逐条：

| 现场 | 代码 | 本卡修法 |
|---|---|---|
| 空 target 拒（「目标机未定」/「未指定目标机」） | `dispatch.go:137-139`；`cmd/card_dispatch.go:156-158` | 空 = 本机。对齐。 |
| 登记 `local=http://127.0.0.1:7777` 后被当远端 | `pool.Names()` 列出它；两条镜像都按名订 | 自机判定 + 发现跳过。对齐（接缝须锁带 scheme 的 Addr，I2）。 |
| `machine=local` EOF | 任务镜像日志键是 `machine=`（`mirror.go:188,252`）；账本镜像是 `target=`（`ledgermirror/mirror.go:292,414`） | 任务镜像不订自机。对齐。 |
| 卡流停在 dispatched，任务流继续 | 空挂账因 `!registered[""]` 根本不订（`ledgermirror/mirror.go:236-238`）；订了 `local` 又被二次 Publish 掐 | 放行不够，要本机源。对齐。 |
| 权限门与 EOF 同一毫秒 | 需 Publish 的事件（升级人工的 `permission_request`，或其它 live 事件）进 Hub → 自订再 Publish 同 seq → `writeLiveBatch` 断开。自动批路径 **不** Publish `approver_decision`（`manager.go:2082-2084`），也 **不** 发 `permission_request` | 见下。 |
| 自动批是 `approver_decision` | `mirrorSkip` 含它；approve 测试断言没有 `permission_request` | **不改 `mirrorSkip`。** 标题里的「收不到权限门」自动批部分不是 bug。拆开了。 |
| `resume --force` 不发 `completed` | `forceToReview`（`manager.go:2909-2924`）只 `appendProgress` + `recoverTransit` 到 `waiting_review`，无 `EventTypeCompleted` | OOS。对齐。 |
| grok 终稿不发 `completed` | 执行器卡 | OOS。对齐。 |

**没有混成一个修法。** 正文 `b271.md:214` 与故事 6 把「自动批的门本来就不进卡流」和「自订掐 WS」拆开：前者保持 skip，后者靠不订自机 + 本机源。故事 1 把 `permission_request` 限定为「未自动批时」，合格。

`--step` 本来就拨本机 agentd：`cmd/card_node.go:32-50` `LocalEndpoint()`，`--target` 只是请求覆盖。空 target 202 受理、异步 ViaTemplate 再失败——现状读数成立。

## 7. 指定核对题（独立确认 / 证伪）

1. **二次 Publish 是否同一条 Event（同一 seq）到同一 Hub？**  
   **确认。** 生产 `cmd/agentd.go:240`：`NewMirror(..., srv.Hub(), ...)`。manager 落库后 `Publish`；镜像 `onEvent` 再 `m.hub.Publish(ev)`（`mirror.go:287`），`ev` 来自上游 WS 帧，`Seq`/`TaskID` 相同。`writeLiveBatch` 按 seq 判乱序迟到。不是两个 Hub，也不是只写 `mirror_events` 不回 Hub。

2. **ledgermirror 对 `target=""` 是否 skip？**  
   **确认。** `reconcile`：`if !registered[link.Target] { continue }`（`ledgermirror/mirror.go:236-238`）。`Names()` 来自配置键，`""` 不在其中。只放行 ViaTemplate、不补本机源，`card wait` 仍看不到任务事件。

3. **ReadTimeout=30s 是否 EOF 主因？**  
   **确认可排除。** `cmd/agentd.go:394-405`：ReadTimeout 只约束请求头/体；Hijack 清 deadline，「实测 Go 1.26 行为」，coder/websocket Accept 走 hijack。与现场 `failed to read frame header: EOF` 的主因是服务端主动断开（乱序迟到），不是 30s 读超时。

4. **本机源「store 重放 + Hub、只写账本不回 Publish」会不会和任务镜像、WaitEvent 抢同一 Hub 订阅者契约？**  
   **会，而且正文没写。** 不抢 `handoff wait` 的磁盘 cursor（`StreamEventsOnce` 那条禁令针对的是镜像走 WaitEvent 路径）。抢的是：① `Watchers` 计数（C2）；② 16 槽慢订阅丢弃（I3）。任务镜像跳过自机之后不再是这条 Hub 上的镜像订阅者；WaitEvent 仍是 `/ws/events` → `Hub.Subscribe`。本机源若再 Subscribe，就是第三个订阅者，且是内部的。

5. **WorkBranch 字符串不等：旧挂账 `local` vs 新 `""` 是否假跨机？比较在哪一行？**  
   **会。比较在 `internal/ledgerstep/dispatch.go:159`。** 而且比 spec 写的更狠：空串短路让 `""` vs `""` 也拒。见 C1。

6. **空 Target 语义从拒发变本机是否该 L3？**  
   **不该。** 见 §3。但必须回写 B192，否则冻结物与实现矛盾会合进主线。

7. **节点等待继续走本机 HTTP/WS 是否留下自订之外的 loopback 死锁/代理坑？**  
   **B161 已修 WS 不走代理**（`internal/client/ws_proxy_test.go` 整文件；`wsDialOptions` 交出 `Proxy: nil` 的 `c.hc`）。HTTP 短请求同一 Transport。B268 已证明本机 HTTP 能建出 task。自订拿掉后，同进程 WS 不再被二次 Publish 掐。残留风险是：本机客户端必须是 `LocalEndpoint` / 同等拨号（I1），以及同步探活打本机是短请求、不要在持着会阻塞 Status 的锁里调。不构成「必须改成进程内 WaitEvent」的理由。spec 弃选成立。

## 8. Out of Scope / roadmap

`docs/roadmap.md:448-452` 三条与 spec OOS 一致：

- `resume --force` 给节点能认的终态（不要假 `completed`）
- 执行器写完终稿不发 `completed`
- 真远端镜像断线 vs 卡流滞后的可观测性

「永不做」四条（魔法别名、闭区间、为自订删乱序断开、自动批进 `card wait`）未混进 roadmap，合格。

`forceToReview` 不发 `completed`：已核 `manager.go:2909-2934`。

## 9. 图覆盖债

与 spec 备注一致，独立核：`codegraph/best.json` 把 `k_agentd_Mirror` 放在 `d_workspace`，`k_agentd_Hub` 在 `d_orchestration`，ledgermirror 在 `d_ledger`。`codegraph context d_ledger` 看不到任务镜像与 `writeLiveBatch` 是预期，不是漏读。两条镜像不要收成一个类型——正文已写，同意。

## 10. 批准前最小补丁（只改 spec 正文，不是代码）

1. **C1**：改写 WorkBranch 现状与方案；去掉空串短路；接缝锁 `""` vs `""`；回写 B192 空 Target 语义；历史缺字段怎么处置写死（可与 I5 一并）。
2. **C2**：本机源 vs `Watchers` 三选一写死；接缝 3 断言无真实 WS 时 Watchers=0。
3. **I1**：接缝覆盖 `stepTransport` / `awaitNode` / `diffNode` 的本机客户端。
4. **I2**：自机判定接缝用生产形态 `http://127.0.0.1:<port>`。

I3、I4、I5 建议一并写入，否则 plan 仍会在组装点分叉。M1–M4 不挡批准。

方向保持：本机身份收口 + 两条镜像不订自机 + 账本本机源；自动批 skip 不动；`from_seq` 不动；`resume --force` 不动。
