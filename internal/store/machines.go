// store machines.go：本机稳定身份与配置机器投影。
//
// 职责：
//   - EnsureLocalMachine：用 control_metadata.local_machine_id 保存稳定本机身份
//   - SyncConfiguredMachines：把 config.Targets 投影为 remote Machine 行，
//     按 config_key 保留稳定 ID；删除的 target 保留 last-known 但标 unavailable
//   - Snapshot：读取控制面全量投影
//
// 边界：
//   - 不落任何 secret/token 值：配置远端只存 secret_ref 引用，token 由运行时
//     credential resolver 从 config.Config 读取
//   - 本机 Machine 的 kind=local 由数据库唯一索引（idx_machines_kind_local）兜底
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/controlplane"
)

// localMachineMetaKey 是 control_metadata 里本机 Machine ID 的键。
const localMachineMetaKey = "local_machine_id"

// EnsureLocalMachine 确保存在稳定的本机 Machine（创建或复用）并返回它。
//
// 为什么用 control_metadata 而非直接在 machines 表按 kind=local 查：
// metadata 是显式的「本机身份」事实，与配置投影的远端机器解耦；同库重启
// 只读同一键即可保持 ID 稳定。
func (s *Store) EnsureLocalMachine(ctx context.Context, displayName string) (controlplane.Machine, error) {
	if displayName == "" {
		displayName = "本机"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.Machine{}, fmt.Errorf("开启本机身份事务: %w", err)
	}
	defer tx.Rollback()

	var id string
	err = tx.QueryRowContext(ctx,
		"SELECT value FROM control_metadata WHERE key = ?", localMachineMetaKey).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		id = uuid.NewString()
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO control_metadata (key, value) VALUES (?, ?)", localMachineMetaKey, id); err != nil {
			return controlplane.Machine{}, fmt.Errorf("记录本机身份: %w", err)
		}
	case err != nil:
		return controlplane.Machine{}, fmt.Errorf("读取本机身份: %w", err)
	}

	now := fmtTime(time.Now())
	// UPSERT：同库重复调用保持 ID，display name 就地更新。
	if _, err := tx.ExecContext(ctx, `
INSERT INTO machines (id, display_name, kind, endpoint, secret_ref, protocol_version, capabilities, status, last_seen_at)
VALUES (?, ?, 'local', '', '', 0, '', 'connected', ?)
ON CONFLICT(id) DO UPDATE SET display_name = excluded.display_name, status = 'connected', last_seen_at = excluded.last_seen_at`,
		id, displayName, now); err != nil {
		return controlplane.Machine{}, fmt.Errorf("写入本机机器: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return controlplane.Machine{}, fmt.Errorf("提交本机身份事务: %w", err)
	}
	return controlplane.Machine{
		ID: id, DisplayName: displayName, Kind: controlplane.MachineKindLocal,
		Status: controlplane.MachineStatusConnected,
	}, nil
}

// SyncConfiguredMachines 把配置的远端机器投影为 Machine 行。
//
// 语义：
//   - 配置中出现的目标按 config_key 关联稳定 ID（endpoint/display 改变不换 ID）
//   - 配置中已删除的目标保留 last-known Machine 但标 unavailable
//   - secret_ref 固定为 config.targets.<name>.token，不落 token 值
func (s *Store) SyncConfiguredMachines(ctx context.Context, configured []controlplane.ConfiguredMachine) ([]controlplane.Machine, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启配置机器同步事务: %w", err)
	}
	defer tx.Rollback()

	now := fmtTime(time.Now())
	var machines []controlplane.Machine
	for _, c := range configured {
		displayName := c.DisplayName
		if displayName == "" {
			displayName = c.ConfigKey
		}
		// 同一 config_key 幂等 upsert：按 id 冲突更新元数据，保留稳定 ID。
		if _, err := tx.ExecContext(ctx, `
INSERT INTO machines (id, display_name, kind, endpoint, secret_ref, protocol_version, capabilities, status, last_seen_at)
VALUES (?, ?, 'remote', ?, ?, 1, '', 'unavailable', ?)
ON CONFLICT(id) DO UPDATE SET display_name = excluded.display_name,
  endpoint = excluded.endpoint, secret_ref = excluded.secret_ref, status = excluded.status,
  last_seen_at = excluded.last_seen_at`,
			c.ConfigKey, displayName, c.Endpoint, c.SecretRef, now); err != nil {
			return nil, fmt.Errorf("写入配置机器 %s: %w", c.ConfigKey, err)
		}
		machines = append(machines, controlplane.Machine{
			ID: c.ConfigKey, DisplayName: displayName, Kind: controlplane.MachineKindRemote,
			Endpoint: c.Endpoint, SecretRef: c.SecretRef, ProtocolVersion: 1,
			Status: controlplane.MachineStatusUnavailable,
		})
	}

	// 已删除的 target：保留 last-known 行，仅标 unavailable。
	// 为什么保留不删：桌面左栏断线语义要求「保留最后已知子树」；硬删会让
	// 已归属该项目的工作区/任务失去机器参照。
	//
	// 注意 SQL 陷阱：空配置时 `id NOT IN (NULL)` 求值为 NULL 不更新任何行，
	// 必须退化为无条件更新所有 remote 行。
	if len(configured) == 0 {
		if _, err := tx.ExecContext(ctx,
			"UPDATE machines SET status = 'unavailable' WHERE kind = 'remote'"); err != nil {
			return nil, fmt.Errorf("标记已删 target 不可用: %w", err)
		}
	} else {
		keys := make([]any, 0, len(configured))
		placeholders := ""
		for _, c := range configured {
			keys = append(keys, c.ConfigKey)
			if placeholders != "" {
				placeholders += ","
			}
			placeholders += "?"
		}
		query := fmt.Sprintf(
			"UPDATE machines SET status = 'unavailable' WHERE kind = 'remote' AND id NOT IN (%s)",
			placeholders)
		if _, err := tx.ExecContext(ctx, query, keys...); err != nil {
			return nil, fmt.Errorf("标记已删 target 不可用: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交配置机器同步: %w", err)
	}
	// 返回 full 投影（含删除标记的 last-known 行）。
	return s.SnapshotMachines(ctx)
}

// Snapshot 返回控制面全量投影（bootstrap 数据源）。
func (s *Store) Snapshot(ctx context.Context) (controlplane.Snapshot, error) {
	machines, err := s.SnapshotMachines(ctx)
	if err != nil {
		return controlplane.Snapshot{}, err
	}
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return controlplane.Snapshot{}, err
	}
	locations, err := s.ListLocations(ctx)
	if err != nil {
		return controlplane.Snapshot{}, err
	}
	workspaces, err := s.ListAllWorkspaces(ctx)
	if err != nil {
		return controlplane.Snapshot{}, err
	}
	gitRefs, err := s.ListAllGitRefs(ctx)
	if err != nil {
		return controlplane.Snapshot{}, err
	}
	taskSummaries, err := s.ListTaskSummaries()
	if err != nil {
		return controlplane.Snapshot{}, err
	}
	operations, err := s.ListOperations(ctx)
	if err != nil {
		return controlplane.Snapshot{}, err
	}
	revision, err := s.currentControlRevision(ctx)
	if err != nil {
		return controlplane.Snapshot{}, err
	}
	return controlplane.Snapshot{
		ControlRevision:     revision,
		Machines:            machines,
		Projects:            projects,
		Locations:           locations,
		Workspaces:          workspaces,
		GitRefs:             gitRefs,
		ActiveTaskSummaries: taskSummaries,
		Operations:          operations,
	}, nil
}

// SnapshotMachines 返回全部 Machine 行。
func (s *Store) SnapshotMachines(ctx context.Context) ([]controlplane.Machine, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, display_name, kind, endpoint, secret_ref, protocol_version, capabilities, status, last_seen_at
FROM machines ORDER BY kind, display_name`)
	if err != nil {
		return nil, fmt.Errorf("查询机器列表: %w", err)
	}
	defer rows.Close()
	var machines []controlplane.Machine
	for rows.Next() {
		var (
			m            controlplane.Machine
			capabilities string
			lastSeenAt   sql.NullString
		)
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.Kind, &m.Endpoint, &m.SecretRef,
			&m.ProtocolVersion, &capabilities, &m.Status, &lastSeenAt); err != nil {
			return nil, fmt.Errorf("读取机器行: %w", err)
		}
		if lastSeenAt.Valid {
			t := parseTime(lastSeenAt.String)
			m.LastSeenAt = &t
		}
		_ = json.Unmarshal([]byte(capabilities), &m.Capabilities)
		if m.Capabilities == nil {
			m.Capabilities = map[string]int{}
		}
		machines = append(machines, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历机器列表: %w", err)
	}
	return machines, nil
}

// currentControlRevision 返回当前 control_events 最大 revision（0=尚无事件）。
func (s *Store) currentControlRevision(ctx context.Context) (int64, error) {
	var rev int64
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(control_revision), 0) FROM control_events").Scan(&rev)
	if err != nil {
		return 0, fmt.Errorf("读取控制面 revision: %w", err)
	}
	return rev, nil
}

// GetMachine 按 id 读取 Machine；不存在返回 ErrNotFound。
func (s *Store) GetMachine(ctx context.Context, id string) (controlplane.Machine, error) {
	var (
		m            controlplane.Machine
		capabilities string
		lastSeenAt   sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, display_name, kind, endpoint, secret_ref, protocol_version, capabilities, status, last_seen_at
FROM machines WHERE id = ?`, id).
		Scan(&m.ID, &m.DisplayName, &m.Kind, &m.Endpoint, &m.SecretRef,
			&m.ProtocolVersion, &capabilities, &m.Status, &lastSeenAt)
	if err == sql.ErrNoRows {
		return controlplane.Machine{}, ErrNotFound
	}
	if err != nil {
		return controlplane.Machine{}, fmt.Errorf("读取机器 %s: %w", id, err)
	}
	if lastSeenAt.Valid {
		t := parseTime(lastSeenAt.String)
		m.LastSeenAt = &t
	}
	_ = json.Unmarshal([]byte(capabilities), &m.Capabilities)
	if m.Capabilities == nil {
		m.Capabilities = map[string]int{}
	}
	return m, nil
}

// SetMachineStatus 更新机器状态（peer 同步状态投影用）。
func (s *Store) SetMachineStatus(ctx context.Context, machineID string, status controlplane.MachineStatus) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE machines SET status = ? WHERE id = ?", string(status), machineID); err != nil {
		return fmt.Errorf("更新机器 %s 状态: %w", machineID, err)
	}
	return nil
}

// ListLocationsForMachine 返回某机器（通常是本机）的全部 ProjectLocation。
func (s *Store) ListLocationsForMachine(ctx context.Context, machineID string) ([]controlplane.ProjectLocation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, machine_id, machine_kind, role, main_workspace_id, source, git_url, created_at, updated_at
FROM project_locations WHERE machine_id = ? ORDER BY created_at`, machineID)
	if err != nil {
		return nil, fmt.Errorf("查询机器 %s 的 locations: %w", machineID, err)
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
