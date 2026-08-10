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
	session := workspaceapi.PtySession{TerminalSessionID: "term1", Incarnation: "inc1", WorkspaceID: "ws1",
		State: workspaceapi.PtyStateActive, Shell: "/bin/zsh", ThroughSeq: 9}
	if roundTrip := a.FromPtySession(a.ToPtySession(session)); roundTrip != session {
		t.Fatalf("pty session round trip = %+v", roundTrip)
	}
	ownerFrame := workspaceapi.PtyServerFrame{
		Version: 1, Kind: workspaceapi.PtyFrameProblem,
		TerminalSessionID: "term1", Incarnation: "inc1", WorkspaceID: "ws1", Seq: 9, ThroughSeq: 9,
		Problem: &workspaceapi.ResourceProblem{Code: "CURSOR_EXPIRED", Message: "游标已过期", Retryable: true},
	}
	frame := a.ToPtyServerFrame(ownerFrame)
	if frame.Version != 1 || frame.Incarnation != "inc1" || frame.WorkspaceID != "ws1" || frame.Seq != 9 || frame.Problem == nil {
		t.Fatalf("pty frame dto = %+v", frame)
	}
	if frame.Problem.Code != ProblemCursorExpired || !frame.Problem.Retryable {
		t.Fatalf("problem dto = %+v", frame.Problem)
	}
	if roundTrip := a.FromPtyServerFrame(frame); roundTrip.Kind != ownerFrame.Kind ||
		roundTrip.Problem == nil || roundTrip.Problem.Code != "CURSOR_EXPIRED" {
		t.Fatalf("pty server frame round trip = %+v", roundTrip)
	}
	client := workspaceapi.PtyClientFrame{Version: 1, Kind: workspaceapi.PtyClientFrameResize,
		TerminalSessionID: "term1", Incarnation: "inc1", Cols: 120, Rows: 40, AckSeq: 8}
	if roundTrip := a.FromPtyClientFrame(a.ToPtyClientFrame(client)); roundTrip != client {
		t.Fatalf("pty client frame round trip = %+v", roundTrip)
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

func TestResourceAssemblerMapsFileEventReplay(t *testing.T) {
	a := &ResourceAssembler{}
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	dto := a.ToFileEvents([]workspaceapi.FileEvent{{
		WorkspaceID: "ws", Seq: 7, Kind: workspaceapi.FileEventModify, Path: "README.md", ObservedAt: now,
	}})
	if len(dto) != 1 || dto[0].Seq != 7 || dto[0].Kind != "modify" {
		t.Fatalf("file event dto = %+v", dto)
	}
	roundTrip := a.FromFileEvents(dto)
	if len(roundTrip) != 1 || roundTrip[0].Path != "README.md" || !roundTrip[0].ObservedAt.Equal(now) {
		t.Fatalf("file event round trip = %+v", roundTrip)
	}
}

func TestResourceAssemblerRoundTripsGitRepositoryMarker(t *testing.T) {
	a := &ResourceAssembler{}
	dto := a.ToGitStatus(workspaceapi.GitStatusSnapshot{
		WorkspaceID: "ws", IsRepository: true, Branch: "main", HeadOID: "abc",
		Entries: []workspaceapi.GitStatusEntry{{Path: "README.md", WorktreeStatus: "M"}},
	})
	if !dto.IsRepository || dto.Branch != "main" || len(dto.Entries) != 1 {
		t.Fatalf("git dto = %+v", dto)
	}
	status := a.FromGitStatus(dto)
	if !status.IsRepository || status.WorkspaceID != "ws" || status.Entries[0].Path != "README.md" {
		t.Fatalf("git round trip = %+v", status)
	}
}
