// b435_briefing_send_test.go —— B435.2 F-RM1 的 CLI 半边集成断言（R plan 段 6、Q1 乙）。
//
// push 协调者按简报敲出的发言命令必须以卡席位身份落账并通过书写者校验。命令形态与
// keystone.briefingSessionSend 一致（`handoff session send --cli <cli> --session <sid> <会话> <正文>`，
// 其形态由 keystone 包 TestB435BriefingSessionSendPresentsSeat 锁住；cmd 不能引用 keystone 私有帮手，
// 此处按同一形态逐字拼 argv）。夹具：真 SQLite 账本 + 真 cobra 入口 + 会话里一张 push 席位卡。
package cmd

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
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

// lastRoomMessage 返回正文含 body 的最后一条房间消息事件（actor 与载荷）。
func (e *b435WaitEnv) lastRoomMessage(t *testing.T, body string) (ledger.Event, bool) {
	t.Helper()
	events, err := e.st.EventsFromAsc(nil, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	var last ledger.Event
	found := false
	for _, ev := range events {
		if ev.Type == ledger.EvRoomMessage && strings.Contains(string(ev.Payload), body) {
			last, found = ev, true
		}
	}
	return last, found
}

// R-K1 (a′)：协调者卡挂在工作房间，按简报到同项目的项目房间发言——显式 @ 驻场者与不带 @ 都以席位落账
// （书写权与寻址是两件事：书写权由项目归属给，寻址由 @ 给）。O1-7 真机缺陷的 CLI 复现。
func TestB435CoordinatorSeatSendsToProjectRoom(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	root := e.card(t) // project=demo，挂在工作房间 e.session
	seat := e.push(t, root, "ses_x")
	pr, _, err := e.svc.EnsureProjectRoom(b435CLIProject, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SitResident(proto.SessionResident{Session: pr.ID, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"@agent:org B435 项目房间提问", "B435 项目房间无@"} {
		out, _, err := runLedgerCLI(t, e.dir, "session", "send", "--cli", "opencode", "--session", "ses_x", pr.ID, body)
		if err != nil || !strings.Contains(out, `"ok":true`) {
			t.Fatalf("%q：项目房间发言应成功: err=%v out=%q", body, err, out)
		}
		ev, ok := e.lastRoomMessage(t, body)
		if !ok || ev.Actor != seat {
			t.Fatalf("%q：落账 actor 应为席位 %s，得 %q（found=%v）", body, seat, ev.Actor, ok)
		}
		var payload struct {
			Room     string   `json:"room"`
			Mentions []string `json:"mentions"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		wantMention := strings.HasPrefix(body, "@agent:org")
		if got := slices.Contains(payload.Mentions, "agent:org"); got != wantMention {
			t.Fatalf("%q：mentions 含 agent:org 应为 %v，得 %v（payload=%s）", body, wantMention, payload.Mentions, ev.Payload)
		}
	}
}
