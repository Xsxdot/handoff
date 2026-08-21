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
