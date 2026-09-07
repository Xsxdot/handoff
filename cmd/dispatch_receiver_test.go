package cmd

// B233.5 T6 接缝测试：CLI --receiver 进入统一接收者字段，全局 --target 只负责拨号。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runReceiverDispatchCapture(t *testing.T, target string, extraArgs ...string) (map[string]any, string, error) {
	t.Helper()
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"disciplines_supported":true}`)
		case "/api/tasks":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode dispatch body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, dispatchTestTaskJSON)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	configText := "listen: \"" + addr + "\"\ntoken: \"" + testToken + "\"\n"
	if target != "" {
		configText += "targets:\n  " + target + ":\n    addr: \"" + addr + "\"\n    token: \"" + testToken + "\"\n"
	}
	resetFlags(t)
	configPath = writeTestConfig(t, configText)
	targetName = target
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	args := []string{"dispatch", "--project", "proj1", "--prompt", "x", "--no-terminal"}
	if target != "" {
		args = append(args, "--no-sync-check")
	}
	args = append(args, extraArgs...)
	rootCmd.SetArgs(args)
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := Execute()
	return got, out.String(), err
}

func TestDispatchReceiverBodyAndTargetNotOverlay(t *testing.T) {
	t.Run("空 receiver 不发键", func(t *testing.T) {
		got, _, err := runReceiverDispatchCapture(t, "")
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if _, ok := got["receiver"]; ok {
			t.Fatalf("未指定 receiver 时 body 不得包含 receiver: %v", got)
		}
	})
	t.Run("显式 receiver", func(t *testing.T) {
		got, _, err := runReceiverDispatchCapture(t, "", "--receiver", "muse", "--executor", "grok")
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if got["receiver"] != "muse" {
			t.Fatalf("receiver=%v, want muse; body=%v", got["receiver"], got)
		}
		if got["executor"] != "grok" {
			t.Fatalf("executor=%v, want grok; body=%v", got["executor"], got)
		}
	})
	t.Run("全局 target 不写入执行 overlay", func(t *testing.T) {
		got, _, err := runReceiverDispatchCapture(t, "linux-01")
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if target, ok := got["target"]; ok && target != "" {
			t.Fatalf("全局 target 不得作为执行 overlay，body target=%v", target)
		}
	})
}

func TestDispatchHelpListsReceiver(t *testing.T) {
	flag := dispatchCmd.Flags().Lookup("receiver")
	if flag == nil {
		t.Fatal("dispatch 缺少 --receiver flag")
	}
	if !strings.Contains(flag.Usage, "默认载体") {
		t.Fatalf("--receiver 帮助应说明默认载体，usage=%q", flag.Usage)
	}
}
