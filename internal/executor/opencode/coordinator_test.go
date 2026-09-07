package opencode

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/hostapi"
)

func TestCoordinatorLaunchEmptyPrompt(t *testing.T) {
	c := NewCoordinator(hostapi.New(), slog.Default())
	_, err := c.Launch(context.Background(), executor.CoordSessionSpec{CLI: "opencode", HomeDir: t.TempDir()}, "")
	if !errors.Is(err, executor.ErrEmptyLaunchPrompt) {
		t.Fatalf("err = %v", err)
	}
	_, err = c.Launch(context.Background(), executor.CoordSessionSpec{CLI: "opencode", HomeDir: t.TempDir()}, "  \n")
	if !errors.Is(err, executor.ErrEmptyLaunchPrompt) {
		t.Fatalf("blank err = %v", err)
	}
}

func TestCoordinatorResumeEmptySession(t *testing.T) {
	c := NewCoordinator(hostapi.New(), slog.Default())
	_, err := c.Resume(context.Background(), executor.CoordSessionRef{CLI: "opencode"}, "hi")
	if !errors.Is(err, executor.ErrEmptyResumeSession) {
		t.Fatalf("err = %v", err)
	}
}

func TestCoordinatorLaunchPassesCtxAndGetsSession(t *testing.T) {
	installFakeCLI(t)
	c := NewCoordinator(hostapi.New(), slog.Default())
	got, err := c.Launch(context.Background(), executor.CoordSessionSpec{
		CLI: "opencode", HomeDir: t.TempDir(), Workdir: t.TempDir(),
	}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID == "" || got.Output == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestCoordinatorLaunchHonorsCancel(t *testing.T) {
	installFakeCLI(t)
	t.Setenv("FAKECLI_SLEEP", "30")
	c := NewCoordinator(hostapi.New(), slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Launch(ctx, executor.CoordSessionSpec{
		CLI: "opencode", HomeDir: t.TempDir(), Workdir: t.TempDir(),
	}, "hello")
	if err == nil {
		t.Fatal("取消后必须失败")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("未在 ctx 取消后打断，耗时 %s", time.Since(start))
	}
}

func TestCoordinatorCancelTurnUsesSessionID(t *testing.T) {
	installFakeCLI(t)
	t.Setenv("FAKECLI_SLEEP_SESSION", "ses-a")
	c := NewCoordinator(hostapi.New(), slog.Default())
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for _, sessionID := range []string{"ses-a", "ses-b"} {
		go func(sessionID string) {
			got, err := c.Resume(context.Background(), executor.CoordSessionRef{
				CLI: "opencode", SessionID: sessionID, HomeDir: t.TempDir(), Workdir: t.TempDir(),
			}, "hello")
			results <- result{id: got.SessionID, err: err}
		}(sessionID)
	}
	time.Sleep(100 * time.Millisecond)
	if err := c.CancelTurn(context.Background(), executor.CoordSessionRef{CLI: "opencode", SessionID: "ses-a"}); err != nil {
		t.Fatal(err)
	}
	var canceled, completed bool
	deadline := time.After(500 * time.Millisecond)
	for canceled == false || completed == false {
		select {
		case got := <-results:
			if got.err != nil {
				canceled = true
				continue
			}
			if got.id != "ses-b" {
				t.Fatalf("取消 ses-a 不得误伤并行 session，id=%q err=%v", got.id, got.err)
			}
			completed = true
		case <-deadline:
			t.Fatal("取消 ses-a 未在途，或并行 session 被同键覆盖")
		}
	}
}

func installFakeCLI(t *testing.T) {
	t.Helper()
	withArgvCapture(t)
	binDir := t.TempDir()
	script := `#!/bin/sh
if [ -n "$FAKECLI_ARGV_FILE" ]; then
  for a in "$@"; do printf '%s\n' "$a" >>"$FAKECLI_ARGV_FILE"; done
  printf 'env:HOME=%s\n' "$HOME" >>"$FAKECLI_ARGV_FILE"
fi
SID=""; prev=""
for a in "$@"; do
  if [ "$prev" = "-s" ]; then SID="$a"; fi
  prev="$a"
done
if [ -z "$SID" ]; then SID="ses_fake_new"; fi
if [ -n "$FAKECLI_SLEEP" ]; then sleep "$FAKECLI_SLEEP"; fi
if [ -n "$FAKECLI_SLEEP_SESSION" ] && [ "$SID" = "$FAKECLI_SLEEP_SESSION" ]; then sleep 30; fi
printf '%s\n' "{\"type\":\"step_start\",\"sessionID\":\"$SID\",\"part\":{\"type\":\"step-start\"}}"
printf '%s\n' "{\"type\":\"text\",\"sessionID\":\"$SID\",\"part\":{\"type\":\"text\",\"text\":\"ok-$SID\"}}"
printf '%s\n' "{\"type\":\"step_finish\",\"sessionID\":\"$SID\",\"part\":{\"type\":\"step-finish\",\"reason\":\"stop\"}}"
`
	fake := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func withArgvCapture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKECLI_ARGV_FILE", p)
	return p
}
