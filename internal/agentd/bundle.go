// 本文件实现任务分支的 git bundle 生成，供 GET /api/tasks/{id}/bundle 使用。
//
// 职责：
//   - 在任务的**主仓库**里按 <have>..<branch> 生成薄包（have 为空则全量），落 OS 临时文件
//   - 把「空区间」与「have 不存在」这两种预期形态与真故障区分成可判别的哨兵
//
// 边界：
//   - 不碰 HTTP：状态码映射在 server.go 的 handleTaskBundle
//   - 不删临时文件：调用方拿到路径后自己 defer os.Remove
//   - 只读仓库，不建分支、不改任何 ref
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrEmptyRange 表示 <have>..<branch> 区间里没有任何提交。
//
// 为什么必须是可判别的哨兵：`git bundle create` 对空区间是**失败**
// （fatal: Refusing to create empty bundle.），而这个情形天天发生——连着 pull
// 两次、或任务没产生新提交就是。不把它与真故障分开，第二次 pull 就变成一个 500。
var ErrEmptyRange = errors.New("提交区间为空，无需生成 bundle")

// ErrHaveMissing 表示调用方声明的 have 提交在任务仓库中不存在。
//
// 为什么响亮失败而不是悄悄退回全量：客户端只会回传任务记录里的 BaseCommit，
// 它在任务仓库里找不到意味着真的出了异常。退回全量会让「协调者拿到的包比预期
// 大得多」这件事无声发生。
var ErrHaveMissing = errors.New("have 提交在任务仓库中不存在")

// BundleRange 在 repo 里生成 <have>..<branch> 的 git bundle，返回临时文件路径。
//
// 参数：
//   - ctx:    调用方上下文，透传给 git 子进程
//   - repo:   任务的**主仓库**路径（task.RepoPath，不是 Workdir()）——worktree 是
//     主仓的从属工作树，分支对象在主仓库里
//   - have:   协调者已有的基线提交；空串表示生成全量包
//   - branch: 任务分支名
//
// 返回：
//   - path: 生成的临时文件路径。**调用方负责 os.Remove**，本函数不回收
//   - err:  ErrEmptyRange（区间为空，属预期形态）/ ErrHaveMissing（have 不存在）/
//     ErrBadBaseBranch（参数以 - 开头或分支名为空）/ 其余为真故障
//
// 注意：
//   - 临时文件落 OS 临时目录，绝不落进 repo——那会让 dispatch 的干净工作区校验误报
//   - 不设体积上限：一个会拒绝合法全量包的上限，是把能用的路径改成坏的
func BundleRange(ctx context.Context, repo, have, branch string) (string, error) {
	// git 会把以 - 开头的参数解释为选项：这是参数注入面，与 Diff 的 base 同源，
	// 所以复用同一个哨兵（ErrBadBaseBranch），调用方的 400 映射也就统一了
	if branch == "" || strings.HasPrefix(branch, "-") {
		log().Warn("bundle 分支名非法被拒绝", "repo", repo, "branch", branch)
		return "", fmt.Errorf("%w: %q", ErrBadBaseBranch, branch)
	}
	if strings.HasPrefix(have, "-") {
		log().Warn("bundle have 参数非法被拒绝", "repo", repo, "have", have)
		return "", fmt.Errorf("%w: %q", ErrBadBaseBranch, have)
	}
	log().Info("开始生成 bundle", "repo", repo, "branch", branch, "have", have)

	revRange := branch
	if have != "" {
		// 用 gitProbe 而非 gitRun：cat-file -e 的非零退出是**正常分支**（协调者
		// 换了机器、在另一个克隆里 pull，都可能报一个本仓库没有的 sha），
		// 走 gitRun 会在成功路径的日志里留下 ERROR
		if _, _, err := gitProbe(ctx, repo, "cat-file", "-e", have+"^{commit}"); err != nil {
			log().Warn("bundle 的 have 提交在任务仓库中不存在", "repo", repo, "have", have)
			return "", fmt.Errorf("%w: %s", ErrHaveMissing, have)
		}
		revRange = have + ".." + branch
	}

	// 先数提交数再决定要不要造包：空区间对 git bundle create 是失败而非空包，
	// 而空区间是常态。判据用 rev-list --count 的数字，**不匹配 stderr 文案**
	//（那是英文、随 git 版本变，把预期形态的判据建在字符串比较上）
	out, _, err := gitRun(ctx, repo, "rev-list", "--count", revRange)
	if err != nil {
		log().Error("bundle 统计提交数失败", "repo", repo, "range", revRange, "cause", err)
		return "", fmt.Errorf("git rev-list --count %s: %w", revRange, err)
	}
	if strings.TrimSpace(out) == "0" {
		log().Info("bundle 区间为空，无需生成", "repo", repo, "range", revRange)
		return "", ErrEmptyRange
	}

	f, err := os.CreateTemp("", "handoff-bundle-*.bundle")
	if err != nil {
		log().Error("创建 bundle 临时文件失败", "repo", repo, "cause", err)
		return "", fmt.Errorf("创建 bundle 临时文件: %w", err)
	}
	path := f.Name()
	// 立刻关掉自己的句柄：真正写这个文件的是 git 子进程，CreateTemp 在这里只被
	// 用来取一个不冲突的路径
	_ = f.Close()

	if _, stderr, err := gitRun(ctx, repo, "bundle", "create", path, revRange); err != nil {
		_ = os.Remove(path)
		log().Error("生成 bundle 失败", "repo", repo, "range", revRange,
			"stderr", truncateRunes(stderr, 500), "cause", err)
		return "", fmt.Errorf("git bundle create %s: %w", revRange, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		log().Error("bundle 生成后无法读取", "repo", repo, "path", path, "cause", err)
		return "", fmt.Errorf("读取生成的 bundle: %w", err)
	}
	log().Info("bundle 生成完成", "repo", repo, "range", revRange, "bytes", fi.Size())
	return path, nil
}
