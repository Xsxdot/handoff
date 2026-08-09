// Package gitidentity 提供 Git 仓库的规范化身份解析。
//
// 职责：
//   - CanonicalRepoIdentity：把 HTTPS、SSH URL 和 scp-like remote 统一为
//     host/owner/repo 规范值
//
// 边界：
//   - 纯字符串解析，不访问网络、不执行 git 命令
//   - 返回的 identity 不保留 userinfo、token、scheme 或 .git 后缀
//
// 为什么需要规范化身份：同一仓库可能有多种 URL 形态（https/git@/ssh://），
// 判定「本机与远端目录属于同一 Project」时必须以同一 identity 为准。
package gitidentity

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// CanonicalRepoIdentity 把远程 URL 规范化为 host/owner/repo 形式。
//
// 参数：
//   - rawURL: git remote 或 clone URL，支持 https/http/ssh/git@host:path
//
// 返回：
//   - 形如 "host/owner/repo"（不含 scheme、userinfo、端口后缀的 .git）；
//     无法解析时返回错误
//
// 注意：
//   - scp-like 形态 "git@github.com:o/r.git" 需要特判冒号分隔
//   - 端口保留在 host 里（如 127.0.0.1:2222/team/repo），因为不同端口可能是
//     不同 git 服务，误合并会串仓库
func CanonicalRepoIdentity(rawURL string) (string, error) {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return "", fmt.Errorf("URL 为空")
	}

	// scp-like: user@host:owner/repo(.git)。必须是「无 scheme」形态——
	// https://user:pass@host/... 含 @ 与 :，但那是 URL 形态，走下方 url.Parse。
	if !strings.Contains(s, "://") && strings.Contains(s, "@") && strings.Contains(s, ":") {
		at := strings.Index(s, "@")
		colon := strings.Index(s[at:], ":")
		host := s[at+1 : at+colon]
		repoPath := s[at+colon+1:]
		if host == "" || repoPath == "" {
			return "", fmt.Errorf("无法解析 scp-like URL %q", rawURL)
		}
		return normalize(host, repoPath)
	}

	// URL 形态（https/http/ssh://git@host/...）
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("解析 URL %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL %q 缺少 host", rawURL)
	}
	repoPath := strings.TrimPrefix(u.Path, "/")
	if repoPath == "" {
		return "", fmt.Errorf("URL %q 缺少仓库路径", rawURL)
	}
	return normalize(u.Host, repoPath)
}

// normalize 去掉 userinfo 后缀与 .git，返回 host/owner/repo。
func normalize(host, repoPath string) (string, error) {
	// host 可能含 userinfo 残留（如 http://user:pass@host 已由 url.Parse 剥离，
	// scp-like 分支已去掉 user@；此处防御性再剥一次）
	if at := strings.Index(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	clean := path.Clean(strings.TrimSuffix(repoPath, ".git"))
	clean = strings.TrimSuffix(clean, "/")
	if clean == "" || clean == "." || clean == ".." {
		return "", fmt.Errorf("规范化路径为空: %q", repoPath)
	}
	return host + "/" + clean, nil
}
