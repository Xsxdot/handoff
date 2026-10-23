package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
)

func TestDiscoverTasksUsesExactBoundedWire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/tasks/discovery" || r.URL.Query().Get("after_seq") != "7" || r.URL.Query().Get("limit") != "12" {
			t.Errorf("discovery request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"items":[{"seq":8,"task_id":"t8","origin_session":"s8","state":"running","has_pending_tickets":false}],"next_seq":8,"has_more":false}`))
	}))
	defer server.Close()
	page, err := client.New(server.URL, "token").DiscoverTasks(context.Background(), 7, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Seq != 8 || page.Items[0].TaskID != "t8" || page.NextSeq != 8 || page.HasMore {
		t.Fatalf("discovery response=%+v", page)
	}
}
