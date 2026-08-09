// store projects.go：Project/ProjectLocation 的持久化与 detached 归并。
//
// 职责：
//   - CreateProject：同事务创建 Project + ProjectLocation + main Workspace，
//     并追加 control event
//   - AdoptWorkspace：把 detached Workspace 归并为主目录（保留 ID 与 Task 引用）
//
// 边界：
//   - 业务校验（Location 数量、identity 匹配）由 ProjectService 执行，本层只落库
//   - 所有写都在事务内，失败整体回滚
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
)

// CreateProject 在同一事务内创建 Project、ProjectLocation 与 main Workspace。
//
// 返回：
//   - ControlEvent：project.upsert 控制事件（带全局 revision）
//   - err: 任一写失败整体回滚
func (s *Store) CreateProject(ctx context.Context, p controlplane.Project,
	locations []controlplane.ProjectLocation, workspaces []controlplane.Workspace) (controlplane.ControlEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.ControlEvent{}, fmt.Errorf("开启项目创建事务: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO projects (id, name, git_identity, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.GitIdentity, fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt)); err != nil {
		return controlplane.ControlEvent{}, fmt.Errorf("创建项目 %s: %w", p.ID, err)
	}
	for _, loc := range locations {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO project_locations (id, project_id, machine_id, machine_kind, role,
  main_workspace_id, source, git_url, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			loc.ID, loc.ProjectID, loc.MachineID, string(loc.MachineKind), string(loc.Role),
			loc.MainWorkspaceID, string(loc.Source), loc.GitURL,
			fmtTime(loc.CreatedAt), fmtTime(loc.UpdatedAt)); err != nil {
			return controlplane.ControlEvent{}, fmt.Errorf("创建 location %s: %w", loc.ID, err)
		}
	}
	for _, ws := range workspaces {
		if err := upsertWorkspaceTx(ctx, tx, ws); err != nil {
			return controlplane.ControlEvent{}, err
		}
	}

	// 追加 control event（全局单调 revision）。
	now := time.Now().UTC()
	var rev int64
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(control_revision), 0) FROM control_events").Scan(&rev); err != nil {
		return controlplane.ControlEvent{}, fmt.Errorf("读取控制面 revision: %w", err)
	}
	rev++
	payload, _ := json.Marshal(p)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO control_events (control_revision, kind, resource_id, payload, created_at)
VALUES (?, ?, ?, ?, ?)`,
		rev, string(controlplane.ControlEventKindProjectUpsert), p.ID, string(payload), fmtTime(now)); err != nil {
		return controlplane.ControlEvent{}, fmt.Errorf("追加 project control event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return controlplane.ControlEvent{}, fmt.Errorf("提交项目创建事务: %w", err)
	}
	return controlplane.ControlEvent{
		ControlRevision: rev, Kind: controlplane.ControlEventKindProjectUpsert,
		ResourceID: p.ID, Payload: payload, CreatedAt: now,
	}, nil
}

// AdoptWorkspace 把 detached Workspace 归并为主目录。
//
// 语义（spec §6.3）：只更新 location_id/kind，保留 Workspace ID 与全部 Task
// 的 workspace_id 引用——不复制 Workspace、不批量重写 Task。
func (s *Store) AdoptWorkspace(ctx context.Context, wsID, locationID string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE workspaces SET location_id = ?, kind = 'main' WHERE id = ? AND kind = 'detached'`,
		locationID, wsID)
	if err != nil {
		return fmt.Errorf("归并 workspace %s: %w", wsID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取归并影响行数: %w", err)
	}
	if n == 0 {
		// 可能已是 main（重复归并）或不存在
		var exists int
		if err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM workspaces WHERE id = ?", wsID).Scan(&exists); err != nil {
			return fmt.Errorf("查询 workspace %s: %w", wsID, err)
		}
		if exists == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// ListProjects 返回全部项目（bootstrap 快照用）。
func (s *Store) ListProjects(ctx context.Context) ([]controlplane.Project, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, git_identity, created_at, updated_at FROM projects ORDER BY created_at")
	if err != nil {
		return nil, fmt.Errorf("查询项目: %w", err)
	}
	defer rows.Close()
	var out []controlplane.Project
	for rows.Next() {
		var (
			p         controlplane.Project
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.GitIdentity, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("读取项目行: %w", err)
		}
		p.CreatedAt = parseTime(createdAt)
		p.UpdatedAt = parseTime(updatedAt)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历项目: %w", err)
	}
	return out, nil
}

// ListLocations 返回全部 ProjectLocation（bootstrap 快照用）。
func (s *Store) ListLocations(ctx context.Context) ([]controlplane.ProjectLocation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, machine_id, machine_kind, role, main_workspace_id, source, git_url, created_at, updated_at
FROM project_locations ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("查询 locations: %w", err)
	}
	defer rows.Close()
	var out []controlplane.ProjectLocation
	for rows.Next() {
		var (
			loc       controlplane.ProjectLocation
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(&loc.ID, &loc.ProjectID, &loc.MachineID, &loc.MachineKind,
			&loc.Role, &loc.MainWorkspaceID, &loc.Source, &loc.GitURL, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("读取 location 行: %w", err)
		}
		loc.CreatedAt = parseTime(createdAt)
		loc.UpdatedAt = parseTime(updatedAt)
		out = append(out, loc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 locations: %w", err)
	}
	return out, nil
}

var _ = sql.ErrNoRows
