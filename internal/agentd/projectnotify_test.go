// projectnotify_test.go —— B435 O1 系统通知（契约 C-243–C-245、C-231/C-238；plan Q6 甲 / Q7 乙 / Q8 甲）。
//
// 观察点：真账本 + 真 collab + 真 consumeAutomationEventsOnce + fake runner（queueTraceRunner.resumes
// = keystone 回合次数）。一律经消费循环驱动，不直调通知函数；「零条」断言都在同一夹具里配「一条」对照。
package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// 段 0b：系统身份 @驻场者 的普通消息落账后跑一轮消费 → 零 keystone Wake（C-231），且
// Recipients 对它恰返回驻场者地址、Default=false（C-214 显式分支）。对照：同会话 @push 卡恰一次 Wake，
// 证明本夹具里消费循环确实能唤醒。
func TestB435NotifyShapeRoutesToResidentWithoutWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	pushCard := createCoordCard(t, env)
	b435BindPush(t, env, pushCard, "notify-0b")
	session, err := env.srv.rooms.CreateSession("项目房间前提", proto.SystemNotifierIdentity, proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.srv.rooms.JoinCard(session.ID, pushCard, "user:tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.SitResident(proto.SessionResident{Session: session.ID, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	drainAutomation(t, env)

	msg := proto.RoomMessage{Room: session.ID, Kind: proto.RoomMsgUser, Body: "卡 B1 新建", Mentions: []string{"agent:org"}}
	sendSessionMessage(t, env.srv.rooms, session.ID, proto.SystemNotifierIdentity, msg.Body, msg.Mentions, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("@驻场者 的系统消息不得 keystone 唤醒: %v", got)
	}
	d, err := env.srv.rooms.Recipients(msg, proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatal(err)
	}
	want := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: session.ID, Identity: "agent:org"}
	if d.Default || len(d.Addresses) != 1 || d.Addresses[0] != want {
		t.Fatalf("应恰驻场者地址且非默认分支，得 %+v", d)
	}

	sendSessionMessage(t, env.srv.rooms, session.ID, "agent:org", "叫 push 卡", []string{pushCard}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 || !b435WokeCard(got[0], pushCard) {
		t.Fatalf("对照：@push 卡应恰一次唤醒: %v", got)
	}
}
