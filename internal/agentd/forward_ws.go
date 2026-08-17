// 本文件实现 WebSocket 的跨机反代：`forwardTo` 的 WS 孪生。
//
// 职责：
//   - 按 ?machine= 找到 target，拨它的同路径 WS 端点（带 Bearer 与防环头）
//   - 两条连接双向对拷，**保持帧类型**（binary 仍是 binary）
//   - 关闭码与原因双向传播
//
// 边界：
//   - **不解析帧内容**。它不知道 PTY、不知道 JSON，只搬帧
//   - 一跳封顶：出站带 X-Handoff-Forwarded，对端 handlePtyWS 因此不再反代
//   - 不重试。与 forwardTo 一致：一次失败就 502 带原文，让调用方决定
//
// 为什么不让浏览器直连远端 agentd：cookie 是 host-only 的，远端那台没有本机
// 这份会话，等于要另做一套跨机 ticket 分发与跨域处理。「本机 agentd 是唯一
// 入口」本来就是既有模型（/api/workspaces/* 的 ?machine= 转发即此）。
package agentd

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// wsDialBudget 是拨远端的握手预算。握手不该慢：慢就是不可达，早点回 502
// 比让用户对着一个转圈的终端强。建连之后的数据流**不受它约束**。
const wsDialBudget = 10 * time.Second

// forwardWS 把一条 WS 请求反代到 machine 指定的机器。
//
// 关键顺序：**先拨上游，成功了再 Accept 本地**。反过来的话，上游不可达时
// 本地已经升级成 101，只能发一个 close——前端看到的是「连上了又断了」，
// 会一直重连；而 502 才让它知道是本机与目标机之间的问题（与 forwardTo 一致）。
func (s *Server) forwardWS(w http.ResponseWriter, r *http.Request, machine string) {
	t, ok := s.conf().Targets[machine]
	if !ok {
		s.log.Warn("WS 转发被拒：机器名未在配置中定义", "machine", machine, "path", r.URL.Path)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "机器 " + machine + " 未在本机配置的 targets 中定义"})
		return
	}
	target, err := forwardURL(t.Addr, r.URL) // 复用 REST 那份：同时摘掉 machine 参数
	if err != nil {
		s.log.Error("WS 转发失败：目标地址不合法", "machine", machine, "addr", t.Addr, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "转发到 " + machine + " 失败: " + err.Error()})
		return
	}
	target = toWSScheme(target)

	dialCtx, cancelDial := context.WithTimeout(r.Context(), wsDialBudget)
	defer cancelDial()
	hdr := http.Header{forwardedHeader: {"1"}}
	if t.Token != "" {
		hdr.Set("Authorization", "Bearer "+t.Token)
	}
	start := time.Now()
	up, _, err := websocket.Dial(dialCtx, target, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		s.log.Error("WS 转发失败：上游不可达", "machine", machine, "path", r.URL.Path,
			"elapsed_ms", time.Since(start).Milliseconds(), "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "转发到 " + machine + " 失败: " + err.Error()})
		return
	}
	defer up.Close(websocket.StatusNormalClosure, "")

	down, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("WS 转发：本地升级失败", "machine", machine, "cause", err)
		return
	}
	defer down.Close(websocket.StatusNormalClosure, "")

	// 上游握手成功后就把读限制放开：PTY 的回放帧可能有几百 KB，
	// coder/websocket 默认 32KiB 的读上限会把它判成协议错误。
	up.SetReadLimit(wsForwardReadLimit)
	down.SetReadLimit(wsForwardReadLimit)

	s.log.Info("WS 转发已建立", "machine", machine, "path", r.URL.Path,
		"elapsed_ms", time.Since(start).Milliseconds())

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	errCh := make(chan error, 2)
	go func() { errCh <- pipeWS(ctx, down, up) }() // 浏览器 → 远端
	go func() { errCh <- pipeWS(ctx, up, down) }() // 远端 → 浏览器

	first := <-errCh
	// 任一方向结束就收工，并把对端的关闭码原样传给另一端：1008「别重连」
	// 这种语义必须穿过反代，否则前端会永远重连一个已经明确拒绝它的会话。
	status, reason := websocket.CloseStatus(first), ""
	var ce websocket.CloseError
	if errors.As(first, &ce) {
		reason = ce.Reason
	}
	if status == -1 {
		status = websocket.StatusNormalClosure
	}
	// **关闭必须发生在 cancel() 之前**：coder/websocket 的 Read 会为传入的
	// context 注册 AfterFunc，ctx 一旦取消就立刻 close 底层连接——先 cancel 再
	// Close，close 帧还没发出去连接就断了，对端只能看到裸 EOF，分不清「会话被
	// 拒」和「网络断了」，1008 的终止分支永远走不到。先 Close 完成握手、再 cancel
	// 只是把已结束的连接补一刀，无损。
	_ = down.Close(status, reason)
	_ = up.Close(status, reason)
	cancel()
	s.log.Info("WS 转发结束", "machine", machine, "path", r.URL.Path,
		"close_status", int(status), "elapsed_ms", time.Since(start).Milliseconds())
}

// wsForwardReadLimit 是反代两侧的单帧上限。512 KiB 覆盖 256 KiB 回放缓冲
// 加上任何合理的膨胀，同时仍是一道防线（不至于让一个坏掉的对端把内存吃光）。
const wsForwardReadLimit = 512 << 10

// pipeWS 把 src 的每一帧原样写进 dst，**保持帧类型**。
//
// 帧类型必须保持：数据走 binary、控制走 text 是 /ws/pty 的契约（spec §5.3），
// 反代若把一切都当 text 转发，二进制里任何非 UTF-8 字节都会被判为协议错误。
func pipeWS(ctx context.Context, src, dst *websocket.Conn) error {
	for {
		typ, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

// toWSScheme 把 http/https 换成 ws/wss。forwardURL 产出的恒是 http(s)://。
func toWSScheme(u string) string {
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u
}
