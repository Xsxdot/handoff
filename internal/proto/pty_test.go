package proto

import (
	"encoding/json"
	"testing"
)

func TestPtyControlMarshalsZeroBacklogBytes(t *testing.T) {
	data, err := json.Marshal(PtyControl{Type: PtyCtrlAttached})
	if err != nil {
		t.Fatalf("marshal attached control: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal attached control: %v", err)
	}
	value, ok := fields["backlog_bytes"]
	if !ok {
		t.Fatal("attached control must include backlog_bytes when zero")
	}
	var got uint64
	if err := json.Unmarshal(value, &got); err != nil {
		t.Fatalf("decode backlog_bytes: %v", err)
	}
	if got != 0 {
		t.Fatalf("backlog_bytes = %d, want 0", got)
	}
}
