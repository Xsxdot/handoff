// client.go —— agentd 侧的 Host：与进程内引擎逐字同签名，内部改为连 ptyhost socket。
//
// 职责：
//   - Open：建会话目录、写 spec、拉起 detached 的 _ptyhost、等待 socket
//   - List / Get：短连接查询 stat，拿到静态事实与活事实
//   - Write / Attach：复用订阅长连接传输 PTY 字节
//   - Close：显式发送 kill 帧；Adopt：登记启动扫描发现的活会话
//
// 边界：
//   - 不认识 PTY：不开伪终端、不解析转义序列，真正的引擎在 ptyhost 进程里
//   - 不做启动扫描：扫描由 agentd 调用 sessdir.Scan 后交给 Adopt
//   - 不删还活着的会话目录；目录收摊由 ptyhost 进程负责
//
// 为什么方法签名一个都不改：pty_api.go 与 pty_ws.go 只跟这六个方法打交道，保持
// 这些边界不变，agentd 的 HTTP 与 WebSocket 层就不需要知道进程已经移到外面。
package ptyhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
	"github.com/Xsxdot/handoff/internal/ptyhost/wire"
	"github.com/google/uuid"
)

const (
	// socketWait 是 Open 等待 ptyhost socket 出现的硬上限。
	socketWait = 3 * time.Second
	// statWait 是一次 stat 查询的硬上限。
	statWait = 1 * time.Second
	// closeWait 必须大于 engine.termGrace（2s），给 hostproc 的 Run defer 留出返回并删目录
	// 的短余量；CloseAll 与 agentd shutdown 的外层 2s 预算保持不变，超时由外层记录。
	closeWait = 3 * time.Second
	// attachmentBuffer 是客户端订阅的输出缓冲。跟不上时让网络连接背压，不能静默丢字节。
	attachmentBuffer = 256
)

// ErrProtoMismatch 表示会话由当前客户端不认识的协议版本托管。
var ErrProtoMismatch = errors.New("会话由不兼容的版本托管")

// Host 是 agentd 侧的 ptyhost 客户端。零值不可用，请用 New。
type Host struct {
	root    string
	selfExe string
	log     *slog.Logger

	mu       sync.RWMutex
	sessions map[string]*clientSession
	// credentials 只保存仍登记且曾被内核启动时刻认证过的 ptyhost；
	// ptyhostCredentialProvider 据此避免把普通同 uid 进程误扣掉。
	credentials map[string]prochost.ProcessCredential
	conns       map[string]map[*clientAttachment]struct{}
}

// clientSession 是一条可恢复的静态会话登记。活事实不缓存为真相，只在 stat 时现问。
type clientSession struct {
	meta     sessdir.Meta
	waitDone <-chan error
}

// launchSpec 是写给 _ptyhost 的启动 JSON。它与 hostproc.Spec 保持同形，但客户端不导入
// hostproc：hostproc 反过来依赖本包的公共类型，双向依赖会让父包无法编译。
type launchSpec struct {
	Root        string   `json:"root"`
	ID          string   `json:"id"`
	BasePath    string   `json:"base_path"`
	BaseKind    string   `json:"base_kind"`
	Cwd         string   `json:"cwd"`
	Shell       string   `json:"shell"`
	Env         []string `json:"env"`
	Cols        int      `json:"cols"`
	Rows        int      `json:"rows"`
	InitCommand string   `json:"init_command,omitempty"`
}

// clientAttachment 是一条 agentd 到 ptyhost 的订阅连接，同时实现 Attachment 的注入行为。
type clientAttachment struct {
	host *Host
	id   string
	conn net.Conn
	out  chan []byte

	writeMu sync.Mutex
	exitMu  sync.Mutex
	exit    *int
	closeMu sync.Once
}

// New 创建一个 ptyhost 客户端。
//
// 参数：root 是 <DataDir>/ptys 会话根目录；selfExe 是当前 handoff 可执行文件的绝对路径；
// log 是 agentd 日志入口，不能为 nil。
// 返回：一个尚未登记会话的 Host。
// 注意：New 不扫描 root，也不连接任何 socket；启动时认领由 Adopt 完成。
func New(root, selfExe string, log *slog.Logger) *Host {
	h := &Host{
		root: root, selfExe: selfExe, log: log,
		sessions:    make(map[string]*clientSession),
		credentials: make(map[string]prochost.ProcessCredential),
		conns:       make(map[string]map[*clientAttachment]struct{}),
	}
	prochost.SetPtyhostCredentialProvider(h.machineCredentials)
	return h
}

// Supported 报告本平台是否支持 PTY。
//
// 返回：Windows 与其它未实现平台为 false；Unix 为 true。
// 注意：这是编译期能力，不会为了探测能力而启动子进程。
func (h *Host) Supported() bool { return Supported() }

// Open 创建会话并拉起一个脱离 agentd 生命周期的 ptyhost 进程。
//
// 参数：opt 是 shell、cwd、环境与初始尺寸。
// 返回：成功时返回会话快照；失败时保证不会留下本次创建的会话目录。
// 注意：启动路径不阻塞等待 ptyhost；成功登记后显式 Close 保留并等待该 Open child
// 的 waitDone。
func (h *Host) Open(opt OpenOptions) (Session, error) {
	if !h.Supported() {
		return Session{}, ErrNotSupported
	}
	if h.selfExe == "" {
		return Session{}, errors.New("无法拉起 ptyhost：handoff 可执行文件路径为空")
	}
	id := uuid.NewString()
	if err := sessdir.CheckSockPath(h.root, id); err != nil {
		return Session{}, fmt.Errorf("检查新会话 socket 路径: %w", err)
	}
	if err := sessdir.Create(h.root, id); err != nil {
		return Session{}, fmt.Errorf("创建新 PTY 会话目录 %s: %w", id, err)
	}
	cleanupDir := true
	defer func() {
		if cleanupDir {
			_ = sessdir.Remove(h.root, id)
		}
	}()

	spec := launchSpec{
		Root: h.root, ID: id, BasePath: opt.BasePath, BaseKind: opt.BaseKind,
		Cwd: opt.BasePath, Shell: opt.Shell, Env: opt.Env, Cols: opt.Cols, Rows: opt.Rows,
		InitCommand: opt.InitCommand,
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return Session{}, fmt.Errorf("编码 ptyhost spec %s: %w", id, err)
	}
	specPath := filepath.Join(sessdir.Dir(h.root, id), "spec.json")
	if err := os.WriteFile(specPath, body, 0o600); err != nil {
		return Session{}, fmt.Errorf("写 ptyhost spec %s: %w", specPath, err)
	}

	cmd := exec.Command(h.selfExe, "_ptyhost", "--spec", specPath)
	configureDetached(cmd)
	if err := cmd.Start(); err != nil {
		return Session{}, fmt.Errorf("拉起 ptyhost %s: %w", id, err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	h.log.Info("ptyhost 启动请求已发出", "session", id, "pid", cmd.Process.Pid)

	deadline := time.Now().Add(socketWait)
	for time.Now().Before(deadline) {
		select {
		case waitErr := <-waitDone:
			if waitErr == nil {
				waitErr = errors.New("退出码 0")
			}
			return Session{}, fmt.Errorf("ptyhost %s 在 socket 出现前退出: %w", id, waitErr)
		default:
		}
		if _, err := os.Stat(sessdir.SockPath(h.root, id)); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(sessdir.SockPath(h.root, id)); err != nil {
		cleanupDetached(cmd, waitDone)
		return Session{}, fmt.Errorf("等待 ptyhost socket %s 超时: %w", sessdir.SockPath(h.root, id), err)
	}

	meta, err := readMetaUntil(h.root, id, deadline)
	if err != nil {
		cleanupDetached(cmd, waitDone)
		return Session{}, fmt.Errorf("读取新 ptyhost 会话元数据 %s: %w", id, err)
	}
	h.remember(meta, waitDone)
	sess, err := h.getFresh(id)
	if err != nil {
		h.forget(id)
		cleanupDetached(cmd, waitDone)
		return Session{}, fmt.Errorf("查询新 ptyhost 会话 %s: %w", id, err)
	}
	cleanupDir = false
	h.log.Info("ptyhost 会话已可用", "session", id, "pid", sess.PID, "cwd", sess.BasePath)
	return sess, nil
}

// List 返回已登记的全部会话。
//
// 返回：每条会话都保留；stat 失败时返回静态元数据与活事实零值，并记 Debug。
// 注意：查询并发进行，每条连接最多等待 statWait，避免 N 个会话串行拖慢恢复。
func (h *Host) List() []Session {
	entries := h.sessionEntries()
	out := make([]Session, len(entries))
	var wg sync.WaitGroup
	for i, entry := range entries {
		wg.Add(1)
		go func(i int, entry *clientSession) {
			defer wg.Done()
			sess := sessionFromMeta(entry.meta)
			fresh, err := h.stat(entry.meta.ID)
			if err != nil {
				h.log.Debug("查询 PTY 会话活事实失败，保留静态元数据", "session", entry.meta.ID, "err", err)
				out[i] = sess
				return
			}
			out[i] = fresh
		}(i, entry)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Get 取一个已登记会话的最新快照。
//
// 返回：第二个返回值 false 表示客户端没有登记该 id；stat 失败时仍返回静态快照与 true。
// 注意：活事实不从旧缓存猜，查询失败的字段保持其零值。
func (h *Host) Get(id string) (Session, bool) {
	entry, ok := h.session(id)
	if !ok {
		return Session{}, false
	}
	sess := sessionFromMeta(entry.meta)
	fresh, err := h.stat(id)
	if err != nil {
		h.log.Debug("查询 PTY 会话活事实失败，返回静态元数据", "session", id, "err", err)
		return sess, true
	}
	return fresh, true
}

// Write 把按键写进会话。
//
// 参数：id 是会话 id；p 是 PTY 原始字节。
// 返回：写入 socket 或 ptyhost 失败时报错。
// 注意：优先复用该会话已有的订阅连接；没有订阅时只建立一条短连接。
func (h *Host) Write(id string, p []byte) error {
	if _, ok := h.session(id); !ok {
		return ErrNoSession
	}
	for _, att := range h.attachments(id) {
		if err := att.writeData(p); err == nil {
			return nil
		}
	}
	conn, err := h.dial(id, statWait)
	if err != nil {
		return fmt.Errorf("连接 PTY 会话 %s 写入: %w", id, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(statWait)); err != nil {
		return fmt.Errorf("设置 PTY 会话 %s 写入超时: %w", id, err)
	}
	if err := wire.WriteData(conn, p); err != nil {
		return fmt.Errorf("写入 PTY 会话 %s: %w", id, err)
	}
	return nil
}

// Close 显式发送 kill 并等待 ptyhost/PTY 收摊后摘除本地登记。
//
// 参数：id 是已登记会话 id。
// 返回：控制写失败、控制读非 EOF/timeout、ptyhost 退出失败或等待超时均返回错误。
// 注意：EOF/timeout 不是收摊事实；只有 cmd_wait 或 session_dir wait 成功才可返回 nil。
func (h *Host) Close(id string) error {
	started := time.Now()
	entry, ok := h.session(id)
	if !ok {
		h.log.Warn("关闭 PTY 会话：登记不存在", "session", id, "pid", 0, "wait_path", "unknown",
			"phase", "lookup", "elapsed", time.Since(started), "cause", ErrNoSession)
		return ErrNoSession
	}
	waitPath := "session_dir"
	if entry.waitDone != nil {
		waitPath = "cmd_wait"
	}
	conn, err := h.dial(id, statWait)
	if err != nil {
		h.log.Warn("关闭 PTY 会话：连接失败", "session", id, "pid", entry.meta.PID, "wait_path", waitPath,
			"phase", "dial", "elapsed", time.Since(started), "cause", err)
		return fmt.Errorf("连接 PTY 会话 %s 关闭: %w", id, err)
	}
	if err := conn.SetDeadline(time.Now().Add(statWait)); err != nil {
		_ = conn.Close()
		h.log.Warn("关闭 PTY 会话：设置 deadline 失败", "session", id, "pid", entry.meta.PID, "wait_path", waitPath,
			"phase", "control_deadline", "elapsed", time.Since(started), "cause", err)
		return fmt.Errorf("设置 PTY 会话 %s 关闭超时: %w", id, err)
	}
	if err := wire.WriteControl(conn, wire.Control{Type: wire.CtrlKill}); err != nil {
		_ = conn.Close()
		h.log.Error("关闭 PTY 会话：发送 kill 失败", "session", id, "pid", entry.meta.PID, "wait_path", waitPath,
			"phase", "send_kill", "elapsed", time.Since(started), "cause", err)
		return fmt.Errorf("发送 PTY 会话 %s kill: %w", id, err)
	}
	_, _, _, controlErr := wire.ReadFrame(conn)
	_ = conn.Close()
	if controlErr != nil {
		h.log.Warn("关闭 PTY 会话：未收到 control ack，继续等待进程事实", "session", id,
			"pid", entry.meta.PID, "wait_path", waitPath, "phase", "control_ack",
			"elapsed", time.Since(started), "cause", controlErr)
	}
	waitErr := h.waitPtyhostExit(entry, time.Now().Add(closeWait))
	h.forget(id)
	if waitErr != nil {
		h.log.Error("关闭 PTY 会话：等待收摊失败", "session", id, "pid", entry.meta.PID,
			"wait_path", waitPath, "phase", "wait", "elapsed", time.Since(started), "control_error", controlErr, "cause", waitErr)
		return fmt.Errorf("等待 PTY 会话 %s 收摊: %w", id, waitErr)
	}
	if controlErr != nil && !errors.Is(controlErr, io.EOF) && !isTimeout(controlErr) {
		h.log.Error("关闭 PTY 会话：control ack 失败", "session", id, "pid", entry.meta.PID,
			"wait_path", waitPath, "phase", "control_ack", "elapsed", time.Since(started), "cause", controlErr)
		return fmt.Errorf("等待 PTY 会话 %s 收摊控制帧: %w", id, controlErr)
	}
	h.log.Info("ptyhost 会话已按请求关闭", "session", id, "pid", entry.meta.PID,
		"wait_path", waitPath, "phase", "complete", "elapsed", time.Since(started), "control_error", controlErr)
	return nil
}

// Attach 建立一条长期订阅连接。
//
// 参数：id 是会话 id；since 是环形缓冲的输出水位。
// 返回：带历史回放与实时输出通道的 Attachment。
// 注意：协议版本先从 meta.json 检查；Backlog 在返回前已从 attached 后的第一数据帧取出。
func (h *Host) Attach(id string, since uint64) (*Attachment, error) {
	entry, ok := h.session(id)
	if !ok {
		return nil, ErrNoSession
	}
	if entry.meta.ProtoVersion != wire.ProtoVersion {
		return nil, fmt.Errorf("会话 %s 由 v%d 托管，本版（v%d）接不进来: %w", id,
			entry.meta.ProtoVersion, wire.ProtoVersion, ErrProtoMismatch)
	}
	conn, err := h.dial(id, socketWait)
	if err != nil {
		return nil, fmt.Errorf("连接 PTY 会话 %s 订阅: %w", id, err)
	}
	closeConn := true
	defer func() {
		if closeConn {
			_ = conn.Close()
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(socketWait)); err != nil {
		return nil, fmt.Errorf("设置 PTY 会话 %s 订阅超时: %w", id, err)
	}
	if err := wire.WriteControl(conn, wire.Control{Type: wire.CtrlAttach, Since: since}); err != nil {
		return nil, fmt.Errorf("发送 PTY 会话 %s attach: %w", id, err)
	}
	kind, _, ctrl, err := wire.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("读取 PTY 会话 %s attached: %w", id, err)
	}
	if kind != wire.KindControl || ctrl == nil || ctrl.Type != wire.CtrlAttached {
		return nil, fmt.Errorf("PTY 会话 %s 首帧不是 attached", id)
	}
	if ctrl.ProtoVersion != wire.ProtoVersion {
		return nil, fmt.Errorf("会话 %s 返回 v%d，本版为 v%d: %w", id, ctrl.ProtoVersion,
			wire.ProtoVersion, ErrProtoMismatch)
	}
	kind, backlog, _, err := wire.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("读取 PTY 会话 %s 回放: %w", id, err)
	}
	if kind != wire.KindData {
		return nil, fmt.Errorf("PTY 会话 %s attached 后未收到数据回放帧", id)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("清除 PTY 会话 %s 订阅超时: %w", id, err)
	}
	a := &clientAttachment{host: h, id: id, conn: conn, out: make(chan []byte, attachmentBuffer)}
	h.addAttachment(a)
	closeConn = false
	go a.readLoop()
	h.log.Info("ptyhost 会话已订阅", "session", id, "since", ctrl.Since,
		"backlog_bytes", len(backlog), "truncated", ctrl.Truncated)
	return NewAttachment(backlog, ctrl.Since, ctrl.Truncated, a.out, a), nil
}

// Adopt 登记启动扫描发现的活会话。
//
// 参数：entries 是 sessdir.Scan 的结果；只有 StateLive 会被登记，broken 与 dead 由调用方处理。
// 返回：无。已有同 id 登记会被新的静态元数据覆盖。
// 注意：Adopt 不连接 socket、不验证协议、不删除目录；它是启动路径的纯登记动作。
func (h *Host) Adopt(entries []sessdir.Entry) {
	h.mu.Lock()
	adopted := 0
	for _, entry := range entries {
		if entry.State != sessdir.StateLive {
			continue
		}
		h.sessions[entry.ID] = &clientSession{meta: entry.Meta, waitDone: nil}
		if credential, ok := prochost.ProcessCredentialForPID(entry.Meta.PID); ok {
			h.credentials[entry.ID] = credential
		} else {
			delete(h.credentials, entry.ID)
		}
		adopted++
	}
	h.mu.Unlock()
	h.log.Info("ptyhost 会话已登记", "count", adopted)
}

func (h *Host) dial(id string, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", sessdir.SockPath(h.root, id), timeout)
	if err != nil {
		return nil, fmt.Errorf("连接会话 socket %s: %w", sessdir.SockPath(h.root, id), err)
	}
	return conn, nil
}

func (h *Host) stat(id string) (Session, error) {
	entry, ok := h.session(id)
	if !ok {
		return Session{}, ErrNoSession
	}
	conn, err := h.dial(id, statWait)
	if err != nil {
		return Session{}, fmt.Errorf("查询会话 %s: %w", id, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(statWait)); err != nil {
		return Session{}, fmt.Errorf("设置会话 %s 查询超时: %w", id, err)
	}
	if err := wire.WriteControl(conn, wire.Control{Type: wire.CtrlStat}); err != nil {
		return Session{}, fmt.Errorf("发送会话 %s stat: %w", id, err)
	}
	kind, _, ctrl, err := wire.ReadFrame(conn)
	if err != nil {
		return Session{}, fmt.Errorf("读取会话 %s stat: %w", id, err)
	}
	if kind != wire.KindControl || ctrl == nil || ctrl.Type != wire.CtrlStatResp {
		return Session{}, fmt.Errorf("会话 %s stat 响应类型错误", id)
	}
	sess := sessionFromMeta(entry.meta)
	sess.Cols, sess.Rows, sess.Attached = ctrl.Cols, ctrl.Rows, ctrl.Attached
	sess.BytesOut, sess.Foreground, sess.ExitCode = ctrl.BytesOut, ctrl.Foreground, ctrl.ExitCode
	return sess, nil
}

func (h *Host) getFresh(id string) (Session, error) {
	deadline := time.Now().Add(statWait)
	var last error
	for time.Now().Before(deadline) {
		sess, err := h.stat(id)
		if err == nil {
			return sess, nil
		}
		last = err
		time.Sleep(10 * time.Millisecond)
	}
	if last == nil {
		last = errors.New("未收到 stat 响应")
	}
	return Session{}, fmt.Errorf("stat 超时: %w", last)
}

func (h *Host) session(id string) (*clientSession, bool) {
	h.mu.RLock()
	entry, ok := h.sessions[id]
	h.mu.RUnlock()
	return entry, ok
}

func (h *Host) sessionEntries() []*clientSession {
	h.mu.RLock()
	out := make([]*clientSession, 0, len(h.sessions))
	for _, entry := range h.sessions {
		copyEntry := *entry
		out = append(out, &copyEntry)
	}
	h.mu.RUnlock()
	return out
}

// waitPtyhostExit 等待本次 Close 触发的 ptyhost 真正退出。
//
// Open 的 waitDone 是唯一 child Wait 结果；Adopt 没有 child Wait，只能把 hostproc
// defer 的 sessdir.Remove 作为完成事实。deadline 由 Host.Close 统一提供。
func (h *Host) waitPtyhostExit(entry *clientSession, deadline time.Time) error {
	waitPath := "session_dir"
	if entry.waitDone != nil {
		waitPath = "cmd_wait"
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("等待 ptyhost %s(pid=%d) 退出超时: wait_path=%s", entry.meta.ID, entry.meta.PID, waitPath)
		}
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case err := <-entry.waitDone:
			if err != nil {
				return fmt.Errorf("ptyhost %s(pid=%d) 退出失败: wait_path=%s: %w", entry.meta.ID, entry.meta.PID, waitPath, err)
			}
			return nil
		case <-timer.C:
			return fmt.Errorf("等待 ptyhost %s(pid=%d) 退出超时: wait_path=%s", entry.meta.ID, entry.meta.PID, waitPath)
		}
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := os.Stat(sessdir.Dir(h.root, entry.meta.ID))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("检查 ptyhost %s(pid=%d) 会话目录 %s: wait_path=%s: %w",
				entry.meta.ID, entry.meta.PID, sessdir.Dir(h.root, entry.meta.ID), waitPath, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 ptyhost %s(pid=%d) 会话目录消失超时: path=%s wait_path=%s",
				entry.meta.ID, entry.meta.PID, sessdir.Dir(h.root, entry.meta.ID), waitPath)
		}
		<-ticker.C
	}
}

// remember 登记静态元数据与 Open 子进程的唯一 Wait 通道。
//
// waitDone 只对当前进程直接启动的 Open 有值；Adopt 的跨进程会话必须传 nil，
// 由 waitPtyhostExit 观察会话目录，而不是错误地对非 child 调 exec.Cmd.Wait。
func (h *Host) remember(meta sessdir.Meta, waitDone <-chan error) {
	credential, credentialOK := prochost.ProcessCredentialForPID(meta.PID)
	h.mu.Lock()
	h.sessions[meta.ID] = &clientSession{meta: meta, waitDone: waitDone}
	if credentialOK {
		h.credentials[meta.ID] = credential
	} else {
		delete(h.credentials, meta.ID)
	}
	h.mu.Unlock()
}

func (h *Host) forget(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	delete(h.credentials, id)
	attachments := h.conns[id]
	delete(h.conns, id)
	h.mu.Unlock()
	for att := range attachments {
		att.close()
	}
}

// machineCredentials 返回仍登记的 ptyhost 凭据快照。
//
// 返回：调用方可安全持有的凭据副本。
// 注意：凭据不是按进程名推断出来的；它们只来自当前 Host 对 StateLive 会话的登记，
// 并且每条都在登记时与内核启动时刻核对过。快照不含无法核对的进程。
func (h *Host) machineCredentials() []prochost.ProcessCredential {
	h.mu.RLock()
	out := make([]prochost.ProcessCredential, 0, len(h.credentials))
	for _, credential := range h.credentials {
		out = append(out, credential)
	}
	h.mu.RUnlock()
	return out
}

func (h *Host) addAttachment(att *clientAttachment) {
	h.mu.Lock()
	if h.conns[att.id] == nil {
		h.conns[att.id] = make(map[*clientAttachment]struct{})
	}
	h.conns[att.id][att] = struct{}{}
	h.mu.Unlock()
}

func (h *Host) removeAttachment(att *clientAttachment) {
	h.mu.Lock()
	if set := h.conns[att.id]; set != nil {
		delete(set, att)
		if len(set) == 0 {
			delete(h.conns, att.id)
		}
	}
	h.mu.Unlock()
}

func (h *Host) attachments(id string) []*clientAttachment {
	h.mu.RLock()
	set := h.conns[id]
	out := make([]*clientAttachment, 0, len(set))
	for att := range set {
		out = append(out, att)
	}
	h.mu.RUnlock()
	return out
}

func (a *clientAttachment) readLoop() {
	defer close(a.out)
	defer a.host.removeAttachment(a)
	defer a.conn.Close()
	for {
		kind, data, ctrl, err := wire.ReadFrame(a.conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				a.host.log.Debug("ptyhost 订阅读取结束", "session", a.id, "err", err)
			}
			return
		}
		if kind == wire.KindData {
			a.out <- data
			continue
		}
		if kind == wire.KindControl && ctrl != nil && ctrl.Type == wire.CtrlExit {
			a.exitMu.Lock()
			if ctrl.ExitCode != nil {
				code := *ctrl.ExitCode
				a.exit = &code
			}
			a.exitMu.Unlock()
			return
		}
	}
}

func (a *clientAttachment) Detach() {
	a.closeMu.Do(func() {
		a.host.log.Info("ptyhost 会话订阅已 detach", "session", a.id)
		a.host.removeAttachment(a)
		_ = a.conn.Close()
	})
}

func (a *clientAttachment) close() {
	a.closeMu.Do(func() { _ = a.conn.Close() })
}

func (a *clientAttachment) ExitCode() *int {
	a.exitMu.Lock()
	defer a.exitMu.Unlock()
	if a.exit == nil {
		return nil
	}
	code := *a.exit
	return &code
}

func (a *clientAttachment) Resize(cols, rows int) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if err := wire.WriteControl(a.conn, wire.Control{Type: wire.CtrlResize, Cols: cols, Rows: rows}); err != nil {
		return fmt.Errorf("发送 PTY 会话 %s resize: %w", a.id, err)
	}
	return nil
}

func (a *clientAttachment) writeData(p []byte) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if err := wire.WriteData(a.conn, p); err != nil {
		return fmt.Errorf("复用 PTY 会话 %s 订阅写入: %w", a.id, err)
	}
	return nil
}

func sessionFromMeta(m sessdir.Meta) Session {
	return Session{ID: m.ID, BasePath: m.BasePath, BaseKind: m.BaseKind, Shell: m.Shell,
		CreatedAt: m.CreatedAt, PID: m.PID, Incompatible: m.ProtoVersion != wire.ProtoVersion}
}

func readMetaUntil(root, id string, deadline time.Time) (sessdir.Meta, error) {
	var last error
	for time.Now().Before(deadline) {
		meta, err := sessdir.ReadMeta(root, id)
		if err == nil {
			return meta, nil
		}
		last = err
		time.Sleep(10 * time.Millisecond)
	}
	return sessdir.Meta{}, fmt.Errorf("等待 meta.json: %w", last)
}

func cleanupDetached(cmd *exec.Cmd, waitDone <-chan error) {
	select {
	case <-waitDone:
		return
	default:
	}
	if cmd.Process != nil {
		_ = killDetached(cmd)
	}
	select {
	case <-waitDone:
	case <-time.After(statWait):
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
