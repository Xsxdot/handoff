// store PTY session 持久化：owner 元数据、command 幂等与 machine outbox。
//
// 职责：
//   - command_id 幂等创建稳定 session/incarnation
//   - session 状态变更与 pty.upsert/pty.exit machine event 同事务
//   - agentd 启动时把遗留 starting/active session 原位标 ended
//   - 为控制 agentd 缓存远端 session 摘要以解析无 workspace 的 session route
//
// 边界：
//   - 不保存 terminal input/output，不启动或终止进程
//   - 业务状态机由 ptyservice 负责；本层只做原子持久化
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// CreatePtySessionWithMachineEvent 幂等创建 PTY session 与 owner outbox 事件。
func (s *Store) CreatePtySessionWithMachineEvent(ctx context.Context, machineID, commandID string,
	session workspaceapi.PtySession) (workspaceapi.PtySession, bool, controlplane.MachineEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workspaceapi.PtySession{}, false, controlplane.MachineEvent{}, fmt.Errorf("开启 PTY 创建事务: %w", err)
	}
	defer tx.Rollback()
	now := fmtTime(time.Now())
	result, err := tx.ExecContext(ctx, `
INSERT INTO pty_sessions (terminal_session_id, command_id, machine_id, workspace_id,
  incarnation, state, shell, through_seq, exit_code, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(command_id) DO NOTHING`, session.TerminalSessionID, commandID, machineID,
		session.WorkspaceID, session.Incarnation, string(session.State), session.Shell,
		session.ThroughSeq, nullableExitCode(session.ExitCode), now, now)
	if err != nil {
		return workspaceapi.PtySession{}, false, controlplane.MachineEvent{}, fmt.Errorf("创建 PTY session %s: %w", session.TerminalSessionID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return workspaceapi.PtySession{}, false, controlplane.MachineEvent{}, fmt.Errorf("读取 PTY 创建结果: %w", err)
	}
	if rows == 0 {
		existing, err := getPtySessionByCommandIDTx(ctx, tx, commandID)
		if err != nil {
			return workspaceapi.PtySession{}, false, controlplane.MachineEvent{}, err
		}
		if err := tx.Commit(); err != nil {
			return workspaceapi.PtySession{}, false, controlplane.MachineEvent{}, fmt.Errorf("提交 PTY 幂等事务: %w", err)
		}
		return existing, false, controlplane.MachineEvent{}, nil
	}
	event, err := appendPtyEventTx(ctx, tx, machineID, controlplane.MachineEventPtyUpsert, session)
	if err != nil {
		return workspaceapi.PtySession{}, false, controlplane.MachineEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return workspaceapi.PtySession{}, false, controlplane.MachineEvent{}, fmt.Errorf("提交 PTY 创建事务: %w", err)
	}
	return session, true, event, nil
}

// UpdatePtySessionWithMachineEvent 原子更新 PTY 元数据并追加指定 outbox 事件。
func (s *Store) UpdatePtySessionWithMachineEvent(ctx context.Context, machineID string,
	session workspaceapi.PtySession, kind controlplane.MachineEventKind) (controlplane.MachineEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("开启 PTY 更新事务: %w", err)
	}
	defer tx.Rollback()
	if err := upsertPtySessionTx(ctx, tx, machineID, nil, session); err != nil {
		return controlplane.MachineEvent{}, err
	}
	event, err := appendPtyEventTx(ctx, tx, machineID, kind, session)
	if err != nil {
		return controlplane.MachineEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("提交 PTY 更新事务: %w", err)
	}
	return event, nil
}

// GetPtySession 按稳定 session ID 读取 PTY 元数据。
func (s *Store) GetPtySession(ctx context.Context, sessionID string) (workspaceapi.PtySession, error) {
	return scanPtySession(s.db.QueryRowContext(ctx, ptySelect+" WHERE terminal_session_id = ?", sessionID))
}

// GetPtySessionByCommandID 按幂等 command ID 读取 PTY 元数据。
func (s *Store) GetPtySessionByCommandID(ctx context.Context, commandID string) (workspaceapi.PtySession, error) {
	return scanPtySession(s.db.QueryRowContext(ctx, ptySelect+" WHERE command_id = ?", commandID))
}

// CachePtySession 缓存已由远端 owner 返回的 session 摘要，不生成 owner event。
func (s *Store) CachePtySession(ctx context.Context, machineID string, session workspaceapi.PtySession) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启 PTY 摘要缓存事务: %w", err)
	}
	defer tx.Rollback()
	if err := upsertPtySessionTx(ctx, tx, machineID, nil, session); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 PTY 摘要缓存事务: %w", err)
	}
	return nil
}

// EndActivePtySessionsWithMachineEvents 把进程重启后不可能仍被本进程持有的
// starting/active session 原位标 ended；保留 ID/incarnation，绝不重绑新 shell。
func (s *Store) EndActivePtySessionsWithMachineEvents(ctx context.Context, machineID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("开启 PTY 启动恢复事务: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, ptySelect+" WHERE machine_id = ? AND state IN ('starting','active')", machineID)
	if err != nil {
		return 0, fmt.Errorf("查询遗留 PTY session: %w", err)
	}
	var sessions []workspaceapi.PtySession
	for rows.Next() {
		session, scanErr := scanPtySession(rows)
		if scanErr != nil {
			rows.Close()
			return 0, scanErr
		}
		sessions = append(sessions, session)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("关闭遗留 PTY rows: %w", err)
	}
	for _, session := range sessions {
		session.State = workspaceapi.PtyStateEnded
		if err := upsertPtySessionTx(ctx, tx, machineID, nil, session); err != nil {
			return 0, err
		}
		if _, err := appendPtyEventTx(ctx, tx, machineID, controlplane.MachineEventPtyExit, session); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交 PTY 启动恢复事务: %w", err)
	}
	return len(sessions), nil
}

const ptySelect = `SELECT terminal_session_id, incarnation, workspace_id, state,
  shell, through_seq, exit_code FROM pty_sessions`

type rowScanner interface{ Scan(...any) error }

func scanPtySession(row rowScanner) (workspaceapi.PtySession, error) {
	var session workspaceapi.PtySession
	var state string
	var exitCode sql.NullInt64
	if err := row.Scan(&session.TerminalSessionID, &session.Incarnation, &session.WorkspaceID,
		&state, &session.Shell, &session.ThroughSeq, &exitCode); err != nil {
		if err == sql.ErrNoRows {
			return workspaceapi.PtySession{}, ErrNotFound
		}
		return workspaceapi.PtySession{}, fmt.Errorf("读取 PTY session: %w", err)
	}
	session.State = workspaceapi.PtyState(state)
	if exitCode.Valid {
		value := int(exitCode.Int64)
		session.ExitCode = &value
	}
	return session, nil
}

func getPtySessionByCommandIDTx(ctx context.Context, tx *sql.Tx, commandID string) (workspaceapi.PtySession, error) {
	return scanPtySession(tx.QueryRowContext(ctx, ptySelect+" WHERE command_id = ?", commandID))
}

func upsertPtySessionTx(ctx context.Context, tx *sql.Tx, machineID string, commandID any,
	session workspaceapi.PtySession) error {
	now := fmtTime(time.Now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO pty_sessions (terminal_session_id, command_id, machine_id, workspace_id,
  incarnation, state, shell, through_seq, exit_code, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(terminal_session_id) DO UPDATE SET machine_id = excluded.machine_id,
  workspace_id = excluded.workspace_id, incarnation = excluded.incarnation,
  state = excluded.state, shell = excluded.shell, through_seq = excluded.through_seq,
  exit_code = excluded.exit_code, updated_at = excluded.updated_at`,
		session.TerminalSessionID, commandID, machineID, session.WorkspaceID, session.Incarnation,
		string(session.State), session.Shell, session.ThroughSeq, nullableExitCode(session.ExitCode), now, now); err != nil {
		return fmt.Errorf("upsert PTY session %s: %w", session.TerminalSessionID, err)
	}
	return nil
}

func appendPtyEventTx(ctx context.Context, tx *sql.Tx, machineID string, kind controlplane.MachineEventKind,
	session workspaceapi.PtySession) (controlplane.MachineEvent, error) {
	payload, err := json.Marshal(session)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("序列化 PTY session: %w", err)
	}
	return appendMachineEventTx(ctx, tx, controlplane.MachineEvent{
		MachineID: machineID, EventID: newEventID(), Kind: kind,
		ResourceID: session.TerminalSessionID, Payload: payload,
	})
}

func nullableExitCode(exitCode *int) any {
	if exitCode == nil {
		return nil
	}
	return *exitCode
}
