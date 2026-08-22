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
	if store.Models != 2 || !reflect.DeepEqual(store.Interfaces, []string{"m_task"}) {
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
