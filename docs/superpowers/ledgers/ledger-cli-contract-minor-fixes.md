# 协调者审阅回路契约小修批执行记录

| 记录 | 范围 | 结果 |
|---|---|---|
| Task 1 门禁首轮 | `96f3c383..工作树` | `TestWSDeadlineStaysWithinBounds` 单测通过；全量首轮只出现后续确认的 16 条豁免项，尚未形成 commit。|
| 配方已知副作用 | Task 1 全量测试 | 8 条以「临时目录不是 git 仓库」为前提的用例在仓库内临时目录下假红，未改动：`TestMainWorktreeRootRejectsNonRepo`、`TestRegisterProjectClaimRejectsNonRepoDest`、`TestRepoWorktreesFailsOnNonRepo`、`TestReclaimListDegradesPerRepo`、`TestEnsureRepoUsableRejectsNonGitPath`、`TestProjectAPIRejectsNonRepoWithReadableReason`、`TestProjectAddRejectsNonRepo`、`TestMarkIgnoredNonRepoFailsOpen`。|
| 环境受限未验证 | `TestInstallScriptUnits` | `install_test.sh` 使用裸 `mktemp -d`，BSD `mktemp` 忽略 `TMPDIR` 并落到 `/var/folders`，沙箱拒绝：`mktemp: mkstemp failed on /var/folders/xc/hpx9c9w153j7tvphw53lc8qr0000gn/T/tmp.94HUOqpb0z: Operation not permitted`。未改动安装测试或脚本，未验证。|
| 环境受限未验证 | 8 条进程枚举链用例 | `internal/prochost` 的 `kern.proc.uid` / `kern.maxprocperuid` 被沙箱拒绝，原始错误含 `operation not permitted`；下游 roster、footprint、status 用例随之出现空结果。未改动：`TestEnumProcsFindsSelf`、`TestEnumProcsFindsSelfPGID`、`TestProcLimitPositive`、`TestStartRecordsStartedAt`、`TestShimWritesRosterImmediately`、`TestStatusFillsProcsForActiveTasks`、`TestFootprintAllCoversArchivedTasks`、`TestFootprintAllReportsVerdict`。均未验证。|
| Task 1 门禁首轮豁免清单 | `96f3c383..工作树` | 7 条非 git 临时目录用例、`TestInstallScriptUnits`、8 条进程枚举链用例均按当时适用口径豁免；除此之外未记录到失败。|
| Task 1 门禁第 2 轮 | `96f3c383..工作树` | 失败清单仅为 8 条非 git 临时目录用例（含新识别的 `TestMarkIgnoredNonRepoFailsOpen`）、`TestInstallScriptUnits`、8 条进程枚举链用例；均按豁免口径处理。|
| Task 1 门禁第 3 轮 | `96f3c383..工作树` | 失败清单仅为上述同一组豁免项；未记录非豁免失败。|
| Task 1 门禁第 4 轮 | `96f3c383..工作树` | 失败清单仅为上述同一组豁免项；未记录非豁免失败。|
| Task 1 门禁第 5 轮 | `96f3c383..工作树` | 失败清单仅为上述同一组豁免项；未记录非豁免失败。|
| Task 1 B125 靶子 | 工作树 | `TestWSTruncationWarnsOnRealGap` 与 `TestWSDeadlineStaysWithinBounds` 连续 5 轮均 PASS。|
| Task 1 完成 | `96f3c383..ca446ebe` | 双裁决：spec 符合（四处期限改走 `wsDeadline`，helper 不低于 base 且不超过 3 倍，指定注释与测试齐全）；代码质量通过（`gofmt -l .` 无输出、`git diff --check` 通过）。按计划提交 `ca446ebe`。|
| Task 2 门禁 | `ca446ebe..工作树` | 定向 `TestGitProbe|TestGitRunMiss` 三测通过；`go build ./...`、`go vet ./...`、`gofmt -l .` 通过。全量回归只出现既知豁免与“待协调者沙箱外复核”项。|

## 待协调者沙箱外复核

| 用例 | 所在包 | 原始报错 | 与本次改动无关的依据 |
|---|---|---|---|
| `TestInstallScriptUnits` | 根包 | `mktemp: mkstemp failed on /var/folders/xc/hpx9c9w153j7tvphw53lc8qr0000gn/T/tmp.94HUOqpb0z: Operation not permitted` | 本次 Task 2 只改 agentd 的 git 调用与测试，不改安装脚本；失败来自裸 `mktemp -d`。|
| `TestResizeDuringShellExitIsRaceFree` | `internal/ptyhost` | `--- FAIL: TestResizeDuringShellExitIsRaceFree (15.00s)`；`ptyhost_test.go:319: 等待 shell 退出超时` | 本次 Task 2 只改 `internal/agentd/workspace.go`、`internal/agentd/manualworktree.go`、`internal/agentd/gitprobe_test.go`，未触及 ptyhost；失败形态是系统 PTY/shell 资源等待超时。|
| 8 条进程枚举链用例 | `internal/prochost` / `internal/agentd` | 原始输出含 `sysctl kern.proc.uid: operation not permitted`、`sysctl kern.maxprocperuid: operation not permitted`，下游出现 roster/footprint/status 空结果 | 本次 Task 2 未改进程枚举或其下游；失败是沙箱拒绝系统 sysctl 能力。|
| `TestMarkIgnoredNonRepoFailsOpen` | `internal/agentd` | `gitignore_test.go:88: 非 git 目录里的条目被标成了忽略` | 用例用 `t.TempDir()` 并明确以“目录不是 git 仓库”为前提；配方将临时目录放入当前 git 仓库，失败形态是非 git 前提失效；本次 Task 2 未改 gitignore。|
| `TestWSTruncationWarnsOnRealGap` | `internal/agentd` | `--- FAIL: TestWSTruncationWarnsOnRealGap (30.01s)` | 该用例虽在 Task 1 改动文件中，但 B125 靶子单跑 `-count=5` 与协调者沙箱外全量 `go test ./... -count=2` 均通过；本次沙箱全量的超时属于系统负载开销。未改 `wsDeadline`。|

| 基线既有：`TestDispatchAutoRegisterSurvivesMissingLocalAgentd` | `cmd` | `cmd` 包 `-count=2` 下失败；`-count=1` 与单独 `-count=2` 通过 | 协调者已在 `96f3c383` 基线复现并确认是 cmd 包跨用例全局状态污染，与本批无关；另开条目，不改测试组织。|
| Task 2 完成 | `ca446ebe..8d8563e6` | 双裁决：spec 符合（`gitExec` 共享执行体、`gitProbe` 只降级预期探测失败、进程配额仍 Error、8 个指定探测点替换且 symbolic-ref 保持不动）；代码质量通过（定向三测、build、vet、gofmt、git diff check）。计划测试引用了外部包 helper，已用同构的同包 `initTestRepo` 修正包边界。按计划提交 `8d8563e6`。|
| Task 3 完成 | `8d8563e6..d81327af` | 双裁决：spec 符合（`diffBaseFor` 优先任务 BaseCommit、两个端点一致、`task_base` 三态展示、日志与文档同步）；代码质量通过（后端三测、web 7/7 与全量 740/740、typecheck、build、gofmt、git diff check；lint 0 errors/13 existing warnings）。按计划提交 `d81327af`。|
| Task 4 门禁 | `d81327af..fc1e97a0` | `TestShellJoin` 六个子用例与既有 `TestRunCommandPassesArgsVerbatim` 通过；build/vet/gofmt 通过；全量回归原始失败仅含已知沙箱受限项（含 `TestWSTruncationWarnsOnRealGap (30.01s)`），未改 Task 1。|
| Task 4 完成 | `d81327af..fc1e97a0` | 双裁决：spec 符合（单参数原文透传、多参数逐个 POSIX 单引号转义、stderr 可观测、服务端线格式未动、文档同步）；代码质量通过（新增六测与既有 cmd 测试通过、build、vet、gofmt、git diff check）。按计划提交并修正注释后 amend 为 `fc1e97a0`。|
| 终审变异：gitProbe quiet | `96f3c383..fc1e97a0` | 临时将 Debug 改回 Error；`TestGitProbeMissDoesNotLogError` 按预期翻红，原始失败含 `不该产生 ERROR` 与 `应留一条 DEBUG`；已恢复。|
| 终审变异：进程配额分支 | `96f3c383..fc1e97a0` | 手工核对 `quotaNote` 分支位于 `if quiet` 之前，直接 `log().Error("git 调用失败（进程配额）")` 并返回；不会受 quiet 影响。|
| 终审变异：diffBaseFor 优先级 | `96f3c383..fc1e97a0` | 临时移除 BaseCommit 分支；`TestDiffBaseForPrefersTaskBaseCommit` 按预期翻红，`got="main" want="012345..."`；已恢复。|
| 终审变异：branches task_base | `96f3c383..fc1e97a0` | 临时移除响应字段；`TestBranchesEndpointReportsTaskBase` 按预期翻红，`task_base 不对：got="" want="012345..."`；已恢复。|
| 终审变异：shellJoin 单参数 | `96f3c383..fc1e97a0` | 临时让单参数也走 shellQuote；单参数回归用例按预期翻红，实得带整段单引号；已恢复。|
| 终审变异：shellJoin 多参数 | `96f3c383..fc1e97a0` | 临时退回 `strings.Join(args, " ")`；含空格与内嵌单引号两个子用例按预期翻红；已恢复。|
| 终审变异：B125 | `96f3c383..fc1e97a0` | 本条无变异靶子；按计划如实记账。|
| 整分支范围复审 | `96f3c383..fc1e97a0` | 通读 15 个变更文件；无超出四条契约范围的实现、无线格式变更、无 `internal/localsync` 或 `runshell.go` 改动。恢复后的后端与命令新增用例通过。|

待复核项逐条原始记录：

| 用例 | 所在包 | 原始报错 | 与本次改动无关的依据 |
|---|---|---|---|
| `TestEnumProcsFindsSelf` | `internal/prochost` | `procenum_test.go:22: enumProcs 失败: sysctl kern.proc.uid: operation not permitted` | 本批未改进程枚举；失败是沙箱拒绝 sysctl。|
| `TestEnumProcsFindsSelfPGID` | `internal/prochost` | `procenum_unix_test.go:20: enumProcs 失败: sysctl kern.proc.uid: operation not permitted` | 本批未改进程枚举；失败是沙箱拒绝 sysctl。|
| `TestProcLimitPositive` | `internal/prochost` | `procenum_test.go:69: procLimit 失败: sysctl kern.maxprocperuid: operation not permitted` | 本批未改进程上限读取；失败是沙箱拒绝 sysctl。|
| `TestStartRecordsStartedAt` | `internal/prochost` | `footprint_test.go:193: Start 未记录 StartedAt，got 0` | 下游依赖受拒绝的进程枚举；本批未改 prochost。|
| `TestShimWritesRosterImmediately` | `internal/prochost` | `shim_test.go:338: 10s 内没等到非空名册（path=.../roster.json）` | 下游名册依赖进程枚举；本批未改 prochost，属于系统资源等待。|
| `TestStatusFillsProcsForActiveTasks` | `internal/agentd` | `status_test.go:288: 活跃任务应带 Procs（取不到时也该留 nil，见下）` | 失败由进程枚举受限向下游传播；本批只改 git 探测、diff、run 与 WS 测试。|
| `TestFootprintAllCoversArchivedTasks` | `internal/agentd` | `status_test.go:328: 体检结果里没有已归档任务 T-archived（共 0 行）` | 失败由进程枚举受限向下游传播；本批未改 status/prochost。|
| `TestFootprintAllReportsVerdict` | `internal/agentd` | `status_test.go:341: 应至少有一行` | 失败由进程枚举受限向下游传播；本批未改 status/prochost。|
| web 全量 Vitest（首次直接执行） | `web` | `Error: EPERM: operation not permitted, mkdir '/var/folders/xc/hpx9c9w153j7tvphw53lc8qr0000gn/T/MU4n4ZZSQZdgt1owe3-xd/client'`，69 个 suite 均为 0 tests | 失败发生在测试变换前的系统临时目录创建；用工作区 `TMPDIR` 重跑后 69 files/740 tests 全部通过，本批前端改动未触发断言失败。|

| 最终 B125 靶子 | `internal/agentd` | `go test ./internal/agentd -run 'TestWSTruncationWarnsOnRealGap|TestWSDeadlineStaysWithinBounds' -count=5`：PASS，0.719s | 两个 B125 靶子五轮通过。|
| 最终 web 门禁 | `web` | Vitest 69 files/740 tests PASS；typecheck PASS；lint 0 errors/13 existing warnings；build PASS | 使用工作区临时目录，仅记录既有 lint/build warnings，不改动。|
| 最终范围检查 | `96f3c383..fc1e97a0` | `go build ./...`、`go vet ./...`、`gofmt -l .`、`git diff --check` PASS；race/full Go 的原始受限项均已在上表记录 | 代码与测试恢复干净，待提交本 ledger。|
