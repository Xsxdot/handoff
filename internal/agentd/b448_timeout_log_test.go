package agentd

// b448_timeout_log_test.go —— B448 V7：转交的时间预算（F34/F35）与两端可对账的转交日志（spec「实现决定」日志条）。

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// F21–F29 受理端还原表：收件端 kind → 受理端类型化错误（入口 handler 的既有 switch 据此命中，零改）。
// 覆盖竖切里难以构造的 kind（resident_occupied / read_only / no_room 需跨机竞态才出现）。
func TestB448FirstRoundFailureRestoresTypedErrors(t *testing.T) {
	fe := func(status int, kind string) error {
		return &client.FirstRoundError{Status: status, Kind: kind, Message: "原因", SessionID: "ses-x"}
	}
	var admission *coordinatorAdmissionError
	var seat *coordinatorSeatConflict
	for _, tc := range []struct {
		name  string
		in    error
		check func(error) bool
	}{
		{"404 版本过旧", client.ErrFirstRoundUnsupported, func(e error) bool {
			return strings.Contains(e.Error(), "m1 的 agentd 版本过旧，不支持首轮转交，请升级")
		}},
		{"no_slot", fe(409, proto.FirstRoundKindNoSlot), func(e error) bool {
			return errors.Is(e, scheduling.ErrNoSlot) && errors.As(e, &admission) && admission.squad == "q"
		}},
		{"admission_rejected", fe(400, proto.FirstRoundKindAdmissionRejected), func(e error) bool {
			return errors.As(e, &admission) && !errors.Is(e, scheduling.ErrNoSlot)
		}},
		{"cas_conflict", fe(409, proto.FirstRoundKindCASConflict), func(e error) bool { return errors.Is(e, ledger.ErrCASConflict) }},
		{"seat_conflict", fe(409, proto.FirstRoundKindSeatConflict), func(e error) bool {
			return errors.As(e, &seat) && seat.result.SessionID == "ses-x"
		}},
		{"resident_occupied", fe(409, proto.FirstRoundKindResidentOccupied), func(e error) bool { return errors.Is(e, collab.ErrResidentOccupied) }},
		{"read_only", fe(409, proto.FirstRoundKindReadOnly), func(e error) bool { return errors.Is(e, collab.ErrReadOnly) }},
		{"no_room", fe(409, proto.FirstRoundKindNoRoom), func(e error) bool { return errors.Is(e, collab.ErrNoRoom) }},
		{"invalid 不伪装成用户错误", fe(400, proto.FirstRoundKindInvalid), func(e error) bool {
			return !errors.As(e, &admission) && strings.Contains(e.Error(), "受理机缺陷")
		}},
		{"未知 kind 原文透出", fe(503, ""), func(e error) bool { return strings.Contains(e.Error(), "HTTP 503：原因") }},
		{"不可达", fmt.Errorf("%w: dial", client.ErrUnreachable), func(e error) bool {
			return strings.Contains(e.Error(), "协调者载体 c 在 m1，转交到该机失败：")
		}},
	} {
		got := firstRoundFailure("q", "c", "m1", tc.in)
		if !tc.check(got) {
			t.Fatalf("%s：还原不符，得 %v", tc.name, got)
		}
	}
}

// F34：受理端出站 ctx 的截止 ≥ 回合上界（不比本机路径更早放弃）。
func TestB448TransferCtxDeadlineCoversTurnTimeout(t *testing.T) {
	before := time.Now()
	ctx, cancel := firstRoundTransferCtx(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(before.Add(coordWakeTurnTimeout)) {
		t.Fatalf("转交出站截止应 ≥ now+coordWakeTurnTimeout(%s)，得 ok=%v deadline=%s", coordWakeTurnTimeout, ok,
			deadline.Sub(before))
	}
}

// F35：执行机 server 级 WriteTimeout 比回合短时，回执仍完整送达（收件端放宽了写截止）。
func TestB448ReceiptOutlivesServerWriteTimeout(t *testing.T) {
	old := coordWakeTurnTimeout
	coordWakeTurnTimeout = 3 * time.Second
	t.Cleanup(func() { coordWakeTurnTimeout = old })
	p := newB448Pair(t, b448PairOpts{bWriteTimeout: 200 * time.Millisecond})
	p.b.runner.delay = 600 * time.Millisecond
	card := b448Card(t, p)
	code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
	if code != http.StatusOK {
		t.Fatalf("回合长于执行机 WriteTimeout 时回执仍应送达（200），得 %d %s", code, body)
	}
	p.assertZeroCounts(t)
}

func b448AssertTransferLog(t *testing.T, h *b448Host, msgPart string, want map[string]string) {
	t.Helper()
	records := h.logs.find(msgPart)
	for _, r := range records {
		match := r.level >= wakeRoundLogLevel
		for k, v := range want {
			if r.attrs[k] != v {
				match = false
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("%s 应有一条缺省级别可见（≥%s）的「%s」日志带 %v，得 %+v", h.name, slog.Level(wakeRoundLogLevel), msgPart, want, records)
}

// V7 日志：受理机与执行机各有一行带 accepted_by / handled_by / carrier / card 或 session 的转交日志，可按键对账。
func TestB448TransferLogsOnBothEnds(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	card := b448Card(t, p)
	if code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`); code != http.StatusOK {
		t.Fatalf("叫机器人应 200，得 %d %s", code, body)
	}
	cardWant := map[string]string{"card": card, "accepted_by": b448MachineA, "handled_by": b448MachineB, "carrier": b448Carrier}
	b448AssertTransferLog(t, p.a, "协调者首轮转交完成：受理机", cardWant)
	b448AssertTransferLog(t, p.b, "协调者首轮转交完成：执行机", cardWant)

	resp := b448CreateSession(t, p, b448Card(t, p))
	if resp.Resident == nil || resp.Resident.Error != "" {
		t.Fatalf("建会话驻场者应成功: %+v", resp.Resident)
	}
	sessWant := map[string]string{"session": resp.ID, "accepted_by": b448MachineA, "handled_by": b448MachineB, "carrier": b448Carrier}
	b448AssertTransferLog(t, p.a, "驻场者首轮转交完成：受理机", sessWant)
	b448AssertTransferLog(t, p.b, "驻场者首轮转交完成：执行机", sessWant)
}
