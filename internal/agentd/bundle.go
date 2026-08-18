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
//   - err:  ErrHaveMissing（have 不存在）/ ErrBadBaseBranch（参数以 - 开头或
//     分支名为空）/ 其余为真故障。**不会因为「没有新提交」而失败**——见下
//
// 注意：
//   - 临时文件落 OS 临时目录，绝不落进 repo——那会让 dispatch 的干净工作区校验误报
//   - 不设体积上限：一个会拒绝合法全量包的上限，是把能用的路径改成坏的
//   - **区间为空时会自动放宽**（§5.2）：git 拒绝造空包，而调用方需要的不只是
//     对象、还有包里那个 ref——客户端的本地分支引用是 fetch 的副产品。放宽到
//     <branch>~1..<branch> 让包一定含 ref，客户端因此一行都不用改
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

	// 先数提交数：空区间对 git bundle create 是**失败**（Refusing to create empty
	// bundle），而它确实会发生——分支 tip 从 have 可达时就是，实践中即「任务一个
	// 提交都没产生」。判据用 rev-list --count 的数字，**不匹配 stderr 文案**
	//（那是英文、随 git 版本变，把预期形态的判据建在字符串比较上）
	out, _, err := gitRun(ctx, repo, "rev-list", "--count", revRange)
	if err != nil {
		log().Error("bundle 统计提交数失败", "repo", repo, "range", revRange, "cause", err)
		return "", fmt.Errorf("git rev-list --count %s: %w", revRange, err)
	}
	if strings.TrimSpace(out) == "0" {
		revRange, err = widenEmptyRange(ctx, repo, branch)
		if err != nil {
			return "", err
		}
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

// widenEmptyRange 在提交区间为空时给出一个**一定能造出包**的替代区间。
//
// 参数：repo 任务主仓库；branch 任务分支名
//
// 返回：可直接交给 git bundle create 的 rev 区间；分支 tip 是根提交时返回 branch
// 本身（全量包）。
//
// 为什么不能就此返回「无需生成」：客户端要的不只是对象，还有**包里那个 ref**。
// 本地分支引用是 `git fetch <分支>:<分支>` 的副产品，不是「有新提交」的副产品——
// ssh 老路无条件 fetch 所以 ref 总会建出来；bundle 路径若在这里短路，客户端就会
// 拿到一句「已是最新」而手上根本没有那个分支（B143 真机验收实测）。
//
// 为什么是放宽区间而不是「让客户端自己建 ref」：后者要新增一个协议头、一块客户端
// 逻辑，还要本设计自己发明一条「本地 ref 指向别处怎么办」的策略——而 git 的 fetch
// 语义本来就有答案（非快进即失败），ssh 老路一直照此行事。放宽区间让 fetch 该怎样
// 就怎样，代价只是多传一个提交的对象（几百字节），而客户端已有这些对象、fetch 是
// 无操作。
func widenEmptyRange(ctx context.Context, repo, branch string) (string, error) {
	// 分支 tip 有父提交：退一格，包里就一定含 ref 且只多带这一个提交的对象
	if _, _, err := gitProbe(ctx, repo, "rev-parse", "--verify", branch+"~1"); err == nil {
		widened := branch + "~1.." + branch
		log().Info("bundle 区间为空，放宽一格以保证包内含 ref",
			"repo", repo, "branch", branch, "range", widened)
		return widened, nil
	}
	// 根提交没有 ~1：退回全量包。此时全量就是那一个根提交，本身也很小
	log().Info("bundle 区间为空且分支 tip 是根提交，退回全量包",
		"repo", repo, "branch", branch)
	return branch, nil
}
