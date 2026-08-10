// 本文件是仓库登记的**纯逻辑**层：把「用户在 --repo 里写了什么」翻译成
// 「executor 应该在哪个目录工作」。
//
// 职责：
//   - normalizeGitURL：把同一仓库的各种 URL 写法折叠成可比对的规范串
//   - looksLikePath：判别用户输入是路径还是登记名
//   - resolveRepoInput：按「路径 / 登记名 / 省略」三分支解析出仓库路径
//
// 边界：
//   - 不碰数据库：登记条目由调用方查好后以切片传入
//   - 不碰 git、不碰文件系统：路径是否真的存在由 EnsureRepoUsable 另行判定
//   - 不碰 HTTP：错误只用哨兵表达，状态码映射在 server.go
//
// 为什么单独成文件且刻意保持纯净：这段规则是 dispatch 的必经之路，一旦错了
// 就会把任务派到错误的仓库上。纯函数才能表驱动穷举 + 变异检验。
package agentd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
)

// 错误哨兵：
//   - ErrRepoNotRegistered：按名字查不到，或省略 --repo 时 origin 零命中
//   - ErrRepoAmbiguous：省略 --repo 时 origin 匹配到多条登记
//
// 两者都映射为 400（调用方先解决请求本身的问题），见 server.go 的 writeDispatchError。
var (
	ErrRepoNotRegistered = errors.New("仓库未登记")
	ErrRepoAmbiguous     = errors.New("origin 匹配到多条登记，无法自动选择")
)

// pathRunes 是「这是一个路径而不是登记名」的特征字符集合。
//
// 为什么不止 '/'：类 Unix 执行机上只判 '/' 已经够用，但 Windows 绝对路径
// C:\repos\x 既不含 '/' 也会被误判成登记名。多这两个字符可以让规则不依赖
// B37（prochost Windows 实现）的搁置状态。
//
// 反向误判（登记名含这三个字符）由 repoadmin.go 的 validateRepoName 在登记
// 入口强制拦下——本包注释不得再假设「登记名天然不含」，那是无人保证的假设。
const pathRunes = `/\:`

// looksLikePath 报告 s 是否应被当作路径处理。
//
// 参数：
//   - s: 用户在 --repo 里写的原始字符串
//
// 返回：
//   - true=当路径（走今天的原有行为，不碰登记表）；false=当登记名
func looksLikePath(s string) bool {
	return strings.ContainsAny(s, pathRunes)
}

// normalizeGitURL 把 git 远程地址折叠成可比对的规范串。
//
// 参数：
//   - raw: 原始 URL，如 git@github.com:xushixin/handoff.git
//
// 返回：
//   - 规范串，如 github.com/xushixin/handoff；输入为空白时返回空串
//
// 注意：
//   - 仅用于**比对**，登记表里存的始终是原始 URL
//   - 只把首段（host）转小写：路径段在部分 git 服务端是大小写敏感的，
//     整串转小写有把两个不同仓库折叠到一起的风险
//   - 不做的事：不解析 DNS、不做 host 别名等价（github.com 与其镜像不视为同一个）
func normalizeGitURL(raw string) string {
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

// repoNames 把登记条目压成一行逗号分隔的名字串，供拒绝报文使用。
// 报文必须带得走「本机登记了什么」——远程派发时审核者读不到执行机的
// agentd.log，一句干巴巴的「未登记」等于让他去猜。
func repoNames(entries []proto.Repo) string {
	if len(entries) == 0 {
		return "（本机尚无任何登记）"
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return strings.Join(names, ", ")
}

// resolveRepoInput 把用户输入解析成执行机上的仓库路径。
//
// 参数：
//   - input: 用户 --repo 的原始取值（路径 / 登记名 / 空）
//   - originURL: 审核者 cwd 的 origin 地址；cwd 不是 git 仓库时为空
//   - entries: 本机全部登记条目
//
// 返回：
//   - 解析出的仓库路径
//   - 错误：ErrRepoNotRegistered / ErrRepoAmbiguous / errBadDispatchRequest，
//     均映射 400，且报文自带可行动线索（已登记的名字或候选清单）
//
// 注意：
//   - input 含路径特征字符时**完全绕开登记表**，保持今天的行为不变
//   - 本函数不判断路径是否真的存在，那是 EnsureRepoUsable 的职责
func resolveRepoInput(input, originURL string, entries []proto.Repo) (string, error) {
	if looksLikePath(input) {
		log().Info("仓库解析：按路径直通", "input", input)
		return input, nil
	}
	if input != "" {
		for _, e := range entries {
			if e.Name == input {
				log().Info("仓库解析：登记名命中", "name", input, "path", e.Path)
				return e.Path, nil
			}
		}
		log().Warn("仓库解析被拒：登记名查不到", "name", input, "registered", repoNames(entries))
		return "", fmt.Errorf("%w: %q；本机已登记的仓库：%s（用 handoff repo ls 查看，或 handoff repo add 先落地）",
			ErrRepoNotRegistered, input, repoNames(entries))
	}
	if originURL == "" {
		log().Warn("仓库解析被拒：未给 --repo 且无 origin 可匹配")
		return "", fmt.Errorf("%w: 未指定 --repo，且当前目录不是 git 仓库，无法自动匹配已登记仓库",
			errBadDispatchRequest)
	}
	want := normalizeGitURL(originURL)
	var hits []proto.Repo
	for _, e := range entries {
		if normalizeGitURL(e.OriginURL) == want {
			hits = append(hits, e)
		}
	}
	switch len(hits) {
	case 1:
		log().Info("仓库解析：origin 唯一命中", "origin", originURL,
			"name", hits[0].Name, "path", hits[0].Path)
		return hits[0].Path, nil
	case 0:
		log().Warn("仓库解析被拒：origin 零命中", "origin", originURL, "registered", repoNames(entries))
		return "", fmt.Errorf("%w: 当前仓库 %s 尚未登记到本机；本机已登记的仓库：%s（用 handoff repo add 落地它）",
			ErrRepoNotRegistered, originURL, repoNames(entries))
	default:
		log().Warn("仓库解析被拒：origin 多命中", "origin", originURL, "candidates", repoNames(hits))
		return "", fmt.Errorf("%w: 当前仓库 %s 在本机登记了 %d 处：%s；请用 --repo <名字> 指定",
			ErrRepoAmbiguous, originURL, len(hits), repoNames(hits))
	}
}
