package cmd

// b460_pending_ticket_test.go —— B460 S1/S2/S3 V2：dispatch --wait 与 wait --final 遇待答工单
// 以退出码 5 退出，stdout 为 F2 工单块（全文取工单请求体、不截断），stderr 不再打「在等人
// 回答」提示；打印的 reply 行可被 handoff reply 原样解析并打到正确端点。
import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

func b460Ticket(id string, req map[string]string) proto.Ticket {
	b, _ := json.Marshal(req)
	return proto.Ticket{ID: id, TaskID: "t1", Kind: req["kind"], Request: b}
}

func b460PendingAgentd(t *testing.T, tickets ...proto.Ticket) *b452Agentd {
	return newB452Agentd(t, &b452Agentd{attach: func() client.AttachInfo {
		return client.AttachInfo{
			Task:           proto.TaskView{Task: proto.Task{ID: "t1", State: proto.TaskStateWaitingAnswer, Target: "linux-01"}},
			PendingTickets: tickets,
			RecentEvents:   []proto.Event{{Seq: 7, TaskID: "t1", Type: proto.EventTypePermissionRequest, Payload: json.RawMessage(`{}`)}},
		}
	}})
}

func TestB460DispatchWaitPermissionTicketExit5(t *testing.T) {
	long := "Bash: stat " + strings.Repeat("a", 320) + " END-MARK-B460"
	a := b460PendingAgentd(t, b460Ticket("t1:p1", map[string]string{"kind": "gate", "permission": long}))
	stdout, stderr, err := runB452CLI(t, a, t.TempDir(), "dispatch", "--prompt", "x", "--project", "p",
		"--wait", "--no-terminal")
	if ExitCode(err) != ExitPendingTicket || ExitPendingTicket != 5 {
		t.Fatalf("应退出码 5，得 %d err=%v stderr=%s", ExitCode(err), err, stderr)
	}
	want := "ticket: t1:p1\nkind: permission\n--- 全文 ---\n" + long + "\n--- 全文完 ---\n" +
		"reply: handoff reply t1 --ticket t1:p1 --approve\n" +
		"reply: handoff reply t1 --ticket t1:p1 --deny --reason \"<原因>\"\n" +
		"\nresume: handoff wait t1 --final --machine linux-01\n"
	if stdout != want {
		t.Fatalf("stdout 应全等 F2:\n got %q\nwant %q", stdout, want)
	}
	if strings.Contains(stderr, "在等人回答") {
		t.Fatalf("stderr 不得再打等人回答提示: %s", stderr)
	}
}

func TestB460WaitFinalQuestionAndReasonAndTwoTickets(t *testing.T) {
	a := b460PendingAgentd(t,
		b460Ticket("t1:p1", map[string]string{"kind": "gate", "permission": "Bash: ls /etc", "reason": "审批模型未形成允许"}),
		b460Ticket("t1:q1", map[string]string{"kind": "ask", "question": "要不要继续？\n第二行"}))
	stdout, _, err := runB452CLI(t, a, t.TempDir(), "wait", "t1", "--final")
	if ExitCode(err) != ExitPendingTicket {
		t.Fatalf("应退出码 5，得 %d %v", ExitCode(err), err)
	}
	want := "ticket: t1:p1\nkind: permission\nreason: 审批模型未形成允许\n--- 全文 ---\nBash: ls /etc\n--- 全文完 ---\n" +
		"reply: handoff reply t1 --ticket t1:p1 --approve\n" +
		"reply: handoff reply t1 --ticket t1:p1 --deny --reason \"<原因>\"\n" +
		"\n" +
		"ticket: t1:q1\nkind: question\n--- 全文 ---\n要不要继续？\n第二行\n--- 全文完 ---\n" +
		"reply: handoff reply t1 --ticket t1:q1 --answer \"<回答>\"\n" +
		"\nresume: handoff wait t1 --final\n"
	if stdout != want {
		t.Fatalf("stdout 应全等 F2:\n got %q\nwant %q", stdout, want)
	}
}

// 打印的 reply: 行原样喂给 handoff reply，请求打到 POST /api/tasks/t1/reply 且带正确工单与答案。
func TestB460PrintedReplyLineParses(t *testing.T) {
	a := b460PendingAgentd(t, b460Ticket("t1:p1", map[string]string{"kind": "gate", "permission": "Bash: ls"}))
	stdout, _, err := runB452CLI(t, a, t.TempDir(), "wait", "t1", "--final")
	if ExitCode(err) != ExitPendingTicket {
		t.Fatalf("应退出码 5: %v", err)
	}
	var line string
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasSuffix(l, "--approve") {
			line = l
		}
	}
	args := strings.Fields(strings.TrimPrefix(line, "reply: handoff "))
	var mu sync.Mutex
	var got map[string]string
	b := newB452Agentd(t, &b452Agentd{fallback: func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/tasks/t1/reply" {
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&got)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}})
	if _, stderr, err := runB452CLI(t, b, t.TempDir(), args...); err != nil {
		t.Fatalf("reply 行 %q 应可执行: %v %s", line, err, stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["ticket_id"] != "t1:p1" || got["answer"] != "allow" {
		t.Fatalf("reply 请求体 = %v", got)
	}
}
