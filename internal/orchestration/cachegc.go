// cachegc.go —— 任务私有缓存叶子的收口删除动作（纯路径/计划逻辑已迁 nested cacheplan）。
//
// 职责：
//   - 给 Done/Stop/compensateWorkspace/Manager.GC 提供 Manager 持有状态上的删除动作
//
// 边界：
//   - 不改任务状态、不删任务目录/分支/render.log/frames.jsonl/proc.json
//   - 不扫描无任务行的孤儿目录，不清空 tmp 根
//   - 纯路径/占用/字节逻辑在 internal/orchestration/internal/cacheplan
package orchestration

import (
	"os"

	"github.com/Xsxdot/handoff/internal/orchestration/internal/cacheplan"
)

func (m *Manager) removeCacheLeaf(path string) error {
	if m.removeCacheLeafFn != nil {
		return m.removeCacheLeafFn(path)
	}
	return os.RemoveAll(path)
}

// purgeTaskCache 尝试删除该任务的两处缓存叶子。失败只打日志。
func (m *Manager) purgeTaskCache(taskID string) {
	if m.log != nil {
		m.log.Info("缓存清理进入", "task", taskID)
	}
	if m.cfg == nil || m.st == nil {
		if m.log != nil {
			m.log.Error("缓存清理缺少 cfg 或 store", "task", taskID)
		}
		return
	}
	tasks, err := m.st.ListTasks()
	if err != nil {
		if m.log != nil {
			m.log.Error("缓存清理读任务表失败", "task", taskID, "cause", err)
		}
		return
	}
	for _, leaf := range cacheplan.PlanTaskCacheLeaves(m.cfg.DataDir, taskID, tasks) {
		if leaf.Skip {
			if m.log != nil {
				m.log.Info("缓存叶子已跳过", "task", taskID, "path", leaf.Path, "kind", leaf.Kind, "reason", leaf.Note)
			}
			continue
		}
		if cacheplan.IsTmpRoot(m.cfg.DataDir, leaf.Path) {
			if m.log != nil {
				m.log.Error("缓存叶子命中 tmp 根，拒绝删除", "task", taskID, "path", leaf.Path)
			}
			continue
		}
		if m.log != nil {
			m.log.Info("缓存叶子删除前", "task", taskID, "path", leaf.Path, "kind", leaf.Kind)
		}
		if err := m.removeCacheLeaf(leaf.Path); err != nil {
			if m.log != nil {
				m.log.Error("缓存叶子删除失败", "task", taskID, "path", leaf.Path, "kind", leaf.Kind, "cause", err)
			}
			continue
		}
		if m.log != nil {
			m.log.Info("缓存叶子已删除", "task", taskID, "path", leaf.Path, "kind", leaf.Kind)
		}
	}
	if m.log != nil {
		m.log.Info("缓存清理完成", "task", taskID)
	}
}
