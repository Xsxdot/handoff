# dispatch 基线决议设计（B35）

> 状态：设计已确认，待写实现计划
> 来源：`docs/superpowers/backlog.md` B35 行（08-10 B34 派发实撞）

## 0. 根因：校验的东西和使用的东西不是同一个

B35 在 backlog 里被记成「`--base` 默认取 HEAD，不提示落后」。**那个说法是错的**，它把问题描述成了可见性问题。读代码后真相更严重：

1. [`cmd/dispatch.go:50`](../../../cmd/dispatch.go) `localHeadCommit()` 取**审核者本机**的 HEAD，作为 `BaseCommit` 随请求上送。
2. [`internal/agentd/workspace.go:415`](../../../internal/agentd/workspace.go) `EnsureBaseCommit` 只校验一件事：**这个 commit 在任务仓库里存不存在**。不在就 fetch 一次再看。
3. [`internal/agentd/workspace.go:227`](../../../internal/agentd/workspace.go) 建分支时执行 `git worktree add -b handoff/<id8> <path>`，**不带起点**——起点是任务仓库的 HEAD。
4. [`internal/agentd/workspace.go:123`](../../../internal/agentd/workspace.go) 的注释白纸黑字写着「自动分支不带 Base」，第 188 行还有硬校验 `base 仅允许与 new-branch 连用` 挡着。

**校验的是 A（你的基线存在吗），使用的是 B（仓库 HEAD）。两者之间没有任何连接。**

08-10 实撞现场：`BaseCommit=d64bac4`，它以 `origin/main` 的身份确实躺在 devbox 的对象库里 → 校验通过；分支从 devbox 的 `HEAD=4fdc241` 开出——中间隔了 B27/B28/B33 三批改动。后果不是「差几个提交」：`skills/` 目录当时还不存在，计划里改 SKILL.md 的步骤做不了；backlog 里还没有 B34 行，回写步骤同样无从下手。最终只能 stop 重派。

B4 的同步校验全程显示「通过」，因为**它从来没承诺过基线会被用上**——它只承诺「你的基线在这儿能找到」。

这就是本设计要消除的东西：不是那一次陈旧基线，而是**让校验与使用可以分头演化的那个结构**。

## 1. 目标与非目标

**目标**：新分支的起点就是审核者派发时看到的那个提交；这个起点事后可查；任务仓库与它分叉时不静默。

**非目标**：

- **不碰 `--branch`（切已存在分支）模式**。切一个已存在的分支时不存在「起点」这回事。
- **不做自动 pull / rebase / merge**。发现分叉只报告，怎么合是人的决定。
- **不改 dispatch stdout 的既有契约**。只增 JSON 字段，不改形状——上层脚本按行解析任务 JSON，不能被这次改动打断。

## 2. 一条顺带的规则订正

[`workspace.go:188`](../../../internal/agentd/workspace.go) 现在的规则是：

```
base 仅允许与 new-branch 连用
```

所以自动分支 `handoff/<id8>` 连起点都不许带——这正是本 bug 得以成立的那道门。

改完之后自动分支**必须**能有起点，规则跟着改为：

```
base 与 branch（已存在分支）互斥
```

这不是为了让代码通过而放宽限制，是把规则改对了：`base` 真正的禁忌是「切一个已存在分支时谈起点没有意义」，跟是不是自动分支毫无关系。改完之后**少一个特例**，不是多一个。

## 3. `ResolveBaseline` 的契约

`EnsureBaseCommit` 升级为 `ResolveBaseline`：校验结论与新分支起点出自**同一次计算**。这是本设计的核心——只要这两样还由两段代码分别产出，今天的缺陷就随时能重现。

```go
// Baseline 是一次基线决议的结果。
type Baseline struct {
    // Start 是新分支起点（40 位 sha）。仓库一个提交都没有时为空，退回 git 默认行为。
    Start string
    // Ahead 是任务仓库 HEAD 上有、而 Start 上没有的提交数——这些提交不会进新分支。
    Ahead int
    // Fetched 表示是否为找到 Start 补拉过远端。
    Fetched bool
}

func ResolveBaseline(ctx context.Context, repo, sha string) (Baseline, error)
```

行为表：

| 入参 `sha` | 结果 |
|---|---|
| 空（`--no-sync-check`、cwd 非 git 仓库） | `Start` = 任务仓库当前 HEAD 的 sha，`Ahead=0`，Info 日志说明起点退回仓库 HEAD |
| 空，且任务仓库一个提交都没有 | `Start=""`，`Ahead=0`，无错误——交给 git 默认行为（空仓库上 `-b` 本来就不能带起点） |
| 非 40 位十六进制 | `ErrBadWorkspaceReq`（与今天一致） |
| 合法但仓库里没有 | fetch 一次；仍没有 → `ErrBaseCommitMissing`（与今天一致） |
| 在仓库里 | `Start = sha`，`Ahead = git rev-list --count <sha>..HEAD` |

**`Start` 永远是个具体 sha**，除非任务仓库一个提交都没有（表里第二行，只在空仓库上出现）。这条是刻意设计：它让「这个任务建在哪个提交上」在任何情况下都答得出来，包括 `--no-sync-check` 那条路——今天那条路上基线是纯粹的空白。

`Fetched` 只用于日志：补拉过远端时 Info 一行「为定位基线补拉了远端」。它不进 `Task`，也不影响起点选择——记它是为了排障时能分清「基线本来就在」和「补拉才拿到」。

**起点优先级**：显式 `--base` > `Baseline.Start` > 空（交给 git 默认）。

**显式 `--base` 时不发分叉警告**：用户已经明确指定了起点，再警告是噪音。

## 4. 可见性

三层，各有各的用途：

**入库**。`proto.Task` 加一个 ``BaseCommit string`` 字段（json 标签 `base_commit`），`tasks` 表加 `base_commit TEXT`，走 [`store.go:122`](../../../internal/store/store.go) 已有的 `ALTER TABLE ... ADD COLUMN` 增量迁移循环（不是新机制）。存的是**实际用的起点**（决议后的 `Start` 或显式 `--base`），不是请求里上送的那个原始 `BaseCommit`。

**dispatch stdout**。不动。多一个 JSON 字段是增量改动。`handoff show` 打印的就是任务 JSON，因此它自动能回答「这任务建在哪个提交上」——不需要单独改 show。

**dispatch stderr**。一行人读摘要，两种形态：

```
基线 d64bac4
基线 d64bac4（任务仓库 HEAD 领先 3 个提交，新分支不含它们）
```

老任务的 `base_commit` 为空，显示为空即可——不编造。

## 5. 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/agentd/workspace.go` | 修改 | `EnsureBaseCommit` → `ResolveBaseline` + `Baseline` 类型；第 1 层规则改为「base 与 branch 互斥」；自动分支放行 Base |
| `internal/agentd/manager.go` | 修改 | 接线：决议 → 起点优先级 → 分叉 WARN → 填 `WorkspaceReq.Base` 与 `task.BaseCommit` |
| `internal/proto/proto.go` | 修改 | `Task` 加 `BaseCommit` |
| `internal/store/store.go` | 修改 | 加列 + 读写 |
| `cmd/dispatch.go` | 修改 | stderr 一行基线摘要 |

## 6. 测试

`workspace_test.go` 里已有真实 git 仓库的集成测试脚手架（B8 那批），复用它：

1. `sha` 空 → `Start` = 仓库 HEAD，`Ahead=0`
2. `sha` 合法且就是 HEAD → `Start=sha`，`Ahead=0`
3. `sha` 是 HEAD 的祖先（仓库领先 N 个）→ `Start=sha`，`Ahead=N`
4. `sha` 格式非法 → `ErrBadWorkspaceReq`
5. `sha` 合法但 fetch 后仍不存在 → `ErrBaseCommitMissing`
6. **本 bug 的回归测试**：自动分支 + `NewWorktree` + 起点是个非 HEAD 的祖先 → 新 worktree 的 HEAD **等于该起点**
7. 显式 `--base` 压过基线起点，且不发分叉警告
8. `Task.BaseCommit` 写得进、读得回

**第 6 条是这份 spec 的锚**：它在今天的代码上**必然失败**——自动分支被 `workspace.go:188` 挡着，根本带不了起点。实现前先让它红，是唯一能证明「洞真的堵上了」的证据。不要跳过这一步直接写实现。

## 7. 落地后的连带动作

`skills/handoff/SKILL.md` 的 dispatch 段落补一句心智模型：

> 新分支的起点是**你派发时的本地 HEAD**，不是远端仓库的 HEAD。

并说明 `--no-sync-check` 的含义变宽了：它现在同时关掉校验**和起点决议**，起点退回远端仓库 HEAD。这个副作用不写出来，下一个人会以为它只关校验——而那正是今天这个 bug 的心智模型版本。
