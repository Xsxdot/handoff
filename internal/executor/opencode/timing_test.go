// timing_test.go 钉住 opencode 工具 part 的去重、终态映射与耗时信号。
//
// 本文件不验时长算法；那是 internal/executor/turn 的职责。
package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
)

type timingRoundTripper func(*http.Request) (*http.Response, error)

func (f timingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestOpencodeToolTimingPaired 钉住「工具 part 的两端都喂给了段切分器」，
// 并钉住 opencode 特有的两条：running 重复到达只算一次、只有终态产结果帧。
func TestOpencodeToolTimingPaired(t *testing.T) {
	a, _ := startFakeRun(t, newFakeServer(t), "timing-paired", t.TempDir(), t.TempDir())
	r := a.lookup("timing-paired")
	events, done := collectEvents(r)
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
	finishIdleForTimingTest(a, r)
	timings := waitForResultEvents(t, events)
	_ = a.Stop(r.taskID)
	<-done

	var tools []proto.TimingEntry
	for _, e := range timings {
		if e.Kind == proto.TimingKindTool {
			tools = append(tools, e)
		}
	}
	if len(tools) != 1 {
		t.Fatalf("一次工具调用应恰好产一条 tool 条目，实得 %d 条", len(tools))
	}
	if got := countTimingKind(timings, proto.TimingKindAPI); got != 2 {
		t.Errorf("真实 BeginTurn 与 mapIdle EndTurn 都应收尾模型段，实得 %d 个 api 条目", got)
	}
	if tools[0].Label != "bash" {
		t.Errorf("Label 应取 part.tool，实得 %q", tools[0].Label)
	}
	if tools[0].DurMS <= 0 {
		t.Errorf("配对成功时耗时应为正，实得 %d", tools[0].DurMS)
	}
	if tools[0].Detail != "echo hi" {
		t.Errorf("tool TimingEntry.Detail 应取非空 input.command，实得 %q", tools[0].Detail)
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
	if !strings.Contains(calls[0].Input, `"echo hi"`) {
		t.Errorf("tool_call 帧应等到非空 input 后写入并含真实命令，实得 %q", calls[0].Input)
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

// TestOpencodeToolInputFallsBackAtTerminal 钉住没有任何非空 input 时的诚实回落：
// 终态前仍应补一条 tool_call 帧，但不能伪造命令内容。
func TestOpencodeToolInputFallsBackAtTerminal(t *testing.T) {
	a, _ := startFakeRun(t, newFakeServer(t), "timing-empty-input", t.TempDir(), t.TempDir())
	r := a.lookup("timing-empty-input")
	events, done := collectEvents(r)
	const base = `"id":"prt_empty","messageID":"msg_empty","type":"tool","tool":"bash","callID":"call_empty"`
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"pending","input":{}}}}`)
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"completed","input":{},"output":"无输入"}}}`)
	finishIdleForTimingTest(a, r)
	timings := waitForResultEvents(t, events)
	_ = a.Stop(r.taskID)
	<-done

	var calls []proto.Frame
	for _, f := range readFrames(t, r) {
		if f.Type == proto.FrameToolCall {
			calls = append(calls, f)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("终态前应补恰好一条回落 tool_call 帧，实得 %d 条", len(calls))
	}
	if calls[0].Input != "{}" {
		t.Errorf("没有非空 input 时应诚实保留空对象，实得 %q", calls[0].Input)
	}
	var tool *proto.TimingEntry
	for _, e := range timings {
		if e.Kind == proto.TimingKindTool {
			copy := e
			tool = &copy
		}
	}
	if tool == nil {
		t.Fatal("终态回落仍应产出 tool TimingEntry")
	}
	if tool.Detail != "{}" {
		t.Errorf("没有非空 input 时 Detail 应诚实保留空对象，实得 %q", tool.Detail)
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
	a, _ := startFakeRun(t, newFakeServer(t), "timing-error", t.TempDir(), t.TempDir())
	r := a.lookup("timing-error")
	events, done := collectEvents(r) // 排空 goroutine 不是可选的，理由见 §1.4
	const base = `"id":"prt_2","messageID":"msg_2","type":"tool","tool":"bash","callID":"call_2"`
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"running","input":{"command":"rm -rf /"}}}}`)
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"error","input":{"command":"rm -rf /"},"output":"权限被拒"}}}`)
	finishIdleForTimingTest(a, r)
	timings := waitForResultEvents(t, events)
	_ = a.Stop(r.taskID)
	<-done
	if got := countTimingKind(timings, proto.TimingKindAPI); got != 2 {
		t.Errorf("真实 BeginTurn 与 mapIdle EndTurn 都应收尾模型段，实得 %d 个 api 条目", got)
	}

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

func TestOpencodePermissionWaitNotToolTime(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	taskDir := t.TempDir()
	a, _ := startFakeRun(t, fs, "permission-timing", t.TempDir(), taskDir)
	r := a.lookup("permission-timing")
	events, done := collectEvents(r)

	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	r.seg = turn.NewSegmenter(func() time.Time { return now })
	a.reportTiming(r, r.seg.BeginTurn(1))
	now = now.Add(2 * time.Second)
	r.turnMu.Lock()
	a.mapToolPart(r, "bash", "call-1", "running", json.RawMessage(`{"command":"git add && git commit"}`), "")
	r.turnMu.Unlock()
	now = now.Add(3 * time.Second)
	permission := json.RawMessage(`{
		"id":"perm-1","sessionID":"sess-1","permission":"bash",
		"metadata":{"command":"git add && git commit"},
		"tool":{"callID":"call-1"}
	}`)
	r.turnMu.Lock()
	a.mapPermissionAsked(r, permission)
	// SSE 重放同一 permission.asked 不得重新打开等待窗口。
	a.mapPermissionAsked(r, permission)
	r.turnMu.Unlock()

	now = now.Add(60 * time.Second)
	if err := a.RespondPermission(context.Background(), r.taskID, "perm-1", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	now = now.Add(5 * time.Second)
	r.turnMu.Lock()
	a.mapToolPart(r, "bash", "call-1", "completed", json.RawMessage(`{"command":"git add && git commit"}`), "ok")
	r.turnMu.Unlock()
	now = now.Add(2 * time.Second)
	a.reportTiming(r, r.seg.EndTurn())

	if err := a.Stop(r.taskID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-done

	var timings []proto.TimingEntry
	for ev := range events {
		if ev.Type == "usage" && ev.Timing != nil {
			timings = append(timings, *ev.Timing)
		}
	}
	var tool, api, total int64
	for _, e := range timings {
		switch e.Kind {
		case proto.TimingKindTool:
			tool += e.DurMS
		case proto.TimingKindAPI:
			api += e.DurMS
		case proto.TimingKindTurn:
			if e.DurMS > total {
				total = e.DurMS
			}
		}
	}
	if tool != 8000 || api != 4000 || total != 72000 {
		t.Fatalf("opencode 权限等待不应进入工具段，实得 total=%d api=%d tool=%d", total, api, tool)
	}
	if other := total - api - tool; other != 60000 {
		t.Fatalf("opencode 权限等待应进入 other，实得 %dms", other)
	}
	perms := fs.perms()
	if len(perms) != 1 || !strings.Contains(perms[0].body, `"response":"once"`) {
		t.Fatalf("权限应答应恰好成功发出一次，实得 %+v", perms)
	}
}

func TestOpencodePermissionReplyFailureKeepsWaitingWindow(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	a, _ := startFakeRun(t, fs, "permission-retry", t.TempDir(), t.TempDir())
	r := a.lookup("permission-retry")
	events, done := collectEvents(r)

	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	r.seg = turn.NewSegmenter(func() time.Time { return now })
	baseTransport := r.api.httpClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	attempts := 0
	r.api.httpClient.Transport = timingRoundTripper(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("暂时无法回发权限裁决")
		}
		return baseTransport.RoundTrip(req)
	})

	a.reportTiming(r, r.seg.BeginTurn(1))
	now = now.Add(2 * time.Second)
	r.turnMu.Lock()
	a.mapToolPart(r, "bash", "call-retry", "running", json.RawMessage(`{"command":"git add && git commit"}`), "")
	r.turnMu.Unlock()
	now = now.Add(3 * time.Second)
	permission := json.RawMessage(`{
		"id":"perm-retry","sessionID":"sess-1","permission":"bash",
		"metadata":{"command":"git add && git commit"},
		"tool":{"callID":"call-retry"}
	}`)
	r.turnMu.Lock()
	a.mapPermissionAsked(r, permission)
	r.turnMu.Unlock()

	now = now.Add(60 * time.Second)
	if err := a.RespondPermission(context.Background(), r.taskID, "perm-retry", "once", ""); err == nil {
		t.Fatal("第一次权限回发失败必须向协调者报错")
	}
	now = now.Add(5 * time.Second)
	if err := a.RespondPermission(context.Background(), r.taskID, "perm-retry", "once", ""); err != nil {
		t.Fatalf("重试权限回发: %v", err)
	}
	now = now.Add(5 * time.Second)
	r.turnMu.Lock()
	a.mapToolPart(r, "bash", "call-retry", "completed", json.RawMessage(`{"command":"git add && git commit"}`), "ok")
	r.turnMu.Unlock()
	now = now.Add(2 * time.Second)
	a.reportTiming(r, r.seg.EndTurn())

	if err := a.Stop(r.taskID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-done

	var total, api, tool int64
	for ev := range events {
		if ev.Type != "usage" || ev.Timing == nil {
			continue
		}
		switch ev.Timing.Kind {
		case proto.TimingKindTurn:
			if ev.Timing.DurMS > total {
				total = ev.Timing.DurMS
			}
		case proto.TimingKindAPI:
			api += ev.Timing.DurMS
		case proto.TimingKindTool:
			tool += ev.Timing.DurMS
		}
	}
	if attempts != 2 {
		t.Fatalf("权限回发应恰好尝试两次，实得 %d", attempts)
	}
	if total != 77_000 || api != 4_000 || tool != 8_000 {
		t.Fatalf("失败重试不应提前恢复，实得 total=%d api=%d tool=%d", total, api, tool)
	}
	if other := total - api - tool; other != 65_000 {
		t.Fatalf("失败期间的等待也应进入 other，实得 %dms", other)
	}
	perms := fs.perms()
	if len(perms) != 1 {
		t.Fatalf("只有成功重试应到达 fake server，实得 %+v", perms)
	}
}

// feedPart 在 turnMu 下喂一条 message.part.updated 载荷。
func feedPart(t *testing.T, a *Adapter, r *runState, js string) {
	t.Helper()
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	a.mapPartUpdated(r, json.RawMessage(js))
}

// finishIdleForTimingTest 走真实 mapIdle 收尾路径，而不是直接替测试调用 Segmenter。
func finishIdleForTimingTest(a *Adapter, r *runState) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	a.mapIdle(r, json.RawMessage(`{"sessionID":"sess-1"}`))
}

// collectEvents 起一个 goroutine 持续排空 evCh。opencode 的 emit 是阻塞的，
// 所以测试不能只在末尾读取耗时事件，否则生产路径可能先卡在 16 条缓冲上。
func collectEvents(r *runState) (<-chan executor.AdapterEvent, <-chan struct{}) {
	events := make(chan executor.AdapterEvent, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		for ev := range r.evCh {
			events <- ev
		}
	}()
	return events, done
}

// waitForResultEvents 消费到 mapIdle 的分类结果，并保留此前所有耗时事件。
func waitForResultEvents(t *testing.T, events <-chan executor.AdapterEvent) []proto.TimingEntry {
	t.Helper()
	var timings []proto.TimingEntry
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("事件通道在 mapIdle 结果前关闭")
			}
			if ev.Type == "usage" && ev.Timing != nil {
				timings = append(timings, *ev.Timing)
			}
			if ev.Type == "result" || ev.Type == "question" {
				return timings
			}
		case <-deadline:
			t.Fatal("等待真实 mapIdle 结果超时")
		}
	}
}

func countTimingKind(es []proto.TimingEntry, kind proto.TimingKind) int {
	var n int
	for _, e := range es {
		if e.Kind == kind {
			n++
		}
	}
	return n
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
