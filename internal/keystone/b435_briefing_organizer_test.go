// b435_briefing_organizer_test.go —— B435 R-K1 (a) / spec r4 D8 / 契约 §12 欠账 13：push 简报写明「向组织者
// 提问须先 `handoff project room <项目>` 取驻场者身份，再显式 @」（O1 plan Q13 甲）。
//
// 缝：Service.Wake（生产入口）送进 Runner.Resume 的 prompt。测试只引用包内常量 briefingAskOrganizer。
package keystone

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB435BriefingAsksOrganizerByMention(t *testing.T) {
	for _, must := range []string{"handoff project room", "@", "项目房间", "session send"} {
		if !strings.Contains(briefingAskOrganizer, must) {
			t.Fatalf("briefingAskOrganizer 应含 %q，得 %q", must, briefingAskOrganizer)
		}
	}
	rec := &b435PromptRecorder{}
	svc := New(rec, nil, modeLedgerView{card: proto.Card{
		DriverSession: "cli:opencode#s1", DriverSource: "coordinate", DriverMode: "push"}}, nil)
	if _, err := svc.Wake(context.Background(), "B1", []WakeEvent{{Kind: WakeMessage, Card: "B1", Summary: "hi"}},
		keysclient.SessionSpec{CLI: "opencode"}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(rec.prompts) != 1 {
		t.Fatalf("push 席位应恰一次回合，得 %d", len(rec.prompts))
	}
	if !strings.Contains(rec.prompts[0], briefingAskOrganizer) {
		t.Fatalf("简报缺「向组织者提问须显式 @」句 %q:\n%s", briefingAskOrganizer, rec.prompts[0])
	}
}
