package executor_test

import (
	"context"
	"os"
	"path/filepath"
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
		new  func() executor.Profile
	}{
		{name: executor.HarnessOpenCode, path: opencode.RulesRelFile, new: func() executor.Profile { return opencode.New(nil).Profile() }},
		{name: executor.HarnessClaude, path: claudecode.RulesRelFile, new: func() executor.Profile { return claudecode.New(nil).Profile() }},
		{name: executor.HarnessGrok, path: grok.RulesRelFile, new: func() executor.Profile { return grok.New(nil).Profile() }},
		{name: executor.HarnessCodex, path: codex.RulesRelFile, new: func() executor.Profile { return codex.New(nil).Profile() }},
		{name: executor.HarnessAGY, path: agy.RulesRelFile, new: func() executor.Profile { return agy.New(nil).Profile() }},
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
			rep, err := tc.new().Verify(context.Background(), executor.ProfileReq{HomeDir: home, Isolated: true})
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
