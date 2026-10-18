// B435 R：会话服务的驻场者写面（Q11 甲唯一入口：记法校验 C-413、Q14 拒系统身份、会话房间前置）、
// 会话详情 resident 成员行（C-208、契约 §13 第 5 条、S9 两行）、驻场者入书写者集（Q4 甲）。
package collab

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB435SitResidentValidatesIdentity(t *testing.T) {
	bad := []string{"cli:x#y", "cli:u@h", "web:h", "user:", "agent:", " agent:a", "User:a", "agent:a b", "",
		proto.SystemNotifierIdentity} // Q14 甲：系统发信身份不得驻场
	for _, identity := range bad {
		f := &fakeLC{}
		svc := New(f)
		if _, err := svc.SitResident(proto.SessionResident{Session: "session:1", Identity: identity}); !errors.Is(err, ErrResidentIdentity) {
			t.Errorf("身份 %q 应被拒（ErrResidentIdentity），得 %v", identity, err)
		}
		if len(f.sitCalls) != 0 {
			t.Errorf("身份 %q 被拒仍写了账本: %+v", identity, f.sitCalls)
		}
	}
	for _, identity := range []string{"user:a", "agent:b"} {
		f := &fakeLC{}
		svc := New(f)
		if _, err := svc.SitResident(proto.SessionResident{Session: "session:1", Identity: identity}); err != nil {
			t.Errorf("身份 %q 应通过: %v", identity, err)
		}
		if len(f.sitCalls) != 1 || f.sitCalls[0].Identity != identity || f.sitCalls[0].Session != "session:1" {
			t.Errorf("身份 %q 应恰一次写调用: %+v", identity, f.sitCalls)
		}
	}
}

func TestB435SitResidentNonSessionRoomRejected(t *testing.T) {
	for _, roomID := range []string{"project:x", "global", "B12", ""} {
		f := &fakeLC{}
		svc := New(f)
		if _, err := svc.SitResident(proto.SessionResident{Session: roomID, Identity: "user:a"}); !errors.Is(err, ErrNoRoom) {
			t.Errorf("房间 %q 不是会话房间应拒（ErrNoRoom），得 %v", roomID, err)
		}
		if err := svc.StandResident(roomID, "user:a"); !errors.Is(err, ErrNoRoom) {
			t.Errorf("stand 房间 %q 应拒（ErrNoRoom），得 %v", roomID, err)
		}
		if len(f.sitCalls) != 0 || len(f.standCalls) != 0 {
			t.Errorf("房间 %q 被拒仍写了账本: sit=%v stand=%v", roomID, f.sitCalls, f.standCalls)
		}
	}
}

func TestB435SitStandResidentErrorMapping(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	session, err := svc.CreateSession("映射", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: "session:999", Identity: "user:r"}); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("会话不存在应 ErrNoRoom，得 %v", err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: session.ID, Identity: "user:r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: session.ID, Identity: "user:x"}); !errors.Is(err, ErrResidentOccupied) {
		t.Fatalf("第二次 sit 应 ErrResidentOccupied，得 %v", err)
	}
	if err := svc.StandResident(session.ID, "user:x"); !errors.Is(err, ErrResidentNotSeated) {
		t.Fatalf("身份不符 stand 应 ErrResidentNotSeated，得 %v", err)
	}
	if err := svc.StandResident(session.ID, "user:r"); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveSession(session.ID, "user:o"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: session.ID, Identity: "user:r"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("已归档会话 sit 应 ErrReadOnly，得 %v", err)
	}
}

// b435DetailMembers 把会话详情经真 json.Marshal 后取 summary.members 原始对象（区分缺键与空值）。
func b435DetailMembers(t *testing.T, d proto.SessionDetail) []map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Summary struct {
			Members []map[string]json.RawMessage `json:"members"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Summary.Members
}

func b435JSONString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestB435SessionDetailResidentRow(t *testing.T) {
	svc, st, lc := newSessionFixture(t)
	session, err := svc.CreateSession("驻场详情", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	// 无驻场者 → 无 resident 行。
	d, err := svc.SessionDetail(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range b435DetailMembers(t, d) {
		if b435JSONString(t, m["kind"]) == proto.SessionMemberResident {
			t.Fatalf("无驻场者不应有 resident 行: %v", m)
		}
	}
	// S9：同会话一张 pull 席位卡 + 驻场者 → resident 与 seat 两行（身份串不同，N2）。
	card := sessionCard(t, st, "升格卡")
	if err := st.BindSeatMode(card.ID, "cli:claude#r9", proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:o"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: session.ID, Identity: "agent:resi"}); err != nil {
		t.Fatal(err)
	}
	d, err = svc.SessionDetail(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	members := b435DetailMembers(t, d)
	var kinds []string
	residentAt, seatAt := -1, -1
	for i, m := range members {
		kind := b435JSONString(t, m["kind"])
		kinds = append(kinds, kind)
		switch kind {
		case proto.SessionMemberResident:
			if residentAt >= 0 {
				t.Fatalf("resident 行应恰一行: %v", kinds)
			}
			residentAt = i
			if got := b435JSONString(t, m["identity"]); got != "agent:resi" {
				t.Fatalf("resident 行 identity=%q", got)
			}
			if _, present := m["card_id"]; present {
				t.Fatalf("resident 行 card_id 应缺键: %v", m)
			}
			if got := b435JSONString(t, m["status"]); got != proto.SessionMemberLastActive {
				t.Fatalf("resident 行无租约应报 last_active，得 %q", got)
			}
		case proto.SessionMemberSeat:
			seatAt = i
			if got := b435JSONString(t, m["identity"]); got != "cli:claude#r9" {
				t.Fatalf("seat 行 identity=%q", got)
			}
		}
	}
	// 位置（执行者定，测试锁住）：显式成员之后、卡席位之前。
	if residentAt != len(session.Members) || seatAt != residentAt+1 {
		t.Fatalf("成员行顺序应为 显式成员 → resident → seat，得 %v（resident@%d seat@%d）", kinds, residentAt, seatAt)
	}
	// 读驻场者失败 → 详情返回错误（不静默少一行）。
	readErr := errors.New("驻场者表读失败")
	broken := New(failingResidentLC{LedgerClient: lc, err: readErr})
	if _, err := broken.SessionDetail(session.ID); !errors.Is(err, readErr) {
		t.Fatalf("读驻场者失败应上抛，得 %v", err)
	}
}

// failingResidentLC 在 SessionResident 读面注入错误（其余方法透传真 Facade）。
type failingResidentLC struct {
	client.LedgerClient
	err error
}

func (f failingResidentLC) SessionResident(string) (proto.SessionResident, bool, error) {
	return proto.SessionResident{}, false, f.err
}

// Q4 甲：驻场者进书写者集（契约 §5 B358 条 14 修订）；stand 即剥写权；读失败上抛不伪装 403。
func TestB435ResidentCanWriteSession(t *testing.T) {
	svc, _, lc := newSessionFixture(t)
	session, err := svc.CreateSession("驻场发言", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	msg := proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "收到"}
	if _, err := svc.Send(session.ID, msg, "agent:r"); !errors.Is(err, ErrNotWriter) {
		t.Fatalf("未驻场的非成员发言应 ErrNotWriter，得 %v", err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: session.ID, Identity: "agent:r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(session.ID, msg, "agent:r"); err != nil {
		t.Fatalf("驻场者应可发言: %v", err)
	}
	readErr := errors.New("驻场者表读失败")
	broken := New(failingResidentLC{LedgerClient: lc, err: readErr})
	if _, err := broken.Send(session.ID, msg, "agent:r"); !errors.Is(err, readErr) || errors.Is(err, ErrNotWriter) {
		t.Fatalf("读驻场者失败应原样上抛、不是 ErrNotWriter，得 %v", err)
	}
	if err := svc.StandResident(session.ID, "agent:r"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(session.ID, msg, "agent:r"); !errors.Is(err, ErrNotWriter) {
		t.Fatalf("stand 后应剥写权（ErrNotWriter），得 %v", err)
	}
}
