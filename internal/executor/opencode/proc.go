// proc.go —— opencode serve 进程生命周期管理。
//
// 职责：
//   - 在 tmux 会话内拉起 `opencode serve --port <随机空闲端口> --hostname 127.0.0.1`
//   - 经 taskDir 下的 0600 启动脚本注入 OPENCODE_SERVER_PASSWORD（随机生成）与
//     OPENCODE_CONFIG（Task 10 生成的配置路径）——argv 只留脚本路径，密码不进
//     任何进程的命令行参数（why 见 writeServeScript）
//   - serve 输出 tee 落盘 <taskDir>/serve.log：serve 所在窗格随其命令退出关闭、
//     capture-pane 必空（P1-8），serve.log 是启动超时与死亡诊断的持久 stderr
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
// 额外收益：用户可 `tmux attach -t handoff-<id8>` 旁观甚至介入任务执行（第二窗口
// tail -f render.log 展示模型文本实况，见 startRenderTailWindow），这是
// 调试与人工兜底的关键通道。
package opencode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/xushixin/handoff/internal/shellq"
)

// serveReadyTimeout 是 StartServe 等待 serve 就绪的总超时。
const serveReadyTimeout = 10 * time.Second

// serveProbeInterval 是就绪/存活探测的轮询间隔。
const serveProbeInterval = 200 * time.Millisecond

// 任务目录内的执行器物料文件名（目录本身 0700，见 manager 创建处）。
const (
	serveScriptFileName = "run_serve.sh" // 0600 启动脚本（含随机密码，见 writeServeScript）
	serveLogFileName    = "serve.log"    // serve 输出持久副本（tee 落盘，P1-8 诊断来源）
	renderLogFileName   = "render.log"   // 模型回合文本增量（tmux 第二窗口 tail -f 目标）
)

// Proc 描述一个运行中的 opencode serve 进程。
//
// 字段说明：
//   - Port: serve 监听的端口（127.0.0.1 上）
//   - Password: 随机生成的 OPENCODE_SERVER_PASSWORD，api.go 的 basic auth 用它
//   - TmuxSession: tmux 会话名（handoff-<taskID 前 8 字符>），用户可 attach 旁观
//   - ServeLogPath: serve 输出日志路径（<taskDir>/serve.log），serve 死后
//     capture-pane 读不到已关闭窗格，诊断只能读它
type Proc struct {
	Port         int
	Password     string
	TmuxSession  string
	ServeLogPath string
}

// StartServe 在 tmux 内启动 opencode serve 并等待就绪。
//
// 参数：
//   - ctx: 上下文；就绪轮询同样受 ctx 取消影响
//   - repoPath: 任务仓库路径，作为 serve 的工作目录（cwd）
//   - taskID: 任务 id，用于生成 tmux 会话名（handoff-<id8>）
//   - taskDir: 任务目录（0700），serve 启动脚本与 serve.log 都放这里
//   - configPath: 任务级 opencode 配置路径（Task 10 生成），注入 OPENCODE_CONFIG
//   - log: 本模块日志入口（StartServe 是进程启动点，日志需要显式传入而非走默认）
//
// 返回：
//   - 就绪的 Proc；就绪 = 端口上已有 HTTP 服务响应（含 401：密码校验属后续请求的事，
//     这里只关心「serve 进程起来且 HTTP 层可应答」）
//   - 错误：启动脚本写失败、tmux 启动失败、10s 内未就绪（错误信息携带 serve.log
//     尾部的 serve stderr）
//
// 注意：
//   - 端口选择存在 TOCTOU 竞态（见 freePort），MVP 接受
//   - 就绪超时后自动 Kill 清理残留 session，避免半启动进程占着端口
func StartServe(ctx context.Context, repoPath, taskID, taskDir, configPath string, log *slog.Logger) (*Proc, error) {
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
	// 密码/配置经 0600 启动脚本注入，tmux argv 只含脚本路径（ps 不可见密码）
	scriptPath, err := writeServeScript(taskDir, port, password, configPath)
	if err != nil {
		log.Error("写 serve 启动脚本失败", "session", session, "cause", err)
		return nil, err
	}
	log.Info("启动 opencode serve", "port", port, "session", session, "script", scriptPath, "repo", repoPath)

	out, err := exec.Command("tmux", serveTmuxArgs(session, repoPath, scriptPath)...).CombinedOutput()
	if err != nil {
		log.Error("tmux 启动 opencode serve 失败", "session", session,
			"stderr_tail", tail(string(out), 500), "cause", err)
		return nil, fmt.Errorf("tmux 启动 %s: %w", session, err)
	}
	// 第二窗口 tail -f render.log（模型文本实况）；失败只 Warn 不阻断，见该函数注释
	startRenderTailWindow(session, taskDir, log)

	p := &Proc{Port: port, Password: password, TmuxSession: session,
		ServeLogPath: filepath.Join(taskDir, serveLogFileName)}
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
			// 就绪超时：读 serve.log 尾部（serve 的 stderr）带进错误，这是
			// 「为什么没起来」的第一手证据（如端口被占、config 解析失败）。
			// 为什么读文件而不是 capture-pane：serve 没起来就谈不上窗格内容，
			// 且窗格随命令退出关闭（P1-8），serve.log 是唯一可靠来源
			stderrTail := serveLogTail(p.ServeLogPath)
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

// writeServeScript 在 taskDir 生成 0600 权限的 serve 启动脚本并返回其路径。
//
// why（脚本化而不是 tmux -e / 命令行内联）：
//   - tmux new-session 的 -e 参数是 tmux 客户端进程的字面 argv，Linux 上
//     /proc/<pid>/cmdline 默认全局可读（ps 即可见），tmux show-environment
//     也能读回会话环境——随机密码只保护到「本机任意用户可读」为止（P0-4）。
//     密码与配置路径改为写进 taskDir 下的 0600 脚本（taskDir 本身 0700），
//     tmux argv 只剩脚本路径，ps 只能看到 sh <taskDir>/run_serve.sh
//   - 脚本没有可执行位、用 `sh <path>` 显式调用：少一个可执行文件面
//
// why（tee -a serve.log）：serve 的 stdout/stderr 同时落盘 taskDir/serve.log。
// serve 所在窗格随其命令退出而关闭，capture-pane 读不到已关闭窗格（P1-8）；
// serve.log 是 serve 死后仍可读的持久诊断副本——启动超时与 serve 死亡错误的
// stderr 尾部都从它取。
//
// why（脚本首行 exec 2>> serve.log）：sh 自身的 stderr（如 exec 的 opencode
// 不存在时报 "not found"、export 失败）同样落盘 serve.log，否则这类报错只进
// tmux 窗格、随命令退出一起消失。opencode 侧 2>&1 仍走管道进 tee，不受此
// 重定向影响——命令级重定向（2>&1）覆盖 shell 级（2>> 文件）；tee 自身继承
// 文件 fd2，它的报错也落盘。
func writeServeScript(taskDir string, port int, password, configPath string) (string, error) {
	serveLogPath := filepath.Join(taskDir, serveLogFileName)
	script := fmt.Sprintf(`#!/bin/sh
# 由 agentd 生成：opencode serve 启动脚本（0600，含随机密码，勿外泄）。
exec 2>> %s
export OPENCODE_SERVER_PASSWORD=%s
export OPENCODE_CONFIG=%s
exec opencode serve --port %d --hostname 127.0.0.1 2>&1 | tee -a %s
`, shellQuote(serveLogPath), shellQuote(password), shellQuote(configPath), port, shellQuote(serveLogPath))
	scriptPath := filepath.Join(taskDir, serveScriptFileName)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return "", fmt.Errorf("写 serve 启动脚本 %s: %w", scriptPath, err)
	}
	return scriptPath, nil
}

// shellQuote 把字符串包成单引号 shell 字面量（内含单引号转义为 '\”），
// 供写进启动脚本与 tmux 命令串——密码/路径可能含引号或空白，不转义会改变
// 脚本语义或让 tmux 把命令拆错。
// 实现委托 internal/shellq（与 cmd 包弹终端的 shell 拼接同源，避免复制漂移）。
func shellQuote(s string) string {
	return shellq.Quote(s)
}

// serveTmuxArgs 组装启动 serve 的 tmux new-session 参数。
//
// why（argv 只含脚本路径）：密码/配置经启动脚本注入，命令行参数层面不出现
// 任何秘密——tmux 客户端进程的 argv 全局可读（/proc/<pid>/cmdline），这是
// P0-4 的安全边界所在。环境变量也不再走 -e：show-environment 会把密码
// 暴露给任何能连上 tmux server 的本机用户。
func serveTmuxArgs(session, repoPath, scriptPath string) []string {
	return []string{
		"new-session", "-d", "-s", session, "-c", repoPath,
		"sh " + shellQuote(scriptPath),
	}
}

// startRenderTailWindow 在会话内开第二窗口 `tail -f <taskDir>/render.log`：
// 模型回合文本实况（spec 三大核心诉求之一，README 已宣称可用）。
//
// 稳健做法：先 touch render.log 再开窗口——tail -f 对不存在的文件会立即报错
// 退出（GNU/BSD 只在 -F 下才等待文件出现），而 render.log 由 adapter 在首个
// 文本增量时才创建。窗口启动失败只 Warn 不阻断主流程：这是增强型可见性
// （attach 第一窗口仍能看到 serve 输出），不值得为它挂掉任务启动。
func startRenderTailWindow(session, taskDir string, log *slog.Logger) {
	renderLogPath := filepath.Join(taskDir, renderLogFileName)
	f, err := os.OpenFile(renderLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Warn("创建 render.log 失败，tmux 第二窗口不可用", "session", session, "cause", err)
		return
	}
	f.Close()
	if err := exec.Command("tmux", "new-window", "-t", session,
		"tail -f "+shellQuote(renderLogPath)).Run(); err != nil {
		log.Warn("tmux 第二窗口启动失败（tail render.log 不可用），不影响主流程",
			"session", session, "cause", err)
	}
}

// serveLogTailBytes 是 serve.log 尾部读取的字节上限（诊断信息取末尾 500 字节，
// 多读一些余量以便 tail 按完整行截断）。
const serveLogTailBytes = 4 << 10

// serveLogTail 读 serve.log 尾部最多 500 字节，供就绪超时/死亡诊断；文件未
// 创建（serve 根本没跑起来）或已被清理时返回空串。
//
// why（读文件而非 tmux capture-pane）：serve 所在窗格随命令退出而关闭，
// capture-pane 读不到已关闭窗格（P1-8）——serve.log 是 tee 落盘的持久副本，
// serve 死后仍可读。
//
// why（Seek 到尾部而非 os.ReadFile 整读）：serve.log 由 tee -a 写满任务全程且
// 无轮转，而本函数的调用时机恰是 serve 死亡/就绪超时——最不该再分配几百 MB 的
// 时刻。整读一份 100MiB 日志只为取末尾 500 字节，是把诊断动作变成第二次故障。
func serveLogTail(serveLogPath string) string {
	f, err := os.Open(serveLogPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	offset := fi.Size() - serveLogTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(f, serveLogTailBytes))
	if err != nil {
		return ""
	}
	return tail(string(b), 500)
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
