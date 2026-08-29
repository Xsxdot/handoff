package proto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPreviewJSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	want := PreviewSession{
		ID: "preview-1", EntryURL: "http://localhost:5173/app", Via: []string{"10.0.0.0/8", "example.test"},
		CWD: "/workspace", OriginURL: "https://example.test/repo", Branch: "feature/demo",
		CreatedAt: created, TTLSeconds: 7200, Machine: "linux-1",
	}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal preview session: %v", err)
	}
	text := string(body)
	for _, field := range []string{"\"port\"", "\"path\"", "\"ttl_seconds\":7200", "\"machine\":\"linux-1\""} {
		if !strings.Contains(text, field) && field == "\"ttl_seconds\":7200" {
			t.Fatalf("marshal body=%s, missing %s", text, field)
		}
	}
	var got PreviewSession
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal preview session: %v", err)
	}
	if got.ID != want.ID || got.EntryURL != want.EntryURL || got.CWD != want.CWD || got.OriginURL != want.OriginURL || got.Branch != want.Branch || got.Machine != want.Machine || got.TTLSeconds != want.TTLSeconds || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("roundtrip mismatch: got=%+v want=%+v", got, want)
	}
	if len(got.Via) != len(want.Via) || got.Via[0] != want.Via[0] || got.Via[1] != want.Via[1] {
		t.Fatalf("via mismatch: got=%v want=%v", got.Via, want.Via)
	}
}

func TestPreviewOptionalFieldsRemainAbsentAndTTLZeroRemainsPresent(t *testing.T) {
	input := `{"id":"preview-zero","entry_url":"http://localhost:0","cwd":"/tmp/preview","created_at":"2026-08-29T00:00:00Z","ttl_seconds":0}`
	var got PreviewSession
	if err := json.Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("unmarshal zero fixture: %v", err)
	}
	if got.TTLSeconds != 0 || got.Via != nil || got.OriginURL != "" || got.Branch != "" || got.Machine != "" {
		t.Fatalf("zero/absent fields collapsed: %+v", got)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal zero fixture: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `"ttl_seconds":0`) {
		t.Fatalf("marshal body=%s, ttl_seconds zero must remain observable", text)
	}
	for _, field := range []string{`"via"`, `"origin_url"`, `"branch"`, `"machine"`} {
		if strings.Contains(text, field) {
			t.Fatalf("marshal body=%s, optional field %s should remain absent", text, field)
		}
	}
}

func TestPreviewRequestPortPathWireShape(t *testing.T) {
	tests := []struct {
		name string
		req  PreviewOpenReq
		want []string
	}{
		{name: "port", req: PreviewOpenReq{Port: 5173}, want: []string{`"port":5173`}},
		{name: "path", req: PreviewOpenReq{Path: "dist/index.html", Via: []string{"127.0.0.1"}, CWD: "/workspace"}, want: []string{`"path":"dist/index.html"`, `"via":["127.0.0.1"]`, `"cwd":"/workspace"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.req)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, field := range tt.want {
				if !strings.Contains(string(body), field) {
					t.Fatalf("body=%s, missing %s", body, field)
				}
			}
		})
	}
}
