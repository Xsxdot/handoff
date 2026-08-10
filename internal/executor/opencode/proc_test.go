// proc 测试：serveSpec 的安全边界（密码走 env 不进 argv）、Alive 的两层判定、
// serveLogTail 尾部读取。真实 opencode 二进制的行为不在自动化覆盖（e2e 清单兜底）。
package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/prochost"
)

// TestServeSpecPutsPasswordInEnvNotArgv 钉死安全边界：
// 密码必须走 env，绝不能出现在 argv 里——argv 经 /proc/<pid>/cmdline 本机全局可读。
// 旧实现靠「密码写进 0600 脚本、argv 只有脚本路径」达成，换成 prochost 后
// 由 Spec.Env 承担，这条断言防止有人图省事把密码拼进 argv。
func TestServeSpecPutsPasswordInEnvNotArgv(t *testing.T) {
	spec := serveSpec("/repo", "/task", "/task/cfg.json", 12345, "s3cr3t",
		[]string{"HTTPS_PROXY=http://u:p@h:8080"})
	for _, a := range spec.Argv {
		if strings.Contains(a, "s3cr3t") {
			t.Fatalf("密码绝不能进 argv: %v", spec.Argv)
		}
	}
	var gotPass, gotCfg, gotProxy bool
	for _, kv := range spec.Env {
		switch kv {
		case "OPENCODE_SERVER_PASSWORD=s3cr3t":
			gotPass = true
		case "OPENCODE_CONFIG=/task/cfg.json":
			gotCfg = true
		case "HTTPS_PROXY=http://u:p@h:8080":
			gotProxy = true
		}
	}
	if !gotPass || !gotCfg || !gotProxy {
		t.Fatalf("env 缺项 pass=%v cfg=%v proxy=%v: %v", gotPass, gotCfg, gotProxy, spec.Env)
	}
	// handoff 自身注入的变量必须排在 env 文件之后，才能覆盖同名键（B19 protectedEnvKeys 纪律）
	passIdx, proxyIdx := -1, -1
	for i, kv := range spec.Env {
		if strings.HasPrefix(kv, "OPENCODE_SERVER_PASSWORD=") {
			passIdx = i
		}
		if strings.HasPrefix(kv, "HTTPS_PROXY=") {
			proxyIdx = i
		}
	}
	if passIdx < proxyIdx {
		t.Fatalf("handoff 注入变量必须排在 env 文件之后以取得覆盖优先级，pass=%d proxy=%d", passIdx, proxyIdx)
	}
	// argv 必须是 opencode serve 的原样形态
	if strings.Join(spec.Argv, " ") != "opencode serve --port 12345 --hostname 127.0.0.1" {
		t.Fatalf("argv 形态不对: %v", spec.Argv)
	}
	if !spec.Sentinel {
		// opencode 有 HTTP 探活面，但哨兵能区分「崩了」与「端口暂时不通」，仍然要
		t.Fatal("Sentinel 必须为 true")
	}
}

// TestOpencodeAliveNeedsBothLockAndHTTP 钉死两层判定。
func TestOpencodeAliveNeedsBothLockAndHTTP(t *testing.T) {
	p := &Proc{Handle: prochost.Handle{PID: os.Getpid(),
		LockPath: filepath.Join(t.TempDir(), "proc.lock")}, Port: 1}
	if p.Alive() {
		t.Fatal("锁无人持有时必须判死，不应再去探 HTTP")
	}
}

// TestServeLogTail 验证 serve.log 尾部读取：文件未创建返回空串，
// 大文件只取末尾 500 字节。
func TestServeLogTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serveLogFileName)
	if got := serveLogTail(path); got != "" {
		t.Errorf("文件不存在时应返回空串，实得 %q", got)
	}
	big := strings.Repeat("x", 10000) + "ENDMARK"
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := serveLogTail(path); !strings.HasSuffix(got, "ENDMARK") || strings.Contains(got, "xENDMARK") && len(got) > 501 {
		t.Errorf("应只取末尾且含 ENDMARK，实得尾部 %d 字节", len(got))
	}
}
