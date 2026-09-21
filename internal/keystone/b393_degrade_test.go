// b393_degrade_test.go —— B393 R3.3/spec §4.4：resume 与重建均失败时，落
// needs_human 的理由必须「可行动」（含两条失败原因摘要），不是光秃秃一句常量。
//
// 职责：经真实 ledger Facade 走 keystone.Service.Wake 的兜底降级链终点，断言理由文本。
// 缝：keystone.Service.Wake；夹具复用 slice_test.go 的 fakeRunner/fakeNarrator/recordingLedger。
// 边界：不复制 keystone 重建规则；只观察落账的理由。
package keystone_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestB393DegradeRecordsActionableReason 锁 spec §4.4：resume 与重建均失败时，
// 落 needs_human 的理由必须含两条失败原因摘要。
//
// 红：今天 reason 是常量 "协调者唤醒失败：resume 与重建均不可用"（无 cause 摘要）。
// 绿：T3.3 后理由含 resume 与重建的错误文本片段。
// 变异：把 reason 改回常量 → 复红。
func TestB393DegradeRecordsActionableReason(t *testing.T) {
	st, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	defer st.Close()
	facade := ledgerapi.New(st)
	if _, err := st.PutWorkflow("slice", ledger.WorkflowDef{States: []string{"进行中", "已完成"}}); err != nil {
		t.Fatalf("建工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "降级理由卡", Project: "handoff", Workflow: "slice", BaseBranch: "main", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if err := st.BindSeat(card.ID, "cli:opencode#sess-old", proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写席位: %v", err)
	}

	runner := &fakeRunner{failNext: 1, failLaunches: true}
	narr := &fakeNarrator{}
	view := &recordingLedger{f: facade}
	svc := keystone.New(runner, narr, view, nil)

	_, _ = svc.Wake(context.Background(), card.ID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: card.ID, Summary: "hi"},
	}, keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Workdir: "/w"})

	if len(view.needArgs) != 1 {
		t.Fatalf("双失败应恰落一条 needs_human，得到 %d", len(view.needArgs))
	}
	reason := view.needArgs[0][0]
	// 判据不是「含 resume/重建 字样」（常量理由也含），而是含两条真实失败原因摘要：
	// fakeRunner 的 Resume 错误 "resume 不可用" 与 Launch 错误 "拉起不可用"。
	if !strings.Contains(reason, "resume 不可用") {
		t.Fatalf("理由缺 resume 失败原因摘要：%q", reason)
	}
	if !strings.Contains(reason, "拉起不可用") {
		t.Fatalf("理由缺重建失败原因摘要：%q", reason)
	}
}
