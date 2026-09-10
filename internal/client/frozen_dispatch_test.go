package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDispatchFrozenIdentityJSONGolden 锁定 B233.10 Ticket 0 的 wire 字段名与
// HomeDir 三态：冻结身份通过已有 POST /api/tasks 发送，空普通派发不凭空带 carrier/squad。
func TestDispatchFrozenIdentityJSONGolden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/tasks" {
			t.Fatalf("request = %s %s, want POST /api/tasks", r.Method, r.URL.Path)
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		want := map[string]any{
			"project_id": "p1",
			"prompt":     "run",
			"target":     "linux-01",
			"executor":   "opencode",
			"model":      "fast",
			"receiver":   "workers",
			"carrier":    "muse",
			"squad":      "workers",
			"home_dir":   "~/.handoff/home/muse",
		}
		for key, value := range want {
			if got[key] != value {
				t.Errorf("body[%q] = %#v, want %#v", key, got[key], value)
			}
		}
		if _, ok := got["carrier"]; !ok {
			t.Error("frozen carrier field missing")
		}
		if _, ok := got["squad"]; !ok {
			t.Error("frozen squad field missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-1","state":"running"}`))
	}))
	defer server.Close()

	home := "~/.handoff/home/muse"
	got, err := New(server.URL, "").Dispatch(context.Background(), DispatchOpts{
		ProjectID: "p1", Prompt: "run", Target: "linux-01", Executor: "opencode",
		Model: "fast", Receiver: "workers", Carrier: "muse", Squad: "workers", HomeDir: &home,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got.ID != "task-1" {
		t.Fatalf("task id = %q, want task-1", got.ID)
	}
}

func TestDispatchFrozenIdentityJSONOmissionAndHomeDirTriState(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request %d: %v", calls, err)
		}
		if _, ok := got["carrier"]; ok {
			t.Errorf("request %d unexpectedly contains carrier", calls)
		}
		if _, ok := got["squad"]; ok {
			t.Errorf("request %d unexpectedly contains squad", calls)
		}
		_, hasHomeDir := got["home_dir"]
		if calls == 1 && hasHomeDir {
			t.Error("nil HomeDir unexpectedly contains home_dir")
		}
		if calls == 2 {
			value, ok := got["home_dir"]
			if !ok || value != "" {
				t.Errorf("explicit empty HomeDir = %#v, want present empty string", value)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-1","state":"running"}`))
	}))
	defer server.Close()

	c := New(server.URL, "")
	if _, err := c.Dispatch(context.Background(), DispatchOpts{Prompt: "run"}); err != nil {
		t.Fatalf("Dispatch nil HomeDir: %v", err)
	}
	home := ""
	if _, err := c.Dispatch(context.Background(), DispatchOpts{Prompt: "run", HomeDir: &home}); err != nil {
		t.Fatalf("Dispatch explicit empty HomeDir: %v", err)
	}
	if calls != 2 {
		t.Fatalf("dispatch calls = %d, want 2", calls)
	}
}
