// timing_test.go 钉住 grok 工具信号是否正确喂入段切分器与结构化帧。
//
// 本文件不验时长算法；那是 internal/executor/turn 的职责。
package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

// TestGrokToolTimingPaired 钉住「工具调用的两端都喂给了段切分器」。
//
// 它不验时长算得对不对（那是 turn 包的事），只验**信号有没有喂对**：
// 一次配对的 tool_call / tool_call_update(completed) 必须产出 tool 条目，
// 且 tool_result 帧上带 dur_ms。
func TestGrokToolTimingPaired(t *testing.T) {
	updates := []string{
		`{"update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"run_terminal_command","rawInput":{"command":"echo hi"}}}`,
		`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","title":"Execute ` + "`echo hi`" + `"}}`,
	}
	a, r := newACPTestRun(t, "timing-paired", updates)
	if err := a.Send(context.Background(), r.taskID, "继续"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	timings := waitForTurnResult(t, r)
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
	if got := countKind(timings, proto.TimingKindAPI); got != 2 {
		t.Errorf("工具前后的两个模型段都应收尾，实得 %d 个 api 条目", got)
	}
	if got := countKind(timings, proto.TimingKindTurn); got < 4 {
		t.Errorf("真实 BeginTurn、工具两端与 EndTurn 都应上报 turn 条目，实得 %d 个", got)
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
	updates := []string{
		`{"update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"x","rawInput":{}}}`,
		`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"in_progress"}}`,
	}
	a, r := newACPTestRun(t, "timing-unknown-status", updates)
	if err := a.Send(context.Background(), r.taskID, "继续"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	timings := waitForTurnResult(t, r)
	if got := countKind(timings, proto.TimingKindTurn); got < 3 {
		t.Errorf("真实 BeginTurn、工具开始与 EndTurn 都应上报 turn 条目，实得 %d 个", got)
	}

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

// newACPTestRun 通过真实 Send 路径造一个带 ACP 连接的运行态。
// 测试不直接调用 Segmenter 的回合边界，BeginTurn/EndTurn 必须由 adapter 自己喂入。
func newACPTestRun(t *testing.T, id string, updates []string) (*Adapter, *runState) {
	t.Helper()
	taskDir := t.TempDir()
	a := New(nil)
	r := &runState{
		taskID: id, taskDir: taskDir, repoPath: taskDir,
		evCh: make(chan executor.AdapterEvent, 256),
		acc:  newTurnAccumulator(), pending: map[string]pendingPerm{},
	}
	fw, err := turn.WriterFor(taskDir, a.log)
	if err != nil {
		t.Fatalf("WriterFor: %v", err)
	}
	r.frames, r.seg = fw, turn.NewSegmenter(nil)

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
			if json.Unmarshal(data, &in) != nil || in.Method != "session/prompt" {
				continue
			}
			for i, update := range updates {
				if i > 0 {
					// 留出可观测的毫秒间隔，避免真实耗时被 Duration.Milliseconds 截成 0。
					time.Sleep(2 * time.Millisecond)
				}
				msg := `{"jsonrpc":"2.0","method":"session/update","params":` + update + `}`
				if err := conn.Write(req.Context(), websocket.MessageText, []byte(msg)); err != nil {
					return
				}
			}
			response := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}`, in.ID)
			if err := conn.Write(req.Context(), websocket.MessageText, []byte(response)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	cli, err := DialACP(context.Background(), "ws"+srv.URL[len("http"):], &acpHandler{a: a, r: r}, a.log)
	if err != nil {
		t.Fatalf("DialACP: %v", err)
	}
	r.cli = cli
	a.mu.Lock()
	a.runs[id] = r
	a.mu.Unlock()
	t.Cleanup(func() { _ = a.Stop(id) })
	return a, r
}

// waitForTurnResult 消费真实回合的事件直到 finishTurn 分类事件到达。
// 这样 EndTurn 缺失时不会被初始 turn 条目掩盖。
func waitForTurnResult(t *testing.T, r *runState) []proto.TimingEntry {
	t.Helper()
	var timings []proto.TimingEntry
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "usage" && ev.Timing != nil {
				timings = append(timings, *ev.Timing)
			}
			if ev.Type == "result" || ev.Type == "question" {
				return timings
			}
		case <-deadline:
			t.Fatal("等待真实 finishTurn 结果超时")
		}
	}
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

func countKind(es []proto.TimingEntry, k proto.TimingKind) int {
	var n int
	for _, e := range es {
		if e.Kind == k {
			n++
		}
	}
	return n
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
