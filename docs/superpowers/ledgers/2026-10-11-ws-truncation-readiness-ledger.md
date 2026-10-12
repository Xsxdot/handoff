# 2026-10-11 WS 截断回归就绪竞态修复台账

## 对象与范围

- 基线：`main`，`0651dd1e7df06a60cb4027c38973f201f6efceae`；起始工作区干净。
- 改动：仅在 `internal/agentd/ws_regression_round2_test.go` 的
  `TestWSTruncationWarnsOnRealGap` 中，等到 seq=1 首条重放事件后再追加并广播
  seq=21；未改生产代码、超时、等待时长或发布标签。
- 原因：WebSocket 握手返回后，`handleEvents` 仍会先校验任务再订阅 hub。首条
  重放事件写出前，服务端已订阅且已捕获 `storeMax`；因此随后发布的 seq=21 既
  不会在零 watcher 时丢失，也不会改变本用例覆盖的缺口快照。
- 同一竞态已有旧记录：`docs/superpowers/ledgers/2026-08-23-b162-ws-truncation-ledger.md`。
  本轮没有改写该历史台账。

## 诊断与确定性复现

- `codegraph sym TestWSTruncationWarnsOnRealGap --repo .`：未命中，回落测试源码。
- `codegraph sym handleEvents --repo .`：命中 `n_agentd_Server_handleEvents`，源码锚
  `internal/agentd/handlers.go:1480`；签名说明 `/ws/events` 先订阅实时流再补发。
- `codegraph who-calls n_agentd_Server_handleEvents --repo .`：命中入口
  `GET /ws/events`。`chain` 只提供静态调用邻居；该用例的调度顺序仍以源码和屏障
  实验核实。
- **图覆盖债**：目标测试未入图；flow 视图 degraded/无可用流程记录，故测试时序依
  源码及以下可控实验判定。
- 修复前强制实验：`go test -overlay /private/tmp/handoff-ws-diagnosis-4y88rit2/forced.json ./internal/agentd -run '^TestWSTruncationWarnsOnRealGap$' -count=1 -timeout=1m`，退出码 1。测试日志原文包含 `diagnosis publish seq=21 watchers=0`、`replays=5 store_max=21`、`live_merged=0`、`sent=5`，并在等待 seq=21 时以 `failed to get reader: context deadline exceeded` 失败。屏障把握手后的任务查询固定在发布之前，重现的是用例本身的超时断言红。
- 初次强制实验曾误用总测试超时 `-timeout=30s`，Go 以 test timeout panic 中止；它不是有效的断言红证据。改为 `-timeout=1m` 后得到上面的用例自身 deadline 失败。
- 修复后同步实验：从当前已修复的真实测试文件生成临时 overlay，只额外加入握手 barrier；源文件保留 `waitEventSeq(..., 1)` 的实际修复。`go test -overlay /private/tmp/handoff-ws-readiness-synced.json ./internal/agentd -run '^TestWSTruncationWarnsOnRealGap$' -count=20 -timeout=3m`：通过，输出 `ok github.com/Xsxdot/handoff/internal/agentd 0.929s`。

## 验证

- `gofmt -w internal/agentd/ws_regression_round2_test.go && git diff --check`：通过，无输出。
- `go test ./internal/agentd -run '^TestWSTruncation(WarnsOnRealGap|GapCountedPerTask)$' -count=20 -timeout=3m`：通过，输出 `ok github.com/Xsxdot/handoff/internal/agentd 1.327s`。
- `go test ./internal/agentd -run '^TestWS(LiveBufferBounded|OutOfOrderPublishNotDropped|TruncationWarnsOnRealGap|TruncationGapCountedPerTask|DeadlineStaysWithinBounds|ClosesNormallyOnArchive)$' -count=1 -timeout=3m`：通过，输出 `ok github.com/Xsxdot/handoff/internal/agentd 0.590s`。
- `go test ./internal/agentd -count=1`：通过，输出 `ok github.com/Xsxdot/handoff/internal/agentd 112.127s`。
- `go build ./...`：通过，退出码 0，无输出。
- 按协调者要求，一组六项 WS 回归 `-count=20` 的额外压力运行已中断；不将其计作通过。全仓 `go test ./...` 由协调者集成运行。

## 外部失败证据

- 同一 SHA 的 main CI 已出现相同失败：[`run 38144609569`](https://github.com/Xsxdot/handoff/actions/runs/38144609569)，唯一失败为 `TestWSTruncationWarnsOnRealGap`，seq=21 等待超时。
- v0.5.0 release 验证同样失败：[`run 38144849023`](https://github.com/Xsxdot/handoff/actions/runs/38144849023)。失败发生在测试就绪竞态，与 tag 命名无关。

## 边界

此改动稳定测试的订阅就绪和 `storeMax` 快照前置条件；它没有增强对生产 WebSocket 丢事件问题的确定性覆盖，也未作该类结论。

## 协调者全仓集成复跑

- `go vet ./... && git diff --check && gofmt -l internal/agentd/ws_regression_round2_test.go`：退出 0，无输出。
- `go test ./... -count=1`：退出 1；WS 截断用例未报告失败，但 `internal/agentd` 的另一个测试在临时目录清理阶段失败。原始片段：

```text
--- FAIL: TestBundleEndToEndCarriesCommit (0.39s)
    testing.go:1464: TempDir RemoveAll cleanup: unlinkat /var/folders/hx/bw838qps02ngtg8wdz5z9sxw0000gn/T/TestBundleEndToEndCarriesCommit2007341164/004/.git/objects/pack: directory not empty
FAIL
FAIL github.com/Xsxdot/handoff/internal/agentd 103.014s
```

当时尚未归因的清理失败已进入下节排查；本节的全仓结果仍是失败，不能记为全仓通过。

图覆盖债补充：`codegraph sym TestBundleEndToEndCarriesCommit` 未命中，回落源码。

## Bundle 测试临时仓库清理竞态排查与修复

- 根因假设与反馈：失败点是 `t.TempDir()` 清理本地 clone 的
  `/004/.git/objects/pack`，而 bundle handler、git bundle create、fetch 的业务断言均已
  通过。候选原因包括 fetch 触发的后台 maintenance、其他测试/服务器 goroutine 留存
  子进程、以及 Git 配置/钩子副作用。测试源码中 git 命令均同步等待；`git config`
  未发现显式的 `gc.*`、`maintenance.*`、相关 fetch/transfer 配置。
- 原始失败证据来自上节协调者全仓 `go test ./... -count=1`，原文明确为
  `TempDir RemoveAll cleanup: unlinkat .../004/.git/objects/pack: directory not empty`。
  该失败回合没有同期进程 trace，因此不能声称 trace 捕获了该次失败的 PID 或调度。
- 独立诊断中的未保存失败：未修复源码 `GIT_TRACE2_EVENT=/private/tmp/handoff-bundle-repeat.jsonl go test ./internal/agentd -run '^TestBundleEndToEndCarriesCommit$' -count=25 -timeout=5m` 退出 1，但工具输出截断且未落 stdout 文件，无法确认精确失败用例/原文；不将它作为根因证据。紧接着的同命令文件捕获重跑通过（10.071s）。
- 新鲜机制证据：`GIT_TRACE2_EVENT=/private/tmp/handoff-bundle-current.jsonl go test ./internal/agentd -run '^TestBundleEndToEndCarriesCommit$' -count=1 -timeout=3m`：测试通过。Trace2 中 `/004` 的
  `git fetch <bundle> feat/x:feat/x` 在 `00:38:49.1946Z` 退出；同一 fetch 已启动
  `git maintenance run --auto --no-quiet --detach`。detached maintenance 随后在 `/004`
  执行 `geometric-repack` 与 `pack-objects`，到 `00:38:49.2229Z` 才退出；主测试在其
  间继续 `cat-file`/`rev-parse` 并进入临时目录清理时段。此 fresh trace 证明后台写入
  与 cleanup 窗口重叠的机制，单独不能替代原失败回合的精确时序记录。
- Trace2 也显示 source 仓 `/003` 的两个 `git commit` 会调用 auto maintenance，但对应
  事件只执行 detach 后即结束，没有后续 repack；`git clone --no-local` 没有启动该维护。
  发生后台 pack 写入的是被删除的本地 clone `/004` 上的 fetch。
- 最小修复：只对测试中的这条 fetch 传 `--no-auto-maintenance`。本机 `git fetch -h`
  显示该选项存在；本机 Git 2.55 文档说明它关闭本次 fetch 结束时默认启用的
  `git maintenance run --auto`。该选项限定于此测试命令，不更改全局配置或生产 fetch。
- 修复后验证：`gofmt -w internal/agentd/bundle_e2e_test.go && git diff --check`：通过。
  `GIT_TRACE2_EVENT=/private/tmp/handoff-bundle-fixed-count25.jsonl go test ./internal/agentd -run '^TestBundleEndToEndCarriesCommit$' -count=25 -timeout=5m`：通过，输出
  `ok github.com/Xsxdot/handoff/internal/agentd 10.999s`。Trace2 检查确认目标 `/004`
  fetch 共 25 次、其 `child_start` maintenance 事件为 0；source `/003` 的 commit
  maintenance 与被报错 clone 区分记录。
- `go build ./...`：通过，退出码 0，无输出。按协调者要求不重跑整包；修复后的全仓
  `go test ./...` 待协调者集成复验。
- 图覆盖债：`TestBundleEndToEndCarriesCommit` 未入 codegraph；已按源码、测试失败路径
  与 Trace2 进程事件核查。

## 协调者最终集成验证（2026-10-12）

- 两项改动均限测试：WS 测试等待首条重放后再发布实时事件；bundle 承重用例仅该次 fetch 禁用自动 maintenance。没有生产路径、超时、协议或全局配置改动。
- `go test ./... -count=1`：退出 0，全部测试包通过；`internal/agentd` 输出 `ok github.com/Xsxdot/handoff/internal/agentd 100.831s`。完整本地日志：`/private/tmp/handoff-release-fix-go-test-20261012.log`。
- `go vet ./...`：退出 0；`git diff --check` 以及两项改动文件 `gofmt -l` 无输出。
- 目标轴：测试就绪竞态已通过强制时序红绿验证，临时仓库维护竞态的修复通过25次fetch及Trace2验证；无新增产品形态或协议范围。
- 架构与证据轴：改动均在测试夹具，真实 WS/事件存储/Hub/编排及bundle消费路径保持现有结构；原因注释齐全，保留原始行为断言。图覆盖债已在前文记录。
- v0.5.0 仍指向旧提交 0651dd1e；该轮本地验收通过不等于新的远端CI或发布已成功。

## 提交记录

命令：`git commit -m 'test(agentd): synchronize WS readiness and disable fixture maintenance'`

```text
[main 43d4bbe1] test(agentd): synchronize WS readiness and disable fixture maintenance
 3 files changed, 109 insertions(+), 2 deletions(-)
 create mode 100644 docs/superpowers/ledgers/2026-10-11-ws-truncation-readiness-ledger.md
```

以上为首次提交的历史原始输出；随后仅 amend 一次将这条提交记录纳入同批提交，不再追写新hash。
