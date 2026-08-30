// 本文件验证 preview CLI 到 owner 的创建请求接缝。
//
// 职责：穿过真实 cobra 命令和 HTTP client，锁定 CLI 工作目录进入
// PreviewOpenReq；不测试 owner 的工作区解析或静态服务行为。

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestPreviewOpenPassesCLIWorkingDirectory(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("create workspace symlink: %v", err)
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	if err := os.Chdir(link); err != nil {
		t.Fatalf("chdir workspace link: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })
	processCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get CLI process directory: %v", err)
	}
	wantCWD, err := filepath.EvalSymlinks(processCWD)
	if err != nil {
		t.Fatalf("eval CLI process directory: %v", err)
	}

	var got proto.PreviewOpenReq
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/previews" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read preview create body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode preview create body: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"preview-cli","entry_url":"http://localhost:5173","cwd":"/owner","created_at":"2026-08-30T00:00:00Z","ttl_seconds":7200}`)
	}))
	t.Cleanup(ts.Close)

	resetFlags(t)
	cfgPath := writeStatusConfig(t)
	rootCmd.SetArgs([]string{"preview", "open", "--port", "5173", "--config", cfgPath, "--agentd", ts.URL})
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := ExecuteContext(context.Background()); err != nil {
		t.Fatalf("preview open: %v; stderr=%s", err, errOut.String())
	}
	if got.CWD != wantCWD {
		t.Fatalf("preview create cwd=%q, want CLI cwd after EvalSymlinks %q; body=%s", got.CWD, wantCWD, out.String())
	}
}
