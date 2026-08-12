// handoff done 的 CLI 行为测试：note 透传、缺说明提醒、超长拒发、旧 agentd 告警。
package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// runDone 执行一次 done 命令，**分开**返回 stdout 与 stderr。
// 为什么必须分开：本命令的契约正是「stdout 恒为 {"ok":true}，人读的信息走
// stderr」，合并到一个 buffer 就测不出这条契约了。
func runDone(t *testing.T, cfgPath, agentdURL string, extra ...string) (string, string, error) {
	t.Helper()
	resetFlags(t)
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	args := append([]string{"done", "task-1", "--config", cfgPath, "--agentd", agentdURL}, extra...)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		doneNote = ""
	})
	err := rootCmd.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

// TestDoneNotePassedThrough 断言 --note 进了请求体，且 stdout 仍是单行 {"ok":true}。
func TestDoneNotePassedThrough(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"ok":true,"note_saved":true}`))
	}))
	t.Cleanup(ts.Close)

	out, _, err := runDone(t, writeStatusConfig(t), ts.URL, "--note", "改完了登录页")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, `"note":"改完了登录页"`) {
		t.Fatalf("note 没发出去: %s", gotBody)
	}
	if strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("stdout 契约被破坏: %q", out)
	}
}

// TestDoneWithoutNoteWarnsOnStderr 断言缺说明时 stderr 有提醒，而 stdout 不变。
// 「可选且完全没有反馈」的字段等于永远为空，这条提醒是本功能唯一的轻推。
func TestDoneWithoutNoteWarnsOnStderr(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"note_saved":false}`))
	}))
	t.Cleanup(ts.Close)

	out, errOut, err := runDone(t, writeStatusConfig(t), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "未留说明") {
		t.Fatalf("stderr 缺少缺说明提醒: %q", errOut)
	}
	if strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("stdout 契约被破坏: %q", out)
	}
}

// TestDoneOversizeNoteRejectedBeforeRequest 断言超长说明在**发请求之前**就报错。
// 为什么要断言"没发出去"：这是本地可判的错误，让它先打一趟网络再被 400 拒回，
// 等于把一次确定的失败变成一次依赖对端版本的失败。
func TestDoneOversizeNoteRejectedBeforeRequest(t *testing.T) {
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Write([]byte(`{"ok":true,"note_saved":true}`))
	}))
	t.Cleanup(ts.Close)

	_, _, err := runDone(t, writeStatusConfig(t), ts.URL,
		"--note", strings.Repeat("a", proto.MaxDoneNoteBytes+1))
	if err == nil {
		t.Fatal("超长说明应报错")
	}
	if hit {
		t.Fatal("超长说明不该发起请求")
	}
}

// TestDoneOldAgentdWarnsButSucceeds 断言旧 agentd 丢了说明时告警、但退出码为 0：
// 任务确实已归档，失败的是说明保存，不是归档本身。
func TestDoneOldAgentdWarnsButSucceeds(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`)) // 旧 agentd：无 note_saved 字段
	}))
	t.Cleanup(ts.Close)

	out, errOut, err := runDone(t, writeStatusConfig(t), ts.URL, "--note", "改完了")
	if err != nil {
		t.Fatalf("归档成功时不该报错: %v", err)
	}
	if !strings.Contains(errOut, "版本较旧") {
		t.Fatalf("stderr 缺少旧版告警: %q", errOut)
	}
	if strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("stdout 契约被破坏: %q", out)
	}
}
