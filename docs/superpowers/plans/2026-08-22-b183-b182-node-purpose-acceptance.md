# B183 + B182 实现计划：模板×节点适配缺陷族（用途归节点 + 判据按节点收放）

> 2026-08-22。协调者写，派发给 linux-01 执行。两卡同族同批：charter 流的节点
> 全部引同一份 `charter-default` 模板，于是**凡是按模板属性裁决的行为，节点
> 一律拿不到自己的语义**。B183 是「审阅路径按模板 Purpose 判」，B182 是
> 「整卡实现级判据灌给每个节点」。根因同一条：**模板是复用物，节点才知道
> 这一列要干什么**。
>
> 卡：B183（高，承载卡）、B182（中，已并入 B183）。

## 事实基线（协调者动手前已在 985f37135 上查证）

- `internal/ledgerstep/dispatch.go` 审阅专用路径的唯一入口判据是
  `tpl.Def.Purpose == ledger.PurposeReview`。
- charter 工作流 v3 的 review 节点：`template=charter-default`（该模板
  `purpose=charter`）、`override.discipline=charter-review`。故审阅路径恒不生效。
  实测取证：B175 的两条 dispatched 事件 purpose 均为 `charter`，审阅轮分支是
  `cards/B175-charter-2`（从卡基线新开），而不是 `cards/B175-review-1`。
- `purpose` 不只决定分支名，还决定另外三件事：`ledger.WorkBranch` 靠它把审阅轮
  排除在「卡的工作分支」之外；`PurposeRounds` / `ReviewRounds` 靠它给轮次挂号；
  审阅轮的 base 取工作分支也由它触发。**只改分支计算不改 purpose 会留下第二个
  洞**——审阅分支会被后续节点当成卡的工作分支。
- `charter-default` v2 的 prompt 对所有节点注入 `{{ACCEPT}}`；`carry_card_context`
  开着的节点在卡上下文段里**再**注入一遍验收判据。B171（判据字段为空）未越轨、
  B175（判据非空）越轨，差异变量就是它。

## 设计决定（执行者按此实现，不要另起炉灶）

1. **用途做成节点对模板的单字段覆盖**（`NodeOverride.Purpose`），不要给节点新加
   「审阅开关」。purpose 已经是四处行为共同的轴，覆盖它一处四处同时正确；再造
   一个布尔开关等于把同一件事表达两遍，早晚不同步。形态与既有
   `NodeOverride.{executor,target,model,discipline}` 一致，**不引入「节点类型」**
   （`NodeDef` 的注释里写死了这条设计约束，别破它）。
2. **判据注入做成节点开关 `NodeDef.OmitAcceptance`**，同时管住两个注入点
   （模板的 `{{ACCEPT}}` 占位符 + 卡上下文段的验收判据行）。只堵一个等于没堵。
3. **不静默丢判据**：开关打开时 `{{ACCEPT}}` 替换成一句显式说明，而不是空串——
   空串会让模板正文出现「验收判据：」后面什么都没有。
4. **默认行为一字不改**：两个字段零值即旧行为。既有用例就是回归网。
5. **线上账本不动**：charter v4 的定义只落成仓内 JSON + 说明，等真机验收时再
   `workflow put`。新版本一旦写入，新建卡即钉 v4，而修好的二进制还没部署。

## Task 1（B183）：用途归节点

**文件 1：`internal/ledger/types.go`** —— `NodeOverride` 结构体末尾加字段：

```go
	// Purpose 覆盖模板的派发用途（implement / review / ...）。
	//
	// why 用途必须能按节点覆盖：模板是**复用物**（一条流的十个节点常引同一份
	// 模板），而用途是**这一列要干什么**。派发期有四处行为按用途裁决——分支
	// 命名、审阅轮的基线取工作分支、卡的工作分支归属（WorkBranch 跳过审阅
	// 轮）、重跑轮次挂号——节点拿不到自己的用途时，这四处会一起判错。
	// 2026-08-22 真机实测：charter 流 review 节点引的是 purpose=charter 的
	// 通用模板，于是审阅轮从卡基线开了条新分支，执行者在空分支上把实现又写
	// 了一遍，等于从未审阅过工作分支（B183）。
	Purpose string `json:"purpose,omitempty"`
```

**文件 2：`internal/ledgerstep/dispatch.go`**

2.1 `TemplateDispatch` 里 `CarryCardContext` 字段之后加：

```go
	// PurposeOverride 覆盖模板的派发用途；空 = 用模板的。
	// 用途决定分支命名、审阅基线、工作分支归属与轮次挂号四件事，见
	// ledger.NodeOverride.Purpose 的注释。
	PurposeOverride string
	// OmitAcceptance 为真时不把整卡验收判据注入 prompt（来自节点的同名开关）。
	OmitAcceptance bool
```

2.2 `ViaTemplate` 里，原来的 `body := strings.NewReplacer(...)` 这一段整体替换为：

```go
	// 有效用途：节点覆盖优先于模板。下面**所有**按用途裁决的地方都读它，
	// 不再读 tpl.Def.Purpose——漏掉任何一处都会让节点只对了一半（例如分支
	// 名对了但快照里记的还是模板用途，WorkBranch 于是把审阅分支当成工作分支）。
	purpose := tpl.Def.Purpose
	if req.PurposeOverride != "" {
		purpose = req.PurposeOverride
	}

	// 判据被收起时不留空冒号：模板正文里「验收判据：{{ACCEPT}}」后面跟一片
	// 空白，比说明白更让执行者困惑。
	acceptance := c.AcceptanceCriteria
	if req.OmitAcceptance {
		acceptance = "（本节点不注入整卡验收判据——那是实现级的最终判据；本节点的产出物与 pass 依据以纪律块为准）"
	}
	body := strings.NewReplacer(
		"{{TITLE}}", c.Title,
		"{{CARD}}", c.ID,
		"{{ACCEPT}}", acceptance,
	).Replace(tpl.Def.Prompt)
```

2.3 同函数内，把余下**全部** `tpl.Def.Purpose` 引用改读 `purpose`，共五处：

- `branch := fmt.Sprintf("%s/%s-%s", tpl.Def.BranchPrefix, c.ID, purpose)`
- `if purpose == ledger.PurposeReview {`
- `rounds, err := d.St.PurposeRounds(c.ID, purpose)` 与其错误文案 `fmt.Errorf("取 %s 轮次: %w", purpose, err)`
- 「重跑轮分支挂号」日志的 `"purpose", purpose`
- `d.St.LinkTask(c.ID, target, taskID, purpose, d.Actor)`
- `ledger.DispatchSnapshot{... Purpose: purpose, ...}`

改完 `grep -n "tpl.Def.Purpose" internal/ledgerstep/dispatch.go` **必须零命中**。

2.4 「按模板派发」日志加三项（现场只看这一行就要答得出走没走审阅路径）：

```go
		"purpose", purpose, "purpose_overridden", req.PurposeOverride != "",
		"omit_acceptance", req.OmitAcceptance,
```

**文件 3：`internal/ledgerstep/runner.go`** —— `dispatchNode` 的 `TemplateDispatch`
字面量里加两行：

```go
			PurposeOverride:    node.Override.Purpose,
			OmitAcceptance:     node.OmitAcceptance,
```

## Task 2（B182）：判据按节点收放

**文件 1：`internal/ledger/types.go`** —— `NodeDef` 的 `MaxRounds` 之后加：

```go
	// OmitAcceptance 为真时，本节点的 prompt 不注入整卡的验收判据。
	//
	// why 需要这个开关：验收判据通常是**实现级**的（测试全绿、真机跑通），
	// 而计划/拆解类节点的法定产出是文档。两者同时在场时，「pass 的依据是你
	// 真实跑到的结果」这条裁决契约在计划节点上无解，执行者化解矛盾的方式是
	// 直接把实现做掉——2026-08-22 真机实测过一次（B182）；对照组是同一条流上
	// 判据字段为空的卡，同一个执行者没有越轨。
	OmitAcceptance bool `json:"omit_acceptance,omitempty"`
```

**文件 2：`internal/ledgerstep/dispatch.go`** —— `buildPrompt` 增参数并改一处判断：

- 调用点：`prompt := buildPrompt(body, c, cardBase, req.CarryCardContext, req.OmitAcceptance, req.Extra)`
- 签名：`func buildPrompt(body string, c ledger.Card, base string, carry, omitAccept bool, extra string) string {`
- 函数头注释的参数表里补一行：`//   - omitAccept: 是否**不**注入整卡验收判据（节点的 OmitAcceptance 开关）`
- 判据行：

```go
		// 判据有两个注入通道（模板的 {{ACCEPT}} 与这一段），开关必须同时管住
		// 两个——只堵一个等于没堵，charter 流的节点两个通道都开着。
		if c.AcceptanceCriteria != "" && !omitAccept {
			fmt.Fprintf(&b, "- 验收判据：\n%s\n", indentLines(c.AcceptanceCriteria, "  "))
		}
```

`internal/ledgerstep/dispatch_test.go` 里既有 5 处 `buildPrompt(...)` 调用补一个
`false` 实参（位置在 `carry` 之后），语义不变——这 5 条是回归网，**断言一个字都
不许改**。

## Task 3：控制台节点编辑器跟上

这两个字段是**用户配的**，控制台是唯一配置界面；只加后端等于加了个没人打得开的开关。

**`web/src/api/ledger.ts`**：`NodeOverride` 加 `purpose?: string`，`NodeDef` 加
`omit_acceptance?: boolean`，各带一行中文注释说明用途（字段名与 Go tag 一字不差）。

**`web/src/app/flows/NodeEditor.tsx`**：

- 文件顶部 `routeOptions` 之前加候选表与并入函数：

```tsx
// 用途候选：review 会让派发走审阅路径（基线取卡的工作分支、开一次性分支、
// 不算作卡的工作分支）；implement 是普通实现轮。用户自建的用途照样存得下，
// 所以当前值不在候选里时把它并进去，避免打开编辑器就被静默改掉。
const knownPurposes = ['implement', 'review']

function purposeOptions(current?: string): string[] {
  return current && !knownPurposes.includes(current) ? [current, ...knownPurposes] : knownPurposes
}
```

- 能力开关那一排（`node.dispatch === true` 的分支里，「携带卡上下文」之后）加一个
  复选框，label 文案 **`不注入验收判据`**，`checked={node.omit_acceptance === true}`，
  onChange 写 `update({ omit_acceptance: event.target.checked || undefined })`
  ——取消勾选要把字段清成 `undefined` 而不是写 `false`（与既有 `require_acceptance`
  同款语义，后端 `omitempty` 才不会存一堆无意义的 false）。
- 模板/纪律块那个 grid 里，`['executor','target','model']` 的 map 之前加一个
  label 为 **`用途`** 的 `<select>`：值取 `node.override?.purpose ?? ''`，
  onChange 走既有的 `updateOverride('purpose', event.target.value)`（选空会自动
  把字段删掉，这是既有行为），选项是 `（沿用模板）` + `purposeOptions(...)`。

## 测试映射（每条都要真跑到绿，不许只写不跑）

**`internal/ledgerstep/dispatch_test.go`** 新增两条：

1. `TestViaTemplateNodePurposeTakesReviewPath`：用 `feature-impl` 模板
   （purpose=implement）先派一轮建立工作分支，再带 `PurposeOverride:
   ledger.PurposeReview` 派第二轮。四层断言缺一不可：
   ① 分支 == `cards/<卡>-review-1`；② `Base` == 第一轮的分支；
   ③ `ResolveDefaultBase` 为 false；④ 穿序列化边界——`st.ReviewRounds(card.ID)`
   == 1 且 `st.WorkBranch(card.ID)` 仍等于第一轮分支。
2. `TestViaTemplateWithoutPurposeOverrideKeepsTemplatePurpose`：无覆盖时分支仍是
   `cards/<卡>-implement`、`Base` 为空且 `ResolveDefaultBase` 为 true。
3. `TestViaTemplateOmitAcceptanceWithholdsCriteria`：两个子测。卡先
   `st.SetAcceptance(card.ID, "go test ./... 全绿且真机跑通", "test")` 再重新
   `GetCard` 取回。开关打开时 prompt **不含**判据原文、且**含**「本节点不注入整卡
   验收判据」；开关关闭时判据原文在 prompt 里出现**恰好 2 次**（两个通道各一次）。

**`internal/ledgerstep/runner_test.go`** 新增 `TestRunnerPassesNodePurposeAndAcceptanceSwitch`：
`PutWorkflow` 造一条含审阅节点的流（`Dispatch: true, Template: "feature-impl",
CarryCardContext: true, OmitAcceptance: true, Override: ledger.NodeOverride{Purpose:
ledger.PurposeReview}`），卡设判据，先直接 `ViaTemplate` 派一轮实现建立工作分支，
再 `StepRunner.Run` 跑该节点，在 Transport 里断言分支是 `-review-1`、prompt 不含判据。

**`internal/ledger/workflows_test.go`** 新增 `TestWorkflowNodeCarriesPurposeAndAcceptanceSwitch`：
`PutWorkflow` 写入带这两个字段的节点，`GetWorkflow` 读回后断言两个字段都还在
（穿 workflow 定义的 JSON 序列化边界——存进去读不回来等于控制台上配了个不生效的开关）。

**`web/src/app/flows/NodeEditor.purpose.test.tsx`** 新增四条：选 review 写进
`override.purpose`；选回空清掉 `override`；用户自建用途（如 `recon`）打开编辑器
不被静默改掉；勾选/取消「不注入验收判据」写 `true` / 清成 `undefined`；以及
`dispatch` 关掉时两个控件都不渲染。

## 测试范围（按 charter:implement 三段律，别跑全量）

- 触及包：`go test ./internal/ledgerstep/ ./internal/ledger/`
- 编译面：`go build ./...` + `go vet ./...`
- 格式：`gofmt -l .` **必须无输出**（执行者的 ledger 漏 gofmt 是有先例的）
- 前端：`cd web && npx tsc -b && npx eslint src/app/flows/NodeEditor.tsx src/api/ledger.ts && npx vitest run src/app/flows/`

## 四项检查（缺陷族对抗，出稿时已过）

1. **序列化边界**：两个新字段各有一条穿边界断言（purpose 穿 LinkTask/快照后由
   `WorkBranch` 读回；`omit_acceptance` 穿 workflow 定义的存取）。
2. **默认值/空洞**：零值 = 旧行为，既有用例即回归网；新用例不得靠改既有断言变绿。
3. **前后端字段名一致**：TS 接口与 Go tag 逐字对齐。
4. **可观测性**：派发日志新增 purpose / purpose_overridden / omit_acceptance 三项。

## 不属于本次的（看见了也不要顺手做）

- **charter 流各节点分支不接续**（每个节点都从卡基线新开分支，plan 轮产出的计划
  文档不在 implement 轮的工作树里）——这是同族的另一个缺陷，协调者另开卡追踪。
  本次只修审阅路径与判据注入。
- **不要写 charter v4 到账本**（`workflow put` / `template put` 一律不碰）。
- 不要动 `docs/superpowers/backlog.md`（已冻结）。
