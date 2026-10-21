package cmd

// b452_skilled_test.go —— B452 CLI 竖切：dispatch --template（V2/V6）、--wait 与 wait --final（V5）、
// handoff artifacts（V7）。agentd 一侧是 httptest 桩：捕获请求体、计数全部请求、按需回快照/事件流/产物包。

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

// b452Agentd 是 agentd 桩。零值字段取缺省：能力位齐全、派发回 b452TaskJSON。
type b452Agentd struct {
	mu         sync.Mutex
	requests   []string
	body       map[string]any
	statusJSON string
	// dispatchCode/dispatchResp：派发应答；0 = 200 + task。
	dispatchCode int
	dispatchResp string
	task         proto.Task
	attach       func() client.AttachInfo
	stream       func(conn *websocket.Conn, r *http.Request)
	artifacts    http.HandlerFunc
	ts           *httptest.Server
}

func (a *b452Agentd) count(prefix string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, r := range a.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func newB452Agentd(t *testing.T, a *b452Agentd) *b452Agentd {
	t.Helper()
	if a.statusJSON == "" {
		a.statusJSON = `{"disciplines_supported":true,"skilled_dispatch_supported":true}`
	}
	if a.task.ID == "" {
		a.task = proto.Task{ID: "t1", State: proto.TaskStateRunning, Target: "linux-01",
			ArtifactsDir: "/nonexistent/b452/tasks/t1/artifacts"}
	}
	a.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.requests = append(a.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		a.mu.Unlock()
		switch {
		case r.URL.Path == "/api/status":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, a.statusJSON)
		case r.Method == http.MethodPost && r.URL.Path == "/api/tasks":
			var got map[string]any
			_ = json.NewDecoder(r.Body).Decode(&got)
			a.mu.Lock()
			a.body = got
			a.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if a.dispatchCode != 0 {
				w.WriteHeader(a.dispatchCode)
				fmt.Fprint(w, a.dispatchResp)
				return
			}
			_ = json.NewEncoder(w).Encode(a.task)
		case r.Method == http.MethodGet && r.URL.Path == "/api/tasks/"+a.task.ID:
			w.Header().Set("Content-Type", "application/json")
			info := client.AttachInfo{Task: proto.TaskView{Task: a.task}, PendingTickets: []proto.Ticket{},
				RecentEvents: []proto.Event{}}
			if a.attach != nil {
				info = a.attach()
			}
			_ = json.NewEncoder(w).Encode(info)
		case r.URL.Path == "/api/tasks/"+a.task.ID+"/artifacts":
			if a.artifacts == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			a.artifacts(w, r)
		case r.URL.Path == "/ws/events":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			if a.stream != nil {
				a.stream(conn, r)
				return
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() { a.ts.CloseClientConnections(); a.ts.Close() })
	return a
}

// seedB452Template 在 dataDir 的账本里写一条模板。
func seedB452Template(t *testing.T, dataDir, name string, def ledger.TemplateDef) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dataDir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.PutTemplate(name, def); err != nil {
		t.Fatal(err)
	}
}

// runB452CLI 以桩为本机 agentd 跑一次 CLI。
func runB452CLI(t *testing.T, a *b452Agentd, dataDir string, args ...string) (string, string, error) {
	t.Helper()
	resetFlags(t)
	resetCommandTree(rootCmd) // 同一用例内多次执行：上一次的 flag 值不得带进来
	addr := strings.TrimPrefix(a.ts.URL, "http://")
	cfg := "listen: \"" + addr + "\"\ntoken: \"" + testToken + "\"\ndatadir: \"" + dataDir + "\"\n" +
		"targets:\n  linux-01:\n    addr: \"" + addr + "\"\n    token: \"" + testToken + "\"\n"
	configPath = writeTestConfig(t, cfg)
	targetName = ""
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	rootCmd.SetArgs(args)
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := ExecuteContext(ctx)
	return out.String(), errOut.String(), err
}

var b452ReadonlyDef = ledger.TemplateDef{Receiver: "mimo", Mode: ledger.TemplateModeReadonly,
	Prompt: "正文 {{CARD}}|{{TITLE}}|{{ACCEPT}}"}

// ---- V2：按名派发的请求体 ----

func TestB452DispatchTemplateBody(t *testing.T) {
	dataDir := t.TempDir()
	seedB452Template(t, dataDir, "code-investigate", b452ReadonlyDef)
	a := newB452Agentd(t, &b452Agentd{})
	stdout, stderr, err := runB452CLI(t, a, dataDir, "dispatch", "--template", "code-investigate",
		"--prompt", "查 Foo", "--project", "proj1", "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v stderr=%s", err, stderr)
	}
	want := ledgerstep.RenderCardlessPrompt(b452ReadonlyDef.Prompt, "查 Foo")
	if want != "正文 ||\n\n## 本次任务\n\n查 Foo" {
		t.Fatalf("无卡渲染基准变了: %q", want)
	}
	if a.body["prompt"] != want || a.body["receiver"] != "mimo" || a.body["mode"] != "readonly" {
		t.Fatalf("请求体 prompt/receiver/mode 不对: %v", a.body)
	}
	if _, ok := a.body["here"]; ok {
		t.Fatalf("定义未给 target 时不应带 here: %v", a.body)
	}
	if !strings.Contains(stderr, "熟练工 code-investigate v1") {
		t.Fatalf("stderr 应回显熟练工名与版本: %s", stderr)
	}
	if strings.Count(stdout, "\n") != 1 || !strings.Contains(stdout, `"id":"t1"`) {
		t.Fatalf("不带 --wait 时 stdout 仍是单行任务 JSON（F22），得 %q", stdout)
	}
}

func TestB452DispatchTemplateOverridesAndAffinity(t *testing.T) {
	dataDir := t.TempDir()
	def := b452ReadonlyDef
	def.Target = "linux-01"
	seedB452Template(t, dataDir, "ci", def)
	t.Run("--receiver 覆盖定义", func(t *testing.T) {
		a := newB452Agentd(t, &b452Agentd{})
		if _, stderr, err := runB452CLI(t, a, dataDir, "dispatch", "--template", "ci", "--prompt", "x",
			"--project", "p", "--receiver", "dp", "--no-terminal"); err != nil {
			t.Fatalf("%v %s", err, stderr)
		}
		if a.body["receiver"] != "dp" {
			t.Fatalf("--receiver 应覆盖定义: %v", a.body["receiver"])
		}
		if a.body["machine"] != "linux-01" {
			t.Fatalf("定义 target=linux-01 应进 machine: %v", a.body)
		}
	})
	t.Run("--here 覆盖定义 target", func(t *testing.T) {
		a := newB452Agentd(t, &b452Agentd{})
		if _, stderr, err := runB452CLI(t, a, dataDir, "dispatch", "--template", "ci", "--prompt", "x",
			"--project", "p", "--here", "--no-terminal"); err != nil {
			t.Fatalf("%v %s", err, stderr)
		}
		if a.body["here"] != true {
			t.Fatalf("应 here=true: %v", a.body)
		}
		if _, ok := a.body["machine"]; ok {
			t.Fatalf("--here 时不应带 machine: %v", a.body)
		}
	})
}

func TestB452DispatchTemplateRejectsBeforeNetwork(t *testing.T) {
	dataDir := t.TempDir()
	seedB452Template(t, dataDir, "ci", b452ReadonlyDef)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"模板不存在", []string{"dispatch", "--template", "nope", "--prompt", "x", "--project", "p"}, "nope"},
		{"只读 × --branch", []string{"dispatch", "--template", "ci", "--prompt", "x", "--project", "p", "--branch", "b"}, "--branch"},
		{"只读 × --new-worktree", []string{"dispatch", "--template", "ci", "--prompt", "x", "--project", "p", "--new-worktree"}, "--new-worktree"},
		{"--here × --target", []string{"--target", "linux-01", "dispatch", "--prompt", "x", "--project", "p", "--here"}, "--target"},
		{"--here × --machine", []string{"dispatch", "--prompt", "x", "--project", "p", "--here", "--machine", "m"}, "machine"},
		{"--timeout 无 --wait", []string{"dispatch", "--prompt", "x", "--project", "p", "--timeout", "1m"}, "--wait"},
		{"--template 缺 --prompt", []string{"dispatch", "--template", "ci", "--project", "p"}, "--prompt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newB452Agentd(t, &b452Agentd{})
			_, _, err := runB452CLI(t, a, dataDir, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("应拒且含 %q，得 %v", tc.want, err)
			}
			if n := a.count(""); n != 0 {
				t.Fatalf("应零网络，得 %d 次请求: %v", n, a.requests)
			}
		})
	}
}

func TestB452DispatchCapabilityBitRequired(t *testing.T) {
	dataDir := t.TempDir()
	seedB452Template(t, dataDir, "ci", b452ReadonlyDef)
	a := newB452Agentd(t, &b452Agentd{statusJSON: `{"disciplines_supported":true}`})
	_, _, err := runB452CLI(t, a, dataDir, "dispatch", "--template", "ci", "--prompt", "x", "--project", "p")
	if err == nil || !strings.Contains(err.Error(), "升级") {
		t.Fatalf("能力位缺席应拒发并提示升级，得 %v", err)
	}
	if n := a.count("POST /api/tasks"); n != 0 {
		t.Fatalf("拒发时零派发请求（F13），得 %d", n)
	}
}

func TestB452DispatchAffinityRefusedExit4(t *testing.T) {
	a := newB452Agentd(t, &b452Agentd{dispatchCode: http.StatusUnprocessableEntity,
		dispatchResp: `{"error":"机器亲和不满足：载体 mimo 在 linux-01，本机是 mac-mini","kind":"affinity_unmet"}`})
	_, _, err := runB452CLI(t, a, t.TempDir(), "dispatch", "--prompt", "x", "--project", "p", "--here", "--no-terminal")
	if ExitCode(err) != ExitAffinityRefused || !strings.Contains(err.Error(), "机器亲和不满足：载体 mimo 在 linux-01") {
		t.Fatalf("422 应退出码 4 且带原文，得 code=%d err=%v", ExitCode(err), err)
	}
}

func TestB452DispatchForwardedProjectErrorSkipsAutoRegister(t *testing.T) {
	a := newB452Agentd(t, &b452Agentd{dispatchCode: http.StatusBadRequest,
		dispatchResp: `{"error":"执行机 linux-01：项目未登记: proj"}`})
	_, stderr, err := runB452CLI(t, a, t.TempDir(), "dispatch", "--prompt", "x", "--project", "p", "--no-terminal")
	if err == nil || !strings.Contains(err.Error(), "linux-01") {
		t.Fatalf("应报执行机侧未登记，得 %v", err)
	}
	if n := a.count("POST /api/tasks"); n != 1 {
		t.Fatalf("转发来的未登记不得触发补登记重发，派发应恰 1 次，得 %d", n)
	}
	if strings.Contains(stderr, "正在自动登记") || a.count("POST /api/projects") != 0 {
		t.Fatalf("不得自动补登记: stderr=%s req=%v", stderr, a.requests)
	}
}

// ---- V5：--wait 与 wait --final ----

func b452CompletedEv(seq int64, text string) proto.Event {
	p, _ := json.Marshal(map[string]any{"summary": "s", "final_text": text})
	return proto.Event{Seq: seq, TaskID: "t1", Type: proto.EventTypeCompleted, Payload: p}
}

func b452StreamEvents(evs ...proto.Event) func(*websocket.Conn, *http.Request) {
	return func(conn *websocket.Conn, r *http.Request) {
		for _, ev := range evs {
			b, _ := json.Marshal(ev)
			if err := conn.Write(r.Context(), websocket.MessageText, b); err != nil {
				return
			}
		}
		<-r.Context().Done()
	}
}

func TestB452DispatchWaitCompletedStdoutIsExactlyFinalText(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"无尾换行补一个", "## 结论\n答案\n{\"summary\":\"x\"}", "## 结论\n答案\n{\"summary\":\"x\"}\n"},
		{"有尾换行原样", "答案\n", "答案\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedB452Template(t, dataDir, "ci", b452ReadonlyDef)
			a := newB452Agentd(t, &b452Agentd{stream: b452StreamEvents(b452CompletedEv(2, tc.text))})
			stdout, stderr, err := runB452CLI(t, a, dataDir, "dispatch", "--template", "ci", "--prompt", "x",
				"--project", "p", "--wait", "--no-terminal")
			if err != nil || ExitCode(err) != 0 {
				t.Fatalf("err=%v stderr=%s", err, stderr)
			}
			if stdout != tc.want {
				t.Fatalf("stdout 应恰为最终文本:\n got %q\nwant %q", stdout, tc.want)
			}
			if !strings.Contains(stderr, `"id":"t1"`) {
				t.Fatalf("--wait 时任务 JSON 应改打 stderr: %s", stderr)
			}
			if !strings.Contains(a.requests[len(a.requests)-1], "machine=linux-01") &&
				a.count("GET /ws/events?task=t1&from_seq=0&machine=linux-01") == 0 {
				t.Fatalf("--wait 应按任务 target 跟随: %v", a.requests)
			}
		})
	}
}

func TestB452DispatchWaitNonCompletionExit3(t *testing.T) {
	p, _ := json.Marshal(map[string]any{"fail_reason": "回合缺 trailer"})
	a := newB452Agentd(t, &b452Agentd{stream: b452StreamEvents(
		proto.Event{Seq: 2, TaskID: "t1", Type: proto.EventTypeTurnFailed, Payload: p})})
	stdout, stderr, err := runB452CLI(t, a, t.TempDir(), "dispatch", "--prompt", "x", "--project", "p",
		"--wait", "--no-terminal")
	if ExitCode(err) != ExitTaskFailed || stdout != "" {
		t.Fatalf("应退出码 3 且 stdout 空，得 code=%d stdout=%q err=%v", ExitCode(err), stdout, err)
	}
	all := stderr + err.Error()
	if !strings.Contains(all, "回合缺 trailer") || !strings.Contains(all, "t1") {
		t.Fatalf("stderr 应含原因与任务 id: %s / %v", stderr, err)
	}
}

func TestB452DispatchWaitTimeoutExit124NoStop(t *testing.T) {
	a := newB452Agentd(t, &b452Agentd{})
	stdout, stderr, err := runB452CLI(t, a, t.TempDir(), "dispatch", "--prompt", "x", "--project", "p",
		"--wait", "--timeout", "300ms", "--no-terminal")
	if ExitCode(err) != ExitTimeout || stdout != "" {
		t.Fatalf("应 124 且 stdout 空，得 code=%d stdout=%q err=%v", ExitCode(err), stdout, err)
	}
	if !strings.Contains(stderr+err.Error(), "handoff wait t1 --final --machine linux-01") {
		t.Fatalf("应给出接着等的命令: %s / %v", stderr, err)
	}
	if n := a.count("POST /api/tasks/t1/stop"); n != 0 {
		t.Fatalf("超时不得停任务，得 %d 次 stop", n)
	}
}

func TestB452WaitFinalLastTurnAndFlagConflicts(t *testing.T) {
	a := newB452Agentd(t, &b452Agentd{attach: func() client.AttachInfo {
		return client.AttachInfo{
			Task: proto.TaskView{Task: proto.Task{ID: "t1", State: proto.TaskStateWaitingReview}},
			RecentEvents: []proto.Event{b452CompletedEv(3, "首回合"),
				{Seq: 4, TaskID: "t1", Type: proto.EventTypeTicketAnswered, Payload: json.RawMessage(`{}`)},
				b452CompletedEv(9, "末回合")},
			PendingTickets: []proto.Ticket{}}
	}})
	stdout, stderr, err := runB452CLI(t, a, t.TempDir(), "wait", "t1", "--final", "--machine", "linux-01")
	if err != nil || stdout != "末回合\n" {
		t.Fatalf("wait --final 应取最后一回合: stdout=%q stderr=%s err=%v", stdout, stderr, err)
	}
	if a.count("GET /api/tasks/t1?machine=linux-01") == 0 {
		t.Fatalf("应按机取任务: %v", a.requests)
	}
	for _, extra := range [][]string{{"--follow"}, {"--until-done"}} {
		b := newB452Agentd(t, &b452Agentd{})
		_, _, err := runB452CLI(t, b, t.TempDir(), append([]string{"wait", "t1", "--final"}, extra...)...)
		if err == nil || !strings.Contains(err.Error(), "--final") {
			t.Fatalf("--final × %v 应拒，得 %v", extra, err)
		}
		if n := b.count(""); n != 0 {
			t.Fatalf("冲突应零网络，得 %v", b.requests)
		}
	}
	c := newB452Agentd(t, &b452Agentd{})
	if _, _, err := runB452CLI(t, c, t.TempDir(), "wait", "t1", "--machine", "m"); err == nil ||
		!strings.Contains(err.Error(), "--final") || c.count("") != 0 {
		t.Fatalf("--machine 只与 --final 同用，应零网络拒，得 %v %v", err, c.requests)
	}
}

// ---- V7 CLI：产物 ----

func b452ArtifactsTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(tw, body)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func b452ServeTarGz(pkg []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(pkg)
	}
}

func TestB452ArtifactsCommand(t *testing.T) {
	t.Run("同机：原路径、零下载", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "artifacts")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "r.md"), []byte("r"), 0o600); err != nil {
			t.Fatal(err)
		}
		a := newB452Agentd(t, &b452Agentd{task: proto.Task{ID: "t1", State: proto.TaskStateCompleted, ArtifactsDir: dir}})
		stdout, stderr, err := runB452CLI(t, a, t.TempDir(), "artifacts", "t1")
		if err != nil || stdout != dir+"\n" {
			t.Fatalf("同机应输出原路径: stdout=%q stderr=%s err=%v", stdout, stderr, err)
		}
		if n := a.count("GET /api/tasks/t1/artifacts"); n != 0 {
			t.Fatalf("同机零下载，得 %d", n)
		}
	})
	t.Run("跨机：解到本机临时目录、内容一致", func(t *testing.T) {
		t.Setenv("TMPDIR", t.TempDir())
		a := newB452Agentd(t, &b452Agentd{artifacts: b452ServeTarGz(b452ArtifactsTarGz(t, map[string]string{"report.md": "# 报告\n"}))})
		stdout, stderr, err := runB452CLI(t, a, t.TempDir(), "artifacts", "t1", "--machine", "linux-01")
		if err != nil || strings.Count(stdout, "\n") != 1 {
			t.Fatalf("应恰一行路径: stdout=%q stderr=%s err=%v", stdout, stderr, err)
		}
		b, rerr := os.ReadFile(filepath.Join(strings.TrimSpace(stdout), "report.md"))
		if rerr != nil || string(b) != "# 报告\n" {
			t.Fatalf("解出内容不一致: %q %v", b, rerr)
		}
		if a.count("GET /api/tasks/t1/artifacts?machine=linux-01") != 1 {
			t.Fatalf("应按机下载: %v", a.requests)
		}
	})
	t.Run("含上跳条目：整次失败、零落地", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		a := newB452Agentd(t, &b452Agentd{artifacts: b452ServeTarGz(b452ArtifactsTarGz(t, map[string]string{"../x": "x"}))})
		_, _, err := runB452CLI(t, a, t.TempDir(), "artifacts", "t1")
		if err == nil {
			t.Fatal("恶意包应失败")
		}
		entries, _ := os.ReadDir(tmp)
		if len(entries) != 0 {
			t.Fatalf("零文件落地，得 %v", entries)
		}
	})
	t.Run("空产物：stdout 空、stderr 提示、退出 0", func(t *testing.T) {
		a := newB452Agentd(t, &b452Agentd{})
		stdout, stderr, err := runB452CLI(t, a, t.TempDir(), "artifacts", "t1")
		if err != nil || stdout != "" || !strings.Contains(stderr, "任务无产物") {
			t.Fatalf("stdout=%q stderr=%s err=%v", stdout, stderr, err)
		}
	})
}

func TestB452DispatchWaitPrintsArtifactsLine(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	a := newB452Agentd(t, &b452Agentd{stream: b452StreamEvents(b452CompletedEv(2, "完成")),
		artifacts: b452ServeTarGz(b452ArtifactsTarGz(t, map[string]string{"r.md": "R"}))})
	stdout, stderr, err := runB452CLI(t, a, t.TempDir(), "dispatch", "--prompt", "x", "--project", "p",
		"--wait", "--no-terminal")
	if err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "完成" || !strings.HasPrefix(lines[1], "artifacts: /") {
		t.Fatalf("stdout 应为「最终文本\\nartifacts: <绝对路径>」，得 %q", stdout)
	}
	b, rerr := os.ReadFile(filepath.Join(strings.TrimPrefix(lines[1], "artifacts: "), "r.md"))
	if rerr != nil || string(b) != "R" {
		t.Fatalf("产物内容不一致: %q %v", b, rerr)
	}
}

// ---- 只读形态基于已提交基线 ----

func TestB452ReadonlyDirtyNoticeAndBaseCommit(t *testing.T) {
	dir := dirtyTestRepo(t)
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://example.com/b452/x.git").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	sha := strings.TrimSpace(string(head))
	dataDir := t.TempDir()
	seedB452Template(t, dataDir, "ci", b452ReadonlyDef)
	a := newB452Agentd(t, &b452Agentd{})
	_, stderr, err := runB452CLI(t, a, dataDir, "dispatch", "--template", "ci", "--prompt", "x", "--no-terminal")
	if err != nil {
		t.Fatalf("只读形态脏树不拒发: %v %s", err, stderr)
	}
	if a.body["base_commit"] != sha {
		t.Fatalf("--template 应总采本地 HEAD 作 base_commit，得 %v", a.body["base_commit"])
	}
	if !strings.Contains(stderr, "只读调查基于已提交的 "+sha[:7]+"，本地未提交改动不可见") {
		t.Fatalf("应提示只读基于已提交基线: %s", stderr)
	}
	// 分支形态：同样的脏树照远端语义拒发
	seedB452Template(t, dataDir, "br", ledger.TemplateDef{Executor: "fake", Prompt: "p", Discipline: "implement", Mode: ledger.TemplateModeBranch})
	b := newB452Agentd(t, &b452Agentd{})
	if _, _, err := runB452CLI(t, b, dataDir, "dispatch", "--template", "br", "--prompt", "x", "--no-terminal"); err == nil ||
		!strings.Contains(err.Error(), "--allow-dirty") || b.count("POST /api/tasks") != 0 {
		t.Fatalf("分支形态脏树应拒发且零派发，得 %v %v", err, b.requests)
	}
}
