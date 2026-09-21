// ask_test.go —— B233.21 提问面权威（工单身份与撞单处置）的缝级测试。
//
// ask 无政策判断，权威只管两件决策事实：工单身份派生（原生提问 id 命名空间化，
// 无原生 id 退 uuid）与撞单处置（未答重放→复用重挂；已答重发→另开新单；
// 重读失败→跳过）。
package approval_test

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/approval"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

func TestAskTicketIDNamespacesNativeQuestionID(t *testing.T) {
	got := approval.AskTicketID("T1", "q-1")
	if want := executor.NamespacedTicketID("T1", "q-1"); got != want || got != "T1:q-1" {
		t.Fatalf("AskTicketID = %q, want %q（taskID:questionID 与 gate 工单同构）", got, want)
	}
}

func TestAskTicketIDFallsBackToUUID(t *testing.T) {
	a := approval.AskTicketID("T1", "")
	b := approval.AskTicketID("T1", "")
	if a == "" || a == b {
		t.Fatalf("无原生 id 时应各发 fresh uuid，实得 %q / %q", a, b)
	}
	if strings.Contains(a, ":") {
		t.Fatalf("uuid 身份不应带命名空间分隔: %q", a)
	}
}

func TestClassifyAskDuplicate(t *testing.T) {
	answered := "allow"
	cases := []struct {
		name  string
		prior *proto.Ticket
		err   error
		want  approval.AskDuplicate
	}{
		{"重读失败→跳过", nil, store.ErrNotFound, approval.AskDuplicateSkip},
		{"未答重放→复用重挂", &proto.Ticket{ID: "T1:q1"}, nil, approval.AskDuplicateWait},
		{"已答重发→另开新单", &proto.Ticket{ID: "T1:q1", Answer: &answered}, nil, approval.AskDuplicateReissue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := approval.ClassifyAskDuplicate(tc.prior, tc.err); got != tc.want {
				t.Fatalf("ClassifyAskDuplicate = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAskReissueTicketIDAlwaysFresh 锁「重发另起新身份」：与原生 questionID
// 无关，一律 fresh uuid（B58——复用已答工单 id 会让协调者再也答不了）。
func TestAskReissueTicketIDAlwaysFresh(t *testing.T) {
	a := approval.AskReissueTicketID()
	b := approval.AskReissueTicketID()
	if a == "" || a == b {
		t.Fatalf("重发身份必须是 fresh uuid，实得 %q / %q", a, b)
	}
}
