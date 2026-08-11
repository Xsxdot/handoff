package agentd

import (
	"errors"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestNormalizeGitURL 验证归一化把同一仓库的各种写法折叠成同一个串。
func TestNormalizeGitURL(t *testing.T) {
	const want = "github.com/xushixin/handoff"
	for _, raw := range []string{
		"git@github.com:xushixin/handoff.git",
		"git@github.com:xushixin/handoff",
		"https://github.com/xushixin/handoff.git",
		"https://github.com/xushixin/handoff/",
		"http://GitHub.com/xushixin/handoff.git",
		"ssh://git@github.com/xushixin/handoff.git",
		"ssh://git@github.com:22/xushixin/handoff.git",
	} {
		if got := normalizeGitURL(raw); got != want {
			t.Errorf("normalizeGitURL(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := normalizeGitURL("   "); got != "" {
		t.Errorf("空白输入应归一化为空串，got %q", got)
	}
	// 不同仓库不得被折叠到一起
	if normalizeGitURL("git@github.com:a/x.git") == normalizeGitURL("git@github.com:b/x.git") {
		t.Error("不同 owner 的同名仓库被错误折叠")
	}
}

// TestLooksLikePath 验证路径与登记名的判别。
func TestLooksLikePath(t *testing.T) {
	for _, s := range []string{"/root/work/handoff", `C:\repos\x`, "C:/repos/x"} {
		if !looksLikePath(s) {
			t.Errorf("looksLikePath(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"handoff", "my-repo", ""} {
		if looksLikePath(s) {
			t.Errorf("looksLikePath(%q) = true, want false", s)
		}
	}
}

// entriesFixture 是解析用例共用的登记表快照。
func entriesFixture() []proto.Repo {
	return []proto.Repo{
		{Name: "handoff", Path: "/root/work/handoff", OriginURL: "git@github.com:xushixin/handoff.git"},
		{Name: "tk", Path: "/root/work/tk", OriginURL: "https://github.com/xushixin/tk.git"},
		{Name: "handoff-2", Path: "/root/work/handoff-2", OriginURL: "https://github.com/xushixin/handoff"},
	}
}

// TestResolveRepoInput 覆盖三分支 × 命中数的全部组合。
func TestResolveRepoInput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		originURL string
		entries   []proto.Repo
		wantPath  string
		wantErr   error
	}{
		{
			name:  "含路径特征字符即当路径，不查登记表",
			input: "/some/where/else", originURL: "git@github.com:xushixin/handoff.git",
			entries: entriesFixture(), wantPath: "/some/where/else",
		},
		{
			name:  "登记表为空时路径依然直通",
			input: "/some/where/else", entries: nil, wantPath: "/some/where/else",
		},
		{
			name:  "短名命中登记",
			input: "tk", entries: entriesFixture(), wantPath: "/root/work/tk",
		},
		{
			name:  "短名查不到",
			input: "nope", entries: entriesFixture(), wantErr: ErrRepoNotRegistered,
		},
		{
			name:      "省略 --repo 且 origin 唯一命中",
			originURL: "https://github.com/xushixin/tk", entries: entriesFixture(),
			wantPath: "/root/work/tk",
		},
		{
			name:      "省略 --repo 且 origin 多命中",
			originURL: "git@github.com:xushixin/handoff.git", entries: entriesFixture(),
			wantErr: ErrRepoAmbiguous,
		},
		{
			name:      "省略 --repo 且 origin 零命中",
			originURL: "git@github.com:other/thing.git", entries: entriesFixture(),
			wantErr: ErrRepoNotRegistered,
		},
		{
			name:    "省略 --repo 且 cwd 不是 git 仓库",
			entries: entriesFixture(), wantErr: errBadDispatchRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRepoInput(tt.input, tt.originURL, tt.entries)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want errors.Is(..., %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if got != tt.wantPath {
				t.Fatalf("path = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

// TestResolveRepoInputErrorsAreActionable 验证拒绝报文带得走「本机登记了什么」，
// 而不是一句干巴巴的「未登记」——这是审核者读不到执行机日志时的唯一线索。
func TestResolveRepoInputErrorsAreActionable(t *testing.T) {
	_, err := resolveRepoInput("nope", "", entriesFixture())
	for _, want := range []string{"nope", "handoff", "tk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文 %q 未包含 %q", err.Error(), want)
		}
	}
	_, err = resolveRepoInput("", "git@github.com:xushixin/handoff.git", entriesFixture())
	for _, want := range []string{"handoff", "handoff-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("歧义报文 %q 未列出候选 %q", err.Error(), want)
		}
	}
}
