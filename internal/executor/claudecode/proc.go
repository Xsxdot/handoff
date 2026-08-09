// proc.go —— Claude Code 进程生命周期管理。
//
// 职责：
//   - 在 tmux 会话 handoff-<id8> 内拉起 claude（headless 双向流式），第二窗口 tail render.log
//   - 经命名管道 in.fifo 投递指令；stdout 经 tee 落 out.jsonl，stderr 落 claude.log
//   - 死亡判定（out.jsonl 末尾的 handoff_exit 哨兵）与凭据持久化（claude.json）
//
// 边界：
//   - 不解析事件：out.jsonl 的解析在 stream.go
//   - 不做权限裁决：socket 服务端在 perm.go
//
// 为什么进程放 tmux：agentd 重启或崩溃时子进程树会被一并回收，正在执行的任务
// 会无辜中断；tmux server 独立守护，session 生命周期与 agentd 解耦——这与
// opencode adapter 的取舍完全一致，也让 handoff attach 一套命令覆盖两个 executor。
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/xushixin/handoff/internal/shellq"
)

// 任务目录内的执行器物料文件名（目录本身 0700，见 manager 创建处）。
const (
	runScriptFileName = "run_claude.sh" // 0600 启动脚本
	fifoFileName      = "in.fifo"       // Send 投递 stream-json user message
	outFileName       = "out.jsonl"     // claude stdout 原样落盘（adapter 按 offset 续读）
	stderrFileName    = "claude.log"    // claude stderr，启动失败/死亡诊断来源
	renderFileName    = "render.log"    // 模型回合文本增量（tmux 第二窗口 tail -f 目标）
	procInfoFileName  = "claude.json"   // 恢复凭据：tmux 会话 / session_id / offset
	sockFileName      = "perm.sock"     // 权限裁决 socket
)

// startReadyTimeout 是等待 system/init 的上限。取 30s 而非 opencode 的 10s：
// claude 冷启动要加载 settings/plugins/MCP 子进程，10s 会造成假阴性。
const startReadyTimeout = 30 * time.Second

// StartProcReq 是一次 StartProc 的完整入参。
//
// 字段说明：
//   - Env 来自 executor.StartReq.Env（manager 按任务执行者从 env 文件解析），
//     已解析已展开。**不读它会静默失效**：漏传编译照过、用户配的代理/密钥在
//     脚本里根本不出现——见 Global Constraints 与 proc_test 的钉子测试
type StartProcReq struct {
	RepoPath     string
	TaskID       string
	TaskDir      string
	SessionID    string
	Model        string
	SettingsPath string
	MCPPath      string
	Env          []string
}

// Proc 描述一个运行中的 claude 进程句柄。
//
// 字段说明：
//   - TmuxSession: tmux 会话名（handoff-<taskID 前 8 字符>），用户可 attach 旁观
//   - TaskDir: 任务目录（fifo/out.jsonl/claude.log 都在其中）
//   - SessionID: claude --session-id（agentd 生成，写进 claude.json 供恢复）
type Proc struct {
	TmuxSession string
	TaskDir     string
	SessionID   string
}

// StartProc 备物料、起 tmux 会话与渲染窗口，返回进程句柄。
//
// 参数：
//   - ctx: 仅用于 tmux 启动阶段的可取消；执行生命周期延续到 Kill
//   - req: 进程完整入参（见 StartProcReq）
//   - log: 本模块日志入口（进程启动点，日志需要显式传入而非走默认）
//
// 返回：
//   - 已拉起的进程句柄；任一阶段失败返回错误（此时无残留 tmux 会话）
//
// 注意：
//   - 就绪判定（等 system/init）由 adapter 在 stream 层完成，本函数只负责拉起
//   - in.fifo 必须先于 tmux 存在：启动脚本第一件事是 exec 3<> in.fifo，
//     fifo 缺失会让整条 claude 命令失败
func StartProc(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error) {
	session := "handoff-" + id8(req.TaskID)
	// env 注入（B19）：只打 key 名不打值——值里可能带凭据（如 http://user:pass@host）
	if len(req.Env) > 0 {
		keys := make([]string, 0, len(req.Env))
		for _, kv := range req.Env {
			k, _, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			keys = append(keys, k)
		}
		log.Info("注入 env 变量到 claude 进程", "session", session, "keys", keys, "count", len(keys))
	}
	scriptPath, err := writeRunScript(req.TaskDir, req)
	if err != nil {
		log.Error("写 claude 启动脚本失败", "session", session, "cause", err)
		return nil, err
	}
	if err := (&Proc{TaskDir: req.TaskDir}).ensureFIFO(); err != nil {
		log.Error("创建 in.fifo 失败", "session", session, "cause", err)
		return nil, err
	}
	log.Info("启动 claude 执行者", "session", session, "script", scriptPath, "repo", req.RepoPath)
	out, err := exec.CommandContext(ctx, "tmux", tmuxArgs(session, req.RepoPath, scriptPath)...).CombinedOutput()
	if err != nil {
		log.Error("tmux 启动 claude 失败", "session", session,
			"stderr_tail", tail(string(out), 500), "cause", err)
		return nil, fmt.Errorf("tmux 启动 %s: %w", session, err)
	}
	// 第二窗口 tail -f render.log（模型文本实况）；失败只 Warn 不阻断，见该函数注释
	startRenderTailWindow(session, req.TaskDir, log)

	p := &Proc{TmuxSession: session, TaskDir: req.TaskDir, SessionID: req.SessionID}
	// 恢复凭据落盘（claude.json）：agentd 重启后 Resume 凭它探活与续读
	if err := writeProcInfo(req.TaskDir, &procInfo{TmuxSession: session, SessionID: req.SessionID}); err != nil {
		log.Warn("写 claude 恢复凭据失败，重启恢复将不可用", "task", req.TaskID, "cause", err)
	}
	return p, nil
}

// writeRunScript 生成 0600 启动脚本，返回其路径。
//
// why（脚本化而非 tmux 内联）：tmux 客户端进程的 argv 全局可读，参数里带路径
// 与模型名不算秘密，但保持与 opencode 同一形态便于两边一起演进；更实际的原因
// 是 fifo 的 exec 3<> 与末行哨兵都必须在 shell 里表达，内联不了。
//
// why（claude 一行不用 exec）：exec 会让 sh 被 claude 替换掉，末行的 handoff_exit
// 哨兵永远不会执行——而它是本 adapter 唯一可靠的死亡信号（tmux has-session 不可用，
// 因为窗口 1 的 tail -f 会一直撑着会话）。
//
// why（env 行排在最前、值单引号包裹）：排在 claude 命令之前进程才拿得到；
// 值必须单引号——Go 侧已展开过一次，不加引号会被 shell 二次展开，含 $ 的值
// 会悄悄变成别的东西。与 opencode writeServeScript 同构，见 Global Constraints。
func writeRunScript(taskDir string, req StartProcReq) (string, error) {
	var args strings.Builder
	args.WriteString("claude -p")
	args.WriteString(" --input-format stream-json --output-format stream-json --verbose")
	args.WriteString(" --include-partial-messages")
	if req.Model != "" {
		args.WriteString(" --model " + req.Model)
	}
	args.WriteString(" --session-id " + req.SessionID)
	args.WriteString(" --setting-sources user,project")
	args.WriteString(" --settings " + shellq.Quote(req.SettingsPath))
	args.WriteString(" --mcp-config " + shellq.Quote(req.MCPPath))
	args.WriteString(" --permission-prompt-tool mcp__handoff__ask")

	// env 注入行：形如 KEY=VALUE，值单引号包裹；不含 = 的畸形条目跳过，
	// 绝不能拼出语法错误的行把整个脚本毁掉
	var envLines strings.Builder
	for _, kv := range req.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		envLines.WriteString("export " + k + "=" + shellq.Quote(v) + "\n")
	}

	script := fmt.Sprintf(`#!/bin/sh
# 由 agentd 生成：Claude Code 执行者启动脚本（0600）。勿手动修改。
exec 2>> %s
%sexec 3<> %s
%s <&3 | tee -a %s
printf '{"type":"handoff_exit","code":%%d}\n' "$?" >> %s
`,
		shellq.Quote(filepath.Join(taskDir, stderrFileName)), envLines.String(),
		shellq.Quote(filepath.Join(taskDir, fifoFileName)), args.String(),
		shellq.Quote(filepath.Join(taskDir, outFileName)),
		shellq.Quote(filepath.Join(taskDir, outFileName)))
	scriptPath := filepath.Join(taskDir, runScriptFileName)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return "", fmt.Errorf("写 claude 启动脚本 %s: %w", scriptPath, err)
	}
	return scriptPath, nil
}

// tmuxArgs 组装启动 claude 的 tmux new-session 参数。
func tmuxArgs(session, repoPath, scriptPath string) []string {
	return []string{
		"new-session", "-d", "-s", session, "-c", repoPath,
		"sh " + shellq.Quote(scriptPath),
	}
}

// startRenderTailWindow 在会话内开第二窗口 `tail -f <taskDir>/render.log`：
// 模型回合文本实况。先 touch render.log 再开窗口（tail -f 对不存在的文件会立即
// 报错退出）；失败只 Warn 不阻断——增强型可见性，不值得为它挂掉任务启动。
func startRenderTailWindow(session, taskDir string, log *slog.Logger) {
	renderLogPath := filepath.Join(taskDir, renderFileName)
	f, err := os.OpenFile(renderLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Warn("创建 render.log 失败，tmux 第二窗口不可用", "session", session, "cause", err)
		return
	}
	f.Close()
	if err := exec.Command("tmux", "new-window", "-t", session,
		"tail -f "+shellq.Quote(renderLogPath)).Run(); err != nil {
		log.Warn("tmux 第二窗口启动失败（tail render.log 不可用），不影响主流程",
			"session", session, "cause", err)
	}
}

// ensureFIFO 幂等创建 in.fifo（已存在且是管道则复用）。
func (p *Proc) ensureFIFO() error {
	path := filepath.Join(p.TaskDir, fifoFileName)
	err := syscall.Mkfifo(path, 0o600)
	if err == nil {
		return nil
	}
	if !os.IsExist(err) {
		return fmt.Errorf("mkfifo %s: %w", path, err)
	}
	// 已存在：校验类型，是管道才复用（残留普通文件会让 exec 3<> 失败）
	fi, serr := os.Stat(path)
	if serr != nil {
		return fmt.Errorf("stat %s: %w", path, serr)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		return fmt.Errorf("%s 已存在但不是命名管道", path)
	}
	return nil
}

// WriteInput 往 in.fifo 投递一条 stream-json user message。
//
// 参数：
//   - text: 指令原文，原样透传不加工（executor 契约要求）
//
// 注意：
//   - 打开 fifo 写端会阻塞直到有读端；启动脚本的 exec 3<> 已永久持有读端，
//     因此这里不会阻塞。若脚本已死，O_NONBLOCK 打开会立刻失败，正是我们要的
//     「进程不在」信号（调用方包装 executor.ErrTaskNotRunning）
func (p *Proc) WriteInput(text string) error {
	path := filepath.Join(p.TaskDir, fifoFileName)
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("打开 %s（进程可能已不在）: %w", path, err)
	}
	defer f.Close()
	line, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": text,
		},
	})
	if err != nil {
		return fmt.Errorf("序列化 user message: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// sentinelPrefix 是死亡哨兵行的类型标记（脚本末行 printf 写出）。
const sentinelPrefix = `"type":"handoff_exit"`

// procExited 检查 out.jsonl 是否出现死亡哨兵。
//
// 返回：
//   - exited: 是否已退出；code: 退出码（exited=false 时无意义）
//
// why（从文件读而非记内存）：agentd 重启后内存态全丢，而哨兵落在文件里——
// 重读同样能发现死亡，这是 Resume 判存活的第一条判据。
func procExited(outJSONLPath string) (exited bool, code int) {
	f, err := os.Open(outJSONLPath)
	if err != nil {
		return false, 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false, 0
	}
	// 反向扫文件尾部：哨兵由脚本在进程退出后追加，必然在末尾；文件可能很大，
	// 只读尾部 8KB 足够覆盖（任何一行的长度都不会超过它）
	const scanBytes = 8 << 10
	offset := fi.Size() - scanBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, fi.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return false, 0
	}
	for _, line := range strings.Split(string(buf), "\n") {
		if !strings.Contains(line, sentinelPrefix) {
			continue
		}
		var s struct {
			Code int `json:"code"`
		}
		_ = json.Unmarshal([]byte(line), &s)
		return true, s.Code
	}
	return false, 0
}

// Kill 销毁 tmux 会话（连同其内的 claude）。
//
// 幂等：会话已不存在（已被外部清理）时返回 nil。
func (p *Proc) Kill() error {
	if p == nil || p.TmuxSession == "" {
		return nil
	}
	err := exec.Command("tmux", "kill-session", "-t", p.TmuxSession).Run()
	if err != nil {
		// 会话不存在视为已清理，不报错；其余错误（权限、tmux 未运行）如实上报
		if exec.Command("tmux", "has-session", "-t", p.TmuxSession).Run() != nil {
			return nil
		}
		return fmt.Errorf("kill tmux session %s: %w", p.TmuxSession, err)
	}
	return nil
}

// procInfo 是 claude 进程恢复凭据的持久化形态，agentd 重启后凭它探活与续读。
type procInfo struct {
	TmuxSession string `json:"tmux_session"`
	SessionID   string `json:"session_id"`
	Offset      int64  `json:"offset"` // out.jsonl 已消费的字节 offset（stream 层维护）
}

// writeProcInfo 把恢复凭据写入任务目录 claude.json（0600）。
//
// why（必须持久化）：agentd 重启后内存中的 Proc 丢失，而 tmux 内的 claude 进程
// 独立存活；Resume 凭此文件探活、续读 offset 并重建事件流。写失败不阻断启动
// （adapter 只 Warn），缺失时重启按「执行器已不在」转 failed——保守胜于静默丢事件。
func writeProcInfo(taskDir string, pi *procInfo) error {
	b, err := json.Marshal(pi)
	if err != nil {
		return fmt.Errorf("序列化 claude 恢复凭据: %w", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, procInfoFileName), b, 0o600); err != nil {
		return fmt.Errorf("写 claude 恢复凭据 %s: %w", procInfoFileName, err)
	}
	return nil
}

// readProcInfo 读取任务目录的 claude 恢复凭据。
//
// 返回：
//   - 文件缺失/损坏/字段不完整时返回错误（调用方据此判「无法恢复」）
func readProcInfo(taskDir string) (*procInfo, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, procInfoFileName))
	if err != nil {
		return nil, fmt.Errorf("读 claude 恢复凭据 %s: %w", procInfoFileName, err)
	}
	var pi procInfo
	if err := json.Unmarshal(b, &pi); err != nil {
		return nil, fmt.Errorf("解析 claude 恢复凭据 %s: %w", procInfoFileName, err)
	}
	if pi.TmuxSession == "" || pi.SessionID == "" {
		return nil, fmt.Errorf("claude 恢复凭据 %s 字段不完整", procInfoFileName)
	}
	return &pi, nil
}

// id8 取字符串前 8 个字符（不足 8 个则原样返回），用于 tmux 会话名。
func id8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// tail 返回字符串尾部最多 n 个字符（按字节截断，日志用）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
