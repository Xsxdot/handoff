package agentd

import (
	"net/http"
)

// previewUnwired is the Ticket 0 boundary. The route and wire shape are
// present so callers compile; owner persistence, tunnel projection and local
// Chromium launching belong to the implementation node.
const previewUnwired = "预览会话尚未接线"

func (s *Server) handlePreviewCreate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
}

func (s *Server) handlePreviewList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
}

func (s *Server) handlePreviewClose(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
}

func (s *Server) handlePreviewOpen(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
}

func (s *Server) handlePreviewWS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
}
