# 协调者审阅回路的契约小修批（B65 / B66 / B81 / B125）设计

> 状态：已定案，待实现。上游 backlog：**B65**、**B66**、**B81（main 线那条）**、**B125**。
> 附带一条**只复验不实现**的条目：**B60**（见 §1.1）。
>
> **基线分支：`handoff/web-console`，提交 `8b1203abd`（08-18）。**
> 本 spec 引用的**全部行号以该提交为准**，实现分支必须从它切出。
> 不要基于 `main`——`main` 落后该线 600+ 提交，本 spec 点名的多个文件在 `main` 上
> 要么行号完全不同，要么根本不存在（如 `manualworktree.go`）。

## 1. 先纠正的前提

写在最前面。下面每条设计都建立在这些结论上，读的人先看到结论比看到推导重要。

### 1.1 B60 已经修好了，本批**不实现它**，只复验

B60 记的是「skill 文档说 `approver_decision` 不唤醒 `wait`，但任务 `7ec762e7` 的
`wait`（`from_seq=2661`）确实被 `seq=2683 type=approver_decision` 唤醒并退出」，
并留了个待判：是**文档过时**还是**实现有出入**。

这个判已经做完了，只是没人回头改 backlog 行。[internal/client/client.go:121](internal/client/client.go:121)：

```go
func isDeliverable(t proto.EventType) bool {
	switch t {
	case proto.EventTypeProgress,
		proto.EventTypeApproverDecision,
		proto.EventTypeApproverDisabled,
		proto.EventTypeTicketsVoided:
		return false
	}
	return true
}
```

函数注释直接描述了 B60 的现场：这几类在服务端「只入库不 Publish」，实时流本就见不到，
**只有 WS 重放（读 store 的 `EventsFromAsc`）会把它们推来**——而 B60 观测到的正是
带 `from_seq` 的重放路径。注释末句写着「handoff skill 早已写明这三类不唤醒 wait，
这里是让代码追上契约」。引入提交 `1da5dd357`（08-11，`fix(client): 统一可交付口径，
审计类事件不再在重放路径唤醒审核者`）与 B60 的观测同日。

**结论**：判定是「实现有出入」，且已按「让代码追上文档」的方向修掉。
B60 在本批里是一条**验收动作**，不是一条实现任务：跑一次复验，通过则直接回填
`✅ done`。复验步骤见 §6.5，**归属审核者、不派发**（理由见 §7）。

### 1.2 B81 的作用域只有 `internal/agentd` 的 `gitRun`，不含 `internal/localsync`

`internal/localsync` 有它自己的 git 包装 [localsync.go:113](internal/localsync/localsync.go:113)：

```go
func run(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// ...
	return out.String(), errb.String(), err
}
```

它**根本不打日志**，所以 B81 描述的噪音在那边不存在。`localsync.go:77` 与 `:80` 那两处
探测型调用看着同形，但不构成本条的修复面。**不要顺手把 localsync 也改了**——那会把
一个「静默」问题换成一个「有日志」问题，是另一件事，且没人报过。

### 1.3 B66 只做 CLI 侧转义，**明确不做** argv 上送

B66 行里列了两条候选修法。本 spec 取第一条（CLI 侧逐 arg 转义），第二条
（把 argv 数组整体上送、由 agentd 决定是否包 `sh -c`）**明确不做**，理由：

那是**线格式变更**。`POST /api/tasks/{id}/run` 的请求体要从 `{cmdline: string}` 变成
`{argv: []string}`，牵动 `internal/proto`、`internal/client`、`handleTaskRun`
三处，而且**老 agentd 收不到 `argv`**——协调者升级了、执行机没升级时，`run` 会静默
退化成空命令。跨版本兼容要单独设计（参考 B88 那条「配了新配置键的机器跨版本回滚会
被砖掉」的教训），不该混进一批小修里。

如果将来真要做 argv 方案，它是一条独立的 backlog 条目，不是本批的「后续步骤」。

### 1.4 B65 的默认值一改，控制台会当场撒谎——所以它不是「一处分支」

B65 行里写的修法是「缺省优先用 `task.base_commit`」，读起来像改 `handleTaskDiff`
一处。**不是。** 同一个任务的基准还有第二个出口：
[handleTaskBranches](internal/agentd/server.go:1406) 返回
`{"branches": [...], "default": resolveBaseBranch(repo)}`，前端
[ReviewSidePanel.tsx:89](web/src/app/task/ReviewSidePanel.tsx:89) 拿它渲染成

```tsx
<option value="">自动推导{branches?.default ? `（${branches.default}）` : ''}</option>
```

只改 `handleTaskDiff` 的话，控制台会显示「自动推导（main）」而 diff 实际用的是
`base_commit`——**一个当场可见的谎**。所以 B65 的范围必然包含这两个端点加一处前端，
见 §4.3。

## 2. 要解决的问题

四条都发生在**协调者审阅回路**上，而且三条是**静默**的——不报错，只是给出错误的东西：

| 条目 | 协调者拿到的东西 | 实际应该是 |
|---|---|---|
| B65 | `diff` 默认吐 26611 行（含 `main` 与特性分支之间的全部历史） | 3274 行，只有本任务的改动 |
| B66 | `handoff run T1 grep -rn 'foo bar' .` 在远端跑成了另一个命令 | 敲什么跑什么 |
| B81 | 成功路径的日志里躺着 `level=ERROR git 调用失败` | 成功路径不该有 ERROR |
| B125 | `go test ./...` 偶发翻红，分不清是新改动挂了还是 flake | 门禁可信 |

审阅是「读了就信」的场景。素材被淹、命令跑歪、日志给错信号，三者都会让审核者
**在错误的证据上做正确的推理**，这比直接报错危险。

### 为什么合成一批

- 四条全是单点、判据明确、互不耦合的小修。各自单独派发的话，探活 + push + 派发 +
  盯全程 + 审核 diff 的开销比修复本身还大。
- B65 / B66 / B81 共用一次真机走查：派一个小任务，依次 `diff`、`run`、看日志，
  三条的真机判据一次取齐。
- **B125 放进来的理由不同**：它卡在同一道门禁上。不先修它，本批的
  `go test ./...` 回归判据本身会偶发翻红，审核者分不清是谁挂的。所以它必须**第一个做**。

## 3. 实现顺序

严格按此顺序，每条一个 task、一个 commit：

```
B125（把门禁修稳）
  → B81（独立，只改日志通道，不改行为）
    → B65（改默认值，含端点 + 前端）
      → B66（触及 run 链路，风险最高，放最后）
```

B125 在前是**硬依赖**：后三条的验收都要跑 `go test ./...`。
B81 在 B65 前是**弱依赖**：`resolveBaseBranch` 的兜底链本身就含一处 B81 要改的探测调用
（[workspace.go:1034](internal/agentd/workspace.go:1034)），先改完噪音再动 B65 的逻辑，
diff 的日志读起来是干净的。

## 4. 设计

### 4.1 B125：`TestWSTruncationWarnsOnRealGap` 的固定期限换成随负载放宽

**现象**：`go test ./...` 第一遍报

```
ws_regression_round2_test.go:229 等待 seq=21 时读失败: failed to get reader: context deadline exceeded
```

耗时 10.01s，正好撞满 [ws_regression_round2_test.go:224](internal/agentd/ws_regression_round2_test.go:224)
的 `10*time.Second`。单独 `-run TestWSTruncationWarnsOnRealGap -count=1` 连跑 3 次全过，
全量第二遍也全绿。与 B105 无关（B105 没碰 WS 路径）。

**这个用例已经被治过一半**。它下面等告警日志那段（`:234`）写了显式轮询，注释还说明了
「服务端在写出事件**之后**才跑截断诊断并打日志……负载下尤其明显」。作者知道这用例对
负载敏感，但只治了**日志那半**；上面 `waitEventSeq` 等实时事件那半仍是硬 10s，
而翻红正出在那一半。

**它等的到底是什么**（backlog 行明确要求先弄清这个，不许直接调大数字）：
`dialWS(t, taskID, 0)` 建连 → 服务端重放最旧 5 条（`replayLimit=5`）→ 再收一条
实时事件 `seq=21`。整包并行时 WS 建连与重放会与其他用例的 goroutine 争抢，
10s 是**建连+重放+一条实时事件**的总预算，不是单纯的网络等待。

**修法**：在 `ws_regression_round2_test.go` 引入一个包内 helper：

```go
// wsDeadline 返回 WS 用例的等待期限：基准值随 -timeout 缩放，全量并行时给足余量。
//
// 为什么不是写死的 10s：本文件的用例要等「建连 + 重放 N 条 + 一条实时事件」，
// 这个链路在整包并行下会与其他用例争 goroutine。B125 实测：单独跑 3 次全过、
// 全量第一遍撞满 10.01s。写死的数字治不了这个——它是负载函数，不是常数。
func wsDeadline(t *testing.T, base time.Duration) time.Duration
```

实现取 `go test -timeout` 的剩余额度（`t.Deadline()`）：有 deadline 时取
`min(base * 3, 剩余额度的 1/4)`，无 deadline 时取 `base * 3`。

**替换范围**：本文件四处固定期限，`:129`、`:224`、`:272`（各 10s）与 `:160`（30s）
全部改走 `wsDeadline`。**`:234`、`:284`、`:358` 那三处 3s 的轮询期限不动**——它们
等的是日志落盘，轮询已经把「诊断未触发」与「日志没写到」区分开了，不是同一类问题。

**明确的局限**：这是**缓解不是根治**。真正的根治是把 WS 用例与重负载用例隔开
（分包或 `t.Parallel()` 分组），那是更大的改动，不在本批。修完要在用例注释里
写清「这是负载缓解，若仍偶发则按分包处理」，**不要让下一个人以为这条已经封死**。

### 4.2 B81：探测型 git 调用不再走 ERROR 通道

**现象**：`--base <分支名>` 的远程派发，成功路径上打一条

```
level=ERROR msg="git 调用失败" args="[rev-parse --verify --quiet b76-smoke-base^{commit}]" cause="exit status 1"
```

**根因**：解析 base 的第一步探测「有没有本地同名分支」。远程执行机只 `fetch` 出
`origin/<name>`、从不建本地分支，所以这一步 exit=1 是**预期内的未命中**，随后
`for-each-ref refs/remotes/*/<name>` 命中、整条路径成功。但它走的是
[workspace.go:131](internal/agentd/workspace.go:131) 的 `gitRun`，非零退出一律经
`:147` 打 `Error`。

**关键是别只改一处。** 按 backlog 行的告诫做了普查，`internal/agentd` 下同形态的
探测调用共 **8 处**：

| 位置 | 调用 | 预期内失败的含义 |
|---|---|---|
| [workspace.go:325](internal/agentd/workspace.go:325) | `rev-parse --verify --quiet refs/heads/<branch>` | 分支不存在（`--new-branch` 的正常前提） |
| [workspace.go:517](internal/agentd/workspace.go:517) | `rev-parse --git-dir` | 目录不是仓库（显式判据） |
| [workspace.go:833](internal/agentd/workspace.go:833) | `rev-parse --verify --quiet <rev>^{commit}` | **B81 那条 ERROR 的出处** |
| [workspace.go:861](internal/agentd/workspace.go:861) | 同上，DWIM 候选第二轮 | 候选未命中 |
| [workspace.go:963](internal/agentd/workspace.go:963) | `cat-file -e <sha>^{commit}` | 提交不在本仓（基线校验的正常否定） |
| [workspace.go:1034](internal/agentd/workspace.go:1034) | `rev-parse --verify --quiet <main\|master>` | `resolveBaseBranch` 兜底链的正常未命中 |
| [manualworktree.go:67](internal/agentd/manualworktree.go:67) | `rev-parse --verify --quiet refs/heads/<branch>` | 分支不存在（B130 建树的正常前提） |
| [manualworktree.go:127](internal/agentd/manualworktree.go:127) | `rev-parse --verify --quiet <base>` | base 不是本地 rev |

**修法**：加一个同签名的 `gitProbe`：

```go
// gitProbe 与 gitRun 相同，但把**非零退出**当成预期内的探测结果而非故障：
// 失败记 Debug 不记 Error，返回值语义完全不变（调用方仍按 err != nil 判未命中）。
//
// 为什么需要它（B81）：探测型调用（rev-parse --verify --quiet、cat-file -e）的
// 非零退出是**正常分支**——远程执行机只 fetch 出 origin/<name>、从不建本地分支，
// 所以「本地同名分支不存在」是常态。经 gitRun 打成 ERROR 后，成功路径的日志里
// 躺着 ERROR，与真故障无法区分；按 level=ERROR 过滤日志会捞出正常路径。
//
// 边界：只用于「失败是预期内结果」的调用。会真正出事的 git 调用（clone/fetch/
// worktree add/diff）仍走 gitRun，它们的失败必须是 ERROR。
func gitProbe(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error)
```

实现上抽出 `gitRun` 的公共体，用一个 `quiet bool` 参数区分失败日志级别。
**进程配额那条分支（`:143` 的 `quotaNote`）不降级**——`EAGAIN`/`fork failed` 无论
出现在哪种调用里都是真故障（B73 的资源耗尽归因就靠它），保持 ERROR。

八处调用点全部改成 `gitProbe`。

**这不改任何行为**，只改日志级别。返回值、错误语义、调用方代码一律不动。

### 4.3 B65：`diff` 的缺省基准优先用任务自己的 `base_commit`

**现象**：W2 任务的 `handoff diff` 默认吐 26611 行，真实改动只有 3274 行。
只有显式 `--base` 才能纠正。

**根因**：[server.go:1379](internal/agentd/server.go:1379)

```go
base := r.URL.Query().Get("base")
if base == "" {
	base = resolveBaseBranch(repo)   // origin/HEAD → main → master
}
```

而任务记录里本来就有 `BaseCommit`（[proto.go:235](internal/proto/proto.go:235)）。
`resolveBaseBranch` 的文档注释写着「派发时并未记录基准分支名」——那是 **B35 加
`base_commit` 之前**的事实，现在过时了，注释也要一并更正。

**`BaseCommit` 为空是有语义的**，不是「老任务缺字段」：proto 注释写明
「空=切已存在分支」。所以退回推导链是**正当的正常分支**，不是兜底。

**`Diff` 不用改。** 它执行 `git diff <base>...HEAD` 与 `git log --oneline <base>..HEAD`
（[workspace.go:993](internal/agentd/workspace.go:993)），`<base>` 是任意 rev，
40 位 sha 直接可用。三点语义在这里与两点等价——`HEAD` 由 `BaseCommit` 派生，
两者的 merge-base 就是 `BaseCommit` 本身。`"-"` 前缀的注入防护已在 `Diff` 内，
sha 不会触发。

**三处改动**：

1. **`handleTaskDiff`**：取任务记录，`base` 缺省时优先 `task.BaseCommit`，
   为空再退 `resolveBaseBranch(repo)`。
   `taskRepoOrErr`（[server.go:1342](internal/agentd/server.go:1342)）目前只返回
   `repo` 并丢掉 `task`，且有 5 个调用点。**不改它的签名**——抽一个
   `taskOrErr(w, taskID) (proto.Task, bool)` 出来，`taskRepoOrErr` 变成它的薄包装，
   `handleTaskDiff` 与 `handleTaskBranches` 改用新的。其余三个调用点不动。

2. **`handleTaskBranches`**（[server.go:1406](internal/agentd/server.go:1406)）：
   响应增加一个字段，与 diff 的实际取值保持一致：

   ```json
   {"branches": [...], "default": "<推导出的分支名或空>", "task_base": "<BaseCommit 或空>"}
   ```

   `default` 的语义**不变**（仍是推导结果），新增 `task_base` 表示「diff 实际会用的
   任务基线」。这样前端能分辨两者，也不破坏既有消费者。

3. **前端**（[ReviewSidePanel.tsx:89](web/src/app/task/ReviewSidePanel.tsx:89)
   与 [types.ts:366](web/src/api/types.ts:366)）：「自动推导」项的括注改为

   - `task_base` 非空 → `自动推导（任务基线 <前 8 位 sha>）`
   - `task_base` 为空、`default` 非空 → `自动推导（<分支名>）`（今天的行为）
   - 两者皆空 → `自动推导`（今天的行为）

**这是行为变更**：审核者看到的默认 diff 范围会变小。方向上是纯改善，但
`handoff diff` 的 help 文案、`README.md` / `README.zh-CN.md` 的命令表、
`skills/handoff/SKILL.md` 的 diff 段都要同步改口径。

### 4.4 B66：`run` 的多参数形态逐个 shell 转义

**现象**：`handoff run T1 grep -rn 'foo bar' .` 在远端跑出来的是另一个命令，
且**静默**——不报错，只是结果不对。

**根因**：链路穿两层 shell，只有第一层的引号被消费：

1. 本地 shell 把 `'foo bar'` 剥成一个词 → argv
2. [cmd/run.go:31](cmd/run.go:31) `strings.Join(args[1:], " ")` 重拼成一个字符串，
   引号已经不在了
3. [workspace.go:1518](internal/agentd/workspace.go:1518) 的 `RunCmd` 执行
   `exec.CommandContext(ctx, sh, "-c", cmdline)` 再解析一次 → `foo` 和 `bar`
   成了两个参数

**修法必须区分两种调用形态**，否则会打破一个今天能用的用法。

`handoff run` 的 `Args` 是 `cobra.MinimumNArgs(2)`，`SetInterspersed(false)`，
所以 `args[1:]` 有两种来源：

- **单参数**：`handoff run T1 "cd sub && go test ./..."` ——用户给的**就是一条
  shell 命令原文**，本来就指望 `sh -c` 解析它。
- **多参数**：`handoff run T1 grep -rn 'foo bar' .` ——用户给的是 **argv**，
  本地 shell 已经完成了分词与去引号。

**如果对单参数也做转义，`sh -c "'cd sub && go test ./...'"` 会把整条命令当成一个
带空格的命令名，报 command not found**——把一个今天能用的用法改坏。

**定案的契约**（要写进 README 与 SKILL.md，不能只活在代码里）：

> `handoff run <task> <命令...>`
> - **只给一个参数**：按 shell 命令原文透传（`sh -c` 解析它）。
> - **给多个参数**：按 argv 处理，逐个做 POSIX 单引号转义后再拼接，
>   交给 `sh -c`。你敲的引号、空格、元字符原样到达远端。

实现：在 `cmd/run.go` 内加一个纯函数并单测它：

```go
// shellJoin 把 argv 拼成一条可安全交给 sh -c 的命令行。
//
// 单参数直接原样返回（用户给的就是 shell 命令原文）；多参数逐个做 POSIX
// 单引号转义（内嵌单引号按 '\'' 拆开）后以空格连接。
//
// 为什么需要它（B66）：命令要穿两层 shell——本地 shell 已经消费掉一层引号，
// 直接 strings.Join 重拼后远端 sh -c 会按新的词边界再解析一次，
// `grep -rn 'foo bar' .` 到远端就成了三个参数。静默失真，不报错。
func shellJoin(args []string) string
```

`cmd/run.go:31` 的 `strings.Join(args[1:], " ")` 换成 `shellJoin(args[1:])`。
服务端一行不动。

**服务端一行不动，而且天然不与 Windows 批撞车**：`RunCmd` 早先写死的 `"sh"` 已经
被 B37 的交付抽走了——现在走 [runshell.go:104](internal/agentd/runshell.go:104) 的
`runShell()`，候选列表由 `runShellCandidates(goos)` 按平台给出。所以本条只改
`cmd/run.go` 一个文件，与 B120–B124 那批的 Windows 面没有共同的修改点。

（顺带：**B82（main 线）「终态任务 run 报 /bin/sh 不存在」这条很可能也已随
`runshell.go` 失效**，清 backlog 时应先复验它是否还复现，不要直接开工。
本 spec 不处理它。）

## 5. 数据流

四条都不引入新的组件，也不改任何持久化格式。变的是三条既有路径上的一个环节：

```
handoff diff <task>
  → GET /api/tasks/{id}/diff
  → handleTaskDiff：base 缺省 = task.BaseCommit ?? resolveBaseBranch(repo)   ← 变（B65）
  → Diff(repo, base)                                                          不变

handoff run <task> <命令...>
  → shellJoin(args[1:])                                                       ← 变（B66）
  → POST /api/tasks/{id}/run  {cmdline}                                       不变（线格式不动）
  → RunCmd → sh -c cmdline                                                    不变

agentd 内任意探测型 git 调用
  → gitProbe(...)  失败记 Debug                                               ← 变（B81）
  → 返回值与错误语义                                                          不变
```

## 6. 验收判据

### 6.1 B125

- `go test ./internal/agentd -run TestWS -count=3` 全过。
- `go test ./... -count=2` **连续跑 5 次不翻红**（这是本条的核心判据，
  单次全绿不算数——原症状就是单次里的第一遍才翻）。
- 用例注释里写明「负载缓解，非根治；若仍偶发则按分包处理」。

### 6.2 B81

- **单测**：用 `slog` 的测试 handler 捕获日志，对 `gitProbe` 的未命中路径断言
  **不产生 `level=ERROR`**、产生 `level=DEBUG`；对进程配额分支断言**仍为 ERROR**。
- **单测**：断言 `gitProbe` 与 `gitRun` 在未命中时返回值逐字段相同（err、stdout、stderr）。
- **真机**（留本地，见 §7）：跑一次带 `--base <远程分支名>` 的远程派发，
  `grep 'level=ERROR' agentd.log` 在该次派发的时间窗内**为空**。

### 6.3 B65

- **单测**：造两个任务，一个 `BaseCommit` 非空且与仓库默认分支不同、一个 `BaseCommit` 为空。
  断言前者 `GET /api/tasks/{id}/diff`（不带 `base`）用的是 `BaseCommit`，
  后者退回 `resolveBaseBranch`。
- **单测**：`GET /api/tasks/{id}/branches` 的响应含 `task_base`，值等于任务的 `BaseCommit`。
- **前端单测**：`ReviewSidePanel` 在 `task_base` 非空/为空/两者皆空三种输入下，
  「自动推导」项的文案分别为三种形态。
- **真机**（留本地）：对一个 `BaseCommit` ≠ 默认分支的任务跑 `handoff diff`，
  输出行数等于本地 `git diff <base_commit>...HEAD` + `git log --oneline <base_commit>..HEAD` 的行数。

### 6.4 B66

- **单测**（表驱动，全部钉死期望字符串）：

  | 输入 argv | 期望输出 |
  |---|---|
  | `["go test ./... && go vet ./..."]`（单参数） | 原样返回 |
  | `["go","test","./..."]` | `go test ./...` |
  | `["grep","-rn","foo bar","."]` | `grep -rn 'foo bar' .` |
  | `["echo","it's"]` | `echo 'it'\''s'` |
  | `["ls","*.go"]` | `ls '*.go'` |

- **真机**（留本地）：在任务仓库里 `handoff run <task> grep -rn 'foo bar' .`
  与直接在该仓库 `grep -rn 'foo bar' .` 输出一致；
  `handoff run <task> "cd <子目录> && ls"` 仍正常（单参数形态未被打破）。

### 6.5 B60（复验，不实现）

起 agentd，配一个会自动放行的审批链，派一个小任务，`handoff wait --follow` 挂着。
审批链放行时 **`wait` 不应退出**；随后从中断处 `wait --from-seq <早于该 decision 的 seq>`
重连，重放路径同样**不应**因 `approver_decision` 唤醒。
通过 → 直接把 B60 回填 `✅ done(已验)`，验收栏写明「代码判据 `client.go:121 isDeliverable`
+ 真机复验」。不通过 → B60 留 `💡 idea` 并把新证据补进行内，**不进本批**。

## 7. 派发与验收归属

按 **B126**（写 plan 时需驱动 agentd 的验收步骤必须显式归审核者）的纪律划线：

**可派发**（写进 plan，交 mac-02 + opencode）：
- §4.1–§4.4 的全部代码改动
- §6.1–§6.4 里的**单测部分**
- README / SKILL.md / help 文案的同步更新（B65 的口径、B66 的契约）

**留本地、不写进派发的 plan**（都要起 agentd、派任务、调 `handoff` CLI，
与执行纪律块 B 版的「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」
直接冲突）：
- §6.2 / §6.3 / §6.4 的**真机走查**
- §6.5 的 **B60 复验**

这四项落在审核者的本地验收清单里，plan 中只以「本条由审核者执行，不派发」标注存在，
**不给执行者任何要跑它的指令**。

## 8. 变异靶子

实现完成后逐个注入，断言测试**翻红**：

| 靶子 | 应翻红的测试 |
|---|---|
| `gitProbe` 的失败日志改回 `Error` | B81 的日志级别断言 |
| `gitProbe` 的进程配额分支也降级到 Debug | B81 的配额仍为 ERROR 断言 |
| `handleTaskDiff` 去掉 `BaseCommit` 优先，直接推导 | B65 的 diff 基准断言 |
| `handleTaskBranches` 不返回 `task_base` | B65 的端点断言 + 前端文案断言 |
| `shellJoin` 对单参数也做转义 | B66 表里的单参数行 |
| `shellJoin` 多参数不转义、退回 `strings.Join` | B66 表里的 `foo bar` 与 `it's` 两行 |

B125 没有变异靶子——它修的是稳定性不是行为，把等待改回硬 10s 只在特定负载下翻红，
不构成可靠判据。**如实记为「本条无变异靶子」，不要编一个。**

## 9. 明确不做

- **B60 的实现**——已修，本批只复验（§1.1）。
- **argv 上送的线格式变更**——独立条目，不是本批的后续步骤（§1.3）。
- **`internal/localsync` 的 `run`**——它不打日志，没有 B81 描述的噪音（§1.2）。
- **服务端的 `run` 端点与 `RunCmd`**——B66 只改 `cmd/run.go`；shell 选择已由
  `runshell.go` 抽走，本批不碰（§4.4）。
- **B82（main 线）的复验**——它很可能已随 `runshell.go` 失效，但那是清 backlog 时
  的独立动作，不在本批（§4.4）。
- **`resolveBaseBranch` 的推导链本身**——B65 只是在它前面加一层优先级，
  推导链的 `origin/HEAD → main → master` 顺序不动。
- **WS 用例的分包隔离**——B125 的根治方案，本批只做负载缓解（§4.1）。

## 10. 上游 backlog 落账

本批完成后要动的行（**在 `handoff/web-console` 上改，不要在 `main` 上加行**——
`main` 的 B114–B119 六行至今是孤儿，`web-console` 上已经因此发生过一次改号：
`e20fedd3c docs: B114 改号为 B130（与 main 的号段冲突）`）：

| 行 | 目标状态 |
|---|---|
| B125 | `✅ done`，验收栏记 `go test ./... -count=2` 连续 5 次的结果 |
| B81（main 线那条） | `✅ done`，验收栏记单测 + 真机 ERROR 过滤为空 |
| B65 | `✅ done`，验收栏记单测 + 真机行数对照 |
| B66 | `✅ done`，验收栏记单测表 + 真机两条命令 |
| B60 | 复验通过 → `✅ done(已验)`；不通过 → 留 `💡 idea` 并补证据 |

全部条目无原型/流程图，自动免除对照。
