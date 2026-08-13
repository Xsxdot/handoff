// proc.go —— opencode serve 进程生命周期管理。
//
// 职责：
//   - 组 `opencode serve --port <随机空闲端口> --hostname 127.0.0.1` 的 argv，
//     经 prochost 以 detached 方式拉起（shim 承载，见 prochost 包）
//   - 密码与配置经 Spec.Env 注入（OPENCODE_SERVER_PASSWORD / OPENCODE_CONFIG），
//     argv 不含任何秘密（why 见 serveSpec）
//   - serve 输出落盘 <taskDir>/serve.log：serve 死亡诊断的持久 stderr
//   - 就绪探测（轮询 GET /）、存活检查（存活锁 + HTTP 探活）、销毁（按进程组 Kill）
//
// 边界：
//   - 不触碰会话：会话的创建、prompt、权限应答由 api.go 完成，本文件只保证
//     「serve 进程活着、端口可用」
//   - 不生成任务级配置（OPENCODE_CONFIG 指向的文件由 Task 10 生成）
//
// 为什么进程经 prochost 而不是 agentd 直接 fork：agentd 重启或崩溃时子进程
// 树会被一并回收，正在执行的任务会无辜中断；prochost 的 shim 以新会话拉起并
// 持有存活锁，生命周期与 agentd 解耦——agentd 重启后靠 Alive() 探测发现存活
// 并重连 SSE。实况观测走 agentd 的 render 流式 endpoint（handoff attach）。
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
	"strconv"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/prochost"
)

// serveReadyTimeout 是 StartServe 等待 serve 就绪的总超时。
const serveReadyTimeout = 10 * time.Second

// protectedEnvKeys 是 handoff 自身注入、不容 env 文件覆盖的变量。
//
// 命中时不静默忽略用户写的行——注入顺序保证 handoff 的变量排在后面因而胜出，
// 同时打 WARN 让用户知道自己那行没生效。
var protectedEnvKeys = map[string]bool{
	"OPENCODE_SERVER_PASSWORD": true,
	"OPENCODE_CONFIG":          true,
}

// serveProbeInterval 是就绪/存活探测的轮询间隔。
const serveProbeInterval = 200 * time.Millisecond

// 任务目录内的执行器物料文件名（目录本身 0700，见 manager 创建处）。
const (
	serveLogFileName  = "serve.log"  // serve 输出持久副本（P1-8 诊断来源）
	renderLogFileName = "render.log" // 模型回合文本增量（render 流式 endpoint 的数据源）
	procInfoFileName  = "proc.json"  // 恢复凭据：prochost.Handle / port / password
	lockFileName      = "proc.lock"  // shim 存活锁（prochost.Alive 的唯一判据）
)

// Proc 描述一个运行中的 opencode serve 进程。
//
// 字段说明：
//   - Handle: prochost 句柄（shim pid + 存活锁路径），存活与回收都靠它
//   - Port: serve 监听的端口（127.0.0.1 上）
//   - Password: 随机生成的 OPENCODE_SERVER_PASSWORD，api.go 的 basic auth 用它
//   - ServeLogPath: serve 输出日志路径（<taskDir>/serve.log），serve 死后诊断只能读它
type Proc struct {
	Handle       prochost.Handle
	Port         int
	Password     string
	ServeLogPath string
}

// startServe 是 StartServe 的测试缝：冷恢复测试替换它断言「起 serve」是否被调用，
// 绕开真实 shim + opencode 二进制。
var startServe = StartServe

// startProcHost 是 prochost.Start 的测试缝：测试替换它断言 spec 内容，
// 绕开真实 shim。
var startProcHost = prochost.Start

// StartServe 经 prochost 拉起 opencode serve 并等待就绪。
//
// 参数：
//   - ctx: 上下文；就绪轮询同样受 ctx 取消影响
//   - repoPath: 任务仓库路径，作为 serve 的工作目录（cwd）
//   - taskID: 任务 id，用于日志与任务目录定位
//   - taskDir: 任务目录（0700），serve.log 与 proc.json 都放这里
//   - configPath: 任务级 opencode 配置路径（Task 10 生成），注入 OPENCODE_CONFIG
//   - env: 额外注入的环境变量（形如 KEY=VALUE，来自 env 文件，已解析已展开）；
//     覆盖顺序见 serveSpec 的 why
//   - log: 本模块日志入口（StartServe 是进程启动点，日志需要显式传入而非走默认）
//
// 返回：
//   - 就绪的 Proc；就绪 = 端口上已有 HTTP 服务响应（含 401：密码校验属后续请求的事，
//     这里只关心「serve 进程起来且 HTTP 层可应答」）
//   - 错误：取端口/密码失败、写 proc.json 失败、拉起 shim 失败、10s 内未就绪
//     （错误信息携带 serve.log 尾部的 serve stderr）
//
// 注意：
//   - 端口选择存在 TOCTOU 竞态（见 freePort），MVP 接受
//   - 就绪超时后自动 Kill 清理残留进程，避免半启动进程占着端口
func StartServe(ctx context.Context, repoPath, taskID, taskDir, configPath string, env []string, log *slog.Logger) (*Proc, error) {
	l := log.With("task", taskID)
	port, err := freePort()
	if err != nil {
		l.Error("获取随机空闲端口失败", "cause", err)
		return nil, fmt.Errorf("获取空闲端口: %w", err)
	}
	password, err := randomPassword()
	if err != nil {
		l.Error("生成 serve 密码失败", "cause", err)
		return nil, fmt.Errorf("生成 serve 密码: %w", err)
	}

	// env 注入（B19）：只打 key 名不打值——值里可能带凭据（如 http://user:pass@host）
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for _, kv := range env {
			k, _, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			keys = append(keys, k)
			if protectedEnvKeys[k] {
				l.Warn("env 文件定义了 handoff 保留变量，将被 handoff 自身注入覆盖",
					"key", k)
			}
		}
		l.Info("注入 env 变量到 serve 进程", "keys", keys, "count", len(keys))
	}
	bin, err := exec.LookPath("opencode")
	if err != nil {
		l.Error("opencode 未安装", "cause", err)
		return nil, fmt.Errorf("opencode 未安装: %w", err)
	}
	// 记绝对路径而不是只记「opencode」：PATH 上同时装着多份 CLI 是常态
	// （nvm / homebrew / npm global 各一份），版本行为不一致时，只有这一行
	// 能回答「当时到底跑的是哪一个」。
	l.Info("解析 opencode 可执行文件", "bin", bin)
	selfExe, err := os.Executable()
	if err != nil {
		l.Error("取 handoff 自身路径失败（shim 无法拉起）", "cause", err)
		return nil, fmt.Errorf("取自身可执行路径: %w", err)
	}
	spec := serveSpec(repoPath, taskDir, configPath, port, password, env)
	spec.Argv[0] = bin
	// 写前置：proc.json 先于进程落盘，Reap 才永远有据可查
	if err := writeProcInfo(taskDir, &procInfo{
		Handle: prochost.Handle{LockPath: spec.LockPath}, Port: port, Password: password,
	}); err != nil {
		l.Error("写恢复凭据失败", "cause", err)
		return nil, err
	}
	l.Info("启动 opencode serve", "port", port, "bin", bin, "repo", repoPath)
	handle, err := startProcHost(spec, selfExe)
	if err != nil {
		l.Error("拉起 opencode serve 失败", "port", port, "cause", err)
		return nil, err
	}
	p := &Proc{Handle: handle, Port: port, Password: password,
		ServeLogPath: filepath.Join(taskDir, serveLogFileName)}
	if err := writeProcInfo(taskDir, &procInfo{
		Handle: handle, Port: port, Password: password,
	}); err != nil {
		l.Warn("回写恢复凭据失败，重启恢复将不可用", "cause", err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, serveReadyTimeout)
	defer cancel()

	start := time.Now()
	for {
		if p.probeHTTP() {
			l.Info("opencode serve 就绪", "port", port, "shim_pid", handle.PID,
				"ready_ms", time.Since(start).Milliseconds())
			return p, nil
		}
		select {
		case <-readyCtx.Done():
			// 就绪超时：读 serve.log 尾部（serve 的 stderr）带进错误，这是
			// 「为什么没起来」的第一手证据（如端口被占、config 解析失败）。
			stderrTail := serveLogTail(p.ServeLogPath)
			l.Error("opencode serve 就绪超时", "port", port, "shim_pid", handle.PID,
				"stderr_tail", stderrTail)
			_ = p.Kill() // 清理残留，避免半启动进程占着端口
			return nil, fmt.Errorf("opencode serve 就绪超时（10s）: %s", stderrTail)
		case <-time.After(serveProbeInterval):
		}
	}
}

// serveSpec 组 opencode serve 的启动描述。
//
// 为什么密码走 Env 而不是 argv：进程 argv 本机全局可读（/proc/<pid>/cmdline），
// 密码进 argv 等于对同机任何用户公开。旧实现靠「密码写进 0600 启动脚本、
// 只把脚本路径给 tmux」达成同样效果；换成 prochost 后由 Spec.Env 承担
// （spec.json 同样 0600）。TestServeSpecPutsPasswordInEnvNotArgv 钉死这条。
//
// 为什么 handoff 注入的变量排在 env 文件之后：切片靠后者覆盖前者，
// 用户 env 文件里若定义了 OPENCODE_* 保留键，必须被 handoff 自己的值压过
// （B19 protectedEnvKeys 纪律，调用方另有 Warn 提示）。
func serveSpec(repoPath, taskDir, configPath string, port int, password string, env []string) prochost.Spec {
	serveLog := filepath.Join(taskDir, serveLogFileName)
	full := append(os.Environ(), env...)
	full = append(full,
		"OPENCODE_SERVER_PASSWORD="+password,
		"OPENCODE_CONFIG="+configPath,
	)
	return prochost.Spec{
		Argv:     []string{"opencode", "serve", "--port", strconv.Itoa(port), "--hostname", "127.0.0.1"},
		Dir:      repoPath,
		Env:      full,
		Stdout:   serveLog,
		Stderr:   serveLog, // serve 的 stdout/stderr 合并落一份，与旧 tee -a 行为一致
		LockPath: filepath.Join(taskDir, lockFileName),
		InfoPath: filepath.Join(taskDir, procInfoFileName),
		Sentinel: true,
	}
}

// Alive 检查 serve 是否仍然存活：存活锁被持有 且 端口有 HTTP 应答。
//
// 两者缺一即视为死亡。锁证明 shim 还在，HTTP 证明 serve 本身还在应答——
// serve 崩了但 shim 尚未收尸的窗口由 HTTP 这条兜住。
func (p *Proc) Alive() bool {
	if !prochost.Alive(p.Handle) {
		return false
	}
	return p.probeHTTP()
}

// Kill 终止 serve 及其后代（按进程组），幂等。
func (p *Proc) Kill() error { return prochost.Kill(p.Handle) }

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

// serveLogTailBytes 是 serve.log 尾部读取的字节上限（诊断信息取末尾 500 字节，
// 多读一些余量以便 tail 按完整行截断）。
const serveLogTailBytes = 4 << 10

// serveLogTail 读 serve.log 尾部最多 500 字节，供就绪超时/死亡诊断；文件未
// 创建（serve 根本没跑起来）或已被清理时返回空串。
//
// why（Seek 到尾部而非 os.ReadFile 整读）：serve.log 由 serve 写满任务全程且
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

// procInfo 是 serve 进程连接凭据的持久化形态，agentd 重启后凭它重建订阅。
//
// LastTurnMsgID 是对账水位（B38）：最后一条**已被翻译成终态事件**的 assistant
// 消息 id。断连恢复后拿它与会话尾部比对——不同即说明有一个已完结的回合没被
// 消费，需要补发。
//
// WatermarkArmed 是水位可信标记（B38 订正），语义：**这个会话是本版本 agentd
// 亲手新建的**，因此「水位为空」可信地表示「尚无任何回合结束」——对账见到
// armed + 空水位 + 尾部已完结就必须补发，这正是 B38 的头号现场（任务的第一
// 个回合死在断连窗口里，水位天然为空，旧判定会把它误当「存量任务升级」吞掉）。
//
// 反例（为什么不能按「这个任务在被维护」理解）：多回合任务的回合 1 终态已正常
// 送达、任务进 waiting_review，用户 continue 后任务回 running、回合 2 刚发出
// prompt 尚未产出 assistant 消息——此时会话尾部仍是回合 1 那条 completed 消息。
// armed 的任务不会踩到（水位 == 回合 1 的 msg.ID，走「已送达过」），但 legacy
// 任务水位是空的、分辨不了，会把它当丢失补发一遍。故 legacy 任务必须保持 unarmed。
//
// 什么时候置 true：
//   - 新建会话成功时（Start 的 startRun 建新会话那一刻）——会话全新，「空水位」
//     无歧义
//   - fresh 模式 Resume（新会话 + 水位清零）——同上
//
// 什么时候不动它：reattach / cold-recovery 沿用盘上已有的值。老任务因此保持
// unarmed、升级保护完整；本版本起出生的任务一出生即 armed，不受影响。
//
// 为什么用消息 id 而不是时间戳：一个断连窗口内至多跨越一个回合边界（spec §2.2，
// 因为新回合只能由经过 agentd 的 Start/Send 发起），因此「id 不同」就无歧义地
// 等于「有新的已完结回合」，不需要任何时间序假设。
type procInfo struct {
	Handle         prochost.Handle `json:"handle"`
	Port           int             `json:"port"`
	Password       string          `json:"password"`
	LastTurnMsgID  string          `json:"last_turn_msg_id,omitempty"`
	WatermarkArmed bool            `json:"watermark_armed,omitempty"`
}

// writeProcInfo 把 serve 连接凭据写入任务目录 proc.json（0600）。
//
// why（必须持久化）：agentd 重启后内存中的 Proc（端口/密码）丢失，而 shim 内的
// serve 进程独立存活；RecoverOnStartup 凭此文件探活并重建 SSE 订阅（spec §8）。
// 写失败不阻断启动（adapter.Start 只 Warn），缺失时该任务重启后按「执行器已不在」
// 转 failed 交协调者——保守胜于静默丢事件。
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

// readProcInfo 读取任务目录的 serve 连接凭据。
//
// 返回：
//   - 文件缺失/损坏/字段不完整时返回错误（调用方据此判「无法重建订阅」）
func readProcInfo(taskDir string) (*procInfo, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, procInfoFileName))
	if err != nil {
		return nil, fmt.Errorf("读恢复凭据 %s: %w", procInfoFileName, err)
	}
	var pi procInfo
	if err := json.Unmarshal(b, &pi); err != nil {
		return nil, fmt.Errorf("解析恢复凭据 %s: %w", procInfoFileName, err)
	}
	// 水位与 armed 标记不进完整性校验：它们是 B38 新加的字段，存量 proc.json
	// 里没有。若把它们算进「字段不完整」，agentd 升级后所有在跑的任务会一起被判死
	if pi.Handle.LockPath == "" || pi.Port == 0 || pi.Password == "" {
		return nil, fmt.Errorf("恢复凭据 %s 字段不完整", procInfoFileName)
	}
	return &pi, nil
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

// tail 返回字符串尾部最多 n 个字符（按字节截断，日志用，不追求多字节安全——
// 截断点切在中间也无碍诊断）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
