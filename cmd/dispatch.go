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
//     实际弹窗行为由 Task 12 的弹终端块驱动
package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/projectid"
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
)

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

// dispatchCmd 派发一个计划任务到 agentd 执行。
//
// 使用方式：handoff dispatch [--project <名字>] [--prompt ...] [--executor x] [--model m]
// [--branch b | --new-branch b] [--base t] [--worktree w | --new-worktree]
// [--no-terminal] [plan 文件]
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
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
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
		task, err := client.New(addr, token).Dispatch(cmd.Context(), client.DispatchOpts{
			ProjectID: projectID, ProjectName: dispatchProject,
			PlanB64: planB64, PlanName: planName, Target: targetName,
			Prompt: dispatchPrompt, Name: dispatchName,
			Executor: dispatchExecutor, Model: dispatchModel,
			Branch: dispatchBranch, NewBranch: dispatchNewBranch, Base: dispatchBase,
			Worktree: dispatchWorktree, NewWorktree: dispatchNewWorktree,
			BaseCommit: baseCommit,
		})
		if err != nil {
			return err
		}
		// 基线摘要走 stderr：stdout 是「单行任务 JSON」的既有契约，上层脚本
		// 按行解析，多打一行就会把它们全部打断。为什么必须打：B35 的现场里
		// 分支开在了三批改动之前，而这件事在任何输出里都不留痕迹——审核者
		// 甚至反过来怀疑是执行者找错了目录
		if task.BaseCommit != "" {
			line := "基线 " + shortSHA(task.BaseCommit)
			if task.BaseAhead > 0 {
				line += fmt.Sprintf("（任务仓库 HEAD 领先 %d 个提交，新分支不含它们）", task.BaseAhead)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), line)
		}
		// B43：新工作树不含执行机仓库里未提交的改动，而审核者看不到那台机器的
		// 工作区——不说，executor 就会在一份没有那些改动的代码上开工而无人知晓。
		// 与基线行同走 stderr（stdout 的单行任务 JSON 契约不能破，见上方注释）
		if task.RepoDirtyCount > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"提示: 执行机仓库有 %d 处未提交改动，新工作树不含它们：%s\n",
				task.RepoDirtyCount, task.RepoDirtyFiles)
		}
		b, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("序列化任务: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		// 弹终端块的入口（Task 12 实现）：--no-terminal 在此使能判定
		dispatchAfterTerminal(cmd, task.ID)
		return nil
	},
}

func init() {
	dispatchCmd.Flags().StringVar(&dispatchProject, "project", "",
		"跨项目派发时指定项目名（省略则由当前目录自动识别；用 handoff project ls 查看有哪些）")
	dispatchCmd.Flags().StringVar(&dispatchPrompt, "prompt", "", "直接指令（prompt-only 派发；与 plan 文件至少其一）")
	dispatchCmd.Flags().StringVar(&dispatchName, "name", "", "任务展示名（默认从 plan 文件名或 prompt 派生）")
	dispatchCmd.Flags().StringVar(&dispatchExecutor, "executor", "", "执行者名（如 opencode/grok/fake；空=agentd 缺省执行者）")
	dispatchCmd.Flags().StringVar(&dispatchModel, "model", "", "任务级模型覆盖（空=执行者自身默认）")
	dispatchCmd.Flags().StringVar(&dispatchBranch, "branch", "", "切到已存在分支（与 --new-branch 互斥）")
	dispatchCmd.Flags().StringVar(&dispatchNewBranch, "new-branch", "", "新建分支名（空且 --branch 空=自动 handoff/<id8>）")
	dispatchCmd.Flags().StringVar(&dispatchBase, "base", "", "新分支起点 commit/分支（与 --branch 互斥；空=取派发时的基线起点）")
	dispatchCmd.Flags().StringVar(&dispatchWorktree, "worktree", "", "用户自带 worktree 路径（与 --new-worktree 互斥）")
	dispatchCmd.Flags().BoolVar(&dispatchNewWorktree, "new-worktree", false, "在 DataDir/worktrees 下新建 managed worktree（任务完成时自动删除）")
	dispatchCmd.Flags().BoolVar(&dispatchNoTerminal, "no-terminal", false, "派发成功后不弹终端实况（默认弹，受配置 terminal.auto 控制）")
	dispatchCmd.Flags().BoolVar(&dispatchNoSyncCheck, "no-sync-check", false,
		"跳过远程仓库基线校验（cwd 与目标项目不是同一个仓库时用）")
	dispatchCmd.Flags().BoolVar(&dispatchAllowDirty, "allow-dirty", false,
		"本地工作区有未提交的已跟踪改动时仍照常派发（executor 看不到这些改动）")
	dispatchCmd.MarkFlagsMutuallyExclusive("branch", "new-branch")
	dispatchCmd.MarkFlagsMutuallyExclusive("worktree", "new-worktree")
	rootCmd.AddCommand(dispatchCmd)
}

// dispatchAfterTerminal 是 dispatch 成功后「弹终端实况」的钩子。
//
// 行为契约：
//   - --no-terminal 或 cfg.Terminal.Auto==false 或非 darwin → 打印提示行
//     「实况: handoff attach <id>」（远程含 --target），不弹窗
//   - darwin 且允许 → osascript 弹 Terminal.app 执行 attach 命令
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
		fmt.Fprintln(cmd.OutOrStdout(), hint)
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
		fmt.Fprintln(cmd.OutOrStdout(), hint)
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
// dispatch 成功时审核者大概率不在本机终端前（在 agentd 所在机器的桌面前），
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
