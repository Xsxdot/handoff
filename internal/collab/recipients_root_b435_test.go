// recipients_root_b435_test.go —— 默认收件人的根卡判定与会话详情投影一致（spec §7 未知 3）。
//
// 真 SQLite Store → 真 Facade → collab：根卡集合由「SessionDetail 的卡集合 + 各卡 parent」
// 独立计算，与 Recipients 默认分支的结论逐场景对照（单根命中 / 多根 needs_mention /
// parent 在会话外的卡算根）。
package collab

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB435RootCardMatchesSessionDetail(t *testing.T) {
	svc, st, lc := newSessionFixture(t)
	mk := func(title, parent string) ledger.Card {
		t.Helper()
		c, err := st.CreateCard(ledger.NewCard{Title: title, Project: "handoff", Workflow: "bug", Parent: parent, Actor: "t"})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	root := mk("根", "")
	child := mk("子", root.ID)
	outsideParent := mk("会话外父卡", "")
	orphan := mk("父在会话外", outsideParent.ID)
	if err := st.BindSeatMode(root.ID, "cli:codex#root", proto.SeatSourceCoordinate, proto.SeatModePush,
		ledger.SeatBearing{Carrier: "car", Machine: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeatMode(orphan.ID, "cli:codex#orphan", proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	sess, err := svc.CreateSession("根卡判定", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}

	rootsFromDetail := func() []string {
		t.Helper()
		detail, err := svc.SessionDetail(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		inSession := map[string]bool{}
		for _, c := range detail.Summary.Cards {
			inSession[c.CardID] = true
		}
		var roots []string
		for _, c := range detail.Summary.Cards {
			card, err := lc.GetCard(c.CardID)
			if err != nil {
				t.Fatal(err)
			}
			if card.ParentID == "" || !inSession[card.ParentID] {
				roots = append(roots, c.CardID)
			}
		}
		return roots
	}
	defaultOf := func() proto.Delivery {
		t.Helper()
		d, err := svc.Recipients(proto.RoomMessage{Room: sess.ID, Kind: proto.RoomMsgUser, Body: "无 @"}, "user:sy")
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	for _, id := range []string{root.ID, child.ID} {
		if err := svc.JoinCard(sess.ID, id, "user:sy"); err != nil {
			t.Fatal(err)
		}
	}
	if roots := rootsFromDetail(); len(roots) != 1 || roots[0] != root.ID {
		t.Fatalf("详情投影的根卡应只有 %s，得 %v", root.ID, roots)
	}
	if d := defaultOf(); !d.Default || len(d.Addresses) != 1 || d.Addresses[0].Card != root.ID {
		t.Fatalf("单根有席位应默认命中根卡 %s，得 %+v", root.ID, d)
	}

	if err := svc.JoinCard(sess.ID, orphan.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if roots := rootsFromDetail(); len(roots) != 2 {
		t.Fatalf("父卡在会话外的卡应算根，详情根卡应为两张，得 %v", roots)
	}
	if d := defaultOf(); len(d.Addresses) != 0 || d.NoRecipient != proto.NoRecipientNeedsMention {
		t.Fatalf("两张根卡应 needs_mention，得 %+v", d)
	}

	if err := svc.LeaveCard(sess.ID, root.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	roots := rootsFromDetail()
	if len(roots) != 2 {
		// 根卡离群后子卡的 parent 不在会话 → 子卡升为根；与 orphan 共两张。
		t.Fatalf("根卡离群后详情根卡应为子卡与 orphan，得 %v", roots)
	}
	if d := defaultOf(); d.NoRecipient != proto.NoRecipientNeedsMention {
		t.Fatalf("两张根卡应 needs_mention，得 %+v", d)
	}
	if err := svc.LeaveCard(sess.ID, child.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if d := defaultOf(); !d.Default || len(d.Addresses) != 1 || d.Addresses[0] != (proto.DeliveryAddress{
		Kind: proto.DeliverySeat, Session: sess.ID, Card: orphan.ID, Identity: "cli:codex#orphan"}) {
		t.Fatalf("只剩父在会话外的 orphan 时应默认命中它，得 %+v", d)
	}
}
