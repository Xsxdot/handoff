// store projects.go：Project/ProjectLocation 的持久化与 detached 归并。
//
// 职责：
//   - CreateProject：同事务创建 Project + ProjectLocation + main Workspace，
//     更新最终 Operation，并为每个公开资源追加 control event
//   - AdoptWorkspace：把 detached Workspace 归并为主目录（保留 ID 与 Task 引用）
//
// 边界：
//   - 业务校验（Location 数量、identity 匹配）由 ProjectService 执行，本层只落库
//   - 所有写都在事务内，失败整体回滚
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/xushixin/handoff/internal/controlplane"
)

// CreateProject 在同一事务内创建 Project、ProjectLocation、main Workspace，
// 并可选提交最终 Operation。
//
// 返回：
//   - ControlEvent 列表：project/location/workspace 与可选 operation 的完整
//     upsert，按事务内全局 revision 顺序返回
//   - err: 任一写失败整体回滚
func (s *Store) CreateProject(ctx context.Context, p controlplane.Project,
	locations []controlplane.ProjectLocation, workspaces []controlplane.Workspace,
	finalOperation *controlplane.Operation) ([]controlplane.ControlEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启项目创建事务: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO projects (id, name, git_identity, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET name = excluded.name, git_identity = excluded.git_identity,
  updated_at = excluded.updated_at`,
		p.ID, p.Name, p.GitIdentity, fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt)); err != nil {
		return nil, fmt.Errorf("创建项目 %s: %w", p.ID, err)
	}
	for _, loc := range locations {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO project_locations (id, project_id, machine_id, machine_kind, role,
  main_workspace_id, source, git_url, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET project_id = excluded.project_id, machine_id = excluded.machine_id,
  machine_kind = excluded.machine_kind, role = excluded.role,
  main_workspace_id = excluded.main_workspace_id, source = excluded.source,
  git_url = excluded.git_url, updated_at = excluded.updated_at`,
			loc.ID, loc.ProjectID, loc.MachineID, string(loc.MachineKind), string(loc.Role),
			loc.MainWorkspaceID, string(loc.Source), loc.GitURL,
			fmtTime(loc.CreatedAt), fmtTime(loc.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("创建 location %s: %w", loc.ID, err)
		}
	}
	for _, ws := range workspaces {
		if err := upsertWorkspaceTx(ctx, tx, ws); err != nil {
			return nil, err
		}
	}

	// 项目聚合的每个公开资源都必须有完整 upsert。只发 project.upsert 会让已连接
	// 桌面永远看不到新 Location/Workspace，直到手工重启或重新 bootstrap。
	eventCapacity := 1 + len(locations) + len(workspaces)
	if finalOperation != nil {
		eventCapacity++
	}
	events := make([]controlplane.ControlEvent, 0, eventCapacity)
	projectEvent, err := appendControlEventTx(ctx, tx, controlplane.ControlEventKindProjectUpsert, p.ID, p)
	if err != nil {
		return nil, err
	}
	events = append(events, projectEvent)
	for _, location := range locations {
		event, eventErr := appendControlEventTx(ctx, tx, controlplane.ControlEventKindLocationUpsert, location.ID, location)
		if eventErr != nil {
			return nil, eventErr
		}
		events = append(events, event)
	}
	for _, workspace := range workspaces {
		event, eventErr := appendControlEventTx(ctx, tx, controlplane.ControlEventKindWorkspaceUpsert, workspace.ID, workspace)
		if eventErr != nil {
			return nil, eventErr
		}
		events = append(events, event)
	}
	if finalOperation != nil {
		event, eventErr := updateOperationTx(ctx, tx, *finalOperation)
		if eventErr != nil {
			return nil, eventErr
		}
		events = append(events, event)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交项目创建事务: %w", err)
	}
	return events, nil
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

// GetProject 按稳定 ID 读取项目；不存在返回 ErrNotFound。
func (s *Store) GetProject(ctx context.Context, id string) (controlplane.Project, error) {
	var (
		project   controlplane.Project
		createdAt string
		updatedAt string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, git_identity, created_at, updated_at FROM projects WHERE id = ?`, id).
		Scan(&project.ID, &project.Name, &project.GitIdentity, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return controlplane.Project{}, ErrNotFound
	}
	if err != nil {
		return controlplane.Project{}, fmt.Errorf("读取项目 %s: %w", id, err)
	}
	project.CreatedAt = parseTime(createdAt)
	project.UpdatedAt = parseTime(updatedAt)
	return project, nil
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
