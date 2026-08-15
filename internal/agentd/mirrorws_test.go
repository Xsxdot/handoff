// /ws/events 覆盖镜像任务的测试：历史从 mirror_events 重放、活事件续接、帧同形。
package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestMirrorWSReplaysAndLive 直接往本机 mirror_tasks + mirror_events 塞数据
// （不起真远端，测的是服务端读取路径），断言：
//  1. 两条历史事件被重放，seq 保留远端原值
//  2. 重放期间 hub.Publish 一条新事件，它随后也被收到（活事件续接）
//  3. 帧形状与本机任务无差别（能直接 json.Unmarshal 成 proto.Event）
func TestMirrorWSReplaysAndLive(t *testing.T) {
	env := newTestAgentdEnv(t)
	taskID := uuid.NewString()
	now := time.Now().UTC()
	if err := env.st.UpsertMirrorTask("devbox", proto.Task{
		ID: taskID, Name: "远端任务", State: proto.TaskStateRunning,
		RepoPath: "/remote/handoff", CreatedAt: now, UpdatedAt: now,
	}, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}
	// 两条远端原 seq 的事件（刻意非连续，验证原值保留）
	evs := []proto.Event{
		{Seq: 7, TaskID: taskID, Type: proto.EventTypeQuestion,
			Payload: json.RawMessage(`{"text":"继续吗"}`), CreatedAt: now},
		{Seq: 9, TaskID: taskID, Type: proto.EventTypeProgress,
			Payload: json.RawMessage(`{"text":"干活中"}`), CreatedAt: now},
	}
	for _, ev := range evs {
		if inserted, err := env.st.AppendMirrorEvent(taskID, ev); err != nil || !inserted {
			t.Fatalf("AppendMirrorEvent: inserted=%v err=%v", inserted, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(env.ts)+"/ws/events?task="+taskID+"&from_seq=0",
		&websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer " + env.token}},
		})
	if err != nil {
		t.Fatalf("WS 拨号失败: %v", err)
	}
	defer conn.CloseNow()

	// 断言 1+3：重放两条，seq 原值保留，帧能解成 proto.Event
	got := make([]proto.Event, 0, 2)
	for i := 0; i < 2; i++ {
		_, b, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("读历史事件 %d 失败: %v", i, err)
		}
		var ev proto.Event
		if err := json.Unmarshal(b, &ev); err != nil {
			t.Fatalf("帧无法解成 proto.Event: %v", err)
		}
		got = append(got, ev)
	}
	if got[0].Seq != 7 || got[1].Seq != 9 {
		t.Fatalf("重放 seq = [%d, %d]，期望保留远端原值 [7, 9]", got[0].Seq, got[1].Seq)
	}

	// 断言 2：重放期间 Publish 一条新事件，应被实时续接（seq 12 > 重放界 9）
	env.srv.Hub().Publish(proto.Event{
		Seq: 12, TaskID: taskID, Type: proto.EventTypeQuestion,
		Payload: json.RawMessage(`{"text":"新的提问"}`), CreatedAt: now,
	})
	_, b, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("读活事件失败: %v", err)
	}
	var live proto.Event
	if err := json.Unmarshal(b, &live); err != nil {
		t.Fatalf("活事件帧无法解成 proto.Event: %v", err)
	}
	if live.Seq != 12 || live.Type != proto.EventTypeQuestion {
		t.Errorf("活事件 = %+v，期望 seq=12 的 question", live)
	}
}
