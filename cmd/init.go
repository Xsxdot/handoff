// 本文件实现 handoff init 子命令：一台新机器的问答式配置。
//
// 职责：
//   - 探测四家 executor 的状态并成表打印
//   - 按角色分支问 11 组问题，把答案写进 config.yaml
//   - 末尾打印本机 token 与现成的配对 yaml 片段
//
// 边界：
//   - **不发起任何真实模型调用**：探测一律用轻量本地判据（见 internal/toolchain）
//   - **不装服务**：托管走 handoff service install。init 只在最后提示一句
//   - **不阻断任何选择**：探测结果只影响默认值与标注；没装任何 executor 也能配完
//     （纯审核者机的正常情况），选了「未登录」的执行者只警告不拦
//   - stdin 非 tty 时一问不问：init 会被 install.sh 经管道调起，问了没人答，
//     卡住比不问糟得多
package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/toolchain"
)

// initStdinIsTTY 判断 stdin 是不是终端。测试替换它以覆盖两条分支。
var initStdinIsTTY = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }

// 角色取值。init 先问角色，再按角色决定后面问什么。
const (
	roleExecutor = 1 // 执行机：跑 agentd 与 executor
	roleReviewer = 2 // 审核者机：派发与审阅
	roleBoth     = 3
)

// initCmd 交互式配置本机。
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "探测本机 executor 并交互式生成配置",
	RunE: func(cmd *cobra.Command, _ []string) error {
		p := effectiveConfigPath()
		out := cmd.OutOrStdout()

		// config.Load 在文件不存在时会生成 token 并写盘，正好作为「当前值」的基线：
		// 已存在则读回实际值（幂等的前提），不存在则拿到一份带默认值的新配置
		cfg, err := config.Load(p)
		if err != nil {
			return fmt.Errorf("加载配置 %s: %w", p, err)
		}

		results := toolchain.Detect()
		printDetection(out, results)

		if !initStdinIsTTY() {
			// 非交互降级：只探测 + 写出厂默认，明确告诉用户下一步做什么
			fmt.Fprintln(out, "\n未交互配置（stdin 不是终端），已写入默认配置。")
			fmt.Fprintf(out, "请在终端里运行 handoff init 完成配置：%s\n", p)
			if err := config.Save(p, cfg); err != nil {
				return err
			}
			printPairing(out, cfg)
			return nil
		}

		r := bufio.NewReader(cmd.InOrStdin())
		if err := askAll(out, r, cfg, results); err != nil {
			return err
		}
		if err := config.Save(p, cfg); err != nil {
			return err
		}
		fmt.Fprintf(out, "\n已写入 %s\n", p)
		printPairing(out, cfg)
		fmt.Fprintln(out, "\n下一步   handoff service install   （把 agentd 交给本机进程管理器托管）")
		return nil
	},
}

// printDetection 打印四家 executor 的探测表。
func printDetection(w io.Writer, rs []toolchain.Result) {
	fmt.Fprintln(w, "本机 executor 探测：")
	for _, r := range rs {
		path := r.Path
		if path == "" {
			path = "—"
		}
		fmt.Fprintf(w, "  %-9s %-20s %s\n", r.Name, r.State.String(), path)
	}
	for _, r := range rs {
		if r.Name == "claude" && r.State == toolchain.StateAuthUnknown {
			// 如实说明为什么判不出来，免得用户以为是探测坏了
			fmt.Fprintln(w, "\n  claude 的登录凭据存在系统 Keychain 里，本机判据够不着，所以只报「登录态未知」。")
			fmt.Fprintln(w, "  想确认是否可用，自己跑一次 claude -p \"hi\" 看有没有输出。")
		}
		if r.Name == "codex" && r.State != toolchain.StateMissing {
			// B30：漏配代理的症状极具迷惑性，探到 codex 就提醒一次（只提醒，不问）
			fmt.Fprintln(w, "\n  codex 若需代理才能连 OpenAI，请在 config.yaml 的 env 段配 codex: codex.env。")
			fmt.Fprintln(w, "  漏配的症状是会话建得起来、状态 running、一个 token 不产，只有 serve.log 里刷")
			fmt.Fprintln(w, "  failed to refresh available models。")
		}
	}
}

// askAll 按角色分支问完全部问题，就地改写 cfg。
func askAll(w io.Writer, r *bufio.Reader, cfg *config.Config, rs []toolchain.Result) error {
	fmt.Fprintln(w, "\n以下每一问直接回车即取方括号里的当前值。")

	// 1. 角色。探到就绪 executor 则默认「执行机」
	defRole := roleReviewer
	if toolchain.FirstReady(rs) != "" {
		defRole = roleExecutor
	}
	role := askInt(w, r, "这台机器的角色 1=执行机 2=审核者机 3=两者", defRole)
	isExec := role == roleExecutor || role == roleBoth
	isReviewer := role == roleReviewer || role == roleBoth

	if isExec {
		// 2-3. 缺省执行者与模型
		defExec := cfg.Executor.Default
		if defExec == "" {
			if first := toolchain.FirstReady(rs); first != "" {
				defExec = first
			} else {
				defExec = "opencode"
			}
		}
		cfg.Executor.Default = askString(w, r, "缺省执行者 executor.default", defExec)
		warnIfNotReady(w, rs, cfg.Executor.Default)
		cfg.Executor.Model = askString(w, r, "执行者模型 executor.model（空=用执行者自身默认）", cfg.Executor.Model)

		// 4. 监听地址
		fmt.Fprintln(w, "  提示：要被外机访问需改成 0.0.0.0:7777")
		cfg.Listen = askString(w, r, "监听地址 listen", cfg.Listen)

		// 5. 仓库落点
		cfg.RepoRoot = askString(w, r, "仓库落点根目录 repo_root（空=repo add --clone 必须显式给路径）", cfg.RepoRoot)

		// 6. 审批链
		cfg.Approver.Executor = askString(w, r, "审批链执行者 approver.executor（空=不启用，权限请求直接找人）", cfg.Approver.Executor)
		if cfg.Approver.Executor != "" {
			cfg.Approver.Model = askString(w, r, "审批链模型 approver.model（空=用执行者自身默认）", cfg.Approver.Model)
		}
	}

	// 7-8. 自动更新（两种角色都要）
	cfg.Update.Auto = askBool(w, r, "启用自动更新 update.auto", cfg.Update.Auto)
	if cfg.Update.Auto {
		cfg.Update.Interval = askDuration(w, r, "检查频率 update.interval", cfg.Update.Interval)
	}

	if isReviewer {
		// 9. 任务结束自动同步分支
		cfg.Sync.Auto = askBool(w, r, "任务结束自动同步远程分支到本地 sync.auto", cfg.Sync.Auto)
		// 10. targets 配对，循环添加
		askTargets(w, r, cfg)
	}
	return nil
}

// warnIfNotReady 在选了「没装」或「未登录」的执行者时警告一句——只警告，不拦。
//
// why 不拦：一台刚装好的机器上什么都还没登录，但用户知道自己等会儿要登；
// 拦住等于逼他先去登录再回来重跑 init。
func warnIfNotReady(w io.Writer, rs []toolchain.Result, name string) {
	for _, r := range rs {
		if r.Name != name {
			continue
		}
		if r.State == toolchain.StateMissing {
			fmt.Fprintf(w, "  ⚠ %s 没装。配置照写，但派活前需要先装上。\n", name)
		} else if r.State == toolchain.StateNoCreds {
			fmt.Fprintf(w, "  ⚠ %s 已安装但未登录。配置照写，但派活前需要先登录。\n", name)
		}
		return
	}
	fmt.Fprintf(w, "  ⚠ %s 不在已知的四家里（opencode/claude/grok/codex），派发时会报未注册。\n", name)
}

// askTargets 循环添加远程执行机配对，回车即结束。
func askTargets(w io.Writer, r *bufio.Reader, cfg *config.Config) {
	if cfg.Targets == nil {
		cfg.Targets = map[string]config.Target{}
	}
	if len(cfg.Targets) > 0 {
		fmt.Fprintf(w, "\n已配对 %d 台远程执行机：\n", len(cfg.Targets))
		for name, t := range cfg.Targets {
			fmt.Fprintf(w, "  %-12s %s  user=%s\n", name, t.Addr, t.User)
		}
	}
	for {
		name := askString(w, r, "\n新增远程执行机名字（直接回车结束）", "")
		if name == "" {
			return
		}
		t := cfg.Targets[name]
		t.Addr = askString(w, r, "  地址 addr（形如 100.73.238.21:7777）", t.Addr)
		t.Token = askString(w, r, "  令牌 token（对方 handoff init 末尾会打出来）", t.Token)
		t.User = askString(w, r, "  ssh 用户名 user（attach/pull 要用）", t.User)
		cfg.Targets[name] = t
	}
}

// printPairing 打印本机 token 与现成的配对片段。
//
// why 直接给 yaml 片段而不是只报 token：配对是最容易配错的一步（键名、缩进、
// 地址形态），给一段能直接粘的比让用户照着文档拼强得多。
func printPairing(w io.Writer, cfg *config.Config) {
	fmt.Fprintln(w, "\n本机 token 与配对片段（贴到审核者机的 config.yaml 里）：")
	fmt.Fprintln(w, "\ntargets:")
	fmt.Fprintf(w, "  <给这台机器起个名字>:\n")
	fmt.Fprintf(w, "    addr: \"%s\"\n", pairAddr(cfg.Listen))
	fmt.Fprintf(w, "    token: \"%s\"\n", cfg.Token)
	fmt.Fprintf(w, "    user: \"%s\"\n", os.Getenv("USER"))
	fmt.Fprintln(w, "\n  注意：addr 里的地址要换成审核者机能连到的实际 IP。")
}

// pairAddr 把 listen 里的 0.0.0.0 换成占位提示，免得用户直接粘一个连不上的地址。
func pairAddr(listen string) string {
	if strings.HasPrefix(listen, "0.0.0.0:") {
		return "<本机IP>:" + strings.TrimPrefix(listen, "0.0.0.0:")
	}
	return listen
}

// ask 打印提示并读一行；空行返回空串（调用方据此取默认值）。
func ask(w io.Writer, r *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(w, "%s []: ", prompt)
	}
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		// stdin 提前结束（脚本喂的答案用完了）：当作全部取默认，不报错。
		// 这样测试与真实的「Ctrl-D 提前结束」都能走到写盘
		fmt.Fprintln(w)
		return ""
	}
	return strings.TrimSpace(line)
}

// askString 读一个字符串，空行取默认。
func askString(w io.Writer, r *bufio.Reader, prompt, def string) string {
	if v := ask(w, r, prompt, def); v != "" {
		return v
	}
	return def
}

// askInt 读一个整数，空行或解析失败取默认。
func askInt(w io.Writer, r *bufio.Reader, prompt string, def int) int {
	v := ask(w, r, prompt, strconv.Itoa(def))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(w, "  «%s» 不是数字，取默认 %d\n", v, def)
		return def
	}
	return n
}

// askBool 读 y/n，空行取默认。
func askBool(w io.Writer, r *bufio.Reader, prompt string, def bool) bool {
	d := "n"
	if def {
		d = "y"
	}
	v := strings.ToLower(ask(w, r, prompt+" (y/n)", d))
	if v == "" {
		return def
	}
	return v == "y" || v == "yes"
}

// askDuration 读一个时长，空行或解析失败取默认。
func askDuration(w io.Writer, r *bufio.Reader, prompt string, def time.Duration) time.Duration {
	v := ask(w, r, prompt, def.String())
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(w, "  «%s» 不是合法时长（如 6h / 30m），取默认 %s\n", v, def)
		return def
	}
	return d
}

func init() { rootCmd.AddCommand(initCmd) }
