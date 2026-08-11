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
// 两种形态由 Path 是否为空决定：
//   - Path 非空：这台机器上已经有一份，用它（agentd 现读它的 origin 校验一致）
//   - Path 为空：由本机 clone 到 cfg.RepoRoot/<Name>
//
// 为什么没有 Clone 布尔位：形态已被 Path 完全决定，多一个布尔位只会多出
// 一组无意义的非法组合。
//
// Name 可省，此时由 OriginURL 末段派生；它只是人可读引用，不参与身份判定。
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

// RegisterProject 登记一个项目位置（两种形态见 RegisterProjectReq）。
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
	if strings.TrimSpace(req.OriginURL) == "" {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 登记必须带 origin_url（项目身份由它派生）",
			errBadDispatchRequest)
	}
	if strings.HasPrefix(req.OriginURL, "-") {
		// git 会把以 - 开头的参数解释为选项——参数注入面，与 ErrBadBaseBranch 同源。
		return proto.ProjectLocation{}, fmt.Errorf("%w: origin_url 不允许以 - 开头", errBadDispatchRequest)
	}
	if req.Path != "" {
		return m.registerExistingProject(ctx, req)
	}
	return m.cloneAndRegisterProject(ctx, req)
}

// registerExistingProject 登记本机上已存在的一份代码。
func (m *Manager) registerExistingProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	if err := EnsureRepoUsable(ctx, req.Path); err != nil {
		m.log.Warn("登记被拒：路径不是可用的 git 仓库", "path", req.Path, "cause", err)
		return proto.ProjectLocation{}, err
	}
	// 归并主工作树：位置表一个项目只允许一行，而 linked worktree 与主仓
	// origin 相同、project_id 相同（spec §5）。
	root, err := MainWorktreeRoot(ctx, req.Path)
	if err != nil {
		m.log.Warn("登记被拒：归并主工作树失败", "path", req.Path, "cause", err)
		return proto.ProjectLocation{}, err
	}
	// origin 由 agentd 在本机现读，而不是采信调用方上送的值：登记的是这个
	// 路径上真实存在的仓库，它的 origin 才是权威。
	actual, err := projectOriginURL(ctx, root)
	if err != nil {
		m.log.Warn("登记被拒：读不到 origin", "path", root, "cause", err)
		return proto.ProjectLocation{}, err
	}
	// 校验一致：挡住「路径敲错但恰好指到另一个真实仓库」这种脏登记。
	if projectid.FromOrigin(actual) != projectid.FromOrigin(req.OriginURL) {
		m.log.Warn("登记被拒：路径上的仓库不是请求的项目",
			"path", root, "actual_origin", actual, "want_origin", req.OriginURL)
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: %s 上的 origin 是 %s，而请求的项目是 %s；换个路径，或去掉 --path 让本机自己 clone",
			ErrProjectOriginMismatch, root, actual, req.OriginURL)
	}
	return m.persistProject(req.Name, root, actual)
}

// cloneAndRegisterProject 先 clone 再登记。
func (m *Manager) cloneAndRegisterProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
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
	// 落点已存在就拒绝：往一个已有目录里 clone 要么失败要么污染它，两种都不该发生。
	if _, err := os.Stat(dest); err == nil {
		m.log.Warn("克隆被拒：落点已存在", "dest", dest)
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: 落点 %s 已存在；用 handoff project add --target <机器> --path %s 直接登记它",
			ErrProjectAlreadyExists, dest, dest)
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
