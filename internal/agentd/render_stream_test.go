package agentd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// newRenderServer 起一个只为 render endpoint 服务的最小 Server，
// 并在其 DataDir 下造出 tasks/<id>/render.log 与对应任务记录。
func newRenderServer(t *testing.T, taskID, content string) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	taskDir := filepath.Join(dir, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("建任务目录失败: %v", err)
	}
	renderPath := filepath.Join(taskDir, "render.log")
	if err := os.WriteFile(renderPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写 render.log 失败: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: taskDir, State: proto.TaskStateRunning})
	cfg := &config.Config{Token: "test", DataDir: dir}
	s := NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := testhttp.NewServer(t, s.Handler())
	return ts, renderPath
}

// renderGet 带鉴权头 GET render endpoint（render 挂在带 Bearer 鉴权的全路由下）。
func renderGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("建请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	return resp
}

func TestRenderReturnsFromOffset(t *testing.T) {
	ts, _ := newRenderServer(t, "t1", "0123456789")
	resp := renderGet(t, ts.URL+"/api/tasks/t1/render?offset=4")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	// 响应头必须带当前文件大小：客户端断线后凭「已收字节数」续传要用它对齐
	if got := resp.Header.Get("X-Handoff-Render-Size"); got != "10" {
		t.Fatalf("X-Handoff-Render-Size = %q, want \"10\"", got)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "456789" {
		t.Fatalf("按 offset 截取错误，实得 %q", b)
	}
}

func TestRenderTailStartsNearEnd(t *testing.T) {
	ts, _ := newRenderServer(t, "t1", strings.Repeat("x", 100)+"TAIL")
	resp := renderGet(t, ts.URL+"/api/tasks/t1/render?tail=4")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "TAIL" {
		t.Fatalf("tail 未从尾部回溯，实得 %q", b)
	}
}

func TestRenderFollowStreamsAppends(t *testing.T) {
	ts, renderPath := newRenderServer(t, "t1", "head")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/api/tasks/t1/render?offset=0&follow=1", nil)
	req.Header.Set("Authorization", "Bearer test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("读首段失败: %v", err)
	}
	if string(buf) != "head" {
		t.Fatalf("首段错误: %q", buf)
	}
	// follow=1 时连接不关：追加内容必须继续流出来
	f, err := os.OpenFile(renderPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("打开 render.log 失败: %v", err)
	}
	if _, err := f.WriteString("MORE"); err != nil {
		t.Fatalf("追加失败: %v", err)
	}
	f.Close()

	more := make([]byte, 4)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(resp.Body, more); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("读追加段失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow 模式未在 5s 内送出追加内容")
	}
	if string(more) != "MORE" {
		t.Fatalf("追加段错误: %q", more)
	}
}

func TestRenderMissingFileIsEmptyNot404(t *testing.T) {
	ts, renderPath := newRenderServer(t, "t1", "")
	if err := os.Remove(renderPath); err != nil {
		t.Fatalf("删 render.log 失败: %v", err)
	}
	resp := renderGet(t, ts.URL+"/api/tasks/t1/render")
	defer resp.Body.Close()
	// 任务刚 dispatch、模型还没吐第一个字时 render.log 尚不存在。
	// 这不是错误——attach 必须能连上并等着，而不是报 404 让人以为任务不对
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("render.log 不存在时应返回 200 空内容，实得 %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Handoff-Render-Size"); got != "0" {
		t.Fatalf("X-Handoff-Render-Size = %q, want \"0\"", got)
	}
	b, _ := io.ReadAll(resp.Body)
	if len(b) != 0 {
		t.Fatalf("应为空内容，实得 %q", b)
	}
}

func TestRenderRejectsUnknownTask(t *testing.T) {
	ts, _ := newRenderServer(t, "t1", "x")
	resp := renderGet(t, ts.URL+"/api/tasks/999/render")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知任务应 404，实得 %d", resp.StatusCode)
	}
}
