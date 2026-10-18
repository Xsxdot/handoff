// b435_briefing_send_test.go —— B435.2 F-RM1 的 CLI 半边集成断言（R plan 段 6、Q1 乙）。
//
// push 协调者按简报敲出的发言命令必须以卡席位身份落账并通过书写者校验。命令形态与
// keystone.briefingSessionSend 一致（`handoff session send --cli <cli> --session <sid> <会话> <正文>`，
// 其形态由 keystone 包 TestB435BriefingSessionSendPresentsSeat 锁住；cmd 不能引用 keystone 私有帮手，
// 此处按同一形态逐字拼 argv）。夹具：真 SQLite 账本 + 真 cobra 入口 + 会话里一张 push 席位卡。
package cmd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// b435LastRoomMessageActor 返回会话最后一条房间消息事件的落账 actor。
func (e *b435WaitEnv) lastRoomMessageActor(t *testing.T, body string) (string, bool) {
	t.Helper()
	events, err := e.st.EventsFromAsc(nil, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	actor, found := "", false
	for _, ev := range events {
		if ev.Type == ledger.EvRoomMessage && strings.Contains(string(ev.Payload), body) {
			actor, found = ev.Actor, true
		}
	}
	return actor, found
}

func TestB435BriefingSendCommandLandsAsSeat(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	root := e.card(t)
	seat := e.push(t, root, "ses_x") // cli:opencode#ses_x 坐在会话卡上

	t.Run("resume 回合：两键已注入 + 字面席位", func(t *testing.T) {
		t.Setenv("HANDOFF_SESSION_CLI", "opencode")
		t.Setenv("HANDOFF_SESSION_ID", "ses_x")
		out, _, err := runLedgerCLI(t, e.dir, "session", "send", "--cli", "opencode", "--session", "ses_x",
			e.session, "B435 回房间-字面")
		if err != nil || !strings.Contains(out, `"ok":true`) {
			t.Fatalf("简报命令应成功: err=%v out=%q", err, out)
		}
		if actor, ok := e.lastRoomMessageActor(t, "B435 回房间-字面"); !ok || actor != seat {
			t.Fatalf("落账 actor 应为席位 %s，得 %q（found=%v）", seat, actor, ok)
		}
	})

	t.Run("对照：旧简报裸 send 以终端身份落账被拒", func(t *testing.T) {
		t.Setenv("HANDOFF_SESSION_CLI", "opencode")
		t.Setenv("HANDOFF_SESSION_ID", "ses_x")
		_, _, err := runLedgerCLI(t, e.dir, "session", "send", e.session, "B435 回房间-裸")
		b435NonZeroNot124(t, err, "裸 session send（终端身份不是书写者）")
		if _, ok := e.lastRoomMessageActor(t, "B435 回房间-裸"); ok {
			t.Fatal("被拒的发言不得落账")
		}
	})

	t.Run("launch 回合：两键未注入时环境键形态显式失败", func(t *testing.T) {
		b435UnsetSessionEnv(t)
		// `--cli "$HANDOFF_SESSION_CLI" --session "$HANDOFF_SESSION_ID"` 在两键未设时经 shell 展开为空串。
		_, _, err := runLedgerCLI(t, e.dir, "session", "send", "--cli", "", "--session", "",
			e.session, "B435 回房间-首轮")
		b435NonZeroNot124(t, err, "两键未注入的环境键形态")
		if _, ok := e.lastRoomMessageActor(t, "B435 回房间-首轮"); ok {
			t.Fatal("失败的发言不得落账")
		}
	})
}
