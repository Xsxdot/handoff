// driver.go —— RunTurn 的 run 形态驱动：把一回合描述翻译成一次
// `<CLI> run --format json [--] <prompt>` 子进程，流式解析 JSONL 事件行，
// 汇出会话 id 与回合文本。
//
// 职责：
//   - CLI 名单门禁（岔口一裁决 A：仅 opencode；名单外报含名+含「未实装」的
//     可行动错误，绝不静默兜底到 opencode）
//   - argv/env 组装：model 透传、`-s` 即 resume、`--` 防横杠开头消息被当
//     flag（b156.3.1-plan §三 F6）、隔离 HOME 确定性注入（HomeDir 字段是 HOME 的
//     语义所有者，赢过 req.Env 里的同名行）
//   - 超时执法：到点杀整棵进程树（unix 进程组 / 其余平台直接子进程），
//     回合判失败不挂死
//   - JSONL 解析：sessionID 取首个非空值；text part 按到达序拼接为 Output
//
// 边界：
//   - 不认识派发状态机的任何概念（task/waiting_review/verdict），铁律同包注释
//   - 零持久化：会话身份由 CLI 自己的存储承担（隔离 HOME 内），resume 连续性
//     靠 `run -s`（plan §三 F2），不靠本层记忆
//   - 只 import 标准库：不经 prochost shim（shim 的 detached 语义与 TaskID/
//     MarkRoot 归属判定是执行任务形状，协调者回合借用会把伪 task 凭据泄进
//     归属子系统）；孤儿风险由 Timeout 上界兜底（breakdown 缺陷族 1 明文
//     接受该形态）。也因此不需要 import opencode 包——run 形态只用它的
//     命令行契约，不用它任何代码
package hostapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultTurnTimeout 是 TurnRequest.Timeout 为 0 时的缺省回合上界。数值不是
// 本票定的：hostapi.go 的 Timeout 字段注释自骨架期（契约提交落库的骨架文件）
// 起即写明「0 = 用包内缺省（30 分钟）」，本常量是它的执行半边，照办不另立。
// 契约 §7 只冻签名——数值的出处是骨架源码注释而非契约文本，两者不混称。
const DefaultTurnTimeout = 30 * time.Minute

// supportedCLIs 是本门面实装的载体 CLI 名单（岔口一裁决 A）。键按
// filepath.Base(req.CLI) 匹配：绝对路径形式的载体同样命中。名单扩张属于
// 契约变更，不在实现票权限内。
var supportedCLIs = map[string]bool{"opencode": true}

// log 返回包日志入口（跟随 agentd 的 slog 配置，先例 prochost.go:142）。
func log() *slog.Logger { return slog.Default().With("mod", "hostapi") }

// runTurn 是 RunTurn 的本体；hostapi.go 只留薄委托（冻结面文件零逻辑）。
func runTurn(ctx context.Context, req TurnRequest) (TurnReply, error) {
	if !supportedCLIs[filepath.Base(req.CLI)] {
		log().Warn("拒绝未实装的载体 CLI", "cli", req.CLI)
		return TurnReply{}, fmt.Errorf(
			"hostapi: 载体 CLI %q 未实装（本门面当前仅支持 opencode，B156.3 岔口一裁决 A）；不静默兜底",
			req.CLI)
	}
	if req.Prompt == "" && req.SessionID == "" {
		// keysclient.Runner.Launch 注释（2026-08-26 协调者改写版）承诺「prompt
		// 必须非空、实现方对空 prompt 必须响报」；run 形态也没有无消息建会话的
		// 形态（plan §三 F5：空消息 exit 1）。本守卫就是那条注释的执行半边。
		log().Warn("拒绝空 prompt 的新建回合", "cli", req.CLI)
		return TurnReply{}, fmt.Errorf(
			"hostapi: 新建会话需要非空 prompt（载体 CLI %q 的 run 形态必须有消息）；出现此错误说明调用方接线有误",
			req.CLI)
	}
	return driveTurn(ctx, req)
}

// ocEvent 是 `run --format json` 事件行的最小解析形状。字段名对齐上游实测
// 抓取（plan §三 F1/F2），不是猜的；未知字段由 json.Unmarshal 默认忽略，
// 上游加字段不炸解析。
type ocEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
}

// driveTurn 是一回合的进程编排主体（门禁通过后由此接力）。
func driveTurn(ctx context.Context, req TurnRequest) (TurnReply, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTurnTimeout
	}
	bin, err := lookPathWithFallback(req.CLI)
	if err != nil {
		log().Error("找不到载体 CLI", "cli", req.CLI, "cause", err)
		return TurnReply{}, fmt.Errorf("hostapi: 找不到载体 CLI %q: %w", req.CLI, err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.Command(bin)
	// argv 整体赋值而非把 bin 再塞进参数表：exec.Command(bin) 已把 bin 记为
	// argv[0]，再拼一次会让真 opencode 收到 [bin, bin, run, …]，根命令吞掉
	// binpath 后不再进入 run 子命令（本节点用真二进制 `run --help` 实证过，
	// 见台账探针 P0）。
	cmd.Args = buildArgv(bin, req)
	cmd.Dir = req.Workdir // 空 = 继承 agentd cwd；生产调用方的 Workdir 非空语义由组装点（K4，澄清 2）保证
	cmd.Env = buildEnv(req)
	configureProcess(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return TurnReply{}, fmt.Errorf("hostapi: 建 stdout 管道: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	log().Info("协调者回合开始", "cli", req.CLI, "mode", resumeOrNew(req),
		"home_dir", req.HomeDir, "workdir", req.Workdir, "timeout", timeout.String(),
		"prompt_bytes", len(req.Prompt)) // 提示词与环境变量值永不进日志，只记长度
	started := time.Now()
	if err := cmd.Start(); err != nil {
		log().Error("拉起载体 CLI 失败", "cli", req.CLI, "cause", err)
		return TurnReply{}, fmt.Errorf("hostapi: 拉起 %q: %w", req.CLI, err)
	}
	watchDone := make(chan struct{})
	go watchDeadline(ctx, cmd.Process, watchDone)

	reply, parseErr := consumeEvents(stdout)
	waitErr := cmd.Wait()
	close(watchDone)
	dur := time.Since(started)

	if ctx.Err() != nil {
		log().Warn("协调者回合超时终止", "cli", req.CLI,
			"timeout", timeout.String(), "duration", dur.String())
		return TurnReply{}, fmt.Errorf("hostapi: 回合超时（上界 %v），已终止进程树: %w",
			timeout, ctx.Err())
	}
	if waitErr != nil {
		tail := tailBytes(&stderr, 4096)
		log().Warn("载体 CLI 回合失败", "cli", req.CLI, "duration", dur.String(),
			"stderr_tail_bytes", len(tail))
		// stderr 尾部进错误消息：resume 打错 id 时 keystone 兜底链需要看到
		// 「Session not found」（plan §三 F3）才能正确降级重建。
		return TurnReply{}, fmt.Errorf("hostapi: 回合失败（%v）stderr 尾部: %s",
			waitErr, tail)
	}
	if parseErr != nil {
		log().Warn("回合输出解析异常", "cli", req.CLI, "cause", parseErr)
		return TurnReply{}, fmt.Errorf("hostapi: 回合输出解析失败: %w", parseErr)
	}
	if reply.SessionID == "" {
		// exit 0 却没拿到会话 id：上游输出形态可能升级，如实响报，
		// 不用空 id 假装成功（静默失败族）。
		log().Warn("回合成功但事件流未携带 sessionID", "cli", req.CLI)
		return TurnReply{}, fmt.Errorf(
			"hostapi: 回合成功但事件流未携带 sessionID（载体 CLI %q 输出形态可能升级，需核对 plan §三 F1 抓取形状）",
			req.CLI)
	}
	log().Info("协调者回合完成", "cli", req.CLI, "session_id", reply.SessionID,
		"output_bytes", len(reply.Output), "duration", dur.String())
	return reply, nil
}

// resumeOrNew 返回回合模式的日志用词。
func resumeOrNew(req TurnRequest) string {
	if req.SessionID == "" {
		return "new"
	}
	return "resume"
}

// watchDeadline 在 ctx 到点时杀掉整棵进程树；done 关闭后安静退出。杀失败只
// 记日志：Wait 的返回错误已足够让回合判失败，这里不再叠加二次错误。
func watchDeadline(ctx context.Context, p *os.Process, done <-chan struct{}) {
	select {
	case <-ctx.Done():
		if err := killGroup(p); err != nil {
			log().Warn("超时终止进程组失败", "pid", p.Pid, "cause", err)
		}
	case <-done:
	}
}

// buildArgv 组装 run 形态完整 argv（含 argv[0]=bin，供 exec.Args 整体赋值）。
// `--` 终止 flag 解析（plan §三 F6）：提示词内容不可信，横杠开头的消息不能被
// 当成未知 flag 吞掉。
func buildArgv(bin string, req TurnRequest) []string {
	args := []string{bin, "run", "--format", "json"}
	if req.Model != "" {
		args = append(args, "-m", req.Model)
	}
	if req.SessionID != "" {
		args = append(args, "-s", req.SessionID)
	}
	return append(args, "--", req.Prompt)
}

// buildEnv 合成子进程环境：基础环境剔除被覆盖键 → 按 req.Env 原序追加 →
// HomeDir 非空时覆写 HOME（spec §4.3：隔离 HOME 是物种边界的环境执法；
// HomeDir 字段赢过 env 清单里的同名行——env 文件不该有能力偷换物种边界）。
// 值永不进日志。
func buildEnv(req TurnRequest) []string {
	override := map[string]bool{}
	for _, kv := range req.Env {
		if k, _, ok := strings.Cut(kv, "="); ok && k != "" {
			override[k] = true
		}
	}
	homeSet := req.HomeDir != ""
	if homeSet {
		override["HOME"] = true
	}
	out := make([]string, 0, len(os.Environ())+len(req.Env)+1)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if override[k] {
			continue
		}
		out = append(out, kv)
	}
	for _, kv := range req.Env {
		if homeSet {
			if k, _, _ := strings.Cut(kv, "="); k == "HOME" {
				continue
			}
		}
		out = append(out, kv)
	}
	if homeSet {
		out = append(out, "HOME="+req.HomeDir)
	}
	return out
}

// consumeEvents 流式消费 JSONL 事件行直到 EOF：sessionID 取首个非空值，
// type=text 且 part.type=text 的文本按到达序以换行拼接为回合输出。Output
// 允许为空（工具型回合可能没有 text part），sessionID 不允许（driveTurn 执法）。
// 非 JSON 行（杂散告警等）跳过并记 Debug，不让脏行炸掉整条流。
func consumeEvents(r io.Reader) (TurnReply, error) {
	var reply TurnReply
	var texts []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // 单行上限 8MB：长回合的 text part 可以很大
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev ocEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			log().Debug("跳过非 JSON 事件行", "bytes", len(line))
			continue
		}
		if reply.SessionID == "" && ev.SessionID != "" {
			reply.SessionID = ev.SessionID
		}
		if ev.Type == "text" && ev.Part.Type == "text" {
			texts = append(texts, ev.Part.Text)
		}
	}
	if err := sc.Err(); err != nil {
		return TurnReply{}, fmt.Errorf("读事件流: %w", err)
	}
	reply.Output = strings.Join(texts, "\n")
	return reply, nil
}

// lookPathWithFallback 先走 PATH，失败时尝试常见安装位，避免 agentd 的最小 PATH
// 导致已安装的 opencode 找不到（mac-02 实测：~/.opencode/bin 不在 launchd PATH）。
func lookPathWithFallback(cli string) (string, error) {
	if bin, err := exec.LookPath(cli); err == nil {
		return bin, nil
	} else {
		// 若本身就是路径（带 /），不再兜底。
		if cli != filepath.Base(cli) {
			return "", err
		}
		// 仅对已支持的 CLI 兜底，未实装的直接返回原错，不静默试其它二进制。
		if !supportedCLIs[filepath.Base(cli)] {
			return "", err
		}
		candidates := make([]string, 0, 4)
		if home, herr := os.UserHomeDir(); herr == nil && home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".opencode", "bin", cli),
				filepath.Join(home, ".local", "bin", cli),
			)
		}
		candidates = append(candidates,
			"/opt/homebrew/bin/"+cli,
			"/usr/local/bin/"+cli,
		)
		for _, cand := range candidates {
			if _, serr := os.Stat(cand); serr == nil {
				return cand, nil
			}
		}
		return "", err
	}
}

// tailBytes 返回缓冲区末尾至多 n 字节；按字节切可能切开 UTF-8，只进错误消息
// 供人读，可接受。
func tailBytes(b *bytes.Buffer, n int) string {
	if b.Len() <= n {
		return b.String()
	}
	return string(b.Bytes()[b.Len()-n:])
}
