package turn

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestHeadTailShortStringUntouched(t *testing.T) {
	out, truncated, orig := HeadTail("hello", 10, 10)
	if out != "hello" || truncated || orig != 5 {
		t.Fatalf("短串不该被动: out=%q truncated=%v orig=%d", out, truncated, orig)
	}
}

func TestHeadTailKeepsBothEnds(t *testing.T) {
	s := "HEAD" + strings.Repeat("x", 100) + "TAIL"
	out, truncated, orig := HeadTail(s, 4, 4)
	if !truncated {
		t.Fatal("应当报告已截断")
	}
	if orig != int64(len(s)) {
		t.Fatalf("orig 应为原始字节数 %d，实得 %d", len(s), orig)
	}
	if !strings.HasPrefix(out, "HEAD") {
		t.Fatalf("头部丢了: %q", out)
	}
	// 尾部是关键：报错与 stack trace 通常在尾部
	if !strings.HasSuffix(out, "TAIL") {
		t.Fatalf("尾部丢了: %q", out)
	}
	if !strings.Contains(out, "…（已截断）") {
		t.Fatalf("缺少截断标记: %q", out)
	}
}

// 多字节字符不能被切成半个：切在 UTF-8 码点中间会产生 U+FFFD，
// 前端拿到的就是一串乱码方块。
func TestHeadTailNeverSplitsRune(t *testing.T) {
	s := strings.Repeat("中", 100) // 每个 3 字节
	out, truncated, _ := HeadTail(s, 4, 4)
	if !truncated {
		t.Fatal("应当报告已截断")
	}
	if strings.ContainsRune(out, '�') {
		t.Fatalf("切出了半个字符: %q", out)
	}
}

// 头尾预算合起来已经覆盖全串时不该截断——否则会出现「截断后反而更长」。
func TestHeadTailNoTruncateWhenBudgetCovers(t *testing.T) {
	s := strings.Repeat("a", 20)
	out, truncated, _ := HeadTail(s, 10, 10)
	if truncated || out != s {
		t.Fatalf("预算刚好覆盖时不该截断: out=%q truncated=%v", out, truncated)
	}
}

func TestHeadTailRunes(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		head, tail int
		want       string
	}{
		{"不足预算原样返回", "abcdef", 3, 3, "abcdef"},
		{"刚好等于预算原样返回", "abcdef", 3, 3, "abcdef"},
		{"英文截断", "abcdefghij", 3, 2, "abc" + executor.TruncationMarker + "ij"},
		// 中文是本函数存在的理由：按字节切会切出半个字符
		{"中文按 rune 切不出乱码", "一二三四五六七八九十", 2, 2, "一二" + executor.TruncationMarker + "九十"},
		{"tail 为 0", "abcdef", 2, 0, "ab" + executor.TruncationMarker},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HeadTailRunes(c.in, c.head, c.tail); got != c.want {
				t.Errorf("HeadTailRunes(%q,%d,%d) = %q，期望 %q", c.in, c.head, c.tail, got, c.want)
			}
		})
	}
}

func TestFrameWriterTurnAccessor(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	if w.Turn() != 0 {
		t.Errorf("还没开回合时应为 0，实得 %d", w.Turn())
	}
	_ = w.BeginTurn("dispatch", "")
	if w.Turn() != 1 {
		t.Errorf("第一回合应为 1，实得 %d", w.Turn())
	}
	_ = w.BeginTurn("send", "")
	if w.Turn() != 2 {
		t.Errorf("第二回合应为 2，实得 %d", w.Turn())
	}
	var nilW *FrameWriter
	if nilW.Turn() != 0 {
		t.Error("nil 接收者应返回 0（全包的 nil 安全约定）")
	}
}
