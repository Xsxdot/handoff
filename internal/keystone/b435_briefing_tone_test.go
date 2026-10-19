// b435_briefing_tone_test.go —— B435 D11：push 简报的房间口吻句。
//
// 缝：Service.Wake（生产入口）送进 Runner.Resume 的 prompt。措辞以 spec D11 为准，
// 测试只引用包内常量 briefingRoomTone，不在测试里另写一份。
package keystone

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

type b435PromptRecorder struct{ prompts []string }

func (r *b435PromptRecorder) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	r.prompts = append(r.prompts, prompt)
	return keysclient.TurnResult{SessionID: "sess-new", Output: "ok"}, nil
}

func (r *b435PromptRecorder) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	r.prompts = append(r.prompts, prompt)
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}

func TestB435BriefingRoomTone(t *testing.T) {
	if strings.TrimSpace(briefingRoomTone) == "" {
		t.Fatal("briefingRoomTone 不得为空")
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
	if !strings.Contains(rec.prompts[0], briefingRoomTone) {
		t.Fatalf("简报缺 D11 房间口吻句 %q:\n%s", briefingRoomTone, rec.prompts[0])
	}
}
