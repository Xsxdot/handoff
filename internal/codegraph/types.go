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
	// Domain 是所属领域 id，必须是**叶子**领域。空串只在整图没有 domains 段时
	// 合法（旧扫描数据，消费方降级为单领域视图）。
	Domain string `json:"domain,omitempty"`
}

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
	ProjScanned  bool       `json:"projScanned,omitempty"`
}

// Edge 是一条调用关系 [caller, callee]。
type Edge [2]string

// Projection 是一条数据实体投影关系 [投影点节点 id, model 节点 id, kind]。
// kind=typed 表示类型可见的投影；handroll 表示手搭 map/字面量拼装（类型系统不可见）；
// twin 表示跨语言孪生的 model↔model 关系。独立顶层列表保持与 implements 一致，旧基线无需迁移。
type Projection [3]string

// Graph 是 codegraph/baseline.json 的顶层结构。
// 顶层 "diffs" 字段是早期原型的兼容残留，一期忽略：视图一律来自 diffs/目录。
type Graph struct {
	Meta Meta `json:"meta"`
	// Domains 是领域段，可为空——空即「该图未划分领域」，消费方降级为单领域视图。
	// **不得按包名伪造领域**：伪造出来的层级会被人和 agent 当成真实架构读。
	Domains    map[string]Domain    `json:"domains,omitempty"`
	Containers map[string]Container `json:"containers"`
	Nodes      map[string]Node      `json:"nodes"`
	Edges      []Edge               `json:"edges"`
	// Implements 是接口满足边 [实现, 接口]。与 Edges 分列是 wire 兼容决策
	//（Edge 是二元组塞不进 kind 字段，spec §3）；语义上它们是 kind=implements 的边。
	Implements  []Edge       `json:"implements,omitempty"`
	Projections []Projection `json:"projections,omitempty"`
}

// Diff 是 codegraph/diffs/<view>.json：某分支/plan 相对基准的差异声明。
type Diff struct {
	View               string          `json:"view"`
	Base               string          `json:"base,omitempty"`
	Summary            string          `json:"summary,omitempty"`
	NodesAdded         map[string]Node `json:"nodesAdded,omitempty"`
	NodesModified      map[string]Node `json:"nodesModified,omitempty"`
	NodesDeleted       []string        `json:"nodesDeleted,omitempty"`
	EdgesAdded         []Edge          `json:"edgesAdded,omitempty"`
	EdgesDeleted       []Edge          `json:"edgesDeleted,omitempty"`
	ImplementsAdded    []Edge          `json:"implementsAdded,omitempty"`
	ImplementsDeleted  []Edge          `json:"implementsDeleted,omitempty"`
	ProjectionsAdded   []Projection    `json:"projectionsAdded,omitempty"`
	ProjectionsDeleted []Projection    `json:"projectionsDeleted,omitempty"`
}
