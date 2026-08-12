// wait_until_done_test.go —— handoff wait --until-done 的 CLI 层契约测试。
//
// 职责：钉住 flag 冲突、成功 stdout 严格单行、失败/超时 stdout 为空、退出码与
// 总时限不被 progress 续命。
//
// 边界：事件流重连/快照分诊在 internal/client 的 wait_archived_test 里验，
// 这里只验 CLI 线格式与退出码，不重复网络细节。
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

func terminalWaitServer(t *testing.T, state proto.TaskState,
	events ...proto.Event) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks/t1" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(client.AttachInfo{
			Task:           proto.TaskView{Task: proto.Task{ID: "t1", State: state}},
			PendingTickets: []proto.Ticket{},
			RecentEvents:   events,
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func runWaitUntilDoneCLI(t *testing.T, serverURL string,
	extraArgs ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetFlags(t)
	addr := strings.TrimPrefix(serverURL, "http://")
	configPath = writeTestConfig(t,
		"listen: \""+addr+"\"\ntoken: \"test-token\"\n")
	targetName = ""
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	waitUntilDone, followFlag, notifyFlag, waitTimeout = false, false, false, 0
	t.Cleanup(func() {
		waitUntilDone, followFlag, notifyFlag, waitTimeout = false, false, false, 0
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	args := []string{"wait", "t1", "--until-done"}
	args = append(args, extraArgs...)
	rootCmd.SetArgs(args)
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = ExecuteContext(ctx)
	return out.String(), errOut.String(), err
}

func TestWaitUntilDoneRejectsFollowBeforeNetwork(t *testing.T) {
	resetFlags(t)
	followFlag, waitUntilDone = true, true
	t.Cleanup(func() { followFlag, waitUntilDone = false, false })
	err := waitCmd.RunE(waitCmd, []string{"t1"})
	if err == nil || !strings.Contains(err.Error(), "--follow") ||
		!strings.Contains(err.Error(), "--until-done") {
		t.Fatalf("应在网络前拒绝冲突 flag: %v", err)
	}
}

func TestWaitUntilDoneOutputsExactlyArchivedJSON(t *testing.T) {
	archived := proto.Event{Seq: 21, TaskID: "t1", Type: proto.EventTypeArchived,
		Payload: json.RawMessage(`{"note":"上游完成"}`)}
	ts := terminalWaitServer(t, proto.TaskStateCompleted, archived)
	stdout, stderr, err := runWaitUntilDoneCLI(t, ts.URL)
	if err != nil || ExitCode(err) != 0 {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	if strings.Count(strings.TrimSpace(stdout), "\n") != 0 {
		t.Fatalf("stdout 必须严格一行: %q", stdout)
	}
	var got proto.Event
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got.Seq != 21 || got.Type != proto.EventTypeArchived ||
		string(got.Payload) != `{"note":"上游完成"}` {
		t.Fatalf("got=%+v", got)
	}
}

func TestWaitUntilDoneFailedHasEmptyStdout(t *testing.T) {
	ts := terminalWaitServer(t, proto.TaskStateFailed)
	stdout, _, err := runWaitUntilDoneCLI(t, ts.URL)
	if !errors.Is(err, client.ErrDependencyFailed) || ExitCode(err) != ExitFailure || stdout != "" {
		t.Fatalf("err=%v code=%d stdout=%q", err, ExitCode(err), stdout)
	}
}

func TestWaitUntilDoneMissingArchivedFails(t *testing.T) {
	ts := terminalWaitServer(t, proto.TaskStateCompleted)
	stdout, _, err := runWaitUntilDoneCLI(t, ts.URL)
	if !errors.Is(err, client.ErrArchivedEventMissing) || ExitCode(err) != ExitFailure || stdout != "" {
		t.Fatalf("err=%v code=%d stdout=%q", err, ExitCode(err), stdout)
	}
	if !strings.Contains(err.Error(), "升级") || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("错误缺处置方向: %v", err)
	}
}

func TestWaitUntilDoneTimeoutIsTotalDespiteProgress(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tasks/t1" {
			_ = json.NewEncoder(w).Encode(client.AttachInfo{
				Task:           proto.TaskView{Task: proto.Task{ID: "t1", State: proto.TaskStateRunning}},
				PendingTickets: []proto.Ticket{},
				RecentEvents:   []proto.Event{},
			})
			return
		}
		if r.URL.Path != "/ws/events" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		var seq int64
		for range ticker.C {
			seq++
			b, _ := json.Marshal(proto.Event{Seq: seq, TaskID: "t1",
				Type: proto.EventTypeProgress})
			if err := conn.Write(r.Context(), websocket.MessageText, b); err != nil {
				return
			}
		}
	}))
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	started := time.Now()
	stdout, _, err := runWaitUntilDoneCLI(t, ts.URL, "--timeout", "120ms")
	if ExitCode(err) != ExitTimeout || stdout != "" {
		t.Fatalf("err=%v code=%d stdout=%q", err, ExitCode(err), stdout)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("progress 错误续命，elapsed=%v", elapsed)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteEventLinePropagatesWriterError(t *testing.T) {
	err := writeEventLine(failingWriter{}, &proto.Event{Type: proto.EventTypeArchived})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("写出失败必须上抛: %v", err)
	}
}
