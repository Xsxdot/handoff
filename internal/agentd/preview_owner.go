// 本文件实现 owner 侧 preview session 的校验、持久化和事件发布。
//
// 职责：
//   - 把 port/path/via 请求校验成唯一 owner truth
//   - 先写 store，再广播 preview.created/preview.closed
//   - 恢复 path 静态服务并按 idle TTL 扫描、续命和收口
//
// 边界：
//   - 不实现 Chromium、SOCKS 或远端 mirror；open 的本地执行由 PreviewOpener 提供
//   - PreviewHub 只承载进程内实时副作用，Store 才是重启后的权威事实
package agentd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

const previewTTLSeconds int64 = 7200

var errPreviewClosed = errors.New("预览会话已关闭")

type previewInputError struct {
	Operation string
	Field     string
	Value     string
	Reason    string
}

func (e *previewInputError) Error() string {
	return fmt.Sprintf("preview %s field=%s value=%q: %s", e.Operation, e.Field, e.Value, e.Reason)
}

// PreviewClock supplies owner time; tests inject it to make idle boundaries deterministic.
type PreviewClock func() time.Time

// PreviewID supplies unique session IDs; IDs are public lookup handles, not credentials.
type PreviewID func() string

// PreviewPortProbe verifies that an owner loopback service is already listening.
type PreviewPortProbe func(context.Context, int) error

// PreviewViaValidator validates the session-local network allowlist.
type PreviewViaValidator func([]string) error

// PreviewStaticServer serves a workspace-relative path on an owner loopback ephemeral port.
type PreviewStaticServer interface {
	Start(ctx context.Context, workspaceRoot, relativePath string) (entryURL string, stop func() error, err error)
}

// PreviewWorkspaceResolver supplies owner workspace metadata. The string argument
// is the creation working directory and is the root for resolving the request.
type PreviewWorkspaceResolver func(context.Context, string) (workspaceRoot, originURL, branch string, err error)

// PreviewOwnerDeps are the side effects at the owner boundary.
// Nil fields are replaced by production implementations by NewPreviewOwner.
type PreviewOwnerDeps struct {
	Now              PreviewClock
	NewID            PreviewID
	Getwd            func() (string, error)
	ProbePort        PreviewPortProbe
	ResolveWorkspace PreviewWorkspaceResolver
	ValidateVia      PreviewViaValidator
	Static           PreviewStaticServer
}

// PreviewHub broadcasts owner preview events to local WS consumers.
// A full subscriber is removed so one stalled browser cannot block owner writes.
type PreviewHub struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan proto.PreviewEvent
	closed      bool
	log         *slog.Logger
}

// NewPreviewHub constructs an empty bounded preview event hub.
func NewPreviewHub(log *slog.Logger) *PreviewHub {
	if log == nil {
		log = slog.Default()
	}
	return &PreviewHub{subscribers: make(map[uint64]chan proto.PreviewEvent), log: log}
}

// Subscribe returns a bounded event stream and an idempotent cancellation function.
func (h *PreviewHub) Subscribe() (<-chan proto.PreviewEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		ch := make(chan proto.PreviewEvent)
		close(ch)
		return ch, func() {}
	}
	h.nextID++
	id := h.nextID
	ch := make(chan proto.PreviewEvent, 16)
	h.subscribers[id] = ch
	return ch, func() { h.unsubscribe(id) }
}

func (h *PreviewHub) unsubscribe(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.subscribers[id]
	if !ok {
		return
	}
	delete(h.subscribers, id)
	close(ch)
}

// Publish broadcasts one already-persisted event without waiting for consumers.
func (h *PreviewHub) Publish(event proto.PreviewEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			delete(h.subscribers, id)
			close(ch)
			h.log.Warn("预览 WS 订阅者过慢，已取消", "subscriber", id, "event", event.Type, "session", event.Session.ID)
		}
	}
}

// Close terminates every preview event stream and is safe to call repeatedly.
func (h *PreviewHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id, ch := range h.subscribers {
		close(ch)
		delete(h.subscribers, id)
	}
}

// PreviewOwner is the owner-side authority for preview session lifecycle.
type PreviewOwner struct {
	st   *store.Store
	hub  *PreviewHub
	deps PreviewOwnerDeps
	log  *slog.Logger

	mu          sync.Mutex
	staticStops map[string]func() error
	expireMu    sync.Mutex
	expireStop  context.CancelFunc
	expireWG    sync.WaitGroup
}

// NewPreviewOwner creates an owner using injected seams or production defaults.
func NewPreviewOwner(st *store.Store, hub *PreviewHub, deps PreviewOwnerDeps, log *slog.Logger) *PreviewOwner {
	if log == nil {
		log = slog.Default()
	}
	if hub == nil {
		hub = NewPreviewHub(log)
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.Getwd == nil {
		deps.Getwd = os.Getwd
	}
	if deps.ProbePort == nil {
		deps.ProbePort = probePreviewPort
	}
	if deps.ResolveWorkspace == nil {
		deps.ResolveWorkspace = func(ctx context.Context, workspaceCWD string) (string, string, string, error) {
			getwd := deps.Getwd
			if workspaceCWD != "" {
				getwd = func() (string, error) { return workspaceCWD, nil }
			}
			return defaultPreviewWorkspaceResolver(ctx, getwd)
		}
	}
	if deps.ValidateVia == nil {
		deps.ValidateVia = validatePreviewViaSyntax
	}
	if deps.Static == nil {
		deps.Static = NewPreviewStaticServer(log)
	}
	return &PreviewOwner{st: st, hub: hub, deps: deps, log: log, staticStops: make(map[string]func() error)}
}

func defaultPreviewWorkspaceResolver(ctx context.Context, getwd func() (string, error)) (string, string, string, error) {
	workspaceRoot, err := getwd()
	if err != nil {
		return "", "", "", fmt.Errorf("取得工作目录: %w", err)
	}
	workspaceRoot, err = filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("解析工作目录: %w", err)
	}
	if out, _, probeErr := gitProbe(ctx, workspaceRoot, "rev-parse", "--show-toplevel"); probeErr == nil {
		if root := strings.TrimSpace(out); root != "" {
			if realRoot, realErr := filepath.EvalSymlinks(root); realErr == nil {
				workspaceRoot = realRoot
			} else {
				log().Warn("预览工作区根目录归一化失败，保留当前目录", "workspace", workspaceRoot, "git_root", root, "cause", realErr)
			}
		}
	}

	var originURL string
	if out, _, probeErr := gitProbe(ctx, workspaceRoot, "remote", "get-url", "origin"); probeErr == nil {
		originURL = strings.TrimSpace(out)
	}
	var branch string
	if out, _, probeErr := gitProbe(ctx, workspaceRoot, "symbolic-ref", "--short", "-q", "HEAD"); probeErr == nil {
		branch = strings.TrimSpace(out)
	}
	log().Info("预览工作区元数据读取成功", "operation", "preview_workspace", "workspace", workspaceRoot, "origin_url", originURL, "branch", branch)
	return workspaceRoot, originURL, branch, nil
}

// Create validates and persists one owner preview, publishing only after the row exists.
func (o *PreviewOwner) Create(ctx context.Context, req proto.PreviewOpenReq) (*proto.PreviewSession, error) {
	now := o.deps.Now().UTC()
	if (req.Port == 0) == (strings.TrimSpace(req.Path) == "") {
		return nil, &previewInputError{Operation: "create", Field: "port/path", Value: fmt.Sprintf("port=%d path=%q", req.Port, req.Path), Reason: "port 与 path 必须二选一"}
	}
	if req.Port != 0 && (req.Port < 1 || req.Port > 65535) {
		return nil, &previewInputError{Operation: "create", Field: "port", Value: fmt.Sprint(req.Port), Reason: "必须在 1..65535"}
	}
	if err := o.deps.ValidateVia(req.Via); err != nil {
		return nil, &previewInputError{Operation: "create", Field: "via", Value: strings.Join(req.Via, ","), Reason: err.Error()}
	}
	cwd := req.CWD
	var err error
	if cwd == "" {
		cwd, err = o.deps.Getwd()
		if err != nil {
			o.log.Error("创建预览无法取得工作目录", "operation", "create", "cause", err)
			return nil, fmt.Errorf("取得工作目录: %w", err)
		}
	} else {
		o.log.Info("创建预览使用请求工作目录", "operation", "create", "requested_cwd", cwd)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		o.log.Error("创建预览工作目录解析失败", "operation", "create", "cwd", cwd, "cause", err)
		return nil, fmt.Errorf("解析工作目录: %w", err)
	}

	session := proto.PreviewSession{ID: o.deps.NewID(), CWD: cwd, CreatedAt: now, TTLSeconds: previewTTLSeconds, Via: append([]string(nil), req.Via...)}
	if session.ID == "" {
		return nil, errors.New("生成预览会话 ID 为空")
	}
	row := store.PreviewRecord{Session: session, LastActiveAt: now}
	var stop func() error
	if req.Port != 0 {
		if err := o.deps.ProbePort(ctx, req.Port); err != nil {
			o.log.Warn("预览端口未监听", "operation", "create", "port", req.Port, "cause", err)
			return nil, &previewInputError{Operation: "create", Field: "port", Value: fmt.Sprint(req.Port), Reason: "端口未监听: " + err.Error()}
		}
		root, origin, branch, err := o.deps.ResolveWorkspace(ctx, cwd)
		if err != nil {
			o.log.Warn("读取 port 预览工作区元数据失败", "operation", "create", "port", req.Port, "cwd", cwd, "cause", err)
			return nil, fmt.Errorf("解析 port 预览工作区: %w", err)
		}
		session.EntryURL = fmt.Sprintf("http://localhost:%d", req.Port)
		session.OriginURL, session.Branch = origin, branch
		if root == "" {
			root = cwd
		}
		row.Source = store.PreviewSource{Kind: "port", Port: req.Port, WorkspaceRoot: root}
	} else {
		root, origin, branch, err := o.deps.ResolveWorkspace(ctx, cwd)
		if err != nil {
			o.log.Warn("读取 path 预览工作区元数据失败", "operation", "create", "cwd", cwd, "path", req.Path, "cause", err)
			return nil, fmt.Errorf("解析预览工作区 path=%q: %w", req.Path, err)
		}
		rel, err := validatePreviewRelativePath(root, req.Path)
		if err != nil {
			o.log.Warn("预览 path 校验失败", "operation", "create", "cwd", cwd, "workspace", root, "path", req.Path, "cause", err)
			return nil, &previewInputError{Operation: "create", Field: "path", Value: req.Path, Reason: err.Error()}
		}
		entry, cleanup, err := o.deps.Static.Start(ctx, root, rel)
		if err != nil {
			o.log.Error("启动预览静态服务失败", "operation", "create", "path", req.Path, "cause", err)
			return nil, fmt.Errorf("启动预览静态服务: %w", err)
		}
		stop = cleanup
		session.EntryURL, session.OriginURL, session.Branch = entry, origin, branch
		row.Source = store.PreviewSource{Kind: "path", WorkspaceRoot: root, RelativePath: rel}
	}
	row.Session = session
	o.mu.Lock()
	err = o.st.InsertPreview(row)
	if err == nil && stop != nil {
		o.staticStops[session.ID] = stop
	}
	o.mu.Unlock()
	if err != nil {
		if stop != nil {
			_ = stop()
		}
		o.log.Error("预览会话持久化失败", "operation", "create", "session", session.ID, "cause", err)
		return nil, err
	}
	o.hub.Publish(proto.PreviewEvent{Type: proto.PreviewEventCreated, Session: session})
	o.log.Info("预览会话创建成功", "operation", "create", "session", session.ID, "entry_url", session.EntryURL)
	return &session, nil
}

// List returns active owner sessions; machine is intentionally absent.
func (o *PreviewOwner) List(ctx context.Context) (*proto.PreviewListResp, error) {
	_ = ctx
	rows, err := o.st.ListActivePreviews(o.deps.Now().UTC())
	if err != nil {
		o.log.Error("读取预览会话列表失败", "operation", "list", "cause", err)
		return nil, err
	}
	out := &proto.PreviewListResp{Sessions: make([]proto.PreviewSession, 0, len(rows))}
	for _, row := range rows {
		row.Session.Machine = ""
		out.Sessions = append(out.Sessions, row.Session)
	}
	o.log.Info("预览会话列表读取成功", "operation", "list", "count", len(out.Sessions))
	return out, nil
}

// Close conditionally closes one owner session and publishes its full closed event once.
func (o *PreviewOwner) Close(ctx context.Context, id string) (*proto.PreviewCloseResp, error) {
	_ = ctx
	o.mu.Lock()
	row, changed, err := o.st.ClosePreview(id, o.deps.Now().UTC())
	var stop func() error
	if err == nil && changed {
		stop = o.staticStops[id]
		delete(o.staticStops, id)
	}
	o.mu.Unlock()
	if err != nil {
		o.log.Warn("关闭预览会话失败", "operation", "close", "session", id, "cause", err)
		return nil, err
	}
	if !changed {
		return nil, fmt.Errorf("预览会话 %s: %w", id, errPreviewClosed)
	}
	if stop != nil {
		if err := stop(); err != nil {
			o.log.Warn("停止预览静态服务失败", "operation", "close", "session", id, "cause", err)
		}
	}
	o.hub.Publish(proto.PreviewEvent{Type: proto.PreviewEventClosed, Session: row.Session})
	o.log.Info("预览会话关闭成功", "operation", "close", "session", id)
	return &proto.PreviewCloseResp{OK: true}, nil
}

// Touch renews idle lifetime for an open session; it does not alter wire is-open state.
func (o *PreviewOwner) Touch(ctx context.Context, id string, at time.Time) error {
	_ = ctx
	ok, err := o.st.TouchPreview(id, at.UTC())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("预览会话 %s: %w", id, store.ErrNotFound)
	}
	return nil
}

// Expire closes idle sessions and publishes one complete event per transition.
func (o *PreviewOwner) Expire(ctx context.Context) error {
	_ = ctx
	rows, err := o.st.ExpirePreviews(o.deps.Now().UTC())
	if err != nil {
		return err
	}
	for _, row := range rows {
		o.stopStatic(row.Session.ID)
		o.hub.Publish(proto.PreviewEvent{Type: proto.PreviewEventClosed, Session: row.Session})
		o.log.Info("预览会话因 idle TTL 关闭", "operation", "expire", "session", row.Session.ID)
	}
	return nil
}

// Restore restarts path static servers for active persisted sessions and starts the TTL loop.
func (o *PreviewOwner) Restore(ctx context.Context) error {
	rows, err := o.st.ListActivePreviews(o.deps.Now().UTC())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Source.Kind != "path" {
			continue
		}
		entry, stop, err := o.deps.Static.Start(ctx, row.Source.WorkspaceRoot, row.Source.RelativePath)
		if err != nil {
			o.log.Error("恢复预览静态服务失败", "operation", "restore", "session", row.Session.ID, "cause", err)
			continue
		}
		o.mu.Lock()
		err = o.st.UpdatePreviewEntry(row.Session.ID, entry)
		if err == nil {
			o.staticStops[row.Session.ID] = stop
		}
		o.mu.Unlock()
		if err != nil {
			_ = stop()
			o.log.Error("恢复预览 entry_url 写入失败", "operation", "restore", "session", row.Session.ID, "cause", err)
			continue
		}
	}
	o.startExpiry(ctx)
	o.log.Info("预览 owner 恢复完成", "operation", "restore", "count", len(rows))
	return nil
}

func (o *PreviewOwner) startExpiry(parent context.Context) {
	o.expireMu.Lock()
	defer o.expireMu.Unlock()
	if o.expireStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	o.expireStop = cancel
	o.expireWG.Add(1)
	go func() {
		defer o.expireWG.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := o.Expire(ctx); err != nil {
					o.log.Error("预览 TTL 扫描失败", "operation", "expire", "cause", err)
				}
			}
		}
	}()
}

// Stop cancels the TTL loop, closes the hub and reclaims owner static servers.
func (o *PreviewOwner) Stop(ctx context.Context) error {
	_ = ctx
	o.expireMu.Lock()
	if o.expireStop != nil {
		o.expireStop()
		o.expireStop = nil
	}
	o.expireMu.Unlock()
	o.expireWG.Wait()
	o.mu.Lock()
	stops := make([]func() error, 0, len(o.staticStops))
	for id, stop := range o.staticStops {
		stops = append(stops, stop)
		delete(o.staticStops, id)
	}
	o.mu.Unlock()
	for _, stop := range stops {
		if err := stop(); err != nil {
			o.log.Warn("停止预览静态服务失败", "operation", "stop", "cause", err)
		}
	}
	o.hub.Close()
	o.log.Info("预览 owner 已停止", "operation", "stop", "static_servers", len(stops))
	return nil
}

func (o *PreviewOwner) stopStatic(id string) {
	o.mu.Lock()
	stop := o.staticStops[id]
	delete(o.staticStops, id)
	o.mu.Unlock()
	if stop == nil {
		return
	}
	if err := stop(); err != nil {
		o.log.Warn("停止预览静态服务失败", "operation", "close", "session", id, "cause", err)
	}
}

func probePreviewPort(ctx context.Context, port int) error {
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	return conn.Close()
}

func validatePreviewViaSyntax(via []string) error {
	for _, raw := range via {
		value := strings.TrimSpace(raw)
		if value == "" {
			return errors.New("不允许空值")
		}
		if strings.ContainsAny(value, "*?[](){}|^$\\") {
			return errors.New("只接受单个 IP、CIDR 或域名，不接受 wildcard/regex/path")
		}
		if net.ParseIP(value) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(value); err == nil {
			continue
		}
		if strings.Contains(value, ":") || strings.ContainsAny(value, "#% ") {
			return errors.New("不接受 host:port 或 URL")
		}
		if !previewDomainPattern.MatchString(value) {
			return errors.New("域名格式非法")
		}
	}
	return nil
}

var previewDomainPattern = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$`)

func validatePreviewRelativePath(root, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" || filepath.IsAbs(raw) {
		return "", errors.New("path 必须是非空 workspace-relative 路径")
	}
	clean := filepath.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+filepathSeparator) {
		return "", errors.New("path 不得逃出 workspace root")
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("workspace root 不可用: %w", err)
	}
	targetReal, err := filepath.EvalSymlinks(filepath.Join(rootReal, clean))
	if err != nil {
		return "", fmt.Errorf("path 不存在或无法解析: %w", err)
	}
	rel, err := filepath.Rel(rootReal, targetReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+filepathSeparator) {
		return "", errors.New("path realpath 逃出 workspace root")
	}
	return filepath.ToSlash(rel), nil
}

const filepathSeparator = string(os.PathSeparator)
