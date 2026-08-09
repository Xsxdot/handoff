package codex_test

import (
	"os"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

func TestWriteServeScriptShape(t *testing.T) {
	dir := t.TempDir()
	p, err := codex.WriteServeScript(dir, 47777, nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "exec codex app-server --listen 'ws://127.0.0.1:47777'") {
		t.Fatalf("启动命令形态不对:\n%s", s)
	}
	if !strings.Contains(s, "serve.log") {
		t.Fatalf("未重定向到 serve.log:\n%s", s)
	}
	if strings.Contains(s, "CODEX_HOME") {
		t.Fatalf("脚本不得设置 CODEX_HOME（本设计复用用户级 ~/.codex）:\n%s", s)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("权限 = %v，应为 0600", fi.Mode().Perm())
	}
}

// B19：env 注入的值必须单引号包裹（Go 侧已展开一次，不能让 shell 再展开）
func TestWriteServeScriptQuotesEnvValues(t *testing.T) {
	dir := t.TempDir()
	p, err := codex.WriteServeScript(dir, 1234, []string{
		"API_BASE=https://a.example.com",
		"WEIRD=$HOME/x y",
		"MALFORMED_NO_EQUALS",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.Contains(s, "export API_BASE='https://a.example.com'") {
		t.Fatalf("普通值未正确导出:\n%s", s)
	}
	if !strings.Contains(s, "export WEIRD='$HOME/x y'") {
		t.Fatalf("含 $ 的值必须单引号包裹防二次展开:\n%s", s)
	}
	if strings.Contains(s, "MALFORMED_NO_EQUALS") {
		t.Fatalf("非 KEY=VALUE 条目必须跳过，不得污染脚本语法:\n%s", s)
	}
}

// CODEX_HOME 必须被丢弃：它一旦生效会把 executor 换到空 home，凭据/插件/sessions 全落空
func TestWriteServeScriptDropsCodexHome(t *testing.T) {
	dir := t.TempDir()
	p, err := codex.WriteServeScript(dir, 1234, []string{
		"CODEX_HOME=/tmp/hijack",
		"KEEP=1",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Contains(s, "/tmp/hijack") || strings.Contains(s, "CODEX_HOME") {
		t.Fatalf("CODEX_HOME 必须被丢弃:\n%s", s)
	}
	if !strings.Contains(s, "export KEEP='1'") {
		t.Fatalf("丢弃 CODEX_HOME 不得牵连其他变量:\n%s", s)
	}
}
