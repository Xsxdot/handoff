package codex_test

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/codex"
)

// TestServeSpecShape 钉住启动形态：codex app-server --listen、不设 CODEX_HOME、
// env 原样透传。
func TestServeSpecShape(t *testing.T) {
	spec := codex.ServeSpecForTest("/repo", "/task", 47777, []string{
		"API_BASE=https://a.example.com",
		"CODEX_HOME=/tmp/hijack",
	})
	joined := strings.Join(spec.Argv, " ")
	if !strings.Contains(joined, "app-server") || !strings.Contains(joined, "--listen ws://127.0.0.1:47777") {
		t.Fatalf("启动命令形态不对: %v", spec.Argv)
	}
	var gotAPI, gotHijack bool
	for _, kv := range spec.Env {
		switch kv {
		case "API_BASE=https://a.example.com":
			gotAPI = true
		case "CODEX_HOME=/tmp/hijack":
			gotHijack = true
		}
	}
	if !gotAPI {
		t.Fatalf("env 必须原样透传，实得 %v", spec.Env)
	}
	// CODEX_HOME 必须被丢弃：它一旦生效会把 executor 换到空 home，凭据/插件/sessions 全落空
	if gotHijack {
		t.Fatalf("CODEX_HOME 必须被丢弃: %v", spec.Env)
	}
	if spec.LockPath == "" || spec.InfoPath == "" || !spec.Sentinel {
		t.Fatalf("LockPath/InfoPath/Sentinel 必填: %+v", spec)
	}
}
