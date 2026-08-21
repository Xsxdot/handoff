package ledgerstep

import "testing"

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
