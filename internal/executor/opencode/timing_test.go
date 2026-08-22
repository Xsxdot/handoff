// timing_test.go 钉住 opencode 工具 part 的去重、终态映射与耗时信号。
//
// 本文件不验时长算法；那是 internal/executor/turn 的职责。
package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestOpencodeToolTimingPaired 钉住「工具 part 的两端都喂给了段切分器」，
// 并钉住 opencode 特有的两条：running 重复到达只算一次、只有终态产结果帧。
func TestOpencodeToolTimingPaired(t *testing.T) {
	a := New(nil)
	r := a.newRun("timing-paired", t.TempDir(), t.TempDir())
	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	// ⚠ opencode 的 emit 是阻塞的、evCh 只有 16：不排空就会死锁（见计划 §1.4）
	timings := make(chan proto.TimingEntry, 256)
	done := collectTimings(r, timings)

	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	const base = `"id":"prt_1","messageID":"msg_1","type":"tool","tool":"bash","callID":"call_1"`
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"pending","input":{}}}}`)
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"running","input":{"command":"echo hi"}}}}`)
	// 留出可观测的毫秒间隔，避免真实耗时被 Duration.Milliseconds 截成 0
	time.Sleep(2 * time.Millisecond)
	// running 重复到达（真机会发很多条，输出边长边发）
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"running","input":{"command":"echo hi"}}}}`)
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"completed","input":{"command":"echo hi"},"output":"hi"}}}`)
	// 终态重复到达也不许再产一条
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"completed","input":{"command":"echo hi"},"output":"hi"}}}`)
	a.reportTiming(r, r.seg.EndTurn())

	closeEventsForTest(r)
	<-done
	close(timings)

	var tools []proto.TimingEntry
	for e := range timings {
		if e.Kind == proto.TimingKindTool {
			tools = append(tools, e)
		}
	}
	if len(tools) != 1 {
		t.Fatalf("一次工具调用应恰好产一条 tool 条目，实得 %d 条", len(tools))
	}
	if tools[0].Label != "bash" {
		t.Errorf("Label 应取 part.tool，实得 %q", tools[0].Label)
	}
	if tools[0].DurMS <= 0 {
		t.Errorf("配对成功时耗时应为正，实得 %d", tools[0].DurMS)
	}

	var calls, results []proto.Frame
	for _, f := range readFrames(t, r) {
		switch f.Type {
		case proto.FrameToolCall:
			calls = append(calls, f)
		case proto.FrameToolResult:
			results = append(results, f)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("重复的 running 不得重复产 tool_call 帧，实得 %d 条", len(calls))
	}
	if len(results) != 1 {
		t.Fatalf("重复的终态不得重复产 tool_result 帧，实得 %d 条", len(results))
	}
	if results[0].Status != "ok" || results[0].DurMS <= 0 {
		t.Errorf("终态帧应是 ok 且带正的 dur_ms，实得 %q / %d", results[0].Status, results[0].DurMS)
	}
	if !strings.Contains(results[0].Output, "hi") {
		t.Errorf("tool_result 帧应带 state.output，实得 %q", results[0].Output)
	}
}

// TestOpencodeToolTextDeltaStillSkipped 钉住既有不变式没被本次改动破坏：
// tool part 的**文本增量**照旧不产 text 帧（工具帧走另一条路）。
func TestOpencodeToolTextDeltaStillSkipped(t *testing.T) {
	if got := frameKind("tool"); got != kindSkip {
		t.Fatalf("tool part 的文本增量必须继续不产帧，实得 %v", got)
	}
}

// TestOpencodeErrorToolStatus 钉住被拒终止留下的 error 状态 tool part
// 产的是 error 结果帧（adapter.go mapIdle 的注释描述的正是这个现场）。
func TestOpencodeErrorToolStatus(t *testing.T) {
	a := New(nil)
	r := a.newRun("timing-error", t.TempDir(), t.TempDir())
	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	done := collectTimings(r, nil) // 排空 goroutine 不是可选的，理由见 §1.4

	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	const base = `"id":"prt_2","messageID":"msg_2","type":"tool","tool":"bash","callID":"call_2"`
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"running","input":{"command":"rm -rf /"}}}}`)
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"error","input":{"command":"rm -rf /"},"output":"权限被拒"}}}`)
	a.reportTiming(r, r.seg.EndTurn())

	closeEventsForTest(r)
	<-done

	var results []proto.Frame
	for _, f := range readFrames(t, r) {
		if f.Type == proto.FrameToolResult {
			results = append(results, f)
		}
	}
	if len(results) != 1 {
		t.Fatalf("error 是终态，应恰好产一条 tool_result 帧，实得 %d 条", len(results))
	}
	if results[0].Status != "error" {
		t.Errorf("error 状态应映射为 error，实得 %q", results[0].Status)
	}
}

// feedPart 在 turnMu 下喂一条 message.part.updated 载荷。
func feedPart(t *testing.T, a *Adapter, r *runState, js string) {
	t.Helper()
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	a.mapPartUpdated(r, json.RawMessage(js))
}

// collectTimings 起一个 goroutine 持续排空 evCh，把耗时条目转投 out
// （out 为 nil 时只排空）。返回的通道在通道关闭、排空结束后关闭。
//
// **排空不是可选的**：opencode 的 emit 阻塞在 evCh 上、缓冲只有 16，
// 不排空的测试会死锁（见计划 §1.4）。
func collectTimings(r *runState, out chan<- proto.TimingEntry) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range r.evCh {
			if out != nil && ev.Type == "usage" && ev.Timing != nil {
				out <- *ev.Timing
			}
		}
	}()
	return done
}

// closeEventsForTest 关掉事件通道让排空 goroutine 退出。
//
// 走 closeOnce 而不是直接调 closeEvents：adapter.go 的注释写明关闭权唯一
// 归 subscribeLoop 的 defer（adapter.go:967），而本测试没起订阅循环。
// 用 closeOnce.Do 与生产路径（adapter.go:780）同款，重复调用也安全。
func closeEventsForTest(r *runState) {
	r.closeOnce.Do(r.closeEvents)
}

// readFrames 读回本任务已落盘的帧。
func readFrames(t *testing.T, r *runState) []proto.Frame {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.taskDir, turn.FramesFileName))
	if err != nil {
		t.Fatalf("读 frames.jsonl: %v", err)
	}
	var out []proto.Frame
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var f proto.Frame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("解析帧 %q: %v", line, err)
		}
		out = append(out, f)
	}
	return out
}
