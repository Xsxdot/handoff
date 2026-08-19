// render_golden_test.go —— opencode 的 render.log 黄金基线。
//
// 职责：用既有的 spike5 抓包回放一整轮，断言落盘的 render.log 与
// testdata/render_golden.txt 逐字节相等
//
// 边界：只比对 render.log 的字节，不断言事件、不断言回合文本（那些各有其测试）
//
// 为什么复用 spike5 而不是 spike3：spike5 是完整一轮（权限 → 应答 → 模型输出
// → idle），render.log 里能同时出现文本与工具动作两类内容；spike3 只到权限就
// 停了，盖不住后半段。
package opencode

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// updateGolden 用 -update 重录基线。只有刻意变更 render 行为时才可以用它。
var updateGolden = flag.Bool("update", false, "重录 render.log golden 基线")

func TestRenderGolden(t *testing.T) {
	// startReplay 内部用 t.TempDir() 建 taskDir，这里需要拿到那个目录，
	// 所以复用它的做法而不是调它本身——见下面 renderGoldenReplay
	taskDir, ch := renderGoldenReplay(t)
	collectReplay(t, ch, 800*time.Millisecond)

	raw, err := os.ReadFile(filepath.Join(taskDir, renderLogFileName))
	if err != nil {
		t.Fatalf("读回放产生的 render.log: %v", err)
	}
	got := string(raw)

	path := filepath.Join("testdata", "render_golden.txt")
	if *updateGolden {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("写 golden: %v", err)
		}
		t.Logf("已重录 golden（%d 字节）", len(raw))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 golden（首次请用 -update 生成）: %v", err)
	}
	if got != string(want) {
		t.Errorf("render.log 与基线不符——W4a 不该改动它。\n基线 %d 字节:\n%q\n实得 %d 字节:\n%q",
			len(want), string(want), len(got), got)
	}
}

// renderGoldenReplay 与 startReplay 做同一件事，额外返回 taskDir，
// 好让本测试读到回放落盘的 render.log。
//
// 为什么不改 startReplay 的签名：它有四个既有调用方，为一个测试改公共
// helper 的形状是把成本摊给无关的用例。复制十行比那便宜。
func renderGoldenReplay(t *testing.T) (string, <-chan executor.AdapterEvent) {
	t.Helper()
	ts := replayServer(t, spike5)
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, promptFileName), []byte("plan"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	ad := New(slog.Default())
	ad.idleGrace = 20 * time.Millisecond
	taskID := "render-golden"
	req := executor.StartReq{
		Task:    proto.Task{ID: taskID, RepoPath: t.TempDir()},
		TaskDir: taskDir,
	}
	if _, err := ad.startRun(t.Context(), req, NewAPI(ts.URL, adapterTestPassword), &fakeProbe{alive: true}); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	t.Cleanup(func() { _ = ad.Stop(taskID) })
	return taskDir, ad.Events(taskID)
}
