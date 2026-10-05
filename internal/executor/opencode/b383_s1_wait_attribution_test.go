// b383_s1_wait_attribution_test.go —— B383 S1（双 WARN 消除与等待归属）红→绿回放。
//
// 缝 2：全部事件走真实 SSE 入口（fakeServer push → /event → streamOnce →
// mapEvent），不绕过 acceptForeign / mapPermissionAsked 的任何一层。
//
// 三条合成序（形态依据：spec §S1 与卡事件 16677 的评审 harness 实测）：
//   - H1（子会话）：子会话工具 part + 子会话 permission.asked + 应答。acceptForeign
//     只放行子会话的审批请求、丢弃其工具 part，子会话工具段永不开——修前请求侧
//     PauseWaiting 落空与应答侧 Resume 落空成对 WARN；修后零 WARN、恰一条「不承载」
//     Info、计时面无被吞等待（无任何 tool 条目）。
//   - H2 序 B（权限早于 part）：permission.asked 先于任何工具 part 到达，应答后
//     part 才来。修前等待整段被吞进 api 桶且成对 WARN；修后等待进 other 桶。
//   - H2 序 C（等待中途 part 才到）：permission.asked 先到，带非空入参的 running
//     part 在应答前到达。修前 1+1 条 WARN 且落空后的等待被吞进 tool 桶；修后零
//     WARN、等待进 other 桶。
//
// 断言纪律：等待/桶断言用计时事件（TimingEntry 的桶归属），不用 sleep 同步——
// H2 用互斥保护的可注入假钟，事件处理与推进时钟之间以收集器的类型信号为栅栏；
// H1 的「无被吞等待」在真实时钟下断言（无 tool 条目与时钟无关）。
package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
)

// permissionAskedEventOn 构造一条 permission.asked，tool.callID 可指定。
// permissionAskedEventFrom 固定 call-1；H2 夹具的权限与工具 part 必须以独立
// callID 配对（PauseWaiting/Resume 靠它配对）。
func permissionAskedEventOn(sessionID, id, perm, command, callID string) string {
	return sseLine(map[string]any{
		"type":      "permission.asked",
		"sessionID": sessionID,
		"properties": map[string]any{
			"id": id, "sessionID": sessionID, "permission": perm,
			"patterns": []string{command},
			"metadata": map[string]any{"command": command},
			"tool":     map[string]any{"messageID": "msg-1", "callID": callID},
		},
	})
}

// toolPartEvent 构造一条 message.part.updated（tool 类型，state 三元组可指定）。
func toolPartEvent(sessionID, msgID, partID, callID, tool, status, input, output string) string {
	return sseLine(map[string]any{
		"type":      "message.part.updated",
		"sessionID": sessionID,
		"properties": map[string]any{
			"sessionID": sessionID,
			"part": map[string]any{
				"type": "tool", "tool": tool, "callID": callID,
				"messageID": msgID, "sessionID": sessionID, "id": partID,
				"state": map[string]any{"status": status, "input": json.RawMessage(input), "output": output},
			},
		},
	})
}

// fakeClock 是互斥保护的可推进时钟：advance 与 Now 都在锁内完成，订阅 goroutine
// （处理事件取时间）与测试 goroutine（推进时钟）并发安全，-race 下无数据竞争。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// s1EventSink 收集全部 AdapterEvent：计时条目必须**保留**——既有的 waitEventType
// 会把途经的 usage 事件丢掉，而本文件的桶断言恰恰需要那部分条目（迟到段关掉的
// api 条目在 permission 事件之前上报，工具条目在 result 事件之前上报）。同时镜像
// permission/result 两类事件作栅栏信号：mapPermissionAsked 在占段/挂起之后才发
// permission 事件、mapIdle 在上报收尾计时条目之后才发 result，收到信号即代表
// 此前 push 的事件已全部处理完毕（SSE 单 goroutine 顺序处理）。
type s1EventSink struct {
	mu       sync.Mutex
	timings  []proto.TimingEntry
	perm     *executor.AdapterEvent
	lastRes  *executor.AdapterEvent
	permCh   chan struct{}
	resultCh chan struct{}
	done     chan struct{} // 收集器排干事件通道后关闭（Stop → closeEvents → range 退出）
}

func startS1Sink(events <-chan executor.AdapterEvent) *s1EventSink {
	s := &s1EventSink{permCh: make(chan struct{}, 16), resultCh: make(chan struct{}, 16), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for ev := range events {
			switch {
			case ev.Type == "usage" && ev.Timing != nil:
				s.mu.Lock()
				s.timings = append(s.timings, *ev.Timing)
				s.mu.Unlock()
			case ev.Type == "permission":
				s.mu.Lock()
				perm := ev
				s.perm = &perm
				s.mu.Unlock()
				s.permCh <- struct{}{}
			case ev.Type == "result":
				s.mu.Lock()
				res := ev
				s.lastRes = &res
				s.mu.Unlock()
				s.resultCh <- struct{}{}
			}
		}
	}()
	return s
}

func (s *s1EventSink) waitPermission(t *testing.T) {
	t.Helper()
	select {
	case <-s.permCh:
	case <-time.After(5 * time.Second):
		t.Fatal("等待 permission 事件超时")
	}
}

func (s *s1EventSink) waitResult(t *testing.T) {
	t.Helper()
	select {
	case <-s.resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("等待 result 事件超时")
	}
}

func (s *s1EventSink) lastResult(t *testing.T) executor.AdapterEvent {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastRes == nil {
		t.Fatal("未收到 result 事件")
	}
	return *s.lastRes
}

func (s *s1EventSink) lastPermission(t *testing.T) executor.AdapterEvent {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perm == nil {
		t.Fatal("未收到 permission 事件")
	}
	return *s.perm
}

// snapshotTimings 返回已收集计时条目的快照。
func (s *s1EventSink) snapshotTimings() []proto.TimingEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]proto.TimingEntry(nil), s.timings...)
}

// assertZeroWaitMissWarn 断言两条「等待窗口落空」WARN 都未出现（S1 的零成对
// WARN 验收线）。两条消息分别来自请求侧 PauseWaiting 落空与应答侧 Resume 落空。
func assertZeroWaitMissWarn(t *testing.T, buf *bufWriter) {
	t.Helper()
	log := buf.String()
	for _, msg := range []string{
		"未找到对应工具等待窗口", // 请求侧
		"未找到工具等待窗口",   // 应答侧
	} {
		if strings.Contains(log, msg) {
			t.Fatalf("不应出现等待窗口落空 WARN（%q）\n日志：\n%s", msg, log)
		}
	}
}

// sumTimingBuckets 汇总计时条目的桶归属：api/tool 各自求和，turn 取最大（同键
// 覆盖，最后一条是终值），other = turn - api - tool（聚合层同款差额口径）。
// 返回值里的 toolEntries 供归属断言（Label/Detail）使用。
func sumTimingBuckets(es []proto.TimingEntry) (api, tool, otherMS int64, toolEntries []proto.TimingEntry) {
	var total int64
	for _, e := range es {
		switch e.Kind {
		case proto.TimingKindAPI:
			api += e.DurMS
		case proto.TimingKindTool:
			tool += e.DurMS
			toolEntries = append(toolEntries, e)
		case proto.TimingKindTurn:
			if e.DurMS > total {
				total = e.DurMS
			}
		}
	}
	otherMS = total - api - tool
	return api, tool, otherMS, toolEntries
}

// takeToolTimings 停止任务并等收集器排干事件通道后，取全部计时条目。
// 等待靠 done 通道（Stop → closeEvents → 收集器 range 退出），不 sleep。
func takeToolTimings(t *testing.T, ad *Adapter, taskID string, sink *s1EventSink) []proto.TimingEntry {
	t.Helper()
	if err := ad.Stop(taskID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-sink.done:
	case <-time.After(5 * time.Second):
		t.Fatal("等待事件通道排干超时")
	}
	return sink.snapshotTimings()
}

// waitToolFrame 轮询 frames.jsonl 直到出现指定 part 的指定类型帧（条件等待，
// 模式同 waitPermCalls）：工具帧在 mapToolPart 里**同步**写盘、且在计时打点之后
// ——帧出现即代表该 part 事件已处理完毕，假钟随之可以安全推进。这是本文件的
// 每事件栅栏：SSE 事件的处理是异步的，逐事件推进假钟前必须等上一条处理完，
// 否则处理时刻会落到最后一次 advance 之后，桶归属不再确定。
func waitToolFrame(t *testing.T, r *runState, frameType proto.FrameType, part string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		var found bool
		raw, err := os.ReadFile(filepath.Join(r.taskDir, turn.FramesFileName))
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if line == "" {
					continue
				}
				var f proto.Frame
				if json.Unmarshal([]byte(line), &f) != nil {
					continue
				}
				if f.Type == frameType && f.Part == part {
					found = true
					break
				}
			}
		}
		if found {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("等待 %s 帧（part=%s）超时", frameType, part)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// TestChildPermissionWaitExplicitlyUncarried（B383 S1 H1）：子会话权限的等待
// 「显式不承载」——修前成对 WARN（请求侧 PauseWaiting 落空 + 应答侧 Resume 落空，
// 因 acceptForeign 丢弃子会话工具 part，子会话工具段永不开）；修后零 WARN、恰一条
// 「不承载」Info，且等待不得伪装成父回合的工具时间（计时面无 tool 条目）。
func TestChildPermissionWaitExplicitlyUncarried(t *testing.T) {
	buf := captureLog(t)
	taskID := "task-b383-h1"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "子任务")
	// 子会话的工具 part：acceptForeign 会丢弃它（回合记账只认单一会话），段切分器
	// 里不会有这个 callID 的段——这正是 H1 双 WARN 的机制前提
	fs.push(toolPartEvent("sess-child", "msg-c1", "prt-c1", "call-c1", "bash",
		"running", `{"command":"curl https://example.com"}`, ""))
	fs.push(permissionAskedEventOn("sess-child", "perm-h1", "bash", "curl https://example.com", "call-c1"))

	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	sink := startS1Sink(ch)
	sink.waitPermission(t)
	if ev := sink.lastPermission(t); ev.PermissionID != "perm-h1" {
		t.Fatalf("PermissionID=%q，期望 perm-h1", ev.PermissionID)
	}
	if err := ad.RespondPermission(context.Background(), taskID, "perm-h1", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	// 回合收尾屏障：push idle 并等 B21 零文本 result，此后计时条目必已齐
	fs.push(statusIdleEvent())
	sink.waitResult(t)
	timings := takeToolTimings(t, ad, taskID, sink)

	assertZeroWaitMissWarn(t, buf)
	// 恰一条「不承载」说明（spec §S1：每次等待只留一条说明不承载的 Info）
	if n := strings.Count(buf.String(), "子 agent 权限等待不承载计时"); n != 1 {
		t.Fatalf("子 agent 等待应恰一条「不承载」Info，实得 %d 条\n日志：\n%s",
			n, buf.String())
	}
	// 计时面无被吞等待：子会话等待不得记进父回合的任何工具段
	_, _, _, toolEntries := sumTimingBuckets(timings)
	if len(toolEntries) != 0 {
		t.Fatalf("子会话权限等待不得产出任何 tool 计时条目，实得 %+v", toolEntries)
	}
	// 应答仍必须路由回子会话（B52 语义未被本次改动波及）
	calls := fs.perms()
	if len(calls) != 1 || calls[0].path != "/session/sess-child/permissions/perm-h1" {
		t.Fatalf("权限应答应恰好发往子会话一次，实得 %+v", calls)
	}
}

// TestParentPermissionBeforeToolPartCarriesWaitInOther（B383 S1 H2 序 B）：
// permission.asked 先于任何工具 part 到达（等待期间无 part 事件，应答后 part 才来）。
// 修前：成对 WARN + 等待整段被吞进 api 桶（工具段在应答后才开）。
// 修后：零 WARN，等待进 other 桶（按时钟：api=2s，tool=1s+5s=6s，other=60s）。
func TestParentPermissionBeforeToolPartCarriesWaitInOther(t *testing.T) {
	buf := captureLog(t)
	fs := newFakeServer(t)
	taskID := "task-b383-h2b"
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	sink := startS1Sink(ch)
	r := ad.lookup(taskID)
	// 假钟接管段切分器：换表发生在任何 push 之前——订阅 goroutine 首次触碰 seg 在
	// 收到第一条 push 之后，channel 收发建立 happens-before，-race 下无竞争
	clock := newFakeClock(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC))
	r.seg = turn.NewSegmenter(clock.Now)
	ad.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))

	clock.advance(2 * time.Second)
	fs.push(permissionAskedEventOn("sess-1", "perm-h2b", "bash", "echo perm-b", "call-b"))
	sink.waitPermission(t) // 栅栏：迟到段已占、等待已挂

	clock.advance(60 * time.Second) // 审批等待窗口
	if err := ad.RespondPermission(context.Background(), taskID, "perm-h2b", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	clock.advance(1 * time.Second)
	fs.push(toolPartEvent("sess-1", "msg-b", "prt-b", "call-b", "bash",
		"running", `{"command":"echo perm-b"}`, ""))
	waitToolFrame(t, r, proto.FrameToolCall, "call-b") // 栅栏：本事件处理完（假钟 63s 已钉住）
	clock.advance(5 * time.Second)
	fs.push(toolPartEvent("sess-1", "msg-b", "prt-b", "call-b", "bash",
		"completed", `{"command":"echo perm-b"}`, "ok"))
	waitToolFrame(t, r, proto.FrameToolResult, "call-b") // 栅栏：ToolEnd 已按假钟 68s 结算
	// 回合收尾屏障：push idle 并等 B21 零文本 result，此后计时条目必已齐
	fs.push(statusIdleEvent())
	sink.waitResult(t)
	timings := takeToolTimings(t, ad, taskID, sink)

	assertZeroWaitMissWarn(t, buf)
	api, tool, otherMS, toolEntries := sumTimingBuckets(timings)
	if len(toolEntries) != 1 {
		t.Fatalf("一次工具调用应恰一条 tool 条目，实得 %d 条（全部条目 %+v）", len(toolEntries), timings)
	}
	if toolEntries[0].Label != "bash" || toolEntries[0].Detail != "echo perm-b" {
		t.Fatalf("tool 条目应带真实工具名与命令，实得 %+v", toolEntries[0])
	}
	if api != 2_000 || tool != 6_000 {
		t.Fatalf("api 桶应只含权限前的 2s、tool 桶应只含执行 6s，实得 api=%dms tool=%dms", api, tool)
	}
	if otherMS != 60_000 {
		t.Fatalf("60s 审批等待应进 other 桶，实得 %dms", otherMS)
	}
	// tool_call 帧仍恰一条且带真实入参：迟到段只承载计时，帧语义不受影响
	var calls []proto.Frame
	for _, f := range readFrames(t, r) {
		if f.Type == proto.FrameToolCall {
			calls = append(calls, f)
		}
	}
	if len(calls) != 1 || !strings.Contains(calls[0].Input, "echo perm-b") {
		t.Fatalf("tool_call 帧应恰一条并含真实命令，实得 %+v", calls)
	}
}

// TestParentPermissionWaitWithLateRunningPartCarriesWaitInOther（B383 S1 H2 序 C）：
// permission.asked 先到，带非空入参的 running part 在等待中途到达（卡事件 16677
// 的「1+1 条 WARN 吞进 tool」形态）。修前：请求侧 PauseWaiting 落空 WARN + 应答侧
// Resume 落空 WARN，且等待被吞进 tool 桶（running part 开段后等待在段内流逝）。
// 修后：零 WARN，等待进 other 桶（按时钟：api=2s，tool=5s，other=61s）。
func TestParentPermissionWaitWithLateRunningPartCarriesWaitInOther(t *testing.T) {
	buf := captureLog(t)
	fs := newFakeServer(t)
	taskID := "task-b383-h2c"
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	sink := startS1Sink(ch)
	r := ad.lookup(taskID)
	clock := newFakeClock(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC))
	r.seg = turn.NewSegmenter(clock.Now)
	ad.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))

	clock.advance(2 * time.Second)
	fs.push(permissionAskedEventOn("sess-1", "perm-h2c", "bash", "echo perm-c", "call-c"))
	sink.waitPermission(t) // 栅栏：等待窗口处理完毕

	// 等待中途 part 才到（running、非空入参）：以 tool_call 帧为栅栏钉住处理时刻，
	// 工具段的真实起点固定在迟到段（同键 ToolStart 去重），桶归属确定
	clock.advance(1 * time.Second)
	fs.push(toolPartEvent("sess-1", "msg-c", "prt-c", "call-c", "bash",
		"running", `{"command":"echo perm-c"}`, ""))
	waitToolFrame(t, r, proto.FrameToolCall, "call-c")

	clock.advance(60 * time.Second) // 审批等待剩余窗口
	if err := ad.RespondPermission(context.Background(), taskID, "perm-h2c", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	clock.advance(5 * time.Second)
	fs.push(toolPartEvent("sess-1", "msg-c", "prt-c", "call-c", "bash",
		"completed", `{"command":"echo perm-c"}`, "ok"))
	waitToolFrame(t, r, proto.FrameToolResult, "call-c")
	fs.push(statusIdleEvent())
	sink.waitResult(t)
	timings := takeToolTimings(t, ad, taskID, sink)

	assertZeroWaitMissWarn(t, buf)
	api, tool, otherMS, toolEntries := sumTimingBuckets(timings)
	if len(toolEntries) != 1 {
		t.Fatalf("一次工具调用应恰一条 tool 条目，实得 %d 条（全部条目 %+v）", len(toolEntries), timings)
	}
	if toolEntries[0].Label != "bash" || toolEntries[0].Detail != "echo perm-c" {
		t.Fatalf("tool 条目应带真实工具名与命令，实得 %+v", toolEntries[0])
	}
	if api != 2_000 || tool != 5_000 {
		t.Fatalf("api 桶应只含权限前的 2s、tool 桶应只含执行 5s，实得 api=%dms tool=%dms", api, tool)
	}
	if otherMS != 61_000 {
		t.Fatalf("61s 审批等待应进 other 桶，实得 %dms", otherMS)
	}
}

// TestLateOpenedSegmentClosesOnDeniedTurn（B383 S1 H2 收口路径）：迟到段开着时
// 权限被拒——opencode 收到 reject 直接终结回合，只留 error 状态的 tool part
// （Wave 0 实测形态）。迟到段必须能被这条权限终局正确闭合：零 WARN，等待进
// other，工具段以 error part 收段（dur=执行口径，不含等待）。
func TestLateOpenedSegmentClosesOnDeniedTurn(t *testing.T) {
	buf := captureLog(t)
	fs := newFakeServer(t)
	taskID := "task-b383-h2-deny"
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	sink := startS1Sink(ch)
	r := ad.lookup(taskID)
	clock := newFakeClock(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC))
	r.seg = turn.NewSegmenter(clock.Now)
	ad.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))

	clock.advance(2 * time.Second)
	fs.push(permissionAskedEventOn("sess-1", "perm-deny", "bash", "rm -rf /", "call-deny"))
	sink.waitPermission(t) // 栅栏：迟到段已占、等待已挂

	clock.advance(60 * time.Second)
	if err := ad.RespondPermission(context.Background(), taskID, "perm-deny", "reject", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	clock.advance(6 * time.Second)
	// 被拒终局：error 状态 tool part（input 非空，session_rejectedend 实测形态）
	fs.push(toolPartEvent("sess-1", "msg-deny", "prt-deny", "call-deny", "bash",
		"error", `{"command":"rm -rf /"}`, "The user rejected permission to use this specific tool call."))
	waitToolFrame(t, r, proto.FrameToolResult, "call-deny")
	// 空文本 + 已确认拒绝 → 被拒失败结果（B383 Wave 0 语义），兼作收尾屏障
	//（事件一律经收集器读取——ch 由收集 goroutine 独占消费，不能两处同读）
	fs.push(statusIdleEvent())
	sink.waitResult(t)
	denied := sink.lastResult(t)
	if denied.Result == nil || denied.Result.OK || denied.Result.VoidReason != executor.VoidReasonPermissionDenied {
		t.Fatalf("被拒空回合应产被拒失败结果，实得 %+v", denied)
	}
	timings := takeToolTimings(t, ad, taskID, sink)

	assertZeroWaitMissWarn(t, buf)
	api, tool, otherMS, toolEntries := sumTimingBuckets(timings)
	if len(toolEntries) != 1 {
		t.Fatalf("被拒终局应把迟到段收成恰一条 tool 条目，实得 %d 条（全部 %+v）", len(toolEntries), timings)
	}
	if api != 2_000 || tool != 6_000 || otherMS != 60_000 {
		t.Fatalf("等待应进 other、工具段应按执行口径收段，实得 api=%dms tool=%dms other=%dms", api, tool, otherMS)
	}
	var results []proto.Frame
	for _, f := range readFrames(t, r) {
		if f.Type == proto.FrameToolResult {
			results = append(results, f)
		}
	}
	if len(results) != 1 || results[0].Status != "error" {
		t.Fatalf("error part 应产恰一条 error 结果帧，实得 %+v", results)
	}
}

// TestRespondAskFollowUpTurnCarriesLatePermissionWait（B383 S1 续聊形状，真机
// 任务 5b3d38ec 2026-10-03 09:00:07 现场）：提问回合已结束（无 pending 原生提问）
// 时 RespondAsk 走回退分支续聊，新回合里 permission.asked（带 tool.callID）先于
// 任何 tool part 到达。修前回退分支只开 frames 回合、不开段回合，整个续聊回合在
// 段切分器里 turn==0，ToolStart/PauseWaiting/Resume 全被「回合外信号一律丢弃」
// 守卫吞掉——迟到段失效、成对 WARN 复活；修后与 Send 同形（BeginTurn 后上报段
// 回合），零 WARN、等待进 other 桶（按时钟：api=2s，tool=1s+5s=6s，other=60s）。
func TestRespondAskFollowUpTurnCarriesLatePermissionWait(t *testing.T) {
	buf := captureLog(t)
	fs := newFakeServer(t)
	taskID := "task-b383-respondask"
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	sink := startS1Sink(ch)
	r := ad.lookup(taskID)
	// 无活动段回合的初态：上一回合已被 mapIdle 收口后 seg.turn==0（EndTurn 归零、
	// 清 open 表），与换上全新段切分器同形——即「第一回合已正常结束、协调者正答复
	// git 兜底提问」的现场前提。换表发生在任何 push 之前（H2 同款 happens-before）。
	clock := newFakeClock(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC))
	r.seg = turn.NewSegmenter(clock.Now)

	// RespondAsk 回退分支：pending 空（原生提问不跨回合），按工单身份续接。
	// 生产驱动形态与 manager.go:1925/3009 一致，走 executor.AskResponder 契约方法。
	if err := ad.RespondAsk(context.Background(), executor.RespondAskReq{
		TaskID: taskID, TicketID: taskID + ":req_old", QuestionID: "req_old",
		Answer: "继续：用方案 A",
	}); err != nil {
		t.Fatalf("RespondAsk: %v", err)
	}
	// 续聊确实以关联 Prompt 起新回合（dispatch + respond_ask 恰两次 prompt）
	prompts := fs.prompts()
	if len(prompts) != 2 || !strings.Contains(prompts[1], "回答工单") {
		t.Fatalf("RespondAsk 回退分支应恰发一次关联 Prompt，实得 %d 次：%+v",
			len(prompts), prompts)
	}

	clock.advance(2 * time.Second)
	fs.push(permissionAskedEventOn("sess-1", "perm-ra", "bash",
		"git rev-parse --abbrev-ref HEAD", "call-ra"))
	sink.waitPermission(t) // 栅栏：迟到段已占、等待已挂（或修前落空 WARN 已打）

	clock.advance(60 * time.Second) // 审批等待窗口
	if err := ad.RespondPermission(context.Background(), taskID, "perm-ra", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	clock.advance(1 * time.Second)
	fs.push(toolPartEvent("sess-1", "msg-ra", "prt-ra", "call-ra", "bash",
		"running", `{"command":"git rev-parse --abbrev-ref HEAD"}`, ""))
	waitToolFrame(t, r, proto.FrameToolCall, "call-ra") // 栅栏：同键 ToolStart 已被去重
	clock.advance(5 * time.Second)
	fs.push(toolPartEvent("sess-1", "msg-ra", "prt-ra", "call-ra", "bash",
		"completed", `{"command":"git rev-parse --abbrev-ref HEAD"}`, "cards/B383-charter"))
	waitToolFrame(t, r, proto.FrameToolResult, "call-ra") // 栅栏：ToolEnd 已按假钟结算
	// 回合收尾屏障：push idle 并等 result，此后计时条目必已齐
	fs.push(statusIdleEvent())
	sink.waitResult(t)
	timings := takeToolTimings(t, ad, taskID, sink)

	assertZeroWaitMissWarn(t, buf)
	// 迟到段命中：恰一条「先占工具段」说明（每次等待只占一段）
	if n := strings.Count(buf.String(), "opencode 权限早于工具 part 到达"); n != 1 {
		t.Fatalf("续聊回合的迟到权限应恰一条「先占工具段」Info，实得 %d 条\n日志：\n%s",
			n, buf.String())
	}
	api, tool, otherMS, toolEntries := sumTimingBuckets(timings)
	if len(toolEntries) != 1 {
		t.Fatalf("一次工具调用应恰一条 tool 条目，实得 %d 条（全部条目 %+v）", len(toolEntries), timings)
	}
	if toolEntries[0].Label != "bash" || toolEntries[0].Detail != "git rev-parse --abbrev-ref HEAD" {
		t.Fatalf("tool 条目应带真实工具名与命令，实得 %+v", toolEntries[0])
	}
	if api != 2_000 || tool != 6_000 {
		t.Fatalf("api 桶应只含权限前的 2s、tool 桶应只含执行 6s，实得 api=%dms tool=%dms", api, tool)
	}
	if otherMS != 60_000 {
		t.Fatalf("60s 审批等待应进 other 桶，实得 %dms", otherMS)
	}
}

// TestSpikeReplayPermissionWaitAttributionUnchanged（B383 S1 回归）：spike3/spike5
// 真实抓包原样本回放——序 A（pending → running 非空 → permission.asked）里
// PauseWaiting 命中已开段，迟到段路径根本不触发，因此：
//   - 零等待窗口落空 WARN（两条样本都是序 A，本就无双 WARN 的土壤）；
//   - 既有桶归属不变：spike5 的 bash 工具段以真实入参收段（Detail=echo spike-hi）；
//     spike3 在权限后截断，工具段开着被回合收尾丢弃，不得伪造条目。
func TestSpikeReplayPermissionWaitAttributionUnchanged(t *testing.T) {
	for _, fx := range []spikeFixture{spike3, spike5} {
		t.Run(fx.file, func(t *testing.T) {
			buf := captureLog(t)
			got := collectReplay(t, startReplay(t, fx), 800*time.Millisecond)

			assertZeroWaitMissWarn(t, buf)
			var toolEntries []proto.TimingEntry
			for _, ev := range got {
				if ev.Type == "usage" && ev.Timing != nil && ev.Timing.Kind == proto.TimingKindTool {
					toolEntries = append(toolEntries, *ev.Timing)
				}
			}
			if fx.file == spike5.file {
				if len(toolEntries) != 1 {
					t.Fatalf("spike5 应恰一条 tool 条目，实得 %d 条", len(toolEntries))
				}
				if toolEntries[0].Label != "bash" || toolEntries[0].Detail != "echo spike-hi" {
					t.Fatalf("spike5 工具段归属不得变化，实得 %+v", toolEntries[0])
				}
			} else {
				if len(toolEntries) != 0 {
					t.Fatalf("spike3 截断在权限处，不得产出 tool 条目，实得 %+v", toolEntries)
				}
			}
		})
	}
}
