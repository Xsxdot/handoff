// Package projectid 计算项目的机器无关身份。
//
// 职责：
//   - NormalizeGitURL：把同一仓库的各种 git URL 写法折叠成可比对的规范串
//   - FromOrigin：由 origin 派生 project_id（跨机一致、可离线计算）
//
// 边界：
//   - 纯函数包：无 I/O、无日志、无数据库、无 git 调用，只依赖标准库
//   - 不判断 URL 是否真的可访问、不解析 DNS、不做 host 别名等价
//   - 不做持久化：表里存的始终是原始 origin，本包只产出比对/派生用的值
//
// 为什么单独成包而不是放 proto 或 agentd：internal/store 的迁移与
// internal/agentd 的登记/解析都要算 project_id，而 store 不能导入 agentd
// （会成引用环），proto 又定了「纯类型包、无业务逻辑」的边界。
package projectid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// idLen 是 project_id 的十六进制字符数。
//
// 为什么 16 位（64 bit）而不是全量 64 位：它要出现在日志、错误报文与将来的
// Web 项目树里，给人读；64 bit 的碰撞概率对「一台机器上几十个项目」的量级
// 远远够用，而全量 sha256 会把有用信息挤出视线。
const idLen = 16

// NormalizeGitURL 把 git 远程地址折叠成可比对的规范串。
//
// 参数：
//   - raw: 原始 URL，如 git@github.com:xushixin/handoff.git
//
// 返回：
//   - 规范串，如 github.com/xushixin/handoff；输入为空白时返回空串
//
// 注意：
//   - 仅用于**比对与派生**，位置表里存的始终是原始 URL
//   - 只把首段（host）转小写：路径段在部分 git 服务端是大小写敏感的，
//     整串转小写有把两个不同仓库折叠到一起的风险
//   - 不做的事：不解析 DNS、不做 host 别名等价（github.com 与其镜像不视为同一个）
func NormalizeGitURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// 1) 剥 scheme
	for _, p := range []string{"ssh://", "git://", "https://", "http://"} {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			s = s[len(p):]
			break
		}
	}
	// 2) 剥 user@ 前缀（只在首个 '/' 之前找 '@'，避免误伤路径里的 '@'）
	if i := strings.IndexByte(s, '@'); i >= 0 {
		if j := strings.IndexByte(s, '/'); j < 0 || i < j {
			s = s[i+1:]
		}
	}
	// 3) 首个 '/' 之前的 ':' 有两种含义，分别处理：
	//    - scp-like 分隔符（github.com:owner/repo）→ 换成 '/'
	//    - 端口（github.com:22/owner/repo）→ 整段丢弃
	//    不处理的话，同一仓库的 ssh 与 https 写法永远匹配不上。
	if c := strings.IndexByte(s, ':'); c >= 0 {
		slash := strings.IndexByte(s, '/')
		if slash < 0 || c < slash {
			rest := s[c+1:]
			seg := rest
			if k := strings.IndexByte(rest, '/'); k >= 0 {
				seg = rest[:k]
			}
			if seg != "" && strings.IndexFunc(seg, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
				// 纯数字=端口，连同它一起丢掉
				s = s[:c] + rest[len(seg):]
			} else {
				s = s[:c] + "/" + rest
			}
		}
	}
	// 4) 去尾部 '/' 与 '.git'（顺序不能反：形如 ".../repo.git/" 两者都要去掉）
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	// 5) 首段（host）转小写
	if i := strings.IndexByte(s, '/'); i > 0 {
		s = strings.ToLower(s[:i]) + s[i:]
	} else {
		s = strings.ToLower(s)
	}
	return s
}

// FromOrigin 由 origin 地址派生 project_id。
//
// 参数：
//   - originURL: 仓库的 origin 原始地址
//
// 返回：
//   - 16 位十六进制串；originURL 归一化后为空时返回空串（调用方据此判「算不出身份」）
//
// 注意：
//   - 这是**纯函数**：每台机器各算各的，同一个 origin 必然得到同一个值。
//     跨机引用因此不需要任何中心服务或协调协议
//   - 取 sha256 而非直接用归一化串：归一化串含 '/' 与大小写，做主键与
//     URL 路径段都要转义；定长十六进制串没有这些麻烦
func FromOrigin(originURL string) string {
	norm := NormalizeGitURL(originURL)
	if norm == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])[:idLen]
}
