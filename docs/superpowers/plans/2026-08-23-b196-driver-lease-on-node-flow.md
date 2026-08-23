# B196（含并入的 B189）实现计划（**实况回填**）

> ⚠️ **这份 plan 是事后回填的，不是事前写的。**
> 2026-08-23 的 `plan` 节点派发（task `9c9641a1`，codex@linux-01）被诱导直接做完了
> 实现（提交 `4c121b2d`），没有产出计划文档——B182 缺陷复发，与同日 B190 同形态，
> 且两次的 `omit_acceptance` 都已生效。新证据已开卡 **B200** 追。
> `implement` 列的 plan 附件门拦住了自动移列，卡落 `needs_human`，协调者据此发现。
> 按 B175/B176 先例处置：协调者逐行审实现后回填实况 plan 补门。**它描述已完成的事。**
>
> spec：`docs/superpowers/specs/2026-08-23-b196-driver-lease-on-node-flow.md`
> 分支：`cards/B196-charter`，起点 `bc842fccd`

## Task 1：给 StepRunner 三个租约字段

**做了什么**：`StepRunner` 新增 `Session`（本次运行的租约标识）、`Heartbeat`
（续租测试缝，空则用 `St.HeartbeatDriver`）、`HeartbeatInterval`（测试用来缩短等待，
零值取默认）。默认续租间隔 `defaultDriverHeartbeatInterval = 2 * time.Minute`
——小于账本 5 分钟 TTL 的一半，给调度抖动留余量。

**落点**：`internal/ledgerstep/runner.go`。

## Task 2：Run 里认领 → 续租 → 释放

**做了什么**：`Run` 在取到节点定义之后：

1. **纯人工节点直接跳过认领**（被误点不该留下租约），原路进 `RunOnce`；
2. 取 `Session`（空则回落 `Dispatcher.Actor` 兼容旧调用方；仍为空则拒绝执行）；
3. `St.ClaimDriver`（**不是 `ClaimCard`**——节点流的列由节点自己推进，把卡推去
   「进行中」是实现流语义）。失败即返回，错误里带库层给出的持有者会话名；
4. 起续租协程，`defer` 里先停协程再 `St.ReleaseCard`；
5. 续租失败**只警告不中止**——回合已经派出去了，硬中止只会留下没人收的任务。

**落点**：`internal/ledgerstep/runner.go#StepRunner.Run`、
`#StepRunner.startDriverHeartbeat`。续租协程绑 `context.WithCancel(ctx)`，
回合结束即取消并等它退出，不留泄漏。

## Task 3：两条装配路径传入会话

**做了什么**：CLI 传 `ledgerSession()`（带 pid，两个进程互相区分得开）；
agentd 传请求 `actor`（`web:<addr>`）。

**落点**：`cmd/card_node.go#runStepDispatch`、`internal/agentd/cardstep.go#Server.startCardStep`。

## Task 4：测试

落在 spec 指定的那**一个**接缝上（`StepRunner.Run`，`internal/ledgerstep/runner_test.go`），
四条断言与 spec 一一对应：

| 用例 | 钉住什么 |
|---|---|
| `TestRunnerClaimsDriverWithoutChangingNodeStatusAndReleasesAfterRun` | 跑起来时驱动会话已落且**状态列没被改成「进行中」**；回合结束租约被释放 |
| `TestRunnerRejectsActiveDriverAndReportsHolder` | 别的会话持有且未过期 → 拒绝、报文含持有者、不派发、不改写驱动 |
| `TestRunnerReleasesDriverAfterDispatchFailure` | 失败路径同样释放 |
| `TestRunnerHeartbeatsDuringLongRun` | 长回合期间续租被调用过（走注入的 `Heartbeat` 缝 + 缩短的 `HeartbeatInterval`，不真等 5 分钟） |

## 协调者验收记录（2026-08-23，独立 worktree `/tmp/b196rev`）

| 判据 | 结果 |
|---|---|
| 编译全量 | `go build ./...` exit 0 |
| 测试局部 | `go test ./internal/ledgerstep/... ./internal/ledger/... -count=1` 全绿（0.968s / 2.465s） |
| 格式 | `gofmt -l .` 空 |
| **变异 ×3** | ①去掉 `ClaimDriver` 调用 → `…ClaimsDriver…` 与 `…RejectsActiveDriver…` 双红；②去掉 `ReleaseCard` 调用 → `…ClaimsDriver…` 与 `…ReleasesDriverAfterDispatchFailure` 双红；③去掉 `heartbeat()` 调用 → `TestRunnerHeartbeatsDuringLongRun` 红。三条实现路径各自被至少一条用例罩住 |
| 真机 | 见卡上的真机 note（用本分支构建的 CLI 真派一次远程节点，观察 `driver_session`） |

## 保留意见（不阻断）

`agentd` 侧的会话标识是 `web:<addr>`，同一地址的两个并发请求在**账本层**判为同一
会话、互不排斥。今天由 agentd 进程内的 `claimCardStep` 兜住（同卡同时只允许一个
环节在飞），所以没有真实敞口；但账本层的并发保护在 web 入口上比 CLI 弱一档。
CLI 侧的 `ledgerSession()` 带 pid，不受影响。记账不修——修它要给 web 请求生成
稳定的会话 id，属另一件事。
