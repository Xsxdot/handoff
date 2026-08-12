package agentd

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor/turn"
	"github.com/xushixin/handoff/internal/proto"
)

// newTestServerWithTask 构造一个 frames 端点可用的 Server 并入库一个任务。
//
// 与 w3a_testhelpers_test.go 的 newTestAgentdEnv 同款：真实 SQLite + 丢弃日志，
// 只额外把 DataDir 落到 t.TempDir()——frames 文件的读写路径基于它，且
// taskRepoOrErr 要求任务有非空 Workdir（这里用 RepoPath 兜底满足）。
func newTestServerWithTask(t *testing.T) (*Server, string) {
	t.Helper()
	env := newTestAgentdEnvWithCfg(t,
		&config.Config{Token: testToken, DataDir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	taskID := "t-frames"
	if err := env.st.CreateTask(&proto.Task{
		ID: taskID, RepoPath: filepath.Join(env.srv.cfg.DataDir, "tasks", taskID),
		State: proto.TaskStateRunning,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return env.srv, taskID
}

// 服务端只发完整行：offset 落在半行中间时，把不完整的头部丢掉，

// 服务端只发完整行：offset 落在半行中间时，把不完整的头部丢掉，
// 从下一个完整行开始——否则客户端第一行永远解析失败。
func TestAlignToLineStart(t *testing.T) {
	buf := []byte(`{"seq":1}` + "\n" + `{"seq":2}` + "\n")
	// 从第 3 字节开始：落在第一行中间
	if got := string(alignToLineStart(buf)); got != `{"seq":2}`+"\n" {
		t.Errorf("应跳过残缺首行，实得 %q", got)
	}
	// 恰好落在行首：一字不丢
	if got := string(alignToLineStart([]byte(`{"seq":1}` + "\n"))); got != `{"seq":1}`+"\n" {
		t.Errorf("行首起点不该丢内容，实得 %q", got)
	}
}

// 尾部不完整的行不发送：等它补齐了下一轮再发。
func TestTrimIncompleteTail(t *testing.T) {
	complete, held := trimIncompleteTail([]byte(`{"seq":1}` + "\n" + `{"seq":2`))
	if string(complete) != `{"seq":1}`+"\n" {
		t.Errorf("应只发完整行，实得 %q", complete)
	}
	if held != len(`{"seq":2`) {
		t.Errorf("残缺尾部应被扣住 %d 字节，实得 %d", len(`{"seq":2`), held)
	}

	complete, held = trimIncompleteTail([]byte(`{"seq":1}` + "\n"))
	if string(complete) != `{"seq":1}`+"\n" || held != 0 {
		t.Errorf("全是完整行时不该扣住任何字节：%q held=%d", complete, held)
	}

	// 一行都没写完：一个字节都不发
	complete, held = trimIncompleteTail([]byte(`{"seq`))
	if len(complete) != 0 || held != len(`{"seq`) {
		t.Errorf("无完整行时应全部扣住：%q held=%d", complete, held)
	}
}

func TestAlignToLineStartNoNewline(t *testing.T) {
	// 整段都没有换行：没有可用的完整行起点，全丢
	if got := alignToLineStart([]byte(`{"seq":1`)); len(got) != 0 {
		t.Errorf("无换行时应返回空，实得 %q", string(got))
	}
}

func TestFramesOffsetParamsReuseRenderSemantics(t *testing.T) {
	// frames 与 render 共用 renderStartOffset：单位都是字节，
	// 优先级都是 offset > tail > 默认回溯。这里只钉住「确实复用了」
	if framesDefaultTail != renderDefaultTail {
		t.Errorf("frames 的默认回溯量应与 render 一致（%d），实得 %d",
			renderDefaultTail, framesDefaultTail)
	}
	_ = strings.TrimSpace("") // 保持 import 有用
}

// 文件不存在时返回 200 空内容而非 404——任务刚 dispatch 是正常状态。
func TestHandleTaskFramesMissingFileReturns200(t *testing.T) {
	srv, taskID := newTestServerWithTask(t)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/frames", nil)
	req.SetPathValue("id", taskID)
	rec := httptest.NewRecorder()

	srv.handleTaskFrames(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，实得 %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("无帧文件时响应体应为空，实得 %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type 应为 application/x-ndjson，实得 %q", ct)
	}
	if rec.Header().Get("X-Handoff-Frames-Size") != "0" {
		t.Errorf("空文件的 size 头应为 0，实得 %q", rec.Header().Get("X-Handoff-Frames-Size"))
	}
}

// offset 落在半行中间时，客户端收到的第一行必须是完整可解析的。
func TestHandleTaskFramesAlignsHalfLineOffset(t *testing.T) {
	srv, taskID := newTestServerWithTask(t)
	dir := filepath.Join(srv.cfg.DataDir, "tasks", taskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建任务目录: %v", err)
	}
	content := `{"seq":1,"type":"text"}` + "\n" + `{"seq":2,"type":"text"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, turn.FramesFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("写帧文件: %v", err)
	}

	// offset=5 落在第一行中间
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/frames?offset=5", nil)
	req.SetPathValue("id", taskID)
	rec := httptest.NewRecorder()

	srv.handleTaskFrames(rec, req)

	got := rec.Body.String()
	if got != `{"seq":2,"type":"text"}`+"\n" {
		t.Fatalf("应从下一个完整行开始，实得 %q", got)
	}
	var fr proto.Frame
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &fr); err != nil {
		t.Fatalf("客户端应能直接解析第一行，实得解析失败: %v", err)
	}
}
