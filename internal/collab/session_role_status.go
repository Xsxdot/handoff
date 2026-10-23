package collab

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// SessionTaskObservation is one already-read local task or cached mirror snapshot.
type SessionTaskObservation struct {
	Target, State string
	FetchedAt     time.Time // Remote snapshot arrival, explanatory only.
	UpdatedAt     time.Time // Source task-state clock; never exposed as a role start.
	Err           error
	Missing       bool
	Cached        bool
}

// ApplySessionTaskObservations applies task lifecycle state in the collaboration projection.
// Snapshot/update time is explanatory only and never becomes a state-start timestamp.
func ApplySessionTaskObservations(detail *proto.SessionDetail, observations map[string]SessionTaskObservation) {
	if detail == nil || detail.RoleStatus == nil {
		return
	}
	for i := range detail.RoleStatus.Roles {
		role := &detail.RoleStatus.Roles[i]
		if role.Kind != "executor" {
			continue
		}
		observation, ok := observations[role.TaskID]
		if !ok {
			continue
		}
		if observation.Err != nil || observation.Missing || (observation.Target != "" && observation.Target != role.Target) {
			if role.Basis.TaskType != "delivery_failed" && role.Basis.TaskType != "turn_failed" && role.Basis.TaskType != "failed" && role.State != "unknown" {
				role.Since = nil
			}
			if role.Basis.TaskType != "delivery_failed" && role.Basis.TaskType != "turn_failed" && role.Basis.TaskType != "failed" {
				role.State = "unknown"
			}
			if observation.Err != nil {
				role.Reason = "任务状态读取失败"
			} else if observation.Target != "" {
				role.Reason = "任务快照目标与当前派发不符"
			} else {
				role.Reason = "没有可读取的任务状态快照"
			}
			role.Conclusion = "任务当前状态未知；最近动态保留账本已观测事实"
			basis := role.Basis
			basis.Source = "snapshot_unknown"
			role.Basis = basis
			applyCoordinatorObservation(detail, role.CardID, role.TaskID, basis, "unknown", role.Reason)
			continue
		}
		if role.Basis.TaskType == "delivery_failed" {
			// Task snapshots describe task lifecycle, not whether this specific
			// ticket answer reached the executor. Keep the explicit delivery error
			// until a newer task_mirrored lifecycle event replaces its ledger basis.
			continue
		}
		coordinatorState, coordinatorReason := "unknown", "任务快照不能证明协调者仍在运行"
		priorState := role.State
		basis := role.Basis
		if observation.Cached && snapshotConflictsWithRole(role, observation.State) &&
			(observation.UpdatedAt.IsZero() || basis.At.IsZero() || !observation.UpdatedAt.After(basis.At)) {
			// A remote mirror may lag the card ledger. Without a comparable source
			// clock it cannot replace a newer explicit fact or invent current state.
			continue
		}
		switch observation.State {
		case string(proto.TaskStateRunning):
			role.State = "running"
			role.Conclusion = "最近任务快照为运行中"
			basis.TaskType = "" // A new live round supersedes prior terminal/failure evidence.
		case string(proto.TaskStateWaitingAnswer):
			if role.Basis.TaskType == "delivery_failed" {
				role.State = "exception"
				role.Conclusion = latestFailureSummary(role.Recent)
				coordinatorState, coordinatorReason = "exception", role.Conclusion
			} else {
				role.State = "waiting"
				role.Conclusion = "最近任务快照显示等待答复"
			}
		case string(proto.TaskStateWaitingReview):
			if role.Basis.TaskType == "turn_failed" || role.Basis.TaskType == "failed" || role.Basis.TaskType == "delivery_failed" {
				role.State = "exception"
				role.Conclusion = latestFailureSummary(role.Recent)
				if role.Basis.TaskType == "delivery_failed" || role.Basis.TaskType == "failed" {
					coordinatorState, coordinatorReason = "exception", role.Conclusion
				} else {
					coordinatorState, coordinatorReason = "waiting", "执行回合已结束，等待协调者接续"
				}
			} else {
				role.State = "idle"
				role.Conclusion = "执行回合已结束，等待协调者接续"
				role.WaitingFor = "协调者"
				coordinatorState, coordinatorReason = "waiting", "执行回合已结束，等待协调者接续"
			}
		case string(proto.TaskStateCompleted):
			role.State = "idle"
			role.Conclusion = "最近任务快照显示回合已完成"
			role.WaitingFor = ""
			coordinatorState, coordinatorReason = "waiting", "执行回合已结束，等待协调者接续"
		case "archived":
			role.State = "idle"
			role.Conclusion = "最近任务快照显示执行任务已归档"
			role.WaitingFor = ""
			coordinatorState, coordinatorReason = "waiting", "执行回合已结束并归档，等待协调者接续"
		case string(proto.TaskStateFailed):
			role.State = "exception"
			role.Conclusion = "最近任务快照显示明确失败"
			coordinatorState, coordinatorReason = "exception", "执行任务明确失败，需要协调者处理"
		default:
			role.State = "unknown"
			role.Conclusion = "任务快照没有可识别的运行状态"
		}
		if role.State != priorState {
			role.Since = nil
		}
		basis.Source = "task_snapshot"
		if !observation.UpdatedAt.IsZero() {
			basis.At = observation.UpdatedAt.UTC()
		}
		role.Basis = basis
		if !observation.FetchedAt.IsZero() {
			role.Reason = "任务快照观测于 " + observation.FetchedAt.UTC().Format(time.RFC3339)
		}
		applyCoordinatorObservation(detail, role.CardID, role.TaskID, basis, coordinatorState, coordinatorReason)
	}
}

func snapshotConflictsWithRole(role *proto.SessionRole, taskState string) bool {
	if role == nil {
		return false
	}
	switch taskState {
	case string(proto.TaskStateRunning):
		return role.State != "running"
	case string(proto.TaskStateWaitingAnswer):
		return role.State != "waiting" && !(role.State == "exception" && role.Basis.TaskType == "delivery_failed")
	case string(proto.TaskStateWaitingReview), string(proto.TaskStateCompleted), "archived":
		return role.State != "idle" && role.State != "exception"
	case string(proto.TaskStateFailed):
		return role.State != "exception"
	default:
		return false
	}
}

func latestFailureSummary(recent []proto.SessionRoleActivity) string {
	for _, activity := range recent {
		if activity.Source == "task_mirrored" && activity.Tone == "failed" {
			return activity.Summary
		}
	}
	return "执行回合明确失败"
}

func roleLifecycleTaskType(typ string) bool {
	switch typ {
	case "permission_request", "question", "ticket_answered", "completed", "turn_failed", "failed", "archived", "delivery_failed":
		return true
	default:
		return false
	}
}

func latestUndeliveredTaskEvent(events []proto.LedgerEvent, target, task string) (proto.LedgerEvent, bool) {
	var latest proto.LedgerEvent
	found := false
	for _, ev := range events {
		if ev.Type != "task_mirrored" || ev.SourceTarget != target || ev.SourceTask != task || taskMirrorPayload(ev.Payload).TaskType != "delivery_failed" {
			continue
		}
		if !found || ev.SourceSeq > latest.SourceSeq {
			latest, found = ev, true
		}
	}
	return latest, found
}

func applyCoordinatorObservation(detail *proto.SessionDetail, cardID, taskID string, taskBasis proto.SessionRoleBasis, state, reason string) {
	if cardID == "" {
		return
	}
	for i := range detail.RoleStatus.Roles {
		role := &detail.RoleStatus.Roles[i]
		if role.Kind != "coordinator" || role.CardID != cardID {
			continue
		}
		// Reconcile only the coordinator state derived from this task. Independent
		// or newer wake-round evidence is authoritative for the coordinator seat.
		if role.Basis.Source == "wake_round" {
			if role.Basis.DispatchSeq == taskBasis.DispatchSeq {
				continue // Same dispatch: wake facts describe coordinator, not executor state.
			}
		} else if (role.Basis.Source != "task_mirrored" && role.Basis.Source != "task_snapshot") || role.Basis.TaskID != taskID || role.Basis.Seq > taskBasis.Seq {
			continue
		}
		if state == "unknown" && role.State != "waiting" && role.State != "exception" {
			continue
		}
		priorState := role.State
		role.State = state
		if priorState != state {
			role.Since = nil // Snapshot fetch time is not a state-start time.
		}
		role.WaitingFor = ""
		role.Reason = reason
		role.Conclusion = reason
		role.Basis = proto.SessionRoleBasis{Source: "task_snapshot", Seq: taskBasis.Seq, TaskID: taskID, DispatchSeq: taskBasis.DispatchSeq, TaskType: taskBasis.TaskType, At: taskBasis.At}
	}
}

// sessionRoleStatus projects only facts returned by the card-scoped bounded ledger reads.
// Task source_seq values are compared only within their own target/task identity.
func sessionRoleStatus(summary proto.SessionSummary, read client.RoleActivityRead, tickets client.RoleTicketRead, observedAt time.Time) *proto.SessionRoleStatus {
	truncated := map[string]bool{}
	for _, id := range read.TruncatedCards {
		truncated[id] = true
	}
	byCard := map[string][]proto.LedgerEvent{}
	latestLifecycleTask := map[string]proto.LedgerEvent{}
	latestDispatch := map[string]struct {
		Seq            int64
		Target, TaskID string
	}{}
	for _, ev := range read.Events {
		byCard[ev.CardID] = append(byCard[ev.CardID], ev)
		if ev.Type == "dispatched" {
			var p struct {
				Target string `json:"target"`
				TaskID string `json:"task_id"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil {
				prior := latestDispatch[ev.CardID]
				if ev.Seq > prior.Seq {
					latestDispatch[ev.CardID] = struct {
						Seq            int64
						Target, TaskID string
					}{ev.Seq, p.Target, p.TaskID}
				}
			}
		}
		if ev.Type != "task_mirrored" || ev.SourceTask == "" {
			continue
		}
		if !roleLifecycleTaskType(taskMirrorPayload(ev.Payload).TaskType) {
			continue // Raw progress/audit remains recent activity, never a lifecycle replacement.
		}
		key := ev.CardID + "\x00" + ev.SourceTarget + "\x00" + ev.SourceTask
		prior, ok := latestLifecycleTask[key]
		if !ok || ev.SourceSeq > prior.SourceSeq {
			latestLifecycleTask[key] = ev
		}
	}
	status := &proto.SessionRoleStatus{ObservedAt: observedAt.UTC(), Roles: []proto.SessionRole{}}
	for _, member := range summary.Members {
		if member.Kind != proto.SessionMemberResident {
			continue
		}
		residentRoundsAllowed := member.DriverMode == proto.ResidentDriverPush
		recent := make([]proto.SessionRoleActivity, 0, len(read.SessionEvents))
		for _, event := range read.SessionEvents {
			if event.Actor != member.Identity {
				continue
			}
			if event.Type == "resident_round" && (!residentRoundsAllowed || event.CreatedAt.Before(member.ResidentSatAt)) {
				continue
			}
			if activity, ok := roleActivity(event); ok {
				recent = append(recent, activity)
			}
		}
		sort.SliceStable(recent, func(i, j int) bool {
			if recent[i].Time.Equal(recent[j].Time) {
				return recent[i].Seq > recent[j].Seq
			}
			return recent[i].Time.After(recent[j].Time)
		})
		if len(recent) > 5 {
			recent = recent[:5]
		}
		count := 0
		for _, event := range read.SessionEvents {
			if event.Actor == member.Identity {
				if event.Type == "resident_round" && (!residentRoundsAllowed || event.CreatedAt.Before(member.ResidentSatAt)) {
					continue
				}
				if _, ok := roleActivity(event); ok {
					count++
				}
			}
		}
		resident := proto.SessionRole{ID: "resident:" + member.Identity, Kind: "resident", Identity: member.Identity,
			State: "unknown", Conclusion: "没有可观测的驻场者回合记录", Recent: recent,
			MoreActivity: read.SessionEventsTruncated || len(read.SessionEvents) >= 100 || count > 5, TicketRefs: []proto.SessionTicketRef{}}
		if residentRoundsAllowed {
			if round, ok := latestResidentRound(read.SessionEvents, member.Identity, member.ResidentSatAt); ok {
				resident.State = round.state
				resident.Since = validTime(round.at, observedAt)
				resident.Conclusion = round.conclusion
				resident.Reason = round.reason
			}
		}
		status.Roles = append(status.Roles, resident)
	}
	for _, card := range summary.Cards {
		events := byCard[card.CardID]
		dispatch := latestDispatch[card.CardID]
		executorCreated := false
		for _, event := range events {
			if event.Type != "task_mirrored" || event.SourceTask != dispatch.TaskID || event.SourceTarget != dispatch.Target || dispatch.TaskID == "" {
				continue
			}
			latest := latestLifecycleTask[card.CardID+"\x00"+event.SourceTarget+"\x00"+event.SourceTask]
			if latest.Seq != event.Seq {
				continue
			}
			payload := taskMirrorPayload(event.Payload)
			state, conclusion := taskRoleState(payload.TaskType)
			executor := proto.SessionRole{ID: "executor:" + event.SourceTask, Kind: "executor", CardID: card.CardID,
				TaskID: event.SourceTask, Target: event.SourceTarget, State: state, Since: validTime(event.CreatedAt, observedAt), Conclusion: conclusion,
				Recent: recentRoleEvents(events, event.SourceTarget, event.SourceTask, truncated[card.CardID]), TicketRefs: []proto.SessionTicketRef{},
				Basis: proto.SessionRoleBasis{Source: "task_mirrored", Seq: event.Seq, TaskID: event.SourceTask, DispatchSeq: dispatch.Seq, TaskType: payload.TaskType, At: event.CreatedAt.UTC()}}
			if failed, ok := latestUndeliveredTaskEvent(events, event.SourceTarget, event.SourceTask); ok && failed.SourceSeq >= event.SourceSeq {
				executor.State = "exception"
				executor.Since = validTime(failed.CreatedAt, observedAt)
				executor.Conclusion = deliveryFailureSummary(failed.Payload)
				executor.Reason = deliveryFailureReason(failed.Payload)
				executor.Basis = proto.SessionRoleBasis{Source: "task_mirrored", Seq: failed.Seq, TaskID: event.SourceTask, DispatchSeq: dispatch.Seq, TaskType: "delivery_failed", At: failed.CreatedAt.UTC()}
			}
			for _, ticket := range tickets.Tickets {
				if ticket.CardID == card.CardID && ticket.TaskID == event.SourceTask && ticket.Target == event.SourceTarget {
					executor.TicketRefs = append(executor.TicketRefs, ticket)
				}
			}
			executor.MoreActivity = truncated[card.CardID] || roleActivityCount(events, event.SourceTarget, event.SourceTask) > 5
			status.Roles = append(status.Roles, executor)
			executorCreated = true
		}
		if dispatch.TaskID != "" && !executorCreated {
			status.Roles = append(status.Roles, proto.SessionRole{ID: "executor:" + dispatch.TaskID, Kind: "executor", CardID: card.CardID, TaskID: dispatch.TaskID, Target: dispatch.Target,
				State: "unknown", Conclusion: "已派发，尚无可用任务镜像", Recent: []proto.SessionRoleActivity{}, TicketRefs: []proto.SessionTicketRef{},
				Basis: proto.SessionRoleBasis{Source: "dispatched", Seq: dispatch.Seq, TaskID: dispatch.TaskID, DispatchSeq: dispatch.Seq}})
		}
		if card.Seat == "" {
			continue
		}
		role := proto.SessionRole{ID: "coordinator:" + card.CardID, Kind: "coordinator", Identity: card.Seat, CardID: card.CardID,
			State: "unknown", Conclusion: "没有协调者运行记录", Recent: []proto.SessionRoleActivity{}, TicketRefs: []proto.SessionTicketRef{}, CanRequestWake: card.Status != "已完成" && card.Status != "终止"}
		role.Recent = recentRoleEvents(events, "", "", truncated[card.CardID])
		role.MoreActivity = truncated[card.CardID] || roleActivityCount(events, "", "") > 5
		var latestWakeSeq int64
		chronological := append([]proto.LedgerEvent(nil), events...)
		sort.SliceStable(chronological, func(i, j int) bool { return chronological[i].Seq < chronological[j].Seq })
		for _, ev := range chronological {
			if ev.Type == "comment" {
				if phase, reason, ok := wakeRoundInfo(ev); ok && ev.Seq >= latestWakeSeq && ev.Seq >= latestDispatch[card.CardID].Seq && currentSeatEvent(events, card.Seat, ev.Seq) {
					latestWakeSeq = ev.Seq
					switch phase {
					case "start":
						role.State = "running"
						role.Since = validTime(ev.CreatedAt, observedAt)
						role.Conclusion = "最近观测到协调者唤醒开始"
						role.Basis = proto.SessionRoleBasis{Source: "wake_round", Seq: ev.Seq, TaskID: latestDispatch[card.CardID].TaskID, DispatchSeq: latestDispatch[card.CardID].Seq, At: ev.CreatedAt.UTC()}
					case "fail":
						role.State = "exception"
						role.Since = validTime(ev.CreatedAt, observedAt)
						role.Conclusion = "协调者唤醒失败"
						role.Reason = reason
						role.Basis = proto.SessionRoleBasis{Source: "wake_round", Seq: ev.Seq, TaskID: latestDispatch[card.CardID].TaskID, DispatchSeq: latestDispatch[card.CardID].Seq, At: ev.CreatedAt.UTC()}
					default:
						role.State = "unknown"
						role.Since = nil
						role.Conclusion = "最近一次协调者唤醒已结束，当前运行状态未知"
						role.Basis = proto.SessionRoleBasis{Source: "wake_round", Seq: ev.Seq, TaskID: latestDispatch[card.CardID].TaskID, DispatchSeq: latestDispatch[card.CardID].Seq, At: ev.CreatedAt.UTC()}
					}
				}
			}
			if ev.Type != "task_mirrored" || ev.SourceTask == "" {
				continue
			}
			dispatch := latestDispatch[card.CardID]
			if dispatch.TaskID != ev.SourceTask || dispatch.Target != ev.SourceTarget {
				continue
			}
			latest := latestLifecycleTask[card.CardID+"\x00"+ev.SourceTarget+"\x00"+ev.SourceTask]
			if latest.Seq != ev.Seq {
				continue
			}
			payload := taskMirrorPayload(ev.Payload)
			if (payload.TaskType == "completed" || payload.TaskType == "turn_failed" || payload.TaskType == "archived") && ev.Seq > latestWakeSeq {
				if card.Status != "已完成" && card.Status != "终止" {
					role.State = "waiting"
					role.Since = validTime(ev.CreatedAt, observedAt)
					role.Conclusion = "执行回合已结束，等待协调者接续"
					role.WaitingFor = "协调者"
					role.Basis = proto.SessionRoleBasis{Source: "task_mirrored", Seq: ev.Seq, TaskID: ev.SourceTask, DispatchSeq: dispatch.Seq, TaskType: payload.TaskType, At: ev.CreatedAt.UTC()}
				}
			}
			if payload.TaskType == "failed" && ev.Seq > latestWakeSeq && card.Status != "已完成" && card.Status != "终止" {
				role.State = "exception"
				role.Since = validTime(ev.CreatedAt, observedAt)
				role.Conclusion = "执行任务明确失败"
				role.Basis = proto.SessionRoleBasis{Source: "task_mirrored", Seq: ev.Seq, TaskID: ev.SourceTask, DispatchSeq: dispatch.Seq, TaskType: payload.TaskType, At: ev.CreatedAt.UTC()}
			}
			if payload.TaskType == "delivery_failed" && ev.Seq > latestWakeSeq && card.Status != "已完成" && card.Status != "终止" {
				role.State = "exception"
				role.Since = validTime(ev.CreatedAt, observedAt)
				role.Conclusion = deliveryFailureSummary(ev.Payload)
				role.Reason = deliveryFailureReason(ev.Payload)
				role.Basis = proto.SessionRoleBasis{Source: "task_mirrored", Seq: ev.Seq, TaskID: ev.SourceTask, DispatchSeq: dispatch.Seq, TaskType: payload.TaskType, At: ev.CreatedAt.UTC()}
			}
		}
		if card.Status == "已完成" || card.Status == "终止" {
			role.State = "idle"
			role.Conclusion = "卡已进入终态"
			role.WaitingFor = ""
			role.Since = nil
			role.Basis = proto.SessionRoleBasis{Source: "card_terminal"}
		}
		status.Roles = append(status.Roles, role)
	}
	sort.SliceStable(status.Roles, func(i, j int) bool { return status.Roles[i].ID < status.Roles[j].ID })
	return status
}

type residentRoundProjection struct {
	state, conclusion, reason string
	at                        time.Time
}

type residentRoundEventPayload struct {
	Identity string `json:"identity"`
	RoundID  string `json:"round_id"`
	Phase    string `json:"phase"`
	Reason   string `json:"reason"`
}

// latestResidentRound chooses the latest-started round for this resident, then
// only accepts its explicitly correlated end/fail outcome.
func latestResidentRound(events []proto.LedgerEvent, identity string, seatedAt time.Time) (residentRoundProjection, bool) {
	type facts struct {
		start proto.LedgerEvent
		end   *proto.LedgerEvent
		fail  *proto.LedgerEvent
	}
	rounds := map[string]*facts{}
	for i := range events {
		ev := events[i]
		if ev.Type != "resident_round" || ev.CreatedAt.Before(seatedAt) {
			continue
		}
		var payload residentRoundEventPayload
		if json.Unmarshal(ev.Payload, &payload) != nil || payload.Identity != identity || payload.RoundID == "" {
			continue
		}
		fact := rounds[payload.RoundID]
		if fact == nil {
			fact = &facts{}
			rounds[payload.RoundID] = fact
		}
		switch payload.Phase {
		case "start":
			if fact.start.Seq == 0 || ev.Seq > fact.start.Seq {
				fact.start = ev
			}
		case "end":
			copy := ev
			if fact.end == nil || ev.Seq > fact.end.Seq {
				fact.end = &copy
			}
		case "fail":
			copy := ev
			if fact.fail == nil || ev.Seq > fact.fail.Seq {
				fact.fail = &copy
			}
		}
	}
	var latest *facts
	for _, fact := range rounds {
		if fact.start.Seq > 0 && (latest == nil || fact.start.Seq > latest.start.Seq) {
			latest = fact
		}
	}
	if latest == nil {
		return residentRoundProjection{}, false
	}
	result := residentRoundProjection{state: "running", conclusion: "最近观测到驻场者回合开始，尚未观测到结束", at: latest.start.CreatedAt}
	if latest.end != nil && latest.end.Seq > latest.start.Seq && (latest.fail == nil || latest.end.Seq > latest.fail.Seq) {
		result.state, result.conclusion, result.at = "idle", "驻场者本轮已结束", latest.end.CreatedAt
	} else if latest.fail != nil && latest.fail.Seq > latest.start.Seq {
		var payload residentRoundEventPayload
		_ = json.Unmarshal(latest.fail.Payload, &payload)
		result.state, result.conclusion, result.reason, result.at = "exception", "驻场者本轮失败", strings.TrimSpace(payload.Reason), latest.fail.CreatedAt
	}
	return result, true
}

type roleMirrorPayload struct {
	TaskType string          `json:"task_type"`
	Payload  json.RawMessage `json:"payload"`
}

func taskMirrorPayload(raw json.RawMessage) roleMirrorPayload {
	var p roleMirrorPayload
	_ = json.Unmarshal(raw, &p)
	return p
}

func taskRoleState(typ string) (string, string) {
	switch typ {
	case "progress":
		return "unknown", "最近观测到执行任务进度，无法证明当前仍在运行"
	case "permission_request", "question":
		return "waiting", "执行任务正在等待答复"
	case "completed":
		return "idle", "执行回合已结束"
	case "turn_failed":
		return "exception", "执行回合明确失败"
	case "failed":
		return "exception", "执行任务明确失败"
	case "delivery_failed":
		return "exception", "工单应答未送达执行者"
	case "ticket_answered":
		return "unknown", "工单答复已记录"
	case "archived":
		return "idle", "执行任务已归档"
	default:
		return "unknown", "没有可识别的任务状态记录"
	}
}

func validTime(value, observedAt time.Time) *time.Time {
	if value.IsZero() || value.After(observedAt) {
		return nil
	}
	value = value.UTC()
	return &value
}

func roleActivityCount(events []proto.LedgerEvent, target, task string) int {
	count := 0
	for _, ev := range events {
		if task != "" && (ev.SourceTarget != target || ev.SourceTask != task) {
			continue
		}
		if _, ok := roleActivity(ev); ok {
			count++
		}
	}
	return count
}

func recentRoleEvents(events []proto.LedgerEvent, target, task string, truncated bool) []proto.SessionRoleActivity {
	out := make([]proto.SessionRoleActivity, 0, 5)
	for _, ev := range events {
		if task != "" && (ev.SourceTarget != target || ev.SourceTask != task) {
			continue
		}
		activity, ok := roleActivity(ev)
		if !ok {
			continue
		}
		out = append(out, activity)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time.Equal(out[j].Time) {
			return out[i].Seq > out[j].Seq
		}
		return out[i].Time.After(out[j].Time)
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func wakeRoundInfo(ev proto.LedgerEvent) (phase, reason string, ok bool) {
	var comment struct {
		Body      string `json:"body"`
		DedupeKey string `json:"dedupe_key"`
	}
	if ev.Type != "comment" || json.Unmarshal(ev.Payload, &comment) != nil || !strings.HasPrefix(comment.DedupeKey, "wake_round:") {
		return "", "", false
	}
	var payload struct {
		Phase string  `json:"phase"`
		Err   *string `json:"err"`
	}
	if json.Unmarshal([]byte(comment.Body), &payload) != nil || (payload.Phase != "start" && payload.Phase != "end" && payload.Phase != "fail") {
		return "", "", false
	}
	if payload.Err != nil {
		reason = strings.TrimSpace(*payload.Err)
	}
	return payload.Phase, reason, true
}

func currentSeatEvent(events []proto.LedgerEvent, identity string, wakeSeq int64) bool {
	var latest int64
	latestIdentity := ""
	for _, ev := range events {
		if ev.Type != "driver_seat_bound" && ev.Type != "driver_takeover" {
			continue
		}
		var payload struct {
			To string `json:"to"`
		}
		if json.Unmarshal(ev.Payload, &payload) == nil && ev.Seq > latest {
			latest = ev.Seq
			latestIdentity = payload.To
		}
	}
	return latest > 0 && latestIdentity == identity && wakeSeq >= latest
}

func roleActivity(ev proto.LedgerEvent) (proto.SessionRoleActivity, bool) {
	activity := proto.SessionRoleActivity{Seq: ev.Seq, Time: ev.CreatedAt.UTC(), Tone: "neutral", Source: ev.Type}
	switch ev.Type {
	case "dispatched":
		activity.Summary = "执行任务已派发"
	case "task_mirrored":
		p := taskMirrorPayload(ev.Payload)
		_, summary := taskRoleState(p.TaskType)
		if p.TaskType == "" {
			return activity, false
		}
		activity.Summary = summary
		switch p.TaskType {
		case "permission_request":
			activity.Summary = "出现待处理权限请求"
			activity.Tone = "needs_you"
		case "question":
			activity.Summary = "执行任务提出需要答复的问题"
			activity.Tone = "needs_you"
		case "ticket_answered":
			activity.Summary = "工单答复已记录"
		case "delivery_failed":
			activity.Summary = deliveryFailureSummary(ev.Payload)
			activity.Tone = "failed"
		case "failed", "turn_failed":
			activity.Tone = "failed"
			if reason := taskFailureReason(ev.Payload); reason != "" {
				activity.Summary += ": " + reason
			}
		}
	case "comment":
		phase, reason, ok := wakeRoundInfo(ev)
		if !ok {
			return activity, false
		}
		activity.Source = "wake_round"
		switch phase {
		case "start":
			activity.Summary = "协调者唤醒已开始"
		case "end":
			activity.Summary = "协调者唤醒回合已结束"
		case "fail":
			activity.Summary = "协调者唤醒失败"
			activity.Tone = "failed"
			if reason != "" {
				activity.Summary += ": " + reason
			}
		}
	case "room_message":
		var p proto.RoomMessage
		if json.Unmarshal(ev.Payload, &p) != nil {
			return activity, false
		}
		activity.Summary = strings.TrimSpace(p.Body)
		if len([]rune(activity.Summary)) > 100 {
			activity.Summary = string([]rune(activity.Summary)[:100]) + "…"
		}
	case "resident_round":
		var p residentRoundEventPayload
		if json.Unmarshal(ev.Payload, &p) != nil || p.Identity == "" || p.RoundID == "" {
			return activity, false
		}
		activity.Source = "resident_round"
		switch p.Phase {
		case "start":
			activity.Summary = "驻场者回合已开始"
		case "end":
			activity.Summary = "驻场者回合已结束"
		case "fail":
			activity.Summary = "驻场者回合失败"
			activity.Tone = "failed"
			if p.Reason != "" {
				activity.Summary += ": " + p.Reason
			}
		default:
			return activity, false
		}
	default:
		return activity, false
	}
	return activity, activity.Summary != ""
}

func deliveryFailureReason(raw json.RawMessage) string {
	p := taskMirrorPayload(raw)
	var detail struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(p.Payload, &detail) != nil {
		return ""
	}
	return truncateActivityText(strings.TrimSpace(detail.Reason), 160)
}

func taskFailureReason(raw json.RawMessage) string {
	p := taskMirrorPayload(raw)
	var detail struct {
		FailReason string `json:"fail_reason"`
		Error      string `json:"error"`
	}
	if json.Unmarshal(p.Payload, &detail) != nil {
		return ""
	}
	reason := strings.TrimSpace(detail.FailReason)
	if reason == "" {
		reason = strings.TrimSpace(detail.Error)
	}
	return truncateActivityText(reason, 160)
}

func deliveryFailureSummary(raw json.RawMessage) string {
	summary := "工单应答未送达执行者"
	if reason := deliveryFailureReason(raw); reason != "" {
		summary += ": " + reason
	}
	return summary
}

func truncateActivityText(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
