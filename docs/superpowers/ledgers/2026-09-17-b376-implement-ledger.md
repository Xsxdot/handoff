# B376 implement 台账

基线：`cards/B233.1-charter-7` @ `bcea5c33`（工作树起于 `cards/B376-charter-3`，按 extra 先 `git fetch origin cards/B233.1-charter-7` + `git reset --hard origin/cards/B233.1-charter-7`）。

## 图查询

- `codegraph --repo . summary` / `sym Gate.Judge` / `sym safeCommandID` / `sym Approver.Decide` / `sym Client.Request` 均命中。
- `codegraph who-calls Gate.Judge`：仅命中 `approval.Authority.Judge`（`internal/approval/authority.go:160`），生产调用方仍以源码为准（`Client.Request` 经 `Hooks.JudgePermission`）。与 spec「图覆盖债」一致，无新增债。

## Task 1 拆段静默 + 白名单补洞

- 先红：`go test ./internal/permgate/ -count=1`。正例全红，原始输出样本：
  `command "grep a && grep b" verdict = permgate.Verdict{Action:1, Reason:"黑名单未命中", Rule:""}, want AutoAllow safe-command`。
  Action=1 即 Consult —— 红因是「功能缺失」，不是 typo；反例保持非 AutoAllow。
- 实现：`splitSilentSegments` / `splitPipeStages` / `silentFields` / `matchSilentSegment` / `compoundSilent` / `matchSilentFields`（单段表抽出复用）；`judgeBash` 在原 `safeCommandID` 未命中后接 `compoundSilent`。白名单仍只认单段，未改 `splitSafeCommand` 吞 `&&`。
- 绿：`go test ./internal/permgate/ -count=1` → `ok`。
- 变异自验（均先确认编译通过）：
  1. `compoundSilent` 循环内 `!ok` → `continue`（全段放行）→ `TestJudgeCompoundNotSilent` 等 4 例红。
  2. `git branch` 的 `-d/-D/...` 守卫整块删除 → `TestJudgeCompoundNotSilent` 红。
  3. `hasNonDiscardWriteRedirect` 短接 `if false && ...` → 全绿（该守卫在 `matchSilentFields` 内还有 `echo/printf` 重定向守卫，此变异不改变行为，判为未打中而非测试无牙，改用上面两条唯一守卫）。

## 发现并修复的安全回归（自行发现，非 plan 明列）

- 加 `echo` 进白名单后，`echo "$(rm -rf x)"` 从基线 Consult 变成 AutoAllow（`splitSafeCommand` 引号状态机把引号内当字面量，词元只看得到 echo）。
- 修法：`hasCommandSubstitution` 独立扫原文拦 `` ` `` 与 `$(`；`matchSilentFields` 入口调用。纯变量展开 `$VAR`/`${VAR}` 不拦（不执行命令，且 `echo "$HOME"` 高频）。
- 复测：`echo "$(rm -rf x)"` → consult；`grep "$(cat /etc/passwd)" f` → consult（基线此处是 AutoAllow，顺带补上一个既存洞）；``echo `rm -rf x` `` → escalate（黑名单）。
- 新测试 `TestJudgeCommandSubstitutionNotSilent`；变异短接守卫后红，确认有牙。
- `echo "$HOME"` → auto_allow（预期）。

## Task 2 executor 列表 + Decide failover

- 先红（编译红，新符号缺席）：`config.ExecutorList` undefined、`BindOneShotFor` undefined、`ApproverDecision.Executor/Attempts` undefined、`approach.Attempts` 等。
- 实现：`config.ExecutorList`（标量/序列 `UnmarshalYAML`）、`Empty()`、validate 拒绝空串元素与重复名；`Approver.names []string` + `shots map`、`BindOneShotFor`、`Decide` 逐候选循环、`ApproverAttempt`；`bindApproverOneShot` 收 `[]string`，全绑不上启动失败、部分缺失 Warn。
- 机械替换：19 处 `ApproverConfig{Executor: "x"}` → `config.ExecutorList{"x"}`；`initflow` 单值写回；`manager.go` 用 `!Empty()`；`cmd/init_test.go` 用 `.Empty()`。
- 绿：`go test ./internal/config/ ./internal/orchestration/ -count=1` → `ok`。
- 变异自验：
  1. `attempt.Err == nil` → `attempt.Err == nil && attempt.Decision == "approve"`（干净 escalate 也继续 failover）→ `TestDecideFailoverCleanEscalateDoesNotTryNext` 红。
  2. config validate 的重复名守卫删除 → `TestLoadApproverExecutorListRejectsDuplicates` 红。
- 副作用：`decisionLabel` 成死代码，删除（`Decide` 内联 `decision` 计算）。

## Task 3 失败可观测 + 停用理由

- 先红：`approval.Hooks` 缺 `ConsultDisabledReason`、`ConsultDecision` 缺 `Executor/Attempts` 等。
- 实现：`ConsultDecision` 增 `Executor`/`Attempts []ApproverAttempt`；`consult` 有 Attempts 时每候选各写一条 `approver_decision`（payload 增 `executor`），空 Attempts 保持单条；`Request` 在 Escalate 前用 `ConsultDisabledReason` 改写理由；`Manager.consultDisabledReason` 停用时返回「审批链已停用（连续 3 次失败）」；`bindApproval` 绑钩子并投影 Attempts。
- 绿：`go test ./internal/approval/ ./internal/orchestration/ -count=1` → `ok`。
- 变异自验：
  1. `Request` 的停用理由改写整块短接 `if false && ...` → `TestClientDisabledEscalatesWithDisabledReason` 红。
  2. `dec.Attempts` 改 `dec.Attempts[:1]`（只写第一条）→ `TestClientConsultWritesAttemptEvents` 红。
- 序列化边界：attempt 事件从 Store 反序列化后断言 `executor` 非空（穿真实 JSON）。

## 收尾检查

- `go build ./...` → 通过。
- `go vet ./...` → 无输出。
- `gofmt -l` 触及文件 → 无输出（`cmd/room_test.go`、`cmd/session.go`、`cmd/session_test.go` 的格式债为基线既有，未触碰）。
- 触及包测试：`permgate` / `config` / `orchestration` / `approval` / `initflow` 全绿。
- 全 test 包编译：`go test ./... -run '^$' -count=1` 无编译失败。
- 已知基线红：`cmd.TestRepoContractGate`（graph dead-contract，`d_transport→d_ledger`），`git stash` 后在基线同样红，与本卡无关。

## 提交

提交当时的命令与原始输出（历史读数；本条随同批 amend 收进）：

```
$ git commit -m "feat(B376): 权限门降噪——复合只读拆段静默 + 审批者 failover + 停用可观测 ..."
[cards/B376-charter-3 c02e642d] feat(B376): 权限门降噪——复合只读拆段静默 + 审批者 failover + 停用可观测
 19 files changed, 1156 insertions(+), 142 deletions(-)
 create mode 100644 docs/superpowers/ledgers/2026-09-17-b376-implement-ledger.md
$ git status --short
（空）
```

amend 后 HEAD 变化是 git 事实；收口判据是工作树干净，不以文件里的 hash 等于 HEAD。

## 复审修复轮（review fail 三项 major）

上一轮 review fail 指出三项 major + 一项 minor，逐项处置：

1. **sed -i 后缀绕过**：`sed -n -i.bak` / `-i'.bak'` / `--in-place=.bak` / `-ni.bak`
   仍 AutoAllow。先写反例进 `TestJudgeCompoundNotSilent`，红：
   `compound non-readonly "sed -n -i.bak 's/a/b/' f" was auto-allowed`。
   实装 `hasSedInPlace`：短选项簇按字母扫 `i`、`--in-place` 前缀匹配。绿。
   变异（`!hasSedInPlace` → `!false`，命中唯一）→ 该反例红，确认有牙。
2. **rg --pre 执行型标志**：`rg --pre '...'` / `--pre=...` 仍 AutoAllow（拉起任意程序）。
   反例红后实装 `hasRGExecuteFlag`；`--pre-glob` 非执行仍静默（正例）。变异后红。
3. **git branch -M/-C 强制改名**：`-M`/`-C` 原整词表遗漏。实装 `hasGitBranchMutator`：
   短选项簇字母扫、长选项前缀匹配（`--move=` 亦中）。变异后红。

**本轮自行发现的第 4 项回归（比 minor 更重）**：`cd` 静默段让后续参数位写命令的
相对落点被按 Workdir 误判为范围内——`cd /etc && gofmt -w passwd` AutoAllow，实际写
`/etc/passwd`。重定向落点有 `hasNonDiscardWriteRedirect` 全拦，参数位写命令（gofmt -w /
git diff --output=）没有。反例红后实装：`compoundSilent` 里 `cd` 后的段若含相对参数位
写落点则整条不静默（绝对落点与丢弃设备不受影响）。变异后红。

正例回归补：`rg -n`、`rg --pre-glob`、`git branch -a/--list`、`sed --quiet`。

复审命令与原始输出（历史读数）：

```
$ go test ./internal/permgate/ -count=1
ok  	github.com/Xsxdot/handoff/internal/permgate	0.009s
$ go build ./...
build-exit=0
$ go vet ./internal/permgate/
（无输出）
$ go test ./internal/permgate/ ./internal/approval/ ./internal/orchestration/ ./internal/config/ -count=1
ok  	.../internal/permgate	0.009s
ok  	.../internal/approval	1.079s
ok  	.../internal/orchestration	45.633s
ok  	.../internal/config	0.013s
$ go test ./... -run '^$' -count=1
（全 test 包编译通过，无 build failed）
```

M1/M3/M4/M5/M6 既有缝未触碰、未回退。`cd` 段带工作目录的 minor 以「参数位写落点
守卫」封住（重定向落点本就有守卫）；未做完整 cwd 跟踪，成本可控且安全面无缺口。

### 本修复轮提交

提交当时的命令与原始输出（历史读数；本条随同批 amend 收进）：

```
$ git commit -m "fix(B376): 复审驳回三项——sed -i 后缀守卫 + rg --pre 执行型标志 + git branch 强制改名 ..."
[cards/B376-charter-4 c82252a8] fix(B376): 复审驳回三项——sed -i 后缀守卫 + rg --pre 执行型标志 + git branch 强制改名
 3 files changed, 188 insertions(+), 7 deletions(-)
$ git status --short
（空）
```

amend 后 HEAD 变化是 git 事实；收口判据是工作树干净，不以文件里的 hash 等于 HEAD。


