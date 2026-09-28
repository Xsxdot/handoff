package ledgerstep

import (
	"strings"
	"testing"
)

func TestParseVerdict(t *testing.T) {
	pass := "审阅完成。\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```\n"
	v, err := ParseVerdict(pass)
	if err != nil || !v.Pass {
		t.Fatalf("pass: %v %+v", err, v)
	}
	fail := "有问题。\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[{\"severity\":\"major\",\"summary\":\"CAS 缺前值\",\"file\":\"a.go\"}],\"notes\":\"n\"}\n```"
	v, err = ParseVerdict(fail)
	if err != nil || v.Pass || len(v.Findings) != 1 || v.Findings[0].Severity != "major" {
		t.Fatalf("fail: %v %+v", err, v)
	}
	two := "示例：\n```handoff-verdict\n{\"verdict\":\"pass\"}\n```\n真裁决：\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```"
	if v, _ = ParseVerdict(two); v.Pass {
		t.Fatalf("应取最后一个 block: %+v", v)
	}
	for _, bad := range []string{
		"没有 block",
		"```handoff-verdict\n{broken\n```",
		"```handoff-verdict\n{\"verdict\":\"maybe\"}\n```",
	} {
		if _, err := ParseVerdict(bad); err == nil {
			t.Fatalf("应解析失败: %q", bad)
		}
	}
}

func TestParseVerdictSalvagesFirstVerdictWhenNotesIsBroken(t *testing.T) {
	fence := strings.Repeat(string(rune(96)), 3)
	message := "正文\n" + fence + "handoff-verdict\n" +
		"{\"verdict\":\"pass\",\"findings\":[{\"severity\":\"minor\",\"summary\":\"保留\"}],\"notes\":\"enabled\":true}\n" +
		fence + "\n"
	got, err := ParseVerdict(message)
	if err != nil {
		t.Fatalf("ParseVerdict() error = %v", err)
	}
	if !got.Pass || len(got.Findings) != 1 || got.Findings[0].Summary != "保留" {
		t.Fatalf("抢救结果 = %+v", got)
	}
	if got.Notes != "" {
		t.Fatalf("Notes = %q, want empty", got.Notes)
	}
	if !strings.Contains(got.Raw, "\"notes\":\"enabled\":true") {
		t.Fatalf("Raw 丢失损坏 notes: %q", got.Raw)
	}
	if !got.salvaged || !got.notesDropped || got.findingsDropped {
		t.Fatalf("抢救标记 = %+v", got)
	}
}

func TestParseVerdictUsesFirstVerdictNotNotesMention(t *testing.T) {
	fence := strings.Repeat(string(rune(96)), 3)
	message := fence + "handoff-verdict\n" +
		"{\"verdict\":\"fail\",\"findings\":[],\"notes\":\"bad \\\"verdict\\\":\\\"pass\\\"\":true}\n" +
		fence + "\n"
	got, err := ParseVerdict(message)
	if err != nil {
		t.Fatalf("ParseVerdict() error = %v", err)
	}
	if got.Pass {
		t.Fatalf("verdict 被 notes 引用覆盖: %+v", got)
	}
}

func TestParseVerdictStillRejectsMissingOrUnknownVerdict(t *testing.T) {
	fence := strings.Repeat(string(rune(96)), 3)
	for _, message := range []string{
		"没有裁决围栏",
		fence + "handoff-verdict\n{\"verdict\":\"maybe\"}\n" + fence + "\n",
	} {
		if _, err := ParseVerdict(message); err == nil {
			t.Fatalf("ParseVerdict(%q) unexpectedly succeeded", message)
		}
	}
}

// TestParseVerdictUnterminatedFenceWithTrailer 钉死 B416：opencode 部分模型
// （mimo-v2.6-flash 实测）漏写收尾围栏，verdict JSON 后直接接回合 trailer JSON。
// 报文里只有开盘围栏时，仍应取出裁决对象解析，而不是整轮转人工；后随的 trailer
// JSON 不得串味进裁决字段。
func TestParseVerdictUnterminatedFenceWithTrailer(t *testing.T) {
	fence := strings.Repeat(string(rune(96)), 3)
	message := fence + "handoff-verdict\n" +
		"{\"verdict\":\"pass\",\"findings\":[{\"severity\":\"minor\",\"summary\":\"保留\"}],\"notes\":\"n\"}\n" +
		"{\"branch\":\"cards/B412-charter-2\",\"commit\":\"de43cae8\",\"summary\":\"s\"}"
	got, err := ParseVerdict(message)
	if err != nil {
		t.Fatalf("ParseVerdict() error = %v", err)
	}
	if !got.Pass || len(got.Findings) != 1 || got.Findings[0].Summary != "保留" || got.Notes != "n" {
		t.Fatalf("抢救结果 = %+v", got)
	}
	if got.salvaged {
		t.Fatalf("第一段 JSON 合法，应走严格解析而非抢救，got %+v", got)
	}
}

// TestParseVerdictUnterminatedFenceStillFailClosed 钉死放宽的边界：缺收尾围栏
// 只放宽「取正文」这一步，verdict 仍不可辨认时必须继续报错，不猜 pass/fail。
func TestParseVerdictUnterminatedFenceStillFailClosed(t *testing.T) {
	fence := strings.Repeat(string(rune(96)), 3)
	for _, message := range []string{
		fence + "handoff-verdict\n{\"verdict\":\"maybe\"}",
		fence + "handoff-verdict\n{broken",
		fence + "handoff-verdict\n",
	} {
		if _, err := ParseVerdict(message); err == nil {
			t.Fatalf("ParseVerdict(%q) unexpectedly succeeded", message)
		}
	}
}
