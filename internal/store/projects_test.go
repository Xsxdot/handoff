package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Xsxdot/handoff/internal/projectid"
	"github.com/Xsxdot/handoff/internal/proto"
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
	a := mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:Xsxdot/handoff.git")
	mustCreateLoc(t, st, "tk", "/w/tk", "git@github.com:Xsxdot/tk.git")

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
	mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:Xsxdot/handoff.git")
	dup := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin("https://github.com/Xsxdot/handoff"),
		Name:      "handoff-again", Path: "/w/handoff-again",
		OriginURL: "https://github.com/Xsxdot/handoff", CreatedAt: time.Now(),
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
	mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:Xsxdot/handoff.git")

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

// TestUpdateProjectLocationRenames 改名成功，且 project_id 不变——
// 身份由 origin 算出，改名不许动它，否则任务与工作树会与项目失联。
func TestUpdateProjectLocationRenames(t *testing.T) {
	st := newProjectStore(t)
	a := mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:Xsxdot/handoff.git")

	got, err := st.UpdateProjectLocation("handoff", "handoff-renamed", "")
	if err != nil {
		t.Fatalf("UpdateProjectLocation 改名: %v", err)
	}
	if got.Name != "handoff-renamed" || got.Path != "/w/handoff" {
		t.Fatalf("改名的记录不对: %+v", got)
	}
	if got.ProjectID != a.ProjectID {
		t.Fatalf("改名后 project_id 变了: got %s want %s", got.ProjectID, a.ProjectID)
	}
	back, err := st.GetProjectLocationByName("handoff-renamed")
	if err != nil {
		t.Fatalf("改名后再按新名字取: %v", err)
	}
	if back.ProjectID != a.ProjectID {
		t.Fatalf("库里的 project_id 也变了: got %s want %s", back.ProjectID, a.ProjectID)
	}
}

// TestUpdateProjectLocationChangesPath 改路径成功，project_id 同样不变。
func TestUpdateProjectLocationChangesPath(t *testing.T) {
	st := newProjectStore(t)
	a := mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:Xsxdot/handoff.git")

	got, err := st.UpdateProjectLocation("handoff", "", "/w/handoff-new")
	if err != nil {
		t.Fatalf("UpdateProjectLocation 改路径: %v", err)
	}
	if got.Name != "handoff" || got.Path != "/w/handoff-new" {
		t.Fatalf("改路径的记录不对: %+v", got)
	}
	if got.ProjectID != a.ProjectID {
		t.Fatalf("改路径后 project_id 变了: got %s want %s", got.ProjectID, a.ProjectID)
	}
	back, err := st.GetProjectLocationByName("handoff")
	if err != nil {
		t.Fatalf("改路径后再取: %v", err)
	}
	if back.ProjectID != a.ProjectID {
		t.Fatalf("库里的 project_id 也变了: got %s want %s", back.ProjectID, a.ProjectID)
	}
}

// TestUpdateProjectLocationRejectsDuplicateName 新名字已被别的位置占用 →
// ErrProjectDuplicate（上层映射 409）。
func TestUpdateProjectLocationRejectsDuplicateName(t *testing.T) {
	st := newProjectStore(t)
	mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:Xsxdot/handoff.git")
	mustCreateLoc(t, st, "other", "/w/other", "git@github.com:Xsxdot/tk.git")

	_, err := st.UpdateProjectLocation("handoff", "other", "")
	if !errors.Is(err, ErrProjectDuplicate) {
		t.Fatalf("改名撞别的位置的名字应 ErrProjectDuplicate，got %v", err)
	}
	back, err := st.GetProjectLocationByName("handoff")
	if err != nil {
		t.Fatalf("撞名后原记录应还在: %v", err)
	}
	if back.Name != "handoff" || back.Path != "/w/handoff" {
		t.Fatalf("撞名后原记录应保持原样: %+v", back)
	}
}

// TestUpdateProjectLocationNotFound 不存在的名字 → ErrNotFound。
func TestUpdateProjectLocationNotFound(t *testing.T) {
	st := newProjectStore(t)

	_, err := st.UpdateProjectLocation("no-such-project", "new", "/w/new")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的名字应 ErrNotFound，got %v", err)
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
		{"handoff", "/w/handoff", "git@github.com:Xsxdot/handoff.git", "2026-01-01T00:00:00Z"},
		{"handoff-wt", "/w/handoff-wt", "https://github.com/Xsxdot/handoff", "2026-02-01T00:00:00Z"},
		{"tk", "/w/tk", "git@github.com:Xsxdot/tk.git", "2026-01-15T00:00:00Z"},
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

// TestMigrateReposToProjectLocationsRollsBackOnFailure 验证迁移**整体事务**：
// 中途某行 INSERT 撞 project_id 主键失败时，已插入的行一并回滚、旧 repos 表保留，
// 且清理冲突后可重跑成功——否则库会死在「半迁入 + 旧表还在」的中间态上，
// 下一次 Open 重跑必然在第一批行的主键冲突上硬失败，库从此打不开。
func TestMigrateReposToProjectLocationsRollsBackOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	// 手工造「旧库」：repos 里先放一行能成功的 tk（时间更早，升序时先插），
	// 再放一行会撞主键的 handoff；project_locations 预置同 project_id 的行。
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := old.Exec(`CREATE TABLE repos (
  name TEXT PRIMARY KEY, path TEXT NOT NULL UNIQUE,
  origin_url TEXT NOT NULL, created_at TIMESTAMP NOT NULL)`); err != nil {
		t.Fatalf("建旧表: %v", err)
	}
	if _, err := old.Exec(`CREATE TABLE project_locations (
  project_id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
  path TEXT NOT NULL UNIQUE, origin_url TEXT NOT NULL, created_at TIMESTAMP NOT NULL)`); err != nil {
		t.Fatalf("建新表: %v", err)
	}
	if _, err := old.Exec(`INSERT INTO repos VALUES (?, ?, ?, ?)`,
		"tk", "/w/tk", "git@github.com:Xsxdot/tk.git", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("塞 tk 行: %v", err)
	}
	if _, err := old.Exec(`INSERT INTO repos VALUES (?, ?, ?, ?)`,
		"handoff", "/w/handoff", "git@github.com:Xsxdot/handoff.git", "2026-01-15T00:00:00Z"); err != nil {
		t.Fatalf("塞 handoff 行: %v", err)
	}
	// 预占 handoff 的 project_id：迁移插它必撞主键（错误出现在第二行，
	// 第一行 tk 已经成功插入——正好验证「失败前的行被回滚」）。
	if _, err := old.Exec(`INSERT INTO project_locations VALUES (?, ?, ?, ?, ?)`,
		projectid.FromOrigin("git@github.com:Xsxdot/handoff.git"), "handoff-pre",
		"/w/handoff-pre", "git@github.com:Xsxdot/handoff.git", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("预占 project_id: %v", err)
	}
	old.Close()

	st, err := Open(path)
	if err == nil {
		st.Close()
		t.Fatal("迁移中途失败时 Open 应返回错误")
	}
	// 中间态必须被回滚干净：旧表还在、project_locations 只剩预占的那一行。
	probe, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	t.Cleanup(func() { probe.Close() })
	var n int
	if err := probe.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='repos'`).Scan(&n); err != nil {
		t.Fatalf("查 sqlite_master: %v", err)
	}
	if n != 1 {
		t.Fatal("迁移失败后 repos 表应仍在（DROP 未发生）")
	}
	if err := probe.QueryRow(`SELECT count(*) FROM project_locations`).Scan(&n); err != nil {
		t.Fatalf("查 project_locations: %v", err)
	}
	if n != 1 {
		t.Fatalf("回滚后 project_locations 应只剩预占的 1 行（tk 已插入却被回滚），got %d", n)
	}

	// 清掉冲突后重跑：迁移应可重试成功，不再把库锁死。
	if _, err := probe.Exec(`DELETE FROM project_locations WHERE name = 'handoff-pre'`); err != nil {
		t.Fatalf("清冲突行: %v", err)
	}
	probe.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("清冲突后重跑 Open 应成功: %v", err)
	}
	defer st2.Close()
	locs, err := st2.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("重跑后应迁入 2 行，got %d: %+v", len(locs), locs)
	}
	if err := st2.db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='repos'`).Scan(&n); err != nil {
		t.Fatalf("查 sqlite_master: %v", err)
	}
	if n != 0 {
		t.Fatal("重跑成功后 repos 表应被 DROP")
	}
}
