// 本文件实现 agentd → agentd 的请求转发基座。
//
// 职责：
//   - 判定一个请求是否要转发（显式 ?machine= / 按任务 id 路由都走这里的搬运）
//   - 原样搬运：方法、路径、请求体、查询参数（去掉路由用的 machine）
//   - 原样回送：状态码、Content-Type、响应体一律不改写
//   - 防环：转发请求带 X-Handoff-Forwarded: 1，带此头的请求一律本机处理
//
// 边界：
//   - 不解释业务语义：登记契约由目标机器解释，本机只做搬运，不加校验也不加解释
//   - 不做凭据转换：用 cfg.Targets 里现成的 addr+token（与 CLI --target 同源
//     同凭据，信任模型零新增）。**token 绝不进日志**
//   - 不做重试：转发失败即如实回 502 带原文。重试会让「已登记成功但响应丢了」
//     变成重复登记
package agentd

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	s.forwardTo(w, r, name, t.Addr, t.Token)
	return true
}

// forwardTo 把请求原样搬到目标机器，并把响应原样回送。
//
// 参数：
//   - name/addr/token: 目标机器的名字、地址与令牌（token 只进请求头）
//
// 注意：
//   - **不设独立超时**：跟随 r.Context()。项目登记可能触发目标机 clone，耗时
//     以分钟计；§5.2 的 3s 预算约束的是**汇总扇出**，不是这条显式路由。
//     浏览器/CLI 断开时 r.Context() 取消，上游连接随之断开
//   - 转发失败回 502 带原文：这是本机与目标机之间的问题，不能伪装成目标机
//     的业务错误
func (s *Server) forwardTo(w http.ResponseWriter, r *http.Request, name, addr, token string) {
	target, err := forwardURL(addr, r.URL)
	if err != nil {
		s.log.Error("转发失败：目标地址不合法", "machine", name, "addr", addr, "cause", err)
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

	resp, err := http.DefaultClient.Do(req)
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
	w.WriteHeader(resp.StatusCode)
	n, cerr := io.Copy(w, resp.Body)
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

// forwardURL 拼出目标 URL：目标机地址 + 原路径 + 去掉 machine 的查询串。
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
