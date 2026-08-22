package claudecode

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestClaudeToolTimingPaired 钉住「工具调用的两端都喂给了段切分器」。
//
// 它不验时长算得对不对（那是 turn 包的事），只验**信号有没有喂对**：
// 一次配对的 tool_use/tool_result 必须产出 tool 条目，且帧上带 dur_ms。
func TestClaudeToolTimingPaired(t *testing.T) {
	src, err := os.Open(filepath.Join("testdata", "turn_success.jsonl"))
	if err != nil {
		t.Fatalf("读 testdata: %v", err)
	}
	defer src.Close()

	taskDir := t.TempDir()
	a := New(nil)
	r := a.newRun("timing-paired", taskDir, t.TempDir())
	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	r.textPart = r.frames.NextPart()
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))

	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var m streamMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("解析夹具: %v", err)
		}
		// 给真实工具调用/结果之间留下可观测的毫秒间隔，避免帧上的真实耗时
		// 因测试回放过快而被 time.Duration.Milliseconds 截成 0。
		if m.Type == "user" {
			time.Sleep(2 * time.Millisecond)
		}
		a.mapMessage(r, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读夹具: %v", err)
	}

	var timings []proto.TimingEntry
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "usage" && ev.Timing != nil {
				timings = append(timings, *ev.Timing)
			}
		default:
			goto drained
		}
	}
drained:
	var tool *proto.TimingEntry
	var hasTurn bool
	for i := range timings {
		e := timings[i]
		if e.Kind == proto.TimingKindTool {
			copy := e
			tool = &copy
		}
		if e.Kind == proto.TimingKindTurn {
			hasTurn = true
		}
	}
	if tool == nil {
		t.Fatal("至少应有一条 tool TimingEntry")
	}
	if tool.Label != "Bash" || !strings.Contains(tool.Detail, "go test") {
		t.Errorf("工具条目的 Label/Detail 不符：%q / %q", tool.Label, tool.Detail)
	}
	if !hasTurn {
		t.Fatal("至少应有一条 turn TimingEntry")
	}

	framesPath := filepath.Join(taskDir, turn.FramesFileName)
	raw, err := os.ReadFile(framesPath)
	if err != nil {
		t.Fatalf("读 frames.jsonl: %v", err)
	}
	var hasCall, hasResult bool
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var frame proto.Frame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("解析帧 %q: %v", line, err)
		}
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
	if !hasCall {
		t.Fatal("夹具应产出 tool_call 帧")
	}
	if !hasResult {
		t.Fatal("夹具应产出 tool_result 帧")
	}
}
