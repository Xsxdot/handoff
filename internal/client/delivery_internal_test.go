package client

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestIsDeliverableAliasesWaitDeliveryPolicy(t *testing.T) {
	types := []proto.EventType{
		proto.EventTypeQuestion, proto.EventTypeProgress,
		proto.EventTypePermissionReuse, proto.EventTypeCompleted,
		proto.EventTypeTicketsVoided, proto.EventTypeStalled,
	}
	for _, tpe := range types {
		if isDeliverable(tpe) != WaitDeliveryPolicy(tpe) {
			t.Fatalf("isDeliverable(%s) 必须与 WaitDeliveryPolicy 同值", tpe)
		}
	}
}
