// 本文件负责 agentd 启动期的 PATH 补全：从用户的登录 shell 解析完整 PATH 并
// 合并进当前进程环境。
//
// 职责：
//   - 以登录 shell（$SHELL -l -c 'echo $PATH'）解析用户实际可用的 PATH
//   - 把其中当前进程 PATH 尚未包含的目录追加到末尾
//
// 边界：
//   - 只补 PATH，不动其他环境变量（补全其他变量的收益远小于误伤风险）
//   - 追加而非覆盖：不改动 systemd/launchd 等显式注入的路径优先级
//   - 解析失败一律降级为 Warn，绝不阻断 agentd 启动——PATH 不全只是找不到某些
//     工具链，而启动失败是整机不可用
package agentd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// loginShellTimeout 是登录 shell 解析的时长上限。
// 登录 shell 会跑用户的 profile 脚本，个别环境里那些脚本可能很慢甚至挂住；
// 这是启动路径，不能为了补 PATH 把 agentd 卡在启动中。
const loginShellTimeout = 3 * time.Second

// loginShellPATH 执行登录+交互 shell 取其 PATH（包级 var 作为测试缝）。
//
// 为什么必须同时带 -l 和 -i（2026-08-08 devbox 实测）：-l 只 source
// .zshenv/.zprofile/.zlogin，而用户的 PATH 追加常写在 .zshrc——那是交互式才
// 加载的文件。实测该机 .zshrc 第 2 行才是 /usr/local/go/bin 的来源，只用 -l
// 拿到的 PATH 里根本没有它，这条修复会在它要解决的那台机器上恰好无效。
//
// 为什么不看退出码、只看 stdout：交互式 shell 在非 TTY 下会输出作业控制告警
// 并可能以非零码退出，但 PATH 本身是拿到了的。stderr 必须丢弃——告警文本混进
// stdout 会直接污染 PATH。
var loginShellPATH = func(ctx context.Context, shell string) (string, error) {
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", `printf %s "$PATH"`)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
	got := strings.TrimSpace(out.String())
	if got == "" {
		if runErr != nil {
			return "", runErr
		}
		return "", errors.New("登录 shell 未输出 PATH")
	}
	return got, nil
}

// MergeLoginShellPATH 把登录 shell 的 PATH 合并进当前进程环境。
//
// 参数：
//   - ctx: 上层上下文；内部叠加 loginShellTimeout
//   - log: 日志入口
//
// 注意：
//   - 无返回值：本函数是 best-effort 增强，任何失败都只记日志
//   - 合并结果对 agentd 之后 fork 的全部子进程生效（executor、审批者 CLI、
//     审阅命令），这正是修在 agentd 侧的价值——用户零配置
func MergeLoginShellPATH(ctx context.Context, log *slog.Logger) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		log.Warn("未设置 $SHELL，跳过登录 shell PATH 合并")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, loginShellTimeout)
	defer cancel()
	got, err := loginShellPATH(ctx, shell)
	if err != nil {
		log.Warn("登录 shell 解析 PATH 失败，保持当前 PATH", "shell", shell, "cause", err)
		return
	}
	cur := os.Getenv("PATH")
	have := map[string]bool{}
	for _, d := range strings.Split(cur, string(os.PathListSeparator)) {
		if d != "" {
			have[d] = true
		}
	}
	var added []string
	merged := cur
	for _, d := range strings.Split(got, string(os.PathListSeparator)) {
		if d == "" || have[d] {
			continue
		}
		have[d] = true
		added = append(added, d)
		merged += string(os.PathListSeparator) + d
	}
	if len(added) == 0 {
		log.Info("登录 shell PATH 无新增目录", "shell", shell)
		return
	}
	if err := os.Setenv("PATH", merged); err != nil {
		log.Warn("写入合并后的 PATH 失败，保持当前 PATH", "cause", err)
		return
	}
	log.Info("已合并登录 shell 的 PATH", "shell", shell, "added", added)
}
