//go:build unix

// service_pty_test.go —— service stop 真实经过 closePtySessionsForStop→CloseAll。
// 职责：锁住显式 stop 的 PTY 收口和 2s 外层预算不改变。
// 边界：不启动真实 service manager，只直接调用 cmd 的 stop cleanup 函数。
package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
)

func TestClosePtySessionsForStopWaitsForLateTrap(t *testing.T) {
	dataParent := os.TempDir()
	if override := os.Getenv("HANDOFF_PTY_TEST_ROOT"); override != "" {
		dataParent = override
	}
	dataDir, err := os.MkdirTemp(dataParent, "b234-svc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	root := filepath.Join(dataDir, "ptys")
	home := t.TempDir()
	id := "b234-stop"
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	spec := hostproc.Spec{
		Root: root, ID: id, BasePath: home, BaseKind: "home", Cwd: home,
		Shell: "/bin/sh", Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=xterm-256color"},
		Cols: 80, Rows: 24,
		InitCommand: `trap 'exit 0' TERM; trap 'printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"; while :; do :; done`,
	}
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(sessdir.Dir(root, id), "spec.json")
	if err := os.WriteFile(specPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- hostproc.Run(specPath) }()
	deadline := time.Now().Add(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	for {
		if _, err := os.Stat(filepath.Join(home, "b234-ready")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("service stop fixture hostproc 提前退出: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			ticker.Stop()
			t.Fatal("service stop fixture 未就绪")
		}
		<-ticker.C
	}
	ticker.Stop()
	cfgPath := filepath.Join(dataDir, "config.yaml")
	cfg := config.Defaults()
	cfg.DataDir = dataDir
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	oldConfigPath := configPath
	configPath = cfgPath
	t.Cleanup(func() { configPath = oldConfigPath })
	var out bytes.Buffer
	closePtySessionsForStop(&out)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hostproc.Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closePtySessionsForStop 后 hostproc 未退出")
	}
	if _, err := os.Stat(filepath.Join(home, "b234-late")); err != nil {
		t.Fatalf("stop cleanup 返回/hostproc 退出后 late marker 不存在: %v", err)
	}
	if _, err := os.Stat(sessdir.Dir(root, id)); !os.IsNotExist(err) {
		t.Fatalf("stop cleanup 后会话目录仍存在: %v", err)
	}
}
