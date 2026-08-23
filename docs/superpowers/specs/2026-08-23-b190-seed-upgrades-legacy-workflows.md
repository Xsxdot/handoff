# B190：存量账本的出厂工作流补版

> 级别：**L2**　路由：plan → implement → review → acceptance → finish
> 状态：**已批准** —— 2026-08-23 用户授权自主推进（原话「直到完成能自主推进的所有任务」）。
> 承载卡：B190。

## 问题陈述

出厂工作流的 seed 是「已存在同名的就整条跳过」，于是**存量账本永远拿不到出厂节点形态**。

实测（2026-08-22 A 组批处理）：`handoff card dispatch B183 --step 待审阅` 被拒，
报文 `节点 "待审阅" 没有 Dispatch 能力，不可执行`，日志 `dispatch=false verdict=false
template=""`。查证：`handoff workflow show bug` 显示 v1 的 def 只有四列 states，
nodes 是读取侧投影出来的**纯人工列**。

后果：所有存量库的 `bug` / `feature` / `domain` 流都不能用卡驱动派发。L1 小修只能
退回裸 `card dispatch --template`，而那条路会把卡认领回「进行中」，与卡当前所在列冲突。
这条**卡住了 L1 通道**——今天每一张小修卡都只能靠协调者手工 `workflow put` 才走得动。

## 现状事实（读数，由 implement 对本轮工作树复核）

| 事实 | 出处 |
|---|---|
| seed 对每条出厂流先 `GetWorkflow(name, 0)`，取到就 `continue`——**整条跳过，不看形态** | `internal/ledger/workflows.go#Store.EnsureDefaultWorkflows` |
| 出厂形态里 `bug` 的「进行中」是 `feature-impl` 派发列、「待审阅」是 `review-generic` 裁决列 | 同上，`defaults["bug"]` |
| 老 def 只存 `States`/`Gates`；**读取侧**补出等价节点序列，补出的全部是纯人工列 | `internal/ledger/workflows.go#WorkflowDef.withNodesFromStates` |
| 新 def 走写入侧投影，`States` 是 `Nodes` 的派生物——所以「原始 def 的 Nodes 为空」是老定义的**准确**签名 | `internal/ledger/workflows.go#WorkflowDef.withStatesFromNodes` |
| `GetWorkflow` 在返回前就做了读取侧投影，调用方**看不到**原始形态 | `internal/ledger/workflows.go#Store.GetWorkflow` |
| 工作流是只插新版本、永不 UPDATE 旧行——钉版本的卡随时取回当时的形状 | `internal/ledger/workflows.go` 包注释 |

## 方案

### 采纳：按存储形态补版（不覆盖，只追加）

seed 时对每条出厂流判一次形态：

- **该名字不存在** → 照旧 `PutWorkflow` 出厂定义（现状行为不变）。
- **存在，且最新版的原始 def 是老形态（`Nodes` 为空、`States` 非空）** → `PutWorkflow`
  一版出厂节点形态。**旧版行一个字不动**：钉在旧版的存量卡完全不受影响，新迁入
  的卡取到最新版就有派发能力。
- **存在，且最新版已是节点形态** → 跳过（这是用户 put 过的，不能碰）。

判据必须落在**存储形态**上，不能落在「投影后有没有 Dispatch 节点」上——后者会把
「用户刻意把某条流改成全人工」误判成老定义，然后追加一版把用户的意图覆盖掉。
seed 因此需要一条能看见原始 def 的内部读取路径；`GetWorkflow` 的对外语义（读出来
就是可用形态）不变。

顺带（同一张卡的次要产出）：**纯人工列被派发时的报文要指路**。今天的
`节点 "X" 没有 Dispatch 能力，不可执行` 说的是结论不是出路；应补一句「这条流是老定义
/ 这一列是人工列，要让它可派发用 `handoff workflow put <流> --file <定义文件>`」。
补版落地后这条报文只在真·人工列上出现，那时它指的路才是对的。

### 弃选

| 方案 | 弃选理由 |
|---|---|
| **只改报文，不补版**（卡上的候选修法②） | 治标。报文改好了，用户每台机器每个库还是得手工 put 一遍，而「装完就有一条能跑通的流」本来就是 seed 的职责 |
| **无条件用出厂定义覆盖同名流** | 会踩掉用户在控制台改过的定义。工作流是数据不是代码语义，seed 没有权威覆盖它 |
| **按名字白名单只补 `bug`** | 判据应该是形态不是名字。`feature` / `domain` 存量库同样是老定义，同样派不动；漏掉它们只是把同一个 bug 留一半 |

## 用户故事

1. 作为存量库的用户，我升级二进制并重启 agentd 之后，`card dispatch <L1 卡> --step 进行中`
   能派出去，不需要先手工 `workflow put bug`。
2. 作为改过 `bug` 流的用户，我升级之后自己的定义不被动，最新版仍是我 put 的那一版。
3. 作为手上有存量在飞卡的协调者，那些钉在旧版本的卡行为完全不变。
4. 作为对真·人工列误发派发的用户，我得到的报文告诉我这一列为什么不可派发、以及怎么让它可派发。

## 实现决定

- 补版走既有的 `PutWorkflow`，因此自动继承节点校验（Verdict 蕴含 Dispatch、模板存在性等）。
- 判形态的读取路径是 Store 内部的，不进对外 API——外部拿到的工作流永远是可用形态。
- seed 是幂等的：第二次启动时最新版已是节点形态，走「跳过」分支，不会每次重启都追加一版。
  **这条要有测试钉住**，否则每次重启涨一版是最容易漏的回归。

## 测试决定（接缝清单）

一个缝：`Store.EnsureDefaultWorkflows`（`internal/ledger/workflows_test.go` 已有
`TestEnsureDefaultWorkflowsDoesNotOverwrite` 等用例在这个缝上）。四条断言：

1. 空库 seed → 出厂形态（现状行为，已有用例，不许退化）；
2. 库里是老 def（只有 States）→ 追加一版节点形态，且**旧版行仍可按版本号取回原样**；
3. 库里是用户 put 过的节点形态 → 版本数不变；
4. 幂等：连跑两次 seed，版本数只涨一次。

报文那条改动落在派发入口的错误分支上，用现有的节点派发用例覆盖一条负向断言即可。

## Out of Scope

| 不做 | 分类 |
|---|---|
| `feature` / `domain` 两条流的退役（B174 已把它们退出日常路径） | 永不做——本 spec 只管形态补版，退役与否是另一件事；补版对所有出厂流一视同仁 |
| 自动把存量**卡**迁到新版本 | 永不做——迁哪张卡是协调者的决定，`workflow migrate` 已经是显式动作 |
| 控制台里的工作流编辑体验 | 本期不做（另有 B179 一族的界面卡） |

## 备注

- 图覆盖债：无。`EnsureDefaultWorkflows`、`GetWorkflow`、`PutWorkflow` 均在
  `codegraph` baseline 命中。
- 定级复核：改动全部落在 `d_ledger` 一个子系统内，不动跨进程 wire 契约，判 **L2**。
