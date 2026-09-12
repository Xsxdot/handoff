// cacheplan.go —— 任务私有缓存叶子的路径、短号占用、tmp 根保护与收口删除计划的纯逻辑
// （B233.17 从 orchestration 公开包迁入嵌套 internal）。
//
// 职责：
//   - 计算现役叶子 executor.TaskTmpDir(DataDir,id) 与遗留叶子 DataDir/tasks/<完整id>/tmp
//   - 用同一份 ListTasks 快照判定短号占用（不含自己；仅非终态占用）
//   - 拒绝任何等值 DataDir/tmp 根的删除目标
//   - 给出每个任务两处缓存叶子的删除计划与字节统计
//
// 边界：
//   - 无 Manager、不写任务状态、不执行删除、不碰 store/hub
//   - 不扫描无任务行的孤儿目录，不清空 tmp 根
//   - 只依赖 stdlib + executor + proto，**不 import orchestration**（避免 import 回边）
package cacheplan

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// ActiveLeaf 返回现役缓存叶子：executor.TaskTmpDir(dataDir, taskID)。
func ActiveLeaf(dataDir, taskID string) string {
	return executor.TaskTmpDir(dataDir, taskID)
}

// LegacyLeaf 返回遗留缓存叶子：dataDir/tasks/<完整 taskID>/tmp。
func LegacyLeaf(dataDir, taskID string) string {
	return filepath.Join(dataDir, "tasks", taskID, "tmp")
}

// TmpRoot 返回 DataDir/tmp 根。
func TmpRoot(dataDir string) string {
	return filepath.Join(dataDir, "tmp")
}

// PathEqual 报告两路径 Clean 后是否相等。
func PathEqual(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// ID8 返回任务短号（前 8 字符，短于 8 则全取）。
func ID8(taskID string) string {
	if len(taskID) > 8 {
		return taskID[:8]
	}
	return taskID
}

// IsTmpRoot 报告 path 是否是 DataDir/tmp 根。
func IsTmpRoot(dataDir, path string) bool {
	return PathEqual(path, TmpRoot(dataDir))
}

// ActiveLeafOccupied 报告同一 id8 上是否存在「自己以外」的非终态任务。
// 自己即使仍是 waiting_review，也不算占用者——收口时它正在进入终态。
// 不用 ActiveTasksByWorkDir：那是 workdir 占用，不是短号占用。
func ActiveLeafOccupied(tasks []proto.Task, selfID string) bool {
	self8 := ID8(selfID)
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
		if ID8(t.ID) == self8 {
			return true
		}
	}
	return false
}

// LeafPlan 描述一处缓存叶子的删除意图。Skip 为真时 Note 给出原因。
type LeafPlan struct {
	TaskID string
	Path   string
	Kind   string
	Skip   bool
	Note   string
}

// PlanTaskCacheLeaves 给出该任务两处缓存叶子的删除计划。
// 遗留叶子也做 tmp 根保护：filepath.Join+Clean 能把 taskID=".." 拼成 DataDir/tmp。
func PlanTaskCacheLeaves(dataDir, taskID string, tasks []proto.Task) []LeafPlan {
	plan := func(path, kind string, skip bool, note string) LeafPlan {
		return LeafPlan{TaskID: taskID, Path: path, Kind: kind, Skip: skip, Note: note}
	}
	var out []LeafPlan
	active := ActiveLeaf(dataDir, taskID)
	switch {
	case IsTmpRoot(dataDir, active):
		out = append(out, plan(active, "active", true, "拒绝删除 DataDir/tmp 根"))
	case ActiveLeafOccupied(tasks, taskID):
		out = append(out, plan(active, "active", true, "短号被其他非终态任务占用"))
	default:
		out = append(out, plan(active, "active", false, ""))
	}
	legacy := LegacyLeaf(dataDir, taskID)
	if IsTmpRoot(dataDir, legacy) {
		out = append(out, plan(legacy, "legacy", true, "拒绝删除 DataDir/tmp 根"))
	} else {
		out = append(out, plan(legacy, "legacy", false, ""))
	}
	return out
}

// SumRegularFileBytes 累加 root 下普通文件字节。WalkDir 不跟随目录 symlink；
// d.Info() 描述链接本身而非目标，因此不会把 symlink 当普通文件计入。
func SumRegularFileBytes(root string) (int64, error) {
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
