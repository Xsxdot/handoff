package agy

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailerScanOnce(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, outFileName)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tl := newTailer(outPath, 0, logger)

	var msgs []streamMsg
	garbage := 0

	// 写入一行完整的 init
	initLine := `{"event":"init","conversation_id":"conv-123","init":{"cwd":"/repo"}}` + "\n"
	os.WriteFile(outPath, []byte(initLine), 0600)

	adv, err := tl.scanOnce(func(m streamMsg) {
		msgs = append(msgs, m)
	}, &garbage)
	if err != nil {
		t.Fatalf("scanOnce 失败: %v", err)
	}
	if adv == 0 || len(msgs) != 1 {
		t.Fatalf("adv=%d msgs=%d, want 1 msg", adv, len(msgs))
	}
	if msgs[0].Event != "init" || msgs[0].ConversationID != "conv-123" {
		t.Fatalf("msg 内容不符合预期: %+v", msgs[0])
	}
}

func TestStreamLoopEndTurn(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, outFileName)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ad := New(logger)
	r := ad.newRun("T1", tmpDir, tmpDir)

	// 准备 out.jsonl
	lines := `{"event":"init","conversation_id":"c1"}
{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"hello"}}
{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"ok\n{\"ask\":\"继续吗?\"}"}}
`
	os.WriteFile(outPath, []byte(lines), 0600)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tl := newTailer(outPath, 0, logger)
	go func() {
		_ = tl.Run(ctx, func(m streamMsg) {
			ad.mapMessage(r, m)
		})
	}()

	select {
	case <-r.ready:
	case <-time.After(time.Second):
		t.Fatalf("未在时限内就绪")
	}

	var gotQuestion bool
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "question" {
				if ev.Text != "继续吗?" {
					t.Fatalf("预期收到 question '继续吗?', got %s", ev.Text)
				}
				gotQuestion = true
				break
			}
		case <-time.After(time.Second):
			t.Fatalf("超时未收到 question 事件")
		}
		if gotQuestion {
			break
		}
	}
}
