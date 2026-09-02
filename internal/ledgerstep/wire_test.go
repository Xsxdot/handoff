package ledgerstep

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestFinalMessageFromEventsUsesProtocolFields(t *testing.T) {
	events := []proto.Event{
		{Seq: 1, Type: proto.EventTypeProgress, Payload: json.RawMessage(`{"text":"working"}`)},
		{Seq: 2, Type: proto.EventTypeCompleted, Payload: json.RawMessage(`{"branch":"cards/B1-implement","commit":"abc","summary":"最终审阅报文"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil || message != "最终审阅报文" {
		t.Fatalf("completed summary: %v %q", err, message)
	}

	events = []proto.Event{{Seq: 3, Type: proto.EventTypeTurnFailed,
		Payload: json.RawMessage(`{"fail_reason":"回合失败原文"}`)}}
	message, err = finalMessageFromEvents(events)
	if err != nil || message != "回合失败原文" {
		t.Fatalf("turn_failed reason: %v %q", err, message)
	}

	if _, err := finalMessageFromEvents([]proto.Event{{Type: proto.EventTypeCompleted,
		Payload: json.RawMessage(`{"branch":"x"}`)}}); err == nil {
		t.Fatal("缺最终文本应报错")
	}
}

func TestFinalMessageFromEventsPrefersOptionalFinalText(t *testing.T) {
	finalText := "正文\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```"
	payload, err := json.Marshal(map[string]string{"summary": "旧摘要", "final_text": finalText})
	if err != nil {
		t.Fatal(err)
	}
	events := []proto.Event{{Type: proto.EventTypeCompleted, Payload: payload}}
	got, err := finalMessageFromEvents(events)
	if err != nil || got != finalText {
		t.Fatalf("应优先取 final_text: err=%v got=%q", err, got)
	}

	// 旧 agentd 的 payload 没有增量字段时必须继续使用 summary。
	got, err = finalMessageFromEvents([]proto.Event{{Type: proto.EventTypeCompleted,
		Payload: json.RawMessage(`{"summary":"历史摘要"}`)}})
	if err != nil || got != "历史摘要" {
		t.Fatalf("缺 final_text 时应回落 summary: err=%v got=%q", err, got)
	}

	// 指针字段把「显式空值」与「字段缺失」区分开，空正文不能静默伪装成摘要。
	if _, err := finalMessageFromEvents([]proto.Event{{Type: proto.EventTypeCompleted,
		Payload: json.RawMessage(`{"summary":"摘要","final_text":""}`)}}); err == nil {
		t.Fatal("显式空 final_text 应报错，不应退回摘要")
	}
}

func TestFinalMessageFromEventsPreservesVerdictAfterTrailer(t *testing.T) {
	finalText := "正文\n" +
		`{"branch":"handoff/B176","commit":"abc","summary":"简短摘要"}` +
		"\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```"
	payload, err := json.Marshal(map[string]string{
		"branch": "handoff/B176", "commit": "abc", "summary": "简短摘要", "final_text": finalText,
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := finalMessageFromEvents([]proto.Event{{Type: proto.EventTypeCompleted,
		Payload: payload}})
	if err != nil {
		t.Fatalf("取回合末正文: %v", err)
	}
	verdict, err := ParseVerdict(message)
	if err != nil || !verdict.Pass {
		t.Fatalf("trailer 后的裁决块应能路由: err=%v verdict=%+v message=%q", err, verdict, message)
	}
}

// codex 收尾会在 completed 之后再补一条 turn_failed（app-server 断连），
// 那是传输层假警报。取报文必须认 completed——否则环节拿到的是断连文案，
// 裁决必然解析失败、每次审阅都白白转人工。2026-08-19 真机实测踩中。
func TestFinalMessagePrefersCompletedOverTrailingTurnFailed(t *testing.T) {
	events := []proto.Event{
		{Type: proto.EventTypeProgress, Payload: []byte(`{"text":"审阅中"}`)},
		{Type: proto.EventTypeCompleted, Payload: []byte(`{"summary":"审阅结论\n` + "```" + `handoff-verdict\n{\"verdict\":\"pass\"}\n` + "```" + `"}`)},
		{Type: proto.EventTypeTurnFailed, Payload: []byte(`{"fail_reason":"codex 连接断开: EOF"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil {
		t.Fatalf("取报文: %v", err)
	}
	if !strings.Contains(message, "handoff-verdict") {
		t.Fatalf("应取 completed 的报文，实得: %q", message)
	}
}

// 真失败（没有 completed）仍要拿到失败原文，交上游转人工。
func TestFinalMessageFallsBackToFailure(t *testing.T) {
	events := []proto.Event{
		{Type: proto.EventTypeProgress, Payload: []byte(`{"text":"起手"}`)},
		{Type: proto.EventTypeTurnFailed, Payload: []byte(`{"fail_reason":"模型 400"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil || message != "模型 400" {
		t.Fatalf("失败回退: %v %q", err, message)
	}
}

// 环节等的必须是「回合终态」而不是「首个可动作事件」：审阅同样要过权限
// 门、也可能发工单，醒在这些事件上就去取报文必然报「没有最终报文」。
func TestWaitForTurnEndSkipsNonTerminalEvents(t *testing.T) {
	seq := []proto.EventType{
		proto.EventTypePermissionRequest,
		proto.EventTypeQuestion,
		proto.EventTypePermissionRequest,
		proto.EventTypeCompleted,
	}
	calls := 0
	err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
		ev := &proto.Event{Type: seq[calls]}
		if ev.Type == proto.EventTypeCompleted {
			ev.Payload = json.RawMessage("{\"final_text\":\"\\u0060\\u0060\\u0060handoff-verdict\\n{\\\"verdict\\\":\\\"pass\\\"}\\n\\u0060\\u0060\\u0060\"}")
		}
		calls++
		return ev, nil
	})
	if err != nil {
		t.Fatalf("等终态: %v", err)
	}
	if calls != len(seq) {
		t.Fatalf("应一直等到 completed（%d 次），实际 %d 次", len(seq), calls)
	}
}

func TestWaitForTurnEndWaitsForCompletedFinalText(t *testing.T) {
	events := []*proto.Event{
		{Type: proto.EventTypeCompleted, Payload: json.RawMessage(`{"summary":"早到摘要"}`)},
		{Type: proto.EventTypeCompleted, Payload: json.RawMessage("{\"summary\":\"最终摘要\",\"final_text\":\"\\u0060\\u0060\\u0060handoff-verdict\\n{\\\"verdict\\\":\\\"pass\\\"}\\n\\u0060\\u0060\\u0060\"}")},
	}
	calls := 0
	err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
		event := events[calls]
		calls++
		return event, nil
	})
	if err != nil {
		t.Fatalf("waitForTurnEnd() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("wait calls = %d, want 2", calls)
	}
}

func TestWaitForTurnEndGraceDeadlineReturnsSuccess(t *testing.T) {
	first := &proto.Event{Type: proto.EventTypeCompleted, Payload: json.RawMessage(`{"summary":"唯一摘要"}`)}
	oldGrace := turnEndGrace
	turnEndGrace = time.Nanosecond
	defer func() { turnEndGrace = oldGrace }()
	calls := 0
	deadlineSeen := make(chan bool, 1)
	err := waitForTurnEnd(context.Background(), func(ctx context.Context) (*proto.Event, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		_, ok := ctx.Deadline()
		deadlineSeen <- ok
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("deadline grace error = %v", err)
	}
	if !<-deadlineSeen {
		t.Fatal("grace wait context has no deadline")
	}
}

func TestWaitForTurnEndDoesNotSwallowParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := &proto.Event{Type: proto.EventTypeCompleted, Payload: json.RawMessage(`{"summary":"摘要"}`)}
	calls := 0
	err := waitForTurnEnd(parent, func(ctx context.Context) (*proto.Event, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestWaitForTurnEndIgnoresFailureDuringGrace(t *testing.T) {
	first := &proto.Event{Type: proto.EventTypeCompleted, Payload: json.RawMessage(`{"summary":"摘要"}`)}
	failed := &proto.Event{Type: proto.EventTypeFailed, Payload: json.RawMessage(`{"error":"late failure"}`)}
	turnFailed := &proto.Event{Type: proto.EventTypeTurnFailed, Payload: json.RawMessage(`{"error":"late turn failure"}`)}
	second := &proto.Event{Type: proto.EventTypeCompleted, Payload: json.RawMessage("{\"final_text\":\"\\u0060\\u0060\\u0060handoff-verdict\\n{\\\"verdict\\\":\\\"pass\\\"}\\n\\u0060\\u0060\\u0060\"}")}
	events := []*proto.Event{first, failed, turnFailed, second}
	calls := 0
	err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
		event := events[calls]
		calls++
		return event, nil
	})
	if err != nil {
		t.Fatalf("grace failure error = %v", err)
	}
	if calls != 4 {
		t.Fatalf("wait calls = %d, want 4", calls)
	}
}

func TestWaitForTurnEndReturnsFailureWithoutCompleted(t *testing.T) {
	for _, eventType := range []proto.EventType{proto.EventTypeFailed, proto.EventTypeTurnFailed} {
		t.Run(string(eventType), func(t *testing.T) {
			event := &proto.Event{Type: eventType, Payload: json.RawMessage(`{"error":"failed"}`)}
			calls := 0
			if err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
				calls++
				return event, nil
			}); err != nil {
				t.Fatalf("waitForTurnEnd() error = %v", err)
			} else if calls != 1 {
				t.Fatalf("wait calls = %d, want 1", calls)
			}
		})
	}
}

func TestFinalMessageUsesNonEmptyFinalTextAcrossCompletedEvents(t *testing.T) {
	events := []proto.Event{
		{Type: proto.EventTypeCompleted, Payload: json.RawMessage(`{"summary":"first summary"}`)},
		{Type: proto.EventTypeCompleted, Payload: json.RawMessage("{\"summary\":\"second summary\",\"final_text\":\"\\u0060\\u0060\\u0060handoff-verdict\\n{\\\"verdict\\\":\\\"pass\\\"}\\n\\u0060\\u0060\\u0060\"}")},
	}
	got, err := finalMessageFromEvents(events)
	if err != nil {
		t.Fatalf("finalMessageFromEvents() error = %v", err)
	}
	if !strings.Contains(got, "handoff-verdict") {
		t.Fatalf("message = %q, want final_text", got)
	}
}

// turn_failed 也是回合终态：executor 还活着，但这一回合结束了，报文在
// fail_reason 里——不能继续等下去把环节挂死。
func TestWaitForTurnEndAcceptsTurnFailed(t *testing.T) {
	calls := 0
	err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
		calls++
		return &proto.Event{Type: proto.EventTypeTurnFailed}, nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("turn_failed 应立即收口: err=%v calls=%d", err, calls)
	}
}
