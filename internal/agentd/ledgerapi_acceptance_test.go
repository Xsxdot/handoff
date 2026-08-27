// 验收判据 PATCH 回归测试：验证 HTTP 路径复用 Store.SetAcceptance，
// 在飞挂账写入卡时间线而不改变成功响应；归档挂账不产生误报。
package agentd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestPatchCardAcceptanceReportsInFlightTasks(t *testing.T) {
	cases := []struct {
		name      string
		states    map[string]string
		wantWarn  bool
		wantTasks []string
		deadTasks []string
	}{
		{name: "completed 仍在飞", states: map[string]string{"T-http-completed": "completed"}, wantWarn: true, wantTasks: []string{"linux-01/T-http-completed"}},
		{name: "只归档不警告", states: map[string]string{"T-http-archived": "archived"}},
		{name: "归档加在飞只列在飞", states: map[string]string{"T-http-archived": "archived", "T-http-live": "turn_failed"}, wantWarn: true, wantTasks: []string{"linux-01/T-http-live"}, deadTasks: []string{"linux-01/T-http-archived"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newNoPTYLedgerEnv(t)
			card := seedCard(t, env, "验收判据 PATCH")
			for taskID, lastType := range tc.states {
				if err := env.ledger.LinkTask(card.ID, "linux-01", taskID, ledger.PurposeImplement, "test"); err != nil {
					t.Fatalf("挂账 %s: %v", taskID, err)
				}
				if _, err := env.ledger.AppendMirroredEvent(card.ID, ledger.MirroredEvent{
					Target: "linux-01", Task: taskID, SourceSeq: 1, Type: lastType,
					Payload: []byte(`{}`), CreatedAt: time.Now(),
				}); err != nil {
					t.Fatalf("镜像 %s: %v", taskID, err)
				}
			}

			code, body := ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID,
				`{"acceptance_criteria":"PATCH 新判据"}`)
			if code != 200 || !strings.Contains(body, `"ok":true`) {
				t.Fatalf("PATCH 成功响应错误：code=%d body=%q", code, body)
			}
			comments := acceptanceCommentBodiesHTTP(t, env.ledger, card.ID)
			joined := strings.Join(comments, "\n")
			if got := strings.Contains(joined, ledger.AcceptanceInFlightNotice); got != tc.wantWarn {
				t.Fatalf("事件是否警告=%v，want %v；events=%q", got, tc.wantWarn, joined)
			}
			for _, taskID := range tc.wantTasks {
				if !strings.Contains(joined, taskID) {
					t.Fatalf("在飞 task %s 未出现在卡事件：%q", taskID, joined)
				}
			}
			for _, taskID := range tc.deadTasks {
				if strings.Contains(joined, taskID) {
					t.Fatalf("终态 task %s 不应出现在警告：%q", taskID, joined)
				}
			}
		})
	}
}

func acceptanceCommentBodiesHTTP(t *testing.T, st *ledger.Store, cardID string) []string {
	t.Helper()
	events, err := st.EventsFromAsc([]string{cardID}, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, event := range events {
		if event.Type != ledger.EvComment {
			continue
		}
		var payload struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("解码评论事件: %v", err)
		}
		bodies = append(bodies, payload.Body)
	}
	return bodies
}
