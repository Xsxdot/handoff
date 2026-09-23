// drop.go —— 把本机拖入的文件写到 PTY 所在机器的 ~/.handoff/drop/。
//
// 职责：
//   - POST /api/drop?name=[&machine=] 接收未编码字节，调用 dropdir.Put
//   - ?machine= 走本文件的 32 MiB 专用转发，不走 forwardTo 的 1 MiB 截断
//
// 边界：
//   - 落盘判断力全在 dropdir；本层只解参、限体、映射错误、转发
//   - 不改 forwardBodyLimit，其它 API 仍 1 MiB
package agentd

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/dropdir"
	"github.com/Xsxdot/handoff/internal/proto"
)

// errDropBadPath 是本机路径上传收到的非绝对路径。
var errDropBadPath = errors.New("路径无效")

// homeDir 取写入目标的 HOME。测试替换它，避免把 t.TempDir 设成进程 HOME
// 把 go 模块缓存在用例目录里、清理失败。
var homeDir = os.UserHomeDir

// handleDropFromPath 处理 POST /api/drop/local?path=&machine=。
//
// 桌面壳的访达拖放只把绝对路径交给页面，WKWebView 拿不到 File。
// 页面把路径交回本机 agentd，由这里读盘再走和 POST /api/drop 相同的落盘或转发。
func (s *Server) handleDropFromPath(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("path")
	name, body, err := readDroppedLocalFile(src)
	if err != nil {
		s.writeLocalDropError(w, src, err)
		return
	}
	q := url.Values{}
	q.Set("name", name)
	if machine := r.URL.Query().Get("machine"); machine != "" {
		q.Set("machine", machine)
	}
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.URL = &url.URL{Path: "/api/drop", RawQuery: q.Encode()}
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/octet-stream")
	if s.forwardDropIfRequested(w, req) {
		return
	}
	s.putDrop(w, name, body)
}

// readDroppedLocalFile 读取用户刚拖进来的本机普通文件。
// 拒绝相对路径、目录和非普通文件，并按未编码字节套用 dropdir 上限。
func readDroppedLocalFile(path string) (string, []byte, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", nil, errDropBadPath
	}
	path = filepath.Clean(path)
	st, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	if !st.Mode().IsRegular() {
		return "", nil, dropdir.ErrNotFile
	}
	if st.Size() > dropdir.MaxBytes {
		return "", nil, dropdir.ErrTooLarge
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	if int64(len(body)) > dropdir.MaxBytes {
		return "", nil, dropdir.ErrTooLarge
	}
	return filepath.Base(path), body, nil
}

func (s *Server) writeLocalDropError(w http.ResponseWriter, path string, err error) {
	base := filepath.Base(path)
	switch {
	case errors.Is(err, errDropBadPath):
		s.log.Warn("drop 本机路径拒绝", "name", base, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径无效"})
	case errors.Is(err, os.ErrNotExist):
		s.log.Warn("drop 本机路径不存在", "name", base)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读不到这个文件"})
	default:
		s.writeDropError(w, base, err)
	}
}

// handleDropPut 处理 POST /api/drop?name=[&machine=]。
//
// 请求体是 application/octet-stream 的原始文件字节，不是 JSON。
func (s *Server) handleDropPut(w http.ResponseWriter, r *http.Request) {
	if s.forwardDropIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	s.log.Info("drop 上传开始", "name", name, "content_length", r.ContentLength)
	if cl := r.ContentLength; cl > dropdir.MaxBytes {
		s.log.Warn("drop 上传拒绝：Content-Length 超限", "name", name, "content_length", cl)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "文件超过 32 MiB"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, dropdir.MaxBytes+1))
	if err != nil {
		s.log.Error("drop 上传读取失败", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取请求体失败"})
		return
	}
	if int64(len(body)) > dropdir.MaxBytes {
		s.log.Warn("drop 上传拒绝：体超限", "name", name, "bytes", len(body))
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "文件超过 32 MiB"})
		return
	}
	s.putDrop(w, name, body)
}

func (s *Server) putDrop(w http.ResponseWriter, name string, body []byte) {
	home, err := homeDir()
	if err != nil || home == "" {
		s.log.Error("drop 上传失败：取 HOME", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法确定 HOME"})
		return
	}
	path, n, err := dropdir.Put(home, name, bytes.NewReader(body))
	if err != nil {
		s.writeDropError(w, name, err)
		return
	}
	s.log.Info("drop 上传完成", "name", name, "path", path, "bytes", n)
	writeJSON(w, http.StatusOK, proto.DropPutResp{Path: path, Bytes: n})
}

func (s *Server) writeDropError(w http.ResponseWriter, name string, err error) {
	switch {
	case errors.Is(err, dropdir.ErrBadName):
		s.log.Warn("drop 上传拒绝：文件名", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件名不合法"})
	case errors.Is(err, dropdir.ErrTooLarge):
		s.log.Warn("drop 上传拒绝：超限", "name", name, "cause", err)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "文件超过 32 MiB"})
	case errors.Is(err, dropdir.ErrNotFile):
		s.log.Warn("drop 上传拒绝：不是文件", "name", name)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不能上传目录"})
	case errors.Is(err, dropdir.ErrNoHome):
		s.log.Error("drop 上传失败：HOME", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法确定 HOME"})
	default:
		s.log.Error("drop 上传失败", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入失败"})
	}
}

// forwardDropIfRequested 在 ?machine= 时把原始字节搬到对端，体上限 32 MiB。
// 超限拒绝，不截断转发。其它路由仍走 forwardTo 的 1 MiB。
func (s *Server) forwardDropIfRequested(w http.ResponseWriter, r *http.Request) bool {
	name := r.URL.Query().Get("machine")
	if name == "" || isForwarded(r) {
		return false
	}
	s.log.Info("drop 开始专用转发", "machine", name, "file", r.URL.Query().Get("name"))
	target, ok := s.conf().Targets[name]
	if !ok {
		s.log.Warn("drop 转发被拒：机器未定义", "machine", name)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "机器 " + name + " 未在本机配置的 targets 中定义"})
		return true
	}
	c, err := s.pool.For(name)
	if err != nil {
		s.log.Error("drop 转发失败：取目标客户端", "machine", name, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return true
	}
	if cl := r.ContentLength; cl > dropdir.MaxBytes {
		s.log.Warn("drop 转发拒绝：Content-Length 超限", "machine", name, "content_length", cl)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "文件超过 32 MiB"})
		return true
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, dropdir.MaxBytes+1))
	if err != nil {
		s.log.Error("drop 转发失败：读取请求", "machine", name, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "读取转发请求失败: " + err.Error()})
		return true
	}
	if int64(len(raw)) > dropdir.MaxBytes {
		s.log.Warn("drop 转发拒绝：体超限", "machine", name, "bytes", len(raw))
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "文件超过 32 MiB"})
		return true
	}
	dest, err := forwardURL(c.BaseURL(), r.URL)
	if err != nil {
		s.log.Error("drop 转发失败：目标地址", "machine", name, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return true
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, dest, bytes.NewReader(raw))
	if err != nil {
		s.log.Error("drop 转发失败：构造请求", "machine", name, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return true
	}
	if target.Token != "" {
		req.Header.Set("Authorization", "Bearer "+target.Token)
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	} else {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	req.Header.Set(forwardedHeader, "1")
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		s.log.Error("drop 转发失败：上游不可达", "machine", name, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return true
	}
	defer resp.Body.Close()
	copyForwardHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	payload, err := io.ReadAll(io.LimitReader(resp.Body, forwardBodyLimit+1))
	if err != nil {
		s.log.Error("drop 转发失败：读响应", "machine", name, "cause", err)
		return true
	}
	if len(payload) > forwardBodyLimit {
		s.log.Warn("drop 转发：目标响应超过 1 MiB 上限", "machine", name)
		return true
	}
	if _, err := w.Write(payload); err != nil {
		s.log.Warn("drop 转发写回失败", "machine", name, "cause", err)
	}
	s.log.Info("drop 转发完成", "machine", name, "status", resp.StatusCode, "bytes", len(raw))
	return true
}
