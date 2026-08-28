// Package engine 托管伪终端（PTY）会话：开 shell、持有会话表、维护回放缓冲、
// 向多个订阅者广播输出、按进程组终止。
//
// 职责：
//   - 会话的完整生命周期：创建、写入、订阅、调尺寸、显式关闭、自然退出
//   - 每会话一个环形缓冲，支撑断线重连的 since 续传
//   - 多方接入时的尺寸协商（取所有订阅者中的最小值）
//
// 边界：
//   - 不认识 HTTP / WebSocket / JSON，也不认识 agentd 的任务模型
//   - 不做鉴权，不做 base_path 白名单校验（那是 agentd 接口层的参数校验）
//   - 不落盘：会话表只在内存里，随 agentd 生死（spec §3.1、§10）
//   - 不解析终端转义序列，只搬字节
package engine

import (
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/google/uuid"
)

const (
	// ringSize 是每会话的回放缓冲大小。256 KiB 够装满屏幕若干次滚动，
	// 又不至于让几十个会话把内存吃光。
	ringSize = 256 << 10
	// maxSubscribers 是单会话的订阅者上限。超出拒绝而不是静默丢弃（spec §3.3）。
	maxSubscribers = 8
	// subscriberBuffer 是单个订阅者的积压上限。写满说明这个客户端跟不上，
	// 此时**关掉它**而不是阻塞广播——它带着 since 重连即可从环里补齐，
	// 这正是环形缓冲存在的意义。
	subscriberBuffer = 256
	// termGrace 是 SIGTERM 到 SIGKILL 的宽限期。
	termGrace = 2 * time.Second
	// initCommandReadyWait 是等 shell 出第一个字节的上限。
	//
	// 到点仍没动静就照写不误：内核的 PTY 输入缓冲一直在，字节不会丢，
	// 最坏情况只是命令排在 shell 的 rc 链之后被读到。**超时不是失败**——
	// 把它当失败就意味着「rc 链慢的机器上启动项静默不工作」。
	initCommandReadyWait = 3 * time.Second
	// readChunk 是单次从 PTY 主端读取的上限。
	readChunk = 32 << 10
)

// Engine 是一个进程内 PTY 引擎的会话持有者。零值不可用，请用 New。
type Engine struct {
	log  *slog.Logger
	mu   sync.Mutex
	sess map[string]*session
}

// New 创建一个 Engine。参数 log 用于记录会话生命周期与错误，不得为 nil。
func New(log *slog.Logger) *Engine {
	return &Engine{log: log, sess: map[string]*session{}}
}

// Supported 报告引擎所在编译目标是否支持 PTY。
func (h *Engine) Supported() bool { return ptySupported }

type subscriber struct {
	ch   chan []byte
	cols int // 0 = 该订阅者还没报过尺寸，不参与最小值计算
	rows int
}

type session struct {
	mu         sync.Mutex
	meta       ptyhost.Session
	f          *os.File
	cmd        *exec.Cmd
	buf        *ring
	subs       map[*subscriber]struct{}
	exited     bool
	exitCode   *int
	exitedDone chan struct{}
	// firstOut 在 shell 吐出第一个字节时关闭，是「shell 就绪」的唯一观测点。
	// 只有带 InitCommand 的会话才有人等它；不带的会话也照常关，代价是一次
	// sync.Once（比在 pump 的热路径上加一个 if 判空更简单）。
	firstOut     chan struct{}
	firstOutOnce sync.Once
}

// Open 起一个新会话。失败时不留残骸。
func (h *Engine) Open(opt ptyhost.OpenOptions) (ptyhost.Session, error) {
	if !ptySupported {
		return ptyhost.Session{}, ptyhost.ErrNotSupported
	}
	if opt.Cols <= 0 {
		opt.Cols = 80
	}
	if opt.Rows <= 0 {
		opt.Rows = 24
	}
	f, cmd, err := startPty(opt.Shell, opt.BasePath, opt.Env, opt.Cols, opt.Rows)
	if err != nil {
		// shell 起不来是最常见的失败（$SHELL 指向不存在的路径、cwd 被删）。
		// 带齐 shell 与 cwd，否则这行日志无法定位。
		h.log.Error("开终端会话失败", "shell", opt.Shell, "cwd", opt.BasePath, "err", err)
		return ptyhost.Session{}, err
	}
	s := &session{
		meta: ptyhost.Session{
			ID: uuid.NewString(), BasePath: opt.BasePath, BaseKind: opt.BaseKind,
			Shell: opt.Shell, CreatedAt: time.Now(),
			Cols: opt.Cols, Rows: opt.Rows, PID: cmd.Process.Pid,
		},
		f: f, cmd: cmd, buf: newRing(ringSize), subs: map[*subscriber]struct{}{},
		exitedDone: make(chan struct{}), firstOut: make(chan struct{}),
	}
	h.mu.Lock()
	h.sess[s.meta.ID] = s
	total := len(h.sess)
	h.mu.Unlock()

	go h.pump(s)
	go h.reap(s)
	// 启动命令走 PTY **输入**，不进 argv：argv 会把 login shell 变成非交互
	// shell，命令退出即会话结束（见 OpenOptions.InitCommand 的注释）。
	// 另起 goroutine 是因为它要等 shell 就绪，而 Open 不能为此阻塞——
	// 阻塞会让建会话的 HTTP 请求挂最多 3 秒，一个 3 秒的空白 tab 比
	// 极小概率的输入交错更糟（拆解 §6.1 的处置）。
	if opt.InitCommand != "" {
		go h.writeInitCommand(s, opt.InitCommand)
	}

	h.log.Info("终端会话已创建", "session", s.meta.ID, "pid", s.meta.PID,
		"shell", opt.Shell, "base_kind", opt.BaseKind, "cwd", opt.BasePath,
		"size", fmtSize(opt.Cols, opt.Rows), "sessions", total)
	return s.snapshot(), nil
}

func fmtSize(cols, rows int) string {
	return strconv.Itoa(cols) + "x" + strconv.Itoa(rows)
}

// pump 是读循环：把 PTY 输出写进环并广播。PTY 主端在子进程退出后返回
// EIO（linux）或 EOF（darwin），两者都只意味着「读到头了」，不记为错误。
func (h *Engine) pump(s *session) {
	b := make([]byte, readChunk)
	for {
		n, err := s.f.Read(b)
		if n > 0 {
			s.broadcast(b[:n])
			// 首字节即「shell 就绪」。放在 broadcast 之后：订阅者先看到输出，
			// 启动命令再写进去，顺序与人肉眼看到提示符再敲字一致
			s.firstOutOnce.Do(func() { close(s.firstOut) })
		}
		if err != nil {
			h.log.Debug("终端会话输出流结束", "session", s.meta.ID, "err", err)
			return
		}
	}
}

// writeInitCommand 等 shell 就绪后把启动命令写进 PTY 输入。
//
// 参数：s 为已入表的会话；cmd 为命令原文（不含换行，本函数补）。
//
// 就绪判据：**首字节输出 或 initCommandReadyWait 到点，以先到者为准**。
// 前者是真实就绪信号，后者保证「rc 链一直不出声」的 shell 也不会永远等下去。
//
// 注意：
//   - 写入内容恰好是 `cmd + "\n"`，**不加任何前缀标记**（Q4 拍板 (a)：
//     用户要的是「像人亲手敲进去一样」，多一行 handoff 自己的标记既破坏
//     这个错觉，那行文本还会混进滚动历史被 Ctrl-R 搜到）
//   - **命令原文绝不进日志**：启动项的命令可能含凭据（`API_KEY=xxx cmd`
//     是常见写法）。失败只记会话 id 与错误
//   - 会话在这 0~3 秒内被关掉是正常的：Write 会返回「会话不存在/已退出」，
//     按 Debug 记，不是告警
func (h *Engine) writeInitCommand(s *session, cmd string) {
	select {
	case <-s.firstOut:
	case <-time.After(initCommandReadyWait):
		h.log.Debug("等 shell 首字节超时，按兜底路径写入启动命令",
			"session", s.meta.ID, "wait", initCommandReadyWait)
	}
	if err := h.Write(s.meta.ID, []byte(cmd+"\n")); err != nil {
		// 不带 cmd：命令原文可能含凭据
		h.log.Debug("写入启动命令未成功（会话可能已关闭），终端不受影响",
			"session", s.meta.ID, "cause", err)
		return
	}
	h.log.Info("启动命令已写入终端", "session", s.meta.ID, "bytes", len(cmd)+1)
}

// reap 是每个 shell 唯一的 cmd.Wait 所有者。
//
// exitedDone 只在 exit code、订阅通道和 PTY 文件都收口后关闭；Engine.Close 只能等
// 这个通道，禁止另起 cmd.Wait，否则 os/exec 的 Wait 竞态会把 exit code 与清理顺序拆开。
func (h *Engine) reap(s *session) {
	code := waitExitCode(s.cmd)
	s.mu.Lock()
	s.exited = true
	s.exitCode = &code
	for sub := range s.subs {
		close(sub.ch)
	}
	s.subs = map[*subscriber]struct{}{}
	s.mu.Unlock()
	_ = s.f.Close()
	close(s.exitedDone)
	h.log.Info("终端会话已退出", "session", s.meta.ID, "pid", s.meta.PID, "exit_code", code)
}

func (s *session) broadcast(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf.write(p)
	for sub := range s.subs {
		cp := make([]byte, len(p))
		copy(cp, p)
		select {
		case sub.ch <- cp:
		default:
			// 跟不上的订阅者：断开它而不是拖垮所有人。它重连时带 since，
			// 能从环里把这段补回来（可能带 truncated 标记）。
			close(sub.ch)
			delete(s.subs, sub)
		}
	}
}

func (s *session) snapshot() ptyhost.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.meta
	m.Attached = len(s.subs)
	m.ExitCode = s.exitCode
	m.BytesOut = s.buf.total()
	fg, fgok := foregroundPgid(s.f)
	// 相等 = shell 自己在前台，也就是在等提示符；不等 = 有命令跑在前台
	m.Foreground = fgok && fg != s.meta.PID
	return m
}

func (h *Engine) get(id string) (*session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sess[id]
	if !ok {
		return nil, ptyhost.ErrNoSession
	}
	return s, nil
}

// List 返回全部会话快照（含已退出但未被显式关闭的）。
func (h *Engine) List() []ptyhost.Session {
	h.mu.Lock()
	all := make([]*session, 0, len(h.sess))
	for _, s := range h.sess {
		all = append(all, s)
	}
	h.mu.Unlock()
	out := make([]ptyhost.Session, 0, len(all))
	for _, s := range all {
		out = append(out, s.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Get 取单个会话快照。第二个返回值 false = 不存在。
func (h *Engine) Get(id string) (ptyhost.Session, bool) {
	s, err := h.get(id)
	if err != nil {
		return ptyhost.Session{}, false
	}
	return s.snapshot(), true
}

// Write 把用户按键送进 PTY。会话已退出时返回 ErrSessionExited。
func (h *Engine) Write(id string, p []byte) error {
	s, err := h.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited {
		return ptyhost.ErrSessionExited
	}
	if _, err := s.f.Write(p); err != nil {
		h.log.Error("向终端会话写入失败", "session", id, "bytes", len(p), "err", err)
		return err
	}
	return nil
}

// Close 显式关闭会话：整组 SIGTERM，宽限 termGrace 后 SIGKILL，并等待已有 reap。
//
// 会话表仍在发信号前摘除，保持 List/第二次 Close 的同步语义；但函数返回前必须
// 观察 exitedDone，确保 shell 的 EXIT trap、exit code、订阅关闭和 PTY 文件关闭已经完成。
func (h *Engine) Close(id string) error {
	h.mu.Lock()
	s, ok := h.sess[id]
	if ok {
		delete(h.sess, id)
	}
	remain := len(h.sess)
	h.mu.Unlock()
	if !ok {
		return ptyhost.ErrNoSession
	}
	started := time.Now()
	if err := terminatePty(s.cmd); err != nil {
		h.log.Error("终止终端会话失败", "session", id, "pid", s.meta.PID, "phase", "sigterm", "err", err)
	}
	sigkill := false
	timer := time.NewTimer(termGrace)
	defer timer.Stop()
	select {
	case <-s.exitedDone:
	case <-timer.C:
		sigkill = true
		h.log.Warn("终端会话在宽限期内未退出，强制终止", "session", id,
			"pid", s.meta.PID, "grace", termGrace)
		if err := killPty(s.cmd); err != nil {
			h.log.Error("强制终止终端会话失败", "session", id, "pid", s.meta.PID, "phase", "sigkill", "err", err)
		}
		<-s.exitedDone
	}
	h.log.Info("终端会话已关闭", "session", id, "pid", s.meta.PID,
		"sessions", remain, "sigkill", sigkill, "elapsed", time.Since(started))
	return nil
}

// Attach 订阅一个会话，并原子地取回 since 之后的历史。
//
// 「原子」是关键：回放与订阅必须在同一把锁里完成，否则两者之间产生的输出
// 会两头都不落，用户看到的历史就缺了一段。
func (h *Engine) Attach(id string, since uint64) (*ptyhost.Attachment, error) {
	s, err := h.get(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.subs) >= maxSubscribers {
		s.mu.Unlock()
		h.log.Warn("终端会话连接数已达上限，拒绝新连接",
			"session", id, "limit", maxSubscribers)
		return nil, ptyhost.ErrTooManySubscribers
	}
	backlog, start, truncated := s.buf.since(since)
	sub := &subscriber{ch: make(chan []byte, subscriberBuffer)}
	if s.exited {
		// 会话已退出：给历史、给一个已关闭的通道，让调用方走「灌完再报 exit」
		// 的正常路径，而不是把它当成一个错误。
		close(sub.ch)
	} else {
		s.subs[sub] = struct{}{}
	}
	attached := len(s.subs)
	s.mu.Unlock()

	h.log.Info("终端会话已接入", "session", id, "since", since,
		"backlog_bytes", len(backlog), "truncated", truncated, "attached", attached)
	return ptyhost.NewAttachment(backlog, start, truncated, sub.ch,
		&engineAttachOps{h: h, s: s, sub: sub}), nil
}

// engineAttachOps 是引擎注入到公共 Attachment 壳里的行为。
type engineAttachOps struct {
	h   *Engine
	s   *session
	sub *subscriber
}

// Resize 上报本订阅者的尺寸，并按「所有订阅者取最小」重新协商实际尺寸。
func (a *engineAttachOps) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil // 客户端还没量出来，忽略而不是把 PTY 调成 0
	}
	s := a.s
	s.mu.Lock()
	a.sub.cols, a.sub.rows = cols, rows
	minC, minR := 0, 0
	for sub := range s.subs {
		if sub.cols <= 0 || sub.rows <= 0 {
			continue
		}
		if minC == 0 || sub.cols < minC {
			minC = sub.cols
		}
		if minR == 0 || sub.rows < minR {
			minR = sub.rows
		}
	}
	changed := minC > 0 && minR > 0 && (minC != s.meta.Cols || minR != s.meta.Rows)
	if changed {
		s.meta.Cols, s.meta.Rows = minC, minR
	}
	exited := s.exited
	s.mu.Unlock()
	if exited || !changed {
		return nil
	}
	if err := resizePty(s.f, minC, minR); err != nil {
		a.h.log.Error("调整终端尺寸失败", "session", s.meta.ID,
			"size", fmtSize(minC, minR), "err", err)
		return err
	}
	a.h.log.Debug("终端尺寸已协商", "session", s.meta.ID, "size", fmtSize(minC, minR))
	return nil
}

// Detach 退订。**只断连接，不动进程**——这是 spec §3.2 的核心分工：
// 关页面、切设备、组件卸载一律走这里，杀会话只有 Close 一条路。
func (a *engineAttachOps) Detach() {
	s := a.s
	s.mu.Lock()
	if _, ok := s.subs[a.sub]; ok {
		delete(s.subs, a.sub)
		close(a.sub.ch)
	}
	attached := len(s.subs)
	s.mu.Unlock()
	a.h.log.Info("终端会话已断开连接", "session", s.meta.ID, "attached", attached)
}

// ExitCode 返回会话的退出码，nil = 还活着。
func (a *engineAttachOps) ExitCode() *int {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	return a.s.exitCode
}
