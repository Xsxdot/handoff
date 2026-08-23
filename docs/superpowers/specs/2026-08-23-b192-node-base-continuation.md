# B192（含并入的 B195）：charter 流节点起点接续

> 级别：**L3**　档位：**轻档**（contract → breakdown → 单轮 plan+implement → review → acceptance → finish）
> 状态：**已批准** —— 2026-08-23 用户授权自主推进（原话「你从 B195+B192 开始自主推进吧，该走派发的节点要走派发」）。
> 承载卡：B192；B195 已 `card merge` 并入（`merged_into` 边，2026-08-23）。

## 问题陈述

charter 流把一张卡分成多个节点依次派发，但**每个节点都从卡的基线新开分支**，
上一节点的产出不在下一节点的工作树里。两处后果已各自真机实测：

1. **产出不接续（原 B192）**。`ViaTemplate` 的起点取卡的有效基线，与上一节点派出的
   分支无关。实测分支序列：B177.1 五轮依次 `cards/B177.1-charter`、`-2`…`-5`，每轮都
   从基线开；B177.5 是 `-charter`（plan 轮）→ `-charter-2`（implement 轮）。于是
   ① plan 节点提交的计划文档不在 implement 节点的工作树里，而 implement 的门恰恰
   要求 plan 附件；② implement 重跑轮看不到上一轮的实现，等于每轮从零开始；
   ③ `integrate` / `图对账` 这类必须看见工作分支的节点同样落空。
2. **审阅轮远程派发必被拒（原 B195）**。B183 已让审阅轮的起点换成卡的工作分支，
   但 agentd 解析**分支名形态**的起点时无条件从 origin 补拉，而工作分支只存在于
   执行机本地、要到合并环节才第一次推 origin。`card dispatch <卡> --step review
   --target <机器>` 稳定 400：`基线提交在任务仓库中不存在: 基线分支 "cards/B1-charter"
   从远端 "origin" 补拉失败（fatal: couldn't find remote ref cards/B1-charter）`。
   决定性实验：手工把该分支 push 到 origin 后，同一条命令立刻成功。

两条是同一族：**B195 是「已经在接续的那一个节点」撞上了基线解析；B192 是「其余节点
根本没在接续」。** 只修其一都不成立——把接续推广到所有节点，会把 B195 的 400 从
一个节点扩散到全部节点。

## 现状事实（读数，出处见符号锚；由 contract 节点对本轮工作树复核）

| 事实 | 出处 |
|---|---|
| 起点取卡的有效基线，只有审阅轮被 `reviewBase` 覆盖成工作分支 | `internal/ledgerstep/dispatch.go#ViaTemplate` |
| 卡的工作分支 = 最近一次**非审阅** dispatched 快照的分支；没有时返回包 `ErrNotFound` 的错误 | `internal/ledger/events.go#Store.WorkBranch` |
| dispatched 快照里带 `Target`（那一轮派往哪台机） | `internal/ledger/events.go#DispatchSnapshot` |
| 本地存在 `refs/heads/<rev>` 即判为分支名形态，走补拉路径 | `internal/agentd/workspace.go#needsBaseBranchSync` |
| 分支名形态**无条件 fetch**，且注释写明这是刻意设计（"解析得到" 不是 "不用拉" 的可靠信号） | `internal/agentd/workspace.go#ResolveBaseBranch` |
| 派发链路已有一个「派发侧告知 agentd 如何解析基线」的布尔字段先例，wire 路径 ledgerstep → client → HTTP → manager | `internal/ledgerstep/dispatch.go#DispatchOpts` 的 `ResolveDefaultBase`；`internal/agentd/server.go` 的 `resolve_default_base` |
| 账本**不记完工提交号**（全仓 `Commit` 只命中 `tx.Commit()`），因此今天拿不到工作分支的尖端 sha | `internal/ledger/` |
| CLI 与 agentd 的环节派发共用同一个编排点，没有第二处起点裁决 | `cmd/card_node.go#runStepDispatch`、`internal/agentd/cardstep.go#Server.startCardStep`，两者都装配 `ledgerstep.StepRunner` |

## 方案

### 采纳：起点接续 + 本地基线标记（方案 A）

三条规则，都收口在既有的两个决策点上，不新增第三个：

**规则一（接续）**：节点派发的起点 = **卡的工作分支**——当它存在、且它上次派往的
目标机与本次目标机是同一台时；否则 = 卡的有效基线。审阅轮的现有特例被这条规则
吸收（它本来取的就是工作分支），`reviewBase` 那段特判随之消失。

**规则二（本地解析）**：起点是**工作分支形态**时，派发请求携带「本地起点」标记；
agentd 对带此标记的起点只解析本地 `refs/heads/<分支>`、**不补拉**；本地缺失即拒发。
卡基线形态的起点不受影响，继续走无条件补拉——那条设计原样保留。

**规则三（跨机拒发并指路）**：上次目标机 ≠ 本次目标机时拒发，文案说清「工作分支
只存在于创建它的那台机器」，并给出口：先把该分支 push 到 origin，再用显式 `--base`
指定。**不静默回落卡基线**——静默丢产出正是本卡要修的病。

为什么值得：它把「审阅能看见代码」从一个节点的特判，升成整条流的通用能力；
`integrate` 与 `图对账` 这两个此前刻意没动的节点，本 spec 交付后自然可用。

### 弃选

| 方案 | 弃选理由 |
|---|---|
| **B：`ResolveBaseBranch` 改成「本地有就不拉」** | 直接推翻它注释里写明的刻意设计（分支名在本地永远解析得到，那正是陈旧的那一份）。会让「执行机镜像陈旧导致起点错」这个 bug 永远不触发修复路径——用一个错换另一个错 |
| **C：派发时自动把工作分支 push 到 origin** | 能顺带解决跨机接续，但把执行机的私有工作分支变成 origin 上的公开分支（外部可见的写动作），且与合并环节的 push 语义重叠。本期不做，落 roadmap |
| **D：起点传提交号而不是分支名** | 语义最干净（commit sha 在本地要么有要么没有，是可靠信号，可直接复用既有 commit 路径），但账本今天不记完工提交号，拿不到工作分支尖端。要先补「完工提交落账」才谈得上——那是另一张卡的活。落 roadmap |

## 用户故事

1. 作为协调者，我在 plan 节点派发后接着派 implement 节点，implement 的执行者能在
   自己的工作树里看到 plan 节点提交的计划文档。
2. 作为协调者，我对一张已有实现产出的卡派 `--step review --target linux-01`，
   派发成功，审阅工作树的尖端就是实现提交，不再返回 400。
3. 作为协调者，我重跑一个失败的 implement 轮，新一轮能看到上一轮的产出，
   而不是从卡基线重新开始。
4. 作为协调者，我把某个节点派到与上一节点不同的机器上时，得到一条说清原因、
   带可执行出口的拒绝，而不是一条 origin 补拉失败的报文。
5. 作为协调者，卡的第一个节点（contract）仍从卡基线开——首轮没有可接续的东西。

## 契约语义与接缝（L3；只定语义，签名归 contract）

- **跨进程 wire 契约增量**：派发请求新增一个布尔字段，语义为「本次起点是**目标机
  本地的工作分支**，按本地 ref 解析，不得补拉」。方向：编排侧（CLI / agentd 的环节
  装配）→ agentd 工作区侧。它与既有的 `resolve_default_base` 互斥（一个说"基线为空、
  你去解析项目默认分支"，一个说"基线是本地工作分支、你别去网络"），互斥关系写进契约。
- **账本侧契约增量**：「卡的工作分支」这个查询需要同时给出**它上次派往的目标机**——
  规则三要靠它判跨机。语义定了；用改返回值还是新增方法，归 contract 节点按现状
  调用方数量决定。
- **收口层**：起点裁决收口在 `ledgerstep`（CLI 与 agentd 共用的唯一编排点）；
  解析裁决收口在 agentd 工作区侧的派发起点解析函数。两侧各一个决策点，不新增第三个。
- **不变式**：卡上下文里写给执行者的「合并目标」始终是卡的有效基线，与本次派发的
  起点是两件事——现状代码已把两者分开取，本 spec 不合并它们。

## 实现决定

- 规则一的判据用「工作分支存在与否」，不用「节点名」或「轮次」——节点名会随工作流
  定义漂移，轮次答不出"上一轮派到哪台机"。
- 规则三的拒发发生在**派发之前**（起点裁决处），不落孤儿任务记录，与既有的准入闸
  同侧。
- 现有的分支命名逻辑（审阅轮 `-review-N`、非审阅轮 `-N` 挂号）不动。

## 测试决定（接缝清单）

理想是一个缝，实际两个 + 一条穿线，因为改动跨了 wire 的两侧：

1. **编排侧**：`ledgerstep` 的模板派发 + 注入 Transport（该缝已存在于
   `internal/ledgerstep/dispatch_test.go`）。断言四种形态下起点与本地标记的取值：
   首轮（无工作分支）/ 接续轮（同机）/ 审阅轮 / 跨机。
2. **解析侧**：agentd 的派发起点解析（该缝已存在于 `internal/agentd/workspace_test.go`，
   带真 git 夹具）。断言带本地标记的起点只解析本地 ref、不产生网络补拉，
   且本地缺失时的报错文案含出口。
3. **穿线**：client → server 的 wire 透传一条，照 `ResolveDefaultBase` 的既有用例形态。

不新建端到端真机用例——本 spec 的真机判据留给 acceptance 节点（真派一轮 review 到
linux-01，看它不再 400）。

## Out of Scope

| 不做 | 分类 |
|---|---|
| 派发时自动 push 工作分支到 origin（方案 C），以及由它带来的跨机接续能力 | 本期不做、后续要做 → roadmap |
| 完工提交号落账（方案 D 的前置，也是"起点传 sha"的前提） | 本期不做、后续要做 → roadmap |
| `图对账` / `integrate` 两个节点的工作流配置改动 | **永不做**——它们缺的就是本 spec 的接续能力，交付后自然可用，不该再加节点级特判 |
| B196（`--step` 不认领驱动）、B189（驱动租约无人续期） | 同族不同事，各有自己的卡 |
| 存量在飞卡的迁移 | 本 spec 只改行为；哪张卡迁到新版本由协调者逐张决定 |

## 备注

- 图覆盖债：无。本 spec 引用的全部符号（`ResolveBaseBranch`、`EffectiveBaseBranch`、
  `WorkBranch`）都在 `codegraph` baseline 里命中。
- 定级复核（按定稿范围）：改动落在 `d_ledger`（工作分支查询）、`d_coordination_task`
  / `d_controlplane`（编排与 manager）、以及 CLI↔agentd 的跨进程 wire 契约上——
  跨子系统且动契约层，判 **L3**。单子系统工作量均远低于流程固定成本，选 **轻档**。
