# B402 contract 台账

## 2026-09-25 开节点与边界

### `git status --short --branch` / `git log --oneline -3`

```text
## cards/B402-charter
nothing to commit, working tree clean
5d6607fc spec(B402): 冻结零文本失败单次续接契约
a8e60208 test(B380 recon): 冻结条 25 白名单补 drop.go——B272 并线晚于 B378 裁决的历史缺口
b265d684 merge(B380-recon): 基线并入——图对账前回灌上游 B382 工作分支登记能力
```

判断：本节点在 `cards/B402-charter`；有效基线 `cards/B402-spec` @ `5d6607fc`（`git merge-base HEAD origin/cards/B402-spec` = `5d6607fc`，与 HEAD 相同）。spec 头部状态行「状态：已批准」已复核一致，无需回写。级别 L3 轻档（直通竖切不适用）。

### 现状查证（源码读数；图内符号用 `file#Symbol`，图外回落到 file:line）

- 生命周期根因：`internal/ledgerstep/node.go` 在 `Await` 返回后注册无条件 `FinishTask` defer（B341 引入），解析失败仍归档。
- 失败生产点五处：opencode `mapIdle`、codex `finishTurn`、grok `finishTurn`、claudecode `fallbackClassify`、agy `fallbackClassify`。
- 等待层只返回裸字符串/错误：`waitForTurnEnd`、`finalMessageFromEvents`、`clientFinalMessage`。
- 续接能力现成：`internal/client/execution.go:38` `ExecutionClient.Continue`；`Manager.Continue` 有 `waiting_review` 状态门；HTTP `handleContinue` 有非空 instructions 门。
- `FailedPayload` 只有人读 `FailReason` + git 实况 + 占用快照。

### codegraph 读数

```text
$ codegraph --repo . sym FailureClass        → 不在图中（近似候选 []）→ 记入图覆盖债
$ codegraph --repo . sym waitForTurnEnd      → n_ledgerstep_waitForTurnEnd（anchor moved）
$ codegraph --repo . sym finalMessageFromEvents / clientFinalMessage / RunOnce / handleResult / Continue / NewFailedPayload / FailedPayload / Result → 均命中
$ codegraph --repo . views                   → 12 个视图（无 B402）
$ codegraph --repo . check                   → fails=[]，exit 0
$ codegraph --repo . validate                → exit 1，2 个既存问题（B272 k_dropdir_fn / B374 k_collab_model），非本卡
```

## 2026-09-25 Ticket 0 落地与冻结

### 改动

- `internal/proto/failure.go`（新）：`FailureClass` + `FailureClassZeroText="zero_text"`。
- `internal/executor/executor.go#Result`：新增 `FailureClass proto.FailureClass`。
- `internal/orchestration/contracts.go`：`FailedPayload.failure_class`（omitempty）；`NewFailedPayload` 加第 4 参数；4 个生产调用点 + 2 个测试调用点补齐。
- `internal/orchestration/manager.go#Manager.handleResult`：`!OK` 分支透传 `r.FailureClass`。
- `internal/ledgerstep/wire.go`：`TurnEnd`/`failedPayload`/`failureClassOf`；`waitForTurnEnd`、`waitForTurnEndGrace` 改类型化返回。`finalMessageFromEvents`/`clientFinalMessage` 签名不变（B233.16 编译期锁）。
- `internal/ledgerstep/runner.go`：`ZeroTextContinueInstruction` 常量；`awaitNode` 内部「首个 turn_failed(zero_text) → Continue 恰一次 → 再等」。
- `internal/ledgerstep/node.go`：删 `FinishTask` 无条件 defer，改收口末尾显式调用；归档失败转 `needs_human`。
- 五家 adapter 零文本分支加 `FailureClass: proto.FailureClassZeroText`。

### 本轮跑过的命令与结果

```text
$ go build ./...                    → BUILD_EXIT=0
$ gofmt -l internal cmd             → internal/agentd/cardstep.go, internal/ledger/types.go（开工前既存，未触碰）
$ go vet ./...                      → VET_EXIT=0
$ go test ./... -count=1            → TEST_EXIT=0
$ go test -race ./internal/ledgerstep/ ./internal/orchestration/ ./internal/proto/ ./internal/executor/... -count=1 → RACE_EXIT=0
$ codegraph --repo . check          → CHECK_EXIT=0
$ codegraph --repo . --view cards-B402-charter check → CHECK_EXIT=0
$ codegraph --repo . resolve --doc docs/superpowers/specs/b402-contract.md → 12 锚全 ok/moved，exit 0
```

### 变异证明（改后立即还原，`git diff --stat` 复核）

```text
变异① runner.go 续接条件改为 "disabled" → TestAwaitNodeAuto/ContinuesAtMostOnce FAIL（实际 0 次续接）
变异② node.go 解析失败早退补 FinishTask → TestNodeStepDoesNotFinishTaskOnParseFailure FAIL（调用 1 次）
变异③ wire.go failureClassOf 恒返回 "" → TestWaitForTurnEndCarriesZeroTextClass FAIL（实得 ""）
```

### 图与视图

- `codegraph/target.json` 未改：无新增跨域方向/超预算调用。
- `codegraph/best.json` 未改。
- 新增 `codegraph/diffs/cards-B402-charter.json`：nodesAdded 3 / nodesModified 6。为结构性最小增量，非全量重扫，absorb 前重扫记为欠账。

### 收口

- 提交事实（历史读数）：先提交 HEAD = `0e015e22`。命令：
  `git add -A && git commit`（message `contract(B402): 零文本失败单次自动续接契约冻结 + Ticket 0 骨架`）。
  随后按台账纪律追加本行并 `git commit --amend` 收进同批提交——amend 会换 hash，不把新 hash 回写本台账，收口判据是工作树干净。
- 提交前新鲜证据：`go build ./...` exit 0；`go vet ./...` exit 0；`go test ./... -count=1` exit 0；`go test -race` 关键包 exit 0；`codegraph check`/`--view cards-B402-charter check` 均 fails=0；`codegraph resolve --doc b402-contract.md` 12 锚全 ok/moved。

## 2026-09-25 交棒

产出：`docs/superpowers/specs/b402-contract.md`、Ticket 0 骨架、`codegraph/diffs/cards-B402-charter.json`、本台账。下一节点：breakdown。欠账见契约 §6/§8/§9。
