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
// 顶层 "diffs" 字段是早期原型的兼容残留，一期忽略：视图一律来自 diffs/目录。
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
