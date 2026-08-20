# 节点化工作流设计（B156.2）

日期：2026-08-21。承接 B156.1 工作项账本，对应用户定下的三条发版线：
①前端能查看/编辑一整条工作流；②能给每个节点配执行者和纪律块；③能手动把
一张卡从头推到尾。

## 0. 一句话

把 superpowers 那套流程做成 handoff 的工作流：**节点=规矩**（纪律块+执行者
+能力开关），**卡=事实**（基线分支/验收判据/附件），**派发=本次补充**；
executor 收到的 prompt 由三段拼装。工作流是**数据**不是代码——没有任何预设
节点类型，语义全部由能力开关组合出来，用户随时在控制台改。

## 1. 三层拼装模型

| 层 | 提供什么 | 谁改、何时定 |
|----|---------|-------------|
| 节点 | 模板名（executor/纪律块名/model/target 的复用包）+ 单字段覆盖 + 能力开关 + 路由 | 用户在控制台改，保存即发工作流新版本 |
| 卡 | 标题、验收判据、**基线分支（含祖先继承）**、spec/plan 附件 | 建卡/推进时定，子卡自动继承基线 |
| 派发 | 本次补充说明、个别字段临时覆盖 | 点按钮那一刻，多数为空 |

**合并目标 = 卡的有效基线分支**（`EffectiveBaseBranch` 已有）。「合到 main
还是功能分支」不配在节点上、不在派发时手填：B1.x 子卡继承
`feat/b156-workbench-ledger`，独立小修卡建卡时基线就是 main。节点纪律只写
规矩（「合并目标取卡的基线分支，不越过基线碰别的分支」），值随卡带入。

## 2. 数据模型

```go
// NodeDef 工作流的一个节点 = 看板的一列 + 卡走到这列时的执行规矩。
type NodeDef struct {
	Name     string       `json:"name"`
	Template string       `json:"template,omitempty"` // Dispatch=true 时必填
	Override NodeOverride `json:"override,omitempty"` // 单字段覆盖模板
	// 能力开关——语义由组合出来，没有预设节点类型
	Dispatch         bool `json:"dispatch,omitempty"`           // 会派发任务
	Verdict          bool `json:"verdict,omitempty"`            // 解析裁决块并按结果路由
	CarryCardContext bool `json:"carry_card_context,omitempty"` // 拼入卡上下文段
	MaxRounds        int  `json:"max_rounds,omitempty"`         // Verdict=true 时的轮次封顶，0=1 轮
	// 路由用名字指向（为 DAG 分叉预留），空 = 停在本列等人
	Next   string `json:"next,omitempty"`    // 通过后去哪列
	OnFail string `json:"on_fail,omitempty"` // 未通过退到哪列
	Gate   Gate   `json:"gate,omitempty"`    // 进入本列的门槛（沿用现有 Gate）
}

// NodeOverride 节点对模板的单字段覆盖；零值 = 用模板的。
type NodeOverride struct {
	Executor   string `json:"executor,omitempty"`
	Discipline string `json:"discipline,omitempty"` // 具名纪律块名
	Target     string `json:"target,omitempty"`
	Model      string `json:"model,omitempty"`
}
```

`WorkflowDef` 增 `Nodes []NodeDef`；`States`/`Gates` 保留为只读兼容字段。
**卡钉工作流版本的机制不变**：老版本行必须继续可解码——读取层把只有
States 的老 def 合成为全开关关闭的纯人工节点序列（Gates 并入对应节点）。

`PutWorkflow` 校验（新增）：节点名唯一非空；`Dispatch=true` ⇒ Template 在
模板库存在、解析出的纪律块名在纪律块库存在；`Next`/`OnFail` 指向存在的节点
名或为空；`MaxRounds`/`OnFail` 仅在 `Verdict=true` 时允许非零。

## 3. 能力开关的组合示例（仅示例，不是内置类型）

| 组合 | 习惯语义 |
|------|---------|
| 全关 | 纯人工列（待办、已完成） |
| Dispatch+Carry | 实现：派 codex 带 plan 附件与卡上下文 |
| Dispatch+Verdict+Carry+MaxRounds | 审阅:裁决块过→Next，不过→OnFail，轮次封顶→等人 |
| Dispatch+Verdict+Carry | 收尾合并：纪律块=finishing 改写版，合并目标从卡上下文取 |

## 4. Prompt 三段拼装

1. **模板 Prompt**：占位符沿用 `{{TITLE}}/{{CARD}}/{{ACCEPT}}`。
2. **卡上下文段**（CarryCardContext=true 时拼入）：卡 ID、标题、**有效基线
   分支（明示「本卡合并目标以此为准」）**、验收判据、附件清单（kind + 仓内
   相对路径）。
3. **本次补充段**：派发请求带入的临时说明，空则不拼。

纪律块**仍然只传名字**，正文由 agentd 按 B129 机制注入——绝不在这里拼正文
（两份纪律同场的事故已实测过）。plan 附件：Web 派发实现时从卡的 kind=plan
附件在项目仓库目录解析出内容（替代 CLI 的 PlanPath 本地读；CLI 路径保留）。

## 5. 通用节点执行器

`StepRunner.Run(ctx, cardID, nodeName)` 不再 switch "review"|"merge"，改为：
按卡钉的工作流版本取 NodeDef → `Dispatch=false` 直接拒（纯人工列没有可执行
能力）→ 按「模板+覆盖+三段拼装」派发 → `Verdict=false` 则记录派发即返回 →
`Verdict=true` 则等回合终态、取最终报文、解析裁决块：通过→移卡到 Next；
不通过→退到 OnFail 再来一轮；轮次到 MaxRounds 封顶→打等人标记。裁决落
`review_verdict` 事件、自动撤回本环节旧等人标记（`ClearNeedsHumanFrom`）
两条机制沿用。

**MergeStep 本地合并退役**：合并改为普通派发型节点，走 finishing 纪律块，
由 executor 在任务分支上完成验证与合并动作；本地客观判据执行
（NewLocalObjective/NewLocalMerge）随之删除，验证责任写进 finishing 纪律块
正文（执行者跑测试并落 ledger）。破坏性/不可逆操作照旧走 permission_request
审批链升级给人。

CLI `card step review` 兼容：step 名先按节点名查，查得到走通用执行器。

## 6. Web 界面增量

- **工作流页**（线①②）：查看整条流的节点序列与每节点配置；编辑节点（改
  模板引用、覆盖 executor/纪律块/model/target、开关、路由）；保存 =
  `PutWorkflow` 发新版本（版本化只增天然留审计）。纪律块下拉的候选来自
  纪律块库 API，executor 候选来自机器登记。
- **卡片写操作**（线③）：建卡（标题/项目/优先级/父卡/工作流/基线分支）、
  改标题优先级、写验收判据、挂/摘附件、移动列（gate 校验沿用）、节点执行
  按钮（替代现在写死的 review/merge）、实现派发（取卡上 plan 附件）。
- **抽屉工单入口**：关联执行行变可点，展开该 task 的工单并可 reply——
  `byTask` 中间件已透明代理到属主 target，纯前端复用 TicketsPanel。

新增 API：`PUT /api/flows/{name}`（发新版本）、`GET /api/flows/{name}`、
`POST /api/cards`（建卡）、`PATCH /api/cards/{id}`（标题/优先级/验收判据/
基线）、`POST /api/cards/{id}/attachments` 与 `DELETE .../attachments`、
`GET /api/disciplines`（纪律块名列表，供下拉）；`POST /api/cards/{id}/step`
的 step 参数改为节点名。

## 7. 出厂 seed（数据，可改可删）

- **纪律块库**补三份具名纪律块（superpowers 对应 skill 的改写版正文，中文）：
  `spec-draft`（出 spec）、`plan-writing`（写 plan）、`finishing`（收尾合并，
  含「合并目标取卡基线」规矩）。`implement`/`review` 已有。
- `EnsureDefaultWorkflows` 的 feature/bug 流改为节点形 seed（引用上述纪律块
  与既有模板），仅在同名工作流不存在时写入，不覆盖用户版本。

## 8. 测试

- 老 WorkflowDef 解码兼容：表驱动，States-only 老行 → 合成人工节点。
- 三段拼装 golden：开关开/关、附件有/无、补充段有/无。
- 通用执行器：裁决通过/不通过路由、轮次封顶打等人、纯人工列拒执行。
- `PutWorkflow` 校验各红线的红→绿用例。
- Web 沿用现有 941 用例套件的组件/交互测试模式。

## 9. 不做（YAGNI）

- DAG 分叉的**执行**（只留名字路由的数据形状）；模板 CRUD 的 Web UI（CLI
  `template put` 已有）；把出 spec/写 plan 环节做成强制门（纪律块先 seed，
  工作流里用不用由用户排）。
