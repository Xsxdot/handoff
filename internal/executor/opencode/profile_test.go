package opencode_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/testperm"
)

func TestProfileZeroReport(t *testing.T) {
	var prof executor.Profile = opencode.New(nil).Profile()
	rep, err := prof.Inspect(context.Background(), executor.ProfileReq{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Prepared || rep.Verified || rep.EngineOK {
		t.Fatalf("零值 report 三布尔必须全为 false: %+v", rep)
	}
}

func TestProfilePrepareWritesRulesAndSkills(t *testing.T) {
	home := t.TempDir()
	var prof executor.Profile = opencode.New(nil).Profile()
	req := executor.ProfileReq{
		HomeDir:  home,
		Isolated: true,
		Rules: []executor.ProfileFile{
			{Name: "AGENTS.md", Content: "carrier rules"},
		},
		Skills: []executor.ProfileFile{
			{Name: "handoff/SKILL.md", Content: "skill content"},
		},
	}
	rep, err := prof.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Prepared {
		t.Fatalf("Prepare 必须成功: %+v", rep)
	}
	if rep.Verified || rep.EngineOK {
		t.Fatalf("Prepare 成功后 Verified/EngineOK 必须仍为 false: %+v", rep)
	}
	rulePath := filepath.Join(home, opencode.RulesRelFile)
	b, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("读取规则失败: %v", err)
	}
	if string(b) != "carrier rules" {
		t.Fatalf("规则内容不符: %q", string(b))
	}
	skillPath := filepath.Join(home, opencode.SkillsRelDir, "handoff", "SKILL.md")
	sb, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("读取 skill 失败: %v", err)
	}
	if string(sb) != "skill content" {
		t.Fatalf("skill 内容不符: %q", string(sb))
	}
}

func TestProfilePreparePreservesOccupiedSessionsDB(t *testing.T) {
	home := t.TempDir()
	dbDir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "sessions.db")
	if err := os.WriteFile(dbPath, []byte("target-sessions"), 0600); err != nil {
		t.Fatal(err)
	}

	var prof executor.Profile = opencode.New(nil).Profile()
	req := executor.ProfileReq{
		HomeDir:  home,
		Isolated: true,
		Rules: []executor.ProfileFile{
			{Name: "AGENTS.md", Content: "rules"},
		},
	}
	rep, err := prof.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Prepared {
		t.Fatalf("Prepare 必须成功: %+v", rep)
	}
	got, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读取 sessions.db: %v", err)
	}
	if string(got) != "target-sessions" {
		t.Fatalf("sessions.db 被覆盖: got %q want target-sessions", string(got))
	}
}

func TestProfilePrepareTaskOverlayDoesNotOverwriteGlobalRules(t *testing.T) {
	home := t.TempDir()
	var prof executor.Profile = opencode.New(nil).Profile()
	req1 := executor.ProfileReq{
		HomeDir:  home,
		Isolated: true,
		Rules: []executor.ProfileFile{
			{Name: "AGENTS.md", Content: "global rules"},
		},
		TaskOverlay: []executor.ProfileFile{
			{Name: filepath.Join("task-1", filepath.Base(opencode.RulesRelFile)), Content: "task 1 overlay"},
		},
	}
	if _, err := prof.Prepare(context.Background(), req1); err != nil {
		t.Fatal(err)
	}
	req2 := executor.ProfileReq{
		HomeDir:  home,
		Isolated: true,
		TaskOverlay: []executor.ProfileFile{
			{Name: filepath.Join("task-2", filepath.Base(opencode.RulesRelFile)), Content: "task 2 overlay"},
		},
	}
	if _, err := prof.Prepare(context.Background(), req2); err != nil {
		t.Fatal(err)
	}
	// Global AGENTS.md must still be "global rules"
	b, err := os.ReadFile(filepath.Join(home, opencode.RulesRelFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "global rules" {
		t.Fatalf("全局 AGENTS.md 被 TaskOverlay 覆盖: %q", string(b))
	}
	// Task overlays should exist in .handoff/task-overlay
	o1, err := os.ReadFile(filepath.Join(home, ".handoff", "task-overlay", "task-1", filepath.Base(opencode.RulesRelFile)))
	if err != nil || string(o1) != "task 1 overlay" {
		t.Fatalf("task 1 overlay 缺失或错误: %v, %q", err, string(o1))
	}
	o2, err := os.ReadFile(filepath.Join(home, ".handoff", "task-overlay", "task-2", filepath.Base(opencode.RulesRelFile)))
	if err != nil || string(o2) != "task 2 overlay" {
		t.Fatalf("task 2 overlay 缺失或错误: %v, %q", err, string(o2))
	}
}

func TestProfilePreparePermissionFailureIsRetryable(t *testing.T) {
	home := t.TempDir()
	rulesDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(rulesDir, 0700); err != nil {
		t.Fatal(err)
	}

	testperm.DenyWrite(t, rulesDir)

	var prof executor.Profile = opencode.New(nil).Profile()
	req := executor.ProfileReq{
		HomeDir:  home,
		Isolated: true,
		Rules: []executor.ProfileFile{
			{Name: "AGENTS.md", Content: "rules"},
		},
	}
	rep, err := prof.Prepare(context.Background(), req)
	if err == nil && rep.Prepared {
		t.Fatal("只读目录写入必须失败")
	}

	// Restore permission and retry
	if err := os.Chmod(rulesDir, 0700); err != nil {
		t.Fatal(err)
	}
	rep, err = prof.Prepare(context.Background(), req)
	if err != nil || !rep.Prepared {
		t.Fatalf("恢复权限后重试必须成功: rep=%+v, err=%v", rep, err)
	}
}
