package agentd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/keysclient"
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

// TestOpenCoordinatorTUIRejectsRemoteMachine 对照：真正的远端仍报尚未接线。
func TestOpenCoordinatorTUIRejectsRemoteMachine(t *testing.T) {
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

	_, err := env.srv.openCoordinatorTUI("B338", scheduling.Carrier{
		Name: "remote", Machine: "linux-01", CLI: "true",
	}, keysclient.SessionSpec{CLI: "true", Workdir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "远端载体 TUI 转发尚未接线") || !strings.Contains(err.Error(), "linux-01") {
		t.Fatalf("远端应报尚未接线并点名机器，得 %v", err)
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
