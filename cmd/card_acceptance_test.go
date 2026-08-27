// 验收判据更新的 CLI 回归测试：验证在飞挂账会在同一轮修改后出声，
// 且警告同时存在于 stderr 与卡事件；已归档挂账不制造误报。
package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestCardUpdateAcceptanceReportsInFlightTasks(t *testing.T) {
	cases := []struct {
		name      string
		states    map[string]string
		wantWarn  bool
		wantTasks []string
		deadTasks []string
	}{
		{name: "无镜像实况仍在飞", states: map[string]string{"T-empty": ""}, wantWarn: true, wantTasks: []string{"linux-01/T-empty"}},
		{name: "completed 仍在飞", states: map[string]string{"T-completed": "completed"}, wantWarn: true, wantTasks: []string{"linux-01/T-completed"}},
		{name: "只归档不警告", states: map[string]string{"T-archived": "archived"}},
		{name: "归档加在飞只列在飞", states: map[string]string{"T-archived": "archived", "T-live": "completed"}, wantWarn: true, wantTasks: []string{"linux-01/T-live"}, deadTasks: []string{"linux-01/T-archived"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, cardID := seedAcceptanceCLICard(t, tc.states)
			_, stderr, err := runLedgerCLI(t, dir, "card", "update", cardID, "--accept", "新判据")
			if err != nil {
				t.Fatalf("card update --accept: %v", err)
			}

			st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			bodies := acceptanceCommentBodies(t, st, cardID)
			joined := strings.Join(bodies, "\n")
			if got := strings.Contains(stderr, ledger.AcceptanceInFlightNotice); got != tc.wantWarn {
				t.Fatalf("stderr 是否警告=%v，want %v；stderr=%q", got, tc.wantWarn, stderr)
			}
			if got := strings.Contains(joined, ledger.AcceptanceInFlightNotice); got != tc.wantWarn {
				t.Fatalf("卡事件是否警告=%v，want %v；bodies=%q", got, tc.wantWarn, joined)
			}
			for _, taskID := range tc.wantTasks {
				if !strings.Contains(stderr, taskID) || !strings.Contains(joined, taskID) {
					t.Fatalf("在飞 task %s 未同时出现在 stderr 与事件：stderr=%q events=%q", taskID, stderr, joined)
				}
			}
			for _, taskID := range tc.deadTasks {
				if strings.Contains(stderr, taskID) || strings.Contains(joined, taskID) {
					t.Fatalf("终态 task %s 不应出现在警告：stderr=%q events=%q", taskID, stderr, joined)
				}
			}
		})
	}
}

func seedAcceptanceCLICard(t *testing.T, states map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "验收判据卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatalf("解码建卡: %v；原文=%q", err, out)
	}
	if created.ID == "" {
		t.Fatalf("建卡响应缺 id：%q", out)
	}

	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for taskID, lastType := range states {
		if err := st.LinkTask(created.ID, "linux-01", taskID, ledger.PurposeImplement, "test"); err != nil {
			t.Fatalf("挂账 %s: %v", taskID, err)
		}
		if lastType == "" {
			continue
		}
		if _, err := st.AppendMirroredEvent(created.ID, ledger.MirroredEvent{
			Target: "linux-01", Task: taskID, SourceSeq: 1, Type: lastType,
			Payload: []byte(`{}`), CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("镜像 %s: %v", taskID, err)
		}
	}
	return dir, created.ID
}

func acceptanceCommentBodies(t *testing.T, st *ledger.Store, cardID string) []string {
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
