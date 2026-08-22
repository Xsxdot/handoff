// launchers_api.go —— 工作台自定义启动项的配置面（GET/PUT /api/launchers）。
//
// 职责：
//   - GET：读该机启动项列表，并**现算** env_missing
//   - PUT：整段替换，保存前一次性跑完全部校验，成功后回最新列表
//
// 边界：
//   - 不落盘、不校验规则本身——那归 internal/launcher（纯函数，可穷举测试）
//   - 不解析 env 文件内容，只查存在性（envfile.Read）
//   - 跨机由 forwardIfRequested 原样转发，本文件不认识 machine 参数
//
// 日志纪律：**命令原文绝不进日志**。启动项的 Command 可能含凭据
// （`API_KEY=xxx some-cmd` 是常见写法），只记条数与「有几条带命令」。
//
// 形态照 env.go 的 handleEnvGet / handleEnvMapping：同一个心智模型
// （一个配置面一个文件、整段替换、保存时一次性校验、成功后回最新状态）。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/launcher"
	"github.com/Xsxdot/handoff/internal/proto"
)

// handleLaunchersGet 处理 GET /api/launchers[?machine=]。
//
// 读盘失败时返回 500 并带真因（文件坏了要让人看得见，不是静默当空）。
// 文件不存在返回空列表——那是正常起点（launcher.Load 的既有语义）。
func (s *Server) handleLaunchersGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	dataDir := s.conf().DataDir
	list, err := launcher.Load(dataDir)
	if err != nil {
		s.log.Error("读取启动项配置失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := proto.LaunchersResp{Launchers: toProtoLaunchers(envfile.Dir(dataDir), list)}
	s.log.Debug("启动项列表查询完成", "count", len(resp.Launchers))
	writeJSON(w, http.StatusOK, resp)
}

// toProtoLaunchers 把落盘形态换算成线格式，并现算 env_missing。
//
// **env_missing 必须在这里算、不落盘**：落一份派生值就有两个真相——文件里
// 说 false、磁盘上那个 env 文件早已被删。这也是 launcher.Item 刻意不带这个
// 字段的原因（见该类型的注释）。
//
// 返回值恒为非 nil 切片：JSON 里 `[]` 与 `null` 对前端是两种东西，
// 列表接口只该给前者。
func toProtoLaunchers(envDir string, list []launcher.Item) []proto.Launcher {
	out := make([]proto.Launcher, 0, len(list))
	for _, it := range list {
		l := proto.Launcher{Name: it.Name, EnvFile: it.EnvFile, Command: it.Command}
		if it.EnvFile != "" {
			// 只关心「读得到吗」，不关心内容：Read 的错误统一折成 missing
			if _, _, _, err := envfile.Read(envDir, it.EnvFile); err != nil {
				l.EnvMissing = true
			}
		}
		out = append(out, l)
	}
	return out
}

// handleLaunchersPut 处理 PUT /api/launchers[?machine=]：整段替换。
//
// 校验分两段：
//  1. launcher.Validate 的四条纯规则（名字非空/唯一、至少填一个、无路径分隔符）；
//  2. 本层追加的第五条——env 文件必须真实存在。它要读盘，故不在纯函数里。
//
// 两段的顺序不能反：先跑纯规则，「第 3 条名字为空」这类错误才不会被
// 「文件不存在」抢先报出来（用户看到的应该是最根本的那条）。
//
// **客户端送来的 env_missing 一律忽略**：它是 GET 时现算的派生字段，
// 采信客户端等于让前端能往磁盘上写一个谎。
func (s *Server) handleLaunchersPut(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	var req proto.LaunchersReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("启动项保存：请求体无法解析")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	dataDir := s.conf().DataDir
	list := make([]launcher.Item, 0, len(req.Launchers))
	withCmd := 0
	for _, l := range req.Launchers {
		// EnvMissing 有意不读：派生字段不采信客户端
		it := launcher.Item{
			Name: strings.TrimSpace(l.Name), EnvFile: strings.TrimSpace(l.EnvFile),
			Command: strings.TrimSpace(l.Command),
		}
		if it.Command != "" {
			withCmd++
		}
		list = append(list, it)
	}
	s.log.Info("启动项保存请求", "count", len(list), "with_command", withCmd)

	if err := launcher.Validate(list); err != nil {
		// 错误文本是中文原文且已点名是哪一条，直接作为 400 响应体
		s.log.Warn("启动项保存被拒：规则校验不通过", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	envDir := envfile.Dir(dataDir)
	for _, it := range list {
		if it.EnvFile == "" {
			continue
		}
		if _, _, _, err := envfile.Read(envDir, it.EnvFile); err != nil {
			// 点名是哪一条：错误会原样成为 400 响应体。
			s.log.Warn("启动项保存被拒：env 文件不可用", "launcher", it.Name, "file", it.EnvFile)
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("启动项 %q 指定的 env 文件 %q 不可用：%v", it.Name, it.EnvFile, err),
			})
			return
		}
	}
	if err := launcher.Save(dataDir, list); err != nil {
		if errors.Is(err, launcher.ErrInvalid) {
			// 理论上到不了这里（上面已 Validate 过），但 Save 自己也校验，
			// 真到了就说明两处规则漂移了——按 400 如实报，不吞成 500
			s.log.Warn("启动项落盘被拒：规则校验不通过", "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.log.Error("启动项落盘失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("启动项已保存", "count", len(list), "with_command", withCmd)
	s.handleLaunchersGet(w, r) // 回最新状态，界面直接拿它刷新（与 handleEnvMapping 同款）
}
