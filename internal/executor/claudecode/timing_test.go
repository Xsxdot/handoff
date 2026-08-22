//go:build unix

package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestClaudeToolTimingPaired 钉住「工具调用的两端都喂给了段切分器」。
//
// 它不验时长算得对不对（那是 turn 包的事），只验**信号有没有喂对**：
// 一次配对的 tool_use/tool_result 必须产出 tool 条目，且帧上带 dur_ms。
func TestClaudeToolTimingPaired(t *testing.T) {
	installPersistentFakeClaude(t)
	taskDir := shortTestDir(t)
	repoPath := shortTestDir(t)
	installFakeShim(t, fakeShimReal)

	a := New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := a.Start(ctx, executor.StartReq{
		Task:        proto.Task{ID: "timing-paired", RepoPath: repoPath},
		TaskDir:     taskDir,
		PlanContent: "测试计划",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r := a.lookup("timing-paired")
	if r == nil {
		t.Fatal("Start 成功后运行态缺失")
	}
	t.Cleanup(func() { _ = a.Stop("timing-paired") })

	src, err := os.Open(filepath.Join("testdata", "turn_success.jsonl"))
	if err != nil {
		t.Fatalf("读 testdata: %v", err)
	}
	defer src.Close()

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

	timings := waitForClaudeResult(t, r)
	var tool *proto.TimingEntry
	var hasTurn bool
	var apiCount, turnCount int
	for i := range timings {
		e := timings[i]
		if e.Kind == proto.TimingKindTool {
			copy := e
			tool = &copy
		}
		if e.Kind == proto.TimingKindTurn {
			hasTurn = true
			turnCount++
		}
		if e.Kind == proto.TimingKindAPI {
			apiCount++
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
	if apiCount != 2 {
		t.Errorf("真实 Start 的 BeginTurn 与 mapResult 的 EndTurn 都应收尾模型段，实得 %d 个 api 条目", apiCount)
	}
	if turnCount < 4 {
		t.Errorf("真实 BeginTurn、工具两端与 EndTurn 都应上报 turn 条目，实得 %d 个", turnCount)
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

func TestClaudePermissionWaitNotToolTime(t *testing.T) {
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(log)
	r := &runState{
		taskID: "permission-timing",
		evCh:   make(chan executor.AdapterEvent, 32),
		stopCh: make(chan struct{}),
		seg:    turn.NewSegmenter(clock),
	}
	a.mu.Lock()
	a.runs[r.taskID] = r
	a.mu.Unlock()

	testRoot := t.TempDir()
	shortRoot := filepath.Dir(filepath.Dir(testRoot))
	sock := filepath.Join(shortRoot, "b171-perm.sock")
	t.Cleanup(func() { _ = os.Remove(sock) })
	srv, err := newPermServer(sock, log, func(ask permAsk) { a.onPermissionAsk(r, ask) })
	if err != nil {
		t.Fatalf("newPermServer: %v", err)
	}
	r.perm = srv
	t.Cleanup(func() { _ = srv.Close() })

	a.reportTiming(r, r.seg.BeginTurn(1))
	now = now.Add(2 * time.Second)
	a.reportTiming(r, r.seg.ToolStart("toolu-wait-1", "Bash", "go test ./..."))
	now = now.Add(3 * time.Second)
	conn := dialAsk(t, sock, "toolu-wait-1", "Bash", `{"command":"git add && git commit"}`)
	defer conn.Close()

	var timings []proto.TimingEntry
	var sawPermission bool
	deadline := time.After(2 * time.Second)
	for !sawPermission {
		select {
		case ev := <-r.evCh:
			if ev.Type == "usage" && ev.Timing != nil {
				timings = append(timings, *ev.Timing)
			}
			if ev.Type == "permission" {
				sawPermission = true
			}
		case <-deadline:
			t.Fatal("等待权限事件超时")
		}
	}
	now = now.Add(60 * time.Second)
	if err := a.RespondPermission(context.Background(), r.taskID, "toolu-wait-1", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	var response permDecision
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&response); err != nil {
		t.Fatalf("读取权限应答: %v", err)
	}
	if response.Behavior != "allow" {
		t.Fatalf("权限应答 behavior=%q，期望 allow", response.Behavior)
	}

	select {
	case ev := <-r.evCh:
		if ev.Type != "usage" || ev.Timing == nil || ev.Timing.Kind != proto.TimingKindTurn || ev.Timing.DurMS != 65000 {
			t.Fatalf("权限应答后应收到 65s Resume 标记，实得 %+v", ev)
		}
		timings = append(timings, *ev.Timing)
	case <-time.After(2 * time.Second):
		t.Fatal("等待权限恢复耗时条目超时")
	}

	now = now.Add(5 * time.Second)
	_, entries := r.seg.ToolEnd("toolu-wait-1")
	a.reportTiming(r, entries)
	now = now.Add(2 * time.Second)
	a.reportTiming(r, r.seg.EndTurn())
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
		t.Fatalf("权限等待不应进入工具段，实得 total=%d api=%d tool=%d", total, api, tool)
	}
	if other := total - api - tool; other != 60000 {
		t.Fatalf("权限等待应进入 other，实得 %dms", other)
	}
}

// installPersistentFakeClaude 让真实 Start 完成 init 后保持存活，避免测试回放夹具
// 时被假执行者的退出哨兵抢先终结。Stop 会沿真实 prochost 路径回收它。
func installPersistentFakeClaude(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  if [ -n "$line" ]; then
    printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-fake"}'
    sleep 1000
    exit 0
  fi
done
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// waitForClaudeResult 消费真实 mapResult 产出的终局事件，并保留它之前的耗时条目。
// 若 BeginTurn 或 EndTurn 漏喂，下面的条目断言会分别捕获静默缺口。
func waitForClaudeResult(t *testing.T, r *runState) []proto.TimingEntry {
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
			t.Fatal("等待真实 mapResult 结果超时")
		}
	}
}
