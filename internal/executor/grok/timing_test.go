// timing_test.go 钉住 grok 工具信号是否正确喂入段切分器与结构化帧。
//
// 本文件不验时长算法；那是 internal/executor/turn 的职责。
package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestGrokToolTimingPaired 钉住「工具调用的两端都喂给了段切分器」。
//
// 它不验时长算得对不对（那是 turn 包的事），只验**信号有没有喂对**：
// 一次配对的 tool_call / tool_call_update(completed) 必须产出 tool 条目，
// 且 tool_result 帧上带 dur_ms。
func TestGrokToolTimingPaired(t *testing.T) {
	a := New(nil)
	r := newTestRun(t, a, "timing-paired")

	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	h := &acpHandler{a: a, r: r}
	h.onSessionUpdate([]byte(`{"update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"run_terminal_command","rawInput":{"command":"echo hi"}}}`))
	// 留出可观测的毫秒间隔，避免真实耗时被 Duration.Milliseconds 截成 0
	time.Sleep(2 * time.Millisecond)
	h.onSessionUpdate([]byte(`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","title":"Execute ` + "`echo hi`" + `"}}`))
	a.reportTiming(r, r.seg.EndTurn())

	timings := drainTimings(r)
	var tool *proto.TimingEntry
	for i := range timings {
		if timings[i].Kind == proto.TimingKindTool {
			tool = &timings[i]
		}
	}
	if tool == nil {
		t.Fatal("配对的工具调用必须产出 tool 条目")
	}
	if tool.Label != "run_terminal_command" {
		t.Errorf("Label 应取 tool_call 的 title（不是 update 的人读句子），实得 %q", tool.Label)
	}
	if tool.DurMS <= 0 {
		t.Errorf("配对成功时耗时应为正，实得 %d", tool.DurMS)
	}
	if !hasKind(timings, proto.TimingKindAPI) {
		t.Error("工具开始时必须收掉当前模型段，缺 api 条目")
	}

	frames := readFrames(t, r)
	var call, result *proto.Frame
	for i := range frames {
		switch frames[i].Type {
		case proto.FrameToolCall:
			call = &frames[i]
		case proto.FrameToolResult:
			result = &frames[i]
		}
	}
	if call == nil || result == nil {
		t.Fatalf("工具两端都要产帧，实得 call=%v result=%v", call, result)
	}
	if call.Tool != "run_terminal_command" {
		t.Errorf("tool_call 帧的工具名应取 title，实得 %q", call.Tool)
	}
	// 帧里存 rawInput 全文，不是 toolLine 的 200 字摘要（这正是 W4a 当初的反对理由）
	if !strings.Contains(call.Input, `"echo hi"`) {
		t.Errorf("tool_call 帧应含 rawInput 原文，实得 %q", call.Input)
	}
	if result.Status != "ok" {
		t.Errorf("completed 应映射为 ok，实得 %q", result.Status)
	}
	if result.DurMS <= 0 {
		t.Errorf("配对成功时帧上应带正的 dur_ms，实得 %d", result.DurMS)
	}
	if call.DurMS != 0 {
		t.Errorf("tool_call 帧不带耗时（那时还不知道），实得 %d", call.DurMS)
	}
}

// TestGrokUnknownToolStatusIsNotTerminal 钉住「不认识的状态不算终态」。
//
// grok 的 status 真实取值集合尚未真机确认（testdata/updates.jsonl 是手写夹具），
// 所以这里锁的是**保守方向**：不认识就不产 tool_result 帧、不收工具段，
// 回合照跑。开着的工具由 EndTurn 丢弃、由聚合层的 Partial 标出。
func TestGrokUnknownToolStatusIsNotTerminal(t *testing.T) {
	a := New(nil)
	r := newTestRun(t, a, "timing-unknown-status")
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	h := &acpHandler{a: a, r: r}
	h.onSessionUpdate([]byte(`{"update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"x","rawInput":{}}}`))
	h.onSessionUpdate([]byte(`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"in_progress"}}`))

	for _, f := range readFrames(t, r) {
		if f.Type == proto.FrameToolResult {
			t.Fatal("非终态不得产 tool_result 帧")
		}
	}
	for _, e := range drainTimings(r) {
		if e.Kind == proto.TimingKindTool {
			t.Fatal("非终态不得收工具段")
		}
	}
}

// newTestRun 造一个只带 frames/seg 的最小运行态：本文件的测试不碰 ACP 连接。
func newTestRun(t *testing.T, a *Adapter, id string) *runState {
	t.Helper()
	taskDir := t.TempDir()
	fw, err := turn.WriterFor(taskDir, a.log)
	if err != nil {
		t.Fatalf("WriterFor: %v", err)
	}
	r := &runState{
		taskID: id, taskDir: taskDir,
		evCh: make(chan executor.AdapterEvent, 256),
		acc:  newTurnAccumulator(), pending: map[string]pendingPerm{},
		frames: fw, seg: turn.NewSegmenter(nil),
	}
	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	r.textPart = r.frames.NextPart()
	return r
}

// drainTimings 取走通道里全部耗时条目（非阻塞排空）。
func drainTimings(r *runState) []proto.TimingEntry {
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

func hasKind(es []proto.TimingEntry, k proto.TimingKind) bool {
	for _, e := range es {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// readFrames 读回本任务已落盘的帧。
//
// 路径常量 turn.FramesFileName 是导出的，别自己拼文件名。
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
