package turn

import (
	"strings"
	"testing"
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
