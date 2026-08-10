// workspaceapi 资源契约序列化测试。
//
// 职责：
//   - 锁定文件、Git、PTY、Preview provider 契约的 snake_case JSON 形态
//   - 锁定 PTY session/incarnation/seq 与文件 version 等并发身份字段
//
// 边界：
//   - 只验证 owner-side 共享契约，不测试 HTTP adapter 或具体 I/O
package workspaceapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResourceContractsCarryStableIdentityAndVersions(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	values := []struct {
		name  string
		value any
		keys  []string
	}{
		{"file entry", FileEntry{WorkspaceID: "ws1", Path: "internal", Name: "internal", Kind: FileKindDirectory, ModifiedAt: now}, []string{`"workspace_id"`, `"modified_at"`}},
		{"file document", FileDocument{WorkspaceID: "ws1", Path: "README.md", Version: "sha256:abc", ContentBase64: "SGVsbG8=", Size: 5, ModifiedAt: now}, []string{`"version":"sha256:abc"`, `"content_base64"`}},
		{"search result", FileSearchResult{WorkspaceID: "ws1", Matches: []FileSearchMatch{{Path: "README.md", Line: 2, Column: 3, Preview: "hello"}}, ScannedFiles: 1, ScannedBytes: 5}, []string{`"scanned_files":1`, `"scanned_bytes":5`}},
		{"git status", GitStatusSnapshot{WorkspaceID: "ws1", Branch: "main", HeadOID: "abc", Entries: []GitStatusEntry{{Path: "README.md", WorktreeStatus: "M"}}}, []string{`"head_oid":"abc"`, `"worktree_status":"M"`}},
		{"pty session", PtySession{TerminalSessionID: "term1", Incarnation: "inc1", WorkspaceID: "ws1", State: PtyStateActive, ThroughSeq: 7}, []string{`"terminal_session_id":"term1"`, `"incarnation":"inc1"`, `"through_seq":7`}},
		{"pty frame", PtyServerFrame{Version: 1, Kind: PtyFrameData, TerminalSessionID: "term1", Incarnation: "inc1", Seq: 8, DataBase64: "b2sK"}, []string{`"version":1`, `"seq":8`, `"data_base64":"b2sK"`}},
		{"preview", PreviewSession{PreviewSessionID: "preview1", WorkspaceID: "ws1", MachineID: "m1", State: PreviewStateActive, URL: "http://127.0.0.1:7777/v1/preview-proxy/redacted/", Port: 3000, ExpiresAt: now}, []string{`"preview_session_id":"preview1"`, `"expires_at"`}},
	}
	for _, tc := range values {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			for _, key := range tc.keys {
				if !strings.Contains(string(got), key) {
					t.Errorf("JSON %s 缺少 %s", got, key)
				}
			}
		})
	}
}
