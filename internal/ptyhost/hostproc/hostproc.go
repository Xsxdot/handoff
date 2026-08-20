// Package hostproc 是 ptyhost 进程的主体：一个进程托管一个 PTY 会话。
//
// 职责：
//   - 持有存活锁（整个生命周期），作为 agentd 侧判活的唯一判据
//   - 起引擎、开 shell、写 meta.json
//   - 监听 unix socket，把每条连接当成一个订阅者，收发 wire 帧
//   - shell 退出后继续活着守退出码与最后那屏输出，ExitedTTL 到点自退
//   - 收到 kill 帧时杀进程组、清目录、退出
//
// 边界：
//   - 不认识 agentd：它不知道对端是谁，也不关心对端在不在；agentd 整段时间不在，
//     本进程照常跑
//   - 不认识 HTTP / WebSocket / 任务模型
//   - 不做鉴权：socket 是 0600、目录 0700，能连上它的人本来就能在这台机器上
//     以同一个 uid 起 shell
//
// 为什么必须是独立进程而不是 agentd 的一个 goroutine：agentd 一死，PTY 主端 fd 关闭，
// shell 收到 SIGHUP，整棵进程树跟着走。这个进程把主端 fd 与环形缓冲一起带出 agentd。
package hostproc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/engine"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
	"github.com/Xsxdot/handoff/internal/ptyhost/wire"
)

// ExitedTTL 是 shell 退出后 ptyhost 保留退出码与最后屏幕的最长时间。
const ExitedTTL = 24 * time.Hour

// Spec 是一个 ptyhost 进程启动所需的完整静态参数。
//
// 参数：Root/ID 决定会话目录；BasePath/BaseKind/Cwd/Shell 是元数据与引擎入参；Env 是
// 完整环境；Cols/Rows 是初始尺寸。
// 返回：由 Run 从 spec.json 解码使用。
// 注意：Env 可能含敏感值，spec.json 必须以 0600 写入并只放在会话目录中。
type Spec struct {
	Root     string   `json:"root"`
	ID       string   `json:"id"`
	BasePath string   `json:"base_path"`
	BaseKind string   `json:"base_kind"`
	Cwd      string   `json:"cwd"`
	Shell    string   `json:"shell"`
	Env      []string `json:"env"`
	Cols     int      `json:"cols"`
	Rows     int      `json:"rows"`
}

// outbound 是单个连接的待写帧。所有 socket 写入都经 writer goroutine 串行化。
type outbound struct {
	kind byte
	data []byte
	ctrl *wire.Control
}

// server 是一个已打开会话的进程内运行状态。
type server struct {
	root     string
	id       string
	engineID string
	eng      *engine.Engine
	log      *slog.Logger
	listener net.Listener

	stopOnce sync.Once
	stop     chan struct{}

	connMu sync.Mutex
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup

	timerMu sync.Mutex
	timer   *time.Timer
}

// Run 读取 spec.json 并阻塞托管一个 PTY 会话，直到收到 kill 或 ExitedTTL 到期。
//
// 参数：specPath 是会话目录内 0600 spec.json 的路径。
// 返回：会话正常收摊时返回 nil；启动、监听或清理失败时返回带路径/会话上下文的错误。
// 注意：本函数必须在独立进程中运行；调用者不应把它放在 agentd 的普通 goroutine 中。
func Run(specPath string) (runErr error) {
	body, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("读取 ptyhost spec %s: %w", specPath, err)
	}
	var spec Spec
	if err := json.Unmarshal(body, &spec); err != nil {
		return fmt.Errorf("解析 ptyhost spec %s: %w", specPath, err)
	}

	lockPath := sessdir.LockPath(spec.Root, spec.ID)
	lock, err := prochost.AcquireLock(lockPath)
	if err != nil {
		if errors.Is(err, prochost.ErrLockHeld) {
			return fmt.Errorf("ptyhost 会话 %s 已被占用: %w", spec.ID, err)
		}
		return fmt.Errorf("占用 ptyhost 会话锁 %s: %w", lockPath, err)
	}

	logPath := sessdir.LogPath(spec.Root, spec.ID)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = lock.Release()
		return fmt.Errorf("打开 ptyhost 日志 %s: %w", logPath, err)
	}
	log := slog.New(slog.NewTextHandler(logFile, nil))

	var ln net.Listener
	var eng *engine.Engine
	var engineID string
	var srv *server
	cleaned := false
	defer func() {
		if srv != nil {
			srv.stopNow()
		}
		if eng != nil && engineID != "" {
			if err := eng.Close(engineID); err != nil && !errors.Is(err, ptyhost.ErrNoSession) {
				log.Warn("ptyhost 收摊时关闭会话失败", "session", spec.ID, "err", err)
			}
		}
		if ln != nil {
			_ = ln.Close()
		}
		_ = os.Remove(sessdir.SockPath(spec.Root, spec.ID))
		if !cleaned {
			log.Info("ptyhost 收摊完成", "session", spec.ID)
		}
		if err := logFile.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("关闭 ptyhost 日志 %s: %w", logPath, err)
		}
		if err := lock.Release(); err != nil && runErr == nil {
			runErr = fmt.Errorf("释放 ptyhost 会话锁 %s: %w", lockPath, err)
		}
		if err := sessdir.Remove(spec.Root, spec.ID); err != nil && runErr == nil {
			runErr = fmt.Errorf("清理 ptyhost 会话目录 %s: %w", sessdir.Dir(spec.Root, spec.ID), err)
		}
		cleaned = true
	}()

	if err := sessdir.CheckSockPath(spec.Root, spec.ID); err != nil {
		return fmt.Errorf("检查会话 socket 路径: %w", err)
	}
	sockPath := sessdir.SockPath(spec.Root, spec.ID)
	if err := os.Remove(sockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("清理陈旧会话 socket %s: %w", sockPath, err)
	}
	ln, err = net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("绑定会话 socket %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		return fmt.Errorf("设置会话 socket 权限 %s: %w", sockPath, err)
	}
	log.Info("ptyhost 已绑定 socket", "session", spec.ID, "socket", sockPath)

	eng = engine.New(log)
	meta, err := eng.Open(ptyhost.OpenOptions{
		BasePath: spec.Cwd, BaseKind: spec.BaseKind, Shell: spec.Shell,
		Env: spec.Env, Cols: spec.Cols, Rows: spec.Rows,
	})
	if err != nil {
		return fmt.Errorf("启动 PTY shell %s: %w", spec.Shell, err)
	}
	engineID = meta.ID
	if err := sessdir.WriteMeta(spec.Root, sessdir.Meta{
		ID: spec.ID, BasePath: spec.BasePath, BaseKind: spec.BaseKind, Cwd: spec.Cwd,
		Shell: spec.Shell, CreatedAt: meta.CreatedAt, PID: os.Getpid(), ProtoVersion: wire.ProtoVersion,
	}); err != nil {
		return fmt.Errorf("写入 ptyhost 会话元数据: %w", err)
	}

	srv = &server{
		root: spec.Root, id: spec.ID, engineID: engineID, eng: eng, log: log, listener: ln,
		stop: make(chan struct{}), conns: make(map[net.Conn]struct{}),
	}
	log.Info("ptyhost 进程已启动", "session", spec.ID, "pid", os.Getpid(), "cwd", spec.Cwd, "shell", spec.Shell)
	go srv.watchShellExit()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-srv.stop:
				break
			default:
				return fmt.Errorf("接受会话 socket %s: %w", sockPath, err)
			}
			break
		}
		if !srv.registerConn(conn) {
			_ = conn.Close()
			continue
		}
		srv.wg.Add(1)
		go func() {
			defer srv.wg.Done()
			srv.serveConn(conn)
		}()
	}
	srv.wg.Wait()
	return nil
}

// registerConn 把连接纳入 stop 的统一关闭范围。
func (s *server) registerConn(conn net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	select {
	case <-s.stop:
		return false
	default:
	}
	s.conns[conn] = struct{}{}
	return true
}

// unregisterConn 从连接表中移除一个已结束连接。
func (s *server) unregisterConn(conn net.Conn) {
	s.connMu.Lock()
	delete(s.conns, conn)
	s.connMu.Unlock()
}

// stopNow 关闭监听器和所有订阅连接，但不杀 PTY；杀会话由 Run 的统一收摊路径完成。
func (s *server) stopNow() {
	s.stopOnce.Do(func() {
		close(s.stop)
		_ = s.listener.Close()
		s.connMu.Lock()
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.connMu.Unlock()
	})
}

// watchShellExit 轮询引擎快照，发现 shell 退出后开启 24 小时守屏计时。
func (s *server) watchShellExit() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			m, ok := s.eng.Get(s.engineID)
			if !ok {
				return
			}
			if m.ExitCode == nil {
				continue
			}
			code := *m.ExitCode
			s.log.Info("ptyhost shell 已退出", "session", s.id, "exit_code", code)
			s.timerMu.Lock()
			s.timer = time.AfterFunc(ExitedTTL, func() {
				s.log.Info("ptyhost 已退出会话到 TTL，自行收摊", "session", s.id, "ttl", ExitedTTL)
				s.stopNow()
			})
			s.timerMu.Unlock()
			return
		}
	}
}

// serveConn 服务一个订阅连接；一个连接对应一个订阅者。
func (s *server) serveConn(conn net.Conn) {
	defer s.unregisterConn(conn)
	done := make(chan struct{})
	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			close(done)
			_ = conn.Close()
		})
	}
	defer finish()

	frames := make(chan outbound, 64)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-done:
				return
			case frame := <-frames:
				var err error
				if frame.kind == wire.KindData {
					err = wire.WriteData(conn, frame.data)
				} else if frame.ctrl != nil {
					err = wire.WriteControl(conn, *frame.ctrl)
				}
				if err != nil {
					s.log.Warn("ptyhost 下行写入失败", "session", s.id, "err", err)
					finish()
					return
				}
			}
		}
	}()

	send := func(frame outbound) bool {
		select {
		case frames <- frame:
			return true
		case <-done:
			return false
		}
	}
	var att *ptyhost.Attachment
	defer func() {
		if att != nil {
			att.Detach()
			s.log.Debug("ptyhost 订阅连接已 detach", "session", s.id, "reason", "连接结束")
		}
		finish()
		<-writerDone
	}()

	for {
		kind, data, ctrl, err := wire.ReadFrame(conn)
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				s.log.Debug("ptyhost 订阅者正常断开", "session", s.id)
			case errors.Is(err, io.ErrUnexpectedEOF):
				s.log.Warn("ptyhost 订阅者连接半截断开", "session", s.id, "err", err)
			default:
				s.log.Debug("ptyhost 订阅连接结束", "session", s.id, "err", err)
			}
			return
		}
		if kind == wire.KindData {
			if err := s.eng.Write(s.engineID, data); err != nil {
				s.log.Warn("ptyhost 上行写入 PTY 失败", "session", s.id, "bytes", len(data), "err", err)
			}
			continue
		}
		if kind != wire.KindControl || ctrl == nil {
			continue
		}
		switch ctrl.Type {
		case wire.CtrlAttach:
			if att != nil {
				att.Detach()
			}
			var aerr error
			att, aerr = s.eng.Attach(s.engineID, ctrl.Since)
			if aerr != nil {
				s.log.Warn("ptyhost attach 失败", "session", s.id, "since", ctrl.Since, "err", aerr)
				return
			}
			s.log.Info("ptyhost 订阅已 attach", "session", s.id, "since", ctrl.Since,
				"backlog_bytes", len(att.Backlog), "truncated", att.Truncated)
			if !send(outbound{kind: wire.KindControl, ctrl: &wire.Control{
				Type: wire.CtrlAttached, Since: att.Since, Truncated: att.Truncated,
				ProtoVersion: wire.ProtoVersion,
			}}) || !send(outbound{kind: wire.KindData, data: att.Backlog}) {
				return
			}
			go s.pumpAttachment(att, send, done)
		case wire.CtrlResize:
			if att == nil {
				s.log.Debug("ptyhost 尚未 attach，忽略 resize", "session", s.id)
				continue
			}
			if err := att.Resize(ctrl.Cols, ctrl.Rows); err != nil {
				s.log.Warn("ptyhost resize 失败", "session", s.id, "cols", ctrl.Cols, "rows", ctrl.Rows, "err", err)
			}
		case wire.CtrlStat:
			m, ok := s.eng.Get(s.engineID)
			if !ok {
				s.log.Warn("ptyhost stat 找不到会话", "session", s.id)
				continue
			}
			if !send(outbound{kind: wire.KindControl, ctrl: &wire.Control{
				Type: wire.CtrlStatResp, Cols: m.Cols, Rows: m.Rows, BytesOut: m.BytesOut,
				Foreground: m.Foreground, Attached: m.Attached, ExitCode: m.ExitCode,
			}}) {
				return
			}
		case wire.CtrlKill:
			s.log.Info("ptyhost 收到 kill", "session", s.id)
			s.stopNow()
			return
		default:
			s.log.Debug("ptyhost 忽略未知控制帧", "session", s.id, "type", ctrl.Type)
		}
	}
}

// pumpAttachment 把引擎实时输出转成数据帧；Out 关闭时发送唯一的 exit 控制帧。
func (s *server) pumpAttachment(att *ptyhost.Attachment, send func(outbound) bool, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case b, ok := <-att.Out:
			if !ok {
				code := att.ExitCode()
				if !send(outbound{kind: wire.KindControl, ctrl: &wire.Control{Type: wire.CtrlExit, ExitCode: code}}) {
					return
				}
				return
			}
			if !send(outbound{kind: wire.KindData, data: b}) {
				return
			}
		}
	}
}
