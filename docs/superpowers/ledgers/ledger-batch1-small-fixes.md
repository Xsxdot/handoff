# 批 1 琐碎修 ledger（B120 / B121 / B82 / B101）

职责：记录本批每个 task 的实现裁决、修复轮次、提交范围与亲自执行过的验证结果，供恢复现场时与 git log 对账。
边界：只记录本批四条缺陷及 Task 5 终审；不把 Windows 真机欠账写成已验证，也不记录未执行的命令结论。
分支起点：`a87c4408`（`docs(plan): 批 1 琐碎修实现计划——5 个 task，四条改动互不依赖`）。

## 执行记录

- Task 1 / 环境复核｜`go test ./internal/agentd/ -run TestProjectNameFromURLHandlesWindowsSeparators -v` 首次原始失败：`go: creating work dir: mkdir /var/folders/xc/hpx9c9w153j7tvphw53lc8qr0000gn/T/go-build572336725: operation not permitted`；随后将 Go 临时目录定向到仓库内临时目录重跑。
- Task 1 / 修复轮 1｜B120｜提交范围 `c557eea7`｜新增 Windows 本地路径与既有 URL 回归用例，`projectNameFromURL` 同时识别 `/`、`\\`、`:`；目标测试由失败变为 5 个子用例通过。
- Task 1 / 完成裁决｜spec 符合、代码质量通过；`gofmt -l .` 无输出，`git diff --check` 通过；`go test ./internal/agentd/` 原始失败摘录如下：

  ```text
  --- FAIL: TestMainWorktreeRootRejectsNonRepo (0.01s)
      gitroot_test.go:55: err = <nil>, want errors.Is(..., ErrRepoUnusable)
  --- FAIL: TestRegisterProjectClaimRejectsNonRepoDest (0.03s)
      projectadmin_test.go:309: err = <nil>, want errors.Is(..., ErrProjectAlreadyExists)
  --- FAIL: TestRepoWorktreesFailsOnNonRepo (0.01s)
      reclaim_test.go:203: 非 git 仓库应返回错误，实得 nil
  --- FAIL: TestReclaimListDegradesPerRepo (0.12s)
      reclaim_test.go:460: 不可达仓库的行必须标 unknown 而不是消失，实得 [{TaskID:t-1787044565608873000 Name: State:failed Branch:f-l4 WorkDir:/Users/sycm/.handoff/worktrees/f547184a/.codex-tmp/build/TestReclaimListDegradesPerRepo1162922057/wt-l4 Worktree:dirty DirtyCount:1 Note:}]
  --- FAIL: TestEnsureRepoUsableRejectsNonGitPath (0.01s)
      workspace_test.go:932: 非 git 目录 err = <nil>, want ErrRepoUnusable
  --- FAIL: TestProjectAPIRejectsNonRepoWithReadableReason (0.10s)
      integration_test.go:1187: 响应体未带 git 原文: {"error":"路径上的仓库与请求的项目不是同一个: /Users/sycm/.handoff/repos/handoff 上的 origin 是 git@github.com:Xsxdot/handoff.git，而请求的项目是 git@example.com:org/x.git；换个路径，或去掉 --path 让本机自己 clone"}
  --- FAIL: TestStatusFillsProcsForActiveTasks (0.01s)
      status_test.go:288: 活跃任务应带 Procs（取不到时也该留 nil，见下）
  --- FAIL: TestFootprintAllCoversArchivedTasks (0.01s)
      status_test.go:328: 体检结果里没有已归档任务 T-archived（共 0 行）
  --- FAIL: TestFootprintAllReportsVerdict (0.00s)
      status_test.go:341: 应至少有一行
  FAIL
  FAIL	github.com/Xsxdot/handoff/internal/agentd	50.942s
  ```

- Task 2 / 修复轮 1｜B121｜提交范围 `db492617`｜测试助手增加 `goos` 缝并新增 Windows/darwin 用例；按计划先得到原始编译失败：`internal/toolchain/detect_test.go:17:73: undefined: goos`、`:31:2: undefined: goos`、`:32:54: undefined: goos`。
- Task 2 / 完成裁决｜spec 符合、代码质量通过；`go test ./internal/toolchain/ -v` 通过，`go test ./internal/toolchain/ -run FirstReady -v` 原始结果为 `testing: warning: no tests to run` 后 `PASS`，`gofmt -l .` 无输出；Windows 分支只有 `goos = "windows"` 单测证据。
- Task 3 / 修复轮 1｜B82｜提交范围 `8052c25c`｜新增 `ErrWorkdirGone`、RunCmd stat 判据、路由 400 映射与三条用例；先按计划得到原始编译失败：`internal/agentd/workspace_run_test.go:30:21: undefined: ErrWorkdirGone`。
- Task 3 / 修复轮 2｜提交范围 `8052c25c`｜路由用例第一次复跑的原始失败为：`workspace_run_test.go:69: 状态码应为 400，实为 401，响应体 {"error":"未授权"}`；在本用例内对齐既有固定 Bearer 后通过，未修改 HTTP 助手。
- Task 3 / 完成裁决｜spec 符合、代码质量通过；`go test ./internal/agentd/ -run 'TestRunCmd|TestTaskRunMissingWorkdirReturns400' -v` 通过，含既有 RunCmd 回归；`gofmt -l .` 无输出；整包 `go test ./internal/agentd/` 的失败原始行与 Task 1 记录相同。
- Task 4 / 修复轮 1｜B101｜提交范围 `bc024651`｜新增 `resolveModel` 四个表驱动用例；按计划先得到原始编译失败：`internal/agentd/manager_test.go:2854:16: m.resolveModel undefined (type *Manager has no field or method resolveModel)`。
- Task 4 / 完成裁决｜spec 符合、代码质量通过；四个 `TestResolveModelOnlyAppliesToDefaultExecutor` 子用例通过，`gofmt -l .` 无输出；整包 `go test ./internal/agentd/` 的原始失败仍为 Task 1 记录的 9 条。
- Task 5 / 全量门｜提交范围 `c557eea7..094bdaed`｜`go build ./...` 通过、`go vet ./...` 通过；终审修复后再次执行 build/vet，二者均退出码 0；终审修复后 `gofmt -l .` 无输出，`git diff --check` 通过。
- Task 5 / 全量门失败原始摘录｜`go test ./...` 实跑失败：

  ```text
  --- FAIL: TestInstallScriptUnits (0.03s)
      install_test.go:15: install.sh 单测失败:
          mktemp: mkstemp failed on /var/folders/xc/hpx9c9w153j7tvphw53lc8qr0000gn/T/tmp.O3QruY38vM: Operation not permitted
  --- FAIL: TestProjectAddRejectsNonRepo (0.01s)
      panic: nil Context
  --- FAIL: TestPermServerAskThenRespond (0.00s)
      perm_test.go:56: newPermServer: 裁决 socket 路径过长（108 字节，上限 107）
  --- FAIL: TestResumeContinuesFromOffset (0.00s)
      resume_test.go:89: Resume 应判活并续读: alive=false err=裁决 socket 路径过长（110 字节，上限 107）
  --- FAIL: TestStartRecordsStartedAt (0.00s)
      footprint_test.go:193: Start 未记录 StartedAt，got 0
  --- FAIL: TestEnumProcsFindsSelf (0.00s)
      procenum_test.go:22: enumProcs 失败: sysctl kern.proc.uid: operation not permitted
  --- FAIL: TestProcLimitPositive (0.00s)
      procenum_test.go:69: procLimit 失败: sysctl kern.maxprocperuid: operation not permitted
  --- FAIL: TestEnumProcsFindsSelfPGID (0.00s)
      procenum_unix_test.go:20: enumProcs 失败: sysctl kern.proc.uid: operation not permitted
  --- FAIL: TestShimWritesRosterImmediately (10.07s)
      shim_test.go:338: 10s 内没等到非空名册（path=/Users/sycm/.handoff/worktrees/f547184a/.codex-tmp/build/TestShimWritesRosterImmediately2112841158/001/roster.json）
  FAIL
  ```

- Task 5 / race 门｜`go test -race ./internal/agentd/ ./internal/toolchain/ ./cmd/` 实跑：toolchain 输出 `ok  github.com/Xsxdot/handoff/internal/toolchain 1.872s`；agentd 原始失败仍含 `gitroot_test.go:55: err = <nil>, want errors.Is(..., ErrRepoUnusable)`、`status_test.go:288: 活跃任务应带 Procs（取不到时也该留 nil，见下）`；cmd 原始失败含 `panic: nil Context`。
- Task 5 / 终审修复轮 1｜B82｜提交范围 `094bdaed`｜路由 Warn 补充 `repo` 字段，使该拒绝日志同时带 `task` 与 `repo`；定向 `TestRunCmd` 与 `TestTaskRunMissingWorkdirReturns400` 全部通过，范围复审无超范围文件。

## 真机欠账

- B120：Windows 本地路径 origin 自动登记未在 Windows 执行机真跑，只有单测证据。
- B121：Windows 分支未在真 Windows 确认 opencode 实际凭证落点，只有 `goos = "windows"` 单测证据。
- B82、B101：无平台真机欠账，已亲自跑到对应单测证据。
