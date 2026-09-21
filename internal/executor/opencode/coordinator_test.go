package opencode

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	// 等两个回合都进在途表：Coordinator.turn 先登记在途表、再拉起 CLI，而假 CLI
	// 脚本首段就把 argv 落进捕获文件，故「两个 -s 都出现」蕴含两个回合都已在途。
	// 原来固定 sleep 100ms 在全量并行负载下不够（2026-09-19 实测全量红、单跑绿）。
	waitSessionTurnStarted(t, os.Getenv("FAKECLI_ARGV_FILE"), "ses-a", "ses-b")
	if err := c.CancelTurn(context.Background(), executor.CoordSessionRef{CLI: "opencode", SessionID: "ses-a"}); err != nil {
		t.Fatal(err)
	}
	var canceled, completed bool
	deadline := time.After(10 * time.Second)
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

// waitSessionTurnStarted 轮询 argv 捕获文件，直到给定的 session id 全部出现。
//
// 判据链条：假 CLI 脚本首段就把 argv 落盘（在 sleep 之前），而 Coordinator.turn
// 是**先登记在途表、再拉起 CLI**——故「id 出现在 argv 里」蕴含该回合已在途，
// CancelTurn 可以命中。用它替代固定 sleep，是为了在「全量 go test ./...」的
// 并行负载下不靠运气（2026-09-19 实测：固定 100ms 时该用例全量红、单跑绿）。
func waitSessionTurnStarted(t *testing.T, argvFile string, ids ...string) {
	t.Helper()
	if argvFile == "" {
		t.Fatal("FAKECLI_ARGV_FILE 未设置（installFakeCLI 应已设）")
	}
	budget := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(argvFile); err == nil {
			all := true
			for _, id := range ids {
				if !strings.Contains(string(b), id) {
					all = false
					break
				}
			}
			if all {
				return
			}
		} else if !os.IsNotExist(err) {
			t.Fatalf("读 argv 捕获文件: %v", err)
		}
		if time.Now().After(budget) {
			t.Fatalf("等待回合在途超时（10s）：%s 里未出现全部 %v", argvFile, ids)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
