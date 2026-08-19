# 代码图一期（codegraph phase 1）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地代码图一期——`internal/codegraph` 数据包 + `handoff graph` CLI 查询 + agentd 只读端点 + Web 控制台「树+图 / 时序图」页，数据契约为仓库内 `codegraph/*.json`。

**Architecture:** 三层硬边界（spec `docs/superpowers/specs/2026-08-19-codegraph-design.md` §2）：数据契约是**被扫描项目仓库里的 JSON 文件**（`codegraph/baseline.json` + `codegraph/diffs/<view>.json`），与 handoff 无关；`handoff graph` CLI **直读本地仓库文件，不经 agentd**；控制台页经 agentd 的只读端点取数、前端合并渲染。`internal/codegraph` 包是唯一的模型与算法源（加载/校验/合并/查询/保鲜），CLI 与 agentd 都只是它的薄壳。

**Tech Stack:** Go 1.x（标准库 only，`log/slog`）、cobra CLI、net/http ServeMux（Go 1.22 方法路由）、React + TypeScript + Tailwind v4 + vitest。

## Global Constraints

- **`internal/codegraph` 不得 import `internal/agentd` / `internal/store` / `internal/client`**——数据契约独立于 handoff 是 spec §2 硬约束（仓库有 `moduleisolation_test.go` 类似机制，本包保持零内部依赖即可）。
- **`handoff graph` 不得发起任何网络请求**，只读 `--repo`（默认 cwd）下的文件。
- 所有新 Go 文件顶部写「职责 + 边界」中文注释；导出函数写参数/返回/注意事项 doc 注释（仓库既有风格，见 `cmd/fetch.go`、`internal/agentd/projectadmin.go`）。
- agentd handler 内日志用 `s.log`（slog），关键节点必打：请求入口带参数、错误分支带 `cause`、成功出口带统计（对照 `handleProjectBranches` 的打法）。CLI 层错误经 RunE 返回、正文输出走 `cmd.OutOrStdout()`——**禁止 `fmt.Printf` 当日志**。
- 前端文案全中文；样式 Tailwind v4（参考 `web/src/app/settings/GeneralPage.tsx`）；纯逻辑（合并/BFS/布局）必须进 `graphmath.ts` 配 vitest，组件不内嵌算法。
- **「查询无上游」与「未扫描」必须可区分**（spec §6）：所有查询输出都带 `unscannedEntries` 计数与警示语。
- 深度默认值 **2**（`--depth 0` = 不限），与 UI 层级下拉一致。
- 每个 task 收尾跑 `gofmt -l .`（必须零输出）——executor ledger 漏 gofmt 是已知坑。
- 提交信息用中文，遵循仓库 `type(scope): 说明` 风格。

## File Structure

```
internal/codegraph/          新包：模型 + 算法（零内部依赖）
  types.go                   schema 结构体（Graph/Node/Diff/View/Result）
  load.go                    LoadGraph / LoadDiff / ListViews
  validate.go                引用完整性校验
  merge.go                   基准 + diff → View
  query.go                   Resolve / Neighborhood（chain、who-calls、并集、深度）
  stale.go                   file:line 签名保鲜检测
  testdata/repo/...          测试夹具（迷你仓库：codegraph/ + 假源码文件）
cmd/graph.go                 handoff graph validate|views|chain|who-calls
cmd/graph_test.go
internal/agentd/codegraph.go GET /api/projects/{name}/codegraph 与 …/codegraph/source
internal/agentd/codegraph_test.go
internal/agentd/server.go    注册两条路由（修改）
docs/codegraph-scan-recipe.md  扫描配方（派发给 executor 的 plan 模板）
web/src/api/types.ts         新增 Codegraph* 类型（修改）
web/src/api/client.ts        fetchCodegraph / fetchCodegraphSource（修改）
web/src/app/codegraph/
  graphmath.ts               mergeView / neighborhood / layoutBands（纯函数）
  graphmath.test.ts
  useCodegraph.ts            数据 hook
  CodegraphPage.tsx          页面外壳：工具条 + 三栏
  CallTree.tsx               左树
  FocusGraph.tsx             中间竖向焦点子图（平移/缩放/历史/并集/层级）
  DetailPanel.tsx            右详情（含源码按 file:line 实时读）
  SeqView.tsx                时序图（辅助视角）
web/src/app/shell/Shell.tsx  路由 /codegraph（修改）
web/src/app/tree/ProjectTree.tsx  底部入口「代码图」（修改）
```

形态验收基准：`prototypes/codegraph/pages/codegraph.html`（**gitignore 副本，只在协调者本机**——executor 看不到它，本 plan 的代码块就是从它固化下来的行为契约，以 plan 为准实现即可；真机对照由审核者本地做）。

数据契约（已入库的真实种子：`codegraph/baseline.json`，189 节点/132 边/16 容器）：

```jsonc
// codegraph/baseline.json
{ "meta": { "project": "handoff", "branch": "main", "commit": "60b944f5",
            "scannedAt": "2026-08-19", "generator": "codex" },
  "containers": { "c_cli": { "label": "CLI 命令", "kind": "入口", "entry": true },
                  "k_client_Client": { "label": "client.Client", "kind": "客户端" } },
  "nodes": { "e_dispatch": { "kind": "entry", "container": "c_cli", "name": "handoff dispatch",
                             "file": "cmd/dispatch.go", "line": 24, "summary": "…", "unscanned": false },
             "n_clientDispatch": { "kind": "func", "container": "k_client_Client",
                                   "name": "Client.Dispatch", "file": "internal/client/client.go", "line": 118,
                                   "signature": "func (c *Client) Dispatch(…) error",
                                   "params": [["ctx", "context.Context", "请求生命周期"]],
                                   "returns": "error", "summary": "…",
                                   "tests": [{ "name": "TestDispatch", "file": "internal/client/client_test.go:41", "snippet": "…" }] },
             "m_task": { "kind": "model", "container": "k_store_ent", "name": "Task",
                         "file": "internal/store/task.go", "line": 18,
                         "fields": [["ID", "string", "任务号"]], "summary": "…" } },
  "edges": [["e_dispatch", "n_runDispatch"]],
  "diffs": {} }   // 兼容字段，一期忽略；视图一律来自 codegraph/diffs/ 目录
```

```jsonc
// codegraph/diffs/<view>.json —— 文件名（去 .json）即视图名
{ "view": "branch:b164-retry-backoff",   // 或 plan:xxx
  "base": "60b944f5", "summary": "重试退避",
  "nodesAdded":    { "n_backoff": { /* 完整 Node */ } },
  "nodesModified": { "n_clientDispatch": { /* 修改后的完整 Node，可带 signatureOld */ } },
  "nodesDeleted":  ["n_oldRetry"],
  "edgesAdded":    [["n_clientDispatch", "n_backoff"]],
  "edgesDeleted":  [["n_clientDispatch", "n_oldRetry"]] }
```

---

### Task 1: internal/codegraph — 类型与加载

**Files:**
- Create: `internal/codegraph/types.go`
- Create: `internal/codegraph/load.go`
- Create: `internal/codegraph/load_test.go`
- Create: `internal/codegraph/testdata/repo/codegraph/baseline.json`
- Create: `internal/codegraph/testdata/repo/codegraph/diffs/branch-x.json`

**Interfaces:**
- Produces: `type Graph/Node/Container/TestRef/Edge/Diff`；`LoadGraph(repoRoot string) (*Graph, error)`；`LoadDiff(repoRoot, view string) (*Diff, error)`；`ListViews(repoRoot string) ([]string, error)`。后续所有 task 都消费这些类型。

- [ ] **Step 1: 写测试夹具**

`internal/codegraph/testdata/repo/codegraph/baseline.json`（后续 Task 2-6 复用同一夹具，故意含 1 个 unscanned 入口）：

```json
{
  "meta": {"project": "demo", "branch": "main", "commit": "abc1234", "scannedAt": "2026-08-19", "generator": "test"},
  "containers": {
    "c_cli": {"label": "CLI 命令", "kind": "入口", "entry": true},
    "k_svc": {"label": "svc.Server", "kind": "服务端"},
    "k_ent": {"label": "store 实体", "kind": "实体"}
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

`internal/codegraph/testdata/repo/codegraph/diffs/branch-x.json`：

```json
{
  "view": "branch:x", "base": "abc1234", "summary": "改 Do 加 Audit",
  "nodesAdded": {"n_audit": {"kind": "func", "container": "k_svc", "name": "Server.Audit",
    "file": "svc/audit.go", "line": 3, "signature": "func (s *Server) Audit() error", "returns": "error", "summary": "审计"}},
  "nodesModified": {"n_do": {"kind": "func", "container": "k_svc", "name": "Server.Do",
    "file": "svc/server.go", "line": 4, "signature": "func (s *Server) Do(ctx context.Context) error",
    "signatureOld": "func (s *Server) Do() error", "returns": "error", "summary": "干活（带 ctx）"}},
  "nodesDeleted": ["n_save"],
  "edgesAdded": [["n_do", "n_audit"]],
  "edgesDeleted": [["n_do", "n_save"]]
}
```

- [ ] **Step 2: 写失败测试** `internal/codegraph/load_test.go`

```go
// codegraph 加载层测试：夹具仓库读取、diffs 目录发现、坏 JSON 报错带路径。
package codegraph

import (
	"path/filepath"
	"testing"
)

func TestLoadGraph(t *testing.T) {
	g, err := LoadGraph(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if g.Meta.Project != "demo" || len(g.Nodes) != 6 || len(g.Edges) != 4 {
		t.Fatalf("解析形状不对: meta=%+v nodes=%d edges=%d", g.Meta, len(g.Nodes), len(g.Edges))
	}
	if !g.Nodes["e_skip"].Unscanned {
		t.Fatal("unscanned 标丢失")
	}
}

func TestLoadGraphMissing(t *testing.T) {
	if _, err := LoadGraph(t.TempDir()); err == nil {
		t.Fatal("无 codegraph/baseline.json 应当报错")
	}
}

func TestListViewsAndLoadDiff(t *testing.T) {
	views, err := ListViews(filepath.Join("testdata", "repo"))
	if err != nil || len(views) != 1 || views[0] != "branch-x" {
		t.Fatalf("ListViews: %v %v", views, err)
	}
	d, err := LoadDiff(filepath.Join("testdata", "repo"), "branch-x")
	if err != nil {
		t.Fatalf("LoadDiff: %v", err)
	}
	if d.View != "branch:x" || len(d.NodesAdded) != 1 || len(d.NodesDeleted) != 1 {
		t.Fatalf("diff 形状不对: %+v", d)
	}
	if d.NodesModified["n_do"].SignatureOld == "" {
		t.Fatal("signatureOld 丢失")
	}
}

func TestListViewsEmptyDir(t *testing.T) {
	// 没有 diffs 目录不是错误：返回空列表（大多数仓库只有基线）
	dir := t.TempDir()
	writeFixtureBaseline(t, dir) // helper: 把最小 baseline 写进 dir/codegraph/
	views, err := ListViews(dir)
	if err != nil || len(views) != 0 {
		t.Fatalf("空 diffs 应返回空列表: %v %v", views, err)
	}
}
```

`writeFixtureBaseline` helper（同文件）：写 `{"meta":{},"containers":{},"nodes":{},"edges":[],"diffs":{}}` 到 `dir/codegraph/baseline.json`。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/codegraph/ -run TestLoad -v`
Expected: FAIL（包不存在 / 未定义）

- [ ] **Step 4: 实现 types.go**

```go
// Package codegraph 实现代码图数据契约的模型与算法：加载、校验、合并、查询、保鲜。
//
// 职责：
//   - 解析目标仓库 codegraph/baseline.json 与 codegraph/diffs/<view>.json
//   - 基准 + 差异合并出视图；BFS 邻域查询（chain / who-calls / 并集 / 深度）
//   - file:line 签名保鲜检测
//
// 边界：
//   - 不依赖 handoff 任何内部包（agentd/store/client）——数据契约独立是 spec
//     2026-08-19-codegraph-design §2 的硬约束，本包必须能原样搬进任何工具
//   - 不产出数据：扫描由 AI executor 完成（见 docs/codegraph-scan-recipe.md）
//   - 不做网络：一切输入都是本地文件
package codegraph

// Meta 是图的来源信息。
type Meta struct {
	Project   string `json:"project"`
	Branch    string `json:"branch"`
	Commit    string `json:"commit"`
	ScannedAt string `json:"scannedAt"`
	Generator string `json:"generator"`
}

// Container 是分组盒子（struct 一级，见 spec §3.1）。
type Container struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Entry bool   `json:"entry,omitempty"`
}

// TestRef 关联一个测试函数。File 形如 "pkg/x_test.go:41"。
type TestRef struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Snippet string `json:"snippet,omitempty"`
}

// Node 是图节点，Kind 三选一：entry / func / model。
// 不存源码——消费方按 File:Line 实时读取，这同时是保鲜检测的抓手。
type Node struct {
	Kind         string     `json:"kind"`
	Container    string     `json:"container"`
	Order        int        `json:"order,omitempty"`
	Name         string     `json:"name"`
	File         string     `json:"file"`
	Line         int        `json:"line"`
	Signature    string     `json:"signature,omitempty"`
	SignatureOld string     `json:"signatureOld,omitempty"` // 仅出现在 diff 的 nodesModified 里
	Params       [][]string `json:"params,omitempty"`       // [名, 类型, 说明]
	Returns      string     `json:"returns,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	Tests        []TestRef  `json:"tests,omitempty"`
	Fields       [][]string `json:"fields,omitempty"` // model 专用: [名, 类型, 说明]
	Unscanned    bool       `json:"unscanned,omitempty"`
}

// Edge 是一条调用关系 [caller, callee]。
type Edge [2]string

// Graph 是 codegraph/baseline.json 的顶层结构。
// 顶层 "diffs" 字段是早期原型的兼容残留，一期忽略：视图一律来自 diffs/ 目录。
type Graph struct {
	Meta       Meta                 `json:"meta"`
	Containers map[string]Container `json:"containers"`
	Nodes      map[string]Node      `json:"nodes"`
	Edges      []Edge               `json:"edges"`
}

// Diff 是 codegraph/diffs/<view>.json：某分支/plan 相对基准的差异声明。
type Diff struct {
	View          string          `json:"view"`
	Base          string          `json:"base,omitempty"`
	Summary       string          `json:"summary,omitempty"`
	NodesAdded    map[string]Node `json:"nodesAdded,omitempty"`
	NodesModified map[string]Node `json:"nodesModified,omitempty"`
	NodesDeleted  []string        `json:"nodesDeleted,omitempty"`
	EdgesAdded    []Edge          `json:"edgesAdded,omitempty"`
	EdgesDeleted  []Edge          `json:"edgesDeleted,omitempty"`
}
```

- [ ] **Step 5: 实现 load.go**

```go
// 本文件实现数据契约文件的加载：baseline、单个 diff、视图列表。
//
// 职责：读文件 + json.Unmarshal + 带路径上下文的错误
// 边界：不校验引用完整性（validate.go 的事）、不合并（merge.go 的事）
package codegraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadGraph 读取 repoRoot/codegraph/baseline.json。
// 文件不存在或 JSON 非法时返回带路径的错误——调用方（CLI/agentd）原文透出。
func LoadGraph(repoRoot string) (*Graph, error) {
	p := filepath.Join(repoRoot, "codegraph", "baseline.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读取基线 %s: %w", p, err)
	}
	var g Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("解析基线 %s: %w", p, err)
	}
	return &g, nil
}

// LoadDiff 读取 repoRoot/codegraph/diffs/<view>.json。view 是文件名（不含 .json）。
func LoadDiff(repoRoot, view string) (*Diff, error) {
	p := filepath.Join(repoRoot, "codegraph", "diffs", view+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读取视图 %s: %w", p, err)
	}
	var d Diff
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("解析视图 %s: %w", p, err)
	}
	return &d, nil
}

// ListViews 列出 diffs 目录下的视图名（文件名去 .json，字典序）。
// 目录不存在返回空列表——大多数仓库只有基线，这不是错误。
func ListViews(repoRoot string) ([]string, error) {
	dir := filepath.Join(repoRoot, "codegraph", "diffs")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("列视图目录 %s: %w", dir, err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(out)
	return out, nil
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/codegraph/ -v`
Expected: PASS（4 个用例）

- [ ] **Step 7: gofmt 检查后提交**

```bash
gofmt -l internal/codegraph/   # 必须零输出
git add internal/codegraph/
git commit -m "feat(codegraph): 数据契约模型与加载层"
```

---

### Task 2: internal/codegraph — 校验与合并

**Files:**
- Create: `internal/codegraph/validate.go`
- Create: `internal/codegraph/merge.go`
- Create: `internal/codegraph/validate_test.go`
- Create: `internal/codegraph/merge_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Graph/Diff/Node/Edge`。
- Produces: `Validate(g *Graph) []string`；`ValidateDiff(g *Graph, d *Diff) []string`；`type View / ViewNode / ViewEdge`；`Merge(g *Graph, d *Diff) *View`（d 为 nil = 纯基准视图）。

- [ ] **Step 1: 写失败测试** `validate_test.go`

```go
package codegraph

import (
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T) *Graph {
	t.Helper()
	g, err := LoadGraph(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestValidateCleanFixture(t *testing.T) {
	if issues := Validate(loadFixture(t)); len(issues) != 0 {
		t.Fatalf("夹具应当干净: %v", issues)
	}
}

func TestValidateCatchesBrokenRefs(t *testing.T) {
	g := loadFixture(t)
	n := g.Nodes["n_do"]
	n.Container = "k_ghost"
	g.Nodes["n_do"] = n
	g.Edges = append(g.Edges, Edge{"n_do", "n_ghost"})
	issues := Validate(g)
	if len(issues) != 2 {
		t.Fatalf("应报 2 条: %v", issues)
	}
	// 报文必须带引用者 id，否则修数据要靠猜
	if !strings.Contains(issues[0], "n_do") || !strings.Contains(issues[1], "n_ghost") {
		t.Fatalf("报文缺上下文: %v", issues)
	}
}

func TestValidateDiff(t *testing.T) {
	g := loadFixture(t)
	d, _ := LoadDiff(filepath.Join("testdata", "repo"), "branch-x")
	if issues := ValidateDiff(g, d); len(issues) != 0 {
		t.Fatalf("夹具 diff 应当干净: %v", issues)
	}
	d.NodesDeleted = append(d.NodesDeleted, "n_ghost") // 删除不存在的节点
	d.EdgesAdded = append(d.EdgesAdded, Edge{"n_audit", "n_ghost"})
	if issues := ValidateDiff(g, d); len(issues) != 2 {
		t.Fatalf("应报 2 条: %v", issues)
	}
}
```

`merge_test.go`：

```go
package codegraph

import (
	"path/filepath"
	"testing"
)

func TestMergeBaselineOnly(t *testing.T) {
	v := Merge(loadFixture(t), nil)
	if v.Name != "baseline" || len(v.Nodes) != 6 || len(v.Edges) != 4 {
		t.Fatalf("基准视图形状: %s %d %d", v.Name, len(v.Nodes), len(v.Edges))
	}
	if v.Nodes["n_do"].Status != "" {
		t.Fatal("基准视图不应有 status")
	}
}

func TestMergeWithDiff(t *testing.T) {
	g := loadFixture(t)
	d, _ := LoadDiff(filepath.Join("testdata", "repo"), "branch-x")
	v := Merge(g, d)
	if v.Name != "branch:x" {
		t.Fatalf("视图名取 diff.view: %s", v.Name)
	}
	if v.Nodes["n_audit"].Status != "added" || v.Nodes["n_do"].Status != "modified" ||
		v.Nodes["n_save"].Status != "deleted" {
		t.Fatalf("节点状态: %+v", v.Nodes)
	}
	// 修改后的节点内容替换为 diff 里的版本，且带 signatureOld
	if v.Nodes["n_do"].SignatureOld == "" || v.Nodes["n_do"].Signature ==
		g.Nodes["n_do"].Signature {
		t.Fatal("modified 节点应替换为新签名并携带旧签名")
	}
	// 删除的节点保留在视图里（status=deleted），供渲染红虚线，不是直接消失
	st := map[string]string{}
	for _, e := range v.Edges {
		st[e.From+"→"+e.To] = e.Status
	}
	if st["n_do→n_audit"] != "added" || st["n_do→n_save"] != "deleted" {
		t.Fatalf("边状态: %v", st)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/codegraph/ -run 'TestValidate|TestMerge' -v`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现 validate.go**

```go
// 本文件实现引用完整性校验：图与 diff 里的一切引用必须落在已定义的对象上。
//
// 职责：Validate（基线自查）、ValidateDiff（diff 相对基线自查）
// 边界：不查 file:line 真实性（stale.go 的事）、不修数据，只报告
package codegraph

import "fmt"

// Validate 检查基线的引用完整性，返回问题列表（空 = 干净）。
// 检查项：节点的 container 必须存在；每条边两端必须是已定义节点。
func Validate(g *Graph) []string {
	var issues []string
	for id, n := range g.Nodes {
		if _, ok := g.Containers[n.Container]; !ok {
			issues = append(issues, fmt.Sprintf("节点 %s 引用不存在的容器 %s", id, n.Container))
		}
	}
	for _, e := range g.Edges {
		for _, end := range e {
			if _, ok := g.Nodes[end]; !ok {
				issues = append(issues, fmt.Sprintf("边 %s→%s 引用不存在的节点 %s", e[0], e[1], end))
			}
		}
	}
	sortStrings(issues) // 确定性输出，测试与 CI 可比对
	return issues
}

// ValidateDiff 检查 diff 相对基线的引用完整性。
// 检查项：nodesModified/nodesDeleted 引用的节点必须在基线里；
// edgesAdded/edgesDeleted 两端必须在「基线 ∪ nodesAdded」里；
// nodesAdded 的 container 必须存在。
func ValidateDiff(g *Graph, d *Diff) []string {
	var issues []string
	known := func(id string) bool {
		if _, ok := g.Nodes[id]; ok {
			return true
		}
		_, ok := d.NodesAdded[id]
		return ok
	}
	for id, n := range d.NodesAdded {
		if _, ok := g.Containers[n.Container]; !ok {
			issues = append(issues, fmt.Sprintf("新增节点 %s 引用不存在的容器 %s", id, n.Container))
		}
	}
	for id := range d.NodesModified {
		if _, ok := g.Nodes[id]; !ok {
			issues = append(issues, fmt.Sprintf("修改的节点 %s 不在基线里", id))
		}
	}
	for _, id := range d.NodesDeleted {
		if _, ok := g.Nodes[id]; !ok {
			issues = append(issues, fmt.Sprintf("删除的节点 %s 不在基线里", id))
		}
	}
	for _, e := range append(append([]Edge{}, d.EdgesAdded...), d.EdgesDeleted...) {
		for _, end := range e {
			if !known(end) {
				issues = append(issues, fmt.Sprintf("diff 边 %s→%s 引用未知节点 %s", e[0], e[1], end))
			}
		}
	}
	sortStrings(issues)
	return issues
}
```

（`sortStrings` = `sort.Strings` 的包装，放 validate.go 底部；或直接 `sort.Strings(issues)` 两处内联。）

- [ ] **Step 4: 实现 merge.go**

```go
// 本文件实现「基准 + 差异 → 视图」的合并（spec §3.2 的渲染时合并）。
//
// 职责：Merge 产出带 Status 标记的 View；删除的对象保留并标 deleted，
//       供消费方画红虚线——直接剔除会让"删了什么"不可见
// 边界：不做查询（query.go）；diff 的合法性由 ValidateDiff 把关，
//       Merge 对非法引用宽容跳过（渲染路径不因脏数据崩）
package codegraph

// ViewNode 是视图里的节点：Node + 差异状态。
type ViewNode struct {
	Node
	Status string `json:"status,omitempty"` // "" | added | modified | deleted
}

// ViewEdge 是视图里的边。
type ViewEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status,omitempty"`
}

// View 是合并后的图视图。
type View struct {
	Name       string               `json:"view"`
	Containers map[string]Container `json:"containers"`
	Nodes      map[string]ViewNode  `json:"nodes"`
	Edges      []ViewEdge           `json:"edges"`
}

// Merge 把基线与一个 diff 合并成视图。d 为 nil 时返回纯基准视图（Name="baseline"）。
func Merge(g *Graph, d *Diff) *View {
	v := &View{Name: "baseline", Containers: g.Containers,
		Nodes: make(map[string]ViewNode, len(g.Nodes))}
	for id, n := range g.Nodes {
		v.Nodes[id] = ViewNode{Node: n}
	}
	for _, e := range g.Edges {
		v.Edges = append(v.Edges, ViewEdge{From: e[0], To: e[1]})
	}
	if d == nil {
		return v
	}
	v.Name = d.View
	for id, n := range d.NodesAdded {
		v.Nodes[id] = ViewNode{Node: n, Status: "added"}
	}
	for id, n := range d.NodesModified {
		if _, ok := v.Nodes[id]; ok {
			v.Nodes[id] = ViewNode{Node: n, Status: "modified"}
		}
	}
	for _, id := range d.NodesDeleted {
		if vn, ok := v.Nodes[id]; ok {
			vn.Status = "deleted"
			v.Nodes[id] = vn
		}
	}
	del := map[string]bool{}
	for _, e := range d.EdgesDeleted {
		del[e[0]+"\x00"+e[1]] = true
	}
	for i := range v.Edges {
		if del[v.Edges[i].From+"\x00"+v.Edges[i].To] {
			v.Edges[i].Status = "deleted"
		}
	}
	for _, e := range d.EdgesAdded {
		v.Edges = append(v.Edges, ViewEdge{From: e[0], To: e[1], Status: "added"})
	}
	return v
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/codegraph/ -v`
Expected: PASS

- [ ] **Step 6: gofmt + 提交**

```bash
gofmt -l internal/codegraph/
git add internal/codegraph/
git commit -m "feat(codegraph): 引用校验与基准+差异合并"
```

---

### Task 3: internal/codegraph — 邻域查询（chain / who-calls / 并集 / 深度）

**Files:**
- Create: `internal/codegraph/query.go`
- Create: `internal/codegraph/query_test.go`

**Interfaces:**
- Consumes: Task 2 的 `View/ViewNode/ViewEdge`。
- Produces:
  - `Resolve(v *View, arg string) (string, error)` —— id 或精确 name 解析成节点 id
  - `Neighborhood(v *View, foci []string, down, up int) (*Result, error)` —— down/up 为各方向深度（0 = 该方向不查，-1 = 不限）
  - `type Result { View string; Foci []string; Nodes []ResultNode; Edges []ViewEdge; UnscannedEntries int; Warning string }`
  - `type ResultNode { ID string; Dist int; ViewNode }`

- [ ] **Step 1: 写失败测试** `query_test.go`

```go
package codegraph

import (
	"path/filepath"
	"strings"
	"testing"
)

func fixtureView(t *testing.T) *View {
	t.Helper()
	return Merge(loadFixture(t), nil)
}

func TestResolve(t *testing.T) {
	v := fixtureView(t)
	if id, err := Resolve(v, "n_do"); err != nil || id != "n_do" {
		t.Fatalf("按 id: %s %v", id, err)
	}
	if id, err := Resolve(v, "Server.Do"); err != nil || id != "n_do" {
		t.Fatalf("按名字: %s %v", id, err)
	}
	if _, err := Resolve(v, "NoSuch"); err == nil ||
		!strings.Contains(err.Error(), "NoSuch") {
		t.Fatalf("未命中要带原词报错: %v", err)
	}
}

func TestNeighborhoodChain(t *testing.T) {
	v := fixtureView(t)
	// e_run 下游不限深：run→runE→do→save→task 共 5 节点
	r, err := Neighborhood(v, []string{"e_run"}, -1, 0)
	if err != nil || len(r.Nodes) != 5 {
		t.Fatalf("全链: %d %v", len(r.Nodes), err)
	}
	// 深度 1：只有 e_run 和 n_runE
	r, _ = Neighborhood(v, []string{"e_run"}, 1, 0)
	if len(r.Nodes) != 2 {
		t.Fatalf("深度 1: %d", len(r.Nodes))
	}
	// dist 排序确定：焦点在前
	if r.Nodes[0].ID != "e_run" || r.Nodes[0].Dist != 0 || r.Nodes[1].Dist != 1 {
		t.Fatalf("排序: %+v", r.Nodes)
	}
}

func TestNeighborhoodWhoCallsUnion(t *testing.T) {
	v := fixtureView(t)
	// save 与 do 的上游并集：do 的上游 runE/e_run，save 的上游 do/runE/e_run
	r, err := Neighborhood(v, []string{"n_save", "n_do"}, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int{}
	for _, n := range r.Nodes {
		ids[n.ID] = n.Dist
	}
	if len(ids) != 4 || ids["n_save"] != 0 || ids["n_do"] != 0 || ids["e_run"] >= 0 {
		t.Fatalf("并集: %v", ids)
	}
	// 夹具有 1 个未扫描入口 → 必须透出（"无上游"≠"没扫过"）
	if r.UnscannedEntries != 1 || r.Warning == "" {
		t.Fatalf("未扫描警示缺失: %d %q", r.UnscannedEntries, r.Warning)
	}
}

func TestNeighborhoodSkipsDeleted(t *testing.T) {
	g := loadFixture(t)
	d, _ := LoadDiff(filepath.Join("testdata", "repo"), "branch-x")
	v := Merge(g, d)
	// branch-x 删了 n_save 与 do→save 边：e_run 全链走 audit 不走 save
	r, _ := Neighborhood(v, []string{"e_run"}, -1, 0)
	for _, n := range r.Nodes {
		if n.ID == "n_save" {
			t.Fatal("deleted 节点不应被遍历到")
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/codegraph/ -run 'TestResolve|TestNeighborhood' -v`
Expected: FAIL

- [ ] **Step 3: 实现 query.go**

```go
// 本文件实现视图上的邻域查询：多源 BFS，下游为正距离、上游为负距离。
//
// 职责：Resolve（id/名字 → 节点 id）、Neighborhood（chain/who-calls/并集共用核心）
// 边界：不做布局（消费方的事）；deleted 节点/边不参与遍历（它们只是渲染残影）
package codegraph

import (
	"fmt"
	"sort"
	"strings"
)

// ResultNode 是查询结果里的节点：id + 与焦点的 BFS 距离（下游正、上游负）。
type ResultNode struct {
	ID   string `json:"id"`
	Dist int    `json:"dist"`
	ViewNode
}

// Result 是一次邻域查询的完整结果。
// UnscannedEntries/Warning 让消费方能区分「查询无结果」与「根本没扫」——
// 这是 spec §6 的硬要求，agent 拿掉这个信息会写出漏影响面的 plan。
type Result struct {
	View             string       `json:"view"`
	Foci             []string     `json:"foci"`
	Nodes            []ResultNode `json:"nodes"`
	Edges            []ViewEdge   `json:"edges"`
	UnscannedEntries int          `json:"unscannedEntries"`
	Warning          string       `json:"warning,omitempty"`
}

// Resolve 把命令行参数解析成节点 id：先按 id 精确匹配，再按 name 精确匹配。
// name 多义或未命中时报错并列出近似候选（contains，最多 5 个），方便 agent 自纠。
func Resolve(v *View, arg string) (string, error) {
	if _, ok := v.Nodes[arg]; ok {
		return arg, nil
	}
	var exact, fuzzy []string
	for id, n := range v.Nodes {
		if n.Name == arg {
			exact = append(exact, id)
		} else if strings.Contains(strings.ToLower(n.Name), strings.ToLower(arg)) {
			fuzzy = append(fuzzy, id+"("+n.Name+")")
		}
	}
	sort.Strings(exact)
	sort.Strings(fuzzy)
	switch len(exact) {
	case 1:
		return exact[0], nil
	case 0:
		if len(fuzzy) > 5 {
			fuzzy = fuzzy[:5]
		}
		return "", fmt.Errorf("节点 %q 不在图中；近似候选: %s", arg, strings.Join(fuzzy, ", "))
	default:
		return "", fmt.Errorf("名字 %q 多义，请用节点 id: %s", arg, strings.Join(exact, ", "))
	}
}

// Neighborhood 从焦点集合做多源 BFS。down/up 是两个方向各自的最大深度：
// 0 = 该方向不查，-1 = 不限。deleted 节点与边不参与遍历。
func Neighborhood(v *View, foci []string, down, up int) (*Result, error) {
	adj, radj := map[string][]string{}, map[string][]string{}
	for _, e := range v.Edges {
		if e.Status == "deleted" ||
			v.Nodes[e.From].Status == "deleted" || v.Nodes[e.To].Status == "deleted" {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		radj[e.To] = append(radj[e.To], e.From)
	}
	dist := map[string]int{}
	for _, f := range foci {
		n, ok := v.Nodes[f]
		if !ok {
			return nil, fmt.Errorf("焦点 %s 不在视图中", f)
		}
		if n.Status == "deleted" {
			return nil, fmt.Errorf("焦点 %s 在该视图中已被删除", f)
		}
		dist[f] = 0
	}
	bfs := func(next map[string][]string, step, limit int) {
		frontier := append([]string{}, foci...)
		for len(frontier) > 0 {
			var nx []string
			for _, id := range frontier {
				d := dist[id] + step
				if limit >= 0 && abs(d) > limit {
					continue
				}
				for _, t := range next[id] {
					if _, seen := dist[t]; !seen {
						dist[t] = d
						nx = append(nx, t)
					}
				}
			}
			frontier = nx
		}
	}
	if down != 0 {
		bfs(adj, 1, down)
	}
	if up != 0 {
		bfs(radj, -1, up)
	}

	r := &Result{View: v.Name, Foci: append([]string{}, foci...)}
	for id, d := range dist {
		r.Nodes = append(r.Nodes, ResultNode{ID: id, Dist: d, ViewNode: v.Nodes[id]})
	}
	sort.Slice(r.Nodes, func(i, j int) bool {
		if r.Nodes[i].Dist != r.Nodes[j].Dist {
			return r.Nodes[i].Dist < r.Nodes[j].Dist
		}
		return r.Nodes[i].ID < r.Nodes[j].ID
	})
	for _, e := range v.Edges {
		if _, a := dist[e.From]; a {
			if _, b := dist[e.To]; b {
				r.Edges = append(r.Edges, e)
			}
		}
	}
	for _, n := range v.Nodes {
		if n.Kind == "entry" && n.Unscanned && n.Status != "deleted" {
			r.UnscannedEntries++
		}
	}
	if r.UnscannedEntries > 0 {
		r.Warning = fmt.Sprintf("基线仍有 %d 个未扫描入口：查询结果为空不等于没有调用方", r.UnscannedEntries)
	}
	return r, nil
}

// abs 是 int 绝对值（标准库到 1.21 仍无泛型版，自备）。
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/codegraph/ -v`
Expected: PASS

- [ ] **Step 5: gofmt + 提交**

```bash
gofmt -l internal/codegraph/
git add internal/codegraph/
git commit -m "feat(codegraph): 多源 BFS 邻域查询（chain/who-calls/并集/深度）"
```

---

### Task 4: internal/codegraph — 保鲜检测（stale）

**Files:**
- Create: `internal/codegraph/stale.go`
- Create: `internal/codegraph/stale_test.go`
- Create: `internal/codegraph/testdata/repo/cmd/run.go`、`testdata/repo/svc/server.go`、`testdata/repo/svc/task.go`（假源码，行号与夹具 baseline 对齐）

**Interfaces:**
- Consumes: Task 1 的 `Graph/Node`。
- Produces: `type StaleNode { ID, File string; Line int; Reason string }`；`CheckStale(repoRoot string, g *Graph) []StaleNode`。

- [ ] **Step 1: 写假源码夹具（行号必须与 baseline 夹具一致）**

`testdata/repo/cmd/run.go`（e_run line 3、n_runE line 5）：

```go
package cmd

// demo run 命令注册处
// 占位
func runE() error { return nil }
```

`testdata/repo/svc/server.go`（n_do line 4、n_save line 9）：

```go
package svc

// Do 干活
func (s *Server) Do() error {
	return nil
}

// Save 落库
func (s *Server) Save() error { return nil }
```

`testdata/repo/svc/task.go`（m_task line 2）：

```go
package svc
type Task struct{ ID string }
```

注意：`testdata/repo/cmd/skip.go` **故意不建**——e_skip 是 unscanned 入口，测试它被跳过。

- [ ] **Step 2: 写失败测试** `stale_test.go`

```go
package codegraph

import (
	"path/filepath"
	"testing"
)

func TestCheckStaleCleanFixture(t *testing.T) {
	g := loadFixture(t)
	stale := CheckStale(filepath.Join("testdata", "repo"), g)
	// unscanned 的 e_skip 没有源文件但必须被跳过——夹具整体干净
	if len(stale) != 0 {
		t.Fatalf("夹具应当不 stale: %+v", stale)
	}
}

func TestCheckStaleDetects(t *testing.T) {
	g := loadFixture(t)
	// 场景 1：行号越界
	n := g.Nodes["n_do"]
	n.Line = 999
	g.Nodes["n_do"] = n
	// 场景 2：文件不存在
	n2 := g.Nodes["n_save"]
	n2.File = "svc/gone.go"
	g.Nodes["n_save"] = n2
	// 场景 3：行内容对不上（把 runE 指到注释行）
	n3 := g.Nodes["n_runE"]
	n3.Line = 2
	g.Nodes["n_runE"] = n3
	stale := CheckStale(filepath.Join("testdata", "repo"), g)
	if len(stale) != 3 {
		t.Fatalf("应报 3 条: %+v", stale)
	}
	reasons := map[string]string{}
	for _, s := range stale {
		reasons[s.ID] = s.Reason
	}
	if reasons["n_do"] == "" || reasons["n_save"] == "" || reasons["n_runE"] == "" {
		t.Fatalf("每条都要带原因: %v", reasons)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/codegraph/ -run TestCheckStale -v`
Expected: FAIL

- [ ] **Step 4: 实现 stale.go**

```go
// 本文件实现保鲜检测：节点声称的 file:line 与真实源码对不上即 stale。
//
// 为什么这么设计（spec §7）：过期的图比没有图更糟——agent 信了它就省了验证。
// 节点刻意不存源码正文，file:line 是唯一锚点，所以校验它就是校验图的新鲜度。
//
// 职责：CheckStale——按廉价规则逐节点比对
// 边界：不重扫、不修复，只报告；unscanned 节点跳过（没人声称它是新鲜的）
package codegraph

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StaleNode 描述一个失鲜节点及原因。
type StaleNode struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// CheckStale 逐节点做三级廉价校验：文件存在 → 行号在界内 →
// 行窗口（line-1..line+1）里能找到名字 token。entry 只做前两级
// （注册行长相多样，token 检查会假红）；func/model 检查 token：
// func 取 Name 最后一个 '.' 之后的段（"Client.Dispatch" → "Dispatch"），
// model 取整名。文件按缓存读，同文件多节点只读一次。
func CheckStale(repoRoot string, g *Graph) []StaleNode {
	cache := map[string][]string{}
	readLines := func(rel string) ([]string, bool) {
		if ls, ok := cache[rel]; ok {
			return ls, ls != nil
		}
		raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			cache[rel] = nil
			return nil, false
		}
		ls := strings.Split(string(raw), "\n")
		cache[rel] = ls
		return ls, true
	}
	var out []StaleNode
	for id, n := range g.Nodes {
		if n.Unscanned || n.File == "" {
			continue
		}
		lines, ok := readLines(n.File)
		if !ok {
			out = append(out, StaleNode{id, n.File, n.Line, "文件不存在"})
			continue
		}
		if n.Line < 1 || n.Line > len(lines) {
			out = append(out, StaleNode{id, n.File, n.Line, "行号越界"})
			continue
		}
		if n.Kind == "entry" {
			continue
		}
		token := n.Name
		if i := strings.LastIndex(token, "."); i >= 0 {
			token = token[i+1:]
		}
		lo, hi := n.Line-2, n.Line+1 // 0 基窗口 [line-1-1, line+1)
		if lo < 0 {
			lo = 0
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		if !strings.Contains(strings.Join(lines[lo:hi], "\n"), token) {
			out = append(out, StaleNode{id, n.File, n.Line, "行内容与名字对不上（疑似代码已移动）"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/codegraph/ -v`
Expected: PASS（全包）

- [ ] **Step 6: gofmt + 提交**

```bash
gofmt -l internal/codegraph/
git add internal/codegraph/
git commit -m "feat(codegraph): file:line 保鲜检测"
```

---

### Task 5: CLI `handoff graph`

**Files:**
- Create: `cmd/graph.go`
- Create: `cmd/graph_test.go`

**Interfaces:**
- Consumes: Task 1-4 的 `LoadGraph/LoadDiff/ListViews/Validate/ValidateDiff/Merge/Resolve/Neighborhood/CheckStale`。
- Produces: 子命令 `handoff graph validate|views|chain|who-calls`，全部输出 JSON 到 stdout；flags `--repo`（默认 "."）、`--depth`（默认 2，0 = 不限）、`--view`、`--stale`。

- [ ] **Step 1: 写失败测试** `cmd/graph_test.go`

测试直接对着 `internal/codegraph/testdata/repo` 夹具跑（相对路径 `../internal/codegraph/testdata/repo`）。仓库既有 cmd 测试用 `runSubcommandForTest` 类 helper（见 `cmd/machines_test.go`）；若该 helper 不适配（graph 不发网络请求），就地用 cobra 执行：

```go
// graph 命令测试：validate/chain/who-calls 的 JSON 契约与退出语义。
package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
)

// runGraph 执行 handoff graph <args...>，返回 stdout 与 err。
func runGraph(t *testing.T, args ...string) (string, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(append([]string{"graph"}, args...))
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	return buf.String(), err
}

const fixtureRepo = "../internal/codegraph/testdata/repo"

func TestGraphValidate(t *testing.T) {
	out, err := runGraph(t, "validate", "--repo", fixtureRepo)
	if err != nil {
		t.Fatalf("validate 应通过: %v\n%s", err, out)
	}
	var r map[string]any
	if json.Unmarshal([]byte(out), &r) != nil ||
		r["nodes"].(float64) != 6 || r["unscannedEntries"].(float64) != 1 {
		t.Fatalf("统计 JSON 形状: %s", out)
	}
}

func TestGraphChainDefaultDepth(t *testing.T) {
	out, err := runGraph(t, "chain", "e_run", "--repo", fixtureRepo)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Nodes   []map[string]any `json:"nodes"`
		Warning string           `json:"warning"`
	}
	if json.Unmarshal([]byte(out), &r) != nil {
		t.Fatalf("非法 JSON: %s", out)
	}
	// 默认深度 2：e_run + runE + do
	if len(r.Nodes) != 3 || r.Warning == "" {
		t.Fatalf("默认深度/警示: %d %q", len(r.Nodes), r.Warning)
	}
}

func TestGraphWhoCallsUnionByName(t *testing.T) {
	// 按名字解析 + 多参数并集 + --depth 0 不限
	out, err := runGraph(t, "who-calls", "Server.Save", "Server.Do", "--depth", "0", "--repo", fixtureRepo)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Foci  []string         `json:"foci"`
		Nodes []map[string]any `json:"nodes"`
	}
	json.Unmarshal([]byte(out), &r)
	if len(r.Foci) != 2 || len(r.Nodes) != 4 {
		t.Fatalf("并集: foci=%v nodes=%d", r.Foci, len(r.Nodes))
	}
}

func TestGraphChainWithView(t *testing.T) {
	out, err := runGraph(t, "chain", "e_run", "--depth", "0", "--view", "branch-x", "--repo", fixtureRepo)
	if err != nil {
		t.Fatal(err)
	}
	// branch-x 视图里链路走 audit 不走 save
	if !bytes.Contains([]byte(out), []byte("n_audit")) || bytes.Contains([]byte(out), []byte("n_save")) {
		t.Fatalf("视图叠加没生效: %s", out)
	}
}

func TestGraphResolveErrorListsCandidates(t *testing.T) {
	out, err := runGraph(t, "chain", "Do", "--repo", fixtureRepo)
	if err == nil {
		t.Fatalf("模糊名应报错: %s", out)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("Server.Do")) {
		t.Fatalf("报错要带候选: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestGraph -v`
Expected: FAIL（graph 子命令不存在）

- [ ] **Step 3: 实现 cmd/graph.go**

```go
// 本文件实现 handoff graph 子命令族：对仓库内代码图数据的本地只读查询。
//
// 职责：
//   - graph validate: 引用完整性 + 可选 --stale 保鲜检查，供 CI 与扫描后自检
//   - graph views:    列出可用视图（diffs 目录）
//   - graph chain:    焦点（可多个，并集）的下游调用链
//   - graph who-calls: 焦点（可多个，并集）的上游调用方——影响面查询
//
// 边界：
//   - 只读 --repo 指向的本地文件，不发任何网络请求、不依赖 agentd 存活
//     ——spec 2026-08-19-codegraph-design §2/§6 的硬约束，agent 离线可用
//   - 不产出/修改图数据（扫描配方见 docs/codegraph-scan-recipe.md）
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/Xsxdot/handoff/internal/codegraph"
	"github.com/spf13/cobra"
)

var (
	graphRepo  string
	graphDepth int
	graphView  string
	graphStale bool
)

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "查询仓库内的代码图（codegraph/*.json，本地只读）",
}

// graphLoadView 加载基线并按 --view 叠加 diff，返回合并视图。
func graphLoadView() (*codegraph.View, *codegraph.Graph, error) {
	g, err := codegraph.LoadGraph(graphRepo)
	if err != nil {
		return nil, nil, err
	}
	var d *codegraph.Diff
	if graphView != "" {
		if d, err = codegraph.LoadDiff(graphRepo, graphView); err != nil {
			return nil, nil, err
		}
		if issues := codegraph.ValidateDiff(g, d); len(issues) > 0 {
			return nil, nil, fmt.Errorf("视图 %s 引用不完整: %v", graphView, issues)
		}
	}
	return codegraph.Merge(g, d), g, nil
}

// graphPrintJSON 把结果编码到 stdout（缩进 JSON，agent 与人都可读）。
func graphPrintJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

var graphValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "校验基线与全部视图的引用完整性（--stale 加保鲜检查），问题即非零退出",
	RunE: func(cmd *cobra.Command, args []string) error {
		g, err := codegraph.LoadGraph(graphRepo)
		if err != nil {
			return err
		}
		issues := codegraph.Validate(g)
		views, err := codegraph.ListViews(graphRepo)
		if err != nil {
			return err
		}
		for _, name := range views {
			d, err := codegraph.LoadDiff(graphRepo, name)
			if err != nil {
				return err
			}
			for _, is := range codegraph.ValidateDiff(g, d) {
				issues = append(issues, "["+name+"] "+is)
			}
		}
		var stale []codegraph.StaleNode
		if graphStale {
			stale = codegraph.CheckStale(graphRepo, g)
		}
		unscanned := 0
		for _, n := range g.Nodes {
			if n.Kind == "entry" && n.Unscanned {
				unscanned++
			}
		}
		out := map[string]any{
			"nodes": len(g.Nodes), "edges": len(g.Edges),
			"containers": len(g.Containers), "views": views,
			"unscannedEntries": unscanned, "issues": issues,
		}
		if graphStale {
			out["stale"] = stale
		}
		if err := graphPrintJSON(cmd, out); err != nil {
			return err
		}
		if len(issues) > 0 || len(stale) > 0 {
			return fmt.Errorf("发现 %d 个完整性问题、%d 个失鲜节点", len(issues), len(stale))
		}
		return nil
	},
}

var graphViewsCmd = &cobra.Command{
	Use:   "views",
	Short: "列出可用视图（codegraph/diffs/ 下的文件名）",
	RunE: func(cmd *cobra.Command, args []string) error {
		views, err := codegraph.ListViews(graphRepo)
		if err != nil {
			return err
		}
		if views == nil {
			views = []string{}
		}
		return graphPrintJSON(cmd, map[string]any{"views": views})
	},
}

// graphQueryRunE 是 chain 与 who-calls 的共用主体：解析焦点 → 邻域查询 → 输出。
func graphQueryRunE(down, up bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		v, g, err := graphLoadView()
		if err != nil {
			return err
		}
		foci := make([]string, 0, len(args))
		for _, a := range args {
			id, err := codegraph.Resolve(v, a)
			if err != nil {
				return err
			}
			foci = append(foci, id)
		}
		limit := graphDepth
		if limit == 0 {
			limit = -1 // CLI 语义：0 = 不限 → 核心语义 -1
		}
		dn, upn := 0, 0
		if down {
			dn = limit
		}
		if up {
			upn = limit
		}
		r, err := codegraph.Neighborhood(v, foci, dn, upn)
		if err != nil {
			return err
		}
		out := map[string]any{"result": r, "depth": graphDepth}
		if graphStale {
			out["stale"] = codegraph.CheckStale(graphRepo, g)
		}
		return graphPrintJSON(cmd, out)
	}
}

var graphChainCmd = &cobra.Command{
	Use:   "chain <节点 id 或名字>...",
	Short: "焦点的下游调用链（多个焦点取并集）",
	Args:  cobra.MinimumNArgs(1),
	RunE:  graphQueryRunE(true, false),
}

var graphWhoCallsCmd = &cobra.Command{
	Use:   "who-calls <节点 id 或名字>...",
	Short: "谁调用了焦点——上游影响面（多个焦点取并集）",
	Args:  cobra.MinimumNArgs(1),
	RunE:  graphQueryRunE(false, true),
}

func init() {
	graphCmd.PersistentFlags().StringVar(&graphRepo, "repo", ".", "目标仓库根目录")
	graphCmd.PersistentFlags().IntVar(&graphDepth, "depth", 2, "查询深度（0 = 不限）")
	graphCmd.PersistentFlags().StringVar(&graphView, "view", "", "叠加的视图名（codegraph/diffs/<名>.json）")
	graphCmd.PersistentFlags().BoolVar(&graphStale, "stale", false, "附带保鲜检测结果")
	graphCmd.AddCommand(graphValidateCmd, graphViewsCmd, graphChainCmd, graphWhoCallsCmd)
	rootCmd.AddCommand(graphCmd)
}
```

注意：cmd 包测试可能残留包级 flag 状态（仓库已知模式，见 `resetW3aFlags`）——若跨用例互扰，在 `runGraph` 里先重置四个 `graph*` 包级变量为默认值。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestGraph -v`
Expected: PASS

- [ ] **Step 5: 真实数据冒烟（本仓库自身的基线）**

```bash
go run . graph validate --repo .
go run . graph chain "handoff continue" --repo . --depth 0
go run . graph who-calls Server.handleDispatch --repo .
```

Expected: validate 输出 nodes=189/edges=132、issues 空、退出 0；chain 输出 continue 全链；who-calls 输出上游含 CLI 入口层。**若 validate 报 stale/issues，如实记进 ledger，不许改判据**。

- [ ] **Step 6: gofmt + 提交**

```bash
gofmt -l cmd/
git add cmd/graph.go cmd/graph_test.go
git commit -m "feat(cli): handoff graph 子命令——validate/views/chain/who-calls 本地只读查询"
```

---

### Task 6: 扫描配方文档

**Files:**
- Create: `docs/codegraph-scan-recipe.md`

**Interfaces:**
- Consumes: 无（纯文档）。
- Produces: 可直接 `handoff dispatch` 派发的扫描 plan 模板，schema 与 Task 1 类型逐字段一致。

- [ ] **Step 1: 写文档**

内容骨架（按此展开成完整文档，schema 部分从 spec §3.1 与 Task 1 的 types.go 抄字段，不许自创字段）：

```markdown
# 代码图扫描配方（派发给 AI executor 的 plan 模板）

## 用法
1. 复制本文档为一次性 plan，替换 <项目名>；
2. `handoff dispatch --target <机器> --new-worktree --new-branch codegraph-scan-<日期> --executor codex <plan 文件>`；
3. 回来后 `handoff graph validate --repo .` 通过才算扫描合格。

## 产物
仅新增/更新 `codegraph/baseline.json`（全量重扫）或 `codegraph/diffs/<视图名>.json`（分支增量），
不改任何源码文件。

## Schema（必须严格遵守）
[逐字段列出 meta/containers/nodes/edges 与 diff 的字段表 —— 与 internal/codegraph/types.go 一致]

## 硬纪律（历次扫描验证过的坑）
- 容器按 struct 一级：Go 方法按 receiver 归 `pkg.Receiver` 容器，
  自由函数归 `pkg（包级函数）`，model 归 `pkg 实体`；入口分 CLI/HTTP/WS 三容器。
- 所有入口必须全量盘点；没追链的标 `"unscanned": true`——宁缺毋滥。
- file/line/signature 必须与真实代码一致（line 指函数定义行）。
- tests 找同包 *_test.go 里直接测到该函数的；找不到就 []，不编造。
- 链路追到导出方法级；承重的未导出函数（如 RunE 主函数）也入图；纯工具小函数不入。
- 收尾自检：`python3 -m json.tool` 合法性 + 引用完整性脚本（或直接
  `handoff graph validate --repo .`），并抽查 5 个节点的 file:line。
```

- [ ] **Step 2: 提交**

```bash
git add docs/codegraph-scan-recipe.md
git commit -m "docs(codegraph): 扫描配方模板——schema 契约与硬纪律"
```

---

### Task 7: agentd 只读端点

**Files:**
- Create: `internal/agentd/codegraph.go`
- Create: `internal/agentd/codegraph_test.go`
- Modify: `internal/agentd/server.go`（Handler() 的路由表，`GET /api/projects/{name}/branches` 附近加两行）

**Interfaces:**
- Consumes: `codegraph.LoadGraph/ListViews/LoadDiff/CheckStale`；`s.st.GetProjectLocationByName`（既有）；`writeJSON`/`truncateRunes`/`s.forwardIfRequested`（既有）。
- Produces:
  - `GET /api/projects/{name}/codegraph[?machine=]` → 200 `{"baseline": Graph, "views": {视图名: Diff}, "stale": [StaleNode]}`；项目不存在 404；无 codegraph/baseline.json 404（报文说明「项目未生成代码图」）；其他 500。
  - `GET /api/projects/{name}/codegraph/source?file=&line=&span=[&machine=]` → 200 `{"file": string, "from": int, "lines": []string}`（from 是 1 基起始行；span 默认 40，上限 200）；路径逃逸 400。

- [ ] **Step 1: 写失败测试** `internal/agentd/codegraph_test.go`

沿用包内既有测试环境模式（参考 `bundle_handler_test.go` 的 `env` 构造与 `projectadmin` 系测试如何登记项目位置）。要点用例：

```go
// codegraph 端点测试：取图、视图叠加数据、源码读取与路径逃逸拒绝。
package agentd

// TestCodegraphEndpoint:
//   1. 建临时项目目录，写入 Task 1 夹具同款 baseline.json + diffs/branch-x.json
//      与 svc/server.go 假源码；登记 project location（名 "demo"）
//   2. GET /api/projects/demo/codegraph → 200；解析 body：
//      baseline.nodes 有 6 个；views 含 "branch-x" 的完整 Diff；stale 为空数组
//   3. GET /api/projects/ghost/codegraph → 404
//   4. 项目目录里删掉 codegraph/ → GET → 404 且报文含「未生成代码图」
//
// TestCodegraphSource:
//   1. GET …/codegraph/source?file=svc/server.go&line=4&span=3 → 200，
//      lines[0] 是第 4 行原文（from=4 起 3 行，窗口按 line 居中前移见实现注释）
//   2. file=../../etc/passwd → 400
//   3. file=/etc/passwd（绝对路径）→ 400
//   4. line 越界 → 200 且 lines 截到文件末尾（读文件看上下文不该因行号偏了就失败）
```

（测试代码按该包既有 env/httptest 模式落实；写不出 env 时降级为直接 `httptest.NewServer(s.Handler())` + 预置 store。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestCodegraph -v`
Expected: FAIL

- [ ] **Step 3: 实现 internal/agentd/codegraph.go**

```go
// 本文件实现代码图的两个只读端点：整图数据与按 file:line 的源码窗口。
//
// 职责：
//   - handleProjectCodegraph: 一次性返回基线 + 全部视图 diff + 保鲜报告，
//     合并渲染在前端做（数据契约见 spec 2026-08-19-codegraph-design §3）
//   - handleProjectCodegraphSource: 详情面板「源码」区按 file:line 实时读
//
// 边界：
//   - 只读，不触发扫描、不写任何文件
//   - source 的路径校验是参数校验不是安全边界（同 workspacefiles.go 的论证）：
//     挡打错的路径，不防有心人——控制台会话本就等价主令牌
package agentd

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Xsxdot/handoff/internal/codegraph"
	"github.com/Xsxdot/handoff/internal/store"
	"errors"
)

// handleProjectCodegraph 处理 GET /api/projects/{name}/codegraph[?machine=]。
func (s *Server) handleProjectCodegraph(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.PathValue("name")
	s.log.Info("代码图请求", "name", name)
	loc, err := s.st.GetProjectLocationByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("代码图被拒：项目不存在", "name", name)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
			return
		}
		s.log.Error("代码图失败：查询位置表", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	g, err := codegraph.LoadGraph(loc.Path)
	if err != nil {
		if os.IsNotExist(errors.Unwrap(err)) || strings.Contains(err.Error(), "no such file") {
			s.log.Warn("代码图缺失", "name", name, "repo", loc.Path)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未生成代码图（无 codegraph/baseline.json）"})
			return
		}
		s.log.Error("代码图加载失败", "name", name, "repo", loc.Path, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	names, err := codegraph.ListViews(loc.Path)
	if err != nil {
		s.log.Error("代码图列视图失败", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	views := map[string]*codegraph.Diff{}
	for _, vn := range names {
		d, err := codegraph.LoadDiff(loc.Path, vn)
		if err != nil {
			// 单个坏视图不拖垮整页：跳过并告警，前端照常渲染其余视图
			s.log.Warn("代码图视图解析失败，跳过", "name", name, "view", vn, "cause", err)
			continue
		}
		views[vn] = d
	}
	stale := codegraph.CheckStale(loc.Path, g)
	if stale == nil {
		stale = []codegraph.StaleNode{}
	}
	s.log.Info("代码图完成", "name", name, "nodes", len(g.Nodes),
		"edges", len(g.Edges), "views", len(views), "stale", len(stale))
	writeJSON(w, http.StatusOK, map[string]any{"baseline": g, "views": views, "stale": stale})
}

// handleProjectCodegraphSource 处理 GET /api/projects/{name}/codegraph/source。
// 窗口规则：from = max(1, line-3)，取 span 行（默认 40，上限 200）——函数定义行
// 上方带 3 行上下文，详情面板不用再拼请求。行号越界不报错，截到文件边界。
func (s *Server) handleProjectCodegraphSource(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.PathValue("name")
	file := r.URL.Query().Get("file")
	line, _ := strconv.Atoi(r.URL.Query().Get("line"))
	span, _ := strconv.Atoi(r.URL.Query().Get("span"))
	if span <= 0 {
		span = 40
	}
	if span > 200 {
		span = 200
	}
	clean := filepath.Clean(file)
	if file == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		s.log.Warn("代码图源码被拒：路径逃逸", "name", name, "file", file)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file 必须是仓库内相对路径"})
		return
	}
	loc, err := s.st.GetProjectLocationByName(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
		return
	}
	raw, err := os.ReadFile(filepath.Join(loc.Path, clean))
	if err != nil {
		s.log.Warn("代码图源码读取失败", "name", name, "file", clean, "cause", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "读取 " + clean + " 失败: " + truncateRunes(err.Error(), 120)})
		return
	}
	lines := strings.Split(string(raw), "\n")
	from := line - 3
	if from < 1 {
		from = 1
	}
	to := from + span
	if to > len(lines)+1 {
		to = len(lines) + 1
	}
	if from > len(lines) {
		from = len(lines)
	}
	s.log.Info("代码图源码完成", "name", name, "file", clean, "from", from, "count", to-from)
	writeJSON(w, http.StatusOK, map[string]any{"file": clean, "from": from, "lines": lines[from-1 : to-1]})
}
```

- [ ] **Step 4: 注册路由**（`internal/agentd/server.go`，`GET /api/projects/{name}/branches` 那行后面）

```go
	api.HandleFunc("GET /api/projects/{name}/codegraph", s.handleProjectCodegraph)
	api.HandleFunc("GET /api/projects/{name}/codegraph/source", s.handleProjectCodegraphSource)
```

（ServeMux 字面段优先，与 `{name}/branches`、`{name}/worktrees` 不冲突。）

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestCodegraph -v`，然后全量 `go test ./internal/agentd/`
Expected: PASS

- [ ] **Step 6: gofmt + 提交**

```bash
gofmt -l internal/agentd/
git add internal/agentd/
git commit -m "feat(agentd): 代码图只读端点——整图数据与 file:line 源码窗口"
```

---

### Task 8: 前端纯逻辑层 graphmath + API 客户端

**Files:**
- Create: `web/src/app/codegraph/graphmath.ts`
- Create: `web/src/app/codegraph/graphmath.test.ts`
- Modify: `web/src/api/types.ts`（追加类型）
- Modify: `web/src/api/client.ts`（追加两个 fetch）

**Interfaces:**
- Consumes: Task 7 的两个端点。
- Produces（后续组件 task 全部消费这些签名）:
  - types: `CgNode/CgGraph/CgDiff/CgStaleNode/CodegraphResp/CgSourceResp`
  - client: `fetchCodegraph(project: string): Promise<CodegraphResp>`；`fetchCodegraphSource(project: string, file: string, line: number, span?: number): Promise<CgSourceResp>`
  - graphmath: `Status/ViewNode/ViewEdge/CgView`；`mergeView(g, d?): CgView`；`scannedEntries(v): string[]`；`buildAdj(v): {adj, radj}`；`neighborhood(v, foci, depth): Record<string, number>`（depth=0 不限；上游负距）；`layoutBands(v, dist, NODE_W=156, XSP=180, YSTEP=112, PADX=60, PADY=70): {px, py, W, H, order}`；`chainTree(v, entry, maxDepth=10)` 树展开数据

- [ ] **Step 1: types.ts 追加**

```ts
// —— 代码图（spec 2026-08-19-codegraph-design §3）——
export interface CgTestRef { name: string; file: string; snippet?: string }
export interface CgNode {
  kind: 'entry' | 'func' | 'model'
  container: string
  order?: number
  name: string
  file: string
  line: number
  signature?: string
  signatureOld?: string
  params?: string[][]
  returns?: string
  summary?: string
  tests?: CgTestRef[]
  fields?: string[][]
  unscanned?: boolean
}
export interface CgContainer { label: string; kind: string; entry?: boolean }
export interface CgGraph {
  meta: { project: string; branch: string; commit: string; scannedAt: string; generator: string }
  containers: Record<string, CgContainer>
  nodes: Record<string, CgNode>
  edges: [string, string][]
}
export interface CgDiff {
  view: string
  base?: string
  summary?: string
  nodesAdded?: Record<string, CgNode>
  nodesModified?: Record<string, CgNode>
  nodesDeleted?: string[]
  edgesAdded?: [string, string][]
  edgesDeleted?: [string, string][]
}
export interface CgStaleNode { id: string; file: string; line: number; reason: string }
export interface CodegraphResp {
  baseline: CgGraph
  views: Record<string, CgDiff>
  stale: CgStaleNode[]
}
export interface CgSourceResp { file: string; from: number; lines: string[] }
```

- [ ] **Step 2: client.ts 追加**（沿用文件内既有 `get`/请求封装约定；与 `fetchProjectTree` 同款）

```ts
export function fetchCodegraph(project: string): Promise<CodegraphResp> {
  return get(`/api/projects/${encodeURIComponent(project)}/codegraph`)
}
export function fetchCodegraphSource(project: string, file: string, line: number, span = 40): Promise<CgSourceResp> {
  return get(`/api/projects/${encodeURIComponent(project)}/codegraph/source?file=${encodeURIComponent(file)}&line=${line}&span=${span}`)
}
```

（若该文件的封装函数不叫 `get`，按文件内实际的同类只读封装写法照抄——**不得**新起一套 fetch 封装。）

- [ ] **Step 3: 写失败测试** `graphmath.test.ts`（vitest；夹具与 Go 侧 Task 1 同构，直接内联小图）

```ts
import { describe, expect, it } from 'vitest'
import { chainTree, layoutBands, mergeView, neighborhood, scannedEntries } from './graphmath'
import type { CgDiff, CgGraph } from '../../api/types'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-19', generator: 'test' },
  containers: { c_cli: { label: 'CLI', kind: '入口', entry: true }, k_svc: { label: 'svc.Server', kind: '服务端' } },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    e_skip: { kind: 'entry', container: 'c_cli', name: 'demo skip', file: 'cmd/skip.go', line: 1, unscanned: true },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    n_do: { kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4 },
    n_save: { kind: 'func', container: 'k_svc', name: 'Server.Save', file: 'svc/server.go', line: 9 },
  },
  edges: [['e_run', 'n_runE'], ['n_runE', 'n_do'], ['n_do', 'n_save']],
}
const d: CgDiff = {
  view: 'branch:x',
  nodesAdded: { n_audit: { kind: 'func', container: 'k_svc', name: 'Server.Audit', file: 'svc/audit.go', line: 3 } },
  nodesModified: { n_do: { kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4, signature: 'new', signatureOld: 'old' } },
  nodesDeleted: ['n_save'],
  edgesAdded: [['n_do', 'n_audit']],
  edgesDeleted: [['n_do', 'n_save']],
}

describe('mergeView', () => {
  it('基准视图无 status，diff 视图状态齐全', () => {
    const base = mergeView(g)
    expect(Object.keys(base.nodes)).toHaveLength(5)
    expect(base.nodes.n_do.status).toBe('')
    const v = mergeView(g, d)
    expect(v.nodes.n_audit.status).toBe('added')
    expect(v.nodes.n_do.status).toBe('modified')
    expect(v.nodes.n_do.signatureOld).toBe('old')
    expect(v.nodes.n_save.status).toBe('deleted')
    expect(v.edges.find((e) => e.to === 'n_audit')?.status).toBe('added')
    expect(v.edges.find((e) => e.to === 'n_save')?.status).toBe('deleted')
  })
})

describe('scannedEntries', () => {
  it('unscanned 入口不进树', () => {
    expect(scannedEntries(mergeView(g))).toEqual(['e_run'])
  })
})

describe('neighborhood', () => {
  it('深度截断：depth=1 只有焦点±1', () => {
    const dist = neighborhood(mergeView(g), ['n_do'], 1)
    expect(Object.keys(dist).sort()).toEqual(['n_do', 'n_runE', 'n_save'])
    expect(dist.n_runE).toBe(-1)
    expect(dist.n_save).toBe(1)
  })
  it('并集：两焦点都在 0 层', () => {
    const dist = neighborhood(mergeView(g), ['n_runE', 'n_save'], 0)
    expect(dist.n_runE).toBe(0)
    expect(dist.n_save).toBe(0)
    expect(dist.e_run).toBe(-1)
  })
  it('deleted 不参与遍历', () => {
    const dist = neighborhood(mergeView(g, d), ['e_run'], 0)
    expect(dist.n_save).toBeUndefined()
    expect(dist.n_audit).toBe(3)
  })
})

describe('layoutBands', () => {
  it('竖向：dist 越大 y 越大，同层 y 相等', () => {
    const v = mergeView(g)
    const dist = neighborhood(v, ['n_do'], 0)
    const { py } = layoutBands(v, dist)
    expect(py.e_run).toBeLessThan(py.n_runE)
    expect(py.n_runE).toBeLessThan(py.n_do)
    expect(py.n_do).toBeLessThan(py.n_save)
  })
})

describe('chainTree', () => {
  it('循环截断且标记 cycle', () => {
    const cyc: CgGraph = { ...g, edges: [['e_run', 'n_runE'], ['n_runE', 'n_do'], ['n_do', 'n_runE']] }
    const tree = chainTree(mergeView(cyc), 'e_run')
    // e_run → runE → do → (cycle runE)
    expect(tree.children[0].children[0].children[0].cycle).toBe(true)
  })
})
```

- [ ] **Step 4: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/graphmath.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 5: 实现 graphmath.ts**（原型 `codegraph.html` 的算法固化版；纯函数、无 DOM）

```ts
// graphmath —— 代码图的纯算法层：合并、邻域 BFS、竖向分层布局、调用树。
//
// 职责：全部确定性纯函数，组件只做渲染与事件
// 边界：不发请求、不碰 DOM；与 Go 侧 internal/codegraph 语义一致
//（deleted 不参与遍历、下游正距上游负距、多焦点并集）——两侧行为分叉
// 就是 bug，以 spec §3/§5 为准
import type { CgDiff, CgGraph, CgNode } from '../../api/types'

export type Status = '' | 'added' | 'modified' | 'deleted'
export interface ViewNode extends CgNode { status: Status }
export interface ViewEdge { from: string; to: string; status: Status }
export interface CgView {
  name: string
  containers: CgGraph['containers']
  nodes: Record<string, ViewNode>
  edges: ViewEdge[]
}

// mergeView 把基线与 diff 合并成视图；无 diff 即纯基准。
export function mergeView(g: CgGraph, d?: CgDiff): CgView {
  const nodes: Record<string, ViewNode> = {}
  for (const [id, n] of Object.entries(g.nodes)) nodes[id] = { ...n, status: '' }
  const edges: ViewEdge[] = g.edges.map(([from, to]) => ({ from, to, status: '' as Status }))
  if (!d) return { name: 'baseline', containers: g.containers, nodes, edges }
  for (const [id, n] of Object.entries(d.nodesAdded ?? {})) nodes[id] = { ...n, status: 'added' }
  for (const [id, n] of Object.entries(d.nodesModified ?? {})) if (nodes[id]) nodes[id] = { ...n, status: 'modified' }
  for (const id of d.nodesDeleted ?? []) if (nodes[id]) nodes[id] = { ...nodes[id], status: 'deleted' }
  const del = new Set((d.edgesDeleted ?? []).map(([a, b]) => `${a} ${b}`))
  for (const e of edges) if (del.has(`${e.from} ${e.to}`)) e.status = 'deleted'
  for (const [from, to] of d.edgesAdded ?? []) edges.push({ from, to, status: 'added' })
  return { name: d.view, containers: g.containers, nodes, edges }
}

// scannedEntries 返回已扫描入口 id（unscanned/deleted 不进左树），按 order+name 稳定排序。
export function scannedEntries(v: CgView): string[] {
  return Object.entries(v.nodes)
    .filter(([, n]) => n.kind === 'entry' && !n.unscanned && n.status !== 'deleted')
    .sort((a, b) => (a[1].order ?? 99) - (b[1].order ?? 99) || a[1].name.localeCompare(b[1].name))
    .map(([id]) => id)
}

// buildAdj 建正反邻接表；deleted 节点/边不参与（渲染残影另算）。
export function buildAdj(v: CgView): { adj: Record<string, string[]>; radj: Record<string, string[]> } {
  const adj: Record<string, string[]> = {}
  const radj: Record<string, string[]> = {}
  for (const e of v.edges) {
    if (e.status === 'deleted' || v.nodes[e.from]?.status === 'deleted' || v.nodes[e.to]?.status === 'deleted') continue
    ;(adj[e.from] ??= []).push(e.to)
    ;(radj[e.to] ??= []).push(e.from)
  }
  return { adj, radj }
}

// neighborhood 多源 BFS：焦点 0 层、下游正、上游负；depth=0 不限。
export function neighborhood(v: CgView, foci: string[], depth: number): Record<string, number> {
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
        for (const t of next[id] ?? []) if (!(t in dist)) { dist[t] = d; nx.push(t) }
      }
      frontier = nx
    }
  }
  sweep(adj, 1)
  sweep(radj, -1)
  return dist
}

// layoutBands 竖向分层：每个 dist 一行，行内先名字序、再由内向外按邻居均值
// 重排减少交叉（原型验证过的布局，常量与 spec §5 基准一致）。
export function layoutBands(
  v: CgView, dist: Record<string, number>,
  NODE_W = 156, XSP = 180, YSTEP = 112, PADX = 60, PADY = 70,
): { px: Record<string, number>; py: Record<string, number>; W: number; H: number; order: number[] } {
  const { adj, radj } = buildAdj(v)
  const bands = new Map<number, string[]>()
  for (const [id, d] of Object.entries(dist)) {
    if (!bands.has(d)) bands.set(d, [])
    bands.get(d)!.push(id)
  }
  const order = [...bands.keys()].sort((a, b) => a - b)
  const maxCnt = Math.max(...order.map((d) => bands.get(d)!.length))
  const W = PADX * 2 + maxCnt * XSP
  const H = PADY * 2 + (order.length - 1) * YSTEP + 46
  const px: Record<string, number> = {}
  const py: Record<string, number> = {}
  const place = (d: number) => {
    const list = bands.get(d)!
    const off = PADX + ((maxCnt - list.length) * XSP) / 2
    list.forEach((id, i) => { px[id] = off + i * XSP; py[id] = PADY + (d - order[0]) * YSTEP })
  }
  const meanX = (id: string) => {
    const nb = [...(adj[id] ?? []), ...(radj[id] ?? [])].filter((t) => px[t] !== undefined)
    return nb.length ? nb.reduce((s, t) => s + px[t], 0) / nb.length : 0
  }
  for (const d of order) bands.get(d)!.sort((a, b) => v.nodes[a].name.localeCompare(v.nodes[b].name))
  place(0)
  for (let d = 1; d <= order[order.length - 1]; d++) if (bands.has(d)) { bands.get(d)!.sort((a, b) => meanX(a) - meanX(b)); place(d) }
  for (let d = -1; d >= order[0]; d--) if (bands.has(d)) { bands.get(d)!.sort((a, b) => meanX(a) - meanX(b)); place(d) }
  return { px, py, W, H, order }
}

// ChainTreeNode 是左树/时序图共用的 DFS 展开结果；cycle=true 表示此处截断回边。
export interface ChainTreeNode { id: string; cycle?: boolean; children: ChainTreeNode[] }

// chainTree 从入口 DFS 展开调用树，路径内重现即标 cycle 截断，深度上限防爆栈。
export function chainTree(v: CgView, entry: string, maxDepth = 10): ChainTreeNode {
  const { adj } = buildAdj(v)
  const walk = (id: string, path: Set<string>, depth: number): ChainTreeNode => {
    const kids = depth >= maxDepth ? [] : (adj[id] ?? [])
    return {
      id,
      children: kids.map((t) =>
        path.has(t) ? { id: t, cycle: true, children: [] }
          : walk(t, new Set([...path, t]), depth + 1)),
    }
  }
  return walk(entry, new Set([entry]), 0)
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/codegraph/graphmath.test.ts && npx tsc --noEmit -p .`
Expected: PASS，类型检查干净

- [ ] **Step 7: 提交**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/app/codegraph/
git commit -m "feat(web): 代码图 API 客户端与纯算法层（合并/BFS/布局/调用树）"
```

---

### Task 9: 页面外壳 + 左树 + 数据 hook + 路由入口

**Files:**
- Create: `web/src/app/codegraph/useCodegraph.ts`
- Create: `web/src/app/codegraph/CodegraphPage.tsx`
- Create: `web/src/app/codegraph/CallTree.tsx`
- Modify: `web/src/app/shell/Shell.tsx`（Routes 里加 `/codegraph`）
- Modify: `web/src/app/tree/ProjectTree.tsx`（底部入口区加「代码图」，模式照抄「设置」入口）
- Test: `web/src/app/codegraph/CallTree.test.tsx`

**Interfaces:**
- Consumes: Task 8 全部；`useProjectTree`（既有，取已登记项目名列表）。
- Produces:
  - `useCodegraph(project: string)` → `{ data: CodegraphResp | null, error: string, loading: boolean }`（一次性取，不轮询——图数据变更频率低，刷新按钮手动重取）
  - `CodegraphPage()` 组件挂 `/codegraph` 路由
  - `CallTree({ view, foci, open, onToggle, onFocus })`：`foci: string[]`、`open: Set<string>`、`onToggle(id, open)`、`onFocus(id, additive: boolean)`（additive = ⌘/Ctrl 按下）
- 页内状态全在 CodegraphPage：`project / viewName / foci: string[] / depth(默认 2) / hist: string[][] / histIdx / mode: 'combo' | 'seq'`

- [ ] **Step 1: 写失败测试** `CallTree.test.tsx`

```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { CallTree } from './CallTree'
import { mergeView } from './graphmath'
// 夹具 g 与 graphmath.test.ts 同款（可提取到 ./testfixtures.ts 共用）

describe('CallTree', () => {
  it('只列已扫描入口；点名字触发 onFocus；⌘+点传 additive', () => {
    const onFocus = vi.fn()
    render(<CallTree view={mergeView(g)} foci={['e_run']} open={new Set(['e_run'])}
      onToggle={() => {}} onFocus={onFocus} />)
    expect(screen.queryByText(/demo skip/)).toBeNull()   // unscanned 不进树
    fireEvent.click(screen.getByText('runE()'))
    expect(onFocus).toHaveBeenCalledWith('n_runE', false)
    fireEvent.click(screen.getByText('runE()'), { metaKey: true })
    expect(onFocus).toHaveBeenLastCalledWith('n_runE', true)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/CallTree.test.tsx`
Expected: FAIL

- [ ] **Step 3: 实现 useCodegraph.ts**

```ts
// useCodegraph —— 按项目一次性取代码图（基线 + 视图 + 保鲜报告）。
// 不轮询：图数据只随扫描/合并变化，页内提供手动刷新即可。
import { useCallback, useEffect, useState } from 'react'
import { fetchCodegraph } from '../../api/client'
import type { CodegraphResp } from '../../api/types'

export function useCodegraph(project: string) {
  const [data, setData] = useState<CodegraphResp | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const reload = useCallback(() => {
    if (!project) return
    setLoading(true)
    setError('')
    fetchCodegraph(project)
      .then(setData)
      .catch((e: Error) => { setData(null); setError(e.message) }) // agentd 中文报错原文透出
      .finally(() => setLoading(false))
  }, [project])
  useEffect(reload, [reload])
  return { data, error, loading, reload }
}
```

- [ ] **Step 4: 实现 CallTree.tsx**（原型 renderCombo 左树的 React 版）

```tsx
// CallTree —— 代码图左树：已扫描入口的调用树导航。
// 展开状态由父组件持有（Set<string>），点名字换焦点、⌘/Ctrl+点做并集追加。
import type { ChainTreeNode } from './graphmath'
import { chainTree, scannedEntries } from './graphmath'
import type { CgView } from './graphmath'

const STATUS_BADGE: Record<string, { text: string; cls: string }> = {
  added: { text: '加', cls: 'bg-green-600' },
  modified: { text: '改', cls: 'bg-amber-500' },
  deleted: { text: '删', cls: 'bg-red-600' },
}

function Row({ view, node, foci, open, onToggle, onFocus }: {
  view: CgView; node: ChainTreeNode; foci: string[]; open: Set<string>
  onToggle: (id: string, open: boolean) => void
  onFocus: (id: string, additive: boolean) => void
}) {
  const n = view.nodes[node.id]
  if (!n) return null
  if (node.cycle) return <div className="pl-4 text-xs text-muted-foreground">↻ {n.name}</div>
  const badge = STATUS_BADGE[n.status]
  return (
    <details open={open.has(node.id)}
      onToggle={(e) => onToggle(node.id, (e.target as HTMLDetailsElement).open)}
      className="pl-3.5">
      <summary className="flex cursor-pointer items-center gap-1.5 rounded px-1 py-0.5 hover:bg-muted">
        <span
          className={`font-mono text-xs ${foci.includes(node.id) ? 'rounded bg-primary px-1 text-primary-foreground' : ''}`}
          onClick={(e) => { e.preventDefault(); e.stopPropagation(); onFocus(node.id, e.metaKey || e.ctrlKey) }}>
          {n.name}{n.kind === 'func' ? '()' : ''}
        </span>
        {badge && <span className={`rounded-full px-1 text-[9px] font-bold text-white ${badge.cls}`}>{badge.text}</span>}
        {n.tests?.length ? <span className="text-[10px] text-green-600">✓{n.tests.length}</span> : null}
      </summary>
      {node.children.map((c, i) => (
        <Row key={`${c.id}-${i}`} view={view} node={c} foci={foci} open={open} onToggle={onToggle} onFocus={onFocus} />
      ))}
    </details>
  )
}

// CallTree 渲染全部已扫描入口，各自一棵 chainTree。
export function CallTree(props: {
  view: CgView; foci: string[]; open: Set<string>
  onToggle: (id: string, open: boolean) => void
  onFocus: (id: string, additive: boolean) => void
}) {
  return (
    <nav className="w-80 shrink-0 overflow-auto border-r p-2 text-[13px]">
      {scannedEntries(props.view).map((e) => (
        <Row key={e} view={props.view} node={chainTree(props.view, e)} foci={props.foci}
          open={props.open} onToggle={props.onToggle} onFocus={props.onFocus} />
      ))}
    </nav>
  )
}
```

- [ ] **Step 5: 实现 CodegraphPage.tsx（外壳；FocusGraph/DetailPanel/SeqView 此时先用占位组件文件，Task 10/11 替换实现）**

```tsx
// CodegraphPage —— 代码图页（/codegraph）：工具条 + 左树/中图/右详情三栏。
//
// 布局契约（spec §5）：左树 320px 固定、右详情 340px 固定、中间自适应。
// 状态机：foci（焦点集合，单选=1 个）、hist/histIdx（焦点历史，语义同浏览器
// 历史——新选择截断前进分支）、depth 默认 2、viewName 默认 baseline。
import { useMemo, useState } from 'react'
import { useProjectTree } from '../data/useProjectTree'
import { CallTree } from './CallTree'
import { DetailPanel } from './DetailPanel'
import { FocusGraph } from './FocusGraph'
import { SeqView } from './SeqView'
import { mergeView, scannedEntries } from './graphmath'
import { useCodegraph } from './useCodegraph'

export function CodegraphPage() {
  const tree = useProjectTree()   // 既有 hook；取已登记项目名列表
  const projects = useMemo(() => (tree.data?.projects ?? []).map((p) => p.name), [tree.data])
  const [project, setProject] = useState('')
  const active = project || projects[0] || ''
  const { data, error, loading, reload } = useCodegraph(active)

  const [viewName, setViewName] = useState('baseline')
  const [mode, setMode] = useState<'combo' | 'seq'>('combo')
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

  // 有效焦点：集合里被视图删除/不存在的成员剔掉，空了退回第一个入口
  const effFoci = useMemo(() => {
    if (!view) return []
    const ok = foci.filter((f) => view.nodes[f] && view.nodes[f].status !== 'deleted')
    return ok.length ? ok : scannedEntries(view).slice(0, 1)
  }, [view, foci])

  // setFociWithHist：换焦点集合并入历史；fromHist 时只移动游标
  const setFociWithHist = (next: string[], fromHist = false) => {
    if (next.join('|') === effFoci.join('|')) return
    if (!fromHist) {
      const h = [...hist.slice(0, histIdx + 1), next]
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

  if (error) return <div className="p-6 text-sm text-red-600">{error}</div>
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-sm">
        <label className="text-muted-foreground">项目</label>
        <select value={active} onChange={(e) => setProject(e.target.value)} className="rounded border px-1.5 py-0.5">
          {projects.map((p) => <option key={p}>{p}</option>)}
        </select>
        <div className="flex overflow-hidden rounded border">
          {(['combo', 'seq'] as const).map((m) => (
            <button key={m} onClick={() => setMode(m)}
              className={`px-2.5 py-0.5 ${mode === m ? 'bg-primary text-primary-foreground' : ''}`}>
              {m === 'combo' ? '树+图' : '时序图'}
            </button>
          ))}
        </div>
        <label className="text-muted-foreground">视图</label>
        <select value={viewName} onChange={(e) => { setViewName(e.target.value) }} className="rounded border px-1.5 py-0.5">
          <option value="baseline">基准 · {data?.baseline.meta.branch ?? ''}</option>
          {Object.entries(data?.views ?? {}).map(([k, v]) => <option key={k} value={k}>{v.view}</option>)}
        </select>
        {data && data.stale.length > 0 && (
          <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700"
            title={data.stale.map((s) => `${s.id}: ${s.reason}`).join('\n')}>
            ⚠ {data.stale.length} 个节点疑似失鲜
          </span>
        )}
        <button onClick={reload} className="ml-auto rounded border px-2 py-0.5 text-xs">刷新</button>
      </div>
      {loading || !view ? (
        <div className="p-6 text-sm text-muted-foreground">{loading ? '加载中…' : '该项目未生成代码图'}</div>
      ) : (
        <div className="flex min-h-0 flex-1">
          <CallTree view={view} foci={effFoci} open={open}
            onToggle={(id, o) => setOpen((s) => { const n = new Set(s); o ? n.add(id) : n.delete(id); return n })}
            onFocus={onFocus} />
          {mode === 'combo' ? (
            <FocusGraph view={view} foci={effFoci} depth={depth} staleIds={staleIds}
              onDepth={setDepth} onFocus={onFocus} onSelect={setSelected}
              canBack={histIdx > 0} canFwd={histIdx < hist.length - 1}
              onBack={() => { setHistIdx(histIdx - 1); setFociWithHist(hist[histIdx - 1], true) }}
              onFwd={() => { setHistIdx(histIdx + 1); setFociWithHist(hist[histIdx + 1], true) }} />
          ) : (
            <SeqView view={view} entry={effFoci[0]} onSelect={setSelected} />
          )}
          <DetailPanel project={active} view={view} nodeId={selected || effFoci[effFoci.length - 1] || ''}
            stale={staleIds} onJump={(id) => setFociWithHist([id])} />
        </div>
      )}
    </div>
  )
}
```

（本 task 先创建 `FocusGraph.tsx`/`DetailPanel.tsx`/`SeqView.tsx` 三个最小占位——导出同签名组件、渲染 `<div>待实现</div>`——保证本 task 可编译可提交；Task 10/11 用真实实现整体替换占位文件。）

- [ ] **Step 6: 挂路由与入口**

Shell.tsx 的 `<Routes>` 里、`/settings` 那条后面加：

```tsx
<Route path="/codegraph" element={<CodegraphPage />} />
```

`ProjectTree.tsx` 底部入口区（找 `onOpenSettings` 被使用的那一排入口），仿「设置」加一条「代码图」入口：新增 prop `onOpenCodegraph?: () => void`，Shell 传 `() => navigate('/codegraph')`。样式与相邻入口逐像素一致（同 class）。

- [ ] **Step 7: 跑测试与类型检查**

Run: `cd web && npx vitest run src/app/codegraph/ && npx tsc --noEmit -p . && npm run build`
Expected: 全绿

- [ ] **Step 8: 提交**

```bash
git add web/src/app/codegraph/ web/src/app/shell/Shell.tsx web/src/app/tree/ProjectTree.tsx
git commit -m "feat(web): 代码图页外壳、左树导航与 /codegraph 路由入口"
```

---

### Task 10: FocusGraph——竖向焦点子图（平移/缩放/历史/并集/层级）

**Files:**
- Modify: `web/src/app/codegraph/FocusGraph.tsx`（替换 Task 9 占位）
- Test: `web/src/app/codegraph/FocusGraph.test.tsx`

**Interfaces:**
- Consumes: `neighborhood/layoutBands`（Task 8）；props 契约见 Task 9 的调用处。
- Produces: `FocusGraph({ view, foci, depth, staleIds, onDepth, onFocus, onSelect, canBack, canFwd, onBack, onFwd })`。

行为契约（= 原型已确认形态，spec §5）：
- 上游在上/焦点居中/下游在下；焦点卡片高亮描边；触焦点的边加粗。
- 单击卡片 = `onFocus(id, ev.metaKey||ev.ctrlKey)`；再点已是唯一焦点的卡片是 no-op。
- 空白拖动平移（grab/grabbing 光标）；滚轮平移；**ctrl/⌘+滚轮以光标为不动点缩放**，0.3–2.5，换焦点保留倍率、平移重算（焦点居中，顶部最多悬空 24px）。
- 顶部控制条：◀ 后退 / 前进 ▶（disabled 跟 canBack/canFwd）、层级下拉（上下 1/2/3 级、全部→depth=0）、多焦点 chips（× 移出）、提示语。
- 差异染色：added 绿框、modified 琥珀框、deleted 红虚线+删除线；deleted 边只在触焦点时画。stale 节点名旁加 ⚠。

- [ ] **Step 1: 写失败测试**（jsdom 下验证结构性行为，不验像素）

```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { FocusGraph } from './FocusGraph'
import { mergeView } from './graphmath'
// 夹具 g 复用 testfixtures.ts

const noop = () => {}
const base = { depth: 2, staleIds: new Set<string>(), onDepth: noop, onSelect: noop,
  canBack: false, canFwd: false, onBack: noop, onFwd: noop }

describe('FocusGraph', () => {
  it('渲染焦点邻域且方向正确（上游卡在焦点上方）', () => {
    const { container } = render(<FocusGraph view={mergeView(g)} foci={['n_do']} onFocus={noop} {...base} />)
    const cards = [...container.querySelectorAll('[data-node]')] as HTMLElement[]
    const top = (id: string) => parseFloat(cards.find((c) => c.dataset.node === id)!.style.top)
    expect(top('n_runE')).toBeLessThan(top('n_do'))
    expect(top('n_do')).toBeLessThan(top('n_save'))
  })
  it('单击换焦点、⌘+单击并集', () => {
    const onFocus = vi.fn()
    const { container } = render(<FocusGraph view={mergeView(g)} foci={['n_do']} onFocus={onFocus} {...base} />)
    const save = container.querySelector('[data-node="n_save"]')!
    fireEvent.click(save)
    expect(onFocus).toHaveBeenCalledWith('n_save', false)
    fireEvent.click(save, { metaKey: true })
    expect(onFocus).toHaveBeenLastCalledWith('n_save', true)
  })
  it('多焦点渲染 chips，层级下拉回调 onDepth', () => {
    const onDepth = vi.fn()
    render(<FocusGraph view={mergeView(g)} foci={['n_runE', 'n_save']} onFocus={noop}
      {...base} onDepth={onDepth} />)
    expect(screen.getByText('runE')).toBeTruthy()       // chip
    fireEvent.change(screen.getByTitle('上下游各展开几级'), { target: { value: '0' } })
    expect(onDepth).toHaveBeenCalledWith(0)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/FocusGraph.test.tsx`
Expected: FAIL（占位组件无 data-node）

- [ ] **Step 3: 实现 FocusGraph.tsx**

```tsx
// FocusGraph —— 竖向焦点子图：上游在上、焦点居中、下游在下。
//
// 交互契约（原型确认形态，spec §5）：单击换焦点、⌘/Ctrl+单击并集、
// 空白拖动/滚轮平移、ctrl+滚轮以光标为不动点缩放、◀▶ 历史、层级下拉。
// 算法全部来自 graphmath；本组件只做渲染、事件与 transform 状态。
import { useEffect, useMemo, useRef, useState } from 'react'
import type { CgView } from './graphmath'
import { layoutBands, neighborhood } from './graphmath'

const NODE_W = 156

export function FocusGraph({ view, foci, depth, staleIds, onDepth, onFocus, onSelect,
  canBack, canFwd, onBack, onFwd }: {
  view: CgView; foci: string[]; depth: number; staleIds: Set<string>
  onDepth: (d: number) => void
  onFocus: (id: string, additive: boolean) => void
  onSelect: (id: string) => void
  canBack: boolean; canFwd: boolean; onBack: () => void; onFwd: () => void
}) {
  const wrap = useRef<HTMLDivElement>(null)
  const [pan, setPan] = useState<{ x: number; y: number } | null>(null)
  const [zoom, setZoom] = useState(1)

  const { dist, px, py, W, H, order } = useMemo(() => {
    const dist = neighborhood(view, foci, depth)
    return { dist, ...layoutBands(view, dist) }
  }, [view, foci, depth])

  // 焦点/层级变化 → 平移重算：锚点是最后加入的焦点，垂直居中但顶部最多悬空 24px
  const anchor = foci[foci.length - 1]
  useEffect(() => { setPan(null) }, [foci.join('|'), depth])   // eslint-disable-line react-hooks/exhaustive-deps
  const effPan = useMemo(() => {
    if (pan) return pan
    const el = wrap.current
    if (!el || px[anchor] === undefined) return { x: 0, y: 0 }
    return {
      x: el.clientWidth / 2 - (px[anchor] + NODE_W / 2) * zoom,
      y: Math.min(el.clientHeight / 2 - (py[anchor] + 22) * zoom, 24),
    }
  }, [pan, px, py, anchor, zoom])

  // 拖动平移：mousedown 只认空白（卡片与控制条 stopPropagation 不到这里靠 closest 判断）
  const onMouseDown = (ev: React.MouseEvent) => {
    if ((ev.target as HTMLElement).closest('[data-node],[data-ctl]')) return
    const sx = ev.clientX; const sy = ev.clientY; const ox = effPan.x; const oy = effPan.y
    const move = (e: MouseEvent) => setPan({ x: ox + e.clientX - sx, y: oy + e.clientY - sy })
    const up = () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up) }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    ev.preventDefault()
  }
  // 滚轮：普通=平移；ctrl/⌘=以光标为不动点缩放（触控板捏合也走 ctrlKey 路径）
  useEffect(() => {
    const el = wrap.current
    if (!el) return
    const onWheel = (ev: WheelEvent) => {
      ev.preventDefault()
      if (ev.ctrlKey || ev.metaKey) {
        const r = el.getBoundingClientRect()
        const mx = ev.clientX - r.left; const my = ev.clientY - r.top
        setZoom((z) => {
          const nz = Math.min(2.5, Math.max(0.3, z * Math.exp(-ev.deltaY * 0.0035)))
          setPan((p) => {
            const cur = p ?? effPan
            return { x: mx - (mx - cur.x) * (nz / z), y: my - (my - cur.y) * (nz / z) }
          })
          return nz
        })
      } else {
        setPan((p) => { const cur = p ?? effPan; return { x: cur.x - ev.deltaX, y: cur.y - ev.deltaY } })
      }
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [effPan])

  const fociSet = new Set(foci)
  const nodeCls = (id: string) => {
    const n = view.nodes[id]
    let c = 'absolute w-[156px] cursor-pointer rounded-lg border bg-background px-2.5 py-1 shadow-sm '
    if (n.kind === 'entry') c += 'rounded-full bg-primary text-primary-foreground '
    if (n.status === 'added') c += 'border-green-600 bg-green-50 '
    if (n.status === 'modified') c += 'border-amber-500 bg-amber-50 '
    if (fociSet.has(id)) c += 'outline outline-2 outline-primary '
    return c
  }

  return (
    <div ref={wrap} className="relative min-w-0 flex-1 cursor-grab overflow-hidden" onMouseDown={onMouseDown}>
      <div data-ctl className="absolute left-3 top-2 z-10 flex flex-wrap items-center gap-1.5">
        <button className="rounded border px-2 py-0.5 text-xs disabled:opacity-40" disabled={!canBack} onClick={onBack}>◀ 后退</button>
        <button className="rounded border px-2 py-0.5 text-xs disabled:opacity-40" disabled={!canFwd} onClick={onFwd}>前进 ▶</button>
        <select title="上下游各展开几级" value={depth} onChange={(e) => onDepth(Number(e.target.value))}
          className="rounded border px-1 py-0.5 text-xs">
          <option value={1}>上下 1 级</option><option value={2}>上下 2 级</option>
          <option value={3}>上下 3 级</option><option value={0}>全部层级</option>
        </select>
        {foci.length > 1 && foci.map((f) => (
          <span key={f} className="flex items-center gap-1 rounded-full bg-primary px-2 py-0.5 font-mono text-[11px] text-primary-foreground">
            {view.nodes[f]?.name}
            <b className="cursor-pointer opacity-70 hover:opacity-100" onClick={() => onFocus(f, true)}>×</b>
          </span>
        ))}
        <span className="rounded-full border bg-muted px-2.5 py-0.5 text-[11px] text-muted-foreground">
          {foci.length > 1 ? '并集视图：N 个焦点的链叠加' : '单击：只看它的链 · ⌘+单击：并集 · 空白拖动 · ⌃滚轮缩放'}
        </span>
      </div>
      <div className="absolute" style={{ width: W, height: H, transform: `translate(${effPan.x}px, ${effPan.y}px) scale(${zoom})`, transformOrigin: '0 0' }}>
        <svg width={W} height={H} className="absolute inset-0">
          {view.edges.map((e, i) => {
            if (!(e.from in dist) || !(e.to in dist)) return null
            if (e.status === 'deleted' && !fociSet.has(e.from) && !fociSet.has(e.to)) return null
            const x1 = px[e.from] + NODE_W / 2; const y1 = py[e.from] + 44
            const x2 = px[e.to] + NODE_W / 2; const y2 = py[e.to]
            const touch = fociSet.has(e.from) || fociSet.has(e.to)
            const color = e.status === 'added' ? '#16a34a' : e.status === 'deleted' ? '#dc2626' : touch ? '#404040' : '#b8b8b8'
            return <path key={i} d={`M ${x1} ${y1} C ${x1} ${y1 + 46}, ${x2} ${y2 - 46}, ${x2} ${y2}`}
              fill="none" stroke={color} strokeWidth={touch ? 2 : 1.5}
              strokeDasharray={e.status === 'deleted' ? '5 4' : undefined} />
          })}
        </svg>
        {order[0] < 0 && <div className="absolute text-[10px] tracking-widest text-muted-foreground" style={{ left: 60, top: 36 }}>↑ 上游（谁调用它）</div>}
        {order[order.length - 1] > 0 && <div className="absolute text-[10px] tracking-widest text-muted-foreground" style={{ left: 60, top: H - 22 }}>下游（它调用谁）↓</div>}
        {Object.keys(dist).map((id) => {
          const n = view.nodes[id]
          return (
            <div key={id} data-node={id} className={nodeCls(id)} style={{ left: px[id], top: py[id] }}
              onClick={(e) => {
                onSelect(id)
                if (!(foci.length === 1 && foci[0] === id)) onFocus(id, e.metaKey || e.ctrlKey)
              }}>
              <div className={`break-all font-mono text-[11px] font-semibold ${n.status === 'deleted' ? 'text-muted-foreground line-through' : ''}`}>
                {n.name}{n.kind === 'func' ? '()' : ''}{staleIds.has(id) ? ' ⚠' : ''}
              </div>
              <div className="flex gap-1 text-[9.5px] opacity-70">
                <span>{view.containers[n.container]?.label ?? ''}</span>
                {n.tests?.length ? <span className="text-green-600">✓{n.tests.length}</span> : null}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
```

（deleted 节点边框样式：在 `nodeCls` 里补 `if (n.status === 'deleted') c += 'border-dashed border-red-600 '`。）

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/codegraph/ && npx tsc --noEmit -p .`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/app/codegraph/FocusGraph.tsx web/src/app/codegraph/FocusGraph.test.tsx
git commit -m "feat(web): 代码图焦点子图——竖向分层/平移缩放/焦点历史/并集多选"
```

---

### Task 11: DetailPanel + SeqView

**Files:**
- Modify: `web/src/app/codegraph/DetailPanel.tsx`（替换占位）
- Modify: `web/src/app/codegraph/SeqView.tsx`（替换占位）
- Test: `web/src/app/codegraph/DetailPanel.test.tsx`

**Interfaces:**
- Consumes: `fetchCodegraphSource`（Task 8）；props 契约见 Task 9 调用处。
- Produces: `DetailPanel({ project, view, nodeId, stale, onJump })`；`SeqView({ view, entry, onSelect })`。

- [ ] **Step 1: 写失败测试** `DetailPanel.test.tsx`

```tsx
// 断言四件事（mock fetchCodegraphSource）：
// 1. 渲染签名/参数表/返回/测试列表；modified 节点渲染新旧签名两行（旧的带删除线样式类）
// 2. 「被谁调用 / 调用了」列表来自 view.edges，点击项触发 onJump(id)
// 3. stale 集合包含 nodeId 时渲染 ⚠ 失鲜提示与 reason 无关的固定文案
// 4. 展开「源码」区触发 fetchCodegraphSource(project, file, line)，行号从 resp.from 起排
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
vi.mock('../../api/client', () => ({
  fetchCodegraphSource: vi.fn().mockResolvedValue({ file: 'svc/server.go', from: 1, lines: ['package svc', '', 'func Do() {}'] }),
}))
import { fetchCodegraphSource } from '../../api/client'
import { DetailPanel } from './DetailPanel'
import { mergeView } from './graphmath'
// …用 testfixtures 的 g；具体断言按上面四条写全
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/codegraph/DetailPanel.test.tsx`
Expected: FAIL

- [ ] **Step 3: 实现 DetailPanel.tsx**

```tsx
// DetailPanel —— 代码图右详情（常显，跟随焦点/选中节点）。
// 区块：职责/签名(新旧对照)/参数/返回/字段/关联测试/被谁调用/调用了/源码。
// 源码按 file:line 经 agentd 实时读——不落地缓存，保鲜以真实文件为准。
import { useEffect, useState } from 'react'
import { fetchCodegraphSource } from '../../api/client'
import type { CgSourceResp } from '../../api/types'
import type { CgView } from './graphmath'

function Sec({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-3.5">
      <div className="mb-1 text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      {children}
    </div>
  )
}

export function DetailPanel({ project, view, nodeId, stale, onJump }: {
  project: string; view: CgView; nodeId: string; stale: Set<string>
  onJump: (id: string) => void
}) {
  const n = view.nodes[nodeId]
  const [src, setSrc] = useState<CgSourceResp | null>(null)
  const [srcOpen, setSrcOpen] = useState(false)
  useEffect(() => { setSrc(null); setSrcOpen(false) }, [nodeId])
  useEffect(() => {
    if (srcOpen && !src && n?.file) {
      fetchCodegraphSource(project, n.file, n.line).then(setSrc).catch(() => setSrc({ file: n.file, from: 0, lines: ['（源码读取失败）'] }))
    }
  }, [srcOpen, src, n, project])
  if (!n) return <aside className="w-[340px] shrink-0 overflow-y-auto border-l p-3.5" />
  const callers = view.edges.filter((e) => e.to === nodeId && e.status !== 'deleted').map((e) => e.from)
  const callees = view.edges.filter((e) => e.from === nodeId && e.status !== 'deleted').map((e) => e.to)
  return (
    <aside className="w-[340px] shrink-0 overflow-y-auto border-l p-3.5 text-sm">
      <h3 className="break-all font-mono text-sm font-semibold">{n.name}</h3>
      <div className="mb-2.5 font-mono text-[11px] text-muted-foreground">{n.file}:{n.line} · {view.containers[n.container]?.label}</div>
      {stale.has(nodeId) && <div className="mb-2.5 rounded border border-amber-300 bg-amber-50 px-2 py-1 text-xs text-amber-700">⚠ 疑似失鲜：file:line 与真实源码对不上，建议重扫后再采信</div>}
      {n.summary && <Sec label="职责"><div>{n.summary}</div></Sec>}
      {n.signature && (
        <Sec label="签名">
          {n.signatureOld && <pre className="mb-1 whitespace-pre-wrap rounded bg-muted px-2 py-1.5 font-mono text-[11.5px] line-through opacity-60">{n.signatureOld}</pre>}
          <pre className="whitespace-pre-wrap rounded bg-muted px-2 py-1.5 font-mono text-[11.5px]">{n.signature}</pre>
        </Sec>
      )}
      {n.params?.length ? (
        <Sec label="参数">
          <table className="w-full text-xs"><tbody>
            {n.params.map(([pn, pt, ps], i) => (
              <tr key={i} className="border-t"><td className="py-0.5 pr-2 font-mono">{pn}</td>
                <td className="pr-2 font-mono text-muted-foreground">{pt}</td><td>{ps ?? ''}</td></tr>
            ))}
          </tbody></table>
        </Sec>
      ) : null}
      {n.returns && <Sec label="返回"><span className="font-mono text-xs">{n.returns}</span></Sec>}
      {n.fields?.length ? (
        <Sec label="字段">
          <table className="w-full text-xs"><tbody>
            {n.fields.map(([fn, ft, fs], i) => (
              <tr key={i} className="border-t"><td className="py-0.5 pr-2 font-mono">{fn}</td>
                <td className="pr-2 font-mono text-muted-foreground">{ft}</td><td>{fs ?? ''}</td></tr>
            ))}
          </tbody></table>
        </Sec>
      ) : null}
      <Sec label="关联测试">
        {n.tests?.length ? n.tests.map((t) => (
          <details key={t.name} className="mb-1">
            <summary className="cursor-pointer font-mono text-xs text-green-700">{t.name} <span className="text-muted-foreground">{t.file}</span></summary>
            {t.snippet && <pre className="mt-1 overflow-x-auto rounded bg-muted p-2 text-[11px]">{t.snippet}</pre>}
          </details>
        )) : <div className="text-xs text-muted-foreground">无——这也是暴露的信号：该方法没有测试覆盖</div>}
      </Sec>
      {[['被谁调用', callers], ['调用了', callees]].map(([label, ids]) => (
        <Sec key={label as string} label={label as string}>
          {(ids as string[]).length ? (ids as string[]).map((id) => (
            <div key={id} className="cursor-pointer font-mono text-xs text-primary hover:underline" onClick={() => onJump(id)}>
              {label === '被谁调用' ? '←' : '→'} {view.nodes[id]?.name}
            </div>
          )) : <div className="text-xs text-muted-foreground">（图内无记录）</div>}
        </Sec>
      ))}
      <details open={srcOpen} onToggle={(e) => setSrcOpen((e.target as HTMLDetailsElement).open)}>
        <summary className="cursor-pointer text-xs text-muted-foreground">源码（实时读自 {n.file}:{n.line}）</summary>
        {src && (
          <pre className="mt-1 overflow-x-auto rounded bg-muted p-2 text-[11px] leading-relaxed">
            {src.lines.map((l, i) => `${String(src.from + i).padStart(4)} ${l}`).join('\n')}
          </pre>
        )}
      </details>
    </aside>
  )
}
```

- [ ] **Step 4: 实现 SeqView.tsx**（原型 renderSeq 的固化：列 = 类（container），箭头按 DFS 边序自上而下）

```tsx
// SeqView —— 时序图辅助视角：单入口链路的跨类调用顺序。
// 列 = 链上出现的容器（按首次出现排）；每条边一行箭头，自上而下即调用顺序。
import { useMemo } from 'react'
import type { CgView } from './graphmath'
import { buildAdj } from './graphmath'

export function SeqView({ view, entry, onSelect }: {
  view: CgView; entry: string; onSelect: (id: string) => void
}) {
  const { cols, calls } = useMemo(() => {
    const { adj } = buildAdj(view)
    const cols: string[] = []
    const calls: { from: string; to: string }[] = []
    const seen = new Set<string>([entry])
    const colOf = (id: string) => {
      const c = view.nodes[id].container
      if (!cols.includes(c)) cols.push(c)
      return c
    }
    colOf(entry)
    const walk = (id: string) => {
      for (const t of adj[id] ?? []) {
        calls.push({ from: id, to: t })
        colOf(t)
        if (!seen.has(t)) { seen.add(t); walk(t) }
      }
    }
    walk(entry)
    return { cols, calls }
  }, [view, entry])

  const X = (c: string) => 90 + cols.indexOf(c) * 190
  const H = 80 + calls.length * 40
  return (
    <div className="min-w-0 flex-1 overflow-auto">
      <svg width={Math.max(700, 90 + cols.length * 190)} height={H}>
        {cols.map((c) => (
          <g key={c}>
            <text x={X(c)} y={28} textAnchor="middle" className="fill-current font-mono text-[11px] font-semibold">{view.containers[c]?.label}</text>
            <line x1={X(c)} y1={40} x2={X(c)} y2={H - 10} stroke="#d4d4d4" strokeDasharray="3 3" />
          </g>
        ))}
        {calls.map((call, i) => {
          const y = 70 + i * 40
          const x1 = X(view.nodes[call.from].container); const x2 = X(view.nodes[call.to].container)
          const self = x1 === x2
          return (
            <g key={i} className="cursor-pointer" onClick={() => onSelect(call.to)}>
              {self ? (
                <path d={`M ${x1} ${y} C ${x1 + 46} ${y}, ${x1 + 46} ${y + 18}, ${x1} ${y + 18}`} fill="none" stroke="#525252" markerEnd="url(#cgArrow)" />
              ) : (
                <line x1={x1} y1={y} x2={x2} y2={y} stroke="#525252" markerEnd="url(#cgArrow)" />
              )}
              <text x={(x1 + x2) / 2} y={y - 5} textAnchor="middle" className="fill-current font-mono text-[10.5px] hover:underline">
                {view.nodes[call.to].name}
              </text>
            </g>
          )
        })}
        <defs>
          <marker id="cgArrow" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto">
            <path d="M0,0 L7,3.5 L0,7 z" fill="#525252" />
          </marker>
        </defs>
      </svg>
    </div>
  )
}
```

- [ ] **Step 5: 跑全部前端测试 + 构建**

Run: `cd web && npx vitest run && npx tsc --noEmit -p . && npm run build`
Expected: 全绿

- [ ] **Step 6: 提交**

```bash
git add web/src/app/codegraph/
git commit -m "feat(web): 代码图详情面板（源码实时读/新旧签名/跳转）与时序图视角"
```

---

### Task 12: 全仓终验

**Files:** 无新建；修任何红。

- [ ] **Step 1: 后端全量**

Run: `go build ./... && go test ./... && gofmt -l .`
Expected: 构建过、测试全绿、gofmt 零输出。红了就修，修不动的如实记 ledger——**不许绕过、不许标 skip**。

- [ ] **Step 2: 前端全量**

Run: `cd web && npm run test && npx tsc --noEmit -p . && npm run build`
Expected: 全绿。

- [ ] **Step 3: 真实数据端到端（CLI 侧）**

```bash
go run . graph validate --repo . --stale
go run . graph who-calls Server.handleDispatch --repo . --depth 0
```

Expected: validate 引用完整性零 issue；stale 结果**如实记录进 ledger**（本仓基线扫描自 60b944f5，落后 main 若干提交，出现少量失鲜是预期内的正常信号，不是本 plan 的 bug——记录数量与节点名即可，不要去改基线数据）。who-calls 输出包含 CLI 入口层节点。

- [ ] **Step 4: 提交收尾**

```bash
git add -A
git commit -m "chore(codegraph): 一期终验收尾" --allow-empty
```

**（审核者本地验收，不派发）**：控制台真机走查——起隔离 agentd 实例（独立 DataDir+端口，见既有验收惯例）打开 `/codegraph`，对照 `prototypes/codegraph/pages/codegraph.html` 逐项核对形态与交互；通过后按 `prototypes/base/README.md` 的确认基准行推进状态。

---

## Self-Review 记录

- **Spec 覆盖**：§2 三层边界（Task 1-4 零依赖包 / Task 5 CLI 直读 / Task 7+9-11 UI）✓；§3 schema 与 diff 生命周期（Task 1、2；合并折基准是流程约定，不在代码范围）✓；§4 扫描配方（Task 6；种子基线已提前入库 41f2c6604）✓；§5 交互全清单（Task 9-11，逐条对齐已确认形态）✓；§6 agent 查询（Task 5：chain/who-calls/并集/--depth/--view/unscanned 警示）✓；§7 保鲜（Task 4 检测 + Task 5/7 透出；「brainstorm 前提示重扫」是流程约定不入代码）✓；§9 测试策略（validate 子命令、纯函数单测、stale 夹具、UI 对照）✓。
- **不派发项**：控制台真机走查需起 agentd，标注为审核者本地执行（Task 12 末尾），与纪律块不冲突。
- **类型一致性**：`Neighborhood(v, foci, down, up)` 在 Task 3 定义、Task 5 消费（-1 语义换算已写明）；前端 `neighborhood(v, foci, depth)` 单参深度是 UI 契约（两方向同值），与 Go 侧双参不冲突（CLI 是单方向查询）。`ViewNode/ViewEdge` Go 与 TS 同名同义。
