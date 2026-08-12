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
