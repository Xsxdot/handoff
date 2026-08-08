// 本文件是 agentd 侧 git 工作区操作与文件/命令读取的唯一出口。
//
// 职责：
//   - 派发前的工作区准备：PrepareWorkspace 按分支×worktree 两个正交维度准备任务
//     工作区（脏工作区一律拒绝；new-worktree 免脏检查）——PrepareBranch 是其
//     原地+自动分支的过渡薄包装
//   - 审核者审阅素材：Diff（基准分支到 HEAD 的差异 + 提交列表）、
//     ReadFile（读仓库内文件）、RunCmd（远程跑测试/lint 等审阅命令）
//
// 边界：
//   - 全部操作是「分支准备 + 只读审阅」：绝不代 executor 写代码/提交，
//     executor 的改动必须经它自己的 commit 落进任务分支
//   - 不解析审阅命令的语义：run 跑什么、diff 怎么审由审核者决定
//   - git 全部经 exec.Command("git","-C",repo,...) 执行，不拼接 shell
//   - 每条命令都有超时/输出护栏（run 10min / 输出有界回收：最多保留 1MB，
//     超出部分只排空不驻留内存），防挂死与内存失控
package agentd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 错误定义：
//   - ErrDirtyWorktree：工作区有未提交/未跟踪的改动，拒绝派发
//   - ErrPathEscape：请求的文件路径逃逸出任务仓库（含符号链接逃逸）
//   - ErrPathIsDir：请求的文件路径指向目录（fetch 只服务普通文件）
//   - ErrNotRegularFile：请求的文件路径指向管道/设备等特殊文件（不可读）
//   - ErrRepoUnusable：git 探活本身失败（仓库路径不存在/不是 git 仓库/权限等），
//     与 ErrDirtyWorktree 的「仓库可用但状态不干净」区分——前者需要审核者先解决
//     仓库本身的问题，后者一条 git 命令即可清理（server 层映射见 writeDispatchError）
//   - ErrBadBaseBranch：diff 的基准分支参数非法（以 "-" 开头，会被 git 解释为
//     选项而非 rev——git 参数注入面）
//   - ErrBadWorkspaceReq：PrepareWorkspace 的参数非法（互斥冲突/分支不存在/
//     路径不是本仓库 worktree/rev 以 "-" 开头等），dispatch 期拒发
var (
	ErrDirtyWorktree   = errors.New("工作区不干净（有未提交改动），拒绝派发")
	ErrPathEscape      = errors.New("路径逃逸被拒绝")
	ErrPathIsDir       = errors.New("路径是目录，不是文件")
	ErrNotRegularFile  = errors.New("路径不是普通文件（管道/设备等特殊文件不可读）")
	ErrRepoUnusable    = errors.New("任务仓库不可用（路径不存在或不是 git 仓库）")
	ErrBadBaseBranch   = errors.New("非法的基准分支：不允许以 - 开头")
	ErrBadWorkspaceReq = errors.New("工作区参数非法")
)

// 执行护栏：
//   - RunCmdTimeout：单条审阅命令的执行上限。包级 var 而非 const，便于测试注入更短值。
//     导出供 cmd 包派生 agentd HTTP WriteTimeout（见 cmd/agentd.go）：响应写超时必须
//     ≥ 该上限，否则长审阅命令会在 handler 执行途中被掐断连接、RunCmd 被提前取消
//   - maxRunOutput：合并输出的截断上限，防止失控命令刷爆内存与响应体
var (
	RunCmdTimeout = 10 * time.Minute
	maxRunOutput  = 1 << 20 // 1 MiB
)

// log 返回 slog.Default()（与 store 同款约定：bootstrap 后统一 logger）。
func log() *slog.Logger { return slog.Default() }

// gitRun 执行 git -C repo <args...>，返回 stdout 与 stderr。
//
// 日志：调用前 Info（repo、args）、调用后 Info（耗时）；失败 Error 带 stderr
// 原文——git 报错原文是排障必需品，不能只留包装后的 error 文本。
func gitRun(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error) {
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
		log().Error("git 调用失败", "repo", repo, "args", args,
			"stderr", truncateRunes(errBuf.String(), 500), "cause", err)
	}
	return outBuf.String(), errBuf.String(), err
}

// PrepareBranch 是 PrepareWorkspace 的过渡薄包装：保持一期「原地 + 自动分支」语义
// 与全部错误哨兵（ErrDirtyWorktree/ErrRepoUnusable），Dispatch 改走 PrepareWorkspace
// 后本函数仅剩测试与本包内部引用（Task 7 会清理调用点）。
func PrepareBranch(repo, taskID string) (branch string, err error) {
	ws, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: taskID})
	if err != nil {
		return "", err
	}
	return ws.Branch, nil
}

// WorkspaceReq 描述 dispatch 的工作区诉求（分支 × worktree 两个正交维度）。
//
// 分支维度三态（互斥）：Branch=已存在分支 / NewBranch=新建分支 / 都空=自动
// handoff/<id8>；Base 仅与 NewBranch 连用（空=HEAD）——自动分支不带 Base
// （校验见 PrepareWorkspace 第 1 层，spec §5 与校验一致）。
// worktree 维度三态（互斥）：Worktree=用户自带 worktree 路径 / NewWorktree=由
// agentd 在 WorktreesDir 下新建 managed worktree / 都空=原地（主仓库）。
type WorkspaceReq struct {
	Repo         string // 主仓库路径
	TaskID       string
	Branch       string // 已存在分支（与 NewBranch 互斥）
	NewBranch    string // 新建分支名（空且 Branch 空 = 自动 handoff/<id8>）
	Base         string // 新分支起点，仅与 NewBranch 连用（空=HEAD；自动分支不带 Base）
	Worktree     string // 已存在 worktree 路径（与 NewWorktree 互斥）
	NewWorktree  bool
	WorktreesDir string // agentd 管理的 worktree 根目录（DataDir/worktrees）
}

// Workspace 是准备完成的工作区结果。
type Workspace struct {
	Branch  string
	WorkDir string // executor cwd 与审阅命令目录；原地模式 = Repo
	Managed bool   // WorkDir 是 agentd 创建的 worktree（done 时代删）
}

// PrepareWorkspace 按 WorkspaceReq 准备任务工作区，返回结果。
//
// 3 分支模式 × 3 工作树模式的 9 种组合行为表（分支 B/新分支 N/自动 A × 新树 N/用户树 U/原地 I）：
//
//	      新树(NewWorktree)        用户树(Worktree)        原地(默认)
//	B   worktree add <p> <b>     校验归属+脏，checkout b   脏检查，checkout b
//	N   worktree add -b b <p> t  校验归属+脏，checkout -b  脏检查，checkout -b b [t]
//	A   worktree add -b h <p>    校验归属+脏，checkout -b  脏检查，checkout -b h
//
// 其中 b=指定分支、h=handoff/<id8>、t=Base（仅 N 行有效，空=HEAD；自动分支 A
// 不允许带 Base，见第 1 层校验）、p=WorktreesDir/<id8> 或用户路径。
// 校验规则：Branch 模式分支必须已存在；用户树模式必须归属本仓库（git-common-dir 比对）；
// 所有以 "-" 开头的分支名/路径一律拒绝（git 参数注入面）。
//
// 为什么 NewWorktree 免脏检查：新 worktree 是从仓库新建的独立工作树，天然干净，
// 与主仓库的脏状态无关——这是 worktree 并行派发的价值：主仓有人手动改动也不阻塞
// 新任务开跑；只有原地/用户树模式（复用既有工作树）才需要脏检查防污染。
//
// 为什么用户树归属校验用 git-common-dir 比对：普通目录与 worktree 的差别就在
// 「共享同一仓库 git 目录」；git-common-dir 对 main 仓库返回其 .git、对 worktree
// 返回同一值，路径经 EvalSymlinks 归一后相等即归属成立——比「读 .git 文件内容」
// 更稳（.git 可能缺失、可能被链到别处）。校验失败按 ErrBadWorkspaceReq 拒发。
func PrepareWorkspace(req WorkspaceReq) (Workspace, error) {
	log().Info("工作区准备进入", "repo", req.Repo, "task", req.TaskID, "branch", req.Branch,
		"new_branch", req.NewBranch, "base", req.Base, "worktree", req.Worktree,
		"new_worktree", req.NewWorktree, "worktrees_dir", req.WorktreesDir)
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
	if req.Base != "" && req.NewBranch == "" {
		return Workspace{}, rejectWorkspace("base 仅允许与 new-branch 连用", req)
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
		if out, _, err := gitRun(context.Background(), req.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+req.Branch); err != nil || strings.TrimSpace(out) == "" {
			return Workspace{}, rejectWorkspace("分支 "+req.Branch+" 不存在", req)
		}
	}

	var ws Workspace
	// 第 3 层：按 worktree 维度分派
	switch {
	case req.NewWorktree:
		// 新树：WorktreesDir/<id8>，MkdirAll 后 git worktree add（已存在分支不带 -b）
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
		if _, stderr, err := gitRun(context.Background(), req.Repo, args...); err != nil {
			return Workspace{}, fmt.Errorf("git %v: %s: %w", args, strings.TrimSpace(stderr), err)
		}
		ws = Workspace{Branch: branch, WorkDir: workDir, Managed: true}
	case req.Worktree != "":
		// 用户树：归属校验 → 脏检查 → 在其中 checkout
		if !worktreeBelongsToRepo(req.Repo, req.Worktree) {
			return Workspace{}, rejectWorkspace("路径 "+req.Worktree+" 不是本仓库的 worktree", req)
		}
		if err := ensureCleanWorktree(req.Worktree); err != nil {
			return Workspace{}, err
		}
		if err := checkoutInWorktree(req.Worktree, branch, req.Base, isExisting); err != nil {
			return Workspace{}, err
		}
		ws = Workspace{Branch: branch, WorkDir: req.Worktree, Managed: false}
	default:
		// 原地：脏检查主仓 → checkout / checkout -b
		if err := ensureCleanWorktree(req.Repo); err != nil {
			return Workspace{}, err
		}
		var args []string
		if isExisting {
			args = []string{"checkout", branch}
		} else {
			args = []string{"checkout", "-b", branch}
			if req.Base != "" {
				args = append(args, req.Base)
			}
		}
		if _, stderr, err := gitRun(context.Background(), req.Repo, args...); err != nil {
			return Workspace{}, fmt.Errorf("git %v: %s: %w", args, strings.TrimSpace(stderr), err)
		}
		ws = Workspace{Branch: branch, WorkDir: req.Repo, Managed: false}
	}
	log().Info("工作区准备完成", "task", req.TaskID, "branch", ws.Branch, "workdir", ws.WorkDir, "managed", ws.Managed)
	return ws, nil
}

// rejectWorkspace 打参数拒绝日志并包装 ErrBadWorkspaceReq（哪个规则、哪个值）。
func rejectWorkspace(rule string, req WorkspaceReq) error {
	log().Warn("工作区参数非法，拒绝准备", "task", req.TaskID, "rule", rule, "req", req)
	return fmt.Errorf("%w: %s", ErrBadWorkspaceReq, rule)
}

// checkoutInWorktree 在用户自带 worktree 内切分支（已存在 → checkout；新建/自动
// → checkout -b [base]），供 Worktree 模式复用。
func checkoutInWorktree(workDir, branch, base string, isExisting bool) error {
	var args []string
	if isExisting {
		args = []string{"checkout", branch}
	} else {
		args = []string{"checkout", "-b", branch}
		if base != "" {
			args = append(args, base)
		}
	}
	if _, stderr, err := gitRun(context.Background(), workDir, args...); err != nil {
		return fmt.Errorf("git -C %s %v: %s: %w", workDir, args, strings.TrimSpace(stderr), err)
	}
	return nil
}

// ensureCleanWorktree 校验工作区干净（status --porcelain 无任何输出）。
// 脏检测含未跟踪文件：未跟踪文件同样可能被执行器误 add 进任务提交，保守拒绝。
// git status 失败（仓库不存在/不是 git 仓库）与「脏」是两种可修复场景，
// 用 ErrRepoUnusable 区分（server 层据此给调用方可读的 400 而非扁平 500）。
func ensureCleanWorktree(dir string) error {
	status, _, err := gitRun(context.Background(), dir, "status", "--porcelain")
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

// worktreeBelongsToRepo 校验 worktree 路径是否属于主仓库 repo。
//
// 原理见 PrepareWorkspace doc 的 why：git-common-dir 对 main 仓库与它的任一
// worktree 返回同一 git 目录，路径经 EvalSymlinks 归一后相等即归属成立。
// 任一步失败（路径不是 git 仓库/不是本仓 worktree）都返回 false（fail-closed）。
func worktreeBelongsToRepo(repo, worktree string) bool {
	repoDir, _, err := gitRun(context.Background(), repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false
	}
	wtDir, _, err := gitRun(context.Background(), worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false
	}
	rp, err1 := filepath.EvalSymlinks(strings.TrimSpace(repoDir))
	wp, err2 := filepath.EvalSymlinks(strings.TrimSpace(wtDir))
	if err1 != nil || err2 != nil {
		return false
	}
	return rp == wp
}

// RemoveManagedWorktree 删除 agentd 管理的 worktree（git -C repo worktree remove workdir）。
//
// 参数：
//   - repo: 主仓库路径
//   - workdir: 待删除的 worktree 路径（必须为 Managed=true 的工作区）
//
// 注意：
//   - 只删工作树不删分支（spec：任务分支保留供审阅/回滚）
//   - workdir 带未提交改动时 git 拒绝删除（错误带 stderr 原文返回）；是否降级
//     由调用方（Done 归档）决定——本函数不做清理性降级
func RemoveManagedWorktree(repo, workdir string) error {
	log().Info("删除 managed worktree", "repo", repo, "workdir", workdir)
	if _, stderr, err := gitRun(context.Background(), repo, "worktree", "remove", workdir); err != nil {
		log().Error("删除 managed worktree 失败", "repo", repo, "workdir", workdir,
			"stderr", truncateRunes(stderr, 300), "cause", err)
		return fmt.Errorf("git worktree remove %s: %s: %w", workdir, strings.TrimSpace(stderr), err)
	}
	log().Info("managed worktree 已删除", "repo", repo, "workdir", workdir)
	return nil
}

// id8 截取任务 ID 前 8 字节，用于分支名与 worktree 目录名的稳定短标识。
// 与 opencode adapter 的 tmux 会话命名（handoff-<id8>）共用同一截断规则，
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

// resolveBaseBranch 确定 diff 的默认基准分支，优先级：
//  1. 仓库远端默认分支（refs/remotes/origin/HEAD 符号引用，如 origin/main）
//  2. 本地 main
//  3. 本地 master
//  4. 都没有 → 空串（由路由层报错，提示审核者显式 --base）
//
// 为什么需要这个兜底链：任务仓库的分支名不可预知（main/master/dev 皆可能），
// 派发时并未记录基准分支名，diff 必须从仓库自身推导出合理默认。
func resolveBaseBranch(repo string) string {
	if out, _, err := gitRun(context.Background(), repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	for _, cand := range []string{"main", "master"} {
		if _, _, err := gitRun(context.Background(), repo, "rev-parse", "--verify", "--quiet", cand); err == nil {
			return cand
		}
	}
	log().Warn("无法确定基准分支，diff 需要显式 base", "repo", repo)
	return ""
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
//
// 非普通文件（目录/管道/设备等）一律拒绝：目录给 ErrPathIsDir（400 语义），
// 其余特殊文件 read 语义不可控（可能无限输出或永久阻塞），给 ErrNotRegularFile。
//
// 参数：
//   - repo: 任务仓库路径
//   - rel: 相对仓库根的路径（如 cmd/foo.go）
//
// 返回：
//   - 文件内容（超过 1MiB 时截断为开头 1MiB）
//   - err: 路径逃逸（含符号链接逃逸）返回 ErrPathEscape；目录返回 ErrPathIsDir；
//     其他特殊文件返回 ErrNotRegularFile；文件不存在返回 *fs.PathError（含 %w 链）
func ReadFile(repo, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if rel == "" || cleaned == "." || filepath.IsAbs(cleaned) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		log().Warn("文件读取路径逃逸被拒绝", "repo", repo, "path", rel)
		return "", fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return "", fmt.Errorf("打开任务仓库 %s: %w", repo, err)
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
			return "", fmt.Errorf("%w: %q", ErrPathEscape, rel)
		}
		return "", fmt.Errorf("读取文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("读取文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	if !fi.Mode().IsRegular() {
		log().Warn("文件读取目标不是普通文件", "repo", repo, "path", rel, "mode", fi.Mode().String())
		if fi.IsDir() {
			return "", fmt.Errorf("%w: %q", ErrPathIsDir, rel)
		}
		return "", fmt.Errorf("%w: %q", ErrNotRegularFile, rel)
	}
	// 只读 maxRunOutput+1 字节：多出的 1 字节用于判定「是否超限」，
	// 不额外多一次 Stat 也能得到截断结论（真实大小取已打开的 f.Stat）
	b, err := io.ReadAll(io.LimitReader(f, int64(maxRunOutput)+1))
	if err != nil {
		return "", fmt.Errorf("读取文件 %s: %w", filepath.Join(repo, cleaned), err)
	}
	if len(b) > maxRunOutput {
		log().Warn("文件超过读取上限，内容已截断", "repo", repo, "path", rel,
			"size", fi.Size(), "limit", maxRunOutput)
		// 截断必须带可见标记：无标记时审核者会把第 1MiB 处当成文件末尾去推理，
		// 而那既不是末行、也没有任何提示说明后面还有内容
		return string(b[:maxRunOutput]) + truncatedNotice(fi.Size()), nil
	}
	return string(b), nil
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
// 这是审核者主动发起的只读审阅动作（跑测试/lint），不走审批门——
// 命令语义（跑什么、看什么）由审核者决定，agentd 只负责执行与回收。
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
	ctx, cancel := context.WithTimeout(ctx, RunCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
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
