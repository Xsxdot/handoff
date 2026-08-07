// 本文件是 agentd 侧 git 工作区操作与文件/命令读取的唯一出口。
//
// 职责：
//   - 派发前的分支准备：PrepareBranch 在任务仓库里开 handoff/<id8> 分支，
//     保证执行器的工作与审核者的 diff 有确定的分界（脏工作区一律拒绝）
//   - 审核者审阅素材：Diff（基准分支到 HEAD 的差异 + 提交列表）、
//     ReadFile（读仓库内文件）、RunCmd（远程跑测试/lint 等审阅命令）
//
// 边界：
//   - 全部操作是「分支准备 + 只读审阅」：绝不代 executor 写代码/提交，
//     executor 的改动必须经它自己的 commit 落进任务分支
//   - 不解析审阅命令的语义：run 跑什么、diff 怎么审由审核者决定
//   - git 全部经 exec.Command("git","-C",repo,...) 执行，不拼接 shell
//   - 每条命令都有超时/输出上限护栏（run 10min / 输出 1MB），防挂死与内存失控
package agentd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 错误定义：
//   - ErrDirtyWorktree：工作区有未提交/未跟踪的改动，拒绝派发
//   - ErrPathEscape：请求的文件路径逃逸出任务仓库
var (
	ErrDirtyWorktree = errors.New("工作区不干净（有未提交改动），拒绝派发")
	ErrPathEscape    = errors.New("路径逃逸被拒绝")
)

// 执行护栏：
//   - runCmdTimeout：单条审阅命令的执行上限。包级 var 而非 const，便于测试注入更短值
//   - maxRunOutput：合并输出的截断上限，防止失控命令刷爆内存与响应体
var (
	runCmdTimeout = 10 * time.Minute
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

// PrepareBranch 在任务仓库上准备任务分支：工作区干净则 checkout -b handoff/<id8>。
//
// 为什么脏工作区必须拒绝：脏工作区上开分支会把无关改动带进任务分支，
// 后续 git diff 基准分支...HEAD 会把与任务无关的改动混进审核素材，审核被污染；
// 派发前强制 clean 是「任务 diff 只含本任务改动」承诺的实现保证。
//
// 参数：
//   - repo: 任务仓库路径
//   - taskID: 任务 ID（分支名取前 8 位，保证分支名可读且冲突概率可忽略）
//
// 返回：
//   - branch: 创建的任务分支名（handoff/<id8>）
//   - err: 工作区脏返回 ErrDirtyWorktree；git 失败返回带 stderr 的错误
func PrepareBranch(repo, taskID string) (branch string, err error) {
	// 脏检测用 status --porcelain：任何输出（含 ?? 未跟踪）都算脏——
	// 未跟踪文件同样可能被执行器误 add 进任务提交，保守拒绝
	if status, _, err := gitRun(context.Background(), repo, "status", "--porcelain"); err != nil {
		return "", fmt.Errorf("git status: %w", err)
	} else if strings.TrimSpace(status) != "" {
		first := firstLine(status)
		log().Warn("工作区不干净，拒绝派发", "repo", repo, "status", truncateRunes(first, 200))
		return "", fmt.Errorf("%w: %s", ErrDirtyWorktree, first)
	}
	branch = taskBranch(taskID)
	if _, stderr, err := gitRun(context.Background(), repo, "checkout", "-b", branch); err != nil {
		return "", fmt.Errorf("git checkout -b %s: %s: %w", branch, strings.TrimSpace(stderr), err)
	}
	log().Info("任务分支已创建", "repo", repo, "branch", branch)
	return branch, nil
}

// taskBranch 由任务 ID 派生分支名 handoff/<id8>。
func taskBranch(taskID string) string {
	id8 := taskID
	if len(id8) > 8 {
		id8 = id8[:8]
	}
	return "handoff/" + id8
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
//   - err: git 失败返回错误（如 baseBranch 不存在，stderr 已并入日志）
func Diff(repo, baseBranch string) (string, error) {
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
// 路径逃逸防御（安全红线）：filepath.Clean 归一化后，任何绝对路径或残留
// .. 前缀（Clean 后为 ".." 或以 "../" 开头）的路径一律拒绝——防审阅接口被
// 用来读取仓库外的任意文件（如 ~/.ssh/id_rsa）。符号链接逃逸不在本层处理
// （仓库自身指向外部链接的文件由审核者自担风险），见 task-9-report 备注。
//
// 参数：
//   - repo: 任务仓库路径
//   - rel: 相对仓库根的路径（如 cmd/foo.go）
//
// 返回：
//   - 文件内容
//   - err: 路径逃逸返回 ErrPathEscape；文件不存在返回 *fs.PathError（含 %w 链）
func ReadFile(repo, rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if rel == "" || cleaned == "." || filepath.IsAbs(cleaned) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		log().Warn("文件读取路径逃逸被拒绝", "repo", repo, "path", rel)
		return "", fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	p := filepath.Join(repo, cleaned)
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("读取文件 %s: %w", p, err)
	}
	return string(b), nil
}

// RunCmd 在任务仓库内执行一条审阅命令（sh -c），合并 stdout+stderr 截断 1MB。
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
	ctx, cancel := context.WithTimeout(ctx, runCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	cmd.Dir = repo
	log().Info("run 命令执行", "repo", repo, "cmd", truncateRunes(cmdline, 200))
	start := time.Now()
	out, err := cmd.CombinedOutput()
	truncated := truncateBytes(out, maxRunOutput)
	elapsed := time.Since(start)

	switch {
	case err != nil && ctx.Err() != nil:
		// 超时：CommandContext 已杀进程，err 是信号类错误；按 time 惯例记 124
		exitCode = 124
		log().Error("run 命令超时被终止", "repo", repo, "cmd", truncateRunes(cmdline, 200),
			"timeout", runCmdTimeout, "elapsed_ms", elapsed.Milliseconds())
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
			log().Error("run 命令启动失败", "repo", repo, "cmd", truncateRunes(cmdline, 200),
				"stderr", truncateRunes(string(truncated), 500), "cause", err)
		}
	default:
		log().Info("run 命令执行完成", "repo", repo, "cmd", truncateRunes(cmdline, 200),
			"exit_code", 0, "elapsed_ms", elapsed.Milliseconds())
	}
	if len(out) > maxRunOutput {
		log().Warn("run 输出超过上限已截断", "repo", repo, "cmd", truncateRunes(cmdline, 200),
			"output_bytes", len(out), "limit", maxRunOutput)
	}
	return string(truncated), exitCode, err
}

// truncateBytes 把字节切片截断到 max 长度（超出部分丢弃）。
func truncateBytes(b []byte, max int) []byte {
	if len(b) <= max {
		return b
	}
	return b[:max]
}

// firstLine 取多行文本的第一行（日志摘要用）。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
