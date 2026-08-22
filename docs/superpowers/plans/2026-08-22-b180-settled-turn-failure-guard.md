# B180 实现计划：回合收口后到达的失败结果要被丢弃（grok / codex 同治）

> 2026-08-22。协调者写。卡：B180（中）。

## 事实基线（协调者在 985f37135 上查证）

现象两次实证：grok（echo probe，22s）与 codex（任务 c43f4a83）都在 `completed`
之后 0.1s 补一条 `turn_failed`，理由是「ACP/codex 连接断开: … EOF」，任务实际成功。

**根因不在 adapter，在 manager 少了一处守卫**——「executor 已不在」有两个到达口，
只有一个有守卫：

| 到达口 | 代码 | 现状 |
|---|---|---|
| 事件通道关闭 | `mediate` 末尾 → `reconcileExecutorGone`（`internal/agentd/reconcile.go:158`） | **有守卫**：`cur.State != running && != waiting_answer` 时只清扫、不落事件 |
| adapter 投 `result{OK:false}` | `handleEvent` → `handleResult`（`internal/agentd/manager.go:2986`） | **守卫不全**：只挡 `completed` / `failed`，不挡 `waiting_review` |

于是进程正常退出时：adapter 的 `emitFatal` 先投一条失败 result（`grok/adapter.go:683`、
codex 同构），此时任务已因 `completed` 处在 `waiting_review`，`handleResult` 照单
全收，落出那条假 `turn_failed`；随后通道关闭走对账，那一路反而被守卫挡住了。

**这解释了为什么两个执行器同构**：假失败由 manager 统一产出，不是各 adapter 各错一次。
因此修**一处**即可同治，不要去改两个 adapter。

## 设计决定

1. **补的是失败结果，不是全部结果**：`waiting_review` 上收到 `OK=false` 判为回合
   收口后的传输层假警报，丢弃 + 留痕（Warn 日志，含 FailReason 原文）。
   `OK=true` 维持现状不动——它带着回合报告，而 agentd 重启丢终态事件的情形下
   （见 B-记忆「agentd 重启丢 codex 终态事件」）迟到的 completed 是**有用**的，
   丢掉会让协调者失去唯一的报文来源。这条不对称是刻意的，注释里写明。
2. **守卫写在 handleResult 的既有状态判断处**，与 `reconcileExecutorGone` 的守卫
   同款理由、同款措辞，让两个到达口看起来是一件事的两半。
3. **不动 adapter**：adapter 不该被要求判断「我这次 EOF 是正常收尾还是猝死」——
   它没有任务状态。判据在 manager 手里。

## Task 1：manager 补守卫

`internal/agentd/manager.go`，`handleResult` 里现有的这段之后：

```go
	if cur.State == proto.TaskStateCompleted || cur.State == proto.TaskStateFailed {
		m.log.Debug("任务已终结，忽略 result 事件", "task", taskID, "state", cur.State)
		return
	}
```

紧接着加：

```go
	// 回合已收口（waiting_review）之后到达的**失败**结果一律丢弃：executor 进程
	// 正常退出时读流会报 EOF，adapter 把它当执行终结投一条 result{OK:false}，
	// 而这一回合的终态事件早已落库。真机两次实证（grok echo probe、codex 任务
	// c43f4a83）：completed 之后 0.1s 补一条 turn_failed，任务其实成功。
	//
	// 与 reconcileExecutorGone 的守卫是同一件事的两半：「executor 已不在」有两个
	// 到达口（事件通道关闭、adapter 投失败 result），那一路早有守卫，这一路没有。
	//
	// 只挡失败、不挡成功是刻意的：迟到的 completed 带着本回合唯一的报文（agentd
	// 重启丢终态事件时正靠它补回），丢掉它的代价远大于一条重复事件。
	if ev.Result != nil && !ev.Result.OK && cur.State == proto.TaskStateWaitingReview {
		m.log.Warn("回合已收口后到达的失败结果，判为传输层假警报并丢弃",
			"task", taskID, "state", cur.State, "fail_reason", ev.Result.FailReason)
		return
	}
```

注意：这段必须排在 `r := ev.Result` 与 `transitToReview` **之前**——落在后面就已经
把假事件写进库了。

## 测试映射

`internal/agentd/` 下与 `handleResult` 同包的测试文件（先 `grep -rn "handleResult"
internal/agentd/*_test.go` 找既有夹具，复用它，不要新造 manager 夹具）新增两条：

1. `TestSettledTurnDropsLateFailureResult`：任务经一次 `OK=true` 的 result 落到
   `waiting_review` 后，再投一条 `OK=false`（FailReason 形如 `codex 连接断开: EOF`）。
   断言：事件流里 `turn_failed` **零条**、`completed` 恰好一条，任务仍在
   `waiting_review`。
2. `TestRunningTurnStillRecordsFailureResult`（回归网）：任务处于 `running` 时投
   `OK=false`，断言照旧落 `turn_failed` 且迁 `waiting_review`——守卫不得把真失败一起吃掉。

变异自检（执行者自己做，结论写进 ledger）：把守卫条件里的 `!ev.Result.OK` 去掉，
用例 1 应仍绿而用例 2 应转红；把整段守卫删掉，用例 1 应转红。两条都不红说明用例
没牙齿，回头重写用例而不是改实现。

## 测试范围

- `go test ./internal/agentd/`（触及包）
- `go build ./...`、`go vet ./...`、`gofmt -l .` 无输出

## 不属于本次

- 不改任何 adapter（grok / codex / opencode / claudecode 一行都不动）。
- 不动 `reconcileExecutorGone`。
- 不处理「agentd 重启丢终态事件」（那是另一条记忆里的独立问题）。
