// desktopapi ResourceAssembler 纯转换测试。
//
// 职责：
//   - 锁定 workspaceapi owner result 到公开 DTO 的字段映射
//   - 锁定 write/search command、base64、PTY seq/incarnation 与 Problem 映射
//
// 边界：
//   - 纯转换测试，不执行授权、网络、文件或 PTY I/O
package desktopapi

import (
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestResourceAssemblerMapsFileCommandsAndResults(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	a := &ResourceAssembler{}
	doc := a.ToFileDocument(workspaceapi.FileDocument{
		WorkspaceID: "ws1", Path: "README.md", Version: "sha256:abc",
		ContentBase64: "SGVsbG8=", Size: 5, ModifiedAt: now,
	})
	if doc.WorkspaceID != "ws1" || doc.Version != "sha256:abc" || doc.ContentBase64 != "SGVsbG8=" {
		t.Fatalf("file document dto = %+v", doc)
	}
	command, err := a.ToWriteFileCommand("ws1", WriteFileRequest{
		CommandID: "cmd1", Path: "README.md", IfMatch: "sha256:abc", ContentBase64: "V29ybGQ=",
	})
	if err != nil {
		t.Fatalf("ToWriteFileCommand: %v", err)
	}
	if command.WorkspaceID != "ws1" || command.IfMatch != "sha256:abc" || command.ContentBase64 != "V29ybGQ=" {
		t.Fatalf("write command = %+v", command)
	}
}

func TestResourceAssemblerMapsPtyIdentityAndProblem(t *testing.T) {
	a := &ResourceAssembler{}
	frame := a.ToPtyServerFrame(workspaceapi.PtyServerFrame{
		Version: 1, Kind: workspaceapi.PtyFrameProblem,
		TerminalSessionID: "term1", Incarnation: "inc1", Seq: 9, ThroughSeq: 9,
		Problem: &workspaceapi.ResourceProblem{Code: "CURSOR_EXPIRED", Message: "游标已过期", Retryable: true},
	})
	if frame.Version != 1 || frame.Incarnation != "inc1" || frame.Seq != 9 || frame.Problem == nil {
		t.Fatalf("pty frame dto = %+v", frame)
	}
	if frame.Problem.Code != ProblemCursorExpired || !frame.Problem.Retryable {
		t.Fatalf("problem dto = %+v", frame.Problem)
	}
}

func TestResourceAssemblerMapsEmptyCollectionsAndPreview(t *testing.T) {
	a := &ResourceAssembler{}
	search := a.ToFileSearchResult(workspaceapi.FileSearchResult{WorkspaceID: "ws1"})
	if search.Matches == nil || len(search.Matches) != 0 {
		t.Fatalf("empty matches 应编码为 []: %+v", search.Matches)
	}
	preview := a.ToPreviewSession(workspaceapi.PreviewSession{
		PreviewSessionID: "preview1", WorkspaceID: "ws1", MachineID: "m1",
		State: workspaceapi.PreviewStateActive, URL: "http://127.0.0.1:7777/v1/preview-proxy/redacted/", Port: 3000,
	})
	if preview.PreviewSessionID != "preview1" || preview.State != "active" || preview.Port != 3000 {
		t.Fatalf("preview dto = %+v", preview)
	}
}
