// b435_briefing_seat_test.go —— B435.2 F-RM1 简报半边（R plan Q1 乙、Q3 甲）。
//
// 缝：Service.Wake / LaunchForCard（生产入口）送进 Runner 的 prompt。断言读包内的
// 帮手与常量，另以字面子串钉住「成对出示席位」的形态，防帮手与断言同错。
package keystone

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

// b435RebuildRunner：Resume 恒报会话不存在，Launch 成功——走重建回合。
type b435RebuildRunner struct{ launches, resumes []string }

func (r *b435RebuildRunner) Launch(_ keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	r.launches = append(r.launches, prompt)
	return keysclient.TurnResult{SessionID: "sess-new", Output: "ok"}, nil
}

func (r *b435RebuildRunner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	r.resumes = append(r.resumes, prompt)
	return keysclient.TurnResult{}, keysclient.ErrSessionNotFound
}

// TestB435BriefingSessionSendPresentsSeat 锁 Q1 乙：resume 回合的简报以字面值成对出示
// 本卡席位（`--cli <cli> --session <sid>`），不再教裸 `handoff session send <会话>`；
// 解析不到席位值时退回 HANDOFF 环境键形态；launch 与重建回合（席位未落定、两键不注入，
// Q3 甲）只写首轮句，且重建回合不得带旧会话的字面席位。
func TestB435BriefingSessionSendPresentsSeat(t *testing.T) {
	const bare = "`handoff session send <会话> <正文>`"
	evs := []WakeEvent{{Kind: WakeMessage, Card: "B1", Summary: "hi"}}
	pushCard := proto.Card{DriverSession: "cli:opencode#ses_x", DriverSource: "coordinate", DriverMode: "push"}

	t.Run("resume 字面席位", func(t *testing.T) {
		rec := &b435PromptRecorder{}
		svc := New(rec, nil, modeLedgerView{card: pushCard}, nil)
		if _, err := svc.Wake(context.Background(), "B1", evs, keysclient.SessionSpec{CLI: "opencode"}); err != nil {
			t.Fatalf("Wake: %v", err)
		}
		if len(rec.prompts) != 1 {
			t.Fatalf("应恰一次回合，得 %d", len(rec.prompts))
		}
		got := rec.prompts[0]
		want := briefingSessionSend("opencode", "ses_x")
		if !strings.Contains(want, "handoff session send --cli opencode --session ses_x ") {
			t.Fatalf("字面形态应成对出示席位，帮手给出 %q", want)
		}
		if !strings.Contains(got, want) {
			t.Fatalf("resume 简报缺成对席位的发言命令 %q:\n%s", want, got)
		}
		if strings.Contains(got, bare) || strings.Contains(got, briefingFirstRound) {
			t.Fatalf("resume 简报不得教裸 send 或写首轮句:\n%s", got)
		}
	})

	t.Run("解析不到值退环境键形态", func(t *testing.T) {
		got := briefingSessionSend("", "ses_x")
		if !strings.Contains(got, `--cli "$HANDOFF_SESSION_CLI" --session "$HANDOFF_SESSION_ID"`) {
			t.Fatalf("缺值时应退 HANDOFF 环境键形态，得 %q", got)
		}
		if briefingSessionSend("opencode", "") != got {
			t.Fatalf("两值缺任一都应退同一环境键形态")
		}
	})

	t.Run("launch 首轮句", func(t *testing.T) {
		rec := &b435PromptRecorder{}
		svc := New(rec, nil, modeLedgerView{card: proto.Card{}}, nil)
		if _, err := svc.LaunchForCard(context.Background(), "B1", "coordinate", keysclient.SessionSpec{CLI: "opencode"}); err != nil {
			t.Fatalf("LaunchForCard: %v", err)
		}
		if len(rec.prompts) != 1 {
			t.Fatalf("应恰一次回合，得 %d", len(rec.prompts))
		}
		got := rec.prompts[0]
		if !strings.Contains(got, briefingFirstRound) || !strings.Contains(briefingFirstRound, "首轮") {
			t.Fatalf("launch 简报缺首轮句 %q:\n%s", briefingFirstRound, got)
		}
		if strings.Contains(got, bare) || strings.Contains(got, "--session ") {
			t.Fatalf("launch 简报不得给出发言命令（席位未落定、两键不注入）:\n%s", got)
		}
	})

	t.Run("重建回合不带旧席位", func(t *testing.T) {
		rec := &b435RebuildRunner{}
		svc := New(rec, nil, modeLedgerView{card: pushCard}, nil)
		res, err := svc.Wake(context.Background(), "B1", evs, keysclient.SessionSpec{CLI: "opencode"})
		if err != nil || !res.Rebuilt {
			t.Fatalf("应走重建回合，得 %+v / %v", res, err)
		}
		if len(rec.launches) != 1 {
			t.Fatalf("应恰一次重建 Launch，得 %d", len(rec.launches))
		}
		got := rec.launches[0]
		if strings.Contains(got, "ses_x") {
			t.Fatalf("重建回合不得带旧会话的字面席位:\n%s", got)
		}
		if !strings.Contains(got, briefingFirstRound) || !strings.Contains(got, "hi") {
			t.Fatalf("重建回合应写首轮句并保留本轮事件:\n%s", got)
		}
	})
}
