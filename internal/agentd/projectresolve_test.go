package agentd

import (
	"errors"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/projectid"
	"github.com/xushixin/handoff/internal/proto"
)

// locFixture 是解析用例共用的位置表快照。
func locFixture() []proto.ProjectLocation {
	mk := func(name, path, origin string) proto.ProjectLocation {
		return proto.ProjectLocation{
			ProjectID: projectid.FromOrigin(origin), Name: name, Path: path, OriginURL: origin,
		}
	}
	return []proto.ProjectLocation{
		mk("handoff", "/root/work/handoff", "git@github.com:xushixin/handoff.git"),
		mk("tk", "/root/work/tk", "https://github.com/xushixin/tk.git"),
	}
}

// TestResolveProject 覆盖 project_id / project_name / 都空 三种入参 × 命中与否。
func TestResolveProject(t *testing.T) {
	handoffID := projectid.FromOrigin("git@github.com:xushixin/handoff.git")
	// 同一仓库的另一种写法必须算出同一个 ID——这是跨机引用成立的前提。
	altID := projectid.FromOrigin("https://github.com/xushixin/handoff")

	tests := []struct {
		name     string
		id       string
		projName string
		entries  []proto.ProjectLocation
		wantPath string
		wantErr  error
	}{
		{name: "project_id 命中", id: handoffID, entries: locFixture(), wantPath: "/root/work/handoff"},
		{name: "另一种 URL 写法算出的 id 同样命中", id: altID, entries: locFixture(), wantPath: "/root/work/handoff"},
		{name: "project_id 未命中（表非空）", id: "deadbeefdeadbeef", entries: locFixture(), wantErr: ErrProjectNotRegistered},
		{name: "project_id 未命中（表为空）", id: handoffID, entries: nil, wantErr: ErrProjectNotRegistered},
		{name: "project_name 命中", projName: "tk", entries: locFixture(), wantPath: "/root/work/tk"},
		{name: "project_name 未命中", projName: "nope", entries: locFixture(), wantErr: ErrProjectNotRegistered},
		{name: "两者都空", entries: locFixture(), wantErr: errBadDispatchRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveProject(tt.id, tt.projName, tt.entries)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want errors.Is(..., %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if got.Path != tt.wantPath {
				t.Fatalf("path = %q, want %q", got.Path, tt.wantPath)
			}
		})
	}
}

// TestResolveProjectErrorsAreActionable 验证拒绝报文带得走「本机登记了什么」，
// 而不是一句干巴巴的「未登记」——远程派发时审核者读不到执行机的 agentd.log，
// 报文是他唯一的线索。
func TestResolveProjectErrorsAreActionable(t *testing.T) {
	_, err := resolveProject("deadbeefdeadbeef", "", locFixture())
	for _, want := range []string{"handoff", "/root/work/handoff", "tk", "/root/work/tk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文 %q 未包含 %q", err.Error(), want)
		}
	}
	_, err = resolveProject("deadbeefdeadbeef", "", nil)
	if !strings.Contains(err.Error(), "本机尚无任何项目") {
		t.Errorf("空表报文应说明本机尚无任何项目，got %q", err.Error())
	}
}
