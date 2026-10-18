// B435 O1：项目房间在会话服务一侧的前提与写面——系统发信身份作群主即书写者（段 0a，Q1 甲前提）、
// EnsureProjectRoom 唯一入口的项目名校验（Q15 甲）、SessionSummary.project 装配（Q9 甲，C-510/511）、
// 项目房间是普通会话（拆解 §5.3「枚举新值过白名单」）。
package collab

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// 段 0a：群主 = agent:handoff 的会话里，系统身份以 @驻场者 的普通消息落账成功，读回载荷无 by_system 键
// （C-243：by_system=false 以缺键表达，绝非 null）。真 Store + Facade + collab。
func TestB435SystemNotifierOwnerCanSendToResident(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	session, err := svc.CreateSession("项目房间前提", proto.SystemNotifierIdentity, proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: session.ID, Identity: "agent:org"}); err != nil {
		t.Fatalf("sit: %v", err)
	}
	seq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "卡 B1 新建", Mentions: []string{"agent:org"},
	}, proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatalf("系统身份作群主应可发言: %v", err)
	}
	ev, ok, err := st.EventBySeq(seq)
	if err != nil || !ok {
		t.Fatalf("读回 seq=%d: ok=%v err=%v", seq, ok, err)
	}
	if ev.Actor != proto.SystemNotifierIdentity {
		t.Fatalf("actor 应为 %s，得 %s", proto.SystemNotifierIdentity, ev.Actor)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if raw, has := payload["by_system"]; has {
		t.Fatalf("载荷不得出 by_system 键，得 %s", raw)
	}
	if string(payload["mentions"]) != `["agent:org"]` || string(payload["kind"]) != `"user"` {
		t.Fatalf("载荷 mentions/kind 不符: %s", ev.Payload)
	}
}

// Q15 甲：项目名只校验形状（裁剪后非空、不含空白与控制字符）；被拒零写调用；通过时恰一次写调用且群主 =
// 系统发信身份（Q1 甲：项目房间以 agent:handoff 为群主创建）。
func TestB435EnsureProjectRoomValidatesName(t *testing.T) {
	for _, name := range []string{"", "   ", "a b", "a\nb", " handoff", "a\tb", "a\x00b"} {
		f := &fakeLC{}
		if _, _, err := New(f).EnsureProjectRoom(name, "cli:u@h"); !errors.Is(err, ErrProjectName) {
			t.Errorf("项目名 %q 应被拒（ErrProjectName），得 %v", name, err)
		}
		if len(f.ensureCalls) != 0 {
			t.Errorf("项目名 %q 被拒仍写了账本: %+v", name, f.ensureCalls)
		}
	}
	for _, name := range []string{"handoff", "b435-x", "项目甲"} {
		f := &fakeLC{}
		if _, _, err := New(f).EnsureProjectRoom(name, "cli:u@h"); err != nil {
			t.Errorf("项目名 %q 应通过: %v", name, err)
		}
		if len(f.ensureCalls) != 1 || f.ensureCalls[0] != [3]string{name, proto.SystemNotifierIdentity, "cli:u@h"} {
			t.Errorf("项目名 %q 应恰一次写调用且群主为系统身份: %+v", name, f.ensureCalls)
		}
	}
}

// b435SummaryKeys 把会话摘要编码成原始键值表（序列化边界：区分缺键与空串）。
func b435SummaryKeys(t *testing.T, s proto.SessionSummary) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// failingProjectLC 让会话 → 项目读失败（其余透传真 Facade）。
type failingProjectLC struct {
	client.LedgerClient
	err error
}

func (f failingProjectLC) SessionProject(string) (string, error) { return "", f.err }

// C-510/C-511/C-512：列表与详情头同一投影——项目房间两处都出 project 且值为项目名；普通会话两处都缺键；
// 读项目绑定失败 → 列表与详情都返回错误（不静默缺键）。
func TestB435SessionSummaryProjectKey(t *testing.T) {
	svc, _, lc := newSessionFixture(t)
	pr, created, err := svc.EnsureProjectRoom("handoff", "cli:u@h")
	if err != nil || !created {
		t.Fatalf("ensure: created=%v err=%v", created, err)
	}
	plain, err := svc.CreateSession("普通", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListSessions("")
	if err != nil {
		t.Fatal(err)
	}
	inList := map[string]map[string]json.RawMessage{}
	for _, s := range list {
		inList[s.ID] = b435SummaryKeys(t, s)
	}
	rd, err := svc.SessionDetail(pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	pd, err := svc.SessionDetail(plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	for src, keys := range map[string][2]map[string]json.RawMessage{
		"list":   {inList[pr.ID], inList[plain.ID]},
		"detail": {b435SummaryKeys(t, rd.Summary), b435SummaryKeys(t, pd.Summary)},
	} {
		if string(keys[0]["project"]) != `"handoff"` {
			t.Errorf("%s：项目房间应出 project=\"handoff\"，得 %s", src, keys[0]["project"])
		}
		if raw, has := keys[1]["project"]; has {
			t.Errorf("%s：普通会话不得出 project 键，得 %s", src, raw)
		}
	}
	readErr := errors.New("项目绑定读失败")
	broken := New(failingProjectLC{LedgerClient: lc, err: readErr})
	if _, err := broken.ListSessions(""); !errors.Is(err, readErr) {
		t.Fatalf("列表读项目绑定失败应返回错误，得 %v", err)
	}
	if _, err := broken.SessionDetail(pr.ID); !errors.Is(err, readErr) {
		t.Fatalf("详情读项目绑定失败应返回错误，得 %v", err)
	}
}

// 拆解 §5.3「枚举新值过白名单」：项目房间是普通会话——IsSessionRoom 为真、驻场者可发言、人类无 @ 经
// Matches 命中驻场者地址、ListRooms 不把它列成旧 project:<name> 房间；旧 project:<name> 房间仍只读。
func TestB435ProjectRoomIsOrdinarySession(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	pr, _, err := svc.EnsureProjectRoom("handoff", "cli:u@h")
	if err != nil {
		t.Fatal(err)
	}
	if !room.IsSessionRoom(pr.ID) {
		t.Fatalf("项目房间 %s 应是会话房间", pr.ID)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: pr.ID, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(pr.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "收到"}, "agent:org"); err != nil {
		t.Fatalf("驻场者应可在项目房间发言: %v", err)
	}
	addr := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: pr.ID, Identity: "agent:org"}
	hit, err := svc.Matches(proto.RoomMessage{Room: pr.ID, Kind: proto.RoomMsgUser, Body: "人类无 @"}, "user:x", addr)
	if err != nil || !hit {
		t.Fatalf("人类无 @ 应默认寄驻场者: hit=%v err=%v", hit, err)
	}
	if _, err := st.CreateCard(ledger.NewCard{Title: "有卡才有 project 旧房间", Project: "handoff", Workflow: "bug", Actor: "t"}); err != nil {
		t.Fatal(err)
	}
	rooms, err := svc.ListRooms("handoff")
	if err != nil {
		t.Fatal(err)
	}
	sawLegacy := false
	for _, r := range rooms {
		if r.ID == pr.ID {
			t.Fatalf("ListRooms 不得把项目房间 %s 列成旧房间: %+v", pr.ID, r)
		}
		if r.ID == "project:handoff" {
			sawLegacy = true
		}
	}
	if !sawLegacy {
		t.Fatalf("前提：ListRooms 应仍列出旧 project:handoff 房间: %+v", rooms)
	}
	if _, err := svc.Send("project:handoff", proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "旧房间"}, "user:x"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("旧 project:<name> 房间应仍只读（ErrReadOnly），得 %v", err)
	}
}

// R-K1 (a′) 追加裁决：项目房间的书写者集另并入同项目各卡的当前席位——协调者的卡挂在自己的工作房间（一卡
// 同时只属一个会话），却要能到项目房间显式 @ 组织者提问（spec D8）。反例：他项目卡席位仍被拒；非项目房间
// 的书写者集不扩；成功那条的寻址不变（显式 @ 命中驻场者地址）。真 Store + Facade + collab。
func TestB435ProjectRoomWritersIncludeSameProjectSeats(t *testing.T) {
	svc, st, lc := newSessionFixture(t)
	pr, _, err := svc.EnsureProjectRoom("p", proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: pr.ID, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	work, err := svc.CreateSession("A 的工作房间", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateSession("A 不在里面的普通会话", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	seat := func(project, sid string) string {
		t.Helper()
		card, err := st.CreateCard(ledger.NewCard{Title: "卡 " + project, Project: project, Workflow: "bug", Actor: "t"})
		if err != nil {
			t.Fatal(err)
		}
		identity := "cli:opencode#" + sid
		if err := st.BindSeat(card.ID, identity, proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
			t.Fatal(err)
		}
		if project == "p" {
			if err := svc.JoinCard(work.ID, card.ID, "user:o"); err != nil {
				t.Fatal(err)
			}
		}
		return identity
	}
	seatA, seatB := seat("p", "a-1"), seat("q", "b-1")

	ask := proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@agent:org 问题", Mentions: []string{"agent:org"}}
	seq, err := svc.Send(pr.ID, ask, seatA)
	if err != nil {
		t.Fatalf("同项目卡席位应可在项目房间发言: %v", err)
	}
	if ev, ok, err := st.EventBySeq(seq); err != nil || !ok || ev.Actor != seatA {
		t.Fatalf("读回 seq=%d: ok=%v err=%v actor=%q", seq, ok, err, ev.Actor)
	}
	if _, err := svc.Send(pr.ID, ask, seatB); !errors.Is(err, ErrNotWriter) {
		t.Fatalf("他项目卡席位向项目房间发言应 ErrNotWriter，得 %v", err)
	}
	if _, err := svc.Send(other.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "串门"}, seatA); !errors.Is(err, ErrNotWriter) {
		t.Fatalf("非项目房间的书写者集不得扩（应 ErrNotWriter），得 %v", err)
	}
	ask.Room = pr.ID
	got, err := svc.Recipients(ask, seatA)
	if err != nil {
		t.Fatal(err)
	}
	want := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: pr.ID, Identity: "agent:org"}
	hit := false
	for _, a := range got.Addresses {
		hit = hit || a == want
	}
	if !hit {
		t.Fatalf("显式 @ 驻场者应命中驻场者地址 %+v，得 %+v", want, got)
	}
	readErr := errors.New("项目绑定读失败")
	broken := New(failingProjectLC{LedgerClient: lc, err: readErr})
	if _, err := broken.Send(pr.ID, ask, seatA); !errors.Is(err, readErr) || errors.Is(err, ErrNotWriter) {
		t.Fatalf("读项目绑定失败应原样上抛、不是 ErrNotWriter，得 %v", err)
	}
}

// R-K1 (a″) 协调者裁决：含项目 p 的卡的会话，书写者集另并入 p 的项目房间驻场者——组织者（项目房间驻场者
// agent:<名字>）要能把组织类回答发回卡所在会话（spec D9）。反例：只含他项目卡的会话、无卡普通会话、项目
// 有房间但无驻场者、项目无项目房间——均不扩（ErrNotWriter）。寻址不变：答话显式 @<卡号> 命中卡席位地址。
// 真 Store + Facade + collab。
func TestB435CardSessionWritersIncludeProjectRoomResident(t *testing.T) {
	svc, st, lc := newSessionFixture(t)
	pr, _, err := svc.EnsureProjectRoom("p", proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: pr.ID, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	// r 有项目房间但无驻场者；s 无项目房间。
	if _, _, err := svc.EnsureProjectRoom("r", proto.SystemNotifierIdentity); err != nil {
		t.Fatal(err)
	}
	cardSession := func(project, sid string) (session, cardID, seat string) {
		t.Helper()
		sess, err := svc.CreateSession("卡会话 "+project, "user:x", "user:x")
		if err != nil {
			t.Fatal(err)
		}
		card, err := st.CreateCard(ledger.NewCard{Title: "卡 " + project, Project: project, Workflow: "bug", Actor: "t"})
		if err != nil {
			t.Fatal(err)
		}
		seat = "cli:opencode#" + sid
		if err := st.BindSeat(card.ID, seat, proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
			t.Fatal(err)
		}
		if err := svc.JoinCard(sess.ID, card.ID, "user:x"); err != nil {
			t.Fatal(err)
		}
		return sess.ID, card.ID, seat
	}
	s1, cardA, seatA := cardSession("p", "a-1")
	s2, _, _ := cardSession("q", "q-1")
	s3, _, _ := cardSession("r", "r-1")
	s4, _, _ := cardSession("s", "s-1")
	plain, err := svc.CreateSession("无卡普通会话", "user:x", "user:x")
	if err != nil {
		t.Fatal(err)
	}

	answer := proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@" + cardA + " 组织类回答", Mentions: []string{cardA}}
	seq, err := svc.Send(s1, answer, "agent:org")
	if err != nil {
		t.Fatalf("项目房间驻场者应可向同项目卡会话发言: %v", err)
	}
	if ev, ok, err := st.EventBySeq(seq); err != nil || !ok || ev.Actor != "agent:org" {
		t.Fatalf("读回 seq=%d: ok=%v err=%v actor=%q", seq, ok, err, ev.Actor)
	}
	for name, sid := range map[string]string{"他项目卡会话": s2, "无卡普通会话": plain.ID, "项目房间无驻场者": s3, "项目无项目房间": s4} {
		if _, err := svc.Send(sid, answer, "agent:org"); !errors.Is(err, ErrNotWriter) {
			t.Errorf("%s：书写者集不得扩（应 ErrNotWriter），得 %v", name, err)
		}
	}
	answer.Room = s1
	got, err := svc.Recipients(answer, "agent:org")
	if err != nil {
		t.Fatal(err)
	}
	want := proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: s1, Card: cardA, Identity: seatA}
	hit := false
	for _, a := range got.Addresses {
		hit = hit || a == want
	}
	if !hit {
		t.Fatalf("显式 @%s 应命中卡席位地址 %+v，得 %+v", cardA, want, got)
	}
	readErr := errors.New("项目房间读失败")
	broken := New(failingProjectRoomLC{LedgerClient: lc, err: readErr})
	if _, err := broken.Send(s1, answer, "agent:org"); !errors.Is(err, readErr) || errors.Is(err, ErrNotWriter) {
		t.Fatalf("读项目房间失败应原样上抛、不是 ErrNotWriter，得 %v", err)
	}
}

// failingProjectRoomLC 在 ProjectRoom 读面注入错误（其余方法透传真 Facade）。
type failingProjectRoomLC struct {
	client.LedgerClient
	err error
}

func (f failingProjectRoomLC) ProjectRoom(string) (string, bool, error) { return "", false, f.err }
