# 回收复核与日志可读性（B47 + B41 + B48）设计

## 1. 范围与动机

三条互不相干的小缺陷，共同点是「**系统在自欺**」——它报告了一个它并没有验证过的结论，或者把真信息淹没在自己的副本里：

| 条目 | 一句话 |
|------|--------|
| B47 | `prochost.Kill` 杀完不复核就 `return nil`，「进程没死」这个信号既产生不出来，也到不了人工提示分支 |
| B41 | codex `TestPendingCallsFailWhenConnectionDies` 偶发翻红——测试脚手架自己有竞态，不是被测代码的问题 |
| B48 | 同一份 git stderr 在一次同步失败里被打印两遍，真正的排障信息被自己的副本淹没 |

放在一个 spec 里做，是因为三条都小、都不改架构、都能各自独立验收；分三次派发的开销大于收益。

**不在范围内**：进程组连坐覆盖不到的逃逸后代（自己 daemon 化的 MCP server、agent 起的 `npm run dev`、docker 容器）。B47 的备注里已记为「已知未排除项，无复现即无事可做」，本次不碰——本次交付的正是「真出这种事时能被发现」的前提。

## 2. B47：杀完复核，并让信号到得了人

### 2.1 两个缺陷，不是一个

**缺陷一：`Kill` 报告的是「信号发出去了」，不是「进程死了」。**

[`prochost.go:96`](../../../internal/prochost/prochost.go) 的 doc 写「仅当**确认**还活着但杀不掉时返回错误」，而实现在 `killGroup(h.PID)` 之后直接 `return nil`，从没做那次确认。`killGroup` 是 `syscall.Kill(-pid, SIGKILL)`（[`platform_unix.go:93`](../../../internal/prochost/platform_unix.go)），SIGKILL 不可捕获不可忽略、`ESRCH` 又被当成功吞掉——于是 `Kill` 几乎不可能返回非 nil。

**缺陷二：光修缺陷一，信号仍然到不了人。**

`stopExecutor`（[`reconcile.go:71`](../../../internal/agentd/reconcile.go)）对任何非 `executor.ErrTaskNotRunning` 的错误只 `m.log.Error` 就 return；那条「回收不掉就留 progress 事件提示人工」的分支只挂在 `ErrTaskNotRunning` + `Reap` 失败的路径上。B20 现场（孤儿存活 11.5 小时）的形态恰恰是前者。

### 2.2 复核用 `Alive`，不用 `kill(pid, 0)`

`Alive(h)` 的判据是 `LockPath` 上的排他锁被占用（[`lock.go:83`](../../../internal/prochost/lock.go) 的 flock 探测）。锁由**内核在进程死亡时释放**，因此：

- 不存在 pid 复用误判——这正是 `Kill` 自己的注释警告过的历史教训（旧实现 300 条成功命令误杀 114 次）；
- 不需要任何清理代码配合，也不受 shim 是否收尸影响。

`kill(pid, 0)` 反而会把「pid 被复用给了别的进程」误报成「还活着」。**定为：复核一律走 `Alive`。**

### 2.3 复核的形状

`killGroup` 之后有界退避轮询 `Alive(h)`：SIGKILL 是异步生效的，立刻探一次必然假阳性。

- 轮询总时长上限 **1s**，退避序列 `10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 370ms`（累计 1s）。
- 任一次探到 `!Alive(h)` 即成功返回 nil。
- 走完仍 `Alive` → 返回包装了新哨兵 `ErrStillAlive` 的错误。

**为什么是 1s 而不是更久**：`Kill` 处在归档/中止的同步路径上，它变慢等于 `handoff done` / `handoff stop` 变慢。1s 足以覆盖 SIGKILL 的正常生效窗口；超过 1s 还活着的，本来就该交给人和后台重试，而不是让审核者对着终端干等。

### 2.4 新哨兵 `prochost.ErrStillAlive`

```go
// ErrStillAlive 表示已发出 SIGKILL 且复核窗口走完，进程组仍然存活。
// 与「信号发送失败」区分开：后者是系统调用出错，前者是进程真的没死——
// 只有后一种才值得惊动人。
var ErrStillAlive = errors.New("进程组仍然存活")
```

哨兵必须能经 `errors.Is` 穿过 adapter 抵达 `stopExecutor`。四个 adapter 的 `Stop` 现状**各不相同**，已逐一核对：

| adapter | 现状 | 哨兵能否抵达 | 本次要做的 |
|---|---|---|---|
| opencode（`adapter.go:526`） | `Alive` 则保留运行态 + `go reapRetained` + `fmt.Errorf("kill serve: %w", kerr)` | ✅ 能 | 不改。见 §2.5 |
| claudecode（`adapter.go:392`） | `return kerr` 裸抛（在 `a.drop` 之前返回，运行态保留） | ✅ 能 | 不改 |
| codex（`adapter.go:377`） | 打 Error + `a.emit` 一条 progress，然后**继续 drop 并 `return nil`** | ❌ 到不了 | 改为把错误上抛 |
| grok（`adapter.go:286`） | `_ = r.proc.Kill()`，错误**整个丢弃** | ❌ 到不了 | 改为把错误上抛 |

**codex 那条自发的 progress 事件一并去掉**，人工提示由 `stopExecutor` 单一持有。两个理由：

1. 与 B48 同一条原则——一段面向人的信息只能有一个持有者，否则审核者看到的是同一件事的两份措辞不同的副本；
2. adapter 侧的 `a.emit` 走的是事件通道，而 `stopExecutor` 在调用 `ad.Stop` **之前**已经 `noteStopping`（会关掉事件通道），这条 emit 能不能落库取决于 mediate 有没有退干净——是个竞态。`stopExecutor` 的 `st.AppendEvent` + `hub.Publish` 是确定落库的。**用可靠的那条替换不可靠的那条，不是删掉一个提示。**

grok / codex 改成上抛后，它们的 `Stop` 在这种情况下不再走到 `a.drop`——与 claudecode / opencode 的既有形态一致（保留运行态才有机会再回收），不是新语义。

### 2.5 一个意外收获：opencode 已有一条休眠的正确路径

`Adapter.Stop`（[`opencode/adapter.go:526-541`](../../../internal/executor/opencode/adapter.go)）里已经写好了完整机制：`kill` 失败 → 若 `Alive` 则**保留运行态**并 `go a.reapRetained(r)` 后台周期重试（带次数上限），再把错误 `%w` 上抛。

**它至今一次都没跑过**，因为 `Kill` 从不返回错误。修好 2.3 等于把这条已经建好的路激活——这不是新增机制，是让既有机制第一次真正工作。这一点必须在实现时确认而不是假设：opencode 的这条分支要有测试覆盖。

### 2.6 `stopExecutor` 补人工提示

在 `!errors.Is(err, executor.ErrTaskNotRunning)` 的分支里增加判定：

```
errors.Is(err, prochost.ErrStillAlive) → 除 log.Error 外，追加 progress 事件并 Publish
其余错误                                 → 保持今天的语义（只记日志）
```

**为什么只对这一种错误加事件**：其余 Stop 失败五花八门（ctx 取消、内部状态不一致），全部发事件等于把审核者淹了；而「确认还活着但杀不掉」是唯一一种「不惊动人就会留下长期孤儿」的错误。

事件文案沿用既有那条的形状（给的是**下一步做什么**，不是出了什么错）：

```go
Text: fmt.Sprintf("executor 进程可能残留（已发 SIGKILL 但复核仍存活），"+
    "请先 handoff status 确认，再 handoff stop %s 回收（原因：%v）", taskID, err)
```

### 2.7 顺带修掉的时序面

`stop` 里 `stopExecutor` → 1ms 后就 `RemoveManagedWorktree`，而 SIGKILL 异步生效，删 worktree 不等进程真死。复核落地后，`Kill` 不确认死亡就不返回，这条顺序**自动**获得保证——不需要额外加同步代码。

## 3. B41：修测试脚手架的竞态

### 3.1 诊断

`codex.Dial` 在**客户端**握手完成即返回；而 `registerFakeConn` 跑在**服务端 handler goroutine** 里、位于 `websocket.Accept` 之后（[`appserver_test.go:70`](../../../internal/executor/codex/appserver_test.go)）。两者没有任何同步。

若测试 goroutine 先跑到 `closeFakeConns(srv)`，登记表还是空的 → 一条连接都没关 → 客户端的挂起请求不会以错误终结 → 撞 3s 超时，报「挂起请求永久悬挂」。

这与观测吻合：偶发一次、11 次复跑不重现、`-race` 下更容易撞（race detector 拉长了调度间隙）。

### 3.2 修法

`closeFakeConns` 在取快照前**有界等待至少一条连接完成登记**（上限 2s，短间隔轮询）；等满仍为空则 `t.Fatal`——「一条连接都没建立」本身就是测试前提被破坏，静默返回等于把断言变成许愿。

因此 `closeFakeConns` 需要 `*testing.T` 参数，其全部调用点一并更新。

### 3.3 必须先证伪

本条的诊断是**推断**，不是复现。实现时必须先把它变成可复现的失败：

1. 在 `registerFakeConn` 调用前注入 `time.Sleep(200 * time.Millisecond)`；
2. 跑 `TestPendingCallsFailWhenConnectionDies` → **必须稳定翻红**，且失败信息是「挂起请求永久悬挂」；
3. 应用 3.2 的修法（延迟仍在）→ **必须转绿**；
4. 移除注入的延迟，确认工作区干净。

**第 2 步若不翻红，说明诊断错了——停下来报告，不许绕过去直接改。**

## 4. B48：谁持有那段 git 原文

`localsync.go:86` 的 `log().Error("本地同步失败", …, "stderr", …)` 记一次（含 git 原文），错误再经返回值冒泡到 [`cmd/wait.go:135`](../../../cmd/wait.go) 的 `fmt.Fprintln(cmd.ErrOrStderr(), "自动同步跳过:", err)` 打第二遍。两条都走 stderr，紧挨着出现。

**定为：呈现层持有面向人的那一份，叶子层降级。**

`localsync.go:86` 的 `Error` 降为 `Debug`。理由与仓库既有纪律一致（`internal/store` 全层不打日志，靠 `%w` 带上下文、由调用方带业务上下文记录）：`localsync` 是被 CLI 调用的库，CLI 已经把这段原文打给人看了，库再打一遍只是噪声。降级而非删除，是因为 agentd 侧若将来复用这个库，Debug 仍留得住线索。

错误返回值本身**不动**——`cmd/wait.go` 那份人可读输出的内容全靠它。

## 5. 测试策略

| 条目 | 测试 |
|------|------|
| B47 复核 | `prochost` 包新增：①锁已释放 → `Kill` 立即 nil 且不发信号 ②起一个真实的短命子进程持锁 → `Kill` 后 `Alive` 转 false、返回 nil ③**用一个持锁但不响应 SIGKILL 的替身**验证复核失败路径 → 返回的错误 `errors.Is(err, ErrStillAlive)` 为真 |
| B47 哨兵穿透 | 四个 adapter 各一条断言：`Kill` 返回 `ErrStillAlive` 时，`Stop` 返回的错误仍能 `errors.Is` 命中（codex / grok 是本次新增行为，必须有测试；opencode / claudecode 是既有行为，补测防回归） |
| B47 人工提示 | agentd 侧：伪造一个 `Stop` 返回 `ErrStillAlive` 的 adapter → `stopExecutor` 必须追加 progress 事件；返回其它错误 → 必须**不**追加（两条都要，只测前一条会让「只对这一种错误加事件」这个决定失去保护） |
| B41 | 见 3.3 的四步证伪流程 |
| B48 | 不新增测试。这是日志级别调整，行为无变化；由 `go test ./... -count=1` 保证没碰坏别的 |

第 ③ 项的「持锁但不响应 SIGKILL 的替身」是本次唯一有技术难度的测试：SIGKILL 在类 Unix 上不可拦截，真进程做不到。做法是**不造真进程**——把 `Kill` 里的存活判定与 `killGroup` 抽成包内可替换的函数变量（仅测试替换），用一个恒返回「还活着」的桩驱动复核走满退避窗口。这是为了测试可达性而做的最小接缝，不改变生产路径的行为。

## 6. 验收闸门

```
gofmt -l .
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./cmd/ ./internal/agentd/ ./internal/store/ ./internal/prochost/ ./internal/executor/codex/ -count=1
GOOS=windows GOARCH=amd64 go build ./...
```

B41 是 flake，单跑一次不算数：`go test -race ./internal/executor/codex/ -count=20` 必须全绿。

`-race` 闸门比平时多带 `./internal/prochost/` 与 `./internal/executor/codex/` 两个包——这次动的正是它们。

## 7. Windows 影响

`internal/prochost` 的平台分裂是 `platform_unix.go` / `platform_other.go`（非 unix 侧目前是 not-implemented 占位，B37 搁置中）。本次改的是 `Kill` 里**平台无关**的复核逻辑，`Alive` 同样平台无关。因此**不新增平台分支、不碰 `platform_other.go`**，`GOOS=windows GOARCH=amd64 go build ./...` 与既有的 `windows_build_test.go` 必须继续通过。B37 不因本次改动而推进。

## 8. 不做的事

- 不追进程组逃逸的后代（见 §1）。
- 不改 `reapRetained` 的重试间隔与上限——那套参数已有 why 注释，本次只是让它第一次被触发。
- 不给 `Kill` 加可配置超时。1s 是常量，需要调再说；提前参数化只会多一个没人配的配置项。
- 不动 B22（wait 重放历史事件）——那条是 cursor 语义问题，与本次三条无关。
