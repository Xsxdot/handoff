// 本文件实现 agentd → agentd 的请求转发基座。
//
// 职责：
//   - 判定一个请求是否要转发（显式 ?machine= / 按任务 id 路由都走这里的搬运）
//   - 原样搬运：方法、路径、请求体、查询参数（去掉路由用的 machine）
//   - 原样回送：状态码、Content-Type、X-Handoff-* 响应头、响应体一律不改写，
//     响应体按上游块边界及时回送
//   - 防环：转发请求带 X-Handoff-Forwarded: 1，带此头的请求一律本机处理
//
// 边界：
//   - 不解释业务语义：登记契约由目标机器解释，本机只做搬运，不加校验也不加解释
//   - 不做凭据转换：token 用 cfg.Targets 里现成的（与 CLI --target 同源同凭据，
//     信任模型零新增）。**token 绝不进日志**
//   - 不自己选路：传输一律取自 s.pool（targetclient）——relay 机器没有 addr，
//     拿 t.Addr 直连构造会退化成 "http:///..."（no Host），选路判据只在池里有一份
//   - 不做重试：转发失败即如实回 502 带原文。重试会让「已登记成功但响应丢了」
//     变成重复登记
package agentd

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
)

// forwardedHeader 是防环标记。一跳封顶：A→B→A 不可能成环。
const forwardedHeader = "X-Handoff-Forwarded"

// forwardBodyLimit 是搬运请求体的上限，与 handleProjectAdd 的 1MB 一致。
const forwardBodyLimit = 1 << 20

// isForwarded 报告该请求是否已经是别的 agentd 转过来的。
//
// 带此头的请求**永不再向外扇出**（scope=all 降级为仅本机、?machine= 忽略）。
func isForwarded(r *http.Request) bool { return r.Header.Get(forwardedHeader) != "" }

// forwardIfRequested 处理显式 ?machine= 路由。
//
// 返回：
//   - true 表示请求已被处理（已转发或已拒绝），调用方必须直接 return
//   - false 表示这是本机的活，继续原来的处理
//
// 注意：
//   - machine 省略或为空串 = 本机（与 Task.Machine 的空串语义一致）
//   - 带转发头时一律返回 false（防环优先于路由）
func (s *Server) forwardIfRequested(w http.ResponseWriter, r *http.Request) bool {
	name := r.URL.Query().Get("machine")
	if name == "" || isForwarded(r) {
		return false
	}
	t, ok := s.conf().Targets[name]
	if !ok {
		s.log.Warn("转发被拒：机器名未在配置中定义", "machine", name, "path", r.URL.Path)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "机器 " + name + " 未在本机配置的 targets 中定义"})
		return true
	}
	c, err := s.pool.For(name)
	if err != nil {
		s.log.Error("转发失败：取目标客户端失败", "machine", name, "relay", t.IsRelay(), "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return true
	}
	s.forwardTo(w, r, name, c, t.Token)
	return true
}

// forwardTo 把请求原样搬到目标机器，并把响应原样回送。
//
// 参数：
//   - name: 目标机器名，只用于日志与错误文案
//   - c: 池选路好的目标客户端——基址与传输都取自它（relay 走隧道、直连走 addr）
//   - token: 目标机器的 Bearer 令牌（只进请求头；与池构造 c 时用的是同一份配置）
//
// 注意：
//   - **不设独立超时**：跟随 r.Context()。项目登记可能触发目标机 clone，耗时
//     以分钟计；§5.2 的 3s 预算约束的是**汇总扇出**，不是这条显式路由。
//     浏览器/CLI 断开时 r.Context() 取消，上游连接随之断开
//   - 转发失败回 502 带原文：这是本机与目标机之间的问题，不能伪装成目标机
//     的业务错误
func (s *Server) forwardTo(w http.ResponseWriter, r *http.Request, name string, c *client.Client, token string) {
	target, err := forwardURL(c.BaseURL(), r.URL)
	if err != nil {
		s.log.Error("转发失败：目标地址不合法", "machine", name, "base_url", c.BaseURL(), "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return
	}
	start := time.Now()
	s.log.Info("转发请求", "machine", name, "method", r.Method, "path", r.URL.Path)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target,
		io.LimitReader(r.Body, forwardBodyLimit))
	if err != nil {
		s.log.Error("转发失败：构造请求", "machine", name, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set(forwardedHeader, "1")

	// 传输取自池客户端：relay 形态经隧道、直连形态带 Proxy:nil 的直连 Transport
	//（agentd↔agentd 永不经代理的纪律随之生效，见 internal/proxycfg 包头）。
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		s.log.Error("转发失败：上游不可达", "machine", name, "path", r.URL.Path,
			"elapsed_ms", time.Since(start).Milliseconds(), "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// 只透传 agentd 自己定义的元数据头；Content-Length 等响应头交给本机
	// net/http 重新编码，否则流式响应的 chunked 传输可能与上游长度冲突。
	for key, values := range resp.Header {
		if !strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Handoff-") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		// 先把响应头推出去；follow 流可能在下一段数据前空闲很久。
		flusher.Flush()
	}

	var n int64
	var cerr error
	if flusher == nil {
		// 理论上 agentd 的 ResponseWriter 支持 Flush；接口不支持时仍沿用
		// io.Copy，避免把转发层绑定到某个具体 ResponseWriter 实现。
		n, cerr = io.Copy(w, resp.Body)
	} else {
		buf := make([]byte, 32*1024)
		for {
			nr, rerr := resp.Body.Read(buf)
			if nr > 0 {
				nw, werr := w.Write(buf[:nr])
				n += int64(nw)
				if werr != nil {
					cerr = werr
					break
				}
				if nw != nr {
					cerr = io.ErrShortWrite
					break
				}
				// 每段都刷新，避免小的 delta 被 net/http 缓冲成一批。
				flusher.Flush()
			}
			if rerr != nil {
				if rerr != io.EOF {
					cerr = rerr
				}
				break
			}
		}
	}
	if cerr != nil {
		// 头已经写出去了，改不了状态码，只能记账
		s.log.Warn("转发响应回送中断", "machine", name, "written", n, "cause", cerr)
	}
	if resp.StatusCode >= 400 {
		s.log.Warn("转发上游返回非 2xx（原样透传，不改写）", "machine", name,
			"path", r.URL.Path, "status", resp.StatusCode,
			"elapsed_ms", time.Since(start).Milliseconds())
		return
	}
	s.log.Info("转发完成", "machine", name, "path", r.URL.Path,
		"status", resp.StatusCode, "elapsed_ms", time.Since(start).Milliseconds())
}

// forwardURL 拼出目标 URL：目标机基址 + 原路径 + 去掉 machine 的查询串。
//
// addr 现在恒为池客户端的 BaseURL()（已带 scheme），normalizeAddr 只是兜底保留。
//
// 为什么要摘掉 machine：它是**本机的路由指令**，不是业务参数。留着它，目标机
// 看到的就是一个「让我转发给我自己」的请求——虽然被防环头挡住，但语义上是脏的。
func forwardURL(addr string, src *url.URL) (string, error) {
	base, err := url.Parse(normalizeAddr(addr))
	if err != nil {
		return "", err
	}
	q := src.Query()
	q.Del("machine")
	base.Path = src.Path
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// normalizeAddr 给缺 scheme 的地址补 http://，与 client.New 的行为一致。
//
// 为什么不直接复用 client 里的那份：那是包内私有的，且这里只需要这三行。
// 若将来 client 导出了它，换过来即可。
func normalizeAddr(addr string) string {
	if !strings.Contains(addr, "://") {
		return "http://" + addr
	}
	return addr
}
