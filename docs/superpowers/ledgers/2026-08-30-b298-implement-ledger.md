# B298 implement 节点台账

日期：2026-08-30
节点：implement（L3 轻档）
工作树：`/Users/sycm/.handoff/worktrees/b298-charter`
分支：`cards/B298-charter`
起点 HEAD：`8ec9d053`（plan 提交）
上游：plan `docs/superpowers/plans/b298-plan.md`；契约 `docs/superpowers/specs/b298-contract.md`（冻结 `26e2ab7f`）

## 事实流水

- 2026-08-30：工作树干净，HEAD `8ec9d053 docs(B298): plan for cache-leaf GC and terminal purge`，跟踪 origin。未 rebase、未 push。
- 2026-08-30：T1 动手前基线 `go test ./internal/agentd -run 'TestHandleGCTicket0|TestDoneRemovesManagedWorktree|TestCompensateKeepsBranchWhenWorktreeRemoveFails|TestDoneWorktreeRemoveFailureDoesNotBlockArchive' -count=1 && go test ./internal/executor -run 'TestTaskTmpDirGoldenVectors' -count=1`。退出 0：`ok .../internal/agentd 4.736s`；`ok .../internal/executor 0.417s`。
- 2026-08-30：helper 测试先落 `cachegc_test.go`（四支纯函数）。首红：`undefined: cacheID8` / `cacheActiveLeaf` / `cacheLegacyLeaf` / `cacheTmpRoot` / `isCacheTmpRoot`（编译未定义，非 typo）。
- 2026-08-30：偏差 1：步骤 1 测试文件 import 只保留 helper 四支用到的包（`os`/`filepath`/`testing`/`executor`/`proto`）。plan 给的 `bytes`/`context`/`errors`/`slog`/`strings`/`time` 在步骤 3 追加缝级测试时才加入。原因：两提交拆开，先绿 helper 时那些 import 未用会 vet 红。断言未改。
- 2026-08-30：按 plan 复制 `cachegc.go`，`Manager` 在 `writeTaskFile` 后加 `removeCacheLeafFn`（生产 nil）。补 plan §2.8 注释：`activeLeafOccupied` 不用 `ActiveTasksByWorkDir`；`planTaskCacheLeaves` 遗留叶子也做 tmp 根保护（`Join`+`Clean` 可把 `..` 拼成根）；`sumRegularFileBytes` 用 WalkDir+Info 不跟随 symlink。helper 测试绿：`ok .../internal/agentd 0.730s`。`go build ./...` 退出 0。
- 2026-08-30：提交 `10be911a feat(B298): add cache-leaf helper with occupancy and tmp-root guard`。
- 2026-08-30：追加缝 1 测试。偏差 2：plan 的 `assertKeptFile` 写 `t.Fatalf("读 %s: %v", err)`，vet 红 `format %v reads arg #2, but call has 1 arg`。改为 `t.Fatalf("读 %s: %v", path, err)`。未削弱断言。
- 2026-08-30：缝级首红（断言，叶子仍在）：`TestDonePurgesBothCacheLeavesAndKeepsTaskDir` / `TestStopPurgesBothCacheLeaves` / `TestDoneKeepsActiveLeafWhenOtherNonTerminalSharesID8` / `TestDoneLegacyLeafIgnoresShortIDOccupancy` / `TestDonePurgeFailureDoesNotBlockArchive` / `TestCompensatePurgesCacheWhenWorktreeRemoveFails`。`TestDoneOnRunningDoesNotPurgeCache` 与 `TestPurgeRefusesTmpRootEvenIfCalledDirectly` 已绿（Done 早退 / helper 直调）。`TestDonePurges*` 未意外先绿。
- 2026-08-30：接线：`Done` 工作树块后 `purgeTaskCache`；`Stop` 工作树块外、`Publish` 前 `purgeTaskCache`；`compensateWorkspace` 空 WorkDir 守卫后 `defer m.purgeTaskCache(taskID)`（C-2）。T1 测试 + 三支回归绿：`ok .../internal/agentd 6.442s`。`go build ./...` 退出 0。
- 2026-08-30：提交 `3c1da2b0 feat(B298): purge cache leaves on done, stop, and compensate`。
- 2026-08-30：T2 动手前基线 `go test ./internal/agentd -run 'TestHandleGCTicket0|TestReclaimRemovesCleanWorktree|TestReclaimRefusesDirtyWithoutForce|TestReclaimListShowsResidueOnly' -count=1`。退出 0：`ok .../internal/agentd 0.683s`。
- 2026-08-30：删除 `TestHandleGCTicket0`，写入 plan 缝 2 测试。首红：`preview: gc 尚未接线`（`ErrGCUnwired`）；`TestGCExecuteDeletesNewTerminal` 因 `preview==nil` 解引用 panic（空壳）。
- 2026-08-30：整文件替换 `internal/agentd/gc.go`（删 `ErrGCUnwired`/`jsonDecode`；成功路径 `writeJSON(w, 200, resp)`；`scanned` 按终态行 `++`；execute 重读 ListTasks）。T2+reclaim：`ok .../internal/agentd 4.373s`。`go build ./...` 退出 0。`ErrGCUnwired` / `TestHandleGCTicket0` / `jsonDecode` 在 `internal/agentd` 零命中。
- 2026-08-30：提交 `744a1e4f feat(B298): implement Manager.GC batch preview/execute and write JSON`。
- 2026-08-30：T3 动手前基线 `go test ./cmd -run 'TestRunGCDegradesOnOldAgentd|TestRenderGCDistinguishesUnknownBytes' -count=1`。退出 0：`ok .../cmd 0.567s`。
- 2026-08-30：CLI 测试追加后断言红：`TestRunGCPreviewUsesGETAndDoesNotPost` 缺「共扫」；`TestRunGCExecuteFailuresNonZero` 恒 nil；`TestRenderGCShowsFourStatuses` 缺「将删」。`TestRunGCDegradesOnOldAgentd` 仍绿。
- 2026-08-30：替换 `runGC` 尾部（execute 仅 `Failures>0` 非零，先打印再 return）与 `renderGC` 四态/共扫行。T3：`ok .../cmd 0.768s`。`go build ./...` 退出 0。
- 2026-08-30：提交 `09a2d93c feat(B298): render gc report and fail execute only on Failures`。
- 2026-08-30：T4 动手前基线 `TestGCPostDouble404IsUnsupported` / `TestRunGCDegradesOnOldAgentd|TestRunGCJSONDistinguishesAbsentAndZero` / `TestGCGoldenJSON` 皆退出 0。
- 2026-08-30：追加 `TestGCPreviewAndGCDecode200ReleasableBytes` 与 `TestRunGCRenderPreservesAbsentVsZeroThroughClient`。零生产改动。直接绿：`ok .../internal/client 0.550s`；`ok .../cmd 0.594s`；`ok .../internal/proto 0.206s`。
- 2026-08-30：四包全量按 plan §5.6 归协调者，本节点不跑。本节点跑 plan 各 task 最小命令 + 回归名。
- 2026-08-30：收口命令 `go test ./internal/agentd -run 'TestCache|TestDonePurge|TestStopPurge|TestCompensatePurge|TestDoneKeepsActiveLeaf|TestDoneLegacyLeaf|TestDoneOnRunning|TestPurgeRefusesTmpRoot|TestDoneRemovesManagedWorktree|TestCompensateKeepsBranchWhenWorktreeRemoveFails|TestDoneWorktreeRemoveFailureDoesNotBlockArchive|TestGC|TestHandleGC' -count=1 && go test ./cmd -run 'TestRunGC|TestRenderGC|TestGCCmd' -count=1 && go test ./internal/client -run 'TestGC' -count=1 && go test ./internal/executor -run 'TestTaskTmpDirGoldenVectors' -count=1 && go build ./...`。原始输出：
  ```
  ok  github.com/Xsxdot/handoff/internal/agentd  6.860s
  ok  github.com/Xsxdot/handoff/cmd  0.414s
  ok  github.com/Xsxdot/handoff/internal/client  0.328s
  ok  github.com/Xsxdot/handoff/internal/executor  0.417s
  ```
  `go build ./...` 退出 0。判断：plan 各 task 最小命令与回归名全绿。
- 2026-08-30：`git diff 26e2ab7f --name-only -- web/` 空。相对 plan 提交 `8ec9d053` 的实现路径：`cmd/gc.go` `cmd/gc_test.go` `internal/agentd/cachegc.go` `cachegc_test.go` `gc.go` `gc_test.go` `manager.go` `internal/client/gc_test.go` + 本台账。未改 `web/`、`internal/executor/tempdir.go`、`internal/proto/gc.go`。
- 2026-08-30：`ErrGCUnwired` / `TestHandleGCTicket0` 在 `internal/agentd` 与 `*.go` 零命中。未 push。

## 偏差

1. T1 helper 测试文件分两步补 import（见上）。签名/DTO/`TaskTmpDir`/reclaim 语义未改。
2. `assertKeptFile` 补 `path` 参数以满足 vet。断言含义不变。
3. `cachegc.go` / `gc.go` / `cmd/gc.go` 按 plan §2.8 / §3.6 / §4.5 补 why 注释，未改冻结签名。

## 提交

1. `10be911a` feat(B298): add cache-leaf helper with occupancy and tmp-root guard
2. `3c1da2b0` feat(B298): purge cache leaves on done, stop, and compensate
3. `744a1e4f` feat(B298): implement Manager.GC batch preview/execute and write JSON
4. `09a2d93c` feat(B298): render gc report and fail execute only on Failures
5. （本文件随 T4）`test(B298): lock gc JSON absent-vs-zero through client and CLI`
