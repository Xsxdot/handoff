// proc.go —— codex app-server 的进程生命周期：prochost 托管、探活、恢复凭据落盘。
//
// 职责：
//   - StartServe：选空闲端口、经 prochost 拉起 app-server、探活等就绪、落 proc.json
//   - Alive/Kill/LogTail：存活探测、回收、诊断尾部
//   - ReadServeInfo：从 proc.json 重建 Proc，供 agentd 重启后 Resume（B18）
//
// 边界：
//   - 不说协议、不解析事件：协议在 appserver.go，语义在 adapter.go
//   - 不做重试决策：探活失败只如实返回，重试与判死节奏归 adapter 的看门狗
//
// 为什么没有 Secret 字段（与 grok 不同）：`codex app-server --listen ws://` 不带
// 鉴权 secret，proc.json 里没有凭据，LogTail 也不需要脱敏。仍写 0600——任务目录
// 里的文件一律 0600，不为个案开口子。
//
// 为什么存活判据是「存活锁 + TCP 连通」而不是只有 HTTP GET（与 grok 不同）：
// 锁证明 shim 还在；`--listen ws://` 起的是纯 WebSocket 服务端，没有 HTTP 面可探，
// TCP 连通是弱于 HTTP 的判据——端口 listen 住但协议层已死时会误判为活。**真正的
// 健康信号是 WS 连接自身的死亡**（Handler.OnClosed），Alive 只用于「起没起来」
// 和看门狗的粗判，不要把它当强判据用。
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
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
	serveReadyTimeout  = 20 * time.Second // 大于 grok 的 15s：codex 冷启动要加载插件与 skills
	serveProbeInterval = 200 * time.Millisecond
	serveProbeDialTO   = 2 * time.Second
	serveLogTailBytes  = 4 << 10
	serveLogTailRunes  = 500

	lockFileName     = "proc.lock" // shim 存活锁（prochost.Alive 的唯一判据）
	procInfoFileName = "proc.json" // 恢复凭据：prochost.Handle / port
)

// Proc 是一个 codex app-server 实例的句柄与恢复凭据。
type Proc struct {
	Handle  prochost.Handle `json:"handle"`
	TaskDir string          `json:"task_dir"` // 任务目录
	Port    int             `json:"port"`
}

// WSURL 返回 app-server 的 WebSocket 端点。
//
// 注意：形态由 Task 1 的 V-5 探针实测确认；若实测形态带路径，改这里一处即可。
func (p *Proc) WSURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d", p.Port)
}

// Alive 检查 codex app-server 是否仍然存活：存活锁被持有 且 WS 端口可连。
//
// 为什么第一条是锁：本地文件操作、微秒级；端口探测要走网络栈且失败要等超时。
// 锁判死就不必再探端口。
func (p *Proc) Alive() bool {
	if !prochost.Alive(p.Handle) {
		return false
	}
	return p.probePort()
}

// probePort 探测 app-server 的 WS 端口（TCP 能连上即算活）。
func (p *Proc) probePort() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port), serveProbeDialTO)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// killProcHost 是 prochost.Kill 的测试缝：SIGKILL 在类 Unix 上不可拦截，
// 真进程做不出「杀不死」的形态，回收失败路径只能靠替换它来驱动。
var killProcHost = prochost.Kill

// Kill 终止 codex app-server 及其后代（按进程组），幂等。
func (p *Proc) Kill() error { return killProcHost(p.Handle) }

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

// startProcHost 是 prochost.Start 的测试缝：测试替换它断言 spec 内容，绕开真实 shim。
var startProcHost = prochost.Start

// StartServe 经 prochost 拉起一个任务专属的 codex app-server 并等其就绪。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消
//   - repoPath: 任务工作目录（serve 的 cwd）
//   - taskID: 任务 ID（日志与 proc.json 定位）
//   - taskDir: 任务物料目录
//   - env: 注入到 app-server 进程的环境变量（B19）
//   - log: 日志入口（nil 退回 slog.Default()）
//
// 返回：就绪的 Proc；任一步失败返回错误（错误携带 serve.log 尾部）
//
// 注意：**没有 model 参数**（与 grok 不同）——codex 的模型选择是协议级的
// （thread/start 的 model 字段），不经启动描述。
func StartServe(ctx context.Context, repoPath, taskID, markRoot, taskDir string, env []string, log *slog.Logger) (*Proc, error) {
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
	bin, err := exec.LookPath("codex")
	if err != nil {
		log.Error("codex 未安装", "task", taskID, "cause", err)
		return nil, fmt.Errorf("codex 未安装: %w", err)
	}
	// 记绝对路径而不是只记「codex」：PATH 上同时装着多份 CLI 是常态
	// （nvm / homebrew / npm global 各一份），版本行为不一致时，只有这一行
	// 能回答「当时到底跑的是哪一个」。
	log.Info("解析 codex 可执行文件", "task", taskID, "bin", bin)
	selfExe, err := os.Executable()
	if err != nil {
		log.Error("取 handoff 自身路径失败（shim 无法拉起）", "task", taskID, "cause", err)
		return nil, fmt.Errorf("取自身可执行路径: %w", err)
	}
	spec := serveSpec(repoPath, taskDir, port, env)
	spec.TaskID = taskID
	spec.MarkRoot = markRoot
	spec.Argv[0] = bin
	// 写前置：proc.json 先于进程落盘，Reap 才永远有据可查
	if err := writeProcInfo(taskDir, &procInfo{
		Handle: prochost.Handle{LockPath: spec.LockPath}, Port: port,
	}); err != nil {
		log.Error("写恢复凭据失败", "task", taskID, "cause", err)
		return nil, err
	}
	log.Info("启动 codex app-server", "task", taskID, "port", port, "bin", bin, "repo", repoPath)
	handle, err := startProcHost(spec, selfExe)
	if err != nil {
		log.Error("拉起 codex app-server 失败", "task", taskID, "port", port, "cause", err)
		return nil, err
	}
	p := &Proc{Handle: handle, TaskDir: taskDir, Port: port}
	if err := writeProcInfo(taskDir, &procInfo{
		Handle: handle, Port: port,
	}); err != nil {
		log.Warn("回写恢复凭据失败，重启恢复将不可用", "task", taskID, "cause", err)
	}

	deadline := time.Now().Add(serveReadyTimeout)
	for time.Now().Before(deadline) {
		if p.probePort() {
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

// serveSpec 组 codex app-server 的启动描述。
//
// 为什么 env 必须完整透传：agentd 从非交互上下文启动，继承不到 shell 里的代理变量。
// executor 机需要代理才能连 OpenAI 时漏配的症状极具迷惑性——会话建得起来、
// 回合发得出去、handoff show 显示 running，但模型一个 token 都不产，
// 只有 serve.log 里刷 failed to refresh available models（见 README codex 章节）。
func serveSpec(repoPath, taskDir string, port int, env []string) prochost.Spec {
	serveLog := filepath.Join(taskDir, serveLogName)
	// 普通派发丢弃 CODEX_HOME，避免 env 文件把 executor 换到不含用户凭据的
	// 空 home；小队派发的 manager 已经把非空 carrier HOME 写入 env，此时载体
	// 的 CODEX_HOME 是有意的隔离凭据，必须保留。只移除这一项，未来新增的
	// dropped 键仍保持普通过滤语义。
	dropped := droppedEnvKeys
	if hasNonEmptyEnv(env, "HOME") {
		dropped = make(map[string]bool, len(droppedEnvKeys))
		for key, drop := range droppedEnvKeys {
			dropped[key] = drop
		}
		delete(dropped, "CODEX_HOME")
	}
	full := append(os.Environ(), filterEnv(env, dropped)...)
	return prochost.Spec{
		Argv:     codexServeArgv(port), // 沿用既有命令形态，原样搬运
		Dir:      repoPath,
		Env:      full,
		Stdout:   serveLog,
		Stderr:   serveLog,
		LockPath: filepath.Join(taskDir, lockFileName),
		InfoPath: filepath.Join(taskDir, procInfoFileName),
		Sentinel: true,
	}
}

// hasNonEmptyEnv 判断 manager 是否明确提供了载体 HOME。空值不触发 CODEX_HOME
// 例外，避免显式空 HOME 被误当成隔离档案；不 trim 是因为 HOME 的值必须逐字节
// 传给目标进程，字符串可能合法地含 ~ 或空格。
func hasNonEmptyEnv(env []string, key string) bool {
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if ok && name == key && value != "" {
			return true
		}
	}
	return false
}

// filterEnv 过滤掉 dropped 的 KEY=VALUE 条目，其余原样保留。
func filterEnv(env []string, dropped map[string]bool) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || dropped[k] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// codexServeArgv 组 codex app-server 的 argv（命令形态沿用旧启动脚本，原样搬运）。
func codexServeArgv(port int) []string {
	return []string{"codex", "app-server", "--listen", "ws://127.0.0.1:" + strconv.Itoa(port)}
}

// writeProcInfo 落恢复凭据（0600：与任务目录内其他文件同档）。
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

// procInfo 是 app-server 连接凭据的持久化形态，agentd 重启后凭它重建订阅。
type procInfo struct {
	Handle prochost.Handle `json:"handle"`
	Port   int             `json:"port"`
}

// ReadServeInfo 从任务目录读回 Proc，供 agentd 重启后 Resume（B18）。
func ReadServeInfo(taskDir string) (*Proc, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, procInfoFileName))
	if err != nil {
		return nil, fmt.Errorf("读恢复凭据: %w", err)
	}
	var pi procInfo
	if err := json.Unmarshal(b, &pi); err != nil {
		return nil, fmt.Errorf("解析恢复凭据: %w", err)
	}
	if pi.Handle.LockPath == "" || pi.Port == 0 {
		return nil, fmt.Errorf("恢复凭据字段不完整")
	}
	p := &Proc{Handle: pi.Handle, Port: pi.Port}
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
