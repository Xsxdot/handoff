package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

type timingTestClock struct{ t time.Time }

func (c *timingTestClock) now() time.Time      { return c.t }
func (c *timingTestClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTimingTestRun(t *testing.T) (*Adapter, *runState, *timingTestClock) {
	t.Helper()
	clock := &timingTestClock{t: time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)}
	taskDir := t.TempDir()
	a := New(nil)
	r := a.newRunState("timing-test", taskDir, taskDir)
	r.seg = turn.NewSegmenter(clock.now)
	r.threadID = "thread-timing"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			_, data, err := conn.Read(req.Context())
			if err != nil {
				return
			}
			var in struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(data, &in) != nil || in.Method != methodTurnStart {
				continue
			}
			response := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"turn":{"id":"turn-timing"}}}`, in.ID)
			if err := conn.Write(req.Context(), websocket.MessageText, []byte(response)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	cli, err := Dial(context.Background(), "ws"+srv.URL[len("http"):], &handler{a: a, r: r}, a.log)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	r.cli = cli
	a.mu.Lock()
	a.runs[r.taskID] = r
	a.mu.Unlock()
	t.Cleanup(func() { _ = a.Stop(r.taskID) })
	if err := a.Send(context.Background(), r.taskID, "继续"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return a, r, clock
}

func feedTimingTestTool(t *testing.T, a *Adapter, r *runState, c *timingTestClock) {
	t.Helper()
	a.appendItemFrame(r, ntfItemStarted, &threadItem{
		Type: "commandExecution", ID: "item-1", Command: "go test ./...",
	})
	c.add(1500 * time.Millisecond)
	exitCode := 0
	a.appendItemFrame(r, ntfItemCompleted, &threadItem{
		Type: "commandExecution", ID: "item-1", Command: "go test ./...", ExitCode: &exitCode,
	})
	c.add(500 * time.Millisecond)
	// ask trailer keeps this test focused on timing; finishTurn still exercises
	// the real回合收尾入口 and produces the final api/turn entries.
	a.finishTurn(r, "completed", "", `{"ask":"下一步？"}`)
}

func collectTimingTestEntries(r *runState) []proto.TimingEntry {
	var out []proto.TimingEntry
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "usage" && ev.Timing != nil {
				out = append(out, *ev.Timing)
			}
		default:
			return out
		}
	}
}

func readTimingTestFrames(t *testing.T, r *runState) []proto.Frame {
	t.Helper()
	f, err := os.Open(filepath.Join(r.taskDir, turn.FramesFileName))
	if err != nil {
		t.Fatalf("打开 frames.jsonl: %v", err)
	}
	defer f.Close()
	var out []proto.Frame
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var frame proto.Frame
		if err := json.Unmarshal(sc.Bytes(), &frame); err != nil {
			t.Fatalf("解析帧: %v", err)
		}
		out = append(out, frame)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("扫描帧: %v", err)
	}
	return out
}

func TestCodexToolTimingPaired(t *testing.T) {
	a, r, c := newTimingTestRun(t)
	feedTimingTestTool(t, a, r, c)

	entries := collectTimingTestEntries(r)
	var tool *proto.TimingEntry
	var hasTurn bool
	for i := range entries {
		if entries[i].Kind == proto.TimingKindTool {
			copy := entries[i]
			tool = &copy
		}
		if entries[i].Kind == proto.TimingKindTurn {
			hasTurn = true
		}
	}
	if tool == nil {
		t.Fatal("至少应有一条 tool TimingEntry")
	}
	if tool.Label != "commandExecution" || tool.Detail != "go test ./..." {
		t.Errorf("工具条目的 Label/Detail 不符：%q / %q", tool.Label, tool.Detail)
	}
	if !hasTurn {
		t.Fatal("至少应有一条 turn TimingEntry")
	}

	frames := readTimingTestFrames(t, r)
	var hasCall, hasResult bool
	for _, frame := range frames {
		switch frame.Type {
		case proto.FrameToolCall:
			hasCall = true
			if frame.DurMS != 0 {
				t.Errorf("tool_call 不应带 dur_ms，实得 %d", frame.DurMS)
			}
		case proto.FrameToolResult:
			hasResult = true
			if frame.DurMS <= 0 {
				t.Errorf("tool_result 应带正 dur_ms，实得 %d", frame.DurMS)
			}
		}
	}
	if !hasCall || !hasResult {
		t.Fatalf("应同时产出 tool_call 与 tool_result，call=%v result=%v", hasCall, hasResult)
	}
}

// TestCodexTimingShapeMatchesClaude 钉住「同形状的回合，两家产出的条目种类与
// 数量相同」。
//
// 它不比时长（那当然不同），只比结构：一次「模型输出 → 一个工具 → 模型输出」
// 的回合，两家都应产出 2 个 api 条目 + 1 个 tool 条目 + ≥1 个 turn 条目。
// 这条一旦红，说明共用切分器在两家上算出的段结构不同构 —— 此时**退回 P1 的
// 选项 (a)**，不要在 codex 侧打补丁把它掰成一样。
func TestCodexTimingShapeMatchesClaude(t *testing.T) {
	a, r, c := newTimingTestRun(t)
	feedTimingTestTool(t, a, r, c)

	entries := collectTimingTestEntries(r)
	var api, tool, turnCount int
	for _, entry := range entries {
		switch entry.Kind {
		case proto.TimingKindAPI:
			api++
		case proto.TimingKindTool:
			tool++
		case proto.TimingKindTurn:
			turnCount++
		}
	}
	if api != 2 || tool != 1 || turnCount < 1 {
		t.Fatalf("codex 与 claude 的段结构应为 api=2 tool=1 turn>=1，实得 api=%d tool=%d turn=%d",
			api, tool, turnCount)
	}
}
