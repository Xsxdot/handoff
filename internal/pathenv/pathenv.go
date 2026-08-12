// Package pathenv 把「本进程能看到的 PATH」补成「这台机器上用户实际可用的 PATH」。
//
// 职责：
//   - 从四个来源按序合成 PATH：进程继承 → 登录 shell → 显式配置目录 → 内置已知目录表
//   - 把合成结果写回 os.Setenv("PATH")，并返回本次新增的目录供调用方解释给用户
//
// 为什么需要第三、四层（B71）：B7 的登录 shell 合并只能拿到用户 rc 文件里写了的
// 目录。opencode 官方安装器把二进制放在 ~/.opencode/bin 却不一定改 rc——那台机器
// 的登录 shell 自己都不知道这个目录，agentd 更不可能知道，重启后第一次派发必然
// 报 "opencode: executable file not found in $PATH"。
//
// 边界：
//   - 只补 PATH，不动其他环境变量（补别的收益远小于误伤风险）
//   - 追加而非覆盖：既有条目顺序一律不动，不改 launchd/systemd 显式注入的优先级
//   - 任何一步失败都只记 WARN、绝不返回错误——PATH 不全只是找不到某些工具，
//     而启动失败是整机不可用
//   - 不做 symlink 归一：EvalSymlinks 会在网络盘与权限受限目录上引入新的失败模式，
//     而重复条目对 exec.LookPath 无害
package pathenv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// loginShellTimeout 是登录 shell 解析的时长上限。
//
// 登录 shell 会跑用户的 profile 脚本，个别环境里那些脚本很慢甚至挂住；
// 这是 agentd 的启动路径，不能为了补 PATH 把服务卡在启动中。
const loginShellTimeout = 3 * time.Second

// Options 描述一次解析要启用哪些来源。
type Options struct {
	// IncludeLoginShell 是否执行登录 shell 取 PATH（最多 loginShellTimeout）。
	// agentd 必须开——它常由非登录 shell 或进程管理器拉起；CLI 关，
	// 它本来就跑在用户的登录 shell 里，再跑一次只是白等。
	IncludeLoginShell bool
	// ExtraDirs 是 config.path_dirs：用户显式声明的目录，优先于内置已知目录表。
	ExtraDirs []string
}

// homeRelDirs 是相对 HOME 的已知安装目录。每一条都对应一个真实的安装落点，
// 不是「顺手加上」——加一条前先确认它是哪个工具的官方落点。
var homeRelDirs = []string{
	".opencode/bin",   // opencode 官方安装器（B71 故障现场）
	".grok/bin",       // grok CLI
	".claude/local",   // Claude Code 本地安装（migrate installer 落点）
	".local/bin",      // Claude Code native install / pipx / handoff 自己
	"bin",             // 传统用户 bin
	".bun/bin",        // bun 全局
	".npm-global/bin", // npm 自定义 prefix 的常见落点
	".cargo/bin",      // rust
	"go/bin",          // go
}

// absDirs 是与 HOME 无关的已知安装目录。
//
// 为什么不展开 ~/.nvm/versions/node/*/bin：用 nvm 的机器 rc 里必有 nvm 初始化，
// 登录 shell 那一层已经覆盖，且拿到的是用户当前选中的版本。glob 只能靠字典序
// 猜一个版本，猜错时的症状（工具在、node 版本不对）比找不到更难诊断。
var absDirs = []string{
	"/opt/homebrew/bin",  // Homebrew（Apple Silicon）
	"/opt/homebrew/sbin", //
	"/usr/local/bin",     // Homebrew（Intel）/ 手工安装
	"/usr/local/sbin",    //
	"/snap/bin",          // Linux snap
}

// 三个测试缝，生产实现即标准库；测试替换它们，从而不依赖跑测机器的真实环境。
var (
	// loginShellPATH 执行登录+交互 shell 取其 PATH。
	//
	// 为什么必须同时带 -l 和 -i（2026-08-08 devbox 实测，B7）：-l 只 source
	// .zshenv/.zprofile/.zlogin，而用户的 PATH 追加常写在 .zshrc——那是交互式才
	// 加载的文件。实测该机 .zshrc 第 2 行才是 /usr/local/go/bin 的来源，只用 -l
	// 拿到的 PATH 里根本没有它，这条补全会在它要解决的那台机器上恰好无效。
	//
	// 为什么不看退出码、只看 stdout：交互式 shell 在非 TTY 下会输出作业控制告警
	// 并可能以非零码退出，但 PATH 本身是拿到了的。stderr 必须丢弃——告警文本混进
	// stdout 会直接污染 PATH。
	loginShellPATH = func(ctx context.Context, shell string) (string, error) {
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

	// dirExists 判断路径存在且是目录。
	dirExists = func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir()
	}

	// homeDir 取当前用户主目录。
	//
	// 为什么要有 user.Current 兜底：老版本 systemd 不为 User= 设置 HOME，
	// 那台机器上 os.UserHomeDir 会失败，而 ~ 系条目正是最需要补的那批。
	homeDir = func() (string, error) {
		if h, err := os.UserHomeDir(); err == nil && h != "" {
			return h, nil
		}
		u, err := user.Current()
		if err != nil {
			return "", err
		}
		if u.HomeDir == "" {
			return "", errors.New("当前用户没有主目录")
		}
		return u.HomeDir, nil
	}
)

// Apply 解析 PATH 并写回进程环境。
//
// 参数：
//   - ctx: 上层上下文；登录 shell 那一层内部叠加 loginShellTimeout
//   - opt: 启用哪些来源
//   - log: 日志入口
//
// 返回：
//   - 本次新增的目录（按加入顺序）；无新增或写回失败时为 nil
//
// 注意：
//   - 不返回 error：本函数是 best-effort 增强，任何失败都只记日志
//   - 调用方拿 added 是为了向用户解释「这个工具是靠补全才找到的」（见 cmd/init.go）
//   - agentd 侧必须在**任何 fork 子进程之前**调用，合并结果才能被 executor、
//     审批者 CLI、审阅命令一并继承
func Apply(ctx context.Context, opt Options, log *slog.Logger) []string {
	cur := os.Getenv("PATH")
	seen := map[string]bool{}
	for _, d := range filepath.SplitList(cur) {
		if d != "" {
			seen[d] = true
		}
	}

	merged := cur
	var added, fromLogin, fromExtra, fromKnown []string
	// appendDir 追加一个尚未出现过的目录，同时记进对应的来源桶。
	// 分桶是为了让日志能说清「这个目录是哪一层带来的」——排障时
	// 「靠内置表兜住的」与「本来就在你 rc 里」是完全不同的结论。
	appendDir := func(d string, bucket *[]string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		if merged == "" {
			merged = d
		} else {
			merged += string(os.PathListSeparator) + d
		}
		added = append(added, d)
		*bucket = append(*bucket, d)
	}

	if opt.IncludeLoginShell {
		for _, d := range loginShellDirs(ctx, log) {
			appendDir(d, &fromLogin)
		}
	}
	for _, d := range opt.ExtraDirs {
		if !dirExists(d) {
			// 用户显式写下的目录却不存在，多半是笔误：必须给信号，不能静默
			log.Warn("config.path_dirs 里的目录不存在，已跳过", "dir", d)
			continue
		}
		appendDir(d, &fromExtra)
	}
	for _, d := range knownDirs(log) {
		appendDir(d, &fromKnown)
	}

	if len(added) == 0 {
		log.Info("PATH 无需补全")
		return nil
	}
	if err := os.Setenv("PATH", merged); err != nil {
		log.Warn("写入补全后的 PATH 失败，保持当前 PATH", "cause", err)
		return nil
	}
	log.Info("已补全 PATH",
		"login_shell", fromLogin, "extra_dirs", fromExtra, "known_dirs", fromKnown)
	return added
}

// loginShellDirs 取登录 shell 的 PATH 并拆成目录列表；失败返回 nil。
func loginShellDirs(ctx context.Context, log *slog.Logger) []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		log.Warn("未设置 $SHELL，跳过登录 shell 的 PATH 解析")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, loginShellTimeout)
	defer cancel()
	got, err := loginShellPATH(ctx, shell)
	if err != nil {
		log.Warn("登录 shell 解析 PATH 失败，跳过该来源", "shell", shell, "cause", err)
		return nil
	}
	return filepath.SplitList(got)
}

// knownDirs 返回内置表里**确实存在**的目录。
//
// 只返回存在的：把一堆不存在的目录塞进 PATH 会让每次 LookPath 多做一轮无用 stat，
// 也让日志里的「已补全」失去意义。
func knownDirs(log *slog.Logger) []string {
	var out []string
	home, err := homeDir()
	if err != nil {
		// 不致命：绝对路径那批（Homebrew / snap）仍然可用
		log.Warn("取不到主目录，跳过全部 ~ 系已知目录", "cause", err)
	} else {
		for _, rel := range homeRelDirs {
			if p := filepath.Join(home, rel); dirExists(p) {
				out = append(out, p)
			}
		}
	}
	for _, p := range absDirs {
		if dirExists(p) {
			out = append(out, p)
		}
	}
	return out
}
