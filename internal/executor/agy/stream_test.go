package agy

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
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

func TestStreamGoldenTurnSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, outFileName)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	goldenData, err := os.ReadFile(filepath.Join("testdata", "turn_success.jsonl"))
	if err != nil {
		t.Fatalf("读取 testdata/turn_success.jsonl 失败: %v", err)
	}
	if err := os.WriteFile(outPath, goldenData, 0600); err != nil {
		t.Fatalf("写入 out.jsonl 失败: %v", err)
	}

	ad := New(logger)
	r := ad.newRun("T1", tmpDir, tmpDir)

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
		t.Fatalf("未就绪")
	}

	var gotResult bool
	var gotUsage bool
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "usage" && ev.Spend != nil {
				gotUsage = true
				if ev.Spend.InputTokens != 220 || ev.Spend.OutputTokens != 65 {
					t.Fatalf("spend 数据不符合预期: %+v", ev.Spend)
				}
			}
			if ev.Type == "result" {
				if !ev.Result.OK || ev.Result.Branch != "feature-1" || ev.Result.CommitHash != "abcdef12" {
					t.Fatalf("result 数据不符合预期: %+v", ev.Result)
				}
				gotResult = true
				break
			}
		case <-time.After(time.Second):
			t.Fatalf("等待事件超时")
		}
		if gotResult {
			break
		}
	}
	if !gotUsage {
		t.Fatalf("未收到 usage/spend 事件")
	}
}

func TestStreamMultiTurnSpendStableKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ad := New(logger)
	r := ad.newRun("T-Spend", t.TempDir(), t.TempDir())
	r.session = "conv-stable-key"

	// Turn 1 result
	ad.mapMessage(r, streamMsg{
		Event: "result",
		Result: &agyResultData{
			ConversationID: "conv-stable-key",
			Status:         "SUCCESS",
			NumTurns:       1,
			Response:       "turn 1 ok",
			Usage: &AgyUsageRaw{
				InputTokens:  100,
				OutputTokens: 20,
			},
		},
	})

	var ev1 executor.AdapterEvent
	select {
	case ev1 = <-r.evCh:
		if ev1.Type != "usage" || ev1.Spend == nil {
			t.Fatalf("Turn 1 未产生 spend 事件: %+v", ev1)
		}
		if ev1.Spend.Key != "conv-stable-key-spend" || ev1.Spend.InputTokens != 100 {
			t.Fatalf("Turn 1 spend 条目不正确: %+v", ev1.Spend)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到 turn 1 spend 事件")
	}

	// Drain any question/result event from turn 1
	select {
	case <-r.evCh:
	case <-time.After(100 * time.Millisecond):
	}

	// Turn 2 result (cumulative usage = 250)
	ad.mapMessage(r, streamMsg{
		Event: "result",
		Result: &agyResultData{
			ConversationID: "conv-stable-key",
			Status:         "SUCCESS",
			NumTurns:       2,
			Response:       "turn 2 ok",
			Usage: &AgyUsageRaw{
				InputTokens:  250,
				OutputTokens: 50,
			},
		},
	})

	select {
	case ev2 := <-r.evCh:
		if ev2.Type != "usage" || ev2.Spend == nil {
			t.Fatalf("Turn 2 未产生 spend 事件: %+v", ev2)
		}
		// 关键断言：Key 必须与 Turn 1 相同，以实现 store 中的覆盖（会话累计语义），而不是求和产生 350
		if ev2.Spend.Key != ev1.Spend.Key {
			t.Fatalf("Turn 2 key (%s) 必须与 Turn 1 key (%s) 相同", ev2.Spend.Key, ev1.Spend.Key)
		}
		if ev2.Spend.InputTokens != 250 {
			t.Fatalf("Turn 2 InputTokens 必须为累计 250，实得 %d", ev2.Spend.InputTokens)
		}
	case <-time.After(time.Second):
		t.Fatalf("超时未收到 turn 2 spend 事件")
	}
}
