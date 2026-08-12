// handoff footprint 的 CLI 行为测试：默认过滤、--all 全量、老 agentd 降级不报错。
package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// footprintBody 是三行体检结果：确有残留 / 判不出结论但 0 进程 / 干净。
const footprintBody = `{"usage":{"used":346,"limit":2666},"rows":[
	{"task_id":"aaaaaaaa-1111-2222-3333-444455556666","name":"有残留","state":"waiting_review","procs":7,"verdict":"ok"},
	{"task_id":"bbbbbbbb-1111-2222-3333-444455556666","name":"判不出","state":"completed","procs":0,"verdict":"leader_reuse"},
	{"task_id":"cccccccc-1111-2222-3333-444455556666","name":"干净","state":"completed","procs":0,"verdict":"ok"}]}`

// runFootprint 执行一次 footprint 命令，返回 stdout 与错误。
func runFootprint(t *testing.T, cfgPath, agentdURL string, all bool) (string, error) {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	args := []string{"footprint", "--config", cfgPath, "--agentd", agentdURL}
	if all {
		args = append(args, "--all")
	}
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		footprintJSONOut = false
		footprintAll = false
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// TestFootprintShowsResidueAndUnverdicted 默认过滤：有残留的与判不出的都要显示，
// 干净的不显示。
//
// **判不出的那行是这条用例的重点**：它 procs=0，一个只按 procs>0 过滤的实现
// 会把它藏起来——那正是「用一个假结论盖住该看的东西」。
func TestFootprintShowsResidueAndUnverdicted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(footprintBody))
	}))
	t.Cleanup(ts.Close)

	out, err := runFootprint(t, writeStatusConfig(t), ts.URL, false)
	if err != nil {
		t.Fatalf("footprint 应成功，得到错误: %v", err)
	}
	for _, want := range []string{"346/2666", "aaaaaaaa", "7 进程", "bbbbbbbb", "leader_reuse"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
	if strings.Contains(out, "cccccccc") {
		t.Fatalf("干净任务不该默认显示：\n%s", out)
	}
}

// TestFootprintAllShowsEverything --all 时三行都在。
func TestFootprintAllShowsEverything(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(footprintBody))
	}))
	t.Cleanup(ts.Close)

	out, err := runFootprint(t, writeStatusConfig(t), ts.URL, true)
	if err != nil {
		t.Fatalf("footprint --all 应成功: %v", err)
	}
	for _, want := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--all 输出缺少 %q：\n%s", want, out)
		}
	}
}

// TestFootprintDegradesOn404 老 agentd 返回 404：输出降级结论，**且不报错**。
func TestFootprintDegradesOn404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	out, err := runFootprint(t, writeStatusConfig(t), ts.URL, false)
	if err != nil {
		t.Fatalf("404 是一条成功的诊断结论，不该报错: %v", err)
	}
	if !strings.Contains(out, "版本过旧") {
		t.Fatalf("应输出降级结论：\n%s", out)
	}
}

// ptyFootprintBody 是带终端会话段的体检结果：一个有前台命令、一个空闲、
// 一个数不出进程数。
const ptyFootprintBody = `{"usage":{"used":346,"limit":2666},"rows":[],"pty":[
	{"id":"2f0f6a3c-8f1e-4f2a-9a77-1c2d3e4f5a6b","base_path":"/home/dev/handoff","pid":48213,"procs":4,"foreground":true},
	{"id":"9b8a7c6d-5e4f-4a3b-2c1d-0e9f8a7b6c5d","base_path":"/home/dev","pid":48999,"procs":1,"foreground":false},
	{"id":"3c3c3c3c-1111-2222-3333-444455556666","base_path":"/x","pid":50001,"foreground":false}]}`

// TestFootprintShowsPtySessions 断言：终端会话进账本，且 procs 缺席时如实说
// 「未知」而不是渲染成 0。
//
// **第三行是重点**：会话足迹的整个立论是「先让占用可见」，用一个 0 盖住
// 「我们数不出来」正是它要防的事。
func TestFootprintShowsPtySessions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(ptyFootprintBody))
	}))
	t.Cleanup(ts.Close)

	out, err := runFootprint(t, writeStatusConfig(t), ts.URL, false)
	if err != nil {
		t.Fatalf("footprint 应成功，得到错误: %v", err)
	}
	for _, want := range []string{"终端", "2f0f6a3c", "4 进程", "前台", "50001", "未知"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
}

// TestFootprintNoPtySectionWhenEmpty 断言：没有终端会话时不打这一段——
// 空标题也是噪音。
func TestFootprintNoPtySectionWhenEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(footprintBody))
	}))
	t.Cleanup(ts.Close)

	out, _ := runFootprint(t, writeStatusConfig(t), ts.URL, false)
	if strings.Contains(out, "终端") {
		t.Fatalf("没有会话时不该打终端段:\n%s", out)
	}
}
