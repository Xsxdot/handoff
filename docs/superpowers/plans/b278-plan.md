# B278 派发基线与账本约定实施计划

状态：执行计划；规格 `docs/superpowers/specs/b278.md` r1 已批准。
法定产出物：`docs/superpowers/plans/b278-plan.md`。
事实台账：`docs/superpowers/specs/b278-ledger.md`；实现者每确立一个事实、跑完一条命令或放弃一次尝试，都追加一行。
范围：B235、B257、B251、B260 四条局部修复；禁止抽出跨四条需求的公共派发框架、公共 fake Git 门面或新的 dispatch 请求协议。

## 1. 基线与现状证据

### 1.1 已在当前基线实跑的判据

执行节点在没有改实现代码的基线工作树上运行过以下命令，均退出 0；实现时先重跑对应 task 的命令，若输出不再符合这里的预期，先把新的原始输出写入台账，再停止该 task 的实现动作。

| task | 基线命令 | 实跑输出 |
|---|---|---|
| B257 | `go test ./internal/agentd -run 'Test(ResolveBaseline\|ResolveDispatchBase\|ResolveBaseBranch\|DispatchWireLocalBaseBranch\|DispatchAutoBranchStartsAtBaseCommit\|DispatchRecordsBaseline\|DispatchBaseBranchName)' -count=1` | `ok  github.com/Xsxdot/handoff/internal/agentd 1.456s` |
| B235 | `go test ./internal/agentd -run 'TestDispatchWireLocalBaseBranchEndToEnd\|TestDispatchCardEmptyBaseStartsAtOriginDefaultTip\|TestDispatchWithoutCardDefaultMarkerKeepsEmptyBaseHead\|TestDispatchCardDefaultBaseRejectsMissingOriginHead' -count=1` | `ok  github.com/Xsxdot/handoff/internal/agentd 0.646s` |
| B251 | `go test ./internal/ledgerstep -run 'Test(ViaTemplate\|BuildPrompt\|NodeStep\|Runner)' -count=1` | `ok  github.com/Xsxdot/handoff/internal/ledgerstep 5.714s` |
| B260 CLI/账本 | `go test ./internal/ledger -run 'Test(RecordDispatch\|LinkTask\|AddRelation\|Wire)' -count=1`；`go test ./cmd -run 'Test(CardDispatch\|CardShow\|Dispatch)' -count=1` | `ok  github.com/Xsxdot/handoff/internal/ledger 0.323s`；`ok  github.com/Xsxdot/handoff/cmd 2.947s` |
| B260 HTTP | `go test ./internal/agentd -run 'TestLedgerAPI' -count=1` | `ok  github.com/Xsxdot/handoff/internal/agentd 0.249s` |
| B257 clone 边界 | `go test ./internal/agentd -run 'TestRegisterProject\|TestClone' -count=1` | `ok  github.com/Xsxdot/handoff/internal/agentd 2.714s` |

上表中的命令是当前节点亲自执行的结果。`cmd` 的实际包路径输出为 `github.com/Xsxdot/handoff/cmd`；计划表中必须以该真实路径为准，不能把包名误写成别的模块。

### 1.2 图、源码和基线分支限制

- 仓内有 `codegraph/best.json`、`codegraph/baseline.json`；已用 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_workspace d_ledger d_gateway d_cli d_transport` 查到现状领域、接口、主链和 `actual` 分布。输出带焦点配额/未扫描入口提示，不能用空结果证明没有调用方。
- 已用同一 codegraph 的 `sym` 查过 `resolveDispatchBase`、`ResolveBaseline`、`ResolveBaseBranch`、`Store.RecordDispatch`、`cliTransport`、`Server.stepTransport`、`Server.writeDispatchError`、`TaskLink`、`Relation`；`resolveLocalBaseBranch` 与 `containsPath` 未命中，必须以源码和既有调用面补查。
- 图的 `buildPrompt` 签名是旧的五参数版本；当前源码 `internal/ledgerstep/dispatch.go:360` 附近实际是：

  ```go
  func buildPrompt(body string, c ledger.Card, base string, carry, omitAccept bool, extra, outputPath string) string
  ```

  实现者不得按旧图签名改调用面；这项漂移是图覆盖债，写入实现提交说明和台账。
- 规格文字给出的有效基线分支为 `fix/dispatch-wire`，但当前工作树只列出 `cards/B278-charter`；执行节点实跑 `git merge-base HEAD fix/dispatch-wire` 的原始错误为：`fatal: Not a valid object name fix/dispatch-wire`。不得切分支或改 git 配置；所有实现仍在当前分支完成，合并目标只按规格文字记录。
- 现状关键源码：`internal/agentd/workspace.go:240` 的 `gitRunNet` 同时服务网络 fetch/clone；`FetchTimeout` 在 `workspace.go:834-836` 为 `2 * time.Minute`；`ResolveBaseline` 在 `workspace.go:1097`；`ResolveBaseBranch` 在 `workspace.go:1140`；clone 网络调用在 `internal/agentd/projectadmin.go:542`。锁只能包 fetch 与紧随其后的目标 ref 读取，不能包 clone 或整个 dispatch。
- 现状 `internal/ledgerstep/dispatch.go:46` 的 Transport 是 `(taskID string, err error)`；`cmd/card_dispatch.go:105` 的 `cliTransport` 和 `internal/agentd/cardstep.go:178` 的 `stepTransport` 都丢弃 `proto.Task.BaseCommit`。现状 `workspace.go:1152-1158` 使用 `FETCH_HEAD`，必须删除这条并发不安全的读取。

### 1.3 跨 task 接口冻结

以下是实现后各 task 必须逐字对齐的接口；类型别名、参数顺序和返回值顺序不能自行改名。

```go
// internal/agentd/workspace.go；保持现有调用面，改内部语义。
func resolveDispatchBase(ctx context.Context, repo, rev string, localBaseBranch bool) (resolved string, fetched bool, err error)
func resolveLocalBaseBranch(ctx context.Context, repo, branch string) (resolved string, fetched bool, err error)
func ResolveBaseline(ctx context.Context, repo, sha string) (Baseline, error)
func ResolveBaseBranch(ctx context.Context, repo, remote, branch string) (string, error)

// internal/ledgerstep/dispatch.go；Transport 直接回传 agentd 应答的 Task.BaseCommit。
type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, baseCommit string, err error)

type DispatchResult struct {
	Card              string `json:"card"`
	Task              string `json:"task"`
	Target            string `json:"target"`
	Branch            string `json:"branch"`
	Base              string `json:"base"`
	BaseCommit        string `json:"base_commit"`
	Template          string `json:"template"`
	TemplateVersion   int    `json:"template_version"`
	DisciplineName    string `json:"discipline_name"`
	DisciplineVersion int    `json:"discipline_version"`
}

// internal/ledger/events.go；新 dispatched 事件必须包含两个键，旧 payload 不回填。
type DispatchSnapshot struct {
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineName  string `json:"discipline_name"`
	DisciplineVersion int `json:"discipline_version,omitempty"`
	Target          string `json:"target"`
	TaskID          string `json:"task_id"`
	Branch          string `json:"branch"`
	Base            string `json:"base"`
	BaseCommit      string `json:"base_commit"`
	Executor        string `json:"executor"`
	Model           string `json:"model"`
	Purpose         string `json:"purpose,omitempty"`
	PlanPath        string `json:"plan_path,omitempty"`
	Actor           string `json:"-"`
}

// internal/agentd；仅供同包 server 错误映射和测试辨认，均不得包装成 ErrBaseCommitMissing。
var errLocalBaseBranchDiverged = errors.New("工作分支本地与 origin 已分叉，先合并再派")
var errFetchRefLockContention = errors.New("基线补拉失败（远端 ref 锁竞争）")
```

`Base` 是本次作为新分支起点来源的分支名：有工作分支时是工作分支名，首派普通基线时是传给 agentd 的基线名，空 base 仍以空字符串键落账。`BaseCommit` 只能来自目标 agentd 回应的 `Task.BaseCommit`，不得在协调者仓库上 `git rev-parse` 猜测。

## 2. Task B257：同仓库 fetch 锁、目标 ref 读取和错误归因

### 2.1 文件边界与 Interfaces

只允许改动以下文件；`projectadmin.go` 仅作为“不进入锁”的被测生产调用面，不改其 clone 实现。

- 生产：`internal/agentd/workspace.go`、`internal/agentd/server.go`。
- 测试：`internal/agentd/workspace_test.go`、`internal/agentd/server_test.go`、`internal/agentd/projectadmin_test.go`。
- 账本：每个事实追加到 `docs/superpowers/specs/b278-ledger.md`；不在 task 内改其它文档。

本 task 的私有实现接口如下；没有新增导出 API，也不抽派发公共框架：

```go
// workspace.go；只服务 fetch + 紧随其后的目标 ref 读取。
func withRepoFetchLock(repo string, fn func() error) error
func runFetchWithRetry(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error)
func isFetchRefLockFailure(stderr string) bool
```

锁表以 `filepath.Clean(repo)` 为唯一键。`withRepoFetchLock` 的闭包必须覆盖完整的“fetch + 目标 ref 读取”序列；`runFetchWithRetry` 只在闭包内调用。B235 的 origin 同名分支 fetch、`ResolveBaseline` 的 `fetch --all --prune`、`ResolveBaseBranch` 的 `fetch <remote> <branch>` 必须调用同一 `withRepoFetchLock`，但不能让它包住 `Manager.Dispatch`、worktree 创建或 clone。

为锁竞争耗尽写一个仅供 B257 测试控制的私有 fetch 命令变量时，生产默认值必须直接调用 `gitRunNet`，并且 clone 仍直接调用 `gitRunNet`：

```go
var runNetFetch = func(ctx context.Context, repo string, args ...string) (string, string, error) {
	return gitRunNet(ctx, repo, args...)
}
```

这不是 B235 的 fake Git；B235 的快进、不可达和分叉测试必须用真实临时 Git 仓库，不能替换 `runNetFetch`。

### 2.2 基线判据先跑与最小测试范围

动手前重跑 1.1 中 B257 两条 `internal/agentd` 命令及 clone 命令。预期分别是已记录的四个 `ok` 输出；只跑 `./internal/agentd` 的列举用例，不跑全量测试。

### 2.3 红绿步骤：锁和 ref 读取

1. 在 `internal/agentd/workspace_test.go` 复用现有 `newOriginAndClone`、`commitOnOrigin`、`gitAt` 等真实 Git 夹具，先写失败测试 `TestResolveBaseBranchConcurrentFetchUsesRemoteRef`：两个 goroutine 对同一 clone、同一 remote/branch 同时调用 `ResolveBaseBranch`，两次都必须无 error、返回同一 40 字符 SHA。测试临时包裹 `runNetFetch` 但仍调用其真实默认实现，记录每次真实 fetch 的 stderr；断言记录中不含 `cannot lock ref`。先跑该单测，必须得到红结果后再改生产代码。
2. 在同一测试文件另写 `TestResolveBaselineAndBranchShareRepoFetchLock`，用一条 goroutine 走缺失 SHA 的 `ResolveBaseline`（触发 `fetch --all --prune`），另一条走 `ResolveBaseBranch`（触发指定分支 fetch）；断言两者都返回预期 SHA、没有互相读错目标 ref。夹具与上一步独立，不把 B235 本地分支夹具冒充 B257 并发夹具；跑红。
3. 在 `workspace.go` 增加按 `filepath.Clean(repo)` 取锁的私有实现。锁只包 fetch 和成功后的立即读取；每次日志带 `repo`、fetch 参数、目标 ref 和阶段。
4. 把 `ResolveBaseline` 改成：先按现有规则检查对象；需要补拉时进入锁，锁内再次检查对象，仍缺失才在同一锁内执行 `fetch --all --prune`，并在同一锁内重新检查目标 SHA。非锁竞争 fetch 失败和 fetch 后仍无对象继续包装 `ErrBaseCommitMissing`，保留 fetch stderr、SHA 和现有 git push/`--no-sync-check` 提示。
5. 把 `ResolveBaseBranch` 改成锁内无条件 fetch 后，立即读取 `refs/remotes/<remote>/<branch>`；删除 `rev-parse FETCH_HEAD`。读取失败但远端确实缺分支仍包装 `ErrBaseCommitMissing`。日志成功路径写出 `remote`、`branch`、目标 ref 和最终 SHA。
6. 对 `cannot lock ref` 做同锁内最多两次重试，间隔固定 100ms（落在规格要求的 50–200ms），重试共享同一 `FetchTimeout` 截止时间，不为每次重试再创建完整 2 分钟预算；非该文本的 fetch 错误不重试。把最后一次 fetch 原文保留在错误中。
7. 先跑步骤 1、2 的并发单测和 `go test ./internal/agentd -run 'Test(ResolveBaseline|ResolveBaseBranch)' -count=1`，确认转绿；再进入错误归因测试。

### 2.4 红绿步骤：锁竞争、真缺失与 clone 边界

1. 在 `workspace_test.go` 用 `runNetFetch` 注入三次带原文 `cannot lock ref refs/remotes/origin/main` 的失败，写 `TestResolveBaseBranchLockContentionHasIndependentSentinel`。断言 `errors.Is(err, errFetchRefLockContention)` 为真，`errors.Is(err, ErrBaseCommitMissing)` 为假，错误包含最后一次 fetch 原文；同时断言调用次数为 3。先跑红。
2. 在 `server_test.go` 通过 `httptest.NewRecorder` 直接从声明缝调用 `Server.writeDispatchError`，传入包装了 `errFetchRefLockContention` 的错误；断言状态 400，body 首句含“基线补拉失败（远端 ref 锁竞争）”或同语义，包含 fetch 原文，不含“基线提交在任务仓库中不存在”和“请先在本地 git push”。再传入 `ErrBaseCommitMissing`，断言仍为 400 且保留原有缺失基线/git push 提示。先跑红。
3. 在 `projectadmin_test.go` 复用现有 clone harness，增加 `TestCloneDoesNotWaitForRepoFetchLock`：以 clone 目标绝对路径 `dest` 作为 `filepath.Clean(dest)` 锁键，持有该私有 fetch 锁时，从本地 origin 克隆到 `dest`；用有界 channel 等待 `cloneAndRegisterProject` 完成，释放锁前必须完成且 `.git` 存在，证明 clone 没有调用该锁。断言 clone 仍成功、位置表可读；不测时间计数，只测锁释放前可完成的行为。先跑红。
4. 在 `server.go` 添加锁竞争 sentinel 分支，顺序放在 `ErrBaseCommitMissing` 前；每条错误分支记录 `project`、错误类别和原错误。不要把未知错误扁平为锁竞争，也不要让锁竞争落到 500。
5. 运行步骤 2–3 的单测、B257 基线命令及 clone 基线命令；每条命令输出为 `ok` 后才进入 task 收尾。

### 2.5 注释、日志与验收

- 更新 `ResolveBaseline`、`ResolveBaseBranch`、`resolveLocalBaseBranch` 邻近注释，说明锁覆盖 fetch+目标 ref、`FETCH_HEAD` 禁用、重试共享总预算；注释说明 `FetchTimeout` 的既有 2 分钟行为来自 `workspace.go:834-836`，不要写成三次独立超时。
- 入口日志带 repo/remote/branch 或 sha；fetch 前后带尝试序号、耗时、stderr 摘要；目标 ref 读失败和 sentinel 分类分别带上下文；成功路径带最终 SHA。使用项目 logger，不使用 `print`。
- 验收清单：同仓同分支并发两次成功；`fetch --all` 与指定分支 fetch 共享同一锁且不串错 SHA；跨仓库并发不互相阻塞；锁竞争耗尽 HTTP 400 且是独立文案；真缺失仍是 `ErrBaseCommitMissing`；clone 在锁持有时仍完成；整个过程没有 `FETCH_HEAD` 读取。

## 3. Task B235：工作分支快进并集与 BaseCommit 回传/快照

### 3.1 文件边界与 Interfaces

- 生产：`internal/agentd/workspace.go`、`internal/agentd/server.go`、`internal/ledgerstep/dispatch.go`、`cmd/card_dispatch.go`、`internal/agentd/cardstep.go`、`internal/ledger/events.go`。
- 测试：`internal/agentd/workspace_test.go`、`internal/agentd/integration_test.go`、`internal/agentd/server_test.go`、`internal/ledgerstep/dispatch_test.go`、`cmd/card_dispatch_test.go`。
- 不改 `internal/client/client.go` 的 dispatch 请求字段，不新增 `base`/`base_commit` 请求键；客户端已经解码 `proto.Task.BaseCommit`，只把它沿返回链传回。

目标接口逐字如下：

```go
// workspace.go
func resolveLocalBaseBranch(ctx context.Context, repo, branch string) (resolved string, fetched bool, err error)

// ledgerstep/dispatch.go
type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, baseCommit string, err error)
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)

// cmd/card_dispatch.go
func cliTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (taskID string, baseCommit string, err error)

// internal/agentd/cardstep.go
func (s *Server) stepTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (taskID string, baseCommit string, err error)

// ledger/events.go
func (s *Store) RecordDispatch(cardID string, snap DispatchSnapshot) error
```

### 3.2 基线判据先跑与最小测试范围

动手前重跑 1.1 中 B235 的 agentd 集成命令、agentd dispatch 单测、ledgerstep 模板命令和 cmd dispatch 命令。预期分别是已记录的 `ok` 输出；本 task 只跑 `./internal/agentd`、`./internal/ledgerstep`、`./cmd` 的命名用例，不跑全量测试。

### 3.3 红绿步骤：本地工作分支的快进并集

1. 在 `internal/agentd/workspace_test.go` 使用独立真实 Git 临时仓库，先写四组从 `resolveDispatchBase(context.Background(), repo, branch, true)` 进入的失败测试并跑红：
   - 本地分支比 origin 新：起点等于本地 SHA，远端不可覆盖本地 ref。
   - origin 比本地快进领先：起点等于 origin 同名分支 SHA，且只作为新任务分支起点。
   - origin 缺失、网络不可达或认证/超时任一 fetch 失败：起点等于本地 SHA，`err == nil`，`fetched` 反映是否成功完成远端探测而不是把失败伪报成功。
   - 本地与 origin 两 SHA 都存在且互不为祖先：错误包含两个完整 SHA 和“工作分支本地与 origin 已分叉，先合并再派”，`errors.Is(err, errLocalBaseBranchDiverged)` 为真，`errors.Is(err, ErrBaseCommitMissing)` 为假。
2. 在该真实夹具中额外断言：本地 ref 初始 SHA 在所有返回路径仍保持不变；没有调用 `ResolveBaseBranch` 的远端分支路径；origin 缺失和 origin 不可达不产生拒发错误。这个夹具不替代 B257 的并发夹具，也不注入 `runNetFetch`。
3. 在 `workspace.go` 更新 `resolveLocalBaseBranch`：先校验非空、非 `-`、不带 `refs/` 的分支名；读取 `refs/heads/<branch>` 作为必需本地 ref。它不存在仍返回 `ErrBadWorkspaceReq`。本地存在后，在 B257 的同一 `withRepoFetchLock` 内 fetch `origin` 同名分支并读取 `refs/remotes/origin/<branch>`；fetch 任何失败、远端分支缺失或 ref 不可读都记录 warning 并返回本地 SHA，不拒发。
4. 在同一锁内用 `git merge-base --is-ancestor` 比较两边：本地是 origin 后代时返回本地；origin 是本地严格后代时返回 origin SHA；相等返回本地；双方互不为祖先时返回独立分叉 sentinel。绝不执行 `reset`、移动 `refs/heads/<branch>` 或把 origin ref 写回本地工作分支。
5. 更新 `resolveDispatchBase` 与 `Manager.Dispatch` 的注释/日志，使 `LocalBaseBranch=true` 始终只进该本地工作分支路径，不进入 `ResolveBaseBranch`；`fetched` 只用于准确日志，不能改变 fallback 结果。跑步骤 1 的红测，确认在实现前红、实现后绿。
6. 在 `server.go` 的 `writeDispatchError` 增加分叉 sentinel 的 400 分支，body 必须有两个 SHA 和合并后重派动作。分叉错误不能走未知错误 500，也不能被误归类为缺失基线。

### 3.4 红绿步骤：Transport、stepTransport 和 dispatched 快照

1. 在 `internal/ledgerstep/dispatch_test.go` 复用现有账本/模板夹具，先让注入的 Transport 返回 `("task-wire", "1111111111111111111111111111111111111111", nil)`，写 `TestViaTemplateCarriesTransportBaseCommitIntoResultAndSnapshot` 并跑红。逐项断言：`DispatchResult.Task`、`Base`、`BaseCommit` 正确；最新 `dispatched` payload 有 `base` 与 `base_commit`，`base_commit` 是返回的 40 字符值，不是协调者仓库的解析值；即使 base 为空，新事件也保留两个键。
2. 将 `ledgerstep.Transport`、`DispatchResult`、`DispatchSnapshot` 按 3.1 完整接口扩展。`ViaTemplate` 接收三返回值，使用它填 `DispatchResult.BaseCommit` 和 snapshot；snapshot 的 `Base` 使用本次传给 agentd 的 `base`，不是卡的 `cardBase` 合并目标。保留 `LinkTask`、写闸和事件追加顺序。
3. 在 `cmd/card_dispatch.go` 让 `dispatchTransportWithOpts`、`dispatchTransport` 以及 `swapDispatchTransport*` 的回调逐一携带 `(taskID string, baseCommit string, err error)`；生产 `client.Dispatch` 后直接返回 `task.ID, task.BaseCommit`，不得本地解析 SHA。保留现有四标量测试缝和完整 opts 测试缝，只改返回值。
4. 在 `internal/agentd/cardstep.go` 让 `stepTransport` 在目标 client `Dispatch` 成功后记录 target/task/base_commit，并返回 `task.ID, task.BaseCommit`；客户端错误分支带 target、executor、model、cause，成功日志带 task 和 base_commit。不能让 `--step` 依靠 CLI stderr 传递任何警告或起点信息。
5. 在 `cmd/card_dispatch_test.go` 更新现有回调的三返回值，并补一条真实 `--step` 路径断言：从本机 agentd 接收的 task `BaseCommit` 原样进入 `dispatched` payload。测试同时断言卡的 `effective_base_branch` 与工作分支名不同不产生每次派发警告，起点和可见性来自 `base`/`base_commit`。
6. 在 `internal/agentd/integration_test.go` 扩展现有 `TestDispatchWireLocalBaseBranchEndToEnd`：origin 不可达时派发成功、任务 `BaseCommit` 等于本地工作分支 ref，且本地工作分支 ref 未移动；另测 origin 快进领先和分叉 HTTP 400。该集成夹具与 B257 的锁竞争夹具分离。
7. 运行 ledgerstep/cmd/agentd 触及用例，确认红转绿；再运行 1.1 的 B235 四组命令。老 dispatched 事件反序列化仍可读而不回填新字段；新 dispatched 事件含两个键。

### 3.5 注释、日志与验收

- `resolveLocalBaseBranch` 导出边界虽为包私有，仍须写参数、返回和“本地 ref 缺失 vs origin 不可达 vs 分叉”的注意事项；删掉现有“`fetched=false`、故意不 fetch”的过期注释，解释为什么 origin 只能快进并集、不能替代本地。
- `Transport`、`DispatchResult`、`DispatchSnapshot` 的字段注释明确 `BaseCommit` 来源是 agentd `Task.BaseCommit`，`Base` 是起点分支名，旧事件不回填。
- ViaTemplate 入口日志带 card/template/target/branch/base/local_base_branch；Transport 前后带 task/base_commit；LinkTask、RecordDispatch 每条错误带 card/task/target。成功必须有完成日志。所有日志用结构化 logger。
- 验收清单覆盖：工作分支同名 origin 快进、本地领先、origin 不可达/缺分支、分叉拒发；没有 `FETCH_HEAD`；没有 origin 顶替本地；没有不同名 effective base 的每次 warning；裸 dispatch 与 `--step` 均能回填，但至少一条锁缝测试必须穿过 `stepTransport`；老事件无迁移副作用。

## 4. Task B251：精确产出路径与日期前缀提示

### 4.1 文件边界与 Interfaces

- 生产：`internal/ledgerstep/node.go`、`internal/ledgerstep/dispatch.go`、`skills/handoff/SKILL.md`。
- 测试：`internal/ledgerstep/node_test.go`、`internal/ledgerstep/dispatch_test.go`、`internal/ledgerstep/runner_test.go`。
- 不改 `deploy/workflows/charter-v4.json` 的 `produces.path`，不改存量历史文件名，不改仓外 `~/.grok/skills/product-backlog/SKILL.md`，不把缺失产出改路由到 `on_fail`。

现有边界保持：

```go
func containsPath(paths []string, want string) bool
func (n *NodeStep) RunOnce(ctx context.Context, cardID, target, taskID string, verdict Verdict) (Outcome, error)
func buildPrompt(body string, c ledger.Card, base string, carry, omitAccept bool, extra, outputPath string) string
```

可以新增包私有纯匹配助手，但只能服务本文件的提示拼装，不改变 `containsPath` 的精确相等语义：

```go
func datePrefixedDeclaredPath(declaredPath string, changedPaths []string) (actualPath string, ok bool)
```

### 4.2 基线判据先跑与最小测试范围

动手前重跑 1.1 中 B251 的 ledgerstep 命令，以及 `go test ./internal/ledgerstep -run 'TestRunnerRendersDeclaredOutputPathAndInjectsPrompt|TestNodeStepMissingDeclaredOutputMarksNeedsHumanWithDiffList' -count=1`。预期是已记录的 `ok`；本 task 只跑 `./internal/ledgerstep` 的 prompt、runner、node 用例。

### 4.3 红绿步骤：prompt 与等人 body

1. 在 `internal/ledgerstep/dispatch_test.go` 的现有 `TestBuildPromptThreeSections` 和 `TestBuildPromptIncludesOutputPathWithoutCardContext` 中先加失败断言：输出路径段同时含“不要加日期前缀”“带 `YYYY-MM-DD-` 的是历史文件，不是本节点法定产出”。跑红。
2. 在 `buildPrompt` 现有输出路径句之后追加同语义句；保留当前三段顺序、`outputPath` 独立于 card context、真实法定路径不加日期。跑 prompt 用例至绿。
3. 在 `internal/ledgerstep/node_test.go` 复用既有 `nodeLedger(t)`、`nodePassMessage()`、`assertHaltOnCard` harness，先加失败用例 `TestNodeStepDatePrefixedDeclaredOutputMarksNeedsHuman`：Diff 返回仅有 `docs/2026-08-25-b249-breakdown.md`，声明路径是 `docs/b249-breakdown.md`。逐项断言 `Outcome.Action == ActionNeedsHuman`、卡有 needs-human、body 含声明路径、实际日期路径和“请改名为：docs/b249-breakdown.md”，且没有调用/产生 `routeTo(breakdown)`。跑红。
4. 在 `node.go` 保持 `containsPath` 原样；在“声明路径不在改动列表”分支中，仅当同目录同 basename 且实际 basename 形如 `YYYY-MM-DD-` + 声明 basename 时追加明确段落：`检测到日期前缀文件名：<实际路径>；这是日期前缀，请改名为：<法定路径>`。其他缺失路径仍只产生原有两段清单。成功日志/失败 warning 带 kind、declared_path、changed_paths、actual_path。
5. 用同一 harness 增加精确相等回归：Diff 同时包含法定路径和日期路径时仍 pass/挂附件；Diff 只有任意日期文件但不是声明 basename 的日期版本时仍按普通缺失等人。跑 node 用例至绿。
6. 在 `skills/handoff/SKILL.md` 的“账本模式”节点派发/自动挂附件说明附近写入下面完整规则，只改该仓内文件：

   ```markdown
   - 节点声明的法定产出路径必须逐字使用；不要在 basename 前加 `YYYY-MM-DD-` 日期前缀。带日期前缀的是历史文件，不是本节点的法定产出；写错时按 prompt 给出的法定路径改名。
   ```

   不复制、不修改外部 product-backlog 技能；为该新增说明补文件头/段落注释时说明它只约束仓内 handoff 账本模式。
7. 运行 1.1 的 B251 命令与 runner 用例；确认精确路径仍 pass、日期前缀只有 needs-human、`on_fail` 未被调用。

### 4.4 注释、日志与验收

- 在 `buildPrompt` 的输出路径注释中说明机器键必须精确相等、日期前缀只是历史文件提示；在 `datePrefixedDeclaredPath` 注释中说明只识别同目录的声明 basename 日期版本，避免误伤其它日期文件。
- NodeStep 的入口日志已有 card/target/task；新增日志要带声明路径、改动列表和匹配到的日期路径；Diff 错误、普通缺失、日期提示三条分支分别带上下文。不得用 `fmt.Print` 或 CLI stderr 作为通道。
- 验收清单：prompt 点名禁止日期前缀；日期版本单独出现时 `ActionNeedsHuman`；body 指明法定路径和改名动作；精确路径通过；普通不相关日期文件仍是普通缺失；不放宽后缀/正则匹配；不走 `on_fail`。

## 5. Task B260：CLI ledger JSON 蛇形键，HTTP PascalCase 不动

### 5.1 文件边界与 Interfaces

- 生产：`internal/ledger/tasks.go`、`internal/ledger/types.go`。
- 测试：`internal/ledger/tasks_test.go`、`internal/ledger/wire_test.go`、`cmd/card_test.go`、`internal/agentd/ledgerapi_test.go`。
- 只给账本结构加 tag；不改 `internal/proto/ledger.go`、`internal/agentd/ledgerapi.go` 的 HTTP 投影、不改 `web/src/api/ledger.ts`。

精确结构变更：

```go
// internal/ledger/tasks.go
type TaskLink struct {
	CardID    string    `json:"card_id"`
	Target    string    `json:"target"`
	TaskID    string    `json:"task_id"`
	Purpose   string    `json:"purpose"`
	CreatedAt time.Time `json:"created_at"`
}

// internal/ledger/types.go
type Relation struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}
```

HTTP 侧保持这些既有 wire 类型与字段名：`proto.Relation.From/To/Type/CreatedAt`、`proto.TaskStateRow.TaskID` 等 PascalCase。`internal/agentd/ledgerapi.go` 的显式 projection 是防止 ledger tag 泄漏到 HTTP 的边界。

### 5.2 基线判据先跑与最小测试范围

动手前重跑 1.1 的 ledger、cmd、Ledger API 三条命令；预期为已记录的 `ok`。本 task 只跑 `./internal/ledger`、`./cmd` 和 `./internal/agentd` 的列举测试，不跑全量测试。

### 5.3 红绿步骤：真实 CLI 边界与 HTTP 负例

1. 在 `cmd/card_test.go` 复用 `runLedgerCLI`，先写失败的真实 CLI 回归：建卡、建任务链接和关系边，运行 `card show`，将 stdout 解成 `map[string]any`。逐项断言 `tasks` 的第一项含 `card_id`、`target`、`task_id`、`purpose`、`created_at`，不含 `CardID`/`TaskID`；`relations` 第一项含 `from`、`to`、`type`、`created_at`，不含 `From`/`To`/`Type`。先跑红。
2. 在 `internal/ledger/tasks.go` 与 `internal/ledger/types.go` 只增加 9 个蛇形 tag，不改字段名、SQL、事件或 CLI map。跑 `go test ./cmd -run 'TestCardAddListShowMove|TestCardShowLedgerWireUsesSnakeCase' -count=1` 与 `go test ./internal/ledger -run 'Test(TaskLink|Relation|Wire)' -count=1` 至绿。
3. 为“缺键”和“零值”可区分性在 `internal/ledger/wire_test.go` 增加 raw-map 断言：分别 marshal 零值 `TaskLink{}` 与 `Relation{}`，先用 map lookup 判断每一个蛇形键存在，再判断其值为空字符串/零时间；不能只把 map value 取出后与零值比较，因为那会把缺键误判成值为零。该单测是附加内部锁，不能替代真实 CLI seam。
4. 在 `internal/agentd/ledgerapi_test.go` 复用 `newLedgerEnv`、`seedCard`、`ledgerGet` 和既有 contract fixture，先加失败 HTTP 负例：真实 GET `/api/cards/:id` 的 `relations[0]` 含 `From`、不含 `from`，`task_states[0]` 含 `TaskID`、不含 `task_id`；状态码 200，其他 detail 字段不变。先跑红。
5. 因为 `ledgerapi.go` 明确构造 `proto.Relation`/`proto.TaskStateRow`，实现阶段不动该投影；仅让 HTTP 回归在加 ledger tags 后仍绿，以证明 CLI 和 HTTP 是两条有意不同的序列化边界。
6. 运行 ledger、cmd、Ledger API 最小命令，确认 CLI 蛇形与 HTTP PascalCase 同时成立；不得出现双键。

### 5.4 注释、日志与验收

- 更新 `TaskLink` 和 `Relation` 的类型注释，说明 tag 只服务直接编码 ledger 结构的 CLI；HTTP 使用 proto 投影并保留 PascalCase。
- 该 task 没有网络入口；CLI 测试日志/失败信息须包含 card id、tasks/relations 片段，HTTP 负例失败信息包含路由和实际键集合；不增加 print。
- 验收清单：`card show` 的 `tasks[].task_id`、`relations[].from` 可直接读取；CLI 没有大驼峰旧键；HTTP `GET /api/cards/:id` 的 `relations` 仍是 `From`、`task_states` 仍是 `TaskID`；零值字段存在而不是缺失；不改 proto/Web。

## 6. DAG、接缝双向覆盖与最终验收

### 6.1 实施顺序与有界文件集

1. B257 先完成 `workspace.go` 的锁与错误归因，才能让 B235 的 origin 同名 fetch 复用同一锁。
2. B235 再完成本地工作分支解析、Transport/stepTransport 回传和 dispatched 快照；它只依赖 B257 的私有锁调用约定，不改 B257 错误分类测试夹具。
3. B251 与 B260 可在 B235 之后并行实现，分别只触及各自列出的文件；B251 不触碰 workflow 配置，B260 不触碰 proto/HTTP。
4. 全部 task 的单包测试转绿后，由协调者执行一次总验收；全量命令不归属任何单个 task：`go test ./...`。总验收同时检查 `git diff --check`、计划之外文件没有改动、账本有逐事实记录。若总验收失败，记录原始输出并回到对应 task，不把失败归因改写成结论。

### 6.2 spec 用户故事归属

| 用户故事 | 具体 task / 缝级测试入口 |
|---|---|
| 工作分支 push 后下一节点包含提交 | B235，`resolveLocalBaseBranch` 真实 Git 快进并集测试 + `TestDispatchWireLocalBaseBranchEndToEnd` |
| 本地领先不回退 | B235，真实 Git 本地后代测试并断言 `refs/heads/<branch>` 不移动 |
| origin 不可达/缺分支仍从本地起 | B235，真实 Git fetch 失败测试与集成派发测试 |
| 本地/origin 分叉可见拒发 | B235，`resolveDispatchBase` + `writeDispatchError` 400 测试 |
| dispatched 可回答 branch/SHA | B235，`ViaTemplate`/`stepTransport` 至 `RecordDispatch` 的真实回传测试 |
| 同仓并发首派不误报基线缺失 | B257，`ResolveBaseBranch` 并发 fetch seam + `ResolveBaseline` 共享锁 seam |
| 日期前缀产出转等人并给改名路径 | B251，`NodeStep.RunOnce` 产出校验 seam |
| CLI 蛇形、HTTP PascalCase | B260，真实 `card show` seam + HTTP `GET /api/cards/:id` 负例 seam |

### 6.3 接缝双向矩阵

| spec seam | 锁定测试入口 | 生产调用链 |
|---|---|---|
| 1. B235 快进并集 | `resolveLocalBaseBranch`（真实 Git） | `resolveDispatchBase` → `Manager.Dispatch` |
| 2. B235 快照/step | `Dispatcher.ViaTemplate`，并穿过真实 `Server.stepTransport` | `card --step` → `stepTransport` → client `Task.BaseCommit` → `RecordDispatch` |
| 3. B257 同仓串行 | `ResolveBaseBranch` 与 `ResolveBaseline` 两 goroutine | fetch helper → 同一 per-repo lock → fetch + 目标 ref read |
| 4. B257 归因 | `Server.writeDispatchError` | Resolve* sentinel → HTTP 400 JSON |
| 5. B251 提示/匹配 | `NodeStep.RunOnce` 与 `buildPrompt` | Diff changed paths → exact `containsPath` → `haltForHuman`；模板 → prompt |
| 6. B260 wire | `runLedgerCLI` 与真实 HTTP GET | CLI 直接 Encode ledger；HTTP `ledgerapi.go` projection → proto JSON |

反向检查：每一支上表测试的入口都在对应声明缝或调用链穿过该缝；每条缝至少有一支行为断言。`wire_test.go` 的零值 raw-map 测试只作为 B260 附加内部锁，不能代替 CLI/HTTP 缝；B257 的 `runNetFetch` 注入只用于锁竞争耗尽分类，不能代替真实并发 fetch；不存在未声明的“若先绿则改入口”退路。

### 6.4 缺陷族对抗审查结论

- 旧 ref/错起点：B235 同名分支只在 `refs/heads` 与 `refs/remotes/origin` 间做祖先关系；B257 禁止 `FETCH_HEAD`；快进/本地领先/分叉/远端失败均有断言。
- 并发错位：锁键为 clean repo path，锁盖 fetch 和目标 ref read；不同分支、`fetch --all` 与分支 fetch、跨仓库均被测试，不用“有 mutex”计数代理行为。
- 错误归因：锁竞争独立 sentinel 先于 `ErrBaseCommitMissing` 映射；真缺失保留旧 sentinel；分叉独立 400；所有错误保留原始 fetch 文本。
- 状态/副作用：B235 不移动本地工作分支、不 reset、不自动合并不同名卡基线；B251 缺失仍 needs-human、不进 on_fail；B257 clone 不进 fetch 锁。
- 异步可见性：`--step` 不靠 CLI stderr；真实 step transport 回填目标 Task 的 BaseCommit，快照键可读。
- 序列化/兼容：新 dispatched `base`/`base_commit` 不回填旧事件；CLI ledger 结构蛇形且零值键仍存在；HTTP proto projection 继续 PascalCase，禁止双键。
- 产出匹配：机器键仍逐字相等；日期前缀只增加人类可行动提示，不放宽匹配。

### 6.5 序列化边界清单与回归要求

1. `internal/agentd/server.go` dispatch response → `internal/client/client.go:Client.Dispatch` 解码 `proto.Task.BaseCommit`：由 B235 真实 `--step` 测试穿过。
2. `cmd/card_dispatch.go`/`internal/agentd/cardstep.go` 的 transport 返回值 → `internal/ledgerstep/dispatch.go` 的 `DispatchResult` 与 `internal/ledger/events.go` 的 `DispatchSnapshot` → 事件 JSON：由返回一个与协调者本地 HEAD 不同的 40 字符值、再读取真实 dispatched payload 的测试穿过；用 raw map 区分 `base_commit` 键缺失与空/零值。
3. `internal/ledgerstep/dispatch.go:buildPrompt` → executor prompt：由 output path 无 card context、日期禁令存在的 prompt 测试穿过。
4. `internal/ledger/tasks.go`/`internal/ledger/types.go` → `cmd/card.go` 的直接 `json.Encoder`：由真实 `runLedgerCLI` 读取 snake keys 的测试穿过，raw-map 零值测试确认“存在但为空”不同于缺键。
5. `internal/agentd/ledgerapi.go` 的 ledger → `proto.Relation`/`proto.TaskStateRow` projection → HTTP JSON：由真实 `GET /api/cards/:id` 负例确认 PascalCase；不以 CLI 端测试代替。

每条边界都列出了产生方、手写投影/编码点、消费方和回归入口；B235 的 BaseCommit 这条链必须用可空/存在性检查区别字段缺失与零值，B260 的 snake/PascalCase 负例必须检查“不含另一种键”。

### 6.6 类型标注与真机清单

- Git 边界型：40 字符 SHA、`refs/heads/<branch>`、`refs/remotes/<remote>/<branch>`；真实仓库验证 fetch、祖先关系、ref 不移动、锁竞争原文。
- HTTP 边界型：锁竞争/分叉/真实缺失均为 400；lock body 不得含缺失基线提示；dispatch response 的 Task.BaseCommit 必须是 40 字符。
- 账本边界型：新 dispatched 有 `base`、`base_commit`；旧 dispatched 可读不回填；CLI `tasks`/`relations` 蛇形；HTTP `relations`/`task_states` PascalCase。
- 产出边界型：`containsPath` 是 exact string equality；日期前缀只有相邻提示；动作是 `ActionNeedsHuman`，不是 `on_fail` 路由。

### 6.7 计划自审声明

- 四个实现 task 都给出了基线命令、最小测试范围、关键节点日志要求和注释要求。
- 测试复用既有 harness 的地方已逐条列出断言和 harness 文件：B235 的真实 Git harness（`internal/agentd/workspace_test.go`、`integration_test.go`）、B251 的 node harness（`internal/ledgerstep/node_test.go`）、B260 的 CLI/HTTP harness（`cmd/card_test.go`、`internal/agentd/ledgerapi_test.go`）。这些是为保持现有包夹具形态而采用的计划例外，不是省略测试行为。
- 未引入占位步骤、未指定的公共框架、日期前缀历史文件改名、HTTP 蛇形迁移、dispatch 请求新字段、`FETCH_HEAD` 或 CLI stderr 警告通道。
- 产出文件和账本文件是本节点唯一写入文档；实现者不得把本计划当成实现代码直接复制到生产文件。
