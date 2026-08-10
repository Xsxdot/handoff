// desktopapi Workspace 资源 wire DTO。
//
// 职责：
//   - 定义文件、Git、PTY 与 Preview 的公开 JSON 线格式
//   - 定义 renderer 可提交的窄 command 请求，不接收 endpoint/token/绝对 owner root
//
// 边界：
//   - DTO 只承载 Workspace/resource ID 与 relative path
//   - 不执行授权、业务校验或 I/O；转换由 ResourceAssembler 完成
package desktopapi

import "time"

// FileEntryDTO 是公开目录项。
type FileEntryDTO struct {
	WorkspaceID string    `json:"workspace_id"`
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Size        int64     `json:"size"`
	ModifiedAt  time.Time `json:"modified_at"`
	Version     string    `json:"version,omitempty"`
}

// FileDocumentDTO 是带乐观锁 version 的公开文件快照。
type FileDocumentDTO struct {
	WorkspaceID   string    `json:"workspace_id"`
	Path          string    `json:"path"`
	Version       string    `json:"version"`
	ContentBase64 string    `json:"content_base64"`
	Size          int64     `json:"size"`
	ModifiedAt    time.Time `json:"modified_at"`
}

// WriteFileRequest 是文件原子写请求。
type WriteFileRequest struct {
	CommandID     string `json:"command_id"`
	Path          string `json:"path"`
	IfMatch       string `json:"if_match"`
	ContentBase64 string `json:"content_base64"`
	CreateOnly    bool   `json:"create_only"`
}

// SearchFilesRequest 是有界 literal 搜索请求。
type SearchFilesRequest struct {
	Query      string `json:"query"`
	Path       string `json:"path"`
	MaxResults int    `json:"max_results"`
}

// FileSearchMatchDTO 是一条公开搜索命中。
type FileSearchMatchDTO struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Preview string `json:"preview"`
}

// FileSearchResultDTO 是公开搜索结果与扫描摘要。
type FileSearchResultDTO struct {
	WorkspaceID  string               `json:"workspace_id"`
	Matches      []FileSearchMatchDTO `json:"matches"`
	Truncated    bool                 `json:"truncated"`
	ScannedFiles int                  `json:"scanned_files"`
	ScannedBytes int64                `json:"scanned_bytes"`
}

// FileEventDTO 是不含内容的文件失效提示。
type FileEventDTO struct {
	WorkspaceID string    `json:"workspace_id"`
	Seq         int64     `json:"seq"`
	Kind        string    `json:"kind"`
	Path        string    `json:"path"`
	ObservedAt  time.Time `json:"observed_at"`
}

// FileStreamFrameDTO 是文件事件 WebSocket 的版本化服务端 frame。
type FileStreamFrameDTO struct {
	Version     int            `json:"version"`
	Kind        string         `json:"kind"`
	WorkspaceID string         `json:"workspace_id"`
	ThroughSeq  int64          `json:"through_seq"`
	Replay      []FileEventDTO `json:"replay,omitempty"`
	Event       *FileEventDTO  `json:"event,omitempty"`
	Problem     *Problem       `json:"problem,omitempty"`
}

// GitStatusEntryDTO 是公开 Git 文件状态。
type GitStatusEntryDTO struct {
	Path           string `json:"path"`
	OriginalPath   string `json:"original_path,omitempty"`
	IndexStatus    string `json:"index_status"`
	WorktreeStatus string `json:"worktree_status"`
}

// GitStatusSnapshotDTO 是公开 Workspace Git 基础状态。
type GitStatusSnapshotDTO struct {
	WorkspaceID string              `json:"workspace_id"`
	Branch      string              `json:"branch,omitempty"`
	HeadOID     string              `json:"head_oid,omitempty"`
	Upstream    string              `json:"upstream,omitempty"`
	Ahead       int                 `json:"ahead"`
	Behind      int                 `json:"behind"`
	Entries     []GitStatusEntryDTO `json:"entries"`
}

// CreateTerminalRequest 是普通 PTY 创建请求。
type CreateTerminalRequest struct {
	CommandID string `json:"command_id"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// PtySessionDTO 是公开普通终端会话元数据。
type PtySessionDTO struct {
	TerminalSessionID string `json:"terminal_session_id"`
	Incarnation       string `json:"incarnation"`
	WorkspaceID       string `json:"workspace_id"`
	State             string `json:"state"`
	Shell             string `json:"shell"`
	ThroughSeq        int64  `json:"through_seq"`
	ExitCode          *int   `json:"exit_code"`
}

// PtyServerFrameDTO 是公开版本化 PTY 服务端 frame。
type PtyServerFrameDTO struct {
	Version           int      `json:"version"`
	Kind              string   `json:"kind"`
	TerminalSessionID string   `json:"terminal_session_id"`
	Incarnation       string   `json:"incarnation"`
	Seq               int64    `json:"seq"`
	ThroughSeq        int64    `json:"through_seq"`
	DataBase64        string   `json:"data_base64,omitempty"`
	State             string   `json:"state,omitempty"`
	ExitCode          *int     `json:"exit_code,omitempty"`
	Problem           *Problem `json:"problem,omitempty"`
}

// CreatePreviewRequest 是 owner-loopback Preview 创建请求。
type CreatePreviewRequest struct {
	CommandID string `json:"command_id"`
	Port      int    `json:"port"`
}

// PreviewSessionDTO 是公开 Preview session；URL 始终指向本机 agentd loopback。
type PreviewSessionDTO struct {
	PreviewSessionID string    `json:"preview_session_id"`
	WorkspaceID      string    `json:"workspace_id"`
	MachineID        string    `json:"machine_id"`
	State            string    `json:"state"`
	URL              string    `json:"url"`
	Port             int       `json:"port"`
	ExpiresAt        time.Time `json:"expires_at"`
}
