# 无人值守推进状态（2026-08-20 起）

> 上下文耗尽后从这里接上。授权：两批都做完，我自己裁决，只有真危险才停。

## 用户定死的模型（2026-08-21 对齐后的最终版，不要再重新设计）

**根本目的**：把 superpowers 那一套流程做成 handoff 工作流——每个节点带一份
可微调的纪律块、配上合适的执行者；但**整个工作流不写死在代码里**，AI coding
变化快，用户要能随时改。

**三层拼装**（executor 收到的 prompt = 三段拼起来）：

1. **节点 = 规矩（怎么干）**：绑模板名 + 可覆盖单字段（executor/discipline/
   model/target），外加能力开关组合出语义——dispatch（派不派）、verdict
   （解析裁决块并路由）、**carry_card_context（带不带卡上下文）**、回合上限、
   next/on_fail（按名字指向，为 DAG 预留）、进入门槛（沿用 Gate）。
   **没有任何预设节点类型**，语义全部由开关组合出来。
2. **卡 = 事实（这件事）**：标题、验收判据、**基线分支（含祖先继承，
   EffectiveBaseBranch 已有）**、spec/plan 附件、子卡。
   **合并目标 = 卡的基线分支**——「合到 main 还是功能分支」不配在节点上、
   也不在派发时手填，建卡时就定了（子卡自动继承）。
3. **派发时补充 = 本次的临时说明**（可空）：额外要求、个别字段覆盖。

**修订记录**：旧版「合并三选一由节点配置预先钉死」已被用户修订——节点只配
纪律（如「合并目标取卡的基线分支；不许越过基线碰别的分支；推送前必须验证」），
具体分支值随卡上下文带入。合并环节是普通节点：纪律块 = finishing 收尾流程
改写版，carry_card_context 必开。

**纪律块库**：具名纪律块（B156.1 已具名化）就是节点纪律的载体；出厂 seed
superpowers 各阶段的改写版（出 spec/写 plan/实现/审阅/收尾合并），全部可在
控制台编辑微调，seed 只是数据不是代码语义。

## 批次

- 批1：Web 卡片写操作（建卡/改卡/挂摘附件/派发/抽屉答工单）
- 批2：节点化工作流（NodeDef + 开关 + 三段拼装；需先出 spec）

## 进度

- [x] main 已合进 feat/b156-workbench-ledger（8940c875c）
- [x] 需求对齐（2026-08-21，三层拼装模型定稿）
- [x] 批2 spec：`docs/superpowers/specs/2026-08-21-node-workflow-design.md`
- [x] 批2 plan：`docs/superpowers/plans/2026-08-21-node-workflow-backend.md`（8 task）
- [x] 批2 已派发 → **task 3ae97099-03d8-455c-a8a9-d95f5fe841ec**
      linux-01 + codex，分支 `feat/b156.2-node-workflow`，起点 777971b，
      纪律块「内置:single-context」（档位正确），model 空=机器默认。
      协调者已挂 `wait --follow` 订阅。
- [ ] 批2 验收（审核者本地做：真机跑一张卡走全程）
- [ ] 批1 plan（Web）——**必须等批2 落地后再派**，两者改同一分支会冲突
- [ ] 批1 派发/验收
- [x] 隔离 demo 已清理（进程无、/tmp/acc 已删）

## 约束

- 分支 `feat/b156-workbench-ledger`，**不碰 main**
- 派发目标 linux-01 + codex，**不传模型参数**（机器默认）
- 纪律块由 agentd 自动注入，不要手工拼进 plan
- 需要驱动 handoff 自身的验收步骤**留本地**，不写进派发的 plan
- 老 WorkflowDef（States/Gates）必须继续可解码——卡钉版本，旧行不能失读
