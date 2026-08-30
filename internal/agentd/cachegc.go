// cachegc.go —— 任务私有缓存叶子的路径、短号占用、tmp 根保护与收口删除。
//
// 职责：
//   - 计算现役叶子 TaskTmpDir(DataDir,id) 与遗留叶子 DataDir/tasks/<完整id>/tmp
//   - 用同一份 ListTasks 快照判定短号占用（不含自己；仅非终态占用）
//   - 拒绝任何等值 DataDir/tmp 根的删除目标
//   - 给 Done/Stop/compensateWorkspace/Manager.GC 提供同一套计划与删除动作
//
// 边界：
//   - 不改任务状态、不删任务目录/分支/render.log/frames.jsonl/proc.json
//   - 不扫描无任务行的孤儿目录，不清空 tmp 根
//   - 不复用 ActiveTasksByWorkDir（那是 workdir 占用，不是短号占用）
package agentd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

func cacheActiveLeaf(dataDir, taskID string) string {
	return executor.TaskTmpDir(dataDir, taskID)
}

func cacheLegacyLeaf(dataDir, taskID string) string {
	return filepath.Join(dataDir, "tasks", taskID, "tmp")
}

func cacheTmpRoot(dataDir string) string {
	return filepath.Join(dataDir, "tmp")
}

func cachePathEqual(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func cacheID8(taskID string) string {
	if len(taskID) > 8 {
		return taskID[:8]
	}
	return taskID
}

func isCacheTmpRoot(dataDir, path string) bool {
	return cachePathEqual(path, cacheTmpRoot(dataDir))
}

// activeLeafOccupied 报告同一 id8 上是否存在「自己以外」的非终态任务。
// 自己即使仍是 waiting_review，也不算占用者——收口时它正在进入终态。
// 不用 ActiveTasksByWorkDir：那是 workdir 占用，不是短号占用。
func activeLeafOccupied(tasks []proto.Task, selfID string) bool {
	self8 := cacheID8(selfID)
	if self8 == "" {
		return false
	}
	for _, t := range tasks {
		if t.ID == selfID {
			continue
		}
		if t.State.IsTerminal() {
			continue
		}
		if cacheID8(t.ID) == self8 {
			return true
		}
	}
	return false
}

type cacheLeafPlan struct {
	TaskID string
	Path   string
	Kind   string
	Skip   bool
	Note   string
}

// planTaskCacheLeaves 给出该任务两处缓存叶子的删除计划。
// 遗留叶子也做 tmp 根保护：filepath.Join+Clean 能把 taskID=".." 拼成 DataDir/tmp。
func planTaskCacheLeaves(dataDir, taskID string, tasks []proto.Task) []cacheLeafPlan {
	plan := func(path, kind string, skip bool, note string) cacheLeafPlan {
		return cacheLeafPlan{TaskID: taskID, Path: path, Kind: kind, Skip: skip, Note: note}
	}
	var out []cacheLeafPlan
	active := cacheActiveLeaf(dataDir, taskID)
	switch {
	case isCacheTmpRoot(dataDir, active):
		out = append(out, plan(active, "active", true, "拒绝删除 DataDir/tmp 根"))
	case activeLeafOccupied(tasks, taskID):
		out = append(out, plan(active, "active", true, "短号被其他非终态任务占用"))
	default:
		out = append(out, plan(active, "active", false, ""))
	}
	legacy := cacheLegacyLeaf(dataDir, taskID)
	if isCacheTmpRoot(dataDir, legacy) {
		out = append(out, plan(legacy, "legacy", true, "拒绝删除 DataDir/tmp 根"))
	} else {
		out = append(out, plan(legacy, "legacy", false, ""))
	}
	return out
}

// sumRegularFileBytes 累加 root 下普通文件字节。WalkDir 不跟随目录 symlink；
// d.Info() 描述链接本身而非目标，因此不会把 symlink 当普通文件计入。
func sumRegularFileBytes(root string) (int64, error) {
	var sum int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			if errors.Is(ierr, fs.ErrNotExist) {
				return nil
			}
			return ierr
		}
		if info.Mode().IsRegular() {
			sum += info.Size()
		}
		return nil
	})
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return sum, err
}

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
	for _, leaf := range planTaskCacheLeaves(m.cfg.DataDir, taskID, tasks) {
		if leaf.Skip {
			if m.log != nil {
				m.log.Info("缓存叶子已跳过", "task", taskID, "path", leaf.Path, "kind", leaf.Kind, "reason", leaf.Note)
			}
			continue
		}
		if isCacheTmpRoot(m.cfg.DataDir, leaf.Path) {
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
