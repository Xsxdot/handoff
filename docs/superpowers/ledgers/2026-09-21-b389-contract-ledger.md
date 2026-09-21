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
