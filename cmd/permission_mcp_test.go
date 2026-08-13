package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandleRPCInitializeAndList(t *testing.T) {
	resp, ok := handleRPC(rpcRequest{ID: json.RawMessage(`1`), Method: "initialize"}, "/tmp/x.sock")
	if !ok {
		t.Fatal("initialize 必须有响应")
	}
	b, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(b), "protocolVersion") {
		t.Errorf("initialize 结果缺 protocolVersion: %s", b)
	}

	resp, ok = handleRPC(rpcRequest{ID: json.RawMessage(`2`), Method: "tools/list"}, "/tmp/x.sock")
	if !ok {
		t.Fatal("tools/list 必须有响应")
	}
	b, _ = json.Marshal(resp.Result)
	if !strings.Contains(string(b), `"ask"`) {
		t.Errorf("tools/list 未暴露 ask 工具: %s", b)
	}
	if !strings.Contains(string(b), "handoff coordinator") {
		t.Errorf("ask 工具描述应为 coordinator: %s", b)
	}
	if strings.Contains(string(b), "reviewer") {
		t.Errorf("ask 工具描述不应再含 reviewer: %s", b)
	}
}

func TestHandleRPCNotificationNoResponse(t *testing.T) {
	// 通知（无 id）不得产生响应，否则 claude 侧会把它当成孤儿响应
	if _, ok := handleRPC(rpcRequest{Method: "notifications/initialized"}, "/tmp/x.sock"); ok {
		t.Error("通知不应产生响应")
	}
}

// tools/call 全链路：起一个假 server 扮演 adapter，校验 ask 帧内容与裁决回传。
func TestHandleRPCToolsCallRoundTrip(t *testing.T) {
	// 注意：不能用 t.TempDir()——macOS unix socket 路径上限 104 字节，
	// 长测试名嵌进临时目录路径会让 bind 报 invalid argument
	sock := filepath.Join(shortSockDir(t), "perm.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var ask struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			ToolName  string          `json:"tool_name"`
			Input     json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(conn).Decode(&ask); err != nil {
			return
		}
		if ask.ToolUseID != "toolu_9" || ask.ToolName != "Bash" {
			t.Errorf("ask 帧内容不符: %+v", ask)
		}
		conn.Write([]byte(`{"type":"decision","behavior":"deny","message":"不批"}` + "\n"))
	}()

	params, _ := json.Marshal(map[string]any{
		"arguments": map[string]any{
			"tool_name":   "Bash",
			"tool_use_id": "toolu_9",
			"input":       map[string]any{"command": "rm -rf x"},
		},
	})
	done := make(chan rpcResponse, 1)
	go func() {
		resp, _ := handleRPC(rpcRequest{ID: json.RawMessage(`3`), Method: "tools/call", Params: params}, sock)
		done <- resp
	}()

	select {
	case resp := <-done:
		b, _ := json.Marshal(resp.Result)
		if !strings.Contains(string(b), `deny`) || !strings.Contains(string(b), "不批") {
			t.Errorf("裁决未透传回 claude: %s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内未拿到裁决")
	}
}

// shortSockDir 建一个短路径目录（os.TempDir 下用短随机名），供 unix socket 使用，
// 避开 macOS 的 104 字节 sockaddr 路径上限；随测试结束清理。
func shortSockDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("psock-%d", time.Now().UnixNano()%1e9))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
