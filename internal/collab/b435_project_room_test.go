// B435 O1：项目房间在会话服务一侧的前提与写面——系统发信身份作群主即书写者（段 0a，Q1 甲前提）、
// EnsureProjectRoom 唯一入口的项目名校验（Q15 甲）、SessionSummary.project 装配（Q9 甲，C-510/511）、
// 项目房间是普通会话（拆解 §5.3「枚举新值过白名单」）。
package collab

import (
	"encoding/json"
	"testing"

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
