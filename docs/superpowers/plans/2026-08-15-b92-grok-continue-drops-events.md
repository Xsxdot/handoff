# B92 grok 续接回合事件被吞 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 grok 的「回合失败」不再被当成「执行终结」——回合失败后 `continue` 能真正续接，事件不再被静默丢弃；万一将来又出现别的关通道路径，`Send` 的守卫把静默吞事件变成明确报错。

**Architecture:** 只改 `internal/executor/grok/`。把承担双重语义的 `emitFailed` 拆成 `emitTurnFailed`（回合级，**不关通道**）与 `emitFatal`（执行级，emit 后关通道），五个 call site 按语义各归其位；`Send` 加一道 `evClosed` 守卫。**不改 manager、不改 watchdog**（避免与正在实施的 B93 冲突）。

**Tech Stack:** Go 1.26 / 标准库

**Spec:** [2026-08-15-b92-grok-continue-drops-events-design.md](../specs/2026-08-15-b92-grok-continue-drops-events-design.md)
**根因报告（先读它，证据都在里面）:** [2026-08-15-b92-failed-not-transiting.md](../notes/2026-08-15-b92-failed-not-transiting.md)

## Global Constraints

- **只改 `internal/executor/grok/` 下的文件。** 不碰 `internal/agentd/`（B93 正在改那里，同时改必然冲突）、不碰 `internal/prochost/`、不碰 `web/`。
- 日志一律 `a.log`，**禁止 `fmt.Printf`**。
- **凭据纪律**：grok 的 `wsURL` 与 `server-key` 含 secret。新增/修改的日志行**绝不能打印 token 值或 wsURL**；需要标识时用 task id、session id、port。
- 新增导出/未导出方法都要写 doc 注释（参数、返回、注意事项）；拆语义这种「为什么」必须写进注释，否则下一个人会把它们合回去。
- 每个 Task 结束即 commit。**不要 `git push`。**

## 已核实的既有事实（照抄，不要再去猜）

| 事实 | 坐标 |
|---|---|
| `emitFailed(r, reason)` = `a.emit(result{OK:false})` + `r.closeEvents()` | `internal/executor/grok/adapter.go:350-355` |
| `closeEvents()` 幂等，置 `r.evClosed = true` 并 `close(r.evCh)`，全程持 `r.emitMu` | `adapter.go:357-367` |
| `emit(r, ev)` 持 `r.emitMu`，`r.evClosed` 为真时打 Debug 并返回 false（**这就是事件被静默丢弃的那一行**） | `adapter.go:329-345` |
| `runState` 的相关字段：`evCh chan executor.AdapterEvent` / `emitMu sync.Mutex` / `evClosed bool` / `stopping bool` | `adapter.go:87-93` |
| `Send` 在 `lookup` 后直接 `r.cli.CallAsync("session/prompt", …)`，成功则 `go a.awaitTurn(r, resCh)`；`CallAsync` 失败时返回包 `executor.ErrTaskNotRunning` 的错误 | `adapter.go:247-267` |
| `finishTurn` 两条失败分支 | `adapter.go:443`（`res.Err`）、`adapter.go:451`（`stopReason != end_turn`） |
| `onClosed` 两条失败分支（连接已断，运行态作废，且 `defer a.dropIf(r.taskID, r)`） | `adapter.go:542`、`adapter.go:550` |
| 看门狗判 serve 死亡 | `internal/executor/grok/resume.go:275` |
| `Stop` 自己调 `closeEvents()` + `drop`，**不经 `emitFailed`** | `adapter.go:293-295` |
| opencode / claudecode 只在订阅循环或流退出时关通道，**不因回合失败关** | `opencode/adapter.go:747-768`、`claudecode/adapter.go:444` |

**五个 call site 的归属（这是本计划的全部要点，不要自己重新分类）：**

| call site | 归属 | 判据 |
|---|---|---|
| `adapter.go:443` 回合异常终止（含 ACP -32603） | **turn** | serve 进程还活着（标本实测 port 50007 探活存活） |
| `adapter.go:451` `stopReason != end_turn` | **turn** | 同上；这正是标本 `398259b7` 踩的那条 |
| `adapter.go:542` 权限应答通道中断 | **fatal** | 在 `onClosed` 里，连接已断 |
| `adapter.go:550` ACP 连接断开 | **fatal** | 同上 |
| `resume.go:275` serve 进程已死亡 | **fatal** | 进程没了 |

---

### Task 1: 拆分 `emitFailed` 为回合级与执行级两个出口

**Files:**
- Modify: `internal/executor/grok/adapter.go`（`emitFailed` 拆成两个；`:443`、`:451` 改调 turn 版；`:542`、`:550` 改调 fatal 版）、`internal/executor/grok/resume.go:275`（改调 fatal 版）
- Test: `internal/executor/grok/adapter_turnfail_internal_test.go`（新建）

**Interfaces:**
- Produces:
  ```go
  // 回合失败：投出 result{OK:false}，**不关闭事件通道**
  func (a *Adapter) emitTurnFailed(r *runState, reason string)
  // 执行终结：投出 result{OK:false} 后关闭事件通道（即原 emitFailed）
  func (a *Adapter) emitFatal(r *runState, reason string)
  ```
  `emitFailed` 这个名字**删掉**，不保留别名——留着它，下一个人还会往上挂新的 call site，而它的语义正是本次要消灭的那个含糊。

- [ ] **Step 1: 写失败的测试**

新建 `internal/executor/grok/adapter_turnfail_internal_test.go`。先读同目录既有的 `*_internal_test.go`（如 `onclosed_drop_internal_test.go`），照它构造 `Adapter` + `runState` 的方式写，**不要自己造第二套 fixture**：

```go
func TestTurnFailureKeepsEventChannelOpen(t *testing.T) {
	// why：回合失败 ≠ executor 完了。以前 emitFailed 一律 closeEvents，于是协调者
	// continue 时 Send 在同一个 runstate 上开新回合，新回合的一切事件在 emit 里
	// 被 evClosed 短路静默丢弃，任务卡 running 到 2h 看门狗（B92 根因报告 §3）
	a, r := newTestAdapterRunState(t) // 名字以既有 fixture 为准

	a.emitTurnFailed(r, "回合非正常收尾 stopReason=cancelled")

	ev := <-r.evCh
	if ev.Result == nil || ev.Result.OK {
		t.Fatalf("应投出 result{OK:false}，实际 %+v", ev)
	}
	r.emitMu.Lock()
	closed := r.evClosed
	r.emitMu.Unlock()
	if closed {
		t.Fatal("回合失败不该关闭事件通道")
	}
	// 通道仍可用：续接回合的事件必须能送出去
	if !a.emit(r, executor.AdapterEvent{Type: "progress", Text: "续接回合的第一条"}) {
		t.Fatal("回合失败后 emit 应仍然成功——这正是 B92 被吞掉的那条路")
	}
	if next := <-r.evCh; next.Text != "续接回合的第一条" {
		t.Fatalf("续接事件没送达，实际 %+v", next)
	}
}

func TestFatalFailureClosesEventChannel(t *testing.T) {
	// why：连接断了、进程死了，这条运行态就真的不可用了，必须关通道让
	// manager 的 mediate 循环退出走对账
	a, r := newTestAdapterRunState(t)

	a.emitFatal(r, "ACP 连接断开: 测试")

	ev := <-r.evCh
	if ev.Result == nil || ev.Result.OK {
		t.Fatalf("应投出 result{OK:false}，实际 %+v", ev)
	}
	if _, ok := <-r.evCh; ok {
		t.Fatal("fatal 之后事件通道应已关闭")
	}
}

func TestFatalIsIdempotent(t *testing.T) {
	// why：断开处置、看门狗判死两条 fatal 路径可能同时到达。一次性语义原本由
	// closeEvents 承担，拆分后必须确认它仍在 fatal 这一侧完整保留
	a, r := newTestAdapterRunState(t)
	a.emitFatal(r, "第一次")
	a.emitFatal(r, "第二次") // 不许 panic（send on closed channel）
	if ev := <-r.evCh; ev.Result == nil || ev.Result.FailReason != "第一次" {
		t.Fatalf("只有先到者生效，实际 %+v", ev)
	}
	if _, ok := <-r.evCh; ok {
		t.Fatal("第二次不该再投出事件")
	}
}

func TestTurnFailureThenFatalStillCloses(t *testing.T) {
	// why：回合失败后 serve 才真的死掉，是完全可能的顺序。此时 fatal 仍须关通道
	a, r := newTestAdapterRunState(t)
	a.emitTurnFailed(r, "回合失败")
	<-r.evCh
	a.emitFatal(r, "serve 死了")
	<-r.evCh
	if _, ok := <-r.evCh; ok {
		t.Fatal("fatal 之后通道应关闭")
	}
}
```

`newTestAdapterRunState` 若既有测试里没有等价物，本步写一个：造 `&Adapter{log: slog.Default()}` 与 `&runState{taskID: "T1", sessionID: "S1", evCh: make(chan executor.AdapterEvent, 8)}`。缓冲区要够大（`emit` 在通道满时会丢弃并打 Warn，缓冲太小会让用例假红）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -run 'TestTurnFailure|TestFatal' ./internal/executor/grok/`
Expected: 编译失败——`emitTurnFailed` / `emitFatal` 未定义。

- [ ] **Step 3: 拆分实现**

把 `adapter.go:346-355` 的 `emitFailed` 换成两个函数：

```go
// emitTurnFailed 产出一个**回合级**失败终局，**不关闭事件通道**。
//
// 参数：reason 为给协调者看的失败原因原文
//
// 为什么不关通道：回合失败 ≠ 这个 executor 完了。serve 进程还活着，协调者
// 一个 continue 就能接着干——那正是 continue 的用途。以前这里一律 closeEvents，
// 于是 Send 在同一个 runstate 上开新回合，新回合的一切事件在 emit 里被 evClosed
// 短路静默丢弃，manager 的 mediate 循环也早已随通道关闭退出，任务停在 running
// 直到 2h 看门狗落 stalled（而 stalled 只唤醒不修复）。B92 根因报告的对照组：
// 3 个 grok 任务 failed 后全哑火，3 个 opencode 任务 failed 后全被 continue
// 救活——差异就在这一行，opencode/claudecode 都不因回合失败关通道。
//
// 一次性语义不受影响：跨回合的去重不需要（finishTurn 每回合只调一次，
// 两条分支互斥），与 fatal 路径之间的去重仍由 evClosed 承担。
func (a *Adapter) emitTurnFailed(r *runState, reason string) {
	a.log.Error("grok 回合失败", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
		Result: &executor.Result{OK: false, SessionID: r.sessionID, FailReason: reason}})
}

// emitFatal 产出**执行级**失败终局并关闭事件通道。
//
// 参数：reason 为给协调者看的失败原因原文
//
// 用于连接已断或进程已死——这条运行态真的不可用了，必须关通道让 manager 的
// mediate 循环退出走对账。
//
// 一次性语义：断开处置与看门狗判死两条路径可能同时到达，closeEvents 的幂等
// 保证只有先到者生效，后到者的 emit 被 evClosed 丢弃，不会双重终结。
func (a *Adapter) emitFatal(r *runState, reason string) {
	a.log.Error("grok 执行终结", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
		Result: &executor.Result{OK: false, SessionID: r.sessionID, FailReason: reason}})
	r.closeEvents()
}
```

然后按上面「五个 call site 的归属」表逐个改：`adapter.go:443`、`:451` → `emitTurnFailed`；
`adapter.go:542`、`:550`、`resume.go:275` → `emitFatal`。

**改 `adapter.go:533` 那段注释**：它现在写「事件通道随 emitFailed 一起关掉」，
函数名变了要跟着改成 `emitFatal`，否则注释指向一个不存在的函数。

- [ ] **Step 4: 确认没有遗漏的 call site**

Run: `grep -rn "emitFailed" internal/`
Expected: **零命中**（含测试文件——既有测试若引用了 `emitFailed`，按同一张归属表改成对应的新名字，**不要**加个别名糊过去）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test -run 'TestTurnFailure|TestFatal' ./internal/executor/grok/ && go test -count=1 ./internal/executor/grok/`
Expected: 新用例全 PASS，**既有用例原样通过**。既有的 `onclosed_drop_internal_test`、
`watchdog_internal_test` 钉的都是 fatal 路径，不该被动到——**若它们变红，停下来判断
是归属分错了还是测试写死了旧行为，不要直接改绿**。

- [ ] **Step 6: Commit**

```bash
git add internal/executor/grok/
git commit -m "fix(grok): 回合失败不再关闭事件通道，拆出 emitTurnFailed / emitFatal（B92 §2.1）"
```

---

### Task 2: `Send` 的 `evClosed` 守卫

**Files:**
- Modify: `internal/executor/grok/adapter.go:247-267`（`Send`）
- Test: `internal/executor/grok/adapter_turnfail_internal_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `emitFatal`
- Produces: `Send` 在事件通道已关闭时返回包 `executor.ErrTaskNotRunning` 的错误，且**不发出 `session/prompt`**。

- [ ] **Step 1: 写失败的测试**

```go
func TestSendRefusesOnClosedChannel(t *testing.T) {
	// why：这是本次修复的安全网。即便将来又冒出某条我们没想到的关通道路径，
	// 这道守卫也会把「静默吞掉一整个回合」变成「continue 当场报错、manager 走
	// 四级恢复阶梯」。B92 花了 2 小时 + 一次人工排查才被发现，代价全部来自静默
	a, r := newTestAdapterRunState(t)
	a.runs = map[string]*runState{r.taskID: r} // 让 lookup 找得到；字段名以实际为准
	a.emitFatal(r, "连接断了")

	err := a.Send(context.Background(), r.taskID, "接着干")

	if err == nil {
		t.Fatal("通道已关闭时 Send 必须报错，不能静默开新回合")
	}
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("必须是 ErrTaskNotRunning（manager 的四级恢复阶梯以它为触发条件），实际 %v", err)
	}
}
```

**注意**：该用例的 `runState` 不能有真实的 `cli`（`r.cli` 为 nil）。守卫必须在
`r.cli.CallAsync` **之前**返回，所以守卫写对了就不会解引用 nil；写错了会 panic
——这本身就是一条有效判据，但为了报错清楚，用例里**不要**给 `r.cli` 赋一个假对象。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -run 'TestSendRefusesOnClosedChannel' ./internal/executor/grok/`
Expected: panic（nil `cli` 被解引用）或 err 为 nil —— 两种都算红，说明守卫不存在。

- [ ] **Step 3: 加守卫**

在 `Send` 的 `lookup` 之后、`a.log.Info("grok 续接回合", …)` **之前**插入：

```go
	// 事件通道已关闭 = 这条运行态已被 fatal 路径判死。此时开新回合是最坏的
	// 结果：session/prompt 发得出去、模型真的会跑，但产出的一切事件都会在
	// emit 里被 evClosed 短路丢弃，任务停在 running 直到 2h 看门狗（B92）。
	//
	// 返回 ErrTaskNotRunning 而不是自定义错误：manager 的四级恢复阶梯以
	// errors.Is(err, ErrTaskNotRunning) 为触发条件，会尝试冷恢复重建运行态
	// ——正是这种情况下该做的事。一个明确的错误哪怕语义不完美，也比无声
	// 无息好一个数量级。
	r.emitMu.Lock()
	closed := r.evClosed
	r.emitMu.Unlock()
	if closed {
		return fmt.Errorf("任务 %s 的事件通道已关闭，运行态已终结: %w", taskID, executor.ErrTaskNotRunning)
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -count=1 ./internal/executor/grok/`
Expected: 全绿。

- [ ] **Step 5: Commit**

```bash
git add internal/executor/grok/
git commit -m "fix(grok): Send 拒绝在已关闭的事件通道上开新回合（B92 §2.4）"
```

---

### Task 3: 回归与变异测试

**Files:** 无新改动，只验证。

- [ ] **Step 1: 全量回归**

Run: `go build ./... && go vet ./... && go test -count=1 ./...`
Expected: 全部包 PASS，0 FAIL。

- [ ] **Step 2: 变异测试两条，各自单独红**

一次只改一处，改完还原。

```bash
# 变异 1：让 emitTurnFailed 又去关通道（还原成 B92 的 bug）
grep -n "func (a \*Adapter) emitTurnFailed" internal/executor/grok/adapter.go   # 定位
# 在该函数体末尾手工加一行 r.closeEvents()，然后：
go test -run 'TestTurnFailureKeepsEventChannelOpen' ./internal/executor/grok/   # 期望 FAIL
git checkout internal/executor/grok/adapter.go

# 变异 2：摘掉 Send 的守卫
# 手工把 Task 2 Step 3 那个 if closed 块注释掉，然后：
go test -run 'TestSendRefusesOnClosedChannel' ./internal/executor/grok/         # 期望 FAIL（或 panic）
git checkout internal/executor/grok/adapter.go
```

两条做完后再跑一次 `go test -count=1 ./internal/executor/grok/` 确认还原干净。

- [ ] **Step 3: 把变异结果写进 ledger**

两条变异各自的实际输出（FAIL 的用例名，或 panic 堆栈首行）逐条记进 ledger。
**只写「做了变异测试」不算。**

- [ ] **Step 4: 真机复验的交接说明**

spec §4 第 6 条要求真机复验，**不由你执行**——它要 mac-02 上先 `grok login` 恢复凭据
（那台的 `~/.grok/auth.json` 缺失，所有 grok 任务派不出去，这是 B98，与本次无关），
再派一个 grok 任务故意拒权限触发 `stopReason=cancelled`，然后 `continue` 看事件通不通。

在 ledger 末尾写一段交接说明，包含：改了哪几个 call site、怎么用 `grep -rn "emitFailed"`
验证没有遗漏、真机复验该怎么构造那个 `stopReason=cancelled` 回合。写完即视为完成。

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: B92 回归与变异测试记录"
```
