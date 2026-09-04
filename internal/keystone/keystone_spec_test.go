package keystone

import (
	"context"
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

// specRecorder 记录 Launch 收到的 SessionSpec——「spec 回潮」的观测点（契约
// §15 澄清 2：launchRound 必须消费入参，防空 spec 回潮）。
type specRecorder struct {
	launches []keysclient.SessionSpec
	resumes  []keysclient.SessionRef
}

func (r *specRecorder) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	r.launches = append(r.launches, spec)
	return keysclient.TurnResult{SessionID: "sess-new", Output: "ok"}, nil
}

func (r *specRecorder) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	r.resumes = append(r.resumes, ref)
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}

// stubLedgerView 为无状态的协调者单测提供一个账本 coordinate 席位；Wake 的
// 来源判定必须仍然经过这个读面，不能只看 keystone 内存。
type stubLedgerView struct{}

func (stubLedgerView) GetCard(string) (proto.Card, error) {
	return proto.Card{DriverSession: "cli:opencode#sess-new", DriverSource: "coordinate"}, nil
}
func (stubLedgerView) EventsFromAsc([]string, int64, int) ([]proto.LedgerEvent, error) {
	return nil, nil
}
func (stubLedgerView) EffectiveBaseBranch(string) (string, error)  { return "", errors.New("not found") }
func (stubLedgerView) MarkNeedsHuman(string, string, string) error { return nil }

// TestLaunchForCardConsumesSessionSpec 锁澄清 2：LaunchForCard 的 spec 必须原样
// 传给承载缝（CLI/HomeDir/Model/Workdir 逐一相等）。
//
// 变异复验程序（必须执行，两次读数落台账）：
//  1. 把 launchRound 改回自造空 spec（忽略入参 spec）——本测试必翻红（CLI 变空）；
//  2. 恢复实现 → 复跑转绿。
func TestLaunchForCardConsumesSessionSpec(t *testing.T) {
	rec := &specRecorder{}
	svc := New(rec, nil, stubLedgerView{}, nil)
	want := keysclient.SessionSpec{CLI: "opencode", HomeDir: "/home/coord", Model: "fast", Workdir: "/w"}
	if _, err := svc.LaunchForCard(context.Background(), "B1", "coordinate", want); err != nil {
		t.Fatalf("LaunchForCard: %v", err)
	}
	if len(rec.launches) != 1 {
		t.Fatalf("Launch 次数=%d，want 1", len(rec.launches))
	}
	got := rec.launches[0]
	// SessionSpec 含 Env []string 不可 ==，逐字段断言（四字段逐一相等是判据本体）。
	if got.CLI != want.CLI || got.HomeDir != want.HomeDir ||
		got.Model != want.Model || got.Workdir != want.Workdir {
		t.Fatalf("spec 未消费（回潮）：\n got=%+v\nwant=%+v", got, want)
	}
	if len(got.Env) != len(want.Env) {
		t.Fatalf("spec.Env 长度不符：%d vs %d", len(got.Env), len(want.Env))
	}
}

// TestWakeWithoutSessionResumesLedgerSeat 进程重启丢失 keystone 内存时，Wake 仍
// 必须从账本 coordinate 席位恢复并 Resume，不能把合法席位误判为空座再 Launch。
func TestWakeWithoutSessionConsumesSpec(t *testing.T) {
	rec := &specRecorder{}
	svc := New(rec, nil, stubLedgerView{}, nil)
	want := keysclient.SessionSpec{CLI: "opencode", HomeDir: "/home/coord", Model: "fast", Workdir: "/w"}
	if _, err := svc.Wake(context.Background(), "B1", []WakeEvent{
		{Kind: WakeMessage, Card: "B1", Summary: "用户留言"},
	}, want); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(rec.launches) != 0 || len(rec.resumes) != 1 {
		t.Fatalf("账本有 coordinate 席位时应 Resume 一次且不 Launch：launch=%d resume=%d", len(rec.launches), len(rec.resumes))
	}
	got := rec.resumes[0]
	if got.CLI != "opencode" || got.SessionID != "sess-new" || got.HomeDir != want.HomeDir ||
		got.Model != want.Model || got.Workdir != want.Workdir {
		t.Fatalf("账本席位/续接 spec 未消费：\n got=%+v\nwant=%+v", got, want)
	}
}

// TestWakeResumeCarriesIsolatedHome 锁 B299：首次拉起不是重建；续接即使
// Wake spec 没带 HOME，也必须沿用 Launch 写入 SessionRef 的隔离环境。
// 真机二次唤醒刷「载体已更换」就是这条丢了。
func TestWakeResumeCarriesIsolatedHome(t *testing.T) {
	rec := &specRecorder{}
	svc := New(rec, nil, stubLedgerView{}, nil)
	want := keysclient.SessionSpec{CLI: "opencode", HomeDir: "/home/coord", Model: "fast", Workdir: "/w"}
	opened, err := svc.LaunchForCard(context.Background(), "B1", "coordinate", want)
	if err != nil {
		t.Fatalf("LaunchForCard: %v", err)
	}
	if opened.Rebuilt {
		t.Fatalf("首次拉起 Rebuilt=%v，want false", opened.Rebuilt)
	}
	if _, err := svc.Wake(context.Background(), "B1", []WakeEvent{
		{Kind: WakeMessage, Card: "B1", Summary: "hi"},
	}, keysclient.SessionSpec{CLI: "opencode"}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(rec.resumes) != 1 {
		t.Fatalf("Resume 次数=%d，want 1", len(rec.resumes))
	}
	got := rec.resumes[0]
	if got.SessionID != opened.SessionID {
		t.Fatalf("续接 session=%q，want %q", got.SessionID, opened.SessionID)
	}
	if got.HomeDir != want.HomeDir || got.Workdir != want.Workdir || got.Model != want.Model {
		t.Fatalf("续接丢了隔离环境：\n got=%+v\nwant HomeDir/Workdir/Model=%s/%s/%s",
			got, want.HomeDir, want.Workdir, want.Model)
	}
}

// TestWakeResumeOverlaysCurrentCarrierHome 当前载体 HOME 变了，续接跟 Wake
// 入参走，不钉死第一次 Launch 的目录。
func TestWakeResumeOverlaysCurrentCarrierHome(t *testing.T) {
	rec := &specRecorder{}
	svc := New(rec, nil, stubLedgerView{}, nil)
	if _, err := svc.LaunchForCard(context.Background(), "B1", "coordinate", keysclient.SessionSpec{
		CLI: "opencode", HomeDir: "/old", Model: "a", Workdir: "/oldw",
	}); err != nil {
		t.Fatalf("LaunchForCard: %v", err)
	}
	fresh := keysclient.SessionSpec{CLI: "opencode", HomeDir: "/home/coord", Model: "fast", Workdir: "/w"}
	if _, err := svc.Wake(context.Background(), "B1", []WakeEvent{
		{Kind: WakeMessage, Card: "B1", Summary: "hi"},
	}, fresh); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(rec.resumes) != 1 {
		t.Fatalf("Resume 次数=%d，want 1", len(rec.resumes))
	}
	got := rec.resumes[0]
	if got.HomeDir != fresh.HomeDir || got.Workdir != fresh.Workdir || got.Model != fresh.Model {
		t.Fatalf("Wake spec 未覆盖续接环境：%+v", got)
	}
}

type bindLedgerView struct{}

func (bindLedgerView) GetCard(string) (proto.Card, error) {
	return proto.Card{DriverSession: "cli:codex#thread-01", DriverSource: "bind"}, nil
}
func (bindLedgerView) EventsFromAsc([]string, int64, int) ([]proto.LedgerEvent, error) {
	return nil, nil
}
func (bindLedgerView) EffectiveBaseBranch(string) (string, error)  { return "", errors.New("not found") }
func (bindLedgerView) MarkNeedsHuman(string, string, string) error { return nil }

func TestWakeBindSeatIsNoOpAndForgetsStaleSession(t *testing.T) {
	rec := &specRecorder{}
	svc := New(rec, nil, bindLedgerView{}, nil)
	if _, err := svc.LaunchForCard(context.Background(), "B1", "coordinate", keysclient.SessionSpec{CLI: "opencode"}); err != nil {
		t.Fatalf("seed memory session: %v", err)
	}
	result, err := svc.Wake(context.Background(), "B1", []WakeEvent{{Kind: WakeMessage, Card: "B1", Summary: "hi"}}, keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("bind Wake: %v", err)
	}
	if result.Woke || len(rec.resumes) != 0 || len(rec.launches) != 1 {
		t.Fatalf("bind 席位必须 no-op（仅有 seed Launch）：result=%+v launches=%d resumes=%d", result, len(rec.launches), len(rec.resumes))
	}
}

func TestLaunchForCardRejectsNonCoordinateSource(t *testing.T) {
	rec := &specRecorder{}
	svc := New(rec, nil, stubLedgerView{}, nil)
	if _, err := svc.LaunchForCard(context.Background(), "B1", "manual", keysclient.SessionSpec{CLI: "opencode"}); err == nil {
		t.Fatal("退役 source 必须被拒绝")
	}
	if len(rec.launches) != 0 {
		t.Fatalf("退役 source 不得调用 Runner，Launch=%d", len(rec.launches))
	}
}

type resolveRefFunc func(string, keysclient.SessionRef) (keysclient.SessionRef, error)

func (f resolveRefFunc) ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
	return f(card, ref)
}

type recordingLocator struct{ Ref keysclient.SessionRef }

func (l *recordingLocator) Locate(ref keysclient.SessionRef, workdir string) (keysclient.AttachInfo, error) {
	l.Ref = ref
	return keysclient.AttachInfo{
		Dir:     workdir,
		Command: "HOME=" + ref.HomeDir + " " + ref.CLI + " --session " + ref.SessionID,
	}, nil
}

func TestLocateHotRefResolvesHomeBeforeLocator(t *testing.T) {
	rec := &specRecorder{}
	loc := &recordingLocator{}
	svc := New(rec, nil, stubLedgerView{}, loc)
	svc.SetSessionRefResolver(resolveRefFunc(func(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
		if card != "B1" || ref.HomeDir != "~/hot" {
			t.Fatalf("hot ref 未原样交给 resolver: card=%q ref=%+v", card, ref)
		}
		ref.HomeDir = "/abs/hot"
		return ref, nil
	}))
	if _, err := svc.LaunchForCard(context.Background(), "B1", "coordinate",
		keysclient.SessionSpec{CLI: "opencode", HomeDir: "~/hot", Workdir: "/w"}); err != nil {
		t.Fatalf("seed LaunchForCard: %v", err)
	}
	got, err := svc.Locate("B1", "/w")
	if err != nil {
		t.Fatalf("Locate hot: %v", err)
	}
	if got.Command != "HOME=/abs/hot opencode --session sess-new" || loc.Ref.HomeDir != "/abs/hot" {
		t.Fatalf("hot locator 未消费展开 HomeDir: got=%+v ref=%+v", got, loc.Ref)
	}
}

func TestLocateColdRefResolvesRegisteredHomeBeforeLocator(t *testing.T) {
	loc := &recordingLocator{}
	svc := New(&specRecorder{}, nil, stubLedgerView{}, loc)
	svc.SetSessionRefResolver(resolveRefFunc(func(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
		if card != "B1" || ref.HomeDir != "" || ref.SessionID != "sess-new" {
			t.Fatalf("cold ref 形状错误: card=%q ref=%+v", card, ref)
		}
		ref.HomeDir = "/abs/cold"
		return ref, nil
	}))
	got, err := svc.Locate("B1", "/w")
	if err != nil {
		t.Fatalf("Locate cold: %v", err)
	}
	if got.Command != "HOME=/abs/cold opencode --session sess-new" || loc.Ref.HomeDir != "/abs/cold" {
		t.Fatalf("cold locator 未消费登记 HomeDir: got=%+v ref=%+v", got, loc.Ref)
	}
}
