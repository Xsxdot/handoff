// 本文件实现「在访达中显示」（Reveal in Finder，B108）：平台能力位与
// POST /api/workspaces/reveal 端点。
//
// 职责：
//   - 报告本平台是否支持这个动作（经 /api/status 与 /api/machines 上报）
//   - 校验「调用方在本机、路径在工作树内」后执行 `open -R`
//
// 边界：
//   - **不接 ?machine= 转发**。转发正是这个端点要拒绝的那件事——在别人的
//     机器上弹一个没人看的 Finder 窗口。理由见 spec §3.2
//   - 不做任何写操作；这是一个只读的、给人看的动作
package agentd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// revealSupportedOS 是本平台是否支持「在访达中显示」。
//
// 为什么是 var 而不是 const：**唯一理由是测试缝**。写成 const 的话 false 分支
// 只有在非 macOS 机器上才跑得到，等于永远不测——与 hostguard.go 的
// nicRefreshGap / localIPsFn 同一条理由。
var revealSupportedOS = runtime.GOOS == "darwin"

// revealTimeout 是 `open` 的执行超时。open 只是把请求投给 Finder 就返回，
// 正常在毫秒级；5 秒是给「Finder 没在跑，要先拉起来」留的余量。
const revealTimeout = 5 * time.Second

// revealOpener 执行真正的揭示动作。是 var 而非直接调用，唯一理由是测试缝——
// 用例要断言「收到了哪个绝对路径」且不能真的弹窗。
var revealOpener = func(ctx context.Context, abs string) error {
	// 不经 sh -c：路径作为独立 argv 元素传入，路径里的空格/引号/$ 都不构成注入面
	cmd := exec.CommandContext(ctx, "open", "-R", abs)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// isLoopbackAddr 判断 RemoteAddr（"host:port"）是否来自回环。
//
// 为什么需要这一层：Host 白名单**不止回环**（回环三件套 + cfg.Listen 的 host +
// cfg.Web.AllowedHosts + 通配监听时本机网卡的非回环 IP，见 B104），所以
// 「请求没带 machine」**不等于**「人坐在这台机器前」。判不出来时返回 false
// （拒绝），不放行——放行的代价是在别人机器上弹窗，拒绝的代价只是一条错误提示。
func isLoopbackAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // 没有端口的形态（少见但不该因此放行）
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// revealTarget 把 rel 解析成一个可以交给 open 的绝对路径，并确认它没跑出工作树。
//
// 返回：解析后的绝对路径；越界或不存在时返回错误。
//
// 为什么这里用 EvalSymlinks 而不是 B107 §3.2 规定的 os.OpenRoot：
// os.Root 的产物是 **jail 内的 fd**，而这里要的是一个**交给外部进程的路径字符串**
// （`open -R /dev/fd/N` 没有意义）。所以红线在这里不适用——**这不是遗漏**。
//
// 代价是校验后、open 前存在残留 TOCTOU 窗口。可接受的依据只有这三条：
//  1. 动作是 reveal-only：不执行、不改写、不读内容，上限是「Finder 选中了另一个文件」
//  2. 利用它要先能在工作树内写符号链接，而有这能力的调用方本来就有 PTY 全权 shell
//  3. 后果发生在用户自己看得见的桌面上，不是一条静默通道
//
// **不要把这条让步反向套回 B107**：那边的动作是 RemoveAll/Rename，上限是静默
// 删掉仓库外的文件，两者不在同一个量级。
func revealTarget(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	abs, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.Clean(rel)))
	if err != nil {
		return "", err
	}
	if abs != realRoot && !strings.HasPrefix(abs, realRoot+string(filepath.Separator)) {
		return "", errors.New("路径逃逸被拒绝")
	}
	return abs, nil
}

// handleWorkspaceReveal 处理 POST /api/workspaces/reveal?path=&rel=。
//
// 在**本机**访达中显示工作树内的 rel 条目（B108）。rel 为空串表示工作树根本身
// ——与删除端点不同，揭示根是正当操作。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel:  条目相对路径，可为空
//   - machine: **不支持**。带了就 400，理由见 spec §3.2
//
// 响应：200 返回 {"ok": true}。
func (s *Server) handleWorkspaceReveal(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("rel")
	machine := r.URL.Query().Get("machine")
	s.log.Info("工作树在访达中显示请求", "path", r.URL.Query().Get("path"),
		"rel", rel, "machine", machine, "remote", r.RemoteAddr)

	if machine != "" {
		s.log.Warn("在访达中显示被拒：不支持转发", "machine", machine, "status", http.StatusBadRequest)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "不支持在远程机器上打开访达：那台机器的桌面前没有人"})
		return
	}
	if !revealSupportedOS {
		s.log.Warn("在访达中显示被拒：平台不支持", "goos", runtime.GOOS, "status", http.StatusNotImplemented)
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "这台机器的系统不支持在访达中显示（仅 macOS）"})
		return
	}
	if !isLoopbackAddr(r.RemoteAddr) {
		s.log.Warn("在访达中显示被拒：调用方不在本机", "remote", r.RemoteAddr, "status", http.StatusConflict)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上"})
		return
	}
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	abs, err := revealTarget(root, rel)
	if err != nil {
		s.log.Warn("在访达中显示被拒：路径不可用", "root", root, "rel", rel,
			"status", http.StatusBadRequest, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), revealTimeout)
	defer cancel()
	if err := revealOpener(ctx, abs); err != nil {
		s.log.Error("在访达中显示失败", "abs", abs, "status", http.StatusInternalServerError, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "open 失败: " + err.Error()})
		return
	}
	s.log.Info("工作树在访达中显示完成", "root", root, "rel", rel, "abs", abs)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
