# dispatch 前置校验与失败补偿 设计

> 覆盖 backlog **B29**（远程派发看不见本地未提交改动，静默放行）与 **B39**（dispatch
> 失败后清理了 worktree 却留下新建分支）。两者同处 dispatch 的准备—补偿这一段代码，
> 且都是「一次派发对世界留下的痕迹与它宣称的语义不符」。

## 1. 背景与根因

### 1.1 B29：脏检查做在了另一棵树上

handoff **有**工作区脏检查。`ensureCleanWorktree`（`internal/agentd/workspace.go:310`）
跑 `git status --porcelain`，非空即以 `ErrDirtyWorktree` 硬拒。

但它检查的是 **agentd 那台机器上的任务仓库**。审核者本地这棵树，`localHeadCommit`
（`cmd/dispatch.go:52`）只跑了一句 `git rev-parse HEAD`——工作区脏不脏从头到尾没人看过。

于是远程派发时，若本地有未提交改动：

1. CLI 上送一个**不含这些改动**的 commit 作基线
2. agentd 的 `ResolveBaseline` 确认该 commit 在远端存在——通过
3. agentd 从该 commit 开分支——通过
4. executor 基于一份缺了你最新改动的代码开工

四步全绿，全程零提示。这与 B35 是同一类失效（基线不是你以为的那个），且 B35 的现场
证明了这类问题有多难自查：执行者报告「找不到 skills/ 目录」，审核者的第一反应是执行者
搞错了路径，ssh 上去核实才发现执行者是对的。

**与 B4 的分工**：B4 保证「本地提交已推送 + 基线提交在远端存在」，管的是 commit 的
可达性；本条管的是 commit 的**完整性**——没进 commit 的东西，任何可达性检查都发现不了。

### 1.2 B39：一个为归档写的函数被复用到了创建失败场景

`RemoveManagedWorktree` 的文档明写：

> 只删工作树不删分支（spec：任务分支保留供审阅/回滚）

这条规则在 `Done` / `Stop` 路径上完全正确——分支上是任务成果，删了就没了。

但 `compensateManagedWorktree`（`internal/agentd/manager.go:601`）走的是 dispatch
**失败**路径：分支是几秒前刚建的，`executorStarted` 守卫保证 executor 从未启动，
分支上保证零提交。「保留供审阅」在这里无的放矢，留下的只是一个挡路的空分支——
实撞现场：修好环境后用同一分支名重试，agentd 直接 500
`fatal: a branch named 'e2e/mac-1' already exists`。

**同一函数还漏掉了非 managed 模式。** 首行 `if !ws.Managed { return }` 意味着原地模式
（当前的**默认**模式）与用户树模式下，PrepareWorkspace 之后的失败**完全没有补偿**：
分支没删，而且那棵工作树就**停在这个空分支上**。原地模式下这棵树是执行机的主仓库，
用户下次 ssh 上去会发现自己不在原来的分支上，且没有任何记录说明是谁切的。

## 2. 目标与非目标

**目标**

1. 远程派发时，本地已跟踪文件的未提交改动**拒发**并给出可行动指引；未跟踪文件**警告**放行
2. dispatch 失败补偿时，删掉**本次新建**的分支，并把非 managed 工作树切回原处
3. 上述补偿在任何一步不确定时一律**保留现场**，绝不误删用户的分支或成果

**非目标**

- **不翻转 `--new-worktree` 缺省**。二期 spec 第 183 行已把它挂为待议项，本次讨论确认
  它与 B29 正交（没进 commit 的改动，远端建什么树都变不出来），且它会把本条的补偿路径
  从边角推上主干——顺序上必须排在本条之后，单独立项。
- **不做本机派发时 cwd 的脏检查**。本机派发时 `--repo` 与 cwd 未必是同一个仓库，拿 cwd
  的状态去拦别的仓库是假拒绝——这正是现有代码不采集本地基线的理由，此处沿用同一判断。
  （由此产生的缝隙：本机派发 + `--new-worktree` 时本地未提交改动同样不可见。已知，
  记入 backlog 另行处理，不在本期。）
- **不改 `RemoveManagedWorktree` 在 Done/Stop 路径的语义**。「只删工作树不删分支」在那里
  是对的，本次只在补偿路径**之外**追加删分支动作，不动该函数本身。
- **不对 executor 已接管的工作区做补偿**。`executorStarted` 守卫保持不变——删运行中任务的
  工作树是把它脚下抽空，泄漏与破坏之间宁可泄漏。

## 3. 设计：B29 本地基线完整性校验

### 3.1 位置与触发门

**位置**：客户端 `cmd/dispatch.go`。这一条没有选择余地——只有客户端看得见自己的工作树。
本地先报错也与该文件既有纪律一致（「plan 与 `--prompt` 都缺就本地先报错，省一次网络往返」）。

**触发门**：与基线采集**共用同一个门**——`targetName != "" && !dispatchNoSyncCheck`。

理由：这个检查存在的唯一目的是保证上送基线的有效性。基线不采集时（本机派发、
`--no-sync-check`），它没有意义。共用一个门也让「什么时候会被拦」这件事只有一条规则。

cwd 不是 git 仓库 / git 不可用时：沿用现有的「跳过」语义，现有提示文案已覆盖。

### 3.2 判据

`git status --porcelain` 的输出逐行分类：

| 行首 | 含义 | 处置 |
|---|---|---|
| `??` | 未跟踪文件 | 计数，stderr 警告一行，**放行** |
| 其余（`M `/` M`/`A `/`D `/`R `/`UU` 等） | 已跟踪文件的改动（含已暂存） | **拒发** |

已暂存的改动（`M `、`A `）归入拒发：`git add` 过但没 commit 的东西，executor 一样看不到。

未跟踪只警告的理由是信噪比：改过的已跟踪文件几乎必定是 executor 要读的代码，而未跟踪
文件在日常工作目录里大多是 `.DS_Store`、临时脚本、scratchpad 产物。代价是**新写的未跟踪
源文件会被降级成警告**——这一点在警告文案里写明，让审核者自己判断。

抽一个纯函数承担分类，不碰 git：

```go
// classifyLocalDirty 把 git status --porcelain 的输出分成「已跟踪改动」与「未跟踪文件」两类。
func classifyLocalDirty(porcelain string) (tracked []string, untracked []string)
```

### 3.3 输出契约

**拒发**（退出码非零，不发起 HTTP 请求）：

```
本地工作区有 3 处未提交的已跟踪改动，executor 看不到它们：
  internal/agentd/workspace.go
  cmd/dispatch.go
  README.md
远程派发会基于不含这些改动的基线开工。请先 git commit 或 git stash；
确要照现状派发，加 --allow-dirty。
```

文件数超过 5 个时只列前 5 个，末行补 `... 另有 N 处`。

**未跟踪警告**（stderr，照常派发）：

```
提示: 本地有 2 个未跟踪文件未被派发（executor 看不到）：scratch.md, tmp.log
```

超过 5 个同样截断计数。

**`--allow-dirty`**：只关掉硬拒，**警告照打且列出文件名**。绕过不等于静默——一个静默的
`--allow-dirty` 就是新的 B29。

所有提示走 **stderr**：stdout 的「单行任务 JSON」契约是上层脚本按行解析的依据，多打一行
就会把它们全部打断。

## 4. 设计：B39 补偿路径的分支与工作树复原

### 4.1 Workspace 需要记住两件事

现有结构只有 `Branch` / `WorkDir` / `Managed`，无法回答「这个分支是不是我建的」和
「我把这棵树从哪儿切走的」。新增两个字段：

```go
type Workspace struct {
	Branch  string
	WorkDir string
	Managed bool

	// NewBranchTip 是本次 dispatch 新建分支时的尖端 sha；空串表示分支不是本次新建的
	// （--branch <已存在分支> 模式）。补偿删分支前用它复核分支自创建以来没动过。
	NewBranchTip string
	// PrevRef 是非 managed 模式下 checkout -b 之前的 HEAD：正常在分支上时为分支名，
	// detached 时为 commit sha。managed 模式恒为空（新工作树没有「之前」）。
	PrevRef string
}
```

用一个 sha 而不是 `BranchCreated bool` + 另一个字段，是为了让「声称新建了分支却说不出
它当时指向哪」这个非法状态在类型上就构造不出来。

`NewBranchTip` 在三种工作树模式下都设（值为 `!isExisting` 时紧接建分支后的
`git rev-parse <branch>`）。`PrevRef` 只在原地/用户树模式、`checkout -b` **之前**采集：
`git symbolic-ref --short -q HEAD`，失败（detached）时退回 `git rev-parse HEAD`。

**采集失败时留空**，不报错——采集失败不该挡住派发；由 4.3 的 fail-safe 承接。

### 4.2 补偿的两种形态

`compensateManagedWorktree` 更名为 `compensateWorkspace`（它不再只管 managed worktree），
先做一道空值守卫（`ws.WorkDir == ""` 直接返回——现有代码把 defer 注册在 PrepareWorkspace
成功之后，理论上到不了，但补偿函数本身不该依赖调用点的注册位置），再按模式分派：

**managed（新工作树）**

1. `RemoveManagedWorktree`
2. 失败 → **就此停手，不删分支**（分支还被那棵树 checkout 着，git 本来也会拒；
   且失败现场要留给人排查）
3. 成功 → 进 4.3 删分支

**非 managed（原地 / 用户树）**

1. `PrevRef` 为空 → **就此停手**，记 WARN 并给出人工指引（不知道该切回哪儿，
   乱切比不切更糟）
2. 在 `ws.WorkDir` 里 `git checkout <PrevRef>`
3. 失败 → 停手，记 ERROR + 人工指引（工作树还停在新分支上，分支也删不掉）
4. 成功 → 进 4.3 删分支

### 4.3 删分支的三道闸

1. `NewBranchTip == ""` → 不动。分支不是本次建的（`--branch <已存在>` 模式），是用户的东西。
2. `git rev-parse <branch>` **不等于** `NewBranchTip` → 只记 WARN 不删。理论上
   `executorStarted` 守卫保证零提交，但删分支不可逆，宁可留个残留也不能删错。
3. `git branch -D <branch>`

第 3 步用 `-D` 而非 `-d`：分支起点可能领先仓库当前 HEAD，`-d` 会因「未合并」误拒；
而「自创建以来零提交」已由第 2 步实证，`-D` 在此处不是暴力而是确定性。

失败只记 ERROR，**不覆盖也不替换原始派发错误**——审核者要看到的是任务为什么没派出去，
补偿的成败是次要信息。这与现有 `compensateManagedWorktree` 的纪律一致。

## 5. 日志

沿用 `logger`（slog），关键节点一条不漏：

| 节点 | 级别 | 内容 |
|---|---|---|
| 本地脏拒发 | —（CLI 无 logger） | stderr 文案，见 3.3 |
| 未跟踪警告 / `--allow-dirty` 放行 | — | stderr 文案，含文件名 |
| 进入补偿 | Warn | 沿用现有「dispatch 后续失败，补偿清理」+ 补 `managed` / `prev_ref` 字段 |
| 切回原 ref | Info | `workdir` / `prev_ref` |
| 切回失败 | Error | 上述 + `cause` + 人工指引 |
| 尝试删分支 | Info | `branch` / `tip` |
| 尖端不符拒删 | Warn | `branch` / `expect` / `actual` + 「疑似有提交，保留待查」 |
| 删分支成功 | Info | `branch` |
| 删分支失败 | Error | `branch` / `cause` |
| `PrevRef` 缺失停手 | Warn | `workdir` / `branch` + 人工指引 |

## 6. 测试

**B29 · 纯函数**（表驱动，无 git）：干净 / 只有未跟踪 / 只有已跟踪 / 混合 /
已暂存（`M `）/ 重命名（`R `）/ 冲突（`UU`）/ 含空格的文件名。

**B29 · CLI 层**：
- 已跟踪脏 → 返回错误**且未发起 HTTP 请求**（用假 server 断言零请求——这是判别力所在，
  只断言错误的话，一个先发请求再报错的实现照样绿）
- `--allow-dirty` → 放行，且 stderr 含被忽略的文件名
- 只有未跟踪 → 放行，stderr 有警告
- 本机派发（无 `--target`）→ 完全不查
- `--no-sync-check` → 完全不查

**B39 · 真实 git 仓库集成测试**（沿用 `workspace_test.go` 中 `TestResolveBaseline*`
已有的建仓套路），注入一个 PrepareWorkspace 之后的失败：

| 场景 | 断言 |
|---|---|
| managed + 自动分支 | worktree 没了 **且** 分支没了 |
| managed + `--branch <已存在>` | worktree 没了，分支**还在** |
| managed，worktree 删除失败 | 分支**还在** |
| 原地 + 自动分支 | 主仓回到原分支 **且** 新分支没了 |
| 原地，detached HEAD 起步 | 回到原 commit（仍 detached），新分支没了 |
| 用户树 + `--new-branch` | 用户树回到原分支，新分支没了 |
| 分支尖端被动过 | 分支**还在**，日志有拒删记录 |

后四行是判别力所在：只有前两行的话，一个「无脑删分支、不管非 managed 模式」的实现
照样全绿。

## 7. 验收

- `go build ./...` + `go vet ./...` + `gofmt -l .` 无新增 + `go test ./...` 全绿
- `GOOS=windows GOARCH=amd64 go build ./...` 全绿（B36 立的门禁）
- 真机（devbox）两条：①本地故意改一个已跟踪文件不提交 → `dispatch --target` 被拒且
  文案列出该文件，加 `--allow-dirty` 后放行且警告里有它；②复现 B39 现场——让 agentd
  在 PrepareWorkspace 之后失败（例如把 executor 从 PATH 里摘掉），确认 worktree 与
  分支都没了，随后**用同一分支名重试成功**（这是 B39 的原始诉求，也是唯一能证明修复
  有效的断言）
- 回归锚：新增测试中至少一条抄回旧实现复跑并确认变红，红→绿证据入 backlog 验收列
