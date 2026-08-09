// store workspaces.go：canonical path 规范化、detached Workspace 复用与旧任务迁移。
//
// 职责：
//   - CanonicalPath：macOS 路径规范化（绝对路径 + clean + 实际可解析 symlink）
//   - MigrateLegacyTasks：把 machine_id 为空的旧任务绑定 local Machine 与
//     稳定 detached Workspace，并在同一事务 upsert task_summaries
//
// 边界：
//   - 唯一键只用 canonical path，展示 path 保留（spec §6.3）
//   - 旧任务迁移不制造新生命周期事件：TaskState 保持原值，只补归属字段
//   - token/secret 永不进入 Workspace 领域对象
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/controlplane"
)

// CanonicalPath 把展示路径规范化为稳定的唯一键。
//
// 规则：
//   - 要求绝对路径；相对路径报错（机器侧必须提供绝对路径）
//   - filepath.Clean 归一 ./ 与重复分隔符
//   - EvalSymlinks 解析实际可解析的 symlink，让「真实路径」与「指向它的链接」
//     归并到同一 Workspace；路径不存在时保留 clean 后的绝对路径
//
// 为什么统一用 canonical path 作唯一键：同一目录可能经不同展示路径（symlink、
// ./ 变体）访问，若按展示 path 去重会出现同一目录多个 Workspace 的重复投影。
func CanonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("路径必须是绝对路径: %q", path)
	}
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved, nil
	}
	return clean, nil
}

// MigrateLegacyTasks 把全部 machine_id 为空的旧任务绑定 local Machine 与
// detached Workspace，并 upsert task_summaries。
//
// 返回迁移的任务数（已绑定过的不重复计数）。
//
// 语义：
//   - 按 Task.Workdir()（WorkDir 回退 RepoPath）规范化 canonical path
//   - 同一 canonical path 复用同一 detached Workspace（Spec §6.3）
//   - 每个任务「写 machine_id + workspace_id + task_summary」在同一事务，
//     失败整体回滚，不出现半迁移（只写 machine_id 没写 workspace_id）
//   - TaskState 保持原值，不追加生命周期事件
func (s *Store) MigrateLegacyTasks(ctx context.Context, localMachineID string) (int, error) {
	tasks, err := s.ListTasks()
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, task := range tasks {
		if task.MachineID != "" && task.WorkspaceID != "" {
			continue // 已绑定（含非本机的，可能来自 peer 投影）
		}
		if task.MachineID != "" {
			// 只写了 machine_id 没写 workspace_id 的半迁移状态：
			// 视为未完成迁移，补 workspace_id（防御历史数据）
		}
		workdir := task.Workdir()
		if workdir == "" {
			continue // 无工作目录的任务无法归属 Workspace，跳过
		}
		canonical, cerr := CanonicalPath(workdir)
		if cerr != nil {
			continue // 无法规范化的路径跳过，不阻塞其余任务迁移
		}
		if err := s.bindLegacyTask(ctx, localMachineID, task.ID, canonical, workdir); err != nil {
			return migrated, err
		}
		migrated++
	}
	return migrated, nil
}

// bindLegacyTask 把单个旧任务在单事务内绑定 Machine + Workspace + TaskSummary。
func (s *Store) bindLegacyTask(ctx context.Context, localMachineID, taskID, canonical, displayPath string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启旧任务迁移事务: %w", err)
	}
	defer tx.Rollback()

	// 复用/创建 detached Workspace：同一 canonical path 只建一次。
	wsID, err := ensureDetachedWorkspaceTx(ctx, tx, localMachineID, canonical, displayPath)
	if err != nil {
		return err
	}
	// 单事务同时写两个归属字段：失败回滚保证不出现「只写 machine_id」的半态。
	if _, err := tx.ExecContext(ctx,
		"UPDATE tasks SET machine_id = ?, workspace_id = ? WHERE id = ?",
		localMachineID, wsID, taskID); err != nil {
		return fmt.Errorf("绑定任务 %s 归属: %w", taskID, err)
	}
	// 旧任务摘要：桌面左栏需展示历史任务，故迁移时也 upsert task_summary。
	if err := upsertTaskSummaryTx(ctx, tx, taskID, localMachineID, wsID); err != nil {
		return err
	}
	return tx.Commit()
}

// ensureDetachedWorkspaceTx 在同一事务里复用或创建 detached Workspace。
//
// 为什么 detached 而非 main：旧任务在项目外路径执行，尚未登记 ProjectLocation；
// 添加项目后由控制面按 machine_id + repo_identity/git_common_dir + canonical_path
// 归并为 main，只更新 location_id/kind，不重写 Workspace ID。
func ensureDetachedWorkspaceTx(ctx context.Context, tx *sql.Tx, machineID, canonical, displayPath string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx,
		"SELECT id FROM workspaces WHERE machine_id = ? AND canonical_path = ?",
		machineID, canonical).Scan(&id)
	if err == nil {
		return id, nil // 复用
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("查询 detached Workspace: %w", err)
	}
	// 新建：canonical_path 上的唯一索引兜底并发复用。
	id = uuid.NewString()
	now := fmtTimeNow()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO workspaces (id, machine_id, location_id, kind, path, canonical_path,
  repo_identity, git_common_dir, branch, head_oid, availability, last_scanned_at)
VALUES (?, ?, NULL, 'detached', ?, ?, '', '', '', '', 'available', ?)`,
		id, machineID, displayPath, canonical, now); err != nil {
		return "", fmt.Errorf("创建 detached Workspace: %w", err)
	}
	return id, nil
}

// upsertTaskSummaryTx 把任务投影为 task_summaries 行。
//
// 为什么迁移时就要 upsert：桌面左栏计数直接读 TaskSummary 投影；旧任务若不
// 同步投影，历史任务在桌面不可见。
func upsertTaskSummaryTx(ctx context.Context, tx *sql.Tx, taskID, machineID, wsID string) error {
	var (
		name, executor, state string
		updatedAt             string
	)
	err := tx.QueryRowContext(ctx,
		"SELECT name, executor, state, updated_at FROM tasks WHERE id = ?", taskID).
		Scan(&name, &executor, &state, &updatedAt)
	if err != nil {
		return fmt.Errorf("读取任务 %s 摘要: %w", taskID, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_summaries (task_id, machine_id, workspace_id, name, executor, state, attention, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 0, ?)
ON CONFLICT(task_id) DO UPDATE SET
  machine_id = excluded.machine_id, workspace_id = excluded.workspace_id,
  name = excluded.name, executor = excluded.executor, state = excluded.state,
  attention = excluded.attention, updated_at = excluded.updated_at`,
		taskID, machineID, wsID, name, executor, state, updatedAt); err != nil {
		return fmt.Errorf("upsert 任务 %s 摘要: %w", taskID, err)
	}
	return nil
}

// ListTaskSummaries 返回全部任务摘要（桌面左栏投影）。
func (s *Store) ListTaskSummaries() ([]controlplane.TaskSummary, error) {
	rows, err := s.db.QueryContext(context.Background(), `
SELECT task_id, machine_id, workspace_id, name, executor, state, attention, updated_at
FROM task_summaries ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询任务摘要: %w", err)
	}
	defer rows.Close()
	var out []controlplane.TaskSummary
	for rows.Next() {
		var ts controlplane.TaskSummary
		var updatedAt string
		if err := rows.Scan(&ts.TaskID, &ts.MachineID, &ts.WorkspaceID, &ts.Name,
			&ts.Executor, &ts.State, &ts.Attention, &updatedAt); err != nil {
			return nil, fmt.Errorf("读取任务摘要行: %w", err)
		}
		ts.UpdatedAt = parseTime(updatedAt)
		out = append(out, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务摘要: %w", err)
	}
	return out, nil
}

// fmtTimeNow 返回当前 UTC RFC3339Nano 文本（与 fmtTime 同规格）。
func fmtTimeNow() string {
	return fmtTime(time.Now())
}

// fmtTime 序列化时间为 UTC RFC3339Nano 文本（在 store.go 定义，此处引用）。
