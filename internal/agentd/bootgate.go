// 启动闸：agentd 占住端口之后、真实 handler 上线之前的那段启动期的 HTTP 应答者。
//
// 职责：启动期对一切请求回 503 + 当前启动阶段（如「账本未挂载：第 N 次重试」），
// 让 `handoff status` 与对端看到「在、但未就绪、卡在哪」，而不是 connection refused；
// Ready 之后原子切到真实 handler，此后只做透传。
//
// 边界：不鉴权、不读任何 Server 状态——启动期 Server 还没装配完，账本相关字段
// 在 Ready 前写、Ready 后才被 handler 读，靠 Ready 的原子发布建立先后关系。
// 因为不鉴权，播报的阶段文案不得含连接串、主机、账号（调用方负责脱敏）。
package agentd

import (
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
)

// BootGate 是 agentd 启动期的 http.Handler。并发安全。
type BootGate struct {
	log   *slog.Logger
	real  atomic.Pointer[http.Handler]
	mu    sync.Mutex
	phase string
}

// NewBootGate 构造启动闸；log 为 nil 时用 slog.Default()。初始阶段为「启动中」。
func NewBootGate(log *slog.Logger) *BootGate {
	if log == nil {
		log = slog.Default()
	}
	return &BootGate{log: log, phase: "启动中"}
}

// SetPhase 更新启动期播报的阶段文案（须已脱敏）。Ready 之后调用无效果。
func (g *BootGate) SetPhase(phase string) {
	g.mu.Lock()
	g.phase = phase
	g.mu.Unlock()
}

// Ready 把真实 handler 原子发布上线；之后所有请求透传给 h。只应调用一次。
func (g *BootGate) Ready(h http.Handler) {
	g.real.Store(&h)
	g.log.Info("启动闸放行，真实 handler 已上线")
}

// ServeHTTP 已 Ready 则透传，否则回 503 {"error": "agentd 未就绪（<阶段>）"}。
func (g *BootGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h := g.real.Load(); h != nil {
		(*h).ServeHTTP(w, r)
		return
	}
	g.mu.Lock()
	phase := g.phase
	g.mu.Unlock()
	g.log.Debug("启动期请求，回 503", "method", r.Method, "path", r.URL.Path, "phase", phase)
	w.Header().Set("Retry-After", "5")
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agentd 未就绪（" + phase + "）"})
}
