// 本文件实现 owner path preview 的最小静态 HTTP 服务。
//
// 职责：只绑定 127.0.0.1:0、服务 workspace subtree、返回 owner localhost entry URL。
// 边界：不占用户 port、不做远端 transport、不启动浏览器；proxy/launcher 属 U3。
package agentd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type previewStatic struct {
	log *slog.Logger
}

// NewPreviewStaticServer returns the production owner-side static server.
func NewPreviewStaticServer(log *slog.Logger) PreviewStaticServer {
	if log == nil {
		log = slog.Default()
	}
	return &previewStatic{log: log}
}

func (s *previewStatic) Start(ctx context.Context, workspaceRoot, relativePath string) (string, func() error, error) {
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", nil, fmt.Errorf("解析静态服务 workspace root: %w", err)
	}
	if _, err := validatePreviewRelativePath(root, relativePath); err != nil {
		return "", nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("绑定预览静态服务回环端口: %w", err)
	}
	server := &http.Server{Handler: securePreviewFileHandler(root)}
	var once sync.Once
	stop := func() error {
		var stopErr error
		once.Do(func() {
			stopErr = server.Shutdown(context.Background())
		})
		return stopErr
	}
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("预览静态服务退出异常", "operation", "serve", "root", root, "cause", err)
		}
	}()
	path := "/" + strings.TrimPrefix(filepath.ToSlash(relativePath), "/")
	entry := (&url.URL{Scheme: "http", Host: "localhost:" + fmt.Sprint(ln.Addr().(*net.TCPAddr).Port), Path: path}).String()
	s.log.Info("预览静态服务启动成功", "operation", "start", "addr", ln.Addr().String(), "entry_url", entry)
	_ = ctx
	return entry, stop, nil
}

func securePreviewFileHandler(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/"))
		if rel == "" {
			rel = "."
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, rel))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		relRoot, err := filepath.Rel(root, resolved)
		if err != nil || relRoot == ".." || strings.HasPrefix(relRoot, ".."+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
