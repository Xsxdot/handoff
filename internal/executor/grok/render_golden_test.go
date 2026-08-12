// render_golden_test.go —— grok 的 render 产物黄金基线。
//
// 职责：把 testdata/updates.jsonl 喂进 turnAccumulator，断言 render 那一股的
// 产物与 testdata/render_golden.txt 逐字节相等
//
// 边界：
//   - 不断言「render 里没有思维链」——grok 今天**就是**把思维链写进 render 的
//     （adapter.go 的 renderBuf 显式收「正文 + 推理 + 工具动作」）。拿「不含」
//     去断言会把正确的现状判成失败
//   - 不解释内容，只做字节比对
//
// 为什么是逐字节而不是「包含若干关键字」：W4a 要在这个分流点上多接一路帧，
// 而「多接一路」最容易的失手方式就是顺手把这一股也改了。关键字断言容忍空格、
// 顺序、前后缀的漂移；字节比对不容忍。这正是本文件唯一的作用。
package grok_test

import (
	"bufio"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

// updateGolden 用 -update 重录基线。**只有在刻意变更 render 行为时才可以用它**。
var updateGolden = flag.Bool("update", false, "重录 render golden 基线")

func TestRenderGolden(t *testing.T) {
	f, err := os.Open("testdata/updates.jsonl")
	if err != nil {
		t.Fatalf("读 testdata: %v", err)
	}
	defer f.Close()

	h := grok.NewTurnAccumulatorForTest()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			h.FeedRawForTest([]byte(line))
		}
	}
	got := h.RenderTextForTest()

	path := filepath.Join("testdata", "render_golden.txt")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("写 golden: %v", err)
		}
		t.Logf("已重录 golden（%d 字节）", len(got))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 golden（首次请用 -update 生成）: %v", err)
	}
	if got != string(want) {
		t.Errorf("render 产物与基线不符——W4a 不该改动这一股。\n基线 %d 字节:\n%q\n实得 %d 字节:\n%q",
			len(want), string(want), len(got), got)
	}
}
