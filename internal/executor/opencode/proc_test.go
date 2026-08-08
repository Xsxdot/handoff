// proc 测试：writeServeScript/serveTmuxArgs/serveLogTail/shellQuote 的纯函数
// 断言——密码不进 argv、脚本 0600、serve.log 尾部读取、shell 引号转义。
// tmux + opencode 二进制的真机行为不在自动化覆盖（现状保持，e2e 清单兜底）。
package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteServeScript 验证启动脚本生成：
// 内容含 export 密码/配置行与 exec opencode serve + tee 落盘 serve.log，
// 文件权限 0600、位于 taskDir 下。
func TestWriteServeScript(t *testing.T) {
	taskDir := t.TempDir()
	configPath := filepath.Join(taskDir, "opencode.json")
	const password = "pw-secret-xyz"
	path, err := writeServeScript(taskDir, 35123, password, configPath)
	if err != nil {
		t.Fatalf("writeServeScript: %v", err)
	}
	if path != filepath.Join(taskDir, serveScriptFileName) {
		t.Errorf("脚本路径=%q，期望 %q", path, filepath.Join(taskDir, serveScriptFileName))
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 脚本: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("脚本权限=%o，期望 600（含密码，防止本机其他用户读取）", st.Mode().Perm())
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读脚本: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		"#!/bin/sh",
		"export OPENCODE_SERVER_PASSWORD='" + password + "'",
		"export OPENCODE_CONFIG='" + configPath + "'",
		"exec opencode serve --port 35123 --hostname 127.0.0.1 2>&1 | tee -a '" +
			filepath.Join(taskDir, serveLogFileName) + "'",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("脚本应含 %q，实际:\n%s", want, content)
		}
	}
}

// TestWriteServeScriptShellQuotes 验证路径/密码含单引号时正确转义
// （'\'' 序列），不转义会改变脚本语义（提前截断 export 行）。
func TestWriteServeScriptShellQuotes(t *testing.T) {
	taskDir := t.TempDir()
	configPath := "weird'name/opencode.json"
	path, err := writeServeScript(taskDir, 1, "p'w", configPath)
	if err != nil {
		t.Fatalf("writeServeScript: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读脚本: %v", err)
	}
	if !strings.Contains(string(b), `export OPENCODE_CONFIG='weird'\''name/opencode.json'`) {
		t.Errorf("含引号路径未按 '\\'' 转义，脚本:\n%s", b)
	}
	if !strings.Contains(string(b), `export OPENCODE_SERVER_PASSWORD='p'\''w'`) {
		t.Errorf("含引号密码未按 '\\'' 转义，脚本:\n%s", b)
	}
}

// TestServeTmuxArgsNoSecrets 验证 tmux argv 不含任何秘密：
// 密码/配置经启动脚本注入，argv 只剩脚本路径——Linux /proc/<pid>/cmdline
// 默认全局可读，argv 泄漏 = P0-4 的修复目标本身被破坏。
func TestServeTmuxArgsNoSecrets(t *testing.T) {
	script := "/home/u/.handoff/tasks/abc/run_serve.sh"
	args := serveTmuxArgs("handoff-abc12345", "/repo/path", script)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "OPENCODE_SERVER_PASSWORD") || strings.Contains(joined, "PASSWORD") {
		t.Errorf("tmux argv 不应含密码相关内容: %q", joined)
	}
	for _, a := range args {
		if a == "-e" || strings.HasPrefix(a, "-e ") || strings.HasPrefix(a, "-e=") {
			t.Errorf("tmux argv 不应再用 -e 注入环境（show-environment 可读回）: %q", a)
		}
	}
	if !strings.Contains(joined, script) {
		t.Errorf("argv 应含脚本路径 %q，实际: %q", script, joined)
	}
	if !strings.HasPrefix(joined, "new-session -d -s handoff-abc12345 -c /repo/path") {
		t.Errorf("argv 前缀不合预期: %q", joined)
	}
}

// TestServeLogTail 验证 serve.log 尾部读取：文件缺失返回空串（serve 根本没
// 跑起来时没有诊断内容可给）；短文件全文；长文件只取末尾 500 字节。
func TestServeLogTail(t *testing.T) {
	dir := t.TempDir()

	if got := serveLogTail(filepath.Join(dir, serveLogFileName)); got != "" {
		t.Errorf("文件缺失时应返回空串，实际 %q", got)
	}

	short := "listen tcp: address already in use\n"
	shortPath := filepath.Join(dir, "short.log")
	if err := os.WriteFile(shortPath, []byte(short), 0o600); err != nil {
		t.Fatalf("写 short.log: %v", err)
	}
	if got := serveLogTail(shortPath); got != short {
		t.Errorf("短文件应全文返回，实际 %q", got)
	}

	long := strings.Repeat("x", 600) + "TAIL-MARKER"
	longPath := filepath.Join(dir, "long.log")
	if err := os.WriteFile(longPath, []byte(long), 0o600); err != nil {
		t.Fatalf("写 long.log: %v", err)
	}
	if got := serveLogTail(longPath); !strings.HasSuffix(got, "TAIL-MARKER") || len(got) != 500 {
		t.Errorf("长文件应取末尾 500 字节（以 TAIL-MARKER 结尾），实际长度 %d", len(got))
	}
}

// TestShellQuote 验证单引号 shell 字面量包装：普通串加引号、内含单引号转义、
// 空串不 panic。
func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/plain/path":     "'/plain/path'",
		"a'b":             `'a'\''b'`,
		"it's here":       `'it'\''s here'`,
		"":                "''",
		"pw-!@#$%^&*()_+": "'pw-!@#$%^&*()_+'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q)=%q，期望 %q", in, got, want)
		}
	}
}
