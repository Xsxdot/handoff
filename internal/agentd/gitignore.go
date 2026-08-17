// 本文件实现目录列举的「被 .gitignore 排除」标注。
//
// 职责：给一层已列举出的条目盖 Ignored 标记，判据是 git 自己的 check-ignore。
//
// 边界：
//   - **不自己解析 .gitignore**。忽略规则有全局配置、仓库 .gitignore、
//     .git/info/exclude、目录级 .gitignore、否定规则（!）与优先级，自己写一份
//     必然与 git 分叉——分叉的方向还很坏：把源码标成垃圾，或把垃圾标成源码
//   - **查不出来就全部按未忽略**（fail open）：git 不在、目录不是仓库、超时，
//     都只打日志。少标一个标记只是少一点信息，标错则是主动误导
//   - 不改变条目顺序、不过滤条目：藏不藏由前端决定，服务端只如实报告
package agentd

import (
	"bytes"
	"context"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ignoreProbeTimeout 是单次 check-ignore 的上限。
//
// 与 worktreeProbeTimeout 同量级但更短：它挂在展开目录这个交互动作上，
// 用户按一下就该出结果；真卡住时宁可少几个标记也不能让这一层列不出来。
const ignoreProbeTimeout = 2 * time.Second

// markIgnored 就地给 entries 打上 Ignored 标记。
//
// 参数：
//   - ctx: 上下文；内部再叠加 ignoreProbeTimeout 作为兜底上限
//   - root: 工作树绝对路径（调用方已过白名单闸门）
//   - rel: 这一层相对工作树根的路径；"" 或 "." 表示根
//   - entries: 待标注的条目（原地修改）
//
// 无返回值：所有失败路径都是「保持原样 + 日志」，调用方不需要为它写分支。
//
// 注意：走一次 git 子进程（每层一次，不是每条一次）。列举本身是按需展开的，
// 所以这笔开销只落在用户真正点开的那几层上。
func markIgnored(ctx context.Context, root, rel string, entries []proto.DirEntry) {
	if len(entries) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, ignoreProbeTimeout)
	defer cancel()

	// 相对仓库根的路径列表；-z 意味着输入输出都用 NUL 分隔，路径里的空格、
	// 换行、引号都不需要转义（文件名什么字符都可能有）
	var in bytes.Buffer
	paths := make([]string, len(entries))
	for i, e := range entries {
		p := e.Name
		if rel != "" && rel != "." {
			p = path.Join(rel, e.Name)
		}
		paths[i] = p
		in.WriteString(p)
		in.WriteByte(0)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-z", "--stdin")
	cmd.Stdin = &in
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		// 退出码 1 = 「一条都没被忽略」，是正常答案不是错误（git 的约定）；
		// 其余（128=不是仓库/git 不可用，超时等）如实记一条，条目保持未标记
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			log().Warn("ignore 探测失败，条目按未忽略返回",
				"root", root, "rel", rel, "cause", strings.TrimSpace(stderr.String()), "err", err)
			return
		}
		log().Debug("ignore 探测完成：本层无忽略项", "root", root, "rel", rel)
		return
	}

	ignored := map[string]bool{}
	for p := range strings.SplitSeq(out.String(), "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}
	n := 0
	for i := range entries {
		if ignored[paths[i]] {
			entries[i].Ignored = true
			n++
		}
	}
	log().Debug("ignore 探测完成", "root", root, "rel", rel, "entries", len(entries), "ignored", n)
}
