package agentd

// b453_inbox_notice_test.go —— B453 Q7 白名单（roomsapi）：分支任务撞额度停在 waiting_review 的
// notice 工单上浮收件箱，标题为告知；waiting_review 上的非 notice 工单不上浮（旧口径不变）。
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB453InboxSurfacesNoticeOnWaitingReview(t *testing.T) {
	env := newRoomsEnv(t)
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingReview})
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "tk-n", TaskID: "t1", Kind: proto.TicketKindNotice,
		Request: json.RawMessage(`{"kind":"notice","notice":"撞额度"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, env.st, &proto.Task{ID: "t2", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingReview})
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: "t2:p1", TaskID: "t2", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var notice, gate *proto.InboxItem
	items := inboxItems(t, env)
	for i := range items {
		switch items[i].RefID {
		case "tk-n":
			notice = &items[i]
		case "t2:p1":
			gate = &items[i]
		}
	}
	if notice == nil || notice.Origin != proto.InboxOriginTicket || notice.Title != "告知工单待确认" {
		t.Fatalf("waiting_review 的 notice 应上浮且标题为告知，得 %+v（全部 %+v）", notice, items)
	}
	if gate != nil {
		t.Fatalf("waiting_review 上的非 notice 工单不上浮（旧口径），得 %+v", gate)
	}
}
