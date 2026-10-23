package proto

import (
	"encoding/json"
	"testing"
)

func TestRetryTaskTicketNotificationResponseWire(t *testing.T) {
	want := RetryTaskTicketNotificationResponse{Notification: TicketNotification{
		Status: "needs_human", Reason: "resident_missing", OwnershipVersion: 3,
	}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"notification":{"status":"needs_human","reason":"resident_missing","ownership_version":3}}` {
		t.Fatalf("retry response wire=%s", raw)
	}
	var got RetryTaskTicketNotificationResponse
	if err := json.Unmarshal(raw, &got); err != nil || got.Notification != want.Notification {
		t.Fatalf("retry response roundtrip=%+v err=%v", got, err)
	}
}
