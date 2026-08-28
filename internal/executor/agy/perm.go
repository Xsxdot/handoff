// perm.go —— 权限裁决 unix socket 服务端。
//
// 职责：
//   - 在任务目录的 perm.sock 上受理裁决请求（由 agy 经 PreToolUse hook 拉起的 permission-hook 进程发来）
//   - 把请求以回调交给 adapter（转成 AdapterEvent 进 manager 审批链）
//   - 收到裁决后回发给对应连接，放行或拒绝该次工具调用
package agy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
)

// permAsk 是一次待裁决的权限请求（线协议 ask 帧的解码结果）。
type permAsk struct {
	ToolUseID string          `json:"tool_use_id"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
}

// permDecision 是回发给 hook 进程的裁决帧。
type permDecision struct {
	Type     string `json:"type"`
	Behavior string `json:"behavior"`          // allow | deny
	Message  string `json:"message,omitempty"` // deny 时的拒绝理由
}

// permServer 在 unix socket 上受理裁决请求并回发裁决。
type permServer struct {
	sockPath string
	ln       net.Listener
	log      *slog.Logger
	onAsk    func(permAsk)

	mu      sync.Mutex
	pending map[string]net.Conn // tool_use_id → 等待裁决的连接
	closed  bool
}

var newPermServerFn = newPermServer

func newPermServer(sockPath string, log *slog.Logger, onAsk func(permAsk)) (*permServer, error) {
	const sunPathMax = 107
	if len(sockPath) > sunPathMax {
		return nil, fmt.Errorf("裁决 socket 路径过长（%d 字节，上限 %d）: %s", len(sockPath), sunPathMax, sockPath)
	}
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		log.Error("清理残留 socket 失败", "sock", sockPath, "cause", err)
		return nil, fmt.Errorf("清理残留 socket %s: %w", sockPath, err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Error("监听裁决 socket 失败", "sock", sockPath, "cause", err)
		return nil, fmt.Errorf("监听 %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0600); err != nil {
		log.Warn("设置 socket 权限失败，继续", "sock", sockPath, "cause", err)
	}
	s := &permServer{
		sockPath: sockPath,
		ln:       ln,
		log:      log,
		onAsk:    onAsk,
		pending:  make(map[string]net.Conn),
	}
	go s.serve()
	return s, nil
}

func (s *permServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			s.log.Warn("接收裁决连接失败", "sock", s.sockPath, "cause", err)
			return
		}
		go s.handle(conn)
	}
}

func (s *permServer) handle(conn net.Conn) {
	defer func() {
		// handle 函数退出的唯一路径是：连接异常断开。
		// 正常的裁决返回由 Respond 关闭连接并从 pending 摘除。
	}()
	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		_ = conn.Close()
		return
	}
	var ask struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(sc.Bytes(), &ask); err != nil || ask.Type != "ask" || ask.ToolUseID == "" {
		s.log.Warn("收到非法 ask 帧", "raw", sc.Text(), "cause", err)
		_ = json.NewEncoder(conn).Encode(permDecision{Behavior: "deny", Message: "非法权限请求"})
		_ = conn.Close()
		return
	}

	s.mu.Lock()
	if old, exists := s.pending[ask.ToolUseID]; exists {
		s.log.Info("tool_use_id 发生重连，关闭旧连接", "id", ask.ToolUseID)
		_ = old.Close()
	}
	s.pending[ask.ToolUseID] = conn
	s.mu.Unlock()

	s.onAsk(permAsk{
		ToolUseID: ask.ToolUseID,
		ToolName:  ask.ToolName,
		Input:     ask.Input,
	})
}

// Respond 对指定 tool_use_id 做出裁决并回发。
func (s *permServer) Respond(toolUseID, behavior, message string) error {
	s.mu.Lock()
	conn, ok := s.pending[toolUseID]
	if ok {
		delete(s.pending, toolUseID)
	}
	s.mu.Unlock()

	if !ok {
		return fmt.Errorf("权限工单 %s 未处于待裁决状态（可能已应答或连接已断开）", toolUseID)
	}
	defer conn.Close()

	if behavior != "allow" && behavior != "deny" {
		behavior = "deny"
	}
	d := permDecision{
		Type:     "decision",
		Behavior: behavior,
		Message:  message,
	}
	if err := json.NewEncoder(conn).Encode(d); err != nil {
		s.log.Error("发送裁决帧失败", "id", toolUseID, "behavior", behavior, "cause", err)
		return fmt.Errorf("发送裁决 %s: %w", toolUseID, err)
	}
	s.log.Info("已下发权限裁决", "id", toolUseID, "behavior", behavior)
	return nil
}

// Close 关闭服务端并释放所有 pending 连接。
func (s *permServer) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for id, conn := range s.pending {
		_ = json.NewEncoder(conn).Encode(permDecision{Behavior: "deny", Message: "服务已停止"})
		_ = conn.Close()
		delete(s.pending, id)
	}
	s.mu.Unlock()

	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	_ = os.Remove(s.sockPath)
	return err
}
