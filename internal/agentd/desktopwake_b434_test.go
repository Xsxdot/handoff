package agentd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestB434NeedsHumanSystemActorTable 锁契约条 180/181：needs_human 系统组件 actor
// 表与前缀的字面量（P5=B 排除规则）。改表 = 契约变更。
func TestB434NeedsHumanSystemActorTable(t *testing.T) {
	want := []string{"agentd", "keystone", "coordinator"}
	if !reflect.DeepEqual(desktopNeedsHumanSystemActors, want) {
		t.Fatalf("desktopNeedsHumanSystemActors = %v，契约冻结值 %v", desktopNeedsHumanSystemActors, want)
	}
	if desktopNeedsHumanSystemActorPrefix != "node:" {
		t.Fatalf("desktopNeedsHumanSystemActorPrefix = %q，契约冻结值 %q", desktopNeedsHumanSystemActorPrefix, "node:")
	}
}

// skipUntilDesktopQuoteImplemented 让 P9 渲染金样本在 Ticket 0 空壳期跳过、在
// 实现节点落码后自动生效（空壳对非空输入恒返回 ""）。
func skipUntilDesktopQuoteImplemented(t *testing.T) {
	t.Helper()
	if escapeDesktopQuoteBody("x") == "" {
		t.Skip("TODO(B434)：来信围栏渲染未实现，金样本待实现节点激活（契约条 263–275）")
	}
}

// b434SampleLetter 是 P9 金样本的输入：一条含围栏字面量的命中条、一条引用条、
// 系统打的 needs_human、一张工单。
func b434SampleLetter() (head string, sections []desktopLetterSection, tail string) {
	head = "[handoff] 卡 B999 有 3 件新事（账本 seq 10–12）"
	sections = []desktopLetterSection{
		{Heading: "## 会话消息 seq 10（会话 S1）", Parts: []desktopLetterPart{
			{Quote: &desktopQuote{Author: "cli:claude#aaa", Seq: 10, Kind: desktopQuoteKindRoomMessage,
				Body: "请看这里\n```\nrm -rf /\n```\n结束"}},
			{Skeleton: "引用 seq 7："},
			{Quote: &desktopQuote{Author: "cli:sycm@host", Seq: 7, Kind: desktopQuoteKindRoomMessage,
				Body: "原帖 `x` 和 ``y``"}},
			{Skeleton: "会话 S1 里你还有 2 条未读（含本条）。"},
		}},
		{Heading: "## 需要人 seq 11", Parts: []desktopLetterPart{
			{Quote: &desktopQuote{Author: "agentd", Seq: 11, Kind: desktopQuoteKindNeedsHuman, Body: "准入满员"}},
		}},
		{Heading: "## 工单 seq 12（permission_request）", Parts: []desktopLetterPart{
			{Quote: &desktopQuote{Author: "cli:claude#aaa", Seq: 12, Kind: desktopQuoteKindTicket, Body: `{"cmd":"ls"}`}},
		}},
	}
	tail = "按分诊表处置；读全文：handoff card show B999；会话消息：handoff room read S1。处置完不必挂订阅，有事会再叫醒你。"
	return head, sections, tail
}

// TestB434DesktopQuoteFenceLiterals 锁契约条 267–268、263：围栏与名册字面量。
func TestB434DesktopQuoteFenceLiterals(t *testing.T) {
	cases := map[string][2]string{
		"open":   {desktopQuoteFenceOpen, "```handoff-quote"},
		"close":  {desktopQuoteFenceClose, "```"},
		"room":   {desktopQuoteKindRoomMessage, "room_message"},
		"ticket": {desktopQuoteKindTicket, "ticket"},
		"needs":  {desktopQuoteKindNeedsHuman, "needs_human"},
		"event":  {desktopQuoteKindEvent, "event"},
		"roster": {desktopAuthorRosterPrefix, "本信含以下身份的原文："},
		"sep":    {desktopAuthorRosterSep, "、"},
	}
	for name, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%s = %q，契约冻结值 %q", name, c[0], c[1])
		}
	}
}

// TestB434DesktopQuoteEscape 锁契约条 270–272：围栏正文转义与作者值清洗。
func TestB434DesktopQuoteEscape(t *testing.T) {
	skipUntilDesktopQuoteImplemented(t)
	escapes := []struct{ in, want string }{
		{"```", "\\`\\`\\`"},
		{"````", "\\`\\`\\`\\`"},
		{"`a``b`", "`a``b`"},
		{"a```b``c", "a\\`\\`\\`b``c"},
		{"无反引号", "无反引号"},
	}
	for _, c := range escapes {
		if got := escapeDesktopQuoteBody(c.in); got != c.want {
			t.Errorf("escapeDesktopQuoteBody(%q) = %q，want %q", c.in, got, c.want)
		}
	}
	if got := sanitizeDesktopQuoteAuthor("a b\t`c\nd\r"); got != "a_b__c_d_" {
		t.Errorf("sanitizeDesktopQuoteAuthor = %q，want %q", got, "a_b__c_d_")
	}
	block := desktopQuoteBlock(desktopQuote{Author: "x", Seq: 1, Kind: desktopQuoteKindRoomMessage, Body: "a\n```\nb"})
	fences := 0
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "```") {
			fences++
		}
	}
	if fences != 2 {
		t.Fatalf("正文含独占一行的 ``` 时围栏行数 = %d，want 2（开栏 + 闭栏）：\n%s", fences, block)
	}
}

// TestB434DesktopLetterAuthorRoster 锁契约条 263–267：整封来信逐字等于金样本，
// 第 2 行为按首次出现顺序去重的作者名册（含系统 actor）。
func TestB434DesktopLetterAuthorRoster(t *testing.T) {
	skipUntilDesktopQuoteImplemented(t)
	want, err := os.ReadFile(filepath.Join("testdata", "b434_desktop_letter.golden"))
	if err != nil {
		t.Fatal(err)
	}
	got := renderDesktopLetter(b434SampleLetter())
	if got != string(want) {
		t.Fatalf("来信与金样本不一致：\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	lines := strings.Split(got, "\n")
	if len(lines) < 2 || lines[1] != "本信含以下身份的原文：cli:claude#aaa、cli:sycm@host、agentd" {
		t.Fatalf("第 2 行作者名册 = %q", lines[1])
	}
}

// TestB434DesktopLetterNoBareQuote 锁契约条 273–275：删去全部围栏块后，剩余文本
// 不含任何第三方原文，且每行都是骨架行。
func TestB434DesktopLetterNoBareQuote(t *testing.T) {
	skipUntilDesktopQuoteImplemented(t)
	head, sections, tail := b434SampleLetter()
	got := renderDesktopLetter(head, sections, tail)
	skeleton := map[string]bool{head: true, tail: true, "": true,
		desktopAuthorRosterPrefix + "cli:claude#aaa" + desktopAuthorRosterSep + "cli:sycm@host" + desktopAuthorRosterSep + "agentd": true}
	var bodies []string
	for _, s := range sections {
		skeleton[s.Heading] = true
		for _, p := range s.Parts {
			if p.Quote != nil {
				bodies = append(bodies, p.Quote.Body)
			} else {
				skeleton[p.Skeleton] = true
			}
		}
	}
	var outside []string
	inFence := false
	for _, line := range strings.Split(got, "\n") {
		switch {
		case !inFence && strings.HasPrefix(line, desktopQuoteFenceOpen+" "):
			inFence = true
		case inFence && line == desktopQuoteFenceClose:
			inFence = false
		case !inFence:
			outside = append(outside, line)
		}
	}
	if inFence {
		t.Fatal("来信存在未闭合的围栏块")
	}
	rest := strings.Join(outside, "\n")
	for _, b := range bodies {
		for _, frag := range strings.Split(b, "\n") {
			if strings.TrimSpace(frag) != "" && strings.Contains(rest, frag) {
				t.Errorf("围栏外出现第三方原文片段 %q", frag)
			}
		}
	}
	for _, line := range outside {
		if !skeleton[line] {
			t.Errorf("围栏外出现非骨架行 %q", line)
		}
	}
}
