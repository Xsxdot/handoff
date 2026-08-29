// proc.go —— grok agent serve 的进程生命周期：prochost 托管、探活、恢复凭据落盘。
//
// 职责：
//   - StartServe：选空闲端口、生成随机 secret、写任务物料、经 prochost 拉起 serve、
//     HTTP 探活等就绪、落 proc.json
//   - Alive/Kill/LogTail：存活探测、回收、脱敏后的诊断尾部
//   - ReadServeInfo：从 proc.json 重建 Proc，供 agentd 重启后 Resume
//
// 边界：
//   - 不说 ACP、不解析事件：协议在 acp.go，语义在 adapter.go
//   - 不做重试决策：探活失败只如实返回，重试与判死节奏归 adapter 的看门狗
//
// 为什么存活判据是「存活锁 + HTTP 端口探活」：锁证明 shim 还在，HTTP 证明 serve
// 本身还在应答。serve 崩了但 shim 尚未收尸的窗口由 HTTP 这条兜住；反过来锁没了
// 也就没有进程可探。grok serve 的根路径返回 404——能收到任何 HTTP 响应就说明
// 进程还在监听。
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
	"strconv"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/prochost"
)

const (
	serveReadyTimeout  = 15 * time.Second // 大于 opencode 的 10s：grok 冷启动要加载配置与索引
	serveProbeInterval = 200 * time.Millisecond
	serveLogTailBytes  = 4 << 10
	serveLogTailRunes  = 500

	// grokSecretEnvKey 是 serve 认证 secret 的环境变量名（沿用既有常量，不得改动）。
	grokSecretEnvKey = "GROK_AGENT_SECRET"
	// lockFileName / procInfoFileName 统一为四 adapter 共用的命名（Global Constraints）。
	lockFileName     = "proc.lock"
	procInfoFileName = "proc.json"
)

// Proc 是一个 grok serve 实例的句柄与恢复凭据。
//
// 注意：Secret 字段是明文 secret，序列化后的 proc.json 必须 0600，
// 且任何日志/错误文本输出前都要经 LogTail 之类的脱敏路径。
type Proc struct {
	Handle  prochost.Handle `json:"handle"`
	TaskDir string          `json:"task_dir"` // 任务目录
	Port    int             `json:"port"`
	Secret  string          `json:"secret"`
}

// WSURL 返回 ACP 的 WebSocket 端点。
//
// 注意：返回值含 secret，**绝不可整体写进日志**。
func (p *Proc) WSURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d/ws?server-key=%s", p.Port, p.Secret)
}

// Alive 检查 grok serve 是否仍然存活：存活锁被持有 且 HTTP 端口有应答。
//
// 为什么第一条是锁而不是端口：锁是本地文件操作、微秒级，端口探测要走网络栈
// 且失败时要等超时。锁判死就没必要再探端口了。
// （旧实现第一条是 tmux has-session，而会话里第二窗口的 tail -f 会一直活着、
// serve 早死了会话依然存在——那个假存活来源已随 tmux 一起消失。）
func (p *Proc) Alive() bool {
	if !prochost.Alive(p.Handle) {
		return false
	}
	return p.probeHTTP()
}

// killProcHost 是 prochost.Kill 的测试缝：SIGKILL 在类 Unix 上不可拦截，
// 真进程做不出「杀不死」的形态，回收失败路径只能靠替换它来驱动。
var killProcHost = prochost.Kill

// Kill 终止 grok serve 及其后代（按进程组），幂等。
func (p *Proc) Kill() error { return killProcHost(p.Handle) }

// probeHTTP 探活 serve 的 HTTP 端口（收到任何响应即算活，含 404）。
func (p *Proc) probeHTTP() bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/", p.Port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
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
// 命中时不静默忽略用户写的行——注入顺序保证 handoff 的变量排在后面因而胜出，
// 同时打 WARN 让用户知道自己那行没生效。
//
// 为什么 GROK_AGENT_SECRET 在列：它被 env 文件覆盖会让 adapter 拿着旧 secret 连
// 不上自己起的 serve；GROK_HOME 被覆盖则整个任务级权限隔离（spec §3.3）失效——
// 那是审批门存在的前提，必须由 handoff 独占。
var protectedEnvKeys = map[string]bool{
	"GROK_HOME":         true,
	"GROK_AGENT_SECRET": true,
}

// startServe 是 StartServe 的测试缝：冷恢复测试替换它断言「起 serve」是否被调用、
// 注入错误，绕开真实 shim + grok 二进制。
var startServe = StartServe

// startProcHost 是 prochost.Start 的测试缝：测试替换它断言 spec 内容，绕开真实 shim。
var startProcHost = prochost.Start

// StartServe 经 prochost 拉起一个任务专属的 grok serve 并等其就绪。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消
//   - repoPath: 任务工作目录（serve 的 cwd）
//   - taskID: 任务 ID（日志与 proc.json 定位）
//   - taskDir: 任务物料目录
//   - model: 任务级模型（空=用 grok 默认）
//   - env: 注入到 serve 进程的环境变量（形如 KEY=VALUE，已由 manager 解析展开）；
//     命中 protectedEnvKeys 的条目会被 handoff 自身注入覆盖并打 WARN
//
// 返回：就绪的 Proc；任一步失败返回错误（错误携带脱敏后的 serve.log 尾部）
func StartServe(ctx context.Context, repoPath, taskID, markRoot, taskDir, model string, env []string, log *slog.Logger) (*Proc, error) {
	if log == nil {
		log = slog.Default()
	}
	start := time.Now()
	log.Info("grok serve 启动中", "task", taskID, "repo", repoPath, "task_dir", taskDir)

	homeDir, err := WriteTaskEnv(taskDir, model)
	if err != nil {
		return nil, err
	}
	carrierHome := nonEmptyEnvValue(env, "HOME")
	if err := ensureAuthLinkAt(homeDir, carrierHome); err != nil {
		log.Error("grok auth 软链建立失败", "task", taskID, "home", carrierHome != "", "cause", err)
		return nil, err
	}
	log.Info("grok auth 权威路径已选择", "task", taskID, "carrier_home", carrierHome != "")
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	secret, err := randomSecret()
	if err != nil {
		return nil, err
	}

	// env 注入（B19）：只打 key 名不打值——值里可能带凭据（如 http://user:pass@host）。
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
	bin, err := exec.LookPath("grok")
	if err != nil {
		log.Error("grok 未安装", "task", taskID, "cause", err)
		return nil, fmt.Errorf("grok 未安装: %w", err)
	}
	// 记绝对路径而不是只记「grok」：PATH 上同时装着多份 CLI 是常态
	// （nvm / homebrew / npm global 各一份），版本行为不一致时，只有这一行
	// 能回答「当时到底跑的是哪一个」。
	log.Info("解析 grok 可执行文件", "task", taskID, "bin", bin)
	selfExe, err := os.Executable()
	if err != nil {
		log.Error("取 handoff 自身路径失败（shim 无法拉起）", "task", taskID, "cause", err)
		return nil, fmt.Errorf("取自身可执行路径: %w", err)
	}
	spec := serveSpec(repoPath, taskDir, model, port, secret, env)
	spec.TaskID = taskID
	spec.MarkRoot = markRoot
	spec.Argv[0] = bin
	// 写前置：proc.json 先于进程落盘，Reap 才永远有据可查
	if err := writeProcInfo(taskDir, &procInfo{
		Handle: prochost.Handle{LockPath: spec.LockPath}, Port: port, Secret: secret,
	}); err != nil {
		log.Error("写恢复凭据失败", "task", taskID, "cause", err)
		return nil, err
	}
	log.Info("启动 grok serve", "task", taskID, "port", port, "bin", bin, "repo", repoPath, "model", model)
	handle, err := startProcHost(spec, selfExe)
	if err != nil {
		log.Error("拉起 grok serve 失败", "task", taskID, "port", port, "cause", err)
		return nil, err
	}
	p := &Proc{Handle: handle, TaskDir: taskDir, Port: port, Secret: secret}
	if err := writeProcInfo(taskDir, &procInfo{
		Handle: handle, Port: port, Secret: secret,
	}); err != nil {
		log.Warn("回写恢复凭据失败，重启恢复将不可用", "task", taskID, "cause", err)
	}

	// 探活等就绪
	deadline := time.Now().Add(serveReadyTimeout)
	for time.Now().Before(deadline) {
		if p.probeHTTP() {
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

// serveSpec 组 grok agent serve 的启动描述。
//
// 为什么 secret 走 Env 而不是 --secret：进程 argv 本机全局可读
// （/proc/<pid>/cmdline），这是 opencode 侧 P0-4 划定的安全边界，本 adapter 原样继承。
// （旧实现另需排除 tmux -e——show-environment 会把它暴露给任何能连上 tmux server
// 的本机用户；tmux 拆掉后这条威胁消失，但「不进 argv」依然成立。）
func serveSpec(repoPath, taskDir, model string, port int, secret string, env []string) prochost.Spec {
	serveLog := filepath.Join(taskDir, serveLogName)
	full := append(os.Environ(), env...)
	full = append(full,
		grokSecretEnvKey+"="+secret, // 变量名沿用既有常量，不得改动
		"GROK_HOME="+filepath.Join(taskDir, homeDirName),
	)
	return prochost.Spec{
		Argv:     grokServeArgv(model, port), // 沿用既有命令形态，原样搬运
		Dir:      repoPath,
		Env:      full,
		Stdout:   serveLog,
		Stderr:   serveLog,
		LockPath: filepath.Join(taskDir, lockFileName),
		InfoPath: filepath.Join(taskDir, procInfoFileName),
		Sentinel: true,
	}
}

// nonEmptyEnvValue 读取 manager 传来的单一环境键；不 trim HOME，保证载体路径
// 的逐字节语义，且空值不误触发隔离凭据例外。
func nonEmptyEnvValue(env []string, key string) string {
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if ok && name == key && value != "" {
			return value
		}
	}
	return ""
}

// grokServeArgv 组 grok agent serve 的 argv（命令形态沿用旧启动脚本，原样搬运）。
func grokServeArgv(model string, port int) []string {
	argv := []string{"grok", "agent", "serve", "--bind", "127.0.0.1:" + strconv.Itoa(port)}
	return argv
}

// writeProcInfo 落恢复凭据（0600：含 secret）。
func writeProcInfo(taskDir string, pi *procInfo) error {
	b, err := json.Marshal(pi)
	if err != nil {
		return fmt.Errorf("序列化恢复凭据: %w", err)
	}
	path := filepath.Join(taskDir, procInfoFileName)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// procInfo 是 serve 连接凭据的持久化形态，agentd 重启后凭它重建订阅。
type procInfo struct {
	Handle prochost.Handle `json:"handle"`
	Port   int             `json:"port"`
	Secret string          `json:"secret"`
}

// ReadServeInfo 从任务目录读回 Proc，供 agentd 重启后 Resume。
func ReadServeInfo(taskDir string) (*Proc, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, procInfoFileName))
	if err != nil {
		return nil, fmt.Errorf("读恢复凭据: %w", err)
	}
	var pi procInfo
	if err := json.Unmarshal(b, &pi); err != nil {
		return nil, fmt.Errorf("解析恢复凭据: %w", err)
	}
	if pi.Handle.LockPath == "" || pi.Port == 0 || pi.Secret == "" {
		return nil, fmt.Errorf("恢复凭据字段不完整")
	}
	p := &Proc{Handle: pi.Handle, Port: pi.Port, Secret: pi.Secret}
	p.TaskDir = taskDir // 目录可能被整体搬动，以实参为准
	return p, nil
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
