# B278 spec 台账

- 2026-08-28 用户「把第三批和第六批合成一批修」。第三批 = B235/B257，第六批 = B251/B260。聚合卡 B278。
- 工作树 `/Users/sycm/.handoff/worktrees/batch-dispatch-wire`，分支 `fix/dispatch-wire`（从 origin/main @ `3ae31175a`，其上 B277 只加了 Go flows 扫描文档）。
- B235 源卡实证：BM1.1 拍板 `e38140d` 在 `origin/card/BM1.1-judge-calibration`，下一节点 `cards/BM1.1-charter-3` 从 `82f2767`（charter-2）起。
- B235 代码：`ViaTemplate` 有工作分支时覆盖 `base` 并 `LocalBaseBranch=true`（`internal/ledgerstep/dispatch.go:214-217`）。`resolveLocalBaseBranch` 故意不 fetch（`internal/agentd/workspace.go:1023-1030`）。`DispatchSnapshot` 无起点 sha。
- B235 源卡第一方案「改用卡 base_branch」否决：会丢掉工作分支上上一节点的代码，与 B192 冲突。本期改为同名分支快进并集 + 不同名只警告。
- B257 源卡实证：B156.3.1 / B156.3.7 并发首派同一 `effective_base_branch`，后到者 `cannot lock ref` 被包进「基线提交在任务仓库中不存在」。重派即成功。
- B257 代码：`ResolveBaseBranch` fetch 失败 `%w ErrBaseCommitMissing`（`workspace.go:1152-1156`）；HTTP `writeDispatchError` 一律当「任务仓库落后」（`server.go:1214-1216`）。根因是同进程双 goroutine 同仓库 fetch，不是基线缺失。修法：仓库路径互斥 + 残留重试 + 独立 sentinel。
- B251 源卡：B249 breakdown 写成 `2026-08-25-b249-breakdown.md`，声明路径无日期。`containsPath` 精确相等（`node.go:303-309`），失败 `haltForHuman`。plan `on_fail=breakdown`，故不能改成裁决 fail。B250 已完成，阻塞边仍在。
- B251 弃选放宽匹配：produces 是机器键（B201 / product-backlog「精确字符串相等」）。
- B260 源卡：CLI `card show` 的 `tasks` 大驼峰，`card` 蛇形。`TaskLink` / `ledger.Relation` 无 json tag。HTTP `proto.TaskStateRow` / `proto.Relation` 注释刻意 PascalCase，Web `ledger.ts` 按 `TaskID`/`From` 消费。本期只改 CLI；relations 同文档一并改。
- `internal/ledger/wire_test.go` 钉了 Card/Event/Decision，没钉 TaskLink/Relation。
- 图：本机无 `codegraph` 二进制；`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve ...` 输出空。定位靠 grep / 读码。
- 无前端页面形态，不走原型。
- 2026-08-28 独立审查 `01a0464e` 被进程重启打断；`01a04667` 续跑出完整报告。Critical 3 / Important 10。协调者吸收见 `docs/superpowers/reviews/b278-spec-review.md`。
- r1 关键吸收：`--step` 不赌 CLI stderr；不同名不每次警告；fetch 失败退回本地；禁止 FETCH_HEAD；Transport 回传 BaseCommit；skill 只改仓内 `skills/handoff/SKILL.md`；HTTP 负例；重试 50–200ms ×2。
- 用户 2026-08-28「老样子」授权批准 r1 并无人值守推进到合 main。
