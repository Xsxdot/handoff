package codex_test

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/codex"
)

func TestOneShotArgvGoldenVectors(t *testing.T) {
	got, err := codex.OneShotArgv("", "p", executor.OneShotLimits{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "exec", "--skip-git-repo-check", "--ephemeral", "--color", "never", "--sandbox", "read-only", "--ignore-user-config", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	got, err = codex.OneShotArgv("gpt-5", "p", executor.OneShotLimits{})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"codex", "exec", "--skip-git-repo-check", "--ephemeral", "--color", "never", "--sandbox", "read-only", "--ignore-user-config", "-m", "gpt-5", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
