package codegraph

import (
	"strings"
	"testing"
)

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

// 组装点死配置：assembly 里写了视图中不存在的文件，必须报 dead-assembly warn。
// 这是与 dead-rule 对称的一条——在此之前 assembly 写错文件名完全没有信号，
// 一条不存在的 "cmd/main.go" 能在基准里躺过整轮而无人发现。
func TestCheckDeadAssembly(t *testing.T) {
	nodes := map[string][2]string{
		"main": {"main", "cmd/main.go"}, "b1": {"b.Facade", "b/f.go"},
	}
	tg := &Target{
		Meta: TargetMeta{Version: 1},
		Domains: []TargetDomain{
			{ID: "d_cmd", Type: "logic", Paths: []string{"cmd/**"}},
			{ID: "d_b", Type: "logic", Paths: []string{"b/**"}},
		},
		Assembly: []string{"cmd/main.go", "cmd/ghost.go"},
	}
	rep := Check(tg, mkView(nodes, [][2]string{{"main", "b1"}}, nil))

	var hits []Finding
	for _, w := range rep.Warns {
		if w.Kind == "dead-assembly" {
			hits = append(hits, w)
		}
	}
	// 恰好一条：存在的 cmd/main.go 不该报，不存在的 cmd/ghost.go 必须报。
	// 断言条数而不只断言「有」，是为了挡住「把所有 assembly 都报一遍」这种实现。
	if len(hits) != 1 {
		t.Fatalf("dead-assembly 应恰好 1 条，实际 %d 条: %+v", len(hits), rep.Warns)
	}
	if !strings.Contains(hits[0].Detail, "cmd/ghost.go") {
		t.Fatalf("dead-assembly 应指向 cmd/ghost.go，实际: %s", hits[0].Detail)
	}
	if len(rep.Fails) != 0 {
		t.Fatalf("dead-assembly 只能是 warn，不能进 fails: %+v", rep.Fails)
	}
}

// 节点被标记 deleted 时，该文件不算「视图里存在」——组装点仍应报死配置。
// 边界条件：deleted 节点只为渲染保留，不代表当前分支里还有这个文件。
func TestCheckDeadAssemblyIgnoresDeletedNodes(t *testing.T) {
	tg := &Target{
		Meta:     TargetMeta{Version: 1},
		Domains:  []TargetDomain{{ID: "d_cmd", Type: "logic", Paths: []string{"cmd/**"}}},
		Assembly: []string{"cmd/gone.go"},
	}
	v := mkView(map[string][2]string{"g": {"main", "cmd/gone.go"}}, nil, nil)
	n := v.Nodes["g"]
	n.Status = "deleted"
	v.Nodes["g"] = n

	rep := Check(tg, v)
	found := false
	for _, w := range rep.Warns {
		if w.Kind == "dead-assembly" {
			found = true
		}
	}
	if !found {
		t.Fatalf("deleted 节点不应让组装点算「命中」，实际 warns: %+v", rep.Warns)
	}
}
