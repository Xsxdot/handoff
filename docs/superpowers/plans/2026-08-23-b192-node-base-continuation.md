# B192（含并入的 B195）实现计划（**实况回填**）

> ⚠️ **这份 plan 是事后回填的。** 2026-08-23 的 `plan` 节点派发（task `8e7e020f`，
> codex@linux-01）被诱导直接做完了实现（提交 `d62fa4b0`）——**同日第三次**，
> 前两次是 B190 与 B196。根因当天已定位并记在 **B200**：linux-01 的 codex 装了
> superpowers 插件，其网关 skill `using-superpowers` 强制「1% 可能相关就必须调
> skill」，而 `executing-plans` 的描述与 handoff 派发的形态高度匹配、它说的是「实现」。
> 按 B175/B176 先例处置：协调者逐行审 + 回填实况 plan 补 `implement` 列的门。
>
> spec：`specs/2026-08-23-b192-node-base-continuation.md`
> contract：`specs/2026-08-23-b192-contract.md`（含 §8 拆解阶段边界澄清）
> breakdown：`specs/2026-08-23-b192-breakdown.md`（T1/T2 划分与真机清单）
> 分支：`cards/B192-charter-3`，起点 `99e056f00`

## T1 · 账本/编排起点矩阵（`d_ledger`）

**做了什么**（`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`）：

1. 在用途裁定之后**一次性**读 `WorkBranch`，分支与目标机同源；`ErrNotFound` 之外的
   错误直接上抛（不静默当成「没有工作分支」）。
2. **跨机拒发**：`hasWorkBranch && (workInfo.Target == "" || workInfo.Target != target)`
   → 在 `Transport`／`LinkTask`／`RecordDispatch` **之前**返回错误，文案带上次目标机、
   本次目标机、以及 `git push origin <分支>` + 显式 `--base` 两个出口。
3. **起点接续**：`hasWorkBranch` 为真时 `base = workInfo.Branch`、`localBaseBranch = true`。
   原先只给审阅轮的 `reviewBase` 特判随之删除——审阅被这条通用规则吸收了，
   分支命名（`-review-N` / `-N` 挂号）一行未动。
4. `resolveDefaultBase := base == ""` 排在其后，因此接续时它必为 false，
   与契约要求的「两个标记互斥」自动成立。

卡上下文里的 `cardBase` 仍单独取 `EffectiveBaseBranch`——**合并目标与本次起点是两件事**，
契约不变式，代码里两次取值刻意没合并。

## T2 · agentd 接缝与本地解析（`d_controlplane` + `d_remote`）

**做了什么**：

- `internal/agentd/manager.go#Manager.Dispatch`：两条前置门，都排在默认基线解析、
  基线 fetch、建任务、建 worktree **之前**——① `LocalBaseBranch && ResolveDefaultBase`
  互斥；② `LocalBaseBranch` 时 `Base` 非空。两条都返回 `ErrBadWorkspaceReq`（400），
  拒发路径不留 task/worktree 副作用。日志加 `local_base_branch` 字段。
- `internal/agentd/workspace.go#resolveDispatchBase`：`localBaseBranch` 为真时转
  `resolveLocalBaseBranch`，**不进 D2 补拉路径**。
- 新增 `#resolveLocalBaseBranch`：拒绝空名/`-` 开头/`refs/` 前缀；`show-ref --verify`
  + `rev-parse ^{commit}` 只读本地 `refs/heads/<分支>`；缺失时返回 `ErrBadWorkspaceReq`，
  文案就是契约要求的那句「工作分支只存在于创建它的那台机器……先 push 到 origin，
  再用显式 --base 指定」。`fetched` 恒为 false。
  **刻意不用 `ResolveBaseBranch`、也不看远程跟踪 ref**——拿 origin 的陈旧镜像替代
  私有工作分支，会把接续节点悄悄带回错误代码。

`ResolveBaseBranch` 的无条件补拉纪律**一行未改**，只是不再被这条路径经过。

## 测试（spec 的两缝 + 一穿线，逐条落位）

| 用例 | 缝 | 钉住什么 |
|---|---|---|
| `TestViaTemplateContinuationUsesLocalWorkBranch` | 编排侧 | 第二轮沿同机工作分支接续并置位标记；首轮仍走空基线默认分支语义 |
| `TestViaTemplateRejectsCrossTargetBeforeTransport` | 编排侧 | 跨机不静默掉回卡基线；拒绝发生在 Transport/LinkTask/RecordDispatch 之前 |
| `TestViaTemplateNodePurposeTakesReviewPath`（既有，被本轮改动波及） | 编排侧 | 审阅轮仍取工作分支，分支命名不变 |
| `TestResolveDispatchBaseLocalBranchUsesLocalRef` | 解析侧（真 git 夹具） | 只读 `refs/heads`，**origin 不可达也能解析**，绝不 fetch |
| `TestResolveDispatchBaseLocalBranchMissingRejects` | 解析侧 | 缺失时给可行动的 `ErrBadWorkspaceReq` 文案，不退回 HEAD、不访远端 |
| `TestDispatchRejectsLocalBaseBranchInvariant` | manager | 两条不变式都在解析/建任务/建 worktree 之前拒绝 |
| `TestDispatchNormalBaseSemanticsUnchanged` | manager | **反向锁**：普通分支仍 D2 补拉、普通空 Base 仍取本地 HEAD、卡空基线仍读 origin/HEAD |
| `TestDispatchWireLocalBaseBranchEndToEnd` | 穿线 | client map → HTTP DTO → server→manager → 真实本地 resolver 全程；**origin 设成不可达占位地址**，只有整条 wire 上的 `local_base_branch=true` 才能不 fetch 地建成任务 |

穿线那条的设计值得点名：把 origin 设成不可达，让「没有 fetch」这个断言**结构上可证**，
而不是靠断言一个布尔。

## 协调者验收记录（2026-08-23，独立 worktree `/tmp/b192impl`）

| 判据 | 结果 |
|---|---|
| 编译全量 | `go build ./...` exit 0 |
| 静态检查 | `gofmt -l .` 空；`go vet ./internal/... ./cmd/...` exit 0 |
| 测试 | `go test ./internal/ledgerstep/... ./internal/agentd/... -count=1` → 两个包 ok（agentd 96.072s），EXIT=0 |
| 首轮假红辨识 | 第一次跑 agentd 撞 `connect: can't assign requested address`（B193 族一，本机临时端口池被前面的全量跑掏空）。**等 TIME_WAIT 回落到 150 以下再复跑即全绿**——按形状认，不是本 diff 的问题 |
| **变异 ×4** | ①`localBaseBranch = false` → `…ContinuationUsesLocalWorkBranch` + `…NodePurposeTakesReviewPath` 双红；②跨机拒发改 `if false` → `…RejectsCrossTargetBeforeTransport` 红；③本地解析退回补拉 → `…LocalBranchUsesLocalRef` + `…LocalBranchMissingRejects` + `…WireLocalBaseBranchEndToEnd` 三红；④manager 两条门改 `if false` → `…RejectsLocalBaseBranchInvariant` 红。四条实现路径各被至少一条用例罩住 |
| 合并干净 | `git merge-tree origin/main cards/B192-charter-3` 无冲突 |
| **真机** | **未验，被部署卡住**——见下节 |

## 真机验收为什么还没做（阻断项）

breakdown 的真机清单第 1、2 条（同机接续、跨机拒发）要求**目标机的 agentd 含本卡改动**。
今天两端的 agentd 都是旧二进制（本机 launchd 托管那份、linux-01 的 `0452a398f`），
旧端会**静默忽略** `local_base_branch`（Go 的 `json.Decoder` 默认不拒未知字段，
契约 §1.1 已查证）——表现是派发看着成功、实际仍走无条件补拉、审阅轮照样 400。
这正是拍板 P3 认下的版本边界。

换 agentd 二进制并重启是外部可见、且会打断 linux-01 上两个不属于本轮任务的动作，
**归用户决定**，协调者不自行执行。
