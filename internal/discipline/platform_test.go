package discipline

import (
	"strings"
	"testing"
)

func TestComposeEnabledKeepsHeadBaseTailOrderAndSources(t *testing.T) {
	base := Block{Text: "角色纪律正文", Source: "配置:charter-plan"}
	got := Compose(base, true)

	if got.Source != "内置:平台不变量 + 配置:charter-plan" {
		t.Fatalf("Source = %q", got.Source)
	}
	head := "# 平台不变量（恒在层）"
	tail := "收口前逐条自查："
	if strings.Count(got.Text, head) != 1 {
		t.Fatalf("平台头部出现次数 = %d，want 1", strings.Count(got.Text, head))
	}
	if strings.Count(got.Text, "角色纪律正文") != 1 {
		t.Fatalf("base 正文出现次数 = %d，want 1", strings.Count(got.Text, "角色纪律正文"))
	}
	if strings.Count(got.Text, tail) != 1 {
		t.Fatalf("平台尾部出现次数 = %d，want 1", strings.Count(got.Text, tail))
	}
	if strings.Contains(got.Text, "handoff graph") {
		t.Fatal("平台正文不得提供 handoff graph 执行入口")
	}
	if !strings.Contains(got.Text, "go run github.com/Xsxdot/charter/graph/cmd/codegraph") {
		t.Fatal("平台正文缺少 canonical codegraph 查询入口")
	}
	if !(strings.Index(got.Text, head) < strings.Index(got.Text, "角色纪律正文") &&
		strings.Index(got.Text, "角色纪律正文") < strings.Index(got.Text, tail)) {
		t.Fatalf("正文顺序错误：%q", got.Text)
	}
}

func TestComposeEnabledWithEmptyBaseStillInjectsPlatformLayer(t *testing.T) {
	got := Compose(Block{}, true)
	if got.Source != "内置:平台不变量" {
		t.Fatalf("Source = %q", got.Source)
	}
	if !strings.Contains(got.Text, "# 平台不变量（恒在层）") {
		t.Fatal("空 base 时缺平台头部")
	}
	if !strings.Contains(got.Text, "收口前逐条自查：") {
		t.Fatal("空 base 时缺平台尾部自查")
	}
}

// B229.7：落台账要求已移出平台层（spec 第 80 行），由角色层只对产出型角色承载。
// 平台层正文出现「台账」即视为有人把该条加了回来，必须红。
func TestComposeEnabledWithEmptyBaseOmitsLedgerFromPlatformLayer(t *testing.T) {
	got := Compose(Block{}, true)
	if n := strings.Count(got.Text, "台账"); n != 0 {
		t.Fatalf("平台层组装产出含「台账」%d 次，落台账要求应由角色层承载：%q", n, got.Text)
	}
}

func TestComposeDisabledPreservesBaseAndLeavesAuditSource(t *testing.T) {
	base := Block{Text: "角色纪律正文\n", Source: "内置:review"}
	got := Compose(base, false)
	if got.Text != base.Text {
		t.Fatalf("关闭平台层后 Text = %q，want 原 base %q", got.Text, base.Text)
	}
	if got.Source != "平台不变量已关闭 + 内置:review" {
		t.Fatalf("关闭平台层后的 Source = %q", got.Source)
	}
	if strings.Contains(got.Text, "# 平台不变量（恒在层）") ||
		strings.Contains(got.Text, "收口前逐条自查：") {
		t.Fatal("关闭平台层后仍注入平台正文")
	}
}

func TestComposeDisabledWithEmptyBaseHasOnlyAuditSource(t *testing.T) {
	got := Compose(Block{}, false)
	if got.Text != "" {
		t.Fatalf("空 base 且关闭平台层后 Text = %q", got.Text)
	}
	if got.Source != "平台不变量已关闭" {
		t.Fatalf("空 base 且关闭平台层后的 Source = %q", got.Source)
	}
}
