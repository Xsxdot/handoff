# B201 产文档节点产出物自动挂卡：实现计划

> **【给 implement 节点执行者的第一句话，2026-08-23 协调者补】**
> **本文档是实现计划，读完它你要写的是 Go 代码，不是文档。**
> 本卡的**主题**恰好是「产文档节点的产出物」——但那是被实现的**功能**，
> 不是**你这一轮的交付物**。08-23 的第一轮 implement 在这里判错了：它读完计划后
> 认定「这是产出实现计划的文档节点」，只提交了一个台账文件就报 pass，实现零行。
> 判别方法很简单：**plan 附件已经挂在卡上了，说明计划阶段早已结束**；
> 你现在站在 implement 列，法定产出是下面五个 task 对应的代码与测试。
> 只提交文档或台账而不动 Go 代码，一律不算 pass。


## 交付边界与冻结决定

本计划只覆盖 docs/superpowers/specs/2026-08-23-b201-node-output-attach.md 已批准的单 kind、单 path 方案；不改变执行者纪律、不校验文档内容、不补挂历史卡、不处理 B205 的工作树可达性。实现必须在 B205 合入后开始，原因是 spec 已记录两者都会触及 internal/ledgerstep 与卡更新路径，但设计没有依赖关系。

跨任务的冻结接口如下，名称与签名必须逐字保持一致：

| 生产方 | Produces | 消费方 |
| --- | --- | --- |
| internal/ledger.NodeDef | Produces *NodeOutput；NodeOutput 为 Kind string、Path string | internal/ledgerstep.NodeStep.RunOnce、internal/agentd wire、Web NodeEditor |
| ledgerstep.RenderOutputPath | func RenderOutputPath(template string, card ledger.Card, node ledger.NodeDef, now time.Time) string | StepRunner.dispatchNode |
| ledgerstep.ChangedPaths | func ChangedPaths(diff string) []string | StepRunner.diffNode 与 NodeStep 输出存在性判定 |
| (*ledger.Store).AttachFile | func (s *Store) AttachFile(cardID, kind, path, actor string) error | NodeStep |
| (*client.Client).Diff | func (c *Client) Diff(ctx context.Context, taskID, base string) (string, error) | StepRunner.diffNode |
| (*ledgerstep.Dispatcher).ViaTemplate | 既有 func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)；新增 TemplateDispatch.OutputPath string | StepRunner.dispatchNode |
| buildPrompt | func buildPrompt(body string, c ledger.Card, base string, carry, omitAccept bool, extra, outputPath string) string | ViaTemplate 与既有单元测试 |
| HTTP projection | ledgerNodeWire.Produces *proto.NodeOutput 与 proto.NodeDef.Produces *proto.NodeOutput | /api/flows GET/PUT、Web API 类型 |
| Web API | type NodeOutput = { kind: string; path: string }；NodeDef.produces?: NodeOutput | NodeEditor |

Produces 的 kind 一律取自现有附件白名单 `spec|plan|doc|contract`（`internal/agentd/ledgerapi.go` 的 `attachmentKinds`），**不新增枚举**：contract 节点产 `contract`，plan 节点产 `plan`（`plan` 本来就在白名单里），breakdown 节点没有专属 kind，产 `doc`。

**【协调者更正 2026-08-23】本段原文写的是「kind 一律用 doc」并要求「charter 的 plan gate 从 spec 改为 doc」，两处都作废：**

1. 原文的理由（白名单只接受那四个所以只能用 doc）**前提就是错的**——`plan` 就在那四个里面，不需要绕道。
2. 原文与本文档第 5 个 task 的配置段（contract 用 `contract`、plan 用 `plan`）**自相矛盾**，以配置段为准。
3. **plan 列的 gate 必须保持 `require_attachment=spec`，一个字都不许改。** 原文只考虑了 L3 链（spec→contract→breakdown→plan），而 **L2 卡是带着 spec 从 triage 直接跳进 plan 列的**——2026-08-23 的 B203 与 B201 两张卡都是这么进来的，靠的正是这道 spec 门。改成 `doc` 会让**今后所有 L2 卡都进不了 plan 列**，而 `Gate.RequireAttachment` 是单值、表达不了「spec 或 doc」。breakdown 产出的 `doc` 照挂（附件留痕有价值），但**不作为门**。

## 基线证据与最小测试边界

动手前已经真实跑过并写入 docs/superpowers/ledgers/2026-08-23-b201-ledger.md 的基线判据：

- go test ./internal/ledger -run 'TestWorkflow(LegacyDefStillDecodes|NodesProjectToStates|NodeCarriesPurposeAndAcceptanceSwitch|PutWorkflowAcceptsGoodNodes)' -count=1：通过。
- go test ./internal/ledgerstep -count=1：通过。
- go test ./internal/proto -run TestContractFixtures -count=1：通过。
- go test ./internal/agentd -run 'Test(FlowGetReturnsNodes|FlowPutCreatesNewVersion|AttachmentKindsCoverDefaultWorkflowGates)' -count=1：通过。
- go build ./...、go vet ./internal/ledger ./internal/ledgerstep ./internal/proto ./internal/client：均退出 0。
- gofmt -l 对本卡触及的 Go 文件无输出，git diff --check 无输出。
- web 的 npm run typecheck 与指定 vitest 基线因 tsc 不存在和 npm cache 的 EROFS 失败，原始输出已入台账；实现后需在依赖可用环境重跑，不能把未跑到的 Web 结果写成通过。
- codegraph 和 B201 视图命令在当前工作树不可用，现状签名按源码和 rg 核对；计划以源码行号作为覆盖债，不凭记忆改签名。

实现期每个 task 只跑列出的包测试；全量 go test ./...、全量 Web 测试属于 implement 三段律的最终整体验收，不归任何单个 task。

## Task 1：账本模型、版本化存储、合法性与 round-trip

### 文件范围

- internal/ledger/types.go
- internal/ledger/workflows.go
- internal/ledger/workflows_test.go

### Interfaces

Consumes：

```go
type NodeDef struct {
    Name string
    Template string
    Override NodeOverride
    Dispatch bool
    Verdict bool
    CarryCardContext bool
    MaxRounds int
    OmitAcceptance bool
    Next string
    OnFail string
    Gate Gate
    HumanBases []string
}
func (d WorkflowDef) withStatesFromNodes() WorkflowDef
func (s *Store) PutWorkflow(name string, def WorkflowDef) (int, error)
func (s *Store) GetWorkflow(name string, version int) (Workflow, error)
```

Produces：

```go
type NodeOutput struct {
    Kind string `json:"kind"`
    Path string `json:"path"`
}
type NodeDef struct {
    Name string
    Template string
    Override NodeOverride
    Dispatch bool
    Verdict bool
    CarryCardContext bool
    MaxRounds int
    OmitAcceptance bool
    Next string
    OnFail string
    Gate Gate
    HumanBases []string
    Produces *NodeOutput `json:"produces,omitempty"`
}
```

### 1. 基线判据先跑

在修改前于仓库根目录执行：

```sh
go test ./internal/ledger -run 'TestWorkflow(LegacyDefStillDecodes|NodesProjectToStates|NodeCarriesPurposeAndAcceptanceSwitch|PutWorkflowAcceptsGoodNodes)' -count=1
```

预期是既有测试通过，输出以 ok github.com/Xsxdot/handoff/internal/ledger 开头。若失败，保留原始输出并停止本 task 的实现，不把失败归因于本卡。

### 2. 写失败测试

在 internal/ledger/workflows_test.go 追加以下完整测试。它同时覆盖 JSON 缺失与显式零值的可空区分，以及旧 States 形态读出时不虚构产出声明：

```go
func TestWorkflowNodeProducesRoundTripAndPresence(t *testing.T) {
    s := newTestStore(t)
    if err := s.EnsureDefaultTemplates(); err != nil {
        t.Fatalf("seed 模板: %v", err)
    }
    want := &NodeOutput{Kind: "doc", Path: "docs/superpowers/specs/b201-breakdown.md"}
    if _, err := s.PutWorkflow("produces", WorkflowDef{Nodes: []NodeDef{{
        Name: "breakdown", Dispatch: true, Template: "feature-impl", Produces: want,
    }}}); err != nil {
        t.Fatalf("写带产出的工作流: %v", err)
    }
    got, err := s.GetWorkflow("produces", 0)
    if err != nil {
        t.Fatalf("读带产出的工作流: %v", err)
    }
    if got.Def.Nodes[0].Produces == nil || *got.Def.Nodes[0].Produces != *want {
        t.Fatalf("产出声明未 round-trip: %+v", got.Def.Nodes[0].Produces)
    }

    var missing NodeDef
    if err := json.Unmarshal([]byte("{\"name\":\"plan\"}"), &missing); err != nil {
        t.Fatalf("解码缺失字段: %v", err)
    }
    if missing.Produces != nil {
        t.Fatalf("字段缺失必须保持 nil: %+v", missing.Produces)
    }

    var zero NodeDef
    if err := json.Unmarshal([]byte("{\"name\":\"plan\",\"produces\":{\"kind\":\"\",\"path\":\"\"}}"), &zero); err != nil {
        t.Fatalf("解码显式零值: %v", err)
    }
    if zero.Produces == nil || zero.Produces.Kind != "" || zero.Produces.Path != "" {
        t.Fatalf("显式零值必须保留为非 nil 指针: %+v", zero.Produces)
    }
}

func TestWorkflowRejectsIncompleteProduces(t *testing.T) {
    cases := []struct {
        name string
        output *NodeOutput
    }{
        {name: "missing kind", output: &NodeOutput{Path: "docs/x.md"}},
        {name: "missing path", output: &NodeOutput{Kind: "doc"}},
        {name: "all empty", output: &NodeOutput{}},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            s := newTestStore(t)
            if err := s.EnsureDefaultTemplates(); err != nil {
                t.Fatalf("seed 模板: %v", err)
            }
            _, err := s.PutWorkflow("invalid-"+tc.name, WorkflowDef{Nodes: []NodeDef{{
                Name: "node", Dispatch: true, Template: "feature-impl", Produces: tc.output,
            }}})
            if err == nil || !errors.Is(err, ErrBadState) || !strings.Contains(err.Error(), "produces") {
                t.Fatalf("不完整 produces 未按 ErrBadState 拒绝: %v", err)
            }
        })
    }
}

func TestLegacyWorkflowNodeHasNoProduces(t *testing.T) {
    s := newTestStore(t)
    if _, err := s.PutWorkflow("legacy-produces", WorkflowDef{
        States: []string{"待办", "完成"},
    }); err != nil {
        t.Fatalf("写 legacy 工作流: %v", err)
    }
    got, err := s.GetWorkflow("legacy-produces", 0)
    if err != nil {
        t.Fatalf("读 legacy 工作流: %v", err)
    }
    for _, node := range got.Def.Nodes {
        if node.Produces != nil {
            t.Fatalf("legacy 节点不应凭空带 produces: %+v", node)
        }
    }
}
```

失败的具体预期：当前没有 NodeOutput/Produces 类型而不能编译；完成字段和校验后同一命令应变绿。

### 3. 最小实现

在 internal/ledger/types.go 的 NodeDef 前新增职责注释和 NodeOutput：

```go
// NodeOutput 是节点在本轮必须写出的单一附件声明。
// Kind 复用附件白名单；Path 是仓内相对路径模板，由派发前渲染。
// nil 表示该节点不声明产出，保持旧工作流行为。
type NodeOutput struct {
    Kind string `json:"kind"`
    Path string `json:"path"`
}
```

在 NodeDef 的能力开关之后加入：

```go
// Produces 为真时，节点裁决 pass 后由协调者按声明路径检查本轮 diff 并挂附件。
// 注意：这里只保存声明，不在账本层读取文件系统或验证文档内容。
Produces *NodeOutput `json:"produces,omitempty"`
```

在 internal/ledger/workflows.go 的 validateNodes 每个节点基础字段校验区加入：

```go
if node.Produces != nil {
    if strings.TrimSpace(node.Produces.Kind) == "" || strings.TrimSpace(node.Produces.Path) == "" {
        return fmt.Errorf("节点 %q 的 produces 必须同时填写 kind 和 path: %w",
            node.Name, ErrBadState)
    }
}
```

保持 withStatesFromNodes 只投影 States/Gates，不把 Produces 丢掉；GetWorkflow 的 JSON 解码天然保留 nil 与非 nil 空对象的区别。PutWorkflow 成功日志增加 produces_nodes 计数，失败日志必须包含 name 与原始校验错误；该日志是成功路径和错误分支的关键节点，使用现有结构化 log()，不使用 print。计数函数为：

```go
func countProducesNodes(nodes []NodeDef) int {
    n := 0
    for _, node := range nodes {
        if node.Produces != nil {
            n++
        }
    }
    return n
}
```

把既有成功日志的 dispatch_nodes 后加入 produces_nodes；不要改变旧日志字段含义。

### 4. 注释与验证

- 新导出的 NodeOutput 写职责、kind/path 边界和 nil 语义注释；Produces 写只声明、不读文件系统的原因。
- 运行最小范围：

```sh
gofmt -w internal/ledger/types.go internal/ledger/workflows.go internal/ledger/workflows_test.go
go test ./internal/ledger -run 'TestWorkflow(NodeProducesRoundTripAndPresence|RejectsIncompleteProduces|LegacyWorkflowNodeHasNoProduces|LegacyDefStillDecodes|NodesProjectToStates|NodeCarriesPurposeAndAcceptanceSwitch|PutWorkflowAcceptsGoodNodes)' -count=1
```

预期输出为 ok github.com/Xsxdot/handoff/internal/ledger；再运行 git diff --check，预期无输出、退出 0。只触及 internal/ledger 测试，不跑全量。

### 5. 缺陷族验收

- 缺失/零值族：nil 与 &NodeOutput{Kind:"",Path:""} 的反序列化断言分别可判定。
- 兼容族：States-only 旧工作流仍能读出节点，所有 Produces 为 nil。
- 非法输入族：kind/path 任一空白被 ErrBadState 拦截。
- 持久化族：Put→SQLite JSON→Get round-trip 断言 exact kind/path。
- 可观测性族：成功计数、校验失败上下文均由结构化 logger 覆盖。

本 task 提交前只提交本 task 代码与测试的事实；本节点最终提交统一在计划全部落盘后完成，不派发任何任务。

## Task 2：路径模板与 diff 路径解析纯函数

### 文件范围

- internal/ledgerstep/output.go（新文件）
- internal/ledgerstep/output_test.go（新文件）

### Interfaces

Consumes：

```go
type Card struct {
    ID string
    Title string
    // 只读取 ID；其余既有定义保持不变。
}
type NodeDef struct {
    Name string
}
```

Produces：

```go
func RenderOutputPath(template string, card ledger.Card, node ledger.NodeDef, now time.Time) string
func ChangedPaths(diff string) []string
func changedPathsText(paths []string) string
```

### 1. 基线判据先跑

在修改前执行：

```sh
go test ./internal/ledgerstep -run 'Test(ReviewStepPassAndFailLoop|NodeStepDispatchOnlyReturnsDispatched|NodeStepVerdictRoutesOnPass)' -count=1
```

预期输出为 ok github.com/Xsxdot/handoff/internal/ledgerstep。该命令已在基线全包通过；若复跑失败，保留原始输出，不假定是本 task 造成。

### 2. 写失败测试

新建 internal/ledgerstep/output_test.go，完整内容如下：

```go
package ledgerstep

import (
    "strings"
    "testing"
    "time"

    "github.com/Xsxdot/handoff/internal/ledger"
)

func TestRenderOutputPath(t *testing.T) {
    now := time.Date(2026, 8, 23, 14, 5, 6, 0, time.FixedZone("CST", 8*60*60))
    got := RenderOutputPath(
        "docs/{{DATE}}/{{CARD_LOWER}}-{{CARD}}-{{NODE}}.md",
        ledger.Card{ID: "B201"},
        ledger.NodeDef{Name: "plan"},
        now,
    )
    want := "docs/2026-08-23/b201-B201-plan.md"
    if got != want {
        t.Fatalf("RenderOutputPath = %q, want %q", got, want)
    }
}

func TestRenderOutputPathLeavesUnknownPlaceholderLiteral(t *testing.T) {
    got := RenderOutputPath("docs/{{UNKNOWN}}.md", ledger.Card{ID: "B201"}, ledger.NodeDef{Name: "plan"}, time.Time{})
    if got != "docs/{{UNKNOWN}}.md" {
        t.Fatalf("unknown placeholder changed: %q", got)
    }
}

func TestChangedPaths(t *testing.T) {
    diff := strings.Join([]string{
        "diff --git a/docs/old.md b/docs/new.md",
        "similarity index 90%",
        "rename from docs/old.md",
        "rename to docs/new.md",
        "diff --git a/docs/modified.md b/docs/modified.md",
        "diff --git a/docs/added.md b/docs/added.md",
        "new file mode 100644",
        "diff --git a/docs/deleted.md b/docs/deleted.md",
        "deleted file mode 100644",
        "commit abc1234",
        "Author: ignored",
    }, "\n")
    got := ChangedPaths(diff)
    for _, want := range []string{"docs/old.md", "docs/new.md", "docs/modified.md", "docs/added.md", "docs/deleted.md"} {
        found := false
        for _, path := range got {
            if path == want {
                found = true
                break
            }
        }
        if !found {
            t.Fatalf("ChangedPaths missing %q: %v", want, got)
        }
    }
    for _, path := range got {
        if strings.Contains(path, "commit") || strings.Contains(path, "Author") {
            t.Fatalf("metadata leaked as path: %v", got)
        }
    }
}

func TestChangedPathsText(t *testing.T) {
    if got := changedPathsText(nil); got != "（无）" {
        t.Fatalf("empty changed paths = %q", got)
    }
    got := changedPathsText([]string{"docs/a.md", "docs/b.md"})
    if got != "docs/a.md\ndocs/b.md" {
        t.Fatalf("changed paths text = %q", got)
    }
}
```

失败预期是新符号不存在而编译失败。实现后的测试要直接覆盖 rename old/new、modified、new、deleted，以及提交元数据不被当成路径；路径文本为空时必须明确显示（无）。

### 3. 最小实现

internal/ledgerstep/output.go 文件头写职责和边界：只做路径模板渲染与已返回 diff 的路径投影，不访问网络、不读文件、不猜产出物。实现下面的完整行为：

```go
package ledgerstep

import (
    "strings"
    "time"

    "github.com/Xsxdot/handoff/internal/ledger"
)

// RenderOutputPath 将工作流声明中的四个占位符渲染成一次派发确定的路径。
// 参数：template 声明模板；card/node 提供卡号与节点名；now 提供派发日期。
// 返回：只做字符串替换；未知占位符原样保留，便于配置错误在 diff 校验时显式暴露。
func RenderOutputPath(template string, card ledger.Card, node ledger.NodeDef, now time.Time) string {
    replacements := map[string]string{
        "{{CARD}}": card.ID,
        "{{CARD_LOWER}}": strings.ToLower(card.ID),
        "{{NODE}}": node.Name,
        "{{DATE}}": now.Format("2006-01-02"),
    }
    return strings.NewReplacer(
        "{{CARD}}", replacements["{{CARD}}"],
        "{{CARD_LOWER}}", replacements["{{CARD_LOWER}}"],
        "{{NODE}}", replacements["{{NODE}}"],
        "{{DATE}}", replacements["{{DATE}}"],
    ).Replace(template)
}
```

ChangedPaths 必须从 git diff 文本兼容现有 Client.Diff 的输出，不能把任意提交标题/作者行当路径。逐行只接受以下记录：diff --git a/<old> b/<new> 的 old/new；rename from <path>、rename to <path>；以及 --- <path> / +++ <path> 的非 /dev/null 路径。去重并保持首次出现顺序。不得从 commit、Author:、index、普通 hunk 行提取路径。若实现直接解析 name-status，必须在测试中增加 R100、A、D、M 四种 tab 分隔断言；若采用上述统一 diff 解析，则补充 ---/+++ 边界断言。changedPathsText 用换行连接去重后的路径，空集合返回（无）。

### 4. 注释与验证

纯函数没有外部调用，不能凭空加日志；在调用方 Task 3/4 记录入口、diff 获取前后、路径判定分支。导出的 RenderOutputPath/ChangedPaths 写参数、返回和未知占位符边界注释；非显然的 diff 行过滤写 why 注释。

运行：

```sh
gofmt -w internal/ledgerstep/output.go internal/ledgerstep/output_test.go
go test ./internal/ledgerstep -run 'Test(RenderOutputPath|ChangedPaths|ChangedPathsText|ReviewStepPassAndFailLoop|NodeStepDispatchOnlyReturnsDispatched|NodeStepVerdictRoutesOnPass)' -count=1
git diff --check
```

预期是 Go 测试输出 ok github.com/Xsxdot/handoff/internal/ledgerstep、git diff --check 无输出且均退出 0。

### 5. 缺陷族验收

- 模板边界族：四个占位符逐一断言；未知占位符不静默改写。
- diff 格式族：rename、modified、added、deleted 均有路径；/dev/null、commit、author、hunk 不污染结果。
- 幂等/顺序族：ChangedPaths 去重且稳定保持首次出现顺序。
- 空结果族：缺文件清单必须显示（无），不能形成空 comment。

## Task 3：NodeStep 在 pass 后按约定路径校验、挂载并路由

### 文件范围

- internal/ledgerstep/node.go
- internal/ledgerstep/node_test.go

### Interfaces

Consumes：

```go
type NodeStep struct {
    St *ledger.Store
    Node ledger.NodeDef
    Dispatch func(context.Context, ledger.Card, ledger.NodeDef) (target, taskID string, err error)
    Await func(context.Context, target, taskID string) (message string, err error)
    OutputPath func() string
    Diff func(context.Context, target, taskID string) ([]string, error)
    Attach func(cardID, kind, path, actor string) error
}
```

Produces：

```go
func (n *NodeStep) RunOnce(ctx context.Context, cardID string) (Outcome, error)
```

语义冻结：只有裁决 pass 且 Node.Produces 非 nil 时调用 OutputPath、Diff、Attach；路径未出现在本轮 ChangedPaths 时不调用 Attach，写一条含法定路径和实际路径清单的 comment，标记 needs_human 并返回 ActionNeedsHuman；Attach 失败只结构化 Warn，仍执行 routeTo；声明缺失时三个输出 hook 都不调用，旧节点行为保持不变。Attach 的 actor 必须是 node:<节点名>。

### 1. 基线判据先跑

在修改前执行：

```sh
go test ./internal/ledgerstep -run 'Test(ReviewStepPassAndFailLoop|ReviewStepRoundCapAndParseFailure|ReviewStepMarksNeedsHumanWhenReviewFails|NodeStepDispatchOnlyReturnsDispatched|NodeStepVerdictRoutesOnPass)' -count=1
```

预期为 ok github.com/Xsxdot/handoff/internal/ledgerstep。现有 node_test.go 夹具 nodeLedger、newNodeStep 必须继续复用，不另起数据库或真实派发。

### 2. 写失败测试

在 internal/ledgerstep/node_test.go 追加以下完整测试；测试消息用字节构造反引号，避免把裁决字符串的语法与本计划 Markdown 混淆：

```go
func nodePassMessage() string {
    fence := string([]byte{96, 96, 96})
    return fence + "handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n" + fence
}

func TestNodeStepAttachesDeclaredOutputAndRoutes(t *testing.T) {
    st, card := nodeLedger(t)
    attached := 0
    step := &NodeStep{
        St: st,
        Node: ledger.NodeDef{
            Name: "breakdown", Dispatch: true, Verdict: true, Template: "review-generic",
            Next: ledger.StatusReview,
            Produces: &ledger.NodeOutput{Kind: "doc", Path: "docs/b201-breakdown.md"},
        },
        Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
            return "linux-01", "task-output", nil
        },
        Await: func(context.Context, string, string) (string, error) {
            return nodePassMessage(), nil
        },
        OutputPath: func() string { return "docs/b201-breakdown.md" },
        Diff: func(context.Context, string, string) ([]string, error) {
            return []string{"docs/b201-breakdown.md"}, nil
        },
        Attach: func(cardID, kind, path, actor string) error {
            attached++
            if cardID != card.ID || kind != "doc" || path != "docs/b201-breakdown.md" || actor != "node:breakdown" {
                t.Fatalf("Attach 参数错误: %q %q %q %q", cardID, kind, path, actor)
            }
            return st.AttachFile(cardID, kind, path, actor)
        },
    }
    out, err := step.RunOnce(context.Background(), card.ID)
    if err != nil || out.Action != ActionPass {
        t.Fatalf("pass 输出: %v %+v", err, out)
    }
    if attached != 1 {
        t.Fatalf("Attach 次数 = %d, want 1", attached)
    }
    got, err := st.GetCard(card.ID)
    if err != nil {
        t.Fatalf("读卡: %v", err)
    }
    if got.Status != ledger.StatusReview {
        t.Fatalf("挂附件后未路由，status=%q", got.Status)
    }
    if len(got.Attachments) != 1 || got.Attachments[0].Kind != "doc" || got.Attachments[0].Path != "docs/b201-breakdown.md" {
        t.Fatalf("附件未落账: %+v", got.Attachments)
    }
}

func TestNodeStepMissingDeclaredOutputMarksNeedsHumanWithDiffList(t *testing.T) {
    st, card := nodeLedger(t)
    step := &NodeStep{
        St: st,
        Node: ledger.NodeDef{
            Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
            Next: ledger.StatusReview,
            Produces: &ledger.NodeOutput{Kind: "plan", Path: "docs/b201-plan.md"},
        },
        Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
            return "linux-01", "task-missing-output", nil
        },
        Await: func(context.Context, string, string) (string, error) {
            return nodePassMessage(), nil
        },
        OutputPath: func() string { return "docs/b201-plan.md" },
        Diff: func(context.Context, string, string) ([]string, error) {
            return []string{"docs/other.md", "internal/changed.go"}, nil
        },
        Attach: func(string, string, string, string) error {
            t.Fatal("缺产物时不应 Attach")
            return nil
        },
    }
    out, err := step.RunOnce(context.Background(), card.ID)
    if err != nil || out.Action != ActionNeedsHuman {
        t.Fatalf("缺产物输出: %v %+v", err, out)
    }
    events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
    if err != nil {
        t.Fatalf("读事件: %v", err)
    }
    found := false
    for _, event := range events {
        if event.Type == ledger.EvComment &&
            strings.Contains(string(event.Payload), "docs/b201-plan.md") &&
            strings.Contains(string(event.Payload), "docs/other.md") &&
            strings.Contains(string(event.Payload), "internal/changed.go") {
            found = true
        }
    }
    if !found {
        t.Fatal("缺产物 comment 未同时写法定路径与实际改动清单")
    }
}

func TestNodeStepAttachFailureWarnsButStillRoutes(t *testing.T) {
    st, card := nodeLedger(t)
    step := &NodeStep{
        St: st,
        Node: ledger.NodeDef{
            Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
            Next: ledger.StatusReview,
            Produces: &ledger.NodeOutput{Kind: "plan", Path: "docs/b201-plan.md"},
        },
        Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
            return "linux-01", "task-attach-error", nil
        },
        Await: func(context.Context, string, string) (string, error) {
            return nodePassMessage(), nil
        },
        OutputPath: func() string { return "docs/b201-plan.md" },
        Diff: func(context.Context, string, string) ([]string, error) {
            return []string{"docs/b201-plan.md"}, nil
        },
        Attach: func(string, string, string, string) error {
            return fmt.Errorf("sqlite 写入失败")
        },
    }
    out, err := step.RunOnce(context.Background(), card.ID)
    if err != nil || out.Action != ActionPass {
        t.Fatalf("挂载失败仍应 pass 并路由: %v %+v", err, out)
    }
    got, err := st.GetCard(card.ID)
    if err != nil {
        t.Fatalf("读卡: %v", err)
    }
    if got.Status != ledger.StatusReview {
        t.Fatalf("挂载失败不应阻断路由: %q", got.Status)
    }
}

func TestNodeStepWithoutProducesDoesNotInvokeOutputHooks(t *testing.T) {
    st, card := nodeLedger(t)
    called := 0
    step := &NodeStep{
        St: st,
        Node: ledger.NodeDef{
            Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
            Next: ledger.StatusReview,
        },
        Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
            return "linux-01", "task-legacy", nil
        },
        Await: func(context.Context, string, string) (string, error) {
            return nodePassMessage(), nil
        },
        OutputPath: func() string { called++; t.Fatal("legacy 节点不应取输出路径"); return "" },
        Diff: func(context.Context, string, string) ([]string, error) {
            called++
            t.Fatal("legacy 节点不应取 diff")
            return nil, nil
        },
        Attach: func(string, string, string, string) error {
            called++
            t.Fatal("legacy 节点不应挂附件")
            return nil
        },
    }
    out, err := step.RunOnce(context.Background(), card.ID)
    if err != nil || out.Action != ActionPass || called != 0 {
        t.Fatalf("legacy 节点行为变化: %v %+v hooks=%d", err, out, called)
    }
}

func TestNodeStepRerunSameOutputPathIsIdempotent(t *testing.T) {
    st, card := nodeLedger(t)
    step := &NodeStep{
        St: st,
        Node: ledger.NodeDef{
            Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
            Produces: &ledger.NodeOutput{Kind: "plan", Path: "docs/b201-plan.md"},
        },
        Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
            return "linux-01", "task-rerun", nil
        },
        Await: func(context.Context, string, string) (string, error) {
            return nodePassMessage(), nil
        },
        OutputPath: func() string { return "docs/b201-plan.md" },
        Diff: func(context.Context, string, string) ([]string, error) {
            return []string{"docs/b201-plan.md"}, nil
        },
        Attach: func(cardID, kind, path, actor string) error {
            return st.AttachFile(cardID, kind, path, actor)
        },
    }
    for i := 0; i < 2; i++ {
        out, err := step.RunOnce(context.Background(), card.ID)
        if err != nil || out.Action != ActionPass {
            t.Fatalf("第 %d 次重跑: %v %+v", i+1, err, out)
        }
    }
    got, err := st.GetCard(card.ID)
    if err != nil {
        t.Fatalf("读卡: %v", err)
    }
    if len(got.Attachments) != 1 {
        t.Fatalf("同 path 重跑应幂等，附件=%+v", got.Attachments)
    }
}
```

测试先红的具体原因是 NodeStep 尚无三个输出 hook；实现后同一命令必须绿。TestNodeStepMissingDeclaredOutputMarksNeedsHumanWithDiffList 不只看 Outcome，还从真实事件流检查 comment，覆盖序列化/投影到人可读报文的边界。

### 3. 最小实现

在 NodeStep 结构体增加三个可注入 hook，并写导出字段注释：

```go
// OutputPath 返回本次派发已经渲染的单一路径；只有 Node.Produces 非 nil 时读取。
OutputPath func() string
// Diff 返回本次 task 相对协调者基线的 changed paths；实现方负责把 Client.Diff 投影为路径。
Diff func(context.Context, target, taskID string) ([]string, error)
// Attach 将法定 kind/path 以节点 actor 挂到协调者账本；同 path 由 Store 保证幂等。
Attach func(cardID, kind, path, actor string) error
```

在 RunOnce 的裁决落账、ClearNeedsHumanFrom 成功后，计算路由前插入以下控制流；顺序必须是输出校验/挂载，再 routeTo，这样需要附件的 gate 才能看到刚挂的附件：

```go
if verdict.Pass && n.Node.Produces != nil {
    if n.OutputPath == nil || n.Diff == nil || n.Attach == nil {
        logger.Error("节点声明产出但输出依赖未装配",
            "kind", n.Node.Produces.Kind, "declared_path", n.Node.Produces.Path)
        return n.haltForHuman(cardID, "产出物校验未装配",
            "本节点声明了产出物，但协调者未装配输出校验依赖")
    }
    declaredPath := n.OutputPath()
    logger.Info("开始校验节点产出物",
        "kind", n.Node.Produces.Kind, "declared_path", declaredPath, "target", target, "task", taskID)
    changedPaths, diffErr := n.Diff(ctx, target, taskID)
    if diffErr != nil {
        logger.Warn("读取本轮改动失败，转等人",
            "kind", n.Node.Produces.Kind, "declared_path", declaredPath,
            "target", target, "task", taskID, "cause", diffErr)
        return n.haltForHuman(cardID, "读取产出物改动失败",
            "本节点无法确认产出物是否在本轮改动中：\n"+diffErr.Error())
    }
    logger.Info("本轮改动已取得",
        "kind", n.Node.Produces.Kind, "declared_path", declaredPath,
        "changed_paths", len(changedPaths))
    if declaredPath == "" || !containsPath(changedPaths, declaredPath) {
        body := "本节点要求的产出物路径：\n" + declaredPath +
            "\n本轮实际改动文件：\n" + changedPathsText(changedPaths)
        logger.Warn("法定产出物未出现在本轮改动",
            "kind", n.Node.Produces.Kind, "declared_path", declaredPath,
            "changed_paths", changedPaths)
        return n.haltForHuman(cardID, "缺少约定产出物", body)
    }
    if err := n.Attach(cardID, n.Node.Produces.Kind, declaredPath, n.actor()); err != nil {
        logger.Warn("挂载节点产出物失败，继续尝试路由",
            "kind", n.Node.Produces.Kind, "path", declaredPath, "cause", err)
    } else {
        logger.Info("节点产出物已挂载",
            "kind", n.Node.Produces.Kind, "path", declaredPath, "actor", n.actor())
    }
}
```

containsPath 的完整实现放在 node.go，避免通过前缀或 basename 误命中：

```go
func containsPath(paths []string, want string) bool {
    for _, path := range paths {
        if path == want {
            return true
        }
    }
    return false
}
```

每条错误分支都带 node/card、kind/path、target/task 等上下文；成功校验和成功挂载也写 Info。挂载失败不得返回 error，不得跳过 routeTo。

### 4. 注释与验证

- NodeStep 新字段和新增分支写参数、返回、为什么挂载在 routeTo 前。
- 运行最小范围：

```sh
gofmt -w internal/ledgerstep/node.go internal/ledgerstep/node_test.go
go test ./internal/ledgerstep -run 'Test(NodeStepAttachesDeclaredOutputAndRoutes|NodeStepMissingDeclaredOutputMarksNeedsHumanWithDiffList|NodeStepAttachFailureWarnsButStillRoutes|NodeStepWithoutProducesDoesNotInvokeOutputHooks|NodeStepRerunSameOutputPathIsIdempotent|ReviewStepPassAndFailLoop|ReviewStepRoundCapAndParseFailure|ReviewStepMarksNeedsHumanWhenReviewFails|NodeStepDispatchOnlyReturnsDispatched|NodeStepVerdictRoutesOnPass)' -count=1
git diff --check
```

预期 Go 输出为 ok github.com/Xsxdot/handoff/internal/ledgerstep，git diff --check 无输出、退出 0。只跑 internal/ledgerstep。

### 5. 缺陷族验收

- pass/route 顺序族：附件在 routeTo 前落账，下一列 require_attachment gate 能看到它。
- missing/error 分支族：缺路径转 needs_human 并留实际清单；Diff 错误转 needs_human；Attach 错误只 Warn 且仍路由。
- 兼容族：无 Produces 的老节点三个 hook 均不调用，原 pass 路由不变。
- 幂等族：同 path 两次运行真实读取卡附件仍只有一条。
- 事件序列化族：缺产物 comment 中同时出现法定路径和 changed paths。

## Task 4：prompt 路径注入、runner 渲染和 Client.Diff 接线

### 文件范围

- internal/ledgerstep/dispatch.go
- internal/ledgerstep/dispatch_test.go
- internal/ledgerstep/runner.go
- internal/ledgerstep/runner_test.go

### Interfaces

Consumes：

```go
type TemplateDispatch struct {
    Template string
    Target string
    PlanPath string
    DisciplineOverride string
    ExecutorOverride string
    ModelOverride string
    CarryCardContext bool
    PurposeOverride string
    OmitAcceptance bool
    Extra string
    OutputPath string
}
func RenderOutputPath(template string, card ledger.Card, node ledger.NodeDef, now time.Time) string
func ChangedPaths(diff string) []string
func (c *client.Client) Diff(ctx context.Context, taskID, base string) (string, error)
```

Produces：

```go
type StepRunner struct {
    St *ledger.Store
    Dispatcher *Dispatcher
    Session string
    Clients func(target string) (*client.Client, error)
    Target string
    Extra string
    Now func() time.Time
}
func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error)
```

### 1. 基线判据先跑

在修改前执行：

```sh
go test ./internal/ledgerstep -run 'Test(BuildPromptThreeSections|RunnerPassesNodePurposeAndAcceptanceSwitch|RunnerFindsNodeInPinnedWorkflowVersion)' -count=1
```

预期为 ok github.com/Xsxdot/handoff/internal/ledgerstep。既有 buildPrompt 的六参数调用面必须全部找到并在本 task 中逐一更新，不能留下编译断点。

### 2. 写失败测试

在 dispatch_test.go 的 TestBuildPromptThreeSections 增加第七个空 outputPath 参数，并追加完整用例：

```go
func TestBuildPromptIncludesOutputPathWithoutCardContext(t *testing.T) {
    card := ledger.Card{ID: "B201", Title: "产文档"}
    got := buildPrompt(
        "模板正文", card, "", false, true, "",
        "docs/b201-plan.md",
    )
    if strings.Contains(got, "## 本卡上下文") {
        t.Fatalf("carry=false 不应注入卡上下文:\n%s", got)
    }
    for _, want := range []string{
        "## 本节点产出物",
        "docs/b201-plan.md",
        "请把本节点产出物写到该路径，不要另起文件名",
    } {
        if !strings.Contains(got, want) {
            t.Fatalf("缺产出路径段 %q:\n%s", want, got)
        }
    }
}
```

在 runner_test.go 追加 runner 装配测试。它用固定 Now 验证模板四占位符被渲染后同时进入 DispatchOpts prompt 与 NodeStep 输出 hook；Diff/Attach 在此测试中注入，不发真实 HTTP：

```go
func TestRunnerRendersDeclaredOutputPathAndInjectsPrompt(t *testing.T) {
    st, _ := nodeLedger(t)
    if _, err := st.PutWorkflow("output-runner", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
        {Name: ledger.StatusTodo, Next: "plan"},
        {
            Name: "plan", Dispatch: true, Verdict: true, Template: "feature-impl",
            Next: ledger.StatusReview, OmitAcceptance: true,
            Produces: &ledger.NodeOutput{
                Kind: "doc", Path: "docs/{{DATE}}/{{CARD_LOWER}}-{{NODE}}.md",
            },
        },
        {Name: ledger.StatusReview},
    }}); err != nil {
        t.Fatalf("写 output workflow: %v", err)
    }
    card, err := st.CreateCard(ledger.NewCard{
        Title: "runner 产物", Project: "p", Workflow: "output-runner", Actor: "t",
    })
    if err != nil {
        t.Fatalf("建卡: %v", err)
    }
    var opts DispatchOpts
    d := &Dispatcher{
        St: st, Actor: "runner-test",
        Transport: func(ctx context.Context, got DispatchOpts) (string, error) {
            opts = got
            return "task-output-runner", nil
        },
    }
    runner := &StepRunner{
        St: st, Dispatcher: d, Session: "output-runner-session", Target: "linux-01",
        Now: func() time.Time { return time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC) },
    }
    path := ""
    node := stMustNode(t, st, card.ID, "plan")
    target, taskID, err := runner.dispatchNode(&path)(context.Background(), card, node)
    if err != nil {
        t.Fatalf("dispatchNode: %v", err)
    }
    if target != "linux-01" || taskID != "task-output-runner" {
        t.Fatalf("dispatch 返回: target=%q task=%q", target, taskID)
    }
    wantPath := "docs/2026-08-23/" + strings.ToLower(card.ID) + "-plan.md"
    if path != wantPath || opts.OutputPath != wantPath {
        t.Fatalf("渲染路径: holder=%q opts=%q want=%q", path, opts.OutputPath, wantPath)
    }
    if !strings.Contains(opts.Prompt, "## 本节点产出物") || !strings.Contains(opts.Prompt, wantPath) {
        t.Fatalf("prompt 未注入法定路径:\n%s", opts.Prompt)
    }
}
```

上面测试依赖的完整测试辅助函数必须一并加入 runner_test.go，避免隐含占位符：

```go
func stMustNode(t *testing.T, st *ledger.Store, cardID, name string) ledger.NodeDef {
    t.Helper()
    card, err := st.GetCard(cardID)
    if err != nil {
        t.Fatalf("读卡: %v", err)
    }
    flow, err := st.GetWorkflow(card.WorkflowName, card.WorkflowVersion)
    if err != nil {
        t.Fatalf("读卡钉工作流: %v", err)
    }
    for _, node := range flow.Def.Nodes {
        if node.Name == name {
            return node
        }
    }
    t.Fatalf("找不到节点 %q", name)
    return ledger.NodeDef{}
}
```

测试先红的具体预期：buildPrompt 参数数目不匹配，TemplateDispatch 没有 OutputPath，StepRunner 没有 Now/dispatchNode 输出路径 holder；实现后同一命令变绿。

### 3. 最小实现

在 TemplateDispatch 加入带职责注释的 OutputPath：

```go
// OutputPath 是本节点声明路径在协调者侧按本轮派发日期渲染后的确定值。
// 它独立于 CarryCardContext：即使不携带卡上下文，执行者仍必须收到该法定路径。
OutputPath string
```

把 ViaTemplate 中 buildPrompt 调用改为传入 req.OutputPath；buildPrompt 扩展为七参数，并在已有 card context/extra 段落之后追加独立的本节点产出物段：

```go
func buildPrompt(body string, c ledger.Card, base string, carry, omitAccept bool, extra, outputPath string) string {
    sections := []string{body}
    if carry {
        var b strings.Builder
        b.WriteString("## 本卡上下文\n\n")
        fmt.Fprintf(&b, "- 卡号：%s\n", c.ID)
        fmt.Fprintf(&b, "- 标题：%s\n", c.Title)
        if base != "" {
            fmt.Fprintf(&b, "- 有效基线分支：%s（本卡的合并目标以此为准，不要越过它碰别的分支）\n", base)
        } else {
            b.WriteString("- 有效基线分支：（未设置，需要合并时先向协调者确认，不要自行假定 main）\n")
        }
        if c.AcceptanceCriteria != "" && !omitAccept {
            fmt.Fprintf(&b, "- 验收判据：\n%s\n", indentLines(c.AcceptanceCriteria, "  "))
        }
        if len(c.Attachments) > 0 {
            b.WriteString("- 附件（仓内相对路径）：\n")
            for _, att := range c.Attachments {
                fmt.Fprintf(&b, "  - %s: %s\n", att.Kind, att.Path)
            }
        }
        sections = append(sections, strings.TrimRight(b.String(), "\n"))
    }
    if strings.TrimSpace(extra) != "" {
        sections = append(sections, "## 本次补充\n\n"+strings.TrimSpace(extra))
    }
    if strings.TrimSpace(outputPath) != "" {
        sections = append(sections,
            "## 本节点产出物\n\n- 法定路径："+outputPath+
                "\n- 请把本节点产出物写到该路径，不要另起文件名。")
    }
    return strings.Join(sections, "\n\n")
}
```

不要把输出段放进 carry 分支；不要给没有 Produces 的旧节点制造空标题。ViaTemplate 的结构化 Info 日志增加 output_path 字段，并在 Transport 前记录已经确定的 path；Transport 错误日志保留 card/template/target 上下文。

StepRunner 增加 Now 和两个有界 helper：

```go
type StepRunner struct {
    St *ledger.Store
    Dispatcher *Dispatcher
    Session string
    Clients func(target string) (*client.Client, error)
    Target string
    Extra string
    // Now 只为路径日期提供可注入时钟；nil 使用 time.Now。
    Now func() time.Time
}

func (r *StepRunner) now() time.Time {
    if r.Now != nil {
        return r.Now()
    }
    return time.Now()
}
```

在 Run 装配一个本次调用独享的 holder，确保路径只渲染一次，并将三个 NodeStep hook 接到 runner：

```go
outputPath := ""
nodeStep := &NodeStep{
    St: r.St,
    Node: node,
    Dispatch: r.dispatchNode(&outputPath),
    Await: r.awaitNode(),
    OutputPath: func() string { return outputPath },
    Diff: r.diffNode(),
    Attach: func(cardID, kind, path, actor string) error {
        return r.St.AttachFile(cardID, kind, path, actor)
    },
}
```

将 dispatchNode 的签名改为：

```go
func (r *StepRunner) dispatchNode(outputPath *string) func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error)
```

闭包中先按既有 Target/Override 选目标；若 node.Produces 非 nil，则用 RenderOutputPath(node.Produces.Path, card, node, r.now()) 写入 holder，并把同一路径放入 TemplateDispatch.OutputPath。若无 Produces，holder 保持空且 req.OutputPath 为空。派发前后日志包含 card、node、target、task、kind/path；任何 ViaTemplate 错误原样包装并带上下文。完整闭包为：

```go
func (r *StepRunner) dispatchNode(outputPath *string) func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
    return func(ctx context.Context, card ledger.Card, node ledger.NodeDef) (string, string, error) {
        target := r.Target
        if target == "" {
            target = node.Override.Target
        }
        renderedPath := ""
        if node.Produces != nil {
            renderedPath = RenderOutputPath(node.Produces.Path, card, node, r.now())
        }
        if outputPath != nil {
            *outputPath = renderedPath
        }
        slog.Default().Info("准备派发节点",
            "card", card.ID, "node", node.Name, "target", target,
            "kind", outputKind(node), "output_path", renderedPath)
        result, err := r.Dispatcher.ViaTemplate(ctx, card, TemplateDispatch{
            Template:           node.Template,
            Target:             target,
            DisciplineOverride: node.Override.Discipline,
            ExecutorOverride:   node.Override.Executor,
            ModelOverride:      node.Override.Model,
            CarryCardContext:   node.CarryCardContext,
            PurposeOverride:    node.Override.Purpose,
            OmitAcceptance:     node.OmitAcceptance,
            Extra:              r.Extra,
            OutputPath:         renderedPath,
        })
        if err != nil {
            slog.Default().Warn("节点派发失败",
                "card", card.ID, "node", node.Name, "target", target,
                "output_path", renderedPath, "cause", err)
            return "", "", err
        }
        slog.Default().Info("节点派发完成",
            "card", card.ID, "node", node.Name, "target", result.Target,
            "task", result.Task, "output_path", renderedPath)
        return result.Target, result.Task, nil
    }
}

func outputKind(node ledger.NodeDef) string {
    if node.Produces == nil {
        return ""
    }
    return node.Produces.Kind
}
```

新增 diffNode，严格只调用已有 Client.Diff，不访问远程工作树的其它 API：

```go
func (r *StepRunner) diffNode() func(context.Context, string, string) ([]string, error) {
    return func(ctx context.Context, target, taskID string) ([]string, error) {
        logger := slog.Default().With("target", target, "task", taskID)
        logger.Info("读取节点本轮 diff")
        cl, err := r.Clients(target)
        if err != nil {
            logger.Warn("取得节点 diff 客户端失败", "cause", err)
            return nil, err
        }
        raw, err := cl.Diff(ctx, taskID, "")
        if err != nil {
            logger.Warn("取得节点 diff 失败", "cause", err)
            return nil, err
        }
        paths := ChangedPaths(raw)
        logger.Info("节点本轮 diff 已投影", "changed_paths", len(paths))
        return paths, nil
    }
}
```

Clients == nil、Client.Diff 错误、Transport 错误都必须保留当前错误分支的上下文；不在 NodeStep 中直接构造 client，也不把 B205 的文件可达性混入本 task。

### 4. 注释与验证

- buildPrompt 的参数注释补充 outputPath 及其独立于 carry 的原因；TemplateDispatch.OutputPath、StepRunner.Now、diffNode 写边界注释。
- 更新 dispatch_test.go 全部既有 buildPrompt 调用的第七参数为空字符串。
- runner_test.go 新增 time import；所有 runner 测试仍使用既有 nodeLedger/dispatchRunner 夹具。
- 运行：

```sh
gofmt -w internal/ledgerstep/dispatch.go internal/ledgerstep/dispatch_test.go internal/ledgerstep/runner.go internal/ledgerstep/runner_test.go
go test ./internal/ledgerstep -run 'Test(BuildPromptThreeSections|BuildPromptIncludesOutputPathWithoutCardContext|RunnerRendersDeclaredOutputPathAndInjectsPrompt|RunnerPassesNodePurposeAndAcceptanceSwitch|RunnerFindsNodeInPinnedWorkflowVersion|RunnerClaimsDriverWithoutChangingNodeStatusAndReleasesAfterRun)' -count=1
git diff --check
```

预期输出为 ok github.com/Xsxdot/handoff/internal/ledgerstep，git diff --check 无输出且退出 0。只跑 internal/ledgerstep。

### 5. 缺陷族验收

- prompt 边界族：carry=false 仍有法定路径；无 outputPath 无空标题；既有卡上下文/extra 语义不变。
- 日期/重跑族：同一次 Run 的 dispatch 与 NodeStep 共享一次渲染结果；Now 可固定测试，默认仍用当前时间。
- 远程调用族：Diff 只通过既有 Client.Diff；客户端取得、Diff 前后和错误均有结构化日志。
- 兼容族：已有 buildPrompt、TemplateDispatch 调用传空路径后行为不变。

## Task 5：Go/TS wire、编辑器、charter 配置与部署说明

### 文件范围

- internal/proto/ledger.go
- internal/proto/contract_fixture_test.go
- web/src/api/ledger.ts
- web/src/api/testdata/NodeDef.json
- web/src/api/contract.test.ts
- internal/agentd/ledgerapi.go
- internal/agentd/ledgerapi_test.go
- web/src/app/flows/NodeEditor.tsx
- web/src/app/flows/NodeEditor.test.tsx
- deploy/workflows/charter-v4.json
- deploy/workflows/README.md

### Interfaces

Consumes：

```go
type ledger.NodeOutput struct {
    Kind string
    Path string
}
type ledger.NodeDef struct {
    Produces *ledger.NodeOutput
}
func ledgerNodeWire(node ledger.NodeDef) proto.NodeDef
```

Produces：

```go
type NodeOutput struct {
    Kind string `json:"kind"`
    Path string `json:"path"`
}
type proto.NodeDef struct {
    Name string `json:"name"`
    Template string `json:"template,omitempty"`
    Override NodeOverride `json:"override,omitempty"`
    Dispatch bool `json:"dispatch,omitempty"`
    Verdict bool `json:"verdict,omitempty"`
    CarryCardContext bool `json:"carry_card_context,omitempty"`
    MaxRounds int `json:"max_rounds,omitempty"`
    Next string `json:"next,omitempty"`
    OnFail string `json:"on_fail,omitempty"`
    Gate Gate `json:"gate,omitempty"`
    HumanBases []string `json:"human_bases,omitempty"`
    Produces *NodeOutput `json:"produces,omitempty"`
}
```

```ts
export interface NodeOutput {
  kind: string
  path: string
}

export interface NodeDef {
  name: string
  template?: string
  override?: NodeOverride
  dispatch?: boolean
  verdict?: boolean
  carry_card_context?: boolean
  max_rounds?: number
  omit_acceptance?: boolean
  next?: string
  on_fail?: string
  gate?: Gate
  human_bases?: string[]
  produces?: NodeOutput
}
```

### 1. 基线判据先跑

修改前执行：

```sh
go test ./internal/proto -run TestContractFixtures -count=1
go test ./internal/agentd -run 'Test(FlowGetReturnsNodes|FlowPutCreatesNewVersion|AttachmentKindsCoverDefaultWorkflowGates)' -count=1
jq empty deploy/workflows/charter-v4.json
```

预期 Go 两条分别输出 ok 对应包，jq 退出 0 无输出。Web 基线 npm run typecheck 与指定 vitest 已因 tsc 缺失和 npm cache EROFS 失败，原始报错已在台账；实现后在依赖可用环境执行相同命令并把真实结果写入台账。

### 2. 写失败测试与序列化边界断言

#### Go wire round-trip

在 internal/agentd/ledgerapi_test.go 追加完整 HTTP 回归测试。它通过真实 PUT 解码进入 ledger.NodeDef，再通过真实 GET 经过 ledgerNodeWire 与 proto JSON 投影，显式区分 legacy 缺失与新字段值：

```go
func TestFlowNodeProducesRoundTripsThroughHTTPWire(t *testing.T) {
    env := newLedgerEnv(t)
    payload := []byte("{\"nodes\":[{\"name\":\"legacy\"},{\"name\":\"breakdown\",\"produces\":{\"kind\":\"doc\",\"path\":\"docs/b201-breakdown.md\"}}]}")
    code, body := ledgerPut(t, env.testAgentdEnv, "/api/flows/feature", string(payload))
    if code != http.StatusOK {
        t.Fatalf("put code = %d, body = %s", code, body)
    }

    code, body = ledgerGet(t, env.testAgentdEnv, "/api/flows/feature")
    if code != http.StatusOK {
        t.Fatalf("get code = %d, body = %s", code, body)
    }
    var got struct {
        Nodes []struct {
            Name     string `json:"name"`
            Produces *struct {
                Kind string `json:"kind"`
                Path string `json:"path"`
            } `json:"produces"`
        } `json:"nodes"`
    }
    if err := json.Unmarshal([]byte(body), &got); err != nil {
        t.Fatalf("解码 flow: %v（原文 %s）", err, body)
    }
    if len(got.Nodes) != 2 {
        t.Fatalf("nodes 数量 = %d, want 2", len(got.Nodes))
    }
    if got.Nodes[0].Produces != nil {
        t.Fatalf("legacy 节点字段缺失必须保持 nil: %+v", got.Nodes[0].Produces)
    }
    if got.Nodes[1].Produces == nil ||
        got.Nodes[1].Produces.Kind != "doc" ||
        got.Nodes[1].Produces.Path != "docs/b201-breakdown.md" {
        t.Fatalf("produces wire round-trip 失败: %+v", got.Nodes[1].Produces)
    }
}
```

这条测试覆盖产生端 JSON、agentd PUT 解码、账本持久化、GET projection、JSON 输出五个边界，不能用直接调用 ledgerNodeWire 的单元测试替代。

在 internal/proto/contract_fixture_test.go 的 NodeDef 样本改为带非零 Produces：

```go
{"NodeDef", NodeDef{
    Name: "定性中",
    Next: "已定性",
    Produces: &NodeOutput{
        Kind: "doc",
        Path: "docs/superpowers/plans/b201-plan.md",
    },
}},
```

在 web/src/api/contract.test.ts 的既有 NodeDef fixture 断言后加入：

```ts
expect(node.produces).toEqual({
  kind: 'doc',
  path: 'docs/superpowers/plans/b201-plan.md',
})
```

先用 Go fixture harness 的显式更新命令生成 web/src/api/testdata/NodeDef.json，再关闭 update 运行同一测试；不得手写绕过 fixture 比较：

```sh
go test ./internal/proto -run TestContractFixtures -update
go test ./internal/proto -run TestContractFixtures -count=1
```

预期第一条更新 NodeDef fixture 后退出 0，第二条输出 ok github.com/Xsxdot/handoff/internal/proto；fixture 应只增加 produces 对象且 kind/path 非零。若 fixture 还因其它类型漂移报错，保留原始输出，不把它归到本卡。

#### Web NodeEditor

在 web/src/app/flows/NodeEditor.test.tsx 追加完整用例，使用 rerender 保证第二次输入消费第一次冒泡后的 NodeDef：

```tsx
it('能编辑产出类型与路径，关闭派发会清掉产出声明', () => {
  let current = { ...base }
  const onChange = vi.fn((next) => {
    current = next
    view.rerender(
      <NodeEditor node={current} {...props} index={0} onChange={onChange} onRemove={() => {}} />,
    )
  })
  const view = render(
    <NodeEditor node={current} {...props} index={0} onChange={onChange} onRemove={() => {}} />,
  )

  fireEvent.change(screen.getByLabelText('产出类型'), { target: { value: 'doc' } })
  expect(current.produces).toEqual({ kind: 'doc' })

  fireEvent.change(screen.getByLabelText('产出路径'), {
    target: { value: 'docs/{{CARD_LOWER}}-plan.md' },
  })
  expect(current.produces).toEqual({
    kind: 'doc',
    path: 'docs/{{CARD_LOWER}}-plan.md',
  })

  fireEvent.click(screen.getByLabelText('派发'))
  expect(current.dispatch).toBe(false)
  expect(current.produces).toBeUndefined()
})

it('产出路径帮助文本列出四个可用占位符', () => {
  render(<NodeEditor node={base} {...props} index={0} onChange={() => {}} onRemove={() => {}} />)
  expect(screen.getByText(/{{CARD}}.*{{CARD_LOWER}}.*{{NODE}}.*{{DATE}}/)).toBeInTheDocument()
})
```

失败预期是 TS NodeDef 无 produces 类型、UI 无控件而查询失败；完成类型、控件与清理逻辑后变绿。

### 3. 最小实现

#### Go wire

在 internal/proto/ledger.go 的 NodeDef 前新增：

```go
// NodeOutput 是工作流节点声明的单一附件 kind/path wire DTO。
// 指针由 NodeDef.Produces 持有，以区分旧 JSON 的字段缺失和显式对象。
type NodeOutput struct {
    Kind string `json:"kind"`
    Path string `json:"path"`
}
```

在 proto.NodeDef 增加：

```go
Produces *NodeOutput `json:"produces,omitempty"`
```

在 internal/agentd/ledgerapi.go 的 ledgerNodeWire 中显式做 nil-preserving projection：

```go
var produces *proto.NodeOutput
if node.Produces != nil {
    produces = &proto.NodeOutput{
        Kind: node.Produces.Kind,
        Path: node.Produces.Path,
    }
}
return proto.NodeDef{
    Name: node.Name,
    Template: node.Template,
    Override: proto.NodeOverride{
        Executor: node.Override.Executor,
        Discipline: node.Override.Discipline,
        Target: node.Override.Target,
        Model: node.Override.Model,
    },
    Dispatch: node.Dispatch,
    Verdict: node.Verdict,
    CarryCardContext: node.CarryCardContext,
    MaxRounds: node.MaxRounds,
    Next: node.Next,
    OnFail: node.OnFail,
    Gate: proto.Gate{
        RequireAttachment: node.Gate.RequireAttachment,
        RequireAcceptance: node.Gate.RequireAcceptance,
        RequireChildrenDone: node.Gate.RequireChildrenDone,
    },
    HumanBases: node.HumanBases,
    Produces: produces,
}
```

保持 HTTP PUT 现有直接解码 ledger.NodeDef 的路径不变；Task 1 的 Store validation 是 PUT 的唯一合法性判据。projection 成功日志可在现有 flow GET 日志中增加 node produces 是否存在，错误响应必须继续带 workflow/name 原上下文。

#### TypeScript wire

在 web/src/api/ledger.ts 的 NodeDef 前新增 NodeOutput interface，在 NodeDef 中加入 produces?: NodeOutput。字段名必须是 kind/path 与 Go JSON 一致；不可使用 nullable 强行把缺失转成空对象。

#### NodeEditor

在 web/src/app/flows/NodeEditor.tsx 增加职责注释和字段更新 helper：

```tsx
const setProducesField = (key: 'kind' | 'path', value: string) => {
  const produces = { ...(node.produces ?? {}) }
  if (value === '') delete produces[key]
  else produces[key] = value
  update({
    produces: Object.keys(produces).length > 0
      ? produces as NonNullable<NodeDef['produces']>
      : undefined,
  })
}
```

在 setDispatch(false) 的 update patch 中增加 produces: undefined。dispatch=true 的模板/纪律网格中增加两个受控文本输入，id 分别为产出类型和产出路径的 controlID suffix；标签必须精确为 产出类型、产出路径；value 取 node.produces?.kind/path；onChange 分别调用 setProducesField。路径输入下方显示固定帮助文本：

```tsx
<p className="mt-1 text-xs text-muted-foreground">
  可用占位符：{{CARD}}、{{CARD_LOWER}}、{{NODE}}、{{DATE}}。
</p>
```

UI 只负责字段冒泡与关闭派发时清除无执行对象的声明；不在前端校验 kind 白名单或路径内容，后端 PutWorkflow 负责合法性。

#### charter 配置与部署说明

修改 deploy/workflows/charter-v4.json：

1. contract 节点增加：
```json
"produces": {
  "kind": "contract",
  "path": "docs/superpowers/specs/{{CARD_LOWER}}-contract.md"
}
```
2. breakdown 节点增加：
```json
"produces": {
  "kind": "doc",
  "path": "docs/superpowers/specs/{{CARD_LOWER}}-breakdown.md"
}
```
3. plan 节点增加：
```json
"produces": {
  "kind": "plan",
  "path": "docs/superpowers/plans/{{CARD_LOWER}}-plan.md"
}
```
4. **四道 gate 一律不动**：contract 继续 `require_attachment=spec`，breakdown 继续 `contract`，**plan 继续 `spec`**（改它会切断 L2 卡直接跳进 plan 列的路径，见「交付边界与冻结决定」里的协调者更正），implement 继续 `plan`。本卡只加 produces 声明，不碰任何 gate。
5. 所有 produces 均与 dispatch/verdict 节点同层，JSON 只新增这些确定字段，不改 next/on_fail/discipline。

修改 deploy/workflows/README.md 的 charter-v4 表格增加一行，明确 contract、breakdown、plan 各自的 kind/path 模板，并写明**四道 gate 本次一律不变**；应用顺序段的二进制先部署、再 workflow put、存量卡显式 migrate 保持不变。补充如下明确说明：

```md
B201 使产文档节点在 pass 后由协调者按约定路径校验本轮 diff 并自动挂卡：
contract 产出 contract 到 docs/superpowers/specs/{{CARD_LOWER}}-contract.md，
breakdown 产出 doc 到 docs/superpowers/specs/{{CARD_LOWER}}-breakdown.md，
plan 产出 plan 到 docs/superpowers/plans/{{CARD_LOWER}}-plan.md。
路径在派发 prompt 中固定告知执行者；未声明 produces 的旧工作流行为不变。
```

### 4. 注释、验证与提交前范围

- 新增 proto.NodeOutput、TS NodeOutput、NodeEditor helper 写职责、字段边界和 nil/undefined 语义注释；HTTP projection 注释为什么必须显式保留指针。
- Go 验证：

```sh
gofmt -w internal/proto/ledger.go internal/proto/contract_fixture_test.go internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go
go test ./internal/proto -run TestContractFixtures -count=1
go test ./internal/agentd -run 'Test(FlowGetReturnsNodes|FlowPutCreatesNewVersion|FlowNodeProducesRoundTripsThroughHTTPWire|AttachmentKindsCoverDefaultWorkflowGates)' -count=1
jq empty deploy/workflows/charter-v4.json
git diff --check
```

预期两条 Go 命令分别输出 ok、jq 无输出退出 0、git diff --check 无输出退出 0。
- Web 验证（依赖可用时）：

```sh
cd web
npm run typecheck
npx vitest run src/api/contract.test.ts src/app/flows/NodeEditor.test.tsx src/app/flows/NodeEditor.purpose.test.tsx
```

预期 typecheck 和 vitest 均退出 0；当前环境基线已实际失败，只有真实复跑结果才能写 pass。
- 配置/序列化边界清单必须在实现 PR 自检中逐条核对：ledger.NodeDef → SQLite JSON → agentd proto.NodeDef → HTTP JSON → TS NodeDef → NodeEditor；nil 缺失与非 nil 零对象必须各有断言，且 HTTP 回归测试穿过真实投影。

### 5. 缺陷族验收

- 线格式族：Go fixture 与 TS import 同时见 kind/path，omitempty 缺失不被改写为空对象。
- HTTP 投影族：PUT/SQLite/GET 的 produces 端到端 round-trip；legacy 节点 produces 为 nil。
- UI 状态族：partial → complete → dispatch off 的三态断言；路径帮助四占位符齐全。
- 配置/门禁族：produces 为 contract→`contract`、breakdown→`doc`、plan→`plan`；**四道 gate 保持原样**（contract=spec、breakdown=contract、plan=spec、implement=plan），本卡不改门。
- 兼容族：旧工作流没有 produces 时 Go/TS/UI 都不凭空产生字段；老节点无需重发定义。
- 可观测性族：HTTP projection/错误路径、配置加载/校验沿用结构化日志；UI 无 print。


## 执行顺序与每 task 的红绿提交动作

以下是实现者必须按序执行的竖切顺序；每一行都是一个可在 2 至 5 分钟内完成的动作，未列入的包不在该 task 测试范围：

1. Task 1：先跑基线命令；追加三组 ledger 失败测试；运行同一 go test 看到缺类型/校验的红；添加 NodeOutput、Produces 指针字段、validateNodes 和结构化计数日志；gofmt 后运行 Task 1 绿测与 git diff --check；提交消息固定为 feat(ledger): declare node output path。
2. Task 2：先跑 ledgerstep 基线；创建 output_test.go 的四个纯函数测试；运行指定 go test 看到 undefined 红；新增 output.go 的四占位符替换、ChangedPaths、changedPathsText；gofmt 后运行 Task 2 绿测与 git diff --check；提交消息固定为 feat(ledgerstep): render declared output paths。
3. Task 3：先跑 node 基线；追加五个 NodeStep 行为测试；运行指定 go test 看到 NodeStep hook 缺失红；增加 hook、containsPath、pass 后校验/Attach/route 顺序和结构化日志；gofmt 后运行 Task 3 绿测与 git diff --check；提交消息固定为 feat(ledgerstep): attach declared node outputs。
4. Task 4：先跑 prompt/runner 基线；更新既有 buildPrompt 调用并追加 carry=false 路径测试和固定时钟 runner 测试；运行指定 go test 看到参数/字段缺失红；接入 OutputPath、Now、diffNode、Client.Diff 和 prompt 独立段；gofmt 后运行 Task 4 绿测与 git diff --check；提交消息固定为 feat(ledgerstep): pass output path to executors。
5. Task 5：先跑 proto/agentd/config 基线；追加 HTTP wire、fixture、NodeEditor 失败断言；运行 Go/Web 红测并保留真实错误；增加 proto/TS projection、UI 控件、fixture 生成和 charter/README；运行 Go 绿测、fixture 非 update 复核、jq、依赖可用时 Web 绿测和 git diff --check；提交消息固定为 feat(charter): declare document node outputs。
6. 完成五个 task 后，协调者在当前分支执行本计划的跨 task 验收，不派发、不调用 handoff CLI；全量测试只在 implement 三段律的最终阶段执行。

Task 5 的 NodeEditor 测试必须把现有 import 扩展为：

```ts
import type { NodeDef } from '../../api/ledger'
```

并将测试变量声明改为：

```ts
let current: NodeDef = { ...base }
const onChange = vi.fn((next: NodeDef) => {
  current = next
  view.rerender(
    <NodeEditor node={current} {...props} index={0} onChange={onChange} onRemove={() => {}} />,
  )
})
```

这样 partial produces、complete produces 和 undefined 三态都由显式 NodeDef 类型检查，不能靠 any 绕过序列化字段。

## 用户故事逐条归属

| spec 用户故事 | 具体归属 |
| --- | --- |
| plan 成功后自动进 implement | Task 3 的 Attach-before-route 测试与实现；Task 5 charter plan produces=plan、implement gate=plan |
| 看板 needs_human 只保留真问题 | Task 3 的 pass 路由、missing path needs_human、Attach error Warn-and-route 三个测试 |
| 执行者开工即知道路径 | Task 4 的 carry=false prompt 测试与 OutputPath 接线；Task 5 NodeEditor 与 charter 声明 |
| 缺产物时显示要求路径和实际改动 | Task 2 ChangedPaths/changedPathsText；Task 3 comment 事件断言 |
| 工作流作者在一处声明 gate 与 produces | Task 1 ledger schema/validation；Task 5 Go/TS wire、UI、charter JSON |

## 跨 task 签名和序列化审计

在扇出前逐字复核：

1. ledger.NodeDef.Produces 的精确签名是 Produces *NodeOutput；Task 1 生产，Task 3/4 消费，Task 5 的 ledgerNodeWire 消费。
2. NodeOutput 的精确 JSON 字段是 Kind string json kind、Path string json path；proto NodeOutput 与 TS NodeOutput 必须分别保持 kind/path，不把 Go 名称暴露到 Web。
3. RenderOutputPath 的四参数签名和 ChangedPaths 的单 diff 参数签名由 Task 2 生产，Task 4 逐字消费；Now 在 Task 4 只负责日期，不改 RenderOutputPath。
4. NodeStep 的 OutputPath、Diff、Attach hook 签名由 Task 3 生产，Task 4 runner 装配；Attach actor 从 NodeStep.actor 生成 node:<name>。
5. buildPrompt 从六参数变为七参数，新增 outputPath 在末尾；Task 4 必须更新 dispatch.go 内 ViaTemplate 和 dispatch_test.go 的每一处调用。
6. HTTP 回归链路是 PUT JSON → ledger.NodeDef JSON 解码 → SQLite definition → ledgerNodeWire → proto.NodeDef JSON；Task 5 的 legacy nil 与非 nil path 断言穿过整条链路。
7. fixture 链路是 proto.NodeDef → json.MarshalIndent → web/src/api/testdata/NodeDef.json → TS import → contract.test.ts；只能用 -update 明确更新 fixture。
8. UI 链路是 NodeEditor inputs → NodeDef.produces partial/complete/undefined → flow save API；UI 只投影字段，不复制后端 kind/path 校验。

## 缺陷族对抗审查结论

- 空字段/缺字段：Task 1 用指针区分 JSON 缺失和显式零对象；validateNodes 拒绝存储零 kind/path；Task 5 HTTP/fixture 同时断言两种状态。
- 路径模板：Task 2 四个占位符、unknown literal、固定时钟；Task 4 同一 Run 只渲染一次，避免 prompt 与 Attach 路径不一致。
- diff 误判：Task 2 只采信 diff --git、rename from/to、非 /dev/null ---/+++；提交元数据、hunk、basename 不进入 paths。
- 顺序/门禁：Task 3 明确 Attach 在 routeTo 前；Task 5 **不改任何 gate**（implement gate 仍是 plan，正好由 plan 节点新增的 produces=plan 满足）；配置 jq 检查。
- 失败处置：Diff/client 失败转 needs_human 并留上下文；Attach 失败只 Warn 后仍路由；旧节点没有 hook 调用。
- 幂等/重跑：Task 3 两次同 path 真实读卡仍一条附件；不使用日期作为 charter 缺省路径。
- 兼容/迁移：Produces optional；老 JSON/老 States 读出保持 nil；charter 文件应用只产生新版本，存量卡不自动迁移。
- 类型/投影：Go wire、agentd projection、TS interface、fixture、HTTP 回归与 UI 断言共同覆盖一条字段链；不以两端孤立测试代替跨边界测试。
- 可观测性：Task 1 PutWorkflow、Task 3 NodeStep、Task 4 dispatch/diff 每条错误分支都有结构化上下文日志；纯函数无外部调用，不人为加入 print。
- 范围污染：B205 工作树可达性、内容质量、执行者 handoff CLI、历史补挂和多产物数组均明确不在本计划。

## 上下文预算与边界类型清单

每个 task 的文件范围已在各自标题列出且互不扩散：Task 1 只碰 ledger 模型/持久化；Task 2 只碰 ledgerstep 纯函数；Task 3 只碰 NodeStep；Task 4 只碰 dispatch/runner；Task 5 只碰 wire/UI/config/docs。若实现中需要超出该文件集，先拆新的竖切卡，不在本计划中隐式扩大范围。

边界型行为验收必须逐项真实跑到：

- Go：ledger round-trip/validation、ledgerstep output/node/prompt/runner、agentd HTTP wire、proto fixture。
- JSON：legacy produces 缺失、显式零对象、非零 kind/path；SQLite 存储前后字段值不变。
- Web：NodeDef TS 编译、contract fixture import、NodeEditor partial/complete/undefined。
- 配置：charter JSON 合法；contract/breakdown/plan 的 produces kind/path 与各 gate 完全匹配。
- 真实派发：实现最终验收需验证 executor 收到法定路径且 pass 后路由；本计划只规定 prompt 与注入 seam，不把真机判据冒充已经跑过。

## 计划自审记录

- spec 覆盖：五条用户故事均已指到具体 task、具体测试与配置字段。
- 占位符扫描：本计划不使用 TBD、TODO、同 Task N 或“适当错误处理”；代码块中的省略号只保留为 TypeScript 对象展开运算符和 go test 的包 glob，不代表待填实现；Go/TS Interfaces 已列出完整字段。
- 跨 task 类型/签名：已按上方八条审计逐字对齐，尤其是 NodeOutput 指针、buildPrompt 七参数、OutputPath/Diff/Attach 三个 hook。
- 依赖事实出处：Store.AttachFile 幂等、gate kind-only、NodeStep 当前 route 顺序、Client.Diff 签名、wire projection、附件白名单均已在台账写明源码行号；web 依赖不可用事实也已写原始报错。
- 派发系统动作：本计划没有任何需要驱动 handoff 的验收步骤；若未来 implement 需要该动作，由协调者执行，不派发给本卡执行者。
- 本节点红线：只生成并提交此计划和台账，不写实现代码、不启动 executor、不调用 handoff CLI。

---

## 本轮任务：修 F1（2026-08-23）

> **执行者请注意：这一节是任务单，不是检查表。**
> 下面描述的缺陷**现在就是没修的状态**——那是预期的，**你的活就是把它修掉并提交**。
> 不要只去核对「修了没有」然后报告未修复：2026-08-23 已经有一轮这么干了，
> 它把自己当成审阅轮，跑了 6 分钟、零改动、报 pass=false，等于白跑。
> 本卡实现主体（提交 `e8f94b764`）已经审过，**不要重做**；本轮只动
> `internal/ledgerstep/output.go` 的 `ChangedPaths` 与它的用例。

审阅轮报了 5 条 findings。协调者逐条复核后**只有一条成立**，本轮只修它；其余四条的
裁定写在最后，**不要去动它们**。

### F1（唯一要修的，major）：ChangedPaths 把 hunk 内容行当成了改动文件

`internal/ledgerstep/output.go` 的 `ChangedPaths` 无条件把任何以 `--- ` / `+++ ` 开头的
行当作文件头。但在 unified diff 里，**hunk 正文中一行原本以 `++ ` 开头的内容，会渲染成
`+++ ...`**——于是文档正文里引用的一段 diff 会被解析成「本轮改动了那个文件」。

协调者本机探针实测（临时用例，跑完已删）：

```
输入：一份只改了 docs/superpowers/ledgers/x.md 的 diff，其 hunk 正文里引用了
      "+++ b/docs/superpowers/plans/b201-plan.md" 这一行
解析出的路径清单 = ["docs/superpowers/ledgers/x.md" "docs/superpowers/plans/b201-plan.md"]
```

**为什么这条是 major 而不是洁癖**：本卡的存在性校验就是
`containsPath(changedPaths, declaredPath)`。于是**一个什么都没干、只提交了台账的轮次，
只要台账正文里引用了产出物路径所在的那行 diff，就能通过校验并把附件挂上**——本机制
本来是要挡住「节点空转」的，这个解析漏洞反而给它发通行证。而本仓每个执行者台账都在
粘命令原始输出，里面就带 diff，触发条件一点都不罕见。

#### 要求

1. **按 unified diff 的结构解析，不要按行首字符猜**：`--- ` / `+++ ` 只有出现在文件头
   区段（`diff --git` 之后、该文件第一个 `@@` 之前）时才算路径；一旦进入 hunk 正文，
   直到下一个 `diff --git` 之前，所有 `---` / `+++` 开头的行一律当内容忽略。
   `rename from/to` 同理只在文件头区段内有效。
2. **既有的 `ChangedPaths` 用例一条都不许改**（它们是这次收紧的防回归网）。若确有必要
   改，必须在报文里逐条说明改了什么、为什么。
3. **注释要写清「为什么不能按行首字符判」**，把上面那个 `++ ` → `+++ ` 的渲染事实写进去
   ——这是个反直觉的点，不写下次还会被改回去。

#### 必须新增的用例（先红后绿，红的原始输出进台账）

- hunk 正文里含 `+++ b/<某路径>` 与 `--- a/<某路径>` → 结果**只有**真正改动的那个文件；
- 文件头区段的 `--- a/x` / `+++ b/x` 仍被正确解析（防止修过头）；
- 新增文件（`--- /dev/null`）与 rename 两个既有场景仍正确。

#### 验收判据

- `gofmt -l .` 无输出；`go vet ./...` 与 `go build ./...` 退出码 0；
- `go test ./internal/ledgerstep/ ./internal/ledger/` 全绿；
- **变异证明**：把第 1 条的结构判定改回「按行首字符判」，新增的第一条用例必须变红、
  其余保持绿；改回后复跑全绿。没跑到这个结果不许写 pass。

### 其余四条的裁定：都不修，不要动

- **「Web typecheck / Vitest 未验证」**：那是执行机的环境限制（tsc 不在 PATH、npm 缓存
  EROFS），不是缺陷。**协调者已在本机补跑**：`npx tsc -b --noEmit` 退出码 0；
  `npx vitest run` 退出码 0，116 文件 / 1139 用例全绿。已结清。
- **「HTTP produces round-trip 测试被 SKIP，序列化边界未验」**：同样是环境。协调者本机
  实测 `TestFlowNodeProducesRoundTripsThroughHTTPWire` **PASS（1.86s）**。已结清。
- **「produces.kind 未按附件白名单校验」**：不改。今天 `Gate.RequireAttachment` 同样不做
  白名单校验，produces 与它保持一致才是对的；单独给 produces 加校验会让 `internal/ledger`
  反向依赖 `internal/agentd` 的 `attachmentKinds`。
- **「裁决落账到挂载/路由之间无重启恢复保障」**：不改，越界。那是整个 `RunOnce` 的既有
  性质（本卡之前就是这样），属于 B185「回合状态落账可恢复」的范围。
