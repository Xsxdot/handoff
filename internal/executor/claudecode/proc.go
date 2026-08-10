// proc.go —— Claude Code 进程生命周期管理。
//
// 职责：
//   - 组 claude 的 argv（headless 双向流式），经 prochost 以 detached 方式拉起
//   - 经命名管道 in.fifo 投递指令；stdout 落 out.jsonl，stderr 落 claude.log
//   - 死亡判定（out.jsonl 末尾的 handoff_exit 哨兵 + 存活锁）与凭据持久化（proc.json）
//
// 边界：
//   - 不解析事件：out.jsonl 的解析在 stream.go
//   - 不做权限裁决：socket 服务端在 perm.go
//   - 不关心进程怎么脱离 agentd：那是 prochost 的事
//
// 为什么进程经 prochost 而不是 agentd 直接 fork：agentd 重启或崩溃时子进程
// 若未脱离会话会被一并回收，正在执行的任务无辜中断；prochost 的 shim 以新会话
// 拉起并持有存活锁，生命周期与 agentd 解耦——agentd 重启后靠 Alive() 探测重连。
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

	"github.com/xushixin/handoff/internal/prochost"
)

// 任务目录内的执行器物料文件名（目录本身 0700，见 manager 创建处）。
const (
	fifoFileName     = "in.fifo"    // Send 投递 stream-json user message
	outFileName      = "out.jsonl"  // claude stdout 原样落盘（adapter 按 offset 续读）
	stderrFileName   = "claude.log" // claude stderr，启动失败/死亡诊断来源
	renderFileName   = "render.log" // 模型回合文本增量（render 流式 endpoint 的数据源）
	procInfoFileName = "proc.json"  // 恢复凭据：prochost.Handle / session_id / offset
	lockFileName     = "proc.lock"  // shim 存活锁（prochost.Alive 的唯一判据）
	sockFileName     = "perm.sock"  // 权限裁决 socket
)

// startReadyTimeout 是等待 system/init 的上限。
//
// 语义（2026-08-09 真机 e2e 实测修正）：claude 在**收到首条输入后**才会吐
// system/init，因此 Start 先投 prompt 再等 init（顺序不可对调，见 adapter.go
// Start 的 why）。本超时等的是「prompt 已投出、claude 仍未进入会话」——鉴权失效、
// settings 非法、MCP 起不来都会卡在这一步。取 30s：claude 冷启动要加载
// settings/plugins/MCP 子进程，opencode 的 10s 会造成假阴性。
const startReadyTimeout = 30 * time.Second

// fifoReaderTimeout 是等待 shim 在 in.fifo 上建立读端的上限。
//
// why（沿用 tmux 时代真机 e2e 次生缺陷的教训）：Start 返回只代表 shim 已被
// fork，**不代表 shim 已打开 in.fifo**；而 WriteInput 以 O_WRONLY|O_NONBLOCK
// 打开 fifo，POSIX 规定读端未就绪时 open 直接失败（errno ENXIO，macOS 文案
// "device not configured"）。8fca917 把「投 prompt」提前到进程拉起之后，
// 写入紧跟在返回之后，撞上读端未开的竞态。shim 是 Go 进程，打开 fifo 比 tmux
// 起 sh 更可预期，但机器负载高时仍会抖动；取 5s 与 startReadyTimeout 同一量级。
//
// 用 var 而非 const：测试需把它调到毫秒量级来快速演练超时路径（见
// start_ordering_test 的 TestStartProcKillsSessionWhenFIFOReaderNeverReady）。
var fifoReaderTimeout = 5 * time.Second

// StartProcReq 是一次 StartProc 的完整入参。
//
// 字段说明：
//   - Env 来自 executor.StartReq.Env（manager 按任务执行者从 env 文件解析），
//     已解析已展开。**不读它会静默失效**：漏传编译照过、用户配的代理/密钥在
//     shim 里根本不出现——见 Global Constraints 与 proc_test 的钉子测试
//   - Resume=true 时启动命令用 --resume（载入既有会话）而非 --session-id
//     （建一个这个 id 的新会话）。两者语义相反，写错的表现是「日志说恢复成功、
//     模型却什么都不记得」
type StartProcReq struct {
	RepoPath     string
	TaskID       string
	TaskDir      string
	SessionID    string
	Model        string
	SettingsPath string
	MCPPath      string
	Env          []string
	Resume       bool
}

// Proc 描述一个运行中的 claude 进程句柄。
//
// 字段说明：
//   - Handle: prochost 句柄（shim pid + 存活锁路径），存活与回收都靠它
//   - TaskDir: 任务目录（fifo/out.jsonl/claude.log/proc.json 都在其中）
//   - SessionID: claude --session-id（agentd 生成，写进 proc.json 供恢复）
type Proc struct {
	Handle    prochost.Handle
	TaskDir   string
	SessionID string
}

// startProcHost 是 prochost.Start 的测试缝（替代旧的 tmuxLaunch 缝）：
// 测试替换它断言 spec 内容与写前置时序，绕开真实 shim 与 claude 二进制。
var startProcHost = prochost.Start

// startProc 是 StartProc 的测试缝：冷恢复测试替换它断言「起进程」是否被调用、
// 注入错误，绕开真实 shim + claude 二进制。
var startProc = StartProc

// killProcHost 是 prochost.Kill 的测试缝：SIGKILL 在类 Unix 上不可拦截，
// 真进程做不出「杀不死」的形态，回收失败路径只能靠替换它来驱动。
var killProcHost = prochost.Kill

// StartProc 备物料、经 prochost 拉起 shim 承载 claude，返回进程句柄。
//
// 参数：
//   - ctx: 保留以与 adapter 契约一致（当前未参与拉起，启动是子秒级操作）；
//     执行生命周期延续到 Kill
//   - req: 进程完整入参（见 StartProcReq）
//   - log: 本模块日志入口（进程启动点，日志需要显式传入而非走默认）
//
// 返回：
//   - 已拉起的进程句柄；任一阶段失败返回错误（此时无残留进程）
//
// 注意：
//   - 就绪判定（等 system/init）由 adapter 在 stream 层完成，本函数只负责拉起
//   - in.fifo 必须先于进程存在：shim 第一件事是 O_RDWR 打开它，
//     fifo 缺失会让整条 claude 命令失败
//   - startProcHost 返回后必须等 in.fifo 出现读者（WaitInputReader）才能返回：
//     Start 只代表 shim 已 fork，写端不等到读端会 ENXIO（见 fifoReaderTimeout 的 why）
//   - startProcHost 成功之后的一切失败路径必须自行 Kill 回收：调用方 rollback
//     依赖 r.proc，而 StartProc 失败时返回 nil（见 WaitInputReader 失败处）
func StartProc(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error) {
	l := log.With("task", req.TaskID)
	if len(req.Env) > 0 {
		// 只打 key 名不打值——值里可能带凭据（如 http://user:pass@host）
		l.Info("注入 env 变量到 claude 进程", "keys", envKeys(req.Env), "count", len(req.Env))
	}
	fifoPath := filepath.Join(req.TaskDir, fifoFileName)
	if err := prochost.CreateInputChannel(fifoPath); err != nil {
		l.Error("创建输入通道失败", "path", fifoPath, "cause", err)
		return nil, err
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		l.Error("claude 未安装", "cause", err)
		return nil, fmt.Errorf("claude 未安装: %w", err)
	}
	// 记绝对路径而不是只记「claude」：PATH 上同时装着多份 CLI 是常态
	// （nvm / homebrew / npm global 各一份），版本行为不一致时，只有这一行
	// 能回答「当时到底跑的是哪一个」。
	l.Info("解析 claude 可执行文件", "bin", bin)
	selfExe, err := os.Executable()
	if err != nil {
		l.Error("取 handoff 自身路径失败（shim 无法拉起）", "cause", err)
		return nil, fmt.Errorf("取自身可执行路径: %w", err)
	}
	argv := claudeArgv(req)
	argv[0] = bin // LookPath 解析结果：prochost 不做 PATH 查找

	lockPath := filepath.Join(req.TaskDir, lockFileName)
	infoPath := filepath.Join(req.TaskDir, procInfoFileName)
	// 写前置：proc.json 必须先于进程存在，否则 Start 成功而记录缺失时 Reap 无据可查。
	// Handle 此刻 PID 未知，先占位；Start 返回后补真实 pid
	if err := writeProcInfo(req.TaskDir, &procInfo{
		Handle: prochost.Handle{LockPath: lockPath}, SessionID: req.SessionID,
	}); err != nil {
		l.Error("写恢复凭据失败", "cause", err)
		return nil, err
	}
	spec := prochost.Spec{
		Argv: argv, Dir: req.RepoPath, Env: append(os.Environ(), req.Env...),
		Stdout:  filepath.Join(req.TaskDir, outFileName),
		Stderr:  filepath.Join(req.TaskDir, stderrFileName),
		InputCh: fifoPath, LockPath: lockPath, InfoPath: infoPath,
		Sentinel: true, // claude 没有 HTTP 探活面，哨兵是唯一可靠的死亡信号
	}
	l.Info("启动 claude 执行者", "bin", bin, "repo", req.RepoPath, "resume", req.Resume)
	handle, err := startProcHost(spec, selfExe)
	if err != nil {
		l.Error("拉起 claude 执行者失败", "cause", err)
		return nil, err
	}
	p := &Proc{Handle: handle, TaskDir: req.TaskDir, SessionID: req.SessionID}
	// 等 shim 在 in.fifo 上建立读端：Start 返回只代表 shim 已 fork，
	// 而 WriteInput 以 O_NONBLOCK 打开 fifo，读端未就绪会 ENXIO（见 prochost 的 why）
	elapsed, err := prochost.WaitInputReader(fifoPath, fifoReaderTimeout)
	if err != nil {
		l.Error("shim 未在时限内打开 in.fifo", "cause", err, "log_tail", claudeLogTail(req.TaskDir))
		// shim 已起来，必须自行回收：调用方 rollback 依赖 r.proc，而这里返回 nil
		if kerr := p.Kill(); kerr != nil {
			l.Warn("回收读端未就绪的执行者失败，可能需人工清理", "cause", kerr)
		}
		return nil, err
	}
	l.Debug("claude in.fifo 读端就绪", "wait", elapsed)
	// 补写真实 pid（写前置时只有 LockPath）
	if err := writeProcInfo(req.TaskDir, &procInfo{
		Handle: handle, SessionID: req.SessionID,
	}); err != nil {
		l.Warn("回写恢复凭据失败，重启恢复将不可用", "cause", err)
	}
	l.Info("claude 执行者已就位", "shim_pid", handle.PID)
	return p, nil
}

// claudeArgv 组 claude 的完整 argv。
//
// 为什么返回 argv 而不是命令串：argv 直接交给 execve，不经任何 shell——
// 旧实现把它拼进 sh 脚本，路径里的空格要引号、值里的 $ 会被二次展开，
// 这类问题在 argv 形态下从根上不存在。
//
// 注意：Resume=true 用 --resume（载入既有会话），false 用 --session-id
// （新建该 id 的会话）。两者语义相反，写错的表现是「日志说恢复成功、模型
// 却什么都不记得」——测试 TestClaudeArgvHasNoShell 钉死了这条。
func claudeArgv(req StartProcReq) []string {
	argv := []string{
		"claude", "-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose", "--include-partial-messages",
	}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if req.Resume {
		argv = append(argv, "--resume", req.SessionID)
	} else {
		argv = append(argv, "--session-id", req.SessionID)
	}
	argv = append(argv, "--setting-sources", "user,project")
	argv = append(argv, "--settings", req.SettingsPath)
	argv = append(argv, "--mcp-config", req.MCPPath)
	argv = append(argv, "--permission-prompt-tool", "mcp__handoff__ask")
	return argv
}

// WriteInput 往 in.fifo 投递一条 stream-json user message。
//
// 参数：
//   - text: 指令原文，原样透传不加工（executor 契约要求）
//
// 注意：
//   - 打开 fifo 写端会阻塞直到有读端；shim 的 O_RDWR 已永久持有读端，
//     因此这里不会阻塞。若 shim 已死，O_NONBLOCK 打开会立刻失败，正是我们要的
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
	// 反向扫文件尾部：哨兵由 shim 在进程退出后追加，必然在末尾；文件可能很大，
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
		if !strings.Contains(line, prochost.SentinelPrefix) {
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

// Kill 终止 claude 及其后代（按进程组），幂等。
func (p *Proc) Kill() error {
	if p == nil {
		return nil
	}
	return killProcHost(p.Handle)
}

// Alive 检查 claude 是否仍然存活：存活锁被持有 且 out.jsonl 无死亡哨兵。
//
// 为什么两条都要：锁只证明 shim 活着；claude 自己退出后 shim 会写哨兵再退出，
// 两者之间有一个极短窗口锁还在但 claude 已死，哨兵兜住它。
// （旧实现这里的第一条是 tmux has-session，而第二窗口的 tail -f 会一直吊着会话，
// 导致 claude 早死了会话还在——换成锁之后这个假存活来源被连根拔掉。）
func (p *Proc) Alive() bool {
	if p == nil || !prochost.Alive(p.Handle) {
		return false
	}
	exited, _ := procExited(filepath.Join(p.TaskDir, outFileName))
	return !exited
}

// procInfo 是恢复凭据的持久化形态，agentd 重启后凭它探活与续读。
//
// 注意：proc.json 只有本包一个写者——shim 记录的执行者 pid 落在同目录的
// child.pid，不进这个文件（why 见 prochost/shim.go 的 recordChildPID：
// 双写者之间会丢更新，代价是 Handle.PID 归零、Reap 假成功）。
type procInfo struct {
	Handle    prochost.Handle `json:"handle"`
	SessionID string          `json:"session_id"`
	Offset    int64           `json:"offset"`
}

// writeProcInfo 把恢复凭据写入任务目录 proc.json（0600）。
//
// why（必须持久化）：agentd 重启后内存中的 Proc 丢失，而 shim 内的 claude 进程
// 独立存活；Resume 凭此文件探活、续读 offset 并重建事件流。写失败不阻断启动
// （adapter 只 Warn），缺失时重启按「执行器已不在」转 failed——保守胜于静默丢事件。
func writeProcInfo(taskDir string, pi *procInfo) error {
	b, err := json.Marshal(pi)
	if err != nil {
		return fmt.Errorf("序列化恢复凭据: %w", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, procInfoFileName), b, 0o600); err != nil {
		return fmt.Errorf("写恢复凭据 %s: %w", procInfoFileName, err)
	}
	return nil
}

// readProcInfo 读取任务目录的恢复凭据。
//
// 返回：
//   - 文件缺失/损坏/字段不完整时返回错误（调用方据此判「无法恢复」）
func readProcInfo(taskDir string) (*procInfo, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, procInfoFileName))
	if err != nil {
		return nil, fmt.Errorf("读恢复凭据 %s: %w", procInfoFileName, err)
	}
	var pi procInfo
	if err := json.Unmarshal(b, &pi); err != nil {
		return nil, fmt.Errorf("解析恢复凭据 %s: %w", procInfoFileName, err)
	}
	if pi.Handle.LockPath == "" || pi.SessionID == "" {
		return nil, fmt.Errorf("恢复凭据 %s 字段不完整", procInfoFileName)
	}
	return &pi, nil
}

// envKeys 提取 KEY=VALUE 列表里的 key（日志用；值绝不出现在日志里）。
func envKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// tail 返回字符串尾部最多 n 个字符（按字节截断，日志用）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
