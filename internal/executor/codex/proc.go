// proc.go —— codex app-server 的进程生命周期：tmux 托管、探活、恢复凭据落盘。
//
// 职责：
//   - StartServe：选空闲端口、写启动脚本、tmux 起 app-server、开 render tail 窗口、
//     探活等就绪、落 serve.json
//   - Alive/Kill/LogTail：存活探测、回收、诊断尾部
//   - ReadServeInfo：从 serve.json 重建 Proc，供 agentd 重启后 Resume（B18）
//
// 边界：
//   - 不说协议、不解析事件：协议在 appserver.go，语义在 adapter.go
//   - 不做重试决策：探活失败只如实返回，重试与判死节奏归 adapter 的看门狗
//
// 为什么没有 Secret 字段（与 grok 不同）：`codex app-server --listen ws://` 不带
// 鉴权 secret，serve.json 里没有凭据，LogTail 也不需要脱敏。仍写 0600——任务目录
// 里的文件一律 0600，不为个案开口子。
//
// 为什么存活判据是 TCP 连通而不是 HTTP GET（与 grok 不同）：`--listen ws://` 起的
// 是纯 WebSocket 服务端，没有 HTTP 面可探。这条判据比 grok 弱——端口 listen 住但
// 协议层已死时会误判为活。**真正的健康信号是 WS 连接自身的死亡**（Handler.OnClosed），
// Alive 只用于「起没起来」和看门狗的粗判，不要把它当强判据用。
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/executor/turn"
	"github.com/xushixin/handoff/internal/shellq"
)

const (
	serveReadyTimeout  = 20 * time.Second // 大于 grok 的 15s：codex 冷启动要加载插件与 skills
	serveProbeInterval = 200 * time.Millisecond
	serveProbeDialTO   = 2 * time.Second
	serveLogTailBytes  = 4 << 10
	serveLogTailRunes  = 500
)

// Proc 是一个 codex app-server 实例的句柄与恢复凭据。
type Proc struct {
	Session string `json:"session"`  // tmux 会话名 handoff-<id8>
	TaskDir string `json:"task_dir"` // 任务目录
	Port    int    `json:"port"`
}

// WSURL 返回 app-server 的 WebSocket 端点。
//
// 注意：形态由 Task 1 的 V-5 探针实测确认；若实测形态带路径，改这里一处即可。
func (p *Proc) WSURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d", p.Port)
}

// Alive 探测 app-server 是否仍在监听（TCP 能连上即算活）。
//
// 注意：判据弱于 grok 的 HTTP 探活，见文件头说明——端口活着不等于协议层活着。
func (p *Proc) Alive() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port), serveProbeDialTO)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// tmuxKill 是 tmux kill-session 的测试缝：测试替换它断言回收的会话名，绕开真实 tmux。
var tmuxKill = func(session string) error {
	return exec.Command("tmux", "kill-session", "-t", session).Run()
}

// tmuxHasSession 是 tmux has-session 探活的测试缝。
var tmuxHasSession = func(session string) bool {
	return exec.Command("tmux", "has-session", "-t", session).Run() == nil
}

// Kill 杀掉 tmux 会话回收 app-server；会话已不存在视为已清理，不报错（B20：回收幂等）。
func (p *Proc) Kill() error {
	err := tmuxKill(p.Session)
	if err != nil {
		if !tmuxHasSession(p.Session) {
			slog.Default().Info("codex tmux 会话已不存在，视为已清理", "session", p.Session)
			return nil
		}
		slog.Default().Error("codex tmux 会话回收失败", "session", p.Session, "cause", err)
		return fmt.Errorf("kill tmux 会话 %s: %w (%s)", p.Session, err, tmuxKillErrTail(err))
	}
	slog.Default().Info("codex tmux 会话已回收", "session", p.Session)
	return nil
}

// tmuxKillErrTail 提取 kill 错误的 stderr 尾部，让失败原因可行动（B16）。
func tmuxKillErrTail(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return err.Error()
}

// LogTail 返回 serve.log 尾部，供启动超时与死亡诊断（B16：失败要给可行动真因）。
func (p *Proc) LogTail() string {
	b, err := os.ReadFile(filepath.Join(p.TaskDir, serveLogName))
	if err != nil {
		return ""
	}
	if len(b) > serveLogTailBytes {
		b = b[len(b)-serveLogTailBytes:]
	}
	return turn.TailRunes(string(b), serveLogTailRunes)
}

// startServe 是 StartServe 的测试缝：冷恢复测试替换它断言「起 serve」是否被调用。
var startServe = StartServe

// StartServe 起一个任务专属的 codex app-server 并等其就绪。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消
//   - repoPath: 任务工作目录（tmux 会话的 cwd）
//   - taskID: 任务 ID（取前 8 字符作会话名后缀）
//   - taskDir: 任务物料目录
//   - env: 注入到 app-server 进程的环境变量（B19）
//   - log: 日志入口（nil 退回 slog.Default()）
//
// 返回：就绪的 Proc；任一步失败返回错误（错误携带 serve.log 尾部）
//
// 注意：**没有 model 参数**（与 grok 不同）——codex 的模型选择是协议级的
// （thread/start 的 model 字段），不经启动脚本。
func StartServe(ctx context.Context, repoPath, taskID, taskDir string, env []string, log *slog.Logger) (*Proc, error) {
	if log == nil {
		log = slog.Default()
	}
	start := time.Now()
	log.Info("codex app-server 启动中", "task", taskID, "repo", repoPath, "task_dir", taskDir)

	port, err := freePort()
	if err != nil {
		return nil, err
	}

	// env 注入（B19）：只打 key 名不打值——值里可能带凭据。
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for _, kv := range env {
			if k, _, ok := strings.Cut(kv, "="); ok {
				keys = append(keys, k)
			}
		}
		log.Info("注入 env 变量到 codex app-server 进程", "task", taskID, "keys", keys, "count", len(keys))
	}

	scriptPath, err := WriteServeScript(taskDir, port, env)
	if err != nil {
		return nil, err
	}

	p := &Proc{Session: "handoff-" + id8(taskID), TaskDir: taskDir, Port: port}
	args := []string{"new-session", "-d", "-s", p.Session, "-c", repoPath,
		"sh " + shellq.Quote(scriptPath)}
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		log.Error("codex tmux 启动失败", "task", taskID, "cause", err, "out", strings.TrimSpace(string(out)))
		return nil, fmt.Errorf("tmux 启动 codex app-server: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	startRenderTailWindow(p.Session, taskDir, log)

	deadline := time.Now().Add(serveReadyTimeout)
	for time.Now().Before(deadline) {
		if p.Alive() {
			if err := writeServeInfo(p); err != nil {
				log.Warn("写 serve.json 失败，Resume 将不可用", "task", taskID, "cause", err)
			}
			log.Info("codex app-server 就绪", "task", taskID, "port", port,
				"elapsed_ms", time.Since(start).Milliseconds())
			return p, nil
		}
		select {
		case <-ctx.Done():
			_ = p.Kill()
			return nil, ctx.Err()
		case <-time.After(serveProbeInterval):
		}
	}
	tail := p.LogTail()
	_ = p.Kill() // 清理残留，不留孤儿进程
	log.Error("codex app-server 就绪超时", "task", taskID, "timeout", serveReadyTimeout, "log_tail", tail)
	return nil, fmt.Errorf("codex app-server %s 内未就绪: %s", serveReadyTimeout, tail)
}

// startRenderTailWindow 在会话内开第二窗口 tail -f render.log（回合实况）。
//
// 稳健做法：先 touch render.log 再开窗口——tail -f 对不存在的文件会立即报错退出。
// 窗口启动失败只 Warn 不阻断：这是增强型可见性，不值得为它挂掉任务启动。
func startRenderTailWindow(session, taskDir string, log *slog.Logger) {
	p := filepath.Join(taskDir, renderLogName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Warn("创建 render.log 失败，tmux 第二窗口不可用", "session", session, "cause", err)
		return
	}
	f.Close()
	if err := exec.Command("tmux", "new-window", "-t", session,
		"tail -f "+shellq.Quote(p)).Run(); err != nil {
		log.Warn("tmux 第二窗口启动失败（tail render.log 不可用），不影响主流程",
			"session", session, "cause", err)
	}
}

// writeServeInfo 落恢复凭据（0600：与任务目录内其他文件同档）。
func writeServeInfo(p *Proc) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 serve.json: %w", err)
	}
	path := filepath.Join(p.TaskDir, serveInfoName)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// ReadServeInfo 从任务目录读回 Proc，供 agentd 重启后 Resume（B18）。
func ReadServeInfo(taskDir string) (*Proc, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, serveInfoName))
	if err != nil {
		return nil, fmt.Errorf("读 serve.json: %w", err)
	}
	var p Proc
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("解析 serve.json: %w", err)
	}
	p.TaskDir = taskDir // 目录可能被整体搬动，以实参为准
	return &p, nil
}

// freePort 让内核分配一个空闲回环端口。
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("分配空闲端口: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// id8 取字符串前 8 字符作 tmux 会话名后缀（与另三个 adapter 同规则，attach 零改动）。
func id8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
