package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

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
