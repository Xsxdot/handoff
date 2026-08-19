// render_golden_test.go —— claude 的 render.log 黄金基线。
//
// 职责：把 testdata/turn_success.jsonl 按生产路径喂进映射函数，断言落盘的
// render.log 与 testdata/render_golden.txt 逐字节相等
//
// 边界：
//   - 只比对 render.log；事件映射与回合文本各有其测试
//   - 不走 tailer（那是 stream_test.go 的事），直接按行喂映射函数——本测试
//     要盯的是「消息 → render.log」这一段，不是文件尾随
//
// 为什么 claude 需要新建回放而另两家不用：现有的 stream_test.go 只验证 tailer
// 能解析样本，映射层（mapStreamEvent / mapAssistant / mapUserMessage）在测试里
// 从未跑到过 render 落盘——而那正是 W4a 要动的地方。
package claudecode

import (
	"bufio"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden 用 -update 重录基线。只有刻意变更 render 行为时才可以用它。
var updateGolden = flag.Bool("update", false, "重录 render.log golden 基线")

func TestRenderGolden(t *testing.T) {
	src, err := os.Open("testdata/turn_success.jsonl")
	if err != nil {
		t.Fatalf("读 testdata: %v", err)
	}
	defer src.Close()

	taskDir := t.TempDir()
	a := New(slog.Default())
	r := a.newRun("render-golden", taskDir, t.TempDir())

	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m streamMsg
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		// 按 Run 里的真实分派复现：三类消息各自的映射入口
		switch m.Type {
		case "stream_event":
			a.mapStreamEvent(r, m.Event)
		case "assistant":
			a.mapAssistant(r, m.Message)
		case "user":
			a.mapUserMessage(r, m.Message)
		}
	}

	raw, err := os.ReadFile(filepath.Join(taskDir, renderFileName))
	if err != nil {
		// 样本里一条 render 内容都没有时文件不存在——那说明这个基线盖不住
		// 任何东西，是配置问题而不是通过
		t.Fatalf("读 render.log（样本未产生任何 render 内容？）: %v", err)
	}

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
	if string(raw) != string(want) {
		t.Errorf("render.log 与基线不符——W4a 不该改动它。\n基线 %d 字节:\n%q\n实得 %d 字节:\n%q",
			len(want), string(want), len(raw), string(raw))
	}
}
