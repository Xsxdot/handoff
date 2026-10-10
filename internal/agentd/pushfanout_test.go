// pushfanout_test.go —— 「需要你」事件 → 通知 → PushSender 的扇出缝（缝 S3）。
//
// 职责：锁三件事——分类映射表（四源 + 反例）、投递到该成员全部登记设备且
// badge 同源、410 删设备不重试；外加非阻塞入队的行为边界。
// 边界：不测 APNs 真实投递（apns_test 用假 APNs 钉请求形状）；不测 HTTP 登记
// （pushapi_test）；badge 计数本身由注入的 fake 提供（真源 collectInboxItems
// 在 wiring 测试里接）。
package agentd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"log/slog"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// pushCall 是 fake PushSender 记下的一次投递。
type pushCall struct {
	Token string
	Note  proto.PushNotification
	Badge int
}

// fakePushSender 是 PushSender 的测试替身：逐次记录并经 channel 交出，
// 可按 token 预设错误（410 映射用）。
type fakePushSender struct {
	mu     sync.Mutex
	errFor map[string]error
	calls  chan pushCall
}

func newFakePushSender() *fakePushSender {
	return &fakePushSender{errFor: map[string]error{}, calls: make(chan pushCall, 32)}
}

func (f *fakePushSender) Send(_ context.Context, token string, n proto.PushNotification, badge int) error {
	f.mu.Lock()
	err := f.errFor[token]
	f.mu.Unlock()
	f.calls <- pushCall{Token: token, Note: n, Badge: badge}
	return err
}

// wait 等第 i 次投递（1 起）；超时即测试失败，避免靠 sleep 猜时序。
func (f *fakePushSender) wait(t *testing.T, n int) []pushCall {
	t.Helper()
	out := make([]pushCall, 0, n)
	deadline := time.After(5 * time.Second)
	for len(out) < n {
		select {
		case c := <-f.calls:
			out = append(out, c)
		case <-deadline:
			t.Fatalf("等待第 %d 次投递超时，只收到 %d 次: %+v", n, len(out), out)
		}
	}
	return out
}

// newPushFanoutStore 开一个真 SQLite 库（fanout 只依赖 store 面）。
func newPushFanoutStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func registerDevice(t *testing.T, st *store.Store, member, device, token string) {
	t.Helper()
	if err := st.UpsertPushDevice(&proto.PushDevice{
		Member: member, DeviceID: device, Platform: proto.PushPlatformIOS,
		APNSToken: token, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertPushDevice: %v", err)
	}
}

const pushMember = "user:sy"

// TestPushFanoutDeliversToRegisteredDevices 是缝 S3 主断言：一次 Notify 投到
// 该成员全部登记设备，badge 取注入的计数器（同源计数的接缝）。
func TestPushFanoutDeliversToRegisteredDevices(t *testing.T) {
	st := newPushFanoutStore(t)
	registerDevice(t, st, pushMember, "d1", "tok-1")
	registerDevice(t, st, pushMember, "d2", "tok-2")
	// 他成员的设备不得被投到（fanout 按事件 Member 取表）。
	registerDevice(t, st, "user:other", "d3", "tok-3")

	sender := newFakePushSender()
	f := NewPushFanout(st, sender, func() string { return pushMember },
		slog.New(&roomsLogCapture{}))
	f.SetBadgeCounter(func(member string) int {
		if member != pushMember {
			t.Errorf("badgeCounter 收到非目标成员 %q", member)
		}
		return 7
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Start(ctx)

	n := proto.PushNotification{EventType: "decision", Member: pushMember,
		Title: "裁决", CardID: "B1", RefID: "42", DeepLink: "/cards?card=B1"}
	f.Notify(n)

	got := sender.wait(t, 2)
	tokens := map[string]bool{}
	for _, c := range got {
		tokens[c.Token] = true
		if c.Note != n {
			t.Errorf("投递载荷 = %+v，期望 %+v", c.Note, n)
		}
		if c.Badge != 7 {
			t.Errorf("badge = %d，期望 7", c.Badge)
		}
	}
	if len(tokens) != 2 || !tokens["tok-1"] || !tokens["tok-2"] {
		t.Fatalf("投递 token 集 = %v，期望 {tok-1,tok-2}", tokens)
	}
	if tokens["tok-3"] {
		t.Fatalf("不得投递他成员的设备")
	}
}

// TestPushFanoutClassifyLedger 表驱动锁 card_events 侧分类映射表（plan §T3）：
// 决策/提及/等人触发，其余与未知类型一律不触发（防新事件类型静默变推送）。
func TestPushFanoutClassifyLedger(t *testing.T) {
	decPayload := json.RawMessage(`{"decision_id":7,"body":"一句话：契约语义冲突\n第二行","options":["a"]}`)
	mentionPayload := json.RawMessage(`{"room":"s1","kind":"user","body":"看 B1","mentions":["user:sy"]}`)
	otherMention := json.RawMessage(`{"room":"s1","kind":"user","body":"看 B1","mentions":["user:someone"]}`)

	cases := []struct {
		name   string
		ev     proto.LedgerEvent
		want   proto.PushNotification
		wantOK bool
	}{
		{
			name: "decision_opened 卡级",
			ev:   proto.LedgerEvent{Seq: 11, CardID: "B1", Type: "decision_opened", Payload: decPayload},
			want: proto.PushNotification{EventType: proto.InboxOriginDecision, Member: pushMember,
				Title: "一句话：契约语义冲突", CardID: "B1", RefID: "7", DeepLink: "/cards?card=B1"},
			wantOK: true,
		},
		{
			name: "decision_opened 项目级无卡",
			ev:   proto.LedgerEvent{Seq: 12, CardID: "", Type: "decision_opened", Payload: decPayload},
			want: proto.PushNotification{EventType: proto.InboxOriginDecision, Member: pushMember,
				Title: "一句话：契约语义冲突", RefID: "7", DeepLink: "/"},
			wantOK: true,
		},
		{
			name: "room_message @我 卡级",
			ev:   proto.LedgerEvent{Seq: 13, CardID: "B2", Type: "room_message", Payload: mentionPayload},
			want: proto.PushNotification{EventType: proto.InboxOriginMention, Member: pushMember,
				Title: "@你：看 B1", CardID: "B2", RefID: "13", DeepLink: "/cards?card=B2"},
			wantOK: true,
		},
		{
			name: "room_message @我 无卡",
			ev: proto.LedgerEvent{Seq: 14, CardID: "", Type: "room_message",
				Payload: json.RawMessage(`{"room":"global","kind":"user","body":"全员点名","mentions":["user:sy"]}`)},
			want: proto.PushNotification{EventType: proto.InboxOriginMention, Member: pushMember,
				Title: "@你：全员点名", RefID: "14", DeepLink: "/"},
			wantOK: true,
		},
		{
			name: "needs_human 卡级",
			ev: proto.LedgerEvent{Seq: 15, CardID: "B3", Type: "needs_human",
				Payload: json.RawMessage(`{"reason":"等你裁决"}`)},
			want: proto.PushNotification{EventType: "needs_human", Member: pushMember,
				Title: "卡等待人工", CardID: "B3", RefID: "15", DeepLink: "/cards?card=B3"},
			wantOK: true,
		},
		{name: "反例：@别人",
			ev:     proto.LedgerEvent{Seq: 16, CardID: "B2", Type: "room_message", Payload: otherMention},
			wantOK: false},
		{name: "反例：needs_cleared",
			ev:     proto.LedgerEvent{Seq: 17, CardID: "B3", Type: "needs_cleared"},
			wantOK: false},
		{name: "反例：status_moved",
			ev: proto.LedgerEvent{Seq: 18, CardID: "B3", Type: "status_moved"}, wantOK: false},
		{name: "反例：review_verdict",
			ev: proto.LedgerEvent{Seq: 19, CardID: "B3", Type: "review_verdict"}, wantOK: false},
		{name: "反例：task_mirrored",
			ev: proto.LedgerEvent{Seq: 20, CardID: "B3", Type: "task_mirrored"}, wantOK: false},
		{name: "反例：未知类型不默认推送",
			ev: proto.LedgerEvent{Seq: 21, CardID: "B3", Type: "some_future_event"}, wantOK: false},
		{name: "反例：裁决已答复不再推",
			ev:     proto.LedgerEvent{Seq: 22, CardID: "B1", Type: "decision_answered", Payload: decPayload},
			wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyLedgerEvent(tc.ev, pushMember)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v，期望 %v（got %+v）", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got != tc.want {
				t.Fatalf("通知 = %+v\n期望   %+v", got, tc.want)
			}
		})
	}
}

// TestPushFanoutClassifyTask 锁 store.events 侧：permission/question 触发，
// approver_decision / ticket_answered 不触发（它们代表「已被处理」）。
func TestPushFanoutClassifyTask(t *testing.T) {
	cases := []struct {
		name   string
		ev     proto.Event
		want   proto.PushNotification
		wantOK bool
	}{
		{
			name: "permission_request",
			ev: proto.Event{Seq: 1, TaskID: "t1", Type: proto.EventTypePermissionRequest,
				Payload: json.RawMessage(`{"ticket_id":"t1:p1","kind":"gate"}`)},
			want: proto.PushNotification{EventType: proto.InboxOriginTicket, Member: pushMember,
				Title: "权限工单待答复", RefID: "t1", DeepLink: "/"},
			wantOK: true,
		},
		{
			name: "question",
			ev: proto.Event{Seq: 2, TaskID: "t1", Type: proto.EventTypeQuestion,
				Payload: json.RawMessage(`{"ticket_id":"t1:q1","kind":"ask"}`)},
			want: proto.PushNotification{EventType: proto.InboxOriginTicket, Member: pushMember,
				Title: "提问工单待答复", RefID: "t1", DeepLink: "/"},
			wantOK: true,
		},
		{name: "反例：approver_decision",
			ev: proto.Event{Seq: 3, TaskID: "t1", Type: proto.EventTypeApproverDecision}, wantOK: false},
		{name: "反例：ticket_answered",
			ev: proto.Event{Seq: 4, TaskID: "t1", Type: proto.EventTypeTicketAnswered}, wantOK: false},
		{name: "反例：未知类型",
			ev: proto.Event{Seq: 5, TaskID: "t1", Type: proto.EventType("some_future")}, wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyTaskEvent(tc.ev, pushMember)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v，期望 %v（got %+v）", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got != tc.want {
				t.Fatalf("通知 = %+v\n期望   %+v", got, tc.want)
			}
		})
	}
}

// TestPushFanoutClassifyRequiresMember 锁「成员未解析就不推」：console_user 未配
// 时分类一律 false（无从寻址，静默降级站内，不报假送达）。
func TestPushFanoutClassifyRequiresMember(t *testing.T) {
	if _, ok := classifyLedgerEvent(proto.LedgerEvent{
		Seq: 1, CardID: "B1", Type: "decision_opened",
		Payload: json.RawMessage(`{"decision_id":1,"body":"x"}`)}, ""); ok {
		t.Fatal("consoleMember 为空时不得产出通知")
	}
	if _, ok := classifyTaskEvent(proto.Event{
		TaskID: "t1", Type: proto.EventTypeQuestion}, ""); ok {
		t.Fatal("consoleMember 为空时不得产出通知")
	}
}

// TestPushFanoutDeletesOnUnregistered 锁 410 → 删设备且不再投（不重试、不假送达）。
func TestPushFanoutDeletesOnUnregistered(t *testing.T) {
	st := newPushFanoutStore(t)
	registerDevice(t, st, pushMember, "dead", "tok-dead")
	registerDevice(t, st, pushMember, "alive", "tok-alive")

	sender := newFakePushSender()
	sender.errFor["tok-dead"] = ErrPushUnregistered
	f := NewPushFanout(st, sender, func() string { return pushMember }, slog.New(&roomsLogCapture{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Start(ctx)

	f.Notify(proto.PushNotification{EventType: "decision", Member: pushMember, RefID: "1", Title: "t"})
	sender.wait(t, 2)

	deadline := time.Now().Add(5 * time.Second)
	for {
		devs, err := st.ListPushDevices(pushMember)
		if err != nil {
			t.Fatalf("ListPushDevices: %v", err)
		}
		if len(devs) == 1 && devs[0].DeviceID == "alive" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("失效设备未被删除，剩余: %+v", devs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPushFanoutNotifyNeverBlocks 锁非阻塞入队：队满丢弃 + Warn，不挡事件写。
func TestPushFanoutNotifyNeverBlocks(t *testing.T) {
	cap := &roomsLogCapture{}
	f := NewPushFanout(newPushFanoutStore(t), nil, func() string { return pushMember }, slog.New(cap))
	n := proto.PushNotification{EventType: "decision", Member: pushMember, RefID: "1", Title: "t"}
	for i := 0; i < 64; i++ {
		f.Notify(n)
	}
	done := make(chan struct{})
	go func() { f.Notify(n); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("队满后 Notify 阻塞（必须非阻塞丢弃）")
	}
	if _, ok := cap.find("推送队列已满，丢弃"); !ok {
		t.Fatal("队满丢弃未打 Warn")
	}
}

// TestPushFanoutNilSenderIsSilent 无 APNs 配置时静默降级站内：不 panic、不报错。
func TestPushFanoutNilSenderIsSilent(t *testing.T) {
	st := newPushFanoutStore(t)
	registerDevice(t, st, pushMember, "d1", "tok-1")
	f := NewPushFanout(st, nil, func() string { return pushMember }, slog.New(&roomsLogCapture{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Start(ctx)
	// sender 为 nil 时 deliver 直接返回——不投、不删设备、不 panic。
	f.Notify(proto.PushNotification{EventType: "decision", Member: pushMember, RefID: "1", Title: "t"})
	time.Sleep(50 * time.Millisecond)
	devs, err := st.ListPushDevices(pushMember)
	if err != nil {
		t.Fatalf("ListPushDevices: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("静默降级不得动设备表，实有 %d 条", len(devs))
	}
}
