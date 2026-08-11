# 补偿路径的分支处置决策：从隐式承重改为显式可测（B44）

> 状态：设计定稿，待实现
> 关联 backlog：B44
> 前置：B39（补偿路径本身）、B29（工作区准备与基线记录）

## 1. 问题

`deleteCreatedBranch`（`internal/agentd/manager.go:728`）有两道闸：

```go
if ws.NewBranchTip == "" {          // 闸1：不是本次新建的，不动
    return
}
if cur := branchTip(ctx, repo, ws.Branch); cur != ws.NewBranchTip {  // 闸2：尖端对不上，不删
    return
}
_, _, _ = gitRun(ctx, repo, "branch", "-D", ws.Branch)
```

把闸1 整段删掉，`go test ./internal/agentd/` **仍然全绿**。这是 2026-08-10 做 B29+B39 完工审阅时用变异检验实测到的。

原因是闸2 在常规场景下顺手替它挡住了：用户自带分支的 `NewBranchTip` 是空串，而分支实际尖端是真实 sha，两者不等 → 闸2 拦下。于是闸1 看起来是一行多余的短路。

**风险不是理论的。** 下一个人做「两道闸看着重复，简化掉一道」时，测试不会拦他，而闸1 有一个闸2 覆盖不到的独占角落。

## 2. 根因：一个哨兵值承载了三种含义

空串 `""` 同时表达：

| 出现位置 | 含义 |
|---|---|
| `ws.NewBranchTip == ""` | 这分支不是本次新建的（是用户的东西） |
| `branchTip()` 返回 `""` | 分支尖端**取不到**（rev-parse 失败） |
| `branchTip()` 返回 `""` | 分支不存在 |

闸2 的 `cur != ws.NewBranchTip` 在 `"" == ""` 时**放行删除**。闸1 的真实职责就是替这次撞车兜底——但这个保护关系是隐式的，代码里没有任何地方写着「闸1 独占的是哪种情形」，所以它也无法被单独测试。

### 2.1 撞车是可达的：悬空符号引用（已实测）

要触发撞车，需要一种「`rev-parse` 失败但 `branch -D` 成功」的状态。**悬空 symref** 就是：

```console
$ git symbolic-ref refs/heads/mine refs/heads/nonexistent
$ git rev-parse refs/heads/mine
warning: ignoring dangling symref refs/heads/mine
fatal: ambiguous argument 'refs/heads/mine': unknown revision or path not in the working tree.
$ echo $?
128
$ git branch -D mine
Deleted branch mine (was refs/heads/nonexistent).
$ echo $?
0
```

于是若闸1 缺席：用户拿 `--branch mine` 派发 → 派发中途失败触发补偿 → `branchTip` 返回 `""` → `"" == ""` → **用户自己的分支被删掉**，日志只会说「补偿删除分支完成」。

这种仓库状态很罕见（要靠 `git symbolic-ref` 手动造，或某个工具留下的残骸）。它的价值不在于「很可能发生」，而在于**证明这条撞车路径是可达的而非纯理论**——对一个不可逆操作来说，「我暂时想不出触发条件」不是安全论证。

### 2.2 两个已排除的猜想（实测，勿重复推导）

- **悬空对象引用不触发**：`refs/heads/x` 的 ref 文件在、指向的对象不存在时，`git rev-parse refs/heads/x` **成功**（exit 0，原样吐出那个 sha，不校验对象存在性）。所以这条路径下 `branchTip` 拿到的是 sha 而非空串，闸2 正常工作，不构成撞车。
- **超时不触发**：「rev-parse 超时返回空串、随后 branch -D 成功」不成立——`gitRun` 的 `WorkspaceGitTimeout`（2 分钟，`workspace.go:82`）是**整组共用一个 ctx**，前一条因 ctx 取消而失败时，后一条同样失败。

### 2.3 更根本的理由

即便触发条件罕见，闸1 的正确性也不该**依赖闸2 碰巧覆盖它**。今天成立是因为「用户分支的尖端取得到」，这是一条没人写下来、也没人测试的隐含前提；任何一次对 `branchTip` 的改动（比如给 `rev-parse` 加上 `--verify`，让它对悬空对象引用也失败）都会在无人察觉的情况下把撞车变成活的删分支缺陷。本设计要消除的是这种「依赖巧合」的结构，而不只是堵住某一个已知触发点。

## 3. 设计

核心是把「删不删」的判断从 I/O 里拆出来，变成一个纯函数——闸1 从「碰巧排在前面的一行短路」变成「表里明写的一条规则，有自己的返回值」。

### 3.1 `branchTip` 如实报告失败

```go
// branchTip 取分支当前尖端 sha。
//
// 返回：
//   - 40 位 sha 与 nil
//   - 取不到时返回空串与非 nil 错误（分支不存在、悬空引用、git 调用失败）
//
// 注意：失败不再塌缩成空串——决策方需要区分「取不到」与「不是本次新建的」，
// 这两件事在旧签名下共用空串，正是 B44 那道闸无法被单独测试的根因。
func branchTip(ctx context.Context, repo, branch string) (string, error)
```

### 3.2 新增纯决策函数

放在 `manager.go` 中 `deleteCreatedBranch` 紧邻处（它是补偿策略，不是 git 能力）：

```go
// branchAction 是补偿路径对「本次分支」的处置决定。
// 每个取值对应一条独立规则，便于表驱动测试逐条钉住。
type branchAction int

const (
	branchDelete        branchAction = iota // 确认是本次新建且自创建以来零提交，可删
	branchKeepNotOurs                       // 不是本次新建的，是用户自己的分支
	branchKeepTipUnknown                    // 尖端取不到，无从复核
	branchKeepTipMoved                      // 尖端与创建时不符，疑似已有提交
)

// decideBranchAction 判定补偿路径是否可以删除该分支。纯函数：不调 git、不打日志。
//
// 参数：
//   - recordedTip: PrepareWorkspace 建分支时记下的尖端；空串=分支不是本次新建的
//   - currentTip:  当前尖端；tipErr 非 nil 时无意义
//   - tipErr:      取当前尖端时的错误
//
// 判定顺序是本函数的全部要点：`recordedTip == ""` 必须排在任何拿 currentTip
// 做的比较之前。旧实现里这条规则靠「碰巧写在前面」生效，一旦有人认为两道闸
// 重复而删掉它，悬空 symref 场景下 ""=="" 会放行删除，毁掉用户自己的分支
// （该场景已实测可达，见 spec §2.1）。
func decideBranchAction(recordedTip, currentTip string, tipErr error) branchAction {
	if recordedTip == "" {
		return branchKeepNotOurs
	}
	if tipErr != nil {
		return branchKeepTipUnknown
	}
	if currentTip != recordedTip {
		return branchKeepTipMoved
	}
	return branchDelete
}
```

### 3.3 `deleteCreatedBranch` 改写

只负责调 git、问决策、按决定打日志并执行：

```go
func (m *Manager) deleteCreatedBranch(ctx context.Context, repo string, ws Workspace) {
	cur, tipErr := branchTip(ctx, repo, ws.Branch)
	switch decideBranchAction(ws.NewBranchTip, cur, tipErr) {
	case branchKeepNotOurs:
		return // 不是本次新建的，静默保留（这是绝大多数非新建分支派发的正常出口）
	case branchKeepTipUnknown:
		m.log.Warn("取分支尖端失败，无从复核，保留待查",
			"repo", repo, "branch", ws.Branch, "expect", ws.NewBranchTip, "cause", tipErr)
		return
	case branchKeepTipMoved:
		m.log.Warn("分支尖端与创建时不符，疑似已有提交，保留待查",
			"repo", repo, "branch", ws.Branch, "expect", ws.NewBranchTip, "actual", cur)
		return
	}
	m.log.Info("补偿删除本次新建的分支", "repo", repo, "branch", ws.Branch, "tip", ws.NewBranchTip)
	// 用 -D 而非 -d：分支起点可能领先仓库当前 HEAD，-d 会因「未合并」误拒；
	// 而「自创建以来零提交」已由上面的尖端复核实证，-D 在这里是确定性而非暴力
	if _, stderr, err := gitRun(ctx, repo, "branch", "-D", ws.Branch); err != nil {
		m.log.Error("补偿删除分支失败", "repo", repo, "branch", ws.Branch,
			"stderr", truncateRunes(stderr, 300), "cause", err)
		return
	}
	m.log.Info("补偿删除分支完成", "repo", repo, "branch", ws.Branch)
}
```

### 3.4 记录侧三处调用点

`workspace.go:264 / 280 / 302` 是「刚建完分支、记录基线」，改成显式处理错误：

```go
tip, err := branchTip(ctx, req.Repo, branch)
if err != nil {
	// 没记到基线意味着补偿届时无从复核，保守起见不会删——留空串即表达这个意思
	log().Warn("新建分支后取尖端失败，补偿将保留该分支", "branch", branch, "cause", err)
}
ws.NewBranchTip = tip
```

这里空串仍只有一个含义（没记到基线），不构成撞车：撞车原本只发生在**决策点**，而决策点现在拿到的是显式的 `tipErr`。

## 4. 测试策略

### 4.1 表驱动单测（新增，本 spec 的主交付）

`TestDecideBranchAction`，五行覆盖全部组合：

| recordedTip | currentTip | tipErr | 期望 | 钉住的是 |
|---|---|---|---|---|
| `""` | 真实 sha | nil | `branchKeepNotOurs` | 闸1 常规路径 |
| `""` | `""` | 非 nil | `branchKeepNotOurs` | **闸1 的独占角落（悬空 symref，见 §2.1）** |
| sha A | sha A | nil | `branchDelete` | 正常删除路径 |
| sha A | sha B | nil | `branchKeepTipMoved` | 闸2 |
| sha A | `""` | 非 nil | `branchKeepTipUnknown` | 取不到时的保守出口 |

第二行就是旧结构下必须引入注入机制才能构造的场景；抽成纯函数后它退化成一行表数据。

### 4.2 回归保护

现有 7 条 `TestCompensate*` 集成测试（`manager_test.go:1387` 起）**一条不改**。它们的作用是证明这次重构没有改变对外行为。

### 4.3 变异检验（验收必做）

删掉 `decideBranchAction` 里 `recordedTip == ""` 那一支，`go test ./internal/agentd/` **必须翻红**。

这条是 B44 存在的全部理由：不做变异检验，就无法证明这次改动真的解决了「测试拦不住删闸1」的问题。B44 本身就是变异检验发现的，用同一手段验收是自洽的。

## 5. 日志与注释要求

按 `instrumenting-code`：

- 三个 keep 分支各一条 WARN，带 `branch` / `expect` / `actual` 或 `cause`；`branchKeepNotOurs` 是正常出口，静默返回（它在每次 `--branch <已存在分支>` 派发失败时都会走到，打 WARN 是噪音）。
- delete 路径保留现有 Info（进入 + 完成）与失败 Error。
- `decideBranchAction` 是纯函数，**不打日志**——日志留在调用点。
- 有一处日志时序变化：旧实现先打「补偿删除本次新建的分支」再复核尖端，于是尖端对不上时会先打一条「要删了」再打一条「保留待查」，读日志的人要看两行才知道结论。新实现把它移到决策之后，日志与实际动作一一对应。这是本次唯一的可观测变化，不影响任何返回值或磁盘状态。
- `branchAction` 的每个枚举值、`decideBranchAction` 的判定顺序、`branchTip` 的新签名语义，都要有注释说明**为什么**，尤其是「顺序是本函数的全部要点」这句。

## 6. 边界：明确不做

- **不改任何对外行为**。四种情形的最终动作与今天逐一相同，这是一次纯粹的可测性重构。
- 不修悬空引用本身——那是 git 仓库的病，不是 handoff 的职责；本设计只保证不在它上面误删。
- 不引入任何注入/mock 机制。
- 不动 `RemoveManagedWorktree` 与归档路径（`Done`/`Stop`）的「只删树不删分支」语义。

## 7. 验收标准

1. `TestDecideBranchAction` 五行全过。
2. 现有 7 条 `TestCompensate*` 不改动且全过。
3. 变异检验：删掉 `recordedTip == ""` 分支 → `go test ./internal/agentd/` 翻红；恢复后转绿。
4. 闸门全绿：`gofmt -l .` 无输出、`go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./cmd/ ./internal/agentd/ ./internal/store/`、`GOOS=windows GOARCH=amd64 go build ./...`。
