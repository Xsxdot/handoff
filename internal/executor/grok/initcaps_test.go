// initcaps_test.go —— 钉死 ACP 握手里 clientCapabilities.fs 的取值（B139）。
//
// 为什么单独一个文件：这条断言防的不是某个函数的返回值，而是一条**跨两个协议
// 事实**的耦合——「声明 fs 能力」与「不处理 fs/* 入站请求」必须同时成立或同时
// 不成立。放在 acp_test.go 的一堆连接语义测试里，读的人会以为它只是个字段断言。
package grok_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/grok"
)

// TestInitializeDeclaresNoClientFileSystem 断言真实的会话建立路径（openSession）
// 发出的 initialize 里，clientCapabilities.fs 两个开关都是 false。
//
// 为什么这条不能松：ACP 里这两个开关的语义是「文件由客户端代读代写」，不是
// 「我允许 agent 读写文件」。一旦声明 true，grok 就把 write / search_replace 的
// 落盘改成回调客户端的 fs/write_text_file；而本客户端只处理
// session/request_permission 与 _x.ai/ask_user_question，其余入站请求一律回
// -32601 "unhandled"（见 TestUnknownAgentRequestGetsMethodNotFound）。两条事实
// 撞在一起的结果是每一次写文件都以 `Tool write failed: IO Error: unhandled` 告终、
// 文件一个都不落盘——2026-08-18 在 macOS 与 win-b37 上双向复现（B139）。
//
// 症状离根因很远（报文长得像 grok 自己的 IO 错误，实际那句 "unhandled" 是
// acp.go 写的），所以这里断言的是**出站帧原文**而不是 initializeParams 的返回值：
// 帧才是对端看见的东西。
func TestInitializeDeclaresNoClientFileSystem(t *testing.T) {
	sent := make(chan string, 8)
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		switch req.Method {
		case "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":1}}`}
		case "session/new":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"sessionId":"s1"}}`}
		}
		return nil
	}, sent)

	if err := grok.StartSessionForTest(wsURL(srv), t.TempDir()); err != nil {
		t.Fatalf("建会话失败: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case raw := <-sent:
			var frame struct {
				Method string `json:"method"`
				Params struct {
					ClientCapabilities struct {
						FS *struct {
							ReadTextFile  bool `json:"readTextFile"`
							WriteTextFile bool `json:"writeTextFile"`
						} `json:"fs"`
					} `json:"clientCapabilities"`
				} `json:"params"`
			}
			if json.Unmarshal([]byte(raw), &frame) != nil || frame.Method != "initialize" {
				continue
			}
			fs := frame.Params.ClientCapabilities.FS
			if fs == nil {
				t.Fatalf("initialize 未带 clientCapabilities.fs，实得: %s", raw)
			}
			if fs.WriteTextFile {
				t.Errorf("声明了 fs.writeTextFile=true —— grok 会把落盘委托给本客户端，" +
					"而入站 fs/write_text_file 会被回 -32601，写文件必全失败（B139）")
			}
			if fs.ReadTextFile {
				t.Errorf("声明了 fs.readTextFile=true —— 同上，读文件会被委托到一条没人接的路上（B139）")
			}
			return
		case <-deadline:
			t.Fatal("没抓到 initialize 出站帧")
		}
	}
}
