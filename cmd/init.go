// 本文件实现 handoff init 子命令：一台新机器的问答式配置。
//
// 职责：
//   - 探测四家 executor 的状态并成表打印
//   - 按角色分支问配置问题，把答案写进 config.yaml
//   - 末尾打印本机 token 与现成的配对 yaml 片段
//
// 边界：
//   - **不发起任何真实模型调用**：探测一律用轻量本地判据（见 internal/toolchain）
//   - **不主动装服务，但会问**：角色含执行机且 stdin 是终端时，init 会追问一句
//     是否托管，答 y 则调 installService（与 handoff service install 同一条路径）。
//     托管是「重启后 agentd 还回得来」的唯一保障，只留一行提示的触达率不够（B71）。
//     Linux 上非 root 时一律不代跑，只打印 sudo 命令
//   - **不阻断任何选择**：探测结果只影响默认值与标注；没装任何 executor 也能配完
//     （纯审核者机的正常情况），选了「未登录」的执行者只警告不拦
//   - stdin 非 tty 时一问不问：init 会被 install.sh 经管道调起，问了没人答，
//     卡住比不问糟得多
package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/pathenv"
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

		// TTY 与测试共用脚本化实现（读 cmd.In）。huh 在 Task 5 再接。
		// askAll 与 maybeInstallService 必须共用同一个 prompter：各自再
		// new 一次会各包一层 bufio，后续答案会被提前吃掉。
		pr := newScriptedPrompter(cmd.InOrStdin(), out)
		isExec, err := askAll(out, pr, cfg, results)
		if err != nil {
			return err
		}
		if err := config.Save(p, cfg); err != nil {
			return err
		}
		fmt.Fprintf(out, "\n已写入 %s\n", p)
		printPairing(out, cfg)
		maybeInstallService(out, pr, isExec, p)
		return nil
	},
}

// printDetection 打印四家 executor 的探测表。
//
// 参数：
//   - w: 输出目标
//   - rs: 探测结果
//   - addedDirs: 本次 PATH 补全新增的目录（pathenv.Apply 的返回值）
//
// 注意：
//   - 工具的所在目录若来自 addedDirs，要在该行下面说明清楚——用户在自己 shell 里
//     `which` 不到它，不解释的话这张表看起来就是错的
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
		if r.Name == "codex" && r.State != toolchain.StateMissing {
			// B30：漏配代理的症状极具迷惑性，探到 codex 就提醒一次（只提醒，不问）
			fmt.Fprintln(w, "\n  codex 若需代理才能连 OpenAI，请在 config.yaml 的 env 段配 codex: codex.env。")
			fmt.Fprintln(w, "  漏配的症状是会话建得起来、状态 running、一个 token 不产，只有 serve.log 里刷")
			fmt.Fprintln(w, "  failed to refresh available models。")
		}
	}
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

// askAll 按角色分支问完全部问题，就地改写 cfg。
//
// 参数：
//   - w: 面向用户的说明文字（非问答本身）
//   - p: 问答通道；本 task 一律是 scriptedPrompter
//   - cfg: 就地改写
//   - rs: 探测结果，只影响默认值与警告
//
// 返回：
//   - isExec: 本机角色是否包含执行机（决定 init 之后要不要追问托管）
//   - 错误：问答失败（脚本化路径几乎不返回错；huh 取消会走这里）
func askAll(w io.Writer, p prompter, cfg *config.Config, rs []toolchain.Result) (bool, error) {
	fmt.Fprintln(w, "\n以下每一问直接回车即取方括号里的当前值。")

	// 1. 角色。探到就绪 executor 则默认「执行机」。
	// 本 task 仍用数字 Input，不改提示文案；Task 4 再换成 Select。
	defRole := roleReviewer
	if toolchain.FirstReady(rs) != "" {
		defRole = roleExecutor
	}
	roleStr, err := p.Input("这台机器的角色 1=执行机 2=审核者机 3=两者", strconv.Itoa(defRole))
	if err != nil {
		return false, err
	}
	role := defRole
	if n, aerr := strconv.Atoi(roleStr); aerr != nil {
		fmt.Fprintf(w, "  «%s» 不是数字，取默认 %d\n", roleStr, defRole)
	} else {
		role = n
	}
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
		cfg.Executor.Default, err = p.Input("缺省执行者 executor.default", defExec)
		if err != nil {
			return false, err
		}
		warnIfNotReady(w, rs, cfg.Executor.Default)
		cfg.Executor.Model, err = p.Input("执行者模型 executor.model（空=用执行者自身默认）", cfg.Executor.Model)
		if err != nil {
			return false, err
		}

		// 4. 监听地址
		fmt.Fprintln(w, "  提示：要被外机访问需改成 0.0.0.0:7777")
		cfg.Listen, err = p.Input("监听地址 listen", cfg.Listen)
		if err != nil {
			return false, err
		}

		// 5. 仓库落点
		cfg.RepoRoot, err = p.Input("项目落点根目录 repo_root（自动登记时 clone 到这里）", cfg.RepoRoot)
		if err != nil {
			return false, err
		}

		// 6. 审批链
		cfg.Approver.Executor, err = p.Input("审批链执行者 approver.executor（空=不启用，权限请求直接找人）", cfg.Approver.Executor)
		if err != nil {
			return false, err
		}
		if cfg.Approver.Executor != "" {
			cfg.Approver.Model, err = p.Input("审批链模型 approver.model（空=用执行者自身默认）", cfg.Approver.Model)
			if err != nil {
				return false, err
			}
		}
	}

	if isReviewer {
		// 9. 任务结束自动同步分支
		cfg.Sync.Auto, err = p.Confirm("任务结束自动同步远程分支到本地 sync.auto", cfg.Sync.Auto)
		if err != nil {
			return false, err
		}
		// 10. targets 配对，循环添加
		if err := askTargets(w, p, cfg); err != nil {
			return false, err
		}
	}
	return isExec, nil
}

// maybeInstallService 在执行机上追问是否现在把 agentd 交给进程管理器托管，
// 答 y 则就地代跑。
//
// 参数：
//   - w: 面向用户的输出
//   - p: 问答通道（与 askAll 共用同一实例）
//   - isExec: 本机角色是否包含执行机
//   - cfgPath: 配置路径（传给服务单元）
//
// 注意：
//   - 无返回值：托管失败**绝不**让 init 失败。配置此时已经写盘，为一个附属动作
//     把整条 init 退非零，用户会以为配置没保存（与 install.sh 对 skill install
//     的处置同一个道理）
//   - Linux 上非 root 时不代跑：systemd 单元要写 /etc/systemd/system，需要 root，
//     而 init 不 sudo。此时只打印命令
//   - why 要追问而不是只提示：托管是「机器重启后 agentd 还回得来」的唯一保障，
//     它此前只是最后一行提示——B71 现场那台就是这么变成手工拉起的，重启后
//     PATH 全靠运气
func maybeInstallService(w io.Writer, p prompter, isExec bool, cfgPath string) {
	if !isExec {
		// 审核者机不跑 agentd，托管对它没有意义
		return
	}
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		fmt.Fprintln(w, "\n下一步   sudo handoff service install")
		fmt.Fprintln(w, "         systemd 单元要写 /etc/systemd/system，需要 root，init 不替你 sudo。")
		fmt.Fprintln(w, "         没有托管的 agentd 在机器重启后不会自己回来。")
		return
	}
	ok, err := p.Confirm("\n现在把 agentd 交给本机进程管理器托管", true)
	if err != nil {
		// 配置已经写盘，附属问答失败不能让 init 退非零
		fmt.Fprintf(w, "托管追问失败：%v\n", err)
		return
	}
	if !ok {
		fmt.Fprintln(w, "\n下一步   handoff service install")
		fmt.Fprintln(w, "         没有托管的 agentd 在机器重启后不会自己回来。")
		return
	}
	fmt.Fprintln(w)
	if err := installService(w, cfgPath); err != nil {
		fmt.Fprintf(w, "托管失败：%v\n", err)
		fmt.Fprintln(w, "配置已经写好了，稍后单独重跑 handoff service install 即可。")
	}
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
func askTargets(w io.Writer, p prompter, cfg *config.Config) error {
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
		name, err := p.Input("\n新增远程执行机名字（直接回车结束）", "")
		if err != nil {
			return err
		}
		if name == "" {
			return nil
		}
		t := cfg.Targets[name]
		t.Addr, err = p.Input("  地址 addr（形如 100.73.238.21:7777）", t.Addr)
		if err != nil {
			return err
		}
		t.Token, err = p.Input("  令牌 token（对方 handoff init 末尾会打出来）", t.Token)
		if err != nil {
			return err
		}
		t.User, err = p.Input("  ssh 用户名 user（attach/pull 要用）", t.User)
		if err != nil {
			return err
		}
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
	fmt.Fprintf(w, "    addr: \"%s\"\n", advertiseAddr(cfg.Listen))
	fmt.Fprintf(w, "    token: \"%s\"\n", cfg.Token)
	fmt.Fprintf(w, "    user: \"%s\"\n", os.Getenv("USER"))
	fmt.Fprintln(w, "\n  注意：addr 里的地址要换成审核者机能连到的实际 IP。")
}

func init() { rootCmd.AddCommand(initCmd) }
