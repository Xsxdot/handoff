// Package agy 提供 agy (Antigravity CLI) 的 executor.Adapter 实现。
//
// 职责：
//   - 组 agy 的 argv（headless 双向 stream-json），经 prochost 以 detached 方式拉起
//   - 经命名管道 in.fifo 投递指令；stdout 落 out.jsonl，stderr 落 agy.log
//   - 死亡判定（out.jsonl 末尾的 handoff_exit 哨兵 + 存活锁）与凭据持久化（proc.json）
package agy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// 任务目录内的执行器物料文件名（目录本身 0700）。
const (
	fifoFileName     = "in.fifo"    // Send 投递 stream-json user message
	outFileName      = "out.jsonl"  // agy stdout 原样落盘（adapter 按 offset 续读）
	stderrFileName   = "agy.log"    // agy stderr，启动失败/死亡诊断来源
	renderFileName   = "render.log" // 模型回合文本增量（render 流式 endpoint 的数据源）
	procInfoFileName = "proc.json"  // 恢复凭据：prochost.Handle / session_id / offset
	lockFileName     = "proc.lock"  // shim 存活锁（prochost.Alive 的唯一判据）
)

// startReadyTimeout 是等待 init 就绪的上限。
const startReadyTimeout = 30 * time.Second

// fifoReaderTimeout 是等待 shim 在 in.fifo 上建立读端的上限。
var fifoReaderTimeout = 5 * time.Second

// StartProcReq 是一次 StartProc 的完整入参。
type StartProcReq struct {
	RepoPath  string
	TaskID    string
	TaskDir   string
	SessionID string
	Model     string
	Env       []string
	MarkRoot  string
	Resume    bool
}

// Proc 描述一个运行中的 agy 进程句柄。
type Proc struct {
	Handle    prochost.Handle
	TaskDir   string
	SessionID string
}

// startProcHost 是 prochost.Start 的测试缝。
var startProcHost = prochost.Start

// startProc 是 StartProc 的测试缝。
var startProc = StartProc

// killProcHost 是 prochost.Kill 的测试缝。
var killProcHost = prochost.Kill

// lookAgyPath 解析 agy 可执行文件的绝对路径。
var lookAgyPath = func() (string, error) { return exec.LookPath("agy") }

// StartProc 备物料、经 prochost 拉起 shim 承载 agy，返回进程句柄。
func StartProc(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error) {
	l := log.With("task", req.TaskID)
	if len(req.Env) > 0 {
		l.Info("注入 env 变量到 agy 进程", "keys", envKeys(req.Env), "count", len(req.Env))
	}
	fifoPath := filepath.Join(req.TaskDir, fifoFileName)
	if err := prochost.CreateInputChannel(fifoPath); err != nil {
		l.Error("创建输入通道失败", "path", fifoPath, "cause", err)
		return nil, err
	}
	bin, err := lookAgyPath()
	if err != nil {
		l.Error("agy 未安装", "cause", err)
		return nil, fmt.Errorf("agy 未安装: %w", err)
	}
	l.Info("解析 agy 可执行文件", "bin", bin)
	selfExe, err := os.Executable()
	if err != nil {
		l.Error("取 handoff 自身路径失败（shim 无法拉起）", "cause", err)
		return nil, fmt.Errorf("取自身可执行路径: %w", err)
	}
	argv := agyArgv(req)
	argv[0] = bin

	lockPath := filepath.Join(req.TaskDir, lockFileName)
	infoPath := filepath.Join(req.TaskDir, procInfoFileName)
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
		Sentinel: true,
	}
	spec.TaskID = req.TaskID
	spec.MarkRoot = req.MarkRoot
	l.Info("启动 agy 执行者", "bin", bin, "repo", req.RepoPath, "resume", req.Resume)
	handle, err := startProcHost(spec, selfExe)
	if err != nil {
		l.Error("拉起 agy 执行者失败", "cause", err)
		return nil, err
	}
	p := &Proc{Handle: handle, TaskDir: req.TaskDir, SessionID: req.SessionID}
	elapsed, err := prochost.WaitInputReader(fifoPath, fifoReaderTimeout)
	if err != nil {
		l.Error("shim 未在时限内打开 in.fifo", "cause", err, "log_tail", agyLogTail(req.TaskDir))
		if kerr := p.Kill(); kerr != nil {
			l.Warn("回收读端未就绪的执行者失败，可能需人工清理", "cause", kerr)
		}
		return nil, err
	}
	l.Debug("agy in.fifo 读端就绪", "wait", elapsed)
	if err := writeProcInfo(req.TaskDir, &procInfo{
		Handle: handle, SessionID: req.SessionID,
	}); err != nil {
		l.Warn("回写恢复凭据失败，重启恢复将不可用", "cause", err)
	}
	l.Info("agy 执行者已就位", "shim_pid", handle.PID)
	return p, nil
}

// agyArgv 组装 agy 的完整命令行参数列表。
func agyArgv(req StartProcReq) []string {
	argv := []string{
		"agy",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
	}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if req.Resume && req.SessionID != "" {
		argv = append(argv, "--conversation", req.SessionID)
	}
	return argv
}

// WriteInput 往输入通道投递一条 stream-json user message。
func (p *Proc) WriteInput(text string) error {
	path := filepath.Join(p.TaskDir, fifoFileName)
	line, err := json.Marshal(map[string]any{
		"event": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]string{
				{
					"type": "text",
					"text": text,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("序列化 user message: %w", err)
	}
	if err := prochost.WriteInputChannel(path, append(line, '\n')); err != nil {
		return err
	}
	slog.Default().Debug("已投递指令到输入通道", "task_dir", p.TaskDir, "bytes", len(line)+1)
	return nil
}

// procExited 检查 out.jsonl 是否出现死亡哨兵。
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

// Alive 探测 agy 进程是否仍存活。
func (p *Proc) Alive() bool {
	if !prochost.Alive(p.Handle) {
		return false
	}
	if exited, _ := procExited(filepath.Join(p.TaskDir, outFileName)); exited {
		return false
	}
	return true
}

// Kill 终止 agy 进程并清理存活锁。
func (p *Proc) Kill() error {
	return killProcHost(p.Handle)
}

// procInfo 是持久化到 proc.json 的恢复凭据。
type procInfo struct {
	Handle    prochost.Handle `json:"handle"`
	SessionID string          `json:"session_id"`
	Offset    int64           `json:"offset"`
}

func readProcInfo(taskDir string) (procInfo, error) {
	data, err := os.ReadFile(filepath.Join(taskDir, procInfoFileName))
	if err != nil {
		return procInfo{}, err
	}
	var pi procInfo
	if err := json.Unmarshal(data, &pi); err != nil {
		return procInfo{}, fmt.Errorf("解析 %s: %w", procInfoFileName, err)
	}
	return pi, nil
}

func writeProcInfo(taskDir string, pi *procInfo) error {
	path := filepath.Join(taskDir, procInfoFileName)
	data, err := json.MarshalIndent(pi, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 proc.json: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("写 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("重命名 %s -> %s: %w", tmp, path, err)
	}
	return nil
}

func rotateOutJSONL(taskDir string) error {
	oldPath := filepath.Join(taskDir, outFileName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		return nil
	}
	ts := time.Now().Format("20060102-150405.000")
	newPath := filepath.Join(taskDir, fmt.Sprintf("out.%s.jsonl", ts))
	return os.Rename(oldPath, newPath)
}

func envKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func agyLogTail(taskDir string) string {
	path := filepath.Join(taskDir, stderrFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("（读取 %s 失败: %v）", stderrFileName, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, "\n")
}
