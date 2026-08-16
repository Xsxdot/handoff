// 项目树测试：分组、单机不变式、探测降级。
package agentd

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TestBuildLocalTreeGroupsAndProbes 断言：按 project_id 分组、单机每项目恒 1 个
// location、工作树被真实探到。
func TestBuildLocalTreeGroupsAndProbes(t *testing.T) {
	env := newTestAgentdEnv(t)
	repo := initGitRepoWithOrigin(t, "git@github.com:x/handoff.git")
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff", Path: repo,
		OriginURL: "git@github.com:x/handoff.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}

	tree, err := env.srv.buildLocalTree(context.Background())
	if err != nil {
		t.Fatalf("buildLocalTree: %v", err)
	}
	if len(tree.Projects) != 1 {
		t.Fatalf("项目数 = %d，期望 1", len(tree.Projects))
	}
	p := tree.Projects[0]
	if p.ProjectID != "aaaa111122223333" || p.Name != "handoff" {
		t.Errorf("项目头信息错：%+v", p)
	}
	// 单机不变式：每个项目恒 0 或 1 个 location（ADR-0008）
	if len(p.Locations) != 1 {
		t.Fatalf("单机 locations 长度 = %d，必须 ≤1", len(p.Locations))
	}
	loc := p.Locations[0]
	if loc.Machine != "" {
		t.Errorf("本机的 machine 必须是空串，实得 %q", loc.Machine)
	}
	if loc.ProbeError != "" {
		t.Errorf("真实仓库不该有探测错误：%s", loc.ProbeError)
	}
	if len(loc.Workspaces) != 1 || !loc.Workspaces[0].IsMain {
		t.Errorf("应探到一个主工作树：%+v", loc.Workspaces)
	}
}

// TestBuildLocalTreeBrokenLocationStillListed 是「登记还在、目录已失效」的核心
// 断言：那条 location 必须仍然出现在树里，带 probe_error，而不是整棵树报错。
func TestBuildLocalTreeBrokenLocationStillListed(t *testing.T) {
	env := newTestAgentdEnv(t)
	gone := filepath.Join(t.TempDir(), "gone")
	os.MkdirAll(gone, 0o755)
	os.RemoveAll(gone)
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "bbbb444455556666", Name: "ghost", Path: gone,
		OriginURL: "git@github.com:x/ghost.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}
	tree, err := env.srv.buildLocalTree(context.Background())
	if err != nil {
		t.Fatalf("目录失效不该让整棵树报错：%v", err)
	}
	if len(tree.Projects) != 1 || len(tree.Projects[0].Locations) != 1 {
		t.Fatalf("失效的 location 必须仍然列出：%+v", tree)
	}
	if tree.Projects[0].Locations[0].ProbeError == "" {
		t.Error("失效的 location 必须带 probe_error")
	}
}

// TestProjectTreeEndpointShape 断言端点返回 200 且空库时 projects/unowned 都是 []。
func TestProjectTreeEndpointShape(t *testing.T) {
	env := newTestAgentdEnv(t)
	var resp proto.ProjectTreeResp
	code := env.getJSONCode(t, "/api/projects/tree", &resp)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if resp.Projects == nil || resp.Unowned == nil {
		t.Fatal("空列表必须序列化为 [] 而非 null")
	}
	if resp.Machines != nil {
		t.Error("单机请求不该带 machines 栏（omitempty）")
	}
}
