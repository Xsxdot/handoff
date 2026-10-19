package agentd

// b448_timeout_log_test.go —— B448 V7：转交的时间预算（F34/F35）与两端可对账的转交日志（spec「实现决定」日志条）。

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

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
