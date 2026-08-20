//go:build unix

// ptyreclaim_test.go —— agentd 启动认领与三态目录决策的测试。
//
// 职责：验证 live/dead/broken 与缺失根目录四条启动边界，以及日志可审计性。
// 边界：不启动真实 shell、不测试 socket 帧；Host 只接收登记结果，进程生命周期由
// sessdir 的文件锁在测试中模拟。
package agentd

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
)

func TestPtyReclaimLiveAdopts(t *testing.T) {
	requireLockSupport(t)
	root := t.TempDir()
	meta := reclaimMeta("live")
	if err := sessdir.Create(root, meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := sessdir.WriteMeta(root, meta); err != nil {
		t.Fatal(err)
	}
	lock, err := prochost.AcquireLock(sessdir.LockPath(root, meta.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	s, _ := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatal(err)
	}
	list := s.pty.List()
	if len(list) != 1 || list[0].ID != meta.ID || list[0].BasePath != meta.BasePath {
		t.Fatalf("List = %+v，期望登记 live 会话", list)
	}
	if _, err := os.Stat(sessdir.Dir(root, meta.ID)); err != nil {
		t.Fatalf("live 目录不应删除: %v", err)
	}
}

func TestPtyReclaimDeadRemoves(t *testing.T) {
	root := t.TempDir()
	meta := reclaimMeta("dead")
	if err := sessdir.Create(root, meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := sessdir.WriteMeta(root, meta); err != nil {
		t.Fatal(err)
	}
	s, _ := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessdir.Dir(root, meta.ID)); !os.IsNotExist(err) {
		t.Fatalf("dead 目录应删除，stat err=%v", err)
	}
	if list := s.pty.List(); len(list) != 0 {
		t.Fatalf("dead 会话不应登记: %+v", list)
	}
}

func TestPtyReclaimBrokenLeavesAndLogs(t *testing.T) {
	requireLockSupport(t)
	root := t.TempDir()
	if err := sessdir.Create(root, "broken"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessdir.MetaPath(root, "broken"), []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := prochost.AcquireLock(sessdir.LockPath(root, "broken"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	s, logs := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessdir.Dir(root, "broken")); err != nil {
		t.Fatalf("broken 目录不应删除: %v", err)
	}
	if list := s.pty.List(); len(list) != 0 {
		t.Fatalf("broken 会话不应登记: %+v", list)
	}
	if !bytes.Contains(logs.Bytes(), []byte("PTY 会话目录异常")) {
		t.Fatalf("日志缺少 broken Error: %s", logs.String())
	}
}

func TestPtyReclaimMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	s, _ := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatalf("首次启动根目录不存在不应报错: %v", err)
	}
	if list := s.pty.List(); len(list) != 0 {
		t.Fatalf("List = %+v，期望空", list)
	}
}

func reclaimServer(root string) (*Server, *bytes.Buffer) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &Server{pty: ptyhost.New(root, "", log), ptyRootPath: root, log: log}, &logs
}

func reclaimMeta(id string) sessdir.Meta {
	return sessdir.Meta{ID: id, BasePath: "/repo/a", BaseKind: "workspace", Cwd: "/repo/a",
		Shell: "/bin/sh", PID: 4242, ProtoVersion: 1}
}

func requireLockSupport(t *testing.T) {
	t.Helper()
	if !prochost.LockSupported() {
		t.Skip("本平台不支持文件锁，判活语义不成立")
	}
}
