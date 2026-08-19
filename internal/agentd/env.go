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
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
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

// handleEnvKeys 处理 GET /api/env/file/keys?name=[&machine=]。
//
// 响应：200 proto.EnvKeysResp / 400 名字非法或**语法错误** / 404 文件不存在。
//
// 这是 Env 分区的默认视图，也是 spec §7 凭据边界的落点：**响应结构里没有
// 任何字段承载值**，只有 key 名、值的字节长度与重复标记。日常最高频的问题
// 是「这台机给某个 executor 注了哪些变量」，回答它不需要看见任何一个值。
//
// 注意：Parse 的 lookup 传 **nil**。展开时不查 agentd 自己的环境变量——否则
// 同一个文件在不同机器上会显示出不同的值长度，既误导又多泄露一层信息。
// 引用了外部变量的值因此会显示为 0，这是刻意的，不是 bug。
func (s *Server) handleEnvKeys(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := envfile.Dir(s.conf().DataDir)
	s.log.Info("env 变量清单请求", "dir", dir, "name", name)

	content, _, size, err := envfile.Read(dir, name)
	if err != nil {
		s.writeEnvReadError(w, dir, name, err)
		return
	}
	kvs, dups, err := envfile.Parse(bytes.NewReader([]byte(content)), nil)
	if err != nil {
		// 原样透传：Parse 的错误自带行号与原行，是用户改对的唯一线索。
		// 错误正文会回给用户，但不把它作为日志 cause，避免非法行里的敏感片段
		// 进入 agentd 日志。
		s.log.Warn("env 变量清单：解析失败", "dir", dir, "name", name, "bytes", size)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dupSet := make(map[string]bool, len(dups))
	for _, k := range dups {
		dupSet[k] = true
	}
	resp := proto.EnvKeysResp{Keys: make([]proto.EnvKey, 0, len(kvs))}
	for _, kv := range kvs {
		resp.Keys = append(resp.Keys, proto.EnvKey{
			Key: kv.Key, ValueBytes: len(kv.Value), Duplicate: dupSet[kv.Key],
		})
	}
	// 日志只记数量与字节数：key 名不是秘密，但一屏几十个没价值；值绝不出现。
	s.log.Info("env 变量清单完成", "dir", dir, "name", name,
		"keys", len(resp.Keys), "dups", len(dups), "bytes", size)
	writeJSON(w, http.StatusOK, resp)
}

// writeEnvReadError 把 envfile.Read 的错误映射成 HTTP 响应（三处读路径共用）。
//
// 为什么抽出来：keys / file 两个 GET 与 mapping 的存在性校验对同一组错误要给
// 同一组状态码，散在三处必然漂移。
func (s *Server) writeEnvReadError(w http.ResponseWriter, dir, name string, err error) {
	switch {
	case errors.Is(err, envfile.ErrBadName):
		s.log.Warn("env 读文件被拒：名字非法", "name", name)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, fs.ErrNotExist):
		s.log.Warn("env 读文件：目标不存在", "dir", dir, "name", name)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "env 文件不存在"})
	default:
		s.log.Error("env 读文件失败", "dir", dir, "name", name)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取 env 文件失败"})
	}
}

// handleEnvFileRead 处理 GET /api/env/file?name=[&machine=]。
//
// 响应：200 proto.FileRead / 400 名字非法 / 404 文件不存在。
//
// **这条会把含值的全文交给浏览器**，且这是刻意的——不然没法编辑。默认视图
// 走 keys 端点；界面只在用户点「编辑正文」时调这条。spec §7 已写明这条边界：
// 掩码防的是肩窥、截图、录屏、整页粘贴，不是防浏览器本身，更不是加密。
func (s *Server) handleEnvFileRead(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := envfile.Dir(s.conf().DataDir)
	s.log.Info("env 读文件请求（含值全文）", "dir", dir, "name", name)

	content, sha, size, err := envfile.Read(dir, name)
	if err != nil {
		s.writeEnvReadError(w, dir, name, err)
		return
	}
	s.log.Info("env 读文件完成", "dir", dir, "name", name, "bytes", size, "sha256", shortHash(sha))
	writeJSON(w, http.StatusOK, proto.FileRead{Content: content, Size: size, SHA256: sha})
}

// handleEnvFileWrite 处理 PUT /api/env/file?name=[&machine=]。
//
// 请求体 proto.FileWriteReq：base_sha256 为空串 = 新建（目标必须不存在）。
//
// 响应：200 FileWriteResp / 400 名字非法、超限或**语法错误** / 409 撞名或冲突
// （带磁盘现状）/ 404 目标在编辑期间被删。
//
// **写前必须解析**（与纪律块的唯一实质差异）：纪律块写错了模型顶多读到一段
// 怪话；env 写错了，症状是「代理配了但连不上」「go test 突然全红」，离根因
// 十万八千里。Parse 已经能产出带行号的错误，白不用。
//
// 重复键**不拦**：Resolver 的既有行为是 WARN + 后者覆盖，界面照此标注即可。
func (s *Server) handleEnvFileWrite(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := envfile.Dir(s.conf().DataDir)

	var req proto.FileWriteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("env 写文件请求体解析失败", "name", name)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 正文含值，日志只记长度与前置哈希，绝不记内容。
	s.log.Info("env 写文件请求", "dir", dir, "name", name,
		"bytes", len(req.Content), "base", shortHash(req.BaseSHA256))

	// 语法门在落盘之前：写坏的文件不该进磁盘。
	if _, _, perr := envfile.Parse(bytes.NewReader([]byte(req.Content)), nil); perr != nil {
		s.log.Warn("env 写文件被拒：语法错误", "dir", dir, "name", name, "bytes", len(req.Content))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": perr.Error()})
		return
	}

	sha, size, err := envfile.Write(dir, name, req.Content, req.BaseSHA256)
	if err != nil {
		switch {
		case errors.Is(err, envfile.ErrBadName), errors.Is(err, envfile.ErrTooLarge):
			s.log.Warn("env 写文件被拒", "name", name, "bytes", len(req.Content))
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, envfile.ErrExists):
			s.log.Warn("env 写文件被拒：撞名", "dir", dir, "name", name)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, envfile.ErrBaseMismatch):
			// 409 的 body 带磁盘现状：界面据此提供「重新加载」，绝不静默覆盖。
			cur, curSHA, curSize, rerr := envfile.Read(dir, name)
			if rerr != nil {
				s.log.Error("env 写文件冲突后读现状失败", "name", name)
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			s.log.Warn("env 写文件冲突", "dir", dir, "name", name,
				"base", shortHash(req.BaseSHA256), "current", shortHash(curSHA))
			writeJSON(w, http.StatusConflict, proto.FileConflictResp{
				Error:   "env 文件已被改动",
				Current: proto.FileRead{Content: cur, Size: curSize, SHA256: curSHA},
			})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("env 写文件：目标在编辑期间被删", "dir", dir, "name", name)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "env 文件不存在"})
		default:
			s.log.Error("env 写文件失败", "dir", dir, "name", name)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入 env 文件失败"})
		}
		return
	}
	s.log.Info("env 写文件完成", "dir", dir, "name", name, "bytes", size, "sha256", shortHash(sha))
	writeJSON(w, http.StatusOK, proto.FileWriteResp{SHA256: sha, Size: size})
}
