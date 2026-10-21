// artifacts_retention.go —— B452 S8 产物目录的保留期清扫。
//
// 职责：按保留期删除终态任务的 <DataDir>/tasks/<id>/artifacts 子目录。
// 边界：只删 artifacts 子目录，绝不删任务目录本身（plan、纪律正文、serve 物料仍归各自的清理路径）；
// 非终态任务一律不动；保留期 0 表示永不清理。
package orchestration

import (
	"context"
	"os"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// SweepExpiredArtifacts 删除「终态且 updated_at 早于 now-retention」的任务产物目录，返回删除个数。
//
// 参数：
//   - now: 判定时刻（注入以便测试）
//   - retention: 保留期；<=0 表示永不清理，直接返回 0
//
// 注意：删除失败只记日志并继续下一个任务——清扫是善后，不阻断任何主流程。
func (m *Manager) SweepExpiredArtifacts(now time.Time, retention time.Duration) int {
	if retention <= 0 {
		m.log.Debug("产物保留期为 0，跳过清扫")
		return 0
	}
	tasks, err := m.st.ListTasks()
	if err != nil {
		m.log.Error("产物清扫：列任务失败", "cause", err)
		return 0
	}
	cutoff := now.Add(-retention)
	removed := 0
	for _, t := range tasks {
		if t.State != proto.TaskStateCompleted && t.State != proto.TaskStateFailed {
			continue
		}
		if !t.UpdatedAt.Before(cutoff) {
			continue
		}
		dir := TaskArtifactsDir(m.cfg.DataDir, t.ID)
		if _, err := os.Lstat(dir); os.IsNotExist(err) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			m.log.Error("产物清扫：删除失败", "task", t.ID, "dir", dir, "cause", err)
			continue
		}
		removed++
		m.log.Info("产物目录已过保留期清理", "task", t.ID, "dir", dir,
			"state", t.State, "updated_at", t.UpdatedAt, "retention", retention)
	}
	m.log.Info("产物清扫完成", "removed", removed, "retention", retention)
	return removed
}

// RunArtifactsRetention 启动即清扫一次，之后每 interval 一次，直到 ctx 取消。
//
// retention 每轮现取（活配置可在运行期改）；interval<=0 时取 1h。
func (m *Manager) RunArtifactsRetention(ctx context.Context, retention func() time.Duration, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	m.log.Info("产物保留期清扫循环启动", "interval", interval, "retention", retention())
	m.SweepExpiredArtifacts(time.Now(), retention())
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			m.log.Info("产物保留期清扫循环退出", "cause", ctx.Err())
			return
		case now := <-tk.C:
			m.SweepExpiredArtifacts(now, retention())
		}
	}
}
