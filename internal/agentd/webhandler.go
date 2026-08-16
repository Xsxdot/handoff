// 本文件持有 Web 控制台静态资源的伺服逻辑。
//
// 职责：
//   - 把 internal/webui 提供的 fs.FS 伺服出去：命中真实文件就发文件，
//     未命中就回落 index.html（客户端路由的深链接需要这个）。
//   - 按「文件名是否带 hash」决定缓存策略。
//
// 边界：
//   - **不处理 /api、/ws、/console**。那三条由路由层用更精确的模式抢走
//     （Go 1.22 ServeMux 精确前缀优先），本 handler 只兜未知路径。
//     这个边界是承重的：若 /api 未命中回落成 HTML，前端会把 HTML 当 JSON
//     解析，报错信息与真实原因完全无关，排查成本极高。
//   - 不做鉴权。本 handler 挂在 s.auth 之内，到这里的请求已经通过鉴权。
package agentd

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// hashedAsset 匹配 vite 产物的带 hash 文件名，如 app-a1b2c3d4.js。
//
// 为什么用文件名判断而不是按目录：vite 的 assets/ 目录里既有带 hash 的
// 构建产物，也可能有原样拷贝的 public/ 静态文件。按目录会把后者也长缓存，
// 而它们换了内容名字不变，用户会拿着一年不过期的旧文件。
var hashedAsset = regexp.MustCompile(`-[0-9a-zA-Z_]{8,}\.[0-9a-z]+$`)

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNone      = "no-cache"
)

// newSPAHandler 返回伺服单页应用的 handler。
//
// 参数：
//   - fsys: 静态资源根文件系统，index.html 必须位于根。通常来自 webui.FS()。
//   - log:  日志器，用于记录回落与异常。不可为 nil。
//
// 行为：
//   - GET/HEAD 命中真实文件 → 200 + 文件内容 + 按 hash 决定的缓存头
//   - GET/HEAD 未命中 → 200 + index.html + no-cache（客户端路由接管）
//   - 其它方法 → 405
//   - index.html 本身缺失 → 500
//
// 注意：本 handler 假定调用方已保证 /api、/ws、/console 不会落到这里。
func newSPAHandler(fsys fs.FS, log *slog.Logger) http.Handler {
	fileServer := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// 非读方法落到 SPA，多半是路由写错了。回落 200 HTML 会让调用方
			// 误以为写成功，所以这里明确拒绝。
			log.Warn("SPA handler 收到非读请求，已拒绝",
				"method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			serveIndex(w, r, fsys, log)
			return
		}

		// fs.ValidPath 会挡掉 ..、绝对路径等非法形态；不合法直接当未命中走回落，
		// 而不是报错——攻击者不该从错误码里读出目录结构的任何信息。
		if !fs.ValidPath(name) {
			log.Debug("SPA 收到非法路径，按未命中处理", "path", r.URL.Path)
			serveIndex(w, r, fsys, log)
			return
		}

		info, err := fs.Stat(fsys, name)
		if err != nil || info.IsDir() {
			// 未命中真实文件 = 客户端路由的深链接，回落 index.html。
			log.Debug("SPA 未命中静态文件，回落 index.html", "path", r.URL.Path)
			serveIndex(w, r, fsys, log)
			return
		}

		if hashedAsset.MatchString(name) {
			w.Header().Set("Cache-Control", cacheImmutable)
		} else {
			w.Header().Set("Cache-Control", cacheNone)
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndex 发送 index.html，并显式设置 no-cache。
//
// 为什么 index.html 必须 no-cache：它引用的 JS/CSS 文件名带 hash，换版后
// hash 会变。若 index.html 被缓存，浏览器会拿着旧 index 去请求已经不存在的
// 资源，表现为白屏，且在用户手工清缓存之前无法自愈。
func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS, log *slog.Logger) {
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		// 这是「不该发生」的状态：stub 和真实产物都必定含 index.html。
		// 但一旦发生，必须响亮地失败——空 200 只会让浏览器显示白页，
		// 运维从现象上完全看不出根因。
		log.Error("控制台 index.html 缺失，这份二进制的前端资源不完整",
			"path", r.URL.Path, "err", err)
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "console assets missing", http.StatusInternalServerError)
			return
		}
		http.Error(w, "console assets unreadable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cacheNone)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(b); err != nil {
		log.Debug("写 index.html 响应失败（客户端多半已断开）", "err", err)
	}
}
