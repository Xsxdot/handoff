package agy

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestPermServerAskAndRespond(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "perm.sock")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	asks := make(chan permAsk, 1)
	srv, err := newPermServer(sockPath, logger, func(a permAsk) {
		asks <- a
	})
	if err != nil {
		t.Fatalf("newPermServer 失败: %v", err)
	}
	defer srv.Close()

	// 模拟 hook 发送 ask
	decisions := make(chan permDecision, 1)
	go func() {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			return
		}
		defer conn.Close()

		askData, _ := json.Marshal(map[string]any{
			"type":        "ask",
			"tool_use_id": "step_1",
			"tool_name":   "run_command",
			"input":       map[string]string{"CommandLine": "ls"},
		})
		_, _ = conn.Write(append(askData, '\n'))

		var dec permDecision
		_ = json.NewDecoder(bufio.NewReader(conn)).Decode(&dec)
		decisions <- dec
	}()

	select {
	case ask := <-asks:
		if ask.ToolUseID != "step_1" || ask.ToolName != "run_command" {
			t.Fatalf("收到未预期的 ask: %+v", ask)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到 ask")
	}

	// 回应 allow
	if err := srv.Respond("step_1", "allow", ""); err != nil {
		t.Fatalf("Respond 失败: %v", err)
	}

	select {
	case dec := <-decisions:
		if dec.Behavior != "allow" {
			t.Fatalf("want allow, got %s", dec.Behavior)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到裁决")
	}
}
