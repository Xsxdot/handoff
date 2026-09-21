// 本文件承载「项目 × 本机位置」登记域的共享词汇：哨兵、状态常量、请求形态
// 与校验/比较助手。登记编排（Manager.RegisterProject 各形态）在
// internal/orchestration，HTTP handler 在 internal/agentd；本文件是两边共用
// 的纯逻辑落点。
//
// B233.19：自 internal/agentd/projectadmin.go 迁入登记/索引逻辑（handler 留
// gateway）；编排包由 agentd.* 引用改为直接引用本包。
package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrProjectAlreadyExists 表示位置冲突或克隆落点已被占用，映射 409。
//
// 与 ErrRepoUnusable（400）的区别：那是「请求本身有问题，改了再来」，
// 这是「当前状态与请求冲突」——和 ErrDirtyWorktree / ErrWorkdirBusy 同层级。
var ErrProjectAlreadyExists = errors.New("项目位置冲突或克隆落点已存在")

// ErrProjectOriginMismatch 表示调用方声称的项目与该路径上实际仓库的 origin 不符，映射 400。
//
// 为什么必须单列一个哨兵：这是自动化最容易造出的脏登记——路径敲错但恰好指到
// 另一个真实仓库。若并进 ErrRepoUnusable，报文就变成含糊的「仓库不可用」，
// 而人需要的是「你说的是 A，那儿实际是 B」。
var ErrProjectOriginMismatch = errors.New("路径上的仓库与请求的项目不是同一个")

// project ls 的状态取值。不落库，每次列出时现场探得。
const (
	ProjectStatusOK      = "有效"
	ProjectStatusMissing = "路径不存在"
	ProjectStatusNotRepo = "不是 git 仓库"
)

// NameFallbackLimit 是名字冲突时的最大退让次数（handoff-2 … handoff-50）。
//
// 为什么要有上限：退让是个循环，没有上限时一张被写坏的表能让登记请求空转。
// 50 远超「一台机器上有 50 个同末段名的不同项目」的现实量级。
const NameFallbackLimit = 50

// RegisterProjectReq 是登记一个项目位置的请求。
//
// 形态由 Path 是否给出 / 路径是否存在 / OriginURL 是否非空共同决定（三态决策表）：
//   - Path 空 + OriginURL 空 → 400：既无身份也无落点
//   - Path 空 + OriginURL 有 → 由本机 clone 到 cfg.RepoRoot/<Name>（或认领已有落点）
//   - Path 非空且目录存在 → 登记已有仓（OriginURL 可省，省则现读 origin）
//   - Path 非空且目录不存在 + OriginURL 空 → 400：无 URL 无法创建
//   - Path 非空且目录不存在 + OriginURL 有 → clone 到该 Path 再登记
//   - 其余非法组合 → 400
//
// Path 的形态约束：必须是**绝对路径**。相对路径与 ~ 一律 400——clone 落点与
// 落库路径的解析基准不同，猜错的代价是一条指向不存在路径的死记录。
//
// 为什么没有 Clone 布尔位：形态已被 Path + 文件系统状态 + OriginURL 是否为空
// 完全决定，多一个布尔位只会多出一组无意义的非法组合。
//
// Name 可省，此时由 OriginURL（请求给出或现读的实际 origin）末段派生；
// 它只是人可读引用，不参与身份判定。
type RegisterProjectReq struct {
	OriginURL string
	Name      string
	Path      string
}

// ProjectNameFromURL 从 git URL 末段派生缺省引用名（去掉 .git 后缀）。
//
// 例：git@github.com:Xsxdot/handoff.git → handoff
//
// why 分隔符集合里有反斜杠：origin 可以是 Windows 本地路径（`C:\work\x.git`）。
// git URL 的四种形态（https/ssh/scp 简写/file）都不含反斜杠，把它加进集合
// 对既有形态零影响，只有本地路径 origin 会走到这一支。
func ProjectNameFromURL(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), `/\`)
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, `/:\`); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// ValidateProjectName 校验引用名的合法性，返回包装 ErrBadDispatchRequest 的错误。
//
// 规则：
//   - 空名或纯空白 → 拒
//   - 名字含 / \ : → 拒：clone 落点是 repo_root/<名字>，这三个字符会让它跑到别处
//   - 名字为 . / .. 或含 .. 路径段 → 拒：会让落点逃出 repo_root
//
// 为什么必须入口拦：名字由 origin 末段派生或人工指定，没人保证它干净；
// 而它会被直接拼进文件系统路径。
func ValidateProjectName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: 项目名不能为空", ErrBadDispatchRequest)
	}
	if strings.ContainsAny(name, `/\:`) {
		return fmt.Errorf("%w: 项目名 %q 含路径特征字符（/ \\ :），会让克隆落点跑到 repo_root 之外",
			ErrBadDispatchRequest, name)
	}
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%w: 项目名 %q 含 . 或 .. 路径段，会让克隆落点逃出 repo_root",
				ErrBadDispatchRequest, name)
		}
	}
	return nil
}

// SameLocation 比较两个路径是否指向同一位置。
//
// 为什么不能直接比字符串：git 在 linked worktree 里返回的 common-dir 是
// 主仓 .git 的**符号链接解析后**绝对路径（macOS 上 /var → /private/var），
// 而首次登记存进表的路径来自调用方目录（未解析符号链接）。两个都解析再比，
// 才能让「linked worktree 归并后幂等命中」在 macOS 上成立。
//
// 路径已不存在时（EvalSymlinks 失败）退回直接比较 Clean 后的串。
func SameLocation(a, b string) bool {
	if ra, errA := filepath.EvalSymlinks(a); errA == nil {
		if rb, errB := filepath.EvalSymlinks(b); errB == nil {
			return ra == rb
		}
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
