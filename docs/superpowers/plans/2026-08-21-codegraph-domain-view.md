# 代码图领域视图改造 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把代码图从「全局树+图」改造为「领域图三级下钻」——数据契约新增 domains 段，CLI 新增领域查询，控制台主视图变为领域全景 → 嵌套子领域 → 叶子领域树+图。

**Architecture:** 领域是**扫描产出的入库数据**（`domains` 段 + `container.domain` 归属），消费方一律读数据、**绝不按包名推导**（伪造的层级会被当成真实架构读）。Go 侧只做领域树与统计（CLI 消费），领域全景的聚合（领域间连线、对外接口、域外占位卡）在 TS 侧；已有的 CallTree / FocusGraph / DetailPanel 全部保留，降为**叶子领域的内部视图**，只增加 scope 裁剪与跨领域横跳。旧扫描数据（无 domains）不伪造结构，降级为单领域视图并明示提示。

**Tech Stack:** Go（`internal/codegraph` 只用标准库）、cobra CLI、React + TypeScript、vitest + @testing-library/react、Tailwind。

## 本 plan 的基线与边界

- **基线分支**：`codegraph-phase1`（tip `4c7531971`，未合并 main）。一期的数据层/CLI/agentd 端点/树+图组件都已在这条分支上，本 plan 是**形态修订增量**，继续提交到同一条分支。
- 开工前先确认基线是绿的（这两条命令已在 `4c7531971` 上实跑通过）：
  - `go test ./internal/codegraph/... ./cmd/...` → ok
  - `cd web && npx vitest run src/app/codegraph` → 4 files / 12 tests passed
- **不在本 plan 范围内**（不要做，也不要为它写 task）：
  - 重扫 handoff 自身的 `codegraph/baseline.json` 以产出真实 domains 数据——那是另一次扫描派发，由审核者本地发起。
  - 控制台真机验收（浏览器里实际点三级下钻）——由审核者本地执行，与既有 ledger 的边界一致。
- 设计依据：`docs/superpowers/specs/2026-08-19-codegraph-design.md`。**注意这份 spec 的 2026-08-21 修订（§3.1 domains 段、§5 领域图交互形态）只在协调者本地，执行机上的分支里是修订前的旧版**——本 plan 已把所需内容全部内联，**以本 plan 为准**，spec 只作背景。
- 形态基准是本地的原型 `prototypes/codegraph/`，该目录在 `.gitignore` 中，**执行机上不存在，不要去找**。形态细节同样已内联进各 task。

## Global Constraints

- `internal/codegraph` **零内部依赖、不打日志、不做网络**——数据契约包的硬约束（spec §2）。该包内不得 import slog / handoff 其他包；可观测性由**返回的错误与 `Validate` 的问题串**承担，每条必须带够定位的 id 与原因。
- `cmd/graph.go` 同样不打日志：只经 `RunE` 返回 error、经 `graphPrintJSON` 输出。
- `internal/agentd` 日志三段式：入口 Info + 每条失败分支 Warn/Error 且**错误原因固定用 `"cause"` 这个 key** + 成功一条 Info 带统计量；一律用 `s.log`，禁止 `fmt.Printf`。
- `web/src/api/types.ts` 是 Go 结构体在前端的镜像：**字段增删必须两侧同步**，漏改一侧契约就分叉。
- 前端无结构化日志设施：可观测性以**界面上的诚实信号**承担（失鲜标记、未扫描计数、无领域提示条），不要引入 console.log 当日志。
- 领域数据缺失时**不得伪造结构**：降级为单领域视图 + 明示提示条，禁止按包名/容器名推导领域。
- 不新增任何第三方依赖。
- 每个 task 提交前 `gofmt -l .` 与 `git diff --check` 必须无输出（本分支 ledger 记载过 gofmt 漏跑的教训）。
- 中文注释写「为什么」和边界，不复述代码。

---

## File Structure

**Go（数据契约与 CLI）**

| 文件 | 责任 |
|------|------|
| `internal/codegraph/types.go`（改） | 新增 `Domain` 类型；`Container.Domain`、`Graph.Domains` 字段 |
| `internal/codegraph/merge.go`（改） | `View.Domains` 字段 + `Merge` 透传 |
| `internal/codegraph/validate.go`（改） | 领域段自洽与容器归属校验 |
| `internal/codegraph/domains.go`（新） | `DomainStat` 投影与 `DomainTree`：领域树 + 成员统计 + 对外接口 |
| `internal/codegraph/domains_test.go`（新） | `DomainTree` 单测 |
| `internal/codegraph/validate_test.go`（改） | 领域校验单测 |
| `internal/codegraph/testdata/repo/codegraph/baseline.json`（改） | 夹具补 domains（含一层嵌套） |
| `cmd/graph.go`（改） | 新增 `graph domains` 子命令；`validate` 输出领域计数 |
| `cmd/graph_test.go`（改） | `graph domains` 契约测试 |
| `internal/agentd/codegraph.go`（改） | 成功日志补 domains 统计量 |
| `docs/codegraph-scan-recipe.md`（改） | 扫描配方补领域切分契约 |

**Web（领域视图）**

| 文件 | 责任 |
|------|------|
| `web/src/api/types.ts`（改） | `CgDomain`；`CgContainer.domain`、`CgGraph.domains` |
| `web/src/app/codegraph/graphmath.ts`（改） | `CgView.domains`；`neighborhood` 增可选 `expand` 边界回调 |
| `web/src/app/codegraph/domains.ts`（新） | 领域层纯算法：路径/祖先/子领域/全景聚合/叶子裁剪 |
| `web/src/app/codegraph/domains.test.ts`（新） | 上述算法单测 |
| `web/src/app/codegraph/domainlayout.ts`（新） | 领域卡确定性力导向布局 |
| `web/src/app/codegraph/domainlayout.test.ts`（新） | 布局确定性与不重叠单测 |
| `web/src/app/codegraph/DomainPanorama.tsx`（新） | 领域全景：卡片 + 连线 + 拖拽 + 下钻 |
| `web/src/app/codegraph/DomainPanorama.test.tsx`（新） | 全景组件测试 |
| `web/src/app/codegraph/DomainDetail.tsx`（新） | 领域详情 + 领域间连线详情 |
| `web/src/app/codegraph/DomainDetail.test.tsx`（新） | 详情组件测试 |
| `web/src/app/codegraph/CallTree.tsx`（改） | scope 化：根 = 本域入口 + 对外接口；域外行横跳 |
| `web/src/app/codegraph/FocusGraph.tsx`（改） | scope 化：域外节点虚线卡、点击横跳；BFS 不越界扩展 |
| `web/src/app/codegraph/CodegraphPage.tsx`（改） | 三态路由 + 面包屑 + 无领域降级提示 |
| `web/src/app/codegraph/SeqView.tsx`（**删**） | 时序图裁掉 |

---

### Task 1: 数据契约新增 domains 段与领域校验

**Files:**
- Modify: `internal/codegraph/types.go`
- Modify: `internal/codegraph/merge.go`
- Modify: `internal/codegraph/validate.go`
- Modify: `internal/codegraph/testdata/repo/codegraph/baseline.json`
- Test: `internal/codegraph/validate_test.go`

**Interfaces:**
- Produces: `codegraph.Domain{Label, Kind, Summary, Desc, Parent string}`；`Container.Domain string`；`Graph.Domains map[string]Domain`；`View.Domains map[string]Domain`。后续所有 task 依赖这些字段名。

- [ ] **Step 1: 写失败测试**

在 `internal/codegraph/validate_test.go` 末尾追加（若文件未 import `reflect`/`strings` 一并补上）：

```go
func TestValidateDomains(t *testing.T) {
	g := &Graph{
		Domains: map[string]Domain{
			"d_svc":      {Label: "svc", Kind: "服务端", Summary: "服务"},
			"d_svc/api":  {Label: "api", Kind: "接口层", Summary: "路由", Parent: "d_svc"},
			"d_ghosted":  {Label: "孤儿", Kind: "x", Parent: "d_nope"},
		},
		Containers: map[string]Container{
			"k_api":  {Label: "svc.Server", Kind: "服务端", Domain: "d_svc/api"},
			"k_core": {Label: "svc.Manager", Kind: "核心", Domain: "d_svc"},
			"k_lost": {Label: "svc.Store", Kind: "存储", Domain: "d_ghost"},
			"k_none": {Label: "svc.Util", Kind: "工具"},
		},
		Nodes: map[string]Node{},
		Edges: []Edge{},
	}
	want := []string{
		"容器 k_core 挂在非叶子领域 d_svc（容器只能挂叶子领域）",
		"容器 k_lost 引用不存在的领域 d_ghost",
		"容器 k_none 未归属领域（domains 非空时每个容器都必须有 domain）",
		"领域 d_ghosted 的 parent d_nope 不存在",
	}
	if got := Validate(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("领域校验:\n got=%q\nwant=%q", got, want)
	}
}

func TestValidateDomainParentCycle(t *testing.T) {
	g := &Graph{
		Domains: map[string]Domain{
			"d_a": {Label: "a", Kind: "x", Parent: "d_b"},
			"d_b": {Label: "b", Kind: "x", Parent: "d_a"},
		},
		Containers: map[string]Container{},
		Nodes:      map[string]Node{},
	}
	got := Validate(g)
	if len(got) != 2 || !strings.Contains(got[0], "父链存在环") {
		t.Fatalf("父链成环应逐个领域报出: %q", got)
	}
}

func TestValidateNoDomainsSectionIsClean(t *testing.T) {
	// 旧扫描数据没有 domains 段：整段校验跳过，不得因此报问题
	g := &Graph{
		Containers: map[string]Container{"k_svc": {Label: "svc", Kind: "服务端"}},
		Nodes:      map[string]Node{},
	}
	if got := Validate(g); len(got) != 0 {
		t.Fatalf("无领域段应零问题: %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/codegraph/ -run TestValidateDomain -v`
Expected: 编译失败，`unknown field Domains in struct literal of type Graph` / `unknown field Domain in struct literal of type Container`

- [ ] **Step 3: 加类型字段**

在 `internal/codegraph/types.go` 的 `Container` 结构体加字段：

```go
// Container 是分组盒子（struct 一级，见 spec §3.1）。
type Container struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Entry bool   `json:"entry,omitempty"`
	// Domain 是所属领域 id，必须是**叶子**领域。空串只在整图没有 domains 段时
	// 合法（旧扫描数据，消费方降级为单领域视图）。
	Domain string `json:"domain,omitempty"`
}
```

在 `Container` 之后新增 `Domain` 类型：

```go
// Domain 是一个领域：领域图的一级组织单位，可嵌套。
//
// 领域由扫描产出、人可在入库后修改（spec §3.1）。Parent 串成树，为空即顶层。
// 容器只能挂叶子领域——挂在中间层的容器既不属于本级全景、也进不了任何子领域，
// 会静默从图里消失，所以 Validate 把它当错误报出来而不是默默丢掉。
type Domain struct {
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Summary string `json:"summary,omitempty"`
	Desc    string `json:"desc,omitempty"`
	Parent  string `json:"parent,omitempty"`
}
```

在 `Graph` 结构体加字段（放在 `Meta` 之后）：

```go
	// Domains 是领域段，可为空——空即「该图未划分领域」，消费方降级为单领域视图。
	// **不得按包名伪造领域**：伪造出来的层级会被人和 agent 当成真实架构读。
	Domains map[string]Domain `json:"domains,omitempty"`
```

在 `internal/codegraph/merge.go` 的 `View` 结构体加字段并在 `Merge` 里透传：

```go
	// Domains 原样来自基线：diff 只改节点与边，不改领域划分
	Domains map[string]Domain `json:"domains,omitempty"`
```

`Merge` 构造 `View` 的两处（纯基准分支与 diff 分支）都要带上 `Domains: g.Domains`。

- [ ] **Step 4: 实现领域校验**

在 `internal/codegraph/validate.go` 里新增：

```go
// validateDomains 检查领域段自洽与容器归属。
// domains 为空时整段跳过——那是旧扫描数据的合法降级路径，不是错误。
func validateDomains(g *Graph) []string {
	if len(g.Domains) == 0 {
		return nil
	}
	var out []string
	hasChild := map[string]bool{}
	for id, d := range g.Domains {
		if d.Parent == "" {
			continue
		}
		if _, ok := g.Domains[d.Parent]; !ok {
			out = append(out, fmt.Sprintf("领域 %s 的 parent %s 不存在", id, d.Parent))
			continue
		}
		hasChild[d.Parent] = true
	}
	// 父链探环：沿 Parent 上溯，重复遇到同一个 id 即成环。
	// 成环会让消费方的路径推导死循环，必须在数据层拦下。
	for id := range g.Domains {
		seen := map[string]bool{id: true}
		for cur := g.Domains[id].Parent; cur != ""; {
			if seen[cur] {
				out = append(out, fmt.Sprintf("领域 %s 的父链存在环", id))
				break
			}
			seen[cur] = true
			d, ok := g.Domains[cur]
			if !ok {
				break // parent 不存在已在上面报过，这里不重复报
			}
			cur = d.Parent
		}
	}
	// 容器归属：必须有 domain、领域必须存在、且必须是叶子。
	// 存在性一律用 ok 判定——拿零值比较会把「存在但字段全空的领域」误判成不存在。
	for cid, c := range g.Containers {
		if c.Domain == "" {
			out = append(out, fmt.Sprintf("容器 %s 未归属领域（domains 非空时每个容器都必须有 domain）", cid))
			continue
		}
		if _, ok := g.Domains[c.Domain]; !ok {
			out = append(out, fmt.Sprintf("容器 %s 引用不存在的领域 %s", cid, c.Domain))
			continue
		}
		if hasChild[c.Domain] {
			out = append(out, fmt.Sprintf("容器 %s 挂在非叶子领域 %s（容器只能挂叶子领域）", cid, c.Domain))
		}
	}
	return out
}
```

在 `Validate` 里、`sort.Strings` **之前**接上：

```go
	out = append(out, validateDomains(g)...)
```

（变量名以 `validate.go` 现有实现为准——它把问题收集在一个切片里最后排序，把这行插在排序前即可。）

- [ ] **Step 5: 夹具补 domains**

把 `internal/codegraph/testdata/repo/codegraph/baseline.json` 改成（只加 `domains` 段与三个容器的 `domain` 字段，其余原样）：

```json
{
  "meta": {"project": "demo", "branch": "main", "commit": "abc1234", "scannedAt": "2026-08-19", "generator": "test"},
  "domains": {
    "d_cli": {"label": "cli", "kind": "命令层", "summary": "命令入口"},
    "d_svc": {"label": "svc", "kind": "服务端", "summary": "服务与实体", "desc": "干活与落库"},
    "d_svc/api": {"label": "api", "kind": "服务端", "summary": "对外方法", "parent": "d_svc"},
    "d_svc/store": {"label": "store", "kind": "存储", "summary": "实体存放", "parent": "d_svc"}
  },
  "containers": {
    "c_cli": {"label": "CLI 命令", "kind": "入口", "entry": true, "domain": "d_cli"},
    "k_svc": {"label": "svc.Server", "kind": "服务端", "domain": "d_svc/api"},
    "k_ent": {"label": "store 实体", "kind": "实体", "domain": "d_svc/store"}
  },
  "nodes": {
    "e_run":  {"kind": "entry", "container": "c_cli", "name": "demo run", "file": "cmd/run.go", "line": 3, "summary": "跑"},
    "e_skip": {"kind": "entry", "container": "c_cli", "name": "demo skip", "file": "cmd/skip.go", "line": 1, "unscanned": true},
    "n_runE": {"kind": "func", "container": "k_svc", "name": "runE", "file": "cmd/run.go", "line": 5,
               "signature": "func runE() error", "returns": "error", "summary": "命令主函数"},
    "n_do":   {"kind": "func", "container": "k_svc", "name": "Server.Do", "file": "svc/server.go", "line": 4,
               "signature": "func (s *Server) Do() error", "returns": "error", "summary": "干活",
               "tests": [{"name": "TestDo", "file": "svc/server_test.go:10", "snippet": "assert"}]},
    "n_save": {"kind": "func", "container": "k_svc", "name": "Server.Save", "file": "svc/server.go", "line": 9,
               "signature": "func (s *Server) Save() error", "returns": "error", "summary": "落库"},
    "m_task": {"kind": "model", "container": "k_ent", "name": "Task", "file": "svc/task.go", "line": 2,
               "fields": [["ID", "string", "号"]], "summary": "实体"}
  },
  "edges": [["e_run", "n_runE"], ["n_runE", "n_do"], ["n_do", "n_save"], ["n_save", "m_task"]],
  "diffs": {}
}
```

`testdata/repo/codegraph/diffs/branch-x.json` **不需要改**：diff 只引用容器 id，容器本身已带 domain。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/codegraph/ -v`
Expected: PASS，含新增的 3 个领域测试，且既有测试全绿（夹具加的是合法领域，`Validate` 仍应零问题）

- [ ] **Step 7: 注释与可观测性自检**

- 本包**不打日志**（零依赖硬约束）——**不要**引入 slog；本 task 的可观测性完全由 `Validate` 的问题串承担：逐条确认每条消息都带得上容器/领域 id 与具体原因，能让人只看这一行就知道改哪。
- `Domain` 类型、`Container.Domain`、`Graph.Domains`、`View.Domains` 四处都要有「为什么」注释（尤其「只能挂叶子」和「不得伪造领域」两条边界）。
- `validateDomains` 的函数注释写清「domains 为空是合法降级、不是错误」。

- [ ] **Step 8: 格式与提交**

```bash
gofmt -l internal/codegraph/ && git diff --check
git add internal/codegraph/
git commit -m "feat(codegraph): 数据契约新增 domains 段与领域校验"
```

---

### Task 2: 领域树投影 DomainTree

**Files:**
- Create: `internal/codegraph/domains.go`
- Test: `internal/codegraph/domains_test.go`

**Interfaces:**
- Consumes: Task 1 的 `View.Domains`、`Container.Domain`。
- Produces: `codegraph.DomainStat` 与 `func DomainTree(v *View) []DomainStat`；私有 `func domainOfContainer(v *View, cid string) string`。Task 3 的 CLI 直接序列化 `DomainTree` 的返回值。

- [ ] **Step 1: 写失败测试**

创建 `internal/codegraph/domains_test.go`：

```go
package codegraph

import (
	"reflect"
	"testing"
)

// 夹具的领域结构：d_cli（c_cli）、d_svc（父）→ d_svc/api（k_svc）、d_svc/store（k_ent）
func TestDomainTreeStatsAndInterfaces(t *testing.T) {
	g, err := LoadGraph("testdata/repo")
	if err != nil {
		t.Fatal(err)
	}
	got := DomainTree(Merge(g, nil))
	byID := map[string]DomainStat{}
	for _, d := range got {
		byID[d.ID] = d
	}
	if len(got) != 4 || got[0].ID != "d_cli" {
		t.Fatalf("应按 id 升序返回 4 个领域: %+v", got)
	}
	cli := byID["d_cli"]
	if cli.Entries != 2 || cli.Unscanned != 1 || cli.Funcs != 0 {
		t.Fatalf("d_cli 统计: %+v", cli)
	}
	svc := byID["d_svc"]
	if !reflect.DeepEqual(svc.Children, []string{"d_svc/api", "d_svc/store"}) {
		t.Fatalf("父领域的 children: %+v", svc.Children)
	}
	if len(svc.Containers) != 0 || svc.Funcs != 0 {
		t.Fatalf("父领域不直接持有成员，统计不重复计入: %+v", svc)
	}
	api := byID["d_svc/api"]
	if api.Funcs != 3 || !reflect.DeepEqual(api.Interfaces, []string{"n_runE"}) {
		t.Fatalf("d_svc/api: funcs=%d ifaces=%v", api.Funcs, api.Interfaces)
	}
	store := byID["d_svc/store"]
	if store.Models != 1 || !reflect.DeepEqual(store.Interfaces, []string{"m_task"}) {
		t.Fatalf("d_svc/store: models=%d ifaces=%v", store.Models, store.Interfaces)
	}
}

func TestDomainTreeNilWhenNoDomains(t *testing.T) {
	v := Merge(&Graph{Containers: map[string]Container{}, Nodes: map[string]Node{}}, nil)
	if got := DomainTree(v); got != nil {
		t.Fatalf("无领域段必须返回 nil 让调用方降级，不能编造: %+v", got)
	}
}

func TestDomainTreeSkipsDeleted(t *testing.T) {
	g, err := LoadGraph("testdata/repo")
	if err != nil {
		t.Fatal(err)
	}
	d, err := LoadDiff("testdata/repo", "branch-x")
	if err != nil {
		t.Fatal(err)
	}
	// branch-x 删了 n_save，n_save→m_task 这条跨领域边随之失效
	byID := map[string]DomainStat{}
	for _, s := range DomainTree(Merge(g, d)) {
		byID[s.ID] = s
	}
	if len(byID["d_svc/store"].Interfaces) != 0 {
		t.Fatalf("deleted 端点的边不该算作对外接口: %+v", byID["d_svc/store"])
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/codegraph/ -run TestDomainTree -v`
Expected: FAIL，`undefined: DomainTree`

- [ ] **Step 3: 实现**

创建 `internal/codegraph/domains.go`：

```go
// domains.go —— 领域层的投影：把视图按领域聚合成带统计的领域树。
//
// 职责：领域树结构（parent/children）、每个领域的成员统计与对外接口清单
// 边界：只读视图，不改数据、不打日志、不做网络；领域一律读自数据的 domains 段,
// **不按包名或容器名推导**——推导出来的层级会被人和 agent 当成真实架构。
// 与前端 web/src/app/codegraph/domains.ts 的「跨领域边 / 对外接口」判定规则
// 必须一致，两侧分叉就是 bug。
package codegraph

import "sort"

// DomainStat 是一个领域的展示投影：元信息 + 结构位置 + 成员统计。
//
// 统计只算**直属容器**里的节点：父领域的数字不含子领域，读数不会重复计入。
// Interfaces 是本领域中被其他领域调用到的节点 id（即「对外开放接口」）。
type DomainStat struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Kind       string   `json:"kind"`
	Summary    string   `json:"summary,omitempty"`
	Desc       string   `json:"desc,omitempty"`
	Parent     string   `json:"parent,omitempty"`
	Children   []string `json:"children"`
	Containers []string `json:"containers"`
	Funcs      int      `json:"funcs"`
	Models     int      `json:"models"`
	Entries    int      `json:"entries"`
	Unscanned  int      `json:"unscannedEntries"`
	Interfaces []string `json:"interfaces"`
}

// DomainTree 把视图投影成领域列表，按 id 升序；列表内各切片字段也按字典序排序，
// 保证同一份数据每次输出一致（CLI 输出要能直接 diff）。
//
// 视图没有领域段时返回 nil——调用方据此降级为单领域视图，不要自行编造领域。
func DomainTree(v *View) []DomainStat {
	if len(v.Domains) == 0 {
		return nil
	}
	stats := make(map[string]*DomainStat, len(v.Domains))
	for id, d := range v.Domains {
		stats[id] = &DomainStat{
			ID: id, Label: d.Label, Kind: d.Kind, Summary: d.Summary, Desc: d.Desc,
			Parent: d.Parent, Children: []string{}, Containers: []string{}, Interfaces: []string{},
		}
	}
	for id, d := range v.Domains {
		if p, ok := stats[d.Parent]; ok {
			p.Children = append(p.Children, id)
		}
	}
	for cid, c := range v.Containers {
		if s, ok := stats[c.Domain]; ok {
			s.Containers = append(s.Containers, cid)
		}
	}
	for _, n := range v.Nodes {
		if n.Status == "deleted" {
			continue
		}
		s, ok := stats[domainOfContainer(v, n.Container)]
		if !ok {
			continue
		}
		switch n.Kind {
		case "entry":
			s.Entries++
			if n.Unscanned {
				s.Unscanned++
			}
		case "model":
			s.Models++
		default:
			s.Funcs++
		}
	}
	// 对外接口 = 被别的领域调用到的节点。同一节点被多个领域调用只记一次。
	seen := map[string]map[string]bool{}
	for _, e := range v.Edges {
		if e.Status == "deleted" {
			continue
		}
		from, okF := v.Nodes[e.From]
		to, okT := v.Nodes[e.To]
		if !okF || !okT || from.Status == "deleted" || to.Status == "deleted" {
			continue
		}
		da := domainOfContainer(v, from.Container)
		db := domainOfContainer(v, to.Container)
		if da == "" || db == "" || da == db {
			continue
		}
		if seen[db] == nil {
			seen[db] = map[string]bool{}
		}
		if seen[db][e.To] {
			continue
		}
		seen[db][e.To] = true
		if s, ok := stats[db]; ok {
			s.Interfaces = append(s.Interfaces, e.To)
		}
	}
	out := make([]DomainStat, 0, len(stats))
	for _, s := range stats {
		sort.Strings(s.Children)
		sort.Strings(s.Containers)
		sort.Strings(s.Interfaces)
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// domainOfContainer 返回容器所属领域 id；容器不存在或未归属时返回空串，
// 调用方据此跳过——未归属的容器由 Validate 报问题，这里不做二次兜底。
func domainOfContainer(v *View, cid string) string {
	c, ok := v.Containers[cid]
	if !ok {
		return ""
	}
	return c.Domain
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/codegraph/ -v`
Expected: PASS（含 3 个 DomainTree 测试）

- [ ] **Step 5: 注释与可观测性自检**

- 本包不打日志：确认 `domains.go` 没有引入任何 logger。
- 文件头注释含职责 + 边界（不推导领域、与前端规则必须一致）。
- `DomainStat`、`DomainTree`、`domainOfContainer` 三处导出/关键注释齐全，写清「父领域不重复计入」「无领域段返回 nil」两条语义。

- [ ] **Step 6: 格式与提交**

```bash
gofmt -l internal/codegraph/ && git diff --check
git add internal/codegraph/domains.go internal/codegraph/domains_test.go
git commit -m "feat(codegraph): 领域树投影 DomainTree（统计与对外接口）"
```

---

### Task 3: CLI `graph domains` 与领域计数

**Files:**
- Modify: `cmd/graph.go`
- Modify: `internal/agentd/codegraph.go`
- Test: `cmd/graph_test.go`

**Interfaces:**
- Consumes: Task 2 的 `codegraph.DomainTree`、`DomainStat`。
- Produces: `handoff graph domains` 子命令，输出 `{"view": …, "domains": [...], "warning"?: …}`；`graph validate` 输出新增 `"domains"` 计数。

- [ ] **Step 1: 写失败测试**

在 `cmd/graph_test.go` 末尾追加：

```go
func TestGraphDomains(t *testing.T) {
	out, err := runGraph(t, "domains", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("domains 应通过: %v\n%s", err, out)
	}
	var r struct {
		View    string `json:"view"`
		Domains []struct {
			ID         string   `json:"id"`
			Children   []string `json:"children"`
			Funcs      int      `json:"funcs"`
			Interfaces []string `json:"interfaces"`
		} `json:"domains"`
		Warning string `json:"warning"`
	}
	if json.Unmarshal([]byte(out), &r) != nil {
		t.Fatalf("非法 JSON: %s", out)
	}
	if len(r.Domains) != 4 || r.Domains[0].ID != "d_cli" || r.Warning != "" {
		t.Fatalf("领域树形状: %s", out)
	}
	if r.Domains[1].ID != "d_svc" || len(r.Domains[1].Children) != 2 {
		t.Fatalf("嵌套子领域没出来: %s", out)
	}
}

func TestGraphValidateReportsDomainCount(t *testing.T) {
	out, err := runGraph(t, "validate", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("validate 应通过: %v\n%s", err, out)
	}
	var r map[string]any
	if json.Unmarshal([]byte(out), &r) != nil || r["domains"].(float64) != 4 {
		t.Fatalf("validate 要报领域计数: %s", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestGraphDomains|TestGraphValidateReportsDomainCount' -v`
Expected: FAIL——`domains` 报 `unknown command`；validate 断言因 `r["domains"]` 为 nil 触发 panic/失败

- [ ] **Step 3: 加子命令与计数**

在 `cmd/graph.go` 里，`graphWhoCallsCmd` 定义之后新增：

```go
// graphDomainsCmd 列领域树：agent 定位「该从哪个领域下手」的第一跳。
var graphDomainsCmd = &cobra.Command{
	Use:   "domains",
	Short: "列出领域树（职责、成员统计、对外接口）",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		doms := codegraph.DomainTree(v)
		out := map[string]any{"view": v.Name, "domains": doms}
		if doms == nil {
			// 明确区分「没有领域」与「查不出领域」：前者是旧数据，给可行动的提示
			out["domains"] = []codegraph.DomainStat{}
			out["warning"] = "该图未包含领域划分（扫描版本较旧）：重扫可获得领域信息"
		}
		return graphPrintJSON(cmd, out)
	},
}
```

在 `init()` 的 `AddCommand` 里加上它：

```go
	graphCmd.AddCommand(graphValidateCmd, graphViewsCmd, graphChainCmd, graphWhoCallsCmd, graphDomainsCmd)
```

在 `graphValidateCmd` 的 `out := map[string]any{...}` 里补一项（与 `"containers"` 并列）：

```go
		"domains": len(g.Domains),
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestGraph -v`
Expected: PASS（含既有 5 个 + 新增 2 个）

- [ ] **Step 5: 加日志（agentd 侧）**

`internal/agentd/codegraph.go` 的成功日志补一个统计量——领域数是「这张图有没有领域划分」的第一手证据，排查前端为何降级成单领域视图时第一眼看它：

```go
	s.log.Info("代码图完成", "name", name, "nodes", len(g.Nodes),
		"edges", len(g.Edges), "domains", len(g.Domains), "views", len(views), "stale", len(stale))
```

（`internal/codegraph` 与 `cmd/graph.go` 依旧不打日志——见 Global Constraints。）

- [ ] **Step 6: 加注释**

- `graphDomainsCmd` 的注释写清它的用途（agent 的第一跳）与 warning 分支的语义。
- validate 新增计数处无需注释（字段名自明）。

- [ ] **Step 7: 真实冒烟**

Run: `go run . graph domains --repo internal/codegraph/testdata/repo`
Expected: 退出 0，输出 4 个领域，`d_svc` 带两个 children，`d_svc/api` 的 interfaces 为 `["n_runE"]`

- [ ] **Step 8: 格式与提交**

```bash
gofmt -l cmd/ internal/agentd/ && git diff --check
git add cmd/graph.go cmd/graph_test.go internal/agentd/codegraph.go
git commit -m "feat(cli): handoff graph domains 与领域计数"
```

---

### Task 4: 扫描配方补领域切分契约

**Files:**
- Modify: `docs/codegraph-scan-recipe.md`

**Interfaces:**
- Consumes: Task 1 的字段定义（本文档的字段表必须与 `types.go` 逐字对齐）。

- [ ] **Step 1: 在 baseline.json 顶层字段表加一行**

在「顶层对象字段」表的 `meta` 之后插入：

```markdown
| domains | Record&lt;string, Domain&gt; | 领域段，key 是领域 ID；可嵌套 |
```

- [ ] **Step 2: 新增 domains 字段表**

在 meta 字段表之后、containers 字段表之前插入：

```markdown
domains 的 value 字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| label | string | 否 | 领域展示名（如 agentd、store） |
| kind | string | 否 | 一两个词的角色说明（如 执行机守护进程、存储） |
| summary | string | 是 | 职责一句话，领域卡正面就显示它 |
| desc | string | 是 | 内部逻辑介绍，点开领域详情时读 |
| parent | string | 是 | 父领域 ID；缺省即顶层领域 |
```

并在 containers 字段表末尾追加一行：

```markdown
| domain | string | 是 | 所属领域 ID，必须是叶子领域；整图无 domains 段时可省 |
```

- [ ] **Step 3: 加「怎么切领域」一节**

在 Schema 一节之后、硬纪律之前插入：

```markdown
## 怎么切领域

领域是这张图的**主视图**：人先看领域全景（领域之间怎么调），再下钻到领域内部。
切得对不对直接决定这张图有没有用，所以它是扫描的一等产物，不是附属信息。

- **默认按包切一层**：一个 Go 包一个顶层领域，label 用包名。
- **大包按职责再切子领域**：一个包超过约 20 个方法、或内部明显分层（对外接口层 /
  业务核心 / 适配层）时，用 parent 切出子领域。层数不限，但**别为了切而切**——
  没有职责差异的拆分只会多一次点击。
- **容器只能挂叶子领域**：挂在中间层的容器会静默从图里消失，`handoff graph validate`
  会把它报成错误。
- **入口容器（CLI/HTTP/WS）挂到它服务的领域**上，不要单独成领域——入口是领域的对外
  门面，不是独立的一层。
- **summary 必填**：一句话说清这个领域负责什么。领域卡正面只显示这一句，写不出一句
  话通常说明领域切错了。
- **desc 写内部逻辑**：这个领域内部怎么组织、有哪些关键类型、对外靠什么方式协作。
- **领域之间的连线不用手写**：消费方按跨领域的调用边自动聚合，只要 container.domain
  归属正确，连线与「对外开放接口」清单就是对的。
```

- [ ] **Step 4: 硬纪律补两条**

在「硬纪律」列表末尾追加：

```markdown
- 每个容器必须带 domain 且必须挂叶子领域；domains 段与容器归属要么全有、要么全无
  （半套数据会让消费方一半降级一半不降级）。
- 领域的 parent 必须指向已定义领域，且父链不能成环。
```

- [ ] **Step 5: 验收命令一节同步**

把收尾自检那条里的验收命令补上领域检查：

```markdown
- 收尾自检：python3 -m json.tool 验证 JSON 合法性 + handoff graph validate --repo .
  （零 issues），再 handoff graph domains --repo . 目视领域树是否符合真实架构，
  并抽查 5 个节点的 file:line。
```

- [ ] **Step 6: 检查与提交**

Run: `git diff --check`
Expected: 无输出

```bash
git add docs/codegraph-scan-recipe.md
git commit -m "docs(codegraph): 扫描配方补领域切分契约"
```

---

### Task 5: 前端类型与领域算法层

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/app/codegraph/graphmath.ts`
- Create: `web/src/app/codegraph/domains.ts`
- Test: `web/src/app/codegraph/domains.test.ts`

**Interfaces:**
- Consumes: Task 1 的 JSON 字段名（`domains` / `container.domain` / `parent`）。
- Produces（后续所有前端 task 依赖这些精确签名）：
  - `CgDomain { label: string; kind: string; summary?: string; desc?: string; parent?: string }`
  - `CgContainer` 增 `domain?: string`；`CgGraph` 增 `domains?: Record<string, CgDomain>`
  - `CgView` 增 `domains: Record<string, CgDomain>`（**恒为对象**，mergeView 用 `?? {}` 兜底）
  - `neighborhood(v, foci, depth, expand?: (id: string) => boolean)`
  - `hasDomains(v)`、`domainPathOf(v, containerId)`、`nodeDomainPathOf(v, nodeId)`、`domainAncestors(v, scope)`、`childDomainsOf(v, scope)`、`inScope(v, nodeId, scope)`、`leafRoots(v, scope)`、`domainAgg(v, scope)`
  - `DomainCard { id; ext; containers; entries; nodes }`、`DomainEdge { from; to; pairs }`、`DomainAgg { cards; edges; ifaces }`

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/codegraph/domains.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import type { CgGraph } from '../../api/types'
import { mergeView } from './graphmath'
import {
  childDomainsOf, domainAgg, domainAncestors, domainPathOf, hasDomains, inScope, leafRoots,
} from './domains'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-21', generator: 'test' },
  domains: {
    d_cli: { label: 'cli', kind: '命令层', summary: '命令入口' },
    d_svc: { label: 'svc', kind: '服务端', summary: '服务与实体', desc: '干活与落库' },
    'd_svc/api': { label: 'api', kind: '服务端', summary: '对外方法', parent: 'd_svc' },
    'd_svc/store': { label: 'store', kind: '存储', summary: '实体存放', parent: 'd_svc' },
  },
  containers: {
    c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
    k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc/api' },
    k_ent: { label: 'store 实体', kind: '实体', domain: 'd_svc/store' },
  },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    n_do: { kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4 },
    n_save: { kind: 'func', container: 'k_svc', name: 'Server.Save', file: 'svc/server.go', line: 9 },
    m_task: { kind: 'model', container: 'k_ent', name: 'Task', file: 'svc/task.go', line: 2 },
  },
  edges: [['e_run', 'n_runE'], ['n_runE', 'n_do'], ['n_do', 'n_save'], ['n_save', 'm_task']],
}
const v = mergeView(g)

describe('领域路径与层级', () => {
  it('domainPathOf 返回顶层到叶子', () => {
    expect(domainPathOf(v, 'k_svc')).toEqual(['d_svc', 'd_svc/api'])
    expect(domainPathOf(v, 'c_cli')).toEqual(['d_cli'])
  })
  it('domainAncestors 走 parent 链而不是拆字符串', () => {
    expect(domainAncestors(v, 'd_svc/store')).toEqual(['d_svc', 'd_svc/store'])
  })
  it('childDomainsOf：null 给顶层，领域给直接子领域', () => {
    expect(childDomainsOf(v, null)).toEqual(['d_cli', 'd_svc'])
    expect(childDomainsOf(v, 'd_svc')).toEqual(['d_svc/api', 'd_svc/store'])
    expect(childDomainsOf(v, 'd_svc/api')).toEqual([])
  })
  it('无领域段时 hasDomains 为假', () => {
    const bare = mergeView({ ...g, domains: undefined })
    expect(hasDomains(bare)).toBe(false)
    expect(hasDomains(v)).toBe(true)
  })
})

describe('domainAgg', () => {
  it('顶层：两张卡一条连线，接口带调用方领域', () => {
    const agg = domainAgg(v, null)
    expect(Object.keys(agg.cards).sort()).toEqual(['d_cli', 'd_svc'])
    expect([...agg.edges.keys()]).toEqual(['d_cli|d_svc'])
    expect(agg.edges.get('d_cli|d_svc')!.pairs).toHaveLength(1)
    expect([...agg.ifaces.d_svc.get('n_runE')!]).toEqual(['d_cli'])
  })
  it('下钻一层：子领域实卡 + 域外虚线占位卡，跨界边保留', () => {
    const agg = domainAgg(v, 'd_svc')
    expect(Object.keys(agg.cards).sort()).toEqual(['d_svc/api', 'd_svc/store', 'ext:d_cli'])
    expect(agg.cards['ext:d_cli'].ext).toBe(true)
    expect([...agg.edges.keys()].sort()).toEqual(['d_svc/api|d_svc/store', 'ext:d_cli|d_svc/api'])
  })
})

describe('leafRoots', () => {
  it('叶子领域的树根 = 本域入口 + 被外部调用的接口', () => {
    expect(leafRoots(v, 'd_cli')).toEqual(['e_run'])
    expect(leafRoots(v, 'd_svc/api')).toEqual(['n_runE'])
    expect(leafRoots(v, 'd_svc/store')).toEqual(['m_task'])
  })
})

describe('inScope', () => {
  it('scope=null 全在范围内；领域按路径包含判定', () => {
    expect(inScope(v, 'm_task', null)).toBe(true)
    expect(inScope(v, 'm_task', 'd_svc')).toBe(true)
    expect(inScope(v, 'm_task', 'd_svc/api')).toBe(false)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/domains.test.ts`
Expected: FAIL，`Failed to resolve import "./domains"`

- [ ] **Step 3: 加类型**

`web/src/api/types.ts` 的代码图段落里，把 `CgContainer` 换成下面这版并新增 `CgDomain`、给 `CgGraph` 加 `domains`：

```ts
// CgDomain 是一个领域（领域图的一级组织单位，可嵌套）。
// 领域由扫描产出、人可在入库后改；parent 为空即顶层。**前端不推导领域**——
// 按包名猜出来的层级会被当成真实架构读（spec §3.1）。
export interface CgDomain {
  label: string
  kind: string
  summary?: string
  desc?: string
  parent?: string
}
export interface CgContainer {
  label: string
  kind: string
  entry?: boolean
  // domain 是所属领域 id，必须是叶子领域；整图无 domains 段时缺席（旧扫描数据）
  domain?: string
}
export interface CgGraph {
  meta: { project: string; branch: string; commit: string; scannedAt: string; generator: string }
  // domains 缺席 = 该图未划分领域，页面降级为单领域视图
  domains?: Record<string, CgDomain>
  containers: Record<string, CgContainer>
  nodes: Record<string, CgNode>
  edges: [string, string][]
}
```

- [ ] **Step 4: graphmath 加 domains 与 BFS 边界**

`web/src/app/codegraph/graphmath.ts`：`CgView` 增字段，`mergeView` 两处返回都带上 `domains`：

```ts
export interface CgView {
  name: string
  // 恒为对象（空对象 = 该图没有领域段）：调用方不必到处判 undefined
  domains: NonNullable<CgGraph['domains']>
  containers: CgGraph['containers']
  nodes: Record<string, ViewNode>
  edges: ViewEdge[]
}
```

`mergeView` 里两处 `return`：
- 纯基准：`return { name: 'baseline', domains: g.domains ?? {}, containers: g.containers, nodes, edges }`
- 带 diff：`return { name: d.view, domains: g.domains ?? {}, containers: g.containers, nodes, edges }`

`neighborhood` 增加可选的扩展边界回调（领域下钻时用它把 BFS 挡在领域边界上——
域外邻居要入图显示成虚线卡，但不再从它继续往外扩）：

```ts
// neighborhood 多源 BFS：焦点 0 层、下游正、上游负；depth=0 不限。
// expand 可选：返回 false 的节点仍会进入结果（要显示），但不再从它继续扩展——
// 领域下钻靠它把邻域裁在领域边界上，否则一跳就跑到别的领域去了。
export function neighborhood(
  v: CgView, foci: string[], depth: number, expand?: (id: string) => boolean,
): Record<string, number> {
  const { adj, radj } = buildAdj(v)
  const dist: Record<string, number> = {}
  for (const f of foci) dist[f] = 0
  const sweep = (next: Record<string, string[]>, step: number) => {
    let frontier = [...foci]
    while (frontier.length) {
      const nx: string[] = []
      for (const id of frontier) {
        const d = dist[id] + step
        if (depth > 0 && Math.abs(d) > depth) continue
        for (const t of next[id] ?? []) {
          if (t in dist) continue
          dist[t] = d
          if (!expand || expand(t)) nx.push(t)
        }
      }
      frontier = nx
    }
  }
  sweep(adj, 1)
  sweep(radj, -1)
  return dist
}
```

- [ ] **Step 5: 实现 domains.ts**

创建 `web/src/app/codegraph/domains.ts`：

```ts
// domains —— 领域层的纯算法：路径、祖先链、子领域、全景聚合、叶子领域裁剪。
//
// 职责：全部确定性纯函数，组件只做渲染与事件
// 边界：不发请求、不碰 DOM；领域一律读自数据的 domains 段，**绝不按包名推导**
// ——推导出来的层级会被人和 agent 当成真实架构读（spec §3.1）。
// 与 Go 侧 internal/codegraph/domains.go 的「跨领域边 / 对外接口」判定规则必须
// 一致（跨领域 = 两端叶子领域不同；接口 = 跨领域边的被调端），两侧分叉就是 bug。
import type { CgView, ViewEdge } from './graphmath'
import { scannedEntries } from './graphmath'

// DomainCard 是全景里的一张卡。ext=true 表示它是**本级之外**的领域占位卡：
// 只为保住跨界连线而画，点击横跳过去，不显示统计。
export interface DomainCard {
  id: string
  ext: boolean
  containers: string[]
  entries: string[]
  nodes: string[]
}

// DomainEdge 是两个领域之间的调用关系；pairs 保留底层方法对，供「谁调用了谁的接口」。
export interface DomainEdge {
  from: string
  to: string
  pairs: ViewEdge[]
}

// DomainAgg 是一次全景聚合的结果。ifaces[领域][被调节点] = 调用方领域集合。
export interface DomainAgg {
  cards: Record<string, DomainCard>
  edges: Map<string, DomainEdge>
  ifaces: Record<string, Map<string, Set<string>>>
}

// hasDomains 判断该图带不带领域段；false 时页面降级为单领域视图并给出提示。
export function hasDomains(v: CgView): boolean {
  return Object.keys(v.domains).length > 0
}

// domainPathOf 返回容器所属领域从顶层到叶子的路径；未归属或领域缺失返回 []。
// 用 seen 防成环——Validate 会拦下环，但坏数据不该让界面死循环。
export function domainPathOf(v: CgView, containerId: string): string[] {
  const path: string[] = []
  const seen = new Set<string>()
  let id = v.containers[containerId]?.domain ?? ''
  while (id && v.domains[id] && !seen.has(id)) {
    seen.add(id)
    path.unshift(id)
    id = v.domains[id].parent ?? ''
  }
  return path
}

// nodeDomainPathOf 同 domainPathOf，按节点取。
export function nodeDomainPathOf(v: CgView, nodeId: string): string[] {
  const n = v.nodes[nodeId]
  return n ? domainPathOf(v, n.container) : []
}

// domainAncestors 返回顶层到 scope 的完整路径（含 scope 自身），面包屑用。
// **按 parent 链走**，不要拆 id 字符串——领域 id 是不透明的，带不带斜杠都合法。
export function domainAncestors(v: CgView, scope: string): string[] {
  const path: string[] = []
  const seen = new Set<string>()
  let id = scope
  while (id && v.domains[id] && !seen.has(id)) {
    seen.add(id)
    path.unshift(id)
    id = v.domains[id].parent ?? ''
  }
  return path
}

// childDomainsOf 返回 scope 的直接子领域（scope=null 即顶层领域），按 id 升序。
// 返回空数组 = scope 是叶子领域，页面据此切换到树+图视图。
export function childDomainsOf(v: CgView, scope: string | null): string[] {
  const want = scope ?? ''
  return Object.entries(v.domains)
    .filter(([, d]) => (d.parent ?? '') === want)
    .map(([id]) => id)
    .sort()
}

// inScope 判断节点是否落在 scope 领域内（含其子领域）；scope=null 恒真。
export function inScope(v: CgView, nodeId: string, scope: string | null): boolean {
  if (!scope) return true
  return nodeDomainPathOf(v, nodeId).includes(scope)
}

// domainAgg 把视图聚合成一层领域全景。
// scope=null 聚到顶层领域；否则聚到 scope 的直接子领域，本级之外的容器归入
// "ext:<顶层领域>" 占位卡。两端都在域外的边不画——那是别人家的事。
export function domainAgg(v: CgView, scope: string | null): DomainAgg {
  const cards: Record<string, DomainCard> = {}
  const byContainer: Record<string, string> = {}
  const put = (cardId: string, cid: string, ext: boolean) => {
    const card = (cards[cardId] ??= { id: cardId, ext, containers: [], entries: [], nodes: [] })
    card.containers.push(cid)
    byContainer[cid] = cardId
  }
  for (const cid of Object.keys(v.containers)) {
    const path = domainPathOf(v, cid)
    if (!path.length) continue
    if (scope === null) {
      put(path[0], cid, false)
      continue
    }
    const i = path.indexOf(scope)
    if (i < 0) {
      put('ext:' + path[0], cid, true)
      continue
    }
    // path[i+1] 缺席 = 本级直属成员（叶子领域的内容），不进全景
    if (path[i + 1]) put(path[i + 1], cid, false)
  }
  for (const [id, n] of Object.entries(v.nodes)) {
    const card = cards[byContainer[n.container]]
    if (!card) continue
    if (n.kind === 'entry') card.entries.push(id)
    else card.nodes.push(id)
  }
  const edges = new Map<string, DomainEdge>()
  const ifaces: Record<string, Map<string, Set<string>>> = {}
  for (const e of v.edges) {
    const a = byContainer[v.nodes[e.from]?.container ?? '']
    const b = byContainer[v.nodes[e.to]?.container ?? '']
    if (!a || !b || a === b) continue
    if (a.startsWith('ext:') && b.startsWith('ext:')) continue
    const key = a + '|' + b
    const de = edges.get(key) ?? { from: a, to: b, pairs: [] }
    de.pairs.push(e)
    edges.set(key, de)
    if (e.status !== 'deleted' && !b.startsWith('ext:')) {
      const m = (ifaces[b] ??= new Map())
      if (!m.has(e.to)) m.set(e.to, new Set())
      m.get(e.to)!.add(a)
    }
  }
  return { cards, edges, ifaces }
}

// leafRoots 是叶子领域内部树的根：本域已扫描入口 + 被域外调用的接口。
// 都没有时（纯被内部使用的领域）退而取本域第一个非入口节点——空白页分不清
// 「这个领域是空的」和「渲染坏了」。
export function leafRoots(v: CgView, scope: string): string[] {
  const ifs = new Set<string>()
  for (const e of v.edges) {
    if (e.status === 'deleted') continue
    if (inScope(v, e.to, scope) && !inScope(v, e.from, scope) && !v.nodes[e.to]?.unscanned) ifs.add(e.to)
  }
  const roots = [
    ...scannedEntries(v).filter((id) => inScope(v, id, scope)),
    ...[...ifs].sort(),
  ]
  const uniq = [...new Set(roots)]
  if (uniq.length) return uniq
  return Object.keys(v.nodes)
    .filter((id) => inScope(v, id, scope) && v.nodes[id].kind !== 'entry')
    .sort()
    .slice(0, 1)
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/codegraph`
Expected: PASS，含新增 domains 测试；既有 graphmath / CallTree / FocusGraph / DetailPanel 测试保持全绿（`neighborhood` 的第 4 参数可选，旧调用不受影响）

- [ ] **Step 7: 注释与可观测性自检**

- 文件头注释含职责 + 边界 + 「与 Go 侧规则必须一致」。
- 每个导出函数有注释，写清**为什么**：`domainAncestors` 为何走 parent 链不拆字符串、`leafRoots` 为何要兜底、`domainAgg` 为何丢弃两端都在域外的边。
- 前端无日志设施：确认没有引入 console.log；本层的可观测性由 `hasDomains` 驱动的页面提示条承担（Task 10 落地）。

- [ ] **Step 8: 类型检查与提交**

```bash
npm --prefix web run typecheck
git add web/src/api/types.ts web/src/app/codegraph/graphmath.ts web/src/app/codegraph/domains.ts web/src/app/codegraph/domains.test.ts
git commit -m "feat(web): 代码图领域类型与领域算法层"
```

---

### Task 6: 领域卡的确定性布局

**Files:**
- Create: `web/src/app/codegraph/domainlayout.ts`
- Test: `web/src/app/codegraph/domainlayout.test.ts`

**Interfaces:**
- Consumes: Task 5 的 `DomainAgg`。
- Produces: `layoutDomains(agg: DomainAgg, ids: string[], seed?: Record<string, [number, number]>): Record<string, [number, number]>`

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/codegraph/domainlayout.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import type { DomainAgg } from './domains'
import { layoutDomains } from './domainlayout'

// 五个领域、两条调用边的最小聚合结果（只用到 layoutDomains 关心的字段）
const agg = {
  cards: {},
  ifaces: {},
  edges: new Map([
    ['a|b', { from: 'a', to: 'b', pairs: [{ from: 'x', to: 'y', status: '' as const }] }],
    ['b|c', { from: 'b', to: 'c', pairs: [{ from: 'y', to: 'z', status: '' as const }] }],
  ]),
} as unknown as DomainAgg
const ids = ['a', 'b', 'c', 'd', 'e']

describe('layoutDomains', () => {
  it('确定性：同样输入两次结果逐位相同（不许用随机数）', () => {
    expect(layoutDomains(agg, ids)).toEqual(layoutDomains(agg, ids))
  })
  it('不重叠：任意两张卡中心距离都拉开', () => {
    const pos = layoutDomains(agg, ids)
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const [x1, y1] = pos[ids[i]]
        const [x2, y2] = pos[ids[j]]
        expect(Math.hypot(x1 - x2, y1 - y2)).toBeGreaterThan(120)
      }
    }
  })
  it('有调用关系的领域比无关领域更近', () => {
    const pos = layoutDomains(agg, ids)
    const d = (p: string, q: string) => Math.hypot(pos[p][0] - pos[q][0], pos[p][1] - pos[q][1])
    expect(d('a', 'b')).toBeLessThan(d('a', 'd') + d('a', 'e'))
  })
  it('seed 生效：给定初始位置时从它继续松弛而不是推倒重来', () => {
    const far = layoutDomains(agg, ids, { a: [4000, 4000] })
    expect(far.a[0]).toBeGreaterThan(layoutDomains(agg, ids).a[0])
  })
  it('不越界：坐标恒在画布内（x≥30, y≥64）', () => {
    const pos = layoutDomains(agg, ids)
    for (const id of ids) {
      expect(pos[id][0]).toBeGreaterThanOrEqual(30)
      expect(pos[id][1]).toBeGreaterThanOrEqual(64)
    }
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/domainlayout.test.ts`
Expected: FAIL，`Failed to resolve import "./domainlayout"`

- [ ] **Step 3: 实现**

创建 `web/src/app/codegraph/domainlayout.ts`：

```ts
// domainlayout —— 领域卡的确定性力导向布局。
//
// 职责：给一层领域全景算出每张卡的左上角坐标
// 边界：纯函数、**不许用随机数**（Math.random / Date.now 都不行）——同一份数据
// 每次打开必须长得一样，用户的肌肉记忆和截图对照都依赖这一点。
//
// 三种力：领域卡是宽扁的，所以斥力按椭圆距离算（横向 340、纵向 200 为半径）；
// 有调用关系的领域用弹簧拉近，调用越密拉得越紧（权重封顶 4，避免一条超密的边
// 把整张图压成一团）；再加一点纵向重力把整体收在可视区里。
import type { DomainAgg } from './domains'

const ITER = 240        // 迭代次数：够收敛且在几毫秒内跑完
const RX = 340          // 椭圆斥力横向半径 ≈ 卡宽 + 间距
const RY = 200          // 纵向半径 ≈ 卡高 + 间距
const REST = 340        // 弹簧静止长度
const GRAVITY_Y = 330   // 纵向重力的目标带

export function layoutDomains(
  agg: DomainAgg,
  ids: string[],
  seed: Record<string, [number, number]> = {},
): Record<string, [number, number]> {
  const pos: Record<string, [number, number]> = {}
  ids.forEach((id, i) => {
    // 无 seed 时用 id 序号散开：质数步长让初值分散又完全确定
    pos[id] = seed[id] ? [seed[id][0], seed[id][1]] : [340 + ((i * 173) % 640), 90 + ((i * 257) % 420)]
  })
  const springs: [string, string, number][] = []
  for (const de of agg.edges.values()) {
    if (pos[de.from] && pos[de.to]) springs.push([de.from, de.to, Math.min(de.pairs.length, 4)])
  }
  for (let it = 0; it < ITER; it++) {
    const f: Record<string, [number, number]> = {}
    for (const id of ids) f[id] = [0, 0]
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const A = ids[i]
        const B = ids[j]
        const dx = pos[A][0] - pos[B][0]
        const dy = pos[A][1] - pos[B][1]
        const nd = Math.sqrt((dx / RX) ** 2 + (dy / RY) ** 2) || 0.01
        if (nd >= 1) continue
        const len = Math.hypot(dx, dy) || 1
        const push = (1 - nd) * 46
        f[A][0] += (dx / len) * push
        f[A][1] += (dy / len) * push
        f[B][0] -= (dx / len) * push
        f[B][1] -= (dy / len) * push
      }
    }
    for (const [a, b, w] of springs) {
      const dx = pos[b][0] - pos[a][0]
      const dy = pos[b][1] - pos[a][1]
      const len = Math.hypot(dx, dy) || 1
      const pull = (len - REST) * 0.012 * w
      f[a][0] += (dx / len) * pull
      f[a][1] += (dy / len) * pull
      f[b][0] -= (dx / len) * pull
      f[b][1] -= (dy / len) * pull
    }
    // 后半程减半阻尼：先快速铺开、再稳下来，避免末尾还在抖
    const damp = (it < 120 ? 1 : 0.5) * 0.5
    for (const id of ids) {
      f[id][1] += (GRAVITY_Y - pos[id][1]) * 0.005
      pos[id][0] = Math.max(30, pos[id][0] + f[id][0] * damp)
      pos[id][1] = Math.max(64, pos[id][1] + f[id][1] * damp)
    }
  }
  return pos
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/codegraph/domainlayout.test.ts`
Expected: PASS（5 tests）

- [ ] **Step 5: 注释自检**

文件头注释写清「不许用随机数」及其原因、三种力各自的作用；常量都要有一句说明它是按什么定的。

- [ ] **Step 6: 类型检查与提交**

```bash
npm --prefix web run typecheck
git add web/src/app/codegraph/domainlayout.ts web/src/app/codegraph/domainlayout.test.ts
git commit -m "feat(web): 领域卡确定性力导向布局"
```

---

### Task 7: 领域全景组件

**Files:**
- Create: `web/src/app/codegraph/DomainPanorama.tsx`
- Test: `web/src/app/codegraph/DomainPanorama.test.tsx`

**Interfaces:**
- Consumes: Task 5 的 `domainAgg / childDomainsOf`、Task 6 的 `layoutDomains`。
- Produces: `<DomainPanorama view scope selectedDomain selectedEdge onSelectDomain onSelectEdge onEnter />`；DOM 约定：领域卡带 `data-domain="<id>"`、连线标签带 `data-dedge="<from>|<to>"`、下钻按钮 `title="下钻到领域内部：<label>"`。

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/codegraph/DomainPanorama.test.tsx`：

```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CgDiff, CgGraph } from '../../api/types'
import { DomainPanorama } from './DomainPanorama'
import { mergeView } from './graphmath'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-21', generator: 'test' },
  domains: {
    d_cli: { label: 'cli', kind: '命令层', summary: '命令入口' },
    d_svc: { label: 'svc', kind: '服务端', summary: '服务与实体' },
    'd_svc/api': { label: 'api', kind: '服务端', summary: '对外方法', parent: 'd_svc' },
    'd_svc/store': { label: 'store', kind: '存储', summary: '实体存放', parent: 'd_svc' },
  },
  containers: {
    c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
    k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc/api' },
    k_ent: { label: 'store 实体', kind: '实体', domain: 'd_svc/store' },
  },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    n_save: { kind: 'func', container: 'k_svc', name: 'Server.Save', file: 'svc/server.go', line: 9 },
    m_task: { kind: 'model', container: 'k_ent', name: 'Task', file: 'svc/task.go', line: 2 },
  },
  edges: [['e_run', 'n_runE'], ['n_runE', 'n_save'], ['n_save', 'm_task']],
}
const noop = () => {}
const base = { selectedDomain: '', selectedEdge: '', onSelectDomain: noop, onSelectEdge: noop, onEnter: noop }
const domainsOf = (c: HTMLElement) =>
  [...c.querySelectorAll('[data-domain]')].map((e) => (e as HTMLElement).dataset.domain).sort()

describe('DomainPanorama', () => {
  it('顶层：领域卡 + 领域间连线，卡上显示职责与统计', () => {
    const { container } = render(<DomainPanorama view={mergeView(g)} scope={null} {...base} />)
    expect(domainsOf(container)).toEqual(['d_cli', 'd_svc'])
    expect(container.querySelectorAll('[data-dedge]')).toHaveLength(1)
    expect(screen.getByText('命令入口')).toBeTruthy()
    expect(screen.getByText(/1 处调用/)).toBeTruthy()
  })
  it('下钻一层：子领域实卡 + 域外虚线占位卡', () => {
    const { container } = render(<DomainPanorama view={mergeView(g)} scope="d_svc" {...base} />)
    expect(domainsOf(container)).toEqual(['d_svc/api', 'd_svc/store', 'ext:d_cli'])
  })
  it('进入按钮下钻；占位卡点击横跳到该领域', () => {
    const onEnter = vi.fn()
    const { container } = render(<DomainPanorama view={mergeView(g)} scope="d_svc" {...base} onEnter={onEnter} />)
    fireEvent.click(screen.getByTitle('下钻到领域内部：api'))
    expect(onEnter).toHaveBeenCalledWith('d_svc/api')
    fireEvent.click(container.querySelector('[data-domain="ext:d_cli"]')!)
    expect(onEnter).toHaveBeenLastCalledWith('d_cli')
  })
  it('叠加 diff 视图时，领域卡显示加/改/删计数徽标', () => {
    const d: CgDiff = {
      view: 'branch:x',
      nodesAdded: { n_new: { kind: 'func', container: 'k_svc', name: 'Server.New', file: 'svc/new.go', line: 2 } },
      nodesDeleted: ['n_save'],
    }
    const { container } = render(<DomainPanorama view={mergeView(g, d)} scope={null} {...base} />)
    const card = container.querySelector('[data-domain="d_svc"]')!
    expect(card.querySelector('[data-badge="add"]')!.textContent).toBe('+1')
    expect(card.querySelector('[data-badge="del"]')!.textContent).toBe('-1')
    expect(card.querySelector('[data-badge="mod"]')).toBeNull()
  })
  it('点卡片选中领域、点连线标签选中连线', () => {
    const onSelectDomain = vi.fn()
    const onSelectEdge = vi.fn()
    const { container } = render(
      <DomainPanorama view={mergeView(g)} scope={null} {...base}
        onSelectDomain={onSelectDomain} onSelectEdge={onSelectEdge} />)
    fireEvent.click(container.querySelector('[data-domain="d_svc"]')!)
    expect(onSelectDomain).toHaveBeenCalledWith('d_svc')
    fireEvent.click(container.querySelector('[data-dedge]')!)
    expect(onSelectEdge).toHaveBeenCalledWith('d_cli|d_svc')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/DomainPanorama.test.tsx`
Expected: FAIL，`Failed to resolve import "./DomainPanorama"`

- [ ] **Step 3: 实现**

创建 `web/src/app/codegraph/DomainPanorama.tsx`：

```tsx
// DomainPanorama —— 领域全景：本层领域卡 + 领域间调用连线。
//
// 一层全景只画一件事：这些领域之间谁调谁。卡上是职责一句话与成员统计，
// 连线粗细是调用处数——点卡看领域详情、点线看「谁调用了谁的接口」、
// 「进入 ▸」下钻。本级之外的领域画成虚线占位卡：不能因为下钻就丢掉跨界关系。
//
// 布局来自 domainlayout（确定性），拖拽只改本组件的位置状态，不回写数据。
import { useEffect, useMemo, useRef, useState } from 'react'
import { domainAgg } from './domains'
import type { DomainCard } from './domains'
import { layoutDomains } from './domainlayout'
import type { CgView } from './graphmath'

const CARD_W = 252
const EXT_W = 196

interface DomainPanoramaProps {
  view: CgView
  scope: string | null
  selectedDomain: string
  selectedEdge: string
  onSelectDomain: (id: string) => void
  onSelectEdge: (key: string) => void
  onEnter: (id: string) => void
}

// cardStats 汇总一张卡的展示数字：类数 / 方法数 / 对外接口数 / 入口（已扫描/总）
// / 本视图的加改删计数。
//
// 入口刻意给「已扫描/总数」两个数：只给总数会被读成「入口都在图里了」，
// 而实际扫描常常只追了一部分链——那正是这张图最容易骗人的地方。
function cardStats(view: CgView, card: DomainCard, ifaceCount: number) {
  const classes = card.containers.filter((cid) => !view.containers[cid]?.entry).length
  const funcs = card.nodes.filter((id) => view.nodes[id]?.kind === 'func').length
  const scanned = card.entries.filter((id) => !view.nodes[id]?.unscanned).length
  const all = [...card.nodes, ...card.entries]
  const count = (s: string) => all.filter((id) => view.nodes[id]?.status === s).length
  return {
    classes, funcs, ifaceCount, scanned, entries: card.entries.length,
    added: count('added'), modified: count('modified'), deleted: count('deleted'),
  }
}

export function DomainPanorama(props: DomainPanoramaProps) {
  const { view, scope, selectedDomain, selectedEdge, onSelectDomain, onSelectEdge, onEnter } = props
  const agg = useMemo(() => domainAgg(view, scope), [view, scope])
  const ids = useMemo(
    () => Object.keys(agg.cards).filter((id) => {
      const c = agg.cards[id]
      return c.ext || c.nodes.length + c.entries.length > 0
    }).sort(),
    [agg],
  )
  // 位置：进入新的一层就按布局算一次；拖拽只改这里的状态，不回写数据
  const [pos, setPos] = useState<Record<string, [number, number]>>({})
  useEffect(() => { setPos(layoutDomains(agg, ids)) }, [agg, ids])
  // 拖拽标志：拖完紧接着会冒出一个 click，用它把那次 click 吞掉，
  // 否则「拖动卡片」会被误判成「点击选中」
  const dragged = useRef(false)

  const onDrag = (id: string, ev: React.MouseEvent) => {
    const sx = ev.clientX
    const sy = ev.clientY
    const orig = pos[id] ?? [0, 0]
    dragged.current = false
    const move = (e: MouseEvent) => {
      const dx = e.clientX - sx
      const dy = e.clientY - sy
      // 4px 阈值：手抖不算拖动，否则选中会变得很难点
      if (Math.abs(dx) > 4 || Math.abs(dy) > 4) dragged.current = true
      if (!dragged.current) return
      setPos((p) => ({ ...p, [id]: [Math.max(10, orig[0] + dx), Math.max(60, orig[1] + dy)] }))
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    ev.preventDefault()
  }

  // 点击：占位卡横跳到那个领域，实卡选中看详情；刚拖过就不算点击
  const onCardClick = (id: string) => {
    if (dragged.current) return
    if (agg.cards[id].ext) onEnter(id.slice(4))
    else onSelectDomain(id)
  }

  const W = Math.max(1200, ...ids.map((id) => (pos[id]?.[0] ?? 0) + 420))
  const H = Math.max(620, ...ids.map((id) => (pos[id]?.[1] ?? 0) + 300))
  const center = (id: string): [number, number] => {
    const p = pos[id] ?? [0, 0]
    const w = agg.cards[id].ext ? EXT_W : CARD_W
    return [p[0] + w / 2, p[1] + 62]
  }

  return (
    <div className="relative min-w-0 flex-1 overflow-auto">
      {/* 拖乱了一键重排：从当前布局重新松弛，不是推倒重来 */}
      <button data-relayout onClick={() => setPos(layoutDomains(agg, ids, pos))}
        className="absolute right-3 top-2.5 z-30 rounded border bg-background px-2 py-0.5 text-xs"
        title="拖乱了就重排一次">重新布局</button>
      <div className="relative" style={{ width: W, height: H }}>
        <svg width={W} height={H} className="absolute inset-0">
          {[...agg.edges.entries()].map(([key, de]) => {
            if (!pos[de.from] || !pos[de.to]) return null
            const [x1, y1] = center(de.from)
            const [x2, y2] = center(de.to)
            const dx = x2 - x1
            const dy = y2 - y1
            const len = Math.hypot(dx, dy) || 1
            // 控制点垂直偏移：双向调用各弯一边，不叠在一起
            const mx = (x1 + x2) / 2 - (dy / len) * 30
            const my = (y1 + y2) / 2 + (dx / len) * 30
            const live = de.pairs.filter((p) => p.status !== 'deleted')
            const added = live.length > 0 && live.every((p) => p.status === 'added')
            const sel = selectedEdge === key
            return (
              <path key={key} d={`M ${x1} ${y1} Q ${mx} ${my} ${x2} ${y2}`} fill="none"
                stroke={added ? '#16a34a' : sel ? '#171717' : '#a8a8a8'}
                strokeWidth={1.5 + Math.min(de.pairs.length, 8) * 0.45} />
            )
          })}
        </svg>
        {[...agg.edges.entries()].map(([key, de]) => {
          if (!pos[de.from] || !pos[de.to]) return null
          const [x1, y1] = center(de.from)
          const [x2, y2] = center(de.to)
          const dx = x2 - x1
          const dy = y2 - y1
          const len = Math.hypot(dx, dy) || 1
          const added = de.pairs.filter((p) => p.status === 'added').length
          return (
            <div key={key} data-dedge={key} onClick={() => onSelectEdge(key)}
              className={'absolute z-10 -translate-x-1/2 -translate-y-1/2 cursor-pointer rounded-full border bg-background px-2 py-0.5 font-mono text-[10.5px] '
                + (selectedEdge === key ? 'border-primary text-primary' : '')}
              style={{ left: (x1 + x2) / 2 - (dy / len) * 30, top: (y1 + y2) / 2 + (dx / len) * 30 }}>
              {de.pairs.length} 处调用{added ? <span className="text-green-600"> +{added}</span> : null}
            </div>
          )
        })}
        {ids.map((id) => {
          const card = agg.cards[id]
          const meta = view.domains[card.ext ? id.slice(4) : id]
          if (!meta) return null
          const p = pos[id] ?? [0, 0]
          if (card.ext) {
            return (
              <div key={id} data-domain={id} onMouseDown={(e) => onDrag(id, e)} onClick={() => onCardClick(id)}
                className="absolute z-20 cursor-pointer select-none rounded-xl border-2 border-dashed bg-background/90 px-3 py-2 text-xs hover:border-primary"
                style={{ left: p[0], top: p[1], width: EXT_W }}>
                <div className="font-semibold text-muted-foreground">◇ {meta.label}<span className="ml-1.5 text-[10.5px] font-normal">{meta.kind}</span></div>
                <div className="mt-0.5 text-[11.5px] text-muted-foreground">领域外 · 点击进入</div>
              </div>
            )
          }
          const st = cardStats(view, card, agg.ifaces[id]?.size ?? 0)
          return (
            <div key={id} data-domain={id} onMouseDown={(e) => onDrag(id, e)} onClick={() => onCardClick(id)}
              className={'absolute z-20 cursor-grab select-none rounded-xl border-2 bg-background text-xs shadow-md '
                + (selectedDomain === id ? 'outline outline-2 outline-primary ' : '')}
              style={{ left: p[0], top: p[1], width: CARD_W }}>
              <div className="flex items-center gap-1.5 px-3.5 pb-1 pt-2 text-[13.5px] font-bold">
                {card.entries.length ? <span className="text-primary">⚑</span> : null}
                {meta.label}
                <span className="text-[10.5px] font-normal text-muted-foreground">{meta.kind}</span>
                <button title={'下钻到领域内部：' + meta.label}
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => { e.stopPropagation(); onEnter(id) }}
                  className="ml-auto rounded bg-muted px-2 py-0.5 text-[11px] hover:bg-primary hover:text-primary-foreground">
                  进入 ▸
                </button>
              </div>
              {meta.summary && <div className="px-3.5 pb-2 text-[11.5px] leading-relaxed text-muted-foreground">{meta.summary}</div>}
              <div className="flex flex-wrap gap-2.5 border-t px-3.5 py-1.5 text-[11px] text-muted-foreground">
                <span>{st.classes} 类</span>
                <span>{st.funcs} 方法</span>
                <span>⇢ {st.ifaceCount} 接口</span>
                {st.entries ? <span>⚑ {st.scanned}/{st.entries} 入口</span> : null}
                {st.added ? <span data-badge="add" className="rounded-full bg-green-600 px-1.5 font-bold text-white">+{st.added}</span> : null}
                {st.modified ? <span data-badge="mod" className="rounded-full bg-amber-500 px-1.5 font-bold text-white">~{st.modified}</span> : null}
                {st.deleted ? <span data-badge="del" className="rounded-full bg-red-600 px-1.5 font-bold text-white">-{st.deleted}</span> : null}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/codegraph/DomainPanorama.test.tsx`
Expected: PASS（5 tests）

- [ ] **Step 5: 注释与可观测性自检**

- 文件头注释写清「一层全景只回答一个问题」与占位卡存在的理由。
- `dragged` ref 与 4px 阈值都要有注释：说明「拖完为什么会冒出一次 click」以及阈值的用意。
- 界面上的诚实信号：入口统计必须是「已扫描/总数」两个数（只显总数会让人以为全扫过了）。

- [ ] **Step 6: 类型检查与提交**

```bash
npm --prefix web run typecheck && npx --prefix web eslint web/src/app/codegraph/DomainPanorama.tsx
git add web/src/app/codegraph/DomainPanorama.tsx web/src/app/codegraph/DomainPanorama.test.tsx
git commit -m "feat(web): 领域全景组件（卡片/连线/下钻/占位卡）"
```

---

### Task 8: 领域与连线详情面板

**Files:**
- Create: `web/src/app/codegraph/DomainDetail.tsx`
- Test: `web/src/app/codegraph/DomainDetail.test.tsx`

**Interfaces:**
- Consumes: Task 5 的 `domainAgg`。
- Produces: `<DomainDetail view scope domainId edgeKey onEnterNode onEnterDomain />`——`domainId` 与 `edgeKey` 互斥，都为空则渲染空壳。

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/codegraph/DomainDetail.test.tsx`：

```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CgGraph } from '../../api/types'
import { DomainDetail } from './DomainDetail'
import { mergeView } from './graphmath'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-21', generator: 'test' },
  domains: {
    d_cli: { label: 'cli', kind: '命令层', summary: '命令入口' },
    d_svc: { label: 'svc', kind: '服务端', summary: '服务与实体', desc: '干活与落库' },
  },
  containers: {
    c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
    k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc' },
  },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
  },
  edges: [['e_run', 'n_runE']],
}
const v = mergeView(g)
const noop = () => {}

describe('DomainDetail', () => {
  it('领域详情：职责 + 内部逻辑 + 对外接口（带调用方领域）', () => {
    render(<DomainDetail view={v} scope={null} domainId="d_svc" edgeKey=""
      onEnterNode={noop} onEnterDomain={noop} />)
    expect(screen.getByText('服务与实体')).toBeTruthy()
    expect(screen.getByText('干活与落库')).toBeTruthy()
    expect(screen.getByText(/runE/)).toBeTruthy()
    expect(screen.getByText(/← cli/)).toBeTruthy()
  })
  it('领域详情：点接口下钻到该节点', () => {
    const onEnterNode = vi.fn()
    render(<DomainDetail view={v} scope={null} domainId="d_svc" edgeKey=""
      onEnterNode={onEnterNode} onEnterDomain={noop} />)
    fireEvent.click(screen.getByText(/runE/))
    expect(onEnterNode).toHaveBeenCalledWith('n_runE')
  })
  it('连线详情：逐对列出谁调用了谁的接口', () => {
    render(<DomainDetail view={v} scope={null} domainId="" edgeKey="d_cli|d_svc"
      onEnterNode={noop} onEnterDomain={noop} />)
    expect(screen.getByText('cli → svc')).toBeTruthy()
    expect(screen.getByText(/1 处跨领域调用/)).toBeTruthy()
    expect(screen.getByText(/demo run/)).toBeTruthy()
    expect(screen.getByText(/runE/)).toBeTruthy()
  })
  it('都为空时渲染空壳，不崩', () => {
    const { container } = render(<DomainDetail view={v} scope={null} domainId="" edgeKey=""
      onEnterNode={noop} onEnterDomain={noop} />)
    expect(container.querySelector('aside')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/DomainDetail.test.tsx`
Expected: FAIL，`Failed to resolve import "./DomainDetail"`

- [ ] **Step 3: 实现**

创建 `web/src/app/codegraph/DomainDetail.tsx`：

```tsx
// DomainDetail —— 领域全景的右详情：领域卡详情 / 领域间连线详情。
//
// 领域详情回答「这个领域负责什么、对外开了哪些口子、外面从哪些入口进来」；
// 连线详情回答「谁调用了谁的接口」——逐对列出底层方法，点任一端下钻过去。
// 与 DetailPanel（方法/实体详情）是两个东西：那个跟着节点走，这个跟着领域走。
import { domainAgg } from './domains'
import type { CgView } from './graphmath'

interface DomainDetailProps {
  view: CgView
  scope: string | null
  domainId: string   // 与 edgeKey 互斥
  edgeKey: string
  onEnterNode: (id: string) => void
  onEnterDomain: (id: string) => void
}

function Sec({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-3.5">
      <div className="mb-1 text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      {children}
    </div>
  )
}

// NodeLink 一行可点的节点：点了就下钻到它所在的叶子领域并聚焦。
function NodeLink({ view, id, suffix, onEnterNode }: {
  view: CgView; id: string; suffix?: string; onEnterNode: (id: string) => void
}) {
  const n = view.nodes[id]
  if (!n) return null
  return (
    <div className="cursor-pointer py-0.5 font-mono text-xs hover:underline" onClick={() => onEnterNode(id)}>
      {n.name}{n.kind === 'func' ? '()' : ''}
      <span className="ml-1.5 font-sans text-[10.5px] text-muted-foreground">
        {view.containers[n.container]?.label}{suffix ? ' ' + suffix : ''}
      </span>
    </div>
  )
}

export function DomainDetail(props: DomainDetailProps) {
  const { view, scope, domainId, edgeKey, onEnterNode, onEnterDomain } = props
  const agg = domainAgg(view, scope)
  const shell = 'w-[340px] shrink-0 overflow-y-auto border-l p-3.5 text-sm'

  if (edgeKey) {
    const de = agg.edges.get(edgeKey)
    if (!de) return <aside className={shell} />
    const label = (cardId: string) => view.domains[cardId.startsWith('ext:') ? cardId.slice(4) : cardId]?.label ?? cardId
    return (
      <aside className={shell}>
        <h3 className="font-mono text-sm font-semibold">{label(de.from)} → {label(de.to)}</h3>
        <div className="mb-2.5 font-mono text-[11px] text-muted-foreground">{de.pairs.length} 处跨领域调用</div>
        <Sec label="谁调用了谁的接口">
          {de.pairs.map((p, i) => (
            <div key={i} className="border-t py-1 text-xs">
              <NodeLink view={view} id={p.from} onEnterNode={onEnterNode} />
              <div className="pl-2 text-[11px] text-muted-foreground">
                ↓ 调用{p.status === 'added' ? ' （本视图新增）' : p.status === 'deleted' ? ' （本视图删除）' : ''}
              </div>
              <NodeLink view={view} id={p.to} onEnterNode={onEnterNode} />
            </div>
          ))}
        </Sec>
      </aside>
    )
  }

  const meta = view.domains[domainId]
  const card = agg.cards[domainId]
  if (!meta || !card) return <aside className={shell} />
  const ifs = agg.ifaces[domainId]
  const scannedEntry = card.entries.filter((id) => !view.nodes[id]?.unscanned)
  const unscanned = card.entries.length - scannedEntry.length
  return (
    <aside className={shell}>
      <h3 className="font-mono text-sm font-semibold">
        {meta.label} <span className="font-sans text-[11px] font-normal text-muted-foreground">{meta.kind}</span>
      </h3>
      <div className="mb-2.5 font-mono text-[11px] text-muted-foreground">
        领域 · {card.containers.filter((cid) => !view.containers[cid]?.entry).length} 个类/分组
      </div>
      {meta.summary && <Sec label="职责"><div>{meta.summary}</div></Sec>}
      {meta.desc && <Sec label="内部逻辑"><div className="text-[12.5px] leading-relaxed text-muted-foreground">{meta.desc}</div></Sec>}
      <Sec label="对外开放接口（被其他领域调用）">
        {ifs && ifs.size ? [...ifs.entries()].map(([nid, callers]) => (
          <NodeLink key={nid} view={view} id={nid} onEnterNode={onEnterNode}
            suffix={'← ' + [...callers].map((c) => view.domains[c.startsWith('ext:') ? c.slice(4) : c]?.label ?? c).join('、')} />
        )) : <div className="text-xs text-muted-foreground">无——没有其他领域调用它的方法</div>}
      </Sec>
      {card.entries.length ? (
        <Sec label="对外入口（CLI / HTTP / WS）">
          {scannedEntry.map((id) => <NodeLink key={id} view={view} id={id} onEnterNode={onEnterNode} />)}
          {/* 未扫描入口只报数不列出：列出来会让人以为它们已经在图里了 */}
          {unscanned ? <div className="mt-1 text-[11.5px] text-muted-foreground">…另有 {unscanned} 个未扫描入口</div> : null}
        </Sec>
      ) : null}
      <button className="rounded border px-2 py-0.5 text-xs" onClick={() => onEnterDomain(domainId)}>
        进入领域内部 ▸
      </button>
    </aside>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/codegraph/DomainDetail.test.tsx`
Expected: PASS（4 tests）

- [ ] **Step 5: 注释与可观测性自检**

- 文件头注释写清与 DetailPanel 的分工。
- 「未扫描入口只报数不列出」那条注释必须在——它是防止「图里没有」被读成「代码里没有」的诚实信号。

- [ ] **Step 6: 类型检查与提交**

```bash
npm --prefix web run typecheck && npx --prefix web eslint web/src/app/codegraph/DomainDetail.tsx
git add web/src/app/codegraph/DomainDetail.tsx web/src/app/codegraph/DomainDetail.test.tsx
git commit -m "feat(web): 领域详情与领域间连线详情"
```

---

### Task 9: 树与焦点图的领域裁剪与跨域横跳

**Files:**
- Modify: `web/src/app/codegraph/CallTree.tsx`
- Modify: `web/src/app/codegraph/FocusGraph.tsx`
- Test: `web/src/app/codegraph/CallTree.test.tsx`
- Test: `web/src/app/codegraph/FocusGraph.test.tsx`

**Interfaces:**
- Consumes: Task 5 的 `inScope / leafRoots`、`neighborhood` 的 `expand` 参数。
- Produces: `CallTree` 与 `FocusGraph` 各新增两个 props：`scope: string | null`、`onCrossJump: (id: string) => void`。DOM 约定：域外节点卡带 `data-ext="1"`。

- [ ] **Step 1: 写失败测试**

在 `web/src/app/codegraph/CallTree.test.tsx` 里，把夹具换成带领域的版本并追加用例（保留原有那条用例，给它补 `scope={null} onCrossJump={noop}`）：

```tsx
const gd: CgGraph = {
  ...g,
  domains: {
    d_cli: { label: 'cli', kind: '命令层', summary: '入口' },
    d_svc: { label: 'svc', kind: '服务端', summary: '干活' },
  },
  containers: {
    c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
    k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc' },
  },
}

it('领域下钻：根是本域入口/接口，域外被调方是可点的横跳行', () => {
  const onCrossJump = vi.fn()
  render(<CallTree view={mergeView(gd)} foci={['e_run']} open={new Set(['e_run'])} scope="d_cli"
    onToggle={() => {}} onFocus={() => {}} onCrossJump={onCrossJump} />)
  fireEvent.click(screen.getByText(/↗ runE · svc 领域/))
  expect(onCrossJump).toHaveBeenCalledWith('n_runE')
})
```

在 `web/src/app/codegraph/FocusGraph.test.tsx` 的 `base` 里加 `scope: null, onCrossJump: () => {}`，并追加：

```tsx
it('领域下钻：域外节点画成虚线卡且点击横跳，不再从它继续扩展', () => {
  const onCrossJump = vi.fn()
  const gd = {
    ...g,
    domains: {
      d_cli: { label: 'cli', kind: '命令层', summary: '入口' },
      d_svc: { label: 'svc', kind: '服务端', summary: '干活' },
    },
    containers: {
      c_cli: { label: 'CLI', kind: '入口', entry: true, domain: 'd_cli' },
      k_svc: { label: 'svc', kind: '服务端', domain: 'd_svc' },
    },
  }
  const { container } = render(
    <FocusGraph view={mergeView(gd)} foci={['e_run']} onFocus={() => {}} {...base}
      scope="d_cli" depth={0} onCrossJump={onCrossJump} />)
  const ext = container.querySelector('[data-node="n_runE"]')!
  expect((ext as HTMLElement).dataset.ext).toBe('1')
  // n_do 在 n_runE 之后，域外不再扩展 → 不入图
  expect(container.querySelector('[data-node="n_do"]')).toBeNull()
  fireEvent.click(ext)
  expect(onCrossJump).toHaveBeenCalledWith('n_runE')
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/CallTree.test.tsx src/app/codegraph/FocusGraph.test.tsx`
Expected: FAIL——CallTree 找不到 `↗ runE · svc 领域` 文本；FocusGraph 的 `dataset.ext` 为 undefined

- [ ] **Step 3: 改 CallTree**

`web/src/app/codegraph/CallTree.tsx`：`Row` 与 `CallTree` 的 props 都加 `scope` 与 `onCrossJump`，根改用 `leafRoots`，域外子节点渲染成横跳行：

```tsx
import { inScope, leafRoots, nodeDomainPathOf } from './domains'
```

`Row` 的 props 增加 `scope: string | null; onCrossJump: (id: string) => void`，在 `node.children.map` 之前先分流：

```tsx
      {node.children.map((c, i) => (
        scope && !inScope(view, c.id, scope) ? (
          // 链路撞到领域外：不截断也不越界，给一行可点的横跳——
          // 截断会让人以为调用到此为止，越界会让这层树无边无际
          <div key={`${c.id}-${i}`} className="ml-4 cursor-pointer text-xs text-muted-foreground hover:underline"
            onClick={() => onCrossJump(c.id)}>
            ↗ {view.nodes[c.id]?.name} · {view.domains[nodeDomainPathOf(view, c.id)[0]]?.label ?? ''} 领域
          </div>
        ) : (
          <Row key={`${c.id}-${i}`} view={view} node={c} foci={foci} open={open}
            scope={scope} onToggle={onToggle} onFocus={onFocus} onCrossJump={onCrossJump} />
        )
      ))}
```

`CallTree` 本体的根改为：

```tsx
  const roots = props.scope ? leafRoots(props.view, props.scope) : scannedEntries(props.view)
```
并把 `roots.map(...)` 里的 `<Row>` 补上 `scope={props.scope} onCrossJump={props.onCrossJump}`。

`chainTree` 调用保持不变（它按 adj 展开，域外节点由上面的分流拦住）。

- [ ] **Step 4: 改 FocusGraph**

`web/src/app/codegraph/FocusGraph.tsx`：

props 增加 `scope: string | null; onCrossJump: (id: string) => void`。

邻域计算改为带边界（域外节点入图但不再扩展）：

```tsx
import { inScope, nodeDomainPathOf } from './domains'
```

```tsx
  const { dist, px, py, W, H, order } = useMemo(() => {
    const dist = neighborhood(view, foci, depth, (id) => inScope(view, id, scope))
    return { dist, ...layoutBands(view, dist) }
  }, [view, foci, depth, scope])
```

节点卡渲染里，先判是否域外，域外走虚线样式 + 横跳：

```tsx
        {Object.keys(dist).map((id) => {
          const n = view.nodes[id]
          const ext = !!scope && !inScope(view, id, scope)
          return (
            <div key={id} data-node={id} data-ext={ext ? '1' : undefined}
              className={nodeCls(id) + (ext ? ' border-dashed bg-muted ' : '')}
              style={{ left: px[id], top: py[id] }}
              onClick={(e) => {
                if (ext) { onCrossJump(id); return }   // 域外节点只有一个动作：跳过去
                onSelect(id)
                if (!(foci.length === 1 && foci[0] === id)) onFocus(id, e.metaKey || e.ctrlKey)
              }}>
              <div className={'break-all font-mono text-[11px] font-semibold ' + (n.status === 'deleted' ? 'text-muted-foreground line-through' : '')}>
                {n.name}{n.kind === 'func' ? '()' : ''}{staleIds.has(id) ? ' ⚠' : ''}
              </div>
              <div className="flex gap-1 text-[9.5px] opacity-70">
                <span className={ext ? 'text-primary' : ''}>
                  {ext ? '◇ ' + (view.domains[nodeDomainPathOf(view, id)[0]]?.label ?? '') + ' 领域'
                       : view.containers[n.container]?.label ?? ''}
                </span>
                {n.tests?.length ? <span className="text-green-600">✓{n.tests.length}</span> : null}
              </div>
            </div>
          )
        })}
```

顶部提示条的文案在 scope 非空时补一句：

```tsx
          {foci.length > 1 ? '并集视图：N 个焦点的链叠加'
            : scope ? '单击：只看它的链 · ⌘+单击：并集 · 虚线卡=领域外，点击横跳'
            : '单击：只看它的链 · ⌘+单击：并集 · 空白拖动 · ⌃滚轮缩放'}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/codegraph`
Expected: PASS，含新增 2 个用例；既有用例全绿

- [ ] **Step 6: 注释自检**

- CallTree 的横跳分流处必须有注释说明「为什么不截断也不越界」。
- FocusGraph 的 `expand` 回调处注释说明领域边界的作用。

- [ ] **Step 7: 类型检查与提交**

```bash
npm --prefix web run typecheck && npx --prefix web eslint web/src/app/codegraph
git add web/src/app/codegraph/CallTree.tsx web/src/app/codegraph/FocusGraph.tsx web/src/app/codegraph/CallTree.test.tsx web/src/app/codegraph/FocusGraph.test.tsx
git commit -m "feat(web): 树与焦点图的领域裁剪与跨域横跳"
```

---

### Task 10: 页面三态路由、面包屑与时序图下线

**Files:**
- Modify: `web/src/app/codegraph/CodegraphPage.tsx`
- Delete: `web/src/app/codegraph/SeqView.tsx`
- Test: `web/src/app/codegraph/CodegraphPage.test.tsx`（新建）

**Interfaces:**
- Consumes: Task 5–9 的全部导出。
- Produces: 页面三态——`scope=null 且有领域` → 全景；`scope` 有子领域 → 子领域全景；`scope` 是叶子（或整图无领域） → 树+图。

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/codegraph/CodegraphPage.test.tsx`：

```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CodegraphResp } from '../../api/types'
import { CodegraphPage } from './CodegraphPage'

// vi.mock 的工厂会被提升到文件顶部执行，直接引用普通的顶层 let 会踩
// 「Cannot access before initialization」。用 vi.hoisted 造一个可变容器，
// 每个用例改它就能换数据，不必 resetModules + 动态 import。
const state = vi.hoisted(() => ({ data: null as unknown as import('../../api/types').CodegraphResp }))

const resp: CodegraphResp = {
  baseline: {
    meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-21', generator: 'test' },
    domains: {
      d_cli: { label: 'cli', kind: '命令层', summary: '命令入口' },
      d_svc: { label: 'svc', kind: '服务端', summary: '服务与实体' },
      'd_svc/api': { label: 'api', kind: '服务端', summary: '对外方法', parent: 'd_svc' },
    },
    containers: {
      c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
      k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc/api' },
    },
    nodes: {
      e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
      n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    },
    edges: [['e_run', 'n_runE']],
  },
  views: {},
  stale: [],
}

vi.mock('../data/useProjectTree', () => ({
  useProjectTree: () => ({ data: { projects: [{ name: 'demo' }] } }),
}))
vi.mock('./useCodegraph', () => ({
  useCodegraph: () => ({ data: state.data, error: '', loading: false, reload: () => {} }),
}))

beforeEach(() => { state.data = resp })

describe('CodegraphPage 三态下钻', () => {
  it('默认落在领域全景', async () => {
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelectorAll('[data-domain]').length).toBe(2))
    expect(screen.getByText('领域全景')).toBeTruthy()
  })
  it('进入有子领域的领域 → 再出一层全景；面包屑可逐级返回', async () => {
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelector('[data-domain="d_svc"]')).toBeTruthy())
    fireEvent.click(screen.getByTitle('下钻到领域内部：svc'))
    await waitFor(() => expect(container.querySelector('[data-domain="d_svc/api"]')).toBeTruthy())
    fireEvent.click(screen.getByText('◀ 领域全景'))
    await waitFor(() => expect(container.querySelector('[data-domain="d_cli"]')).toBeTruthy())
  })
  it('进入叶子领域 → 切到树+图视图', async () => {
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelector('[data-domain="d_cli"]')).toBeTruthy())
    fireEvent.click(screen.getByTitle('下钻到领域内部：cli'))
    await waitFor(() => expect(container.querySelectorAll('[data-node]').length).toBeGreaterThan(0))
    expect(container.querySelector('[data-domain]')).toBeNull()
  })
  it('无领域数据：降级为单领域视图并给出提示', async () => {
    state.data = { ...resp, baseline: { ...resp.baseline, domains: undefined } }
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelectorAll('[data-node]').length).toBeGreaterThan(0))
    expect(container.querySelector('[data-domain]')).toBeNull()
    expect(screen.getByText(/未包含领域划分/)).toBeTruthy()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/CodegraphPage.test.tsx`
Expected: FAIL——页面还是旧的树+图 + 图型切换，找不到 `[data-domain]` 与「领域全景」

- [ ] **Step 3: 重写页面**

把 `web/src/app/codegraph/CodegraphPage.tsx` 整体换成：

```tsx
// CodegraphPage —— 代码图页（/codegraph）：领域图三级下钻。
//
// 三态（spec §5 定稿形态）：
//   scope=null 且图里有领域 → 领域全景
//   scope 还有子领域        → 子领域全景（域外领域画成占位卡）
//   scope 是叶子领域        → 树+图视图（左树 320 / 中图自适应 / 右详情 340）
// 整图没有领域段时降级为单领域视图并明示提示——不按包名伪造领域。
import { useMemo, useState } from 'react'
import { useProjectTree } from '../data/useProjectTree'
import { CallTree } from './CallTree'
import { DetailPanel } from './DetailPanel'
import { DomainDetail } from './DomainDetail'
import { DomainPanorama } from './DomainPanorama'
import { FocusGraph } from './FocusGraph'
import { childDomainsOf, domainAncestors, hasDomains, nodeDomainPathOf } from './domains'
import { mergeView, scannedEntries } from './graphmath'
import { useCodegraph } from './useCodegraph'

export function CodegraphPage() {
  const tree = useProjectTree()
  const projects = useMemo(() => (tree.data?.projects ?? []).map((p) => p.name), [tree.data])
  const [project, setProject] = useState('')
  const active = project || projects[0] || ''
  const { data, error, loading, reload } = useCodegraph(active)

  const [viewName, setViewName] = useState('baseline')
  const [scope, setScope] = useState<string | null>(null)
  const [selDomain, setSelDomain] = useState('')
  const [selEdge, setSelEdge] = useState('')
  const [depth, setDepth] = useState(2)
  const [foci, setFoci] = useState<string[]>([])
  const [hist, setHist] = useState<string[][]>([])
  const [histIdx, setHistIdx] = useState(-1)
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [selected, setSelected] = useState('')

  const view = useMemo(() => {
    if (!data) return null
    const d = viewName === 'baseline' ? undefined : data.views[viewName]
    return mergeView(data.baseline, d)
  }, [data, viewName])
  const staleIds = useMemo(() => new Set((data?.stale ?? []).map((s) => s.id)), [data])

  const single = !!view && !hasDomains(view)               // 旧图：整张图当一个领域看
  const pano = !!view && !single && (scope === null || childDomainsOf(view, scope).length > 0)
  const leafScope = single ? null : scope

  const effFoci = useMemo(() => {
    if (!view) return []
    const ok = foci.filter((f) => view.nodes[f] && view.nodes[f].status !== 'deleted')
    return ok.length ? ok : scannedEntries(view).slice(0, 1)
  }, [view, foci])

  const setFociWithHist = (next: string[], fromHist = false) => {
    if (next.join('|') === effFoci.join('|')) return
    if (!fromHist) {
      // 历史为空时先把当前（默认）焦点垫底：否则第一次换焦点后「后退」无处可退
      const base = hist.length ? hist.slice(0, histIdx + 1) : [effFoci]
      const h = [...base, next]
      setHist(h)
      setHistIdx(h.length - 1)
    }
    setFoci(next)
    setSelected(next[next.length - 1] ?? '')
  }
  const onFocus = (id: string, additive: boolean) => {
    if (additive) {
      const s = effFoci.includes(id) ? effFoci.filter((x) => x !== id) : [...effFoci, id]
      if (s.length) setFociWithHist(s)
    } else setFociWithHist([id])
  }

  // 换一层领域：焦点历史与展开状态都作废——它们是上一层的语境，带过去只会误导
  const goScope = (next: string | null) => {
    setScope(next)
    setSelDomain('')
    setSelEdge('')
    setFoci([])
    setHist([])
    setHistIdx(-1)
    setOpen(new Set())
    setSelected('')
  }
  // 横跳：落到目标节点所在的叶子领域并把它设为焦点
  const enterNode = (id: string) => {
    if (!view) return
    const path = nodeDomainPathOf(view, id)
    goScope(path.length ? path[path.length - 1] : null)
    setFoci([id])
    setHist([[id]])
    setHistIdx(0)
    setSelected(id)
  }

  if (error) return <div className="p-6 text-sm text-red-600">{error}</div>
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-sm">
        <label className="text-muted-foreground">项目</label>
        <select value={active} onChange={(e) => { setProject(e.target.value); goScope(null) }}
          className="rounded border px-1.5 py-0.5">
          {projects.map((p) => <option key={p}>{p}</option>)}
        </select>
        <label className="text-muted-foreground">视图</label>
        <select value={viewName} onChange={(e) => { setViewName(e.target.value); goScope(null) }}
          className="rounded border px-1.5 py-0.5">
          <option value="baseline">基准 · {data?.baseline.meta.branch ?? ''}</option>
          {Object.entries(data?.views ?? {}).map(([k, v]) => <option key={k} value={k}>{v.view}</option>)}
        </select>
        {data && data.stale.length > 0 && (
          <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700"
            title={data.stale.map((s) => `${s.id}: ${s.reason}`).join('\n')}>
            ⚠ {data.stale.length} 个节点疑似失鲜
          </span>
        )}
        {single && (
          <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700">
            该图未包含领域划分（扫描版本较旧）：重扫可获得领域全景
          </span>
        )}
        <button onClick={reload} className="ml-auto rounded border px-2 py-0.5 text-xs">刷新</button>
      </div>
      {loading || !view ? (
        <div className="p-6 text-sm text-muted-foreground">{loading ? '加载中…' : '该项目未生成代码图'}</div>
      ) : (
        <div className="relative flex min-h-0 flex-1">
          {!single && (
            <div className="absolute left-3.5 top-2.5 z-30 inline-flex items-center gap-2 rounded-full border bg-background px-3.5 py-1 text-xs shadow-sm">
              {scope === null ? (
                <>
                  <b>领域全景</b>
                  <span className="text-[11px] text-muted-foreground">点卡片看职责 · 点连线看谁调谁 · 进入 ▸ 下钻</span>
                </>
              ) : (
                <>
                  <span className="cursor-pointer text-muted-foreground hover:underline" onClick={() => goScope(null)}>◀ 领域全景</span>
                  {domainAncestors(view, scope).map((id, i, arr) => (
                    <span key={id} className="inline-flex items-center gap-2">
                      <span className="text-muted-foreground">▸</span>
                      {i === arr.length - 1 ? (
                        <>
                          <b>{view.domains[id]?.label}</b>
                          <span className="text-[11px] text-muted-foreground">{view.domains[id]?.kind}</span>
                        </>
                      ) : (
                        <span className="cursor-pointer text-muted-foreground hover:underline" onClick={() => goScope(id)}>
                          {view.domains[id]?.label}
                        </span>
                      )}
                    </span>
                  ))}
                </>
              )}
            </div>
          )}
          {pano ? (
            <>
              <DomainPanorama view={view} scope={scope} selectedDomain={selDomain} selectedEdge={selEdge}
                onSelectDomain={(id) => { setSelDomain(id); setSelEdge('') }}
                onSelectEdge={(k) => { setSelEdge(k); setSelDomain('') }}
                onEnter={goScope} />
              <DomainDetail view={view} scope={scope} domainId={selDomain} edgeKey={selEdge}
                onEnterNode={enterNode} onEnterDomain={goScope} />
            </>
          ) : (
            <>
              <CallTree view={view} foci={effFoci} open={open} scope={leafScope}
                onToggle={(id, o) => setOpen((s) => {
                  const n = new Set(s)
                  if (o) n.add(id)
                  else n.delete(id)
                  return n
                })}
                onFocus={onFocus} onCrossJump={enterNode} />
              <FocusGraph view={view} foci={effFoci} depth={depth} staleIds={staleIds} scope={leafScope}
                onDepth={setDepth} onFocus={onFocus} onSelect={setSelected} onCrossJump={enterNode}
                canBack={histIdx > 0} canFwd={histIdx < hist.length - 1}
                onBack={() => { setHistIdx(histIdx - 1); setFociWithHist(hist[histIdx - 1], true) }}
                onFwd={() => { setHistIdx(histIdx + 1); setFociWithHist(hist[histIdx + 1], true) }} />
              <DetailPanel project={active} view={view} nodeId={selected || effFoci[effFoci.length - 1] || ''}
                stale={staleIds} onJump={enterNode} />
            </>
          )}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: 删掉时序图**

```bash
git rm web/src/app/codegraph/SeqView.tsx
```

确认没有别处 import 它：

Run: `grep -rn "SeqView" web/src || echo "无引用"`
Expected: `无引用`

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/codegraph`
Expected: PASS，含新增 4 个页面用例

- [ ] **Step 6: 注释与可观测性自检**

- 页面文件头注释写清三态判定规则与「不伪造领域」的降级立场。
- `goScope` 的「历史与展开状态作废」必须有注释说明原因。
- 诚实信号自检：失鲜徽标、未扫描计数、无领域提示条三样都在，且措辞给得出下一步动作。

- [ ] **Step 7: 全量前端检查与提交**

```bash
cd web && npx vitest run && npx tsc --noEmit -p . && npm run build && npx eslint src/app/codegraph
```
Expected: 全部通过（build 只有 Vite chunk size 提示属正常）

```bash
git add -A web/src/app/codegraph
git commit -m "feat(web): 代码图三级下钻页面，时序图下线"
```

---

### Task 11: 整分支终审与收尾

**Files:**
- Modify: `docs/superpowers/ledgers/2026-08-19-codegraph-phase1-ledger.md`

- [ ] **Step 1: 后端全量**

Run: `go build ./... && go test ./internal/codegraph ./cmd ./internal/agentd`
Expected: 全绿。仓库里另有若干**环境敏感的既有红测试**（`internal/client` / `internal/config` / `internal/executor/grok` 的 cursor root、socket 路径过长等），与本次改动无关——如遇到，如实记进 ledger，**不要改无关判据去凑绿**。

- [ ] **Step 2: 前端全量**

Run: `cd web && npx vitest run && npx tsc --noEmit -p . && npm run build`
Expected: 全绿

- [ ] **Step 3: 真实 CLI 冒烟**

```bash
go run . graph domains --repo internal/codegraph/testdata/repo
go run . graph validate --repo internal/codegraph/testdata/repo
```
Expected: 两条都退出 0；domains 输出 4 个领域且 `d_svc` 带 2 个 children；validate 的 `issues` 为 null、`domains` 为 4

- [ ] **Step 4: 格式与范围复审**

```bash
gofmt -l . && git diff --check
git diff --stat 4c7531971..HEAD
```
Expected: 前两条无输出；diff 范围只含本 plan 列出的文件（Go 11 个 + Web 12 个 + 本 ledger），出现计划外文件要说明理由

- [ ] **Step 5: 续写 ledger**

在 `docs/superpowers/ledgers/2026-08-19-codegraph-phase1-ledger.md` 末尾追加本轮记录，格式与既有条目一致，每条必须写**实际跑过的命令与实际输出**（不是"应该通过"）：

```markdown
- 领域视图增量（plan `docs/superpowers/plans/2026-08-21-codegraph-domain-view.md`，基线 `4c7531971`）
- Task 1 / 完成裁决：… 验证：`go test ./internal/codegraph/ -v` 实际通过（N tests）；`gofmt -l internal/codegraph/` 实际无输出。Commit 范围：…
- …（逐 task 一条）
- Task 11 / 终审：后端/前端全量与 CLI 冒烟的实际结果、范围复审结论、既有环境敏感红测试的如实记录
```

- [ ] **Step 6: 提交**

```bash
git add docs/superpowers/ledgers/2026-08-19-codegraph-phase1-ledger.md
git commit -m "chore(codegraph): 领域视图增量终审收尾"
```

---

## 审核者本地验收清单（不派发，不属于上面任何 task）

以下三项需要驱动本机浏览器或另一次 handoff 派发，与执行纪律块冲突，**由审核者本地做**：

1. 控制台真机走查：起 web dev server，在浏览器里实际走一遍「领域全景 → 有子领域的领域 → 叶子领域树+图 → 域外虚线卡横跳 → 面包屑逐级返回」。
2. 用新配方重扫 handoff 自身，产出带 domains 的 `codegraph/baseline.json`，再对照真实数据看领域切分是否符合真实架构。
3. 对照 `prototypes/codegraph/pages/codegraph.html` 验收形态，通过后把 `prototypes/base/README.md` 的代码图行推进为「已确认」。
