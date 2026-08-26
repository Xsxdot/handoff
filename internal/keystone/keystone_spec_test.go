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
}

func (r *specRecorder) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	r.launches = append(r.launches, spec)
	return keysclient.TurnResult{SessionID: "sess-new", Output: "ok"}, nil
}

func (r *specRecorder) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}

// stubLedgerView 让 briefing 的读账路径返回「未找到」而非崩溃——launchRound 会
// 拼开场简报，nil 接口直接 panic。
type stubLedgerView struct{}

func (stubLedgerView) GetCard(string) (proto.Card, error) {
	return proto.Card{}, errors.New("not found")
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
	if _, err := svc.LaunchForCard(context.Background(), "B1", "card_create", want); err != nil {
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
