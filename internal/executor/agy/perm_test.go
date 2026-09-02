package agy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
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

func TestAdapterRespondPermissionOnceMapsToAllow(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "perm.sock")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ad := New(logger)
	r := ad.newRun("T1", sockDir, sockDir)

	srv, err := newPermServer(sockPath, logger, func(a permAsk) {
		ad.onPermissionAsk(r, a)
	})
	if err != nil {
		t.Fatalf("newPermServer 失败: %v", err)
	}
	defer srv.Close()
	r.permSrv = srv

	decisions := make(chan permDecision, 1)
	go func() {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			return
		}
		defer conn.Close()

		askData, _ := json.Marshal(map[string]any{
			"type":        "ask",
			"tool_use_id": "step_0",
			"tool_name":   "write_to_file",
			"input":       map[string]string{"TargetFile": "/tmp/a.txt"},
		})
		_, _ = conn.Write(append(askData, '\n'))

		var dec permDecision
		_ = json.NewDecoder(bufio.NewReader(conn)).Decode(&dec)
		decisions <- dec
	}()

	select {
	case ev := <-r.evCh:
		if ev.Type != "permission" || ev.PermissionID != "step_0" {
			t.Fatalf("未收到预期 permission 事件: %+v", ev)
		}
		if ev.Perm == nil || ev.Perm.Tool != "write" || len(ev.Perm.Paths) == 0 || ev.Perm.Paths[0] != "/tmp/a.txt" {
			t.Fatalf("PermRequest 未正确结构化: %+v", ev.Perm)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到 permission 事件")
	}

	// 模拟 manager 调 RespondPermission 传 "once"
	if err := ad.RespondPermission(context.Background(), "T1", "step_0", "once", ""); err != nil {
		t.Fatalf("RespondPermission 失败: %v", err)
	}

	select {
	case dec := <-decisions:
		if dec.Behavior != "allow" {
			t.Fatalf("once 必须映射为 allow，实得 %s", dec.Behavior)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到裁决")
	}
}

func TestAdapterRespondPermissionRejectMapsToDeny(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "perm.sock")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ad := New(logger)
	r := ad.newRun("T2", sockDir, sockDir)

	srv, err := newPermServer(sockPath, logger, func(a permAsk) {
		ad.onPermissionAsk(r, a)
	})
	if err != nil {
		t.Fatalf("newPermServer 失败: %v", err)
	}
	defer srv.Close()
	r.permSrv = srv

	decisions := make(chan permDecision, 1)
	go func() {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			return
		}
		defer conn.Close()

		askData, _ := json.Marshal(map[string]any{
			"type":        "ask",
			"tool_use_id": "step_5",
			"tool_name":   "run_command",
			"input":       map[string]string{"CommandLine": "rm -rf /"},
		})
		_, _ = conn.Write(append(askData, '\n'))

		var dec permDecision
		_ = json.NewDecoder(bufio.NewReader(conn)).Decode(&dec)
		decisions <- dec
	}()

	select {
	case ev := <-r.evCh:
		if ev.Type != "permission" || ev.PermissionID != "step_5" {
			t.Fatalf("未收到预期 permission 事件: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到 permission 事件")
	}

	// 模拟 manager 调 RespondPermission 传 "reject"
	if err := ad.RespondPermission(context.Background(), "T2", "step_5", "reject", "forbidden root deletion"); err != nil {
		t.Fatalf("RespondPermission 失败: %v", err)
	}

	select {
	case dec := <-decisions:
		if dec.Behavior != "deny" {
			t.Fatalf("reject 必须映射为 deny，实得 %s", dec.Behavior)
		}
		if !strings.Contains(dec.Message, "forbidden root deletion") {
			t.Fatalf("Message 未包含拒绝理由: %s", dec.Message)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到裁决")
	}
}

func TestPermServerRespondNotFound(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "perm.sock")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv, err := newPermServer(sockPath, logger, func(a permAsk) {})
	if err != nil {
		t.Fatalf("newPermServer 失败: %v", err)
	}
	defer srv.Close()

	if err := srv.Respond("non_existent_id", "allow", ""); err == nil {
		t.Fatalf("对不存在的工单 Respond 必须报错")
	}
}

func TestPermServerLargePayloadDecode(t *testing.T) {
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

	// 生成 > 128KB 的大文件写入内容，确保突破 bufio.Scanner 的 64KiB 上限
	largeContent := strings.Repeat("const x = 1;\n", 10000) // ~130KB
	go func() {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			return
		}
		defer conn.Close()

		askData, _ := json.Marshal(map[string]any{
			"type":        "ask",
			"tool_use_id": "step_large",
			"tool_name":   "write_to_file",
			"input": map[string]string{
				"TargetFile":  "/tmp/large.ts",
				"CodeContent": largeContent,
			},
		})
		_, _ = conn.Write(append(askData, '\n'))

		var dec permDecision
		_ = json.NewDecoder(bufio.NewReader(conn)).Decode(&dec)
	}()

	select {
	case ask := <-asks:
		if ask.ToolUseID != "step_large" || ask.ToolName != "write_to_file" {
			t.Fatalf("收到未预期的 ask: %+v", ask)
		}
	case <-time.After(time.Second):
		t.Fatalf("大载荷 >64KiB 导致 ask 接收超时（可能被 Scanner 截断）")
	}

	if err := srv.Respond("step_large", "allow", ""); err != nil {
		t.Fatalf("Respond 失败: %v", err)
	}
}

func TestPermServerDisconnectBeforeRespond(t *testing.T) {
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

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}

	askData, _ := json.Marshal(map[string]any{
		"type":        "ask",
		"tool_use_id": "step_disconnect",
		"tool_name":   "run_command",
		"input":       map[string]string{"CommandLine": "ls"},
	})
	_, _ = conn.Write(append(askData, '\n'))

	select {
	case <-asks:
	case <-time.After(time.Second):
		t.Fatalf("超时未收到 ask")
	}

	// 客户端主动断开连接
	_ = conn.Close()

	// 稍作等待让 read EOF 协程触发
	time.Sleep(50 * time.Millisecond)

	// 断开后 Respond 必须报错
	if err := srv.Respond("step_disconnect", "allow", ""); err == nil {
		t.Fatalf("客户端已断开时 Respond 必须报错，不得静默成功")
	}
}

func TestAdapterRespondPermissionEmptyReason(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "perm.sock")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ad := New(logger)
	r := ad.newRun("T-EmptyReason", sockDir, sockDir)

	srv, err := newPermServer(sockPath, logger, func(a permAsk) {
		ad.onPermissionAsk(r, a)
	})
	if err != nil {
		t.Fatalf("newPermServer 失败: %v", err)
	}
	defer srv.Close()
	r.permSrv = srv

	decisions := make(chan permDecision, 1)
	go func() {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			return
		}
		defer conn.Close()

		askData, _ := json.Marshal(map[string]any{
			"type":        "ask",
			"tool_use_id": "step_99",
			"tool_name":   "run_command",
			"input":       map[string]string{"CommandLine": "ls"},
		})
		_, _ = conn.Write(append(askData, '\n'))

		var dec permDecision
		_ = json.NewDecoder(bufio.NewReader(conn)).Decode(&dec)
		decisions <- dec
	}()

	select {
	case <-r.evCh:
	case <-time.After(time.Second):
		t.Fatalf("超时未收到 permission 事件")
	}

	// 传空 reason
	if err := ad.RespondPermission(context.Background(), "T-EmptyReason", "step_99", "reject", ""); err != nil {
		t.Fatalf("RespondPermission 失败: %v", err)
	}

	select {
	case dec := <-decisions:
		if dec.Behavior != "deny" {
			t.Fatalf("want deny, got %s", dec.Behavior)
		}
		if dec.Message != "协调者拒绝了本次操作" {
			t.Fatalf("空 reason 未回退为默认提示: %s", dec.Message)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到裁决")
	}
}
