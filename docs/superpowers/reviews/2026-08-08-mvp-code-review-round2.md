# handoff MVP 代码审阅报告（第二轮 / 修复验证）

日期：2026-08-08
范围：`bd0a1c6`..`be7ba9a`（19 commits，4765 行新增，41 文件）
方式：5 名独立审查者并行——P0 对抗性验证、adapter 重写专项、P1 逐条验证、修复回归猎捕、adapter/api diff 专项。结论以复现探针为准，不采信实现者自带的通过测试。
结论：**不通过。** 2 个 Critical、1 个 P1 未修复、P0-5 仍开放、5 个修复引入的新 Important。

---

## 〇、先说确凿的好消息

这不是一次敷衍的修复，验证时它经受住了远比原报告更狠的攻击：

- **没有一个测试被删、被跳过、被放宽。** 逐文件 diff 19 个测试文件：测试函数 62 → 128，`comm -23` 集合差为空；全仓无 `t.Skip`；无超时被注水。几处 `-` 行反而是**加强**（`config_test.go` 把吞掉的 error 改成断言，`integration_test.go` 把变量比较改成硬编码期望值）。
- **P0-2 / P0-3 / P0-4 三条真正关闭**，且是双向验证的：
  - P0-2：401 经 WS 握手 6ms 返回、任务不存在 9ms 返回、`--timeout 3s` 实测 3.016s 退出；**且没有过度修正**——connection refused / 500 / 502 / 握手中 EOF 全部仍然重试到 ctx 截止，笔记本离线数小时这条产品级需求完好。
  - P0-3：原探针从 114/300 降到 **0/500**；且超时方向仍然有效（`exitCode=124 kills=1`，孙进程确认被杀）。修得不是"永不杀"。
  - P0-4：真起 tmux 实测，`ps -ax -o command` 全量扫描、`tmux show-environment`、`capture-pane`、`serve.log` 均无密码；`run_serve.sh` 0600 且 `O_CREATE` 时即带模式（无 create-then-chmod 窗口）；密码含 `'; touch PWNED` 的注入探针未逃逸；`crypto/rand` 128 位熵。
- **P1 17 条里 12 条干净**：#1 ASC 重放（10050 积压跨 cursor 分页验证）、#3 超时（WS 因 hijack 清除 deadline 实测免疫，`handoff run` 10min 不被掐）、#5 symlink 防逃逸（绝对/相对/目录/两跳链/TOCTOU 25829 次读全部 `ErrPathEscape`）、#6 #7 #12 #13 #14 #15 全部实测通过。
- **纪律满分保持**：143 个导出符号有效 143/143 有 doc 注释，新增 7 个文件 7/7 有中文职责/边界头，`proto_test.go` 补齐了上轮点名的缺失。
- **SPIKE 是真的**：审查者在 `.superpowers/sdd/.../spike{3,5}-events.jsonl` 找到原始抓包，**用真实样本重放过 `mapEvent`**，确认 `permission.asked` / `session.status` / `message.part.delta` / `properties.sessionID` 四个形态记录准确，新映射对观测到的流程是对的。SPIKE-2 也实测生效。

以下所有问题都是在这个基础上说的。

---

## 一、Critical（必须修，均为纯逻辑缺陷，不依赖 opencode 行为）

### C-1 `startCommit` 基线只取一次，第二回合起必然误判 completed

`internal/executor/opencode/adapter.go:284-291, 945-977`

`captureStartCommit` 只在 `startRun`（:273）和 `Resume`（:346）调用，而 `fallbackClassify` 永远拿 `HEAD != r.startCommit` 比。回合 1 提交后，回合 2（审核者提问 → executor 用散文回答、不提交）仍然 `hasNew == true`，于是带着**回合 1 的 commit hash** 报 completed。

真 git 仓库探针：
```
turn1 → result {Commit:a23b4b4… Summary:"第一回合做完了但忘了 trailer" OK:true}   ← 正确
turn2 → result {Commit:a23b4b4… Summary:"我确认了一下，不需要改动"   OK:true}   ← 错误，没有新提交
```

**多回合正是 handoff 的主路径**（提问 → reply → continue），而现有测试 `TestIdleFallbackNoTrailer` 只跑单回合，所以漏掉了。修法：`mapIdle` 结束时把 `r.startCommit` 刷新为当前 HEAD（基线按回合，不按 run）。两行改动 + 一个双回合测试。

### C-2 任何 `session.status idle` 都结束回合，没有 busy→idle 边沿判定

`internal/executor/opencode/adapter.go:904-936`

`mapSessionStatus` 见 idle 就 `mapIdle`，没有边沿跟踪、没有去抖、不要求 trailer 存在、不校验模型是否真停。配合 `fallbackClassify`，一个中途 idle 在 executor 已提交代码后会产出 `Result{OK:true}` → manager 发 completed → 审核者以为完工，跑 `handoff done` → `Stop` **在 opencode 还在干活时杀掉 tmux 会话**。

探针：
```
feed: text "我先看看代码。" → idle → busy → text+trailer → idle
emitted question: "我先看看代码。"      ← 伪造的回合结束
emitted result:   {Branch:b Commit:c OK:true}
```

adapter 的行为 CONFIRMED；**触发条件未知**——两份 spike 样本都是秒级单工具调用（spike5 `busy×5 → idle×1`，spike3 只有 `busy×2`），而 handoff 最关键的场景（权限门挂起数小时期间是否发 idle）完全没覆盖。这是把最高风险子系统的正确性押在第三方二进制的一个未观测属性上。加边沿判定很便宜且严格更安全，不要赌。

---

## 二、未修复 / 半修复

### U-1 P1-2（manager 时序）**未修复** — 窗口只是挪了位置

`internal/agentd/manager.go:507`（CreateTicket）…`:531`（transit WaitingAnswer），`handleQuestion` 在 `:584`/`:604` 同形。

把 `Publish` 挪到 transit 之后，关掉的是**事件驱动**的 reply 窗口。`CreateTicket` 与 transit 之间的窗口没关，而这个窗口恰好可经 spec §7 规定的流程抵达：`attach` → 读 `pending_tickets` → `reply`。此时 `AnswerTicket` → `NotifyAnswer`（无 waiter）→ `RelayAnswer`（executor **确实**恢复了）→ `resumeIfIdle` 读到 `running` 直接返回；随后第 531 行给一个零挂起工单的任务盖上 `waiting_answer`。

真 HTTP 路径复现：**28/400 与 4/400**。终态 `reply`→404、`continue`→409、`done`→409，且 executor 已在跑，2h 看门狗也只能发个 `stalled`，恢复不了状态。

修法一行：两个 handler 里把 `transitBestEffort(..., WaitingAnswer, ...)` **移到 `CreateTicket` 之前**——reply 不可能早于工单存在，state-first 没有反向窗口。

### U-2 P0-5（reply 中继失败）**只做了可见化，没有修复**

`internal/agentd/server.go` handleReply

502 + `relayed` + `reason` 确实有了，CLI 也能拿到原因——这是真实改进。但工单照样被消耗，端到端探针：

```
reply(首次)   -> 502 {"ok":true,"relayed":false,"reason":"中继权限应答: 任务 task-1 不在运行中"}
工单 answer 已落库 = "allow"        (answer IS NULL 守卫已消耗)
attach 挂起工单 = 0，任务状态 = waiting_answer
reply(重试)   -> 404      continue -> 409      done -> 409
```

**且有一个比原报告更坏的子情况**：重启救援只在 executor **已死**时有效。若中继是**瞬时**失败（`RelayAnswer` 用有界 `unaryCtx`，opencode 短暂卡顿即超时），executor **还活着且仍阻塞在权限上**；`RecoverOnStartup` 探活成功、恢复订阅、而已应答工单从不重放——**永久死锁，CLI 和运维都没有恢复路径**。

你们把它列为遗留 #1 是诚实的，但两名审查者独立判断它应当保持 P0：这是唯一一个"审核者做了正确操作，系统进入不可恢复状态"的路径。

### U-3 P1-16（dead 任务工单作废）**半修复** — 类没关，只关了实例

`internal/agentd/watchdog.go:219` 只覆盖 `RecoverOnStartup`。进程内死亡路径——`subscribeLoop` → `result{OK:false}` → `manager.handleResult`（`manager.go:662-704`）→ failed 事件 → `waiting_review`——从不调 `VoidPendingTickets`。

探针：executor 带着挂起权限工单死亡后，`state=waiting_review, pending_tickets=1`；`reply` 无 waiter → `RelayAnswer` 失败 → 502，工单被消耗。**即"attach 显示可操作项，一操作就撞 P0-5"原封不动，而且不需要 agentd 重启就能触发。** 修法：`handleResult` 的 `!r.OK` 分支调 `VoidPendingTickets`。

---

## 三、修复引入的新缺陷

### N-1 WS `live` 缓冲无上限 — 把有界丢弃换成了无界内存（Important）

`internal/agentd/server.go:756-779`

P0-1 的修法用一个 drainer goroutine 把所有事件 `append` 进无上限的 `live` 切片，绕过了 hub 的慢订阅者丢弃契约（`hub.go:9`，16 槽 + `select`/`default`）。`writeEvent` 没有写超时，`CloseRead` 的 ctx 只在**读**错误时触发。一个不读但不 RST 的对端——**合盖的笔记本，也就是本产品的头号场景**——会让 handler 卡在 `conn.Write`，而 `live` 无限增长。

两名审查者独立探针：20000×4KB → **+88MB**；50000×4KB → **+209MB**，零丢弃，且 handler 被钉住内存永不回收。

修法便宜且设计已支持：六个生产 `Publish` 站点发的都是 `AppendEvent` 返回的 `evt`，即**每条实时事件都已持久化**，所以给 `live` 加上限（如 1000）、溢出丢弃、让客户端凭 cursor 重连补拉即可；顺带给 `writeEvent` 加写 deadline。

### N-2 乱序 Publish 的低 seq 事件被静默丢弃（Important）

`internal/agentd/server.go:823` `writeLiveBatch` 的 `if ev.Seq <= lastWrittenSeq { continue }`，而注释声称"乱序迟到的已写出"——**不成立**。探针（watchdog 追加 seq=1 stalled，mediate 追加 seq=2 question，跨批次反序发布）：客户端只收到 seq=2，**seq=1 的 stalled 被服务端丢弃**，且 cursor 前移使其永不补拉。生产可达：`scanStalled`（`watchdog.go:153`）与 per-task `mediate` 并发发布，窗口在 `AppendEvent` 与 `Publish` 之间。概率低，但牺牲品恰好是兜底唤醒的 `stalled`。

### N-3 截断诊断又变成死代码（Important）

`internal/agentd/server.go:857` 的 `storeMax > lastWrittenSeq` 比的是最大值而非连续性。任何 seq 大于 storeMax 的实时事件都会把 `lastWrittenSeq` 顶过去，于是中段缺口存在而 Warn 被抑制。探针：缺口 `(10000, 12001)` 投递给客户端，`服务端Warn=[]`。**这正是 P1-1 当初报的死代码问题，换了个位置重现。**

### N-4 `!created` 提前返回，删掉了崩溃自愈能力（Important）

`internal/agentd/manager.go:515-523`

`handlePermission` 仅凭 `created` 标志判定"这是重放，全跳过"，在 `AppendEvent`/transit/waiter/`Publish` **之前**返回。若在 ticket 插入与事件追加之间崩溃（或 `AppendEvent` 失败），就留下一个有工单无事件的状态；此后每次重放都命中 `!created` 返回，**permission_request 事件永不产生，任务停在 running，无 waiter，审核者的 `wait` 永不触发**。基线版本在这里是能自愈的。

探针：`permission_request 事件数=0(want 1)、state=running(want waiting_answer)、waiter=0(want 1)`。修法：`CreateTicket`+`AppendEvent` 放进一个事务；或 `!created` 时查一下对应事件是否存在，不存在就继续走（顺带修复已损坏的行）。

### N-5 `serveLogTail` 整文件读入内存（Important）

`internal/executor/opencode/proc.go:275-281` 用 `os.ReadFile` 读整个 `serve.log` 只为取尾部 500 字节，而 `serve.log` 由 `tee -a` 写满任务全程、无轮转无上限。调用时机恰恰是最糟的时刻（就绪超时 / serve 死亡 → 失败事件载荷）。探针：300MiB 日志 → **+315MB 堆，为了 500 字节**。

**这是"只修了一个调用点"的最清楚实例**：同一批修复在 20 行外的 `workspace.go` 给 `ReadFile` 加了 `io.LimitReader`，却没给自己新引入的同类兄弟加。修法：`Seek(-1024, io.SeekEnd)`。

### N-6 测试重写中一条保护性断言被悄悄删除（Important）

`internal/executor/opencode/adapter_test.go:117`

旧 helper `msgEvent(id, role, text)` 带真实用户文本，旧 `TestIdleFallbackNoTrailer` 借此断言用户消息文本不进回合；新 helper `userMsgEvent(id)` **不带任何文本**，新套件里没有任何用例推送 user 名下的文本 part。

变异验证：把 `adapter.go:834` 和 `:877` 两处 `if r.userMsgs[...] { return }` 守卫**全部删掉**，`./internal/executor/...` 全套仍然通过。生产影响明确——初始 prompt（渲染后的整份 plan）就是以 user 消息下的 `message.part.updated` 到达的，守卫一旦回归，整份计划会被追加进回合与 `render.log`，`ParseTrailer` 在计划正文上跑。

（这是 5 名审查者里唯一一处"测试被削弱"的发现，且是间接削弱——函数没删，覆盖没了。）

---

## 四、adapter 重写的其余 Important（新代码，此前未经审阅）

| # | 问题 | 位置 | 影响 |
|---|------|------|------|
| A-1 | 会话隔离**双向**都不对：缺 `sessionID` 时 fail-open（无 sessionID 的 `permission.asked` 被当真实审批工单发出，零日志）；而**子会话** sessionID 不匹配时静默 Debug 丢弃 | `adapter.go:685-698` | opencode 为 subagent/`task` 工具派生子会话；若其权限请求带子会话 id，审批请求被丢、审核者永不知情、serve 活着所以 adapter 看门狗不触发——静默挂起且无可观测性。审批门上 fail-open 与静默 fail-closed 都是错的方向 |
| A-2 | 审批文本无下限、截断无标记 | `adapter.go:751-759` | 三种真实形态探针均产出 `text=""`，审核者被要求批准一个空白行；长 bash 命令在 200 字处无省略号截断，审核者批准自己看不全的命令 |
| A-3 | `Resume` 后首个 user 重发抹掉整个回合 | `adapter.go:342` vs `:782-791` | `Resume` 新建空 `userMsgs`；spike5 证实 opencode 每次 `session.diff` 后重播同一 user 消息 → 被当"首见" → `clearTurn()` → 恢复后累积的全部文本丢弃，随后 idle 命中空回合分支不分类。探针：**零事件产出** |
| A-4 | `clearTurn` 抹掉 `partTypes`，reasoning 泄漏进下一回合 | `adapter.go:1004-1009` | 探针：`emitted question: "推理B-泄漏"` ——模型思维链变成了面向审核者的提问。与 C-2 叠加时同时误判 + 开闸 |
| A-5 | `delta` 先于 `part.updated` 到达时按 text 处理 | `adapter.go:874-888` | 顺序只是 spike5 的**观测**属性（17→18），非保证；SSE 跨重连无顺序保证。探针：`question: "秘密推理：我打算直接问用户"`。且测试注释直接引用 spike 的顺序（`adapter_test.go:620-624`），属于"测试复述解析器假设" |
| A-6 | 快照被修订时整段重复计入 | `adapter.go:842-851` | `"Hello world"` → `"Hi world"` 得到 `"Hello worldHi world"`；`"ABCDEF"` → `"ABC"` 得到 `"ABCDEFABC"`。注释称"宁可重复也不丢字"，但 fallback question 与 render.log 是**给人读的** |
| A-7 | `SubscribeEvents` 永远返回 nil，失败原因恒为 `<nil>` | `api.go:316-319`；`adapter.go:540` | 上轮已报（B#12），未修。可达路径：看门狗三次抖动判死而 `Alive()` 又恢复 → 任务标记 **failed**，`FailReason="opencode 事件流意外中断: <nil>"`，零信息 |
| A-8 | backoff 在收到 **200 响应头**时即复位 | `api.go:329-336` | 半死的 opencode（接受连接、200、立刻关流）永不退避。探针：50ms/2s 配置下 **1.2 秒内 23 次重连**（正确行为约 5 次）；生产值下即每秒一次重连 + 每次一行 Info 日志，永不升到 30s 上限。修法：按"连接存活时长"复位，而非按 200 |
| A-9 | 客户端 WS backoff 从不复位 | `client.go:408-439` | 同一个缺陷 `api.go:330` 修了、`client.go` 没修——`backoff` 声明在循环外且永不重置，长时间离线后余下整个 `wait` 期间钉在 60s |
| A-10 | Kill 失败的运行态无限保留、无重试无过期 | `adapter.go:464-481` | 探针：50 个任务 kill 恒失败 → `len(a.runs)==50` 永久。`Stop` 只由归档时的 `Done` 调用，`RecoverOnStartup` 不接管，故**永不重试**。状态是惰性的（无 goroutine），是内存与 `lookup` 阴影，不是挂起 |
| A-11 | 探活"活跃保持快速"基本是虚构 | `adapter.go:69, 574` | `lastEventAt` 只在 `emit` 更新，而 `progressThrottle=30s`，于是**正在流式输出的任务约 93% 时间处于慢速档**；且死亡检测最坏 ~2.4s 而非注释所称 6s。一行修法：改在 `mapEvent` 里更新 |
| A-12 | `serve.log` 尾部无脱敏即进 `FailReason` 与 agentd.log | `adapter.go:526-530` | 该尾部是 opencode 完全可控的输出。若任何版本在启动横幅、panic 环境转储或带认证的 URL 中回显 `OPENCODE_SERVER_PASSWORD`，密码就落进事件库与日志文件。`Proc.Password` 就在手边，一行 `strings.ReplaceAll(tail, p.Password, "***")` 即可。SUSPECTED（无法验证 opencode 实际打印什么），但成本为零 |

---

## 五、证据链断裂：spike 抓包未入库

`docs/superpowers/e2e-checklist.md:4-20` 把 SPIKE-1/1b 标记为 `[x]`，引用 `spike3-events.jsonl` / `spike5-events.jsonl`。这两个文件存在于本机 `.superpowers/sdd/...`，而 `.superpowers/sdd/.gitignore` 内容是 `*`，`git ls-files .superpowers` 返回空。

**整个 adapter 重写的唯一依据，从任何一个 clone 都无法复核。** 审查者是在本机偶然找到它们才得以重放验证；换台机器，这次审查的核心结论就无法复现。修法：把两份 jsonl 作为 `internal/executor/opencode/testdata/` 固化入库 + 一个约 30 行的重放测试（实测 0.06s）。这应当是合入的硬条件。

---

## 六、文档与代码漂移

1. **`README.md:93` 现在是错的**：称 tmux "会话随 serve 退出销毁，以 serve.log 为准"。加了 `startRenderTailWindow` 之后，`tail -f` 窗口会让会话在 serve 死后继续存活（真 tmux 实测）。同一个过时前提还留在 `proc.go:202` 和 `adapter.go:527` 的注释里（而 `adapter.go:534` 是按新行为写的，自相矛盾）。
2. **`README.md:90-93` 列举任务目录时漏了 `run_serve.sh`**——那个目录里最需要读者知道的、装着明文密码的 0600 文件，恰恰没列。
3. **spec `:80` 与 plan `:19` 仍写"ticket id = opencode permissionID"**，已被 P1-6 反转（README 是对的）。
4. **`handoff run` 的新参数顺序约束没进用户可见文档**：`SetInterspersed(false)` 意味着 `handoff run T1 --target devbox go test` 会把 `--target devbox` 静默传给被执行命令。
5. `Resume` 的 not-alive 分支不 `Kill()`，每个这类任务永久遗留一个 tmux 会话 + `tail -f` 进程（`adapter.go:317-341`，真 tmux 实测）。

---

## 七、其余 Minor（可后置）

`ReadFile` 对仓库内 FIFO 会永久阻塞在 `openat`（`IsRegular` 检查在 `Open` 之后，`ErrNotRegularFile` 对 FIFO 不可达，handler goroutine + fd 永久泄漏，executor 可随意 `mkfifo`；用 `O_NONBLOCK` 或先 `Lstat` 一行解决）；`SilenceUsage: true` 连**参数错误**的 usage 也吞了（`handoff wait` 缺参时只说 "accepts 1 arg(s)"，不给语法提示），且 `root_test.go:159` 把这行为固化成了断言；`fetch` 1MiB 截断无标记，审核者可能据"文件末尾"推理而那不是末尾；cursor 临时文件改 `os.CreateTemp` 后被杀会留下永不清理的 `.tmp`；`--timeout -5s` 静默当作无超时；`StallTimeout: 0` 无值校验会让每个 running 任务首 tick 即 stall；退出码仍全是 1，`--timeout` 的无人值守场景无法区分超时与鉴权失败；`killProcGroup` / `RunCmdTimeout` 为测试注入降级为可变包级变量；生产仍用 `Execute()` 而非 `ExecuteContext()`，与 `run_test.go` 分歧；`render.log` / `serve.log` 无轮转无上限；`partSeen` + `r.turn` 对同一回合文本双份留存（2000 part × 4KB → 16MB）；fallback question 文本无长度上限（探针 20 万 rune 直接进工单行与终端）。

---

## 八、建议的修复顺序

1. **C-1、C-2**（adapter 两个 Critical）——纯逻辑，各两行加一个测试，且 C-1 命中主路径。
2. **U-1**（P1-2 一行换序）、**U-3**（`handleResult` 调 `VoidPendingTickets`）。
3. **N-1**（`live` 加上限 + 写 deadline）、**N-5**（`Seek` 尾读）、**N-4**（事务或落地检查）、**N-2/N-3**（乱序与诊断）。
4. **把两份 spike jsonl 入库为 testdata + 重放测试**，并把 `e2e-checklist.md` 里 `[x]` 的依据指向入库路径。
5. **N-6**（补回 user 文本守卫的断言）、**A-2**（审批文本下限与截断标记）、**A-4**（`partTypes` 生命周期）、**A-12**（一行脱敏）。
6. **U-2 / P0-5** 需要一个决策而非一行修补：要么让 `RelayAnswer` 失败时回滚工单为未应答，要么加一个 `handoff resume-ticket` 类的显式恢复操作。在此之前，"审核者做了正确操作却进入不可恢复状态"这条路径一直开着。
7. A-1、A-3、A-5~A-11 与第六节文档漂移。
8. 第七节 Minor。

---

## 八之二、修复状态（2026-08-08 由审核者直接修复）

本轮已修 9 项，每项按 TDD 先写失败测试再实现，且新测试都在未修复的代码上验证过确实会失败（在 scratchpad 副本里逐个撤销修复复跑确认）。

| 项 | 状态 | 修复要点 | 回归测试 |
|----|------|---------|---------|
| C-1 基线不刷新 | 已修 | `mapIdle` 收尾刷新 `startCommit`，基线按回合走 | `TestFallbackBaselineRefreshedPerTurn`（双回合，第二回合无提交必须判 question） |
| C-2 任意 idle 结束回合 | 已修 | idle 改为「候选回合结束」+ `idleGrace` 去抖（生产 1.5s）；宽限期内任何新增文本 / 非 idle 状态 / 新 idle 都自增 `idleGen` 使其失效 | `TestTransientIdleDoesNotEndTurn`、`TestIdleClassifiesAfterGrace`（反向保证：去抖不得变成永不结束） |
| U-1 工单先于状态可见 | 已修 | 两个 handler 都改为 transit → CreateTicket → AppendEvent；建单失败回滚 running | `TestPermissionStateVisibleBeforeTicket`（200 轮竞态，修复前 31/200 卡死） |
| U-3 dead 任务留挂起工单 | 已修 | `handleResult` 的 `!OK` 分支调 `VoidPendingTickets`，与重启恢复路径同语义 | `TestDeadExecutorVoidsPendingTickets` |
| N-1 `live` 缓冲无上限 | 已修 | 缓冲上限 `liveBufferLimit=1000`，越限即断开连接（事件已落库，客户端凭 cursor 重连无损补拉） | `TestWSLiveBufferBounded` |
| N-2 乱序迟到被静默丢弃 | 已修 | 去重判据改为 `seq <= maxReplayed`（真重复）；落在 `(maxReplayed, lastWrittenSeq]` 的乱序迟到事件断开连接由重放补齐 | `TestWSOutOfOrderPublishNotDropped`（含重连补齐断言） |
| N-3 截断诊断死代码 | 已修 | 诊断改为「截断发生 且 缺口实际条数 > 已补出条数」，缺口条数用 `Store.CountEvents` 按任务精确统计 | `TestWSTruncationWarnsOnRealGap`、`TestWSTruncationGapCountedPerTask` |
| N-4 `!created` 吞掉唤醒 | 已修 | 重放判定拆成三态：工单不存在→新请求；已应答→跳过；有工单无事件→**放行补发自愈** | `TestPermissionSelfHealsWhenEventMissing`、`TestPermissionReplayStillSkipped`（幂等不得回退） |
| N-5 `serveLogTail` 整读 | 已修 | `Seek` 到尾部 + `io.LimitReader` 有界读 | `TestServeLogTailBounded`（100MiB 稀疏文件，断言分配 < 4MiB） |

修复过程中另外发现并已处理的一点：**`events.seq` 是全局 AUTOINCREMENT 而非按任务递增**，因此任何以「seq 是否逐格衔接」为依据的缺口判定，在多任务并发下都会误报。N-3 的第一版实现正是这么写的，靠补一个交错场景的测试（`TestWSTruncationGapCountedPerTask`，按 seq 跨度算得 30、按本任务条数算得 15）才暴露出来，已改为按任务精确计数。

新增的 `Store.CountEvents` / `Store.TicketHasEvent` 各有直接单测。全量 `go test -race ./...` 绿，`go vet`、`gofmt` 干净。

### U-2 / P0-5 已按「新增显式恢复操作」收口

根因是 schema 把两件不同的事实压在了一个字段上：`answer` 既表示「审核者已裁决」，又被当成「裁决已送达 executor」。中继一失败，工单的 `answer IS NULL` 守卫已被消耗，而 executor 根本没收到——系统再也分不清该不该重投。

修法：

1. **`tickets.delivered_at` 独立成列**（含旧库 `ALTER TABLE` 迁移）。只有 `RespondPermission`/`Send` 真正返回成功才写入，它是「该不该重投」的唯一依据。
2. **`delivery_failed` 事件**。此前投递失败只有一行日志：等待者还在时 `reply` 甚至返回 **200**（应答确实落库并唤醒了等待者，失败发生在 `waitPermission` 内部），审核者拿不到任何错误码，而 executor 仍原地阻塞——这个变体比 502 那条更隐蔽，是这次实现时才发现的。现在两条路径都产出事件，`wait` 不过滤它，审核者会被唤醒并看到 hint。
3. **`handoff resume <task>`**（`POST /api/tasks/{id}/resume` → `Manager.RecoverStuck`）：重投全部未送达应答；全部成功则任务回 `running`；遇 `executor.ErrTaskNotRunning`（新增的哨兵错误，替代按错误文本判别）则追加 failed 事件、作废挂起工单、转 `waiting_review` 交审核者；遇其他错误保持 `waiting_answer` 可重试。幂等——已标记送达的不会重投。

覆盖：`TestResumeRedeliversUndeliveredAnswer`、`TestResumeWhenExecutorGone`、`TestResumeTransientFailureKeepsRetryable`、`TestResumeNoopWhenNotStuck`、`TestNormalDeliveryMarksTicketDelivered`，以及走真实 HTTP + client 的端到端 `TestResumeRoute`（reply 200 → delivery_failed 唤醒 → resume 重投 → 再次 resume 为 0 条）。

### 八之三、第五/四/六/七节的收口（2026-08-08 续）

**第五节 证据链**：两份抓包已入库 `internal/executor/opencode/testdata/`，`replay_spike_test.go` 把原始 SSE 字节原样喂进生产解析路径回放，断言权限 id 与描述、reasoning/user 文本不泄漏、回合分类结果、会话隔离按真实形态（sessionID 在 `properties` 而非顶层）生效，并有一条守住样本本身是真抓包而非手写 JSON。`e2e-checklist.md` 的 `[x]` 依据已改指入库路径。

顺带闭掉 **N-6**：删掉 `mapMessageUpdated`/`mapPartUpdated` 的 user 守卫后，`TestReplaySpike3NoLeak` 立刻变红（`user 消息原文泄漏进 progress 事件`）——覆盖回来了，而且是靠真实样本而非手写事件。

**第四节 A-1~A-12 全部已修**，各配回归测试（`regression_group_a_test.go`、client 侧 `ws_backoff_test.go`）：

| 项 | 修法 |
|----|------|
| A-1 | 任务级事件（permission/message/session 系列）缺 sessionID 即拒绝（不再 fail-open）；会话不符的 `permission.asked` 从 Debug 提到 **Warn** 并带 properties，不再静默丢弃 |
| A-2 | 描述拼不出内容时给「未提供描述 + 权限 id + 去 tmux 看现场」；截断统一走 `truncateMarked`，带「…（已截断）」 |
| A-3 | user 消息不再用作清空回合的信号（只登记 id）——进程内 `userMsgs` 在 Resume 后是空表，重播的老消息会被当首见而抹掉整个回合；回合缓冲由 `mapIdle` 分类后清空即可 |
| A-4 | `partTypes`/`userMsgs` 是会话级事实，`clearTurn` 不再抹掉 |
| A-5 | 类型未知的 delta 不再默认当文本，暂存进 `pendingDelta`（64KB 上限），等 `part.updated` 揭示类型再落地或整段丢弃 |
| A-6 | 回合文本改为按 part 存当前值 + `turnOrder` 顺序拼接，服务端修订快照即覆盖而非叠加（顺带消掉第七节「`partSeen` + `turn` 双份留存」——`turn` 字符串已不存在） |
| A-7 | `SubscribeEvents` 退出时返回最后一次连接的真实原因；纯由取消派生的错误滤掉（`dropCtxCause`） |
| A-8 | 退避复位判据从「拿到 200」改为「连接活够 `sseStableAfter`=5s」；顺带修正了复位与等待的先后（原顺序会让复位推迟到下下次才生效） |
| A-9 | 客户端 WS 同款修法（`wsStableAfter`），并加 `NewWithWSTiming` 供注入 |
| A-10 | kill 失败保留的运行态转入 `reapRetained` 后台重试（30s × 20 次），成功即注销，耗尽则 Error 交人工并注销——不再只增不减 |
| A-11 | 活跃打点从 `emit` 移到 `mapEvent`：挂在收到 SSE 事件上，不再被 30s 的 progress 节流遮住 |
| A-12 | `procHandle.LogTail` 用 `Proc.Password` 做一次 `ReplaceAll` 脱敏 |

其中 A-8 让既有的 `TestSubscribeBackoffResetAfterSuccess` 变红——它原本把「200 即复位」这个缺陷固化成了断言，已按新判据重写（成功连接改为保持 250ms 再断）。

**第六节 文档漂移全部已修**：README 的 tmux 说明改为「serve 退出后窗格关闭但会话仍在，由 adapter 显式回收」，补上 `run_serve.sh`（0600 + 明文密码）；`adapter.go` 里同一处过时前提一并改正；spec/plan 的「ticket id = permissionID」改为命名空间化并注明是 P1-6 的修正；`handoff run` 的 flag 顺序约束进了命令表。第 5 小点是代码问题，已修：`Resume` 探活失败时补 `Kill()`，否则每个这类任务永久遗留一个 tmux 会话 + `tail -f` 进程，而此后没有任何路径会碰它。

**第七节 Minor 已修 8 项**：

- `ReadFile` 以 `O_NONBLOCK` 打开（unix/非 unix 各一个常量文件），仓库内 FIFO 不再让 handler 永久阻塞在 `openat`
- `fetch` 截断补上带真实文件大小的显式提示，审核者不会再把截断处当文件末尾
- 兜底 question 文本上限 8000 字符并指明全文在 `render.log`
- `stalltimeout` 显式非正值即报错（省略走默认 2h 不受影响）
- `--timeout` 负值报错，不再静默当作「不设上限」
- 退出码分级：超时 124（沿用 `timeout(1)` 与 `handoff run` 的惯例）、其余 1，无人值守脚本可区分「继续等」与「立刻告警」
- `SilenceUsage` 改为在 `PersistentPreRun` 里才置位：参数错误照常打 usage（根因就是用法），运行期错误仍然只打错误
- cursor 临时文件按 1 小时年龄阈值清理遗留 `.tmp`（阈值是为了不误删并发进程在途的写入）

**顺带修掉了之前报告的既有测试隔离缺陷**：根因是 cobra 的 `ExecuteC` 只在 `cmd.ctx == nil` 时才把根的 ctx 传给子命令，包级单例第二次执行时子命令还挂着上一次那个已取消的 ctx。现在 `Execute`/`ExecuteContext` 入口先 `resetPerRunState` 清掉命令树上的 ctx 与 `SilenceUsage`，`go test -race -count=2 ./...` 全绿。

**仍未做（明确判为可后置）**：`render.log`/`serve.log` 无轮转无上限（属真功能，非缺陷修补）；`killProcGroup`/`RunCmdTimeout` 为测试注入降级为可变包级变量（设计洁癖，行为正确）；生产入口未改用 `ExecuteContext`（`resetPerRunState` 已消除其副作用，剩下的只是信号处理的增强）。

## 九、一句话总结这次修复的失败模式

这一轮不是马虎——能探到的修复都做到了它声称的事，测试翻倍且无一被削弱，注释里的推理每次核对都成立。它的失败模式是：**在替换掉有界结构的地方引入了无界的新结构**（有界丢弃 → 无界 `live`；有界 `ReadFile` → 无界 `serveLogTail`；随命令退出的 tmux → 常驻窗口），以及**最大的新增面（adapter 重写）带着与原版同一类未经验证的假设进场**——只不过这次假设来自两份秒级样本，而非凭空。
