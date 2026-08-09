// proc.go —— grok agent serve 的进程生命周期：tmux 托管、探活、恢复凭据落盘。
//
// 职责：
//   - StartServe：选空闲端口、生成随机 secret、写物料与启动脚本、tmux 起 serve、
//     开 render tail 窗口、HTTP 探活等就绪、落 serve.json
//   - Alive/Kill/LogTail：存活探测、回收、脱敏后的诊断尾部
//   - ReadServeInfo：从 serve.json 重建 Proc，供 agentd 重启后 Resume
//
// 边界：
//   - 不说 ACP、不解析事件：协议在 acp.go，语义在 adapter.go
//   - 不做重试决策：探活失败只如实返回，重试与判死节奏归 adapter 的看门狗
//
// 为什么存活判据是 HTTP 端口探活而不是 tmux has-session：会话里第二个窗口的
// tail -f 会一直活着，serve 早死了会话依然存在。grok serve 的根路径返回 404
// ——能收到任何 HTTP 响应就说明进程还在监听。
package grok

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/executor/turn"
	"github.com/xushixin/handoff/internal/shellq"
)

const (
	serveReadyTimeout  = 15 * time.Second // 大于 opencode 的 10s：grok 冷启动要加载配置与索引
	serveProbeInterval = 200 * time.Millisecond
	serveLogTailBytes  = 4 << 10
	serveLogTailRunes  = 500
)

// Proc 是一个 grok serve 实例的句柄与恢复凭据。
//
// 注意：Secret 字段是明文 secret，序列化后的 serve.json 必须 0600，
// 且任何日志/错误文本输出前都要经 LogTail 之类的脱敏路径。
type Proc struct {
	Session string `json:"session"`  // tmux 会话名 handoff-<id8>
	TaskDir string `json:"task_dir"` // 任务目录
	Port    int    `json:"port"`
	Secret  string `json:"secret"`
}

// WSURL 返回 ACP 的 WebSocket 端点。
//
// 注意：返回值含 secret，**绝不可整体写进日志**。
func (p *Proc) WSURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d/ws?server-key=%s", p.Port, p.Secret)
}

// Alive 探测 serve 是否仍在监听（收到任何 HTTP 响应即算活，含 404）。
func (p *Proc) Alive() bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/", p.Port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// Kill 杀掉 tmux 会话回收 serve；会话已不存在视为已清理，不报错。
func (p *Proc) Kill() error {
	out, err := exec.Command("tmux", "kill-session", "-t", p.Session).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "can't find session") ||
			strings.Contains(string(out), "no server running") {
			slog.Default().Info("grok tmux 会话已不存在，视为已清理", "session", p.Session)
			return nil
		}
		return fmt.Errorf("kill tmux 会话 %s: %w (%s)", p.Session, err, strings.TrimSpace(string(out)))
	}
	slog.Default().Info("grok tmux 会话已回收", "session", p.Session)
	return nil
}

// LogTail 返回脱敏后的 serve.log 尾部，供启动超时与死亡诊断。
//
// 为什么必须脱敏：这段尾部会进 Result.FailReason（落事件库）与 agentd.log，
// 而它的内容完全由 grok 决定——实测启动横幅就会原样打印 Secret 一行。
func (p *Proc) LogTail() string {
	b, err := os.ReadFile(filepath.Join(p.TaskDir, serveLogName))
	if err != nil {
		return ""
	}
	if len(b) > serveLogTailBytes {
		b = b[len(b)-serveLogTailBytes:]
	}
	tail := turn.TailRunes(string(b), serveLogTailRunes)
	if p.Secret == "" {
		return tail
	}
	return strings.ReplaceAll(tail, p.Secret, "***")
}

// protectedEnvKeys 是 handoff 自身注入、不容 env 文件覆盖的变量（B19）。
//
// 命中时不静默忽略用户写的行——注入顺序保证 handoff 的 export 排在后面因而胜出，
// 同时打 WARN 让用户知道自己那行没生效。
//
// 为什么 GROK_AGENT_SECRET 在列：它被 env 文件覆盖会让 adapter 拿着旧 secret 连
// 不上自己起的 serve；GROK_HOME 被覆盖则整个任务级权限隔离（spec §3.3）失效——
// 那是审批门存在的前提，必须由 handoff 独占。
var protectedEnvKeys = map[string]bool{
	"GROK_HOME":         true,
	"GROK_AGENT_SECRET": true,
}

// StartServe 起一个任务专属的 grok serve 并等其就绪。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消
//   - repoPath: 任务工作目录（tmux 会话的 cwd）
//   - taskID: 任务 ID（取前 8 字符作会话名后缀）
//   - taskDir: 任务物料目录
//   - model: 任务级模型（空=用 grok 默认）
//   - env: 注入到 serve 进程的环境变量（形如 KEY=VALUE，已由 manager 解析展开）；
//     命中 protectedEnvKeys 的条目会被 handoff 自身注入覆盖并打 WARN
//
// 返回：就绪的 Proc；任一步失败返回错误（错误携带脱敏后的 serve.log 尾部）
func StartServe(ctx context.Context, repoPath, taskID, taskDir, model string, env []string, log *slog.Logger) (*Proc, error) {
	if log == nil {
		log = slog.Default()
	}
	start := time.Now()
	log.Info("grok serve 启动中", "task", taskID, "repo", repoPath, "task_dir", taskDir)

	homeDir, err := WriteTaskEnv(taskDir, model)
	if err != nil {
		return nil, err
	}
	if err := EnsureAuthLink(homeDir); err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	secret, err := randomSecret()
	if err != nil {
		return nil, err
	}

	// env 注入（B19）：只打 key 名不打值——值里可能带凭据（如 http://user:pass@host）。
	// 与 opencode 的 StartServe 同构。
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for _, kv := range env {
			k, _, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			keys = append(keys, k)
			if protectedEnvKeys[k] {
				log.Warn("env 文件定义了 handoff 保留变量，将被 handoff 自身注入覆盖",
					"key", k, "task", taskID)
			}
		}
		log.Info("注入 env 变量到 grok serve 进程", "task", taskID, "keys", keys, "count", len(keys))
	}

	scriptPath, err := WriteServeScript(taskDir, homeDir, port, secret, env)
	if err != nil {
		return nil, err
	}

	p := &Proc{Session: "handoff-" + id8(taskID), TaskDir: taskDir, Port: port, Secret: secret}
	args := []string{"new-session", "-d", "-s", p.Session, "-c", repoPath,
		"sh " + shellq.Quote(scriptPath)}
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		log.Error("grok tmux 启动失败", "task", taskID, "cause", err, "out", strings.TrimSpace(string(out)))
		return nil, fmt.Errorf("tmux 启动 grok serve: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	startRenderTailWindow(p.Session, taskDir, log)

	// 探活等就绪
	deadline := time.Now().Add(serveReadyTimeout)
	for time.Now().Before(deadline) {
		if p.Alive() {
			if err := writeServeInfo(p); err != nil {
				log.Warn("写 serve.json 失败，Resume 将不可用", "task", taskID, "cause", err)
			}
			log.Info("grok serve 就绪", "task", taskID, "port", port,
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
	_ = p.Kill() // 清理残留，不留孤儿 serve
	log.Error("grok serve 就绪超时", "task", taskID, "timeout", serveReadyTimeout, "log_tail", tail)
	return nil, fmt.Errorf("grok serve %s 内未就绪: %s", serveReadyTimeout, tail)
}

// startRenderTailWindow 在会话内开第二窗口 tail -f render.log（模型回合文本实况）。
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

// writeServeInfo 落恢复凭据（0600：含 secret）。
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

// ReadServeInfo 从任务目录读回 Proc，供 agentd 重启后 Resume。
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

// randomSecret 生成 32 字符十六进制随机 secret。
func randomSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 serve secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// id8 取字符串前 8 字符作 tmux 会话名后缀（与 opencode 同规则，attach 零改动）。
func id8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
