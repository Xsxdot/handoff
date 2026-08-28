# B278 spec 审查（B235 / B257 / B251 / B260）

审查对象：`docs/superpowers/specs/b278.md`（待用户批准稿，后吸收为 r1）  
对照代码：工作树 `fix/dispatch-wire`，Go 与 origin/main 一致  
源卡：B235 / B257 / B251 / B260  
审查者：独立 subagent `01a04667`（前次 `01a0464e` 被进程重启打断后续接）  
日期：2026-08-28

## Summary

四条根因成立，用户方向对：不改用卡 `base_branch`、不放宽 produces、CLI 蛇形而 HTTP PascalCase 不动。B257 / B251 / B260 是治本。B235 同名快进并集修的是「push 到同一条工作分支、执行机本地还停在旧尖端」，吃不进 BM1.1 那种写在另一条 worktree 分支上的提交——这是用户明确要的。

主导残留风险三（Critical）：`--step` 主路径上看不见 CLI stderr 警告；`FETCH_HEAD` 与「锁只包 fetch」组合会错起点；`LocalBaseBranch` fetch 失败按字面拒发会打穿 B192。L2 成立，不抬 L3。

全文与分级见该审查会话回传。下面只保留吸收用的 Findings 与定级。

## Findings

### Critical

1. B235 警告钉在 CLI stderr，`card dispatch --step` 是 202 受理，ViaTemplate 在本机 agentd 异步跑（`cmd/card_node.go` / `internal/agentd/cardstep.go:81-84`）。主路径上这次命令看不见警告。
2. B257 锁若只包 `gitRunNet` fetch，`ResolveBaseBranch` 随后 `rev-parse FETCH_HEAD`（`workspace.go:1152-1166`）会被并发另一次 fetch 覆盖。加上 B235 新 fetch 后不再碰巧同 sha。
3. B235 让 `LocalBaseBranch` fetch，正文只覆盖「远端没有这条分支」。origin 不可达时硬失败会打穿 B192（工作分支常态从未 push；`TestDispatchWireLocalBaseBranchEndToEnd` 用不可达 origin 钉死不 fetch 才能派成）。

### Important

4. `Transport` 只返回 taskID，两条生产 transport 丢掉 `Task.BaseCommit`（`card_dispatch.go:76-97`，`cardstep.go:178-208`）。必须扩返回值，禁止协调者本地 `git rev-parse`。
5. 「名字不等就警告」过宽：工作分支 `cards/<卡>-<purpose>` 与卡基线几乎总是不同名。
6. B235 新 fetch 必须走 B257 同一把 per-repo 锁；`ResolveBaseline` 的 `fetch --all` 也要进锁；clone 不进锁。
7. 分叉拒发不得包 `ErrBaseCommitMissing`；未知 sentinel 会走 500。
8. B251 skill：仓内真源 `skills/handoff/SKILL.md`；product-backlog 在 `~/.grok/skills`，linux-01 改不到。
9. B260 接缝缺 HTTP 负例，防止给 proto 类型打 tag。
10. 锁内重试不得把 `FetchTimeout=2min` 叠三次。

### Minor

11. `resolveLocalBaseBranch` 的 `fetched=false` 注释会过期。
12. `--extra` 旁证行号未漂。
13. 图覆盖债：`gitRunNet` 的 clone 调用方必须排除在 fetch 锁外。
14. 源卡 note 未用 `handoff card show` 现场复核（审查进程无 shell）。

## 定级

L2 成立。不新增 dispatch 请求 JSON 键；HTTP CardDetail 线不动；B192 规则二从「不得补拉」收窄为「可 fetch 只做快进，失败退回本地」。

## 协调者吸收（2026-08-28）

- Critical 1：不在 `--step` 上赌 CLI stderr。不同名分支不打每次警告（Important 5：噪音会淹掉 BM1.1）。可见性改走 `dispatched.base` / `base_commit`；skill 写明提交必须落在工作分支。BM1.1 自动带上仍 OOS。
- Critical 2：锁覆盖 fetch **以及** 读目标 ref；禁止用 `FETCH_HEAD` 当起点，改读 `refs/remotes/<remote>/<branch>`（本地工作分支读 `refs/heads/<branch>`）。
- Critical 3：本地工作分支存在时，fetch 任何失败都退回纯本地，不得拒发。
- Important 4、6、7、8、9、10：写进 r1。
- 保持 L2。
