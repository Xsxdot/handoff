// 本文件实现 preview 的本地开窗编排，并声明 OS launcher 的稳定接缝。
//
// 职责：
//   - 按 (machine, session) 解析 owner/mirror 会话
//   - 组装 loopback SOCKS、PAC、独立 Chromium profile 与启动参数
//   - 复用已启动进程、续命本机 owner，并在进程结束或 Stop 时回收本地资源
//
// 边界：不关闭 owner session；进程发现、参数落地、进程组停止和聚焦由同文件的
// PreviewLauncher 接口及 preview_launcher_unix.go / preview_launcher_windows.go 负责。
package agentd

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

// PreviewLaunchSpec contains the exact isolated-browser inputs. ProxyBypassList
// must be <-loopback>; otherwise Chromium silently bypasses the preview SOCKS
// for localhost and can connect to the desktop machine instead of the owner.
type PreviewLaunchSpec struct {
	SessionID       string
	EntryURL        string
	PACPath         string
	ProxyServer     string
	ProxyNonce      string
	ProxyBypassList string
	UserDataDir     string
}

// PreviewBrowserHandle identifies a started browser and its eventual exit.
// Done may be nil for a launcher that cannot observe process exit.
type PreviewBrowserHandle struct {
	PID  int
	Done <-chan error
}

// PreviewLauncher is the OS boundary for executable discovery, isolated start,
// focus, and process-group stop. Tests provide a fake; production uses the
// platform implementation in the paired OS-specific files.
type PreviewLauncher interface {
	FindExecutable(context.Context) (string, error)
	Start(context.Context, string, PreviewLaunchSpec) (PreviewBrowserHandle, error)
	Focus(context.Context, int) error
	// StopPID requests termination of one managed browser process and its child
	// process group/tree. The caller waits for PreviewBrowserHandle.Done before
	// deleting the proxy and profile.
	StopPID(context.Context, int) error
	Stop(context.Context) error
}

type previewProcess struct {
	proxy       *PreviewProxy
	pacPath     string
	userDataDir string
	browser     PreviewBrowserHandle
	cleanupOnce sync.Once
	exitDone    chan struct{}
}

// previewProcessStopTimeout bounds owner-close cleanup so an uncooperative
// child cannot stall event consumption or service shutdown forever.
const previewProcessStopTimeout = 5 * time.Second

// PreviewOpenService owns the local desktop side of preview open. It keeps
// browser resources in memory only; owner session truth remains in Store and
// the coordinator projection remains in PreviewMirror.
type PreviewOpenService struct {
	owner    *PreviewOwner
	mirror   *PreviewMirror
	pool     *targetclient.Pool
	launcher PreviewLauncher
	log      *slog.Logger

	mu           sync.Mutex
	processes    map[string]*previewProcess
	stopped      bool
	eventCancels []func()
	eventWG      sync.WaitGroup
	cleanupWG    sync.WaitGroup
}

// NewPreviewOpenService constructs the local open boundary. A nil launcher
// selects the platform launcher; tests should inject a fake launcher.
func NewPreviewOpenService(owner *PreviewOwner, mirror *PreviewMirror, pool *targetclient.Pool,
	launcher PreviewLauncher, log *slog.Logger) *PreviewOpenService {
	if log == nil {
		log = slog.Default()
	}
	if launcher == nil {
		launcher = newPreviewOSLauncher(log)
	}
	service := &PreviewOpenService{
		owner: owner, mirror: mirror, pool: pool, launcher: launcher, log: log,
		processes: make(map[string]*previewProcess),
	}
	hubs := make([]*PreviewHub, 0, 2)
	if owner != nil && owner.hub != nil {
		hubs = append(hubs, owner.hub)
	}
	if mirror != nil && mirror.hub != nil && (owner == nil || mirror.hub != owner.hub) {
		// Remote events are published by PreviewMirror, so this subscription is
		// required even when the local owner uses a separate hub.
		hubs = append(hubs, mirror.hub)
	}
	for _, hub := range hubs {
		var events <-chan proto.PreviewEvent
		var cancel func()
		events, cancel = hub.Subscribe()
		service.eventCancels = append(service.eventCancels, cancel)
		service.eventWG.Add(1)
		go service.watchOwnerEvents(events)
	}
	return service
}

// OpenPreview opens or focuses one session on this desktop. machine selects a
// mirrored owner; an empty machine selects the local owner. A successful call
// returns Opened=true only after the browser process has started or was focused.
func (o *PreviewOpenService) OpenPreview(ctx context.Context, id, machine string) (*proto.PreviewOpenResp, error) {
	resp := &proto.PreviewOpenResp{Opened: false}
	if strings.TrimSpace(id) == "" {
		return resp, errors.New("preview open: session id 不能为空")
	}
	key := previewSessionKey(machine, id)

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return resp, errors.New("preview open service 已停止")
	}
	if process := o.processes[key]; process != nil {
		if err := o.launcher.Focus(ctx, process.browser.PID); err != nil {
			o.log.Warn("聚焦已有 preview 浏览器失败", "operation", "preview_focus", "session", id, "machine", machine, "pid", process.browser.PID, "cause", err)
			return resp, fmt.Errorf("聚焦 preview 浏览器: %w", err)
		}
		if machine == "" {
			if err := o.owner.Touch(ctx, id, o.owner.deps.Now()); err != nil {
				o.log.Warn("聚焦后续命本机 preview 失败", "operation", "preview_touch", "session", id, "machine", machine, "cause", err)
				return resp, fmt.Errorf("续命 preview 会话: %w", err)
			}
		}
		o.log.Info("已有 preview 浏览器聚焦成功", "operation", "preview_focus", "session", id, "machine", machine, "pid", process.browser.PID)
		resp.Opened = true
		return resp, nil
	}

	session, err := o.resolveSession(ctx, id, machine)
	if err != nil {
		o.log.Warn("解析待打开 preview 会话失败", "operation", "preview_open", "session", id, "machine", machine, "cause", err)
		return resp, err
	}

	profile, err := os.MkdirTemp("", "handoff-preview-"+previewSafeName(id)+"-")
	if err != nil {
		o.log.Error("创建 preview 独立 profile 失败", "operation", "preview_open", "session", id, "machine", machine, "cause", err)
		return resp, fmt.Errorf("创建 preview profile: %w", err)
	}
	cleanupProfile := func() { _ = os.RemoveAll(profile) }
	proxy, err := NewPreviewProxy(ctx, id, session.Via, o.rawDial(machine, session), o.log)
	if err != nil {
		cleanupProfile()
		o.log.Warn("创建 preview SOCKS 失败", "operation", "preview_open", "session", id, "machine", machine, "cause", err)
		return resp, err
	}
	cleanupProxy := func() {
		if err := proxy.Close(); err != nil {
			o.log.Warn("清理 preview SOCKS 失败", "operation", "preview_cleanup", "session", id, "machine", machine, "cause", err)
		}
	}
	pacPath := filepath.Join(profile, "preview.pac")
	proxyNonce := hex.EncodeToString(proxy.Nonce())
	proxyURL := previewProxyURL(proxy.Addr().String(), proxyNonce)
	pac, err := RenderPreviewPAC(proxyURL, proxy.allowlist)
	if err == nil {
		err = os.WriteFile(pacPath, pac, 0o600)
	}
	if err != nil {
		cleanupProxy()
		cleanupProfile()
		o.log.Warn("写入 preview PAC 失败", "operation", "preview_open", "session", id, "machine", machine, "cause", err)
		return resp, fmt.Errorf("写入 preview PAC: %w", err)
	}
	go func() {
		if err := proxy.Serve(context.Background()); err != nil {
			o.log.Warn("preview SOCKS 服务结束并带错误", "operation", "preview_proxy", "session", id, "machine", machine, "cause", err)
		}
	}()

	executable, err := o.launcher.FindExecutable(ctx)
	if err != nil {
		cleanupProxy()
		cleanupProfile()
		o.log.Warn("未找到 preview 浏览器可执行文件", "operation", "preview_open", "session", id, "machine", machine, "cause", err)
		return resp, fmt.Errorf("查找 preview 浏览器: %w", err)
	}
	spec := PreviewLaunchSpec{
		SessionID:       id,
		EntryURL:        session.EntryURL,
		PACPath:         pacPath,
		ProxyServer:     proxy.Addr().String(),
		ProxyNonce:      proxyNonce,
		ProxyBypassList: "<-loopback>",
		UserDataDir:     profile,
	}
	o.log.Info("preview 浏览器启动开始", "operation", "preview_open", "session", id, "machine", machine, "executable", executable, "entry_url", session.EntryURL, "proxy", spec.ProxyServer)
	browser, err := o.launcher.Start(ctx, executable, spec)
	if err != nil {
		cleanupProxy()
		cleanupProfile()
		o.log.Warn("preview 浏览器启动失败", "operation", "preview_open", "session", id, "machine", machine, "cause", err)
		return resp, fmt.Errorf("启动 preview 浏览器: %w", err)
	}
	process := &previewProcess{proxy: proxy, pacPath: pacPath, userDataDir: profile, browser: browser, exitDone: make(chan struct{})}
	o.processes[key] = process
	go o.watchProcess(key, process)
	if machine == "" {
		if err := o.owner.Touch(ctx, id, o.owner.deps.Now()); err != nil {
			delete(o.processes, key)
			if cleanupErr := o.stopAndCleanupProcess(ctx, id, machine, process); cleanupErr != nil {
				o.log.Warn("preview 浏览器启动后续命失败且本地回收不完整", "operation", "preview_touch_cleanup", "session", id, "machine", machine, "pid", browser.PID, "cause", cleanupErr)
			}
			o.log.Warn("preview 浏览器启动后续命失败", "operation", "preview_touch", "session", id, "machine", machine, "pid", browser.PID, "cause", err)
			return resp, fmt.Errorf("续命 preview 会话: %w", err)
		}
	}
	o.log.Info("preview 浏览器启动成功", "operation", "preview_open", "session", id, "machine", machine, "pid", browser.PID)
	resp.Opened = true
	return resp, nil
}

// Stop stops all local browser/proxy resources. It does not close owner
// sessions, because closing the desktop window must leave the session listed
// until explicit owner close or idle TTL expiry.
func (o *PreviewOpenService) Stop(ctx context.Context) error {
	o.mu.Lock()
	if o.stopped {
		o.mu.Unlock()
		return nil
	}
	o.stopped = true
	processes := make([]*previewProcess, 0, len(o.processes))
	for _, process := range o.processes {
		processes = append(processes, process)
	}
	o.processes = make(map[string]*previewProcess)
	o.mu.Unlock()

	stopErr := o.launcher.Stop(ctx)
	if stopErr != nil {
		o.log.Warn("停止 preview 浏览器失败", "operation", "preview_stop", "cause", stopErr)
	}
	for _, cancel := range o.eventCancels {
		cancel()
	}
	for _, process := range processes {
		if err := waitPreviewProcess(ctx, process); err != nil {
			stopErr = errors.Join(stopErr, err)
			o.log.Warn("等待 preview 浏览器收尾失败", "operation", "preview_stop", "cause", err)
		}
	}
	o.eventWG.Wait()
	o.cleanupWG.Wait()
	for _, process := range processes {
		o.cleanupProcess("", "", process)
	}
	o.log.Info("preview 本地资源已停止", "operation", "preview_stop", "processes", len(processes))
	return stopErr
}

func (o *PreviewOpenService) watchOwnerEvents(events <-chan proto.PreviewEvent) {
	defer o.eventWG.Done()
	for event := range events {
		if event.Type != proto.PreviewEventClosed {
			continue
		}
		machine := event.Machine
		key := previewSessionKey(machine, event.Session.ID)
		o.mu.Lock()
		process := o.processes[key]
		if process != nil {
			delete(o.processes, key)
		}
		o.mu.Unlock()
		if process != nil {
			o.cleanupWG.Add(1)
			go func(event proto.PreviewEvent, machine string, process *previewProcess) {
				defer o.cleanupWG.Done()
				if err := o.stopAndCleanupProcess(context.TODO(), event.Session.ID, machine, process); err != nil {
					o.log.Warn("owner 关闭后回收本地 preview 资源不完整", "operation", "preview_owner_close", "session", event.Session.ID, "machine", machine, "pid", process.browser.PID, "cause", err)
					return
				}
				o.log.Info("owner 关闭后回收本地 preview 资源", "operation", "preview_owner_close", "session", event.Session.ID, "machine", machine, "pid", process.browser.PID)
			}(event, machine, process)
		}
	}
}

func (o *PreviewOpenService) stopAndCleanupProcess(ctx context.Context, sessionID, machine string, process *previewProcess) error {
	if ctx == nil {
		ctx = context.TODO()
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), previewProcessStopTimeout)
	defer cancel()
	var stopErr error
	if err := o.launcher.StopPID(stopCtx, process.browser.PID); err != nil {
		stopErr = fmt.Errorf("停止 preview 浏览器进程组: %w", err)
		o.log.Warn("停止 preview 浏览器进程组失败", "operation", "preview_stop_pid", "session", sessionID, "machine", machine, "pid", process.browser.PID, "cause", err)
	}
	if err := waitPreviewProcess(stopCtx, process); err != nil {
		o.log.Warn("等待 preview 浏览器退出失败，保留本地资源", "operation", "preview_stop_pid", "session", sessionID, "machine", machine, "pid", process.browser.PID, "cause", err)
		return errors.Join(stopErr, err)
	}
	o.cleanupProcess(sessionID, machine, process)
	return stopErr
}

func (o *PreviewOpenService) resolveSession(ctx context.Context, id, machine string) (proto.PreviewSession, error) {
	if machine != "" {
		if o.mirror == nil {
			return proto.PreviewSession{}, errors.New("preview mirror 未配置")
		}
		session, ok := o.mirror.Resolve(id, machine)
		if !ok {
			return proto.PreviewSession{}, fmt.Errorf("preview 会话 %s machine=%s: %w", id, machine, store.ErrNotFound)
		}
		return session, nil
	}
	if o.owner == nil || o.owner.st == nil {
		return proto.PreviewSession{}, errors.New("preview owner 未配置")
	}
	row, err := o.owner.st.GetPreview(id)
	if err != nil {
		return proto.PreviewSession{}, err
	}
	if row.ClosedAt != nil || !row.LastActiveAt.Add(time.Duration(row.Session.TTLSeconds)*time.Second).After(o.owner.deps.Now().UTC()) {
		return proto.PreviewSession{}, fmt.Errorf("preview 会话 %s: %w", id, store.ErrNotFound)
	}
	return row.Session, nil
}

func (o *PreviewOpenService) rawDial(machine string, session proto.PreviewSession) PreviewRawDial {
	parsed, err := url.Parse(session.EntryURL)
	addr := ""
	if err == nil && parsed.Hostname() != "" && parsed.Port() != "" {
		addr = net.JoinHostPort(parsed.Hostname(), parsed.Port())
	}
	return func(ctx context.Context, network, destination string) (net.Conn, error) {
		if addr == "" {
			return nil, fmt.Errorf("preview entry_url 无有效端口: %q", session.EntryURL)
		}
		if machine != "" {
			if o.pool == nil {
				return nil, errors.New("preview target pool 未配置")
			}
			return o.pool.DialContext(ctx, machine, network, destination)
		}
		return (&net.Dialer{}).DialContext(ctx, network, destination)
	}
}

func (o *PreviewOpenService) watchProcess(key string, process *previewProcess) {
	if process.browser.Done == nil {
		close(process.exitDone)
		return
	}
	err := <-process.browser.Done
	close(process.exitDone)
	o.mu.Lock()
	if o.processes[key] == process {
		delete(o.processes, key)
	}
	o.mu.Unlock()
	o.cleanupProcess("", "", process)
	if err != nil {
		o.log.Warn("preview 浏览器退出并带错误", "operation", "preview_process_exit", "pid", process.browser.PID, "cause", err)
		return
	}
	o.log.Info("preview 浏览器退出，已回收本地资源", "operation", "preview_process_exit", "pid", process.browser.PID)
}

func waitPreviewProcess(ctx context.Context, process *previewProcess) error {
	select {
	case <-process.exitDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等待 preview 浏览器退出: %w", ctx.Err())
	}
}

func (o *PreviewOpenService) cleanupProcess(sessionID, machine string, process *previewProcess) {
	process.cleanupOnce.Do(func() {
		if err := process.proxy.Close(); err != nil {
			o.log.Warn("回收 preview SOCKS 失败", "operation", "preview_cleanup", "session", sessionID, "machine", machine, "cause", err)
		}
		if err := os.RemoveAll(process.userDataDir); err != nil {
			o.log.Warn("回收 preview profile 失败", "operation", "preview_cleanup", "session", sessionID, "machine", machine, "cause", err)
		}
	})
}

func previewSafeName(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

func previewLaunchArgs(spec PreviewLaunchSpec) []string {
	return []string{
		"--user-data-dir=" + spec.UserDataDir,
		"--proxy-pac-url=" + previewFileURL(spec.PACPath),
		"--proxy-server=" + previewProxyURL(spec.ProxyServer, spec.ProxyNonce),
		"--proxy-bypass-list=" + spec.ProxyBypassList,
		"--no-first-run",
		"--no-default-browser-check",
		spec.EntryURL,
	}
}

func previewProxyURL(addr, nonce string) string {
	return "socks5://" + previewProxyUsername + ":" + nonce + "@" + addr
}

func previewFileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}
