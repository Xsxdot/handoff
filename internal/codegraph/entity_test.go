package codegraph

import (
	"strings"
	"testing"
)

func TestEntityBasic(t *testing.T) {
	v, repo := loadFixtureView(t)
	r, err := EntityLookup(v, repo, "Task")
	if err != nil {
		t.Fatal(err)
	}
	if r.Model.ID != "m_task" || r.Model.Anchor != "ok" || r.Model.Line != 2 {
		t.Fatalf("主卡: %+v", r.Model)
	}
	if len(r.Typed) != 1 || r.Typed[0].ID != "n_save" || r.Typed[0].Anchor != "ok" || r.Typed[0].Via != "direct" {
		t.Fatalf("typed 投影: %+v", r.Typed)
	}
	foundDirect := false
	for _, site := range r.Handroll {
		if site.ID == "n_do" && site.Anchor == "ok" && site.Via == "direct" {
			foundDirect = true
		}
	}
	if !foundDirect {
		t.Fatalf("handroll 投影: %+v", r.Handroll)
	}
	if !r.ProjScanned || r.Warning != "" {
		t.Fatalf("盘点状态: scanned=%v warning=%q", r.ProjScanned, r.Warning)
	}
}

func TestEntityTwinMergesRemoteSites(t *testing.T) {
	v, repo := loadFixtureView(t)
	r, err := EntityLookup(v, repo, "Task")
	if err != nil {
		t.Fatal(err)
	}
	foundTwin := false
	for _, twin := range r.Twins {
		if twin.ID == "m_task_ts" {
			foundTwin = true
		}
	}
	if !foundTwin {
		t.Fatalf("孪生卡片: %+v", r.Twins)
	}
	foundRemote := false
	for _, site := range r.Handroll {
		if site.ID == "m_task_ts" && site.Via == "twin:m_task_ts" {
			foundRemote = true
		}
	}
	if !foundRemote {
		t.Fatalf("孪生侧手搭投影未并入: %+v", r.Handroll)
	}
}

func TestEntityUnscannedWarns(t *testing.T) {
	v, repo := loadFixtureView(t)
	n := v.Nodes["m_task"]
	n.ProjScanned = false
	v.Nodes["m_task"] = n
	r, err := EntityLookup(v, repo, "Task")
	if err != nil {
		t.Fatal(err)
	}
	if r.Warning == "" || !strings.Contains(r.Warning, "未做投影盘点") {
		t.Fatalf("未盘点警示: %q", r.Warning)
	}
}

func TestEntityNotModel(t *testing.T) {
	v, repo := loadFixtureView(t)
	_, err := EntityLookup(v, repo, "n_do")
	if err == nil || !strings.Contains(err.Error(), "不是 model") {
		t.Fatalf("非 model 查询错误: %v", err)
	}
}

func TestEntityGoPreferredOnTie(t *testing.T) {
	v, repo := loadFixtureView(t)
	r, err := EntityLookup(v, repo, "Task")
	if err != nil {
		t.Fatal(err)
	}
	if r.Model.ID != "m_task" || !strings.HasPrefix(r.Model.File, "svc/") {
		t.Fatalf("Go 主卡优先: %+v", r.Model)
	}
	foundTS := false
	for _, twin := range r.Twins {
		if twin.ID == "m_task_ts" {
			foundTS = true
		}
	}
	if !foundTS {
		t.Fatalf("TS 侧应进入 Twins: %+v", r.Twins)
	}
}
