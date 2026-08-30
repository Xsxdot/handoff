// gc.go —— 机器级 handoff gc 的编排与 HTTP 接缝。
//
// 职责：
//   - 按任务表终态行预览/执行缓存叶子删除，并在 agentd 内复用 reclaim 收残树
//   - GET /api/gc 只预览，POST /api/gc 才执行；两条路由继续走 Server.Handler 的 auth
//
// 边界：
//   - 纯资源动作：不改任务状态、不删任务目录/分支/用户自建树/repos/agentd.log
//   - 不扫描无任务行孤儿目录；不 RemoveAll DataDir/tmp 根
//   - CLI 不得逐任务 POST /api/tasks/{id}/reclaim——残树循环只在本文件
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/proto"
)

// GC 预览或执行目标 agentd 上的终态缓存与残留 managed worktree 清理。
//
// 参数：
//   - ctx: HTTP 请求生命周期；执行路径传入 Reclaim
//   - force: 是否允许按 reclaim 语义强删脏 managed worktree；缓存删除不读 force
//   - execute: false 为预览，true 为在同一判定语义下执行；执行自己重读 ListTasks
//
// 返回：
//   - GC 报告；任务列表失败才返回 error。Failures>0 仍返回 resp 且 err=nil
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
			if resp != nil {
				var bytes int64
				if resp.ReleasableBytes != nil {
					bytes = *resp.ReleasableBytes
				}
				m.log.Info("gc 完成", "force", force, "execute", execute,
					"preview", resp.Preview, "scanned", resp.Scanned,
					"failures", resp.Failures, "bytes", bytes,
					"cache_rows", len(resp.CacheRows), "worktree_rows", len(resp.WorktreeRows))
			}
		}()
	}
	if m.st == nil || m.cfg == nil {
		return nil, fmt.Errorf("gc 未就绪")
	}
	tasks, err := m.st.ListTasks()
	if err != nil {
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	if m.log != nil {
		m.log.Info("gc 已读任务快照", "tasks", len(tasks), "execute", execute)
	}
	resp = &proto.GCResp{
		Preview:      !execute,
		Force:        force,
		CacheRows:    []proto.GCCacheRow{},
		WorktreeRows: []proto.GCWorktreeRow{},
	}
	var releasable int64
	seen := map[string]struct{}{}
	for _, t := range tasks {
		if !t.State.IsTerminal() {
			continue
		}
		resp.Scanned++
		for _, leaf := range planTaskCacheLeaves(m.cfg.DataDir, t.ID, tasks) {
			key := filepath.Clean(leaf.Path)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			row := proto.GCCacheRow{TaskID: t.ID, Path: leaf.Path}
			if leaf.Skip || isCacheTmpRoot(m.cfg.DataDir, leaf.Path) {
				row.Status = proto.GCItemSkipped
				row.Error = leaf.Note
				if row.Error == "" {
					row.Error = "拒绝删除 DataDir/tmp 根"
				}
				if m.log != nil {
					m.log.Info("gc 跳过缓存叶子", "task", t.ID, "path", leaf.Path, "reason", row.Error)
				}
				resp.CacheRows = append(resp.CacheRows, row)
				continue
			}
			n, werr := sumRegularFileBytes(leaf.Path)
			if werr != nil {
				if m.log != nil {
					m.log.Error("gc 统计缓存字节失败", "task", t.ID, "path", leaf.Path, "cause", werr)
				}
			} else {
				row.Bytes = n
			}
			_, lerr := os.Lstat(leaf.Path)
			missing := lerr != nil && os.IsNotExist(lerr)
			if !execute {
				if missing {
					continue
				}
				row.Status = proto.GCItemPlanned
				releasable += row.Bytes
				resp.CacheRows = append(resp.CacheRows, row)
				continue
			}
			if missing {
				continue
			}
			if m.log != nil {
				m.log.Info("gc 删除缓存叶子前", "task", t.ID, "path", leaf.Path, "bytes", row.Bytes)
			}
			if rerr := m.removeCacheLeaf(leaf.Path); rerr != nil {
				if m.log != nil {
					m.log.Error("gc 删除缓存叶子失败", "task", t.ID, "path", leaf.Path, "cause", rerr)
				}
				row.Status = proto.GCItemFailed
				row.Error = rerr.Error()
				resp.Failures++
				resp.CacheRows = append(resp.CacheRows, row)
				continue
			}
			row.Status = proto.GCItemDeleted
			releasable += row.Bytes
			resp.CacheRows = append(resp.CacheRows, row)
		}
	}
	resp.ReleasableBytes = &releasable
	if execute {
		m.appendGCWorktreesExecute(ctx, resp, tasks, force)
	} else {
		m.appendGCWorktreesPreview(resp, tasks, force)
	}
	return resp, nil
}

// appendGCWorktreesPreview 用 ReclaimList 填预览工作树行。ReclaimList 无 ctx（B77 冻结签名）。
func (m *Manager) appendGCWorktreesPreview(resp *proto.GCResp, tasks []proto.Task, force bool) {
	list, err := m.ReclaimList()
	if err != nil {
		if m.log != nil {
			m.log.Error("gc 残树体检失败，工作树行留空继续缓存报告", "cause", err)
		}
		return
	}
	for _, r := range list.Rows {
		row := proto.GCWorktreeRow{
			TaskID: r.TaskID, Name: r.Name, State: r.State, Branch: r.Branch,
			WorkDir: r.WorkDir, Worktree: r.Worktree, DirtyCount: r.DirtyCount, Note: r.Note,
		}
		switch r.Worktree {
		case proto.WorktreeDirty:
			if !force {
				row.Status = proto.GCItemSkipped
				if row.Note == "" {
					row.Note = "脏工作树未带 force，跳过"
				}
			} else {
				row.Status = proto.GCItemPlanned
			}
		case proto.WorktreeUnknown:
			row.Status = proto.GCItemSkipped
			if row.Note == "" {
				row.Note = "工作树状态判不出，跳过"
			}
		default:
			row.Status = proto.GCItemPlanned
		}
		resp.WorktreeRows = append(resp.WorktreeRows, row)
	}
	for _, t := range tasks {
		if !t.State.IsTerminal() || t.WorkDir == "" || t.WorktreeManaged {
			continue
		}
		resp.WorktreeRows = append(resp.WorktreeRows, proto.GCWorktreeRow{
			TaskID: t.ID, Name: t.Name, State: string(t.State), Branch: t.Branch,
			WorkDir: t.WorkDir, Status: proto.GCItemSkipped, Note: "非 managed 工作树，跳过",
		})
	}
}

func (m *Manager) appendGCWorktreesExecute(ctx context.Context, resp *proto.GCResp, tasks []proto.Task, force bool) {
	for _, t := range tasks {
		if !t.State.IsTerminal() || t.WorkDir == "" {
			continue
		}
		row := proto.GCWorktreeRow{
			TaskID: t.ID, Name: t.Name, State: string(t.State), Branch: t.Branch, WorkDir: t.WorkDir,
		}
		if !t.WorktreeManaged {
			row.Status = proto.GCItemSkipped
			row.Note = "非 managed 工作树，跳过"
			resp.WorktreeRows = append(resp.WorktreeRows, row)
			continue
		}
		if m.log != nil {
			m.log.Info("gc 调用 reclaim 前", "task", t.ID, "force", force, "workdir", t.WorkDir)
		}
		wr, err := m.Reclaim(ctx, t.ID, force)
		if err == nil {
			row.Status = proto.GCItemDeleted
			if wr != nil {
				row.WorkDir = wr.WorkDir
				row.Branch = wr.Branch
			}
			resp.WorktreeRows = append(resp.WorktreeRows, row)
			continue
		}
		if m.log != nil {
			m.log.Info("gc reclaim 未删除", "task", t.ID, "cause", err)
		}
		var dirty *DirtyWorktreeError
		switch {
		case errors.As(err, &dirty):
			row.Status = proto.GCItemSkipped
			row.Worktree = proto.WorktreeDirty
			row.DirtyCount = len(dirty.Files)
			row.Note = err.Error()
		case errors.Is(err, ErrReclaimNotManaged),
			errors.Is(err, ErrReclaimNotTerminal),
			errors.Is(err, ErrReclaimRepoUnreachable):
			row.Status = proto.GCItemSkipped
			row.Error = err.Error()
			row.Note = err.Error()
		default:
			row.Status = proto.GCItemFailed
			row.Error = err.Error()
			resp.Failures++
		}
		resp.WorktreeRows = append(resp.WorktreeRows, row)
	}
}

// handleGC 处理 GET/POST /api/gc。GET 仅预览，POST 才执行；两条路由都受 Server.Handler 的 auth 包裹。
// POST 解码失败时 force=false。
func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"
	if r.Method == http.MethodPost {
		var req proto.GCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.log.Warn("gc 请求体解码失败，按默认 force=false 处理", "cause", err)
		}
		force = req.Force
	}
	execute := r.Method == http.MethodPost
	s.log.Info("gc HTTP 进入", "method", r.Method, "path", r.URL.Path, "force", force, "execute", execute)
	if s.mgr == nil {
		s.log.Warn("gc 请求到达但 manager 未注入", "method", r.Method, "path", r.URL.Path)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.GC(r.Context(), force, execute)
	if err != nil {
		s.log.Error("gc 请求失败", "method", r.Method, "force", force, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	s.log.Info("gc HTTP 完成", "method", r.Method, "force", force, "execute", execute,
		"scanned", resp.Scanned, "failures", resp.Failures)
	writeJSON(w, http.StatusOK, resp)
}
