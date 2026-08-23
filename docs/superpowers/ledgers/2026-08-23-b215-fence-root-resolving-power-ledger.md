# B215 实现台账：围栏用例改断直接性质，root 机器上恢复分辨力

任务：0fc9b461-9d28-445d-88f3-cdf8e28311b7
分支：cards/B215-implement
基线：eb4d7f18a2f92fd9e69abba6a7db27a455b862d9（merge(b212)）

## 进度

- 事实：当前分支 `cards/B215-implement`，工作区在本轮开始时干净；`git log -1 --oneline` 为 `eb4d7f18 merge(b212): executor 的任务 tmp 改短路径，派发环境的全量测试不再必红`。
- 事实：本机 `id -u` 输出 `0`（root 执行机）；`go version go1.26.1 linux/amd64`。
- 命令：`go test ./internal/prochost/ -count=1`
- 原始输出：最终 `FAIL  github.com/Xsxdot/handoff/internal/prochost  6.858s`；退出码 `1`（经 `${PIPESTATUS[0]}` 确认）。其它用例通过，含 TestFenceSurvivesSetsid（plan 所述 root 下仍绿）。
- 命令：`go test ./internal/prochost/ -run 'TestFenceCannotBeRaisedBack' -count=1 -v`
- 原始失败：`fence_inherit_test.go:147: 围栏应拆不掉，helper 报告 "RAISE_OK"`；`--- FAIL: TestFenceCannotBeRaisedBack (0.00s)`；最终 `FAIL`，退出码 `1`。
- 判断：基线复现 plan 所述 root 下恒红——`RAISE_OK` 说明在 CAP_SYS_RESOURCE 下 helper 能把硬限抬回，间接证据（RAISE_DENIED）在此环境失效，需按 plan 改断直接性质。
- 修改：实现 subagent 完成 T1+T2+T3，只改 `internal/prochost/fence_unix.go` 与 `internal/prochost/fence_inherit_test.go` 两个文件。T1 新增 `getNprocLimits() (soft, hard int, err error)`（软硬限各按 RLIM_INFINITY 钳到 math.MaxInt32），未动 `getNprocLimit` 签名语义；T2 helper `TestHelperFenceRaise` 在 `setNprocLimit(want)` 成功后读硬限，输出改 `RAISE_DENIED HARD=<n>` / `RAISE_OK HARD=<n>`，读硬限失败输出 `GETFAIL ...`；T3 `TestFenceCannotBeRaisedBack` 两段判据——第 1 段恒断言 `HARD == want`（所有特权档都跑），第 2 段仅 `os.Geteuid() != 0` 时断言行以 `RAISE_DENIED` 开头，root 上用 `return`（非 t.Skip）并注释「测我们写的，不测内核写的」。
- 命令：`gofmt -l internal/prochost/fence_unix.go internal/prochost/fence_inherit_test.go`，无输出，退出码 0。
- 命令：`go build ./...`，无输出，退出码 0。
- 命令：`go test ./internal/prochost/ -run TestFenceCannotBeRaisedBack -count=1 -v`，`--- PASS: TestFenceCannotBeRaisedBack (0.00s)`，`ok  github.com/Xsxdot/handoff/internal/prochost  0.003s`，退出码 0。
- 命令：`go test ./internal/prochost/ -count=1`，`ok  github.com/Xsxdot/handoff/internal/prochost  2.995s`，退出码 0。
- 命令：`go vet ./internal/prochost/`，无输出，退出码 0。
- 审查：T1-T3 双裁决 PASS。spec PASS——getNprocLimits 软硬两值均钳值、getNprocLimit 未动、helper 同行报硬限、T3 第 1 段恒断言第 2 段按 euid 分档、root 走 return 非 t.Skip、注释回答「测我们写的不测内核写的」、范围仅两文件；quality PASS——gofmt/vet 干净、解析逻辑正确、无死代码。提交范围 `12177555`。
- 提交：`121775551c99a7eeff5b13e31bb5e2ca579db7c5`（`test(prochost): 围栏用例改断直接性质，root 下恢复分辨力`），当前分支 `cards/B215-implement`。
- 事实：非 root 执行环境搭建——`/root` 为 `drwx------`，`ubuntu`（uid 1000）无法穿越到 worktree；将整个 worktree 复制到 `/tmp/b215-nonroot` 并 `chmod -R a+rX`，以 `runuser -u ubuntu` 运行测试。基线：修正后实现下非 root `TestFenceCannotBeRaisedBack` 也绿（`ok  github.com/Xsxdot/handoff/internal/prochost  0.004s`），证明该用例在两种特权档下都真正通过。
- 变异一（守 T3 第 1 段）：把 `setNprocLimit` 改为只设软限（`rl := unix.Rlimit{Cur: uint64(n), Max: <原硬限>}`，临时读取原硬限）。**root 上下文**原始失败：`fence_inherit_test.go:167: 装围栏后硬限应为 28210，实际为 28211（helper 完整输出 "RAISE_OK HARD=28211"）`，`FAIL`，退出码 1。**非 root（ubuntu）上下文**原始失败：`fence_inherit_test.go:167: 装围栏后硬限应为 31344，实际为 31345（helper 完整输出 "RAISE_OK HARD=31345"）`，`FAIL`。两档都红在 HARD 那条断言（第 1 段），红因正是产品性质本身。恢复后两档都绿。
- 变异二（确认第 2 段非恒真、分档写对位置）：在 `/tmp/b215-nonroot` 用三组对照证明。
  - M2-1（gate 改恒 false=任何机器都跳过 RAISE 断言，正确围栏，非 root）：测试通过 `--- PASS: TestFenceCannotBeRaisedBack`——第 2 段端到端保护静默消失，第 1 段仍在，用例不红。证明分档写错位置（恒 false）会让非 root 上的端到端保护消失而不被察觉。
  - M2-3（gate 改恒 true=任何机器都执行 RAISE 断言，正确围栏，非 root）：测试通过——第 2 段在非 root 上确实执行且正确围栏下通过。
  - M2-4（正确 gate，但把第 1 段 HARD 断言临时中和、围栏改只压软限，非 root）：原始失败 `fence_inherit_test.go:178: 非特权进程应拆不掉围栏，helper 完整输出 "RAISE_OK HARD=31345"`，`FAIL`，退出码 1——证明第 2 段有牙：它能抓住「围栏可被拆掉」的坏实现，不是恒真。
  - 三组做完立刻恢复全部临时改动，恢复后两档测试均绿。
- 命令：`go build ./...`，无输出，退出码 0。
- 命令：`git ls-files '*.go' | xargs gofmt -l`，无输出，退出码 0。
- 命令：`go vet ./...`，无输出，退出码 0。
- 命令：`go test ./internal/prochost/ -count=1`，`ok  github.com/Xsxdot/handoff/internal/prochost  3.054s`，退出码 0。root 下本卡修后真绿。
- 命令：`go test ./...`（超时 600s 重跑）
- 原始输出：多数包 `ok`（含 `internal/prochost (cached)`）；四个用例红，分布在三个包：`internal/client` 的 `TestCursorRootFallsBackToCwdWhenHomeUnwritable`（`cursordir_test.go:57: 根 = "/tmp/.../001/.handoff/cursors", want "/tmp/.../002/..."（应降级到 cwd）`）与 `TestCursorRootErrorNamesBothPaths`（`cursordir_test.go:95: 两处都不可写时必须报错，不得静默`）；`internal/config` 的 `TestLoadStripUpdateDoesNotBlockOnSaveFailure`（`config_test.go:488: 回写应失败，磁盘上仍须留着 update 段`）；`internal/executor/grok` 的 `TestSyncAuthKeepsTaskCopyWhenWriteFails`（`authsync_test.go:263: 写回失败应返回错误`）。最终 `FAIL`，退出码 1。
- 判断：上述四个红是**模拟「写入失败」的用例在 root 下失效**（root 对权限不可写目录仍可写，测试夹具构造的失败条件不成立），与本卡无关。已证：① 在非 root 用户 `ubuntu` 下同一代码跑 `internal/client`、`internal/config`、`internal/executor/grok` 对应用例全部 `ok`；② 在独立克隆 `/tmp/b215-basecheck` 于基线提交 `eb4d7f18`（无 B215 改动）以 root 跑同一批用例，红法与红点与 B215 分支完全一致——即这些红在 B215 之前就存在，是环境问题不是本卡引入。
- 判断：`internal/executor/claudecode` 本轮 `ok (cached)`——plan 预告的 B212 socket 路径超限红已随基线合入 B212 解决，本卡无涉。
- 事故记录：为在非 root 下跑测试，把 worktree 复制到 `/tmp/b215-nonroot` 后，在副本里执行了 `git checkout eb4d7f18`——因副本的 `.git` 是 gitdir 指针（指向 `/root/.handoff/repos/handoff/.git/worktrees/0fc9b461`），该命令把真实 worktree 的 HEAD 从 `cards/B215-implement`（12177555）移到了基线，工作区文件未变但 HEAD 与 index 失配。已用 `git checkout -f cards/B215-implement` 恢复，恢复后 `git log -1` 回到 `12177555`、`branch --show-current` 回到 `cards/B215-implement`、`git status --short` 仅剩台账未跟踪文件。教训：副本与真实 worktree 共享 gitdir，严禁在副本内执行任何 git 写操作；此后非 root 验证一律用 `cp` 覆盖文件后只跑 `go test`。
- 整分支终审（审查 subagent）：规格符合性 PASS——T1 `getNprocLimits`（fence_unix.go:82）软硬两值均钳值、`getNprocLimit` 未动；T2 helper 同行报硬限且各分支一行 stdout、`TestHelperFenceChild` 未动；T3 两段判据（第 1 段恒断言 HARD==want 不按特权分档、第 2 段仅非 root 且 root 走 return 非 t.Skip）、want 构造保留；范围仅两源文件 + 台账。质量 PASS——gofmt/vet/diff --check 干净、root 下 `TestFenceCannotBeRaisedBack` PASS、无调试残留、注释准确、台账与仓库现状可互相印证。Minor 三项（不影响裁决）：① 第 2 段用 `strings.HasPrefix(line, "RAISE_DENIED")` 判整行前缀，第 1 段用 `fields[0]` 判字段，风格稍不一致，可统一为 `fields[0]`；② helper GETFAIL 在第 1 段会以「格式异常」报错而非透出底层错误，可接受；③ 台账为未跟踪文件，本卡作为交付物随分支入库。
- 判断：Minor 项按纪律记台账不进修复回路；整分支终审无 FAIL，不派修复波，直接收口。