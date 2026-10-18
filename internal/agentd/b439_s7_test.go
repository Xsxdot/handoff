// b439_s7_test.go —— B439 S7 健康面：账本健康探针加性报「无归属在飞挂账 N 条」。
package agentd

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestB439S7LedgerHealthUnattributedInflight(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	seedAgentdLedger(t, env.ledger, "bug")
	card, err := env.ledger.CreateCard(ledger.NewCard{Title: "无归属", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if err := env.ledger.LinkTask(card.ID, "", "T-inflight", "implement", "t"); err != nil {
		t.Fatalf("挂非终态 '' 账: %v", err)
	}

	code, body := ledgerGet(t, env.testAgentdEnv, "/api/ledger/health")
	if code != 200 {
		t.Fatalf("health 应 200，实得 %d: %s", code, body)
	}
	var resp struct {
		Enabled              bool `json:"enabled"`
		UnattributedInflight int  `json:"unattributed_inflight"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解码 health: %v", err)
	}
	if !resp.Enabled {
		t.Fatal("health 应 enabled=true")
	}
	if resp.UnattributedInflight != 1 {
		t.Fatalf("unattributed_inflight = %d, want 1", resp.UnattributedInflight)
	}
}
