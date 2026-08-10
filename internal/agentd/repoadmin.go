// 本文件是 agentd 侧「执行机 × 仓库登记」的操作层。
//
// 职责：
//   - RegisterRepo：登记执行机上已有的克隆，或先 clone 再登记
//   - ListRepos：列出登记，并**现场探测**每条的实际状态（漂移可见化）
//   - UnregisterRepo：注销登记（只删记录，不动磁盘）
//
// 边界：
//   - 不做解析：dispatch 时「--repo 写的是什么」由 reporegistry.go 的纯函数决定
//   - 不做持久化细节：SQL 在 internal/store/repos.go
//   - 不删磁盘上的仓库：注销只影响登记，磁盘由人自己处置
//   - clone 在**执行机本地**执行（agentd 就跑在这台机器上），不走 ssh——
//     用的是执行机自己的 git 凭据，与 agentd 既有的 fetch 回退同一条路径
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// ErrRepoAlreadyExists 表示登记冲突或克隆落点已被占用，映射 409。
//
// 与 ErrRepoUnusable（400）的区别：那是「请求本身有问题，改了再来」，
// 这是「当前状态与请求冲突」——和 ErrDirtyWorktree / ErrWorkdirBusy 同层级。
var ErrRepoAlreadyExists = errors.New("仓库登记冲突或克隆落点已存在")

// repo ls 的状态取值。不落库，每次列出时现场探得。
const (
	repoStatusOK      = "有效"
	repoStatusMissing = "路径不存在"
	repoStatusNotRepo = "不是 git 仓库"
)

// RegisterRepoReq 是登记一个仓库的请求。
//
// 两种形态互斥：
//   - Clone=false：登记执行机上已存在的克隆，Path 必填
//   - Clone=true：先 clone 再登记，URL 必填；Path 为落点，空则用 cfg.RepoRoot/<Name>
//
// Name 可省，此时由 origin URL 末段派生。
type RegisterRepoReq struct {
	Name  string
	Path  string
	URL   string
	Clone bool
}

// repoOriginURL 读取仓库的 origin 地址。
//
// 参数：
//   - ctx: 控制 git 调用生命周期
//   - repo: 仓库路径
//
// 返回：
//   - origin 地址；仓库不可用或没有 origin 时返回包装 ErrRepoUnusable 的错误
//
// 注意：
//   - 没有 origin 的仓库拒绝登记：它永远参与不了 dispatch 省略 --repo 时的
//     origin 自动匹配，登记进来只会变成一条永远匹配不上的死记录
func repoOriginURL(ctx context.Context, repo string) (string, error) {
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

// repoNameFromURL 从 git URL 末段派生缺省登记名（去掉 .git 后缀）。
//
// 例：git@github.com:xushixin/handoff.git → handoff
func repoNameFromURL(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// RegisterRepo 登记一个仓库（两种形态见 RegisterRepoReq）。
//
// 参数：
//   - ctx: 控制整组 git 调用的生命周期
//   - req: 登记请求
//
// 返回：
//   - 落库后的登记条目
//   - 错误：ErrRepoUnusable（400，路径不是仓库/无 origin/clone 失败）、
//     ErrRepoAlreadyExists（409，名字或路径已被占用/落点已存在）、
//     errBadDispatchRequest（400，参数缺失或互斥冲突）
//
// 注意：
//   - **登记在 clone 成功之后才落库**：反过来会在 clone 失败时留下一条指向
//     不存在路径的死记录
//   - clone 的落点若已存在则直接拒绝，绝不往里 clone、绝不覆盖
func (m *Manager) RegisterRepo(ctx context.Context, req RegisterRepoReq) (proto.Repo, error) {
	m.log.Info("登记仓库请求", "name", req.Name, "path", req.Path,
		"url", req.URL, "clone", req.Clone)
	if req.Clone {
		return m.cloneAndRegister(ctx, req)
	}
	return m.registerExisting(ctx, req)
}

// registerExisting 登记执行机上已存在的克隆。
func (m *Manager) registerExisting(ctx context.Context, req RegisterRepoReq) (proto.Repo, error) {
	if req.Path == "" {
		return proto.Repo{}, fmt.Errorf("%w: 登记已有仓库必须指定路径（或加 --clone 让 agentd 克隆一份）",
			errBadDispatchRequest)
	}
	if err := EnsureRepoUsable(ctx, req.Path); err != nil {
		m.log.Warn("登记被拒：路径不是可用的 git 仓库", "path", req.Path, "cause", err)
		return proto.Repo{}, err
	}
	// origin 由 agentd 在执行机上现读，而不是采信调用方上送的值：
	// 登记的是这个路径上真实存在的仓库，它的 origin 才是权威。
	origin, err := repoOriginURL(ctx, req.Path)
	if err != nil {
		m.log.Warn("登记被拒：读不到 origin", "path", req.Path, "cause", err)
		return proto.Repo{}, err
	}
	return m.persistRepo(req.Name, req.Path, origin)
}

// cloneAndRegister 先 clone 再登记。
func (m *Manager) cloneAndRegister(ctx context.Context, req RegisterRepoReq) (proto.Repo, error) {
	if req.URL == "" {
		return proto.Repo{}, fmt.Errorf("%w: --clone 需要仓库 URL（当前目录不是 git 仓库时必须显式指定）",
			errBadDispatchRequest)
	}
	if strings.HasPrefix(req.URL, "-") {
		// git 会把以 - 开头的参数解释为选项——参数注入面，与 ErrBadBaseBranch 同源。
		return proto.Repo{}, fmt.Errorf("%w: 仓库 URL 不允许以 - 开头", errBadDispatchRequest)
	}
	name := req.Name
	if name == "" {
		name = repoNameFromURL(req.URL)
	}
	if name == "" {
		return proto.Repo{}, fmt.Errorf("%w: 无法从 URL %q 派生登记名，请显式指定", errBadDispatchRequest, req.URL)
	}
	dest := req.Path
	if dest == "" {
		if m.cfg.RepoRoot == "" {
			return proto.Repo{}, fmt.Errorf("%w: 未指定落点，且执行机未配置 repo_root（在 agentd 的 config.yaml 里配它，或显式给路径）",
				errBadDispatchRequest)
		}
		dest = filepath.Join(m.cfg.RepoRoot, name)
	}
	// 落点已存在就拒绝：往一个已有目录里 clone 要么失败要么污染它，两种都不该发生。
	if _, err := os.Stat(dest); err == nil {
		m.log.Warn("克隆被拒：落点已存在", "dest", dest)
		return proto.Repo{}, fmt.Errorf("%w: 落点 %s 已存在；换一个路径，或用不带 --clone 的形态直接登记它",
			ErrRepoAlreadyExists, dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return proto.Repo{}, fmt.Errorf("%w: 探查落点 %s: %v", ErrRepoUnusable, dest, err)
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return proto.Repo{}, fmt.Errorf("%w: 创建落点父目录 %s: %v", ErrRepoUnusable, parent, err)
	}
	m.log.Info("开始克隆仓库", "url", req.URL, "dest", dest)
	start := time.Now()
	// gitRun 以 parent 为 cwd 执行；-- 分隔符防止 URL/路径被当成选项。
	if _, stderr, err := gitRun(ctx, parent, "clone", "--", req.URL, dest); err != nil {
		m.log.Error("克隆仓库失败", "url", req.URL, "dest", dest,
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return proto.Repo{}, fmt.Errorf("%w: 克隆 %s 到 %s 失败: %s: %v",
			ErrRepoUnusable, req.URL, dest, strings.TrimSpace(stderr), err)
	}
	m.log.Info("克隆仓库完成", "url", req.URL, "dest", dest,
		"elapsed_ms", time.Since(start).Milliseconds())
	return m.persistRepo(name, dest, req.URL)
}

// persistRepo 把一条登记落库，并把 store 的冲突哨兵翻译成 agentd 的哨兵。
func (m *Manager) persistRepo(name, path, origin string) (proto.Repo, error) {
	if name == "" {
		name = repoNameFromURL(origin)
	}
	if name == "" {
		return proto.Repo{}, fmt.Errorf("%w: 无法派生登记名，请显式指定", errBadDispatchRequest)
	}
	r := proto.Repo{Name: name, Path: path, OriginURL: origin, CreatedAt: time.Now()}
	if err := m.st.CreateRepo(&r); err != nil {
		if errors.Is(err, store.ErrRepoDuplicate) {
			m.log.Warn("登记被拒：名字或路径已被占用", "name", name, "path", path, "cause", err)
			return proto.Repo{}, fmt.Errorf("%w: 名字 %q 或路径 %s 已被登记（handoff repo ls 查看）",
				ErrRepoAlreadyExists, name, path)
		}
		m.log.Error("登记落库失败", "name", name, "path", path, "cause", err)
		return proto.Repo{}, err
	}
	m.log.Info("仓库登记完成", "name", name, "path", path, "origin", origin)
	r.Status = repoStatusOK
	return r, nil
}

// ListRepos 列出全部登记，并现场探测每条的实际状态。
//
// 参数：
//   - ctx: 控制探测用的 git 调用生命周期
//
// 返回：
//   - 登记列表（Status 已填充）；查库失败时返回错误
//
// 注意：
//   - 探测是登记与文件系统漂移的可见化手段。探测失败不影响列出——
//     状态本身就是要报给人看的结果，不是错误
func (m *Manager) ListRepos(ctx context.Context) ([]proto.Repo, error) {
	repos, err := m.st.ListRepos()
	if err != nil {
		m.log.Error("列出仓库登记失败", "cause", err)
		return nil, err
	}
	for i := range repos {
		repos[i].Status = probeRepoStatus(ctx, repos[i].Path)
	}
	m.log.Info("列出仓库登记", "count", len(repos))
	return repos, nil
}

// probeRepoStatus 探测一条登记指向的路径当前是什么状态。
func probeRepoStatus(ctx context.Context, path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return repoStatusMissing
	}
	if err := EnsureRepoUsable(ctx, path); err != nil {
		return repoStatusNotRepo
	}
	return repoStatusOK
}

// UnregisterRepo 注销一条登记。
//
// 参数：
//   - ctx: 上下文（当前实现不发起 git 调用，保留以对齐其余操作签名）
//   - name: 登记名
//
// 返回：
//   - 错误：登记不存在时 store.ErrNotFound（404）；路径被活跃任务占用时
//     ErrWorkdirBusy（409）
//
// 注意：
//   - **只删登记，永不删磁盘上的仓库**。磁盘上那份是不是还要留，由人自己决定；
//     handoff 不替审核者做删代码的决定
func (m *Manager) UnregisterRepo(ctx context.Context, name string) error {
	r, err := m.st.GetRepoByName(name)
	if err != nil {
		m.log.Warn("注销登记失败：登记不存在", "name", name, "cause", err)
		return err
	}
	tasks, err := m.st.ActiveTasksByRepoPath(r.Path)
	if err != nil {
		m.log.Error("注销登记前查活跃任务失败", "name", name, "path", r.Path, "cause", err)
		return err
	}
	if len(tasks) > 0 {
		ids := make([]string, 0, len(tasks))
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
		m.log.Warn("注销登记被拒：仓库被活跃任务占用",
			"name", name, "path", r.Path, "tasks", strings.Join(ids, ","))
		return fmt.Errorf("%w: 仓库 %s 上还有 %d 个活跃任务（%s）；先 done 或 stop 它们",
			ErrWorkdirBusy, r.Path, len(tasks), strings.Join(ids, ", "))
	}
	if err := m.st.DeleteRepo(name); err != nil {
		m.log.Error("注销登记落库失败", "name", name, "cause", err)
		return err
	}
	m.log.Info("仓库登记已注销（磁盘仓库未动）", "name", name, "path", r.Path)
	return nil
}
