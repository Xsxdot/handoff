package agentd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// TestOpenCoordinatorTUITreatsSelfTargetAsLocal 锁 B338：targets 里指向本
// listen 的登记名（现网 muse.machine=mac-02）必须走本机 PTY，不能误报远端未接线。
func TestOpenCoordinatorTUITreatsSelfTargetAsLocal(t *testing.T) {
	env := newLedgerEnv(t)
	env.srv.openCoordTUI = nil
	listen := env.srv.conf().Listen
	if err := env.srv.swapConf(func(cfg *config.Config) error {
		if cfg.Targets == nil {
			cfg.Targets = map[string]config.Target{}
		}
		cfg.Targets["mac-02"] = config.Target{Addr: listen, Token: testToken}
		return nil
	}); err != nil {
		t.Fatalf("写入 self-target: %v", err)
	}

	id, err := env.srv.openCoordinatorTUI("B338", scheduling.Carrier{
		Name: "muse", Machine: "mac-02", CLI: "true",
	}, keysclient.SessionSpec{CLI: "true", Workdir: t.TempDir()})
	if err != nil {
		t.Fatalf("本机 target 名应打开 TUI，得 %v", err)
	}
	if id == "" {
		t.Fatal("本机 TUI 应返回 PTY id")
	}
	t.Cleanup(func() { _ = env.srv.pty.Close(id) })
}

// TestOpenCoordinatorTUIOpensRemotePty 锁 B344：真正的远端经建会话缝打开，
// 不再报尚未接线。忽略协调机本地 Workdir。
func TestOpenCoordinatorTUIOpensRemotePty(t *testing.T) {
	env := newLedgerEnv(t)
	env.srv.openCoordTUI = nil
	if err := env.srv.swapConf(func(cfg *config.Config) error {
		if cfg.Targets == nil {
			cfg.Targets = map[string]config.Target{}
		}
		cfg.Targets["linux-01"] = config.Target{Addr: "10.0.0.9:7777", Token: testToken}
		return nil
	}); err != nil {
		t.Fatalf("写入远端 target: %v", err)
	}
	var gotMachine, gotPath, gotInit string
	env.srv.lookupRemoteCoordWorkdir = func(machine, card string) (string, error) {
		if machine != "linux-01" || card != "B344" {
			t.Fatalf("lookup machine/card = %s/%s", machine, card)
		}
		return "/remote/handoff", nil
	}
	env.srv.createRemoteCoordPty = func(machine string, req proto.CreatePtySessionReq) (string, error) {
		gotMachine, gotPath, gotInit = machine, req.BasePath, req.InitCommand
		return "remote-pty", nil
	}

	id, err := env.srv.openCoordinatorTUI("B344", scheduling.Carrier{
		Name: "agy", Machine: "linux-01", CLI: "agy", HomeDir: "~",
	}, keysclient.SessionSpec{CLI: "agy", Workdir: t.TempDir()})
	if err != nil {
		t.Fatalf("远端应打开 TUI，得 %v", err)
	}
	if id != "remote-pty" {
		t.Fatalf("pty id = %q，want remote-pty", id)
	}
	if gotMachine != "linux-01" || gotPath != "/remote/handoff" {
		t.Fatalf("建会话 machine/path = %s/%s", gotMachine, gotPath)
	}
	if !strings.Contains(gotInit, "agy") {
		t.Fatalf("InitCommand 应含 CLI: %q", gotInit)
	}
}

func TestCloseCoordinatorTabDeletesRemotePty(t *testing.T) {
	env := newLedgerEnv(t)
	var gotMachine, gotID string
	env.srv.closeRemoteCoordPty = func(machine, ptyID string) error {
		gotMachine, gotID = machine, ptyID
		return nil
	}
	env.srv.rememberCoordinatorTab("B344", coordinatorLiveTab{
		PtyID: "remote-pty", Machine: "linux-01", Card: "B344",
	})
	env.srv.closeCoordinatorTab("B344")
	if gotMachine != "linux-01" || gotID != "remote-pty" {
		t.Fatalf("关闭远端 = %s/%s", gotMachine, gotID)
	}
}

// TestOpenCoordinatorTUIKeepsLocalAliases 回归：本机别名仍走本机 PTY。
func TestOpenCoordinatorTUIKeepsLocalAliases(t *testing.T) {
	env := newLedgerEnv(t)
	env.srv.openCoordTUI = nil
	id, err := env.srv.openCoordinatorTUI("B338", scheduling.Carrier{
		Name: "plain", Machine: "本机", CLI: "true",
	}, keysclient.SessionSpec{CLI: "true", Workdir: t.TempDir()})
	if err != nil {
		t.Fatalf("本机别名应打开 TUI，得 %v", err)
	}
	if id == "" {
		t.Fatal("本机 TUI 应返回 PTY id")
	}
	t.Cleanup(func() { _ = env.srv.pty.Close(id) })
}
