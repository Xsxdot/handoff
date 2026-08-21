# codegraph 目标图与契约对照机制（graph check）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地 `codegraph/target.json`（事前基准：域划分/域类型/入口级契约面）+ `handoff graph check`（机械对照实际图，legacy 预算棘轮）+ `handoff graph absorb`（diff 回灌 baseline），并把消费点写进分域协议三模板。

**Architecture:** 全部图算法进 `internal/codegraph`（该包**不依赖 handoff 任何内部包**，这是既有硬约束，见包头注释）；CLI 薄壳进 `cmd/graph.go`（cobra 既有模式：`RunE` + `defer graphResetState()` + `graphPrintJSON`）；协议集成只改 `internal/ledger/templates.go` 的三个 prompt 常量与 `internal/discipline/builtin/` 五个纪律块文本。spec：`docs/superpowers/specs/2026-08-21-codegraph-target-check-design.md`。

**Tech Stack:** Go（stdlib only，不引入 glob 库）、cobra、既有 testdata 夹具仓模式。

## Global Constraints

- `internal/codegraph` 不得 import handoff 任何内部包（包头注释的硬约束，破坏即失败）。
- 图内文件路径一律 `/` 分隔（JSON 数据即如此）；路径比较用 `strings` 前缀/相等，**不用 `filepath`**（避免 Windows 假红）。
- 错误必须带中文上下文（`fmt.Errorf("加载目标图: %w", err)` 风格，与包内现状一致）；禁止 `fmt.Printf` 当日志；CLI 用户反馈走 `cmd.OutOrStdout()`/`cmd.ErrOrStderr()`（cmd 包既有模式）。
- 每个 task 完成即 commit；测试跑 `-count=1`；**每次 commit 前跑 `gofmt -l .` 确认无输出**（executor 的 ledger 历史上漏过 gofmt）。
- target paths 语法只支持两种形态：精确文件路径、`dir/**` 前缀——其他写法 ValidateTarget 报错（spec §4）。
- 本 plan 触及的域全部是**逻辑域**（本机文件/DB，测试可闭环）。唯一外部接触点是 absorb CLI 缺省从 git 取 commit 号，已设计为可注入 flag，测试不碰真 git。

---

### Task 1: Target 类型、加载与校验

**Files:**
- Create: `internal/codegraph/target.go`
- Create: `internal/codegraph/target_test.go`
- Create: `internal/codegraph/testdata/repo/codegraph/target.json`（夹具）

**Interfaces:**
- Consumes: 无（仅 stdlib）
- Produces: `type Target / TargetMeta / TargetDomain / Assignment / Contract`；`LoadTarget(repoRoot string) (*Target, error)`；`ValidateTarget(t *Target) []string`。后续 Task 2/4/5/8 依赖这些签名。

- [ ] **Step 1: 写失败测试**

`internal/codegraph/target_test.go`：

```go
package codegraph

import (
	"strings"
	"testing"
)

func TestLoadTarget(t *testing.T) {
	tg, err := LoadTarget("testdata/repo")
	if err != nil {
		t.Fatalf("加载目标图: %v", err)
	}
	if tg.Meta.Version != 1 || len(tg.Domains) == 0 {
		t.Fatalf("meta/domains 解析不对: %+v", tg.Meta)
	}
}

// 缺失必须是显式错误——check 无基准静默通过是本机制的头号静默失败模式（spec §5）。
func TestLoadTargetMissingIsError(t *testing.T) {
	if _, err := LoadTarget(t.TempDir()); err == nil {
		t.Fatal("target 缺失应报错，不能返回 nil,nil")
	}
}

func TestValidateTarget(t *testing.T) {
	bad := &Target{
		Meta: TargetMeta{Version: 1},
		Domains: []TargetDomain{
			{ID: "d_a", Name: "A", Type: "logic", Paths: []string{"pkg/**"}},
			{ID: "d_a", Name: "重复", Type: "magic", Paths: []string{"[bad"}},
		},
		Assignments: []Assignment{{Path: "x.go", Domain: "d_nope"}},
		Contracts:   []Contract{{From: "d_a", To: "d_nope", LegacyBudget: -1}},
	}
	issues := ValidateTarget(bad)
	for _, want := range []string{"重复", "type", "paths", "d_nope", "legacyBudget"} {
		found := false
		for _, is := range issues {
			if strings.Contains(is, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("缺少对 %q 的校验报告，实际: %v", want, issues)
		}
	}
}

// legacyBudget 缺省与 0 同义 = 硬拦（spec §4 钉死的语义）。
func TestContractBudgetDefaultZero(t *testing.T) {
	var c Contract
	if c.LegacyBudget != 0 {
		t.Fatal("缺省预算必须是 0（硬拦）")
	}
}
```

夹具 `internal/codegraph/testdata/repo/codegraph/target.json`（与既有 testdata 仓的 svc/cmd 布局对齐）：

```json
{
  "meta": { "version": 1, "project": "fixture" },
  "domains": [
    { "id": "d_svc", "name": "服务", "type": "logic", "paths": ["svc/**"] },
    { "id": "d_cmd", "name": "入口", "type": "logic", "paths": ["cmd/**"] }
  ],
  "assembly": ["cmd/run.go"],
  "contracts": [
    { "from": "d_cmd", "to": "d_svc", "entries": ["svc.Server"], "legacyBudget": 0 }
  ]
}
```

- [ ] **Step 2: 跑测试确认失败**（`go test ./internal/codegraph/ -run 'Target|ContractBudget' -count=1`，期望 undefined 编译错）

- [ ] **Step 3: 实现 target.go**

```go
// 本文件实现目标图 target.json 的模型、加载与校验（spec
// docs/superpowers/specs/2026-08-21-codegraph-target-check-design.md §4）。
//
// 职责：类型定义、LoadTarget、ValidateTarget、归域 DomainOf（Task 2）
// 边界：不做对照（check.go）；不写文件——target 是人写的，程序只读
package codegraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TargetMeta 目标图来源信息。
type TargetMeta struct {
	Version int    `json:"version"`
	Project string `json:"project"`
}

// TargetDomain 一个声明的域。Type 二选一：logic / boundary（分域协议的域类型标注）。
type TargetDomain struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Paths []string `json:"paths"`
	Note  string   `json:"note,omitempty"`
}

// Assignment 例外文件的显式归域，优先级高于 paths 规则。
type Assignment struct {
	Path   string `json:"path"`
	Domain string `json:"domain"`
}

// Contract 一个允许的跨域依赖方向 from → to。
// Entries：允许 call 边进入的 to 域容器 Label 清单（pkg.Receiver 规范形）。
// Interfaces：允许 to 域跨域实现的 from 域接口节点 Name 清单（回调契约面）。
// LegacyBudget：不走声明入口的存量直调边上限；缺省 0 = 硬拦（与缺失同义，spec §4）。
type Contract struct {
	From         string   `json:"from"`
	To           string   `json:"to"`
	Entries      []string `json:"entries,omitempty"`
	Interfaces   []string `json:"interfaces,omitempty"`
	LegacyBudget int      `json:"legacyBudget,omitempty"`
}

// Target 是 codegraph/target.json 的顶层结构：事前基准。
type Target struct {
	Meta        TargetMeta     `json:"meta"`
	Domains     []TargetDomain `json:"domains"`
	Assignments []Assignment   `json:"assignments,omitempty"`
	Assembly    []string       `json:"assembly,omitempty"`
	Contracts   []Contract     `json:"contracts,omitempty"`
}

// LoadTarget 读取 repoRoot/codegraph/target.json。
// 文件缺失或解析失败都是显式错误——check 的调用方绝不允许把「无基准」
// 当「通过」（spec §5 反静默约定）。
func LoadTarget(repoRoot string) (*Target, error) {
	p := filepath.Join(repoRoot, "codegraph", "target.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("加载目标图 %s: %w", p, err)
	}
	var t Target
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("解析目标图 %s: %w", p, err)
	}
	return &t, nil
}

// validPathRule 判断归域规则语法：精确路径或 "dir/**" 前缀，仅此两种（spec §4）。
func validPathRule(rule string) bool {
	if rule == "" || strings.ContainsAny(rule, "[]?{}") {
		return false
	}
	// "dir/**" 之外不允许出现 *；裸 "**" 会把整个仓库圈进一个域，禁止。
	if i := strings.Index(rule, "*"); i >= 0 {
		return strings.HasSuffix(rule, "/**") && !strings.Contains(strings.TrimSuffix(rule, "/**"), "*")
	}
	return true
}

// ValidateTarget 校验目标图内部一致性，返回问题清单（空 = 合法）。
func ValidateTarget(t *Target) []string {
	var issues []string
	ids := make(map[string]bool, len(t.Domains))
	for _, d := range t.Domains {
		if ids[d.ID] {
			issues = append(issues, fmt.Sprintf("域 id %q 重复", d.ID))
		}
		ids[d.ID] = true
		if d.Type != "logic" && d.Type != "boundary" {
			issues = append(issues, fmt.Sprintf("域 %s 的 type 取值非法: %q（只认 logic/boundary）", d.ID, d.Type))
		}
		for _, p := range d.Paths {
			if !validPathRule(p) {
				issues = append(issues, fmt.Sprintf("域 %s 的 paths 规则 %q 语法非法（只支持精确路径或 dir/**）", d.ID, p))
			}
		}
	}
	for _, a := range t.Assignments {
		if !ids[a.Domain] {
			issues = append(issues, fmt.Sprintf("assignments %s 指向不存在的域 %q", a.Path, a.Domain))
		}
	}
	for _, c := range t.Contracts {
		for _, ref := range []string{c.From, c.To} {
			if !ids[ref] {
				issues = append(issues, fmt.Sprintf("契约 %s→%s 引用不存在的域 %q", c.From, c.To, ref))
			}
		}
		if c.LegacyBudget < 0 {
			issues = append(issues, fmt.Sprintf("契约 %s→%s 的 legacyBudget 不能为负", c.From, c.To))
		}
	}
	return issues
}
```

- [ ] **Step 4: 跑测试确认全绿**
- [ ] **Step 5: 加注释自检**（文件头职责边界注释、导出类型/函数 doc 注释已含在上方代码里；确认无遗漏）
- [ ] **Step 6: `gofmt -l .` 无输出后 commit**：`feat(codegraph): target.json 模型/加载/校验`

**验收（缺陷族结论）**：静默失败族——缺失即错的测试钉死；假红族——测试全部走 `t.TempDir()` 与包内 testdata，不碰 git；序列化边界——LoadTarget 测试穿真实 JSON 文件，不是内存构造。

---

### Task 2: 归域 DomainOf

**Files:**
- Modify: `internal/codegraph/target.go`（追加方法）
- Modify: `internal/codegraph/target_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Target/Assignment/TargetDomain`
- Produces: `(t *Target) DomainOf(file string) string`（"" = 图外）。Task 4 的 Check 依赖它。

- [ ] **Step 1: 写失败测试**

```go
func TestDomainOf(t *testing.T) {
	tg := &Target{
		Domains: []TargetDomain{
			{ID: "d_svc", Type: "logic", Paths: []string{"svc/**"}},
			{ID: "d_cmd", Type: "logic", Paths: []string{"cmd/run.go"}},
		},
		Assignments: []Assignment{{Path: "svc/mirror.go", Domain: "d_cmd"}},
	}
	cases := []struct{ file, want string }{
		{"svc/task.go", "d_svc"},        // 前缀规则
		{"svc/mirror.go", "d_cmd"},      // assignments 优先于 paths
		{"cmd/run.go", "d_cmd"},         // 精确规则
		{"web/x.ts", ""},                // 图外
		{"svcx/task.go", ""},            // 前缀必须整段匹配，svcx 不是 svc/
	}
	for _, c := range cases {
		if got := tg.DomainOf(c.file); got != c.want {
			t.Errorf("DomainOf(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**
- [ ] **Step 3: 实现**

```go
// DomainOf 返回 file 的归属域 id，"" 表示图外。
// 三级优先：assignments 精确指派 > 域 paths 规则 > 图外（spec §4）。
// file 与规则都是 '/' 分隔的仓内相对路径——图数据即此形态，不做 filepath 转换。
func (t *Target) DomainOf(file string) string {
	for _, a := range t.Assignments {
		if a.Path == file {
			return a.Domain
		}
	}
	for _, d := range t.Domains {
		for _, rule := range d.Paths {
			if rule == file {
				return d.ID
			}
			if prefix, ok := strings.CutSuffix(rule, "/**"); ok && strings.HasPrefix(file, prefix+"/") {
				return d.ID
			}
		}
	}
	return ""
}
```

- [ ] **Step 4: 跑测试全绿；`gofmt -l .`；commit**：`feat(codegraph): 目标图三级归域 DomainOf`

**验收（缺陷族结论）**：跨平台假设族——`svcx` 用例钉死「整段前缀」语义，路径全程 `/` 比较；上下文预算——本 task 只动 target.go 一个文件。

---

### Task 3: implements 边表（schema + validate + merge + 配方文档）

**Files:**
- Modify: `internal/codegraph/types.go`（Graph/Diff 增字段）
- Modify: `internal/codegraph/merge.go`（View 增 Implements 并合并）
- Modify: `internal/codegraph/validate.go`（引用完整性）
- Modify: `internal/codegraph/merge_test.go`、`internal/codegraph/validate_test.go`
- Modify: `internal/codegraph/testdata/repo/codegraph/baseline.json`、`internal/codegraph/testdata/repo/codegraph/diffs/branch-x.json`（夹具补 implements）
- Modify: `docs/codegraph-scan-recipe.md`（schema 表补三字段 + 扫描规则一条）

**Interfaces:**
- Consumes: 既有 `Edge [2]string`、`Graph/Diff/View`、`Merge/Validate/ValidateDiff`
- Produces: `Graph.Implements []Edge`、`Diff.ImplementsAdded/ImplementsDeleted []Edge`、`View.Implements []ViewEdge`。Task 4 的 Check 与 Task 6 的 Absorb 依赖。

- [ ] **Step 1: 写失败测试**

merge_test.go 追加（**穿真实 JSON 加载链**，不在内存拼 Graph——序列化边界设问的要求）：

```go
// implements 边必须穿过 LoadGraph→LoadDiff→Merge 全链出现在视图里。
// 只测内存构造会漏掉 json tag 拼写错这类 wire 缺陷（ChildrenTotal 前科）。
func TestMergeImplementsThroughWire(t *testing.T) {
	g, err := LoadGraph("testdata/repo")
	if err != nil {
		t.Fatalf("加载基线: %v", err)
	}
	if len(g.Implements) == 0 {
		t.Fatal("夹具基线应含 implements 边")
	}
	d, err := LoadDiff("testdata/repo", "branch-x")
	if err != nil {
		t.Fatalf("加载 diff: %v", err)
	}
	v := Merge(g, d)
	var added, kept int
	for _, e := range v.Implements {
		switch e.Status {
		case "added":
			added++
		case "":
			kept++
		}
	}
	if kept == 0 || added == 0 {
		t.Fatalf("视图 implements 合并不对: kept=%d added=%d", kept, added)
	}
}
```

validate_test.go 追加：

```go
func TestValidateImplementsRefs(t *testing.T) {
	g := &Graph{
		Containers: map[string]Container{"k": {Label: "svc"}},
		Nodes:      map[string]Node{"n1": {Container: "k", File: "svc/a.go"}},
		Implements: []Edge{{"n1", "n_missing"}},
	}
	issues := Validate(g)
	found := false
	for _, is := range issues {
		if strings.Contains(is, "implements") && strings.Contains(is, "n_missing") {
			found = true
		}
	}
	if !found {
		t.Fatalf("implements 悬空引用未报: %v", issues)
	}
}
```

夹具改动：`testdata/repo/codegraph/baseline.json` 加一个接口节点与 `"implements": [["实现节点id","接口节点id"]]`；`diffs/branch-x.json` 加 `"implementsAdded"` 一条（引用 diff 里已有的 added 节点）。按夹具现有节点 id 就地取材。

- [ ] **Step 2: 跑测试确认失败**
- [ ] **Step 3: 实现**——types.go：

```go
// Graph 增（Edges 之后）：
	// Implements 是接口满足边 [实现, 接口]。与 Edges 分列是 wire 兼容决策
	//（Edge 是二元组塞不进 kind 字段，spec §3）；语义上它们是 kind=implements 的边。
	Implements []Edge `json:"implements,omitempty"`

// Diff 增：
	ImplementsAdded   []Edge `json:"implementsAdded,omitempty"`
	ImplementsDeleted []Edge `json:"implementsDeleted,omitempty"`
```

merge.go：`View` 增 `Implements []ViewEdge`；Merge 里按既有 Edges 的合并写法（deleted 保留标 deleted、added 标 added）同构处理 Implements——直接对照函数内 Edges 段落抄结构，改字段名。validate.go：`Validate` 对 `g.Implements` 每条边校验两端节点存在（报文含 "implements" 字样）；`ValidateDiff` 对 `ImplementsAdded/Deleted` 校验引用（基线节点 ∪ nodesAdded）。

- [ ] **Step 4: 跑包内全部测试确认绿**（既有测试不许转红：`go test ./internal/codegraph/ -count=1`）
- [ ] **Step 5: 配方文档**——`docs/codegraph-scan-recipe.md`：schema 表补 `implements` / `implementsAdded` / `implementsDeleted` 三行（元素 `[实现节点id, 接口节点id]`）；「怎么扫」节加一条：「接口类型建 model 节点；扫到 `var _ Iface = (*Impl)(nil)`、方法集满足或显式注入处，为每个实现产一条 implements 边；接口节点归**使用方**的容器/域，实现节点归提供方（消费者侧接口惯例，spec §3）」。
- [ ] **Step 6: `gofmt -l .`；commit**：`feat(codegraph): implements 边表贯通 schema/validate/merge 与扫描配方`

**验收（缺陷族结论）**：序列化边界——TestMergeImplementsThroughWire 穿真实 json 文件链；假红——夹具改动后既有全部测试必须仍绿（防夹具改动去牙齿：不动任何既有断言）。

---

### Task 4: Check 引擎

**Files:**
- Create: `internal/codegraph/check.go`
- Create: `internal/codegraph/check_test.go`

**Interfaces:**
- Consumes: Task 1 `Target/Contract`、Task 2 `DomainOf`、Task 3 `View.Implements`
- Produces: `type Finding{Kind,From,To,Edge,Detail}`、`type Report{Fails,Warns []Finding; LegacyHits map[string]int}`、`Check(t *Target, v *View) *Report`。Task 5/8 依赖。

- [ ] **Step 1: 写失败测试**（表驱动；View 在内存构造——check 的输入契约已在 Task 3 被 wire 测试罩住，这里测判定逻辑本身）

```go
package codegraph

import "testing"

// mkView 拼一个最小视图：nodes 映射 id→(container,file)，edges/impls 是边表。
func mkView(nodes map[string][2]string, edges, impls [][2]string) *View {
	v := &View{Containers: map[string]Container{}, Nodes: map[string]ViewNode{}}
	for id, cf := range nodes {
		v.Containers[cf[0]] = Container{Label: cf[0]}
		v.Nodes[id] = ViewNode{Node: Node{Container: cf[0], File: cf[1], Name: id}}
	}
	for _, e := range edges {
		v.Edges = append(v.Edges, ViewEdge{From: e[0], To: e[1]})
	}
	for _, e := range impls {
		v.Implements = append(v.Implements, ViewEdge{From: e[0], To: e[1]})
	}
	return v
}

func twoDomainTarget(entries []string, budget int) *Target {
	return &Target{
		Meta: TargetMeta{Version: 1},
		Domains: []TargetDomain{
			{ID: "d_a", Type: "logic", Paths: []string{"a/**"}},
			{ID: "d_b", Type: "logic", Paths: []string{"b/**"}},
		},
		Contracts: []Contract{{From: "d_a", To: "d_b", Entries: entries, LegacyBudget: budget}},
	}
}

func TestCheckTable(t *testing.T) {
	nodes := map[string][2]string{
		"a1": {"a.Server", "a/s.go"}, "b1": {"b.Facade", "b/f.go"}, "b2": {"b.Store", "b/st.go"},
	}
	cases := []struct {
		name          string
		tg            *Target
		edges, impls  [][2]string
		wantFailKinds []string
		wantWarnKinds []string
	}{
		{"域内边不检查", twoDomainTarget(nil, 0), [][2]string{{"b1", "b2"}}, nil, nil, nil},
		{"走声明入口合法", twoDomainTarget([]string{"b.Facade"}, 0), [][2]string{{"a1", "b1"}}, nil, nil, nil},
		{"越界但有预算=warn", twoDomainTarget([]string{"b.Facade"}, 1), [][2]string{{"a1", "b2"}}, nil, nil, []string{"legacy"}},
		{"越界超预算=fail", twoDomainTarget([]string{"b.Facade"}, 0), [][2]string{{"a1", "b2"}}, nil, []string{"over-budget"}, nil},
		{"无契约方向=fail", &Target{Meta: TargetMeta{Version: 1}, Domains: twoDomainTarget(nil, 0).Domains},
			[][2]string{{"a1", "b1"}}, nil, []string{"new-direction"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := Check(c.tg, mkView(nodes, c.edges, c.impls))
			assertKinds(t, "fail", rep.Fails, c.wantFailKinds)
			assertKinds(t, "warn", rep.Warns, c.wantWarnKinds)
		})
	}
}

func assertKinds(t *testing.T, label string, got []Finding, want []string) {
	t.Helper()
	if len(want) == 0 && len(got) != 0 {
		t.Fatalf("%s 应为空，实际: %+v", label, got)
	}
	for _, k := range want {
		found := false
		for _, f := range got {
			if f.Kind == k {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s 缺 kind=%s，实际: %+v", label, k, got)
		}
	}
}

// implements：接口在 d_a（使用方），实现落 d_b。声明了才合法。
func TestCheckImplements(t *testing.T) {
	nodes := map[string][2]string{
		"iface": {"a.Notifier", "a/n.go"}, "impl": {"b.Hook", "b/h.go"},
	}
	tg := twoDomainTarget(nil, 0)
	tg.Contracts[0].Interfaces = []string{"iface"}
	rep := Check(tg, mkView(nodes, nil, [][2]string{{"impl", "iface"}}))
	if len(rep.Fails) != 0 {
		t.Fatalf("已声明接口应合法: %+v", rep.Fails)
	}
	tg.Contracts[0].Interfaces = nil
	rep = Check(tg, mkView(nodes, nil, [][2]string{{"impl", "iface"}}))
	assertKinds(t, "fail", rep.Fails, []string{"off-interface"})
}

// 组装点出边豁免；deleted 状态的边不检查；图外文件与死规则进 warn。
func TestCheckExemptionsAndWarns(t *testing.T) {
	nodes := map[string][2]string{
		"main": {"main", "cmd/main.go"}, "b1": {"b.Facade", "b/f.go"}, "out": {"x", "web/x.ts"},
	}
	tg := &Target{
		Meta: TargetMeta{Version: 1},
		Domains: []TargetDomain{
			{ID: "d_cmd", Type: "logic", Paths: []string{"cmd/**"}},
			{ID: "d_b", Type: "logic", Paths: []string{"b/**"}},
			{ID: "d_dead", Type: "logic", Paths: []string{"ghost/**"}},
		},
		Assembly: []string{"cmd/main.go"},
	}
	v := mkView(nodes, [][2]string{{"main", "b1"}}, nil)
	v.Edges = append(v.Edges, ViewEdge{From: "b1", To: "out", Status: "deleted"})
	rep := Check(tg, v)
	if len(rep.Fails) != 0 {
		t.Fatalf("组装豁免/deleted 边不应 fail: %+v", rep.Fails)
	}
	assertKinds(t, "warn", rep.Warns, []string{"outside-file", "dead-rule"})
}
```

- [ ] **Step 2: 跑测试确认失败**
- [ ] **Step 3: 实现 check.go**

```go
// 本文件实现目标图对实际图的契约对照（spec §5）。
//
// 职责：Check——归域、逐边判定、legacy 预算结算，产出 Report
// 边界：不做 I/O、不打日志——纯函数，可观测性由返回的 Report 承担；
//	加载与退出码语义在 cmd 层
package codegraph

import "fmt"

// Finding 一条对照发现。Kind 取值：
// fail 侧：new-direction（无契约方向）/ off-entry 归并进 legacy 或 over-budget /
// off-interface（未声明的跨域实现）/ over-budget（legacy 超预算）
// warn 侧：legacy（预算内直调计数）/ outside-file（图外文件）/ dead-rule（规则未命中任何节点）
type Finding struct {
	Kind   string `json:"kind"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Edge   *Edge  `json:"edge,omitempty"`
	Detail string `json:"detail"`
}

// Report 是 Check 的产出。Fails 非空即闸门不过（cmd 层译成非零退出码）。
type Report struct {
	Fails      []Finding      `json:"fails"`
	Warns      []Finding      `json:"warns"`
	LegacyHits map[string]int `json:"legacyHits,omitempty"` // "from->to" → 命中数
}

// Check 把合并视图 v 套在目标图 t 上对照。算法四步见 spec §5。
// deleted 状态的节点/边不参与——它们只为渲染保留。
func Check(t *Target, v *View) *Report {
	rep := &Report{Fails: []Finding{}, Warns: []Finding{}, LegacyHits: map[string]int{}}
	assembly := make(map[string]bool, len(t.Assembly))
	for _, f := range t.Assembly {
		assembly[f] = true
	}
	contracts := make(map[string]*Contract, len(t.Contracts))
	for i := range t.Contracts {
		c := &t.Contracts[i]
		contracts[c.From+"->"+c.To] = c
	}
	// 归域 + 图外收集（每文件报一次）
	nodeDomain := make(map[string]string, len(v.Nodes))
	outside := map[string]bool{}
	fileHit := map[string]bool{} // 供死规则检测：哪些文件被规则命中过
	for id, n := range v.Nodes {
		if n.Status == "deleted" {
			continue
		}
		d := t.DomainOf(n.File)
		nodeDomain[id] = d
		if d == "" {
			outside[n.File] = true
		} else {
			fileHit[n.File] = true
		}
	}
	// call 边
	for i := range v.Edges {
		e := v.Edges[i]
		if e.Status == "deleted" {
			continue
		}
		from, to := nodeDomain[e.From], nodeDomain[e.To]
		if from == "" || to == "" || from == to {
			continue // 图外已单独 warn；域内不检查
		}
		if callerNode, ok := v.Nodes[e.From]; ok && assembly[callerNode.File] {
			continue // 组装点豁免（依赖注入的绑定边）
		}
		c := contracts[from+"->"+to]
		if c == nil {
			rep.Fails = append(rep.Fails, Finding{Kind: "new-direction", From: from, To: to,
				Edge: &Edge{e.From, e.To}, Detail: fmt.Sprintf("跨域方向 %s→%s 无契约条目", from, to)})
			continue
		}
		label := ""
		if callee, ok := v.Nodes[e.To]; ok {
			label = v.Containers[callee.Container].Label
		}
		if inList(c.Entries, label) {
			continue
		}
		rep.LegacyHits[from+"->"+to]++
	}
	// implements 边：实现(from 侧域=to 契约方) → 接口(from 契约方)
	for i := range v.Implements {
		e := v.Implements[i]
		if e.Status == "deleted" {
			continue
		}
		implDom, ifaceDom := nodeDomain[e.From], nodeDomain[e.To]
		if implDom == "" || ifaceDom == "" || implDom == ifaceDom {
			continue
		}
		c := contracts[ifaceDom+"->"+implDom]
		ifaceName := ""
		if n, ok := v.Nodes[e.To]; ok {
			ifaceName = n.Name
		}
		if c == nil || !inList(c.Interfaces, ifaceName) {
			rep.Fails = append(rep.Fails, Finding{Kind: "off-interface", From: ifaceDom, To: implDom,
				Edge: &Edge{e.From, e.To},
				Detail: fmt.Sprintf("跨域实现未声明: %s 实现了 %s 的 %s", implDom, ifaceDom, ifaceName)})
		}
	}
	// 预算结算
	for key, hits := range rep.LegacyHits {
		c := contracts[key]
		if hits > c.LegacyBudget {
			rep.Fails = append(rep.Fails, Finding{Kind: "over-budget", From: c.From, To: c.To,
				Detail: fmt.Sprintf("%s 直调 %d 条超出预算 %d", key, hits, c.LegacyBudget)})
		} else {
			rep.Warns = append(rep.Warns, Finding{Kind: "legacy", From: c.From, To: c.To,
				Detail: fmt.Sprintf("%s 预算内直调 %d/%d（可收窄后调低预算）", key, hits, c.LegacyBudget)})
		}
	}
	// 图外文件 + 死规则
	for f := range outside {
		rep.Warns = append(rep.Warns, Finding{Kind: "outside-file", Detail: "图外文件（目标图未覆盖）: " + f})
	}
	for _, d := range t.Domains {
		for _, rule := range d.Paths {
			if !ruleHitsAny(rule, fileHit) {
				rep.Warns = append(rep.Warns, Finding{Kind: "dead-rule", From: d.ID,
					Detail: fmt.Sprintf("域 %s 的规则 %q 未命中视图中任何节点文件", d.ID, rule)})
			}
		}
	}
	sortFindings(rep) // 输出稳定排序，测试与 diff 可复现
	return rep
}
```

同文件三个小工具：

```go
func inList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ruleHitsAny 判断一条 paths 规则是否命中过任何已归域的节点文件。
func ruleHitsAny(rule string, fileHit map[string]bool) bool {
	prefix, isPrefix := strings.CutSuffix(rule, "/**")
	for f := range fileHit {
		if f == rule || (isPrefix && strings.HasPrefix(f, prefix+"/")) {
			return true
		}
	}
	return false
}

// sortFindings 把 Fails/Warns 按 Kind+Detail 排序——map 遍历序不定，
// 输出必须可复现，否则 CLI diff 与测试都不稳。
func sortFindings(rep *Report) {
	cmp := func(a, b Finding) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Detail, b.Detail)
	}
	slices.SortFunc(rep.Fails, cmp)
	slices.SortFunc(rep.Warns, cmp)
}
```

- [ ] **Step 4: 跑测试全绿**（含包内全部既有测试）
- [ ] **Step 5: `gofmt -l .`；commit**：`feat(codegraph): 契约对照引擎 Check`

**验收（缺陷族结论）**：生命周期族——deleted 节点/边跳过有专测；静默失败族——Check 是纯函数无静默路径，Report 空切片显式初始化（JSON 出 `[]` 不出 `null`，下游解析不歧义）；测试表驱动覆盖 fail/warn 全部 kind。

---

### Task 5: CLI `handoff graph check`

**Files:**
- Modify: `cmd/graph.go`（新子命令 + 注册）
- Modify: `cmd/graph_test.go`

**Interfaces:**
- Consumes: Task 1/4 的 `LoadTarget/ValidateTarget/Check`、既有 `graphLoadView/graphPrintJSON/graphResetState`
- Produces: `handoff graph check [--repo] [--view]`，stdout 是 Report 的缩进 JSON，有 fail 退出非零。Task 7 的模板文本、Task 8 的门测试引用此命令语义。

- [ ] **Step 1: 写失败测试**（cmd 包既有的进程内 cobra 模式；夹具 target 已在 Task 1 就位）

```go
// check：夹具 target 声明 svc.Server 为入口、预算 0——若夹具图存在
// cmd→svc 的越入口边则非零退出。这里用两个子用例钉两侧行为。
func TestGraphCheck(t *testing.T) {
	out := runGraph(t, "check", "--repo", "../internal/codegraph/testdata/repo") // cmd 测试 cwd 是 cmd/；路径与助手名以既有测试写法为准
	for _, want := range []string{`"fails"`, `"warns"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("check 输出缺字段 %s: %s", want, out)
		}
	}
}

func TestGraphCheckMissingTargetFails(t *testing.T) {
	// 指向一个没有 target.json 的仓：必须报错退出，不能静默通过。
	err := runGraphErr(t, "check", "--repo", t.TempDir())
	if err == nil {
		t.Fatal("无 target 的 check 必须失败")
	}
}
```

（`runGraph/runGraphErr` 按 cmd/graph_test.go 既有测试的执行方式实现/复用；若既有测试用别的助手名，跟随既有命名，不另起炉灶。夹具仓路径以既有测试写法为准。）

- [ ] **Step 2: 跑测试确认失败**
- [ ] **Step 3: 实现子命令**

```go
var graphCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "目标图契约对照：实际跨域边 ⊆ target.json 声明的契约面，违规即非零退出",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		t, err := codegraph.LoadTarget(graphRepo)
		if err != nil {
			// 无基准绝不静默通过——这是本机制的头号反静默约定（spec §5）
			return fmt.Errorf("目标图不可用，check 拒绝执行: %w", err)
		}
		if issues := codegraph.ValidateTarget(t); len(issues) > 0 {
			return fmt.Errorf("目标图自身不合法: %v", issues)
		}
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		rep := codegraph.Check(t, v)
		if err := graphPrintJSON(cmd, rep); err != nil {
			return err
		}
		if len(rep.Fails) > 0 {
			return fmt.Errorf("契约对照发现 %d 处违规", len(rep.Fails))
		}
		return nil
	},
}
```

注册：`graphCmd.AddCommand(...)` 行加入 `graphCheckCmd`。

- [ ] **Step 4: 跑 cmd 包测试全绿**
- [ ] **Step 5: `gofmt -l .`；commit**：`feat(cmd): graph check 子命令——契约对照闸门`

**验收（缺陷族结论）**：门禁绕过族——非零退出由测试钉死（err != nil），JSON 照常输出后再报错（人能看到 report 再看到失败）；假红族——`defer graphResetState()` 防 cobra 进程内复用串 flag（cmd 包既有教训）。

---

### Task 6: Absorb 回灌 + CLI `handoff graph absorb`

**Files:**
- Create: `internal/codegraph/absorb.go`
- Create: `internal/codegraph/absorb_test.go`
- Modify: `cmd/graph.go`（新子命令 + 注册 + flags）
- Modify: `cmd/graph_test.go`

**Interfaces:**
- Consumes: Task 3 的 Graph/Diff（含 implements 字段）
- Produces: `Absorb(g *Graph, d *Diff) *Graph`（纯函数，入参不变）、`SaveGraph(repoRoot string, g *Graph) error`（temp+rename 原子写）、`handoff graph absorb <view> [--commit] [--branch]`（併入→写盘→删 diff 文件）。

- [ ] **Step 1: 写失败测试**

```go
// absorb 后写盘再重载，图与併入结果逐字段等价——穿真实序列化边界，
// 只比内存对象会漏 json tag 缺陷。
func TestAbsorbRoundTrip(t *testing.T) {
	g, _ := LoadGraph("testdata/repo")
	d, _ := LoadDiff("testdata/repo", "branch-x")
	merged := Absorb(g, d)
	// nodesAdded 进图、nodesDeleted 出图、edgesAdded/implementsAdded 进表
	for id := range d.NodesAdded {
		if _, ok := merged.Nodes[id]; !ok {
			t.Fatalf("added 节点 %s 未併入", id)
		}
	}
	for _, id := range d.NodesDeleted {
		if _, ok := merged.Nodes[id]; ok {
			t.Fatalf("deleted 节点 %s 仍在", id)
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "codegraph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveGraph(dir, merged); err != nil {
		t.Fatalf("写盘: %v", err)
	}
	reloaded, err := LoadGraph(dir)
	if err != nil {
		t.Fatalf("重载: %v", err)
	}
	if !reflect.DeepEqual(merged, reloaded) {
		t.Fatal("写盘重载后不等价——序列化链路丢数据")
	}
}

// 入参不可变：absorb 失败重试的前提。
func TestAbsorbDoesNotMutateInput(t *testing.T) {
	g, _ := LoadGraph("testdata/repo")
	before := len(g.Nodes)
	d, _ := LoadDiff("testdata/repo", "branch-x")
	_ = Absorb(g, d)
	if len(g.Nodes) != before {
		t.Fatal("Absorb 改了入参 Graph")
	}
}
```

cmd 侧测试：absorb 一个临时仓（拷贝夹具到 t.TempDir()），断言 baseline 更新、diff 文件被删、`--commit abc123` 落进 meta；再断言**写盘失败路径不删 diff**（把 codegraph 目录设只读构造失败）。

- [ ] **Step 2: 跑测试确认失败**
- [ ] **Step 3: 实现 absorb.go**

```go
// 本文件实现 diff 回灌基线（spec §7）：分支合并回 main 后，把分支视图
// 机械併入 baseline，让基线保鲜成为流程副产物。
//
// 职责：Absorb（纯函数併入）、SaveGraph（原子写盘）
// 边界：不删 diff 文件、不取 git 信息——那是 cmd 层的编排；
//	不校验 diff 合法性——调用方先过 ValidateDiff
package codegraph

// Absorb 返回 g 併入 d 后的新图，入参不变（失败重试无损的前提）。
// 顺序：节点增/改/删 → 删除节点牵连的边一并剔除（否则 Validate 报悬空）→
// 边与 implements 增/删。
func Absorb(g *Graph, d *Diff) *Graph {
	out := &Graph{
		Meta:       g.Meta,
		Domains:    maps.Clone(g.Domains),
		Containers: maps.Clone(g.Containers),
		Nodes:      maps.Clone(g.Nodes),
		Edges:      slices.Clone(g.Edges),
		Implements: slices.Clone(g.Implements),
	}
	for id, n := range d.NodesAdded {
		out.Nodes[id] = n
	}
	for id, n := range d.NodesModified {
		n.SignatureOld = "" // 旧签名是 diff 展示用字段，不进基线
		out.Nodes[id] = n
	}
	dead := make(map[string]bool, len(d.NodesDeleted))
	for _, id := range d.NodesDeleted {
		dead[id] = true
		delete(out.Nodes, id)
	}
	out.Edges = mergeEdges(out.Edges, d.EdgesAdded, d.EdgesDeleted, dead)
	out.Implements = mergeEdges(out.Implements, d.ImplementsAdded, d.ImplementsDeleted, dead)
	return out
}

// mergeEdges 边表併入：加 added、剔 deleted、剔任一端指向已删节点的边，顺带去重。
func mergeEdges(base, added, deleted []Edge, dead map[string]bool) []Edge {
	drop := make(map[Edge]bool, len(deleted))
	for _, e := range deleted {
		drop[e] = true
	}
	seen := make(map[Edge]bool, len(base)+len(added))
	out := make([]Edge, 0, len(base)+len(added))
	for _, e := range append(slices.Clone(base), added...) {
		if drop[e] || dead[e[0]] || dead[e[1]] || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// SaveGraph 原子写 repoRoot/codegraph/baseline.json：同目录临时文件 + rename——
// 写盘半途失败不得留下截断的基线（生命周期中断族）。缩进单空格与既有基线一致。
func SaveGraph(repoRoot string, g *Graph) error {
	raw, err := json.MarshalIndent(g, "", " ")
	if err != nil {
		return fmt.Errorf("编码基线: %w", err)
	}
	dir := filepath.Join(repoRoot, "codegraph")
	tmp, err := os.CreateTemp(dir, "baseline-*.json")
	if err != nil {
		return fmt.Errorf("建临时基线文件: %w", err)
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("写临时基线: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("关闭临时基线: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, "baseline.json")); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("原子替换基线: %w", err)
	}
	return nil
}
```

（`maps`/`slices` 是 stdlib `maps`、`slices` 包。`Edge` 是 `[2]string` 数组、可比较，能直接当 map 键。）

cmd 子命令：

```go
var (
	absorbCommit string
	absorbBranch string
)

var graphAbsorbCmd = &cobra.Command{
	Use:   "absorb <view>",
	Short: "把分支视图 diff 併入 baseline 并删除该 diff（分支合并回主线后执行）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		g, err := codegraph.LoadGraph(graphRepo)
		if err != nil {
			return err
		}
		d, err := codegraph.LoadDiff(graphRepo, args[0])
		if err != nil {
			return err
		}
		if issues := codegraph.ValidateDiff(g, d); len(issues) > 0 {
			return fmt.Errorf("视图 %s 引用不完整，拒绝併入: %v", args[0], issues)
		}
		merged := codegraph.Absorb(g, d)
		// 刷新来源戳。--commit/--branch 未给时从 git 取；取不到就报错，
		// 不猜——基线的 meta 是审计锚点（worktree 版本戳说谎的前科）。
		merged.Meta.Commit, merged.Meta.Branch = absorbCommit, absorbBranch
		if merged.Meta.Commit == "" {
			if merged.Meta.Commit, err = gitHead(graphRepo); err != nil {
				return fmt.Errorf("取 HEAD 失败，请显式传 --commit: %w", err)
			}
		}
		if merged.Meta.Branch == "" {
			if merged.Meta.Branch, err = gitBranch(graphRepo); err != nil {
				return fmt.Errorf("取分支失败，请显式传 --branch: %w", err)
			}
		}
		if err := codegraph.SaveGraph(graphRepo, merged); err != nil {
			return err // 写盘失败：diff 保留，重试无损
		}
		diffPath := filepath.Join(graphRepo, "codegraph", "diffs", args[0]+".json")
		if err := os.Remove(diffPath); err != nil {
			return fmt.Errorf("基线已更新但删除 diff 失败（手动删除 %s）: %w", diffPath, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "已併入视图 %s：+%d 节点 ~%d -%d，基线 %d 节点 @%s\n",
			args[0], len(d.NodesAdded), len(d.NodesModified), len(d.NodesDeleted),
			len(merged.Nodes), merged.Meta.Commit)
		return nil
	},
}
```

（`gitHead/gitBranch` 是 cmd 包内小助手：`exec.Command("git","-C",repo,"rev-parse","HEAD")` / `branch --show-current`，错误带上下文返回。）

- [ ] **Step 4: 跑测试全绿**
- [ ] **Step 5: `gofmt -l .`；commit**：`feat(codegraph): absorb 回灌基线 + graph absorb 子命令`

**验收（缺陷族结论）**：生命周期中断族——写盘 temp+rename 原子、删 diff 严格在写盘成功之后、失败路径不删（有专测）；静默失败族——git 取号失败报错要求显式 flag，不猜；序列化边界——RoundTrip 测试写盘重载 DeepEqual。

---

### Task 7: 分域三模板接入 check + 纪律块只读子命令例外

**Files:**
- Modify: `internal/ledger/templates.go`（三个 prompt 常量）
- Modify: `internal/ledger/templates_test.go`
- Modify: `internal/discipline/builtin/plan-writing.md`、`review.md`、`finishing.md`、`single-context.md`、`spec-draft.md`（5 处禁令加例外）
- Modify: `internal/discipline` 的既有正文断言测试（如有——grep `不要调用 handoff CLI` 的测试引用，随文本同步）

**Interfaces:**
- Consumes: Task 5 的 `graph check` 命令语义
- Produces: 模板文本（数据）。无代码接口。

**背景（为什么要动纪律块）**：三模板将要求执行者跑 `handoff graph check`，而 5 个纪律块写着「不要调用 handoff CLI」一刀切禁令。B105 实测过这种冲突的代价：执行者只能如实记「未验证」，最承重的判据等于没验。红线必须给正当例外出口（不给出口会诱发绕过，2026-08 红线 grep 教训）。`graph` 子命令只读本地 `codegraph/*.json`、不触任务机器、不派发，例外是安全的。

- [ ] **Step 1: 改 5 个纪律块**——把每处 `不要调用 handoff CLI` 改为 `不要调用 handoff CLI（只读本地图数据的 handoff graph 子命令除外——它不碰任务、不派发、不触网）`。逐文件确认改后语句通顺。
- [ ] **Step 2: 改三个 prompt 常量**（`internal/ledger/templates.go`）：

`domainBreakdownPrompt` 两处增补——第 1 节句尾加：`域 id 与域类型引用项目根 codegraph/target.json 的 domains 段（有 target 的项目以它为准，没有才按域文档）。`；第 3 节③验收句尾加：`项目根有 codegraph/target.json 时，每张子卡验收一律追加一条：增量扫描触及文件产出分支视图 diff 后，handoff graph check --view <分支视图> 无 fail。`

`domainTicket0Prompt` 在「涉及域的领域文档条目一并更新。」后加：`契约增量涉及跨域接缝时，同步把入口/接口/预算写进 codegraph/target.json 的 contracts——契约冻结即提交该文件（对照机制见 docs/superpowers/specs/2026-08-21-codegraph-target-check-design.md）。`

`domainIntegrationPrompt` 的「做两件事」改「做三件事」，加第 3 条：`3. 契约对照：项目根有 codegraph/target.json 时，跑 handoff graph check（全量与分支视图各一次），fail 清单必须清零；若你的接线让某接缝 legacy 命中数下降，在报文里提请审核者调低对应 legacyBudget（棘轮只减不增）。`

- [ ] **Step 3: 更新 templates_test.go 断言**——对三个模板的 prompt 增加 `strings.Contains` 断言（各取增补句的一个不易撞车的片段，如 `graph check --view`、`契约冻结即提交该文件`、`棘轮只减不增`）；跑 `go test ./internal/ledger/ ./internal/discipline/... -count=1` 全绿。既有断言一条不许删。
- [ ] **Step 4: `gofmt -l .`；commit**：`feat(ledger): 分域三模板接入 graph check，纪律块给只读子命令例外`

**验收（缺陷族结论）**：门禁绕过族——例外范围钉死为「graph 子命令」，禁令主体原文保留；既有模板断言不删（防去牙齿）。注意：模板 seed 是幂等不覆盖的——已有 DB 的安装不会自动拿到新 prompt，属预期（模板不可变版本化），报文里说明即可，不要去改 seed 语义。

---

### Task 8: handoff 自身最小 target.json + 真数据门测试

**Files:**
- Create: `codegraph/target.json`（仓库根，真实数据）
- Create: `cmd/graph_gate_test.go`

**Interfaces:**
- Consumes: Task 1-5 全部
- Produces: handoff 仓库自身的契约闸（一个跑在真实 baseline 上的 go test）。

**做法（bootstrap 回路，两轮）**：

- [ ] **Step 1: 写门测试**（先写测试——它同时是本 task 的填数工具）

```go
// 本测试是 handoff 仓库自身的契约闸：真实 baseline 套真实 target。
// 它转红的含义不是「测试坏了」，是「出现了未声明的跨域依赖」——
// 处置是改走契约面或走契约变更调 target，不是改测试（spec §8）。
func TestRepoContractGate(t *testing.T) {
	tg, err := codegraph.LoadTarget("..")
	if err != nil {
		t.Fatalf("仓库 target.json 不可用: %v", err)
	}
	if issues := codegraph.ValidateTarget(tg); len(issues) > 0 {
		t.Fatalf("target 不合法: %v", issues)
	}
	g, err := codegraph.LoadGraph("..")
	if err != nil {
		t.Fatalf("加载仓库基线: %v", err)
	}
	rep := codegraph.Check(tg, codegraph.Merge(g, nil))
	for _, f := range rep.Fails {
		t.Errorf("契约违规 [%s] %s", f.Kind, f.Detail)
	}
	t.Logf("legacy 命中: %v，warn %d 条", rep.LegacyHits, len(rep.Warns))
}
```

- [ ] **Step 2: 写 target 骨架**——六顶层域（与 baseline 的顶层六域同名同划分），paths 按包路径：协作控制 `internal/agentd/** cmd/** internal/proto/** internal/store/** internal/codegraph/**`；执行运行 `internal/executor/** internal/prochost/**`；本机治理 `internal/config/** internal/selfupdate/** internal/release/** internal/upgrade/**`；跨机连接 `internal/client/** internal/relay/** internal/targetclient/**`；终端会话 `internal/ptyhost/**`；项目工作区 `internal/localsync/** internal/projectid/**`。assignments 两条跨域文件：`internal/agentd/mirror.go → 项目工作区域`（该文件 15/16 节点在 workspace）。contracts 先留空数组。域类型：执行运行/跨机连接/终端会话 = boundary，其余 = logic。**以第 3 步的实际输出为准修正**——上述清单来自 2026-08-21 对 baseline 的实测分析，跑出来对不上就信输出、改 target，不要信本段。
- [ ] **Step 3: 第一轮跑门测试**——期望大量 `new-direction` fail 与 `outside-file` warn。按输出：每个报出的跨域方向补一条 contract（`entries: []`，`legacyBudget` = 该方向报出的边数，即全部挂账为存量）；`outside-file` 报出的文件补进某域的 paths 或 assignments（确实不属于六域的，留 warn 不管——warn 不挡闸）。
- [ ] **Step 4: 第二轮跑门测试**——绿（fails 空、legacy 全部预算内）。`go test ./cmd/ -run RepoContractGate -count=1 -v` 留存输出进 ledger。
- [ ] **Step 5: 跑全仓测试确认无连带**（`go test ./... -count=1`；web 不受影响不用跑）
- [ ] **Step 6: commit**：`feat(codegraph): handoff 自身 target v1——六域契约面全额挂账为 legacy 预算`

**验收（缺陷族结论）**：假红族——门测试对 baseline 数据敏感是**设计意图**（它就是闸门），注释已写明转红的语义与处置；此判据在当前 baseline（meta.commit 8425099）上实测通过后才算过；序列化边界——target 是手写 JSON，经 LoadTarget+ValidateTarget 双闸。**上下文预算说明**：本 task 只写数据与一个测试，不改任何机制代码；圈定文件集 = 2 个新文件。

---

### Task 9: 收尾钩子（**归审核者本地执行，不派发**）

**Files:**
- Modify: `~/.claude/CLAUDE.md`（用户全局规则 §3「规则」清单）

**为什么不派发**：改的是审核者本机的用户级配置文件，不在仓库内，executor 机器上没有这份文件。

- [ ] **Step 1: 在 CLAUDE.md §3 规则清单的 finishing 条目**（现文：「`finishing-a-development-branch` 收尾时，提示本分支原型改动是否回流入 `prototypes/base/`…」）**扩为同时提示图回流**：加一句「若项目有 `codegraph/` 且本分支产出过视图 diff，一并提示是否 `handoff graph absorb <视图>` 回灌 baseline（先合并分支、后 absorb，meta 锚点才指向主线提交）」。
- [ ] **Step 2: commit 到用户配置的管理方式**（该文件不在 git 仓库内则改完即生效，无 commit 步骤）。

---

## 派发前自审（写给协调者，不进派发正文）

- **验收步骤归谁**：Task 1-8 全部可派发——测试均为 `go test` 进程内执行，无一驱动 agentd/派发/任务机器；模板文本改动是纯代码编辑。Task 9 归审核者本地。Task 7 改纪律块正文——纪律块由 agentd 派发时注入（B129），本 plan 的执行任务本身拿到的是**旧版**纪律块（含一刀切禁令），但 plan 内没有任何步骤需要执行者跑 `handoff graph`（Task 8 走 go test 进程内），不构成 B105 式冲突。
- **判据基线**：Task 8 的六域划分与「310 跨域边」等数字来自 2026-08-21 实测；plan 明确写了「信跑出的输出、不信清单」，判据自带重验步骤。
- **序列化边界清单**：target.json 解析（T1）、implements 穿 wire（T3）、absorb 写盘重载（T6）、check JSON 输出字段（T5）——每处都有穿真实边界的测试。
- **域类型**：全部触及面为逻辑域；absorb 的 git 取号已设计为可注入，无真机清单。
