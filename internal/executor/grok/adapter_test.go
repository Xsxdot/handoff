package grok_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor/grok"
)

// TestMapUpdateRoutesByKind 验证四类 session/update 的分流：
// 正文进回合文本、thought 与 tool_call 只进 render.log、私有通知忽略。
func TestMapUpdateRoutesByKind(t *testing.T) {
	f, err := os.Open("testdata/updates.jsonl")
	if err != nil {
		t.Fatalf("读 testdata 失败: %v", err)
	}
	defer f.Close()

	h := grok.NewTurnAccumulatorForTest()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		h.FeedRawForTest([]byte(line))
	}

	// 回合正文只应含 agent_message_chunk 的内容
	body := h.TurnTextForTest()
	if !strings.Contains(body, "我要改 foo.go") {
		t.Errorf("回合正文缺 agent_message_chunk 内容: %q", body)
	}
	if strings.Contains(body, "思考中") {
		t.Errorf("推理流不得进回合正文（会污染 trailer 解析）: %q", body)
	}
	if strings.Contains(body, "run_terminal_command") {
		t.Errorf("工具调用不得进回合正文: %q", body)
	}

	// render.log 应同时含正文、推理与工具动作
	render := h.RenderTextForTest()
	for _, want := range []string{"我要改 foo.go", "思考中", "echo hi"} {
		if !strings.Contains(render, want) {
			t.Errorf("render.log 缺 %q，实际: %q", want, render)
		}
	}
}

// TestTurnTextEndsWithTrailerSoParseWorks 验证累积后的正文能被 turn.ParseTrailer 判为 ask。
func TestTurnTextEndsWithTrailerSoParseWorks(t *testing.T) {
	f, _ := os.Open("testdata/updates.jsonl")
	defer f.Close()
	h := grok.NewTurnAccumulatorForTest()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			h.FeedRawForTest([]byte(line))
		}
	}
	kind, tr := h.ClassifyForTest()
	if kind != "ask" {
		t.Fatalf("分类 = %q，期望 ask（正文以 {\"ask\":...} 收尾）", kind)
	}
	if tr.Question != "用哪个库？" {
		t.Errorf("Question = %q", tr.Question)
	}
}

// TestSessionNewAuthErrorGivesActionableMessage 固定 spec §8：凭据问题重试无用，
// 必须给出「跑 grok login」的可操作指引，而不是一个裸的 ACP 错误码。
func TestSessionNewAuthErrorGivesActionableMessage(t *testing.T) {
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
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) +
				`,"error":{"code":-32000,"message":"Authentication required","data":"no auth method id provided"}}`}
		}
		return nil
	}, nil)
	err := grok.StartSessionForTest(wsURL(srv), t.TempDir())
	if err == nil {
		t.Fatal("auth 失败必须返回错误")
	}
	if !strings.Contains(err.Error(), "grok login") {
		t.Errorf("错误必须可操作（提示跑 grok login），实际: %v", err)
	}
}

// TestStopDoesNotEmitFailedResult 钉住 Stop 与 onClosed 的竞态：Stop 主动关连接
// 会触发读循环退出→OnClosed→onClosed，后者不得产出「ACP 连接断开」的假失败结果
// （真实原因是用户主动停了，不是执行失败）。
func TestStopDoesNotEmitFailedResult(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string { return nil }, nil) // 永不回应
	h := newFakeHandler()
	a, r := grok.NewAdapterWithRunForTest("t1")
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	r.AttachClientForTest(cli)

	if err := a.Stop("t1"); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}

	// Stop 关闭了事件通道：收集直到关闭，期间不得出现 OK=false 的 result
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-r.EventsForTest():
			if !ok {
				return
			}
			if ev.Type == "result" && ev.Result != nil && !ev.Result.OK {
				t.Fatalf("Stop 之后出现假的失败 result: %+v", ev.Result)
			}
		case <-deadline:
			t.Fatal("事件通道未在时限内关闭")
		}
	}
}
