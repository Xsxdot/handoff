# B304 台账

- 2026-09-02：建卡 B304，基线 `feat/agy-executor`，用户授权自主推进到合入标准。
- 2026-09-02：定级 L2。审查对象 HEAD `34b7265a`。linux-01 agentd 可用，执行者含 codex。
- 2026-09-02：方案钉死为 taskDir sidecar + Restore 成对调用；已跟踪 skip-worktree；exclude 只 `.agents/hooks.json` 且 Restore 撤行。
- 2026-09-02：实测基线 `go test ./internal/executor/agy ./internal/executor -count=1`：`ok github.com/Xsxdot/handoff/internal/executor/agy 0.319s`、`ok github.com/Xsxdot/handoff/internal/executor 0.002s`；`go build ./...`：无输出、退出 0。
- 2026-09-02：确认当前分支 `cards/B304-charter`、工作树干净；现状 `taskenv.go` 无 sidecar/Restore，仍调用 `ensureGitExclude(workdir, ".agents/")`，agy 带模型金样仍为旧顺序。
- 2026-09-02：先写 Task 1 接缝测试；首红命令 `go test ./internal/executor/agy -count=1` 原始结果为 `undefined: RestoreTaskEnv`、`undefined: restoreFileName`。落空壳后 `go test ./internal/executor/agy -run TestRestoreTrackedHooks -count=1` 原始结果为 `--- FAIL: TestRestoreTrackedHooks`，错误为还原后的 hooks.json 仍含 `/tmp/perm.sock`。
- 2026-09-02：Task 1 变异前用完整写回语句锚定 `state.OriginalJSON`，命中计数 `1`（同名字段赋值另有 1 次）；将唯一写回表达式改为 `data` 后 `go build ./...` 无输出退出 0，`go test ./internal/executor/agy -run TestRestoreTrackedHooks -count=1` 原始结果为 `--- FAIL: TestRestoreTrackedHooks`，错误为还原后丢失 `user-linter`；已恢复原表达式。
- 2026-09-02：Task 1 提交命令 `git commit -m 'fix(B304): restore agy hooks.json on stop/rollback/reap'` 原始输出：`[cards/B304-charter d09ab976] fix(B304): restore agy hooks.json on stop/rollback/reap`、`10 files changed, 547 insertions(+), 42 deletions(-)`；随后仅为收录本行执行一次 amend。
