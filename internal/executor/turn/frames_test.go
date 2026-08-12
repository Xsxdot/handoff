package turn

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// readFrames 读出 taskDir 下 frames.jsonl 的全部帧。
func readFrames(t *testing.T, taskDir string) []proto.Frame {
	t.Helper()
	f, err := os.Open(filepath.Join(taskDir, FramesFileName))
	if err != nil {
		t.Fatalf("打开 frames.jsonl: %v", err)
	}
	defer f.Close()
	var out []proto.Frame
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var fr proto.Frame
		if err := json.Unmarshal(sc.Bytes(), &fr); err != nil {
			t.Fatalf("解析帧 %q: %v", sc.Text(), err)
		}
		out = append(out, fr)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("扫描 frames.jsonl: %v", err)
	}
	return out
}

func TestFrameWriterWritesEachType(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFrameWriter(dir, nil)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if err := w.BeginTurn("dispatch"); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	if err := w.Reasoning("p01", "先看看测试"); err != nil {
		t.Fatalf("Reasoning: %v", err)
	}
	if err := w.Text("p02", "我来实现"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if err := w.ToolCall("p03", "Bash", "go test ./..."); err != nil {
		t.Fatalf("ToolCall: %v", err)
	}
	if err := w.ToolResult("p03", "ok", "PASS"); err != nil {
		t.Fatalf("ToolResult: %v", err)
	}
	if err := w.EventRef(88, "permission_request"); err != nil {
		t.Fatalf("EventRef: %v", err)
	}

	frames := readFrames(t, dir)
	if len(frames) != 6 {
		t.Fatalf("应有 6 帧，实得 %d", len(frames))
	}
	wantTypes := []proto.FrameType{
		proto.FrameTurnStart, proto.FrameReasoning, proto.FrameText,
		proto.FrameToolCall, proto.FrameToolResult, proto.FrameEvent,
	}
	for i, want := range wantTypes {
		if frames[i].Type != want {
			t.Errorf("第 %d 帧类型应为 %s，实得 %s", i, want, frames[i].Type)
		}
		if frames[i].Seq != int64(i+1) {
			t.Errorf("第 %d 帧 seq 应为 %d，实得 %d", i, i+1, frames[i].Seq)
		}
		if frames[i].Turn != 1 {
			t.Errorf("第 %d 帧 turn 应为 1，实得 %d", i, frames[i].Turn)
		}
	}
	if frames[0].Reason != "dispatch" {
		t.Errorf("turn_start 的 reason 应为 dispatch，实得 %q", frames[0].Reason)
	}
	if frames[5].RefSeq != 88 || frames[5].Event != "permission_request" {
		t.Errorf("event 帧应带 ref_seq=88 与类型名，实得 %+v", frames[5])
	}
}

func TestFrameWriterTurnIncrements(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	_ = w.BeginTurn("dispatch")
	_ = w.Text("p01", "第一轮")
	_ = w.BeginTurn("send")
	_ = w.Text("p01", "第二轮")

	frames := readFrames(t, dir)
	if frames[1].Turn != 1 {
		t.Errorf("第一轮的 text 应在 turn 1，实得 %d", frames[1].Turn)
	}
	if frames[3].Turn != 2 {
		t.Errorf("第二轮的 text 应在 turn 2，实得 %d", frames[3].Turn)
	}
	if frames[2].Reason != "send" {
		t.Errorf("第二个 turn_start 的 reason 应为 send，实得 %q", frames[2].Reason)
	}
}

// agentd 重启后 adapter 会重建 FrameWriter：必须接着上次的 seq/turn 写，
// 从 1 重来会让前端把新帧插到时间线开头。
func TestFrameWriterResumesSeqAndTurn(t *testing.T) {
	dir := t.TempDir()
	w1, _ := NewFrameWriter(dir, nil)
	_ = w1.BeginTurn("dispatch")
	_ = w1.Text("p01", "重启前")
	_ = w1.BeginTurn("send")

	w2, err := NewFrameWriter(dir, nil)
	if err != nil {
		t.Fatalf("重建 FrameWriter: %v", err)
	}
	if err := w2.Text("p01", "重启后"); err != nil {
		t.Fatalf("Text: %v", err)
	}

	frames := readFrames(t, dir)
	last := frames[len(frames)-1]
	if last.Seq != 4 {
		t.Errorf("重建后应接着写 seq 4，实得 %d", last.Seq)
	}
	if last.Turn != 2 {
		t.Errorf("重建后应沿用 turn 2，实得 %d", last.Turn)
	}
}

func TestFrameWriterTruncatesToolFields(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	_ = w.BeginTurn("dispatch")
	big := strings.Repeat("x", FrameFieldHead+FrameFieldTail+1000)
	_ = w.ToolResult("p01", "ok", big)

	frames := readFrames(t, dir)
	fr := frames[len(frames)-1]
	if !fr.Truncated {
		t.Error("超长输出应被标记为已截断")
	}
	if fr.Bytes != int64(len(big)) {
		t.Errorf("bytes 应为原始长度 %d，实得 %d", len(big), fr.Bytes)
	}
	if len(fr.Output) >= len(big) {
		t.Errorf("截断后不该还是原长：%d", len(fr.Output))
	}
}

// SSE / stream-json 的处理可能跑在多个 goroutine 上：seq 必须与写入顺序
// 严格一致，否则按 offset 续读会错位。
func TestFrameWriterConcurrentWritesKeepSeqDense(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Text("p01", "x")
		}()
	}
	wg.Wait()

	frames := readFrames(t, dir)
	if len(frames) != n {
		t.Fatalf("应有 %d 帧（行未交错），实得 %d", n, len(frames))
	}
	seen := map[int64]bool{}
	for _, fr := range frames {
		if seen[fr.Seq] {
			t.Fatalf("seq %d 重复", fr.Seq)
		}
		seen[fr.Seq] = true
	}
	for i := int64(1); i <= n; i++ {
		if !seen[i] {
			t.Fatalf("seq %d 缺失（应当连续无洞）", i)
		}
	}
}

// nil 接收者安全：构造失败时 adapter 直接持有 nil，调用点不必到处判空。
func TestFrameWriterNilReceiverIsNoop(t *testing.T) {
	var w *FrameWriter
	if err := w.BeginTurn("dispatch"); err != nil {
		t.Errorf("nil.BeginTurn 应返回 nil，实得 %v", err)
	}
	if err := w.Text("p01", "x"); err != nil {
		t.Errorf("nil.Text 应返回 nil，实得 %v", err)
	}
	if p := w.NextPart(); p != "" {
		t.Errorf("nil.NextPart 应返回空串，实得 %q", p)
	}
}

func TestFrameWriterNextPartIsUniqueWithinTurn(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	_ = w.BeginTurn("dispatch")
	a, b := w.NextPart(), w.NextPart()
	if a == b {
		t.Fatalf("同回合内 NextPart 应互不相同，两次都是 %q", a)
	}
}
