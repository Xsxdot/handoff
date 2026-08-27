// 本文件实现 /ws/pty：浏览器与 PTY 会话之间的双向字节通道。
//
// 职责：
//   - 建连：按 session/since 订阅 ptyhost，首帧回 attached（含 since 与 truncated）
//   - 下行：binary 帧搬 PTY 原始字节，text 帧搬 exit / error 控制信息
//   - 上行：binary 帧是用户按键，text 帧是 resize；debug 取证帧只记日志不进 PTY
//
// 边界：
//   - 不持有会话状态，全部转交 s.pty
//   - **断开只 detach，不杀会话**（spec §3.2）：关页面、切设备、网络抖动
//     都走这条路，杀会话只有 DELETE 一条
//   - machine != "" 的远程会话不落在本文件，走 forward_ws.go 的反代
package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/coder/websocket"
)

// handlePtyWS 处理 GET /ws/pty?session=<id>&since=<n>[&machine=]。
func (s *Server) handlePtyWS(w http.ResponseWriter, r *http.Request) {
	if machine := r.URL.Query().Get("machine"); machine != "" && !isForwarded(r) {
		s.forwardWS(w, r, machine)
		return
	}
	id := r.URL.Query().Get("session")
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("终端 WS 升级失败", "session", id, "cause", err)
		return
	}
	att, aerr := s.pty.Attach(id, since)
	if aerr != nil {
		// **先升级再 close(1008)，不要在升级前回 404。** 会话不存在与连接数超限
		// 都是「你这个请求不合法，别重连」，1008 policy violation 正是这个语义，
		// 与 /ws/events 对未知 task-id 的处理同款，前端 ws.ts 已有对应的终止分支。
		// 若在升级前回 HTTP 状态码，coder/websocket 的 Dial 只会返回一个泛化的
		// 握手错误，前端分不清「服务端不接」与「网络断了」，会一直重连下去。
		s.log.Warn("终端 WS 建连被拒", "session", id, "since", since, "cause", aerr)
		_ = conn.Close(websocket.StatusPolicyViolation, aerr.Error())
		return
	}
	defer att.Detach()
	ctx := r.Context()

	// 首帧必须是 attached：truncated 决定前端要不要先清屏，不给它前端就会
	// 把同一段输出重复画一遍（spec §5.3）。
	if err := writeCtrl(ctx, conn, proto.PtyControl{
		Type: proto.PtyCtrlAttached, Since: att.Since, Truncated: att.Truncated,
		BacklogBytes: uint64(len(att.Backlog)),
	}); err != nil {
		s.log.Warn("终端 WS 首帧写失败", "session", id, "cause", err)
		_ = conn.Close(websocket.StatusInternalError, "首帧写失败")
		return
	}
	if len(att.Backlog) > 0 {
		if err := conn.Write(ctx, websocket.MessageBinary, att.Backlog); err != nil {
			s.log.Warn("终端 WS 回放写失败", "session", id,
				"backlog_bytes", len(att.Backlog), "cause", err)
			_ = conn.Close(websocket.StatusInternalError, "回放写失败")
			return
		}
	}
	s.log.Info("终端 WS 已建连", "session", id, "since", att.Since,
		"backlog_bytes", len(att.Backlog), "truncated", att.Truncated)

	// 上行独立一条 goroutine：读用户按键与 resize。它出错即整体收工。
	upDone := make(chan struct{})
	go func() {
		defer close(upDone)
		s.pumpPtyUplink(ctx, conn, att, id)
	}()

	// 下行在本 goroutine：att.Out 关闭 = 会话结束（不是网络抖动），
	// 此时补一帧 exit 让前端停止重连，再正常 close(1000)。
	for {
		select {
		case <-ctx.Done():
			s.log.Info("终端 WS 断开（客户端离开）", "session", id)
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		case <-upDone:
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		case b, ok := <-att.Out:
			if !ok {
				code := att.ExitCode()
				s.log.Info("终端 WS 收尾：会话已退出", "session", id, "exit_code", code)
				_ = writeCtrl(ctx, conn, proto.PtyControl{Type: proto.PtyCtrlExit, ExitCode: code})
				_ = conn.Close(websocket.StatusNormalClosure, "会话已退出")
				return
			}
			if err := conn.Write(ctx, websocket.MessageBinary, b); err != nil {
				s.log.Warn("终端 WS 下行写失败", "session", id, "bytes", len(b), "cause", err)
				return
			}
		}
	}
}

// pumpPtyUplink 读客户端上行：binary=按键，text=控制帧。
func (s *Server) pumpPtyUplink(ctx context.Context, conn *websocket.Conn, att *ptyhost.Attachment, id string) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			s.log.Debug("终端 WS 上行结束", "session", id, "cause", err)
			return
		}
		if typ == websocket.MessageBinary {
			if err := s.pty.Write(id, data); err != nil {
				// 会话已退出时用户还在打字：告诉他，不要静默吞掉按键
				s.log.Warn("终端 WS 上行写入失败", "session", id, "bytes", len(data), "cause", err)
				_ = writeCtrl(ctx, conn, proto.PtyControl{Type: proto.PtyCtrlError, Message: err.Error()})
				return
			}
			continue
		}
		var ctrl proto.PtyControl
		if err := json.Unmarshal(data, &ctrl); err != nil {
			s.log.Warn("终端 WS 控制帧无法解析", "session", id, "cause", err)
			continue
		}
		if ctrl.Type == proto.PtyCtrlDebug {
			s.log.Debug("[DEBUG-b270] 前端", "session", id, "msg", ctrl.Message)
			continue
		}
		if ctrl.Type != proto.PtyCtrlResize {
			s.log.Warn("终端 WS 收到未知控制帧类型", "session", id, "type", ctrl.Type)
			continue
		}
		if err := att.Resize(ctrl.Cols, ctrl.Rows); err != nil {
			s.log.Warn("终端 WS resize 失败", "session", id,
				"cols", ctrl.Cols, "rows", ctrl.Rows, "cause", err)
		}
	}
}

// writeCtrl 发一帧 JSON 控制信息（text 帧）。
func writeCtrl(ctx context.Context, conn *websocket.Conn, c proto.PtyControl) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, b)
}
