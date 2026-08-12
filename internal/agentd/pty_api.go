// 本文件实现 PTY 终端会话的 HTTP 接口层。
//
// 职责：
//   - REST 三个端点：列会话（含 ?scope=all 扇出）、建会话、显式关会话
//   - 把 base_path / base_kind 归一化成 ptyhost.OpenOptions
//   - 组装会话环境：基础环境 + env_forward 解析结果
//
// 边界：
//   - **不持有任何会话状态**，全部转交 s.pty（internal/ptyhost）
//   - 不认识 PTY 的平台细节；平台不支持时只负责把 ErrNotSupported 翻成 501
//   - WS 数据通道在 pty_ws.go，反代在 forward_ws.go
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/ptyhost"
)

// ptyFanoutBudget 是 ?scope=all 整轮扇出的总预算，与 treeFanoutBudget 同量级：
// 短于任何调用方超时，单台慢机器不拖垮整个列表。
const ptyFanoutBudget = 3 * time.Second

// sessionEnv 组装一个会话的完整环境。
//
// 基础三件：从 agentd 自身环境继承（PATH 已由 B71 的 pathenv.Apply 在任何 fork
// 之前补全过，见 spec §4.1），再钉死 TERM / COLORTERM。
//
// 之后叠加 env_forward 的解析结果：配置为 nil 时用内置默认清单，非 nil 时完全
// 以配置为准（含显式空列表 = 一个都不转发）。**默认值只在这里取，绝不回填进
// cfg**——回填会让下一次 Save 把它落进 config.yaml 顶死旧 agentd（spec §4.2）。
func (s *Server) sessionEnv() []string {
	base := append([]string{}, os.Environ()...)
	base = append(base, "TERM=xterm-256color", "COLORTERM=truecolor")
	names := s.cfg.EnvForward
	if names == nil {
		names = ptyhost.DefaultEnvForward()
	}
	return ptyhost.ResolveEnvForward(names, base, s.log)
}

// resolvePtyBase 把请求里的 base_kind/base_path 归一化成实际 cwd。
//
// **这是参数校验，不是安全边界。** 控制台会话在能力上等价于主令牌
//（POST /api/tasks/{id}/run 就是 sh -c，见 spec §1），白名单挡不住任何有心人
// ——终端里一条 `cd ~` 就出去了。它存在的唯一理由是：防止前端传一个打错的
// 路径、让 shell 起在文件系统某个莫名其妙的角落。因此失败是 400（参数错），
// 不是 403（没权限）。
func (s *Server) resolvePtyBase(r *http.Request, req proto.CreatePtySessionReq) (path, kind string, err error) {
	if req.BaseKind == "home" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", errors.New("服务端无法确定 $HOME: " + herr.Error())
		}
		return home, "home", nil
	}
	if req.BasePath == "" {
		return "", "", errors.New("缺少 base_path 参数")
	}
	root, ok := s.resolveWorkspace(r.Context(), req.BasePath)
	if !ok {
		return "", "", errors.New("base_path " + filepath.Clean(req.BasePath) +
			" 不是本机已探测到的工作树，请从工作树列表里选一个")
	}
	return root, "workspace", nil
}

// handleCreatePtySession 处理 POST /api/pty/sessions[?machine=]。
func (s *Server) handleCreatePtySession(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	var req proto.CreatePtySessionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("建终端会话：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON: " + err.Error()})
		return
	}
	s.log.Info("建终端会话请求", "base_kind", req.BaseKind, "base_path", req.BasePath,
		"size", req.Cols, "rows", req.Rows)

	base, kind, err := s.resolvePtyBase(r, req)
	if err != nil {
		s.log.Warn("建终端会话：基准目录不合法", "base_kind", req.BaseKind,
			"base_path", req.BasePath, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh" // 兜底：托管形态下 $SHELL 常常是空的
	}
	sess, err := s.pty.Open(ptyhost.OpenOptions{
		BasePath: base, BaseKind: kind, Shell: shell,
		Env: s.sessionEnv(), Cols: req.Cols, Rows: req.Rows,
	})
	if errors.Is(err, ptyhost.ErrNotSupported) {
		s.log.Warn("建终端会话：本平台不支持 PTY")
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": ptyhost.ErrNotSupported.Error()})
		return
	}
	if err != nil {
		s.log.Error("建终端会话失败", "base_kind", kind, "cwd", base, "shell", shell, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "开终端失败: " + err.Error()})
		return
	}
	s.log.Info("终端会话已建立", "session", sess.ID, "pid", sess.PID, "cwd", base, "base_kind", kind)
	writeJSON(w, http.StatusOK, ptySessionView(sess, ""))
}

// handleListPtySessions 处理 GET /api/pty/sessions[?scope=all][&machine=]。
//
// 平台不支持时返回**空列表而不是错误**：「本机没有终端会话」是一句真话，
// 让列表接口报错会把前端的会话恢复路径整个打断（spec §7）。
func (s *Server) handleListPtySessions(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	local := make([]proto.PtySession, 0)
	for _, sess := range s.pty.List() {
		local = append(local, ptySessionView(sess, ""))
	}
	if r.URL.Query().Get("scope") != "all" || isForwarded(r) {
		// 带转发头时降级为仅本机：防环优先于范围，与 projectfanout 同款
		s.log.Info("终端会话列表", "count", len(local), "scope", "local")
		writeJSON(w, http.StatusOK, proto.PtySessionsResp{Sessions: local})
		return
	}
	writeJSON(w, http.StatusOK, s.ptySessionsAll(r, local))
}

// ptySessionsAll 现场扇出所有 target，给每行盖 machine 章。
//
// 现场扇出而非读镜像：终端会话是内存态、生死以秒计，缓存出来的列表会让用户
// 恢复出一批早就没了的 tab。单台失败只影响它自己那一行（machines 里 ok=false）。
func (s *Server) ptySessionsAll(r *http.Request, local []proto.PtySession) proto.PtySessionsResp {
	out := proto.PtySessionsResp{
		Sessions: local,
		Machines: []proto.MachineStatus{{Name: "", Ok: true, FetchedAt: time.Now().UTC()}},
	}
	names := make([]string, 0, len(s.cfg.Targets))
	for name := range s.cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)

	ctx, cancel := context.WithTimeout(r.Context(), ptyFanoutBudget)
	defer cancel()

	type result struct {
		status proto.MachineStatus
		resp   *proto.PtySessionsResp
	}
	results := make([]result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			t := s.cfg.Targets[name]
			st := proto.MachineStatus{Name: name, FetchedAt: time.Now().UTC()}
			resp, err := client.New(t.Addr, t.Token).MarkForwarded().PtySessions(ctx)
			if err != nil {
				s.log.Warn("终端会话扇出失败", "machine", name, "addr", t.Addr, "cause", err)
				st.Error = err.Error()
				results[i] = result{status: st}
				return
			}
			st.Ok = true
			results[i] = result{status: st, resp: resp}
		}(i, name)
	}
	wg.Wait()

	for _, res := range results {
		out.Machines = append(out.Machines, res.status)
		if res.resp == nil {
			continue
		}
		for _, sess := range res.resp.Sessions {
			// 远端答的恒是 machine=""；由**汇总方**盖章为 target 名
			sess.Machine = res.status.Name
			out.Sessions = append(out.Sessions, sess)
		}
	}
	s.log.Info("终端会话汇总完成", "machines", len(out.Machines), "sessions", len(out.Sessions))
	return out
}

// handleDeletePtySession 处理 DELETE /api/pty/sessions/{id}[?machine=]。
func (s *Server) handleDeletePtySession(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	id := r.PathValue("id")
	err := s.pty.Close(id)
	if errors.Is(err, ptyhost.ErrNoSession) {
		s.log.Warn("关终端会话：会话不存在", "session", id)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "终端会话 " + id + " 不存在"})
		return
	}
	if err != nil {
		s.log.Error("关终端会话失败", "session", id, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("终端会话已按请求关闭", "session", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ptySessionView 把 ptyhost 的内存快照翻成线格式，machine 由调用方盖章。
func ptySessionView(s ptyhost.Session, machine string) proto.PtySession {
	return proto.PtySession{
		ID: s.ID, Machine: machine, BasePath: s.BasePath, BaseKind: s.BaseKind,
		Shell: s.Shell, CreatedAt: s.CreatedAt, Cols: s.Cols, Rows: s.Rows,
		Attached: s.Attached, PID: s.PID, ExitCode: s.ExitCode, BytesOut: s.BytesOut,
	}
}
