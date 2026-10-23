package agentd_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestTaskDiscoveryHTTPIsExactBoundedRoute(t *testing.T) {
	env := newTestEnv(t)
	for _, task := range []proto.Task{
		{ID: "discovery-a", RepoPath: "/repo", State: proto.TaskStateRunning, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "discovery-b", RepoPath: "/repo", OriginSession: "session-b", State: proto.TaskStateCompleted, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	} {
		if err := env.st.CreateTask(&task); err != nil {
			t.Fatal(err)
		}
	}
	resp := env.get(t, "/api/tasks/discovery?after_seq=0&limit=1")
	var page proto.TaskDiscoveryPage
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("GET discovery page status=%d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(page.Items) != 1 || page.Items[0].TaskID != "discovery-a" || page.Items[0].Seq != 1 || !page.HasMore || page.NextSeq != 1 {
		t.Fatalf("first source discovery page=%+v", page)
	}
	if resp := env.get(t, "/api/tasks/discovery?after_seq=-1&limit=1"); resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("negative after_seq status=%d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := env.get(t, "/api/tasks/discovery?after_seq=0&limit=101"); resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("oversize limit status=%d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	// The exact route must win over /api/tasks/{id}; malformed queries are 400, not 404.
	if resp := env.get(t, "/api/tasks/discovery?after_seq=0"); resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("route fell through to task detail: status=%d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}
