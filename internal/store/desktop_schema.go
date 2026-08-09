// store 桌面控制面 schema 迁移：控制面域表/索引的创建与幂等迁移。
//
// 职责：
//   - 定义控制面全部表（machines/projects/project_locations/workspaces/
//     git_refs/task_summaries/operations/machine_events/machine_cursors/
//     control_events/control_metadata）与唯一索引
//   - 全部 DDL 在事务内执行：失败 rollback 并让 Open 返回错误，不能半迁移
//
// 边界：
//   - 只建表与约束，不含各表的读写逻辑（由专项文件实现）
//   - 迁移日志只记录 schema version 与 DB path；失败日志由 store.Open 调用方
//     带上下文记录，不在 leaf store 重复打两遍
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// desktopSchemaVersion 是桌面控制面 schema 的版本号。
const desktopSchemaVersion = 1

// migrateDesktopV1 创建桌面控制面 v1 域表与索引。
//
// 为什么用显式事务：任一条 DDL 失败都必须整体回滚，否则 agentd 会在「半迁移」
// 的库上提供写服务——这是 spec §15「迁移失败时拒绝以半迁移状态提供写服务」
// 的数据库层实现。CREATE TABLE IF NOT EXISTS 保证幂等（重复 Open 不报错）。
func migrateDesktopV1(ctx context.Context, db *sql.DB) error {
	ddl := []string{
		// control_metadata：控制面元数据（本机 machine id、schema version）。
		`CREATE TABLE IF NOT EXISTS control_metadata (
  key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		// machines：机器注册表。kind=local 唯一：一个 agentd 只有一个本机身份。
		`CREATE TABLE IF NOT EXISTS machines (
  id TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL, endpoint TEXT NOT NULL DEFAULT '',
  secret_ref TEXT NOT NULL DEFAULT '', protocol_version INTEGER NOT NULL DEFAULT 0,
  capabilities TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'unavailable',
  last_seen_at TIMESTAMP)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_machines_kind_local ON machines(kind) WHERE kind = 'local'`,
		// projects：用户项目。
		`CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, git_identity TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`,
		// project_locations：项目在本机/远端的位置；(project_id, role) 唯一，
		// 数据库层兜底「本机至多一个、远端至多一个」。
		`CREATE TABLE IF NOT EXISTS project_locations (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL, machine_id TEXT NOT NULL,
  machine_kind TEXT NOT NULL, role TEXT NOT NULL,
  main_workspace_id TEXT NOT NULL DEFAULT '', source TEXT NOT NULL,
  git_url TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_project_locations_project_role ON project_locations(project_id, role)`,
		// workspaces：机器工作区；(machine_id, canonical_path) 唯一保证同一机器
		// 同一路径只有一个稳定 Workspace ID。
		`CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY, machine_id TEXT NOT NULL, location_id TEXT,
  kind TEXT NOT NULL, path TEXT NOT NULL, canonical_path TEXT NOT NULL,
  repo_identity TEXT NOT NULL DEFAULT '', git_common_dir TEXT NOT NULL DEFAULT '',
  branch TEXT NOT NULL DEFAULT '', head_oid TEXT NOT NULL DEFAULT '',
  availability TEXT NOT NULL DEFAULT 'available', last_scanned_at TIMESTAMP NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_workspaces_machine_canonical ON workspaces(machine_id, canonical_path)`,
		// git_refs：ProjectLocation 下的分支引用。
		`CREATE TABLE IF NOT EXISTS git_refs (
  location_id TEXT NOT NULL, name TEXT NOT NULL, head_oid TEXT NOT NULL DEFAULT '',
  checked_out_workspace_ids TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (location_id, name))`,
		// task_summaries：跨机器任务摘要投影。
		`CREATE TABLE IF NOT EXISTS task_summaries (
  task_id TEXT PRIMARY KEY, machine_id TEXT NOT NULL, workspace_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '', executor TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL, attention INTEGER NOT NULL DEFAULT 0, updated_at TIMESTAMP NOT NULL)`,
		// operations：durable 长操作。
		`CREATE TABLE IF NOT EXISTS operations (
  operation_id TEXT PRIMARY KEY, kind TEXT NOT NULL, state TEXT NOT NULL,
  project_id TEXT NOT NULL DEFAULT '', targets TEXT NOT NULL DEFAULT '[]',
  progress TEXT NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`,
		// machine_events：所属机器 durable outbox；(machine_id, machine_seq) 唯一，
		// 让 peer catch-up 与 ApplyMachineEvent 幂等去重。
		`CREATE TABLE IF NOT EXISTS machine_events (
  id TEXT PRIMARY KEY, machine_id TEXT NOT NULL, machine_seq INTEGER NOT NULL,
  event_id TEXT NOT NULL, kind TEXT NOT NULL, resource_id TEXT NOT NULL DEFAULT '',
  payload TEXT NOT NULL, created_at TIMESTAMP NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_machine_events_machine_seq ON machine_events(machine_id, machine_seq)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_machine_events_event_id ON machine_events(machine_id, event_id)`,
		// machine_cursors：每台机器消费到哪个 machine_seq（控制面投影进度）。
		`CREATE TABLE IF NOT EXISTS machine_cursors (
  machine_id TEXT PRIMARY KEY, last_machine_seq INTEGER NOT NULL DEFAULT 0)`,
		// control_events：全局投影事件，control_revision 全局单调唯一。
		`CREATE TABLE IF NOT EXISTS control_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, control_revision INTEGER NOT NULL,
  kind TEXT NOT NULL, resource_id TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_control_events_revision ON control_events(control_revision)`,
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启桌面 schema 迁移事务: %w", err)
	}
	defer tx.Rollback()
	for _, d := range ddl {
		if _, err := tx.ExecContext(ctx, d); err != nil {
			return fmt.Errorf("桌面 schema 迁移 DDL 失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交桌面 schema 迁移: %w", err)
	}
	return nil
}
