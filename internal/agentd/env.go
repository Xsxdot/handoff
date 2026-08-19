// env.go —— 控制台的 env 文件配置 HTTP 面（B158）。
//
// 职责：
//   - GET  /api/env                列出该机 env 文件与每个 executor 的档位
//   - GET  /api/env/file/keys      解析出的变量清单（**只有 key 名与值长度**）
//   - GET  /api/env/file           读单个 env 文件正文（含值，仅编辑时调用）
//   - PUT  /api/env/file           写单个 env 文件（前置哈希 + **写前解析校验**）
//   - PUT  /api/env/mapping        整段替换该机的 env 配置段
//
// 边界：
//   - 文件判断力全在 internal/envfile（名字校验、大小上限、冲突判定），本层
//     只做 HTTP 编解码与错误映射，**中文错误原文原样透传**
//   - 跨机由 forwardIfRequested 处理（?machine=），本文件只管本机
//   - **任何路径都不得把 env 的值写进日志或响应**（正文读写端点除外——它就是
//     为编辑而存在的，见 spec §7 的诚实边界）
//   - 两档语义：键不存在 = 不注入；值为文件名 = 读该文件。**绝不写空串**
package agentd

import (
	"net/http"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/proto"
)

// handleEnvGet 处理 GET /api/env[?machine=]。
//
// 响应：
//   - 200 proto.EnvResp
//   - 503：manager 未就绪（与 dispatch 等路由同款：executor 名单来自 manager）
func (s *Server) handleEnvGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Info("env 配置查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("env 配置查询：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	dir := envfile.Dir(s.conf().DataDir)
	files, err := envfile.List(dir)
	if err != nil {
		s.log.Error("env 配置查询：列举文件失败", "dir", dir, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := proto.EnvResp{
		Dir:      dir,
		Files:    make([]proto.EnvFile, 0, len(files)),
		Bindings: s.envBindings(),
	}
	for _, f := range files {
		resp.Files = append(resp.Files, proto.EnvFile{Name: f.Name, Size: f.Size, SHA256: f.SHA256})
	}
	s.log.Info("env 配置查询完成", "dir", dir, "files", len(resp.Files), "bindings", len(resp.Bindings))
	writeJSON(w, http.StatusOK, resp)
}

// envBindings 把「已注册的 executor ∪ 配置里已出现的键」折成档位列表，按名字升序。
//
// **两档映射**：键不存在（或值 trim 后为空）→ off；否则 → file。
//
// 为什么空串也归到 off：历史配置里可能已经写着空串（手改 yaml 留下的），把它
// 显示成「指向一个名字为空的文件」只会让人困惑。**但保存时绝不写回空串**——
// 读宽写严，脏数据经过一次保存就被洗掉。
func (s *Server) envBindings() []proto.EnvBinding {
	m := s.conf().Env
	seen := map[string]bool{}
	names := []string{}
	for _, n := range s.mgr.ExecutorNames() {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for n := range m {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)

	out := make([]proto.EnvBinding, 0, len(names))
	for _, n := range names {
		b := proto.EnvBinding{Executor: n}
		if v := strings.TrimSpace(m[n]); v == "" {
			b.Mode = proto.EnvModeOff
		} else {
			b.Mode, b.File = proto.EnvModeFile, v
		}
		out = append(out, b)
	}
	return out
}
