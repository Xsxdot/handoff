// store machine_events.go：durable outbox 与事件投影入口。
//
// 职责：
//   - appendMachineEventTx：在同一 sql.Tx 内追加 (machine_id, machine_seq) 事件
//   - MachineEventsAfter：按机器、machine_seq 升序拉取 outbox（peer catch-up 用）
//   - ApplyMachineEvent：控制面投影入口——幂等记录 machine event → 更新
//     Workspace/GitRef/TaskSummary/Operation 投影 → 追加 ControlEvent →
//     更新 last_machine_seq
//
// 边界：
//   - 事件 payload 是完整可幂等 upsert 的公开投影，不含本地 secret 或文件内容
//   - 重复 (machine_id, machine_seq) 幂等忽略，不分配新 revision
package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// nextMachineSeqTx 在事务内分配该机器的下一个 machine_seq（每机器单调递增）。
func nextMachineSeqTx(ctx context.Context, tx *sql.Tx, machineID string) (int64, error) {
	var cur int64
	err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(machine_seq), 0) FROM machine_events WHERE machine_id = ?",
		machineID).Scan(&cur)
	if err != nil {
		return 0, fmt.Errorf("读取机器 %s 事件序号: %w", machineID, err)
	}
	return cur + 1, nil
}

// appendMachineEventTx 在事务内追加一条 machine event 并返回完整事件。
//
// eventID 用于跨重启幂等（机器级唯一索引兜底）；调用方负责保证 eventID 唯一。
func appendMachineEventTx(ctx context.Context, tx *sql.Tx, ev controlplane.MachineEvent) (controlplane.MachineEvent, error) {
	seq, err := nextMachineSeqTx(ctx, tx, ev.MachineID)
	if err != nil {
		return controlplane.MachineEvent{}, err
	}
	if ev.EventID == "" {
		ev.EventID = newEventID()
	}
	now := fmtTime(time.Now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO machine_events (machine_id, machine_seq, event_id, kind, resource_id, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.MachineID, seq, ev.EventID, string(ev.Kind), ev.ResourceID, string(ev.Payload), now); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("追加机器 %s 事件: %w", ev.MachineID, err)
	}
	ev.MachineSeq = seq
	ev.CreatedAt = parseTime(now)
	return ev, nil
}

// MachineEventsAfter 返回机器在 afterSeq 之后的事件，按 machine_seq 升序，最多 limit 条。
func (s *Store) MachineEventsAfter(_ context.Context, machineID string, afterSeq int64, limit int) ([]controlplane.MachineEvent, error) {
	rows, err := s.db.QueryContext(context.Background(), `
SELECT machine_id, machine_seq, event_id, kind, resource_id, payload, created_at
FROM machine_events WHERE machine_id = ? AND machine_seq > ? ORDER BY machine_seq ASC LIMIT ?`,
		machineID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("查询机器 %s 事件: %w", machineID, err)
	}
	defer rows.Close()
	var out []controlplane.MachineEvent
	for rows.Next() {
		var (
			ev        controlplane.MachineEvent
			kind      string
			payload   string
			createdAt string
		)
		if err := rows.Scan(&ev.MachineID, &ev.MachineSeq, &ev.EventID, &kind, &ev.ResourceID,
			&payload, &createdAt); err != nil {
			return nil, fmt.Errorf("读取机器 %s 事件行: %w", machineID, err)
		}
		ev.Kind = controlplane.MachineEventKind(kind)
		ev.Payload = json.RawMessage(payload)
		ev.CreatedAt = parseTime(createdAt)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历机器 %s 事件: %w", machineID, err)
	}
	return out, nil
}

// ApplyMachineEvent 把一条 machine event 投影进控制面，返回产生的 ControlEvent。
//
// 返回：
//   - ControlEvent：投影产生的控制事件（重复事件返回零值）
//   - applied：true 表示本次新投影并分配了 revision；false 表示已投影重复被幂等忽略
//   - err：数据库错误
//
// 语义（单事务）：
//  1. 按 (machine_id, event_id) 幂等记录 machine event；若本机 owner 已先写
//     outbox，则复用该行并从 cursor 判断是否尚待投影
//  2. 按 kind 解析 payload 并 upsert/remove 对应投影表
//  3. 追加 ControlEvent（全局单调 revision）
//  4. 更新 machine_cursors.last_machine_seq
//
// 为什么本机与远端走同一入口：本机 machine event 也经本方法投影，桌面 handler
// 不得为 local 分支直接查原始表（spec §8.3）。
func (s *Store) ApplyMachineEvent(ctx context.Context, ev controlplane.MachineEvent) (controlplane.ControlEvent, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.ControlEvent{}, false, fmt.Errorf("开启投影事务: %w", err)
	}
	defer tx.Rollback()

	var cursor int64
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE((SELECT last_machine_seq FROM machine_cursors WHERE machine_id = ?), 0)",
		ev.MachineID).Scan(&cursor); err != nil {
		return controlplane.ControlEvent{}, false, fmt.Errorf("读取机器 %s cursor: %w", ev.MachineID, err)
	}

	// 本机 owner 与控制面共用同一库：资源事务会先把事件写进 outbox，随后
	// projector 再消费。已存在不等于已投影，只有 seq <= cursor 才是重复。
	var (
		storedKind      string
		storedPayload   string
		storedCreatedAt string
	)
	err = tx.QueryRowContext(ctx, `
SELECT machine_seq, kind, resource_id, payload, created_at
FROM machine_events WHERE machine_id = ? AND event_id = ?`, ev.MachineID, ev.EventID).
		Scan(&ev.MachineSeq, &storedKind, &ev.ResourceID, &storedPayload, &storedCreatedAt)
	existing := err == nil
	if err != nil && err != sql.ErrNoRows {
		return controlplane.ControlEvent{}, false, fmt.Errorf("检查重复事件: %w", err)
	}
	if existing {
		if ev.MachineSeq <= cursor {
			return controlplane.ControlEvent{}, false, nil
		}
		ev.Kind = controlplane.MachineEventKind(storedKind)
		ev.Payload = json.RawMessage(storedPayload)
		ev.CreatedAt = parseTime(storedCreatedAt)
	} else if ev.MachineSeq == 0 {
		ev.MachineSeq = cursor + 1
	}
	if ev.MachineSeq != cursor+1 {
		return controlplane.ControlEvent{}, false, fmt.Errorf(
			"机器 %s 事件序号不连续: cursor=%d event=%d", ev.MachineID, cursor, ev.MachineSeq)
	}
	ev, err = normalizeMachineEventPayload(ev)
	if err != nil {
		return controlplane.ControlEvent{}, false, err
	}
	seq := ev.MachineSeq
	now := time.Now().UTC()
	if !existing {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO machine_events (machine_id, machine_seq, event_id, kind, resource_id, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ev.MachineID, seq, ev.EventID, string(ev.Kind), ev.ResourceID, string(ev.Payload), fmtTime(now)); err != nil {
			return controlplane.ControlEvent{}, false, fmt.Errorf("记录 machine event: %w", err)
		}
	}

	kind, err := projectMachineEventTx(ctx, tx, ev)
	if err != nil {
		return controlplane.ControlEvent{}, false, err
	}

	// 追加 ControlEvent：全局单调 revision。
	var rev int64
	err = tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(control_revision), 0) FROM control_events").Scan(&rev)
	if err != nil {
		return controlplane.ControlEvent{}, false, fmt.Errorf("读取控制面 revision: %w", err)
	}
	rev++
	if _, err := tx.ExecContext(ctx, `
INSERT INTO control_events (control_revision, kind, resource_id, payload, created_at)
VALUES (?, ?, ?, ?, ?)`,
		rev, string(kind), ev.ResourceID, string(ev.Payload), fmtTime(now)); err != nil {
		return controlplane.ControlEvent{}, false, fmt.Errorf("追加 control event: %w", err)
	}

	// 更新 last_machine_seq。
	if _, err := tx.ExecContext(ctx, `
INSERT INTO machine_cursors (machine_id, last_machine_seq) VALUES (?, ?)
ON CONFLICT(machine_id) DO UPDATE SET last_machine_seq = excluded.last_machine_seq`,
		ev.MachineID, seq); err != nil {
		return controlplane.ControlEvent{}, false, fmt.Errorf("更新机器 %s cursor: %w", ev.MachineID, err)
	}

	if err := tx.Commit(); err != nil {
		return controlplane.ControlEvent{}, false, fmt.Errorf("提交投影事务: %w", err)
	}
	return controlplane.ControlEvent{
		ControlRevision: rev, Kind: kind, ResourceID: ev.ResourceID,
		Payload: ev.Payload, CreatedAt: now,
	}, true, nil
}

// normalizeMachineEventPayload 在事件进入 durable machine/control 表之前净化
// 安全敏感 payload。PTY outbox 只能携带会话摘要；任何终端字节或未知字段都
// 必须在 INSERT 前拒绝，避免控制面成为隐式终端日志。
func normalizeMachineEventPayload(ev controlplane.MachineEvent) (controlplane.MachineEvent, error) {
	if ev.Kind != controlplane.MachineEventPtyUpsert && ev.Kind != controlplane.MachineEventPtyExit {
		return ev, nil
	}
	var session workspaceapi.PtySession
	decoder := json.NewDecoder(bytes.NewReader(ev.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&session); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("解析 %s 安全 payload: %w", ev.Kind, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return controlplane.MachineEvent{}, fmt.Errorf("解析 %s 安全 payload: 存在额外 JSON 内容", ev.Kind)
	}
	if session.TerminalSessionID == "" || session.TerminalSessionID != ev.ResourceID ||
		session.WorkspaceID == "" || session.Incarnation == "" || session.Shell == "" {
		return controlplane.MachineEvent{}, fmt.Errorf("%s payload 身份不完整或与 resource_id 不一致", ev.Kind)
	}
	if session.ThroughSeq < 0 {
		return controlplane.MachineEvent{}, fmt.Errorf("%s payload through_seq 不能为负数", ev.Kind)
	}
	switch ev.Kind {
	case controlplane.MachineEventPtyUpsert:
		if session.State != workspaceapi.PtyStateStarting && session.State != workspaceapi.PtyStateActive {
			return controlplane.MachineEvent{}, fmt.Errorf("%s payload state 必须是 starting/active", ev.Kind)
		}
		if session.ExitCode != nil {
			return controlplane.MachineEvent{}, fmt.Errorf("%s payload 非 ended 状态不能有 exit_code", ev.Kind)
		}
	case controlplane.MachineEventPtyExit:
		if session.State != workspaceapi.PtyStateEnded {
			return controlplane.MachineEvent{}, fmt.Errorf("%s payload state 必须是 ended", ev.Kind)
		}
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("规范化 %s payload: %w", ev.Kind, err)
	}
	ev.Payload = payload
	return ev, nil
}

// projectMachineEventTx 按 kind 更新投影表并返回对应 ControlEventKind。
func projectMachineEventTx(ctx context.Context, tx *sql.Tx, ev controlplane.MachineEvent) (controlplane.ControlEventKind, error) {
	switch ev.Kind {
	case controlplane.MachineEventWorkspaceUpsert:
		var ws controlplane.Workspace
		if err := json.Unmarshal(ev.Payload, &ws); err != nil {
			return "", fmt.Errorf("解析 workspace.upsert payload: %w", err)
		}
		if err := upsertWorkspaceTx(ctx, tx, ws); err != nil {
			return "", err
		}
		return controlplane.ControlEventKindWorkspaceUpsert, nil
	case controlplane.MachineEventWorkspaceRemove:
		// payload 无内容，resource_id 即 workspace id。
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM workspaces WHERE machine_id = ? AND id = ?", ev.MachineID, ev.ResourceID); err != nil {
			return "", fmt.Errorf("移除 workspace %s: %w", ev.ResourceID, err)
		}
		return controlplane.ControlEventKindWorkspaceRemove, nil
	case controlplane.MachineEventGitRefUpsert:
		var ref controlplane.GitRef
		if err := json.Unmarshal(ev.Payload, &ref); err != nil {
			return "", fmt.Errorf("解析 git_ref.upsert payload: %w", err)
		}
		if err := upsertGitRefTx(ctx, tx, ref); err != nil {
			return "", err
		}
		return controlplane.ControlEventKindGitRefUpsert, nil
	case controlplane.MachineEventGitRefRemove:
		var ref controlplane.GitRef
		if err := json.Unmarshal(ev.Payload, &ref); err != nil {
			return "", fmt.Errorf("解析 git_ref.remove payload: %w", err)
		}
		// 旧版事件的 payload 为 {}，没有足够身份安全删除同名分支；保留投影，
		// 等下一次 owner snapshot/Reconcile 修复。新事件必须按 location+name 删除。
		if ref.LocationID == "" || ref.Name == "" {
			return controlplane.ControlEventKindGitRefRemove, nil
		}
		if ref.Name != ev.ResourceID {
			return "", fmt.Errorf("git_ref.remove resource_id %q 与 payload name %q 不一致", ev.ResourceID, ref.Name)
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM git_refs WHERE location_id = ? AND name = ?", ref.LocationID, ref.Name); err != nil {
			return "", fmt.Errorf("移除 git ref %s/%s: %w", ref.LocationID, ref.Name, err)
		}
		return controlplane.ControlEventKindGitRefRemove, nil
	case controlplane.MachineEventTaskUpsert:
		var ts controlplane.TaskSummary
		if err := json.Unmarshal(ev.Payload, &ts); err != nil {
			return "", fmt.Errorf("解析 task.upsert payload: %w", err)
		}
		if err := upsertTaskSummaryTx(ctx, tx, ts.TaskID, ts.MachineID, ts.WorkspaceID); err != nil {
			return "", err
		}
		return controlplane.ControlEventKindTaskSummaryUpsert, nil
	case controlplane.MachineEventTaskRemove:
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM task_summaries WHERE task_id = ? AND machine_id = ?",
			ev.ResourceID, ev.MachineID); err != nil {
			return "", fmt.Errorf("移除任务摘要 %s: %w", ev.ResourceID, err)
		}
		return controlplane.ControlEventKindTaskSummaryRemove, nil
	case controlplane.MachineEventOperationUpsert:
		return controlplane.ControlEventKindOperationUpsert, nil
	case controlplane.MachineEventPtyUpsert, controlplane.MachineEventPtyExit:
		var session workspaceapi.PtySession
		if err := json.Unmarshal(ev.Payload, &session); err != nil {
			return "", fmt.Errorf("解析 %s payload: %w", ev.Kind, err)
		}
		if err := validatePtyProjectionTransitionTx(ctx, tx, ev.MachineID, session); err != nil {
			return "", err
		}
		if err := upsertPtyProjectionTx(ctx, tx, ev.MachineID, session); err != nil {
			return "", err
		}
		if ev.Kind == controlplane.MachineEventPtyExit {
			return controlplane.ControlEventKindPtyExit, nil
		}
		return controlplane.ControlEventKindPtyUpsert, nil
	default:
		return "", fmt.Errorf("未知 machine event kind %q", ev.Kind)
	}
}

func validatePtyProjectionTransitionTx(ctx context.Context, tx *sql.Tx, machineID string,
	session workspaceapi.PtySession) error {
	var existingMachineID, workspaceID, incarnation, shell string
	err := tx.QueryRowContext(ctx, `
SELECT machine_id, workspace_id, incarnation, shell
FROM pty_sessions WHERE terminal_session_id = ?`, session.TerminalSessionID).
		Scan(&existingMachineID, &workspaceID, &incarnation, &shell)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("读取 PTY %s 当前投影状态: %w", session.TerminalSessionID, err)
	}
	if err == nil && (existingMachineID != machineID || workspaceID != session.WorkspaceID ||
		incarnation != session.Incarnation || shell != session.Shell) {
		return fmt.Errorf("PTY %s 稳定身份不可跨 machine/workspace/incarnation/shell 改写", session.TerminalSessionID)
	}
	// owner 和本机控制投影共用 pty_sessions 表，owner 状态可能领先于尚未投影的
	// outbox。状态机单向性因此必须与“最后已投影事件”比较，不能误把 owner
	// 当前状态当作上一事件，否则 starting -> active -> ended 会在 catch-up 时假失败。
	var previousPayload string
	err = tx.QueryRowContext(ctx, `
SELECT payload FROM control_events
WHERE resource_id = ? AND kind IN (?, ?)
ORDER BY control_revision DESC LIMIT 1`, session.TerminalSessionID,
		string(controlplane.ControlEventKindPtyUpsert), string(controlplane.ControlEventKindPtyExit)).
		Scan(&previousPayload)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 PTY %s 上次控制投影: %w", session.TerminalSessionID, err)
	}
	var previous workspaceapi.PtySession
	if err := json.Unmarshal([]byte(previousPayload), &previous); err != nil {
		return fmt.Errorf("解析 PTY %s 上次控制投影: %w", session.TerminalSessionID, err)
	}
	if previous.WorkspaceID != session.WorkspaceID || previous.Incarnation != session.Incarnation {
		return fmt.Errorf("PTY %s 已投影身份不可改写", session.TerminalSessionID)
	}
	// ended 是外部可见的最终事实；重试只能逐字段重复同一份摘要，不能借由
	// 更大的 through_seq 或不同 exit code 把已经发布的终态改写成另一段历史。
	if previous.State == workspaceapi.PtyStateEnded {
		if !samePtySession(previous, session) {
			return fmt.Errorf("PTY %s ended 终态不可改写", session.TerminalSessionID)
		}
		return nil
	}
	if session.ThroughSeq < previous.ThroughSeq {
		return fmt.Errorf("PTY %s through_seq 不可回退: current=%d incoming=%d",
			session.TerminalSessionID, previous.ThroughSeq, session.ThroughSeq)
	}
	currentRank := ptyStateRank(previous.State)
	incomingRank := ptyStateRank(session.State)
	if currentRank == 0 || incomingRank == 0 || incomingRank < currentRank {
		return fmt.Errorf("PTY %s state 不可回退: current=%s incoming=%s",
			session.TerminalSessionID, previous.State, session.State)
	}
	return nil
}

func samePtySession(left, right workspaceapi.PtySession) bool {
	if left.TerminalSessionID != right.TerminalSessionID || left.Incarnation != right.Incarnation ||
		left.WorkspaceID != right.WorkspaceID || left.State != right.State || left.Shell != right.Shell ||
		left.ThroughSeq != right.ThroughSeq {
		return false
	}
	if left.ExitCode == nil || right.ExitCode == nil {
		return left.ExitCode == nil && right.ExitCode == nil
	}
	return *left.ExitCode == *right.ExitCode
}

func upsertPtyProjectionTx(ctx context.Context, tx *sql.Tx, machineID string,
	session workspaceapi.PtySession) error {
	var currentState string
	var currentThrough int64
	err := tx.QueryRowContext(ctx, `
SELECT state, through_seq FROM pty_sessions WHERE terminal_session_id = ?`, session.TerminalSessionID).
		Scan(&currentState, &currentThrough)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("读取 PTY %s owner 摘要: %w", session.TerminalSessionID, err)
	}
	if err == nil {
		currentRank := ptyStateRank(workspaceapi.PtyState(currentState))
		incomingRank := ptyStateRank(session.State)
		if currentRank == 0 || incomingRank == 0 {
			return fmt.Errorf("PTY %s 包含未知状态", session.TerminalSessionID)
		}
		// 本机 owner 状态可以领先于待投影 outbox；旧事件仍生成控制事件，但
		// 不能把 owner 的 durable 最新摘要倒写回旧 state/seq。
		if incomingRank < currentRank || session.ThroughSeq < currentThrough {
			return nil
		}
	}
	return upsertPtySessionTx(ctx, tx, machineID, nil, session)
}

func ptyStateRank(state workspaceapi.PtyState) int {
	switch state {
	case workspaceapi.PtyStateStarting:
		return 1
	case workspaceapi.PtyStateActive:
		return 2
	case workspaceapi.PtyStateEnded:
		return 3
	default:
		return 0
	}
}

// upsertWorkspaceTx 幂等 upsert 一个 Workspace 行。
func upsertWorkspaceTx(ctx context.Context, tx *sql.Tx, ws controlplane.Workspace) error {
	locationID := any(nil)
	if ws.LocationID != nil {
		locationID = *ws.LocationID
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO workspaces (id, machine_id, location_id, kind, path, canonical_path,
  repo_identity, git_common_dir, branch, head_oid, availability, last_scanned_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(machine_id, canonical_path) DO UPDATE SET
  location_id = excluded.location_id, kind = excluded.kind, path = excluded.path,
  repo_identity = excluded.repo_identity, git_common_dir = excluded.git_common_dir,
  branch = excluded.branch, head_oid = excluded.head_oid,
  availability = excluded.availability, last_scanned_at = excluded.last_scanned_at`,
		ws.ID, ws.MachineID, locationID, string(ws.Kind), ws.Path, ws.CanonicalPath,
		ws.RepoIdentity, ws.GitCommonDir, ws.Branch, ws.HeadOID,
		string(ws.Availability), fmtTime(ws.LastScannedAt)); err != nil {
		return fmt.Errorf("upsert workspace %s: %w", ws.ID, err)
	}
	return nil
}

// upsertGitRefTx 幂等 upsert 一个 GitRef 行。
func upsertGitRefTx(ctx context.Context, tx *sql.Tx, ref controlplane.GitRef) error {
	ids, err := json.Marshal(ref.CheckedOutWorkspaceIDs)
	if err != nil {
		return fmt.Errorf("序列化 checked_out_workspace_ids: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO git_refs (location_id, name, head_oid, checked_out_workspace_ids)
VALUES (?, ?, ?, ?)
ON CONFLICT(location_id, name) DO UPDATE SET
  head_oid = excluded.head_oid,
  checked_out_workspace_ids = excluded.checked_out_workspace_ids`,
		ref.LocationID, ref.Name, ref.HeadOID, string(ids)); err != nil {
		return fmt.Errorf("upsert git_ref %s/%s: %w", ref.LocationID, ref.Name, err)
	}
	return nil
}

// newEventID 生成事件 ID（跨重启幂等键）。
func newEventID() string {
	return uuidNewString()
}

// uuidNewString 是 google/uuid.NewString 的别名。
func uuidNewString() string {
	return uuid.NewString()
}

// UpsertWorkspaceWithMachineEvent 在同一事务内 upsert Workspace 并追加 machine
// event，保证资源更新与 outbox 同生同灭（spec §8.1）。
func (s *Store) UpsertWorkspaceWithMachineEvent(ctx context.Context, ws controlplane.Workspace, kind controlplane.MachineEventKind) (controlplane.MachineEvent, error) {
	payload, err := json.Marshal(ws)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("序列化 workspace payload: %w", err)
	}
	ev := controlplane.MachineEvent{
		MachineID: ws.MachineID, EventID: newEventID(), Kind: kind,
		ResourceID: ws.ID, Payload: payload,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("开启 workspace outbox 事务: %w", err)
	}
	defer tx.Rollback()
	if err := upsertWorkspaceTx(ctx, tx, ws); err != nil {
		return controlplane.MachineEvent{}, err
	}
	ev, err = appendMachineEventTx(ctx, tx, ev)
	if err != nil {
		return controlplane.MachineEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("提交 workspace outbox 事务: %w", err)
	}
	return ev, nil
}

// RemoveWorkspaceWithMachineEvent 在同一事务内移除 Workspace 并追加事件。
func (s *Store) RemoveWorkspaceWithMachineEvent(ctx context.Context, machineID, workspaceID string) (controlplane.MachineEvent, error) {
	ev := controlplane.MachineEvent{
		MachineID: machineID, EventID: newEventID(),
		Kind: controlplane.MachineEventWorkspaceRemove, ResourceID: workspaceID,
		Payload: json.RawMessage(`{}`),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("开启 workspace remove outbox 事务: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM workspaces WHERE machine_id = ? AND id = ?", machineID, workspaceID); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("移除 workspace %s: %w", workspaceID, err)
	}
	ev, err = appendMachineEventTx(ctx, tx, ev)
	if err != nil {
		return controlplane.MachineEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("提交 workspace remove outbox 事务: %w", err)
	}
	return ev, nil
}

// UpsertGitRefsWithMachineEvents 在同一事务内 upsert GitRef 集合并追加事件。
func (s *Store) UpsertGitRefsWithMachineEvents(ctx context.Context, locationID string, refs []controlplane.GitRef) ([]controlplane.MachineEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启 git_ref outbox 事务: %w", err)
	}
	defer tx.Rollback()
	var events []controlplane.MachineEvent
	for _, ref := range refs {
		payload, err := json.Marshal(ref)
		if err != nil {
			return nil, fmt.Errorf("序列化 git_ref payload: %w", err)
		}
		ev, err := appendMachineEventTx(ctx, tx, controlplane.MachineEvent{
			MachineID:  machineIDForLocation(ctx, tx, locationID),
			EventID:    newEventID(),
			Kind:       controlplane.MachineEventGitRefUpsert,
			ResourceID: ref.Name,
			Payload:    payload,
		})
		if err != nil {
			return nil, err
		}
		if err := upsertGitRefTx(ctx, tx, ref); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交 git_ref outbox 事务: %w", err)
	}
	return events, nil
}

// RemoveGitRefWithMachineEvent 在同一事务删除 GitRef 并追加 remove outbox。
func (s *Store) RemoveGitRefWithMachineEvent(ctx context.Context, machineID, locationID, name string) (controlplane.MachineEvent, error) {
	payload, err := json.Marshal(controlplane.GitRef{LocationID: locationID, Name: name})
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("序列化 git ref remove payload: %w", err)
	}
	ev := controlplane.MachineEvent{MachineID: machineID, EventID: newEventID(),
		Kind: controlplane.MachineEventGitRefRemove, ResourceID: name, Payload: payload}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("开启 git ref remove 事务: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM git_refs WHERE location_id = ? AND name = ?", locationID, name); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("删除 git ref %s/%s: %w", locationID, name, err)
	}
	ev, err = appendMachineEventTx(ctx, tx, ev)
	if err != nil {
		return controlplane.MachineEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("提交 git ref remove 事务: %w", err)
	}
	return ev, nil
}

// machineIDForLocation 从 project_locations 反查 location 所属机器。
func machineIDForLocation(ctx context.Context, tx *sql.Tx, locationID string) string {
	var machineID string
	if err := tx.QueryRowContext(ctx,
		"SELECT machine_id FROM project_locations WHERE id = ?", locationID).Scan(&machineID); err != nil {
		return ""
	}
	return machineID
}

// AppendTaskSummaryEvent 追加任务摘要事件（Task 创建/更新时调用）。
func (s *Store) AppendTaskSummaryEvent(ctx context.Context, summary controlplane.TaskSummary) (controlplane.MachineEvent, error) {
	payload, err := json.Marshal(summary)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("序列化 task summary payload: %w", err)
	}
	ev := controlplane.MachineEvent{
		MachineID: summary.MachineID, EventID: newEventID(),
		Kind:       controlplane.MachineEventTaskUpsert,
		ResourceID: summary.TaskID, Payload: payload,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("开启 task summary outbox 事务: %w", err)
	}
	defer tx.Rollback()
	ev, err = appendMachineEventTx(ctx, tx, ev)
	if err != nil {
		return controlplane.MachineEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("提交 task summary outbox 事务: %w", err)
	}
	return ev, nil
}

// CreateTaskWithMachineEvent 在同一事务内创建 Task、upsert TaskSummary 并追加
// task.upsert outbox 事件（spec §8.1：Task 创建必须先落库再启动 adapter）。
//
// 返回完整 machine event（带 machine_seq）。
func (s *Store) CreateTaskWithMachineEvent(ctx context.Context, task *proto.Task) (controlplane.MachineEvent, error) {
	summary := taskToSummary(task)
	payload, err := json.Marshal(summary)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("序列化 task summary payload: %w", err)
	}
	ev := controlplane.MachineEvent{
		MachineID: task.MachineID, EventID: newEventID(),
		Kind:       controlplane.MachineEventTaskUpsert,
		ResourceID: task.ID, Payload: payload,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("开启任务创建 outbox 事务: %w", err)
	}
	defer tx.Rollback()

	if err := createTaskTx(ctx, tx, task); err != nil {
		return controlplane.MachineEvent{}, err
	}
	if err := upsertTaskSummaryTx(ctx, tx, summary.TaskID, summary.MachineID, summary.WorkspaceID); err != nil {
		return controlplane.MachineEvent{}, err
	}
	ev, err = appendMachineEventTx(ctx, tx, ev)
	if err != nil {
		return controlplane.MachineEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("提交任务创建 outbox 事务: %w", err)
	}
	return ev, nil
}

// UpdateTaskStateWithEvent 在同一事务内迁移任务状态、upsert TaskSummary 并追加
// task.upsert outbox 事件。
//
// 状态迁移合法性仍由 proto.CanTransit + CAS 守卫（WHERE state=旧值）保证。
func (s *Store) UpdateTaskStateWithEvent(ctx context.Context, id string, st proto.TaskState) (controlplane.MachineEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("开启状态更新 outbox 事务: %w", err)
	}
	defer tx.Rollback()

	var (
		curState  proto.TaskState
		machineID string
		wsID      string
	)
	err = tx.QueryRowContext(ctx,
		"SELECT state, machine_id, workspace_id FROM tasks WHERE id = ?", id).
		Scan(&curState, &machineID, &wsID)
	if err == sql.ErrNoRows {
		return controlplane.MachineEvent{}, ErrNotFound
	}
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("读取任务 %s: %w", id, err)
	}
	if !proto.CanTransit(curState, st) {
		log().Warn("非法状态迁移被拒绝", "task", id, "from", curState, "to", st)
		return controlplane.MachineEvent{}, ErrBadTransit
	}
	res, err := tx.ExecContext(ctx,
		"UPDATE tasks SET state = ?, updated_at = ? WHERE id = ? AND state = ?",
		st, fmtTime(time.Now()), id, curState)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("更新任务 %s 状态: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("读取更新任务 %s 状态影响行数: %w", id, err)
	}
	if affected == 0 {
		log().Warn("状态迁移被并发变更拒绝", "task", id, "from", curState, "to", st)
		return controlplane.MachineEvent{}, ErrBadTransit
	}

	// 状态更新与 task.upsert outbox 同事务。
	ev, err := appendTaskUpsertTx(ctx, tx, id, machineID, wsID)
	if err != nil {
		return controlplane.MachineEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("提交状态更新 outbox 事务: %w", err)
	}
	return ev, nil
}

// appendTaskUpsertTx 在事务内按当前任务行 upsert summary 并追加 task.upsert 事件。
func appendTaskUpsertTx(ctx context.Context, tx *sql.Tx, taskID, machineID, wsID string) (controlplane.MachineEvent, error) {
	if machineID == "" {
		// 未绑定机器的任务不产生 outbox（旧任务迁移前）。
		return controlplane.MachineEvent{}, nil
	}
	if err := upsertTaskSummaryTx(ctx, tx, taskID, machineID, wsID); err != nil {
		return controlplane.MachineEvent{}, err
	}
	var (
		summary   controlplane.TaskSummary
		state     string
		updatedAt string
	)
	if err := tx.QueryRowContext(ctx, `
SELECT task_id, machine_id, workspace_id, name, executor, state, attention, updated_at
FROM task_summaries WHERE task_id = ?`, taskID).Scan(
		&summary.TaskID, &summary.MachineID, &summary.WorkspaceID,
		&summary.Name, &summary.Executor, &state, &summary.Attention, &updatedAt); err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("读取任务 %s 完整事件摘要: %w", taskID, err)
	}
	summary.State = controlplane.TaskSummaryState(state)
	summary.UpdatedAt = parseTime(updatedAt)
	payload, err := json.Marshal(summary)
	if err != nil {
		return controlplane.MachineEvent{}, fmt.Errorf("序列化 task summary payload: %w", err)
	}
	return appendMachineEventTx(ctx, tx, controlplane.MachineEvent{
		MachineID: machineID, EventID: newEventID(),
		Kind:       controlplane.MachineEventTaskUpsert,
		ResourceID: taskID, Payload: payload,
	})
}

// taskToSummary 把 proto.Task 投影为 TaskSummary。
func taskToSummary(task *proto.Task) controlplane.TaskSummary {
	return controlplane.TaskSummary{
		TaskID: task.ID, MachineID: task.MachineID, WorkspaceID: task.WorkspaceID,
		Name: task.Name, Executor: task.Executor, State: controlplane.TaskSummaryState(task.State),
		UpdatedAt: task.UpdatedAt,
	}
}

// createTaskTx 在事务内创建任务行。
func createTaskTx(ctx context.Context, tx *sql.Tx, task *proto.Task) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO tasks (id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at,
  name, executor, model, work_dir, worktree_managed, machine_id, workspace_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Target, task.RepoPath, task.Branch, task.PlanPath, task.PlanSummary,
		task.ExecutorSession, task.State, fmtTime(task.CreatedAt), fmtTime(task.UpdatedAt),
		task.Name, task.Executor, task.Model, task.WorkDir, boolToInt(task.WorktreeManaged),
		task.MachineID, task.WorkspaceID)
	if err != nil {
		return fmt.Errorf("写入任务 %s: %w", task.ID, err)
	}
	return nil
}
