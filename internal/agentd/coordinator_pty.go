// coordinator_pty.go —— 协调者控制台 TUI tab（B336）。
//
// 职责：在 leader 载体那台机器、项目主工作树上打开原生 CLI TUI；
// LaunchAdmit 名额占到 tab 关掉；卡到已完成/终止时关 tab。
// 边界：不伪造宿主 session id、不写 HANDOFF_SESSION_*、不写主 HOME 的 AGENTS.md。
package agentd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

type coordinatorLiveTab struct {
	Binding scheduling.Binding
	PtyID   string
	Machine string
	Card    string
}

func (s *Server) rememberCoordinatorTab(card string, tab coordinatorLiveTab) {
	s.coordTabsMu.Lock()
	defer s.coordTabsMu.Unlock()
	if s.coordTabs == nil {
		s.coordTabs = map[string]coordinatorLiveTab{}
	}
	s.coordTabs[card] = tab
}

func (s *Server) coordinatorTab(card string) (coordinatorLiveTab, bool) {
	s.coordTabsMu.Lock()
	defer s.coordTabsMu.Unlock()
	tab, ok := s.coordTabs[card]
	return tab, ok
}

func (s *Server) openCoordinatorTUI(card string, carrier scheduling.Carrier, spec keysclient.SessionSpec) (string, error) {
	if s.openCoordTUI != nil {
		return s.openCoordTUI(card, carrier, spec)
	}
	if s.pty == nil {
		return "", fmt.Errorf("PTY 未装配，无法打开协调者 TUI")
	}
	workdir := spec.Workdir
	if workdir == "" {
		workdir = s.resolveCoordWorkdir(card)
	}
	if workdir == "" {
		return "", fmt.Errorf("卡 %s 没有项目主工作树，无法打开协调者 TUI", card)
	}
	// IsLocalMachine 只认空/local/本机/hostname。现网载体常登记成 targets
	// 键（mac-02），地址却指向本进程——那是 IsSelfTarget 的本机身份。
	localAlias := scheduling.IsLocalMachine(carrier.Machine)
	selfTarget := s.IsSelfTarget(carrier.Machine)
	if !localAlias && !selfTarget {
		s.log.Warn("协调者 TUI 目标机非本机", "card", card, "machine", carrier.Machine)
		return "", fmt.Errorf("远端载体 TUI 转发尚未接线：machine=%s", carrier.Machine)
	}
	if selfTarget && !localAlias {
		s.log.Info("协调者 TUI 把本机 target 名当本地", "card", card, "machine", carrier.Machine)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := spec.CLI
	if strings.TrimSpace(carrier.HomeDir) != "" {
		cmd = "HOME=" + strconv.Quote(carrier.HomeDir) + " " + spec.CLI
	}
	env := pinGrokOsc52Sink(append(s.sessionEnv(), "HANDOFF_CARD="+card))
	s.log.Info("打开协调者 TUI", "card", card, "machine", carrier.Machine,
		"cli", spec.CLI, "workdir", workdir, "home_empty", strings.TrimSpace(carrier.HomeDir) == "")
	sess, err := s.pty.Open(ptyhost.OpenOptions{
		BasePath: workdir, BaseKind: "workspace", Shell: shell,
		Env: env, Cols: 120, Rows: 32, InitCommand: cmd,
	})
	if err != nil {
		s.log.Error("打开协调者 TUI 失败", "card", card, "workdir", workdir, "cli", spec.CLI, "cause", err)
		return "", err
	}
	s.log.Info("协调者 TUI 已打开", "card", card, "pty", sess.ID, "pid", sess.PID, "workdir", workdir)
	return sess.ID, nil
}

func (s *Server) closeCoordinatorTab(card string) {
	s.coordTabsMu.Lock()
	tab, ok := s.coordTabs[card]
	if ok {
		delete(s.coordTabs, card)
	}
	s.coordTabsMu.Unlock()
	if !ok {
		return
	}
	if tab.PtyID != "" && s.pty != nil {
		if err := s.pty.Close(tab.PtyID); err != nil {
			s.log.Warn("关闭协调者 TUI 失败", "card", card, "pty", tab.PtyID, "cause", err)
		} else {
			s.log.Info("协调者 TUI 已关闭", "card", card, "pty", tab.PtyID)
		}
	}
	s.releaseSchedulingBinding(card, tab.Binding)
}

func (s *Server) closeCoordinatorTabIfTerminal(card, status string) {
	if status != ledger.StatusDone && status != ledger.StatusClosed {
		return
	}
	s.log.Info("卡终态，关闭协调者 TUI", "card", card, "status", status)
	s.closeCoordinatorTab(card)
}
