// 任务归属 join 的纯索引测试：命中 / 未登记 / 已注销 / 遗留 linked worktree 四态。
// （B233.19 自 agentd/projectjoin_test.go 随实现迁入；/api/tasks 注解的 HTTP
// 集成断言留在 gateway：agentd/b23319_retained_test.go。）
package workspace

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestProjectIndexLookup(t *testing.T) {
	idx := NewProjectIndex([]proto.ProjectLocation{
		{ProjectID: "aaaa111122223333", Name: "handoff", Path: "/home/dev/handoff"},
		{ProjectID: "bbbb444455556666", Name: "tk", Path: "/home/dev/tk/"},
	})

	cases := []struct {
		name     string
		repoPath string
		want     string
	}{
		{"命中", "/home/dev/handoff", "aaaa111122223333"},
		{"命中（尾斜杠归一）", "/home/dev/tk", "bbbb444455556666"},
		{"命中（非规范路径归一）", "/home/dev/./handoff", "aaaa111122223333"},
		// 已注销 = 表里没这行了；未登记 = 从来没登记过。对 join 是同一件事：
		// 诚实显示未归属，而不是留一列陈旧数据说谎
		{"未登记", "/home/dev/other", ""},
		// B62 之前派发的任务，repo_path 可能指向 linked worktree（当时不归并）。
		// 这类任务 join 不中，显示未归属——这是诚实的降级，不做回填
		{"遗留 linked worktree", "/home/dev/handoff/.worktrees/w1", ""},
		{"空路径", "", ""},
	}
	for _, c := range cases {
		if got := idx.ProjectIDOf(c.repoPath); got != c.want {
			t.Errorf("%s: projectIDOf(%q) = %q，期望 %q", c.name, c.repoPath, got, c.want)
		}
	}
}
