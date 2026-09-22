# B394 implement 节点台账（回声自激：needs_cleared 移出唤醒映射）

> 分支 `cards/B394-charter-3`，起手 HEAD `c4bf45ef`（plan 提交）。
> 台账随产出物同批提交；记录原则：只写亲跑到结果的读数，未跑的写「未验证」与原因。
> 按 `docs/superpowers/plans/b394-plan.md` 实施 T1–T4。

## 0. 起手状态

```text
$ git status
On branch cards/B394-charter-3
nothing to commit, working tree clean
$ git log --oneline -1
c4bf45ef plan(B394): 唤醒回声自激——根因/分流/红色回路 + 清标去唤醒四任务
$ echo $TMPDIR
/root/.handoff/tmp/4c22114e
```

## 图查询记录（codegraph，只读）

```text
$ codegraph --repo . sym automationWakeEvent
命中 n_agentd_automationWakeEvent，file=internal/agentd/wakeconsumer.go, line=206，
signature 仍是**旧版**（含 case ledger.EvNeedsHuman, ledger.EvNeedsCleared ... 同一 case、
且带 EvRoomMessage 分支）——与 plan 台账 §1 记录的基线陈旧一致；实源以实读为准（实源
:237 已把 EvNeedsHuman/EvSeatBearingMissing 单独分流，EvNeedsCleared 与 decision 同 case）。
$ codegraph --repo . sym EvNeedsCleared
Error: 符号 "EvNeedsCleared" 不在图中（图未覆盖或名字有误）；近似候选: []
⇒ 回落 grep。grep -rn EvNeedsCleared internal/agentd/ --include=*.go → 仅
  internal/agentd/wakeconsumer.go:237 一处（与 plan 族 5 门禁绕过的结论一致）。
```

**图覆盖债**：`codegraph sym automationWakeEvent` 的 signature/行号仍是 B358.3 前旧版；
`codegraph sym EvNeedsCleared` 未覆盖该常量。行号与函数体一律以实读源码为准。

## T1 红色回路转正（先红）

- 新建 `internal/agentd/wakeconsumer_b394_test.go`（照 plan T1 代码块逐字）。
- 跑红（先红）：`go test ./internal/agentd/ -run TestB394ClearNeedsDoesNotWake -count=1` → 原文：

  ```
  --- FAIL: TestB394ClearNeedsDoesNotWake (0.19s)
      wakeconsumer_b394_test.go:39: 清标不得唤醒协调者（回声自激）：processed=1
  FAIL
  FAIL	github.com/Xsxdot/handoff/internal/agentd	0.192s
  ```

  与 plan / spec §4-1 预期一致（基线 processed=1）。

## T2 契约收窄（文档）

- 新建 `docs/superpowers/specs/b394-contract.md`（照 plan T2 内容）。
- 就地指针两处：
  - `docs/superpowers/specs/b358-contract.md:396` 规则 31 扩入 `needs_cleared` + 指针；
  - `docs/superpowers/specs/b353-contract.md:90` 条目 40 标注 B394 取代。
- 无代码接口，不跑测试。

## T3 实现：automationWakeEvent 移出 EvNeedsCleared

- 改 `internal/agentd/wakeconsumer.go:237`：拆 case，`EvNeedsCleared` 返回
  `keystone.WakeEvent{}, false, nil`，带 Debug 日志（seq/card/type/reason）；
  `EvDecisionOpened`/`EvDecisionAnswered` 保持原样（同 case 拆分）。
- 跑绿：`go test ./internal/agentd/ -run TestB394ClearNeedsDoesNotWake -count=1` → 原文：

  ```
  ok  	github.com/Xsxdot/handoff/internal/agentd	0.184s
  ```

- 编译与静态检查：`go build ./...` → `BUILD_EXIT=0`；`go vet ./internal/agentd/` → `VET_EXIT=0`。

## T4 更新既有期望 + 回归 + 变异复验

- 改 `internal/agentd/wakeconsumer_test.go:539`：`TestB353AutomationMapsCardActionEvents`
  期望 `processed` 3→2，briefing want 去掉 `needs_cleared`（只留 `decision body`/`answer`）。
- 该测试复核：`go test ./internal/agentd/ -run TestB353AutomationMapsCardActionEvents -count=1 -v`
  → `--- PASS`。
- 触及包全量：`go test ./internal/agentd/ -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 189.355s`。
- 不误伤回归（展示通路 + 正常唤醒源）：

  ```text
  $ go test ./cmd/ ./internal/collab/... ./internal/ledger/... ./internal/ledgerstep/ ./internal/keystone/ -count=1
  ok  	github.com/Xsxdot/handoff/cmd	73.670s
  ok  	github.com/Xsxdot/handoff/internal/collab	9.806s
  ?   	github.com/Xsxdot/handoff/internal/collab/client	[no test files]
  ?   	github.com/Xsxdot/handoff/internal/collab/cursor	[no test files]
  ok  	github.com/Xsxdot/handoff/internal/collab/room	0.004s
  ok  	github.com/Xsxdot/handoff/internal/ledger	27.515s
  ok  	github.com/Xsxdot/handoff/internal/ledger/api	1.230s
  ok  	github.com/Xsxdot/handoff/internal/ledgerstep	17.715s
  ok  	github.com/Xsxdot/handoff/internal/keystone	0.382s
  ```

  `cmd` 绿 ⇒ `card wait` 条目 15 不回归；`collab` 绿 ⇒ 会话列表 `needsHumanByCard` 不回归。

### 变异自验（先确认编译过、命中唯一，再数失败）

- 命中唯一：`case ledger.EvNeedsCleared:` 在 `wakeconsumer.go` 出现 `count=1`。
- 变异（改语义：yes=false→true，可编译）：把 T3 的返回改为
  `return keystone.WakeEvent{Kind: keystone.WakeTaskTerminal, Card: ev.CardID}, true, nil`。
  - `go build ./internal/agentd/` → `MUT_BUILD_EXIT=0`（编译过，判定有效）。
  - `go test ./internal/agentd/ -run TestB394ClearNeedsDoesNotWake -count=1` → 原文：

    ```
    --- FAIL: TestB394ClearNeedsDoesNotWake (0.34s)
        wakeconsumer_b394_test.go:39: 清标不得唤醒协调者（回声自激）：processed=1
    FAIL
    FAIL	github.com/Xsxdot/handoff/internal/agentd	0.343s
    ```

    ⇒ 护栏去掉重新变红（b394-contract 条 6 / spec §4-2 达成）。
- 撤回临改：从 `$TMPDIR/wakeconsumer.go.bak` 还原；复核
  `go test ... -run TestB394ClearNeedsDoesNotWake` → `ok`；`git diff --stat` 只剩 T3 的正式改动
  （+ 文档 + 测试），无变异残留。

## 边界遵守

- 未动路由/承载记录、未动 keystone 失败重打、未动线上状态与配置；只碰
  `internal/agentd` 与 `docs/superpowers/specs/`。
- 未派发、未调用 handoff CLI、未起新 executor。

## 提交事实

```
$ git add -A && git commit -q -m "fix(B394): 清标移出唤醒映射——断协调者账务动作的回声自激"
$ git log --oneline -1
184c5d53 fix(B394): 清标移出唤醒映射——断协调者账务动作的回声自激
$ git status --short
（空）
```

第一次提交实得 `184c5d53`；本台账补记（上面两行）随后单独提交，故收口时 HEAD 会前移，
以 `git status` 干净为收口判据。
