# 派发入口的一致性与可诊断性（B42 + B43 + B45）

> 状态：已定稿，待 writing-plans
> 覆盖 backlog：**B42**（工作目录占用守卫）、**B43**（新树模式下任务仓库脏无提示）、**B45**（仓库不可用被扁平成 500）

## 1. 背景

三条缺口都落在 `Dispatch` 的**派发前置**这一小段代码上：`Manager.Dispatch` 从收到请求到 `PrepareWorkspace` 返回之间的那段。它们互不相同，但共享同一个失效形态——**派发看起来成功了（或失败得看不懂），而真实后果发生在审核者看不见的那台机器上**。

- B42：两个任务原地打同一个仓库，互相切走 HEAD，两个任务同时损坏且无任何报错。
- B43：`--new-worktree` 豁免了脏检查，于是任务仓库里未提交的改动对新工作树不可见，无人提示。
- B45：仓库路径压根不是 git 仓库时，本机派发得到无原因的 500，远程派发得到**误诊**的 400。

远程派发时审核者读不到执行机的 `agentd.log`，这是三条都值得修的共同理由。

### 1.1 一条被否决的前身

B42 原本写的是「`--new-worktree` 翻为缺省」。brainstorm 时确认其真实动机是「每次把本地项目第一次放到远程开发机上都要自己 ssh 去 clone 再回来填 `--repo`」，而**翻缺省根本不解决它**：`--repo` 仍须指向执行机上已存在的克隆，managed worktree 是从那个仓库长出来的。该动机已单列为 **B46**，本 spec 不覆盖。

翻缺省剩下的理由（不要求主仓干净、不动用户工作状态）经审视都是便利性，且有实打实代价（新树只含跟踪文件，没有 `node_modules`/`vendor`/`.venv`/构建缓存），**本 spec 明确不做**。同一次探查发现了它顺带遮住的真缺陷（§2.1），B42 改为只修那个缺陷。

## 2. 现状与实证

### 2.1 B42：没有任何占用守卫

`PrepareWorkspace` 只做 `git status --porcelain` 的脏检查（`workspace.go:333` 的 `ensureCleanWorktree`），`internal/agentd/` 内**没有**任何按 repo 或工作目录的占用登记。失效路径：

1. 任务 A 原地派发，仓库 HEAD 停在 `feat/a`，executor A 在 `/repo` 里干活
2. A 刚提交完一轮（writing-plans 明写 "Frequent commits"，这是常态而非边角），此刻 `git status` **是干净的**
3. 任务 B 原地派发同一仓库 → 脏检查放行 → `checkout -b feat/b` 成功，**共享的 HEAD 被切走**
4. executor A 毫不知情地继续改文件，它的下一次提交落在 B 的分支上

**保护窗口是反的**：A 有未提交改动时脏检查反而会挡住 B，A 一提交保护就消失。

范围比直觉小——**git 自己已经挡住了分支级冲突**：`git worktree add -b` 遇到已被别的工作树检出的分支会直接失败。洞只有一个，就是被共享的主工作树。managed 树之间、managed 树与原地任务之间都不冲突。

### 2.2 B43：新树模式豁免脏检查，但基线不含那些改动

`ensureCleanWorktree` 只在两个分支被调用：`req.Worktree != ""`（用户树，`workspace.go:261`）与 `default`（原地，`:274`）。managed 的 `--new-worktree` 分支**不经过它**。

豁免本身在「能不能建树」这件事上成立（新树天然干净，主仓有人手动改动也不该阻塞新任务开跑），但它没考虑另一件事：**主仓脏意味着基线不含那些改动**。于是 executor 在新树里拿到的是一份没有那些改动的代码，而无人提示。

对 B43 原始描述的一处修正：它记的是「本机派发」，但豁免在 agentd 侧，**远程派发同样中招**，且远程更严重——审核者根本看不到执行机仓库是脏的。本 spec 覆盖两者。

### 2.3 B45：仓库不可用没有被归类，且远程那半是误诊

`server.go:506` 的文档注释明写「ErrRepoUnusable → 400：调用方先解决请求本身的问题（仓库路径不对…）」，但全仓库唯一包装 `ErrRepoUnusable` 的地方是 `workspace.go:336` 的 `ensureCleanWorktree`（`git status` 失败），而它只在上述两个分支被调用。于是 managed 路径上仓库不可用有两种表现：

| 场景 | 现在的行为 | 问题 |
|------|-----------|------|
| 本机派发（无 `base_commit`） | `headCommit` 返回空串 → 一路走到 `worktree add` 失败 → 落 `server.go:538` 的 default → **500「派发任务失败」** | 真因 `fatal: not a git repository` 只在 agentd.log 里 |
| 远程派发（有 `base_commit`） | `hasCommit` 查不到 → 触发 `fetch` → 也失败 → **400「任务仓库落后于本地；请先在本地 git push」** | **自信地误诊**：仓库不是落后，它压根不是仓库，报文却指挥你去 push |

第二条是报告里没有的，且远程派发正是 B45 自己陈述的动机场景。只在 `worktree add` 之前加校验修不到它，因为 `ResolveBaseline` 跑在更前面。

`headCommit`（`workspace.go:543` 附近）的注释目前写着「真正的仓库问题会在 PrepareWorkspace 的脏检查/建树阶段暴露」——这句话正是本缺口的来源，修复后必须一并改掉，否则留一条骗后人的线索。

另两条路径不受影响：原地模式先跑 `git status` 会正常归类，用户树模式先被 `worktreeBelongsToRepo` 挡成 `ErrBadWorkspaceReq`。

## 3. 设计

### 3.1 派发前置的执行顺序

三处校验统一放进 `Manager.Dispatch` 的前置块，顺序固定：

```
EnsureRepoUsable(repo)        // §3.2 —— 最基础、最便宜
  ↓
guardWorkdirBusy(workDir)     // §3.3
  ↓
ResolveBaseline(repo, sha)    // 既有
  ↓
PrepareWorkspace(...)         // 既有（脏快照在此，见 §3.4）
```

**为什么占用守卫排在 `ResolveBaseline` 之前**：后者在基线缺失时会做一次 `git fetch --all --prune`（网络代价）。一个注定要被拒的派发不该先付这笔钱。

**为什么这三处不放进 `workspace.go`**：`PrepareWorkspace` 是纯 git 模块，不认识任务表；把 store 传进去等于给它引入存储依赖。而守卫需要的 `WorkDir` 在派发前就已知（见 §3.3），`Dispatch` 自己就能算出来，零耦合。§3.2 的校验是纯 git 操作，函数体放在 `workspace.go`（与其他 git helper 和 `ErrRepoUnusable` 同处），**调用点**在 `Dispatch`。

### 3.2 B45：仓库有效性校验

`internal/agentd/workspace.go` 新增导出函数：

```go
// EnsureRepoUsable 校验 repo 确实是一个可用的 git 仓库。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - repo: 任务仓库路径
//
// 返回：
//   - nil：是可用的 git 仓库
//   - ErrRepoUnusable：路径不存在 / 不是 git 仓库 / git 不在 PATH / 权限不足，
//     错误文本带 git stderr 原文
//
// 注意：
//   - 由 Dispatch 在 ResolveBaseline 之前调用。放在那里而不是建树前，是因为
//     ResolveBaseline 对非 git 仓库会误报成 ErrBaseCommitMissing（「落后于本地，
//     请先 push」），那是个比沉默更糟的答案
func EnsureRepoUsable(ctx context.Context, repo string) error {
	if _, stderr, err := gitRun(ctx, repo, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrRepoUnusable, strings.TrimSpace(stderr), err)
	}
	return nil
}
```

用 `rev-parse --git-dir` 而不是在 `worktree add` 的错误串里 grep `"not a git repository"`：前者是显式判据，后者依赖 git 的文案不变。它同时覆盖「git 不在 PATH」（`gitRun` 返回 exec 错误，stderr 为空但 err 有原文）。

`ensureCleanWorktree` 里原有的 `ErrRepoUnusable` 包装**保留**——它仍是 `git status` 因其他原因失败时的兜底，且删掉会动到既有测试。

`headCommit` 的注释改为说明「仓库有效性已由 Dispatch 前置的 `EnsureRepoUsable` 保证，这里返回空串只对应空仓库」。

### 3.3 B42：工作目录占用守卫

**判定语义**：所有**非终态**任务都算占用（`pending` / `running` / `waiting_answer` / `waiting_review`），`completed` / `failed` 不算。

`waiting_review` 必须计入：审核期间要跑 `diff` / `fetch` / `run` / `continue`，HEAD 被切走这些全会看错东西，`continue` 回去更是在别人的分支上干活。代价是同一仓库上要再派一个原地任务必须先 `done` 掉上一个——这是正确的代价。

**判定键是 `WorkDir`，一条规则覆盖三种模式**：

| 模式 | 目标工作目录 | 冲突可能 |
|------|------------|---------|
| managed（`--new-worktree`） | `DataDir/worktrees/<id8>`，每任务唯一 | 天然不冲突，**不查** |
| 原地（缺省） | `req.Repo` | 与同仓库的其他原地任务冲突 |
| 用户树（`--worktree`） | `req.Worktree` | 与指同一棵树的任务冲突 |

`Dispatch` 侧计算：

```go
// managed 树每任务一棵，天然不冲突，不必查；另两种模式的目标目录在派发前已知
occupied := ""
if !req.NewWorktree {
	occupied = req.Repo
	if req.Worktree != "" {
		occupied = req.Worktree
	}
}
```

**占用状态放在哪**（三选一，取 A）：

- **A. 查任务表**（采用）——唯一事实来源就是已有的任务表，agentd 重启后自动正确（启动恢复本来就会把非终态任务读回来），不引入任何新状态。代价只是多一次 DB 查询。
- B. Manager 内存占用表——重启即丢。而 agentd 重启后 `waiting_review` 任务仍占着那棵树（B18 做的正是这个续接场景），内存表恰好在最需要它的时候失效。**否决**。
- C. 工作树里放锁文件——B34 已用 DataDir 文件锁保证同一 DataDir 只有一个 agentd；两个不同 agentd 打同一仓库属配置错误，不该由这里兜底。还要写锁文件的残留清理路径，成本高于收益。**否决**。

**新增代码**：

`internal/proto/proto.go`——终态谓词（现在 `manager.go:923` 是裸比较，无共享谓词）：

```go
// TerminalStates 是任务的两个终态：到此不再有 executor 持有工作区。
var TerminalStates = []TaskState{TaskStateCompleted, TaskStateFailed}

// IsTerminal 报告该状态是否为终态（completed / failed）。
func (s TaskState) IsTerminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed
}
```

`manager.go:923` 的裸比较改用 `cur.State.IsTerminal()`——这是我们正在改的代码里的既有毛刺，不是无关重构。

`internal/store/store.go`——按工作目录查活跃任务：

```go
// ActiveTasksByWorkDir 返回工作目录为 workDir 的全部非终态任务。
//
// 参数：workDir 为工作目录绝对路径（原地模式即仓库路径）
//
// 返回：非终态任务切片（可能为空）；查询失败返回错误
//
// 注意：
//   - 终态清单取自 proto.TerminalStates，避免与状态机定义漂移
//   - 空 workDir 直接返回空切片：不查是刻意的，managed 模式不需要这个判据
func (s *Store) ActiveTasksByWorkDir(workDir string) ([]proto.Task, error)
```

SQL 用 `WHERE work_dir = ? AND state NOT IN (...)`，占位符个数由 `proto.TerminalStates` 长度生成。

> **一处已过期的注释，顺带修掉**：`manager.go:532` 写着「WorkDir 原地模式存空串」，`store.go:78` 的建表注释同样写着「work_dir=工作区目录（空=原地模式…）」。**两处都已过期**——`PrepareWorkspace` 的原地分支现在写的是 `WorkDir: req.Repo`，用户树分支写 `WorkDir: req.Worktree`，新建任务的 `work_dir` 一定是满的。`proto.Task.Workdir()` 回退到 `repo_path` 的那条路，对新任务而言已是死代码。本 spec 一并改掉这两条注释。
>
> 查询仍保留对空串的兜底——`WHERE (work_dir = ? OR (work_dir = '' AND repo_path = ?))`。理由不是「不确定」，而是**旧库里可能存在历史行**：那时原地模式确实存空串，`Workdir()` 的回退就是为它们写的。三个词的代价换旧库正确，值得。

`internal/agentd/workspace.go`——新哨兵：

```go
// ErrWorkdirBusy 表示目标工作目录已被一个非终态任务占用。
ErrWorkdirBusy = errors.New("目标工作目录已被活跃任务占用")
```

**拒发报文**（带上审核者下一步要用的一切）：

```
目标工作目录已被活跃任务占用: /repo 正被任务 a1b2c3d4-...（重构登录, waiting_review）占用；
先 handoff done/stop 它，或改用 --new-worktree 在独立工作树上开工
```

`server.go` 的 `writeDispatchError` 新增一路，与「工作区不干净」同为 **409**（都是状态冲突而非请求错误）：

```go
case errors.Is(err, ErrWorkdirBusy):
	s.log.Warn("dispatch 被拒：目标工作目录被占用", "repo", repo, "cause", err)
	writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
```

同时更新 `writeDispatchError` 上方的映射规则文档注释。

### 3.4 B43：新树模式下的脏快照

只在 managed 分支做，**不拒发**，保住「新树天然干净，不阻塞」的既有价值。

**不区分已跟踪 / 未跟踪**：B29 分它们是因为处置不同（拒 vs 警告）；这里两者对新工作树同样不可见，处置完全一样，分了只是噪音。

**数据结构**——`proto.Task` 新增两个字段。用两个而非一个：条数必须在封顶之后仍然准确（9 个文件只列 5 个时，"9 处" 这个信息不能丢）。这与既有的 `BaseCommit` / `BaseAhead` 是同一种成对形态。

```go
// RepoDirtyCount 是派发当时任务仓库未提交改动的**总数**（含未跟踪文件）；
// 0=干净或非 managed 模式。这些改动不在新工作树里，executor 看不到它们。
RepoDirtyCount int `json:"repo_dirty_count"`
// RepoDirtyFiles 是上述改动的文件名展示串（逗号分隔，封顶 5 个）；
// 服务端截断后的展示用字段，与 PlanSummary 同形，不供程序消费。
RepoDirtyFiles string `json:"repo_dirty_files"`
```

**存储迁移**：走 `store.go:117` 既有的增量列循环（逐条 `ALTER TABLE tasks ADD COLUMN`，容忍 duplicate column），新增两列：

```
"repo_dirty_count": "INTEGER NOT NULL DEFAULT 0",
"repo_dirty_files": "TEXT NOT NULL DEFAULT ''",
```

`CREATE TABLE` 里也要同步补上这两列与注释（新库不走迁移路径）。`insertTask` / `scanTask` 的字段列表随之扩展。

**采集点**：`PrepareWorkspace` 的 managed 分支，建树**之前**对 `req.Repo` 跑 `git status --porcelain`；结果写进 `Workspace` 的两个新字段，由 `Dispatch` 落到 `proto.Task`。采集失败（例如 git 临时故障）**不阻断派发**——打一条 WARN 日志，快照留空。理由与 `currentRef` 相同：诊断信息的采集不该挡住主流程。

**渲染**：`cmd/dispatch.go` 在既有的「基线」摘要之后，`RepoDirtyCount > 0` 时向 **stderr** 追加一行（stdout 的「单行任务 JSON」契约不能破，见 `cmd/dispatch.go:127`）：

```
提示: 执行机仓库有 3 处未提交改动，新工作树不含它们：a.go, b.go, c.go
```

服务端同时打一条 WARN，含完整未截断的文件列表。

## 4. 错误分类总表（改动后）

| 场景 | 哨兵 | 状态码 | 报文 |
|------|------|--------|------|
| 仓库路径不是 git 仓库 / git 不在 PATH | `ErrRepoUnusable` | 400 | 带 git stderr 原文 |
| 目标工作目录被活跃任务占用 | `ErrWorkdirBusy` | 409 | 点名占用任务 + 两条出路 |
| 工作区不干净（原地 / 用户树） | `ErrDirtyWorktree` | 409 | 既有 |
| 基线提交缺失 | `ErrBaseCommitMissing` | 400 | 既有（不再被非 git 仓库误触发） |
| 新树模式下任务仓库脏 | 无（不是错误） | 200 | 任务 JSON 带快照 + stderr 提示 |

## 5. 测试

### 5.1 占用守卫

- **store 层单测**：六个状态各造一条任务，断言 `ActiveTasksByWorkDir` 只捞回四个非终态
- **store 层单测**：直接插一条 `work_dir` 为空串、`repo_path` 为目标路径的历史行 → 断言仍能被查到（§3.3 旧库兜底分支的守门人；这条不能靠正常派发构造，必须直插）
- **集成**：原地派 A → 同仓库再派 B → 断言 409 且报文含 A 的 id；`done` 掉 A 后 B 能成功
- **集成（防误伤）**：同一仓库连派两个 `--new-worktree` 任务 → **都成功**。守卫不该挡住本来就安全的路径
- **集成**：两个任务指同一棵用户树 → 第二个被拒

### 5.2 B45

- **集成**：`--new-worktree` + 非 git 路径 → 400 且响应体含可读原因
- **集成（远程形态，不可省）**：带 `base_commit` 派同一个非 git 路径 → 断言拿到 `ErrRepoUnusable` 而**不再是** `ErrBaseCommitMissing` 的误诊。这条是 B45 动机场景的守门人
- **集成**：git 不在 PATH（PATH 剥离构造）→ 同样归入 `ErrRepoUnusable`
- 注意 `internal/agentd/integration_test.go:528` 附近既有的「未知错误扁平化为 500」断言，新行为可能与它冲突，必要时收窄那条的构造条件

### 5.3 B43

- **集成**：managed 模式 + 主仓有未提交改动 → 派发成功、Task 带上快照、条数正确、文件串封顶生效（造 9 个脏文件，断言只列 5 个而 count 为 9）
- **集成**：主仓干净 → 两个字段为零值
- **`cmd/` 单测**：断言 stderr 那行提示的文案与渲染，以及 `RepoDirtyCount == 0` 时不打印

### 5.4 判别力

每条实现完成后做一次变异检验：打断实现，确认对应测试转红。这是 B44 的直接教训——测试全绿不等于钉住了行为，上一轮就有一道闸被穿透而无人知晓。

## 6. 验收

- `go build ./...` / `go vet ./...` / `gofmt -l .` 无输出 / `go test ./... -count=1` 全绿
- `go test -race ./cmd/ ./internal/agentd/ ./internal/store/`
- `GOOS=windows GOARCH=amd64 go build ./...`
- **真机（devbox）**：
  1. 原地派发一个任务占住仓库 → 同仓库再派一个 → 确认 409 且报文点名前者；`done` 后重派成功
  2. 远程派发一个非 git 路径 → 确认拿到可读的 400 而非 500，也不是「请先 git push」的误诊
  3. 执行机仓库造几个未提交改动 → `--new-worktree` 派发 → 确认 stderr 那行提示出现且文件名正确

## 7. 明确不做

- **`--new-worktree` 翻为缺省**（B42 原标题）：动机与机制脱节，见 §1.1
- **远程仓库首次落地自动化**：单列 B46，需要独立 brainstorm（仓库放哪、克隆凭据、执行机 × 仓库登记表、幂等、多执行机）
- **占用守卫的 `--force` 逃生口**：YAGNI。`--new-worktree` 已经是一条正当且无副作用的出路，再加一个绕过闸只会被当成默认操作
- **把 `classifyLocalDirty` 提到共享包**：§3.4 已决定不区分跟踪状态，服务端不需要那个分类器
