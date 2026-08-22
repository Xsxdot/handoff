package turn_test

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
)

func TestTruncateMarkedAppendsMarkerOnlyWhenTruncated(t *testing.T) {
	if got := turn.TruncateMarked("短", 10); got != "短" {
		t.Errorf("未超限不应加标记，得到 %q", got)
	}
	got := turn.TruncateMarked(strings.Repeat("字", 20), 5)
	if !strings.HasSuffix(got, executor.TruncationMarker) {
		t.Errorf("超限必须以截断标记收尾，得到 %q", got)
	}
	if r := []rune(strings.TrimSuffix(got, executor.TruncationMarker)); len(r) != 5 {
		t.Errorf("截断后正文应为 5 个 rune，得到 %d", len(r))
	}
}

func TestTailRunesKeepsSuffix(t *testing.T) {
	if got := turn.TailRunes("abcdef", 3); got != "def" {
		t.Errorf("TailRunes = %q，期望 def", got)
	}
	if got := turn.TailRunes("ab", 5); got != "ab" {
		t.Errorf("不足 n 时应原样返回，得到 %q", got)
	}
}

func TestFinalTextKeepsVerdictAtTail(t *testing.T) {
	verdict := "```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```"
	text := strings.Repeat("前文 ", turn.FinalTextLimit+10) + verdict
	got := turn.FinalText(text)
	if !strings.HasSuffix(got, verdict) {
		t.Fatalf("正文尾部窗口必须保全裁决块，尾部为 %q", turn.TailRunes(got, 120))
	}
	if len([]rune(got)) != turn.FinalTextLimit {
		t.Fatalf("正文窗口应为 %d rune，实际 %d", turn.FinalTextLimit, len([]rune(got)))
	}
}

// TestClampQuestionPointsAtRenderLog 钉住 ClampQuestion 与 TruncateMarked 的
// **语义差异**：question 的全文只在 render.log 里，截断后必须指路。
// opencode 的 regression_group_a_test.go 断言同一件事，这里是搬包后的同源钉子。
func TestClampQuestionPointsAtRenderLog(t *testing.T) {
	short := "很短的问题"
	if got := turn.ClampQuestion(short); got != short {
		t.Errorf("未超限不应改写，得到 %q", got)
	}

	long := strings.Repeat("长", turn.QuestionTextLimit+1000)
	got := turn.ClampQuestion(long)
	if n := len([]rune(got)); n > turn.QuestionTextLimit+200 {
		t.Errorf("截断后 %d 字符仍超限（上限 %d）", n, turn.QuestionTextLimit)
	}
	if !strings.Contains(got, "render.log") {
		t.Errorf("截断后必须指明全文去处，尾部为 %q", turn.TailRunes(got, 80))
	}
	// 反向断言：不得退化成 TruncateMarked 的通用尾缀——那会丢掉 render.log 指路，
	// 而 question 的全文不在工单里，协调者将无处可查（见 ClampQuestion 的注释）。
	if strings.HasSuffix(got, executor.TruncationMarker) {
		t.Error("ClampQuestion 不得复用 TruncateMarked 的尾缀")
	}
}
