// b435_seat_mode_test.go —— keystone 按席位模式判定（契约 C-315、Locate 冷 attach；C-318）。
//
// 判别夹具：来源词与模式相反的卡（coordinate + pull、bind + push）——只有读 mode 的实现能过。
package keystone

import (
	"context"
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

type modeLedgerView struct{ card proto.Card }

func (v modeLedgerView) GetCard(string) (proto.Card, error) { return v.card, nil }
func (modeLedgerView) EventsFromAsc([]string, int64, int) ([]proto.LedgerEvent, error) {
	return nil, nil
}
func (modeLedgerView) EffectiveBaseBranch(string) (string, error)  { return "", errors.New("not found") }
func (modeLedgerView) MarkNeedsHuman(string, string, string) error { return nil }

// C-315：pull 席位 Wake no-op（不 Resume、不 Launch）；对照 push 席位 Resume 恰一次；
// mode 非两值而席位非空 → 非法席位（报错且不 Resume）。
func TestB435KeystoneWakePullIsNoop(t *testing.T) {
	evs := []WakeEvent{{Kind: WakeMessage, Card: "B1", Summary: "hi"}}
	spec := keysclient.SessionSpec{CLI: "opencode"}
	for _, tc := range []struct {
		name        string
		card        proto.Card
		wantResumes int
		wantErr     bool
	}{
		{"coordinate 来源但 pull 模式", proto.Card{DriverSession: "cli:opencode#s1", DriverSource: "coordinate", DriverMode: "pull"}, 0, false},
		{"push 模式对照", proto.Card{DriverSession: "cli:opencode#s1", DriverSource: "coordinate", DriverMode: "push"}, 1, false},
		{"模式缺失的非空座", proto.Card{DriverSession: "cli:opencode#s1", DriverSource: "coordinate"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &specRecorder{}
			svc := New(rec, nil, modeLedgerView{card: tc.card}, nil)
			result, err := svc.Wake(context.Background(), "B1", evs, spec)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if len(rec.resumes) != tc.wantResumes || len(rec.launches) != 0 {
				t.Fatalf("resumes=%d launches=%d want resumes=%d", len(rec.resumes), len(rec.launches), tc.wantResumes)
			}
			if tc.wantResumes == 0 && result.Woke {
				t.Fatalf("no-op 不得报 Woke: %+v", result)
			}
		})
	}
}

type b435Locator struct{ calls int }

func (l *b435Locator) Locate(ref keysclient.SessionRef, workdir string) (keysclient.AttachInfo, error) {
	l.calls++
	return keysclient.AttachInfo{Machine: "local", Dir: workdir}, nil
}

// Locate 冷 attach（内存无会话）只认 push 席位：pull 席位不得走到 locator。
func TestB435LocateRequiresPush(t *testing.T) {
	pullLoc := &b435Locator{}
	pull := New(&specRecorder{}, nil, modeLedgerView{card: proto.Card{
		DriverSession: "cli:opencode#s1", DriverSource: "coordinate", DriverMode: "pull"}}, pullLoc)
	if _, err := pull.Locate("B1", "/w"); err == nil || pullLoc.calls != 0 {
		t.Fatalf("pull 席位不得冷 attach: err=%v locator 调用=%d", err, pullLoc.calls)
	}
	pushLoc := &b435Locator{}
	push := New(&specRecorder{}, nil, modeLedgerView{card: proto.Card{
		DriverSession: "cli:opencode#s1", DriverSource: "bind", DriverMode: "push"}}, pushLoc)
	if _, err := push.Locate("B1", "/w"); err != nil || pushLoc.calls != 1 {
		t.Fatalf("push 席位应可冷 attach: err=%v locator 调用=%d", err, pushLoc.calls)
	}
}
