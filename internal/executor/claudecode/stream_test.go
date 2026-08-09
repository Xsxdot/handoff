package claudecode

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailerParsesRealSample(t *testing.T) {
	src, err := os.ReadFile("testdata/turn_success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), outFileName)
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var kinds []string
	var sessionID, resultText string
	tl := newTailer(path, 0, slog.Default())
	go tl.Run(ctx, func(m streamMsg) {
		switch {
		case m.Type == "system" && m.Subtype == "init":
			kinds = append(kinds, "init")
			sessionID = m.SessionID
		case m.Type == "result":
			kinds = append(kinds, "result")
			resultText = m.Result
			cancel()
		}
	})
	<-ctx.Done()

	if sessionID != "sess-1" {
		t.Errorf("session_id=%q want sess-1", sessionID)
	}
	if resultText == "" {
		t.Error("未取到回合文本（trailer 解析的输入）")
	}
	if len(kinds) < 2 {
		t.Errorf("事件序列不完整: %v", kinds)
	}
	if tl.Offset() <= 0 {
		t.Error("offset 应随消费推进")
	}
}

func TestTailerResumeFromOffsetSkipsConsumed(t *testing.T) {
	path := filepath.Join(t.TempDir(), outFileName)
	first := `{"type":"system","subtype":"init","session_id":"s"}` + "\n"
	if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	// 从已消费的 offset 起读：这一行不得重放，否则 agentd 重启会把旧回合再走一遍
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	seen := 0
	tl := newTailer(path, int64(len(first)), slog.Default())
	go tl.Run(ctx, func(streamMsg) { seen++ })
	<-ctx.Done()
	if seen != 0 {
		t.Errorf("offset 之前的行被重放了 %d 次", seen)
	}
}

func TestTailerToleratesGarbageLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), outFileName)
	content := "这不是 JSON\n" + `{"type":"result","subtype":"success","result":"ok"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got := ""
	tl := newTailer(path, 0, slog.Default())
	go tl.Run(ctx, func(m streamMsg) {
		if m.Type == "result" {
			got = m.Result
			cancel()
		}
	})
	<-ctx.Done()
	// 非 JSON 行必须跳过而不是中断解析循环——claude 偶发往 stdout 打非协议内容时
	// 不能让整个任务失联
	if got != "ok" {
		t.Errorf("非 JSON 行后应继续解析，result=%q", got)
	}
}

func TestTextDeltaIgnoresThinking(t *testing.T) {
	thinking := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"内心戏"}}`)
	if _, ok := textDelta(thinking); ok {
		t.Error("thinking_delta 不得进 render.log 与回合文本（与 opencode 的 reasoning 隔离一致）")
	}
	text := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"正文"}}`)
	got, ok := textDelta(text)
	if !ok || got != "正文" {
		t.Errorf("text_delta 提取失败: %q ok=%v", got, ok)
	}
}
