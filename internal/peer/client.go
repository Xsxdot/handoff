// peer client：本机 agentd 对远端 agentd 的 HTTP 客户端。
//
// 职责：
//   - Hello/MachineSnapshot/EventsAfter 控制同步
//   - 文件 list/read/write/search 与 PTY replay+live 双向资源代理
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

const (
	maxPtyReplayFrames       = 4096
	maxPtyReplayEncodedBytes = 6 << 20
	maxPtyReplayWireBytes    = 8 << 20
	maxPtyLiveEncodedBytes   = 128 << 10
	maxPtyLiveWireBytes      = maxPtyLiveEncodedBytes + (64 << 10)
	maxPtyWireFrameBytes     = maxPtyReplayEncodedBytes + (64 << 10)
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

// CreateTerminal 通过远端 owner agentd 幂等创建普通 PTY。
func (c *Client) CreateTerminal(ctx context.Context, ws workspaceapi.WorkspaceRef,
	command workspaceapi.CreateTerminalCommand) (workspaceapi.PtySession, error) {
	path := "/v1/workspaces/" + url.PathEscape(ws.WorkspaceID) + "/terminals"
	request := desktopapi.CreateTerminalRequest{CommandID: command.CommandID, Cols: command.Cols, Rows: command.Rows}
	var response desktopapi.PtySessionDTO
	if err := c.doResource(ctx, http.MethodPost, path, request, &response); err != nil {
		return workspaceapi.PtySession{}, err
	}
	return (&desktopapi.ResourceAssembler{}).FromPtySession(response), nil
}

// GetTerminal 读取远端 owner 的 PTY 元数据，不创建新进程。
func (c *Client) GetTerminal(ctx context.Context, terminalSessionID string) (workspaceapi.PtySession, error) {
	path := "/v1/terminals/" + url.PathEscape(terminalSessionID)
	var response desktopapi.PtySessionDTO
	if err := c.doResource(ctx, http.MethodGet, path, nil, &response); err != nil {
		return workspaceapi.PtySession{}, err
	}
	return (&desktopapi.ResourceAssembler{}).FromPtySession(response), nil
}

// ConnectTerminal 连接远端 owner PTY，并在返回前原子收完握手时声明的 replay。
func (c *Client) ConnectTerminal(ctx context.Context, terminalSessionID, incarnation string,
	after int64) (*workspaceapi.PtySubscription, error) {
	endpoint, err := c.terminalStreamURL(terminalSessionID, incarnation, after)
	if err != nil {
		return nil, err
	}
	header := http.Header{"Authorization": []string{"Bearer " + c.token}}
	conn, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: c.hc, HTTPHeader: header})
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			var problem desktopapi.Problem
			if decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&problem); decodeErr == nil && problem.Code != "" {
				return nil, &workspaceapi.Error{Code: workspaceapi.ErrorCode(problem.Code), Message: problem.Message, Retryable: problem.Retryable}
			}
		}
		return nil, fmt.Errorf("%w: PTY WebSocket 连接失败: %v", ErrUnavailable, err)
	}
	// 4 MiB 原始 ring 经 base64 会膨胀到约 5.33 MiB；wire 上限必须覆盖
	// 合法 snapshot，同时仍由 replay 聚合上限约束总恢复体积。
	conn.SetReadLimit(maxPtyWireFrameBytes)
	first, firstWireBytes, err := readPtyStreamFrame(ctx, conn)
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	if err := validatePtyServerFrame(first, terminalSessionID, incarnation, ""); err != nil {
		conn.CloseNow()
		return nil, err
	}
	if first.Kind != string(workspaceapi.PtyFrameSubscribed) {
		conn.CloseNow()
		return nil, fmt.Errorf("peer PTY 首帧必须是 subscribed: kind=%s", first.Kind)
	}
	if err := validateSubscribedPtyFrame(first, after, firstWireBytes); err != nil {
		conn.CloseNow()
		return nil, err
	}
	if first.Capabilities["input"] < 1 || first.Capabilities["resize"] < 1 {
		conn.CloseNow()
		return nil, fmt.Errorf("peer PTY 首帧缺基础 capability: %+v", first.Capabilities)
	}

	assembler := &desktopapi.ResourceAssembler{}
	replay := make([]workspaceapi.PtyServerFrame, 0)
	var snapshot *workspaceapi.PtyServerFrame
	cursorExpired := false
	lastReplaySeq := after
	replayEncodedBytes := 0
	replayWireBytes := 0
	for first.ThroughSeq > after && (snapshot == nil || snapshot.ThroughSeq < first.ThroughSeq) &&
		(len(replay) == 0 || replay[len(replay)-1].Seq < first.ThroughSeq) {
		frame, wireBytes, readErr := readPtyStreamFrame(ctx, conn)
		if readErr != nil {
			conn.CloseNow()
			return nil, fmt.Errorf("%w: 读取 PTY replay 失败: %v", ErrUnavailable, readErr)
		}
		if validateErr := validatePtyServerFrame(frame, terminalSessionID, incarnation, first.WorkspaceID); validateErr != nil {
			conn.CloseNow()
			return nil, validateErr
		}
		if frame.Kind == string(workspaceapi.PtyFrameSubscribed) {
			conn.CloseNow()
			return nil, fmt.Errorf("%w: peer PTY replay 重复 subscribed", ErrIncompatible)
		}
		replayWireBytes += wireBytes
		if replayWireBytes > maxPtyReplayWireBytes {
			conn.CloseNow()
			return nil, fmt.Errorf("%w: peer PTY replay wire bytes 超过上限", ErrIncompatible)
		}
		ownerFrame := assembler.FromPtyServerFrame(frame)
		switch ownerFrame.Kind {
		case workspaceapi.PtyFrameProblem:
			if wireBytes > maxPtyLiveWireBytes || ownerFrame.Problem == nil || ownerFrame.Seq != 0 ||
				ownerFrame.DataBase64 != "" || len(ownerFrame.Capabilities) != 0 ||
				ownerFrame.State != "" || ownerFrame.ExitCode != nil {
				conn.CloseNow()
				return nil, fmt.Errorf("%w: peer PTY replay problem frame 字段非法", ErrIncompatible)
			}
			if ownerFrame.Problem.Code != string(workspaceapi.ErrorCursorExpired) {
				conn.CloseNow()
				return nil, &workspaceapi.Error{Code: workspaceapi.ErrorCode(ownerFrame.Problem.Code),
					Message: ownerFrame.Problem.Message, Retryable: ownerFrame.Problem.Retryable}
			}
			if cursorExpired {
				conn.CloseNow()
				return nil, fmt.Errorf("%w: peer PTY replay 重复 cursor problem", ErrIncompatible)
			}
			cursorExpired = true
		case workspaceapi.PtyFrameSnapshot:
			if !cursorExpired || snapshot != nil || ownerFrame.Seq != first.ThroughSeq ||
				ownerFrame.ThroughSeq != first.ThroughSeq || len(ownerFrame.Capabilities) != 0 ||
				ownerFrame.State != "" || ownerFrame.ExitCode != nil || ownerFrame.Problem != nil {
				conn.CloseNow()
				return nil, fmt.Errorf("%w: peer PTY snapshot 游标非法", ErrIncompatible)
			}
			replayEncodedBytes += len(ownerFrame.DataBase64)
			if replayEncodedBytes > maxPtyReplayEncodedBytes {
				conn.CloseNow()
				return nil, fmt.Errorf("%w: peer PTY snapshot 超过恢复上限", ErrIncompatible)
			}
			copyFrame := ownerFrame
			snapshot = &copyFrame
		case workspaceapi.PtyFrameData:
			if cursorExpired || ownerFrame.Seq != lastReplaySeq+1 ||
				ownerFrame.ThroughSeq != ownerFrame.Seq || ownerFrame.Seq > first.ThroughSeq {
				conn.CloseNow()
				return nil, fmt.Errorf("%w: peer PTY replay seq 非严格单调: previous=%d current=%d through=%d",
					ErrIncompatible, lastReplaySeq, ownerFrame.Seq, first.ThroughSeq)
			}
			if len(replay) >= maxPtyReplayFrames {
				conn.CloseNow()
				return nil, fmt.Errorf("%w: peer PTY replay frame 超过上限", ErrIncompatible)
			}
			if wireBytes > maxPtyLiveWireBytes || len(ownerFrame.DataBase64) > maxPtyLiveEncodedBytes ||
				len(ownerFrame.Capabilities) != 0 || ownerFrame.State != "" ||
				ownerFrame.ExitCode != nil || ownerFrame.Problem != nil {
				conn.CloseNow()
				return nil, fmt.Errorf("%w: peer PTY replay data frame 字段非法", ErrIncompatible)
			}
			replayEncodedBytes += len(ownerFrame.DataBase64)
			if replayEncodedBytes > maxPtyReplayEncodedBytes {
				conn.CloseNow()
				return nil, fmt.Errorf("%w: peer PTY replay bytes 超过上限", ErrIncompatible)
			}
			replay = append(replay, ownerFrame)
			lastReplaySeq = ownerFrame.Seq
		default:
			conn.CloseNow()
			return nil, fmt.Errorf("%w: peer PTY replay 不允许 kind=%s", ErrIncompatible, ownerFrame.Kind)
		}
	}

	events := make(chan workspaceapi.PtyServerFrame, 64)
	done := make(chan error, 1)
	streamCtx, cancel := context.WithCancel(ctx)
	var closeOnce sync.Once
	var writeMu sync.Mutex
	liveLastSeq := first.ThroughSeq
	liveState := workspaceapi.PtyState(first.State)
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
			frame, wireBytes, readErr := readPtyStreamFrame(streamCtx, conn)
			if readErr != nil {
				if streamCtx.Err() == nil && websocket.CloseStatus(readErr) != websocket.StatusNormalClosure {
					finish(fmt.Errorf("%w: 远端 PTY 流中断: %v", ErrUnavailable, readErr))
				}
				return
			}
			if validateErr := validatePtyServerFrame(frame, terminalSessionID, incarnation, first.WorkspaceID); validateErr != nil {
				finish(validateErr)
				return
			}
			if wireBytes > maxPtyLiveWireBytes {
				finish(fmt.Errorf("%w: peer PTY live frame 超过上限", ErrIncompatible))
				return
			}
			ownerFrame := assembler.FromPtyServerFrame(frame)
			if ownerFrame.Kind == workspaceapi.PtyFrameProblem {
				if ownerFrame.DataBase64 != "" || len(ownerFrame.Capabilities) != 0 ||
					ownerFrame.State != "" || ownerFrame.ExitCode != nil {
					finish(fmt.Errorf("%w: peer PTY problem frame 字段非法", ErrIncompatible))
					return
				}
				if ownerFrame.Problem != nil {
					finish(&workspaceapi.Error{Code: workspaceapi.ErrorCode(ownerFrame.Problem.Code),
						Message: ownerFrame.Problem.Message, Retryable: ownerFrame.Problem.Retryable})
				} else {
					finish(fmt.Errorf("peer PTY problem frame 缺 problem"))
				}
				return
			}
			terminal, nextSeq, nextState, validateErr := validateLivePtyFrame(ownerFrame, liveLastSeq, liveState)
			if validateErr != nil {
				finish(validateErr)
				return
			}
			liveLastSeq = nextSeq
			liveState = nextState
			select {
			case events <- ownerFrame:
			case <-streamCtx.Done():
				return
			}
			if terminal {
				return
			}
		}
	}()
	session := workspaceapi.PtySession{TerminalSessionID: terminalSessionID, Incarnation: incarnation,
		WorkspaceID: first.WorkspaceID, State: workspaceapi.PtyState(first.State),
		ThroughSeq: first.ThroughSeq, ExitCode: first.ExitCode}
	send := func(sendCtx context.Context, frame workspaceapi.PtyClientFrame) error {
		raw, marshalErr := json.Marshal(assembler.ToPtyClientFrame(frame))
		if marshalErr != nil {
			return fmt.Errorf("编码 PTY 客户端 frame: %w", marshalErr)
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if writeErr := conn.Write(sendCtx, websocket.MessageText, raw); writeErr != nil {
			return fmt.Errorf("%w: 写远端 PTY 控制帧: %v", ErrUnavailable, writeErr)
		}
		return nil
	}
	subscription := workspaceapi.NewPtySubscription(session, replay, events, done, cursorExpired, snapshot, send, func() {
		cancel()
		conn.CloseNow()
	})
	subscription.Capabilities = first.Capabilities
	return subscription, nil
}

func validateSubscribedPtyFrame(frame desktopapi.PtyServerFrameDTO, after int64, wireBytes int) error {
	state := workspaceapi.PtyState(frame.State)
	if wireBytes > maxPtyLiveWireBytes || after < 0 || frame.Seq != 0 || frame.ThroughSeq < after ||
		frame.DataBase64 != "" || frame.Problem != nil ||
		(state != workspaceapi.PtyStateActive && state != workspaceapi.PtyStateEnded) ||
		(state == workspaceapi.PtyStateActive && frame.ExitCode != nil) {
		return fmt.Errorf("%w: peer PTY subscribed frame 字段非法", ErrIncompatible)
	}
	return nil
}

func validateLivePtyFrame(frame workspaceapi.PtyServerFrame, lastSeq int64,
	currentState workspaceapi.PtyState) (bool, int64, workspaceapi.PtyState, error) {
	switch frame.Kind {
	case workspaceapi.PtyFrameData:
		if currentState == workspaceapi.PtyStateEnded || frame.Seq != lastSeq+1 || frame.ThroughSeq != frame.Seq {
			return false, lastSeq, currentState, fmt.Errorf("%w: peer PTY live seq 非连续: previous=%d current=%d through=%d",
				ErrIncompatible, lastSeq, frame.Seq, frame.ThroughSeq)
		}
		if len(frame.DataBase64) > maxPtyLiveEncodedBytes || len(frame.Capabilities) != 0 ||
			frame.State != "" || frame.ExitCode != nil || frame.Problem != nil {
			return false, lastSeq, currentState, fmt.Errorf("%w: peer PTY data frame 字段非法", ErrIncompatible)
		}
		return false, frame.Seq, currentState, nil
	case workspaceapi.PtyFrameStatus:
		if frame.Seq != lastSeq || frame.ThroughSeq != lastSeq || frame.DataBase64 != "" ||
			len(frame.Capabilities) != 0 || frame.ExitCode != nil || frame.Problem != nil ||
			currentState != workspaceapi.PtyStateActive || frame.State != currentState {
			return false, lastSeq, currentState, fmt.Errorf("%w: peer PTY status frame 字段非法", ErrIncompatible)
		}
		return false, lastSeq, currentState, nil
	case workspaceapi.PtyFrameExit:
		if frame.Seq != lastSeq || frame.ThroughSeq != lastSeq || frame.DataBase64 != "" ||
			len(frame.Capabilities) != 0 || frame.Problem != nil || frame.State != workspaceapi.PtyStateEnded {
			return false, lastSeq, currentState, fmt.Errorf("%w: peer PTY exit frame 字段非法", ErrIncompatible)
		}
		return true, lastSeq, workspaceapi.PtyStateEnded, nil
	default:
		return false, lastSeq, currentState, fmt.Errorf("%w: peer PTY live 不允许 kind=%s", ErrIncompatible, frame.Kind)
	}
}

func validatePtyServerFrame(frame desktopapi.PtyServerFrameDTO, terminalSessionID,
	incarnation, workspaceID string) error {
	if frame.Version != 1 {
		return fmt.Errorf("%w: peer PTY frame version=%d", ErrIncompatible, frame.Version)
	}
	if frame.TerminalSessionID != terminalSessionID || frame.Incarnation != incarnation || frame.WorkspaceID == "" {
		return fmt.Errorf("peer PTY frame 身份非法: kind=%s terminal_session_id=%s incarnation=%s workspace_id=%s",
			frame.Kind, frame.TerminalSessionID, frame.Incarnation, frame.WorkspaceID)
	}
	if workspaceID != "" && frame.WorkspaceID != workspaceID {
		return fmt.Errorf("peer PTY frame workspace 漂移: expected=%s actual=%s", workspaceID, frame.WorkspaceID)
	}
	switch workspaceapi.PtyFrameKind(frame.Kind) {
	case workspaceapi.PtyFrameSubscribed, workspaceapi.PtyFrameSnapshot, workspaceapi.PtyFrameData,
		workspaceapi.PtyFrameStatus, workspaceapi.PtyFrameExit, workspaceapi.PtyFrameProblem:
		return nil
	default:
		return fmt.Errorf("%w: peer PTY frame kind=%s", ErrIncompatible, frame.Kind)
	}
}

func (c *Client) terminalStreamURL(terminalSessionID, incarnation string, after int64) (string, error) {
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return "", fmt.Errorf("解析 peer endpoint: %w", err)
	}
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	default:
		return "", fmt.Errorf("peer endpoint scheme 不支持 WebSocket: %s", endpoint.Scheme)
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/v1/terminals/" + url.PathEscape(terminalSessionID) + "/stream"
	query := endpoint.Query()
	query.Set("incarnation", incarnation)
	query.Set("after", fmt.Sprintf("%d", after))
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func readPtyStreamFrame(ctx context.Context, conn *websocket.Conn) (desktopapi.PtyServerFrameDTO, int, error) {
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return desktopapi.PtyServerFrameDTO{}, 0, err
	}
	var frame desktopapi.PtyServerFrameDTO
	if err := json.Unmarshal(raw, &frame); err != nil {
		return desktopapi.PtyServerFrameDTO{}, 0, fmt.Errorf("解码 peer PTY frame: %w", err)
	}
	return frame, len(raw), nil
}

// CloseTerminal 显式终止远端 owner PTY，保留会话元数据。
func (c *Client) CloseTerminal(ctx context.Context, terminalSessionID, incarnation string) (workspaceapi.PtySession, error) {
	path := "/v1/terminals/" + url.PathEscape(terminalSessionID) + "?incarnation=" + url.QueryEscape(incarnation)
	var response desktopapi.PtySessionDTO
	if err := c.doResource(ctx, http.MethodDelete, path, nil, &response); err != nil {
		return workspaceapi.PtySession{}, err
	}
	return (&desktopapi.ResourceAssembler{}).FromPtySession(response), nil
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
