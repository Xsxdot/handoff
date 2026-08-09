// gitidentity 包测试：规范化 repo identity 解析。
//
// 职责：
//   - 统一 HTTPS、SSH URL 和 scp-like remote 为 host/owner/repo 规范值
//   - 不保留 userinfo、token、scheme 或 .git 后缀
//
// 边界：
//   - 纯字符串解析，不访问网络、不执行 git 命令
package gitidentity

import "testing"

func TestCanonicalRepoIdentity(t *testing.T) {
	cases := []struct {
		name    string
		rawURL  string
		want    string
		wantErr bool
	}{
		{"github https", "https://github.com/owner/repo.git", "github.com/owner/repo", false},
		{"github https no .git", "https://github.com/owner/repo", "github.com/owner/repo", false},
		{"github ssh", "git@github.com:owner/repo.git", "github.com/owner/repo", false},
		{"github scp-like", "git@github.com:owner/repo", "github.com/owner/repo", false},
		{"github ssh full", "ssh://git@github.com/owner/repo.git", "github.com/owner/repo", false},
		{"http scheme", "http://example.com/a/b.git", "example.com/a/b", false},
		{"nested path", "https://github.com/org/team/repo.git", "github.com/org/team/repo", false},
		{"userinfo stripped", "https://user:pass@github.com/o/r.git", "github.com/o/r", false},
		{"token in https", "https://ghp_x@github.com/o/r.git", "github.com/o/r", false},
		{"localhost", "ssh://git@127.0.0.1:2222/team/repo.git", "127.0.0.1:2222/team/repo", false},
		{"gitlab https", "https://gitlab.com/group/sub/repo.git", "gitlab.com/group/sub/repo", false},
		{"empty", "", "", true},
		{"garbage", "not a url at all", "", true},
		{"no repo part", "https://github.com", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CanonicalRepoIdentity(c.rawURL)
			if c.wantErr {
				if err == nil {
					t.Fatalf("CanonicalRepoIdentity(%q) 应报错，实际 %q", c.rawURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalRepoIdentity(%q): %v", c.rawURL, err)
			}
			if got != c.want {
				t.Fatalf("CanonicalRepoIdentity(%q) = %q, want %q", c.rawURL, got, c.want)
			}
		})
	}
}

// TestCanonicalRepoIdentitySameAcrossForms 验证同一仓库的不同 URL 形态归一为
// 同一 identity——这是「本机与远端目录组成同一 Project」的前提。
func TestCanonicalRepoIdentitySameAcrossForms(t *testing.T) {
	forms := []string{
		"https://github.com/o/r.git",
		"https://github.com/o/r",
		"git@github.com:o/r.git",
		"git@github.com:o/r",
		"ssh://git@github.com/o/r.git",
		"https://user:token@github.com/o/r",
	}
	var first string
	for i, u := range forms {
		got, err := CanonicalRepoIdentity(u)
		if err != nil {
			t.Fatalf("形态 %d (%s): %v", i, u, err)
		}
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("形态 %d (%s) = %q, want 与首形态一致 %q", i, u, got, first)
		}
	}
}
