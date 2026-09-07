package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/spf13/cobra"
)

func TestStopCLIRetainCopy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks/T1/stop" || r.Method != http.MethodPost {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"stopped","worktree_removed":false}`))
	}))
	t.Cleanup(ts.Close)
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	if err := runStop(cmd, client.New(ts.URL, "tok"), "T1"); err != nil {
		t.Fatalf("runStop: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "留存") {
		t.Fatalf("成功未删树应提示留存，实得 %q", got)
	}
	if strings.Contains(got, "已删除") || strings.Contains(got, "清理失败") {
		t.Fatalf("不得把 false 说成已删除或清理失败，实得 %q", got)
	}
}
