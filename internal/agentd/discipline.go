// discipline.go —— 控制台的纪律配置 HTTP 面（B157 立面；B229 退役本地目录）。
//
// 职责：
//   - GET  /api/discipline            返回恒空的内置/文件清单 + 每个 executor 的档位
//   - GET  /api/discipline/file       拒服务：纪律块权威副本已入账本（P4 裁决 a）
//   - PUT  /api/discipline/file       拒服务：同上，且绝不在磁盘留下新文件
//   - PUT  /api/discipline/mapping    整段替换该机的 discipline 配置段（③层语义不动）
//
// 边界：
//   - B229 起执行机不做任何纪律解析（收文即用）：目录 <DataDir>/discipline 不再被
//     读取或写入，Resolver/builtin/文件读写面已随本卡拆除
//   - Builtins/Files 字段类型保留、值恒空数组——防 TS 类型断裂，界面改造归后续卡
//   - 跨机由 forwardIfRequested 处理（?machine=），本文件只管本机
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// retiredDisciplineFileMsg 是 file 两端点拒服务的统一文案。
//
// 为什么点名 CLI 命令而不是只说「不支持」：控制台的编辑入口从此永远不生效，
// 必须当场告诉用户正确的替代路径（handoff discipline put 写账本），否则就是
// P4 被否方案 (b) 的静默失败换了个马甲。
const retiredDisciplineFileMsg = "纪律块已入账本：请用 handoff discipline put/get/list 管理，本机的纪律块文件面已退役"

// handleDisciplineGet 处理 GET /api/discipline[?machine=]。
//
// 响应：
//   - 200 proto.DisciplineResp：Builtins/Files 恒为空数组（类型保留防 TS 断裂）；
//     Bindings 照常反映机器级映射（③层语义本轮不动）
//   - 503：manager 未就绪（与 dispatch 等路由同款：executor 名单来自 manager）
func (s *Server) handleDisciplineGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Info("纪律配置查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("纪律配置查询：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp := proto.DisciplineResp{
		// Dir 是退役目录的展示字段：不再读取该目录，置空让界面显示留白，
		// 避免把一个已经没有任何作用的路继续当成有效信息呈现
		Dir:      "",
		Builtins: []proto.DisciplineBuiltin{},
		Files:    []proto.DisciplineFile{},
		Bindings: s.disciplineBindings(),
	}
	s.log.Info("纪律配置查询完成", "bindings", len(resp.Bindings))
	writeJSON(w, http.StatusOK, resp)
}

// disciplineBindings 把「已注册的 executor ∪ 配置里已出现的键」折成档位列表，按名字升序。
//
// 三档映射：键不存在 → default；值为空串 → off；否则 → file。
// DefaultTier 恒空串：内置档位表已随 B229 删除，「改回默认」不再对应任何正文，
// 字段保留只为 TS 类型不断裂。
func (s *Server) disciplineBindings() []proto.DisciplineBinding {
	m := s.conf().Discipline
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

	out := make([]proto.DisciplineBinding, 0, len(names))
	for _, n := range names {
		b := proto.DisciplineBinding{Executor: n}
		v, configured := m[n]
		switch {
		case !configured:
			b.Mode = proto.DisciplineModeDefault
		case strings.TrimSpace(v) == "":
			b.Mode = proto.DisciplineModeOff
		default:
			b.Mode, b.File = proto.DisciplineModeFile, strings.TrimSpace(v)
		}
		out = append(out, b)
	}
	return out
}

// handleDisciplineFileRead 处理 GET /api/discipline/file?name=[&machine=]。
//
// B229 P4 裁决 (a)：拒服务。路由与 TS 类型保留不动 UI（界面改造归后续卡），
// 但绝不读退役目录——返回可行动错误，指路 handoff discipline put。
func (s *Server) handleDisciplineFileRead(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Warn("纪律块读文件被拒：文件面已退役", "name", r.URL.Query().Get("name"))
	writeJSON(w, http.StatusGone, map[string]string{"error": retiredDisciplineFileMsg})
}

// handleDisciplineFileWrite 处理 PUT /api/discipline/file?name=[&machine=]。
//
// B229 P4 裁决 (a)：拒服务。被否方案是「继续写死目录」——那会造出「编辑成功但
// 永不生效」的静默失败通道，并把刚埋掉的漂移载体重新变成写入面。这里连名字
// 校验都不做：任何写入尝试一律 410，磁盘上不出现新文件。
func (s *Server) handleDisciplineFileWrite(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Warn("纪律块写文件被拒：文件面已退役", "name", r.URL.Query().Get("name"))
	writeJSON(w, http.StatusGone, map[string]string{"error": retiredDisciplineFileMsg})
}

// legacyDisciplineFilePath 换算机器级映射指向的旧文件路径（只读校验用）。
//
// 为什么它还活着：PUT /api/discipline/mapping 的保存语义**不动**（B229 契约 §2.7，
// ③层映射 Out of Scope），file 档仍要求目标文件此刻可读——这是既有行为，不是新
// 解析入口。目录布局知识内联于此并注明来历，避免误以为它可复用。
func legacyDisciplineFilePath(dataDir, name string) string {
	return filepath.Join(dataDir, "discipline", name)
}

// validateLegacyDisciplineFileName 校验映射里的文件名仍是「纯文件名」，
// 与退役前 resolvePath 的判据一致（拒分隔符/./..），保存语义不动的一部分。
func validateLegacyDisciplineFileName(name string) error {
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') ||
		name == "" || name == "." || name == ".." {
		return fmt.Errorf("%q 不能含路径分隔符或为空", name)
	}
	return nil
}

// handleDisciplineMapping 处理 PUT /api/discipline/mapping[?machine=]。
//
// 请求体 proto.DisciplineMappingReq：**整段替换**该机的 discipline 配置段。
//
// 响应：200 proto.DisciplineResp（保存后的最新状态，界面直接拿它刷新）
//
//	400 mode 非法 / executor 为空 / file 档指向不存在的文件
//	503 manager 未就绪
//
// 为什么仍要校验「file 档的文件必须存在」（B229 后）：映射的派发消费方已随
// Resolver 一起退役，但保存语义不动意味着既有承诺原样保留——存进去的东西必须
// 当下真实存在，不许借机放宽。注意这只是保存时的一次性校验。
func (s *Server) handleDisciplineMapping(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	if s.mgr == nil {
		s.log.Warn("纪律映射保存：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req proto.DisciplineMappingReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("纪律映射保存：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	dir := legacyDisciplineFilePath(s.conf().DataDir, "")
	s.log.Info("纪律映射保存请求", "bindings", len(req.Bindings), "dir", dir)

	next := map[string]string{}
	for _, b := range req.Bindings {
		name := strings.TrimSpace(b.Executor)
		if name == "" {
			s.log.Warn("纪律映射保存被拒：executor 名为空", "cause", "executor 名不能为空")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "executor 名不能为空"})
			return
		}
		switch b.Mode {
		case proto.DisciplineModeDefault:
			// 默认档 = 配置里**不出现这个键**，什么都不写。
		case proto.DisciplineModeOff:
			next[name] = ""
		case proto.DisciplineModeFile:
			file := strings.TrimSpace(b.File)
			if verr := validateLegacyDisciplineFileName(file); verr != nil {
				s.log.Warn("纪律映射保存被拒：文件名非法", "executor", name, "file", file, "cause", verr)
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("%s 指定的纪律块文件不可用：%v", name, verr)})
				return
			}
			path := legacyDisciplineFilePath(s.conf().DataDir, file)
			// 只查存在性与可读性，与退役前的读文件前置判定等价；
			// 正文内容在此不关心（消费方已退役）
			if _, err := os.Stat(path); err != nil {
				werr := err
				if errors.Is(err, os.ErrNotExist) {
					werr = fmt.Errorf("纪律块文件 %s 不存在", path)
				}
				s.log.Warn("纪律映射保存被拒：文件不可用", "executor", name, "file", file, "cause", werr)
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("%s 指定的纪律块文件不可用：%v", name, werr)})
				return
			}
			next[name] = file
		default:
			s.log.Warn("纪律映射保存被拒：档位非法", "executor", name, "mode", b.Mode,
				"cause", "只支持 default/file/off")
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("%s 的档位 %q 非法：只支持 default/file/off", name, b.Mode)})
			return
		}
	}
	if err := s.swapConf(func(c *config.Config) error {
		c.Discipline = next
		return nil
	}); err != nil {
		s.log.Error("纪律映射落盘失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("纪律映射已保存", "configured", len(next))
	s.handleDisciplineGet(w, r) // 回最新状态，界面直接拿它刷新
}
