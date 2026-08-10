// desktopapi Workspace 资源无状态转换器。
//
// 职责：
//   - 把 workspaceapi owner result 转换为公开 DTO
//   - 把公开 write/search/PTY/Preview request 转换为 owner command
//   - 把 owner-side ResourceProblem 转为稳定 desktop Problem
//
// 边界：
//   - 纯字段与枚举转换，无授权、DB、网络或文件 I/O
//   - 不把 Workspace owner root、endpoint 或 token 放进公开 DTO
package desktopapi

import (
	"fmt"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

// ResourceAssembler 是可并发复用的无状态资源转换器。
type ResourceAssembler struct{}

// ToFileEntry 把 owner 目录项转换为公开 DTO。
func (a *ResourceAssembler) ToFileEntry(entry workspaceapi.FileEntry) FileEntryDTO {
	return FileEntryDTO{WorkspaceID: entry.WorkspaceID, Path: entry.Path, Name: entry.Name,
		Kind: string(entry.Kind), Size: entry.Size, ModifiedAt: entry.ModifiedAt, Version: entry.Version}
}

// ToFileEntries 把 owner 目录项集合转换为非 nil DTO 数组。
func (a *ResourceAssembler) ToFileEntries(entries []workspaceapi.FileEntry) []FileEntryDTO {
	out := make([]FileEntryDTO, 0, len(entries))
	for _, entry := range entries {
		out = append(out, a.ToFileEntry(entry))
	}
	return out
}

// ToFileDocument 把 owner 文件快照转换为公开 DTO。
func (a *ResourceAssembler) ToFileDocument(doc workspaceapi.FileDocument) FileDocumentDTO {
	return FileDocumentDTO{WorkspaceID: doc.WorkspaceID, Path: doc.Path, Version: doc.Version,
		ContentBase64: doc.ContentBase64, Size: doc.Size, ModifiedAt: doc.ModifiedAt}
}

// ToWriteFileCommand 把公开写请求绑定到已由路由解析的 Workspace ID。
func (a *ResourceAssembler) ToWriteFileCommand(workspaceID string, req WriteFileRequest) (workspaceapi.WriteFileCommand, error) {
	if workspaceID == "" || req.CommandID == "" || req.Path == "" {
		return workspaceapi.WriteFileCommand{}, fmt.Errorf("workspace_id、command_id 和 path 不能为空")
	}
	return workspaceapi.WriteFileCommand{WorkspaceID: workspaceID, CommandID: req.CommandID,
		Path: req.Path, IfMatch: req.IfMatch, ContentBase64: req.ContentBase64, CreateOnly: req.CreateOnly}, nil
}

// ToSearchFilesCommand 把公开搜索请求绑定到 Workspace ID。
func (a *ResourceAssembler) ToSearchFilesCommand(workspaceID string, req SearchFilesRequest) workspaceapi.SearchFilesCommand {
	return workspaceapi.SearchFilesCommand{WorkspaceID: workspaceID, Query: req.Query, Path: req.Path, MaxResults: req.MaxResults}
}

// ToFileSearchResult 把 owner 搜索结果转换为非 nil DTO 数组。
func (a *ResourceAssembler) ToFileSearchResult(result workspaceapi.FileSearchResult) FileSearchResultDTO {
	matches := make([]FileSearchMatchDTO, 0, len(result.Matches))
	for _, match := range result.Matches {
		matches = append(matches, FileSearchMatchDTO{Path: match.Path, Line: match.Line, Column: match.Column, Preview: match.Preview})
	}
	return FileSearchResultDTO{WorkspaceID: result.WorkspaceID, Matches: matches, Truncated: result.Truncated,
		ScannedFiles: result.ScannedFiles, ScannedBytes: result.ScannedBytes}
}

// ToGitStatus 把 owner Git 状态转换为公开 DTO。
func (a *ResourceAssembler) ToGitStatus(status workspaceapi.GitStatusSnapshot) GitStatusSnapshotDTO {
	entries := make([]GitStatusEntryDTO, 0, len(status.Entries))
	for _, entry := range status.Entries {
		entries = append(entries, GitStatusEntryDTO{Path: entry.Path, OriginalPath: entry.OriginalPath,
			IndexStatus: entry.IndexStatus, WorktreeStatus: entry.WorktreeStatus})
	}
	return GitStatusSnapshotDTO{WorkspaceID: status.WorkspaceID, Branch: status.Branch, HeadOID: status.HeadOID,
		Upstream: status.Upstream, Ahead: status.Ahead, Behind: status.Behind, Entries: entries}
}

// ToPtySession 把 owner PTY session 转换为公开 DTO。
func (a *ResourceAssembler) ToPtySession(session workspaceapi.PtySession) PtySessionDTO {
	return PtySessionDTO{TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation,
		WorkspaceID: session.WorkspaceID, State: string(session.State), Shell: session.Shell,
		ThroughSeq: session.ThroughSeq, ExitCode: session.ExitCode}
}

// ToPtyServerFrame 把 owner PTY frame 转换为公开 DTO，并保留稳定并发身份。
func (a *ResourceAssembler) ToPtyServerFrame(frame workspaceapi.PtyServerFrame) PtyServerFrameDTO {
	dto := PtyServerFrameDTO{Version: frame.Version, Kind: string(frame.Kind), TerminalSessionID: frame.TerminalSessionID,
		Incarnation: frame.Incarnation, Seq: frame.Seq, ThroughSeq: frame.ThroughSeq,
		DataBase64: frame.DataBase64, State: string(frame.State), ExitCode: frame.ExitCode}
	if frame.Problem != nil {
		dto.Problem = &Problem{Code: ProblemCode(frame.Problem.Code), Message: frame.Problem.Message, Retryable: frame.Problem.Retryable}
	}
	return dto
}

// ToCreateTerminalCommand 把公开请求绑定到 Workspace ID。
func (a *ResourceAssembler) ToCreateTerminalCommand(workspaceID string, req CreateTerminalRequest) workspaceapi.CreateTerminalCommand {
	return workspaceapi.CreateTerminalCommand{WorkspaceID: workspaceID, CommandID: req.CommandID, Cols: req.Cols, Rows: req.Rows}
}

// ToCreatePreviewCommand 把公开请求绑定到 Workspace ID。
func (a *ResourceAssembler) ToCreatePreviewCommand(workspaceID string, req CreatePreviewRequest) workspaceapi.CreatePreviewCommand {
	return workspaceapi.CreatePreviewCommand{WorkspaceID: workspaceID, CommandID: req.CommandID, Port: req.Port}
}

// ToPreviewSession 把 owner Preview session 转换为公开 DTO。
func (a *ResourceAssembler) ToPreviewSession(session workspaceapi.PreviewSession) PreviewSessionDTO {
	return PreviewSessionDTO{PreviewSessionID: session.PreviewSessionID, WorkspaceID: session.WorkspaceID,
		MachineID: session.MachineID, State: string(session.State), URL: session.URL,
		Port: session.Port, ExpiresAt: session.ExpiresAt}
}
