package claudecode

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// dialAsk 模拟 MCP 进程：连 socket、发一条 ask、返回连接供读裁决。
func dialAsk(t *testing.T, sock, toolUseID, toolName, inputJSON string) net.Conn {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("连 socket: %v", err)
	}
	line, _ := json.Marshal(map[string]any{
		"type": "ask", "tool_use_id": toolUseID, "tool_name": toolName,
		"input": json.RawMessage(inputJSON),
	})
	if _, err := c.Write(append(line, '\n')); err != nil {
		t.Fatalf("写 ask: %v", err)
	}
	return c
}

func TestPermServerAskThenRespond(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "perm.sock")
	asks := make(chan permAsk, 4)
	srv, err := newPermServer(sock, slog.Default(), func(a permAsk) { asks <- a })
	if err != nil {
		t.Fatalf("newPermServer: %v", err)
	}
	defer srv.Close()

	conn := dialAsk(t, sock, "toolu_1", "Bash", `{"command":"rm -rf x"}`)
	defer conn.Close()

	select {
	case a := <-asks:
		if a.ToolUseID != "toolu_1" || a.ToolName != "Bash" {
			t.Fatalf("登记内容不符: %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未收到 ask 回调")
	}

	if err := srv.Respond("toolu_1", "allow", ""); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	var got struct {
		Type     string `json:"type"`
		Behavior string `json:"behavior"`
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&got); err != nil {
		t.Fatalf("读裁决: %v", err)
	}
	if got.Behavior != "allow" {
		t.Errorf("behavior=%q want allow", got.Behavior)
	}
}

func TestPermServerRespondUnknownID(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "perm.sock")
	srv, err := newPermServer(sock, slog.Default(), func(permAsk) {})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	// 挂起请求不存在（进程已死 / 请求被重试替换）必须报错，不能静默成功——
	// 静默成功会让 manager 以为裁决已送达，任务永远等不到下一步
	if err := srv.Respond("toolu_missing", "allow", ""); err == nil {
		t.Fatal("对未知 id 应报错")
	}
}

// 断线重连：同一 tool_use_id 用新连接重新登记，裁决必须回到新连接上。
// 这是 agentd 重启后能继续裁决的关键路径。
func TestPermServerReRegisterSameID(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "perm.sock")
	asks := make(chan permAsk, 4)
	srv, err := newPermServer(sock, slog.Default(), func(a permAsk) { asks <- a })
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	old := dialAsk(t, sock, "toolu_1", "Bash", `{"command":"ls"}`)
	<-asks
	old.Close() // 模拟 agentd 重启导致的连接断开

	fresh := dialAsk(t, sock, "toolu_1", "Bash", `{"command":"ls"}`)
	defer fresh.Close()
	select {
	case a := <-asks:
		if a.ToolUseID != "toolu_1" {
			t.Fatalf("重登记 id 不符: %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("重登记未触发回调（manager 侧 ticket 幂等，这里必须重发）")
	}
	if err := srv.Respond("toolu_1", "deny", "不批"); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	var got struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(bufio.NewReader(fresh)).Decode(&got); err != nil {
		t.Fatalf("新连接读裁决: %v", err)
	}
	if got.Behavior != "deny" || got.Message != "不批" {
		t.Errorf("裁决未回到新连接: %+v", got)
	}
}
