# 合并环节的 origin 拓扑与工作区隔离（D 组四条）

> 状态：spec（2026-08-20 讨论定案）。属 B156.1。上游：
> [蓝图](2026-08-18-workbench-blueprint-design.md)、
> [一期 spec](2026-08-18-workbench-phase1-design.md)、
> [交接文档](../ledgers/2026-08-19-b156.1-handoff.md) 的「D 组三条未定的观察」。
>
> 本 spec 处理**四**条：交接文档记的三条，加上审阅本轮实测发现的第四条
> （合并环节会自动 `git push` 基线分支，此前无人记录）。

## 1. 现状：链是断的，断点很具体

### 1.1 D1 —— 工作分支从来没上过 origin

`internal/ledgernode/wire.go` 的两个真机执行体都从 origin 取工作分支：

- `NewLocalObjective`：`git fetch origin <工作分支> <基线>` → `git worktree add "$tmp" origin/<工作分支>`
- `NewLocalMerge`：`git fetch origin <工作分支> <基线>` → `git merge --no-ff origin/<工作分支>`

而实际的分支流向是：

```
执行者在执行机上 commit          → 分支只存在于执行机的任务仓库
wait 的 sync.auto（默认开）      → 每个回合结束自动 fetch 到协调者本地
handoff pull                     → 同上，只 fetch 不 checkout 不合并
                                 → ✗ 没有任何一步把工作分支送上 origin
```

于是合并环节必然报 `couldn't find remote ref`。B156.1 真机验收时是审核者手工
`git push origin <分支>` 绕过去的。

**交接文档把根因记成「纪律块禁止执行者 push」，这条不准确。** 实测：
`internal/discipline/builtin/single-context.md` 里没有任何 push 相关条款；
真实拦截点是 `internal/permgate/blacklist.go:43`，它只自动拒**强推**
（`git\b.*\bpush\b.*(--force\b|-f\b)`）；普通 `git push` 在
`internal/executor/{claudecode,grok}/taskenv.go:51` 是 **ask（升级给协调者）**，
注释写的是「外推：收尾纪律要求不 push，出现即异常」。**执行者能推，只是每次
要人批。**这一条改变了设计空间，故记于此。

### 1.2 D2 —— 基线**分支名**那条路径不补拉

`ResolveBaseline`（`internal/agentd/workspace.go:925`）**是会 fetch 的**：基线
**提交**在任务仓库缺失时补拉 `--all --prune` 再判，还拉不到才拒发。所以
「派发解析基线时不 fetch」这个表述也要修正。

真正缺补拉的是另一条路径：`card dispatch` 传的 `Base` 是
`EffectiveBaseBranch` 返回的**分支名**（`cmd/card_dispatch.go:179`），分支名在
执行机上按本地引用解析，没有对应的补拉。执行机镜像陈旧时，
`--base-branch main` 的卡会基于旧 main 起分支；刚推上去的新分支则报「起点不存在」。

### 1.3 D3 —— 合并把协调者的工作区切走且不切回

`NewLocalMerge` 的脚本直接在 `repoDir`（协调者自己的仓库）上 `git checkout <基线>`，
合并完不切回。两个后果：

- 协调者的 checkout 被静默换成基线分支；
- 工作区有未提交改动时，`git checkout` 本来就会被 git 拒绝，**整个合并环节直接失败**——
  而「协调者正在改东西」是常态。

**同一个文件里的 `NewLocalObjective` 已经是对的**：它用
`tmp=$(mktemp -d)` + `git worktree add "$tmp"` + `trap 'git worktree remove --force "$tmp"' EXIT`，
全程不碰主工作区。`NewLocalMerge` 是没跟上的那个，不是需要发明新做法。

### 1.4 D4 —— 自动推基线分支（此前无人记录）

`NewLocalMerge` 脚本的最后一行是 `git push origin <基线>`。主线由 `isMainline`
挡着（基线为 main 或空时根本不进自动合并），所以它只在**基线是集成分支**时触发。
这是一个外部可见、不易撤回的动作，**自动执行且此前未写进任何文档**。

## 2. 裁决

| # | 裁决 | 理由 |
|---|------|------|
| 1 | **工作分支的汇合点是 origin** | 与蓝图 §3.1「多协调机对等、无 home 概念」一致：任何一台协调机都能接手合并。选「协调者本地仓」会把合并能力钉死在做过 pull 的那一台上 |
| 2 | **由合并环节自己补推送** | 合并环节本来就跑在协调者仓里，而 `wait` 的 `sync.auto` 已把工作分支自动 fetch 到本地。零新增角色：不动纪律块、不动审批面、不给执行者加每次都要批的推送工单 |
| 3 | **自动推基线保留，但显式化** | 既然 origin 是汇合点，集成分支推上去才能被其他协调机与执行机看到。保留行为，但在 spec、skill、timeline 事件三处都写明「已推 origin」，不再是隐形动作 |
| 4 | **D3 用临时 worktree（方案 A）** | 与同文件 `NewLocalObjective` 的既有做法同源；协调者工作区一根汗毛不动，脏工作区也不影响。「存-切-还原」没解决脏工作区那一半；plumbing 无 checkout 合并要自写冲突检测与提交构造，复杂度不值 |

## 3. 设计

### 3.1 合并环节重写（`NewLocalMerge`）

新脚本，与 `NewLocalObjective` 同构：

```bash
set -e
git push origin <工作分支>:<工作分支>       # ← 新增：补齐 origin
git fetch origin <工作分支> <基线>
tmp=$(mktemp -d)
git worktree add --detach "$tmp" origin/<基线>   # ← 改：脱头 + 用远端引用
trap 'git worktree remove --force "$tmp"' EXIT
cd "$tmp"
git merge --no-ff origin/<工作分支> || { git diff --name-only --diff-filter=U; git merge --abort; exit 1; }
git push origin HEAD:<基线>
```

要点：

- **必须 `--detach`，且落点用 `origin/<基线>` 而不是本地分支名。** 两个原因，
  缺一条都会踩：
  ① git 不允许同一个分支同时在两个 worktree 里被 checkout——协调者的主工作区
  恰好停在基线分支上时（合并完成后想看结果，这很常见），`worktree add <基线>`
  会直接失败；
  ② 用刚 fetch 下来的 `origin/<基线>` 作落点，顺带消灭「本地基线分支陈旧」这个
  变量——合并的起点永远是 origin 的当前状态。
  同文件的 `NewLocalObjective` 已经是 `git worktree add "$tmp" origin/<分支>`，
  本改动与它同构。
- **推送用显式 refspec**（`<分支>:<分支>` 与 `HEAD:<基线>`），不依赖当前分支或
  upstream 配置。教训：upstream 名字对不上时裸 `push` 什么都不做**且不报错**。
- **绝不 `--force`**。origin 上已有同名分支且已分叉时，push 会以非快进失败——
  这是正确行为，按 §3.3 转「等人」。
- `trap ... EXIT` 保证临时 worktree 无论成功失败都回收。
- `mktemp -d` 落在系统临时目录（**不在仓库内**）——仓库内的临时目录会破坏
  git 相关测试的前提。

**随之而来的行为变化，必须说明**：脱头合并 + `push HEAD:<基线>` 意味着
**协调者本地的基线分支引用不再被推进**，新的合并提交只落在 origin 上。这是
裁决 1 的直接推论（origin 是权威），也是它比「切分支再切回」更好的地方——本地
引用不再是一份会漂移的影子。协调者想在本地看到结果，`git fetch` 即可。
skill 与 timeline 事件都要把这条讲明白，否则会有人对着没动的本地基线以为没合上。

### 3.2 客观判据环节（`NewLocalObjective`）同步修 D1

它已经是临时 worktree 形态，只缺 D1 那一半：同样在 `git fetch` 之前补
`git push origin <工作分支>:<工作分支>`。

**两处共用一个辅助函数**，不各写一份——两段脚本的 fetch 行本来就一模一样，
分开写迟早分叉。

### 3.3 工作分支在协调者本地缺失时的处置

合并环节要推工作分支，前提是本地有它。常态下 `wait` 的 `sync.auto` 已经拉过；
用了 `--no-sync` 或换机接管时可能没有。处置阶梯，**不许含糊失败**：

1. 本地有该分支 → 直接推。
2. 本地没有 → 先试 `git fetch origin <工作分支>`（可能别的协调机已经推过）。
   拉到了就跳过推送，直接进合并。
3. 还是没有 → **转「等人」**，reason 写明：
   `工作分支 <名> 在本地与 origin 都不存在，先 handoff pull <task> 再重试`。
   不静默失败，也不猜。

### 3.4 事件与呈现

合并环节成功时落的 timeline 事件，payload 里显式带上做过的外部动作：

- `pushed_work_branch`: 工作分支名（本次是否真的推了）
- `merged_into`: 基线分支名
- `pushed_base`: 基线分支名（即 D4 那次推送）

看板与 `card show` 因此能回答「这次合并到底往 origin 推了什么」。skill 的
「账本模式」第 4 点补一句：合并环节会把工作分支与基线都推上 origin。

### 3.5 D2：基线分支名补拉

`card dispatch` 传分支名时，执行机侧在用该分支起工作分支**之前**补拉一次，
形状照抄 `ResolveBaseline` 的提交路径（`internal/agentd/workspace.go:925`）：

1. `git fetch origin <基线分支>` —— 提交路径是「本地没有才拉」，分支路径**必须
   无条件拉**：分支名在本地永远「解析得到」（那正是陈旧的那一份），拿它当
   「不用拉」的信号，等于让这个 bug 永远不触发修复路径。这是两条路径唯一
   不同的地方，且必须不同。
2. 起点取 `origin/<基线分支>`，不取本地同名分支。
3. fetch 失败或该分支在 origin 上不存在 → 拒发，错误里带 fetch 的 stderr 原文
   （与 `ErrBaseCommitMissing` 同样的「结论都是这次派不出去，先解决远程仓库」
   的归类方式）。

**不改** `ResolveBaseline` 的提交路径：它已经是对的，且它的「命中才不 fetch」
是刻意的性能设计（`cat-file` 纯本地查询，只有真落后才付网络代价）。

### 3.6 代码改名：`节点` → `环节`

文档侧已于 `0dca5ff27` 完成。代码侧留到本轮，因为本轮必然要动
`wire.go` / `node.go`：

| 现名 | 新名 |
|---|---|
| 包 `internal/ledgernode` | `internal/ledgerstep` |
| `ReviewNode` / `MergeNode` | `ReviewStep` / `MergeStep` |
| `Node` 字段、`node:` actor 前缀 | `Step` / `step:` |
| CLI `card dispatch --node review\|merge` | `--step review\|merge` |
| 卡抽屉「节点动作」区标题 | 「环节动作」 |

**改名独立成一个 task、独立成一个提交**，不与行为修复交织——混在一起会让
行为 diff 淹没在改名噪音里，审不动。

`node:` actor 前缀是**落在既有事件里的历史数据**：改了之后旧事件仍是
`node:review`。`CountRounds` 等按 actor 前缀推导的地方**必须同时认两种前缀**，
否则已有卡的回合计数会被清零、绕开 3 轮封顶这道安全阀。这条是本次改名唯一
的真风险点。

## 4. 明确不做

- 不动纪律块、不动 permgate、不给执行者加推送步骤（裁决 2 的直接推论）。
- 不给自动推基线加开关（YAGNI：目前没有需要关它的具体场景）。
- 不碰 `NewLocalObjective` 里写死的 `gofmt` + `go test ./...`——客观判据只认 Go
  项目是既有限制，与本轮无关，另立条目。
- 不做 A 组两条（按环节派发按钮、子任务树 rollup）。
- 不做存量切换（§8）、不合 main。

## 5. 验收判据

① **D1 正向**：账本关—开—派发一张卡走完实现→审阅→合并，合并环节不再报
   `couldn't find remote ref`；`git ls-remote origin <工作分支>` 能查到该分支。
② **D3 工作区隔离**：合并环节跑之前记下协调者仓的当前分支与 `git status`，
   跑完后**两者都不变**；在协调者仓**留有未提交改动**的情况下重跑一次，
   合并仍然成功（不再因 checkout 被拒而失败）。**另跑一次「主工作区正停在
   基线分支上」的情形**——这是 `--detach` 那条要挡的失败，不覆盖等于没验。
   合并结果以 `git ls-remote origin <基线>` 为准（本地基线引用按 §3.1 不再推进）。
③ **D3 清理**：合并成功与合并冲突两条路径跑完后，`git worktree list` 里都没有
   残留的临时 worktree。
④ **D4 显式化**：合并成功后 `card show <id>` 的 timeline 事件里能读到
   `pushed_work_branch` / `merged_into` / `pushed_base` 三个字段；skill 的账本
   模式节有对应描述。
⑤ **§3.3 阶梯**：故意用 `--no-sync` 造一张本地无工作分支的卡，合并环节应
   走到「先 fetch origin」；再把 origin 上的分支也删掉重试，应转「等人」且
   reason 含「先 handoff pull」。
⑥ **非快进保护**：造一条 origin 上已分叉的同名工作分支，合并环节的 push 应
   以非快进失败并转「等人」，**不得出现 `--force`**。
⑦ **D2**：执行机仓库人为回退到旧提交后派发一张 `--base-branch main` 的卡，
   新分支的起点应是补拉后的 main，而非执行机的陈旧本地 main。
⑧ **改名不清零回合计数**：取一张事件流里已有 `node:review` actor 的存量卡，
   改名后 `CountRounds` 读出的轮次与改名前**相同**（防止绕开 3 轮封顶）。
⑨ **全量门**：`gofmt -l .` 无输出、`go build/vet/test ./...` 全绿、
   web `tsc` 0 错 + vitest 全绿。

判据①②③⑤⑥⑦需要真机（起隔离 agentd 实例 + 真实 git 远端），**由协调者执行，
不派发**——它们要驱动 handoff 自身，与执行纪律块的「不要调用 handoff CLI」冲突。
