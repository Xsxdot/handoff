// profile_contract_test.go —— 所有 executor Profile 的任务层规则契约。
//
// 职责：验证 Prepare/Verify 对全局规则与任务 overlay 的物理隔离及逐字节校验。
// 边界：只覆盖机内 Profile seam；不宣称真实引擎可用或跨机投递成功。
package executor_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/agy"
	"github.com/Xsxdot/handoff/internal/executor/claudecode"
	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
)

func TestProfilesVerifyExistingRules(t *testing.T) {
	profiles := []struct {
		name string
		path string
		new  func(*slog.Logger) executor.Profile
	}{
		{name: executor.HarnessOpenCode, path: opencode.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return opencode.New(log).Profile() }},
		{name: executor.HarnessClaude, path: claudecode.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return claudecode.New(log).Profile() }},
		{name: executor.HarnessGrok, path: grok.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return grok.New(log).Profile() }},
		{name: executor.HarnessCodex, path: codex.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return codex.New(log).Profile() }},
		{name: executor.HarnessAGY, path: agy.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return agy.New(log).Profile() }},
	}

	for _, tc := range profiles {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			rulePath := filepath.Join(home, tc.path)
			if err := os.MkdirAll(filepath.Dir(rulePath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rulePath, []byte("rules"), 0o600); err != nil {
				t.Fatal(err)
			}
			rep, err := tc.new(nil).Verify(context.Background(), executor.ProfileReq{HomeDir: home, Isolated: true})
			if err != nil {
				t.Fatal(err)
			}
			if !rep.Verified {
				t.Fatalf("%s Verify must report an existing rules file: %+v", tc.name, rep)
			}
			if rep.EngineOK {
				t.Fatalf("%s Verify must not claim engine health: %+v", tc.name, rep)
			}
		})
	}
}

// TestProfilesVerifyTaskOverlayContent locks the real task-layer boundary for
// all harnesses: same production rules basename, different task namespaces,
// exact bytes on disk, and content-aware Verify.
func TestProfilesVerifyTaskOverlayContent(t *testing.T) {
	profiles := []struct {
		name string
		path string
		new  func(*slog.Logger) executor.Profile
	}{
		{name: executor.HarnessOpenCode, path: opencode.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return opencode.New(log).Profile() }},
		{name: executor.HarnessClaude, path: claudecode.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return claudecode.New(log).Profile() }},
		{name: executor.HarnessGrok, path: grok.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return grok.New(log).Profile() }},
		{name: executor.HarnessCodex, path: codex.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return codex.New(log).Profile() }},
		{name: executor.HarnessAGY, path: agy.RulesRelFile, new: func(log *slog.Logger) executor.Profile { return agy.New(log).Profile() }},
	}

	for _, tc := range profiles {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			home := t.TempDir()
			global := "global rules\n"
			taskA := "task A\n纪律 α\r\n"
			taskB := "task B\n纪律 β\n"
			aName := filepath.Join("task-a", filepath.Base(tc.path))
			bName := filepath.Join("task-b", filepath.Base(tc.path))
			req := executor.ProfileReq{
				HomeDir: home, Isolated: true,
				Rules: []executor.ProfileFile{{Name: filepath.Base(tc.path), Content: global}},
				TaskOverlay: []executor.ProfileFile{
					{Name: aName, Content: taskA},
					{Name: bName, Content: taskB},
				},
			}
			prof := tc.new(logger)
			if rep, err := prof.Prepare(context.Background(), req); err != nil || !rep.Prepared {
				t.Fatalf("Prepare rep=%+v err=%v", rep, err)
			}
			globalPath := filepath.Join(home, tc.path)
			if got, err := os.ReadFile(globalPath); err != nil || string(got) != global {
				t.Fatalf("全局规则被错误写入：err=%v content=%q", err, got)
			}
			aPath := filepath.Join(home, ".handoff", "task-overlay", aName)
			bPath := filepath.Join(home, ".handoff", "task-overlay", bName)
			if got, err := os.ReadFile(aPath); err != nil || string(got) != taskA {
				t.Fatalf("task A overlay 不符：err=%v content=%q", err, got)
			}
			if got, err := os.ReadFile(bPath); err != nil || string(got) != taskB {
				t.Fatalf("task B overlay 不符：err=%v content=%q", err, got)
			}
			if filepath.Base(globalPath) == filepath.Base(aPath) && globalPath == aPath {
				t.Fatalf("任务层不得与全局规则落在同一物理路径：%s", aPath)
			}

			if err := os.Remove(aPath); err != nil {
				t.Fatal(err)
			}
			logs.Reset()
			if rep, err := prof.Verify(context.Background(), req); err != nil || rep.Verified {
				t.Fatalf("缺失 task A 时 Verify 必须未验证：rep=%+v err=%v", rep, err)
			}
			if got := logs.String(); !strings.Contains(got, "expected_state=present") || !strings.Contains(got, "actual_state=missing") {
				t.Fatalf("缺失 task A 的 Verify 日志必须含 expected-vs-actual 结构化状态，日志=%s", got)
			}
			if err := os.WriteFile(aPath, []byte("wrong bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			logs.Reset()
			if rep, err := prof.Verify(context.Background(), req); err != nil || rep.Verified {
				t.Fatalf("错误 task A 内容时 Verify 必须未验证：rep=%+v err=%v", rep, err)
			}
			if got := logs.String(); !strings.Contains(got, "expected_state=present") || !strings.Contains(got, "actual_state=content_mismatch") || !strings.Contains(got, "expected_bytes=") || !strings.Contains(got, "actual_bytes=") {
				t.Fatalf("错误 task A 的 Verify 日志必须含 expected-vs-actual 内容信息，日志=%s", got)
			}
			if err := os.Remove(aPath); err != nil {
				t.Fatal(err)
			}
			if rep, err := prof.Verify(context.Background(), req); err != nil || rep.Verified {
				t.Fatalf("只有全局规则时 Verify 必须未验证：rep=%+v err=%v", rep, err)
			}
			if err := os.WriteFile(aPath, []byte(taskA), 0o600); err != nil {
				t.Fatal(err)
			}
			rep, err := prof.Verify(context.Background(), req)
			if err != nil || !rep.Verified || rep.EngineOK {
				t.Fatalf("全局与两个任务层内容正确时 Verify 错误：rep=%+v err=%v", rep, err)
			}
			if got, err := os.ReadFile(filepath.Join(home, ".handoff", "task-overlay", filepath.Join("task-b", filepath.Base(tc.path)))); err != nil || string(got) != taskB {
				t.Fatalf("task B 读取到了错误正文：err=%v content=%q", err, got)
			}
		})
	}
}
