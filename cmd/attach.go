// 本文件实现 handoff attach 子命令：终端实况入口（人类命令）。
//
// 职责：
//   - attach <task>：组装并执行 attach 命令进入 executor 的 tmux 会话实况
//     （本机 tmux attach -t handoff-<id8>；远程经 ssh -t <host> tmux attach）
//   - attach（无参）：任务选择列表（交互）或非 TTY 下的建议命令打印（远程任务
//     的建议命令带 --target）
//
// 边界：
//   - 终端实况是「看着执行者干活」的入口，不输出快照——快照恢复走 handoff show
//   - 不改任务状态：attach 只进入 tmux 会话，不触发任何状态机动作
//   - fake executor 无 tmux 会话，attach 失败属预期（tmux 报找不到会话）
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
)

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
			// 有 task：组装并执行 attach 命令（exec 替换进程，让 tmux 拿到真 TTY）
			return runAttach(cmd, cli, args[0])
		}
		// 无 task：任务选择列表
		return pickAttachTask(cmd, cli)
	},
}

// attachCommandFor 组装 attach 命令的 argv。
//
// 参数：
//   - taskID: 任务 ID（tmux 会话名取前 8 位：handoff-<id8>）
//   - target: --target 目标名；空=本机（tmux 直接 attach）
//   - cfg: 配置（target 换算 ssh host）
//
// 返回：
//   - argv: exec 可直接执行的参数列表
//   - err: target 未在配置中定义时报错
//
// 注意：
//   - id8 截断规则与 opencode adapter 的 tmux 会话命名（handoff-<id8>）耦合，
//     改一处必改两处（proc.go StartServe 的 session 命名）
//   - 远程 ssh 目标经 sshHostFromTarget 换算：取 Addr 冒号前段，user 非空时带
//     user@ 前缀（Addr 形如 devbox:7777，ssh 目标是主机名，不含 agentd 端口）
func attachCommandFor(taskID, target string, cfg *config.Config) (argv []string, err error) {
	id8 := taskID
	if len(id8) > 8 {
		id8 = id8[:8]
	}
	session := "handoff-" + id8
	if target == "" {
		return []string{"tmux", "attach", "-t", session}, nil
	}
	t, ok := cfg.Targets[target]
	if !ok {
		return nil, fmt.Errorf("target %q 未在配置中定义", target)
	}
	return []string{"ssh", "-t", sshHostFromTarget(t), "tmux", "attach", "-t", session}, nil
}

// sshHostFromTarget 把配置 target 换算成 ssh 目标（attach/pull 共用的唯一换算点）。
//
// 规则：取 Addr 冒号前段（Addr 形如 devbox:7777，ssh 目标是主机名，不含 agentd
// 端口）；User 非空时返回 user@host，否则只返回 host（与历史行为一致）。
//
// 为什么必须抽共用函数：attach（远程实况）与 pull（远程分支同步）都需要把
// Targets[target] 换算成 ssh 目标，各自实现会因只改一处而行为漂移。user 字段让
// ssh 用户名可配置——本机用户名与远程不一致（如本机 xushixin 连远端 sycm）时，
// 裸 host 的 ssh 会直接 Permission denied。
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

// execveFn 是 syscall.Exec 的测试缝：验证「execve 第一参必须是 LookPath 解析
// 出的绝对路径」时替换它记录实参（P0-1 回归覆盖）。
var execveFn = syscall.Exec

// runAttach 执行 attach 命令进入终端实况。
//
// 为什么用 syscall.Exec：tmux attach 需要真正的 TTY，而 exec.Command 会维持
// 本进程的 stdio 转发——对需要终端控制语义的 tmux 不充分；Exec 用 attach 命令
// 替换当前进程，fd 原样继承，tmux 拿到完整终端控制。
//
// 为什么第一参必须是 LookPath 的解析结果：syscall.Exec 是 execve(2) 直接封装，
// 不做 PATH 查找，传裸名 "tmux"/"ssh" 会得到 "no such file or directory"；
// argv[0] 保持裸名（execve 约定：argv[0] 是程序名，路径由第一参指定）。
func runAttach(cmd *cobra.Command, cli *client.Client, taskID string) error {
	cfg := loadCLIConfig()
	target := targetName
	if target == "" && cli != nil {
		// 未显式 --target 时回退任务自身记录的 target（P2-7）：远程任务派发时
		// 已把目标主机名写进 task.Target，用户忘带 --target 不该去连本机不存在的
		// tmux 会话；取不到任务/无 target 时保持空（退回本机，tmux 报找不到会话
		// 即提示用户补 --target）
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background() // 裸 cobra 命令（测试）Context() 返回 nil
		}
		if info, err := cli.Attach(ctx, taskID); err == nil && info.Task.Target != "" {
			target = info.Task.Target
		}
	}
	argv, err := attachCommandFor(taskID, target, cfg)
	if err != nil {
		return err
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("%s 未安装（%v），无法进入终端实况", argv[0], err)
	}
	// exec 前打印将执行的完整命令（spec §7 错误处理项）：用户可复制重试，
	// 且 exec 后进程被替换、任何输出都来自 tmux 本体
	fmt.Fprintln(cmd.OutOrStdout(), strings.Join(argv, " "))
	if err := execveFn(bin, argv, os.Environ()); err != nil {
		return fmt.Errorf("执行 %s: %w", argv[0], err)
	}
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
// 一个本机不存在的 tmux 会话——两条错都指不到「你少了个 --target」这个真原因。
func printAttachSuggestions(w io.Writer, tasks []proto.Task) {
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

// truncateName 按 rune 截断展示名到 32 字符（列宽内）。
func truncateName(s string) string {
	r := []rune(s)
	if len(r) > 32 {
		return string(r[:32])
	}
	return s
}

// isTTY 判定 stdin 是否为字符设备（真终端）。
//
// 非 TTY（管道/脚本调用）时 attach 不阻塞读输入，改为打印建议命令——
// 无人值守场景给可复制的命令即可，交互选择框对脚本无意义。
func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// loadCLIConfig 加载 CLI 侧配置（attach 需要 Targets 换算 ssh host）。
// 配置加载失败时返回空配置：attachCommandFor 对无 target 的本机路径不依赖
// Targets，空配置即可工作；加载失败的错误由 TargetEndpoint 在更早处暴露。
func loadCLIConfig() *config.Config {
	p := configPath
	if p == "" {
		p = config.DefaultPath()
	}
	cfg, err := config.Load(p)
	if err != nil {
		return &config.Config{}
	}
	return cfg
}

func init() {
	rootCmd.AddCommand(attachCmd)
}
