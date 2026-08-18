// 本文件是 agentd 侧 git 工作区操作与文件/命令读取的唯一出口。
//
// 职责：
//   - 派发前的工作区准备：PrepareWorkspace 按分支×worktree 两个正交维度准备任务
//     工作区（脏工作区一律拒绝；new-worktree 免脏检查）——PrepareBranch 是其
//     原地+自动分支的过渡薄包装
//   - 审阅素材：Diff（基准分支到 HEAD 的差异 + 提交列表）、
//     ReadFile（读仓库内文件）、ListDir（列举工作树内一层目录）、
//     RunCmd（远程跑测试/lint 等审阅命令）
//   - 派发前的基线决议：ResolveBaseline 一次算出「校验结论 + 新分支起点 +
//     任务仓库领先多少提交」，保证校验的东西和用的东西是同一个
//
// 边界：
//   - 全部操作是「分支准备 + 只读审阅」：绝不代 executor 写代码/提交，
//     executor 的改动必须经它自己的 commit 落进任务分支
//   - 不解析审阅命令的语义：run 跑什么、diff 怎么审由协调者决定
//   - git 全部经 exec.Command("git","-C",repo,...) 执行，不拼接 shell
//   - 每条命令都有超时/输出护栏：工作区准备/清理一组 git 调用上限
//     WorkspaceGitTimeout=2min（pre-checkout hook / credential 交互提示会挂死 git，
//     这些调用同步跑在 dispatch handler 里，必须有兜底上限）；审阅命令 run 10min；
//     输出有界回收（最多保留 1MB，超出部分只排空不驻留内存），防挂死与内存失控
package agentd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/proxycfg"
)

// 错误定义：
//   - ErrDirtyWorktree：工作区有未提交/未跟踪的改动，拒绝派发
//   - ErrPathEscape：请求的文件路径逃逸出任务仓库（含符号链接逃逸）
//   - ErrPathIsDir：请求的文件路径指向目录（fetch 只服务普通文件）
//   - ErrPathNotDir：请求列举的路径不是目录（ListDir 只服务目录）
//   - ErrNotRegularFile：请求的文件路径指向管道/设备等特殊文件（不可读）
//   - ErrRepoUnusable：git 探活本身失败（仓库路径不存在/不是 git 仓库/权限等），
//     与 ErrDirtyWorktree 的「仓库可用但状态不干净」区分——前者需要协调者先解决
//     仓库本身的问题，后者一条 git 命令即可清理（server 层映射见 writeDispatchError）
//   - ErrBadBaseBranch：diff 的基准分支参数非法（以 "-" 开头，会被 git 解释为
//     选项而非 rev——git 参数注入面）
//   - ErrBadWorkspaceReq：PrepareWorkspace 的参数非法（互斥冲突/分支不存在/
//     路径不是本仓库 worktree/rev 以 "-" 开头等），dispatch 期拒发
var (
	ErrDirtyWorktree   = errors.New("工作区不干净（有未提交改动），拒绝派发")
	ErrPathEscape      = errors.New("路径逃逸被拒绝")
	ErrPathIsDir       = errors.New("路径是目录，不是文件")
	ErrPathNotDir      = errors.New("路径不是目录")
	ErrNotRegularFile  = errors.New("路径不是普通文件（管道/设备等特殊文件不可读）")
	ErrRepoUnusable    = errors.New("任务仓库不可用（路径不存在或不是 git 仓库）")
	ErrBadBaseBranch   = errors.New("非法的基准分支：不允许以 - 开头")
	ErrBadWorkspaceReq = errors.New("工作区参数非法")
	ErrWorkdirBusy     = errors.New("目标工作目录已被活跃任务占用")

	// 以下五个是在线编辑（B81）的拒绝面。文案就是 HTTP 层原样吐给用户的中文，
	// 所以每条都要能独立读懂——「操作失败」帮不上任何人
	ErrGitDirWrite   = errors.New("不允许写入 .git 目录")
	ErrSymlinkTarget = errors.New("目标是符号链接，不支持在线编辑")
	ErrBinaryFile    = errors.New("二进制文件不支持在线编辑")
	ErrFileTooLarge  = errors.New("文件超过 1 MB，不支持在线编辑")
	ErrBaseMismatch  = errors.New("文件已被改动")

	// ErrBaseCommitMissing 表示审核者本地的基线提交在任务仓库中不存在，
	// 且 fetch 后仍补不回来——远程仓库落后于本地，派发出去的活会建在错误的基准上。
	ErrBaseCommitMissing = errors.New("基线提交在任务仓库中不存在")

	// ErrBranchIdentityMismatch 表示 git 报告成功，但工作区实际所在的分支
	// 不是我们请求的那个（B76：worktree add -b 被 DWIM 顶替）。
	ErrBranchIdentityMismatch = errors.New("工作区分支与请求不符")

	// 以下三个是文件树条目操作（B107：建/改名/删）的拒绝面。文案就是 HTTP 层
	// 原样吐给用户的中文，每条都要能独立读懂。
	ErrEntryExists   = errors.New("目标已存在")
	ErrEntryNotFound = errors.New("目标不存在")
	ErrBadEntryName  = errors.New("名字不合法")
)

// 执行护栏：
//   - RunCmdTimeout：单条审阅命令的执行上限。包级 var 而非 const，便于测试注入更短值。
//     导出供 cmd 包派生 agentd HTTP WriteTimeout（见 cmd/agentd.go）：响应写超时必须
//     ≥ 该上限，否则长审阅命令会在 handler 执行途中被掐断连接、RunCmd 被提前取消
//   - maxRunOutput：合并输出的截断上限，防止失控命令刷爆内存与响应体
//   - WorkspaceGitTimeout：工作区准备/清理这一整组 git 调用的时长上限（见其注释）
var (
	RunCmdTimeout = 10 * time.Minute
	maxRunOutput  = 1 << 20 // 1 MiB

	// WorkspaceGitTimeout 是工作区准备/清理这一整组 git 调用的时长上限。
	//
	// 为什么必须有：worktree add / checkout 在网络文件系统、pre-checkout hook 或
	// credential 交互式提示下会永久挂住；这些调用同步跑在 dispatch 的 HTTP handler
	// 里，一次挂死等于一个连接与一条 handler goroutine 永不释放。
	// 包级 var 而非 const：测试可注入更短值。
	WorkspaceGitTimeout = 2 * time.Minute
)

// log 返回 slog.Default()（与 store 同款约定：bootstrap 后统一 logger）。
func log() *slog.Logger { return slog.Default() }

// quotaNote 把一次进程创建失败翻译成归因文案；与配额无关时返回空串。
//
// 为什么在 agentd 侧包一层而不是四处直接调 prochost.ExplainForkFailure：
// 归因文案要同时进日志和进返回给协调者的 error，收在一处才不会两边写法漂移。
func quotaNote(err error) string {
	note, _ := prochost.ExplainForkFailure(err)
	return note
}

// gitExec 是 gitRun / gitProbe 的公共体：执行 git -C repo <args...>。
//
// quiet 只影响**失败时的日志级别**：false 记 Error（真故障），true 记 Debug
// （预期内的探测未命中）。返回值语义与 quiet 无关。
func gitExec(ctx context.Context, repo string, quiet bool, args ...string) (stdout, stderr string, err error) {
	log().Info("git 调用", "repo", repo, "args", args)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	log().Info("git 调用完成", "repo", repo, "args", args,
		"elapsed_ms", time.Since(start).Milliseconds())
	if err != nil {
		if note := quotaNote(err); note != "" {
			log().Error("git 调用失败（进程配额）", "repo", repo, "args", args,
				"note", note, "cause", err)
			return outBuf.String(), errBuf.String(), fmt.Errorf("%s: %w", note, err)
		}
		if quiet {
			log().Debug("git 探测未命中（预期内）", "repo", repo, "args", args,
				"stderr", truncateRunes(errBuf.String(), 500), "cause", err)
		} else {
			log().Error("git 调用失败", "repo", repo, "args", args,
				"stderr", truncateRunes(errBuf.String(), 500), "cause", err)
		}
	}
	return outBuf.String(), errBuf.String(), err
}

// gitRun 执行 git -C repo <args...>，返回 stdout 与 stderr。
//
// 日志：调用前 Info（repo、args）、调用后 Info（耗时）；失败 Error 带 stderr
// 原文——git 报错原文是排障必需品，不能只留包装后的 error 文本。
//
// 用于**失败即故障**的调用（clone / fetch / worktree add / diff / log 等）。
// 失败是预期内结果的探测型调用请用 gitProbe。
func gitRun(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error) {
	return gitExec(ctx, repo, false, args...)
}

// gitProbe 与 gitRun 相同，但把**非零退出**当成预期内的探测结果而非故障：
// 失败记 Debug 不记 Error。返回值语义完全不变（调用方仍按 err != nil 判未命中）。
//
// 为什么需要它（B81）：探测型调用（rev-parse --verify --quiet、cat-file -e）的
// 非零退出是**正常分支**——远程执行机只 fetch 出 origin/<name>、从不建本地分支，
// 所以「本地同名分支不存在」是常态。经 gitRun 打成 ERROR 后，成功路径的日志里
// 躺着 ERROR，与真故障无法区分；按 level=ERROR 过滤日志会捞出正常路径。
//
// 边界：只用于「失败是预期内结果」的调用。会真正出事的 git 调用仍走 gitRun。
// 进程配额失败无论走哪个入口都仍是 Error。
func gitProbe(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error) {
	return gitExec(ctx, repo, true, args...)
}

// gitProxy 是本机出网 git（clone/fetch）使用的代理地址，由 agentd bootstrap
// 经 SetGitProxy 注入一次。
//
// 为什么是包级变量而不是把 proxy 串进签名：ResolveBaseline 等函数是包级函数，
// 调用链上大部分环节与网络无关，把 proxy 串进每个签名会污染一大片无关代码。
// 这与本包 log() 用运行时取值而非依赖注入是同一个权衡。
var gitProxy string

// SetGitProxy 设置出网 git 使用的代理，由 agentd bootstrap 调用一次。
//
// 参数：
//   - proxy: 代理地址；空串 = 不用代理
//
// 注意：
//   - 只影响 clone / fetch 两处出网操作，本地 git 操作（rev-parse/status/
//     worktree/diff…）一律不带代理——它们根本不出网，带上只会平白多一个配置
//   - 非并发安全：只在启动期调用一次。测试里改它必须串行
func SetGitProxy(proxy string) { gitProxy = proxy }

// gitNetArgs 在 git 参数前插入代理参数。
//
// 代理参数必须排在子命令**之前**：git 的 -c 是全局选项，放到子命令后面
// git 会把它当成子命令的参数并直接报错。
func gitNetArgs(args ...string) []string {
	p := proxycfg.GitArgs(gitProxy)
	if len(p) == 0 {
		return args
	}
	return append(p, args...)
}

// gitRunNet 执行**会出网**的 git 操作（clone / fetch），自动带上代理参数。
//
// 参数与返回同 gitRun。
//
// 注意：
//   - 只有 clone 与 fetch 该用它。给本地操作用不会出错，但会让「哪些操作出网」
//     这个信息从代码里消失，下一个人就无从判断代理配错会影响哪些功能
//   - git 的 http.proxy 对 ssh:// 与 git@host:path 的 remote **无效**。
//     SSH remote 要走代理得配 ssh 的 ProxyCommand，那会动到用户的 ssh 配置面，
//     不在 handoff 职责内；README 给出改用 HTTPS remote（insteadOf）的解法
func gitRunNet(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error) {
	if gitProxy != "" {
		log().Info("git 出网操作将走代理", "repo", repo, "args", args,
			"proxy", proxycfg.Redact(gitProxy))
	}
	return gitRun(ctx, repo, gitNetArgs(args...)...)
}

// PrepareBranch 是 PrepareWorkspace 的过渡薄包装：保持一期「原地 + 自动分支」语义
// 与全部错误哨兵（ErrDirtyWorktree/ErrRepoUnusable），Dispatch 改走 PrepareWorkspace
// 后本函数仅剩测试与本包内部引用（Task 7 会清理调用点）。
//
// 参数：
//   - ctx: 控制整组 git 调用的生命周期，内部再叠加 WorkspaceGitTimeout 作为兜底上限
func PrepareBranch(ctx context.Context, repo, taskID string) (branch string, err error) {
	ws, err := PrepareWorkspace(ctx, WorkspaceReq{Repo: repo, TaskID: taskID})
	if err != nil {
		return "", err
	}
	return ws.Branch, nil
}

// WorkspaceReq 描述 dispatch 的工作区诉求（分支 × worktree 两个正交维度）。
//
// 分支维度三态（互斥）：Branch=已存在分支 / NewBranch=新建分支 / 都空=自动
// handoff/<id8>；Base 是新分支起点（空=HEAD），与 NewBranch 和自动分支都能
// 连用，只与 Branch 互斥——切已存在分支时没有「起点」这回事。
// worktree 维度三态（互斥）：Worktree=用户自带 worktree 路径 / NewWorktree=由
// agentd 在 WorktreesDir 下新建 managed worktree / 都空=原地（主仓库）。
type WorkspaceReq struct {
	Repo         string // 主仓库路径
	TaskID       string
	Branch       string // 已存在分支（与 NewBranch 互斥）
	NewBranch    string // 新建分支名（空且 Branch 空 = 自动 handoff/<id8>）
	Base         string // 新分支起点（空=HEAD）；与 Branch 互斥，NewBranch/自动分支均可带
	Worktree     string // 已存在 worktree 路径（与 NewWorktree 互斥）
	NewWorktree  bool
	WorktreesDir string // agentd 管理的 worktree 根目录（DataDir/worktrees）
}

// Workspace 是准备完成的工作区结果。
type Workspace struct {
	Branch  string
	WorkDir string // executor cwd 与审阅命令目录；原地模式 = Repo
	Managed bool   // WorkDir 是 agentd 创建的 worktree（done 时代删）

	// NewBranchTip 是本次 dispatch 新建分支时的尖端 sha；空串表示分支不是本次
	// 新建的（--branch <已存在分支> 模式）。补偿删分支前用它复核「自创建以来
	// 没动过」。
	//
	// 为什么用 sha 而不是 BranchCreated bool：一个 bool 加一个 sha 能构造出
	// 「声称建了分支却说不出它当时指向哪」这种非法状态，用单字段就构造不出来。
	NewBranchTip string
	// PrevRef 是非 managed 模式下 checkout 之前的 HEAD：正常在分支上时为分支名，
	// detached 时为 commit sha，两者都能直接喂给 git checkout 复原。managed 模式
	// 恒为空（新工作树没有「之前」）。空串表示采集失败，补偿据此放弃复原而非乱切。
	PrevRef string
	// RepoDirtyCount / RepoDirtyFiles 是 managed 模式下派发当时**主仓库**的脏快照
	// （语义见 proto.Task 同名字段）；非 managed 模式恒为零值——那两条路径的脏
	// 工作区已被 ensureCleanWorktree 拒发，不存在「有改动却看不见」的情形。
	RepoDirtyCount int
	RepoDirtyFiles string
}

// PrepareWorkspace 按 WorkspaceReq 准备任务工作区，返回结果。
//
// 参数：
//   - ctx: 控制整组 git 调用的生命周期，内部再叠加 WorkspaceGitTimeout 作为兜底上限
//
// 3 分支模式 × 3 工作树模式的 9 种组合行为表（分支 B/新分支 N/自动 A × 新树 N/用户树 U/原地 I）：
//
//	      新树(NewWorktree)        用户树(Worktree)        原地(默认)
//	B   worktree add <p> <b>     校验归属+脏，checkout b   脏检查，checkout b
//	N   worktree add -b b <p> t  校验归属+脏，checkout -b  脏检查，checkout -b b [t]
//	A   worktree add -b h <p> [t] 校验归属+脏，checkout -b  脏检查，checkout -b h [t]
//
// 其中 b=指定分支、h=handoff/<id8>、t=Base（N 行与 A 行均有效，空=HEAD；B 行
// 切已存在分支，不接受 Base，见第 1 层校验）、p=WorktreesDir/<id8> 或用户路径。
// 校验规则：Branch 模式分支必须已存在；用户树模式必须归属本仓库（git-common-dir 比对
// + show-toplevel 必须等于入参）；所有以 "-" 开头的分支名/路径一律拒绝（git 参数注入面）。
//
// 为什么 NewWorktree 免脏检查：新 worktree 是从仓库新建的独立工作树，天然干净，
// 与主仓库的脏状态无关——这是 worktree 并行派发的价值：主仓有人手动改动也不阻塞
// 新任务开跑；只有原地/用户树模式（复用既有工作树）才需要脏检查防污染。
//
// 为什么用户树归属校验用 git-common-dir 比对：普通目录与 worktree 的差别就在
// 「共享同一仓库 git 目录」；git-common-dir 对 main 仓库返回其 .git、对 worktree
// 返回同一值，路径经 EvalSymlinks 归一后相等即归属成立——比「读 .git 文件内容」
// 更稳（.git 可能缺失、可能被链到别处）。校验失败按 ErrBadWorkspaceReq 拒发。
func PrepareWorkspace(ctx context.Context, req WorkspaceReq) (Workspace, error) {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	log().Info("工作区准备进入", "repo", req.Repo, "task", req.TaskID, "branch", req.Branch,
		"new_branch", req.NewBranch, "base", req.Base, "worktree", req.Worktree,
		"new_worktree", req.NewWorktree, "worktrees_dir", req.WorktreesDir,
		"timeout", WorkspaceGitTimeout)
	// 第 1 层：纯内存参数校验（互斥/依赖/注入面），全部包 ErrBadWorkspaceReq
	if req.Repo == "" || req.TaskID == "" {
		return Workspace{}, rejectWorkspace("repo 与 task_id 必填", req)
	}
	if req.Branch != "" && req.NewBranch != "" {
		return Workspace{}, rejectWorkspace("branch 与 new-branch 互斥", req)
	}
	if req.Worktree != "" && req.NewWorktree {
		return Workspace{}, rejectWorkspace("worktree 与 new-worktree 互斥", req)
	}
	// base 是「新分支的起点」，切一个已存在分支时谈起点没有意义——真正的禁忌
	// 只有这一条。自动分支 handoff/<id8> 同样是新建分支，必须允许带起点：
	// 缺了它，dispatch 校验的基线与新分支的实际起点就成了两码事（B35 根因）。
	if req.Base != "" && req.Branch != "" {
		return Workspace{}, rejectWorkspace("base 与 branch（已存在分支）互斥", req)
	}
	for _, name := range []struct{ what, v string }{
		{"branch", req.Branch}, {"new-branch", req.NewBranch}, {"base", req.Base}, {"worktree", req.Worktree},
	} {
		if strings.HasPrefix(name.v, "-") {
			return Workspace{}, rejectWorkspace(name.what+" 不允许以 - 开头（git 参数注入面）", req)
		}
	}

	// 第 2 层：分支名决议（B/N/A 三态收敛为唯一分支名）
	branch := req.Branch
	if branch == "" {
		branch = req.NewBranch
	}
	if branch == "" {
		branch = taskBranch(req.TaskID)
	}
	isExisting := req.Branch != "" // 仅 Branch 模式要求分支已存在
	if isExisting {
		// 分支存在性：rev-parse --verify --quiet refs/heads/<name>，非零即不存在
		if out, _, err := gitProbe(ctx, req.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+req.Branch); err != nil || strings.TrimSpace(out) == "" {
			return Workspace{}, rejectWorkspace("分支 "+req.Branch+" 不存在", req)
		}
	}

	var ws Workspace
	// 第 3 层：按 worktree 维度分派
	switch {
	case req.NewWorktree:
		// 新树：WorktreesDir/<id8>，MkdirAll 后 git worktree add（已存在分支不带 -b）
		// B43：新树免脏检查是对的（新树天然干净），但主仓的未提交改动不在基线里，
		// executor 在新树里看不到它们。建树之前采一次快照，供 dispatch 回显
		dirtyCount, dirtyFiles := repoDirtySnapshot(ctx, req.Repo)
		workDir := filepath.Join(req.WorktreesDir, id8(req.TaskID))
		if err := os.MkdirAll(req.WorktreesDir, 0o700); err != nil {
			return Workspace{}, fmt.Errorf("创建 worktrees 目录 %s: %w", req.WorktreesDir, err)
		}
		var args []string
		if isExisting {
			args = []string{"worktree", "add", workDir, branch}
		} else {
			args = []string{"worktree", "add", "-b", branch, workDir}
			if req.Base != "" {
				args = append(args, req.Base)
			}
		}
		if _, stderr, err := gitRun(ctx, req.Repo, args...); err != nil {
			return Workspace{}, fmt.Errorf("git %v: %s: %w", args, strings.TrimSpace(stderr), err)
		}
		ws = Workspace{Branch: branch, WorkDir: workDir, Managed: true,
			RepoDirtyCount: dirtyCount, RepoDirtyFiles: dirtyFiles}
		if !isExisting {
			ws.NewBranchTip = recordNewBranchTip(ctx, req.Repo, branch)
		}
	case req.Worktree != "":
		// 用户树：归属校验 → 脏检查 → 在其中 checkout
		if !worktreeBelongsToRepo(ctx, req.Repo, req.Worktree) {
			return Workspace{}, rejectWorkspace("路径 "+req.Worktree+" 不是本仓库的 worktree", req)
		}
		if err := ensureCleanWorktree(ctx, req.Worktree); err != nil {
			return Workspace{}, err
		}
		prev := currentRef(ctx, req.Worktree)
		if err := checkoutInWorktree(ctx, req.Worktree, branch, req.Base, isExisting); err != nil {
			return Workspace{}, err
		}
		ws = Workspace{Branch: branch, WorkDir: req.Worktree, Managed: false, PrevRef: prev}
		if !isExisting {
			ws.NewBranchTip = recordNewBranchTip(ctx, req.Repo, branch)
		}
	default:
		// 原地：脏检查主仓 → checkout / checkout -b
		if err := ensureCleanWorktree(ctx, req.Repo); err != nil {
			return Workspace{}, err
		}
		prev := currentRef(ctx, req.Repo)
		var args []string
		if isExisting {
			args = []string{"checkout", branch}
		} else {
			args = []string{"checkout", "-b", branch}
			if req.Base != "" {
				args = append(args, req.Base)
			}
		}
		if _, stderr, err := gitRun(ctx, req.Repo, args...); err != nil {
			return Workspace{}, fmt.Errorf("git %v: %s: %w", args, strings.TrimSpace(stderr), err)
		}
		ws = Workspace{Branch: branch, WorkDir: req.Repo, Managed: false, PrevRef: prev}
		if !isExisting {
			ws.NewBranchTip = recordNewBranchTip(ctx, req.Repo, branch)
		}
	}
	// 守卫（B76）：三条路径统一在此核对，因为它们都已把结果收敛进 ws
	if verr := verifyBranchIdentity(ctx, ws.WorkDir, ws.Branch); verr != nil {
		log().Error("工作区分支身份核对失败，回滚并拒发", "task", req.TaskID,
			"want", ws.Branch, "workdir", ws.WorkDir, "managed", ws.Managed, "cause", verr)
		rollbackWorkspace(ctx, req.Repo, ws)
		return Workspace{}, verr
	}
	log().Info("工作区分支身份核对通过", "task", req.TaskID, "branch", ws.Branch)
	log().Info("工作区准备完成", "task", req.TaskID, "branch", ws.Branch, "workdir", ws.WorkDir, "managed", ws.Managed)
	// ctx 超时与 git 报错的错误文本很像（都是 "signal: killed" 一类），
	// 不显式记录一条就无法在日志里区分「命令自己失败」与「被我们掐断」
	if ctx.Err() != nil {
		log().Error("工作区准备超时", "task", req.TaskID, "timeout", WorkspaceGitTimeout, "cause", ctx.Err())
	}
	return ws, nil
}

// rejectWorkspace 打参数拒绝日志并包装 ErrBadWorkspaceReq（哪个规则、哪个值）。
func rejectWorkspace(rule string, req WorkspaceReq) error {
	log().Warn("工作区参数非法，拒绝准备", "task", req.TaskID, "rule", rule, "req", req)
	return fmt.Errorf("%w: %s", ErrBadWorkspaceReq, rule)
}

// checkoutInWorktree 在用户自带 worktree 内切分支（已存在 → checkout；新建/自动
// → checkout -b [base]），供 Worktree 模式复用。ctx 控制本次 git 调用的生命周期。
func checkoutInWorktree(ctx context.Context, workDir, branch, base string, isExisting bool) error {
	var args []string
	if isExisting {
		args = []string{"checkout", branch}
	} else {
		args = []string{"checkout", "-b", branch}
		if base != "" {
			args = append(args, base)
		}
	}
	if _, stderr, err := gitRun(ctx, workDir, args...); err != nil {
		return fmt.Errorf("git -C %s %v: %s: %w", workDir, args, strings.TrimSpace(stderr), err)
	}
	return nil
}

// verifyBranchIdentity 核对工作区实际所在分支是否就是决议出的分支。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - workDir: 已建好的工作区目录
//   - want: 第 2 层决议出的分支名
//
// 返回：不符或读取失败时返回包 ErrBranchIdentityMismatch 的错误，文本同时
// 含请求分支与实到分支。
//
// 为什么需要这道核对：git 的退出码只说明「命令没报错」，不说明「它做了你要
// 的事」。B76 里 `worktree add -b X <dir> <base>` 在 base 只有远程跟踪 ref 时
// 被 DWIM 顶替成「检出 base」，丢掉 X 且退出码为 0——要的分支与实到分支从来
// 没被比对过，这是结构性空白，不是某一次 git 行为的补丁。
func verifyBranchIdentity(ctx context.Context, workDir, want string) error {
	out, stderr, err := gitRun(ctx, workDir, "branch", "--show-current")
	got := strings.TrimSpace(out)
	if err != nil {
		return fmt.Errorf("%w: 读取工作区 %s 的当前分支失败（请求分支 %s）: %s",
			ErrBranchIdentityMismatch, workDir, want, strings.TrimSpace(stderr))
	}
	if got != want {
		return fmt.Errorf("%w: 请求分支 %s，git 实际给出 %s（工作区 %s）",
			ErrBranchIdentityMismatch, want, got, workDir)
	}
	return nil
}

// rollbackWorkspace 在 PrepareWorkspace 内部失败时回滚已建的工作区。
//
// 为什么不能交给 manager 的补偿 defer：那个 defer 用的是 PrepareWorkspace 的
// **返回值** ws，失败时它是零值，WorkDir 为空，compensateWorkspace 会直接返回。
// 所以 PrepareWorkspace 自己建的东西必须自己收。
func rollbackWorkspace(ctx context.Context, repo string, ws Workspace) {
	if ws.WorkDir == "" {
		return
	}
	if ws.Managed {
		if err := RemoveManagedWorktree(ctx, repo, ws.WorkDir); err != nil {
			log().Error("回滚 managed worktree 失败，需人工清理", "repo", repo,
				"workdir", ws.WorkDir, "cause", err)
			return
		}
		log().Info("已回滚 managed worktree", "repo", repo, "workdir", ws.WorkDir)
		return
	}
	if ws.PrevRef == "" {
		log().Warn("无 PrevRef 可复原，工作区停在当前 ref", "workdir", ws.WorkDir)
		return
	}
	if _, stderr, err := gitRun(ctx, ws.WorkDir, "checkout", ws.PrevRef); err != nil {
		log().Error("回滚切回原 ref 失败，需人工处理", "workdir", ws.WorkDir,
			"prev_ref", ws.PrevRef, "stderr", strings.TrimSpace(stderr), "cause", err)
		return
	}
	log().Info("已回滚至原 ref", "workdir", ws.WorkDir, "prev_ref", ws.PrevRef)
}

// EnsureRepoUsable 校验 repo 确实是一个可用的 git 仓库。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - repo: 任务仓库路径
//
// 返回：
//   - nil：是可用的 git 仓库
//   - ErrRepoUnusable：路径不存在 / 不是 git 仓库 / git 不在 PATH / 权限不足，
//     错误文本带 git stderr 原文（server 层据此给 400，见 writeDispatchError）
//
// 注意：
//   - 由 Dispatch 在 ResolveBaseline 之前调用。放在那里而不是建树前，是因为
//     ResolveBaseline 对非 git 仓库会误报成 ErrBaseCommitMissing（「落后于本地，
//     请先 push」），那是个比沉默更糟的答案
//   - 判据用 rev-parse --git-dir 而不是 grep worktree add 的错误串：前者是显式
//     判据，后者依赖 git 的文案不变
//   - ensureCleanWorktree 里原有的 ErrRepoUnusable 包装保留——它仍是 git status
//     因其他原因失败时的兜底
func EnsureRepoUsable(ctx context.Context, repo string) error {
	_, stderr, err := gitProbe(ctx, repo, "rev-parse", "--git-dir")
	if err != nil {
		log().Warn("dispatch 前置：任务仓库不可用，拒绝派发", "repo", repo,
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return fmt.Errorf("%w: %s: %v", ErrRepoUnusable, strings.TrimSpace(stderr), err)
	}
	log().Info("dispatch 前置：仓库有效性校验通过", "repo", repo)
	return nil
}

// ensureCleanWorktree 校验工作区干净（status --porcelain 无任何输出）。
// 脏检测含未跟踪文件：未跟踪文件同样可能被执行器误 add 进任务提交，保守拒绝。
// git status 失败（仓库不存在/不是 git 仓库）与「脏」是两种可修复场景，
// 用 ErrRepoUnusable 区分（server 层据此给调用方可读的 400 而非扁平 500）。
// ctx 控制本次 git 调用的生命周期。
func ensureCleanWorktree(ctx context.Context, dir string) error {
	status, _, err := gitRun(ctx, dir, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("%w: git status: %v", ErrRepoUnusable, err)
	}
	if strings.TrimSpace(status) != "" {
		first := firstLine(status)
		log().Warn("工作区不干净，拒绝派发", "dir", dir, "status", truncateRunes(first, 200))
		return fmt.Errorf("%w: %s", ErrDirtyWorktree, first)
	}
	return nil
}

// maxDirtyFilesListed 是脏快照展示串里最多列出的文件数。
// 封顶而不是全列：这是给人读的一行提示，几十个文件名会把它撑成一屏；
// 真实条数由 RepoDirtyCount 单独承载，不会因为封顶而丢失。
const maxDirtyFilesListed = 5

// repoDirtySnapshot 采集仓库未提交改动的快照（条数 + 文件名展示串）。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - repo: 任务仓库路径（managed 模式下即主仓库）
//
// 返回：
//   - count: 未提交改动总数（含未跟踪文件）；干净或采集失败时为 0
//   - files: 逗号分隔的文件名串，最多 maxDirtyFilesListed 个，超出补「等 N 处」
//
// 注意：
//   - 采集失败不返回错误：这是诊断信息，不该挡住主流程（与 currentRef 同款约定），
//     失败只打 Warn 并返回零值
//   - 不区分已跟踪/未跟踪：B29 分它们是因为处置不同（拒发 vs 警告），这里两者
//     对新工作树同样不可见、处置完全一样，分了只是噪音
func repoDirtySnapshot(ctx context.Context, repo string) (count int, files string) {
	out, _, err := gitRun(ctx, repo, "status", "--porcelain")
	if err != nil {
		log().Warn("采集任务仓库脏快照失败，提示留空（不阻断派发）", "repo", repo, "cause", err)
		return 0, ""
	}
	var names []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// porcelain v1 行格式为 XY<空格>路径；重命名是 "R  旧名 -> 新名"，取新名
		name := strings.TrimSpace(line)
		if len(line) > 3 {
			name = strings.TrimSpace(line[3:])
		}
		if i := strings.LastIndex(name, " -> "); i >= 0 {
			name = name[i+4:]
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return 0, ""
	}
	shown := names
	if len(names) > maxDirtyFilesListed {
		shown = names[:maxDirtyFilesListed]
	}
	files = strings.Join(shown, ", ")
	if len(names) > len(shown) {
		files += fmt.Sprintf(" 等 %d 处", len(names))
	}
	// 服务端日志带完整未截断列表：展示串封顶 5 个是给人读的，排障要看全的
	log().Warn("任务仓库有未提交改动，新工作树不含它们",
		"repo", repo, "count", len(names), "files", strings.Join(names, ", "))
	return len(names), files
}

// worktreeBelongsToRepo 校验 worktree 路径是否属于主仓库 repo。
//
// 两道校验：
//  1. git-common-dir 比对：对 main 仓库与它的任一 worktree 返回同一 git 目录，
//     路径经 EvalSymlinks 归一后相等即归属成立——证明入参「在同一个仓库里」
//  2. show-toplevel 必须等于入参（经 EvalSymlinks 归一）：git-common-dir 会
//     向上查找，/repo/internal/sub 与主仓返回同一 git 目录，只有第二道才证明
//     「入参就是这棵工作树的根」——缺它，仓库子目录会被当成合法 worktree，
//     实际改的是主仓 HEAD、审阅面也被收窄到那个子目录
//
// 任一步失败（路径不是 git 仓库/不是本仓 worktree/不是工作树根）都返回 false
// （fail-closed）。ctx 控制本次 git 调用的生命周期。
func worktreeBelongsToRepo(ctx context.Context, repo, worktree string) bool {
	repoDir, _, err := gitRun(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false
	}
	wtDir, _, err := gitRun(ctx, worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false
	}
	rp, err1 := filepath.EvalSymlinks(strings.TrimSpace(repoDir))
	wp, err2 := filepath.EvalSymlinks(strings.TrimSpace(wtDir))
	if err1 != nil || err2 != nil {
		return false
	}
	if rp != wp {
		return false
	}
	// 第二道：入参必须是工作树的根，不能是它下面的任意子目录。
	// git-common-dir 只证明「在同一个仓库里」，--show-toplevel 才证明
	// 「就是这棵树的根」——缺这道，/repo/internal/sub 会被当成合法 worktree。
	top, _, err := gitRun(ctx, worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	tp, err3 := filepath.EvalSymlinks(strings.TrimSpace(top))
	ap, err4 := filepath.EvalSymlinks(worktree)
	if err3 != nil || err4 != nil {
		return false
	}
	if tp != ap {
		log().Warn("worktree 校验失败：路径不是工作树根（疑似传了仓库子目录）",
			"repo", repo, "worktree", worktree, "toplevel", tp)
		return false
	}
	return true
}

// currentRef 取工作树当前 HEAD 的**可复原引用**：正常在分支上时返回分支名，
// detached 时返回 commit sha。两种形态都能直接喂给 git checkout。
//
// 参数：dir 为工作树路径（原地模式即主仓库）
//
// 返回：引用字符串；取不到时返回空串
//
// 注意：
//   - 返回空串**不是错误**，调用方按「不知道该切回哪儿」处置。采集失败不该
//     挡住派发，但也绝不能拿一个猜测值去 checkout——乱切比不切更糟
func currentRef(ctx context.Context, dir string) string {
	// -q 让 detached 时安静地非零退出，而不是往 stderr 刷错误
	if out, _, err := gitRun(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref
		}
	}
	out, _, err := gitRun(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		log().Warn("采集原 ref 失败，补偿将无法复原工作树", "dir", dir, "cause", err)
		return ""
	}
	return strings.TrimSpace(out)
}

// branchTip 取分支当前尖端 sha。
//
// 参数：repo 为主仓库路径，branch 为分支名
//
// 返回：
//   - 成功时返回 40 位 sha 与 nil
//   - 取不到时返回空串与非 nil 错误（分支不存在、悬空引用、git 调用失败）
//
// 注意：失败不再塌缩成空串，也不在此处打日志——两件事都交给调用方。补偿侧的
// 决策必须能区分「尖端取不到」与「分支不是本次新建的」，旧签名让这两件事共用
// 空串，正是那道闸无法被单独测试的根因（见
// docs/superpowers/specs/2026-08-10-compensation-branch-decision-design.md §2）。
func branchTip(ctx context.Context, repo, branch string) (string, error) {
	out, stderr, err := gitRun(ctx, repo, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("git rev-parse refs/heads/%s: %s: %w", branch, strings.TrimSpace(stderr), err)
	}
	return strings.TrimSpace(out), nil
}

// recordNewBranchTip 记录刚新建分支的尖端，作为补偿路径复核「自创建以来零提交」
// 的基线。PrepareWorkspace 的三条建分支路径（managed worktree / 用户树 / 原地）
// 共用它，避免同一段告警逻辑抄三遍。
//
// 参数：repo 为主仓库路径，branch 为刚新建的分支名
//
// 返回：取到则为 40 位 sha；取不到返回空串。
//
// 注意：取不到时返回空串是刻意的保守选择——没记到基线意味着补偿届时无从复核，
// 空串会让 decideBranchAction 判成 branchKeepNotOurs，即保留该分支不删。代价是
// 补偿时日志说不清「为什么不删」，所以失败必须在**这里**留下 WARN。
func recordNewBranchTip(ctx context.Context, repo, branch string) string {
	tip, err := branchTip(ctx, repo, branch)
	if err != nil {
		log().Warn("新建分支后取尖端失败，补偿将保留该分支", "repo", repo, "branch", branch, "cause", err)
		return ""
	}
	return tip
}

// removeWorktreeAttempts / removeWorktreeBackoff 是删 worktree 的重试参数。
//
// 为什么需要重试：Kill 的复核判据是 shim 的存活锁，而执行者子进程是被 job 的
// KILL_ON_JOB_CLOSE 连坐杀掉的。shim 拆解时「锁释放」与「连坐」是两个并列后果，
// 内核不保证顺序——于是存在一个窗口：Alive() 已转 false，而执行者仍活着，它的
// cwd 正是这棵 worktree（Windows 上等于一个不带 FILE_SHARE_DELETE 的目录句柄）。
//
// unix 上第一次就会成功（允许删除作为他人 cwd 的目录），重试是零代价；统一启用
// 是为了避免出现一条只在 Windows 上走过的路径。
//
// 是变量而非常量：测试要把它们调到毫秒级，否则每条用例都真等几秒。
var (
	removeWorktreeAttempts = 5
	removeWorktreeBackoff  = 400 * time.Millisecond
)

// worktreeRemoveFn 是单次 git worktree remove 的测试缝。
// **生产路径恒为下面的默认值**，非测试代码不得赋值。
var worktreeRemoveFn = func(ctx context.Context, repo, workdir string) (string, error) {
	_, stderr, err := gitRun(ctx, repo, "worktree", "remove", workdir)
	return stderr, err
}

// RemoveManagedWorktree 删除 agentd 管理的 worktree（git -C repo worktree remove workdir）。
//
// 参数：
//   - ctx: 控制整组 git 调用的生命周期，内部再叠加 WorkspaceGitTimeout 作为兜底上限
//   - repo: 主仓库路径
//   - workdir: 待删除的 worktree 路径（必须为 Managed=true 的工作区）
//
// 返回：error 非 nil 表示重试耗尽仍未删掉；调用方按现状只 Warn 不阻断。
//
// 注意：
//   - 只删工作树不删分支（spec：任务分支保留供审阅/回滚）
//   - workdir 带未提交改动时 git 拒绝删除（错误带 stderr 原文返回）；是否降级
//     由调用方（Done 归档）决定——本函数不做清理性降级
//   - 失败会重试若干次，见 removeWorktreeAttempts 的注释
func RemoveManagedWorktree(ctx context.Context, repo, workdir string) error {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	log().Info("删除 managed worktree", "repo", repo, "workdir", workdir,
		"timeout", WorkspaceGitTimeout, "attempts", removeWorktreeAttempts)
	var lastStderr string
	var lastErr error
	for i := 1; i <= removeWorktreeAttempts; i++ {
		stderr, err := worktreeRemoveFn(ctx, repo, workdir)
		if err == nil {
			log().Info("managed worktree 已删除", "repo", repo, "workdir", workdir, "attempt", i)
			return nil
		}
		lastStderr, lastErr = stderr, err
		if i < removeWorktreeAttempts {
			// 常见成因是执行者进程还没被内核收干净、cwd 句柄仍钉着这棵树；
			// 退一步等它散场，比当场放弃留一棵残树划算
			log().Warn("删除 managed worktree 失败，稍后重试", "repo", repo, "workdir", workdir,
				"attempt", i, "of", removeWorktreeAttempts,
				"stderr", truncateRunes(stderr, 300), "cause", err)
			select {
			case <-ctx.Done():
				log().Error("删除 managed worktree 被取消", "repo", repo, "workdir", workdir,
					"attempt", i, "cause", ctx.Err())
				return fmt.Errorf("git worktree remove %s: %w", workdir, ctx.Err())
			case <-time.After(removeWorktreeBackoff):
			}
		}
	}
	log().Error("删除 managed worktree 失败（重试耗尽）", "repo", repo, "workdir", workdir,
		"attempts", removeWorktreeAttempts, "stderr", truncateRunes(lastStderr, 300), "cause", lastErr)
	return fmt.Errorf("git worktree remove %s: %s: %w", workdir, strings.TrimSpace(lastStderr), lastErr)
}

// FetchTimeout 是基线缺失时补拉远端的时长上限。
// 独立于 WorkspaceGitTimeout：fetch 走网络，与本地 git 操作不是一个量级。
var FetchTimeout = 2 * time.Minute

// baseCommitRe 限定基线只能是 40 位小写十六进制（git rev-parse HEAD 的输出形态）。
var baseCommitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Baseline 是一次基线决议的结果：校验结论与新分支起点出自同一次计算。
//
// 为什么必须是同一个结构而不是分两次算：B35 的根因就是「校验这个 sha 存不存在」
// 与「新分支从哪起」由两段代码各自决定，中间没有任何连接——校验通过了，分支却
// 从任务仓库 HEAD 开出去，两者可以静默地差出几十个提交而不留任何痕迹。
type Baseline struct {
	// Start 是新分支起点（40 位 sha）。任务仓库一个提交都没有时为空，
	// 退回 git 默认行为（空仓库上 checkout -b 本来就不能带起点）。
	Start string
	// Ahead 是任务仓库 HEAD 上有、而 Start 上没有的提交数——这些提交不会进新分支。
	Ahead int
	// Fetched 表示是否为定位 Start 补拉过远端。只用于日志：排障时要能分清
	// 「基线本来就在」与「补拉才拿到」，前者说明两边同步，后者说明执行机落后过。
	Fetched bool
}

// resolveCommit 把任意 commit-ish（分支名/tag/sha）解析成 40 位提交号。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - repo: 任务仓库路径
//   - rev: 待解析的起点原文（用户的 --base 或决议出的基线）
//
// 返回：
//   - 40 位 sha；解析不出或歧义时返回包 ErrBadWorkspaceReq 的错误（server 映射 400）
//
// 为什么起点必须以 sha 形态交给 git（B76）：给分支名会触发 DWIM——base 只有
// origin/<name> 时，`worktree add -b X <dir> <base>` 会忽略显式的 -b、开出
// 名为 <base> 的分支，且退出码为 0。传 sha 让 DWIM 从原理上无从发生。
//
// 注意：^{commit} 的剥离是必需的：rev 可能是 annotated tag，裸解析给的是
// tag 对象而不是提交。rev-parse 本身不做「远程跟踪分支简写」的 DWIM，这里
// 手动补一次唯一匹配（与 git checkout 的 guess_remote 同语义），保证
// --base <只有远程跟踪 ref 的分支> 的用户语义不变；多个远端同时同名时视为
// 歧义拒发，错误文本列出全部候选 ref 并给出出路（用带远端前缀的全名或 sha）——
// 歧义不是「不存在」，把它降级成 git push 建议会把 fork 工作流的协调者引向
// 错误排查方向。
func resolveCommit(ctx context.Context, repo, rev string) (string, error) {
	out, stderr, err := gitProbe(ctx, repo, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	sha := strings.TrimSpace(out)
	if err == nil && sha != "" {
		log().Info("起点已解析为提交号", "repo", repo, "base", rev, "sha", sha)
		return sha, nil
	}
	// rev-parse 不 DWIM：base 分支只以 refs/remotes/*/<rev> 存在时上面的调用取不到
	// （B76 的触发前提正是这种仓库）。按 git checkout 的 guess 语义补一次「唯一
	// 远程跟踪 ref」匹配，剥到 commit 后与主路径同款校验与落日志。
	matches, _, _ := gitRun(ctx, repo, "for-each-ref", "--format=%(refname)", "refs/remotes/*/"+rev)
	var cands []string
	for _, line := range strings.Split(matches, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			cands = append(cands, line)
		}
	}
	// 多于一棵远端同时有这个分支名：歧义，拒发（git 同样不猜）。歧义不是
	// 「不存在」——起点明明在，让协调者去 git push 是把他引向错误方向（fork
	// 工作流 origin+upstream 同名分支会真实撞上）；出路是给带远端前缀的全名
	// 或直接给 sha，两者 resolveCommit 都认。
	if len(cands) > 1 {
		log().Warn("起点解析歧义：多棵远端都有同名分支，拒绝派发",
			"repo", repo, "base", rev, "cands", cands)
		return "", fmt.Errorf("%w: 起点 %s 在多个远端同时存在（%s）；"+
			"请改用带远端前缀的全名（如 --base upstream/%s）或直接给 40 位 sha",
			ErrBadWorkspaceReq, rev, strings.Join(cands, "、"), rev)
	}
	if len(cands) == 1 {
		if mout, _, e := gitProbe(ctx, repo, "rev-parse", "--verify", "--quiet", cands[0]+"^{commit}"); e == nil && strings.TrimSpace(mout) != "" {
			sha = strings.TrimSpace(mout)
			log().Info("起点已解析为提交号（远程跟踪分支）", "repo", repo, "base", rev, "sha", sha, "ref", cands[0])
			return sha, nil
		}
	}
	log().Warn("起点解析失败，拒绝派发", "repo", repo, "base", rev,
		"stderr", strings.TrimSpace(truncateRunes(stderr, 300)))
	return "", fmt.Errorf("%w: 起点 %s 在任务仓库中不存在"+
		"（若它是你本地的分支，先 git push 再派发；或换一个起点）", ErrBadWorkspaceReq, rev)
}

// ResolveBaseline 决议任务的基线：校验协调者本地基线在任务仓库中可用，并给出
// 新分支应当使用的起点与「任务仓库比它多出多少提交」。
//
// 参数：
//   - ctx: 上层上下文；fetch 阶段内部叠加 FetchTimeout
//   - repo: 任务仓库路径
//   - sha: 协调者本地 HEAD 的 40 位十六进制提交号；空=未提供（--no-sync-check
//     或调用方 cwd 不是 git 仓库），此时起点退回任务仓库当前 HEAD
//
// 返回：
//   - Baseline: Start=新分支起点；Ahead=任务仓库 HEAD 领先 Start 的提交数
//   - ErrBadWorkspaceReq: sha 格式非法（会拼进 git 参数，不校验等于开注入面）
//   - ErrBaseCommitMissing: fetch 后仍缺失，错误文本含 sha、fetch stderr 与动作提示
//
// 注意：
//   - 空 sha 也返回一个具体的 Start：让「这个任务建在哪个提交上」在任何路径下
//     都答得出来，包括 --no-sync-check 那条——今天那条路上基线是纯粹的空白
//   - 「命中才不 fetch」是刻意设计：常态下远程并不落后，cat-file 是纯本地对象库
//     查询（微秒级），只有真落后时才付网络代价
//   - fetch 失败（无凭证/网络不通）不单独成一类错误，一并归入 ErrBaseCommitMissing：
//     对调用方而言结论都是「这次派不出去，先解决远程仓库」，stderr 原文已带出根因
func ResolveBaseline(ctx context.Context, repo, sha string) (Baseline, error) {
	if sha == "" {
		head := headCommit(ctx, repo)
		log().Info("未提供基线提交，起点退回任务仓库 HEAD", "repo", repo, "start", head)
		return Baseline{Start: head}, nil
	}
	if !baseCommitRe.MatchString(sha) {
		log().Warn("基线提交格式非法，拒绝派发", "repo", repo, "base_commit", truncateRunes(sha, 80))
		return Baseline{}, fmt.Errorf("%w: 基线提交必须是 40 位十六进制，实得 %q", ErrBadWorkspaceReq, truncateRunes(sha, 80))
	}
	fetched := false
	if !hasCommit(ctx, repo, sha) {
		log().Info("基线提交缺失，补拉远端", "repo", repo, "base_commit", sha, "timeout", FetchTimeout)
		fctx, cancel := context.WithTimeout(ctx, FetchTimeout)
		defer cancel()
		_, stderr, ferr := gitRunNet(fctx, repo, "fetch", "--all", "--prune")
		if ferr != nil {
			log().Error("补拉远端失败", "repo", repo, "base_commit", sha,
				"stderr", truncateRunes(stderr, 500), "cause", ferr)
		}
		fetched = true
		if !hasCommit(ctx, repo, sha) {
			log().Warn("基线提交补拉后仍缺失，拒绝派发", "repo", repo, "base_commit", sha)
			return Baseline{}, fmt.Errorf("%w: %s（任务仓库 %s 落后于本地；fetch 输出：%s）；请先在本地 git push，或用 --no-sync-check 跳过校验",
				ErrBaseCommitMissing, sha, repo, strings.TrimSpace(truncateRunes(stderr, 300)))
		}
		log().Info("补拉远端后基线提交已就位", "repo", repo, "base_commit", sha)
	}
	bl := Baseline{Start: sha, Ahead: countAhead(ctx, repo, sha), Fetched: fetched}
	log().Info("基线决议完成", "repo", repo, "start", bl.Start, "ahead", bl.Ahead, "fetched", bl.Fetched)
	return bl, nil
}

// headCommit 取仓库当前 HEAD 的完整 sha。
//
// 返回空串只对应「仓库一个提交都没有」：仓库有效性已由 Dispatch 前置的
// EnsureRepoUsable 保证（B45），走到这里时路径一定是可用的 git 仓库。
// 空起点交给 git 默认行为，不是错误。
func headCommit(ctx context.Context, repo string) string {
	out, _, err := gitRun(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		log().Info("任务仓库没有 HEAD（空仓库或非 git 仓库），起点留空", "repo", repo)
		return ""
	}
	return strings.TrimSpace(out)
}

// countAhead 数任务仓库 HEAD 上有、而 start 上没有的提交数。
//
// 数不出来时返回 0 并打 Warn：这是给人看的提示数字，不该因为数不出来就把
// 整次派发拒掉——起点本身已经校验过了，提示缺失不影响正确性。
func countAhead(ctx context.Context, repo, start string) int {
	out, _, err := gitRun(ctx, repo, "rev-list", "--count", start+"..HEAD")
	if err != nil {
		log().Warn("统计任务仓库领先提交数失败，按 0 处理", "repo", repo, "start", start, "cause", err)
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		log().Warn("领先提交数解析失败，按 0 处理", "repo", repo,
			"out", truncateRunes(out, 80), "cause", err)
		return 0
	}
	return n
}

// hasCommit 判断 sha 是否已在 repo 的对象库中（^{commit} 保证它确实是提交对象，
// 而不是同名的 tree/blob）。
func hasCommit(ctx context.Context, repo, sha string) bool {
	_, _, err := gitProbe(ctx, repo, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// id8 截取任务 ID 前 8 字节，用于分支名与 worktree 目录名的稳定短标识。
// 与执行者进程的短 id 展示共用同一截断规则，
// 改动必须两侧同步（见 attach 命令的 id8 注释）。
func id8(taskID string) string {
	if len(taskID) > 8 {
		return taskID[:8]
	}
	return taskID
}

// taskBranch 由任务 ID 派生分支名 handoff/<id8>。
func taskBranch(taskID string) string {
	return "handoff/" + id8(taskID)
}

// Diff 取任务分支相对基准分支的完整审阅素材：git diff <base>...HEAD 的差异
// + git log --oneline <base>..HEAD 的提交列表，按序拼接。
//
// 参数：
//   - repo: 任务仓库路径
//   - baseBranch: 基准分支（通常为派发前的分支，如 main/master）
//
// 返回：
//   - 拼接后的文本（diff 为空时只有提交列表，两边都空时为空串）
//   - err: git 失败返回错误（如 baseBranch 不存在，stderr 已并入日志）；
//     baseBranch 以 "-" 开头返回 ErrBadBaseBranch
func Diff(repo, baseBranch string) (string, error) {
	// 拒绝空串与 "-" 前缀的 base（L-4）：空 base 会拼出 "git diff ...HEAD" 的
	// 裸 diff（不是相对基准分支的语义）；以 "-" 开头则会被 git 解释为选项而非
	// rev——如 "--output=/tmp/x" 会让 git 把 diff 写到任意路径（git 参数注入）。
	// handoff 的 base 只可能是分支名或 commit（合法 rev），不会以 "-" 开头
	// （"-" rev 的用法如 "-" 表示 stdin 与本工具无关），一律拒绝是保守且正确的
	if baseBranch == "" || strings.HasPrefix(baseBranch, "-") {
		log().Warn("diff 基准分支非法（空或 - 前缀，疑似 git 参数注入）", "repo", repo, "base", baseBranch)
		return "", fmt.Errorf("%w: %q", ErrBadBaseBranch, baseBranch)
	}
	diffText, _, err := gitRun(context.Background(), repo, "diff", baseBranch+"...HEAD")
	if err != nil {
		return "", fmt.Errorf("git diff %s...HEAD: %w", baseBranch, err)
	}
	logText, _, err := gitRun(context.Background(), repo, "log", "--oneline", baseBranch+"..HEAD")
	if err != nil {
		return "", fmt.Errorf("git log %s..HEAD: %w", baseBranch, err)
	}
	var parts []string
	if d := strings.TrimSpace(diffText); d != "" {
		parts = append(parts, d)
	}
	if l := strings.TrimSpace(logText); l != "" {
		parts = append(parts, l)
	}
	return strings.Join(parts, "\n\n"), nil
}

// diffBaseFor 返回任务 diff 的缺省基准 rev：任务自己的 BaseCommit 优先，
// 为空才按仓库推导。
//
// 为什么优先任务基线（B65）：按仓库默认分支推导会把「默认分支与任务分支之间的
// 全部历史」也算进 diff——实测一个真实任务默认吐 26611 行而真实改动只有 3274 行，
// 审核者第一眼拿到的素材被淹掉。任务记录里本来就有它建在哪个提交上（B35 起）。
//
// BaseCommit 为空**不是缺字段**：proto 注释写明「空=切已存在分支（没有起点这回事）
// 或老任务」，所以退回推导链是正常分支，不是兜底。
//
// 返回空串表示两条路都取不到（非 git 仓库、既无 main 也无 master），
// 由调用方报 400 并提示显式指定 base。
func diffBaseFor(task *proto.Task, repo string) string {
	if task != nil && task.BaseCommit != "" {
		return task.BaseCommit
	}
	return resolveBaseBranch(repo)
}

// resolveBaseBranch 确定 diff 的默认基准分支，优先级：
//  1. 仓库远端默认分支（refs/remotes/origin/HEAD 符号引用，如 origin/main）
//  2. 本地 main
//  3. 本地 master
//  4. 都没有 → 空串（由路由层报错，提示协调者显式 --base）
//
// 为什么需要这个推导链：任务仓库的分支名不可预知（main/master/dev 皆可能）。
//
// 注意（B65 更正）：「派发时并未记录基准分支名」这句原文已经过时——B35 起任务
// 记录里有 BaseCommit。缺省基准现在由 diffBaseFor 决定，本函数只负责
// **没有任务基线时**的推导，不再是 diff 的唯一入口。
func resolveBaseBranch(repo string) string {
	if out, _, err := gitRun(context.Background(), repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	for _, cand := range []string{"main", "master"} {
		// 这里的未命中是正常分支（仓库可能既没有 main 也没有 master），走 gitProbe
		// 才不会在成功路径上留 ERROR。
		if _, _, err := gitProbe(context.Background(), repo, "rev-parse", "--verify", "--quiet", cand); err == nil {
			return cand
		}
	}
	log().Warn("无法确定基准分支，diff 需要显式 base", "repo", repo)
	return ""
}

// Branches 列出仓库的本地分支名（refname:short，字母序）。
//
// 供审阅栏「改动」的基准分支下拉用：协调者从列表里选，不手填。
// 只列本地分支——diff 的 base 语义是本地 rev，远端跟踪分支由默认推导覆盖。
//
// 返回：分支名切片（可能为空，如空仓库）；git 失败返回错误（stderr 在 err 里）。
func Branches(repo string) ([]string, error) {
	out, _, err := gitRun(context.Background(), repo, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		log().Error("列分支失败", "repo", repo, "cause", err)
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if b := strings.TrimSpace(line); b != "" {
			branches = append(branches, b)
		}
	}
	log().Debug("列分支完成", "repo", repo, "count", len(branches))
	return branches, nil
}

// binaryProbeBytes 是二进制判定的探测长度：前 8 KiB 内出现 NUL 字节即判为二进制。
//
// 判据抄自 orca 的 relay 文件通道（BINARY_PROBE_BYTES = 8192）。它朴素、无依赖，
// 且对源码/配置/文案这类真正需要在线编辑的东西零误判——真正的文本文件不会在
// 头 8 KiB 里塞 NUL。
const binaryProbeBytes = 8192

// isBinaryPrefix 判定一段内容的开头是否含 NUL 字节。
//
// 参数：
//   - b: 已读到的内容（可能已被 maxRunOutput 截断）
//
// 返回：前 min(len(b), 8192) 字节内出现 0x00 为真
func isBinaryPrefix(b []byte) bool {
	if len(b) > binaryProbeBytes {
		b = b[:binaryProbeBytes]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// isGitPath 判定一个已 Clean 的相对路径是否落在工作树根的 .git 下。
//
// 为什么要挡：`.git` 在 worktree 里是个几十字节的指针文件（内容 `gitdir: <路径>`），
// 改它能把整个工作树重指向别处；在主仓库里 `.git/config` 写进 core.pager /
// core.sshCommand / hooksPath，就是下一次任何 git 操作时的任意命令执行，改 HEAD、
// 删 index 也都能直接搞坏仓库。
//
// 这不是提权（控制台会话本来就与主令牌等价，见 spec §1.1），是「一次误操作就把
// 仓库弄坏」——正是那条参数校验闸门该挡的东西。
//
// 只挡工作树**根下**的 .git：嵌套子模块不在本期范围。前缀相同的 .gitignore /
// .gitattributes 不受影响。
//
// 参数：
//   - cleaned: 已经 filepath.Clean 过的相对路径
func isGitPath(cleaned string) bool {
	return cleaned == ".git" || strings.HasPrefix(cleaned, ".git"+string(filepath.Separator))
}

// atomicReplace 用同目录临时文件 + rename 原子替换目标文件。
//
// 为什么做原子替换（而不是像 orca 那样对用户文件裸 WriteFile）：executor 就在
// 同一个工作树里跑，裸覆盖有一个窗口能让它读到半截文件。orca 的编辑对象通常
// 没有一个高频读者在旁边，我们有。
//
// 为什么**不** fsync：工作树在 git 管着，掉电丢一次编辑不是灾难，而每次保存
// fsync 的代价在远程机上更明显。orca 只对自己的状态文件做 fsync——那些丢了
// 没有第二份，工作树文件不是。
//
// 参数：
//   - root: 已打开的工作树 Root（全程不出根）
//   - cleaned: 目标文件的相对路径
//   - data: 新内容
//   - perm: 目标文件原有的权限位（保留可执行位，丢了是静默故障）
func atomicReplace(root *os.Root, cleaned string, data []byte, perm fs.FileMode) error {
	tmp := filepath.Join(filepath.Dir(cleaned),
		fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(cleaned), os.Getpid(), time.Now().UnixNano()))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("创建临时文件 %s: %w", tmp, err)
	}
	// 任何没走到 rename 的路径（含 panic）都要把 tmp 删掉：留一个 .foo.tmp
	// 在工作树里会进 git status，下一次 dispatch 的「工作区必须干净」检查会直接拒发
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			if err := root.Remove(tmp); err != nil {
				log().Warn("清理临时文件失败", "tmp", tmp, "cause", err)
			}
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("写临时文件 %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭临时文件 %s: %w", tmp, err)
	}
	if err := root.Rename(tmp, cleaned); err != nil {
		return fmt.Errorf("替换文件 %s: %w", cleaned, err)
	}
	committed = true
	return nil
}

// WriteFile 用新内容原子替换工作树内一个已存在的文本文件，带基线哈希前置条件。
//
// 冲突保护的整个机制就是这个前置条件：调用方把它**读到那一版**的 sha256 带回来，
// 本函数比对磁盘现状，不一致就拒绝并把现状带回去。为什么不用 mtime：executor 在
// 工作树里频繁跑 git 操作，checkout/rebase 会动 mtime 但不动内容，用 mtime 会把
// 大量无害情况报成冲突。
//
// **已知窗口，如实记录**：第 6 步读哈希与第 9 步 rename 之间不是原子的。executor
// 恰好在这个窗口里写同一个文件，本函数检测不到，结果是它的改动被覆盖。加锁解决
// 不了——锁只挡得住 agentd 自己的并发写，executor 直接动文件系统，根本不经过这里。
// 窗口从「整个编辑时长」缩到「一次读 + 一次 rename」，是这条路能拿到的全部。
//
// 参数：
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - rel: 相对工作树根的路径
//   - content: 新内容
//   - baseSHA256: 调用方读到那一版的 sha256 十六进制串；空串一律判为不匹配
//
// 返回：
//   - err == nil：res 是**新内容**的结论（SHA256 可直接当下一次的基线，Size 是新大小）
//   - errors.Is(err, ErrBaseMismatch)：res 是**磁盘现状**（含正文与现状哈希），
//     给调用方省一次往返
//   - 其余错误：res 是零值
//   - 错误取值：ErrPathEscape / ErrGitDirWrite / ErrSymlinkTarget / ErrPathIsDir /
//     ErrNotRegularFile / ErrBinaryFile / ErrFileTooLarge / ErrBaseMismatch /
//     fs.ErrNotExist（含 %w 链）
func WriteFile(repo, rel, content, baseSHA256 string) (proto.FileRead, error) {
	cleaned := filepath.Clean(rel)
	if rel == "" || cleaned == "." || filepath.IsAbs(cleaned) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		log().Warn("文件写入路径逃逸被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	if isGitPath(cleaned) {
		log().Warn("文件写入命中 .git 被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrGitDirWrite, rel)
	}
	// 新内容也受同一个上限约束。理由对称：读不回来的东西就不该写得进去，
	// 否则存一次之后这个文件自己就变成不可编辑的了
	if len(content) > maxRunOutput {
		log().Warn("写入内容超过上限被拒绝", "repo", repo, "path", rel,
			"bytes", len(content), "limit", maxRunOutput)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrFileTooLarge, rel)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return proto.FileRead{}, fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()

	// Lstat 而不是 Stat：要看到符号链接本身。原子替换的 rename 会把链接换成普通
	// 文件，语义悄悄就变了——与其猜用户想改链接还是改目标，不如拒掉并说清楚
	fi, err := root.Lstat(cleaned)
	if err != nil {
		if rootErrIsEscape(err) {
			log().Warn("文件写入路径逃逸被拒绝", "repo", repo, "path", rel)
			return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathEscape, rel)
		}
		return proto.FileRead{}, fmt.Errorf("检查文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		log().Warn("文件写入目标是符号链接被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrSymlinkTarget, rel)
	case fi.IsDir():
		log().Warn("文件写入目标是目录被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathIsDir, rel)
	case !fi.Mode().IsRegular():
		log().Warn("文件写入目标不是普通文件被拒绝", "repo", repo, "path", rel,
			"mode", fi.Mode().String())
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrNotRegularFile, rel)
	}

	// 复用 ReadFile 而不是另写一遍读取：「这文件能不能编辑」的判据必须由**同一段
	// 代码**在读侧和写侧给出。两边各判一次，早晚会分叉成「前端说能编辑、后端说不能」
	cur, err := ReadFile(repo, cleaned)
	if err != nil {
		return proto.FileRead{}, err
	}
	// 这两种情况下 cur.SHA256 必然是空值，下面的比对必定不通过——但要在这里用
	// **说得清的理由**拒掉，而不是让它掉进一个「哈希对不上」的 409，
	// 那会让用户以为「文件被谁改了」
	if cur.Binary {
		log().Warn("文件写入目标是二进制被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrBinaryFile, rel)
	}
	if cur.Truncated {
		log().Warn("文件写入目标超过读取上限被拒绝", "repo", repo, "path", rel, "size", cur.Size)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrFileTooLarge, rel)
	}
	if cur.SHA256 != baseSHA256 {
		log().Warn("文件写入基线不匹配", "repo", repo, "path", rel,
			"base", shortHash(baseSHA256), "current", shortHash(cur.SHA256))
		return cur, fmt.Errorf("%w: %q", ErrBaseMismatch, rel)
	}

	log().Info("开始原子替换文件", "repo", repo, "path", rel, "bytes", len(content))
	if err := atomicReplace(root, cleaned, []byte(content), fi.Mode().Perm()); err != nil {
		log().Error("文件写入失败", "repo", repo, "path", rel, "cause", err)
		return proto.FileRead{}, err
	}
	sum := sha256.Sum256([]byte(content))
	res := proto.FileRead{
		Content: content,
		Size:    int64(len(content)),
		SHA256:  hex.EncodeToString(sum[:]),
	}
	log().Info("文件写入完成", "repo", repo, "path", rel,
		"bytes", res.Size, "sha256", shortHash(res.SHA256))
	return res, nil
}

// shortHash 取哈希前 8 位供日志用。全量 64 位十六进制串在日志里既占地方又没人读，
// 而排障时要的只是「这两个是不是同一个」。
func shortHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}

// ReadFile 读取任务仓库内相对路径文件的内容（审核者取上下文用）。
//
// 路径逃逸防御（安全红线，两道）：
//  1. filepath.Clean 归一化后，任何绝对路径或残留 .. 前缀的路径一律拒绝（ErrPathEscape）
//  2. 实际打开经 os.OpenRoot（内核级 jail）：路径中任何符号链接指向仓库外时，
//     打开直接失败。为什么不用 EvalSymlinks 前缀校验：那是「先校验、后打开」两步，
//     校验与打开之间有 TOCTOU 竞态窗口——恶意 executor 经 run 命令完全能在这个
//     窗口里把链接换成指向仓库外的版本；os.OpenRoot 的解析在单次打开内由内核完成，
//     无窗口。
//
// os.OpenRoot 的边界（stdlib 契约，Go 1.24+）：符号链接目标必须是相对路径
// （"Symbolic links must not be absolute"），故绝对目标链接一律拒绝（ErrPathEscape），
// 即使目标在仓库内也拒绝——保守语义，见 TestReadFileSymlinkEscape；
// 仓库根自身是符号链接时 OpenRoot 跟随之，读链接指向的真实仓库，正常可用。
//
// 大小上限：只读 maxRunOutput+1 字节（+1 仅用于判定是否超限），超限截断并 Warn——
// 与 RunCmd 的输出截断语义一致：返回开头、不整读内存，64MiB 大文件不会把 agentd 读挂。
// 截断而非拒绝：fetch 的用途是看文件开头（审阅上下文），1MiB 对源文件足够；
// 大文件多为生成物/数据文件，拒绝会让审核者误以为路径有误。
// 截断提示不再由本函数拼接，改由端点层按各自契约决定（handleTaskFile 拼、
// handleWorkspaceFile 不拼）——本函数返回的是保真的读，在线编辑那条线才敢把
// 内容原样存回磁盘。
//
// 非普通文件（目录/管道/设备等）一律拒绝：目录给 ErrPathIsDir（400 语义），
// 其余特殊文件 read 语义不可控（可能无限输出或永久阻塞），给 ErrNotRegularFile。
//
// 参数：
//   - repo: 任务仓库路径
//   - rel: 相对仓库根的路径（如 cmd/foo.go）
//
// 返回：
//   - proto.FileRead{Content, Size, Truncated, Binary, SHA256}，其中 SHA256 仅当
//     !Binary && !Truncated 时有值
//   - err: 路径逃逸（含符号链接逃逸）返回 ErrPathEscape；目录返回 ErrPathIsDir；
//     其他特殊文件返回 ErrNotRegularFile；文件不存在返回 *fs.PathError（含 %w 链）
func ReadFile(repo, rel string) (proto.FileRead, error) {
	cleaned := filepath.Clean(rel)
	if rel == "" || cleaned == "." || filepath.IsAbs(cleaned) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		log().Warn("文件读取路径逃逸被拒绝", "repo", repo, "path", rel)
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return proto.FileRead{}, fmt.Errorf("打开任务仓库 %s: %w", repo, err)
	}
	defer root.Close()
	// 以 O_NONBLOCK 打开（why）：没有写端的 FIFO 会让 openat 本身一直挂住，
	// 而 IsRegular 检查排在打开之后——ErrNotRegularFile 对 FIFO 根本不可达，
	// handler goroutine 与 fd 就此永久泄漏，而 executor 可以随手 mkfifo。
	// O_NONBLOCK 让特殊文件立即返回，交给下面的 IsRegular 正常拒绝；
	// 对普通文件它没有任何语义影响
	f, err := root.OpenFile(cleaned, os.O_RDONLY|openNonBlock, 0)
	if err != nil {
		if rootErrIsEscape(err) {
			// 符号链接逃逸在 OpenRoot 层被内核拒绝：与词汇层逃逸同一语义
			log().Warn("文件读取路径逃逸被拒绝", "repo", repo, "path", rel)
			return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathEscape, rel)
		}
		return proto.FileRead{}, fmt.Errorf("读取文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return proto.FileRead{}, fmt.Errorf("读取文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	if !fi.Mode().IsRegular() {
		log().Warn("文件读取目标不是普通文件", "repo", repo, "path", rel, "mode", fi.Mode().String())
		if fi.IsDir() {
			return proto.FileRead{}, fmt.Errorf("%w: %q", ErrPathIsDir, rel)
		}
		return proto.FileRead{}, fmt.Errorf("%w: %q", ErrNotRegularFile, rel)
	}
	// 只读 maxRunOutput+1 字节：多出的 1 字节用于判定「是否超限」，
	// 不额外多一次 Stat 也能得到截断结论（真实大小取已打开的 f.Stat）
	b, err := io.ReadAll(io.LimitReader(f, int64(maxRunOutput)+1))
	if err != nil {
		return proto.FileRead{}, fmt.Errorf("读取文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	out := proto.FileRead{Size: fi.Size()}
	if len(b) > maxRunOutput {
		log().Warn("文件超过读取上限，内容已截断", "repo", repo, "path", rel,
			"size", fi.Size(), "limit", maxRunOutput)
		// 合并 B102：main 侧在 ReadFile 里拼截断提示并返回 string，w4 侧改签名为
		// proto.FileRead 并设 Truncated 标志、由端点层各自决定要不要拼提示。这里取
		// w4 侧——合并后的 server.go handleTaskFile 按 w4 契约读 res.Truncated +
		// truncatedNotice(res.Size)，main 侧那几行与 FileRead 签名不匹配。
		out.Truncated = true
		b = b[:maxRunOutput]
	}
	out.Content = string(b)
	out.Binary = isBinaryPrefix(b)
	// 哈希只在「完整且是文本」时才算：它唯一的用途是当写入的前置条件，
	// 而截断内容当基线等于允许把文件截断后存回去，二进制本来就不许写。
	// 空值在契约上就是「这文件不可编辑」，前后端共用这一个判据
	if !out.Binary && !out.Truncated {
		sum := sha256.Sum256(b)
		out.SHA256 = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// ListDir 列举工作树内某个目录的**直接子项**，不递归。
//
// 与 ReadFile 共用同一套路径防护（安全红线，两道）：
//  1. filepath.Clean 归一化后，绝对路径或残留 .. 前缀一律拒绝（ErrPathEscape）
//  2. 实际打开经 os.OpenRoot（内核级 jail），符号链接逃逸由内核在单次系统调用
//     内拒绝，不留 TOCTOU 窗口
//
// 为什么不递归：一次递归列举一个大仓库要遍历几十万个 inode，而前端一次只画
// 一层。按需展开把成本摊到用户真正点开的那几层上。
//
// 排序：目录在前、各自按名称字典序。为什么由服务端排而不是前端排：前端会有
// 搜索过滤与虚拟滚动，排序稳定性交给一处比在多处各排一次可靠。
//
// 参数：
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - rel: 相对工作树根的目录路径；"" 与 "." 都表示根
//
// 返回：
//   - 子项列表（**永不为 nil**）
//   - err: 逃逸返回 ErrPathEscape；目标不是目录返回 ErrPathNotDir；
//     目标不存在返回 *fs.PathError（含 %w 链，errors.Is(err, fs.ErrNotExist) 为真）
func ListDir(repo, rel string) ([]proto.DirEntry, error) {
	cleaned := filepath.Clean(rel)
	if rel == "" {
		cleaned = "."
	}
	if filepath.IsAbs(cleaned) || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		log().Warn("目录列举路径逃逸被拒绝", "repo", repo, "path", rel)
		return nil, fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()
	// O_NONBLOCK 的理由与 ReadFile 相同：没有写端的 FIFO 会让 openat 永久挂住，
	// 而「不是目录」的判定排在打开之后
	f, err := root.OpenFile(cleaned, os.O_RDONLY|openNonBlock, 0)
	if err != nil {
		if rootErrIsEscape(err) {
			log().Warn("目录列举路径逃逸被拒绝", "repo", repo, "path", rel)
			return nil, fmt.Errorf("%w: %q", ErrPathEscape, rel)
		}
		return nil, fmt.Errorf("列举目录 %s: %w", filepath.Join(repo, cleaned), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("列举目录 %s: %w", filepath.Join(repo, cleaned), err)
	}
	if !fi.IsDir() {
		log().Warn("目录列举目标不是目录", "repo", repo, "path", rel, "mode", fi.Mode().String())
		return nil, fmt.Errorf("%w: %q", ErrPathNotDir, rel)
	}
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("列举目录 %s: %w", filepath.Join(repo, cleaned), err)
	}
	entries := make([]proto.DirEntry, 0, len(des))
	for _, de := range des {
		e := proto.DirEntry{Name: de.Name(), IsDir: de.IsDir()}
		if !de.IsDir() {
			// Info 失败（列举与 stat 之间文件被删）不是整次列举的失败：
			// 少一个 size 比整棵树列不出来强，如实按 0 记并 Debug
			if info, err := de.Info(); err == nil {
				e.Size = info.Size()
			} else {
				log().Debug("取子项大小失败，按 0 记", "repo", repo, "path", rel, "name", de.Name(), "cause", err)
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir // 目录在前
		}
		return entries[i].Name < entries[j].Name
	})
	log().Debug("目录列举完成", "repo", repo, "path", rel, "entries", len(entries))
	return entries, nil
}

// truncatedNotice 生成附在截断内容末尾的醒目提示（含真实文件大小与上限）。
func truncatedNotice(size int64) string {
	return fmt.Sprintf("\n\n===== 内容已截断：文件共 %d 字节，以上仅为开头 %d 字节 =====\n",
		size, maxRunOutput)
}

// rootErrIsEscape 判断 os.Root 返回的路径错误是否为「逃逸出根目录」。
//
// os.Root 对越界解析统一报内部私有错误（文本 "path escapes from parent"，见
// go/src/os/root.go 的 errPathEscapes），stdlib 未导出哨兵，无法 errors.Is，只能
// 按错误文本识别。该文本自 Go 1.24 引入 os.Root 以来未变，且 TestReadFileEscapeRejected
// 与 TestReadFileSymlinkEscape 全量断言「逃逸 → ErrPathEscape」——文本一旦变化，
// 测试立刻失败提示本函数需要同步。
func rootErrIsEscape(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}

// runOutputBuffer 是 RunCmd 的有界输出回收器：最多保留 limit 字节，
// 超出部分继续排空但只计数不存储，保证命令输出再大 agentd 也不驻留内存。
//
// 为什么不能先完整缓存再截断：CombinedOutput 会把全部输出写进内存，
// 高吞吐命令（如 dd 刷 10GB）在 10min 超时前就能撑爆进程内存——
// 截断只保护响应体，不保护进程。本实现把「保留」与「排空」分开，
// 排空必须持续到子进程写完，否则管道写满会反向阻塞子进程。
type runOutputBuffer struct {
	buf    bytes.Buffer // 至多保留 limit 字节
	limit  int
	total  int64 // 已写入的总字节数（含丢弃部分），供截断日志统计
	capped bool  // 是否发生过截断
}

func (b *runOutputBuffer) Write(p []byte) (int, error) {
	b.total += int64(len(p))
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
			b.capped = true
		} else {
			b.buf.Write(p)
		}
	} else {
		b.capped = true
	}
	// 必须返回 len(p)：丢弃的部分也要报告「已消费」，
	// 否则 io.Copy 因 short write 提前结束排空，管道写满后子进程阻塞
	return len(p), nil
}

// RunCmd 在任务仓库内执行一条审阅命令（sh -c），合并 stdout+stderr 有界回收 1MB。
//
// 这是协调者主动发起的只读审阅动作（跑测试/lint），不走审批门——
// 命令语义（跑什么、看什么）由协调者决定，agentd 只负责执行与回收。
//
// 参数：
//   - ctx: 上层上下文（HTTP 请求取消会终止命令）
//   - repo: 命令工作目录（任务仓库）
//   - cmdline: shell 命令原文（sh -c 执行）
//
// 返回：
//   - stdout: 合并后的输出（超过 1MB 截断）
//   - exitCode: 命令退出码；超时被杀时返回 124（与 time 命令的约定一致）
//   - err: 超时返回 context 相关错误；启动失败返回 exec 错误。
//     命令非零退出**不**返回错误——exitCode 已表达结果，路由层据此返回 200
func RunCmd(ctx context.Context, repo, cmdline string) (stdout string, exitCode int, err error) {
	if err := checkProcHeadroom("run"); err != nil {
		return "", -1, err
	}
	ctx, cancel := context.WithTimeout(ctx, RunCmdTimeout)
	defer cancel()
	// why 判在这里而不是在路由层按任务状态反推：目录缺失的原因不止「任务已归档」
	// 一种——人手删、盘掉了、路径被改都会到这里。按状态反推只覆盖归档那一种，
	// 其余场景会退回误导性报错；stat 是对真实原因的直接判据。
	//
	// why 必须排在 runShell() 之前：否则 Windows 上「找不到 sh」的错误会抢在
	// 「工作树没了」前面报出来，又是一次指错方向。
	if _, statErr := os.Stat(repo); statErr != nil {
		log().Warn("run 被拒：工作目录不存在", "repo", repo,
			"cmd", truncateRunes(cmdline, 200), "cause", statErr)
		return "", -1, fmt.Errorf("%w（managed worktree 可能已被 done/stop 回收）: %s",
			ErrWorkdirGone, repo)
	}
	sh, serr := runShell()
	if serr != nil {
		log().Error("run 命令的 shell 解析失败", "repo", repo, "cause", serr)
		return "", -1, serr
	}
	cmd := exec.CommandContext(ctx, sh, "-c", cmdline)
	cmd.Dir = repo
	// 命令设为独立进程组组长：超时/取消时按组回收，孙进程不留孤儿（见 workspace_procgroup_unix.go）
	setProcGroup(cmd)
	// stdout/stderr 指向同一个有界回收器：与 CombinedOutput 相同的合并语义
	// （os/exec 对相同 writer 走单管道），但存储上限 maxRunOutput，超出排空
	out := runOutputBuffer{limit: maxRunOutput}
	cmd.Stdout = &out
	cmd.Stderr = &out
	log().Info("run 命令执行", "repo", repo, "cmd", truncateRunes(cmdline, 200))
	start := time.Now()
	if err := cmd.Start(); err != nil {
		if note := quotaNote(err); note != "" {
			log().Error("run 命令启动失败（进程配额）", "repo", repo,
				"cmd", truncateRunes(cmdline, 200), "note", note, "cause", err)
			return "", -1, fmt.Errorf("%s: %w", note, err)
		}
		log().Error("run 命令启动失败", "repo", repo, "cmd", truncateRunes(cmdline, 200),
			"cause", err)
		return "", -1, err
	}
	// 进程组回收协程：ctx 取消（10min 超时或请求断开）时 kill 整个进程组——
	// CommandContext 只杀 sh 本身，孙进程必须按组回收。
	//
	// 为什么回收后不能再杀（P0-3）：cmdDone 关闭 = cmd.Wait() 已回收进程，
	// 对已回收的 pid 发 SIGKILL 通常得到 ESRCH，但一旦 OS 把该 pid 复用作新
	// 进程组组长，就会误杀 executor 机器上毫不相干的进程组（实测旧实现
	// 300 条成功命令误杀 114 次）。而 Wait 能返回意味着进程组已空——仍持有
	// 输出管道写端的孙进程会阻塞 Wait 的管道 EOF 排空——因此 Wait 返回后
	// 不再需要、也绝不允许再补杀。
	cmdDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-cmdDone:
				// 进程已被 Wait 回收（组内成员同时全部退出），不杀
				return
			default:
				// Wait 尚未返回：组内仍有成员持有输出管道，按组回收
				killProcGroup(cmd.Process.Pid)
			}
		case <-cmdDone:
			// 进程已回收：无论 ctx 是否取消都不再补杀（见上方 why）
			return
		}
	}()
	err = cmd.Wait()
	close(cmdDone)
	elapsed := time.Since(start)

	switch {
	case err != nil && ctx.Err() != nil:
		// 超时：CommandContext 已杀进程，err 是信号类错误；按 time 惯例记 124
		exitCode = 124
		log().Error("run 命令超时被终止", "repo", repo, "cmd", truncateRunes(cmdline, 200),
			"timeout", RunCmdTimeout, "elapsed_ms", elapsed.Milliseconds())
	case err != nil:
		if ee, ok := err.(*exec.ExitError); ok {
			// 命令正常执行完（非零退出）：结果经 exitCode 表达，不返回错误，
			// 路由层据此返回 200 而非 500
			exitCode = ee.ExitCode()
			err = nil
			log().Info("run 命令执行完成", "repo", repo, "cmd", truncateRunes(cmdline, 200),
				"exit_code", exitCode, "elapsed_ms", elapsed.Milliseconds())
		} else {
			exitCode = -1
			log().Error("run 命令运行异常", "repo", repo, "cmd", truncateRunes(cmdline, 200),
				"stderr", truncateRunes(out.buf.String(), 500), "cause", err)
		}
	default:
		log().Info("run 命令执行完成", "repo", repo, "cmd", truncateRunes(cmdline, 200),
			"exit_code", 0, "elapsed_ms", elapsed.Milliseconds())
	}
	if out.capped {
		log().Warn("run 输出超过上限已截断", "repo", repo, "cmd", truncateRunes(cmdline, 200),
			"output_bytes", out.total, "limit", maxRunOutput)
	}
	return out.buf.String(), exitCode, err
}

// firstLine 取多行文本的第一行（日志摘要用）。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// 为什么条目的全部写操作都经 os.OpenRoot(repo) 返回的 *os.Root：ReadFile 的路径
// 逃逸防御（workspace.go:1203-1208）已论证「先校验、后打开」的两步式存在 TOCTOU
// 窗口——恶意 executor 经 run 完全能在校验与打开之间把中间组件换成指向仓库外的
// 链接。这里的三写操作（建/改名/删）比读更危险，复用同一个内核级 jail：每个
// 动作的路径解析都在单次系统调用内由内核完成，无窗口。

// cleanEntryRel 归一化条目操作的目标相对路径并做词汇层逃逸检查。
//
// 与 ReadFile/WriteFile/ListDir 相同的第 1 道红线（Clean 后绝对路径或残留 ..
// 前缀一律拒绝，ErrPathEscape）；"." 归一成 ""（表示工作树根）。
//
// 参数：
//   - repo: 工作树绝对路径（供日志定位）
//   - rel: 待归一化的相对路径
//
// 返回：
//   - 归一化后的相对路径（"." 归为空串）
//   - ErrPathEscape: rel 逃逸出工作树
func cleanEntryRel(repo, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		log().Warn("条目操作路径逃逸被拒绝", "repo", repo, "path", rel)
		return "", fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	if cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}

// validateEntryName 校验新条目名：空、.、..、含 / 或 \、含 NUL 字节一律
// ErrBadEntryName（单层名，本期不做跨目录操作）。
func validateEntryName(repo, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) ||
		strings.ContainsRune(name, 0) {
		log().Warn("条目名字不合法被拒绝", "repo", repo, "name", name)
		return fmt.Errorf("%w: %q", ErrBadEntryName, name)
	}
	return nil
}

// statEntry 从工作树现取一条目的 proto.DirEntry（Name/IsDir/Size 取自磁盘实况）。
//
// 为什么不凭入参拼：name 入参只保证「未逃逸」，磁盘实况才是建/改名后的事实
// 来源（如目录 size 恒 0，见 proto.DirEntry 的文档约定）。
func statEntry(root *os.Root, repo, target string) (proto.DirEntry, error) {
	fi, err := root.Stat(target)
	if err != nil {
		return proto.DirEntry{}, fmt.Errorf("读取条目 %s: %w", filepath.Join(repo, target), err)
	}
	e := proto.DirEntry{Name: fi.Name(), IsDir: fi.IsDir()}
	if !fi.IsDir() {
		e.Size = fi.Size()
	}
	return e, nil
}

// CreateEntry 在工作树内新建一个空文件或空目录（B107 文件树右键菜单）。
//
// 参数：
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - parentRel: 目标所在父目录的相对路径；空串与 "." 都表示工作树根
//   - name: 新条目名（单层名，不得含 / 或 \）
//   - kind: "file" 或 "dir"
//
// 返回：
//   - proto.DirEntry: 新建条目的信息（Name/IsDir/Size 取自磁盘实况，非入参拼装）
//   - ErrBadEntryName: name 非法（空 / . / .. / 含分隔符），或 parentRel 不是目录
//   - ErrPathEscape: parentRel 逃逸出工作树（含符号链接逃逸）
//   - ErrGitDirWrite: 目标落在工作树根的 .git 下
//   - ErrEntryNotFound: parentRel 不存在
//   - ErrEntryExists: 目标已存在
func CreateEntry(repo, parentRel, name, kind string) (proto.DirEntry, error) {
	log().Info("新建工作树条目", "repo", repo, "parent", parentRel, "name", name, "kind", kind)
	root, err := os.OpenRoot(repo)
	if err != nil {
		log().Warn("新建条目打开工作树失败", "repo", repo, "path", parentRel, "cause", err)
		return proto.DirEntry{}, fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()
	parent, err := cleanEntryRel(repo, parentRel)
	if err != nil {
		return proto.DirEntry{}, err
	}
	if err := validateEntryName(repo, name); err != nil {
		return proto.DirEntry{}, err
	}
	if kind != "file" && kind != "dir" {
		log().Warn("新建条目未知类型被拒绝", "repo", repo, "kind", kind)
		return proto.DirEntry{}, fmt.Errorf("%w: 未知的条目类型 %q", ErrBadEntryName, kind)
	}
	target := filepath.Join(parent, name)
	if isGitPath(target) {
		log().Warn("新建条目命中 .git 被拒绝", "repo", repo, "path", target)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrGitDirWrite, target)
	}
	if parent != "" {
		pfi, err := root.Stat(parent)
		if err != nil {
			if rootErrIsEscape(err) {
				log().Warn("新建条目父目录逃逸被拒绝", "repo", repo, "path", parent, "cause", err)
				return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, parent)
			}
			if os.IsNotExist(err) {
				log().Warn("新建条目父目录不存在", "repo", repo, "path", parent, "cause", err)
				return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrEntryNotFound, parent)
			}
			log().Warn("新建条目父目录检查失败", "repo", repo, "path", parent, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("检查父目录 %s: %w", filepath.Join(repo, parent), err)
		}
		if !pfi.IsDir() {
			log().Warn("新建条目父目录不是目录", "repo", repo, "path", parent, "mode", pfi.Mode().String())
			return proto.DirEntry{}, fmt.Errorf("%w: 父目录 %q 不是目录", ErrBadEntryName, parent)
		}
	}
	if _, err := root.Stat(target); err == nil {
		log().Warn("新建条目目标已存在", "repo", repo, "path", target)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrEntryExists, target)
	} else if rootErrIsEscape(err) {
		log().Warn("新建条目路径逃逸被拒绝", "repo", repo, "path", target, "cause", err)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, target)
	}
	if kind == "dir" {
		if err := root.Mkdir(target, 0o755); err != nil {
			if rootErrIsEscape(err) {
				log().Warn("新建条目路径逃逸被拒绝", "repo", repo, "path", target, "cause", err)
				return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, target)
			}
			log().Warn("新建条目建目录失败", "repo", repo, "path", target, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("建目录 %s: %w", filepath.Join(repo, target), err)
		}
	} else {
		f, err := root.Create(target)
		if err != nil {
			if rootErrIsEscape(err) {
				log().Warn("新建条目路径逃逸被拒绝", "repo", repo, "path", target, "cause", err)
				return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, target)
			}
			log().Warn("新建条目建文件失败", "repo", repo, "path", target, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("建文件 %s: %w", filepath.Join(repo, target), err)
		}
		// 建的是空文件，立刻关掉即可
		if err := f.Close(); err != nil {
			log().Warn("新建条目关闭文件失败", "repo", repo, "path", target, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("关闭文件 %s: %w", filepath.Join(repo, target), err)
		}
	}
	e, err := statEntry(root, repo, target)
	if err != nil {
		log().Warn("新建条目读取结果失败", "repo", repo, "path", target, "cause", err)
		return proto.DirEntry{}, err
	}
	log().Info("新建条目完成", "repo", repo, "path", target, "kind", kind)
	return e, nil
}

// RenameEntry 给工作树内一个条目改名（B107 文件树右键菜单）。
//
// 参数：
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - rel: 待改名条目的相对路径
//   - newName: 新名字（单层名，不得含 / 或 \——本期不做跨目录移动）
//
// 返回：
//   - proto.DirEntry: 改名后条目的信息（Name/IsDir/Size 取自磁盘实况）
//   - ErrBadEntryName: newName 非法（空 / . / .. / 含分隔符），或 rel 是工作树根
//   - ErrPathEscape: rel 逃逸出工作树（含符号链接逃逸）
//   - ErrGitDirWrite: 目标是 .git 下的条目
//   - ErrEntryNotFound: rel 不存在
//   - ErrEntryExists: 新名字已存在
func RenameEntry(repo, rel, newName string) (proto.DirEntry, error) {
	log().Info("改名工作树条目", "repo", repo, "path", rel, "new_name", newName)
	root, err := os.OpenRoot(repo)
	if err != nil {
		log().Warn("条目改名打开工作树失败", "repo", repo, "path", rel, "cause", err)
		return proto.DirEntry{}, fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()
	cleaned, err := cleanEntryRel(repo, rel)
	if err != nil {
		return proto.DirEntry{}, err
	}
	if cleaned == "" {
		log().Warn("条目改名目标是工作树根被拒绝", "repo", repo, "path", rel)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrBadEntryName, rel)
	}
	if err := validateEntryName(repo, newName); err != nil {
		return proto.DirEntry{}, err
	}
	if isGitPath(cleaned) {
		log().Warn("条目改名命中 .git 被拒绝", "repo", repo, "path", cleaned)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrGitDirWrite, cleaned)
	}
	target := filepath.Join(filepath.Dir(cleaned), newName)
	if isGitPath(target) {
		log().Warn("条目改名命中 .git 被拒绝", "repo", repo, "target", target)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrGitDirWrite, target)
	}
	if _, err := root.Stat(cleaned); err != nil {
		if rootErrIsEscape(err) {
			log().Warn("条目改名路径逃逸被拒绝", "repo", repo, "path", cleaned, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, cleaned)
		}
		if os.IsNotExist(err) {
			log().Warn("条目改名目标不存在", "repo", repo, "path", cleaned, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrEntryNotFound, cleaned)
		}
		log().Warn("条目改名检查目标失败", "repo", repo, "path", cleaned, "cause", err)
		return proto.DirEntry{}, fmt.Errorf("检查条目 %s: %w", filepath.Join(repo, cleaned), err)
	}
	if _, err := root.Stat(target); err == nil {
		log().Warn("条目改名撞名被拒绝", "repo", repo, "path", cleaned, "target", target)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrEntryExists, target)
	} else if rootErrIsEscape(err) {
		log().Warn("条目改名路径逃逸被拒绝", "repo", repo, "path", target, "cause", err)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, target)
	}
	if err := root.Rename(cleaned, target); err != nil {
		if rootErrIsEscape(err) {
			log().Warn("条目改名路径逃逸被拒绝", "repo", repo, "path", target, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, target)
		}
		log().Warn("条目改名执行失败", "repo", repo, "path", cleaned, "target", target, "cause", err)
		return proto.DirEntry{}, fmt.Errorf("改名 %s → %s: %w", filepath.Join(repo, cleaned), filepath.Join(repo, target), err)
	}
	e, err := statEntry(root, repo, target)
	if err != nil {
		log().Warn("条目改名读取结果失败", "repo", repo, "path", target, "cause", err)
		return proto.DirEntry{}, err
	}
	log().Info("条目改名完成", "repo", repo, "path", cleaned, "new_path", target)
	return e, nil
}

// DeleteEntry 删除工作树内一个条目（B107 文件树右键菜单），目录连同其内容一并删。
//
// 不做回收站：git 能救已跟踪文件（checkout / restore 一条命令），却救不了未跟踪
// 文件——新建即删除的落盘即永久，这正是「误删会弄丢东西」的全部理由，与凭据
// 强弱无关（控制台会话的权限本来就是主令牌等价，见 isGitPath 的 why）；这道闸
// 该挡的是「一次误操作就把数据/仓库弄坏」。
//
// 参数：
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - rel: 待删除条目的相对路径
//
// 返回：
//   - ErrBadEntryName: rel 是工作树根（空串 / "."）
//   - ErrPathEscape: rel 逃逸出工作树（含符号链接逃逸）
//   - ErrGitDirWrite: 目标是 .git 下的条目
//   - ErrEntryNotFound: rel 不存在
func DeleteEntry(repo, rel string) error {
	log().Info("删除工作树条目", "repo", repo, "path", rel)
	root, err := os.OpenRoot(repo)
	if err != nil {
		log().Warn("删除条目打开工作树失败", "repo", repo, "path", rel, "cause", err)
		return fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()
	cleaned, err := cleanEntryRel(repo, rel)
	if err != nil {
		return err
	}
	if cleaned == "" {
		log().Warn("删除工作树根本身被拒绝", "repo", repo, "path", rel)
		return fmt.Errorf("%w: %q", ErrBadEntryName, rel)
	}
	if isGitPath(cleaned) {
		log().Warn("删除条目命中 .git 被拒绝", "repo", repo, "path", cleaned)
		return fmt.Errorf("%w: %q", ErrGitDirWrite, cleaned)
	}
	fi, err := root.Stat(cleaned)
	if err != nil {
		if rootErrIsEscape(err) {
			log().Warn("删除条目路径逃逸被拒绝", "repo", repo, "path", cleaned, "cause", err)
			return fmt.Errorf("%w: %q", ErrPathEscape, cleaned)
		}
		if os.IsNotExist(err) {
			log().Warn("删除条目不存在", "repo", repo, "path", cleaned, "cause", err)
			return fmt.Errorf("%w: %q", ErrEntryNotFound, cleaned)
		}
		log().Warn("删除条目检查失败", "repo", repo, "path", cleaned, "cause", err)
		return fmt.Errorf("检查条目 %s: %w", filepath.Join(repo, cleaned), err)
	}
	kind := "file"
	if fi.IsDir() {
		kind = "dir"
		err = root.RemoveAll(cleaned)
	} else {
		err = root.Remove(cleaned)
	}
	if err != nil {
		if rootErrIsEscape(err) {
			log().Warn("删除条目路径逃逸被拒绝", "repo", repo, "path", cleaned, "cause", err)
			return fmt.Errorf("%w: %q", ErrPathEscape, cleaned)
		}
		log().Warn("删除条目执行失败", "repo", repo, "path", cleaned, "kind", kind, "cause", err)
		return fmt.Errorf("删除条目 %s: %w", filepath.Join(repo, cleaned), err)
	}
	log().Info("删除条目完成", "repo", repo, "path", cleaned, "kind", kind)
	return nil
}

// copyEntryName 为源条目挑一个未被占用的副本名（B107 文件树右键菜单）。
//
// 命名规则（spec §3.4）：文件用 filepath.Ext 把 base 与扩展名拆开，候选依次是
// `base copy<ext>`、`base copy 2<ext>` …… 到 `base copy 99<ext>`；目录不拆扩展名，
// 整体当 base（如 `a.b` 目录 → `a.b copy`，与 Mac Finder 同款，拆了会得到
// `a copy.b` 这种错误形态）。isDir 决定是否拆。
//
// 为什么封顶 99：候选名是给人读的（Mac 文件系统的复制命名同款），无限试探只会
// 在「全被占用」的场景白白做几十上百次 Stat，且名字本身会越来越难读。封顶后
// 全部占用按 ErrEntryExists 拒掉，明确告诉用户手动清一清。
//
// 返回：目标名（与 source 同目录）与试过的候选数；root.Stat 出现逃逸以外的
// 意外错误时直接透传。
func copyEntryName(root *os.Root, repo, source string, isDir bool) (string, int, error) {
	base := filepath.Base(source)
	nameBase, ext := base, ""
	if !isDir {
		ext = filepath.Ext(base)
		if len(base) == len(ext) { // 隐藏文件等"整个名字都是扩展名"的形态不拆
			ext = ""
		}
		if ext != "" {
			nameBase = base[:len(base)-len(ext)]
		}
	}
	for i := 1; i <= 99; i++ {
		var name string
		if i == 1 {
			name = nameBase + " copy" + ext
		} else {
			name = nameBase + fmt.Sprintf(" copy %d", i) + ext
		}
		target := filepath.Join(filepath.Dir(source), name)
		if _, err := root.Stat(target); err != nil {
			if rootErrIsEscape(err) {
				log().Warn("条目复制命名探测逃逸被拒绝", "repo", repo, "path", source, "candidate", target, "cause", err)
				return "", i, fmt.Errorf("%w: %q", ErrPathEscape, target)
			}
			if os.IsNotExist(err) {
				return target, i, nil
			}
			log().Warn("条目复制命名探测失败", "repo", repo, "path", source, "candidate", target, "cause", err)
			return "", i, fmt.Errorf("探测副本名 %s: %w", filepath.Join(repo, target), err)
		}
	}
	log().Warn("条目复制候选名全部被占用", "repo", repo, "path", source)
	return "", 99, fmt.Errorf("%w: %q 的副本名到 copy 99 已全被占用", ErrEntryExists, source)
}

// copyEntryFile 把 root 内的一个普通文件复制到目标路径（同为 root 内相对路径）。
//
// 权限用源文件的 Stat().Mode() 经 root.Chmod 补上——root.Create 只带 0o666 &
// umask，源文件的可执行位等权限位会丢，丢了是静默故障（与 atomicReplace 的
// perm 语义同款）。
func copyEntryFile(root *os.Root, repo, src, dst string) (bytes int64, err error) {
	sfi, err := root.Stat(src)
	if err != nil {
		if rootErrIsEscape(err) {
			return 0, fmt.Errorf("%w: %q", ErrPathEscape, src)
		}
		return 0, fmt.Errorf("读取源文件 %s: %w", filepath.Join(repo, src), err)
	}
	srcF, err := root.Open(src)
	if err != nil {
		if rootErrIsEscape(err) {
			return 0, fmt.Errorf("%w: %q", ErrPathEscape, src)
		}
		return 0, fmt.Errorf("打开源文件 %s: %w", filepath.Join(repo, src), err)
	}
	defer srcF.Close()
	dstF, err := root.Create(dst)
	if err != nil {
		if rootErrIsEscape(err) {
			return 0, fmt.Errorf("%w: %q", ErrPathEscape, dst)
		}
		return 0, fmt.Errorf("创建副本 %s: %w", filepath.Join(repo, dst), err)
	}
	defer dstF.Close()
	n, err := io.Copy(dstF, srcF)
	if err != nil {
		return 0, fmt.Errorf("复制 %s → %s: %w", filepath.Join(repo, src), filepath.Join(repo, dst), err)
	}
	if err := root.Chmod(dst, sfi.Mode().Perm()); err != nil {
		return 0, fmt.Errorf("补副本权限 %s: %w", filepath.Join(repo, dst), err)
	}
	return n, nil
}

// copyEntryDir 把 root 内的一个目录连同全部内容递归复制到目标路径。
//
// 为什么整段都在 root.FS() 上走：os.Root 的 FS 是相对根目录的文件系统视图，
// WalkDir 产出的每条路径（含根目录本身）都以 "/" 为分隔符、可以原样喂回 root
// 的 Open/Mkdir/Stat——从 src 到 dst 的目标相对路径也只在相对路径之间拼（Rel +
// Join），中途不得落回绝对路径，一旦把哪一段换成 filepath.Join(rootPath, rel)
// 就绕过了内核级 jail，这条红线的理由见本文件 1543 行注释。
func copyEntryDir(root *os.Root, repo, src, dst string) (entries, bytes int64, err error) {
	if err := root.Mkdir(dst, 0o755); err != nil {
		if rootErrIsEscape(err) {
			return 0, 0, fmt.Errorf("%w: %q", ErrPathEscape, dst)
		}
		return 0, 0, fmt.Errorf("建副本目录 %s: %w", filepath.Join(repo, dst), err)
	}
	entries = 1
	counted := 0
	werr := fs.WalkDir(root.FS(), src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		counted++
		if counted%200 == 0 {
			log().Debug("目录复制进度", "repo", repo, "path", src, "visited", counted)
		}
		if p == src {
			return nil // 根目录已在上面 Mkdir
		}
		relPart, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relPart)
		if d.IsDir() {
			if err := root.Mkdir(target, 0o755); err != nil {
				if rootErrIsEscape(err) {
					return fmt.Errorf("%w: %q", ErrPathEscape, target)
				}
				return fmt.Errorf("建副本目录 %s: %w", filepath.Join(repo, target), err)
			}
			entries++
			return nil
		}
		if !d.Type().IsRegular() {
			// 符号链接（WalkDir 不跟随）与其他特殊文件：不复制链接、也不让整次
			// 复制失败，跳过并留痕
			log().Warn("目录复制跳过非普通文件", "repo", repo, "path", p, "mode", d.Type().String())
			return nil
		}
		n, err := copyEntryFile(root, repo, p, target)
		if err != nil {
			return err
		}
		entries++
		bytes += n
		return nil
	})
	if werr != nil {
		return 0, 0, fmt.Errorf("递归复制 %s: %w", filepath.Join(repo, src), werr)
	}
	return entries, bytes, nil
}

// CopyEntry 复制工作树内一个条目（B107 文件树右键菜单），目录连同其内容一并
// 递归复制。副本名按「foo copy.go、foo copy 2.go …… 到 foo copy 99.go」的规则
// 取第一个未被占用的；目录整体当 base 不拆扩展名（`a.b` → `a.b copy`）。
// 命名细节与 99 上限的理由见 copyEntryName doc。
//
// 路径遏制与 CreateEntry/RenameEntry/DeleteEntry 同一红线：全部写操作经
// os.OpenRoot 的内核级 jail（理由见 1543 行注释），符号链接等特殊文件不复制、
// 只跳过并 Warn，绝不在递归途中落回绝对路径。
//
// 参数：
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - rel: 待复制条目的相对路径（空串与 "." 都表示工作树根，按 ErrBadEntryName
//     拒绝——复制整棵工作树没有意义，落点必然撞已存在的副本名）
//
// 返回：
//   - proto.DirEntry: 复制后条目的信息（Name/IsDir/Size 取自磁盘实况）
//   - ErrBadEntryName: rel 是工作树根（空串 / "."）
//   - ErrPathEscape: rel 逃逸出工作树（含符号链接逃逸）
//   - ErrGitDirWrite: 目标是 .git 下的条目
//   - ErrEntryNotFound: rel 不存在
//   - ErrEntryExists: 副本名到 copy 99 已全部被占用
func CopyEntry(repo, rel string) (proto.DirEntry, error) {
	log().Info("复制工作树条目", "repo", repo, "path", rel)
	root, err := os.OpenRoot(repo)
	if err != nil {
		log().Warn("条目复制打开工作树失败", "repo", repo, "path", rel, "cause", err)
		return proto.DirEntry{}, fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()
	cleaned, err := cleanEntryRel(repo, rel)
	if err != nil {
		return proto.DirEntry{}, err
	}
	if cleaned == "" {
		log().Warn("条目复制目标是工作树根被拒绝", "repo", repo, "path", rel)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrBadEntryName, rel)
	}
	if isGitPath(cleaned) {
		log().Warn("条目复制命中 .git 被拒绝", "repo", repo, "path", cleaned)
		return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrGitDirWrite, cleaned)
	}
	fi, err := root.Stat(cleaned)
	if err != nil {
		if rootErrIsEscape(err) {
			log().Warn("条目复制路径逃逸被拒绝", "repo", repo, "path", cleaned, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, cleaned)
		}
		if os.IsNotExist(err) {
			log().Warn("条目复制目标不存在", "repo", repo, "path", cleaned, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrEntryNotFound, cleaned)
		}
		log().Warn("条目复制检查目标失败", "repo", repo, "path", cleaned, "cause", err)
		return proto.DirEntry{}, fmt.Errorf("检查条目 %s: %w", filepath.Join(repo, cleaned), err)
	}
	target, tried, err := copyEntryName(root, repo, cleaned, fi.IsDir())
	if err != nil {
		return proto.DirEntry{}, err
	}
	log().Info("条目复制选定副本名", "repo", repo, "path", cleaned, "target", target, "tried", tried)
	var entries, copied int64
	if fi.IsDir() {
		entries, copied, err = copyEntryDir(root, repo, cleaned, target)
	} else {
		entries = 1
		copied, err = copyEntryFile(root, repo, cleaned, target)
	}
	if err != nil {
		if rootErrIsEscape(err) {
			log().Warn("条目复制路径逃逸被拒绝", "repo", repo, "path", cleaned, "cause", err)
			return proto.DirEntry{}, fmt.Errorf("%w: %q", ErrPathEscape, cleaned)
		}
		log().Warn("条目复制执行失败", "repo", repo, "path", cleaned, "target", target, "cause", err)
		return proto.DirEntry{}, err
	}
	e, err := statEntry(root, repo, target)
	if err != nil {
		log().Warn("条目复制读取结果失败", "repo", repo, "path", target, "cause", err)
		return proto.DirEntry{}, err
	}
	log().Info("条目复制完成", "repo", repo, "path", cleaned, "target", target,
		"entries", entries, "bytes", copied)
	return e, nil
}

// 搜索护栏（B107 文件树右键菜单「在文件夹内查找」）。
//
// 为什么需要条数护栏：结果面板是给人扫的，几千条命中没人会读，只会把响应体
// 与渲染撑爆；撞顶必须标 Truncated，否则「返回了 200 条」会被读成「全仓只有
// 200 处」，用户以为已经看完了全部命中。
// 为什么需要超时护栏：全仓遍历要扫几十万个文件，一次搜索不该无限占用 HTTP
// handler；到点带着已有结果返回，不把超时当错误。
const (
	searchDefaultLimit = 200
	searchMaxLimit     = 1000
	searchTimeout      = 10 * time.Second
)

// searchSkipDirs 是不进入的目录名（任意层级命中即跳过整棵子树）。
//
// 为什么跳过这些目录：.git 是版本元数据，node_modules/vendor/dist/target 是
// 依赖与构建产物——它们不承载「人想找的代码」，遍历只会拖慢每次搜索。
var searchSkipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "target": true}

// searchHitTextMax 是命中行文本的截断长度。
// 为什么截断：结果面板一行放不下整行，且超长行多半是压缩/生成产物。
const searchHitTextMax = 300

// searchMaxLine 是逐行扫描的单行上限（1 MiB）。
// 为什么设上限：bufio.Scanner 默认 64 KiB 就会报 ErrTooLong，1 MiB 放宽到
// 「绝大多数源码行都够用」，同时防止把一条压缩巨行整个读进内存。
const searchMaxLine = 1 << 20

// searchBinaryProbe 是二进制判定的探测长度。
// 为什么只探 512 字节：搜索是只读扫描，判「是不是文本」不需要像在线编辑那样
// 读 8 KiB；真正的文本文件不会在头部塞 NUL，512 对源码/配置零误判。
const searchBinaryProbe = 512

// errSearchStop 是 fs.WalkDir 提前结束的信号：命中数达上限或超时到点。
// 调用方据此返回已有结果并标 Truncated，不把提前结束当成错误透传。
var errSearchStop = errors.New("搜索提前结束")

// SearchInDir 在工作树某个目录内全文搜索关键词，返回命中的行
// （B107 文件树右键菜单「在文件夹内查找」）。
//
// 参数：
//   - ctx: 控制搜索生命周期；内部叠加 searchTimeout 作为兜底上限
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - rel: 搜索范围（相对工作树根的目录）；"" 与 "." 都表示整棵工作树
//   - query: 关键词，空串直接报错
//   - limit: 命中数上限；<=0 取 searchDefaultLimit，>searchMaxLimit 收敛到它
//
// 返回：
//   - proto.SearchResult：Hits 的 Rel 含 scope 前缀、Line 从 1 起、Text 是匹配
//     行原文（超过 searchHitTextMax 截断）；Truncated=true 表示撞到命中数上限
//     或超时，此时 Hits 是已扫到的部分结论——必须把「只看到这些」与「总共就
//     这些」分开，用户才知道要收窄范围
//   - ErrPathEscape: rel 逃逸出工作树（含符号链接逃逸）
//
// 路径遏制与 ReadFile/ListDir 同一红线：遍历经 os.OpenRoot 的内核级 jail
// （root.FS() 做 fs.WalkDir），不落回 filepath.Join(repo, rel) 后的包级 os
// 调用（理由见本文件 1543 行注释）。
//
// 三条护栏（每条的为什么见对应常量/变量注释）：
//   - 命中数：达 limit 立即停止并标 Truncated
//   - 超时：searchTimeout 到点带已有结果返回，不把超时当错误
//   - 跳过生成物：searchSkipDirs 命中的目录任意层级即跳整棵子树
func SearchInDir(ctx context.Context, repo, rel, query string, limit int) (proto.SearchResult, error) {
	scope, err := cleanEntryRel(repo, rel)
	if err != nil {
		return proto.SearchResult{}, err
	}
	if query == "" {
		log().Warn("搜索空关键词被拒绝", "repo", repo, "scope", scope)
		return proto.SearchResult{}, fmt.Errorf("空关键词")
	}
	if limit <= 0 {
		limit = searchDefaultLimit
	} else if limit > searchMaxLimit {
		limit = searchMaxLimit
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return proto.SearchResult{}, fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()

	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	walkRoot := scope
	if walkRoot == "" {
		walkRoot = "."
	}
	log().Info("文件夹内搜索开始", "repo", repo, "scope", scope, "query", query,
		"limit", limit, "timeout", searchTimeout)

	res := proto.SearchResult{Hits: []proto.SearchHit{}}
	start := time.Now()
	scanned := 0
	werr := fs.WalkDir(root.FS(), walkRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if searchSkipDirs[d.Name()] {
				log().Debug("搜索跳过目录", "repo", repo, "dir", p)
				return fs.SkipDir
			}
			return nil
		}
		scanned++
		if scanned%100 == 0 && ctx.Err() != nil {
			return errSearchStop
		}
		if !d.Type().IsRegular() {
			return nil // 符号链接与其他特殊文件不搜索
		}
		f, err := root.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		head := make([]byte, searchBinaryProbe)
		n, _ := io.ReadFull(f, head)
		if bytes.IndexByte(head[:n], 0) >= 0 {
			return nil // 二进制文件不搜索
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), searchMaxLine)
		line := 0
		for sc.Scan() {
			line++
			text := sc.Text()
			if !strings.Contains(text, query) {
				continue
			}
			res.Hits = append(res.Hits, proto.SearchHit{
				Rel:  filepath.ToSlash(p),
				Line: line,
				Text: truncateRunes(text, searchHitTextMax),
			})
			if len(res.Hits) >= limit {
				return errSearchStop
			}
		}
		if err := sc.Err(); err != nil {
			// 行长超过 searchMaxLine（压缩产物常见）：本文件扫不完，跳过不打断整次搜索
			log().Debug("搜索跳过超长行文件", "repo", repo, "path", p, "cause", err)
		}
		return nil
	})
	if werr != nil && !errors.Is(werr, errSearchStop) {
		log().Error("搜索遍历失败", "repo", repo, "scope", scope, "cause", werr)
		return proto.SearchResult{}, fmt.Errorf("搜索遍历 %s: %w", filepath.Join(repo, scope), werr)
	}
	if errors.Is(werr, errSearchStop) {
		res.Truncated = true
		if ctx.Err() != nil {
			log().Warn("搜索超时，返回已扫到的部分结果", "repo", repo, "scope", scope,
				"query", query, "hits", len(res.Hits), "scanned", scanned, "timeout", searchTimeout)
		} else {
			log().Info("搜索命中数达上限，返回部分结果", "repo", repo, "scope", scope,
				"query", query, "hits", len(res.Hits), "scanned", scanned, "limit", limit)
		}
	}
	log().Info("搜索完成", "repo", repo, "scope", scope, "query", query,
		"hits", len(res.Hits), "scanned", scanned, "elapsed_ms", time.Since(start).Milliseconds())
	return res, nil
}
