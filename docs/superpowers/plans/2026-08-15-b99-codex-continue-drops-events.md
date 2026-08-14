# B99 codex 续接回合事件被吞（B92 的同型缺陷）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 B92 在 grok 上的修复原样应用到 codex——回合失败不再关闭事件通道，`Send` 加 `evClosed` 守卫。

**Architecture:** 与 B92 完全同构。设计理由**不在本文件重复**，见 [B92 spec](../specs/2026-08-15-b92-grok-continue-drops-events-design.md)（尤其 §2 拆语义、§2.4 守卫为何比拆分更重要）与 [B92 根因报告](../notes/2026-08-15-b92-failed-not-transiting.md)。**动手前先读那两份。** 本文件只写 codex 特有的部分：六个 call site 的归属，以及与 grok 的三处形态差异。

**Tech Stack:** Go 1.26 / 标准库

## 0. 这条缺陷是怎么发现的（不是实测，是结构推定）

B92 的根因报告做了对照组：3 个 grok 任务 failed 后全哑火，3 个 opencode 任务
failed 后全被 `continue` 救活。**codex 不在对照组里**——样本里恰好没有「codex
回合失败后被 continue」的任务，不是因为它没问题。

审核者在验收 B92 时对 `emitFailed` 做残留检索，发现 `internal/executor/codex/`
有一份结构完全相同的实现：

- `codex/adapter.go:441-446` 的 `emitFailed` 同样以 `r.closeEvents()` 收尾
- `codex/adapter.go:569` / `:579` 两条**回合级**失败同样调它
- `codex/adapter.go:341-348` 的 `Send` 同样 `lookup` 后直接在原 runstate 上
  `startTurn`，`startTurn`（`:280`）内也**没有**任何 `evClosed` 判断

所以「回合失败 → 通道关 → continue 开新回合 → 事件全被 `emit` 短路丢弃 →
任务卡 `running` 到 2h 看门狗」这条链在 codex 上同样成立。**这是结构推定，
不是实测复现**——验收时也不要求真机复现，要求的是单测把行为钉死。

## Global Constraints

- **只改 `internal/executor/codex/` 下的文件。** 不碰 `grok/`（B92 已修完并合入
  main，再动它只会制造冲突）、不碰 `internal/agentd/`（B93 可能正在改那里）。
- 日志一律 `a.log`，**禁止 `fmt.Printf`**。
- **凭据纪律**：codex 的连接参数含 secret。新增/修改的日志行绝不能打印 token 值；
  要标识就用 task id、thread id、port。
- 新函数写 doc 注释；「为什么拆开」必须写进注释，否则下一个人会把它们合回去。
- 每个 Task 结束即 commit。**不要 `git push`。**

## 六个 call site 的归属（本计划的全部要点，不要自己重新分类）

| call site | 现文案 | 归属 | 判据 |
|---|---|---|---|
| `adapter.go:569` | `回合失败: …` | **turn** | `finishTurn` 的 `status=="failed"`；app-server 进程还活着，`continue` 正该救它 |
| `adapter.go:579` | `回合被中断（非 handoff 发起）: …` | **turn** | 同上，`status=="interrupted"` 且 `stopping` 为假；进程还在 |
| `adapter.go:662` | `权限应答通道中断（N 个未决请求作废）…` | **fatal** | 在 `onClosed` 里，连接已断 |
| `adapter.go:670` | `codex 连接断开: …` | **fatal** | 同上 |
| `adapter.go:837` | `codex 登录态失效，请在 executor 机重新 codex login` | **fatal** | **该处既有注释自己写着「登录态失效重试一万次也不会好」**——判 turn 的话，`continue` 会开一个立刻又失败的新回合，变成人肉重试循环。判 fatal 让运行态作废、错误明确交回给人去 `codex login`，这才是那句注释想要的效果 |
| `resume.go:268` | `codex app-server 进程已退出…` | **fatal** | 进程没了 |

## 与 grok 的三处形态差异（照抄会写错的地方）

1. **字段名**：codex 的会话标识是 `r.threadID`（不是 grok 的 `r.sessionID`）。
   `emit` 里的 `SessionID:` 字段仍然叫 `SessionID`，值取 `r.threadID`——照抄
   `emitFailed` 现有实现即可，不要改字段名。
2. **`Send` 的形态**：grok 的 `Send` 自己调 `CallAsync`；codex 的 `Send`
   （`:341-348`）转手给 `startTurn(r, text)`。**守卫加在 `Send` 里**（`lookup`
   之后、`a.log.Info("codex 续接回合", …)` 之前），**不要加在 `startTurn` 里**
   ——`startTurn` 也被首轮启动路径调用，那时通道当然没关，加在那里是给热路径
   平白多一把锁。
3. **`interrupted` 分支有 `stopping` 前置判断**（`:571-577`）：`stopping` 为真时
   直接 `return`，压根不产出失败。那段**一字不动**，只把它后面那行 `emitFailed`
   换成 `emitTurnFailed`。

---

### Task 1: 拆分 `emitFailed` 并按归属表改六个 call site

**Files:**
- Modify: `internal/executor/codex/adapter.go`、`internal/executor/codex/resume.go:268`
- Test: `internal/executor/codex/adapter_turnfail_internal_test.go`（新建）

**Interfaces:**
- Produces：
  ```go
  func (a *Adapter) emitTurnFailed(r *runState, reason string) // 不关通道
  func (a *Adapter) emitFatal(r *runState, reason string)      // emit 后关通道
  ```
  `emitFailed` 这个名字**删掉**，不保留别名——留着它下一个人还会往上挂新 call site，
  而它的语义含糊正是本次要消灭的东西。

- [ ] **Step 1: 先读 grok 那份已合入的实现当参照**

Run: `git log --oneline -5 -- internal/executor/grok/adapter.go && git show 9618f02f2 --stat`
然后读 `internal/executor/grok/adapter.go` 里的 `emitTurnFailed` / `emitFatal` 与
`Send` 的守卫，以及 `internal/executor/grok/adapter_turnfail_internal_test.go`。
**本 Task 就是把那一套搬到 codex**，注释里的「为什么」可以沿用同样的论证，但
必须按 codex 的实际情况改写（进程叫 app-server、会话标识是 threadID）。

- [ ] **Step 2: 写失败的测试**

新建 `internal/executor/codex/adapter_turnfail_internal_test.go`。先读同目录既有
`*_internal_test.go` 的 fixture 构造方式并复用，**不要造第二套**：

```go
func TestTurnFailureKeepsEventChannelOpen(t *testing.T) {
	// why：回合失败 ≠ codex app-server 完了。以前 emitFailed 一律 closeEvents，
	// 于是 Send→startTurn 在同一个 runstate 上开新回合，新回合的一切事件在 emit
	// 里被 evClosed 短路静默丢弃，任务卡 running 到 2h 看门狗。这是 grok 上实测
	// 到的 B92，codex 结构相同（见本计划 §0）
	a, r := newTestAdapterRunState(t) // 名字以既有 fixture 为准

	a.emitTurnFailed(r, "回合失败: 测试")

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
	if !a.emit(r, executor.AdapterEvent{Type: "progress", Text: "续接回合的第一条"}) {
		t.Fatal("回合失败后 emit 应仍然成功——这正是被吞掉的那条路")
	}
	if next := <-r.evCh; next.Text != "续接回合的第一条" {
		t.Fatalf("续接事件没送达，实际 %+v", next)
	}
}

func TestFatalFailureClosesEventChannel(t *testing.T) {
	a, r := newTestAdapterRunState(t)
	a.emitFatal(r, "codex 连接断开: 测试")
	if ev := <-r.evCh; ev.Result == nil || ev.Result.OK {
		t.Fatalf("应投出 result{OK:false}，实际 %+v", ev)
	}
	if _, ok := <-r.evCh; ok {
		t.Fatal("fatal 之后事件通道应已关闭")
	}
}

func TestFatalIsIdempotent(t *testing.T) {
	// why：断开处置与进程判死两条 fatal 路径可能同时到达。一次性语义原本由
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

func TestAuthRefreshIsFatal(t *testing.T) {
	// why：登录态失效判 turn 的话，continue 会开一个立刻又失败的新回合，变成
	// 人肉重试循环——而该处既有注释自己写着「登录态失效重试一万次也不会好」。
	// 判 fatal 让运行态作废、错误明确交回给人去 codex login
	a, r := newTestAdapterRunState(t)
	a.emitFatal(r, "codex 登录态失效，请在 executor 机重新 `codex login`")
	<-r.evCh
	if _, ok := <-r.evCh; ok {
		t.Fatal("登录态失效属 fatal，通道应关闭")
	}
}
```

**注意**：`TestAuthRefreshIsFatal` 直接测 `emitFatal`，不去驱动 `reqAuthRefresh`
那条完整报文路径——那需要造一个假 ACP client，成本远超收益。归属正确性由
Step 3 的改动 + Step 4 的检索共同保证。

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test -run 'TestTurnFailure|TestFatal|TestAuthRefresh' ./internal/executor/codex/`
Expected: 编译失败——`emitTurnFailed` / `emitFatal` 未定义。

- [ ] **Step 4: 拆分实现并改六个 call site**

把 `codex/adapter.go:440-446` 的 `emitFailed` 换成两个函数（照 grok 那份写，
注释按 codex 实际改写：进程是 app-server、会话标识 `r.threadID`）：

```go
// emitTurnFailed 产出一个**回合级**失败终局，**不关闭事件通道**。
//
// 参数：reason 为给协调者看的失败原因原文
//
// 为什么不关通道：回合失败 ≠ codex app-server 完了，进程还活着，协调者一个
// continue 就能接着干——那正是 continue 的用途。以前这里一律 closeEvents，
// 于是 Send→startTurn 在同一个 runstate 上开新回合，新回合的一切事件在 emit
// 里被 evClosed 短路静默丢弃，manager 的 mediate 循环也早已随通道关闭退出，
// 任务停在 running 直到 2h 看门狗落 stalled（而 stalled 只唤醒不修复）。
// 这是 grok 上实测到并已修复的 B92，codex 结构相同。
//
// 一次性语义不受影响：跨回合的去重不需要（finishTurn 每回合只调一次，
// 各 case 互斥），与 fatal 路径之间的去重仍由 evClosed 承担。
func (a *Adapter) emitTurnFailed(r *runState, reason string) {
	a.log.Error("codex 回合失败", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
		Result: &executor.Result{OK: false, SessionID: r.threadID, FailReason: reason}})
}

// emitFatal 产出**执行级**失败终局并关闭事件通道。
//
// 参数：reason 为给协调者看的失败原因原文
//
// 用于连接已断、进程已死、登录态失效——这条运行态真的不可用了，必须关通道让
// manager 的 mediate 循环退出走对账。登录态失效也归这里：判回合级的话 continue
// 会开一个立刻又失败的新回合，变成人肉重试循环（见 reqAuthRefresh 处那句
// 「登录态失效重试一万次也不会好」）。
//
// 一次性语义：断开处置与进程判死两条路径可能同时到达，closeEvents 的幂等保证
// 只有先到者生效，后到者的 emit 被 evClosed 丢弃，不会双重终结。
func (a *Adapter) emitFatal(r *runState, reason string) {
	a.log.Error("codex 执行终结", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
		Result: &executor.Result{OK: false, SessionID: r.threadID, FailReason: reason}})
	r.closeEvents()
}
```

然后按上面「六个 call site 的归属」表逐个改。**`interrupted` 那段的 `stopping`
前置判断一字不动**（见「三处差异」第 3 条）。

- [ ] **Step 5: 确认没有遗漏**

Run: `grep -rn "emitFailed" internal/executor/codex/`
Expected: **零命中**（含测试文件——既有测试若引用 `emitFailed`，按同一张归属表
改成对应新名，**不要**加别名糊过去）。

Run: `grep -rn "emitFailed" internal/`
Expected: 只可能命中 `internal/executor/claudecode/`（若它也有同名函数）。
**若命中 claudecode，不要改它**——只在 ledger 里记一行「claudecode 是否同型待查」，
留给审核者判断。grok 已修完，命中 grok 说明你改错了分支。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test -count=1 ./internal/executor/codex/`
Expected: 新用例全 PASS，**既有用例原样通过**。既有测试钉的多是 fatal 路径与
回合正常收尾，不该被动到——**若变红，停下来判断是归属分错了还是测试写死了
旧行为，不要直接改绿**。

- [ ] **Step 7: Commit**

```bash
git add internal/executor/codex/
git commit -m "fix(codex): 回合失败不再关闭事件通道，拆出 emitTurnFailed / emitFatal（B99）"
```

---

### Task 2: `Send` 的 `evClosed` 守卫

**Files:**
- Modify: `internal/executor/codex/adapter.go:341-348`（`Send`）
- Test: `internal/executor/codex/adapter_turnfail_internal_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `emitFatal`
- Produces: `Send` 在事件通道已关闭时返回包 `executor.ErrTaskNotRunning` 的错误，
  且**不进入 `startTurn`**。

- [ ] **Step 1: 写失败的测试**

```go
func TestSendRefusesOnClosedChannel(t *testing.T) {
	// why：这是本次修复的安全网。即便将来又冒出某条没想到的关通道路径，这道
	// 守卫也把「静默吞掉一整个回合」变成「continue 当场报错、manager 走四级
	// 恢复阶梯」。B92 在 grok 上花了 2 小时 + 一次人工排查才被发现，代价全部
	// 来自静默
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

该用例的 `runState` **不要**给 `r.cli` 赋值。守卫写对了就在 `startTurn` 之前
返回、不会解引用 nil；写错了会 panic——那本身就是有效的红信号。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -run 'TestSendRefusesOnClosedChannel' ./internal/executor/codex/`
Expected: panic（nil `cli` 被解引用）或 err 为 nil——两种都算红。

- [ ] **Step 3: 加守卫**

在 `Send` 的 `lookup` 之后、`a.log.Info("codex 续接回合", …)` 之前插入（照 grok
那份，措辞按 codex 改）：

```go
	// 事件通道已关闭 = 这条运行态已被 fatal 路径判死。此时开新回合是最坏的
	// 结果：turn/start 发得出去、模型真的会跑，但产出的一切事件都会在 emit 里
	// 被 evClosed 短路丢弃，任务停在 running 直到 2h 看门狗（B92 在 grok 上实测）。
	//
	// 加在 Send 而不是 startTurn：startTurn 也被首轮启动路径调用，那时通道当然
	// 没关，加在那里是给热路径平白多一把锁。
	//
	// 返回 ErrTaskNotRunning 而不是自定义错误：manager 的四级恢复阶梯以
	// errors.Is(err, ErrTaskNotRunning) 为触发条件，会尝试冷恢复重建运行态。
	r.emitMu.Lock()
	closed := r.evClosed
	r.emitMu.Unlock()
	if closed {
		return fmt.Errorf("任务 %s 的事件通道已关闭，运行态已终结: %w", taskID, executor.ErrTaskNotRunning)
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -count=1 ./internal/executor/codex/`
Expected: 全绿。

- [ ] **Step 5: Commit**

```bash
git add internal/executor/codex/
git commit -m "fix(codex): Send 拒绝在已关闭的事件通道上开新回合（B99）"
```

---

### Task 3: 回归、变异测试、claudecode 同型排查

**Files:** 只验证 + 一份 ledger。

- [ ] **Step 1: 全量回归**

Run: `go build ./... && go vet ./... && go test -count=1 ./...`
Expected: 全部包 PASS，0 FAIL。

- [ ] **Step 2: 变异测试两条，一次只改一处**

```bash
# 变异 1：让 emitTurnFailed 又去关通道
# 手工在 emitTurnFailed 函数体末尾加一行 r.closeEvents()，然后：
go test -run 'TestTurnFailureKeepsEventChannelOpen' ./internal/executor/codex/   # 期望 FAIL
git checkout internal/executor/codex/adapter.go

# 变异 2：摘掉 Send 的守卫
# 手工把 Task 2 Step 3 那个 if closed 块注释掉，然后：
go test -run 'TestSendRefusesOnClosedChannel' ./internal/executor/codex/         # 期望 FAIL 或 panic
git checkout internal/executor/codex/adapter.go
```

两条做完后再跑一次 `go test -count=1 ./internal/executor/codex/` 确认还原干净。

- [ ] **Step 3: 排查 claudecode 是不是第三个同型**

**只查不改。** 回答三个问题，把结论写进 ledger：

1. `internal/executor/claudecode/` 里有没有「回合失败时关闭事件通道」的路径？
   （找 `closeEvents` / `close(` 的调用点，看它们各自的触发条件）
2. 它的 `Send`（或等价的续接入口）会不会在同一个 runstate 上复用已关闭的通道？
3. 结论：**同型 / 不同型 / 查不出来**。三选一，每条都要给代码坐标。

**如果结论是同型，不要顺手改**——记进 ledger 交给审核者，由他决定另开一条。
B92 的经验是这类改动要逐 call site 分类，分类错了比不改更糟。

- [ ] **Step 4: 写 ledger**

新建 `docs/superpowers/notes/2026-08-15-b99-codex-continue-drops-events-ledger.md`，
包含：每个 Task 的完成情况与 commit、**两条变异测试各自 FAIL 的用例名**（只写
「做了变异测试」不算）、Step 3 的 claudecode 排查结论与坐标、以及任何已知偏离。

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: B99 回归、变异测试与 claudecode 同型排查记录"
```
