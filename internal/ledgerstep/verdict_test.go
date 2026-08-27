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
