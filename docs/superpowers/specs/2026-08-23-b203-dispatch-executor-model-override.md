# B203：card dispatch 支持 --executor / --model 一次性覆盖

> 状态：**已批准（2026-08-23，用户）** · 级别 **L2** · 档位不适用

## 问题陈述

想让**这一次**派发换个执行器或模型时，`card dispatch` 没有任何临时通道。

今天的五个 flag 是 `--template / --target / --plan / --discipline-override / --step`
（`cmd/card_dispatch.go#init`），没有 `--executor` / `--model`；而 `--step` 模式下
`--template` 根本不生效——`runStepDispatch`（`cmd/card_node.go#runStepDispatch`）只把
`cardDispatchTarget` 传进 `StepRunner`，模板一律从卡钉住的工作流节点定义里查。

于是唯一能改执行器/模型的地方是 `NodeOverride`（`internal/ledger/types.go#NodeOverride`），
而那是**工作流定义的一部分**：为一次派发改它，等于把全局所有卡的该节点都换掉，用完还得
记得 put 一版改回来。2026-08-23 实测：v4 改 v5、派两张卡、v6 改回，全靠人记得。

代价有三层，最后一层最贵：

1. 想换执行器时没有便宜的路，于是要么不换、要么改全局定义；
2. 改全局定义有一个**必须靠记忆闭合的窗口**（改回来之前，所有卡的该节点都被换了）；
3. 这个窗口没有任何结构性提醒——忘了改回来不会报错，只会在下一张卡上静默生效。

## 现状事实（本轮工作树读数，交 plan 复核）

| 事实 | 出处 |
|---|---|
| `card dispatch` 只有五个 flag，无 executor/model | `cmd/card_dispatch.go#init` |
| `--step` 只透传 Target，模板从卡钉的工作流查 | `cmd/card_node.go#runStepDispatch` |
| **裸 `handoff dispatch` 早就有 `--executor` / `--model`** | `cmd/dispatch.go#init` |
| `StepRunner.Target` 已是「一次性覆盖」的现成先例，含三级优先级 | `internal/ledgerstep/runner.go#StepRunner`、`#dispatchNode` |
| `TemplateDispatch` 已有 `ExecutorOverride` / `ModelOverride` 字段 | `internal/ledgerstep/dispatch.go#TemplateDispatch` |
| `DispatchOpts` 已有 `Executor` / `Model`，且是 `POST /api/tasks` 的请求键 | `internal/client/client.go#DispatchOpts`、`#Client.Dispatch` |
| 模型的下层来源是模板的按机器映射 `ModelByTarget[target]` | `internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate` |
| **活模板 `charter-default` 没有 `model_by_target`**，charter-v4 的节点只覆盖 discipline/purpose | 活账本 `template show charter-default`、`deploy/workflows/charter-v4.json` |
| 服务端已拒未注册执行器并列出可用名单（400，任务不建） | `internal/agentd/manager.go#Manager.resolveExecutor` |
| 服务端已钉死「机器默认模型只属于缺省执行者」 | `internal/agentd/manager.go#Manager.resolveModel` + `TestResolveModelOnlyAppliesToDefaultExecutor` |
| agentd **不认识任何执行器的模型名单**，模型名无判据可校验 | `internal/agentd/executor_default.go` 文件头注释 |

**结论：能力链路整条都在，断的只有 CLI 那一截。** 本卡不是加机制，是把 `Target`
已经走通的那个模式对称扩到 Executor/Model。

## 方案

### 采纳：把 Target 的一次性覆盖模式对称扩到 Executor / Model

1. **CLI 加两个 flag**：`card dispatch --executor / --model`，语义与裸 `handoff dispatch`
   的同名 flag 一致（只作用于本次派发，不落进工作流定义）。
2. **两条路径都吃**：`--step`（节点派发）与无 `--step`（模板派发）。今天模板路径也没有
   这两个 flag，是同一个缺口的两半。
3. **`StepRunner` 加 `Executor` / `Model` 两个字段**，优先级与 `Target` 逐字一致：
   **CLI > 节点覆盖 > 模板**。
4. **成对规则**：任何一层覆盖了 executor 而**同层**没给 model 时，model **不得从下层继承**
   ——置空，交给执行器自身的默认模型。
5. **`dispatched` 事件补记 executor 与 model**。今天的 payload 只有
   template/target/purpose/branch/task_id，一次性覆盖不落账，事后复盘不出这一轮是谁跑的。
6. **CLI 侧不做任何校验**（理由见下方弃选 A）。

### 关于成对规则（第 4 条）——这是本卡唯一的真设计决定

用户 2026-08-23 定的语义：*每个执行器内部都有自己的默认模型；机器默认模型只属于缺省
执行者；所以换执行器的时候不应该把默认模型传进去。*

服务端已经在**机器默认模型**这一层实现了它（`resolveModel` 判 `execName == Default`，
四条用例钉死）。但模型还有另一个下层来源——模板的 `ModelByTarget[target]`，而
`resolveModel` 救不了它：该函数第一行是 `if reqModel != "" { return reqModel }`，
它分不清这个值是调用方显式给的，还是从模板漏下来的。

于是：`--executor grok` 而不给 `--model` → executor 换成 grok，model 仍是
`ModelByTarget[target]`（属于模板声明的执行器）→ 跨执行器复用模型名 → 第一个事件 400。

**这条今天不可达**：活模板 `charter-default` 没有 `model_by_target`，所以现在 model 恒空。
它是**潜伏**缺陷，不是现行缺陷——`ModelByTarget` 这个字段存在的理由正是「模型名按机器
不同」，一旦有人填上，本卡新加的 flag 就是它的第一个触发路径。

**选择做（而不是等它真的可达）**，理由只有一条：这条规则现在活在人的记忆里和一条
（已经过时的）纪律里，成本是三行代码加一条用例，把它写进代码就再也不用记。
弃选是「等 `model_by_target` 真被填了再补」——弃因是那一刻不会有人记起本卡这段讨论。

### 弃选 A：CLI 侧预校验覆盖值

卡上原话是「覆盖值该在派发前对目标机校验而不是等 400」。查证后**这个诉求按字面做是错的**：

- **执行器名**能校验（`GET /api/executor/default?machine=` 返回 `Available[]`），但它是
  **失败最便宜**的那个——`resolveExecutor` 已经回「执行者 %q 未注册（可用: …）」并归入
  `errBadDispatchRequest`，`POST /api/tasks` 当场拒，任务不建，卡上不留 dispatched 事件。
  CLI 再校一遍只是把同一句话提前说，多一次 HTTP 往返换一个已经存在的报错。
- **模型名**是失败最贵的那个（任务会建起来、executor 会拉起来、第一个事件才 400），
  但它**无法校验**——`executor_default.go` 文件头明写「agentd 不认识任何执行器的模型
  名单，没有可判据」。

即：能校的不值得校，值得校的校不了。真正挡住贵的那一半的是上面的成对规则，不是校验。

### 弃选 B：给 `--step` 恢复 `--template` 支持

看似能顺带解决问题（换个模板就换了执行器）。弃因：模板是**节点的属性**，卡钉工作流版本
的整套设计（`StepRunner.nodeFor` 的注释）就是为了让「这张卡这一列该用什么」可复现。
用临时换模板来达成临时换执行器，是拿一个更大的语义去顶一个更小的需求。

### 弃选 C：把 executor/model 做成卡级字段（这张卡以后都用 grok）

弃因 YAGNI：今天的需求是「这一次」，没有证据表明存在「这张卡恒定换执行器」的需要。
真需要时它是另一张卡，且要先回答「与工作流定义谁优先」。

## 用户故事

1. 作为协调者，我想让这一次节点派发换成 grok 跑，敲一个 flag 就行，不必改工作流定义，
   也不必记得改回来。
2. 作为协调者，我换执行器时不给模型名，应当由那个执行器用它自己的默认模型跑起来，
   而不是把上一个执行器的模型名带过去在第一个事件撞 400。
3. 作为协调者，事后看卡的事件流，应当能看出某一轮是用哪个执行器/模型跑的。
4. 作为协调者，`card dispatch` 与裸 `handoff dispatch` 在「换执行器」这件事上应当是
   同一套 flag、同一套语义，不需要记两套。

## 实现决定

1. **优先级顺序与 `Target` 逐字相同**：CLI 字段 > `node.Override` > 模板。不发明新顺序。
2. **成对规则在 `Dispatcher.ViaTemplate` 里收口，不在 CLI 层**：CLI 只负责把值传下来；
   「executor 被覆盖时 model 不继承下层」是派发语义，属于装配层。
   放 CLI 层会漏掉看板按钮那条路径（agentd 侧同样构造 StepRunner）。
3. **两条路径共用同一对 flag 变量**，不为 `--step` 单开一套——同名 flag 两种语义是坑。
4. **`dispatched` 事件的 payload 只加字段不改既有键**，老事件不回填（事件流不可变）。
5. **不新增配置键、不动模板 schema、不动 `NodeOverride`**：本卡是纯 CLI 表面 + 一条
   装配层规则。
6. `--model` 的 flag 说明里写清「空 = 交给执行器自身默认」，与裸 dispatch 的措辞对齐。

## 测试决定（接缝清单）

最高可测缝是 **`Dispatcher.ViaTemplate` 产出的 `DispatchOpts`**——`Transport` 是现成的
可替换缝（`internal/ledgerstep/dispatch_test.go` 已有夹具），不需要起 agentd、不需要真派发。
第二个缝是 `StepRunner.dispatchNode` 的优先级组装（`runner` 层，同样已有夹具）。

必须覆盖：

1. **CLI 覆盖 executor** → `DispatchOpts.Executor` 是 CLI 值，压过节点覆盖与模板；
2. **CLI 覆盖 model** → `DispatchOpts.Model` 是 CLI 值；
3. **成对规则（本卡核心行为）**：模板有 `model_by_target` 命中值、CLI 只给 `--executor`
   不给 `--model` → `DispatchOpts.Model` 为**空**，不是模板值；
4. **成对规则不误伤**：CLI 两个都不给 → executor/model 与今天逐字一致（模板值照常生效）；
5. **节点覆盖层同规则**：`node.Override.Executor` 有值而 `node.Override.Model` 空 →
   同样不继承模板的 `ModelByTarget`；
6. **`dispatched` 事件含 executor/model**；
7. **模板路径（无 `--step`）同样吃这两个 flag**。

第 4 条是防回归的主网：本卡改的是所有节点派发都要走的公共路径。

## Out of Scope

- **模型名校验**——agentd 没有任何执行器的模型名单，无判据。**永不做**（除非将来
  executor adapter 自己上报可用模型，那是另一张卡的前提）。
- **执行器名的 CLI 侧预校验**——服务端已拒且报文已指路，重复。**永不做**。
- **卡级/持久的执行器绑定**（弃选 C）——本期不做，落 roadmap。
- **裸 `handoff dispatch`** 一行不改，它已经有这两个 flag。
- **`NodeOverride` 与工作流定义 schema** 一行不改。
- **`--step` 恢复 `--template`**（弃选 B）——**永不做**，语义冲突。
- **CLAUDE.md 里「改派非缺省执行器时才需显式 `--model`」这条纪律已过时**（服务端修好
  之后留空才是正解）。改纪律不在本卡范围，另记。

## 备注

- 本卡与 B201（产出物自动挂卡）、B185（环节执行体入驻 agentd）同在
  `internal/ledgerstep`。B185 会把这块的宿主整个搬进 agentd；本卡加的是
  `StepRunner` 的两个字段与 `ViaTemplate` 的一条规则，两者都在搬迁时原样跟走，
  不构成 B185 的返工面。
- 实现期需留意 B205 正在 review（同包），合入顺序由协调者定。

## 图覆盖债

无。本轮涉及的符号（`NodeOverride`、`StepRunner`、`DispatchOpts`、`TemplateDispatch`）
在 `codegraph sym` 全部命中。
