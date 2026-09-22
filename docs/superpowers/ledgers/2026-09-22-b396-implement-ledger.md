# B396 implement 节点台账（陈旧环节运行位不自愈：archived 收口）

> 分支 `cards/B396-charter-3`，起手 HEAD `5b12c7b0`（plan 提交）。
> 按 `docs/superpowers/plans/b396-plan.md` 实施 T1–T3。
> 台账随产出物同批提交；只写亲跑到结果的读数，未跑的写「未验证」与原因。
> 图查询：`codegraph --repo . sym waitForTurnEnd/waitForTurnEndGrace/startCardStep/claimCardStep/releaseCardStep` 全部命中（见下「图查询」）；`sym EventTypeArchived` 未命中（图覆盖债）。

## 0. 起手状态

```text
$ echo $TMPDIR
/root/.handoff/tmp/48f46667
$ git branch --show-current
cards/B396-charter-3
$ git status --short
（空）
$ git log --oneline -1
5b12c7b0 plan(B396): 陈旧环节运行位不自愈——根因/分流 + archived 收口修复计划
```

## 图查询记录（codegraph，只读）

```text
$ codegraph --repo . sym waitForTurnEnd      → 命中 n_ledgerstep_waitForTurnEnd（wire.go:53，函数体与实源一致）
$ codegraph --repo . sym waitForTurnEndGrace → 命中 n_ledgerstep_waitForTurnEndGrace（wire.go:84）
$ codegraph --repo . sym startCardStep       → 命中 n_agentd_Server_startCardStep（cardstep.go:128）
$ codegraph --repo . sym claimCardStep       → 命中 n_agentd_Server_claimCardStep（cardstep.go:285）
$ codegraph --repo . sym releaseCardStep     → 命中 n_agentd_Server_releaseCardStep（cardstep.go:295）
$ codegraph --repo . sym EventTypeArchived   → Error：符号不在图中；近似候选 [] → 回落 grep，
    命中 internal/proto/proto.go:115，记入图覆盖债。
```

**图覆盖债**：`EventTypeArchived` 常量不在图中（回落 grep 取 `proto.go:115`）；
其余受触符号均命中且与实源一致。

## T1 红色回路转正（先红）

- 新建 `internal/agentd/b396_archived_step_test.go`（照 plan T1 代码块逐字）。
- 先红：`go test ./internal/agentd/ -run TestB396ArchivedTaskReleasesCardStepSlot -count=1 -timeout 60s`
  → 原文：

  ```
  --- FAIL: TestB396ArchivedTaskReleasesCardStepSlot (3.01s)
      b396_archived_step_test.go:128: 等待条件超时
  FAIL
  FAIL	github.com/Xsxdot/handoff/internal/agentd	3.016s
  ```

  失败原因是功能缺失（`waitForTurnEnd` 不认 archived ⇒ 槽位不释放），非拼写/夹具错：
  日志里反复 `wait 事件返回 ... type=archived` 后接续重连，`Run` 不返回 ⇒
  `cardStepInFlight` 恒 true。与 plan / spec §4-1 预期一致。

## T2 实现：waitForTurnEnd 认 archived 为终态

- 文件 `internal/ledgerstep/wire.go`，改动三处 + 函数头注释同步：
  1. `:19-22` 新增包内哨兵 `errTurnArchived`；
  2. `:69-78` 主循环 switch 首位插入 `case proto.EventTypeArchived: … return errTurnArchived`；
  3. `:126-130` 宽限循环插入一致性 `if event.Type == proto.EventTypeArchived { … return errTurnArchived }`；
  4. `waitForTurnEnd` 函数头注释补「或 task 归档（archived，B396）」。
- 跑绿：`go test ./internal/agentd/ -run TestB396ArchivedTaskReleasesCardStepSlot -count=1 -timeout 60s`
  → 原文 `ok  github.com/Xsxdot/handoff/internal/agentd  1.063s`。
- 编译与静态检查：`go build ./...` → `BUILD_EXIT=0`；
  `go vet ./internal/ledgerstep/ ./internal/agentd/` → `VET_EXIT=0`。

## T3 变异复验 + 不误伤回归 + 并发反例在场

### 变异复验（先确认编译过、命中唯一，再数失败）

- 命中唯一：主循环 archived 块 `count(old) == 1`（脚本断言 `COUNT 1`）。
- 变异：删除主循环 `case proto.EventTypeArchived` 块（改回忽略）。
  `go build ./internal/ledgerstep/` → `MUT_BUILD_EXIT=0`（编译过，判定有效）。
  行为断言：`go test ./internal/agentd/ -run TestB396ArchivedTaskReleasesCardStepSlot -count=1 -timeout 60s`
  → 原文：

  ```
  --- FAIL: TestB396ArchivedTaskReleasesCardStepSlot (4.04s)
      b396_archived_step_test.go:128: 等待条件超时
  FAIL
  FAIL	github.com/Xsxdot/handoff/internal/agentd	4.044s
  ```

  ⇒ 去掉 archived 分支重新变红（护栏承重、可红），spec §4-3 达成。
- 撤回临改：重新插回同一块；`grep -c "case proto.EventTypeArchived:"` = 1；
  复跑 → `ok`；`git diff --stat` 只剩 wire.go 与新增测试文件，无变异残留。

### 不误伤回归（跑既有测试，不新增）

```text
$ go test ./internal/ledgerstep/ -count=1 -timeout 300s
ok  	github.com/Xsxdot/handoff/internal/ledgerstep	13.978s

$ go test ./internal/agentd/ -run 'TestStartCardStep|TestCardStep|TestB23310|TestB393' -count=1 -timeout 300s
ok  	github.com/Xsxdot/handoff/internal/agentd	11.653s

$ go test ./cmd/... -count=1 -timeout 600s
ok  	github.com/Xsxdot/handoff/cmd	67.075s
```

`TestStartCardStepRejectsSecondInFlight` / `TestStartCardStepReleasesSlotOnFinish` /
`TestCardStepSecondReturns409` 均绿 ⇒ 并发互斥与槽位释放语义不回归。

### 并发反例在场

T1 测试内已含「真在跑时并发重派应 409」断言（第二次 `post()` 在 `release()` 之前，
此时槽位确在飞），且整个测试绿 ⇒ 互斥未被一起放开（spec §4-2 反例不误伤）。

## 边界遵守

- 未改 409 判据（`claimCardStep`/`cardStepFlight`）与互斥语义；未手改锁数据；
  未动其他卡或线上状态与配置；未修 plan 声明的预存在 PTY flake。
- 只碰 `internal/ledgerstep/wire.go`（+ 新增 `internal/agentd/b396_archived_step_test.go`）
  与本文档。未派发、未调用 handoff CLI、未起新 executor。

## 未验证项

- 真机重放（spec §4-4 / plan §11）：需在跑 T2 改动的 agentd 上对 B393 重派，本节点未跑。

## 提交事实（历史读数）

```text
$ git add -A && git commit -q -m "fix(B396): waitForTurnEnd 认 archived 为终态——归档后释放卡槽位与运行锁"
$ git log --oneline -1
<见下方收尾行>
$ git status --short
（空）
```

本台账在提交时随产出物同批提交；提交后若补记本节则 amend 一次。收口判据是工作树干净。
