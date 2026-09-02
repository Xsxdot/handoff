// 本文件实现 handoff init 子命令：一台新机器的问答式配置。
//
// 职责：
//   - 探测四家 executor 的状态并成表打印
//   - 问答编排在 internal/initflow（AskAll / MaybeInstallService）——本文件
//     只负责把它问到的答案写进 config.yaml
//   - 末尾打印本机 token 与现成的配对 yaml 片段
//
// 边界：
//   - **不发起任何真实模型调用**：探测一律用轻量本地判据（见 internal/toolchain）
//   - **不主动装服务，但会问**：角色含执行机且 stdin 是终端时，init 会追问一句
//     是否托管，答 y 则调 initflow.InstallService（与 handoff service install
//     同一条路径）。托管是「重启后 agentd 还回得来」的唯一保障，只留一行提示的
//     触达率不够（B71）。Linux 上非 root 时一律不代跑，只打印 sudo 命令
//   - **不阻断任何选择**：探测结果只影响默认值与标注；没装任何 executor 也能配完
//     （纯协调者机的正常情况），选了「未登录」的执行者只警告不拦
//   - stdin 非 tty 时一问不问：init 会被 install.sh 经管道调起，问了没人答，
//     卡住比不问糟得多
package cmd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/initflow"
	"github.com/Xsxdot/handoff/internal/pathenv"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// initStdinIsTTY 判断 stdin 是不是终端。测试替换它以覆盖两条分支。
var initStdinIsTTY = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }

// newInteractivePrompter 是 TTY 问答的构造缝。生产返回 huh；测试在
// runInitWith 里换成脚本化——huh 要真终端，CI 没有，不换就会挂死。
var newInteractivePrompter = func(in io.Reader, out io.Writer) initflow.Prompter {
	return newHuhPrompter()
}

// initCmd 交互式配置本机。
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "探测本机 executor 并交互式生成配置",
	RunE: func(cmd *cobra.Command, _ []string) error {
		p := effectiveConfigPath()
		out := cmd.OutOrStdout()

		// 必须在 Load 之前 stat：Load 发现文件不存在会按出厂值写盘，事后
		// 永远「存在」。出厂 listen 和用户选过「仅本机」都是 127.0.0.1:7777，
		// 只能靠「这次 init 之前文件在不在」区分首次执行机（预选所有网卡）
		// 和重跑保 loopback。
		_, statErr := os.Stat(p)
		cfgExisted := statErr == nil

		cfg, err := config.Load(p)
		if err != nil {
			return fmt.Errorf("加载配置 %s: %w", p, err)
		}

		// PATH 补全（B71）：探测前先按 agentd 的同一套规则补全，否则 init 说
		// 「就绪」而 agentd 说「未安装」是可能的——两边的 PATH 来源本就不同。
		// 关掉登录 shell 那一层：init 本来就跑在用户的登录 shell 里，再跑一次
		// 只是白等最多 3 秒。
		//
		// 用一个只放行 WARN 的 logger：补全成功是常态，把 INFO 打进交互向导的
		// 输出里只会挤掉用户真正要读的探测表；真出问题（$SHELL 没设、path_dirs
		// 目录不存在）仍然要让用户看见。
		quiet := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelWarn}))
		added := pathenv.Apply(cmd.Context(), pathenv.Options{ExtraDirs: cfg.PathDirs}, quiet)

		results := toolchain.Detect()
		printDetection(out, results, added)

		tty := initStdinIsTTY()
		slog.Info("init 进入问答", "tty", tty)
		if !tty {
			// 非交互降级：只探测 + 写出厂默认，明确告诉用户下一步做什么
			fmt.Fprintln(out, "\n未交互配置（stdin 不是终端），已写入默认配置。")
			fmt.Fprintf(out, "请在终端里运行 handoff init 完成配置：%s\n", p)
			if err := config.Save(p, cfg); err != nil {
				return err
			}
			slog.Info("init 已写盘", "path", p, "role", "")
			printPairing(out, cfg)
			return nil
		}

		// 生产走 huh；测试把 newInteractivePrompter 换成脚本化（见 runInitWith）。
		// AskAll 与 MaybeInstallService 必须共用同一个 prompter：各自再
		// new 一次会各包一层，后续答案会被提前吃掉。
		pr := newInteractivePrompter(cmd.InOrStdin(), out)
		isExec, role, err := initflow.AskAll(out, pr, cfg, results, cfgExisted)
		if err != nil {
			// 取消和问答失败都不得写盘：半截答案比取消本身更糟。
			if errors.Is(err, initflow.ErrCanceled) {
				slog.Warn("init 已取消，不写盘", "path", p)
			} else {
				slog.Error("init 问答失败，不写盘", "path", p, "cause", err)
			}
			return err
		}
		if err := config.Save(p, cfg); err != nil {
			return err
		}
		slog.Info("init 已写盘", "path", p, "role", role)
		fmt.Fprintf(out, "\n已写入 %s\n", p)
		printPairing(out, cfg)
		fmt.Fprintln(out, "init 可随时重跑，默认取当前配置，一路回车即保持不变。")
		initflow.MaybeInstallService(out, pr, isExec, p)
		return nil
	},
}

// printDetection 打印四家 executor 的探测表，再打一段与执行者无关的 env 提示。
//
// 参数：
//   - w: 输出目标
//   - rs: 探测结果
//   - addedDirs: 本次 PATH 补全新增的目录（pathenv.Apply 的返回值）
//
// 注意：
//   - 工具的所在目录若来自 addedDirs，要在该行下面说明清楚——用户在自己 shell 里
//     `which` 不到它，不解释的话这张表看起来就是错的
//   - claude 的 keychain 说明保留；不再按「探到了哪家」写代理专文
func printDetection(w io.Writer, rs []toolchain.Result, addedDirs []string) {
	fmt.Fprintln(w, "本机 executor 探测：")
	for _, r := range rs {
		path := r.Path
		if path == "" {
			path = "—"
		}
		fmt.Fprintf(w, "  %-9s %-20s %s\n", r.Name, r.State.String(), path)
		if d := coveredBy(r.Path, addedDirs); d != "" {
			fmt.Fprintf(w, "            ↳ %s 不在你的 PATH 里，agentd 启动时会自动补上。\n", d)
		}
	}
	for _, r := range rs {
		if r.Name == "claude" && r.State == toolchain.StateAuthUnknown {
			// 如实说明为什么判不出来，免得用户以为是探测坏了
			fmt.Fprintln(w, "\n  claude 的登录凭据存在系统 Keychain 里，本机判据够不着，所以只报「登录态未知」。")
			fmt.Fprintln(w, "  想确认是否可用，自己跑一次 claude -p \"hi\" 看有没有输出。")
		}
		if r.Name == "agy" && r.State == toolchain.StateAuthUnknown {
			fmt.Fprintln(w, "\n  agy 的登录凭据存在本地配置中，静态判据不直接断言，所以报「登录态未知」。")
			fmt.Fprintln(w, "  想确认是否可用，自己跑一次 agy -p \"hi\" 看有没有输出。")
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "执行者若需要代理、私有 registry 或额外 PATH，把变量写进 ~/.handoff/env/<名字>.env，再在 config.yaml 的 env 段挂上（如 codex: codex.env）。init 不创建、不修改这些文件。")
}

// coveredBy 返回 path 所在目录——当且仅当那个目录是本次 PATH 补全新增的；
// 否则返回空串。
//
// 为什么按目录精确相等而不是前缀匹配：前缀匹配会把 /opt/homebrew/bin/x/y 这类
// 更深层的路径也算进来，那不是同一个目录，说明会是错的。
func coveredBy(path string, added []string) string {
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	for _, d := range added {
		if d == dir {
			return d
		}
	}
	return ""
}

// printPairing 打印本机 token 与现成的配对片段。
//
// why 直接给 yaml 片段而不是只报 token：配对是最容易配错的一步（键名、缩进、
// 地址形态），给一段能直接粘的比让用户照着文档拼强得多。
func printPairing(w io.Writer, cfg *config.Config) {
	fmt.Fprintln(w, "\n本机 token 与配对片段（贴到协调者机的 config.yaml 里）：")
	fmt.Fprintln(w, "\ntargets:")
	fmt.Fprintf(w, "  <给这台机器起个名字>:\n")
	fmt.Fprintf(w, "    addr: \"%s\"\n", advertiseAddr(cfg.Listen))
	fmt.Fprintf(w, "    token: \"%s\"\n", cfg.Token)
	fmt.Fprintf(w, "    user: \"%s\"\n", os.Getenv("USER"))
	fmt.Fprintln(w, "\n  注意：addr 里的地址要换成协调者机能连到的实际 IP。")
}

func init() { rootCmd.AddCommand(initCmd) }
