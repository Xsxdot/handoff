// gc.go —— 机器级 handoff gc 的 agentd 接缝空壳。
//
// 职责：
//   - 固定 Manager.GC 的编排签名与纯资源动作的报告类型
//   - 为 GET/POST /api/gc 保留同鉴权入口下的 HTTP 路由处理接线
//
// 边界：
//   - Ticket 0 不删除文件、不调用 git、不改任务状态或事件
//   - 缓存判定、短号占用保护和 reclaim 批处理留给 implement 节点
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrGCUnwired 表示 gc 的 Ticket 0 接缝尚未接入资源清理实现。
var ErrGCUnwired = errors.New("gc 尚未接线")

// GC 预览或执行目标 agentd 上的终态缓存与残留 managed worktree 清理。
//
// 参数：
//   - ctx: HTTP 请求生命周期；执行节点需把它传入清理与 reclaim 调用
//   - force: 是否允许按 reclaim 语义强删脏 managed worktree
//   - execute: false 为预览，true 为在同一判定语义下执行
//
// 返回：
//   - GC 报告；Ticket 0 返回 ErrGCUnwired
//   - 任务列表或清理失败的错误由实现节点定义，不能改变纯资源动作边界
//
// 注意：缓存删除不依赖 force；force 不加 execute 仍只能影响预览报告。
func (m *Manager) GC(ctx context.Context, force, execute bool) (resp *proto.GCResp, err error) {
	if m.log != nil {
		m.log.Info("gc 进入", "force", force, "execute", execute)
		defer func() {
			if err != nil {
				m.log.Error("gc 未完成", "force", force, "execute", execute, "cause", err)
				return
			}
			m.log.Info("gc 完成", "force", force, "execute", execute)
		}()
	}
	return nil, ErrGCUnwired
}

// handleGC 是 gc HTTP 接缝的 Ticket 0 空壳。
// GET 仅预览，POST 才表达执行；两条路由最终都受 Server.Handler 的 auth 包裹。
func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"
	if r.Method == http.MethodPost {
		var req proto.GCRequest
		// 强删是破坏性动作；看不懂的请求体必须保持 force=false。
		if err := jsonDecode(r, &req); err != nil {
			s.log.Warn("gc 请求体解码失败，按默认 force=false 处理", "cause", err)
		}
		force = req.Force
	}
	if s.mgr == nil {
		s.log.Warn("gc 请求到达但 manager 未注入", "method", r.Method, "path", r.URL.Path)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	_, err := s.mgr.GC(r.Context(), force, r.Method == http.MethodPost)
	if errors.Is(err, ErrGCUnwired) {
		s.log.Info("gc 接缝尚未接线", "method", r.Method, "force", force)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		s.log.Error("gc 请求失败", "method", r.Method, "force", force, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
}

// jsonDecode 保留 gc 空壳对请求体解码的接线位置；实现节点可替换为统一解码器。
func jsonDecode(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}
