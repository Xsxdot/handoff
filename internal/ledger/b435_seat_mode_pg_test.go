// B435 席位模式迁移的 PG 孪生（C-325/C-326）。默认 skip；设 LEDGER_TEST_PG_DSN 后启用
// （真机清单 #K0-PG）。只动本测试新建的卡行，不改库内其它数据。
package ledger

import (
	"context"
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// C-151：席位候选新支两方言同集同序（PG 孪生；隔离 schema，门控）。
func TestB435SeatCandidatesPG(t *testing.T) {
	f := newU5FixturePG(t)
	const sess = "session:1"
	missing := f.u5Send(t, f.run, sess, proto.RoomMsgUser, nil, 0, false)
	badReply := f.u5Send(t, f.run, sess, proto.RoomMsgUser, nil, 999999999, false)
	atCard := f.u5Send(t, f.run, sess, proto.RoomMsgUser, []string{f.card1}, 0, false)
	f.u5Send(t, f.run, "session:2", proto.RoomMsgUser, nil, 0, false)
	f.u5Send(t, f.run, sess, proto.RoomMsgUser, []string{"user:other"}, 0, false)
	got, err := f.s.SeatMessageCandidatesContext(context.Background(), u5MemberSeat, sess, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{missing, badReply, atCard}; !reflect.DeepEqual(seqsOf(got), want) {
		t.Fatalf("PG 席位候选不符: got=%v want=%v", seqsOf(got), want)
	}
}

func TestB435DriverModeMigrationPG(t *testing.T) {
	s := newPGStore(t)
	if err := seedTestWorkflows(t, s); err != nil {
		t.Fatal(err)
	}
	pullCard := b435Card(t, s, "b435-pg-pull")
	pushCard := b435Card(t, s, "b435-pg-push")
	if err := s.BindSeatMode(pullCard.ID, "cli:codex#b435pg1", proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSeatMode(pushCard.ID, "cli:codex#b435pg2", proto.SeatSourceCoordinate, proto.SeatModePush, b435Bearing); err != nil {
		t.Fatal(err)
	}
	// 模拟 B435 前的存量：列存在但值为空。
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_mode = NULL WHERE id IN (?, ?)`), pullCard.ID, pushCard.ID); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		if err := s.migrateDriverMode(); err != nil {
			t.Fatalf("第 %d 次迁移失败: %v", round, err)
		}
		for id, want := range map[string]string{pullCard.ID: "pull", pushCard.ID: "push"} {
			card, err := s.GetCard(id)
			if err != nil || card.DriverMode != want {
				t.Fatalf("第 %d 次迁移后 %s 应为 %s: %v %+v", round, id, want, err, card)
			}
		}
	}
}
