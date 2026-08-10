// Package workspaceapi 定义由 Workspace 所属机器执行的资源契约。
//
// 职责：
//   - 定义文件、Git、PTY 与 Preview 的 owner-side command/result 类型
//   - 定义 local machineauthority 与 remote peer authority 共用的窄接口
//   - 固化 workspace/resource 稳定 ID、文件 version 和 PTY incarnation/seq
//
// 边界：
//   - 不依赖 HTTP、SQLite、Electron 或具体本机/远端实现
//   - RootPath 只在 agentd 内部 owner 路由使用，不进入公开桌面 DTO
//   - 文件内容与终端字节只以 base64 字段经过资源通道，不进入控制面事件
package workspaceapi

import (
	"context"
	"fmt"
	"time"
)

// ErrorCode 是 owner authority 返回给 gateway 的稳定资源错误类别。
type ErrorCode string

const (
	ErrorResourceNotFound      ErrorCode = "RESOURCE_NOT_FOUND"
	ErrorPathOutsideWorkspace  ErrorCode = "PATH_OUTSIDE_WORKSPACE"
	ErrorVersionConflict       ErrorCode = "VERSION_CONFLICT"
	ErrorCommandConflict       ErrorCode = "COMMAND_CONFLICT"
	ErrorCursorExpired         ErrorCode = "CURSOR_EXPIRED"
	ErrorCapabilityUnsupported ErrorCode = "CAPABILITY_UNSUPPORTED"
	ErrorUnavailable           ErrorCode = "MACHINE_OFFLINE"
)

// Error 是 owner-side 可安全跨 gateway/peer 映射的 typed error。
//
// Message 只能包含可公开摘要；Cause 保留底层原因，仅供结构化服务日志使用。
type Error struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	Cause     error
}

// Error 返回稳定错误码与安全摘要，不拼接 Cause。
func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Unwrap 暴露底层原因给 errors.Is/As，wire adapter 不应直接编码它。
func (e *Error) Unwrap() error { return e.Cause }

// FileKind 表示目录项类型。
type FileKind string

const (
	FileKindFile      FileKind = "file"
	FileKindDirectory FileKind = "directory"
	FileKindSymlink   FileKind = "symlink"
)

// PtyState 表示普通终端会话的持久状态。
type PtyState string

const (
	PtyStateStarting PtyState = "starting"
	PtyStateActive   PtyState = "active"
	PtyStateEnded    PtyState = "ended"
)

// PtyFrameKind 表示服务端 PTY frame 类型。
type PtyFrameKind string

const (
	PtyFrameSubscribed PtyFrameKind = "subscribed"
	PtyFrameSnapshot   PtyFrameKind = "snapshot"
	PtyFrameData       PtyFrameKind = "data"
	PtyFrameStatus     PtyFrameKind = "status"
	PtyFrameExit       PtyFrameKind = "exit"
	PtyFrameProblem    PtyFrameKind = "problem"
)

// PreviewState 表示 Preview session 状态。
type PreviewState string

const (
	PreviewStatePending PreviewState = "pending"
	PreviewStateActive  PreviewState = "active"
	PreviewStateClosed  PreviewState = "closed"
	PreviewStateExpired PreviewState = "expired"
)

// WorkspaceRef 是 owner authority 执行资源操作所需的内部工作区引用。
//
// RootPath 是所属机器上的绝对根目录，只在 agentd 内部传递；公开 wire 层只暴露
// WorkspaceID 与 relative path，避免 renderer 构造任意远端绝对路径。
type WorkspaceRef struct {
	WorkspaceID string
	MachineID   string
	RootPath    string
}

// FileEntry 表示 Workspace-relative 目录项。
type FileEntry struct {
	WorkspaceID string    `json:"workspace_id"`
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Kind        FileKind  `json:"kind"`
	Size        int64     `json:"size"`
	ModifiedAt  time.Time `json:"modified_at"`
	Version     string    `json:"version,omitempty"`
}

// FileDocument 表示带内容版本的文件快照。
type FileDocument struct {
	WorkspaceID   string    `json:"workspace_id"`
	Path          string    `json:"path"`
	Version       string    `json:"version"`
	ContentBase64 string    `json:"content_base64"`
	Size          int64     `json:"size"`
	ModifiedAt    time.Time `json:"modified_at"`
}

// WriteFileCommand 表示带幂等 ID 和乐观锁版本的原子写命令。
type WriteFileCommand struct {
	WorkspaceID   string `json:"workspace_id"`
	CommandID     string `json:"command_id"`
	Path          string `json:"path"`
	IfMatch       string `json:"if_match"`
	ContentBase64 string `json:"content_base64"`
	CreateOnly    bool   `json:"create_only"`
}

// SearchFilesCommand 表示有界 literal 文件搜索。
type SearchFilesCommand struct {
	WorkspaceID string `json:"workspace_id"`
	Query       string `json:"query"`
	Path        string `json:"path"`
	MaxResults  int    `json:"max_results"`
}

// FileSearchMatch 表示一条 Workspace-relative 搜索命中。
type FileSearchMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Preview string `json:"preview"`
}

// FileSearchResult 表示有界搜索结果与扫描摘要。
type FileSearchResult struct {
	WorkspaceID  string            `json:"workspace_id"`
	Matches      []FileSearchMatch `json:"matches"`
	Truncated    bool              `json:"truncated"`
	ScannedFiles int               `json:"scanned_files"`
	ScannedBytes int64             `json:"scanned_bytes"`
}

// FileEventKind 表示文件树失效提示类型；事件不携带文件内容。
type FileEventKind string

const (
	FileEventCreate    FileEventKind = "create"
	FileEventModify    FileEventKind = "modify"
	FileEventRemove    FileEventKind = "remove"
	FileEventGitStatus FileEventKind = "git_status"
)

// FileEvent 是单个 Workspace 内按 Seq 单调递增的文件失效提示。
type FileEvent struct {
	WorkspaceID string        `json:"workspace_id"`
	Seq         int64         `json:"seq"`
	Kind        FileEventKind `json:"kind"`
	Path        string        `json:"path"`
	ObservedAt  time.Time     `json:"observed_at"`
}

// FileSubscription 是文件事件流的 replay + live 订阅。
type FileSubscription struct {
	Replay []FileEvent
	Events <-chan FileEvent
	Done   <-chan error
	cancel func()
}

// NewFileSubscription 创建 provider 实现可返回的 replay + live 订阅。
func NewFileSubscription(replay []FileEvent, events <-chan FileEvent, done <-chan error, cancel func()) *FileSubscription {
	return &FileSubscription{Replay: replay, Events: events, Done: done, cancel: cancel}
}

// Cancel 释放订阅；可重复调用。
func (s *FileSubscription) Cancel() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

// FileStreamer 是 owner authority 的文件失效提示端口。
type FileStreamer interface {
	SubscribeFiles(context.Context, WorkspaceRef, int64) (*FileSubscription, error)
}

// GitStatusEntry 表示 porcelain v2 中一条文件状态。
type GitStatusEntry struct {
	Path           string `json:"path"`
	OriginalPath   string `json:"original_path,omitempty"`
	IndexStatus    string `json:"index_status"`
	WorktreeStatus string `json:"worktree_status"`
}

// GitStatusSnapshot 表示 Workspace 的只读 Git 基础状态。
type GitStatusSnapshot struct {
	WorkspaceID  string           `json:"workspace_id"`
	IsRepository bool             `json:"is_repository"`
	Branch       string           `json:"branch,omitempty"`
	HeadOID      string           `json:"head_oid,omitempty"`
	Upstream     string           `json:"upstream,omitempty"`
	Ahead        int              `json:"ahead"`
	Behind       int              `json:"behind"`
	Entries      []GitStatusEntry `json:"entries"`
}

// CreateTerminalCommand 表示幂等的普通 PTY 创建命令。
type CreateTerminalCommand struct {
	WorkspaceID string `json:"workspace_id"`
	CommandID   string `json:"command_id"`
	Cols        uint16 `json:"cols"`
	Rows        uint16 `json:"rows"`
}

// PtySession 表示普通终端会话元数据；旧 ID 永不重绑新 incarnation。
type PtySession struct {
	TerminalSessionID string   `json:"terminal_session_id"`
	Incarnation       string   `json:"incarnation"`
	WorkspaceID       string   `json:"workspace_id"`
	State             PtyState `json:"state"`
	Shell             string   `json:"shell"`
	ThroughSeq        int64    `json:"through_seq"`
	ExitCode          *int     `json:"exit_code"`
}

// ResourceProblem 是 owner-side 资源错误；desktop adapter 再映射为公开 Problem。
type ResourceProblem struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// PtyServerFrame 是版本化 PTY 服务端 frame。
type PtyServerFrame struct {
	Version           int              `json:"version"`
	Kind              PtyFrameKind     `json:"kind"`
	TerminalSessionID string           `json:"terminal_session_id"`
	Incarnation       string           `json:"incarnation"`
	Seq               int64            `json:"seq"`
	ThroughSeq        int64            `json:"through_seq"`
	DataBase64        string           `json:"data_base64,omitempty"`
	State             PtyState         `json:"state,omitempty"`
	ExitCode          *int             `json:"exit_code,omitempty"`
	Problem           *ResourceProblem `json:"problem,omitempty"`
}

// CreatePreviewCommand 表示幂等的 owner-loopback Preview 创建命令。
type CreatePreviewCommand struct {
	WorkspaceID string `json:"workspace_id"`
	CommandID   string `json:"command_id"`
	Port        int    `json:"port"`
}

// PreviewSession 表示本机可访问的短期 Preview 代理会话。
type PreviewSession struct {
	PreviewSessionID string       `json:"preview_session_id"`
	WorkspaceID      string       `json:"workspace_id"`
	MachineID        string       `json:"machine_id"`
	State            PreviewState `json:"state"`
	URL              string       `json:"url"`
	Port             int          `json:"port"`
	ExpiresAt        time.Time    `json:"expires_at"`
}

// Authority 是 Workspace 所属机器提供的资源能力端口。
//
// 为什么 owner 端仍需二次鉴权：local gateway 的 Workspace 解析只能证明调用目标，
// 最终 path/PTY/preview 安全边界必须由实际持有目录和进程的机器判定。
type Authority interface {
	ListDirectory(context.Context, WorkspaceRef, string) ([]FileEntry, error)
	ReadFile(context.Context, WorkspaceRef, string) (FileDocument, error)
	WriteFile(context.Context, WorkspaceRef, WriteFileCommand) (FileDocument, error)
	SearchFiles(context.Context, WorkspaceRef, SearchFilesCommand) (FileSearchResult, error)
	GitStatus(context.Context, WorkspaceRef) (GitStatusSnapshot, error)
	CreateTerminal(context.Context, WorkspaceRef, CreateTerminalCommand) (PtySession, error)
	GetTerminal(context.Context, string) (PtySession, error)
	CreatePreview(context.Context, WorkspaceRef, CreatePreviewCommand) (PreviewSession, error)
}
