// 本文件实现 owner 侧 preview session 的校验、持久化和事件发布。
//
// 职责：
//   - 把 port/path/via 请求校验成唯一 owner truth
//   - 先写 store，再广播 preview.created/preview.closed
//   - 恢复 path 静态服务并按 idle TTL 扫描、续命和收口
//
// 边界：
//   - 不实现 Chromium、SOCKS 或远端 mirror；open 的本地执行由 gateway 侧的
//     PreviewOpener 提供
//   - PreviewHub 只承载进程内实时副作用，Store 才是重启后的权威事实
//   - 静态 HTTP 服务（PreviewStaticServer 的生产实现）与浏览器 launcher 留
//     gateway（agentd），经 PreviewOwnerDeps.Static 注入
//
// B233.19：自 internal/agentd/preview_owner.go 迁入。持久化触点收成本地窄接口
// PreviewStore（行类型经 PreviewRow/PreviewSource 投影，gateway 侧适配器负责
// 与 store.PreviewRecord 互转），store.ErrNotFound 等持久层错误原样穿透，
// gateway 的 404 映射与错误文案逐字节不变。
package workspace

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
)

// PreviewTTLSeconds 是 owner preview 的 idle TTL（秒）。
const PreviewTTLSeconds int64 = 7200

// ErrPreviewClosed 表示预览会话已关闭（重复关闭映射 404）。
var ErrPreviewClosed = errors.New("预览会话已关闭")

// PreviewInputError 表示请求字段校验失败（映射 400），Operation/Field/Value
// 供 gateway 与测试定位是哪个字段被拒。
type PreviewInputError struct {
	Operation string
	Field     string
	Value     string
	Reason    string
}

func (e *PreviewInputError) Error() string {
	return fmt.Sprintf("preview %s field=%s value=%q: %s", e.Operation, e.Field, e.Value, e.Reason)
}

// PreviewSource 标识 owner 会话的来源。Kind 为 "port" 或 "path"；path 来源
// 另带 workspace root 与 workspace 相对路径。Source 是持久化内部数据，
// 不进 wire DTO。
type PreviewSource struct {
	Kind          string
	Port          int
	WorkspaceRoot string
	RelativePath  string
}

// PreviewRow 是 owner preview 持久化行在本包的窄投影。ClosedAt 为 nil 表示
// 行仍开着；LastActiveAt 是 idle TTL 的时钟，与 session 的 CreatedAt 线字段
// 刻意分开。
type PreviewRow struct {
	Session      proto.PreviewSession
	Source       PreviewSource
	LastActiveAt time.Time
	ClosedAt     *time.Time
}

// PreviewStore 是 owner preview 会话持久化的窄接口（B233.19 收窄的 store 触点）。
// gateway 侧适配器把 *store.Store 包进本接口并做 PreviewRow 互转；持久层
// 错误（如 not found）原样穿透，调用方的 errors.Is 判别不受影响。
type PreviewStore interface {
	InsertPreview(row PreviewRow) error
	GetPreview(id string) (PreviewRow, error)
	ListActivePreviews(now time.Time) ([]PreviewRow, error)
	ClosePreview(id string, at time.Time) (PreviewRow, bool, error)
	// TouchPreview 续命一个开着的会话；id 不存在时返回 not-found 错误（适配器
	// 负责把持久层的「未命中」包成带原文案的错误，gateway 的 404 映射不受影响）。
	TouchPreview(id string, at time.Time) error
	ExpirePreviews(now time.Time) ([]PreviewRow, error)
	UpdatePreviewEntry(id, entryURL string) error
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
// Nil fields are replaced by production implementations by NewPreviewOwner,
// except Static: 静态 HTTP 服务的生产实现留在 gateway（agentd），
// 由组装点（cmd）注入，本包不反向承载 HTTP 面。
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

// SubscriberCount returns the current number of live subscribers. 测试用它等待
// 「WS 订阅者已连上」；不加锁拷贝，计数仅供观测。
func (h *PreviewHub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subscribers)
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
	st   PreviewStore
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
// Static 不设缺省：生产实现（gateway 的静态 HTTP 服务）由组装点注入。
func NewPreviewOwner(st PreviewStore, hub *PreviewHub, deps PreviewOwnerDeps, log *slog.Logger) *PreviewOwner {
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
		deps.ValidateVia = ValidatePreviewViaSyntax
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
	if root, ok := TopLevel(ctx, workspaceRoot); ok {
		if realRoot, realErr := filepath.EvalSymlinks(root); realErr == nil {
			workspaceRoot = realRoot
		} else {
			log().Warn("预览工作区根目录归一化失败，保留当前目录", "workspace", workspaceRoot, "git_root", root, "cause", realErr)
		}
	}

	var originURL string
	if u, ok := ProbeOriginURL(ctx, workspaceRoot); ok {
		originURL = u
	}
	branch := HeadBranch(ctx, workspaceRoot)
	log().Info("预览工作区元数据读取成功", "operation", "preview_workspace", "workspace", workspaceRoot, "origin_url", originURL, "branch", branch)
	return workspaceRoot, originURL, branch, nil
}

// Create validates and persists one owner preview, publishing only after the row exists.
func (o *PreviewOwner) Create(ctx context.Context, req proto.PreviewOpenReq) (*proto.PreviewSession, error) {
	now := o.deps.Now().UTC()
	if (req.Port == 0) == (strings.TrimSpace(req.Path) == "") {
		return nil, &PreviewInputError{Operation: "create", Field: "port/path", Value: fmt.Sprintf("port=%d path=%q", req.Port, req.Path), Reason: "port 与 path 必须二选一"}
	}
	if req.Port != 0 && (req.Port < 1 || req.Port > 65535) {
		return nil, &PreviewInputError{Operation: "create", Field: "port", Value: fmt.Sprint(req.Port), Reason: "必须在 1..65535"}
	}
	if err := o.deps.ValidateVia(req.Via); err != nil {
		return nil, &PreviewInputError{Operation: "create", Field: "via", Value: strings.Join(req.Via, ","), Reason: err.Error()}
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

	session := proto.PreviewSession{ID: o.deps.NewID(), CWD: cwd, CreatedAt: now, TTLSeconds: PreviewTTLSeconds, Via: append([]string(nil), req.Via...)}
	if session.ID == "" {
		return nil, errors.New("生成预览会话 ID 为空")
	}
	row := PreviewRow{Session: session, LastActiveAt: now}
	var stop func() error
	if req.Port != 0 {
		if err := o.deps.ProbePort(ctx, req.Port); err != nil {
			o.log.Warn("预览端口未监听", "operation", "create", "port", req.Port, "cause", err)
			return nil, &PreviewInputError{Operation: "create", Field: "port", Value: fmt.Sprint(req.Port), Reason: "端口未监听: " + err.Error()}
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
		row.Source = PreviewSource{Kind: "port", Port: req.Port, WorkspaceRoot: root}
	} else {
		root, origin, branch, err := o.deps.ResolveWorkspace(ctx, cwd)
		if err != nil {
			o.log.Warn("读取 path 预览工作区元数据失败", "operation", "create", "cwd", cwd, "path", req.Path, "cause", err)
			return nil, fmt.Errorf("解析预览工作区 path=%q: %w", req.Path, err)
		}
		rel, err := ValidatePreviewRelativePath(root, req.Path)
		if err != nil {
			o.log.Warn("预览 path 校验失败", "operation", "create", "cwd", cwd, "workspace", root, "path", req.Path, "cause", err)
			return nil, &PreviewInputError{Operation: "create", Field: "path", Value: req.Path, Reason: err.Error()}
		}
		entry, cleanup, err := o.deps.Static.Start(ctx, root, rel)
		if err != nil {
			o.log.Error("启动预览静态服务失败", "operation", "create", "path", req.Path, "cause", err)
			return nil, fmt.Errorf("启动预览静态服务: %w", err)
		}
		stop = cleanup
		session.EntryURL, session.OriginURL, session.Branch = entry, origin, branch
		row.Source = PreviewSource{Kind: "path", WorkspaceRoot: root, RelativePath: rel}
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
		return nil, fmt.Errorf("预览会话 %s: %w", id, ErrPreviewClosed)
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
	return o.st.TouchPreview(id, at.UTC())
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

// Hub 暴露会话事件广播面，供 gateway 的 /ws/previews 订阅。
func (o *PreviewOwner) Hub() *PreviewHub { return o.hub }

// Now 返回 owner 时钟当前值（gateway 侧 open 路径续命与 TTL 判定共用同一时钟）。
func (o *PreviewOwner) Now() time.Time { return o.deps.Now() }

// Get 返回一个会话的持久化行（含生命周期时间），供 gateway 侧判定会话是否
// 仍可打开。未登记时持久层的 not found 错误原样穿透。
func (o *PreviewOwner) Get(id string) (PreviewRow, error) {
	if o.st == nil {
		return PreviewRow{}, errors.New("preview owner 未配置")
	}
	return o.st.GetPreview(id)
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

// ValidatePreviewViaSyntax 校验 session 级网络 allowlist 的每一项（gateway 的
// proxy 面与 owner 的 Create 共用同一套语法规则）。
func ValidatePreviewViaSyntax(via []string) error {
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

// ValidatePreviewRelativePath 校验请求 path 是 workspace root 之内的非空相对
// 路径，返回可安全用于静态服务与持久化的 slash 形式（gateway 的静态服务在
// Start 前复用同一校验）。
func ValidatePreviewRelativePath(root, raw string) (string, error) {
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
