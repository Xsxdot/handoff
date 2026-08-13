// perm.go —— 权限裁决 unix socket 服务端。
//
// 职责：
//   - 在任务目录的 perm.sock 上受理裁决请求（由 claude 拉起的 permission-mcp 进程发来）
//   - 把请求以回调交给 adapter（转成 AdapterEvent 进 manager 审批链）
//   - 收到裁决后回发给对应连接，放行或拒绝该次工具调用
//
// 边界：
//   - 不做任何审批判断：批不批由 manager 依协调者应答决定（executor 包级边界）
//   - 不认识 claude 的消息格式：只处理本文件定义的两条线协议
//
// 为什么用 unix socket 而不是 agentd 的 HTTP 口：被监管的 executor 不该拿到
// agentd token；socket 文件落在 0700 的任务目录内，权限即边界，且无需分配端口。
package claudecode

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

// permDecision 是回发给 MCP 进程的裁决帧。
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

// newPermServerFn 是 newPermServer 的测试缝（与 startProcHost 同手法）：
// 测试替换它绕开真实 unix socket 绑定（长路径会超 sun_path 上限），语义不变。
var newPermServerFn = newPermServer

// newPermServer 建立并开始受理裁决请求。
//
// 参数：
//   - sockPath: socket 文件路径（位于 0700 的任务目录内）
//   - log: 日志入口
//   - onAsk: 收到请求时的回调（adapter 在其中 emit permission 事件）；同一
//     tool_use_id 重连重登记时会**再次**回调——manager 侧 ticket 按 id 幂等，
//     重发是 agentd 重启后能继续裁决的必要条件
//
// 返回：
//   - 已在受理的服务端；监听失败时返回错误
//
// 注意：
//   - 复用已存在的 socket 文件前会先删除（agentd 重启后残留），否则 bind 报
//     address already in use
func newPermServer(sockPath string, log *slog.Logger, onAsk func(permAsk)) (*permServer, error) {
	// 残留 socket 文件会让 bind 直接失败，而它恰恰是 agentd 重启后的常态
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		log.Error("清理残留 socket 失败", "sock", sockPath, "cause", err)
		return nil, fmt.Errorf("清理残留 socket %s: %w", sockPath, err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Error("监听裁决 socket 失败", "sock", sockPath, "cause", err)
		return nil, fmt.Errorf("监听 %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		log.Warn("设置 socket 权限失败，继续（任务目录本身 0700）", "sock", sockPath, "cause", err)
	}
	s := &permServer{sockPath: sockPath, ln: ln, log: log, onAsk: onAsk,
		pending: map[string]net.Conn{}}
	log.Info("裁决 socket 已就绪", "sock", sockPath)
	go s.acceptLoop()
	return s, nil
}

// acceptLoop 持续受理连接，每条连接一次请求。
func (s *permServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				s.log.Debug("裁决 socket 已关闭，停止受理", "sock", s.sockPath)
				return
			}
			s.log.Error("受理裁决连接失败", "sock", s.sockPath, "cause", err)
			return
		}
		go s.serveConn(conn)
	}
}

// serveConn 读一条 ask 帧、登记挂起并回调；连接在裁决回发后由 Respond 关闭。
func (s *permServer) serveConn(conn net.Conn) {
	var ask permAsk
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&ask); err != nil {
		s.log.Error("解析裁决请求失败，丢弃该连接", "cause", err)
		conn.Close()
		return
	}
	if ask.ToolUseID == "" {
		s.log.Error("裁决请求缺 tool_use_id，丢弃", "tool", ask.ToolName)
		conn.Close()
		return
	}
	s.mu.Lock()
	// 同 id 重连：旧连接多半已死（agentd 重启），换成新连接后仍要回调，
	// 让 manager 重建/复用同一 ticket（id 幂等），否则这次请求永远等不到人
	if old, ok := s.pending[ask.ToolUseID]; ok && old != conn {
		old.Close()
		s.log.Info("裁决请求重连重登记", "tool_use_id", ask.ToolUseID, "tool", ask.ToolName)
	}
	s.pending[ask.ToolUseID] = conn
	s.mu.Unlock()

	s.log.Info("收到权限裁决请求", "tool_use_id", ask.ToolUseID, "tool", ask.ToolName)
	s.onAsk(ask)
}

// Respond 回发裁决并关闭该连接。
//
// 参数：
//   - toolUseID: 目标请求 id（= claude 的 tool_use_id）
//   - behavior: "allow" 或 "deny"
//   - message: deny 时的拒绝理由（allow 时忽略）
//
// 返回：
//   - 找不到挂起请求或写失败时报错。**不得静默成功**：静默成功会让 manager
//     以为裁决已送达，而 claude 侧其实还在等，任务就此卡死
func (s *permServer) Respond(toolUseID, behavior, message string) error {
	s.mu.Lock()
	conn, ok := s.pending[toolUseID]
	if ok {
		delete(s.pending, toolUseID)
	}
	s.mu.Unlock()
	if !ok {
		s.log.Error("裁决目标不存在（请求已失效或进程已退）", "tool_use_id", toolUseID, "behavior", behavior)
		return fmt.Errorf("裁决请求 %s 不存在", toolUseID)
	}
	defer conn.Close()
	b, err := json.Marshal(permDecision{Type: "decision", Behavior: behavior, Message: message})
	if err != nil {
		s.log.Error("序列化裁决失败", "tool_use_id", toolUseID, "cause", err)
		return fmt.Errorf("序列化裁决: %w", err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		s.log.Error("回发裁决失败", "tool_use_id", toolUseID, "behavior", behavior, "cause", err)
		return fmt.Errorf("回发裁决 %s: %w", toolUseID, err)
	}
	s.log.Info("裁决已回发", "tool_use_id", toolUseID, "behavior", behavior)
	return nil
}

// Close 停止受理并关闭全部挂起连接。
//
// 挂起连接被关闭后，MCP 侧会按退避重连重登记——若 agentd 是重启而非退出，
// 重连会落到新的 permServer 上继续裁决。
func (s *permServer) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conns := make([]net.Conn, 0, len(s.pending))
	for _, c := range s.pending {
		conns = append(conns, c)
	}
	s.pending = map[string]net.Conn{}
	s.mu.Unlock()

	for _, c := range conns {
		c.Close()
	}
	err := s.ln.Close()
	s.log.Info("裁决 socket 已关闭", "sock", s.sockPath, "closed_pending", len(conns))
	return err
}
