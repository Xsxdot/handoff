# B465 台账

## 偏离与自主决定

见 `b465.md` 同名节（readonly 行逐字保留；未知形态无括注；未增删政策条目）。

## implement

- 2026-10-22 建卡：`handoff card add ... --project handoff --priority 高` → B465。`card move B465 implement` 被 gate 拒（需 plan 附件）→ L1 spec 即 plan，以 `b465.md` 作 plan 附件。
- 第 0 步身份：worktree `festive-vaughan-56f213`，基于 `755c0aa5`（main），分支 `fix/b465-approver-branch-mode`。
- 根因复核：`approver_context.go:66` 无条件写 readonly 解释；`proto.go:350-351` 只有 branch/readonly 两个形态。
- 提示词组装点：`approver_chain.go:355` `RunLocalRing` 内调 `approverPrompt`——在跑环的机器上组装，生效需环所在机（linux-01）换版。
- 红（`go test ./internal/orchestration -run TestB465 -count=1`，修前）：
    --- FAIL: TestB465BranchContextHasNoReadonlyExplanation (0.00s)
        b465_approver_branch_mode_test.go:19: 形态 "branch" 的提示词不得出现 "只读调查"：
    FAIL
    FAIL	github.com/Xsxdot/handoff/internal/orchestration	0.510s
    FAIL
  首版 readonly 断言写成去掉「readonly = 」前缀的新措辞而红——是测试自己的措辞，非功能缺失；决定 readonly 行逐字保留后改回原文，readonly 测试转为回归守护（修前即绿）。branch 红为断言红。
- 修：`approver_context.go` 加 `modeExplanation`，形态行改用它。
- 绿：`gofmt -l internal/orchestration` 空；`go vet ./internal/orchestration` 过；`go build ./...` 过；`go test ./internal/orchestration -count=1`：
    ok  	github.com/Xsxdot/handoff/internal/orchestration	41.065s
- 提交：`git commit` → 81718a5d fix(B465): 审批上下文形态行只解释本任务形态——branch 不再带 readonly 解释

## review 第 1 轮修改

- 审查 Important：括注「提交到任务分支」有推送歧义。测试期望改为「git commit 到本地任务分支；push 等改变远端仓库的操作不在此列」→ 红：
    --- FAIL: TestB465BranchContextHasNoReadonlyExplanation (0.00s)
        b465_approver_branch_mode_test.go:24: 形态 "branch" 的提示词缺分支形态说明：
    FAIL
    FAIL	github.com/Xsxdot/handoff/internal/orchestration	1.015s
    FAIL
- 改 approver_context.go 括注（b460-plan §9、b465.md 同步）→ gofmt/vet/build 过，`go test ./internal/orchestration -count=1`：
    ok  	github.com/Xsxdot/handoff/internal/orchestration	41.453s
- 提交：`git commit` → 2882fc34 fix(B465): 审查第 1 轮——branch 括注写明本地 commit、push 不在此列
