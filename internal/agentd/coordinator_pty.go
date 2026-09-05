// coordinator_pty.go —— 协调者控制台 TUI tab（B336）。
//
// 职责：在 leader 载体那台机器、项目主工作树上打开原生 CLI TUI；
// LaunchAdmit 名额占到 tab 关掉；卡到已完成/终止时关 tab。
// 边界：不伪造宿主 session id、不写 HANDOFF_SESSION_*、不写主 HOME 的 AGENTS.md。
package agentd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
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
	// IsLocalMachine 只认空/local/本机/hostname。现网载体常登记成 targets
	// 键（mac-02），地址却指向本进程——那是 IsSelfTarget 的本机身份。
	localAlias := scheduling.IsLocalMachine(carrier.Machine)
	selfTarget := s.IsSelfTarget(carrier.Machine)
	if !localAlias && !selfTarget {
		s.log.Info("协调者 TUI 转发到远端", "card", card, "machine", carrier.Machine)
		return s.openRemoteCoordinatorTUI(card, carrier, spec)
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
	if selfTarget && !localAlias {
		s.log.Info("协调者 TUI 把本机 target 名当本地", "card", card, "machine", carrier.Machine)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := coordTUIInitCommand(carrier, spec)
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

func coordTUIInitCommand(carrier scheduling.Carrier, spec keysclient.SessionSpec) string {
	cmd := spec.CLI
	if strings.TrimSpace(carrier.HomeDir) != "" {
		cmd = "HOME=" + strconv.Quote(carrier.HomeDir) + " " + spec.CLI
	}
	return cmd
}

func (s *Server) openRemoteCoordinatorTUI(card string, carrier scheduling.Carrier, spec keysclient.SessionSpec) (string, error) {
	workdir, err := s.remoteCoordWorkdir(carrier.Machine, card)
	if err != nil {
		s.log.Error("解析远端协调者工作目录失败", "card", card, "machine", carrier.Machine, "cause", err)
		return "", err
	}
	if workdir == "" {
		return "", fmt.Errorf("卡 %s 在机器 %s 没有项目主工作树，无法打开协调者 TUI", card, carrier.Machine)
	}
	req := proto.CreatePtySessionReq{
		BasePath: workdir, BaseKind: "workspace",
		Cols: 120, Rows: 32, InitCommand: coordTUIInitCommand(carrier, spec),
	}
	s.log.Info("打开远端协调者 TUI", "card", card, "machine", carrier.Machine,
		"cli", spec.CLI, "workdir", workdir)
	id, err := s.createRemoteCoordinatorPty(carrier.Machine, req)
	if err != nil {
		s.log.Error("打开远端协调者 TUI 失败", "card", card, "machine", carrier.Machine,
			"workdir", workdir, "cli", spec.CLI, "cause", err)
		return "", err
	}
	s.log.Info("远端协调者 TUI 已打开", "card", card, "machine", carrier.Machine, "pty", id, "workdir", workdir)
	return id, nil
}

func (s *Server) remoteCoordWorkdir(machine, card string) (string, error) {
	if s.lookupRemoteCoordWorkdir != nil {
		return s.lookupRemoteCoordWorkdir(machine, card)
	}
	row, err := s.ledger.GetCard(card)
	if err != nil {
		return "", fmt.Errorf("读卡 %s: %w", card, err)
	}
	c, err := s.clientForTarget(machine)
	if err != nil {
		return "", fmt.Errorf("取机器 %s 客户端: %w", machine, err)
	}
	locs, err := c.MarkForwarded().ProjectList(context.Background())
	if err != nil {
		return "", fmt.Errorf("列机器 %s 项目: %w", machine, err)
	}
	loc, err := resolveProject("", row.Project, locs)
	if err != nil {
		return "", err
	}
	return loc.Path, nil
}

func (s *Server) createRemoteCoordinatorPty(machine string, req proto.CreatePtySessionReq) (string, error) {
	if s.createRemoteCoordPty != nil {
		return s.createRemoteCoordPty(machine, req)
	}
	c, err := s.clientForTarget(machine)
	if err != nil {
		return "", fmt.Errorf("取机器 %s 客户端: %w", machine, err)
	}
	sess, err := c.MarkForwarded().CreatePtySession(context.Background(), req)
	if err != nil {
		return "", err
	}
	if sess == nil || sess.ID == "" {
		return "", fmt.Errorf("机器 %s 建终端会话未返回 id", machine)
	}
	return sess.ID, nil
}

func (s *Server) closeRemoteCoordinatorPty(machine, ptyID string) error {
	if s.closeRemoteCoordPty != nil {
		return s.closeRemoteCoordPty(machine, ptyID)
	}
	c, err := s.clientForTarget(machine)
	if err != nil {
		return fmt.Errorf("取机器 %s 客户端: %w", machine, err)
	}
	return c.MarkForwarded().DeletePtySession(context.Background(), ptyID)
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
	if tab.PtyID != "" {
		remote := !scheduling.IsLocalMachine(tab.Machine) && !s.IsSelfTarget(tab.Machine)
		if remote {
			if err := s.closeRemoteCoordinatorPty(tab.Machine, tab.PtyID); err != nil {
				s.log.Warn("关闭远端协调者 TUI 失败", "card", card, "machine", tab.Machine, "pty", tab.PtyID, "cause", err)
			} else {
				s.log.Info("远端协调者 TUI 已关闭", "card", card, "machine", tab.Machine, "pty", tab.PtyID)
			}
		} else if s.pty != nil {
			if err := s.pty.Close(tab.PtyID); err != nil {
				s.log.Warn("关闭协调者 TUI 失败", "card", card, "pty", tab.PtyID, "cause", err)
			} else {
				s.log.Info("协调者 TUI 已关闭", "card", card, "pty", tab.PtyID)
			}
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
