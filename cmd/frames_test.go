package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// frames 每行原样输出一帧 JSON：它是 TUI 与脚本的数据源，不做人类友好格式化。
func TestFramesCmdEmitsRawJSONLines(t *testing.T) {
	body := `{"seq":1,"type":"text","delta":"你好"}` + "\n" +
		`{"seq":2,"type":"tool_call","tool":"Bash"}` + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/frames") {
			t.Errorf("应请求 /frames，实得 %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Handoff-Frames-Size", "999")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := runFrames(t.Context(), srv.URL, "", "task-1", 0, 0, false, &out); err != nil {
		t.Fatalf("runFrames: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("应输出 2 行，实得 %d：%q", len(lines), out.String())
	}
	var fr proto.Frame
	if err := json.Unmarshal([]byte(lines[0]), &fr); err != nil {
		t.Fatalf("每行都应是可解析的帧 JSON：%v", err)
	}
	if fr.Seq != 1 || fr.Delta != "你好" {
		t.Errorf("首帧内容应原样透传，实得 %+v", fr)
	}
}

// 空行是服务端的心跳，不是帧：不能原样喷给消费方。
func TestFramesCmdSkipsHeartbeatBlankLines(t *testing.T) {
	body := "\n" + `{"seq":1,"type":"text"}` + "\n" + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := runFrames(t.Context(), srv.URL, "", "task-1", 0, 0, false, &out); err != nil {
		t.Fatalf("runFrames: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != `{"seq":1,"type":"text"}` {
		t.Errorf("心跳空行应被跳过，实得 %q", out.String())
	}
}
