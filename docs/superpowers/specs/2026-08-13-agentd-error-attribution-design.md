# B84：agentd 错误面归因整治（并入 B81 / B82）

> 状态：设计完成，待实现
> 来源：08-13 B76 真机烟测的两条旁证。原登记为 B81（成功路径上的假 ERROR）与 B82（失败路径上的假报文），08-13 brainstorm 判定为同一病的两种表现，合并为本条
> 前置：B69/B70（reclaim 与工作树状态词汇）、B42（CLI 错误体透传上限）、P0-3（RunCmd 进程组回收）

## 1. 问题

两条原始条目指向同一件事：**agentd 的错误面不可信**。一条是成功路径上躺着 ERROR（假阳性），一条是失败路径上报错指向了错误的原因（假线索）。落点都在 [internal/agentd/workspace.go](../../../internal/agentd/workspace.go)，且都只在排障时才咬人——功能全对，人被带偏。

### 1.1 预期内的探测未命中被打成 ERROR（原 B81）

`--base <分支名>` 的解析路径第一步是 `git rev-parse --verify --quiet <base>^{commit}`，用来判断有没有本地同名分支。远程派发里**没有**本地同名分支是常态（执行机只 `fetch` 出 `origin/<name>`，从不建本地分支），所以这一步 exit=1 是预期内的未命中，随后 `for-each-ref refs/remotes/*/<name>` 命中、整条路径成功。

但这次未命中经 [`gitRun`](../../../internal/agentd/workspace.go) 的统一错误通道打成：

```
level=ERROR msg="git 调用失败" args="[rev-parse --verify --quiet b76-smoke-base^{commit}]" cause="exit status 1"
```

成功路径上躺着一条 ERROR，与真正的失败在日志里无法区分。按 `level=ERROR` 过滤会捞出正常流程，真出事时信噪比更差。

**普查结论**：`internal/agentd` 共 37 处 `gitRun` 调用，其中 9 处是明确的探测（退出码本身就是判据、且有后续兜底路径）：

| 位置 | 调用 | 未命中为何是常态 |
|---|---|---|
| [workspace.go:252](../../../internal/agentd/workspace.go) | `rev-parse --verify --quiet refs/heads/<b>` | 判分支存在性，退出码即判据 |
| [workspace.go:444](../../../internal/agentd/workspace.go) | `rev-parse --git-dir` | 判仓库可用性 → `ErrRepoUnusable` |
| [workspace.go:591](../../../internal/agentd/workspace.go) | `symbolic-ref --short -q HEAD` | detached HEAD 是正常态，落 596 兜底 |
| [workspace.go:713](../../../internal/agentd/workspace.go) | `rev-parse --verify --quiet <rev>^{commit}` | 本条现场，落 722 兜底 |
| [workspace.go:722](../../../internal/agentd/workspace.go) | `for-each-ref refs/remotes/*/<rev>` | 错误已被 `_, _, _` 整个丢弃 |
| [workspace.go:741](../../../internal/agentd/workspace.go) | `rev-parse --verify --quiet <cand>^{commit}` | 候选 ref 试探 |
| [workspace.go:843](../../../internal/agentd/workspace.go) | `cat-file -e <sha>^{commit}` | 注释已自认「命中才不 fetch 是刻意设计」 |
| [workspace.go:910](../../../internal/agentd/workspace.go) | `symbolic-ref --short refs/remotes/origin/HEAD` | 仓库没设 `origin/HEAD` 是常态 |
| [workspace.go:914](../../../internal/agentd/workspace.go) | `rev-parse --verify --quiet main` / `master` | 循环试候选，未命中是常态 |

普查牵出两条比原始记录更严重的事实：

**（a）最密的噪音源不是本条现场，是 [`resolveBaseBranch`](../../../internal/agentd/workspace.go)。** 仓库若只有 `master` 且未设 `origin/HEAD`，每次不带 `--base` 的 `handoff diff` 打 2 条 ERROR；两者都没有则 3 条。而现场那条一次派发只打一条。

**（b）[reclaim.go:191](../../../internal/agentd/reclaim.go) 已经是双日志。** `gitRun` 打一条 ERROR（噪音），紧接着 `classifyWorktree` 打一条 `Warn "工作树判定：读不到 status，判不出"`（有信息的那条）。上层已经把归因做对了，是底层多嘴——这说明修法应落在 `gitRun` 的边界上让调用方显式表态，而不是让 25 个真失败调用点陪着改。

### 1.2 工作树已回收时 `run` 报「/bin/sh 不存在」（原 B82）

08-13 mac-02 的实况日志：任务 `546dc4c3` 在 12:22:01 被 `stop`，agentd 打 `stop managed worktree 已清理 … worktree_removed=true`；25 秒后同一任务收到 `run`，随即：

```
level=ERROR msg="run 命令启动失败" cause="fork/exec /bin/sh: no such file or directory"
```

原推断是「Go 的 os/exec 在 `Dir` 不存在时把 ENOENT 归到可执行文件头上」。**08-13 复现证明结论对、机制不对**，而且缺的那一环是我们自己的代码触发的。

复现程序（Go 1.26.1 / darwin-arm64）：造临时目录 → 确认命令可跑 → 删除目录 → 分别以「不设 `SysProcAttr`」和「设 `SysProcAttr{Setpgid:true}`」再执行：

```
[存在]            err=<nil>  out="/var/.../b82-worktree-266313663"
[已删]            Start err=chdir /var/.../b82-worktree-266313663: no such file or directory
[已删+进程组]      Start err=fork/exec /bin/sh: no such file or directory      ← 与线上日志逐字一致
[对照·真缺二进制]  Start err=fork/exec /var/.../no-such-binary: no such file or directory
```

差别的根因在 Go 标准库 `os/exec_posix.go` 的 `startProcess`：

```go
// If there is no SysProcAttr (ie. no Chroot or changed
// UID/GID), double-check existence of the directory we want
// to chdir into. We can make the error clearer this way.
if attr != nil && attr.Sys == nil && attr.Dir != "" {
```

`attr.Sys == nil` 是这段好意的前提。而 `RunCmd` 调了 [`setProcGroup`](../../../internal/agentd/workspace_procgroup_unix.go)（P0-3 的进程组回收所必需），它设了 `SysProcAttr{Setpgid: true}`，**于是这次预检被跳过**，chdir 的 ENOENT 落进 `forkAndExecInChild`，被归到 argv[0] 头上。

完整真因链：`stop` 删除 managed worktree → `cmd.Dir` 悬空 → 进程组回收要求 `SysProcAttr` → Go 的友好归因被关掉 → 报文指向 `/bin/sh`。

两条副产品：

- `fork/exec /bin/sh` 与真缺可执行文件的报文**形态完全相同**（都是 `*fs.PathError`，只是路径不同），事后从 error 里挽救不回来。预检必须在 `Start()` 之前做。
- 工作区存在性检查在 [`taskRepoOrErr`](../../../internal/agentd/server.go) 里同样缺失——它只校验 `workdir != ""`。该函数被 `run` / `diff` / `file` 共用，因此这个盲区不止影响 `run`。

### 1.3 状态机灰区：本 spec 的判断

原 B82 提出「终态任务的 managed worktree 已回收，此时的 `run` 是否该在入口就拒绝」，并指出 `handoff` 状态机对 `failed` 只列了只读取证（`show`/`diff`/`pull`），`run` 属未表态的灰区。

**本 spec 判定：不按任务状态设门，按工作区存在性设门。** 终态但工作树仍在的情况合法且常见——`reclaim` 明确保留 `dirty` 工作树正是为了让人事后进去翻（[reclaim.go:203](../../../internal/agentd/reclaim.go) 「脏，默认拒绝回收」）。按终态拦 `run` 会恰好掐掉最需要现场勘查的那个场景。真正的前置条件是工作区还在不在，它跨状态成立，状态机因此不需要改。

## 2. 目标与非目标

### 目标

- 探测性 git 调用与真失败在日志里可区分：`level=ERROR` 只对应真故障。
- `run` / `diff` / `file` 在工作树已回收时给出指向真因的拒绝，而不是走到 exec 才炸出风马牛不相及的报错。
- 上述两条各自带回归测试，且回归测试不能被无意改写成假通过。

### 非目标

- 不改 CLI。[`httpError`](../../../internal/client/client.go) 已把服务端报文原样透传（上限 4096，B42 时特意抬上去的量级），够用。
- 不动 [reclaim.go:300](../../../internal/agentd/reclaim.go)（`worktree remove` 失败退回 `prune`）。其注释写明「remove 成功是常路，本分支是保险」，只防旧版 git 行为不同，失败罕见且 stderr 原文有排障价值，保留 ERROR。
- 不动 `diff` / `file` 各自的 git 报错文案。入口门禁已拦住「工作树已回收」这个主场景，其余 git 报错是另一类问题。
- 不给状态机补 `run` 对 `failed` 的表态（理由见 §1.3）。
- 不消除 TOCTOU 残留窗口（见 §6）。

## 3. 设计

### 3.1 组件 A：`gitProbe`

在 [workspace.go](../../../internal/agentd/workspace.go) 中紧邻 `gitRun` 新增：

```go
// gitProbe 执行一次探测性 git 调用：非零退出是预期内的未命中，不是故障。
func gitProbe(ctx context.Context, repo string, args ...string) (stdout, stderr string, ok bool)
```

**返回 `ok` 而不是 `err`**：调用点从「拿 err 当判据」变成「拿 ok 当判据」，探测语义在调用处自解释，不必读被调函数才知道这次失败是预期的。

**保留 `stderr`**：11 处改造点里有 3 处真在用它——[444](../../../internal/agentd/workspace.go) 包进 `ErrRepoUnusable` 的提示、[617](../../../internal/agentd/workspace.go) 包进返回的 error、[reclaim.191](../../../internal/agentd/reclaim.go) 当 `note` 交给上层。

**共用执行体**：`gitRun` 与 `gitProbe` 必须复用同一个内部执行函数（命令构造、双缓冲、耗时统计、`quotaNote` 归因）。两者唯一的差别是失败时走 `Debug` 还是 `Error`。抄成两份实现的话，超时或缓冲策略下次只会被改一处。

**必须保留的例外**：`quotaNote(err)` 非空时（fork 失败、进程配额耗尽），`gitProbe` **照样打 `Error`** 并返回 `ok=false`。fork 不出进程不是「探测未命中」，是这台机器真的没资源了；它恰好经由同一个错误返回值到达，但性质完全不同。把 `gitProbe` 的失败一律降 Debug，等于在另一个方向上重新造出本 spec 要消灭的问题。

### 3.2 改造范围：11 处

§1.1 表中的 9 处，加上两处有据可依的边界点：

| 位置 | 转 `gitProbe` 的理由 |
|---|---|
| [reclaim.go:191](../../../internal/agentd/reclaim.go) | 现状双日志：底层 ERROR 是噪音，上层 `Warn "读不到 status，判不出"` 才是有信息的那条。工作树可能已被回收（`prunable`），读不到 status 是设计好的分支，落 `WorktreeUnknown` |
| [workspace.go:617](../../../internal/agentd/workspace.go) `branchTip` | 其注释明写「失败不再塌缩成空串，**也不在此处打日志**——两件事都交给调用方」，而底下的 `gitRun` 照打 ERROR，**现状与它自己声明的契约直接矛盾**。调用方 `recordNewBranchTip` 取不到即返回空串当正常 |

其余约 25 处（`checkout` / `clone` / `fetch` / `branch -D` / `worktree add` / `worktree remove` / `diff` / `log` / `status` 判 dirty 等）保持 `gitRun` 不变。

### 3.3 组件 B：工作区存在性门禁（两道）

新增包级哨兵 `ErrWorkspaceGone`，供两道共用与测试断言。

**入口道** —— [`taskRepoOrErr`](../../../internal/agentd/server.go) 在 `workdir != ""` 分支之后加一次 `os.Stat`：

| 情况 | 响应 |
|---|---|
| 目录不存在 | **409** +「任务工作树已回收，无法执行该操作（工作区 `<path>` 已不存在）」 |
| Stat 报其他错（权限等） | 500，不冒充「已回收」 |
| 存在但不是目录 | 500，同上——这是环境异常，不是回收 |

为什么是 409 不是 404：同一函数上一分支已用 404 表达「任务不存在」，两者处置完全不同（一个是 ID 敲错，一个是任务还在、去 `reclaim` 或重新派发），报文必须能分开。409 也对得上 [`writeManagerError`](../../../internal/agentd/server.go) 既有的「`ErrBadTransit` → 409 状态不允许」约定。

这道一改，`run` / `diff` / `file` 三个动词一并覆盖——它们共用这个入口。

**纵深道** —— `RunCmd` 在 `cmd.Start()` 之前自己再 `os.Stat(repo)` 一次，不存在则返回包装了 `ErrWorkspaceGone` 的错误，日志 Error 带真因。

这段代码的注释**必须写明它为什么不是多余的**：`setProcGroup` 设了 `SysProcAttr`，Go 的 `os.startProcess` 因此跳过自带的 chdir 预检（引 §1.2 的标准库片段），这里补的正是 Go 放弃的那一步。缺了这段注释，下一个人读到「上面已经 Stat 过了」就会删掉它，而删掉之后 bug 原样复活、且再没人记得为什么。

两道并存的第二个理由：`RunCmd` 是导出函数，不应依赖调用方先检才不说假话。

## 4. 验收判据

总判据：**按 `level=ERROR` 过滤 agentd 日志，出现的每一条都对应一次真故障；每条故障报文指向真因。** 拆成五条可测：

| # | 判据 | 防的是 |
|---|---|---|
| 1 | 一次成功的 `--base <仅远程存在的分支>` 派发，全程 ERROR 计数 = 0 | §1.1 本体 |
| 2 | 一次必然失败的真调用（如 `checkout` 不存在的 ref），ERROR 计数 = 1 | **过度降级**——把噪音连同信号一起静音 |
| 3 | `gitProbe` 遇进程配额失败仍打 ERROR | §3.1 的例外，不能只活在注释里 |
| 4 | 工作树被删后调 `RunCmd`，报文含工作区路径、**不含** `/bin/sh` | §1.2 本体 |
| 5 | 工作树被删后请求 `run` / `diff` / `file`，均返回 409 且报文一致 | §3.3 入口道 |

判据 2 是刻意加的对照。§1.1 单看只会驱动人往「少打点 ERROR」使劲，而这件事做过头的失败形态极隐蔽：日志变干净了，看起来像成功了，直到真出事时发现什么都没记。降级与保留必须在同一组测试里对着测。

## 5. 测试设计

**日志断言的形态**：照搬 [`internal/executor/opencode`](../../../internal/executor/opencode) 里 `captureLog` 的形态，在 `internal/agentd` 建等价 helper。`log()` 取的是 `slog.Default()`，测试需临时替换全局 default 并在结束恢复——因此这组测试**不能 `t.Parallel()`**。[hub_test.go](../../../internal/agentd/hub_test.go) 的 `TestMain` 已把默认 logger 设为 `io.Discard` 且 level = `LevelDebug`，意味着降级后的 Debug 日志在测试里确实产生、捕获得到，判据 1 的「ERROR 计数 = 0」才有区分力（而不是因为整个 handler 静音而恒成立）。

**判据 4 的陷阱（必须写进实现约束）**：该测试**必须保留 `setProcGroup`**。这个 bug 存在的前提正是 `SysProcAttr` 关掉了 Go 的友好归因；若将来有人在测试里图省事不设进程组，Go 会自动给出清楚的 `chdir ...` 报文，**测试照样通过，而线上照样报 `/bin/sh`**。这是该回归测试唯一的失效方式，因此它不属于实现细节，属于设计约束。

**判据 3 的可测性**：取决于 `prochost` / `checkProcHeadroom` 是否有可注入点。若注入成本过高，退而求其次——实现上保留显式分支并加注释，在 plan 阶段明确记为「未覆盖」，不假装测过。

## 6. 残留风险

入口 Stat 与 `exec.Start()` 之间仍有 TOCTOU 窗口：`reclaim` 恰在这几毫秒内删掉目录的话，仍会报 `fork/exec /bin/sh`。纵深预检把窗口压到极小但不可能归零。要归零需让 `run` 与 `reclaim` 互斥加锁，成本远超收益，本 spec 不做，也不把它写成已解决。

## 7. 影响面

| 文件 | 改动 |
|---|---|
| [internal/agentd/workspace.go](../../../internal/agentd/workspace.go) | 新增 `gitProbe` 与共用执行体；10 处探测点改调（§1.1 的 9 处 + `branchTip` 617）；`RunCmd` 加 Dir 预检；新增 `ErrWorkspaceGone` |
| [internal/agentd/reclaim.go](../../../internal/agentd/reclaim.go) | 1 处（191）改调 `gitProbe` |
| [internal/agentd/server.go](../../../internal/agentd/server.go) | `taskRepoOrErr` 加工作区存在性门禁与 409 分支 |
| 测试 | 新增日志断言 helper；判据 1–5 的用例 |

CLI、proto、store 均不改。
