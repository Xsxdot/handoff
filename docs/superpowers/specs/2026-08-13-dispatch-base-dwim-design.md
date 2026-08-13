# B76：`--base <分支名>` 触发 git DWIM，静默把任务开在别人的分支上

> 状态：设计完成，待实现
> 来源：08-12 B72 派发的实地观察；08-13 排查定位根因并实证复现

---

## 1. 问题

### 1.1 现象

08-12 派发 B72 时用的命令是：

```bash
handoff dispatch --target devbox --new-worktree \
  --new-branch feat/b72-birth-registry \
  --base worktree-b69-b70-proc-footprint
```

结果：

- dispatch 应答 JSON 说 `"branch":"feat/b72-birth-registry"`，一切正常
- 任务实际开在 `worktree-b69-b70-proc-footprint` 上
- 11 个提交全落到那条分支——**那是审核者自己正在用的分支，被未经声明地改写**
- 收工 `handoff pull` 报 `couldn't find remote ref feat/b72-birth-registry`
- devbox 上 `feat/b72-birth-registry` 这个 ref 从来就不存在

### 1.2 根因：git 的 DWIM 顶替了显式 `-b`

[workspace.go:269](../../../internal/agentd/workspace.go) 执行的是：

```
git worktree add -b <new-branch> <workdir> <base>
```

当 `<base>` 是一个**在执行机仓库里只有 `origin/<name>` 远程跟踪 ref、没有本地同名分支**的名字时，git 把末位参数 DWIM 成「检出这个远程分支」，**丢掉 `-b` 指定的名字**，并且**退出码为 0**：

```
$ git worktree add -b feat/b72-birth-registry <path> worktree-b69-b70-proc-footprint
Preparing worktree (new branch 'worktree-b69-b70-proc-footprint')
branch 'worktree-b69-b70-proc-footprint' set up to track 'origin/worktree-b69-b70-proc-footprint'.
$ echo $?
0
```

这不是推断，是 08-13 在克隆仓库上稳定复现的结果。

**为什么这个前提在远程派发里是常态**：审核者 push 自己的分支后，devbox 侧 `ResolveBaseline` 跑 `git fetch --all --prune` 拿到 `origin/<branch>`，但 fetch **永远不会**建出本地同名分支。所以「base 是分支名」+「远程派发」这个组合天然满足触发条件。

### 1.3 唯一的告警信号到不了人

devbox 的 agentd.log 逐字印证了整个过程：

```
20:53:31.319  git 调用完成  args=[worktree add -b feat/b72-birth-registry … worktree-b69-b70-proc-footprint]   （成功）
20:53:31.325  git 调用失败  args=[rev-parse refs/heads/feat/b72-birth-registry]
              stderr="fatal: ambiguous argument 'refs/heads/feat/b72-birth-registry': unknown revision…"
20:53:31.325  WARN  新建分支后取尖端失败，补偿将保留该分支  branch=feat/b72-birth-registry
```

`recordNewBranchTip` 立刻就发现分支不存在了，但它的处置是**打一条 WARN 然后继续**。这条 WARN 只进 agentd.log，审核者一侧的应答、事件流、回显里一个字都没有。于是错误一路静默到收工 `pull` 才炸。

### 1.4 三条工作树路径的表现不一致

| 路径 | 命令 | base 只有 `origin/<name>` 时 |
|---|---|---|
| `--new-worktree` | `worktree add -b b <dir> <base>` | **静默开在 base 名字的分支上，退出码 0** |
| 原地 | `checkout -b b <base>` | 硬失败：`fatal: '<base>' is not a commit and a branch 'b' cannot be created from it` |
| 用户树 | `checkout -b b <base>` | 同上，硬失败 |

静默改写只发生在 `--new-worktree` 一条路上。另两条不会走偏，但错得难懂——一条本该成立的派发被一句 git 内部措辞拒掉。

### 1.5 影响面

devbox 上 4 条用了非 sha base 的任务里，**2 条的任务分支实际不存在**：

| 任务分支 | base | 分支是否存在 |
|---|---|---|
| `feat/b72-birth-registry` | `worktree-b69-b70-proc-footprint` | **缺失**（本条根因，已确认） |
| `feat/message-binary-encoding` | `claude/low-cost-performance-f97432` | **缺失**（提交去向未查，见 §6） |
| `feat/b74-no-false-completion` | `main` | 存在（base 有本地同名分支，不触发） |
| `feat/b74-no-false-completion-v2` | `origin/main` | 存在 |

不是孤例。

### 1.6 顺带坐实的第二个缺陷：`BaseCommit` 存的不是 sha

[manager.go:656](../../../internal/agentd/manager.go) 把 `BaseCommit` 赋成未经解析的 `start`——显式 `--base` 时那就是分支名原文。而 `proto.Task.BaseCommit` 的注释写的是「40 位 sha」，[cmd/dispatch.go:218](../../../cmd/dispatch.go) 又用 `shortSHA()` 打印它。

于是当时 stderr 打出的是 `基线 worktre`——分支名被按短 sha 截成 7 字符。审核者盯着那行也看不出分支错了。

---

## 2. 目标与非目标

### 目标

1. `--new-worktree` 路径不再可能开出与请求不符的分支
2. 「要的分支 ≠ 实到分支」这件事本身必须能被发现并拒发，而不是只留一条 WARN
3. `proto.Task.BaseCommit` 的实现与其注释契约对齐（恒为 40 位 sha 或空）
4. 派发当场就能看出分支与起点对不对，不用等到收工 `pull`
5. 原地/用户树路径上「base 只有 `origin/<name>`」从难懂的失败变成正常工作

### 非目标（本条明确不做）

- **不改 `--base` 的语义**：仍然接受分支名、tag、sha，用户侧用法零变化
- **不追溯修复历史受损任务**。B72 那 11 个提交没丢（在 `worktree-b69-b70-proc-footprint` 上且已合入 main）；`feat/message-binary-encoding` 另记 backlog（§6）
- **不动 `ResolveBaseline` 的 fetch 与领先计数逻辑**（B35 的机制照旧）
- **不给 `worktree add` 加 `--no-guess-remote`**。它同样能挡住（已实测），但那是针对 git 某一个具体 DWIM 行为打的补丁；传 sha 是让 DWIM 从原理上无从发生

---

## 3. 设计

### 3.1 源头：起点决议时解析成 sha

在 [manager.go 的「基线起点已确定」](../../../internal/agentd/manager.go) 那段，`start` 定下来之后、送进 `PrepareWorkspace` 之前，统一解析一次：

```
git -C <repo> rev-parse --verify --quiet <start>^{commit}
```

之后 `WorkspaceReq.Base` 与 `proto.Task.BaseCommit` **都用解析出的 sha**，一次解析服务两处，不存在两者分叉的可能。

关键事实（已实测）：`rev-parse` 对「只有 `origin/<name>` 的分支简写」**能解析**，返回远程尖端。所以这个改动不损失任何易用性——`--base <分支名>` 依然是「从那条分支的尖端开分支」，只是新分支终于叫用户要的名字。

`^{commit}` 的剥离是必需的：base 可能是 annotated tag，那种情况下裸 `rev-parse` 给的是 tag 对象而非 commit。

**解析失败的处置**：当场拒发，按 `ErrBadWorkspaceReq` 走 400。错误文本点名 base 原文，并给出出路（在本地 `git push`，或换一个起点）。这比现状好：同样的输入现在在 `checkout -b` 路径上得到的是 `is not a commit and a branch cannot be created from it`，没人看得懂那说的是「你的 base 没推上去」。

`ResolveBaseline` 返回的 `baseline.Start` 本来就是 sha，再解析一次幂等无害，不需要为它开分支处理。

### 3.2 守卫：要的分支 ≠ 实到分支就拒发

`PrepareWorkspace` 的三条路径各自建完工作区后，统一核对一次：

```
git -C <workDir> branch --show-current   ==   <决议出的 branch>
```

不等就是事故：删掉刚建的 managed worktree（非 managed 路径切回 `PrevRef`），返回错误，文本**同时点名「要的分支」与「实到的分支」**。

**为什么这道守卫独立于 §3.1 存在**，而不是「已经传 sha 了就够了」：

B76 的教训本身就是 agentd 信任了 git 的退出码。git 报 0、干了另一件事，这种可能性不会因为这次传了 sha 就消失——DWIM 只是它的一种形态。只要没有任何一处比对「要的」和「拿到的」，同类事故就会以别的形态再栽一次。守卫是这个结构性空白的填补，§3.1 是这次具体洞的封堵，两者不互相替代。

**与 `recordNewBranchTip` 的关系**：守卫排在它之前。B76 现场里 `recordNewBranchTip` 其实第一时间就发现了异常，只是它的职责是「记尖端」，发现不了就保守降级（返回空串 → 补偿路径保留分支）——那个降级对它自己的职责是对的，不该被改成硬失败。分支身份的判定交给守卫，各司其职。

### 3.3 契约与回显

**`proto.Task.BaseCommit`**：由 §3.1 保证恒为 40 位 sha（切已存在分支时为空）。注释无需改动——改的是实现，让它终于符合早已写明的契约。

**dispatch 回显**（[cmd/dispatch.go](../../../cmd/dispatch.go)）：

```
分支 feat/b72-birth-registry，起点 e911147（worktree-b69-b70-proc-footprint）
```

- 分支名取自应答里的 `task.branch`
- 起点是解析后 sha 的短号
- 括号内是用户输入的 `--base` 原文，**仅在用户给了非 sha 的 `--base` 时出现**；没给或本来就是 sha 时不打括号
- 「领先 N 个提交，新分支不含它们」的既有提示照旧追加在后面

三个信息在同一行互相印证：分支名对不对、起点是不是我要的那个位置、我输的 base 被理解成了什么。任何一项不符当场就能看见。仍然是一行，不增噪音。

---

## 4. 验证策略

### 4.1 fixture 必须是克隆出来的仓库

这是本条测试设计的要害。现有 `initTestRepo` 造的是本地 `git init` 仓库，base 分支总是本地存在——**恰好绕开触发条件**。这正是这个 bug 一直没被任何测试抓到的原因。

新增 fixture：建上游仓库 → `git clone` → 在克隆出的仓库里，base 只以 `origin/<name>` 形式存在。

### 4.2 单测（真 git 仓库集成，不 mock git）

| 用例 | 断言 |
|---|---|
| `--new-worktree` + `--new-branch X` + base 只有 `origin/<b>` | 工作树当前分支 == `X`（B76 原案） |
| 原地 + 同上 | 建分支成功，当前分支 == `X`（原为硬失败） |
| 用户树 + 同上 | 同上 |
| base 解析不出来 | 按 `ErrBadWorkspaceReq` 拒发，错误文本含 base 原文 |
| 守卫：要的分支 ≠ 实到分支 | 拒发，且刚建的工作树已被清理，错误文本同时含两个分支名 |
| base 是 annotated tag | 解析到 commit 而非 tag 对象 |
| `BaseCommit` 落库形态 | 显式 `--base <分支名>` 派发后，任务记录里是 40 位 sha |

### 4.3 变异检验

摘掉 §3.1 的 sha 解析后，`--new-worktree` 那条用例**必须翻红**。不翻红说明 fixture 没造出触发条件（多半又退化成本地分支存在），测试没咬住根因。

守卫的用例同理：摘掉守卫后必须翻红。

### 4.4 真机烟测（devbox）

在 devbox 上重放 B76 原案：push 一条本地分支，`--target devbox --new-worktree --new-branch <X> --base <该分支名>` 派发一个空任务，确认：

1. dispatch 回显打出 `分支 X，起点 <sha>（<分支名>）`
2. devbox 上 `refs/heads/X` 确实存在，且指向该 base 的尖端
3. 任务工作树的当前分支是 `X`
4. `handoff pull` 能拉到 `X`

### 4.5 日志与注释自检（`instrumenting-code`）

- sha 解析：解析前后各一条 Info（base 原文 → sha），解析失败 Error 带 git stderr 原文
- 守卫：核对通过打 Info（分支名），不符打 Error 并带上「要的/实到的」两个值
- 新增/改动的导出方法补参数与返回说明；解析与守卫两处都要用中文注释写清「为什么」——尤其守卫，它的存在理由不在代码表面

---

## 5. 影响与兼容

- **用户侧用法零变化**：`--base` 接受的输入形态不变
- **行为变化有三处**，都是从错到对：`--new-worktree` 不再开错分支；原地/用户树从硬失败转为成功；base 不存在时的报错从 git 内部措辞变成可操作的提示
- **历史任务记录不迁移**：老记录里 `BaseCommit` 存的分支名原样保留。它们是终态任务，回填没有意义，且会让「这条记录当时到底发生了什么」变得不可考

---

## 6. 已知残留

`feat/message-binary-encoding` 任务（base `claude/low-cost-performance-f97432`）的分支在 devbox 上同样缺失，**其提交去向未经排查**。本条不处理，另记 backlog 条目跟踪。
