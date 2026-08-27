// B229 T3 裸派发测试：handoff dispatch 的每一次派发都带组装好的纪律正文与
// 版本号过 wire；目标机能力位不支持时拒发且零任务请求；--discipline-file 把
// 文件内容作 RawText 直通（P1 裁决 (a)）。假目标机是 httptest 真服务，
// 断言落在它收到的原始 JSON 上。
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// captureTarget 是假目标机：/api/status 按 statusBody 应答；其余路径记录请求
// 并回任务 JSON。statusHit 不计入 taskHits——「目标机请求计数」判据数的是任务。
type captureTarget struct {
	ts *httptest.Server

	mu        sync.Mutex
	bodies    []map[string]any
	taskHits  int
	otherPath []string
}

func newCaptureTarget(t *testing.T, statusBody string) *captureTarget {
	t.Helper()
	ct := &captureTarget{}
	ct.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, statusBody)
			return
		}
		ct.mu.Lock()
		ct.otherPath = append(ct.otherPath, r.URL.Path)
		if r.URL.Path == "/api/tasks" && r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			ct.bodies = append(ct.bodies, body)
			ct.taskHits++
			ct.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, dispatchTestTaskJSON)
			return
		}
		ct.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, dispatchTestTaskJSON)
	}))
	t.Cleanup(ct.ts.Close)
	return ct
}

func (c *captureTarget) tasks() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.taskHits
}

func (c *captureTarget) lastBody() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		return nil
	}
	return c.bodies[len(c.bodies)-1]
}

// runBareDispatchAgainstFake 以远程模式（--target fake-01）执行 dispatch，
// 假目标机的 /api/status 返回 statusBody。返回 stdout、stderr 与错误。
func runBareDispatchAgainstFake(t *testing.T, statusBody string, extraArgs ...string) (string, string, *captureTarget, error) {
	t.Helper()
	ct := newCaptureTarget(t, statusBody)
	addr := strings.TrimPrefix(ct.ts.URL, "http://")
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:7777\"\ntoken: \""+testToken+"\"\n"+
		"targets:\n  fake-01:\n    addr: \""+addr+"\"\n    token: \""+testToken+"\"\n")
	resetFlags(t)
	configPath = cfgPath
	targetName = "fake-01"
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	t.Cleanup(func() {
		dispatchNoTerminal = false
		dispatchAllowDirty = false
		dispatchNoSyncCheck = false
	})

	// --no-sync-check：测试进程的 cwd 是本仓工作树，基线校验那一段与本卡无关，
	// 关掉后判据只落在纪律正文与请求计数上（与既有远程派发用例同款取舍）。
	args := append([]string{"dispatch", "--project", "proj1", "--prompt", "x", "--no-terminal",
		"--no-sync-check"}, extraArgs...)
	rootCmd.SetArgs(args)
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := Execute()
	return out.String(), errBuf.String(), ct, err
}

// TestBareDispatchCarriesAssembledDiscipline 未点名裸派发也注入平台层正文
// （实现决定 1 / §3.1）：wire 上有 discipline_text（含平台不变量标记）与
// discipline_version=0。
func TestBareDispatchCarriesAssembledDiscipline(t *testing.T) {
	out, _, ct, err := runBareDispatchAgainstFake(t, `{"disciplines_supported":true}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out, "task-abc123") {
		t.Fatalf("stdout 应仍是单行任务 JSON，得到 %q", out)
	}
	body := ct.lastBody()
	if body == nil {
		t.Fatal("假目标机没收到派发请求")
	}
	text, _ := body["discipline_text"].(string)
	if !strings.Contains(text, "平台不变量") || !strings.Contains(text, "收口前逐条自查") {
		t.Fatalf("wire 的 discipline_text 应是平台层组装产物（头部+尾部），实得前 80 字节: %q",
			truncateBytesForTest(text, 80))
	}
	if v, ok := body["discipline_version"]; !ok || v != float64(0) {
		t.Fatalf("wire discipline_version = %v（present=%v），want 0", v, ok)
	}
}

// TestBareDispatchRefusesUnsupportedTarget 三态能力位缺席/false 都拒发：
// 错误文案可行动（升级指引），目标机零任务请求。
func TestBareDispatchRefusesUnsupportedTarget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusBody string
	}{
		{"能力位缺席", `{}`},
		{"能力位false", `{"disciplines_supported":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, ct, err := runBareDispatchAgainstFake(t, tc.statusBody)
			if err == nil {
				t.Fatal("目标机不支持时必须拒发")
			}
			joined := err.Error() + errOut
			if !strings.Contains(joined, "升级") {
				t.Fatalf("拒发文案应含可行动的升级指引: %v / %s", err, errOut)
			}
			if n := ct.tasks(); n != 0 {
				t.Fatalf("拒发后目标机不应收到任何任务请求，实际 %d 次", n)
			}
		})
	}
}

// TestBareDispatchProbeFailureDoesNotClaimUnsupported 探活返回非 JSON 时，裸派发
// 必须保留真实 cause；这与能力位缺席的「升级」拒发是两条不同的用户处置路径。
func TestBareDispatchProbeFailureDoesNotClaimUnsupported(t *testing.T) {
	_, errOut, target, err := runBareDispatchAgainstFake(t, "not-json")
	if err == nil {
		t.Fatal("Status 失败时裸派发必须返回错误")
	}
	joined := err.Error() + errOut
	if !strings.Contains(joined, "探活失败") {
		t.Fatalf("错误必须说明探活失败：%s", joined)
	}
	if !strings.Contains(joined, "invalid character") {
		t.Fatalf("错误必须保留 Status cause：%s", joined)
	}
	if strings.Contains(joined, "升级到同批版本") {
		t.Fatalf("探活失败不得归因成版本升级：%s", joined)
	}
	if n := target.tasks(); n != 0 {
		t.Fatalf("探活失败不得发送任务，实际 %d 次", n)
	}
}

// TestBareDispatchDisciplineFileRawText P1(a)：--discipline-file 读文件作
// RawText 直通——正文含文件原文与平台层标记，不落库所以版本记 0。
func TestBareDispatchDisciplineFileRawText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ad-hoc.md")
	if err := os.WriteFile(path, []byte("临时捏一份的纪律正文RAWTEXT"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, ct, err := runBareDispatchAgainstFake(t, `{"disciplines_supported":true}`,
		"--discipline-file", path)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	body := ct.lastBody()
	text, _ := body["discipline_text"].(string)
	if !strings.Contains(text, "临时捏一份的纪律正文RAWTEXT") || !strings.Contains(text, "平台不变量") {
		t.Fatalf("RawText 应与平台层组装后直通下发，实得前 100 字节: %q", truncateBytesForTest(text, 100))
	}
	if body["discipline_version"] != float64(0) {
		t.Fatalf("RawText 直通的版本应记 0，实得 %v", body["discipline_version"])
	}
}

// TestBareDispatchDisciplineFileMissingActionable --discipline-file 文件不存在时
// 给可行动错误，且不发任何请求。
func TestBareDispatchDisciplineFileMissingActionable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.md")
	_, _, ct, err := runBareDispatchAgainstFake(t, `{"disciplines_supported":true}`,
		"--discipline-file", missing)
	if err == nil {
		t.Fatal("文件不存在时应报错")
	}
	joined := err.Error()
	if !strings.Contains(joined, "--discipline-file") || !strings.Contains(joined, "nope.md") {
		t.Fatalf("错误应点名 flag 与路径: %v", err)
	}
	if n := ct.tasks(); n != 0 {
		t.Fatalf("读不到文件就不该发出任何请求，实际 %d 次", n)
	}
}

func truncateBytesForTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
