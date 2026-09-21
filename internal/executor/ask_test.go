package executor_test

import (
	"context"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

type fakeAskResponder struct{}

func (fakeAskResponder) RespondAsk(context.Context, executor.RespondAskReq) error {
	return nil
}

var _ executor.AskResponder = fakeAskResponder{}

func TestNamespacedTicketIDGoldenVectors(t *testing.T) {
	tests := []struct {
		name     string
		taskID   string
		nativeID string
		want     string
	}{
		{name: "permission id", taskID: "T1", nativeID: "perm-1", want: "T1:perm-1"},
		{name: "native id contains colon", taskID: "T1", nativeID: "a:b", want: "T1:a:b"},
		{name: "question id", taskID: "137a7dc9-df89-4c1c-891e-ebe106c68b37", nativeID: "que_xxx", want: "137a7dc9-df89-4c1c-891e-ebe106c68b37:que_xxx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := executor.NamespacedTicketID(tt.taskID, tt.nativeID)
			if got != tt.want {
				t.Fatalf("NamespacedTicketID(%q, %q) = %q, want %q", tt.taskID, tt.nativeID, got, tt.want)
			}
			if native := executor.NativeIDFromTicket(tt.taskID, got); native != tt.nativeID {
				t.Fatalf("NativeIDFromTicket(%q, %q) = %q, want %q", tt.taskID, got, native, tt.nativeID)
			}
		})
	}
}
