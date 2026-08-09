# handoff 任务运行态对账与恢复出口 —— 设计

> 合并 backlog **B20**（归档时 executor 回收失败无兜底也无信号）、**B21**（executor
> 空回合后任务卡在 running，信号迟到 2 小时且无恢复出口）、**B24**（executor tmux
> 会话丢失后 waiting_review 任务成为孤儿）。
>
> 三条是同一个根因的三种表现：**任务状态机记录的状态，与 executor 的实际存活性之间
> 没有一致的对账机制，失配之后也没有恢复出口。**

日期：2026-08-09
状态：待实现

---

## 1. 背景与目标

### 1.1 三条 backlog 的现场

| ID | 现场 | 后果 |
|---|---|---|
| B20 | `done` 归档时 `Adapter.Stop` 返回 `ErrTaskNotRunning`（agentd 曾重启、adapter 内存运行态已丢），代码按设计只打一条 ERROR 继续归档 | tmux 会话 `handoff-46e84025` 与其 `opencode serve`（port 49234）孤儿存活 11.5 小时，直到人工 `tmux kill-session`。且**无任何信号**——worktree 清理失败会发 progress 事件提示人工，executor 停不掉却完全静默 |
| B21 | opencode 连做 7 个回合工具调用后，最后一步 `step-finish` 的 `reason=unknown`、tokens 全 0（供应商流中断，reasoning 截在半句「Loop opening the fif」），会话随即 idle。adapter 只打了 `WARN idle 但回合无文本，跳过分类` | 任务 `356368f2` 在 `state=running` 下静止 1 小时。审核者要等 `StallTimeout`（默认 2h）才收到 stalled；而 stalled 按设计只唤醒不改状态，`continue` 要求 waiting_review、`resume` 只重投未送达应答，两条 CLI 出口都够不着 |
| B24 | tmux server 被 `pkill -f "handoff agentd"` 误杀（该模式匹配到了 tmux server 自身保留的 argv），整个 server 连同三个 executor 会话一起没了 | 任务永久停在 waiting_review：`continue` 报 409「运行态已丢失，请重新派发」。分支上的成果只能靠 `pull` 抢救，后续修改只能新开任务 |

### 1.2 B24 的一处更正

B24 原记「`done` 也走不通」。**复核代码后不成立**：`Manager.Done` 只要任务处于
`waiting_review` 就能归档——它调 `adapterFor` + `ad.Stop`，失败只打 ERROR 不影响归档
（`manager.go:655`）。当时真正走不通的只有 `continue`。

因此 B24 的实际缺口是「**没法在原分支上续接**」，不是「没法归档」。本设计据此收窄。

### 1.3 目标

1. **失配立刻可见**：executor 终结这一事实，无论从哪个口子到达，都产出事件并把任务
   推到审核者能操作的状态，不再依赖 2 小时兜底。
2. **原地续接**：`waiting_review` 的任务即使 executor 进程已死，`continue` 也能续上
   ——优先带着原会话上下文续，实在续不上才降级并明确告知。
3. **回收有兜底、有信号**：运行态丢失不等于资源没了，按确定性命名兜底回收；回收不掉
   时留事件，与 worktree 清理失败的信号对称。

### 1.4 非目标

- 不新增周期性存活探活。见 §2.2。
- 不改状态机（仍是 6 状态，迁移表不动）。
- 不解决 B22（`handoff wait` 重放历史事件）。它与本设计同时触及事件订阅层，但需求形态
  独立，等本设计落地后再单独评估。

---

## 2. 根因：一个事实，三个到达口

### 2.1 「执行终结」的三个到达口

「executor 已经不在了」这一个事实，可以从三个口子到达 agentd。现状只有第一个接了处置：

| 到达口 | 触发时机 | 现状 |
|---|---|---|
| ① agentd 启动探活 | agentd 重启 | `RecoverOnStartup` 处置完整：追加 failed 事件 → 作废挂起工单 → 迁 waiting_review（`watchdog.go:222-243`） |
| ② **adapter 事件通道关闭** | executor 死在 agentd 活着期间 | **`mediate` 循环退出后什么都不做**，只打一条「中介循环结束」（`manager.go:897`）。任务永久停在 running |
| ③ 审核者动作撞上失配 | `continue` / `reply` / `done` | 各自散着处理：`continue` 直接 409；`RecoverStuck` 有 `abandonToReview`（`manager.go:1478`），是①处置逻辑的第二份拷贝 |

三个 adapter 在自己的进程/连接死亡时都会关闭事件通道（`closeEvents`），所以到达口②是
**最常见**的一条路——也正是唯一一条完全没接处置的路。B21 现场的 opencode serve 其实
还活着（只是回合空转），而 B24 现场的进程真死了，两者都从②过，都没人接。

### 2.2 为什么不加周期性探活

一个直觉的方案是让 `RunWatchdog` 每轮对 running/waiting_answer/waiting_review 任务做
一次存活探测。本设计**不采用**：

- 到达口②是**事件驱动**的——通道关闭这个信号本身就是「executor 终结」的精确通知，
  精度和及时性都优于轮询，且零额外开销。
- 周期性探活会引入一类新的误判：探活抖动（HTTP 瞬时失败、tmux 短暂无响应）导致把活着
  的 executor 判死。三个 adapter 自己的看门狗已经各自处理了抖动吸收（grok 的
  `watchdogFailThreshold=3`、opencode 同规格），在 manager 层再做一遍是重复且更糟。
- `RunWatchdog` 的 `StallTimeout` 保留原职责不变：它兜的是「executor 活着但长时间无
  产出」这个通用兜底，与本设计正交。

### 2.3 「恢复」从布尔判据变成一道阶梯

三个 adapter 的 `Resume` 目前**全是热重连**：第一步就要求进程还活着（grok 要
`proc.Alive()` 端口探活通过，claude 要 tmux 会话在且无退出哨兵，opencode 要
has-session + HTTP），为假即 `return false`。落库的 `task.ExecutorSession` 根本没机会
用上。

这不是能力缺失，是判据写死成了热重连。**实测证据（2026-08-09，devbox）**：三个
executor 的会话数据都持久化在磁盘上，进程死了它还在。

| executor | 会话落盘位置 | 冷载入手段 |
|---|---|---|
| claude | `~/.claude/projects/<slug(cwd)>/<session-id>.jsonl`（未做 HOME 隔离，会话在用户真实 home） | `-r, --resume <session-id>`；帮助文本明写可与 `--print` 配合 |
| grok | `<taskDir>/grokhome/sessions/<urlencode(cwd)>/<session-id>/` | ACP `session/load`（二进制符号表实证存在，adapter 已在调用） |
| opencode | `~/.local/share/opencode/opencode.db`（全局 sqlite） | `POST /session/<id>/prompt_async` 直接打已有 id |

devbox 上的直接实证：
`~/.handoff/tasks/0fb81b4c-.../grokhome/sessions/%2FUsers%2Fsycm%2F.handoff%2Fworktrees%2F0fb81b4c/019fe553-afa3-72e2-9bc7-03a7947c0120`
——任务级会话目录完整躺在磁盘上。

于是恢复分四级：

```
1. 运行态在 adapter 内存里   → 直接用（现状）
2. 进程还活着                → 热重连（现有实现）
3. 进程死了、会话数据在盘上  → 冷恢复：重启进程 + 载入会话，上下文完整
4. 会话数据也没了/载不进     → 真降级：新会话，把本条指令当首个 prompt
```

第 3 级是本设计的主体：它把 B24 的「只能新开任务」变成「原地续接、上下文不丢」。
第 4 级是兜底，实测下来应当很少触发。

---

## 3. 契约变更

### 3.1 共享数据类型（`internal/executor/resume.go`，新建）

```go
// ResumeReq 是恢复请求。Cold 决定是否允许把死掉的 executor 进程重新拉起来。
type ResumeReq struct {
	TaskID    string
	TaskDir   string // DataDir/tasks/<id>
	RepoPath  string // 任务工作区（worktree 任务为 Workdir），冷恢复的 cwd
	SessionID string // 落库的 task.ExecutorSession
	Cold      bool   // true=允许冷恢复；false=只热重连
}

// ResumeMode 是本次恢复实际走到的级别。
const (
	ResumeModeReattach = "reattach" // 第 2 级：进程还活着，重连
	ResumeModeCold     = "cold"     // 第 3 级：重启进程 + 载入原会话，上下文完整
	ResumeModeFresh    = "fresh"    // 第 4 级：原会话载不进，已新开会话
)

// ResumeOutcome 是恢复结果。
type ResumeOutcome struct {
	Alive     bool   // 是否拿到了可用的运行态
	Mode      string // ResumeMode* 之一；Alive=false 时为空串
	SessionID string // 恢复后实际生效的会话 id（fresh 时是新 id，manager 据此落库）
	Note      string // 一句话结论，供 manager 转成事件文本
}
```

### 3.2 可选能力接口（保持在 `manager.go`，私有）

**对第 2 节口头设计的一处修正**：讨论时说过要把 `restorer` 搬到 `internal/executor`
成为导出契约。落到代码上不该这么做——Go 的惯例是**消费方定义接口**，而 manager 才是
消费方；现有 `restorer` 的 doc 注释也明确记着「把恢复作为可选能力（interface 断言）
既保住核心五动作契约，又让『不支持恢复的 adapter 一律按不存活走 failed 恢复路径』成为
自然语义」。沿用这个既有模式。**只有数据类型进 `internal/executor`**（三方都要引用）。

```go
// restorer 是「重建执行」的可选 adapter 能力（三个真实 adapter 均实现，fake 不实现）。
type restorer interface {
	Resume(executor.ResumeReq) (executor.ResumeOutcome, error)
}

// reaper 是「无内存运行态时按确定性命名兜底回收」的可选 adapter 能力。
//
// 为什么单开一个方法而不是让 Stop 自己兜底：Stop 只拿得到 taskID，拿不到 taskDir
// （proc 信息文件在里面）；给 Stop 加参数会改动五动作核心契约、波及 fake 等全部实现。
type reaper interface {
	Reap(taskID, taskDir string) error
}
```

`volatilePermitter` 不动。

### 3.3 调用方的迁移

`Manager.ResumeTask(taskID string) bool` 的签名保留（`RecoverOnStartup` 的探活闭包契约
是 `func(string) bool`，不改），内部构造 `ResumeReq{Cold: false}`——**启动时不冷恢复**，
理由见 §5.4。

---

## 4. 恢复的触发策略：按需，不预热

冷恢复只由 `Continue` 触发，`RecoverOnStartup` 与到达口②一律只做热重连。

**为什么**：agentd 重启时若有 10 个任务的 executor 已死，急着冷恢复就等于凭空拉起
10 个没人跟它说话的 executor 进程——白烧机器，还可能烧掉配额。而 `continue` 时审核者
手里正好有一条指令要送，会话拉起来立刻有用。

这条策略的直接后果：启动恢复与到达口②把任务落到 `waiting_review` 之后，任务处于
「会话数据在盘上、进程不在」的静止态；审核者一 `continue`，阶梯就把它接上。

---

## 5. 组件详解

### 5.1 `reconcileExecutorGone`——对账的唯一实现

**现状**：「executor 没了怎么收尾」这段逻辑有**两份拷贝**，措辞与顺序还不完全一致：

- `watchdog.go:222-243`（`RecoverOnStartup`）：追加 failed 事件 → `recoverTransit` →
  `VoidPendingTickets` → `hub.Publish`
- `manager.go:1478`（`abandonToReview`）：`VoidPendingTickets` → 追加 failed 事件 →
  `transitToReview` → `hub.Publish`

**改动**：抽成 `internal/agentd` 的包级函数，三个到达口共用。

```go
// reconcileExecutorGone 是「executor 已不在」这一事实的唯一收尾实现：
// 作废挂起工单 → 追加 failed 事件（reason 说明来源）→ 迁 waiting_review → 广播。
//
// 参数：
//   - reason: 失配来源的人话说明，直接进 failed 事件的 fail_reason，审核者据此
//     区分「agentd 重启后 executor 已不在」「executor 事件流终结」「恢复操作发现
//     executor 已不在」三种现场
//
// 返回：
//   - 收尾后的任务状态（调用方需要回给 CLI 时用）
//
// 注意：
//   - 对 waiting_review / completed / failed 任务是**空操作**：前者本就是待审核终态
//     （追加事件只是噪音），后两者已终结
//   - 工单作废在事件之前：作废失败只记日志不中断，事件是审核者的主要信息来源，
//     必须落
func reconcileExecutorGone(st *store.Store, hub *Hub, taskID, reason string, log *slog.Logger) proto.TaskState
```

`recoverTransit` 的两跳逻辑（`waiting_answer → running → waiting_review`，因为迁移表里
`waiting_answer` 只允许去 running/failed）并入本函数。

### 5.2 到达口②：`mediate` 退出时对账

```go
func (m *Manager) mediate(taskID string) {
	// ...
	for ev := range events {
		m.handleEvent(taskCtx, taskID, ev)
	}
	m.log.Info("中介循环结束", "task", taskID)
	// 事件通道关闭 = executor 终结。若这次关闭不是我们自己发起的（done/stop），
	// 任务此刻还停在 running/waiting_answer，必须对账，否则它会一直停在那里
	// 直到 2h 看门狗（B21 实测：静止 1 小时无任何信号）
	if !m.takeStopping(taskID) {
		m.reconcileExecutorGone(taskID, "executor 事件流已终结（进程退出或连接断开）")
	}
}
```

### 5.3 `stopping` 意图标记——竞态

**这是设计过程中撞出来的一个真竞态，不是假想。** `Manager.Stop` 先调 `ad.Stop()`
（`manager.go:731`）再落 failed（`manager.go:747`）。加了 §5.2 之后：

```
Stop: ad.Stop() ──关闭事件通道──> mediate 退出 ──对账──> 看到 state=running
                                                        → 补 failed 事件 + 迁 waiting_review
Stop: 继续执行 ──> 迁 failed
```

`waiting_review → failed` 在迁移表里合法（`proto.go:131`），所以不会硬失败，但会多出
一条噪音 failed 事件和一次状态抖动（running → waiting_review → failed）。`Done` 同理
（`ad.Stop()` 在 `manager.go:658`，此时状态已是 completed，对账是空操作——所以 `Done`
其实安全，但用同一个标记保持一致，避免以后调整顺序时重新踩进来）。

**方案**：`Manager` 加一个 `stopping map[string]struct{}`（`mu` 保护）。

```go
// noteStopping 标记「接下来这次事件通道关闭是我们自己发起的」。
// 必须在 ad.Stop() 之前调用。
func (m *Manager) noteStopping(taskID string)

// takeStopping 取走并清空标记（取走式：标记的生命周期就是一次主动停止，
// 读一次即失效，否则下一次异常终结会被上一次的主动停止误抑制，真出现
// executor 猝死就没人对账了）
func (m *Manager) takeStopping(taskID string) bool
```

取走式的理由与 grok adapter 的 `takeAskedViaTool` 同源：标记若长期驻留，抑制的就不再
是它本该抑制的那一次。

**为什么不改 `Stop` 的顺序**（先落 failed 再 `ad.Stop()`）：那样 executor 在状态已定型
后仍可能产出事件，各 handler 的「已终结则丢弃」判断会散在更多路径上，风险大于收益。
显式标记是诚实的——它说的正是「这次关闭是我们自己关的」。

### 5.4 `Continue` 接入恢复阶梯

```
transit(running)
  → adapterFor
  → Send()  ──成功──> 返回
      └─ 失败且 errors.Is(err, executor.ErrTaskNotRunning)
          → Resume(ResumeReq{..., Cold: true})
              ├─ err != nil 或 Alive == false
              │    → transitBestEffort(waiting_review)
              │    → 返回带 Outcome.Note 的错误（server 映射 409）
              └─ Alive == true
                   → SessionID 与落库值不同时 SetTaskField("executor_session", ...)
                   → Mode != "reattach" 时追加 progress 事件并广播
                   → 重试 Send 一次（只一次，不循环）
                       ├─ 成功 → 返回
                       └─ 失败 → transitBestEffort(waiting_review) + 返回错误
```

**只重试一次**：重试的前提是「刚刚成功建立了运行态」，这个前提一次就够验证。循环重试
只会在 executor 反复启动失败时放大伤害。

**progress 事件文案**（`Mode` 决定，事件必须落，不能只写日志）：

| Mode | 事件文本 |
|---|---|
| `cold` | `executor 进程已不在，已重启并从磁盘载入原会话 <session-id>，上下文完整` |
| `fresh` | `原会话 <old-id> 已不可载入，已新开会话 <new-id>；上下文从本条指令开始，必要时请在指令中重述背景` |

`fresh` 必须产出事件的理由：上下文断了是审核者**需要知道**的事实——它直接决定下一条
指令要不要重述背景。只写日志等于让审核者在不知情的前提下继续对话。

### 5.5 三个 adapter 的冷恢复

统一形态：`Resume` 在「进程不存活」分支不再直接 `return false`，而是在 `Cold=true` 时
重新拉起进程并载入会话。

#### 5.5.1 grok（改动最小）

现有 `Resume`（`internal/executor/grok/resume.go:42`）：
`ReadServeInfo → proc.Alive() → EnsureAuthLink → DialACP → initialize → session/load`。

改动只在第二步：

```go
mode := executor.ResumeModeReattach
if !proc.Alive() {
	if !req.Cold {
		a.log.Info("serve 已不在且不允许冷恢复，判不可恢复",
			"task", req.TaskID, "port", proc.Port)
		return executor.ResumeOutcome{}, nil
	}
	// 冷恢复：会话数据在 <taskDir>/grokhome/sessions/<urlencode(cwd)>/<session-id>/，
	// 只要 taskDir 在它就在。重起一个 serve（新端口，GROK_HOME 不变）后，
	// 下面的 session/load 原样可用
	newProc, err := StartProc(req.TaskID, req.TaskDir, req.RepoPath) // 与 Start 同路径
	if err != nil {
		// 起不来是可预期现场（配额/凭据过期），按不可恢复处理而非错误
		a.log.Warn("冷恢复重起 serve 失败，判不可恢复", "task", req.TaskID, "cause", err)
		return executor.ResumeOutcome{}, nil
	}
	proc = newProc
	mode = executor.ResumeModeCold
}
```

`DialACP → initialize → session/load` 那段代码**一行不动**。`session/load` 失败时，
`Cold=true` 则降级为 `session/new`（第 4 级），返回 `Mode=fresh` 与新 session id。

`EnsureAuthLink` 在冷恢复路径同样要调（token 刷新期间软链可能已被干掉，见 B26）。

#### 5.5.2 opencode（风险最低）

会话存在全局 sqlite（`~/.local/share/opencode/opencode.db`），`serve` 只是它前面的一层
HTTP。冷恢复：

1. 重起 serve（新端口）
2. `GET /session` 确认 `sessionID` 仍在列表里 → 在则 `Mode=cold`
3. 不在 → `POST /session` 建新会话 → `Mode=fresh`
4. 重建 SSE 订阅（与热重连共用同一段代码）

`Send` 侧无需改动，仍打 `/session/<id>/prompt_async`。

#### 5.5.3 claude（风险最高，需先 spike）

冷恢复要把启动命令从 `--session-id <uuid>`（`proc.go:195`）换成 `--resume <uuid>`。
配套三处：

1. **cwd 必须是原工作区**：会话文件路径按 cwd 编码（`~/.claude/projects/<slug(cwd)>/`），
   传 `req.RepoPath`（manager 已给的 `task.Workdir()`）。
2. **`out.jsonl` 轮转**：冷恢复后是全新的输出流，旧 offset 无意义。把 `out.jsonl`
   重命名为 `out.<n>.jsonl` 保留（诊断价值），新开 `out.jsonl`，`claude.json` 的
   `offset` 归零。
3. **先回收旧 tmux 会话**：窗口 1 的 `tail -f render.log` 会一直吊着会话（现有
   `Resume` 的哨兵分支已经在做这件事，`adapter.go:447`），冷恢复路径同样要做，否则
   会话名冲突。

**未知数**：`--resume` + `--print --output-format stream-json` + fifo 持续喂 stdin 这个
组合能不能成立，帮助文本回答不了。**由 spike 定案**（§9.1）。走不通则 claude 的冷恢复
退到第 4 级（新会话降级），另外两个 adapter 不受影响——这正是把第 4 级留在设计里的
原因，不是为了凑数。

### 5.6 空回合不静默（B21 的信号）

**opencode（确认的静默路径）**：`mapIdle` 在 `strings.TrimSpace(text) == ""` 且不是
「被拒终止」时，只打 `WARN idle 但回合无文本，跳过分类` 后直接 return
（`adapter.go:1269`）。任务停在 running。

改为产出失败结果：

```go
a.log.Warn("idle 但回合无文本，转失败结果交审核者", "task", r.taskID,
	"event", turn.TailRunes(string(raw), 120))
a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.session, Result: &executor.Result{
	OK: false,
	FailReason: "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，" +
		"可 continue 续接重试",
}})
r.clearTurn()
r.captureStartCommit(a)
```

`handleResult` 对 `OK=false` 的既有处置正是我们要的：作废挂起工单 → 追加 failed 事件 →
`transitToReview`（`manager.go:1591-1606`）。任务落 `waiting_review`，`continue` 立刻
可用。

**为什么是 `result{OK:false}` 而不是 `question`**：同文件里「被拒终止」的空回合走的是
`question`（`adapter.go:1262`），因为那个现场**有内容可问**（「我拒了这些权限，接下来
怎么办」）。零文本回合没有任何东西可问，它是一份故障报告——`result{OK:false}` 的语义
才对得上，且 `FailReason` 能把现场写清楚。

**grok / claude 的空文本守卫**：两者的兜底分支在无新提交时会 `emit question` 携带回合
文本（`grok/adapter.go:468`、`claudecode/adapter.go:742`）。文本为空时这会产出一张**空
工单**——审核者收到一个没有内容的问题。加一条守卫：文本空白且无新提交时，同样产出
`result{OK:false}` 而非空 question。三个 adapter 由此对称。

### 5.7 `Reap` 兜底回收与信号对称（B20）

**现状**：`Adapter.Stop` 在无内存运行态时直接返回 `ErrTaskNotRunning`
（如 `opencode/adapter.go:576`），manager 只打一条 ERROR 就继续归档
（`manager.go:659`）。而三个 adapter 的 tmux 会话名都是**确定性**的
`handoff-<id8>`（`claudecode/proc.go:118`、`grok/proc.go:178`、`opencode/proc.go:114`），
proc 信息也都落在 taskDir 里——完全有确定性兜底可用，只是没用上。

**改动 A：adapter 实现 `Reap`**

```go
// Reap 在没有内存运行态时按确定性命名兜底回收 executor 侧资源。
//
// 回收顺序：
//  1. 读 taskDir 下的 proc 信息文件（claude.json / serve.json / …）拿 tmux 会话名
//  2. 文件缺失/损坏时退到确定性命名 "handoff-" + id8(taskID)（与 StartProc 同规则）
//  3. tmux kill-session
//
// 会话本就不存在时返回 nil——目标是「确保它没了」，不是「确保我杀了它」。
func (a *Adapter) Reap(taskID, taskDir string) error
```

**改动 B：manager 侧接线与信号**（`Done` 与 `Stop` 共用一个辅助函数）

```
noteStopping(taskID)
ad.Stop(taskID)
  └─ errors.Is(err, executor.ErrTaskNotRunning) 且 adapter 实现 reaper
       → Reap(taskID, taskDir)
           ├─ nil  → Info 日志「按确定性命名兜底回收成功」
           └─ err  → 追加 progress 事件：
                     "executor 资源可能残留：tmux 会话 handoff-<id8>，
                      请手动 tmux kill-session -t handoff-<id8>"
```

progress 事件这一条是**信号对称**的落点：worktree 清理失败会发事件提示人工
（`manager.go:671`、`manager.go:764`），executor 停不掉却完全静默——B20 现场的孤儿
存活 11.5 小时正是因为没人知道它在。

---

## 6. 并发与幂等约束

这些是实现必须满足的约束，不是建议。

1. **冷恢复的进程拉起必须互斥。** 两条并发 `continue` 会各自撞到
   `ErrTaskNotRunning`，各自调 `Resume(Cold:true)`——若无互斥，会为同一任务起两个
   executor 进程抢同一个会话。adapter 在 `runs` map 上先占位（持 `a.mu` 检查+写入
   占位项）再拉进程，后到者等待占位者的结果或直接返回「恢复进行中」。
2. **`reconcileExecutorGone` 幂等。** 对 waiting_review/completed/failed 是空操作，
   重复调用不产生重复事件。到达口①②③可能对同一任务先后触发。
3. **`stopping` 标记取走式。** 见 §5.3。
4. **`Reap` 幂等。** 会话不存在返回 nil。
5. **冷恢复不重建 worktree。** `taskDir` 或 `RepoPath` 已不存在时直接
   `Alive=false` + Note 说明。重建工作区是 `Dispatch` 的职责，越界重建会让归档过的
   任务诈尸。

---

## 7. 失败语义

| 情形 | 行为 |
|---|---|
| `Resume` 返回 error | 按不可恢复处理（保留现状语义：探活闭包契约只有 bool，原因已由 Error 日志留痕） |
| 冷恢复起进程失败 | `Alive=false`，**不返回 error**——「起不来」是可预期的现场（配额、凭据过期），不是程序错误 |
| `session/load` 失败且 `Cold=true` | 降级第 4 级（新会话），`Mode=fresh` |
| `Continue` 阶梯全走完仍失败 | `transitBestEffort(waiting_review)` + 返回错误，server 映射 409，错误体带 `Outcome.Note` |
| `reconcileExecutorGone` 中作废工单失败 | 只记日志，继续追加事件——事件是审核者的主要信息来源，必须落 |
| `reconcileExecutorGone` 中追加事件失败 | 记 Error 并返回当前状态，不迁移（迁移了却没事件 = 审核者看到状态变化却不知原因） |
| `Reap` 失败 | 追加 progress 事件提示人工，不影响归档/中止本身 |

---

## 8. 可观测性

### 8.1 新增/变更的事件

不新增事件类型（`proto` 的 EventType 不动）。复用：

| 事件 | 何时 | payload 要点 |
|---|---|---|
| `failed` | 到达口②对账 | `fail_reason` = "executor 事件流已终结（进程退出或连接断开）" |
| `failed` | 空回合 | `fail_reason` = "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试" |
| `progress` | 冷恢复/降级成功 | §5.4 的两条文案 |
| `progress` | `Reap` 失败 | "executor 资源可能残留：tmux 会话 handoff-\<id8\>，请手动 tmux kill-session -t handoff-\<id8\>" |

### 8.2 关键节点日志

按 `instrumenting-code` 的要求，以下节点必须有日志（全部用 `slog`，不用 `fmt.Printf`）：

- `reconcileExecutorGone` 进入（task、来源 reason、当前 state）与退出（收尾后 state、
  作废工单数）
- `mediate` 退出时的对账分支：`stopping=true`（主动停止，跳过）/ `false`（执行对账）
- `Continue` 的阶梯每一跳：`Send 首次失败`（cause）、`进入冷恢复`、`恢复结果`
  （mode、alive、session_id 变化）、`重试 Send 结果`
- 三个 adapter 的冷恢复：`进程不存活，进入冷恢复`（旧端口/会话名）、`新进程就绪`
  （新端口/会话名）、`会话载入结果`（mode）
- `Reap`：`按确定性命名兜底回收`（会话名、来源=proc 文件/命名推导）与结果
- 空回合：`idle 但回合无文本，转失败结果交审核者`（保留现有的事件尾部上下文字段）

---

## 9. 测试

### 9.1 Spike（plan 的第一个 task，前置于全部实现）

三个 executor 各跑一遍，验证冷恢复是否成立：

1. 起一个真会话，让它产出可辨识的上下文（例如让模型记住一个随机口令）
2. 杀掉 executor 进程/tmux 会话
3. 用落库的 session id 冷恢复（手工命令即可，不必先写 adapter 代码）
4. 发一条指令：**让模型复述第 1 步的口令**

**通过判据：模型能复述出口令。** 日志里写 `mode=cold` 不算数——只有模型答得出原会话
里的内容，才证明上下文真的回来了。

spike 结论写进本 spec 的实测附录（§11）。claude 若不通过，其冷恢复实现退到第 4 级。

### 9.2 单元测试

| 测试 | 断言 |
|---|---|
| `reconcileExecutorGone` 表驱动 | running/waiting_answer → 产出 failed 事件 + 工单作废 + 落 waiting_review；waiting_review/completed/failed → 零事件零迁移 |
| `mediate` 退出对账 | fake adapter 关闭事件通道后，running 任务落 waiting_review 并有 failed 事件 |
| `stopping` 标记 | 先 `noteStopping` 再关通道 → **无**对账事件；`takeStopping` 取走后第二次关闭 → 有对账事件（防止标记长期驻留） |
| `Continue` 阶梯 | fake 实现 `restorer`：Send 首次返 `ErrTaskNotRunning` → 断言 `Resume` 被调用且 `Cold=true`、Send 被重试一次、`Mode=fresh` 时产出 progress 事件 |
| `Continue` 阶梯失败路径 | `Resume` 返回 `Alive=false` → 任务回落 waiting_review，错误可被 `errors.Is(store.ErrBadTransit)` 之外的路径识别为 409 |
| 空回合（opencode） | `mapIdle` 喂空文本 → 断言 emit `result{OK:false}` 且 `FailReason` 非空 |
| 空文本守卫（grok/claude） | 兜底分支喂空文本且无新提交 → 断言 emit `result{OK:false}` 而非空 question |
| `Reap` | 无运行态时 `Stop` 返 `ErrTaskNotRunning` → 断言 `Reap` 被调用；`Reap` 失败 → 断言产出 progress 事件 |
| 冷恢复互斥 | 并发两次 `Resume(Cold:true)` → 断言进程只被拉起一次 |

### 9.3 真机验收（devbox，三个 executor 各一遍）

1. 派发任务跑到 `waiting_review`，记下 `executor_session`
2. `tmux kill-session -t handoff-<id8>` 杀掉 executor
3. **B21/B24 的信号**：事件流里立刻出现 failed 事件（不等 2h），`show` 显示
   `waiting_review`
4. **B24 的续接**：`handoff continue <task> --instructions "复述你上一回合做了什么"`
   → 冷恢复日志 `mode=cold` → **模型答出上一回合的真实内容** → 产出真实提交
5. **B20 的兜底**：重启 agentd（丢掉内存运行态）后 `handoff done <task>` →
   日志「按确定性命名兜底回收成功」→ `tmux ls` 无 `handoff-<id8>` 残留
6. **B20 的信号**：令 `Reap` 必然失败（如让 tmux 不在 PATH 中）→ 事件流里出现
   带会话名的 progress 提示

---

## 10. 明确的范围外

- **B22（`handoff wait` 重放历史事件）**：与本设计同时触及事件订阅层，但需求形态独立
  （服务端游标语义 + CLI `--since=now`），等本设计落地后单独评估——恢复出口定型后，
  B22 的形态可能会变。
- **B25（`TestNilApproverKeepsCurrentBehavior` 对负载敏感）**：sleep 式断言改条件轮询，
  不值得走 spec，随手修。
- **周期性存活探活**：理由见 §2.2。
- **状态机扩展**：不新增状态、不改迁移表。
- **`RunWatchdog` 的 `StallTimeout` 语义**：保持不变，它兜的是「executor 活着但长时间
  无产出」这个通用兜底，与本设计正交。

---

## 11. 实测附录

（spike 完成后回填：三个 executor 的冷恢复实测结论、命令、模型复述证据。）
