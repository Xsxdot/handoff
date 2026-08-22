// newrun_test.go 钉住 runState 的**唯一构造点**。
//
// 为什么单独立一个文件：首发与冷恢复曾各搓一份 runState 字面量，于是每次给
// runState 加可见性字段，都只有首发那份被改到——frames 漏过一次、seg 又漏过
// 一次，两次都无声（两个字段对 nil 接收者都是空操作）。本文件的两条测试分别
// 罩「构造器把字段接对了」与「没人再绕开构造器」。
package grok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewRunWiresFramesAndSegmenter：构造器必须同时接上帧写入器与段切分器。
//
// 这两个字段的共同点是「漏了不报错」：FrameWriter 与 Segmenter 的方法对 nil
// 接收者都是空操作，所以漏接的表现不是崩溃，而是数据从此不再产生。
func TestNewRunWiresFramesAndSegmenter(t *testing.T) {
	a := New(nil)
	taskDir := t.TempDir()
	r := a.newRun("t1", taskDir, t.TempDir(), nil)
	if r.frames == nil {
		t.Fatal("frames 未接上：恢复后整轮不会有结构化帧，且全程无声")
	}
	if r.seg == nil {
		t.Fatal("seg 未接上：恢复后整轮不会有耗时账目，且全程无声")
	}
	// 正面断言配套：光断言「不是 nil」挡不住「接了个什么都不产出的空壳」——
	// nil 接收者恰恰就是那种空壳，两者的 != nil 判定并不能区分
	if err := r.frames.BeginTurn("test", ""); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(taskDir, "frames.jsonl"))
	if err != nil || !strings.Contains(string(b), "turn_start") {
		t.Errorf("帧写入器没真写出 turn_start 帧：err=%v 内容=%q", err, string(b))
	}
	entries := r.seg.BeginTurn(r.frames.Turn())
	if len(entries) == 0 {
		t.Error("段切分器没产出条目：耗时账目会是空的")
	}
}

// allowMarker 是绕开 newRun 的**正当出口**：确实不承载运行态的字面量
// （如冷恢复互斥的占位）在同一行标上它即可，不必为了过测试去改结构。
// 红线不给出口就会诱发绕过，那比没有红线更糟。
const allowMarker = "runstate-literal-ok"

// TestRunStateConstructionSitesStayCentralized：非测试源码里不得再出现
// 未标注的 &runState{ 字面量。
//
// 这是一条源码级不变式而非行为断言——因为失效的形状恰恰是「两处构造点分叉」，
// 它不体现在任何单次运行的行为里，只体现在源码结构上。
func TestRunStateConstructionSitesStayCentralized(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("列目录: %v", err)
	}
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("读 %s: %v", name, err)
		}
		inNewRun := false
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "func (a *Adapter) newRun(") {
				inNewRun = true
			} else if inNewRun && line == "}" {
				inNewRun = false
			}
			if !strings.Contains(line, "&runState{") {
				continue
			}
			if inNewRun || strings.Contains(line, allowMarker) {
				continue
			}
			offenders = append(offenders, name+":"+itoa(i+1)+" "+strings.TrimSpace(line))
		}
	}
	if len(offenders) > 0 {
		t.Errorf("runState 只许在 newRun 里构造（确不承载运行态的写 %q 放行）；越界处:\n  %s",
			allowMarker, strings.Join(offenders, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
