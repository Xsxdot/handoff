package agentd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewTicket0HandlersReturnUnwired(t *testing.T) {
	s := &Server{}
	handlers := []struct {
		name string
		h    http.HandlerFunc
	}{
		{"create", s.handlePreviewCreate},
		{"list", s.handlePreviewList},
		{"close", s.handlePreviewClose},
		{"open", s.handlePreviewOpen},
		{"ws", s.handlePreviewWS},
	}
	for _, tc := range handlers {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.h(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d, want %d", rec.Code, http.StatusServiceUnavailable)
			}
			if !strings.Contains(rec.Body.String(), previewUnwired) {
				t.Fatalf("body=%q, want %q", rec.Body.String(), previewUnwired)
			}
		})
	}
}
