# B382 实现台账（人工登记工作分支来源）

> 卡 B382 · 实现节点 · plan `docs/superpowers/plans/b382-plan.md`
> 分支 `cards/B382-charter-3`。边干边落：每确立一个事实追加一行。
> 纪律：没跑到结果不写结论；跑了失败贴原文。

## §1 基线读数（实现节点亲跑）

- 2026-09-23：分支 `cards/B382-charter-3`，HEAD=`1412f188`（plan 提交）；`git status --short` 空。
- `go version` → `go1.26.1 linux/amd64`。
- `go build ./...` → `BUILD_EXIT=0`。
- `codegraph --repo . sym WorkBranch` → 命中 `n_ledger_Store_WorkBranch`（events.go:575，anchor=moved）。
- `codegraph --repo . who-calls n_ledger_Store_WorkBranch` → 上游：`Dispatcher.ViaTemplate`（dispatch.go）、`NodeStep.RunOnce`（node.go）、经 `StepRunner` 两级；另 `cardDispatchCmd.RunE`（cmd）经 runner。图命中，无覆盖债。
- 夹具确认：`seedStore`（cards_test.go:12）、`mk`（relations_test.go:8）、`dispatchTestCard`（dispatch_test.go:40）、`runLedgerCLI`（ledgercli_test.go:33）、`log()`（ledger.go:33）均在。
- `TestWorkBranchSkipsReviewRounds` 在 `internal/ledger/events_test.go:199`。

## §2 T1 红锚

- 首跑因 `encoding/json` 未使用编译红（T1 尚不用 json）；去掉该 import 后重跑。
- `go test ./internal/ledger/ -run TestB382WorkBranchErrorPointsToRegistration -count=1 -timeout 120s` → EXIT=1，断言红原文：
  `--- FAIL: TestB382WorkBranchErrorPointsToRegistration (0.12s)`
  `    b382_work_branch_test.go:27: 报错应指路登记（缺 "work-branch"）：卡 P1 没有非审阅的 dispatched 快照（还没派过实现轮？）: ledger: 记录不存在`
  失败原因核实为功能缺失（现状文案不含 work-branch），非 typo。红锚成立。

## §3 T2 实现（账本层）

- 改动：`internal/ledger/types.go` 加 `EvWorkBranchRegistered`；`internal/ledger/events.go`
  加 `WorkBranchRegistration`、`nonReviewDispatch`、`lastWorkBranchRegistration`、
  `RegisterWorkBranch`，重写 `WorkBranch`（读序：快照 → 登记 → 报错指路）。
- 追加 T2 六支用例到 `internal/ledger/b382_work_branch_test.go`（恢复 `encoding/json` import）。
- `go test ./internal/ledger/ -run 'TestB382' -count=1 -timeout 120s` → `ok github.com/Xsxdot/handoff/internal/ledger 0.814s` EXIT=0（含 T1 转绿）。
- 不误伤：`go test ./internal/ledger/ -count=1 -timeout 300s` → `ok … 22.536s` EXIT=0（含 `TestWorkBranchSkipsReviewRounds`）。
- `go vet ./internal/ledger/` → VET_EXIT=0；`go build ./...` → BUILD_EXIT=0。

## §4 T3 锁（ledgerstep，零生产改动）

- 落 `internal/ledgerstep/b382_work_branch_dispatch_test.go`（五支，生产代码零改动）。
- `go test ./internal/ledgerstep/ -run 'TestB382' -count=1 -timeout 120s` → `ok … 0.670s` EXIT=0。
- 不误伤：`go test ./internal/ledgerstep/ -count=1 -timeout 300s` → `ok … 14.828s` EXIT=0。
- `go vet ./internal/ledgerstep/` → VET_EXIT=0。

## §5 T4 CLI（cmd/card_work_branch.go）

- 落 `cmd/card_work_branch.go`（`cardWorkBranchCmd`，init 注册到 cardCmd）与
  `cmd/card_work_branch_test.go`。
- `go test ./cmd/ -run 'TestB382CardWorkBranchCLIEndToEnd' -count=1 -timeout 120s` → `ok … 0.209s` EXIT=0。
- 不误伤：`go test ./cmd/ -count=1 -timeout 600s` → `ok … 67.380s` EXIT=0（含 TestRepoContractGate）。
- `go vet ./cmd/` → VET_EXIT=0；`go build ./...` → BUILD_EXIT=0。

## §6 T5 变异复验（手动，不留代码）

锚唯一性断言（python count，三处均 =1）：`if !hasSnapshot {`、
`if hasSnapshot && info.Branch != ""`、`handoff card work-branch %s <分支>`。

三发均**先确认编译过**（输出无 build failed/undefined/cannot）再数失败；每发命中唯一
（replace(...,1) 前 count==1）；每发先跑最相关单测确认行为改变：

1. **M1** `if !hasSnapshot {` → `if false && !hasSnapshot {`（禁用登记回落）：
   `go test ./internal/ledger/ -run TestB382WorkBranchReturnsRegisteredBranch -count=1`
   → exit=1 compile_ok=True fail_count=1（登记不再被采纳 → 红）。
2. **M2** 把「登记先于快照返回」（语义对换优先级）：
   plan §5 原稿建议的 `hasSnapshot &&` 单独去掉是**语义等价变异**（登记块本就嵌在
   `!hasSnapshot` 下，去掉外层条件不改变行为，会得假绿读数）；按「变异要改语义」换为
   交换返回顺序（登记优先）。`-run TestB382WorkBranchSnapshotWinsOverRegistration`
   → exit=1 compile_ok=True fail_count=1（快照不再绝对优先 → 红）。
3. **M3** 新报错文案改回旧文「没有非审阅的 dispatched 快照（还没派过实现轮？）」：
   `-run TestB382WorkBranchErrorPointsToRegistration` → exit=1 compile_ok=True fail_count=1。

三发后 `internal/ledger/events.go` 字节级还原（file_restored=True），工作树只剩正式改动。

## §7 收口读数（全部亲跑）

- 变异还原后复跑：`go test ./internal/ledger/ -run 'TestB382'` → `ok … 0.806s`；`./internal/ledgerstep/ -run 'TestB382'` → `ok … 0.620s`；`./cmd/ -run 'TestB382'` → `ok … 0.191s`。
- `go build ./...` → B=0；`go vet ./internal/ledger/ ./internal/ledgerstep/ ./cmd/` → V=0。
- 全量触及包（T2/T3/T4 收尾各跑一次）：ledger `ok 22.536s`、ledgerstep `ok 14.828s`、cmd `ok 67.380s`，均 EXIT=0。
- `git status --short`：仅 6 个正式产出物（2 改 4 新），无临时文件。

## §8 收尾自审

- 错误分支带上下文：`nonReviewDispatch`/`lastWorkBranchRegistration`/`RegisterWorkBranch` 的
  wrap 均带「读卡…/登记工作分支: 卡 …」；成功路径 `RegisterWorkBranch` 一条 Info（card/branch/applied/actor）。
- 新文件头注释 + 导出函数文档注释：`RegisterWorkBranch`/`WorkBranch`/`WorkBranchRegistration`/
  `EvWorkBranchRegistered`/两个测试文件头/cmd 文件头齐。
- 与 plan Interfaces 一致：`RegisterWorkBranch(cardID, branch, actor) (applied bool, err error)`、
  `EvWorkBranchRegistered`、`WorkBranchRegistration{Branch}`、`cardWorkBranchCmd` 逐字对齐。
- 未碰 handoff CLI、未派发、未起新 executor；codegraph 已用（sym/who-calls 命中，无覆盖债）。

