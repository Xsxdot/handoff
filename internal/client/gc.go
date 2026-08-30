// gc.go —— handoff gc 的 HTTP client 空壳与旧 agentd 探测接线。
//
// 职责：
//   - 把预览/执行分别映射到 GET/POST /api/gc
//   - 保留与 reclaim 同形的双 404 旧 agentd 判别入口
//
// 边界：
//   - 不在协调者侧计算字节、不遍历 DataDir、不逐个调用任务 reclaim
//   - 请求鉴权、超时与错误分级复用 Client.do/httpError 的既有行为
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrGCUnsupported 表示对端 agentd 尚未提供 gc 端点。
var ErrGCUnsupported = errors.New("对端 agentd 不支持 gc")

// GCPreview 请求目标 agentd 生成一次只读 gc 预览。
//
// 参数：
//   - ctx: 请求生命周期，用于取消 HTTP 调用
//   - force: 只改变脏 managed worktree 的预览处置，不会触发删除
//
// 返回：
//   - 目标 agentd 返回的 gc 报告
//   - 请求失败、响应非法或对端过旧时返回错误
func (c *Client) GCPreview(ctx context.Context, force bool) (*proto.GCResp, error) {
	slog.Default().Info("gc 预览进入", "force", force)
	path := "/api/gc"
	if force {
		path += "?force=true"
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		slog.Default().Error("gc 预览请求失败", "force", force, "cause", err)
		return nil, fmt.Errorf("gc 预览请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		c.log().Debug("对端 agentd 不支持 /api/gc，按版本过旧处理")
		return nil, ErrGCUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("gc 预览", resp)
	}
	var out proto.GCResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		slog.Default().Error("gc 预览响应解析失败", "force", force, "cause", err)
		return nil, fmt.Errorf("解析 gc 预览响应: %w", err)
	}
	slog.Default().Info("gc 预览完成", "force", force, "cache_rows", len(out.CacheRows),
		"worktree_rows", len(out.WorktreeRows))
	return &out, nil
}

// GC 执行目标 agentd 的 gc；只有本次调用的 execute=true 才允许动盘。
//
// 参数：
//   - ctx: 请求生命周期，用于取消 HTTP 调用
//   - force: 是否透传 reclaim 语义的脏 managed worktree 强删开关
//
// 返回：
//   - 目标 agentd 返回的 gc 报告
//   - 请求失败、响应非法或对端过旧时返回错误
//
// 注意：POST 404 时补探测 GET /api/gc，只有两条路由皆 404 才判为旧 agentd。
func (c *Client) GC(ctx context.Context, force bool) (*proto.GCResp, error) {
	slog.Default().Info("gc 执行进入", "force", force)
	resp, err := c.do(ctx, http.MethodPost, "/api/gc", proto.GCRequest{Force: force})
	if err != nil {
		slog.Default().Error("gc 执行请求失败", "force", force, "cause", err)
		return nil, fmt.Errorf("gc 执行请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		if _, lerr := c.GCPreview(ctx, force); errors.Is(lerr, ErrGCUnsupported) {
			c.log().Debug("对端两条 gc 路由皆 404，按版本过旧处理")
			return nil, ErrGCUnsupported
		}
		return nil, c.httpError("gc 执行", resp)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("gc 执行", resp)
	}
	var out proto.GCResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		slog.Default().Error("gc 执行响应解析失败", "force", force, "cause", err)
		return nil, fmt.Errorf("解析 gc 执行响应: %w", err)
	}
	slog.Default().Info("gc 执行完成", "force", force, "cache_rows", len(out.CacheRows),
		"worktree_rows", len(out.WorktreeRows), "failures", out.Failures)
	return &out, nil
}
