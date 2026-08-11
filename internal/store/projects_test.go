package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/xushixin/handoff/internal/projectid"
	"github.com/xushixin/handoff/internal/proto"
)

// newProjectStore 开一个临时库，供本文件的用例共用。
func newProjectStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// mustCreateLoc 按 origin 算出 project_id 后落一条位置。
func mustCreateLoc(t *testing.T, st *Store, name, path, origin string) proto.ProjectLocation {
	t.Helper()
	loc := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin(origin), Name: name, Path: path,
		OriginURL: origin, CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(&loc); err != nil {
		t.Fatalf("CreateProjectLocation(%s): %v", name, err)
	}
	return loc
}

// TestProjectLocationCRUD 覆盖增查列删的正常路径。
func TestProjectLocationCRUD(t *testing.T) {
	st := newProjectStore(t)
	a := mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:xushixin/handoff.git")
	mustCreateLoc(t, st, "tk", "/w/tk", "git@github.com:xushixin/tk.git")

	got, err := st.GetProjectLocationByName("handoff")
	if err != nil {
		t.Fatalf("GetProjectLocationByName: %v", err)
	}
	if got.ProjectID != a.ProjectID || got.Path != "/w/handoff" {
		t.Fatalf("查回的行不对: %+v", got)
	}

	list, err := st.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(list) != 2 || list[0].Name != "handoff" || list[1].Name != "tk" {
		t.Fatalf("列表应按名字字典序返回 2 行，got %+v", list)
	}

	if err := st.DeleteProjectLocation("handoff"); err != nil {
		t.Fatalf("DeleteProjectLocation: %v", err)
	}
	if _, err := st.GetProjectLocationByName("handoff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后再查应 ErrNotFound，got %v", err)
	}
	if err := st.DeleteProjectLocation("handoff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删不存在的行应 ErrNotFound（不能静默成功），got %v", err)
	}
}

// TestProjectLocationPrimaryKeyEnforcesOneLocationPerProject 验证 ADR-0008
// 的「一台机器上一个项目最多一个位置」由主键直接强制：同一个 origin 的
// 第二次登记（哪怕换了名字和路径）必须被拒。
func TestProjectLocationPrimaryKeyEnforcesOneLocationPerProject(t *testing.T) {
	st := newProjectStore(t)
	mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:xushixin/handoff.git")
	dup := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin("https://github.com/xushixin/handoff"),
		Name:      "handoff-again", Path: "/w/handoff-again",
		OriginURL: "https://github.com/xushixin/handoff", CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(&dup); !errors.Is(err, ErrProjectDuplicate) {
		t.Fatalf("同 origin 的第二个位置应被主键拒绝，got %v", err)
	}
}

// TestProjectLocationNameAndPathAreUnique 验证名字与路径各自唯一：
// 名字唯一是因为 --project <名字> 与 project rm <名字> 要靠它引用；
// 路径唯一是因为两个不同项目不能声称在同一个目录。
func TestProjectLocationNameAndPathAreUnique(t *testing.T) {
	st := newProjectStore(t)
	mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:xushixin/handoff.git")

	sameName := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin("git@github.com:other/handoff.git"),
		Name:      "handoff", Path: "/w/other", OriginURL: "git@github.com:other/handoff.git",
		CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(&sameName); !errors.Is(err, ErrProjectDuplicate) {
		t.Fatalf("重名应被拒，got %v", err)
	}
	samePath := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin("git@github.com:other/thing.git"),
		Name:      "thing", Path: "/w/handoff", OriginURL: "git@github.com:other/thing.git",
		CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(&samePath); !errors.Is(err, ErrProjectDuplicate) {
		t.Fatalf("同路径应被拒，got %v", err)
	}
}

// TestMigrateReposToProjectLocations 验证旧 repos 表迁入新表：算出 project_id、
// 同 origin 多行保留 created_at 最早的一条、迁完 DROP 掉旧表。
func TestMigrateReposToProjectLocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	// 先手工造一个「旧库」：建 repos 表并塞三行，其中两行同 origin。
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := old.Exec(`CREATE TABLE repos (
  name TEXT PRIMARY KEY, path TEXT NOT NULL UNIQUE,
  origin_url TEXT NOT NULL, created_at TIMESTAMP NOT NULL)`); err != nil {
		t.Fatalf("建旧表: %v", err)
	}
	rows := []struct{ name, path, origin, created string }{
		{"handoff", "/w/handoff", "git@github.com:xushixin/handoff.git", "2026-01-01T00:00:00Z"},
		{"handoff-wt", "/w/handoff-wt", "https://github.com/xushixin/handoff", "2026-02-01T00:00:00Z"},
		{"tk", "/w/tk", "git@github.com:xushixin/tk.git", "2026-01-15T00:00:00Z"},
	}
	for _, r := range rows {
		if _, err := old.Exec(`INSERT INTO repos VALUES (?, ?, ?, ?)`,
			r.name, r.path, r.origin, r.created); err != nil {
			t.Fatalf("塞旧行 %s: %v", r.name, err)
		}
	}
	old.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open（应顺带跑完迁移）: %v", err)
	}
	defer st.Close()

	locs, err := st.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("同 origin 的两行应折叠成一行，共 2 行，got %d: %+v", len(locs), locs)
	}
	got, err := st.GetProjectLocationByName("handoff")
	if err != nil {
		t.Fatalf("最早的那条应被保留: %v", err)
	}
	if got.Path != "/w/handoff" {
		t.Fatalf("保留的应是 created_at 最早的一条，got %q", got.Path)
	}

	// 旧表必须已被 DROP，且第二次 Open 是无操作（幂等）。
	var n int
	if err := st.db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='repos'`).Scan(&n); err != nil {
		t.Fatalf("查 sqlite_master: %v", err)
	}
	if n != 0 {
		t.Fatal("迁移完成后 repos 表应被 DROP")
	}
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("第二次 Open 应无操作: %v", err)
	}
	defer st2.Close()
	locs2, err := st2.ListProjectLocations()
	if err != nil || len(locs2) != 2 {
		t.Fatalf("第二次 Open 不应改变数据: locs=%d err=%v", len(locs2), err)
	}
}
