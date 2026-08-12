// project ls --tree 测试：三层缩进输出，以及不带 --tree 时与 B62 输出逐字节一致。
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// treeFixture 是 fake agentd 对 GET /api/projects/tree 的固定响应。
var treeFixture = proto.ProjectTreeResp{
	Projects: []proto.ProjectNode{
		{
			ProjectID: "a1b2c3d4e5f60718", OriginURL: "git@github.com:x/handoff.git",
			Name: "handoff",
			Locations: []proto.ProjectLocationNode{
				{
					Machine: "", Name: "handoff", Path: "/Users/dev/handoff",
					Workspaces: []proto.Workspace{
						{Path: "/Users/dev/handoff", Branch: "main", Head: "482aab1", IsMain: true},
						{Path: "/Users/dev/.handoff/worktrees/w1", Branch: "handoff/w1", Head: "9e12a3b", Managed: true},
					},
				},
				{
					Machine: "devbox", Name: "handoff", Path: "/home/dev/handoff",
					Workspaces: []proto.Workspace{},
					ProbeError: "path doesn't exist",
				},
			},
		},
	},
	Unowned: []string{"dirty-row"},
}

// fakeProjectTreeServer 起一个服务 /api/projects/tree 的假 agentd。
func fakeProjectTreeServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects/tree":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(treeFixture)
		case "/api/projects":
			// B62 契约对照用的扁平列表
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]proto.ProjectLocation{
				{ProjectID: "a1b2c3d4e5f60718", Name: "handoff", Path: "/Users/dev/handoff",
					OriginURL: "git@github.com:x/handoff.git", Status: "有效"},
			})
		default:
			t.Errorf("非预期路径: %s", r.URL.Path)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// TestProjectLsTreeIndented 断言 --tree 输出三层缩进、机器名与探测失败可见。
func TestProjectLsTreeIndented(t *testing.T) {
	resetW3aFlags(t)
	ts := fakeProjectTreeServer(t)
	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, ts.URL, "测试令牌", []string{"project", "ls", "--tree"})
	if err != nil {
		t.Fatalf("project ls --tree: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"handoff  (a1b2c3d4e5f60718)  git@github.com:x/handoff.git",
		"* main", "handoff/w1", "[任务工作树]", "本机", "devbox", "← 探测失败"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺 %q：\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// 第一行是项目，第二行是「本机」location（缩进 2），第三行是主工作树（缩进 4）
	if len(lines) < 4 {
		t.Fatalf("输出行数不足（期望三层缩进）：\n%s", out)
	}
	if !strings.HasPrefix(lines[0], "handoff") {
		t.Errorf("第一行应是项目：%q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  本机") {
		t.Errorf("第二行应是缩进的本机 location：%q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "    ") {
		t.Errorf("第三行应是缩进 4 的工作树：%q", lines[2])
	}
}

// TestProjectLsWithoutTreeMatchesB62 断言不带 --tree 时输出与 B62 逐字节一致。
//
// 这是验收面最容易回归的地方：B62 的文档与测试都按扁平形态写，任何改动都会
// 破坏既有消费方。
func TestProjectLsWithoutTreeMatchesB62(t *testing.T) {
	resetW3aFlags(t)
	ts := fakeProjectTreeServer(t)
	var stdout bytes.Buffer
	err := runSubcommandForTest(t, &stdout, ts.URL, "测试令牌", []string{"project", "ls"})
	if err != nil {
		t.Fatalf("project ls: %v", err)
	}
	want := "名字       路径                  状态  project_id        origin\n" +
		"handoff  /Users/dev/handoff  有效  a1b2c3d4e5f60718  git@github.com:x/handoff.git\n"
	if stdout.String() != want {
		t.Fatalf("不带 --tree 的输出必须逐字节等于 B62 契约：\n实得: %q\n期望: %q", stdout.String(), want)
	}
}
