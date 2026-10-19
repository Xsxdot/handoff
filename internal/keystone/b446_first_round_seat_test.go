// b446_first_round_seat_test.go —— B446：launch 首轮简报不得诱导协调者自己坐席位。
//
// 缝：Service.LaunchForCard（生产入口）送进 Runner 的 prompt。真机 B444 R6（卡 D5）：首轮句只说
// 「席位尚未落定」，协调者读成「该我去坐」，从 opencode 日志里翻出自己的会话 id 跑了
// `card bind --cli opencode --session <id>`（落成 bind/pull 席位），launch 收尾的 BindSeatMode
// 随即撞「席位非空」→ 409 + needs_human。
package keystone

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestB446LaunchBriefingForbidsSelfSeat 锁首轮句的两件事：席位由 agentd 在本轮结束后落定
// （说清是谁落、何时落），以及本轮不得自己 `card bind` / `card rebind` 坐下。
func TestB446LaunchBriefingForbidsSelfSeat(t *testing.T) {
	rec := &b435PromptRecorder{}
	svc := New(rec, nil, modeLedgerView{card: proto.Card{}}, nil)
	if _, err := svc.LaunchForCard(context.Background(), "B1", "coordinate", keysclient.SessionSpec{CLI: "opencode"}); err != nil {
		t.Fatalf("LaunchForCard: %v", err)
	}
	if len(rec.prompts) != 1 {
		t.Fatalf("应恰一次回合，得 %d", len(rec.prompts))
	}
	got := rec.prompts[0]
	for _, want := range []string{"本轮结束后由 agentd", "`handoff card bind`", "`handoff card rebind`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("launch 首轮简报缺 %q:\n%s", want, got)
		}
	}
}
