package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
)

func TestServePermissionHookAllow(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "perm.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var ask map[string]any
		_ = json.NewDecoder(conn).Decode(&ask)

		// 模拟回复 allow
		_ = json.NewEncoder(conn).Encode(map[string]string{
			"behavior": "allow",
		})
	}()

	input := `{"toolCall":{"name":"run_command","args":{"CommandLine":"ls"}},"stepIdx":1}`
	var out bytes.Buffer

	if err := servePermissionHook(bytes.NewBufferString(input), &out, sockPath); err != nil {
		t.Fatalf("servePermissionHook 失败: %v", err)
	}

	var res hookDecisionOutput
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("解析输出 JSON 失败: %v", err)
	}
	if res.Decision != "allow" {
		t.Fatalf("want allow, got %s", res.Decision)
	}
}

func TestServePermissionHookDenyWithReasonAndStep0(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "perm.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}
	defer ln.Close()

	var receivedToolUseID string
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var ask struct {
			ToolUseID string `json:"tool_use_id"`
		}
		_ = json.NewDecoder(conn).Decode(&ask)
		receivedToolUseID = ask.ToolUseID

		_ = json.NewEncoder(conn).Encode(map[string]string{
			"behavior": "deny",
			"message":  "security violation: command not allowed",
		})
	}()

	input := `{"toolCall":{"name":"run_command","args":{"CommandLine":"rm -rf /"}},"stepIdx":0}`
	var out bytes.Buffer

	if err := servePermissionHook(bytes.NewBufferString(input), &out, sockPath); err != nil {
		t.Fatalf("servePermissionHook 失败: %v", err)
	}

	if receivedToolUseID != "step_0" {
		t.Fatalf("tool_use_id 应为 step_0，实得 %s", receivedToolUseID)
	}

	var res hookDecisionOutput
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("解析输出 JSON 失败: %v", err)
	}
	if res.Decision != "deny" || res.Reason != "security violation: command not allowed" {
		t.Fatalf("不符合预期: %+v", res)
	}
}
