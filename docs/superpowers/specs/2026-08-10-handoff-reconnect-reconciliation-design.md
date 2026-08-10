# handoff 断连窗口的会话对账 —— 设计

> 对应 backlog：**B38**（agentd 重启窗口内完成的回合，终态事件永久丢失，任务冻死在 running）
>
> 状态：设计定稿，待实现
>
> 前置：**§7 探针**必须先在真机跑完；探针拿不到结论的 adapter 不实现对账（见 §7.4）

---

## 1. 背景与目标

### 1.1 实撞的现场

08-10 detmux-prochost 分支在 devbox（macOS）旁挂实例真机验收时撞上，完整证据链：

| 时刻 | 事实 | 取证方式 |
|---|---|---|
| 15:54:26 | `reply` 投递成功，日志 `relayed=true` | agentd.log |
| 15:54:41 | executor 完成提交 `06cc394`（README 标题已改成审核者的答复） | git log |
| 15:54:45 | opencode 侧最后一条 assistant 消息 `completed`、`error: None` | 直接查 opencode HTTP API |
| ~15:54:45 | kill agentd | 人为 |
| 15:54:53 | agentd 重启，`recovered=1 / alive=true / mode=reattach` | agentd.log |
| 之后 | handoff 事件永远停在 seq 3（question），状态永远 `running` | handoff show |

回合**真的完成了**，成果**真的在分支上**，而 handoff 永远不知道。

### 1.2 四层机制共同促成

读代码确认，没有任何一层拦得住：

1. opencode 的 `/event` **无重放语义**——adapter 自己的注释就写着（`internal/executor/opencode/adapter.go:607` 的 P1-10b 降级告警）
2. `resume.go:160` 的 reattach 只做 `subscribeLoop` + `watchdog`，**不与会话现状对账**
3. adapter 的 watchdog（`adapter.go:677`）只判 serve 死活，serve 活着就永不报
4. agentd 看门狗只发 advisory `stalled`，按设计「只唤醒不改状态」

后果是**没有任何 CLI 出口**：`continue`/`done` 都要求 `waiting_review`，`handoff resume` 判「没有卡在半路的应答，无需恢复」（就其当时的职责而言判得对），只剩 `stop`——而 `stop` 会把一个其实成功了的任务落成 `failed`。

### 1.3 一处必须记下的前情

`internal/executor/opencode/resume.go:37-40` 的注释**预告了这个缝隙**：

> 重启时正在进行的回合文本累积在内存里已丢失：重建后的回合从 SSE 重放的新快照重新累积……这是 MVP 接受的缝隙，由 e2e 清单「agentd 重启」项实测观察

本次就是那次观察。**实际后果比预告严重**：预告说的是「回合文本累积丢失」（观感问题），实际丢的是**回合的终态本身**（任务冻死）。

缝隙是既有的、非 detmux-prochost 引入，但该分支把「随便重启 agentd」从罕见路径推成了常规路径。

### 1.4 四个 adapter 里只有三个有这个缝

| adapter | 事件传输 | 断连窗口内的事件 |
|---|---|---|
| opencode | SSE `/event`，无重放 | **丢** |
| grok | ACP over WebSocket，实时通知 | **丢**（待探针确认，见 §7.2） |
| codex | app-server WebSocket，实时通知 | **丢** |
| claudecode | `out.jsonl` 文件 + `proc.json` 里持久化的 `Offset` | **不丢** |

`internal/executor/claudecode/resume.go:141` 热重连时 `r.startOffset = pi.Offset`，tailer 从上次消费到的位置续读——claude 在 agentd 死着的时候写进文件的每一行，重启后都会被读到。

**这是同一个仓库里已有的存在性证明**：把传输换成可重放的持久流，这个缺陷从根上不存在。本设计不换传输（见 §1.6），但借用它的水位机制（见 §3）。

### 1.5 目标

1. **自动对账**：断连恢复后，把窗口内错过的会话进展补回事件流，任务按补发的事件自然迁移
2. **补发不新增语义**：补出来的事件与实时事件**同形**，下游（manager 中介循环、工单、状态机、`wait`）零改动
3. **权限请求尽可能捧回**：断连期间丢失的权限请求重新上报——**能力按 adapter 而定**，opencode 已实证做不到（§7.1），做不到的 adapter 退为「检测到并如实播报」
4. **人工出口兜底**：对账判不出来或 adapter 不支持时，审核者有一条不撒谎的手动出口，绝不把成功的任务落成 `failed`

### 1.6 非目标

- **不改 claudecode**——它结构上没这个缝，改它是净风险
- **不做看门狗周期性自查**——会给每个运行中任务加持续轮询流量，且「周期多长」又是一个要拍的参数
- **不改传输层**——不把 SSE/WS 换成持久化可重放流。那是三个 adapter 的传输层重写，且落盘点在哪要重新设计
- **不做「自动重跑回合」**——对账只还原**已经发生**的事实，不代替审核者决策
- **不覆盖「executor 死了因此任务卡住」这一类**——那是 Spec A（B20/B21/B24，`2026-08-09-handoff-runtime-reconciliation-design.md`）已解决的另一条路径：恢复阶梯、`Reap` 兜底回收、空回合转失败都归它。本设计处理的是 **我们错过了 executor 说的话**

  > 注意这两者**不是**按「进程死没死」切分的，而是按「缺的是什么」切分。冷恢复（进程死了、已重起、会话从盘上载回）之后**仍然要对账**：回合完全可能在进程死亡前几秒就已完结，那条终态同样丢在窗口里。恢复阶梯负责把 executor 弄回来，对账负责把它说过的话补回来，两者串行、互不替代。

---

## 2. 根因与不变量

### 2.1 根因一句话

事件传输是**实时单播**，而消费者（agentd）可以下线。传输层没有重放，消费者恢复后也不回头看，于是「断连窗口 = 事件黑洞」。

### 2.2 断连窗口内至多跨越一个回合边界

**这条不变量把丢失区间锁死了，是对账只需看会话尾巴、不需要全量重放的根据。**

论证：新回合只能由 `Start` 或 `Send` 发起，两者都要经过 agentd——

- agentd 死了：发不出
- agentd 活着但连接断了：`Send` 走的正是那条断掉的连接，同样发不出

第 1 层审批者的自动批准（`RespondPermission`）走同一条连接，同理。

所以一个断连窗口内，会话至多从「回合进行中」走到「回合结束」，不可能再开下一个回合。

### 2.3 断连窗口丢的不只是终态

`internal/executor/opencode/adapter.go:613` 的告警原文是「SSE 断连已恢复：断连间隙的**权限请求**可能丢失（`/event` 无重放语义）」。

这条丢了的后果与终态丢失不同：executor 阻塞在等审批上，会话一直 busy，任务同样冻在 `running`，但对账去查会看到「回合还没结束」，如实报告「它还在忙」——而真相是它**永远不会不忙了**。

不捧回权限请求，等于把一个可修的冻死包装成不可修的冻死。故列入目标（§1.5 第 3 条）。

**但能不能捧回是 adapter 的协议能力，不是本设计能决定的。** opencode 已实证捧不回来（§7.1）：消息流里的 tool part 只有 `callID` 没有权限 id，而应答端点要求真实 id。对这类 adapter，退而求其次的诚实做法是**检测到并如实播报**，把审核者引向 `handoff attach` 或 `--force`，而**绝不建一张批了也送不回去的假工单**。

---

## 3. 契约：可选接口与持久化水位

### 3.1 `Reconciler` 可选接口（`internal/executor`）

```go
// Reconciler 是 adapter 的可选能力：把断连期间错过的会话进展补回事件流。
//
// 实现方职责：
//   - 取回持久化水位之后的会话内容，交给既有的回合分类逻辑，补发同形事件
//   - 重新上报悬而未决的权限请求（靠稳定 PermissionID 保证 manager 幂等去重）
//   - 补发成功后前进并持久化水位
//
// 边界：
//   - 不写 store（沿用 executor 包级边界）
//   - 不改任务状态：补发的事件经既有 evCh 交给 manager，状态迁移仍归 manager
//   - 不发明新的事件语义：只产出 permission / question / progress / result 四类
type Reconciler interface {
    Reconcile(ctx context.Context, taskID string) (ReconcileOutcome, error)
}

// ReconcileOutcome 是一次对账的结论，供 CLI 呈现与日志记录。
type ReconcileOutcome struct {
    TurnEnded bool   // 断连期间回合是否已完结
    Emitted   int    // 补发的终态事件数（0 或 1，受 §2.2 不变量约束）
    Pending   int    // 重新上报的悬而未决权限请求数
    Note      string // 一句话结论
}
```

**为什么是可选接口而不是加进 `Adapter`**：探针可能证明某个 adapter 的协议给不出所需信息（§7.4）。可选接口让「不支持」成为类型系统里可查询的事实，manager 据此如实告知审核者，而不是塞一个永远返回 `TurnEnded=false` 的空实现——那会让「查不了」和「查了没事」长得一样。

仓库内同形先例：manager 的 `restorer`、`volatilePermitter` 都是挂在 adapter 上的可选接口。

### 3.2 持久化水位

**这是本设计最大的一笔改动面，也是幂等的唯一根据。**

三个 adapter 各在自己已有的凭据文件（opencode `proc.json`、grok `serve.json`、codex `serve.json`）里增加一个水位字段。

- **语义**：最后一次成功产出**终态或权限事件**时的会话位置
- **形态**：各 adapter 自定（消息 id / item 序号 / `completed` 时间戳），由探针（§7）确定；对上层不透明
- **写入时机**：只在产出终态与权限事件时写，**不跟 progress 走**——progress 是节流的高频事件，跟着它写盘既无必要也吵
- **`fresh` 模式清零**：降级新开会话时旧水位失去意义，必须清掉，否则下次对账拿旧水位去比新会话
- **armed 标记（B38 订正）**：凭据文件里加一个布尔位，语义是「**这个会话是本版本 agentd 亲手新建的**」，因此空水位可信地表示「尚无任何回合结束」——对账见到 armed + 空水位 + 尾部已完结必须补发，这正是 B38 头号场景（第一个回合死在断连窗口，水位天然为空，旧判定会把它误当「存量任务升级」吞掉）。新建会话时与 fresh 模式置 true；reattach / cold-recovery 沿用盘上已有值——legacy 任务因此保持 unarmed，升级保护完整（见 §8.1 断言 1/2）

**这不是发明**：claudecode 的 `proc.json.Offset` 就是同一个东西，它正是 claudecode 免疫的原因。本节做的是把那个已被验证的机制补给另外三个。

### 3.3 对账的定义

有了水位，对账有了确定定义：

> **取回水位之后的会话内容 → 交给既有的回合分类 → 补发 → 前进水位**

分类**复用** `internal/executor/turn` 里既有的那套（`ParseTrailer` 等），于是 `question` / `result` / 空回合转失败三条路原样还原。

**这一条是本设计的核心断言**：以提问收尾的回合，对账后必须变成一张**提问工单**，而不是一条假的「做完了」。若对账一律合成 `result`，审核者会以为任务完成，实际模型在等他回答——任务换个姿势继续冻死。

**回合边界的判定（B38 Task9 订正）**：对账必须判清「取回的尾部消息是否真是回合终态」，不能只靠 `completed` 时间戳——opencode 一个用户回合会产多条 assistant 消息，工具调用各自成条、各自带 `completed`，executor 死亡后会话冻结在纯工具消息上，只凭 `completed != 0` 会把它误判成终态、补出假事件（比冻死更糟）。判据见代码 `reconcileTurnEnded`（六行顺序判定，命中即停）：

| # | 判据 | 判定 | 依据 |
|---|---|---|---|
| 1 | `CompletedMS == 0` | 未结束 | 消息未 finalize（在飞或 completed=null 冻结） |
| 2 | `Finish == "stop"` | 已结束 | 自然结束（无 tool part） |
| 3 | `ErrorName == "MessageAbortedError"` | 已结束 | 会话被 abort 而终（finish 缺席） |
| 4 | `ToolStatus == "error"` | 已结束 | 工具被拒/报错而终——14/14 实测零反例，且对齐实时路径 `rejectedTurnQuestion` |
| 5 | `ToolStatus == "completed"` | 未结束 | 真·回合中途冻结 |
| 6 | 兜底（无 tool、无 error、finish 缺席/其它） | 已结束 | 窄兜底：finish=unknown 实测为真实终态 |

**`finish` 只当正向结束标记用**（`stop` ⇒ 结束），不能反过来当「未结束」判据（`tool-calls` 既可能是中间消息也可能是被拒而终）。

---

## 4. 触发点

三个触发点，全部调同一个 `Reconcile` 方法：

| 触发点 | 位置 | 说明 |
|---|---|---|
| 热重连 / 冷恢复成功后 | 各 adapter `Resume` 末尾 | `mode=fresh` **不调**——新会话没有「错过的进展」可言，且水位已清零 |
| 连接重连 | `onReconnect` 回调内 | agentd 活着、只是 SSE/WS 抖了一下。opencode 的 `SubscribeEvents(ctx, onEvent, onReconnect)` 已带该回调（`api.go:391`） |
| 审核者手动 | `Manager.ReconcileTask` → `handoff resume`（§5） | adapter 未实现接口时如实回「不支持」 |

**为什么连接重连也在内**：它和 agentd 重启是同一个根因（断连期间无重放）。`onReconnect` 挂钩点现成，增量成本近零。只做重启那一条，等于明知同一个缝只堵一半——而中途断连没有重启日志这类明显痕迹，更难被发现。

---

## 5. 人工出口：扩展 `handoff resume`

### 5.1 为什么扩 `resume` 而不新开命令

一条真实的用户行为证据：B38 取证时，审核者撞上冻死任务，**第一反应就是敲 `handoff resume`**，得到「没有卡在半路的应答，无需恢复」。

`cmd/resume.go` 的 `Short` 原文是「恢复卡死的任务」，括号里的「重投未送达的应答」是**机制**不是职责。对账是同一职责的第二种机制。让审核者先自行诊断「我这次是丢应答还是丢终态」才能选对命令，是把实现细节漏给了用户。

### 5.2 扩展后的行为

1. 先走既有路径——有未送达的应答就重投，**行为完全不变**
2. 没有未送达应答、且任务在 `running`/`waiting_answer` → 调 `Reconcile`

   其余状态**不进对账**，如实说明而不是静默返回成功：`pending` 尚未启动、`waiting_review` 本就是待审核终态（该走 `continue`/`done`）、`completed`/`failed` 已终结。这与既有 `resume` 对非卡死任务回「无需恢复」的语气一致。
3. 结论三选一，都如实写进恢复报告：

   | 结论 | 动作 | 报告 |
   |---|---|---|
   | 对上了 | 补发事件，任务按事件自然迁移（`result`→`waiting_review`，`question`→`waiting_answer`） | 说明补了什么 |
   | 会话确实还在忙 | 不动状态 | 写明；若顺带重新上报了权限请求，明确告知审核者下一步是去批那张工单，不是强制收口 |
   | adapter 不支持对账 | 不动状态 | 如实说明，提示 `--force` |

4. `--force`：仍然把上面整套跑完（好让事件里留下真实结论），然后**无论结论如何**把任务收口到 `waiting_review`，追加一条事件写明**人工强制收口、未经 executor 确认**

### 5.3 `--force` 的风险与护栏

风险：executor 其实还在跑，收口后 `continue` 会往一个忙碌会话里塞指令。

护栏只有两条——那条事件，和报告里的警告文案。**不加更硬的拦截**：更硬的拦截就是 `stop`（杀掉 executor），而这个场景的全部意义恰恰是**保住会话**以便 `continue` 续接。

---

## 6. 并发、幂等与失败语义

### 6.1 幂等

| 事件类 | 去重机制 | 是否新增机制 |
|---|---|---|
| 权限请求 | 稳定 `PermissionID` + manager 现有 ticket 去重（`internal/executor/executor.go:16` 的包级约定） | 否，现成 |
| 回合终态 | 持久化水位（§3.2） | 是，本设计新增 |

### 6.2 与实时事件的竞态

对账跑在 `Resume` 末尾和 `onReconnect` 内，此时订阅**已经重建**，理论上对账与一条新到的实时事件可能同时描述同一个回合终态。

**约束**：两条路都必须在同一把锁（各 adapter 已有的 `turnMu`）下完成「比对水位 → 补发 → 前进水位」，**不能拆成两步**。拆开就是典型的 check-then-act 竞态，会补出两条终态。

### 6.3 失败语义

| 失败 | 处置 |
|---|---|
| 对账查询失败（HTTP/RPC 报错、超时） | 不改任何状态，WARN 带 cause，向调用方返回错误。**自动触发路径不因对账失败而失败**——`Resume` 照常返回 `Alive=true`，`onReconnect` 照常继续消费 |
| 水位写盘失败 | WARN，不回滚已补发的事件。后果是下次可能重复补发同一条终态：事件表多一条重复记录，状态机 `waiting_review → waiting_review` 会被 `ErrBadTransit` 挡掉只留一条 WARN。可接受，但 CLI 报告里必须看得见 |
| 任务已归档 / 任务目录不存在 | 返回「无需对账」，不报错 |

**「自动触发路径不因对账失败而失败」这条是硬要求**：否则一次网络抖动会把一个本来能恢复的任务判成不可恢复，比 B38 本身还糟。

---

## 7. 探针（实现前置）

三个 adapter 里只有 opencode 部分实证过。探针在 devbox 上跑真实回合、人为掐断连接、抓真实报文，形态参照 B28 那份 spike（`docs/superpowers/plans/2026-08-09-permission-payload-probe.md`）。

**不按 schema 名字推断**——B28 的 spike 一口气推翻了四处这样的推断（`turn/start` 实为异步、workspace-write 不拦 `/tmp`、沙箱拒网不产工单、`CODEX_HOME` 隔离不掉内置插件）。

### 7.1 opencode

- ✅ **已实证（可行）**：`GET /session/:id/message` 取得最后一条 assistant 消息，带 `completed` 时间戳与 `error`（B38 取证时直接查 HTTP API 拿到）
- ✅ **已实证（不可行）**：**悬而未决的权限请求捧不回来**。`internal/executor/opencode/adapter.go:604-613` 记着一次更早的 spike 结论——`GET /session/{id}/message` 的 tool part 只有 `callID` **没有权限 id**，而 `RespondPermission` 要求真实 id、**伪造即 404**。所以对 opencode 而言，工单就算建出来，审核者批了也送不回 executor

  > **这条改变了 opencode 的对账形态**：`Pending` 恒为 0，权限那半边退为「检测 + 如实播报」——若消息尾部呈现「在等一个工具决策」的形态，报告里说明「断连窗口内可能有丢失的权限请求，opencode 无法查询重建，请 `handoff attach` 查看或 `--force` 收口」。**不建假工单**：一张批了也送不回去的工单，比没有工单更糟。
  >
  > 这条 spike 结论写在代码注释里而非 backlog 里，本 spec 的 brainstorm 阶段没读到，是在 writing-plans 核准签名时才撞见的。教训记在这里：**降级告警的注释里往往埋着已经做过的探针结论**。
- ❓ 水位用消息 id 还是 `completed` 时间戳（唯一剩下的 opencode 待验项，可与实现同批验）

  > **已定（实现选型，8/10 真机验证通过）**：水位用**消息 id**。依据是 spec §2.2 的不变量——一个断连窗口内至多跨越一个回合边界，因此「最后一条 assistant 消息 id 与水位不同」无歧义地等于「有一个新的已完结回合没被消费」，不需要任何时间序假设，也不怕两条消息落在同一毫秒。`completed` 时间戳只用作「回合是否已完结」的判据（`CompletedMS==0` = 仍在进行），不进水位。

### 7.2 grok

- ❓ **`session/load` 到底重不重放历史 `session/update`**。ACP 协议文档说会，但没实测过。若重放，**热重连路径现在就可能在产生重复事件**——这是独立于 B38 的既有正确性问题，探针要一并回答，结论若为「重放」则本设计对 grok 的形态要重新考虑（可能不需要主动查询，只需要去重）
- ❓ ACP 有无「查会话历史 / 当前状态」的调用
- ❓ 悬而未决的 `session/request_permission` 断连后是否可查、会不会自行重发

### 7.3 codex

- ❓ app-server 有无列 thread items 的方法
- ❓ rollout 落在 `~/.codex/sessions/**`，能否直接读盘取回。B28 的 spec 已把「rollout 在用户级目录、进程重启后 thread 仍在盘上」记为 codex 相对 grok 的**结构性优势**，这里正好用上
- ❓ 悬而未决的 `requestApproval` 断连后是否可查

### 7.4 探针的产出直接决定范围

某个 adapter 拿不到需要的信息，它就**不实现** `Reconciler`，走 §5 的人工出口，并在本 spec 回填**为什么拿不到**。

范围收缩不是失败——把「查不了」写清楚，比塞一个永远返回「没事」的空实现诚实得多。

---

## 8. 测试与验收

### 8.1 单测（每个实现了 `Reconciler` 的 adapter 一套，用伪造的会话查询）

| # | 断言 |
|---|---|
| 1 | **armed + 空水位 + 尾部已完结 → 补发 1 条，水位前进**（B38 头号场景：第一个回合死在断连窗口，水位天然为空） |
| 2 | 未 armed（legacy）+ 空水位 + 尾部已完结 → 补 **0** 条，只认基线（升级保护） |
| 3 | armed + 水位 == 尾部 msg.ID → 补 **0** 条（幂等，已送达过） |
| 4 | 会话仍在忙（`completed==0` 未 finalize）→ 补 0 条，不改状态 |
| 5 | 查询失败 → 补 0 条、返回 error，而 `Resume` 仍然成功 |
| 6 | **以提问收尾的回合被还原成 `question` 而不是 `result`** ← §3.3 的核心断言 |
| 7 | 悬而未决的权限被重新上报，manager 侧不出第二张工单 |
| 8 | **冻结在纯工具消息尾部（`finish=tool-calls` + tool `completed`）→ 补 0 条**（B38 Task9：executor 死亡后会话冻结，不得补假终态） |
| 9 | **被拒/工具报错而终（tool `status=error`）→ 补发成 `question`**（对齐实时路径 `rejectedTurnQuestion`） |
| 10 | `finish=stop` / `finish=unknown` 的真实终态 → 补发（主流终态与窄兜底各一） |

### 8.2 agentd / CLI 层

| # | 断言 |
|---|---|
| 7 | `resume` 在无未送达应答时转入对账；`--force` 在对账判「忙」时仍收口并留下人工强制事件 |
| 8 | adapter 未实现 `Reconciler` 时报告如实说明，不伪装成「对账过了」 |

### 8.3 真机（devbox）

- **复现 B38 原始现场**：回合进行中 kill agentd → 重启 → 自动对上 → 任务进 `waiting_review` → `continue` 真的能续接并产出提交
- **中途断连**：agentd 不动，掐掉 SSE/WS → 对账补回
- **权限丢失**：断连期间产生权限请求 → 对账重新上报 → 工单只有**一张**
- **`--force`**：造一个对账判不出来的现场 → 收口成功 → 事件写明人工强制

**实测证据（8/10，devbox 旁挂实例 127.0.0.1:7779、独立 datadir `/tmp/b38-e2e/data`、独立 repo `/tmp/b38-e2e/repo`，与生产 7777 零交集；执行者 opencode，模型 opencode-go/deepseek-v4-flash）：**

- **armed + 空水位**（B38 头号场景，spec §8.1 断言 1）：dispatch 任务 `60a9bbf5-791c-43c1-be74-5c85af336d0a`（plan2，13 个实现小步），20s 时 kill agentd，黑洞窗口内轮询 opencode HTTP API 直至回合真正完结（`completed=1786365242255` 非零、提交 `615919f` 已落地），重启后日志：`对账完成：已补发断连期间丢失的终态 ... event=result armed=true` → `恢复后对账完成 ... trigger=startup emitted=1`；任务离开 `running` → `waiting_review`，事件流出现补发的 `completed`（commit `615919ffa5b0c002294f74fdca17e4c964219799`）。随后 `continue` 产出第二个真实提交 `4d40079`（README 含 `B38-SECOND`），证明补发后会话仍可用。
- **armed + 水位落后**（spec §8.1 断言 1 的另一形态）：任务 `9a9c28b5-95e8-4d25-b65f-07c7505b70ea` 回复第一问后 kill agentd，回合在窗口内完结（尾部 `msg_febac60000011aZCBXvgXeTsYz`、completed=1786365390806），盘上水位 `msg_feba122a2001Smu8LwcJu7zEA2`，两者不同；重启后 `对账完成：已补发断连期间丢失的终态 ... event=result armed=true` → `emitted=1` → `waiting_review`。
- **幂等**（spec §8.1 断言 3）：同一次重启里，`c0888dd7`（前一轮场景、终态早已送达）`对账结论：终态已送达过，无需补发` → `emitted=0`；对已对过账的 `b2c0b8f1` 再跑 `handoff resume`，报告 `emitted=0`，事件流无重复 `completed`。
- **以提问收尾的回合还原成 question**（spec §3.3 核心断言，§8.1 断言 6）：任务 `a059b32a-0e25-486e-bec1-a5806056b4cd`（planq2，两个只有审核者能定的具体值），模型提问后 reply 只回答一个值、故意留第二个，随即 kill agentd；模型的反问回合在窗口内完结（尾部 `{"ask":"收到，保留份数=7。请提供备份目标目录（绝对路径）。"}`、completed=1786366380965），重启后 `对账完成：已补发断连期间丢失的终态 ... event=question note="补回了一条断连期间丢失的提问" armed=true` → `emitted=1` → `任务状态迁移 from=running to=waiting_answer reason=question`；任务出现一张可回答的 pending question 工单（`8d7c43fb`），`reply --answer` 后模型真的续接（进入实现阶段并触达权限门）。
- **六行判据回归（8/11，B38 Task9 修复后）**：dispatch 任务 `744d936f-3504-4502-8380-0131a55491a0`（plan2），20s kill agentd，黑洞窗口内轮询直至回合真正完结（`completed=1786390706461` 非零、提交 `d0a5de6` 落地），重启后 `对账完成：已补发断连期间丢失的终态 ... event=result armed=true` → `emitted=1` → `waiting_review`——**六行判据不砍掉 B38 已验能力**。再次以 `1f097c18-9b56-47cb-8843-af5f5617844b` 复验（含 abort 分支改动后）：同样 `event=result armed=true emitted=1` → `waiting_review`，幂等侧 `60a9bbf5` 仍 `emitted=0`。
- **冻结尾部不补假终态（8/11，B38 Task9 修正版）**：派发工具密集任务后**只杀 agentd、serve 不动**（执行者靠 Setsid 活过，任务 `19718807-7c47-4c71-b753-ecc7d2098288`）——这正是「回合中途重启」的常见形态。基线（serve 存活）尾部 `completed=None`（在飞），重启后热重连对账：`对账结论：回合仍在进行，不补发 ... reason=unfinalized` → `turn_ended=false emitted=0`，任务保持 `running`、无新终态事件。**六行判据确实在 agentd 全链路里被执行且落点正确**（reason= 行可见）。早期「同时杀 serve 与 agentd」的两轮（`dfa2e4c1`/`2fc17ee3`）未达新判据——运行时对账（executor 已不在）先接管把任务落 failed，属既有设计（启动恢复 `Cold=false` 不冷拉起），已弃用该验证方式。
- **abort 而终补发成 question（8/11，B38 Task9）**：单测 `TestReconcileAbortedTurnEmitsQuestionNotResult`（真机夹具 `msg_fec00880…`，卡 2h 后 abort 解开的设计消息）钉死——abort 是人工救援而非任务失败，补发 question 带正文，`MessageAbortedError` 是本机唯一出现过的 error 形态。

**真机覆盖范围（8/11，B38 Task9 六行判据，如实分侧记账）**：
- **否定侧**（回合未结束不补发）：真机已验（任务 `19718807-7c47-4c71-b753-ecc7d2098288`，只杀 agentd、serve 靠 Setsid 活，热重连对账 `reason=unfinalized` → `emitted=0`、任务保持 running）。覆盖 row1/row5 的「不补发」行为。
- **补发侧**（回合真结束补得回）：真机已验（任务 `1f097c18-9b56-47cb-8843-af5f5617844b`，kill agentd 后回合在窗口内自然完结，重启后对账 `对账完成：已补发断连期间丢失的终态 ... event=result armed=true emitted=1` → `waiting_review`）。该运行补发的消息 `msg_fed3d8b6…` 经 DB 核对为 `finish="stop"`、completed 非零、无 error——**命中 row2（stop）**。即补发侧最主流的 row2 已全链路真机验证。
- **row3/row4（abort / 工具被拒 → question）**：仅单测覆盖（`TestReconcileAbortedTurnEmitsQuestionNotResult`、`TestReconcileEmitsRejectedToolEnd`），真机未造出「断连窗口内刚好落在 abort/被拒终态上」的现场。这两行的产出是 question 而非 result，语义错在测试里可被断言拦截。

**未覆盖**（如实记录）：
- **中途断连**（SSE 抖动而非 agentd 重启）：真机上未能稳定制造出「agentd 不动、SSE 断流」的现场（`onReconnect` 回调触发需掐断 serve 与 agentd 之间的连接，本机旁挂实例难以无副作用模拟）。该触发点已由单测覆盖（`TestResumeTriggersReconcile`/`TestReconcileAfterRecoverySwallowsError`），真机一格空缺。
- **`--force` 收口**：造「对账判不出」的现场需让 executor 真的还在忙，本机复现成本过高，未做真机验收；该路径已由单测覆盖（`TestRecoverStuckForceTransitsToReview`）。
- **权限重新上报**：opencode 已实证捧不回来（spec §7.1），无真机可验。

### 8.4 回归锚

把对账代码抄回旧形态复跑，§8.3 第一条必须**变红**。红→绿证据需由审阅者独立复核，不采信执行者自述（沿用 B35/B36 的验收纪律）。

---

## 9. 可观测性

新增/变更的事件与日志：

| 类型 | 内容 |
|---|---|
| 日志 INFO | 对账开始（task、触发点：startup/reconnect/manual） |
| 日志 INFO | 对账结论（`turn_ended`、`emitted`、`pending`、水位前进前后） |
| 日志 WARN | 对账查询失败（带 cause）；水位写盘失败（带 cause 与「下次可能重复补发」提示） |
| 事件 | 补发的 `question` / `result` / `permission`——**与实时事件同形，不加标记**。补发的痕迹留在日志与 `resume` 报告里，不污染事件语义 |
| 事件 | `--force` 收口时追加一条，写明人工强制收口、未经 executor 确认、以及对账当时的真实结论 |

**为什么补发的事件不加标记**：加了标记就等于新增语义，下游要判它、`wait` 要过滤它、工单要区分它——那正是 §1.5 第 2 条要避免的。补发的事件在语义上就**是**那条本该实时到达的事件，它只是迟到了。
