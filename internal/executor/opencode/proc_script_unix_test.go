//go:build unix

// serve 启动脚本的运行时语义测试（仅 unix，需要真实 sh 执行生成的脚本）。
//
// 覆盖 Minor #1 的两条语义保证：
//   - sh 自身 stderr 落盘 serve.log：脚本首行的 `exec 2>> '<serveLogPath>'`
//     把 shell 级 fd2 指向 serve.log，sh 自身的报错（cd/export 失败、命令
//     找不到等，不经管道）必须出现在 serve.log，否则只进 tmux 窗格、随窗格
//     关闭丢失（P1-8 同类问题）
//   - opencode 的 2>&1 仍走管道进 tee：命令级重定向（2>&1）覆盖 shell 级
//     （2>> 文件）——serve 的 stderr 经 tee 同时出现在脚本 stdout（终端）
//     与 serve.log，新增重定向不改变原语义
package opencode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestServeScriptShStderrLandsInLog 验证 shell 级 `exec 2>> serve.log`：
// sh 自身的内建命令报错（cd 到不存在的目录）必须落盘 serve.log、且不出现在
// 脚本的外部 stderr——修复前这类报错只进 tmux 窗格，随窗格关闭丢失。
//
// 用 cd 内建命令而非语法错误触发：两者都走 sh 自身 stderr，但 cd 的失败是
// 纯运行时行为，bash/dash 两种实现下行为一致、消息格式差异也可接受。
func TestServeScriptShStderrLandsInLog(t *testing.T) {
	taskDir := t.TempDir()
	serveLog := filepath.Join(taskDir, serveLogFileName)
	script := filepath.Join(taskDir, "bad.sh")
	content := "#!/bin/sh\n" +
		"exec 2>> " + shellQuote(serveLog) + "\n" +
		"cd /nonexistent-dir-xyz\n"
	if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试脚本: %v", err)
	}

	cmd := exec.Command("/bin/sh", script)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("cd 失败应使脚本非零退出")
	}
	b, rerr := os.ReadFile(serveLog)
	if rerr != nil {
		t.Fatalf("读 serve.log: %v", rerr)
	}
	if !strings.Contains(string(b), "nonexistent-dir-xyz") {
		t.Errorf("serve.log 应含 sh 自身报错（exec 2>> 接管 shell 级 stderr），实际:\n%s", b)
	}
	if stderr.Len() != 0 {
		t.Errorf("sh 报错不应出现在外部 stderr（已被脚本内 exec 2>> 接管），实际: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("sh 报错不应出现在 stdout，实际: %q", stdout.String())
	}
}

// TestServeScriptServeStderrStillThroughPipe 验证命令级 `2>&1` 仍走管道进 tee：
// 用 PATH 上的 opencode 桩（写 stderr/stdout 后退出 3）执行生成的脚本——
// serve 的 stderr 必须经 tee 同时出现在脚本 stdout（终端可见）与 serve.log，
// 未被脚本首行的 shell 级重定向截走。
func TestServeScriptServeStderrStillThroughPipe(t *testing.T) {
	taskDir := t.TempDir()
	binDir := t.TempDir()
	stub := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf 'serve-stderr-line\\n' >&2\nprintf 'serve-stdout-line\\n'\nexit 3\n"), 0o755); err != nil {
		t.Fatalf("写 opencode 桩: %v", err)
	}

	path, err := writeServeScript(taskDir, 35123, "pw", filepath.Join(taskDir, "opencode.json"))
	if err != nil {
		t.Fatalf("writeServeScript: %v", err)
	}
	serveLog := filepath.Join(taskDir, serveLogFileName)

	cmd := exec.Command("/bin/sh", path)
	cmd.Env = []string{"PATH=" + binDir + ":/usr/bin:/bin"}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("桩 opencode 应正常结束: %v", err)
	}
	for _, line := range []string{"serve-stdout-line", "serve-stderr-line"} {
		if !strings.Contains(stdout.String(), line) {
			t.Errorf("脚本 stdout 应含 %q（serve 的 2>&1 仍走管道进 tee），实际:\n%s", line, stdout.String())
		}
	}
	b, rerr := os.ReadFile(serveLog)
	if rerr != nil {
		t.Fatalf("读 serve.log: %v", rerr)
	}
	for _, line := range []string{"serve-stdout-line", "serve-stderr-line"} {
		if !strings.Contains(string(b), line) {
			t.Errorf("serve.log 应含 %q，实际:\n%s", line, b)
		}
	}
}
