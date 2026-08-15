// mirrorstream_test.go —— StreamEventsOnce 的行为契约测试。
//
// 职责：钉住「一次连接、交付每帧、不读写 cursor 文件」三条。最后一条是本方法
// 存在的全部理由——agentd 做事件镜像时若复用带 cursor 的路径，会与人工
// handoff wait 互相推进对方的水位。
package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestStreamEventsOnceDeliversAndNoCursor 断言：两条事件都被交付给 onEvent，
// 且调用前后 ~/.handoff/cursor-<task> 都不存在。
func TestStreamEventsOnceDeliversAndNoCursor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	evs := []proto.Event{
		{Seq: 7, TaskID: "t1", Type: proto.EventTypeQuestion, Payload: json.RawMessage(`{"text":"继续吗"}`)},
		{Seq: 9, TaskID: "t1", Type: proto.EventTypeProgress, Payload: json.RawMessage(`{"text":"干活中"}`)},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for _, ev := range evs {
			b, err := json.Marshal(ev)
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), websocket.MessageText, b); err != nil {
				return
			}
		}
		c.CloseNow()
	}))
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })

	home := os.Getenv("HOME")
	cursorPath := filepath.Join(home, ".handoff", "cursor-t1")
	if _, err := os.Stat(cursorPath); !os.IsNotExist(err) {
		t.Fatalf("调用前 cursor 文件不该存在：%s", cursorPath)
	}

	var got []proto.Event
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := client.New(ts.URL, "").StreamEventsOnce(ctx, "t1", 0, func(ev proto.Event) error {
		got = append(got, ev)
		return nil
	})
	// 服务端推完即断，StreamEventsOnce 因此返回读取错误——单次连接的自然终点，
	// 镜像订阅循环正是靠「连接结束就重连」来续拉。错误值本身不是契约，交付才是
	if err != nil && ctx.Err() == nil {
		t.Logf("连接结束（预期）：%v", err)
	}
	if len(got) != 2 || got[0].Seq != 7 || got[1].Seq != 9 {
		t.Fatalf("交付事件 = %+v，期望两条 seq=7,9", got)
	}
	if got[1].Type != proto.EventTypeProgress {
		t.Errorf("StreamEventsOnce 不过滤 progress：实得 %v", got[1].Type)
	}
	if _, err := os.Stat(cursorPath); !os.IsNotExist(err) {
		t.Errorf("调用后 cursor 文件必须仍不存在：%s", cursorPath)
	}
}
