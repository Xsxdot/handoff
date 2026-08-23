# B190 实现计划（**实况回填**）

> ⚠️ **这份 plan 是事后回填的，不是事前写的。**
> 2026-08-23 的 `plan` 节点派发（task `e6a9071c`，codex@linux-01）被诱导直接做完了
> 实现（提交 `89513416`），没有产出计划文档——即 B182 的缺陷。`implement` 列的
> 「要 plan 附件」门结构性地拦住了自动移列，卡落 `needs_human`，协调者据此发现越轨。
> 本文按 B175/B176 的先例处置：协调者逐行审过实现后，把**实际发生的**分解回填成
> plan，用来补上那道门，并留下越轨痕迹。**它描述已完成的事，不是待办清单。**
>
> spec：`docs/superpowers/specs/2026-08-23-b190-seed-upgrades-legacy-workflows.md`
> 分支：`cards/B190-charter`，起点 `272d84f1d`

## Task 1：给 Store 一条看得见原始定义的内部读取路径

**做了什么**：把 `GetWorkflow` 的库读部分抽成 `getWorkflowStored`，后者**不做**老 def
的节点投影；`GetWorkflow` 保持原语义（读出来就是可用的节点形态），只是改为在
`getWorkflowStored` 之上再投影一次。

**为什么必须这样**：`GetWorkflow` 在返回前就投影了，调用方看不到「这一行原本是不是
只有 States」。而这正是区分「存量老定义」与「用户发布的全人工节点形态」的唯一准确
信号——拿投影后的能力去判，两者长得一模一样。

**落点**：`internal/ledger/workflows.go#Store.getWorkflowStored`（新增，包内私有）、
`internal/ledger/workflows.go#Store.GetWorkflow`（改为两段）。

## Task 2：seed 按存储形态补版

**做了什么**：`EnsureDefaultWorkflows` 的循环由「取到就 `continue`」改为三分支：

| 存储形态 | 动作 |
|---|---|
| 该名字不存在 | `PutWorkflow` 出厂定义（现状行为不变） |
| 最新版 `Nodes` 为空且 `States` 非空 | `PutWorkflow` 出厂节点形态，**只追加新版本，旧版行不动** |
| 最新版已是节点形态 | 跳过（用户 put 过的，不碰） |

补版走既有的 `PutWorkflow`，因此自动继承节点校验；失败即返回错误并落 Error 日志。
三条分支各有 Info/Debug 日志（`from_version` / `version` / `dispatch_nodes`），
「这条流为什么不动」在日志里答得出来。

**落点**：`internal/ledger/workflows.go#Store.EnsureDefaultWorkflows`。

## Task 3：纯人工列的派发报文指路

**做了什么**：把 `GetCard` 提到能力判断之前（报文需要工作流名），报文由
`节点 "X" 没有 Dispatch 能力，不可执行` 扩成同时给出**原因**（这条流是老定义 /
这一列是人工列）与**出路**（`handoff workflow put <流> --file <定义文件>`）。

**落点**：`internal/ledgerstep/node.go#NodeStep.RunOnce`。

## Task 4：测试

落在 spec 指定的那**一个**接缝上（`Store.EnsureDefaultWorkflows`），四条断言：

| 用例 | 钉住什么 |
|---|---|
| `TestEnsureDefaultWorkflowsUpgradesLegacyDefinition` | 老 def → 追加 v2 且带出厂派发能力；**v1 行按版本号取回仍是原样**（连原始 JSON 都断言了没有 Nodes） |
| `TestEnsureDefaultWorkflowsDoesNotUpgradeNodeDefinition` | 用户 put 过的节点形态 → 版本数不变 |
| `TestEnsureDefaultWorkflowsLegacyUpgradeIsIdempotent` | 连跑两次 seed → 只涨一次版本 |
| `TestEnsureDefaultWorkflowsDoesNotOverwrite`（既有） | 空库 seed 的现状行为不许退化 |

报文那条在既有的 `TestRunnerFindsNodeInPinnedWorkflowVersion` 上补负向断言。

**夹具改动一处，需要知道**：`TestEnsureDefaultsKeepsUserDomainWorkflow` 的夹具从
`States: {"甲","乙"}` 改成了等价的节点形态。原因是本次改动之后 States-only 定义
**按设计**就是「老定义」，原夹具表达不出「用户自建的节点形定义」这个意思了。
测试意图（用户自建的同名流不被覆盖）没有被削弱，且新增的
`TestEnsureDefaultWorkflowsDoesNotUpgradeNodeDefinition` 覆盖同一件事。

## 已知后果（spec 的判据里没写全，此处补记）

判据「存储行是 States-only」会把**用户刻意 put 的 States-only 定义**也当成老定义补版
（spec 只论证了它不会误判「用户发布的全人工节点形态」，没论证这一种）。评估为可接受：
① 只对四个出厂流名生效；② 只追加不覆盖，旧版行原样保留，钉在旧版的卡完全不受影响；
③ 用户重新 put 一次即可恢复。**不改判据**——按名字白名单的替代方案会把 `feature` /
`domain` 的同一个 bug 留一半，spec 已弃选。

## 协调者验收记录（2026-08-23，本地 `/tmp/b190rev` 独立 worktree）

| 判据 | 结果 |
|---|---|
| 编译全量 | `go build ./...` exit 0 |
| 测试局部 | `go test ./internal/ledger/... ./internal/ledgerstep/... -count=1` 全绿（ledger 2.597s / ledgerstep 1.937s） |
| 格式 | `gofmt -l .` 空；`go vet ./internal/ledger ./internal/ledgerstep` 无输出 |
| **变异** | 把补版分支的条件改成 `if false`，`go test -run TestEnsureDefault` **变红**——新用例确实罩住了实现，不是装饰 |
| **真机** | 对**生产账本副本**（`~/.handoff/ledger.db` 拷贝）跑一次 seed：`bug` v1(dispatch=0)→v2(dispatch=2)、`feature` v1(dispatch=0)→v2(dispatch=3)、`domain` v1(dispatch=4) 不动、`triage` v1 不动；再跑一次 `bug` 稳定在 v2（幂等）。探针文件跑完即删，未入库 |

