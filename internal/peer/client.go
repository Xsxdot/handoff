// peer client：本机 agentd 对远端 agentd 的 HTTP 客户端。
//
// 职责：
//   - Hello/MachineSnapshot/EventsAfter 控制同步
//   - 文件 list/read/write/search 与 replay+live stream 资源代理
//   - 项目目录 InspectPath/Clone 命令代理
//   - 区分资源 Problem、协议不兼容、认证失败与网络不可达
//
// 边界：
//   - 只能由本机 agentd 构造；credential resolver 通过 Machine.SecretRef 取 token
//   - 不记录 token、文件内容、搜索 query 或 preview
package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// 错误哨兵（Supervisor 据此映射 Machine 状态）。
var (
	ErrAuthFailed   = errors.New("peer 认证失败")
	ErrIncompatible = errors.New("peer 协议不兼容")
	ErrUnavailable  = errors.New("peer 不可达")
)

// ClientConfig 是 peer client 的构造参数。
type ClientConfig struct {
	Endpoint string // 远端 agentd 地址（含 scheme）
	Token    string // 远端 token（来自 Machine.SecretRef 解析）
}

// Client 是 peer v1 HTTP 客户端。
type Client struct {
	endpoint string
	token    string
	hc       *http.Client
}

// NewClient 创建 peer client。
//
// 为什么 token 由调用方注入而非 client 自取：client 不持有 config，
// credential resolver 在 supervisor 层按 Machine.SecretRef 解析后传入。
func NewClient(cfg ClientConfig) *Client {
	return &Client{
		endpoint: strings.TrimSuffix(cfg.Endpoint, "/"),
		token:    cfg.Token,
		hc: &http.Client{
			// peer 是机器到机器的同步，超时严格有界防止半死连接挂起
			Timeout: 30 * time.Second,
		},
	}
}

// Hello 获取远端协议版本与 capability。
func (c *Client) Hello(ctx context.Context) (Hello, error) {
	var out Hello
	if err := c.do(ctx, http.MethodGet, "/v1/peer/hello", nil, &out); err != nil {
		return Hello{}, err
	}
	return out, nil
}

// MachineSnapshot 获取远端全量快照。
func (c *Client) MachineSnapshot(ctx context.Context) (MachineSnapshot, error) {
	var out MachineSnapshot
	if err := c.do(ctx, http.MethodGet, "/v1/machine/snapshot", nil, &out); err != nil {
		return MachineSnapshot{}, err
	}
	return out, nil
}

// EventsAfter 拉取 machine 在 afterSeq 之后的事件，最多 limit 条。
func (c *Client) EventsAfter(ctx context.Context, machineID string, afterSeq int64, limit int) ([]MachineEvent, error) {
	path := fmt.Sprintf("/v1/machine/events?machine_id=%s&after=%d&limit=%d", url.QueryEscape(machineID), afterSeq, limit)
	var out []MachineEvent
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectPath 通过远端 owner agentd 检查项目既有目录。
func (c *Client) InspectPath(ctx context.Context, command controlplane.InspectPathCommand) (controlplane.PathInspection, error) {
	request := ProjectInspectRequest{
		OperationID: command.OperationID, TargetID: command.TargetID, Path: command.Path,
	}
	var response ProjectPathInspection
	if err := c.doResource(ctx, http.MethodPost, "/v1/machine/project/inspect-path", request, &response); err != nil {
		return controlplane.PathInspection{}, err
	}
	return fromPeerPathInspection(response), nil
}

// Clone 通过远端 owner agentd 克隆项目仓库。
func (c *Client) Clone(ctx context.Context, command controlplane.CloneLocationCommand) (controlplane.PathInspection, error) {
	request := ProjectCloneRequest{
		OperationID: command.OperationID, TargetID: command.TargetID,
		GitURL: command.GitURL, ClonePath: command.ClonePath,
	}
	var response ProjectPathInspection
	if err := c.doResource(ctx, http.MethodPost, "/v1/machine/project/clone", request, &response); err != nil {
		return controlplane.PathInspection{}, err
	}
	return fromPeerPathInspection(response), nil
}

func fromPeerPathInspection(value ProjectPathInspection) controlplane.PathInspection {
	return controlplane.PathInspection{
		Path: value.Path, CanonicalPath: value.CanonicalPath, IsRepo: value.IsRepo,
		RepoIdentity: value.RepoIdentity, GitCommonDir: value.GitCommonDir,
		Branch: value.Branch, HeadOID: value.HeadOID,
	}
}

// ListDirectory 代理远端 owner 的目录浏览。
func (c *Client) ListDirectory(ctx context.Context, ws workspaceapi.WorkspaceRef, relativePath string) ([]workspaceapi.FileEntry, error) {
	path := "/v1/workspaces/" + url.PathEscape(ws.WorkspaceID) + "/entries?path=" + url.QueryEscape(relativePath)
	var out []desktopapi.FileEntryDTO
	if err := c.doResource(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return (&desktopapi.ResourceAssembler{}).FromFileEntries(out), nil
}

// ReadFile 代理远端 owner 的文件读取。
func (c *Client) ReadFile(ctx context.Context, ws workspaceapi.WorkspaceRef, relativePath string) (workspaceapi.FileDocument, error) {
	path := "/v1/workspaces/" + url.PathEscape(ws.WorkspaceID) + "/file?path=" + url.QueryEscape(relativePath)
	var out desktopapi.FileDocumentDTO
	if err := c.doResource(ctx, http.MethodGet, path, nil, &out); err != nil {
		return workspaceapi.FileDocument{}, err
	}
	return (&desktopapi.ResourceAssembler{}).FromFileDocument(out), nil
}

// WriteFile 代理远端 owner 的版本化原子写。
func (c *Client) WriteFile(ctx context.Context, ws workspaceapi.WorkspaceRef, command workspaceapi.WriteFileCommand) (workspaceapi.FileDocument, error) {
	path := "/v1/workspaces/" + url.PathEscape(ws.WorkspaceID) + "/file"
	req := desktopapi.WriteFileRequest{CommandID: command.CommandID, Path: command.Path, IfMatch: command.IfMatch,
		ContentBase64: command.ContentBase64, CreateOnly: command.CreateOnly}
	var out desktopapi.FileDocumentDTO
	if err := c.doResource(ctx, http.MethodPut, path, req, &out); err != nil {
		return workspaceapi.FileDocument{}, err
	}
	return (&desktopapi.ResourceAssembler{}).FromFileDocument(out), nil
}

// SearchFiles 代理远端 owner 的有界 literal 搜索。
func (c *Client) SearchFiles(ctx context.Context, ws workspaceapi.WorkspaceRef, command workspaceapi.SearchFilesCommand) (workspaceapi.FileSearchResult, error) {
	path := "/v1/workspaces/" + url.PathEscape(ws.WorkspaceID) + "/files/search"
	req := desktopapi.SearchFilesRequest{Query: command.Query, Path: command.Path, MaxResults: command.MaxResults}
	var out desktopapi.FileSearchResultDTO
	if err := c.doResource(ctx, http.MethodPost, path, req, &out); err != nil {
		return workspaceapi.FileSearchResult{}, err
	}
	return (&desktopapi.ResourceAssembler{}).FromFileSearchResult(out), nil
}

// SubscribeFiles 代理远端 owner 的 replay + live 文件事件流。
func (c *Client) SubscribeFiles(ctx context.Context, ws workspaceapi.WorkspaceRef, after int64) (*workspaceapi.FileSubscription, error) {
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("解析 peer endpoint: %w", err)
	}
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	default:
		return nil, fmt.Errorf("peer endpoint scheme 不支持 WebSocket: %s", endpoint.Scheme)
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/v1/workspaces/" + url.PathEscape(ws.WorkspaceID) + "/files/stream"
	query := endpoint.Query()
	query.Set("after", fmt.Sprintf("%d", after))
	endpoint.RawQuery = query.Encode()
	header := http.Header{"Authorization": []string{"Bearer " + c.token}}
	conn, response, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{HTTPClient: c.hc, HTTPHeader: header})
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			var problem desktopapi.Problem
			if decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&problem); decodeErr == nil && problem.Code != "" {
				return nil, &workspaceapi.Error{Code: workspaceapi.ErrorCode(problem.Code), Message: problem.Message, Retryable: problem.Retryable}
			}
		}
		return nil, fmt.Errorf("%w: 文件事件 WebSocket 连接失败: %v", ErrUnavailable, err)
	}
	conn.SetReadLimit(4 << 20)
	first, err := readFileStreamFrame(ctx, conn)
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	if first.Kind == "problem" && first.Problem != nil {
		conn.CloseNow()
		return nil, &workspaceapi.Error{Code: workspaceapi.ErrorCode(first.Problem.Code), Message: first.Problem.Message, Retryable: first.Problem.Retryable}
	}
	if first.Kind != "subscribed" || first.WorkspaceID != ws.WorkspaceID {
		conn.CloseNow()
		return nil, fmt.Errorf("peer 文件事件首帧非法: kind=%s workspace_id=%s", first.Kind, first.WorkspaceID)
	}
	assembler := &desktopapi.ResourceAssembler{}
	replay := assembler.FromFileEvents(first.Replay)
	events := make(chan workspaceapi.FileEvent, 64)
	done := make(chan error, 1)
	streamCtx, cancel := context.WithCancel(ctx)
	var closeOnce sync.Once
	finish := func(reason error) {
		closeOnce.Do(func() {
			if reason != nil {
				done <- reason
			}
			close(events)
			close(done)
			conn.CloseNow()
		})
	}
	go func() {
		defer finish(nil)
		for {
			frame, readErr := readFileStreamFrame(streamCtx, conn)
			if readErr != nil {
				if streamCtx.Err() == nil {
					finish(fmt.Errorf("%w: 远端文件事件流中断: %v", ErrUnavailable, readErr))
				}
				return
			}
			switch frame.Kind {
			case "event":
				if frame.Event == nil {
					finish(fmt.Errorf("peer 文件事件 frame 缺 event"))
					return
				}
				event := assembler.FromFileEvent(*frame.Event)
				select {
				case events <- event:
				case <-streamCtx.Done():
					return
				}
			case "problem":
				if frame.Problem != nil {
					finish(&workspaceapi.Error{Code: workspaceapi.ErrorCode(frame.Problem.Code), Message: frame.Problem.Message, Retryable: frame.Problem.Retryable})
				} else {
					finish(fmt.Errorf("peer 文件事件 problem frame 缺 problem"))
				}
				return
			default:
				finish(fmt.Errorf("peer 文件事件 frame kind 不支持: %s", frame.Kind))
				return
			}
		}
	}()
	cancelSubscription := func() {
		cancel()
		conn.CloseNow()
	}
	return workspaceapi.NewFileSubscription(replay, events, done, cancelSubscription), nil
}

func readFileStreamFrame(ctx context.Context, conn *websocket.Conn) (desktopapi.FileStreamFrameDTO, error) {
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return desktopapi.FileStreamFrameDTO{}, err
	}
	var frame desktopapi.FileStreamFrameDTO
	if err := json.Unmarshal(raw, &frame); err != nil {
		return desktopapi.FileStreamFrameDTO{}, fmt.Errorf("解码 peer 文件事件 frame: %w", err)
	}
	return frame, nil
}

// GitStatus 代理远端 owner 的只读 Git 基础状态。
func (c *Client) GitStatus(ctx context.Context, ws workspaceapi.WorkspaceRef) (workspaceapi.GitStatusSnapshot, error) {
	path := "/v1/workspaces/" + url.PathEscape(ws.WorkspaceID) + "/git/status"
	var out desktopapi.GitStatusSnapshotDTO
	if err := c.doResource(ctx, http.MethodGet, path, nil, &out); err != nil {
		return workspaceapi.GitStatusSnapshot{}, err
	}
	return (&desktopapi.ResourceAssembler{}).FromGitStatus(out), nil
}

// CreateTerminal 在 Task 4 接入远端 wire route。
func (c *Client) CreateTerminal(context.Context, workspaceapi.WorkspaceRef, workspaceapi.CreateTerminalCommand) (workspaceapi.PtySession, error) {
	return workspaceapi.PtySession{}, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported, Message: "远端 PTY 资源能力尚未接入"}
}

// GetTerminal 在 Task 4 接入远端 wire route。
func (c *Client) GetTerminal(context.Context, string) (workspaceapi.PtySession, error) {
	return workspaceapi.PtySession{}, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported, Message: "远端 PTY 资源能力尚未接入"}
}

// CreatePreview 在 Task 5 接入远端 wire route。
func (c *Client) CreatePreview(context.Context, workspaceapi.WorkspaceRef, workspaceapi.CreatePreviewCommand) (workspaceapi.PreviewSession, error) {
	return workspaceapi.PreviewSession{}, &workspaceapi.Error{Code: workspaceapi.ErrorCapabilityUnsupported, Message: "远端 Preview 资源能力尚未接入"}
}

// Close 释放资源（当前无持久连接，保留接口便于生命周期统一）。
func (c *Client) Close() {}

// do 执行带鉴权与错误映射的 HTTP 请求。
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	return c.doWithLimit(ctx, method, path, body, out, 1<<20)
}

func (c *Client) doResource(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("编码 peer 资源请求: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	return c.doWithLimit(ctx, method, path, reader, out, 32<<20)
}

func (c *Client) doWithLimit(ctx context.Context, method, path string, body io.Reader, out any, responseLimit int64) error {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return fmt.Errorf("构造请求 %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		// 网络错误（连接拒绝/超时/断线）→ unavailable
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrAuthFailed
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		var problem desktopapi.Problem
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&problem); err == nil && problem.Code != "" {
			return &workspaceapi.Error{Code: workspaceapi.ErrorCode(problem.Code), Message: problem.Message, Retryable: problem.Retryable}
		}
		if resp.StatusCode == http.StatusConflict {
			return ErrIncompatible
		}
		return fmt.Errorf("peer 返回非 2xx: %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, responseLimit)).Decode(out); err != nil {
			return fmt.Errorf("解码 peer 响应 %s: %w", path, err)
		}
	}
	return nil
}
