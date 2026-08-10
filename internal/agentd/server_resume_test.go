package agentd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleResumeParsesForce 验 force 查询参数被正确透传给 manager。
func TestHandleResumeParsesForce(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"", false},
		{"?force=true", true},
		{"?force=1", true},
		{"?force=false", false},
	} {
		got := parseForce(httptest.NewRequest(http.MethodPost,
			"/api/tasks/t1/resume"+tc.query, nil))
		if got != tc.want {
			t.Fatalf("query %q: force want %v got %v", tc.query, tc.want, got)
		}
	}
}
