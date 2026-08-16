package projectid

import "testing"

// TestNormalizeGitURLFolds 验证同一仓库的各种写法折叠成同一个规范串。
// 用例沿用被替换的 agentd/reporegistry_test.go，行为不得回退。
func TestNormalizeGitURLFolds(t *testing.T) {
	const want = "github.com/Xsxdot/handoff"
	for _, raw := range []string{
		"git@github.com:Xsxdot/handoff.git",
		"git@github.com:Xsxdot/handoff",
		"https://github.com/Xsxdot/handoff.git",
		"https://github.com/Xsxdot/handoff/",
		"http://GitHub.com/Xsxdot/handoff.git",
		"ssh://git@github.com/Xsxdot/handoff.git",
		"ssh://git@github.com:22/Xsxdot/handoff.git",
	} {
		if got := NormalizeGitURL(raw); got != want {
			t.Errorf("NormalizeGitURL(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := NormalizeGitURL("   "); got != "" {
		t.Errorf("空白输入应归一化为空串，got %q", got)
	}
	if NormalizeGitURL("git@github.com:a/x.git") == NormalizeGitURL("git@github.com:b/x.git") {
		t.Error("不同 owner 的同名仓库被错误折叠")
	}
}

// TestFromOriginIsStableAndDistinct 验证 project_id 的两条性质：
// 同仓库各写法必得同一个 ID（跨机一致的基础）；不同仓库必得不同 ID。
func TestFromOriginIsStableAndDistinct(t *testing.T) {
	id := FromOrigin("git@github.com:Xsxdot/handoff.git")
	if len(id) != 16 {
		t.Fatalf("project_id 长度 = %d, want 16（值 %q）", len(id), id)
	}
	for _, raw := range []string{
		"https://github.com/Xsxdot/handoff",
		"https://github.com/Xsxdot/handoff.git",
		"ssh://git@GitHub.com/Xsxdot/handoff.git",
	} {
		if got := FromOrigin(raw); got != id {
			t.Errorf("FromOrigin(%q) = %q, want %q", raw, got, id)
		}
	}
	if FromOrigin("git@github.com:Xsxdot/tk.git") == id {
		t.Error("不同仓库得到了相同的 project_id")
	}
	if got := FromOrigin("  "); got != "" {
		t.Errorf("空 origin 应返回空 ID，got %q", got)
	}
}
