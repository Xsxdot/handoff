package store

// b460_answered_ticket_test.go —— B460 Q3 甲 / V3：「建单即带答案」写入。审批链批准的工单
// 从不出现在 PendingTickets（answer IS NULL）里——等待命令以待答列表为「要人答」的判据。
import (
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB460CreateAnsweredTicket(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: "T1", RepoPath: "/r", State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	tk := &proto.Ticket{ID: "T1:p1", TaskID: "T1", Kind: "gate",
		Request: []byte(`{"kind":"gate","permission":"Bash: ls"}`), CreatedAt: now}
	created, err := st.CreateAnsweredTicket(tk, "allow")
	if err != nil || !created {
		t.Fatalf("首次应 created=true：%v %v", created, err)
	}
	pend, err := st.PendingTickets("T1")
	if err != nil || len(pend) != 0 {
		t.Fatalf("已答工单不得进待答列表：%+v %v", pend, err)
	}
	got, err := st.GetTicket("T1:p1")
	if err != nil || got.Answer == nil || *got.Answer != "allow" || got.AnsweredAt == nil {
		t.Fatalf("工单应已答 allow：%+v %v", got, err)
	}
	created, err = st.CreateAnsweredTicket(tk, "allow")
	if err != nil || created {
		t.Fatalf("同 id 同答案重复调用应幂等 created=false：%v %v", created, err)
	}
	if _, err := st.CreateAnsweredTicket(tk, "reject"); !errors.Is(err, ErrTicketConflict) {
		t.Fatalf("同 id 不同答案应 ErrTicketConflict，得 %v", err)
	}
	if got, _ := st.GetTicket("T1:p1"); *got.Answer != "allow" {
		t.Fatalf("重复调用不得改答案，得 %q", *got.Answer)
	}

	// 既有未答工单（升级路径崩溃留下的半截状态）：与旧的「建单 + AnswerTicket」同语义——补答
	pending := &proto.Ticket{ID: "T1:p2", TaskID: "T1", Kind: "gate", Request: []byte(`{}`), CreatedAt: now}
	if _, err := st.CreateTicket(pending); err != nil {
		t.Fatal(err)
	}
	created, err = st.CreateAnsweredTicket(pending, "allow")
	if err != nil || created {
		t.Fatalf("既有未答工单应被补答且 created=false：%v %v", created, err)
	}
	if pend, _ := st.PendingTickets("T1"); len(pend) != 0 {
		t.Fatalf("补答后不得再待答：%+v", pend)
	}
}
