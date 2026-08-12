// 本文件是 agentd 侧「项目 × 本机位置」的操作层。
//
// 职责：
//   - RegisterProject：登记本机已有的一份代码，或先 clone 再登记
//   - ListProjects：列出位置，并**现场探测**每条的实际状态（漂移可见化）
//   - UnregisterProject：注销位置（只删记录，不动磁盘）
//
// 边界：
//   - 不做解析：派发时「这个请求指哪个项目」由 projectresolve.go 的纯函数决定
//   - 不做持久化细节：SQL 在 internal/store/projects.go
//   - 不算 project_id：那是 internal/projectid 的纯函数
//   - 不删磁盘上的仓库：注销只影响登记，磁盘由人自己处置
//   - clone 在**本机**执行（agentd 就跑在这台机器上），不走 ssh——
//     用的是这台机器自己的 git 凭据
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/projectid"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
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
	projectStatusOK      = "有效"
	projectStatusMissing = "路径不存在"
	projectStatusNotRepo = "不是 git 仓库"
)

// nameFallbackLimit 是名字冲突时的最大退让次数（handoff-2 … handoff-50）。
//
// 为什么要有上限：退让是个循环，没有上限时一张被写坏的表能让登记请求空转。
// 50 远超「一台机器上有 50 个同末段名的不同项目」的现实量级。
const nameFallbackLimit = 50

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

// projectOriginURL 读取仓库的 origin 地址。
//
// 参数：
//   - ctx: 控制 git 调用生命周期
//   - repo: 仓库路径
//
// 返回：
//   - origin 地址；仓库不可用或没有 origin 时返回包装 ErrRepoUnusable 的错误
//
// 注意：
//   - 没有 origin 的仓库拒绝登记：project_id 由 origin 派生，没有 origin 就
//     算不出身份，登记进来只会是一条永远引用不到的死记录
func projectOriginURL(ctx context.Context, repo string) (string, error) {
	out, stderr, err := gitRun(ctx, repo, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("%w: 读取 %s 的 origin 失败: %s: %v",
			ErrRepoUnusable, repo, strings.TrimSpace(stderr), err)
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", fmt.Errorf("%w: 仓库 %s 没有配置 origin remote", ErrRepoUnusable, repo)
	}
	return url, nil
}

// projectNameFromURL 从 git URL 末段派生缺省引用名（去掉 .git 后缀）。
//
// 例：git@github.com:xushixin/handoff.git → handoff
func projectNameFromURL(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// validateProjectName 校验引用名的合法性，返回包装 errBadDispatchRequest 的错误。
//
// 规则：
//   - 空名或纯空白 → 拒
//   - 名字含 / \ : → 拒：clone 落点是 repo_root/<名字>，这三个字符会让它跑到别处
//   - 名字为 . / .. 或含 .. 路径段 → 拒：会让落点逃出 repo_root
//
// 为什么必须入口拦：名字由 origin 末段派生或人工指定，没人保证它干净；
// 而它会被直接拼进文件系统路径。
func validateProjectName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: 项目名不能为空", errBadDispatchRequest)
	}
	if strings.ContainsAny(name, `/\:`) {
		return fmt.Errorf("%w: 项目名 %q 含路径特征字符（/ \\ :），会让克隆落点跑到 repo_root 之外",
			errBadDispatchRequest, name)
	}
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%w: 项目名 %q 含 . 或 .. 路径段，会让克隆落点逃出 repo_root",
				errBadDispatchRequest, name)
		}
	}
	return nil
}

// RegisterProject 登记一个项目位置（各形态与分派见 RegisterProjectReq 的决策表）。
//
// 参数：
//   - ctx: 控制整组 git 调用的生命周期
//   - req: 登记请求
//
// 返回：
//   - 落库后的位置条目
//   - 错误：ErrRepoUnusable（400，路径不是仓库/无 origin/clone 失败）、
//     ErrProjectOriginMismatch（400，路径上是另一个项目）、
//     ErrProjectAlreadyExists（409，项目/名字/路径已被占用，或落点已存在）、
//     errBadDispatchRequest（400，参数缺失或名字非法）
//
// 注意：
//   - **登记在 clone 成功之后才落库**：反过来会在 clone 失败时留下一条指向
//     不存在路径的死记录
//   - clone 的落点若已存在则直接拒绝，绝不往里 clone、绝不覆盖
func (m *Manager) RegisterProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	m.log.Info("登记项目请求", "origin", req.OriginURL, "name", req.Name, "path", req.Path)
	req.OriginURL = strings.TrimSpace(req.OriginURL)
	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)
	if req.OriginURL != "" && strings.HasPrefix(req.OriginURL, "-") {
		// git 会把以 - 开头的参数解释为选项——参数注入面，与 ErrBadBaseBranch 同源。
		return proto.ProjectLocation{}, fmt.Errorf("%w: origin_url 不允许以 - 开头", errBadDispatchRequest)
	}
	if req.Path != "" {
		return m.registerAtPath(ctx, req)
	}
	// 无 path：只能 clone 到 repo_root，必须带 origin——否则既无落点也无项目身份。
	if req.OriginURL == "" {
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: 不带 path 时必须提供 origin_url（否则既无落点也无项目身份）",
			errBadDispatchRequest)
	}
	return m.cloneAndRegisterProject(ctx, req)
}

// registerAtPath 处理 Path 非空的请求：按目录是否存在在「登记已有仓」与
// 「clone 到该 Path」间分派。
//
// 目录不存在时：无 OriginURL → 400（无 URL 无法创建）；有 OriginURL →
// cloneToPathAndRegister。目录存在时一律走 registerExistingProject——
// 绝不往已存在的目录里 clone（那里是不是仓库、是不是本项目，由 inspect 校验说）。
func (m *Manager) registerAtPath(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	_, err := os.Stat(req.Path)
	if errors.Is(err, os.ErrNotExist) {
		if req.OriginURL == "" {
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 路径 %s 不存在，且未提供 origin_url，无法 clone",
				errBadDispatchRequest, req.Path)
		}
		return m.cloneToPathAndRegister(ctx, req)
	}
	if err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 探查路径 %s: %v", ErrRepoUnusable, req.Path, err)
	}
	return m.registerExistingProject(ctx, req)
}

// cloneToPathAndRegister 把 origin clone 到调用方指定的 dest（req.Path）再登记。
//
// 与 cloneAndRegisterProject 的区别只在落点：这里落点是 req.Path 原样使用，
// 那里落点是 repo_root/<name> 并带「落点已存在则尝试认领」的归并逻辑。这里
// 不做认领——落点已存在的请求根本不会进入本函数（由 registerAtPath 分流到
// registerExistingProject），任意 path 上也没有 repo_root 那种「rm 后磁盘残留」
// 的自动登记场景。
func (m *Manager) cloneToPathAndRegister(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	// 幂等短路：同一项目已登记过就直接返回已有行，必须发生在 clone 之前——
	// 与 clone 形态同一份理由（见 cloneAndRegisterProject），重复登记同一个
	// 项目不应再 clone 出第二份。
	pid := projectid.FromOrigin(req.OriginURL)
	if pid != "" {
		if existing, ok, err := m.registeredProjectByID(pid); err != nil {
			return proto.ProjectLocation{}, err
		} else if ok {
			m.log.Info("项目位置已存在，幂等返回",
				"project_id", existing.ProjectID, "name", existing.Name, "path", existing.Path)
			existing.Status = projectStatusOK
			return existing, nil
		}
	}
	// 与 cloneAndRegisterProject 同款提前校验：显式给的 name 若不合法，等 clone
	// 跑完 persistProject 再拦就晚了，会留下已 clone 未登记的孤儿目录。空名走
	// projectNameFromURL 派生，由 persistProject 统一校验。
	if req.Name != "" {
		if err := validateProjectName(req.Name); err != nil {
			m.log.Warn("克隆登记被拒：项目名非法", "name", req.Name, "cause", err)
			return proto.ProjectLocation{}, err
		}
	}
	dest := req.Path
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 创建落点父目录 %s: %v", ErrRepoUnusable, parent, err)
	}
	m.log.Info("开始克隆项目到指定路径", "origin", req.OriginURL, "dest", dest)
	start := time.Now()
	// gitRun 以 parent 为 cwd 执行；-- 分隔符防止 URL/路径被当成选项。
	if _, stderr, err := gitRun(ctx, parent, "clone", "--", req.OriginURL, dest); err != nil {
		m.log.Error("克隆到指定路径失败", "origin", req.OriginURL, "dest", dest,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return proto.ProjectLocation{}, fmt.Errorf("%w: 克隆 %s 到 %s 失败: %s: %v",
			ErrRepoUnusable, req.OriginURL, dest, strings.TrimSpace(stderr), err)
	}
	m.log.Info("克隆到指定路径完成", "origin", req.OriginURL, "dest", dest,
		"elapsed_ms", time.Since(start).Milliseconds())
	name := req.Name
	if name == "" {
		name = projectNameFromURL(req.OriginURL)
	}
	return m.persistProject(name, dest, req.OriginURL)
}

// registeredProjectByID 在位置表里按 project_id 查已登记的位置。
//
// 返回：
//   - 命中条目的拷贝；未命中时零值
//   - 是否命中
//   - 查库失败时的错误
//
// 为什么单独抽一个助手：登记是**幂等**操作，clone 形态与已有目录形态都要在
// 各自流程的合适节点查一次表（clone 形态要在 clone 之前，已有目录形态要在
// 归并+读 origin+校验之后），两边共用同一条「查表 + 按 project_id 匹配」逻辑。
func (m *Manager) registeredProjectByID(pid string) (proto.ProjectLocation, bool, error) {
	entries, err := m.st.ListProjectLocations()
	if err != nil {
		m.log.Error("登记前读位置表失败", "cause", err)
		return proto.ProjectLocation{}, false, err
	}
	for _, e := range entries {
		if e.ProjectID == pid {
			return e, true, nil
		}
	}
	return proto.ProjectLocation{}, false, nil
}

// sameLocation 比较两个路径是否指向同一位置。
//
// 为什么不能直接比字符串：git 在 linked worktree 里返回的 common-dir 是
// 主仓 .git 的**符号链接解析后**绝对路径（macOS 上 /var → /private/var），
// 而首次登记存进表的路径来自调用方目录（未解析符号链接）。两个都解析再比，
// 才能让「linked worktree 归并后幂等命中」在 macOS 上成立。
//
// 路径已不存在时（EvalSymlinks 失败）退回直接比较 Clean 后的串。
func sameLocation(a, b string) bool {
	if ra, errA := filepath.EvalSymlinks(a); errA == nil {
		if rb, errB := filepath.EvalSymlinks(b); errB == nil {
			return ra == rb
		}
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// inspectRepoDir 检查一个目录是否是一份可用的、带 origin 的 git 仓库，并归并到
// 主工作树。
//
// 三步序列（EnsureRepoUsable → MainWorktreeRoot → 现读 origin）在「登记已有目录」
// 与「克隆落点认领」两条路径共用，绝不能复制两份——校验逻辑一分开就会漂移。
//
// 参数：
//   - ctx: 控制 git 调用生命周期
//   - dir: 待检查的目录
//
// 返回：
//   - root: 归并主工作树后的仓库根（绝对路径）
//   - origin: 该仓库 origin 的现读值（非空；读不到即返回错误）
//   - err: 任一步失败时的包装错误（ErrRepoUnusable 系列）
func (m *Manager) inspectRepoDir(ctx context.Context, dir string) (root, origin string, err error) {
	if err := EnsureRepoUsable(ctx, dir); err != nil {
		m.log.Warn("登记被拒：路径不是可用的 git 仓库", "path", dir, "cause", err)
		return "", "", err
	}
	// 归并主工作树：位置表一个项目只允许一行，而 linked worktree 与主仓
	// origin 相同、project_id 相同（spec §5）。
	root, err = MainWorktreeRoot(ctx, dir)
	if err != nil {
		m.log.Warn("登记被拒：归并主工作树失败", "path", dir, "cause", err)
		return "", "", err
	}
	// origin 由 agentd 在本机现读，而不是采信调用方上送的值：登记的是这个
	// 路径上真实存在的仓库，它的 origin 才是权威。
	origin, err = projectOriginURL(ctx, root)
	if err != nil {
		m.log.Warn("登记被拒：读不到 origin", "path", root, "cause", err)
		return "", "", err
	}
	return root, origin, nil
}

// registerExistingProject 登记本机上已存在的一份代码（Path 已确认存在）。
//
// OriginURL 可空：空时采用 inspectRepoDir 现读的 actual 作为项目身份与落库值
// （Web「只填 path」主路径——磁盘上的仓库本身就是权威，不要求调用方复述）；
// 非空时仅作一致性校验，不一致仍报 ErrProjectOriginMismatch。
// 落库 origin 永远用 actual，不采信请求串里未校验的写法。
func (m *Manager) registerExistingProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	root, actual, err := m.inspectRepoDir(ctx, req.Path)
	if err != nil {
		return proto.ProjectLocation{}, err
	}
	if req.OriginURL == "" {
		m.log.Info("登记已有目录（origin 由磁盘现读）", "path", root, "origin", actual)
	} else if projectid.FromOrigin(actual) != projectid.FromOrigin(req.OriginURL) {
		// 校验一致：挡住「路径敲错但恰好指到另一个真实仓库」这种脏登记。
		m.log.Warn("登记被拒：路径上的仓库不是请求的项目",
			"path", root, "actual_origin", actual, "want_origin", req.OriginURL)
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: %s 上的 origin 是 %s，而请求的项目是 %s；换个路径，或去掉 --path 让本机自己 clone",
			ErrProjectOriginMismatch, root, actual, req.OriginURL)
	}
	// 幂等短路：同一项目已经登记在**同一位置**时直接返回已有行。自动登记
	// （cmd/project.go 的 registerProjectBothHops）的 hop-1 在第二次及以后的
	// 每次派发都会把「本机自己」重复登记到这里——同一项目、同一位置，重复声明
	// 同一个事实不是错误，不短路的话整条自动登记链会被 409 打断。
	//
	// 位置比较必须在归并**之后**：linked worktree 与主仓同 project_id，归并后
	// 的 root 就是主仓路径，因此用 worktree 路径重复登记会幂等命中主仓那行。
	pid := projectid.FromOrigin(actual)
	if pid != "" {
		if existing, ok, err := m.registeredProjectByID(pid); err != nil {
			return proto.ProjectLocation{}, err
		} else if ok {
			if sameLocation(existing.Path, root) {
				m.log.Info("项目位置已存在，幂等返回",
					"project_id", existing.ProjectID, "name", existing.Name, "path", existing.Path)
				existing.Status = projectStatusOK
				return existing, nil
			}
			// 同一项目已登记在别处：真正的冲突（ADR-0008：一台机器一个项目只能
			// 有一个位置），报文指向已有位置。
			m.log.Warn("登记被拒：该项目在本机已有位置",
				"project_id", pid, "existing", existing.Path, "requested", root)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 项目 %s 在本机已登记于 %s；要换位置先 handoff project rm %s",
				ErrProjectAlreadyExists, existing.Name, existing.Path, existing.Name)
		}
	}
	return m.persistProject(req.Name, root, actual)
}

// cloneAndRegisterProject 先 clone 再登记。
func (m *Manager) cloneAndRegisterProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	// 幂等短路：同一项目已经登记过就直接返回已有行，**必须发生在 clone 之前**——
	// 自动登记（registerProjectBothHops）的 hop-1 在第二次及以后的每次派发都会
	// 带着空 Path 打到本机，不短路的话每派发一次就重复 clone 一份，repo_root
	// 会被同名落点塞满（而落点已存在还会反过来 409）。
	pid := projectid.FromOrigin(req.OriginURL)
	if pid != "" {
		if existing, ok, err := m.registeredProjectByID(pid); err != nil {
			return proto.ProjectLocation{}, err
		} else if ok {
			m.log.Info("项目位置已存在，幂等返回",
				"project_id", existing.ProjectID, "name", existing.Name, "path", existing.Path)
			existing.Status = projectStatusOK
			return existing, nil
		}
	}
	name := req.Name
	if name == "" {
		name = projectNameFromURL(req.OriginURL)
	}
	// 校验必须早于 dest 计算：名字含 .. 时 dest=repo_root/<名字> 会逃出 repo_root，
	// 等 persistProject 再拦就晚了（落点已建/克隆已跑）。
	if err := validateProjectName(name); err != nil {
		m.log.Warn("克隆登记被拒：项目名非法", "name", name, "cause", err)
		return proto.ProjectLocation{}, err
	}
	if m.cfg.RepoRoot == "" {
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: 本机未配置 repo_root，无法决定克隆落点（在 config.yaml 里配它）", errBadDispatchRequest)
	}
	dest := filepath.Join(m.cfg.RepoRoot, name)
	// 落点已存在时先尝试**认领**，而不是直接拒绝：`project rm` 只删登记不动磁盘，
	// rm 之后磁盘上的克隆还在——若它本来就是本项目，直接登记它就能让「rm 后再派发
	// → 自动重登记」成立（spec §12 的验收场景）。认领走与已有目录登记相同的三步
	// 校验（共用 inspectRepoDir，绝不复制第二份）；认领失败（不是仓库 / 读不到
	// origin / 属于另一个项目）才 409，绝不自动删目录、绝不改名——那是替用户做决定。
	if _, err := os.Stat(dest); err == nil {
		root, actual, ierr := m.inspectRepoDir(ctx, dest)
		if ierr != nil {
			m.log.Warn("克隆被拒：落点已存在但认领失败", "dest", dest, "cause", ierr)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 克隆落点 %s 已存在，但它不是本项目可认领的仓库（%v）；该目录不是 git 仓库或读不到 origin，请人工处置（agentd 不会自动删除或改名）",
				ErrProjectAlreadyExists, dest, ierr)
		}
		if projectid.FromOrigin(actual) != projectid.FromOrigin(req.OriginURL) {
			m.log.Warn("克隆被拒：落点已存在且属于另一个项目",
				"dest", dest, "actual_origin", actual, "want_origin", req.OriginURL)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 克隆落点 %s 已存在，其 origin 是 %s（另一个项目），请求的项目是 %s；请人工处置该目录（agentd 不会自动删除或改名）",
				ErrProjectAlreadyExists, dest, actual, req.OriginURL)
		}
		// 落点就是本项目：认领登记，不 clone（origin 现读的就是权威，见
		// inspectRepoDir）。认领后仓库可能落后于远端——这里不 fetch，派发时
		// baseline 缺提交自然会去拉（既有机制）。
		m.log.Info("落点已存在且就是本项目，直接登记，未重复 clone",
			"dest", dest, "project_id", projectid.FromOrigin(actual))
		return m.persistProject(name, root, actual)
	} else if !errors.Is(err, os.ErrNotExist) {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 探查落点 %s: %v", ErrRepoUnusable, dest, err)
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 创建落点父目录 %s: %v", ErrRepoUnusable, parent, err)
	}
	m.log.Info("开始克隆项目", "origin", req.OriginURL, "dest", dest)
	start := time.Now()
	// gitRun 以 parent 为 cwd 执行；-- 分隔符防止 URL/路径被当成选项。
	if _, stderr, err := gitRun(ctx, parent, "clone", "--", req.OriginURL, dest); err != nil {
		m.log.Error("克隆项目失败", "origin", req.OriginURL, "dest", dest,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return proto.ProjectLocation{}, fmt.Errorf("%w: 克隆 %s 到 %s 失败: %s: %v",
			ErrRepoUnusable, req.OriginURL, dest, strings.TrimSpace(stderr), err)
	}
	m.log.Info("克隆项目完成", "origin", req.OriginURL, "dest", dest,
		"elapsed_ms", time.Since(start).Milliseconds())
	return m.persistProject(name, dest, req.OriginURL)
}

// persistProject 把一条位置落库：算 project_id、定引用名、翻译冲突哨兵。
func (m *Manager) persistProject(name, path, origin string) (proto.ProjectLocation, error) {
	// 落库路径必须归一化为绝对路径（spec §4.1：登记时 Abs+Clean）。clone 落点
	// 来自 cfg.RepoRoot，配置写成相对路径（如 repos/）时 dest 也是相对的，
	// 不处理就会落成一条指向相对路径的死位置。
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		m.log.Warn("登记落库被拒：路径无法归一化为绝对路径", "path", path, "cause", err)
		return proto.ProjectLocation{}, fmt.Errorf("%w: 归一化路径 %s: %v", errBadDispatchRequest, path, err)
	}
	path = absPath
	pid := projectid.FromOrigin(origin)
	if pid == "" {
		m.log.Warn("登记落库被拒：origin 算不出 project_id", "origin", origin, "path", path)
		return proto.ProjectLocation{}, fmt.Errorf("%w: origin %q 归一化后为空，算不出项目身份",
			errBadDispatchRequest, origin)
	}
	entries, err := m.st.ListProjectLocations()
	if err != nil {
		m.log.Error("登记落库前读位置表失败", "cause", err)
		return proto.ProjectLocation{}, err
	}
	// 同一项目已有位置时直接拒，并把已有路径写进报文——比等主键冲突再报
	// 一句「已存在」有用得多（ADR-0008：一台机器一个项目只能有一个位置）。
	for _, e := range entries {
		if e.ProjectID == pid {
			m.log.Warn("登记被拒：该项目在本机已有位置",
				"project_id", pid, "existing", e.Path, "requested", path)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 项目 %s 在本机已登记于 %s；要换位置先 handoff project rm %s",
				ErrProjectAlreadyExists, e.Name, e.Path, e.Name)
		}
	}
	if name == "" {
		name = projectNameFromURL(origin)
	}
	if err := validateProjectName(name); err != nil {
		m.log.Warn("登记落库被拒：项目名非法", "name", name, "cause", err)
		return proto.ProjectLocation{}, err
	}
	name, err = uniqueProjectName(name, entries)
	if err != nil {
		m.log.Warn("登记落库被拒：名字退让次数用尽", "name", name, "cause", err)
		return proto.ProjectLocation{}, err
	}
	loc := proto.ProjectLocation{
		ProjectID: pid, Name: name, Path: path,
		OriginURL: origin, CreatedAt: time.Now(),
	}
	if err := m.st.CreateProjectLocation(&loc); err != nil {
		if errors.Is(err, store.ErrProjectDuplicate) {
			m.log.Warn("登记被拒：项目/名字/路径已被占用",
				"project_id", pid, "name", name, "path", loc.Path, "cause", err)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 项目 %s、名字 %q 或路径 %s 已被登记（handoff project ls 查看）",
				ErrProjectAlreadyExists, pid, name, loc.Path)
		}
		m.log.Error("登记落库失败", "name", name, "path", loc.Path, "cause", err)
		return proto.ProjectLocation{}, err
	}
	m.log.Info("项目位置登记完成",
		"project_id", pid, "name", name, "path", loc.Path, "origin", origin)
	loc.Status = projectStatusOK
	return loc, nil
}

// uniqueProjectName 在 base 已被占用时退让为 base-2、base-3……
//
// 参数：
//   - base: 期望的引用名
//   - entries: 本机现有位置
//
// 返回：
//   - 未被占用的名字；退让 nameFallbackLimit 次仍冲突时返回 errBadDispatchRequest
//
// 注意：
//   - 只在**不同项目**撞名时才会走到这里：同项目重复登记已在 persistProject
//     前段按 project_id 拒掉了
func uniqueProjectName(base string, entries []proto.ProjectLocation) (string, error) {
	taken := make(map[string]bool, len(entries))
	for _, e := range entries {
		taken[e.Name] = true
	}
	if !taken[base] {
		return base, nil
	}
	for i := 2; i <= nameFallbackLimit; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if !taken[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: 名字 %s 及其 -2..-%d 变体全部被占用，请用 [名字] 参数显式指定",
		errBadDispatchRequest, base, nameFallbackLimit)
}

// ListProjects 列出本机全部项目位置，并现场探测每条的实际状态。
//
// 参数：
//   - ctx: 控制探测用的 git 调用生命周期
//
// 返回：
//   - 位置列表（Status 已填充）；查库失败时返回错误
//
// 注意：
//   - 探测是登记与文件系统漂移的可见化手段。探测失败不影响列出——
//     状态本身就是要报给人看的结果，不是错误
func (m *Manager) ListProjects(ctx context.Context) ([]proto.ProjectLocation, error) {
	locs, err := m.st.ListProjectLocations()
	if err != nil {
		m.log.Error("列出项目位置失败", "cause", err)
		return nil, err
	}
	for i := range locs {
		locs[i].Status = probeProjectStatus(ctx, locs[i].Path)
	}
	m.log.Info("列出项目位置", "count", len(locs))
	return locs, nil
}

// probeProjectStatus 探测一条位置指向的路径当前是什么状态。
func probeProjectStatus(ctx context.Context, path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return projectStatusMissing
	}
	if err := EnsureRepoUsable(ctx, path); err != nil {
		return projectStatusNotRepo
	}
	return projectStatusOK
}

// UnregisterProject 注销一条项目位置。
//
// 参数：
//   - ctx: 上下文（当前实现不发起 git 调用，保留以对齐其余操作签名）
//   - name: 项目引用名
//
// 返回：
//   - 错误：位置不存在时 store.ErrNotFound（404）；路径被活跃任务占用时
//     ErrWorkdirBusy（409）
//
// 注意：
//   - **只删登记，永不删磁盘上的仓库**。磁盘上那份是不是还要留，由人自己决定
func (m *Manager) UnregisterProject(ctx context.Context, name string) error {
	loc, err := m.st.GetProjectLocationByName(name)
	if err != nil {
		m.log.Warn("注销项目失败：位置不存在", "name", name, "cause", err)
		return err
	}
	tasks, err := m.st.ActiveTasksByRepoPath(loc.Path)
	if err != nil {
		m.log.Error("注销项目前查活跃任务失败", "name", name, "path", loc.Path, "cause", err)
		return err
	}
	if len(tasks) > 0 {
		ids := make([]string, 0, len(tasks))
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
		m.log.Warn("注销项目被拒：仓库被活跃任务占用",
			"name", name, "path", loc.Path, "tasks", strings.Join(ids, ","))
		return fmt.Errorf("%w: 项目 %s（%s）上还有 %d 个活跃任务（%s）；先 done 或 stop 它们",
			ErrWorkdirBusy, name, loc.Path, len(tasks), strings.Join(ids, ", "))
	}
	if err := m.st.DeleteProjectLocation(name); err != nil {
		m.log.Error("注销项目落库失败", "name", name, "cause", err)
		return err
	}
	m.log.Info("项目位置已注销（磁盘仓库未动）", "name", name, "path", loc.Path)
	return nil
}
