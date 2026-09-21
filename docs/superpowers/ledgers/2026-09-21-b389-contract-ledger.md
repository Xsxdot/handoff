# B389 契约节点台账（2026-09-21）

契约文档：`docs/superpowers/specs/b389-contract.md`（本节点产出物）
工作树：`/Users/sycm/.handoff/worktrees/b233.1-fl`，分支 `cards/B233.1-charter-7`

## 起手状态

```
$ git rev-parse --abbrev-ref HEAD   → cards/B233.1-charter-7
$ git log --oneline -1              → 241b71720 fix(B381): print 协调者只支持本机载体——远端载体显式拒绝，不静默跑错机器
$ git status --short                → （空，工作树干净）
```

## 查证记录（读到的代码事实）

- `internal/ledger/binding.go`：`BindSeat` 在 `:33`、`RebindSeat` 在 `:83`；两者都在 `s.mutate` 事务内读卡/判空/写字/落事件；文件头 `:1-3` 明写 `driver_carrier` 与 `driver_leases` 不能成为席位第二真源。
- `docs/superpowers/specs/b312-contract.md` §2.2：「已有 `driver_carrier` 迁移列只作兼容存量，**不读、不写、不映射到席位**」——本卡因此不复用该列。
- `internal/proto/seat.go`：`EncodeSeatIdentity` 语法 `cli:<cli>#<session_id>`，恰一个 `#`、两段非空无空白、`cli` 不含 `:`——承载信息塞不进席位身份。
- `internal/ledger/store.go`：`ddlStatements(pg bool)` 两支语句表（PG `:204` 起、SQLite `:388` 起前的 driver_leases 段），加列迁移在 `:416` 起并容忍 `duplicate column`/`already exists`；`TestDDLDialectParity` 是"一个方言缺表"的机内防线。
- `internal/agentd/scheddrain.go`：`launchCoordinatorRoundWithExpect` 在 `:255` `LaunchAdmit(squad)` 现挑载体、`:312-314` CAS 写席位；`wakeCoordinatorRound` 在 `:357` **同样现挑载体**、`:382` 本机 `keystone.Wake`、`:400` 重建后换绑。
- 调用点普查：生产 5 处（`cmd/card_driver.go:38,126`、`internal/agentd/scheddrain.go:312,315,400`），测试 60 处（15 个文件）。

## 做出的判断（写进契约 §6 拍板记录）

1. 承载记录用**独立小表 `seat_bearings`**，不塞进 `cards` 列、不复用 `driver_carrier`。
2. `bind` 席位**必须零承载**（人尺度坐下没有载体归属）；`coordinate` 席位**必须有载体 + 机器**。
3. 认领键取 **`seq` 本身**；游标是**终局前缀水位**（在飞认领挡住推进）。
4. 唤醒执行走**冻结载体准入**（新增 `AdmitSeatCarrier`），不再 `LaunchAdmit` 重选。

## 执行记录（命令与原始输出）

### 一次失败尝试（有价值，留档）

第一版参数补齐脚本用"行尾最后一个 `)`"作为插入点：

```
$ python3 patch_bearing.py
patched files: 15
$ gofmt -l internal cmd
internal/agentd/coordapi_test.go:349:45: expected '{', found newline
…（共 54 行同类解析错，15 个文件全中）
```

根因：`if err := …); err != nil {` 这类行尾还有别的 `)`，插入点落到外层条件上，把调用截断。
处置：`git checkout -- $(git diff --name-only | grep '_test\.go$')` 全量回退测试文件，改用只匹配调用自身括号的正则 `\.(Bind|Rebind)Seat\(([^()]*)\)`：

```
$ python3 patch2.py
files: 15 sites: 60
$ gofmt -l internal cmd      → （空）
$ go build ./...             → exit 0
$ go vet ./internal/ledger/ ./cmd/  → （无输出，exit 0）
```

### 测试

```
$ go test ./internal/ledger/... ./internal/collab/... ./internal/keystone/... ./internal/ledgerstep/... ./cmd/... -count=1
ok  github.com/Xsxdot/handoff/internal/ledger        2.819s
ok  github.com/Xsxdot/handoff/internal/ledger/api    0.708s
ok  github.com/Xsxdot/handoff/internal/collab        2.844s
ok  github.com/Xsxdot/handoff/internal/collab/room   1.512s
ok  github.com/Xsxdot/handoff/internal/keystone      2.003s
ok  github.com/Xsxdot/handoff/internal/ledgerstep    3.518s
ok  github.com/Xsxdot/handoff/cmd                   49.557s

$ go test ./internal/ledger/ -run 'TestSeatBearing|TestDDLDialectParity' -count=1
ok  github.com/Xsxdot/handoff/internal/ledger  0.773s
```

新用例文件：`internal/ledger/bearing_test.go`（锁契约 §4 第 1-13 条中除 §4-11 外的全部条目）。

## 变异复验（金样本的牙）

把 `BindSeat` 事务内的承载写入换成"只删不写"（`writeSeatBearing` → `deleteSeatBearing`）：

```
$ go test ./internal/ledger/ -run 'TestSeatBearing' -count=1
--- FAIL: TestSeatBearingFollowsSeatLifecycle (0.01s)
    bearing_test.go:87: 读承载: ok=false err=<nil>
FAIL   github.com/Xsxdot/handoff/internal/ledger  0.711s
```

复原（`git checkout -- internal/ledger/binding.go`）后：

```
$ go test ./internal/ledger/ -run 'TestSeatBearing' -count=1
ok  github.com/Xsxdot/handoff/internal/ledger  0.306s
```

结论：金样本确实咬住"承载与席位同事务落盘"这条不变量，不是空跑。

## 欠账（不静默）

- **契约 §4-11（终态转移原子清席位）** 未在本节点落码：`CloseCard` 归第 3 片（判据收口与终态）改，本节点不碰它，避免把两个域的行为混在一个提交里。
- **`codegraph/target.json` 未追加 B389 条目**：该文件是 `{from,to,entries[]}` 的 51 条契约数组，追加需要按域对逐条落位；本节点只改了既有边的语义（ledger 写面新增承载、agentd 组装点写承载），未新增跨域依赖方向。落位与 `codegraph/diffs/` 视图 diff 由实现节点的第 1 片随代码提交补齐。
- `wake_claims` 表已建但 `ClaimWake/CompleteWake` 三个方法属第 1 片，本节点未落（表结构是契约面，方法体是实现面）。

---

# 修订轮（2026-09-21，review 退回三项）

**触发**：卡 B389 `review_verdict` 事件退回三项——(1) §3.1.1 认领键错（单键，扇出丢唤醒）；(2) §4-29 锁点不可区分；(3) 新增多卡扇出冻结条目。
**工作树**：`/root/.handoff/worktrees/e0257285`，分支 `cards/B389-charter-8`，起手 HEAD `6f20dfce`（工作树干净）。
**产出物**：`docs/superpowers/specs/b389-contract.md`（就地修订，法定路径）。

## 起手复跑（本轮新鲜证据）

```text
$ git status --short                  → （空）
$ go test ./internal/ledger/... ./internal/agentd/... -count=1
ok  github.com/Xsxdot/handoff/internal/ledger       24.124s
ok  github.com/Xsxdot/handoff/internal/ledger/api    0.933s
ok  github.com/Xsxdot/handoff/internal/agentd      176.798s
$ codegraph --repo . check            → CHECK_EXIT=0
$ codegraph --repo . resolve --doc docs/superpowers/specs/b389-contract.md
   → 9 anchors：6 ok / 3 moved / 0 坏
```

图覆盖债（本轮新增）：`RoomMessageWakeEvents`、`claimWakeBatch`、`Store.ClaimWake`（`codegraph sym` 退出码 1，无近似候选）——以源码为准。

## (1) 扇出丢唤醒：实测复现 + 复合键验证

在 `$TMPDIR`（`/root/.handoff/tmp/e0257285/bsrc`）用 `git archive HEAD` 建副本，副本内落双卡扇出复现（一条 `room_message` @ 两张各有 coordinate 席位的卡，走真实 `consumeAutomationEventsOnce`）：

```text
$ go test ./internal/agentd/ -run TestZZB389FanoutRepro -count=1 -v
INFO 唤醒认领成功 seq=8 card=B1 holder=handoff#466354
INFO 唤醒认领让过 seq=8 card=B2 holder=handoff#466354     ← 第二张卡被静默跳过
FANOUT_ROUND1 processed=1 resumes=1 cursor=8               ← 游标越过 seq
FANOUT_ROUND2 processed=0 err=<nil> resumes_total=1        ← 第二轮补不回来，永久丢
```

把副本改成 `(card,seq)` 复合键（DDL 主键 + `ClaimWake`/`CompleteWake` + `WakeClaimsBefore` 的 `DISTINCT seq`）后同测：

```text
INFO 唤醒认领成功 seq=8 card=B1
INFO 唤醒认领成功 seq=8 card=B2
FANOUT_ROUND1 processed=2 resumes=2 cursor=8
$ go test ./internal/ledger/ ./internal/agentd/ -count=1
ok  github.com/Xsxdot/handoff/internal/ledger   23.518s
ok  github.com/Xsxdot/handoff/internal/agentd  180.518s
```

结论：原理由（取 `seq` 本身即可排他）被证伪；复合键修复成立且不回归既有测试。**副本改动未落到本分支工作树**——落码归第 1 片返工（已写进契约 §5 欠账）。

## (2) §4-29 锁点不可区分：实测复现 + 新锁点验证

把副本 `scheddrain.go:449` 的 `AdmitSeatCarrier` 换成 `LaunchAdmit`，跑原锁点：

```text
$ go test ./internal/agentd/ -run 'TestB389WakeUsesFrozenCarrierNotLaunchAdmit' -count=1
ok  github.com/Xsxdot/handoff/internal/agentd  0.223s    ← 抓不住，四包全绿
```

新锁点 A（回合进行中在 `Resume` 内读两级计数 `runningCountIn`）：

```text
冻结路径：ZZOBS carrier/coord-carrier=0 carrier/coord-carrier-2=1 ... PASS
换 LaunchAdmit：ZZOBS carrier/coord-carrier=1 carrier/coord-carrier-2=0
   回合进行中冻结载体 carrier/coord-carrier-2 应占用=1，实得 0 → FAIL
```

新锁点 B（记录型 `SchedulingClient` 装饰器，断言以 `("coord","coord-carrier-2")` 调用 `AdmitSeatCarrier` 恰一次、`LaunchAdmit` 零调用）：

```text
冻结路径：ZZLOCK frozen=[coord/coord-carrier-2] launch=0 → PASS
换 LaunchAdmit：ZZLOCK frozen=[] launch=1 → FAIL
```

结论：原锁点在回合**结束后**的归零计数上断言，对两条准入路径不可区分；契约 §4-29 已改为要求「回合进行中占用读数」或「记录型准入调用」两种可区分锁点之一，推荐前者（与 §4-24 同族观测，不新造接缝）。

## (3) 扇出冻结条目：已随 (1) 写进 §4-33–35

新增 `§4-33`（两卡各唤醒一次、`processed==2`）、`§4-34`（游标越过该 seq、两卡认领均收尾）、`§4-35`（一卡被他机持有时不误伤扇出兄弟），期望值取自 (1) 的实跑读数；另加 `§4-36`（旧单键表升级到复合键的迁移处置）。

## 本轮命令与原文（工作树内，只跑不改）

```text
$ codegraph --repo . check            → CHECK_EXIT=0
$ codegraph --repo . resolve --doc docs/superpowers/specs/b389-contract.md
   → 9 anchors：6 ok / 3 moved / 0 坏（修订后复跑同读数）
$ git status --short                  → 仅 docs/ 两个文件改动
```

## 本轮欠账（显式）

- `(card,seq)` 复合键的 DDL/方法/索引落码归第 1 片返工（本轮只在副本验证）。
- §4-33–35 的 agentd 侧锁归第 4 片；§4-36 的旧表迁移归第 1 片 `Open` 处置。
- `codegraph/target.json` 仍未见 B389 条目（原欠账未变）。
