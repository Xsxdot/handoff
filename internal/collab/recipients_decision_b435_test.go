// recipients_decision_b435_test.go —— B435 匹配函数判定表的行为锁（契约 §3.2 / §4.4
// C-202–C-205、C-214–C-228）。
//
// 判定表用 fakeLC（可配会话/卡/驻场者/错误）逐行表驱动；根卡判定另有真账本闭环
// （TestB435RootCardMatchesSessionDetail）。驻场者行由夹具直写（P2 甲：真实写面归 R）。
package collab

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

func b435Seat(session, card, identity string) proto.DeliveryAddress {
	return proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: session, Card: card, Identity: identity}
}

func b435Resident(session, identity string) proto.DeliveryAddress {
	return proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: session, Identity: identity}
}

func b435RoomEvent(seq int64, roomID, actor string) proto.LedgerEvent {
	payload := fmt.Sprintf(`{"room":%q,"kind":"user","body":"原消息"}`, roomID)
	return proto.LedgerEvent{Seq: seq, Type: room.RoomEventType, Actor: actor, Payload: []byte(payload)}
}

// b435World 是判定表的固定世界：
//   - session:1 = 根 R（push 席位）+ 子 C（pull 席位）+ 子 E（空座）；无驻场者
//   - session:2 = 唯一根 R2 空座 → root_no_seat
//   - session:3 = 两张有席位的根 → needs_mention；session:4 零卡 → needs_mention
//   - session:5 = O5（parent 不在本会话 → 根）+ 其子 C5
//   - session:6 = 根 R6 有席位 + 驻场者 user:boss
//   - session:7 = 唯一根 R7 为 pull 席位
//   - session:8 = 同一身份坐 P8（根）与 Q8（子）
//   - X 不在任何会话；XB 不在会话但与 C 同身份（C-205 字面的已知边界）
func b435World() *fakeLC {
	card := func(id, parent, seat, mode string) proto.Card {
		return proto.Card{ID: id, ParentID: parent, DriverSession: seat, DriverMode: mode, Project: "p", Status: "进行中"}
	}
	return &fakeLC{
		cards: []proto.Card{
			card("R", "", "cli:codex#r", "push"),
			card("C", "R", "cli:codex#c", "pull"),
			card("E", "R", "", ""),
			card("R2", "", "", ""),
			card("A3", "", "cli:codex#a3", "push"),
			card("B3", "", "cli:codex#b3", "push"),
			card("O5", "Z", "cli:codex#o5", "push"),
			card("C5", "O5", "cli:codex#c5", "pull"),
			card("R6", "", "cli:codex#r6", "push"),
			card("R7", "", "cli:codex#r7", "pull"),
			card("P8", "", "cli:codex#dup", "push"),
			card("Q8", "P8", "cli:codex#dup", "push"),
			card("X", "", "cli:codex#x", "push"),
			card("XB", "", "cli:codex#c", "pull"),
		},
		sessions: map[string]proto.Session{
			"session:1": {ID: "session:1", Cards: []string{"R", "C", "E"}},
			"session:2": {ID: "session:2", Cards: []string{"R2"}},
			"session:3": {ID: "session:3", Cards: []string{"A3", "B3"}},
			"session:4": {ID: "session:4"},
			"session:5": {ID: "session:5", Cards: []string{"O5", "C5"}},
			"session:6": {ID: "session:6", Cards: []string{"R6"}},
			"session:7": {ID: "session:7", Cards: []string{"R7"}},
			"session:8": {ID: "session:8", Cards: []string{"P8", "Q8"}},
		},
		residents: map[string]proto.SessionResident{
			"session:6": {Session: "session:6", Identity: "user:boss"},
		},
		events: []proto.LedgerEvent{
			b435RoomEvent(100, "session:1", "cli:codex#c"),
			b435RoomEvent(101, "session:6", "user:boss"),
			b435RoomEvent(102, "session:1", "user:a"),
		},
	}
}

type b435Case struct {
	name   string
	msg    proto.RoomMessage
	author string
	want   proto.Delivery
}

func b435Msg(roomID string, mentions ...string) proto.RoomMessage {
	return proto.RoomMessage{Room: roomID, Kind: proto.RoomMsgUser, Body: "hi", Mentions: mentions}
}

func b435Cases() []b435Case {
	none := func(r proto.NoRecipientReason) proto.Delivery { return proto.Delivery{NoRecipient: r} }
	explicit := func(a ...proto.DeliveryAddress) proto.Delivery { return proto.Delivery{Addresses: a} }
	def := func(a proto.DeliveryAddress) proto.Delivery {
		return proto.Delivery{Addresses: []proto.DeliveryAddress{a}, Default: true}
	}
	reply := func(roomID string, to int64) proto.RoomMessage {
		m := b435Msg(roomID)
		m.ReplyTo = to
		return m
	}
	bySystem := b435Msg("session:1", "C")
	bySystem.BySystem = true
	pointer := b435Msg("session:1", "C")
	pointer.Kind = proto.RoomMsgPointer
	seatR := b435Seat("session:1", "R", "cli:codex#r")
	seatC := b435Seat("session:1", "C", "cli:codex#c")
	return []b435Case{
		// 第 1 行：结构行（C-202）
		{"by_system 结构行", bySystem, "user:a", none(proto.NoRecipientStructural)},
		{"pointer 结构行", pointer, "user:a", none(proto.NoRecipientStructural)},
		// 第 2 行：非会话房间（C-203）
		{"project 房间", b435Msg("project:p", "C"), "user:a", none(proto.NoRecipientNotSessionRoom)},
		{"旧卡房间", b435Msg("R"), "user:a", none(proto.NoRecipientNotSessionRoom)},
		// 第 3 行：显式分支（C-204/205/214–217、C-227）
		{"@本会话卡 → 该卡席位", b435Msg("session:1", "C"), "user:a", explicit(seatC)},
		{"@push 根卡与 pull 子卡都是地址", b435Msg("session:1", "R", "C"), "user:a", explicit(seatR, seatC)},
		{"@驻场者身份 → 驻场者地址", b435Msg("session:6", "user:boss"), "user:a", explicit(b435Resident("session:6", "user:boss"))},
		{"@空座卡丢弃且不回落根卡", b435Msg("session:1", "E"), "user:a", none(proto.NoRecipientAddressedUnresolved)},
		{"@非本会话卡丢弃", b435Msg("session:1", "X"), "user:a", none(proto.NoRecipientAddressedUnresolved)},
		{"@未驻场 user 丢弃", b435Msg("session:1", "user:nobody"), "user:a", none(proto.NoRecipientAddressedUnresolved)},
		{"agent 作者显式 @ 仍按显式分支", b435Msg("session:1", "C"), "agent:x", explicit(seatC)},
		{"多 @ 去重保序", b435Msg("session:1", "C", "C", "R", "cli:codex#c"), "user:a", explicit(seatC, seatR)},
		{"已知边界：@会话外卡同身份命中本会话卡", b435Msg("session:1", "XB"), "user:a", explicit(seatC)},
		{"同一身份坐两卡：@子卡只到子卡", b435Msg("session:8", "Q8"), "user:a", explicit(b435Seat("session:8", "Q8", "cli:codex#dup"))},
		{"同一身份坐两卡：@根卡只到根卡", b435Msg("session:8", "P8"), "user:a", explicit(b435Seat("session:8", "P8", "cli:codex#dup"))},
		{"reply_to 席位作者 → 该卡席位", reply("session:1", 100), "user:a", explicit(seatC)},
		{"reply_to 驻场者 → 驻场者地址", reply("session:6", 101), "user:a", explicit(b435Resident("session:6", "user:boss"))},
		{"reply_to 人类原作者（未驻场）丢弃", reply("session:1", 102), "user:b", none(proto.NoRecipientAddressedUnresolved)},
		{"reply_to 无效按无寻址", reply("session:1", 999), "user:a", def(seatR)},
		// 第 4 行：无寻址非人类（C-218）
		{"agent:x 无 @", b435Msg("session:1"), "agent:x", none(proto.NoRecipientNonHumanUnaddressed)},
		{"agent:handoff 无 @", b435Msg("session:6"), "agent:handoff", none(proto.NoRecipientNonHumanUnaddressed)},
		{"席位作者无 @", b435Msg("session:1"), "cli:codex#t1", none(proto.NoRecipientNonHumanUnaddressed)},
		{"web 作者无 @", b435Msg("session:1"), "web:h", none(proto.NoRecipientNonHumanUnaddressed)},
		// 第 5 行：有驻场者（C-219），先于根卡；作者本人即驻场者仍命中（C-224）
		{"user 无 @ → 驻场者", b435Msg("session:6"), "user:a", def(b435Resident("session:6", "user:boss"))},
		{"CLI 人形无 @ → 驻场者", b435Msg("session:6"), "cli:alice@mbp", def(b435Resident("session:6", "user:boss"))},
		{"驻场者本人无 @ 仍命中自己", b435Msg("session:6"), "user:boss", def(b435Resident("session:6", "user:boss"))},
		// 第 6 行：单根卡有席位（C-220、C-223、C-227）
		{"user 无 @ → 根卡席位", b435Msg("session:1"), "user:a", def(seatR)},
		{"CLI 人形无 @ → 根卡席位", b435Msg("session:1"), "cli:alice@mbp", def(seatR)},
		{"parent 不在本会话的卡算根", b435Msg("session:5"), "user:a", def(b435Seat("session:5", "O5", "cli:codex#o5"))},
		{"pull 根卡同为默认地址", b435Msg("session:7"), "user:a", def(b435Seat("session:7", "R7", "cli:codex#r7"))},
		// 第 7 行（C-221）、第 8 行（C-222）
		{"单根卡空座", b435Msg("session:2"), "user:a", none(proto.NoRecipientRootNoSeat)},
		{"两张根卡", b435Msg("session:3"), "user:a", none(proto.NoRecipientNeedsMention)},
		{"零张根卡", b435Msg("session:4"), "user:a", none(proto.NoRecipientNeedsMention)},
		{"同身份两卡的会话仍是单根", b435Msg("session:8"), "user:a", def(b435Seat("session:8", "P8", "cli:codex#dup"))},
	}
}

func TestB435RecipientsDecisionTable(t *testing.T) {
	svc := New(b435World())
	for _, tc := range b435Cases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.Recipients(tc.msg, tc.author)
			if err != nil {
				t.Fatalf("Recipients 报错: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("判定不符:\n got=%+v\nwant=%+v", got, tc.want)
			}
		})
	}
}

// C-225：Default=true ⇒ 恰一个地址；地址集为空 ⇒ 必有原因。
func TestB435DefaultImpliesSingleAddress(t *testing.T) {
	svc := New(b435World())
	defaults := 0
	for _, tc := range b435Cases() {
		got, err := svc.Recipients(tc.msg, tc.author)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.Default {
			defaults++
			if len(got.Addresses) != 1 {
				t.Fatalf("%s: Default=true 必须恰一个地址，得 %d", tc.name, len(got.Addresses))
			}
		}
		if len(got.Addresses) == 0 && got.NoRecipient == "" {
			t.Fatalf("%s: 空地址集必须给原因", tc.name)
		}
		if len(got.Addresses) > 0 && got.NoRecipient != "" {
			t.Fatalf("%s: 有地址时不得带原因 %q", tc.name, got.NoRecipient)
		}
	}
	if defaults == 0 {
		t.Fatal("用例集里没有默认分支，C-225 断言空转")
	}
}

// C-226：Matches(msg,a,addr) ⇔ addr 逐字段等于 Recipients(msg,a).Addresses 某项；
// 候选含全世界的地址与每个命中地址「只差一个字段」的近似地址。
func TestB435MatchesAgreesWithRecipients(t *testing.T) {
	svc := New(b435World())
	var universe []proto.DeliveryAddress
	for _, tc := range b435Cases() {
		got, err := svc.Recipients(tc.msg, tc.author)
		if err != nil {
			t.Fatal(err)
		}
		universe = append(universe, got.Addresses...)
	}
	near := func(a proto.DeliveryAddress) []proto.DeliveryAddress {
		kind := proto.DeliveryResident
		if a.Kind == proto.DeliveryResident {
			kind = proto.DeliverySeat
		}
		k, s, c, i := a, a, a, a
		k.Kind, s.Session, c.Card, i.Identity = kind, a.Session+"x", a.Card+"x", a.Identity+"x"
		return []proto.DeliveryAddress{a, k, s, c, i}
	}
	checked := 0
	for _, tc := range b435Cases() {
		want, err := svc.Recipients(tc.msg, tc.author)
		if err != nil {
			t.Fatal(err)
		}
		for _, base := range universe {
			for _, cand := range near(base) {
				ok, err := svc.Matches(tc.msg, tc.author, cand)
				if err != nil {
					t.Fatalf("%s: Matches 报错: %v", tc.name, err)
				}
				if in := slices.Contains(want.Addresses, cand); ok != in {
					t.Fatalf("%s: Matches(%+v)=%v 与 Recipients 集合成员关系 %v 不一致", tc.name, cand, ok, in)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("没有比对任何候选地址")
	}
}

// C-228：任何账本读失败都返回非 nil 错误，Matches 不得返回「未命中成功」。
func TestB435RecipientsReadErrorIsNotMiss(t *testing.T) {
	readErr := errors.New("账本瞬时读失败")
	replyMsg := b435Msg("session:1")
	replyMsg.ReplyTo = 100
	for _, tc := range []struct {
		name   string
		inject func(*fakeLC)
		msg    proto.RoomMessage
	}{
		{"GetSession 默认分支", func(f *fakeLC) { f.errGetSession = readErr }, b435Msg("session:1")},
		{"GetSession 显式分支", func(f *fakeLC) { f.errGetSession = readErr }, b435Msg("session:1", "C")},
		{"GetCard 显式分支", func(f *fakeLC) { f.errGetCard = readErr }, b435Msg("session:1", "C")},
		{"GetCard 默认分支", func(f *fakeLC) { f.errGetCard = readErr }, b435Msg("session:1")},
		{"SessionResident 默认分支", func(f *fakeLC) { f.errResident = readErr }, b435Msg("session:6")},
		{"SessionResident 显式分支", func(f *fakeLC) { f.errResident = readErr }, b435Msg("session:6", "user:boss")},
		{"reply 原作者读取", func(f *fakeLC) { f.errEvents = readErr }, replyMsg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := b435World()
			tc.inject(lc)
			svc := New(lc)
			d, err := svc.Recipients(tc.msg, "user:a")
			if !errors.Is(err, readErr) {
				t.Fatalf("Recipients 必须上抛读错误，得 %+v, %v", d, err)
			}
			ok, err := svc.Matches(tc.msg, "user:a", b435Seat("session:1", "R", "cli:codex#r"))
			if ok || !errors.Is(err, readErr) {
				t.Fatalf("Matches 必须上抛读错误且不命中，得 %v, %v", ok, err)
			}
		})
	}
}
