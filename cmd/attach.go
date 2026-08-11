// 本文件实现 handoff attach 子命令：在终端跟随任务实况。
//
// 职责：
//   - 从 agentd 的 render 流式接口取任务实况，原样打印到 stdout，Ctrl+C 退出
//
// 边界：
//   - 不解析实况内容：render.log 是模型回合文本原样增量，这里只做搬运
//   - 不连 executor、不碰任务目录：一切经 agentd 的 HTTP 接口
//
// 为什么不再 exec 外部命令：旧实现用 syscall.Exec 换进程进 tmux（本机），
// 或 ssh -t <host> tmux attach（远程）。tmux 拆除后实况改由 agentd 落盘 +
// 流式吐出，attach 退化成一个普通 HTTP 客户端——顺带拿到三个收益：
// 远程不再需要 ssh（复用 agentd 连接与鉴权，配置里的 user 字段对 attach 不再必要）、
// Windows 审核者可用（syscall.Exec 在 Windows 上直接返回 EWINDOWS）、
// 断线可凭已收字节数续传。
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
)

// attachAll 表示从头播放全部实况（--all）；默认只从尾部回溯 attachDefaultTail。
var attachAll bool

// attachNoFollow 表示放完当前内容即退出（--no-follow），不等待后续增量。
var attachNoFollow bool

// attachDefaultTail 是默认回溯字节数：跟上实况又不至于把历史全刷一遍。
const attachDefaultTail = 4 << 10

// attachCmd 进入任务 executor 的终端实况（有参）或展示任务选择列表（无参）。
var attachCmd = &cobra.Command{
	Use:   "attach [task]",
	Short: "进入任务 executor 的终端实况（无参时选择任务）",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		cli := client.New(addr, token)
		if len(args) == 1 {
			// 有 task：连上任务的 render 实况流
			return runAttach(cmd, cli, args[0])
		}
		// 无 task：任务选择列表
		return pickAttachTask(cmd, cli)
	},
}

// sshHostFromTarget 把配置 target 换算成 ssh 目标（pull 用的远程 git 地址换算点）。
//
// 规则：取 Addr 冒号前段（Addr 形如 devbox:7777，ssh 目标是主机名，不含 agentd
// 端口）；User 非空时返回 user@host，否则只返回 host（与历史行为一致）。
//
// 为什么只服务 pull：attach 已改为走 agentd 的 render 流（复用 agentd 连接与
// 鉴权），不再拼 ssh；只有 pull 仍需要把 Targets[target] 换算成 ssh 目标来做
// git-over-ssh 同步。user 字段让 ssh 用户名可配置——本机用户名与远程不一致
// （如本机 xushixin 连远端 sycm）时，裸 host 的 ssh 会直接 Permission denied。
func sshHostFromTarget(t config.Target) string {
	host := t.Addr
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if t.User != "" {
		return t.User + "@" + host
	}
	return host
}

// runAttach 连上任务的实况流并打印到 stdout，直到流结束或用户 Ctrl+C。
//
// 参数：
//   - cli: 已按 target 解析好 endpoint 的客户端
//   - taskID: 目标任务
//
// 返回：
//   - 连接失败（任务不存在、鉴权失败）时返回错误；用户主动中断（ctx 取消）返回 nil
//
// 注意：
//   - target 解析沿用既有规则（显式 --target → 任务自身记录的 target → 本机），
//     但换算结果只用于选 agentd endpoint，不再用于拼 ssh 命令
func runAttach(cmd *cobra.Command, cli *client.Client, taskID string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background() // 裸 cobra 命令（测试）Context() 返回 nil
	}
	tail := int64(attachDefaultTail)
	if attachAll {
		tail = 0
	}
	follow := !attachNoFollow
	rc, size, err := cli.RenderStream(ctx, taskID, 0, tail, follow)
	if err != nil {
		return err
	}
	defer rc.Close()
	slog.Debug("attach 实况流已连接", "task", taskID, "size", size, "follow", follow)
	// 原样搬运：实况是给人看的文本，不做任何加工
	n, err := io.Copy(cmd.OutOrStdout(), rc)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("attach 实况流中断", "task", taskID, "printed", n, "cause", err)
		return fmt.Errorf("读实况流: %w", err)
	}
	slog.Debug("attach 实况流结束", "task", taskID, "printed", n)
	return nil
}

// pickAttachTask 无参 attach：列出任务并让用户选择进入实况。
//
// 非 TTY（stdin 非字符设备，如脚本/管道调用）打印每行建议命令后退出 0，
// 不进交互——无人值守场景给出可复制的命令即可。
func pickAttachTask(cmd *cobra.Command, cli *client.Client) error {
	tasks, err := cli.ListTasks(cmd.Context())
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "暂无任务")
		return nil
	}
	// 可动作状态（running/waiting_answer/waiting_review）在前，组内 created_at 降序
	sort.SliceStable(tasks, func(i, j int) bool {
		pi, pj := attachPriority(tasks[i].State), attachPriority(tasks[j].State)
		if pi != pj {
			return pi < pj
		}
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})

	if !isTTY() {
		// 非 TTY：打印建议命令即可，不阻塞读输入
		printAttachSuggestions(cmd.OutOrStdout(), tasks)
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "序号  name                              executor  状态            更新时间")
	for i, t := range tasks {
		name := t.Name
		if name == "" {
			name = id8(t.ID)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%2d   %-32s %-10s %-16s %s\n",
			i+1, truncateName(name), t.Executor, t.State, t.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprint(cmd.OutOrStdout(), "选择序号: ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return fmt.Errorf("读取选择: %w", err)
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &n); err != nil || n < 1 || n > len(tasks) {
		return fmt.Errorf("序号 %q 非法（范围 1-%d）", strings.TrimSpace(line), len(tasks))
	}
	return runAttach(cmd, cli, tasks[n-1].ID)
}

// printAttachSuggestions 打印每个任务的 attach 建议命令（非 TTY 降级路径）。
//
// 远程任务必须带 --target：不带的话命令会打到本机 agentd，先 404、再 attach
// 一个本机不存在的实况——两条错都指不到「你少了个 --target」这个真原因。
func printAttachSuggestions(w io.Writer, tasks []proto.TaskView) {
	for _, t := range tasks {
		line := "handoff attach " + t.ID
		if t.Target != "" {
			line += " --target " + t.Target
		}
		fmt.Fprintln(w, line)
	}
}

// attachPriority 返回任务状态在列表中的优先级（越小越靠前）：
// 可动作状态（running/waiting_answer/waiting_review）在前，终态与挂起外的
// 状态靠后——attach 是为了「看活着的执行者」，优先把能进的会话排前面。
func attachPriority(st proto.TaskState) int {
	switch st {
	case proto.TaskStateRunning:
		return 0
	case proto.TaskStateWaitingAnswer:
		return 1
	case proto.TaskStateWaitingReview:
		return 2
	default:
		return 3
	}
}

// id8 取字符串前 8 个字符（不足 8 个则原样返回），用于展示。
// 与 wait.go 的同名函数共用同一规则（都是「展示短 id」），实现留在 wait.go。

// truncateName 按 rune 截断展示名到 32 字符（列宽内）。
func truncateName(s string) string {
	r := []rune(s)
	if len(r) > 32 {
		return string(r[:32])
	}
	return s
}

// isTTY 判定 stdin 是否为真终端。
//
// 非 TTY（管道/脚本调用）时 attach 不阻塞读输入，改为打印建议命令——
// 无人值守场景给可复制的命令即可，交互选择框对脚本无意义。
//
// 为什么用 go-isatty 而非 os.ModeCharDevice（修复 5）：字符设备≠终端——/dev/null
// 正是字符设备，旧实现会把它误判成 TTY，导致脚本按标准做法 handoff attach
// < /dev/null 走进交互分支、打完表格再报「读取选择」错误——非 TTY 降级路径在最该
// 生效的场景里失效。go-isatty 走 ioctl 查终端属性（TIOCGETA），/dev/null 无终端
// 语义 → false；管道、重定向文件同样返回 false。
func isTTY() bool {
	return isatty.IsTerminal(os.Stdin.Fd())
}

func init() {
	attachCmd.Flags().BoolVar(&attachAll, "all", false, "从头播放全部实况（默认只回溯末尾 4KB）")
	attachCmd.Flags().BoolVar(&attachNoFollow, "no-follow", false, "放完当前内容即退出，不等待后续增量")
	rootCmd.AddCommand(attachCmd)
}
