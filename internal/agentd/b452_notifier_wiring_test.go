package agentd

// b452_notifier_wiring_test.go —— 修复轮第 5 条：SetManager 挂接的编排实现若不支持终态回调接线，
// 启动时必须打一行 Error 说清后果（只读任务自动归档后不归还名额），而不是静默。

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/store"
)

// b452NoNotifierMgr 只有 OrchestrationClient 的方法集，没有 SetTerminalNotifier。
type b452NoNotifierMgr struct{ orchestration.OrchestrationClient }

func b452Store(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(looseTempDir(t), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestB452SetManagerWithoutNotifierLogsErrorOnce(t *testing.T) {
	logs := &b448LogCapture{}
	srv := NewServer(&config.Config{Token: testToken, DataDir: t.TempDir()}, b452Store(t), slog.New(logs.handler()))
	srv.SetManager(&b452NoNotifierMgr{})
	var hits []b448LogRecord
	for _, r := range logs.find("") {
		if r.level == slog.LevelError && strings.Contains(r.msg, "终态回调") {
			hits = append(hits, r)
		}
	}
	if len(hits) != 1 || !strings.Contains(hits[0].msg, "名额") || !strings.Contains(hits[0].msg, "只读") {
		t.Fatalf("应恰一行 Error 点名后果（只读任务自动归档不归还名额），得 %+v", hits)
	}

	// 真实编排实现支持接线：不得打这行 Error
	logs2 := &b448LogCapture{}
	srv2 := NewServer(&config.Config{Token: testToken, DataDir: t.TempDir()}, b452Store(t), slog.New(logs2.handler()))
	newManagerForServer(t, srv2, nil)
	for _, r := range logs2.find("终态回调") {
		if r.level == slog.LevelError {
			t.Fatalf("真实编排实现不应报接线失败: %+v", r)
		}
	}
}
