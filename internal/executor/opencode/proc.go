// proc.go —— opencode serve 进程生命周期管理。
//
// 职责：
//   - 在 tmux 会话内拉起 `opencode serve --port <随机空闲端口> --hostname 127.0.0.1`
//   - 注入 OPENCODE_SERVER_PASSWORD（随机生成）与 OPENCODE_CONFIG（Task 10 生成的配置路径）
//   - 就绪探测（轮询 GET /）、存活检查（tmux has-session + HTTP 探活）、销毁（kill-session）
//
// 边界：
//   - 不触碰会话：会话的创建、prompt、权限应答由 api.go 完成，本文件只保证
//     「serve 进程活着、端口可用」
//   - 不生成任务级配置（OPENCODE_CONFIG 指向的文件由 Task 10 生成）
//
// 为什么进程放 tmux 而不是 agentd 子进程：agentd 重启或崩溃时子进程树会被一并
// 回收，正在执行的任务会无辜中断；tmux server 是独立守护进程，session 及其子进程
// 生命周期与 agentd 完全解耦——agentd 重启后靠 Alive() 探测发现存活并重连 SSE。
// 额外收益：用户可 `tmux attach -t handoff-<id8>` 旁观甚至介入任务执行，这是
// 调试与人工兜底的关键通道。
package opencode

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
	"time"
)

// serveReadyTimeout 是 StartServe 等待 serve 就绪的总超时。
const serveReadyTimeout = 10 * time.Second

// serveProbeInterval 是就绪/存活探测的轮询间隔。
const serveProbeInterval = 200 * time.Millisecond

// Proc 描述一个运行中的 opencode serve 进程。
//
// 字段说明：
//   - Port: serve 监听的端口（127.0.0.1 上）
//   - Password: 随机生成的 OPENCODE_SERVER_PASSWORD，api.go 的 basic auth 用它
//   - TmuxSession: tmux 会话名（handoff-<taskID 前 8 字符>），用户可 attach 旁观
type Proc struct {
	Port        int
	Password    string
	TmuxSession string
}

// StartServe 在 tmux 内启动 opencode serve 并等待就绪。
//
// 参数：
//   - ctx: 上下文；就绪轮询同样受 ctx 取消影响
//   - repoPath: 任务仓库路径，作为 serve 的工作目录（cwd）
//   - taskID: 任务 id，用于生成 tmux 会话名（handoff-<id8>）
//   - configPath: 任务级 opencode 配置路径（Task 10 生成），注入 OPENCODE_CONFIG
//   - log: 本模块日志入口（StartServe 是进程启动点，日志需要显式传入而非走默认）
//
// 返回：
//   - 就绪的 Proc；就绪 = 端口上已有 HTTP 服务响应（含 401：密码校验属后续请求的事，
//     这里只关心「serve 进程起来且 HTTP 层可应答」）
//   - 错误：tmux 启动失败、10s 内未就绪（错误信息携带 tmux 窗格捕获的 stderr 尾部）
//
// 注意：
//   - 端口选择存在 TOCTOU 竞态（见 freePort），MVP 接受
//   - 就绪超时后自动 Kill 清理残留 session，避免半启动进程占着端口
func StartServe(ctx context.Context, repoPath, taskID, configPath string, log *slog.Logger) (*Proc, error) {
	port, err := freePort()
	if err != nil {
		log.Error("获取随机空闲端口失败", "cause", err)
		return nil, fmt.Errorf("获取空闲端口: %w", err)
	}
	password, err := randomPassword()
	if err != nil {
		log.Error("生成 serve 密码失败", "cause", err)
		return nil, fmt.Errorf("生成 serve 密码: %w", err)
	}

	session := "handoff-" + id8(taskID)
	cmd := fmt.Sprintf("opencode serve --port %d --hostname 127.0.0.1", port)
	log.Info("启动 opencode serve", "port", port, "session", session, "cmd", cmd, "repo", repoPath)

	// 环境变量走 tmux new-session 的 -e 参数注入：tmux 负责把 NAME=VALUE 原样放进
	// 会话初始进程的环境，避免把随机密码拼进 shell 命令串带来的转义/注入问题
	out, err := exec.Command("tmux", "new-session", "-d",
		"-s", session, "-c", repoPath,
		"-e", "OPENCODE_SERVER_PASSWORD="+password,
		"-e", "OPENCODE_CONFIG="+configPath,
		cmd).CombinedOutput()
	if err != nil {
		log.Error("tmux 启动 opencode serve 失败", "session", session,
			"stderr_tail", tail(string(out), 500), "cause", err)
		return nil, fmt.Errorf("tmux 启动 %s: %w", session, err)
	}

	p := &Proc{Port: port, Password: password, TmuxSession: session}
	readyCtx, cancel := context.WithTimeout(ctx, serveReadyTimeout)
	defer cancel()

	start := time.Now()
	for {
		if p.probeHTTP() {
			log.Info("opencode serve 就绪", "port", port, "session", session,
				"ready_ms", time.Since(start).Milliseconds())
			return p, nil
		}
		select {
		case <-readyCtx.Done():
			// 就绪超时：把 tmux 窗格内容尾部（serve 的 stderr）带进错误，这是
			// 「为什么没起来」的第一手证据（如端口被占、config 解析失败）
			stderrTail := p.capturePaneTail()
			log.Error("opencode serve 就绪超时", "port", port, "session", session,
				"stderr_tail", stderrTail)
			_ = p.Kill() // 清理残留，避免半启动进程占着端口
			return nil, fmt.Errorf("opencode serve 就绪超时（10s）: %s", stderrTail)
		case <-time.After(serveProbeInterval):
		}
	}
}

// Alive 检查 serve 进程是否仍然存活：tmux 会话存在且端口有 HTTP 应答。
//
// 注意：探测是全副「tmux session + HTTP」，两者缺一即视为死亡——tmux 会话在但
// serve 进程已退出（如崩溃）时 HTTP 探活会失败，反之亦然。
func (p *Proc) Alive() bool {
	if exec.Command("tmux", "has-session", "-t", p.TmuxSession).Run() != nil {
		return false
	}
	return p.probeHTTP()
}

// Kill 销毁 tmux 会话（连同其内的 opencode serve）。
//
// 幂等：会话已不存在（已被外部清理）时返回 nil。
func (p *Proc) Kill() error {
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

// probeHTTP 探活 serve 的 HTTP 端口。
//
// 任一 HTTP 响应（含 401/404）都视为「进程起来且 HTTP 层可应答」；
// 网络层失败（连接拒绝/超时）视为不可用。
func (p *Proc) probeHTTP() bool {
	client := &http.Client{Timeout: time.Second}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", p.Port), nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth("opencode", p.Password)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// capturePaneTail 捕获 tmux 窗格当前可见内容（serve 的 stdout/stderr）尾部，
// 供就绪超时诊断；会话已死时返回空串。
func (p *Proc) capturePaneTail() string {
	out, err := exec.Command("tmux", "capture-pane", "-t", p.TmuxSession, "-p").Output()
	if err != nil {
		return ""
	}
	return tail(string(out), 500)
}

// serveInfoFileName 是 serve 连接凭据文件名（任务目录内，0600 权限）。
const serveInfoFileName = "serve.json"

// serveInfo 是 serve 进程连接凭据的持久化形态，agentd 重启后凭它重建订阅。
type serveInfo struct {
	Port        int    `json:"port"`
	Password    string `json:"password"`
	TmuxSession string `json:"tmux_session"`
}

// writeServeInfo 把 serve 连接凭据写入任务目录 serve.json。
//
// why（必须持久化）：agentd 重启后内存中的 Proc（端口/密码）丢失，而 tmux 内的
// serve 进程独立存活（进程模型见文件头）；RecoverOnStartup 凭此文件探活并重建
// SSE 订阅（spec §8）。写失败不阻断启动（adapter.Start 只 Warn），缺失时该任务
// 重启后按「执行器已不在」转 failed 交审核者——保守胜于静默丢事件。
func writeServeInfo(taskDir string, p *Proc) error {
	b, err := json.Marshal(serveInfo{Port: p.Port, Password: p.Password, TmuxSession: p.TmuxSession})
	if err != nil {
		return fmt.Errorf("序列化 serve 凭据: %w", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, serveInfoFileName), b, 0o600); err != nil {
		return fmt.Errorf("写 serve 凭据 %s: %w", serveInfoFileName, err)
	}
	return nil
}

// readServeInfo 读取任务目录的 serve 连接凭据。
//
// 返回：
//   - 文件缺失/损坏/字段不完整时返回错误（调用方据此判「无法重建订阅」）
func readServeInfo(taskDir string) (*serveInfo, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, serveInfoFileName))
	if err != nil {
		return nil, fmt.Errorf("读 serve 凭据 %s: %w", serveInfoFileName, err)
	}
	var si serveInfo
	if err := json.Unmarshal(b, &si); err != nil {
		return nil, fmt.Errorf("解析 serve 凭据 %s: %w", serveInfoFileName, err)
	}
	if si.Port == 0 || si.Password == "" || si.TmuxSession == "" {
		return nil, fmt.Errorf("serve 凭据 %s 字段不完整", serveInfoFileName)
	}
	return &si, nil
}

// freePort 找一个随机空闲端口。
//
// 注意：存在 TOCTOU 竞态——端口在 Close 之后、opencode serve 真正监听之前可能被
// 其他进程抢走。MVP 阶段接受该竞态（本机空闲端口被抢概率极低），若真被占用，
// serve 会启动失败退出，StartServe 的就绪轮询超时并以 stderr 尾部暴露原因。
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port, nil
}

// randomPassword 生成 32 位十六进制随机串，用作 OPENCODE_SERVER_PASSWORD。
func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// id8 取字符串前 8 个字符（不足 8 个则原样返回），用于 tmux 会话名。
func id8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// tail 返回字符串尾部最多 n 个字符（按字节截断，日志用，不追求多字节安全——
// 截断点切在中间也无碍诊断）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
