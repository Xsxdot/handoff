// 本文件实现 handoff dispatch 子命令：把本地 plan 文件（或 --prompt 直接指令）
// 派发到 agentd 执行。
//
// 职责：
//   - 读取本地 plan 文件并 base64 编码，连同项目身份/计划名/target/执行者/模型/
//     分支/worktree 等参数一并 POST 给 agentd（body {project_id, plan_b64, prompt, ...}）
//   - 派发的项目由 cwd 识别：读当前目录 git 仓库的 origin 离线算出 project_id，
//     cwd 不是目标项目时用 --project <名字> 显式指定
//   - 远程派发时采集本地 HEAD 作基线随请求上送，并校验本地工作区完整性
//     （已跟踪改动拒发、未跟踪警告；--no-sync-check 关掉整块，--allow-dirty 只关拒发）
//   - 派发成功后在 stderr 打一行基线摘要（起点短号 + 任务仓库领先的提交数）
//   - 派发成功后在 stderr 提示执行机仓库的未提交改动（managed 工作树不含它们）
//   - 成功时单行输出任务 JSON（state=running，供上层脚本解析任务 id）
//
// 边界：
//   - 只做文件读取与上传，不校验计划内容语义（解析与执行由 executor 负责）
//   - --no-terminal 在本文件只注册 flag 并参与「是否弹终端」的判定骨架；
//     弹终端默认**不弹**（cfg.Terminal.Auto 默认 false），配置 auto: true 时
//     才在 darwin 弹窗，--no-terminal 用于逐次关闭
package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/projectid"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var (
	dispatchProject     string
	dispatchPrompt      string
	dispatchName        string
	dispatchExecutor    string
	dispatchModel       string
	dispatchBranch      string
	dispatchNewBranch   string
	dispatchBase        string
	dispatchWorktree    string
	dispatchNewWorktree bool
	dispatchNoTerminal  bool
	dispatchNoSyncCheck bool
	dispatchAllowDirty  bool
	// dispatchDisciplineFile 是 P1 裁决 (a) 的临时正文入口：读文件作 RawText 走
	// 缝 1，只随这一次派发下发，不落账本（版本记 0）。flag 不属契约冻结物。
	dispatchDisciplineFile string
)

// projectNotRegisteredMarker 是 agentd 侧 ErrProjectNotRegistered 的哨兵文案。
//
// 为什么用文本而不是 errors.Is：错误跨进程传递，到 CLI 这一侧只剩报文。
// agentd 的报文一律形如 fmt.Errorf("%w: …", ErrProjectNotRegistered)，
// 因此这四个字必然出现在报文里。**改动 agentd 侧那个哨兵的文案就必须同步改这里**
// （internal/agentd/projectresolve.go 的 ErrProjectNotRegistered 上有对应提示）。
const projectNotRegisteredMarker = "项目未登记"

// isProjectNotRegistered 报告一个 dispatch 错误是不是「目标机上没有这个项目」。
//
// 参数：
//   - err: Dispatch 返回的错误（可为 nil）
//
// 返回：
//   - true 表示可以走自动补登记后重发的路径
func isProjectNotRegistered(err error) bool {
	return err != nil && strings.Contains(err.Error(), projectNotRegisteredMarker)
}

// dispatchWithAutoRegister 执行「派发 → 未登记则补登记 → 重发一次」的编排。
//
// 参数：
//   - dispatch: 发一次派发请求
//   - register: 补一次登记（两跳：本机 + 目标机）
//
// 返回：
//   - 派发成功的任务；任一环节失败时返回错误
//
// 注意：
//   - 为什么「先发再被拒再重发」而不是先预检：项目解析是 dispatch 的第一道闸，
//     早于建任务目录、早于工作区准备、早于 executor 启动——被拒的全部代价就是
//     一次 HTTP 400，没有任何残留要清理。而预检还多一次 TOCTOU（查完到派发之间
//     可以 project rm），服务端照样得判（spec §6.4）
//   - **最多重发一次**：登记成功后仍被拒说明另有原因（如刚被别人 rm 掉），
//     无限重试只会把一个可诊断的失败变成一个死循环
//   - 登记失败时**不重发、不降级**，原文透出：clone 失败或落点被占都需要人去
//     那台机器上处置，替它猜只会掩盖真因
//   - 两个副作用以闭包注入，编排本身因此可以零网络单测
func dispatchWithAutoRegister(dispatch func() (*proto.Task, error), register func() error) (*proto.Task, error) {
	task, err := dispatch()
	if !isProjectNotRegistered(err) {
		return task, err
	}
	if rerr := register(); rerr != nil {
		return nil, fmt.Errorf("自动登记失败: %w", rerr)
	}
	return dispatch()
}

// localHeadCommit 取当前工作目录所在 git 仓库的 HEAD 提交号，作为远程基线校验的基准。
//
// 返回空串的三种情况（都按「不校验」处理，不报错）：cwd 不是 git 仓库、
// 仓库还没有任何提交、git 不可用。为什么不报错：dispatch 完全可以在非仓库目录
// 发起（如只用 --prompt 派发一次性任务），把它做成硬性前提会挡掉正常用法。
func localHeadCommit() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// shortSHA 取提交号前 7 位（git 惯例的短号）；不足 7 位原样返回。
// 摘要行给人读，40 位全量 sha 会把有用信息挤出视线——完整值在任务 JSON 的
// base_commit 里，需要精确比对时从那里取。
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// looksLikeSHA 判断字符串是否是提交号形态（全十六进制且不短于 7 位）。
// 用途单一：决定回显里要不要把用户输入的 --base 原文再打一遍——原文就是
// sha 时再打一遍是纯噪音。
func looksLikeSHA(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// baselineLine 拼派发后回显给协调者的那一行（分支 + 起点 [+ base 原文] [+ 领先提示]）。
//
// 参数：
//   - task: 派发应答里的任务；BaseCommit 由 agentd 保证是 40 位 sha
//   - userBase: 用户在命令行给的 --base 原文（空=没给）
//
// 为什么要同时打三样：B76 的现场里回显只有「基线 worktre」——BaseCommit 当时
// 存的是分支名，又被 shortSHA 按短 sha 截成 7 字符，于是「任务开错了分支」这件
// 事在派发那一刻毫无痕迹，一路静默到收工 pull 才炸。三个信息同行互证，任何一
// 项不符当场看得见。
func baselineLine(task *proto.Task, userBase string) string {
	line := fmt.Sprintf("分支 %s，起点 %s", task.Branch, shortSHA(task.BaseCommit))
	if userBase != "" && !looksLikeSHA(userBase) {
		line += "（" + userBase + "）"
	}
	if task.BaseAhead > 0 {
		line += fmt.Sprintf("（任务仓库 HEAD 领先 %d 个提交，新分支不含它们）", task.BaseAhead)
	}
	return line
}

// dispatchCmd 派发一个计划任务到 agentd 执行。
//
// 使用方式：handoff dispatch [--project <名字>] [--prompt ...] [--executor x] [--model m]
// [--branch b | --new-branch b] [--base t] [--worktree w | --new-worktree]
// [--no-terminal] [plan 文件]
// resolveBareDiscipline 是裸派发的缝 1 收口（B229 契约 §2.2/§3.1）：未点名只注入
// 平台层正文（版本 0）；--discipline-file 把文件内容作 RawText 直通（不落库、版本 0，
// 服务 spec 用户故事 3 的「临时捏一份下发」）。无论哪种形态都先探目标机能力位——
// 探活失败必须保留网络/认证 cause 并停止派发；只有探活成功但能力位缺席=nil 或
// false 才交给 ResolveDispatch 产生「升级」拒发文案。
//
// 参数：cli 已装配的目标机客户端（探活用）；rawFile --discipline-file 路径（空=未点名）。
// 返回：随派发下发的正文三元组；文件不可读或拒发闸拦下时返回错误。
func resolveBareDiscipline(ctx context.Context, cli *client.Client, rawFile string) (discipline.ResolvedDiscipline, error) {
	ref := discipline.DisciplineRef{}
	if rawFile != "" {
		content, err := os.ReadFile(rawFile)
		if err != nil {
			slog.Warn("裸派发读临时纪律正文失败", "path", rawFile, "cause", err)
			return discipline.ResolvedDiscipline{}, fmt.Errorf("读取 --discipline-file %s 失败: %w", rawFile, err)
		}
		ref.RawText = string(content)
	}
	var cap *bool
	status, err := cli.Status(ctx)
	if err != nil {
		slog.Error("裸派发前目标机探活失败", "target", targetName, "cause", err)
		return discipline.ResolvedDiscipline{}, fmt.Errorf(
			"目标机探活失败：请确认目标机可达、agentd 正在运行且 token 一致：%w", err)
	}
	cap = status.DisciplinesSupported
	res, err := discipline.ResolveDispatch(nil, ref, loadCLIConfig().PlatformInvariantsEnabled(), cap)
	if err != nil {
		slog.Warn("裸派发被拒发闸拦下", "target", targetName,
			"has_raw_text", rawFile != "", "cap_absent", cap == nil, "cause", err)
		return discipline.ResolvedDiscipline{}, err
	}
	slog.Info("裸派发纪律正文已就绪", "target", targetName,
		"source", res.Source, "bytes", len(res.Text))
	return res, nil
}

var dispatchCmd = &cobra.Command{
	Use:   "dispatch [plan 文件]",
	Short: "派发一个计划任务到 agentd 执行",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// B62：派发的项目由 cwd 识别，路径不再出现在命令上。只依赖本机信息，
		// 多跑一次网络毫无意义——识别不了的，这里就直接说清楚怎么补。
		var planB64, planName string
		if len(args) == 1 {
			content, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("读取计划文件 %s: %w", args[0], err)
			}
			planB64 = base64.StdEncoding.EncodeToString(content)
			planName = filepath.Base(args[0])
		}
		// plan 文件与 --prompt 都缺：本地先报错，省一次网络往返（服务端也会拒）
		if planB64 == "" && dispatchPrompt == "" {
			return fmt.Errorf("必须提供 plan 文件或 --prompt（至少其一）")
		}
		// B62：项目识别。这一条在 CLI 侧判是因为它只依赖本机信息，多跑一次网络毫无意义。
		projectID := ""
		if dispatchProject == "" {
			origin := localOriginURL()
			if origin == "" {
				return fmt.Errorf("派发的项目由当前目录识别：当前目录不是 git 仓库（或没有 origin）；" +
					"请在项目目录内执行，或用 --project <名字> 指定")
			}
			projectID = projectid.FromOrigin(origin)
		}
		cli, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		// 只对远程 target 采集基线：本机派发时 cwd 与目标项目未必是同一个仓库，
		// 拿 cwd 的 HEAD 去校验别的仓库会造成假拒绝
		baseCommit := ""
		if targetName != "" && !dispatchNoSyncCheck {
			baseCommit = localHeadCommit()
			if baseCommit == "" {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"提示: 当前目录不是 git 仓库，已跳过远程基线校验（远程仓库可能落后于你的本地代码）")
			} else if err := checkLocalWorktree(cmd.ErrOrStderr(), dispatchAllowDirty); err != nil {
				// B29：基线只带得走已提交的东西。这里拒发是为了不在远端留下
				// 任何副作用（分支、worktree、任务记录），所以必须在发请求之前
				return err
			}
		}
		opts := client.DispatchOpts{
			ProjectID: projectID, ProjectName: dispatchProject,
			PlanB64: planB64, PlanName: planName, Target: targetName,
			Prompt: dispatchPrompt, Name: dispatchName,
			Executor: dispatchExecutor, Model: dispatchModel,
			Branch: dispatchBranch, NewBranch: dispatchNewBranch, Base: dispatchBase,
			Worktree: dispatchWorktree, NewWorktree: dispatchNewWorktree,
			BaseCommit: baseCommit,
		}
		// B229 缝 1：裸派发也注入平台层正文（实现决定 1），每一次派发都过拒发闸
		// （§3.1）。解析必须在发出任何任务请求之前完成——被拒时目标机上零残留。
		resolved, err := resolveBareDiscipline(cmd.Context(), cli, dispatchDisciplineFile)
		if err != nil {
			return err
		}
		opts.DisciplineText = resolved.Text
		opts.DisciplineVersion = resolved.Version
		task, err := dispatchWithAutoRegister(
			func() (*proto.Task, error) { return cli.Dispatch(cmd.Context(), opts) },
			func() error {
				// 用 --project <名字> 指名的项目查不到时，自动登记帮不上忙：
				// 名字不是身份，本机无从知道那个名字该指向哪个 origin。
				if dispatchProject != "" {
					return fmt.Errorf("--project 指定的 %q 在目标机上不存在；"+
						"在该项目目录里执行 handoff project add 登记它", dispatchProject)
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "目标机上还没有这个项目，正在自动登记…")
				root, rerr := localProjectRoot(cmd.Context())
				if rerr != nil {
					return rerr
				}
				// 走的就是 project add 那条路：既补本机，也补目标机（spec §6.2）。
				// 本机送主工作树路径、永不 clone；目标机不送 path，由它 clone 到
				// 自己的 repo_root/<名字>——本机因此一个远程细节都不需要知道。
				return registerProjectBothHops(cmd, localOriginURL(), "", root, "")
			},
		)
		if err != nil {
			return err
		}
		// 基线摘要走 stderr：stdout 是「单行任务 JSON」的既有契约，上层脚本
		// 按行解析，多打一行就会把它们全部打断。为什么必须打：B35 的现场里
		// 分支开在了三批改动之前，而这件事在任何输出里都不留痕迹——协调者
		// 甚至反过来怀疑是执行者找错了目录
		if task.BaseCommit != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), baselineLine(task, dispatchBase))
		}
		// B43：新工作树不含执行机仓库里未提交的改动，而协调者看不到那台机器的
		// 工作区——不说，executor 就会在一份没有那些改动的代码上开工而无人知晓。
		// 与基线行同走 stderr（stdout 的单行任务 JSON 契约不能破，见上方注释）
		if task.RepoDirtyCount > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"提示: 执行机仓库有 %d 处未提交改动，新工作树不含它们：%s\n",
				task.RepoDirtyCount, task.RepoDirtyFiles)
		}
		if task.Discipline != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "纪律块:", task.Discipline)
		}
		b, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("序列化任务: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		// 弹终端块的入口：--no-terminal 在此使能判定，默认不弹（auto: true 才弹）
		dispatchAfterTerminal(cmd, task.ID)
		return nil
	},
}

func init() {
	dispatchCmd.Flags().StringVar(&dispatchProject, "project", "",
		"跨项目派发时指定项目名（省略则由当前目录自动识别；用 handoff project ls 查看有哪些）")
	dispatchCmd.Flags().StringVar(&dispatchPrompt, "prompt", "", "直接指令（prompt-only 派发；与 plan 文件至少其一）")
	dispatchCmd.Flags().StringVar(&dispatchName, "name", "", "任务展示名（默认从 plan 文件名或 prompt 派生）")
	dispatchCmd.Flags().StringVar(&dispatchExecutor, "executor", "", "执行者名（opencode/claude/grok/codex/fake；空=agentd 默认执行者）")
	dispatchCmd.Flags().StringVar(&dispatchModel, "model", "", "任务级模型覆盖（空=执行者自身默认）")
	dispatchCmd.Flags().StringVar(&dispatchBranch, "branch", "", "切到已存在分支（与 --new-branch 互斥）")
	dispatchCmd.Flags().StringVar(&dispatchNewBranch, "new-branch", "", "新建分支名（空且 --branch 空=自动 handoff/<id8>）")
	dispatchCmd.Flags().StringVar(&dispatchBase, "base", "", "新分支起点 commit/分支（与 --branch 互斥；空=取派发时的基线起点）")
	dispatchCmd.Flags().StringVar(&dispatchWorktree, "worktree", "", "用户自带 worktree 路径（与 --new-worktree 互斥）")
	dispatchCmd.Flags().BoolVar(&dispatchNewWorktree, "new-worktree", false, "在 DataDir/worktrees 下新建 managed worktree（任务完成时自动删除）")
	dispatchCmd.Flags().BoolVar(&dispatchNoTerminal, "no-terminal", false, "不弹终端实况（默认不弹；配置 terminal.auto: true 时才弹，本标志用于逐次关闭）")
	dispatchCmd.Flags().BoolVar(&dispatchNoSyncCheck, "no-sync-check", false,
		"跳过远程仓库基线校验（cwd 与目标项目不是同一个仓库时用）")
	dispatchCmd.Flags().BoolVar(&dispatchAllowDirty, "allow-dirty", false,
		"本地工作区有未提交的已跟踪改动时仍照常派发（executor 看不到这些改动）")
	dispatchCmd.Flags().StringVar(&dispatchDisciplineFile, "discipline-file", "",
		"临时捏一份纪律块正文直接下发：读文件作本次派发的角色层正文（与平台层组装），不落账本、版本记 0")
	dispatchCmd.MarkFlagsMutuallyExclusive("branch", "new-branch")
	dispatchCmd.MarkFlagsMutuallyExclusive("worktree", "new-worktree")
	rootCmd.AddCommand(dispatchCmd)
}

// dispatchAfterTerminal 是 dispatch 成功后「弹终端实况」的钩子。
//
// 行为契约：
//   - 默认**不弹**：仅当 cfg.Terminal.Auto==true 且 darwin 时 osascript 弹
//     Terminal.app 执行 attach 命令；--no-terminal 在 auto: true 时逐次关闭
//     （保留不删：老脚本带着它不能报错，且 auto: true 时它仍是唯一逐次关闭入口）
//   - 不弹（--no-terminal / Terminal.Auto==false / 非 darwin）→ 打印提示行
//     「实况: handoff attach <id>」（远程含 --target）
//
// 为什么提示行走 stderr 而不是 stdout：stdout 是「单行任务 JSON」的既有契约
// （见上方基线行、dirty 提示的注释），上层脚本按行解析，多一行就全乱。默认
// 不弹之后这行提示**每次**派发都会出现，必须与基线行、dirty 提示同走 stderr。
//
// 为什么弹窗失败不影响退出码：派发已成功（任务 JSON 已输出），弹窗只是增强
// 可见性——失败降级为同款提示行 + stderr 警告，绝不把已成功的派发变成失败。
func dispatchAfterTerminal(cmd *cobra.Command, taskID string) {
	hint := "实况: handoff attach " + taskID
	if targetName != "" {
		hint += " --target " + targetName
	}
	cfg := loadCLIConfig()
	if dispatchNoTerminal || !cfg.Terminal.Auto || runtime.GOOS != "darwin" {
		fmt.Fprintln(cmd.ErrOrStderr(), hint)
		return
	}
	// 弹的窗口跑 handoff attach <id> [--target <t>]：attach 已改走 agentd 的
	// render 流（复用 agentd 连接与鉴权），弹窗命令只指向 CLI 自身，不再拼 ssh
	argv := []string{"handoff", "attach", taskID}
	if targetName != "" {
		argv = append(argv, "--target", targetName)
	}
	if err := openTerminal(argv); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "弹终端失败:", err)
		fmt.Fprintln(cmd.ErrOrStderr(), "可手动执行:", strings.Join(argv, " "))
		// 降级提示行同样走 stderr：stdout 的单行任务 JSON 契约不能破（见函数头注释）
		fmt.Fprintln(cmd.ErrOrStderr(), hint)
	}
}

// appleScriptQuote 把字符串包成单引号 shell 字面量（内含单引号转义为 '\”）。
//
// 为什么留在 cmd 包而不是抽公共包：拆掉 tmux 后全项目只剩这一处需要 shell 引号
// ——osascript 的 do script 参数。为一个调用点维护一个包不划算，且抽出去容易
// 被误当成「通用 shell 拼接工具」重新用起来，那正是我们刚拆掉的东西。
func appleScriptQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// openTerminal 用 osascript 在 macOS 上弹 Terminal.app 执行 attach 命令。
//
// 测试缝：包级变量，测试替换记录调用。为什么用 do script 而不是让 agentd 弹：
// dispatch 成功时协调者大概率不在本机终端前（在 agentd 所在机器的桌面前），
// Terminal.app 弹窗把「executor 实况」直接送到桌面——do script 让 Terminal
// 打开新窗口执行 attach，窗口内即实况；activate 把窗口置前。
var openTerminal = func(attachArgv []string) error {
	// do script 的参数是 shell 命令串：attach argv 逐元素 appleScriptQuote 拼接，
	// 保证含空白的路径/会话名不被拆错；AppleScript 字符串用 strconv.Quote
	// 包裹（与 wait 命令的系统通知同约定）
	quoted := make([]string, len(attachArgv))
	for i, a := range attachArgv {
		quoted[i] = appleScriptQuote(a)
	}
	cmdline := strings.Join(quoted, " ")
	doScript := "tell application \"Terminal\" to do script " + strconv.Quote(cmdline)
	activate := "tell application \"Terminal\" to activate"
	out, err := exec.Command("osascript", "-e", doScript, "-e", activate).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %v: %s", err, truncateBytes(string(out), 200))
	}
	return nil
}
