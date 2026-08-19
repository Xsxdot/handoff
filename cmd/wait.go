// 本文件实现 handoff wait 子命令：阻塞等待任务的下一个可动作事件并输出单行 JSON。
//
// 职责：
//   - 调用 client.WaitEvent（progress 不唤醒、断线自动退避重连、cursor 续拉），
//     事件到达时把完整事件 JSON 单行输出到 stdout（供上层脚本解析）
//   - --notify：事件到达时发 macOS 系统通知（spec §7 风险#4 的兜底：协调者会话
//     不在时提醒其重新拉起），失败仅 Warn 不影响主流程
//   - 收到 SIGINT（Ctrl+C）时由进程默认行为终止，WaitEvent 随 ctx 取消退出
//   - 任务结束事件到达时自动同步远程任务分支到本地（输出走 stderr，不污染
//     stdout 的事件 JSON 契约）
//   - --follow：持续订阅同一任务的事件流，每条事件单行输出，直到任务终结
//     （failed 事件或被 done 归档）。此模式下 --timeout 的语义是**空闲**上限
//     ——距上一次收到任何帧（含被过滤掉的 progress）的时长，且跨重连累计
//   - --follow 每次建连前先对账：本机 cursor 之后有积压时吐**一行** backlog_summary
//     （带 missed/stale/actionable），把 cursor 推到当前水位，积压事件不再逐条重放
//     ——stdout 每行是一次会话唤醒，逐条重放会把一次重连变成 N 次唤醒
//   - --until-done：B67 依赖门闩。--timeout 在此时是**总时限**，progress 等任何
//     中间帧都不能续命；成功只输出一行 archived，等待/失败/超时 stdout 全空
//
// 边界：
//   - 不做事件语义判断与审批（审批在协调者脑中），事件原样输出
//   - 不覆盖「协调者会话被关闭」：Monitor 是会话级的，会话没了订阅就没了，
//     本命令给不出任何补救（spec §7.2 明确接受的边界）
//   - --until-done 不读写审核者 cursor、不自动派发/回答后续任务：它只是
//     「真实 archived 到达」的唤醒门闩，不是审核流程的替代品
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/logx"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// notifyFlag 为 true 时事件到达同时发 macOS 系统通知（spec §7 风险#4 的兜底）。
var notifyFlag bool

// followFlag 为 true 时持续订阅：事件逐行流出，不在首个事件后退出。
//
// 为什么需要它：一次性 wait 的「一事件一退出」让每两个事件之间必然存在一段
// 无人订阅的真空，而「回合结束后记得重挂」是要每轮重做的人工动作——漏一次
// 就是永久断链（08-11 实撞：f7d07ece 的 wait 退出后空转 7 小时 43 分）。
var followFlag bool

// waitNoSync 关闭「任务结束后自动同步远程任务分支到本地」。
var waitNoSync bool

// waitUntilDone 开启 B67 依赖门闩：静默忽略中间事件，只等真实 archived。
//
// 为什么它与 --follow 互斥：follow 的契约是把 question/permission/completed 等
// 中间事件逐条交付给审核者；until-done 的契约是全部吞掉只等终态 archived——
// 两种消费模型相反，组合起来连语义都说不清。
var waitUntilDone bool

// waitTimeout 为 0 表示不设上限；大于 0 时等待超过该时长返回错误退出非 0。
//
// 为什么需要它：wait 的正常形态是无限阻塞（断线自动退避重连），但无人值守时
// 配置错误（token 未同步）或打错 task-id 曾表现为无限挂起（P0-2 已修复为立即
// 报错）；--timeout 是最后一道防线，到点以非 0 退出，与「事件到达退出 0」可区分，
// 脚本侧据此判断是继续等还是告警。
var waitTimeout time.Duration

// waitCmd 阻塞等待指定任务的下一个可动作事件。
//
// 使用方式：handoff wait <task> —— 事件到达打印 {"seq":..,"type":..,"payload":..} 退出 0；
// 加 --notify 时同时发 macOS 系统通知；加 --timeout <时长>（如 1h）时到点报错退出非 0；
// 加 --until-done 时静默等待真实 archived，成功才输出一行。
var waitCmd = &cobra.Command{
	Use:   "wait <task>",
	Short: "阻塞等待任务的下一个可动作事件（question/permission_request 等）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		// 负时长必须报错而不是当「不设上限」：--timeout 的用途正是无人值守时
		// 的最后一道防线，把 -5s 静默当成「永远等下去」，等于在最需要兜底的
		// 场景把兜底悄悄关掉
		if waitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", waitTimeout)
		}
		// 冲突校验必须在任何网络请求之前：两种模式的 stdout 契约完全相反
		// （逐事件交付 vs 静默只等终态），提前报错比发出请求后失败更好诊断
		if followFlag && waitUntilDone {
			return fmt.Errorf("--follow 与 --until-done 不能同时使用：前者交付审核事件，后者只等归档")
		}
		addr, _, err := TargetEndpoint()
		if err != nil {
			return err
		}
		cli, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		// 统一日志格式：wait 是长驻命令，stderr 日志是「为什么没唤醒」的唯一线索
		slog.SetDefault(logx.Setup("cli", ""))
		if waitUntilDone {
			return runUntilDone(cmd, taskID, addr, cli)
		}
		if followFlag {
			return runFollow(cmd, taskID, addr, cli)
		}
		// ——以下一次性路径与改动前完全一致——
		ctx := cmd.Context()
		if waitTimeout > 0 {
			// 到点 ctx 触发 DeadlineExceeded：WaitEvent 返回 ctx.Err()，
			// 下方转成带时长的明确报错（区别于事件到达的正常返回）
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, waitTimeout)
			defer cancel()
		}

		ev, err := cli.WaitEvent(ctx, taskID, false)
		if err != nil {
			if waitTimeout > 0 && errors.Is(err, context.DeadlineExceeded) {
				slog.Error("wait 超时未等到事件", "task", taskID, "timeout", waitTimeout.String())
				// 专属退出码：无人值守场景只看得到退出码，「等满了时限」必须
				// 与「配置/鉴权失败」区分开（前者继续等，后者要立刻告警）
				return &exitCodeError{code: ExitTimeout,
					err: fmt.Errorf("wait 超时（%s）未等到事件", waitTimeout)}
			}
			return err
		}
		if notifyFlag {
			notifyEvent(ev)
		}
		if err := writeEventLine(cmd.OutOrStdout(), ev); err != nil {
			return err
		}
		// 任务结束后把远程任务分支拉到本地（B12）。
		// 为什么输出走 stderr：wait 的 stdout 是「单行事件 JSON」的契约，
		// 上层脚本按行解析——往 stdout 多打一行同步说明会直接打断它们
		autoSyncAfterWait(cmd, cli, addr, ev)
		return nil
	},
}

// runUntilDone 实现 B67 依赖门闩：静默等待真实 archived，成功只输出一行事件。
//
// 参数：
//   - taskID: 完整 UUID
//   - waitTimeout: 总时限。它被套在 ctx 上，任何中间帧/重连都不能重置——
//     到点即 124，与「事件到达退出 0」可区分
//
// 返回：
//   - nil: 已归档，stdout 是原始 archived JSON（唯一写 stdout 的路径）
//   - exitCodeError(ExitTimeout): 总时限到，任务尚未归档
//   - 其他: 依赖 failed / 鉴权 / 任务不存在 / 协议异常（退出 1）
//
// 边界：不写审核者 cursor、不自动派发后续任务；等待期间 stdout 恒为空。
func runUntilDone(cmd *cobra.Command, taskID, addr string, cli *client.Client) error {
	ctx := cmd.Context()
	if waitTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, waitTimeout)
		defer cancel()
	}
	slog.Debug("等待依赖任务归档", "task", taskID, "addr", addr,
		"timeout", waitTimeout.String())
	ev, err := cli.WaitArchived(ctx, taskID)
	if err != nil {
		switch {
		case waitTimeout > 0 && errors.Is(err, context.DeadlineExceeded):
			slog.Error("等待依赖任务归档超时",
				"task", taskID, "timeout", waitTimeout.String(), "cause", err)
			return &exitCodeError{code: ExitTimeout, err: fmt.Errorf(
				"等待任务 %s 归档超时（%s）", taskID, waitTimeout)}
		case errors.Is(err, client.ErrDependencyFailed):
			slog.Error("依赖任务失败，归档门闩终止", "task", taskID, "cause", err)
			return fmt.Errorf("依赖任务 %s 已失败: %w", taskID, err)
		case errors.Is(err, client.ErrArchivedEventMissing):
			slog.Error("任务已完成但缺归档事件", "task", taskID, "cause", err)
			return fmt.Errorf("任务 %s 已归档但缺 archived 事件；请升级对端 agentd 或检查事件数据: %w",
				taskID, err)
		default:
			slog.Error("等待依赖任务归档失败", "task", taskID, "cause", err)
			return err
		}
	}
	if notifyFlag {
		notifyEvent(ev)
	}
	if err := writeEventLine(cmd.OutOrStdout(), ev); err != nil {
		slog.Error("输出归档事件失败", "task", taskID, "seq", ev.Seq, "cause", err)
		return err
	}
	slog.Debug("依赖任务已归档", "task", taskID, "seq", ev.Seq)
	return nil
}

// writeEventLine 把事件作为**严格一行** JSON 写出。
//
// 为什么统一走它而不是各处 Fprintln：wait 的 stdout 是「每行一个 JSON 对象」的
// 机器契约，写出失败（管道断裂等）必须上抛——原来一次性 wait 的 Fprintln 把
// 错误吞掉，脚本侧拿到的行是残缺的，却没人知道。
func writeEventLine(w io.Writer, ev *proto.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("序列化事件: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(b)); err != nil {
		return fmt.Errorf("写出事件: %w", err)
	}
	return nil
}

// runFollow 持续订阅任务事件流，每条事件单行输出到 stdout，直到任务终结。
//
// 参数：
//   - taskID: 完整 UUID（agentd 精确匹配，不做前缀补全）
//
// 返回：
//   - nil: 任务终结（failed 或已归档），退出 0
//   - ExitTimeout 的 exitCodeError: 空闲超过 --timeout
//   - 其他错误: 鉴权失败 / 任务不存在 / 连接永久失败
//
// 注意：
//   - stdout 严格是「每事件一行 JSON」，任何人读信息一律走 stderr——上层
//     （Monitor）按行解析，多打一行说明就会打断它
func runFollow(cmd *cobra.Command, taskID, addr string, cli *client.Client) error {
	// 异步核对 --timeout 与对端 stalltimeout：status 要逐个探活，最坏 10 秒，
	// 不能让一句告警把开始跟随这件事拖后
	go warnIfTimeoutBelowStall(cmd.Context(), cli, waitTimeout)

	err := cli.FollowEvents(cmd.Context(), taskID, false, waitTimeout,
		func(ev *proto.Event) error {
			if notifyFlag {
				notifyEvent(ev)
			}
			if err := writeEventLine(cmd.OutOrStdout(), ev); err != nil {
				return err
			}
			// 每次遇到回合结束都同步一次：--follow 下一个任务会有多个 completed
			autoSyncAfterWait(cmd, cli, addr, ev)
			return nil
		},
		func(sum *client.BacklogSummary) error {
			if notifyFlag {
				notifyBacklog(sum)
			}
			return writeBacklogLine(cmd.OutOrStdout(), sum)
		})
	switch {
	case err == nil:
		slog.Info("follow 正常结束：任务已终结", "task", taskID)
		return nil
	case errors.Is(err, client.ErrIdleTimeout):
		slog.Error("follow 空闲超时：期间未收到任何帧（含 progress）",
			"task", taskID, "timeout", waitTimeout.String())
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf(
			"follow 空闲超时（%s）：期间一帧都没收到。agentd 的 stalled 本应先到，"+
				"先跑 handoff show 看任务状态，再怀疑 agentd 是否失联", waitTimeout)}
	default:
		return err
	}
}

// warnIfTimeoutBelowStall 核对本次 --timeout 是否会抢在 agentd 的 stalled 前面，
// 是则打一条 WARN。
//
// 注意：
//   - 全部失败路径静默（Debug）：这是锦上添花的提醒，取不到对端状态不该影响跟随
//   - 单独设 15 秒时限：status 端要逐个探活，不能挂在这里
func warnIfTimeoutBelowStall(ctx context.Context, cli *client.Client, idle time.Duration) {
	if idle <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st, err := cli.Status(ctx)
	if err != nil {
		slog.Debug("取不到对端 stalltimeout，跳过 --timeout 核对", "cause", err)
		return
	}
	stall, err := time.ParseDuration(st.StallTimeout)
	if err != nil {
		slog.Debug("对端 stalltimeout 无法解析，跳过核对", "raw", st.StallTimeout, "cause", err)
		return
	}
	if msg := idleTimeoutWarning(idle, stall); msg != "" {
		slog.Warn(msg)
	}
}

// idleTimeoutWarning 判断 follow 的空闲超时是否会盖过 agentd 的停滞诊断。
//
// 参数：
//   - idle: 本次 --timeout；<=0 表示不设上限
//   - stall: 对端 agentd 的 stalltimeout；<=0 表示未知
//
// 返回：
//   - 需要告警时返回完整告警文案，否则返回空串
//
// 为什么「相等」也要告警：两个计时器同时到点时，客户端的 124 会抢在 agentd 的
// stalled 事件前面退出进程——而 stalled 是带着 last_seq 与 idle 时长的诊断，
// 124 只是一句「我没收到东西」。让前者盖住后者，等于主动把信息量调低。
func idleTimeoutWarning(idle, stall time.Duration) string {
	if idle <= 0 || stall <= 0 || idle > stall {
		return ""
	}
	return fmt.Sprintf(
		"--timeout %s 不大于对端 stalltimeout %s：两者同时到点时空闲超时会抢在 "+
			"agentd 的 stalled 诊断前面退出，把一次已诊断的任务停滞降级成一次连接超时。"+
			"建议设为大于 %s（如 %s）", idle, stall, stall, stall+time.Hour)
}

// autoSyncAfterWait 在任务进入等待审核的事件（completed/turn_failed）或任务
// 终结（failed）到达后，把远程任务分支同步到本地仓库。
//
// 参数：
//   - ev: 刚返回的事件；只有 completed/turn_failed/failed 触发（回合中途的
//     permission/question/progress 不触发——那时活还没干完）
//
// 注意：
//   - 全部失败路径只打印到 stderr、绝不改变 wait 的退出码：wait 的唯一职责是
//     唤醒协调者，把同步做成阻塞条件等于让「ssh 临时不通」变成「收不到完成通知」
//   - 失败（含回合失败）也同步：失败恰恰是最需要把代码拉到本地翻的时候
func autoSyncAfterWait(cmd *cobra.Command, cli *client.Client, addr string, ev *proto.Event) {
	if waitNoSync || ev == nil {
		return
	}
	if !shouldAutoSync(ev.Type) {
		return
	}
	if !loadCLIConfig().Sync.Auto {
		slog.Debug("配置 sync.auto=false，跳过自动同步", "task", ev.TaskID)
		return
	}
	info, err := cli.Attach(cmd.Context(), ev.TaskID)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "自动同步跳过：读取任务失败:", err)
		return
	}
	res, err := syncTaskBranch(cmd.Context(), &info.Task.Task)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "自动同步跳过:", err)
		return
	}
	fmt.Fprintln(cmd.ErrOrStderr(), syncMessage(res))
}

// shouldAutoSync 判断这类事件要不要触发自动同步。
//
// 为什么 turn_failed 也要：任务此刻在 waiting_review，协调者马上就要 diff 审代码，
// 而失败恰恰是最需要把代码拉到本地翻的时候。
func shouldAutoSync(t proto.EventType) bool {
	return t == proto.EventTypeCompleted || t == proto.EventTypeFailed || t == proto.EventTypeTurnFailed
}

// notifyEvent 发 macOS 系统通知提醒协调者事件已到达（--notify 的兜底实现）。
//
// 参数：
//   - ev: WaitEvent 返回的事件（client 返回指针，nil 由调用方保证不会传入）
//
// 注意：
//   - 仅 darwin 生效：其他平台静默跳过（Debug 记录），避免误报「发送失败」
//   - osascript 失败只打 Warn、绝不影响 wait 主流程：通知是锦上添花的兜底，
//     stdout 的 JSON 与 stderr 日志才是事件送达的主通道
func notifyEvent(ev *proto.Event) {
	if runtime.GOOS != "darwin" {
		slog.Debug("非 macOS，--notify 忽略", "task", ev.TaskID, "type", ev.Type)
		return
	}
	msg := "任务 " + id8(ev.TaskID) + ": " + string(ev.Type)
	// strconv.Quote 把文本安全地转成 AppleScript 字符串字面量（转义引号/反斜杠）。
	// 事件类型与任务 id 均为 ASCII，不存在 AppleScript 不认的转义序列
	script := "display notification " + strconv.Quote(msg) + " with title " + strconv.Quote("handoff")
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		slog.Warn("发送系统通知失败", "cause", err, "output", truncateBytes(string(out), 200))
	}
}

// writeBacklogLine 把积压摘要作为**一行** JSON 写出。
//
// 参数：
//   - w: 目标（生产环境是 cmd.OutOrStdout()）
//   - sum: 对账结果
//
// 返回：
//   - 序列化失败时返回错误——写不出摘要就等于协调者不知道自己错过了什么，
//     必须让 follow 停下而不是继续跑一个没人看得见的循环
//
// 注意：严格一行。stdout 是「每行一个 JSON 对象」的契约，上层（Monitor）按行
// 解析，每一行都是一次会话唤醒——多打一行就多叫醒一次，这正是本功能要消灭的东西。
func writeBacklogLine(w io.Writer, sum *client.BacklogSummary) error {
	b, err := json.Marshal(sum)
	if err != nil {
		return fmt.Errorf("序列化积压摘要: %w", err)
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// notifyBacklog 为积压摘要发一条系统通知（--notify）。
//
// 为什么摘要也要通知：它正是「你离开期间发生了事」的那一次唤醒信号。漏掉它
// 等于把 --notify 在最需要它的场景（断网回来、补挂）悄悄关掉。
func notifyBacklog(sum *client.BacklogSummary) {
	if runtime.GOOS != "darwin" {
		slog.Debug("非 macOS，--notify 忽略", "task", sum.TaskID, "type", sum.Type)
		return
	}
	msg := fmt.Sprintf("任务 %s: 错过 %d 条，待处置 %d 张",
		id8(sum.TaskID), sum.Missed, len(sum.Actionable))
	script := "display notification " + strconv.Quote(msg) +
		" with title " + strconv.Quote("handoff")
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		slog.Warn("发送系统通知失败", "cause", err, "output", truncateBytes(string(out), 200))
	}
}

// id8 取字符串前 8 个字符（通知文案用）。
func id8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// truncateBytes 将字符串截断为最多 n 个字节（osascript 输出截断，日志用）。
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func init() {
	waitCmd.Flags().BoolVar(&notifyFlag, "notify", false, "事件到达时发 macOS 系统通知（spec §7 兜底）")
	waitCmd.Flags().BoolVar(&waitNoSync, "no-sync", false,
		"任务结束（completed/failed）时不自动同步远程任务分支到本地")
	waitCmd.Flags().BoolVar(&followFlag, "follow", false,
		"持续订阅：每条事件单行输出，任务终结（failed/归档）才退出")
	waitCmd.Flags().BoolVar(&waitUntilDone, "until-done", false,
		"静默等待 handoff done；成功只输出 archived，failed 立即失败")
	waitCmd.Flags().DurationVar(&waitTimeout, "timeout", 0,
		"超时（如 3h）；默认不设上限。一次性模式=等不到事件的总时长上限，"+
			"--follow 模式=空闲上限（期间一帧都没收到，含 progress），"+
			"--until-done 模式=总时限（任何帧都不能续命），到点退出非 0")
	rootCmd.AddCommand(waitCmd)
}
