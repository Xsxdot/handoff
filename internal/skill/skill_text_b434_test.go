// skill_text_b434_test.go —— skills/handoff/SKILL.md 的来信授权规则文本（B434 契约条 277、278；
// B435 X 删桌面端坐席与拉起器两支并加退役反面断言，plan b435.5 段 6）。
//
// 职责：读仓内 SKILL.md（与 main.go //go:embed 的是同一文件）按节断言；授权规则文案只从
// DesktopLetterAuthorizationRule 取，不在测试里另抄一份（改字 = 契约变更，两处同红）。
// 边界：只锁文本在位；授权的真实效果（模型是否照做）归 §6 真机清单。
package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readHandoffSkill 读仓库根下 skills/handoff/SKILL.md（测试在 internal/skill 跑，根在上两级）。
func readHandoffSkill(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "..", "..", "skills", "handoff", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// skillSection 返回以 headingPrefix 开头的标题行起、到下一个 #/##/### 标题之前的正文；
// 围栏代码块内的行不当标题（片段里可能有 # 开头的行）。找不到即 Fatal。
func skillSection(t *testing.T, text, headingPrefix string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, headingPrefix) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("SKILL.md 缺少以 %q 开头的标题", headingPrefix)
	}
	end := len(lines)
	inFence := false
	for i := start + 1; i < len(lines); i++ {
		ln := lines[i]
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
			continue
		}
		if !inFence && (strings.HasPrefix(ln, "# ") || strings.HasPrefix(ln, "## ") || strings.HasPrefix(ln, "### ")) {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// fencedBlockAfter 返回 section 里 marker 之后第一个围栏代码块的内容（不含开闭栏行）。
func fencedBlockAfter(t *testing.T, section, marker string) string {
	t.Helper()
	i := strings.Index(section, marker)
	if i < 0 {
		t.Fatalf("节内缺少标记 %q", marker)
	}
	var body []string
	in := false
	for _, ln := range strings.Split(section[i:], "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			if in {
				return strings.Join(body, "\n")
			}
			in = true
			continue
		}
		if in {
			body = append(body, ln)
		}
	}
	t.Fatalf("标记 %q 之后没有闭合的围栏代码块", marker)
	return ""
}

// 条 277（B435 契约 §7「改写」、O2 plan Q4 甲）：规则的承载节是「组织者」节。测试名沿用；
// 「占座」节的 B434 引用块已随 B435 X 删除（plan b435.5 Q4 甲：保节、删 B434 段落）。
func TestB434SkillSeatHasAuthorizationRule(t *testing.T) {
	text := readHandoffSkill(t)
	if sec := skillSection(t, text, "### 组织者"); !strings.Contains(sec, DesktopLetterAuthorizationRule) {
		t.Fatal("「组织者」节必须逐字含 DesktopLetterAuthorizationRule")
	}
}

// 条 278：「主 agent 回路」的 CLAUDE.md 常设授权片段（围栏块）含授权规则逐字与 [handoff] 标记；B435 O2
// 起另含门铃取信句：照信第二行跑 `handoff session wait`，且带 `--timeout`（plan N1：裸命令在两封门铃
// 排队时会无限期阻塞）。
func TestB434SkillClaudeMDSnippetHasAuthorizationRule(t *testing.T) {
	sec := skillSection(t, readHandoffSkill(t), "### 主 agent 回路")
	block := fencedBlockAfter(t, sec, "CLAUDE.md 常设授权片段")
	// 围栏错配（如闭栏被吞）时块会越过后续标题、吞进「组织者」节的那份规则而假绿：块内不得出现标题行。
	for _, ln := range strings.Split(block, "\n") {
		if strings.HasPrefix(ln, "#") {
			t.Fatalf("CLAUDE.md 常设授权片段的围栏块越过了标题 %q（围栏未正确闭合）", ln)
		}
	}
	if !strings.Contains(block, DesktopLetterAuthorizationRule) {
		t.Fatalf("CLAUDE.md 常设授权片段必须逐字含授权规则，得:\n%s", block)
	}
	if !strings.Contains(block, "[handoff]") {
		t.Fatalf("常设授权片段应点名 [handoff] 来信标记，得:\n%s", block)
	}
	if !strings.Contains(block, OrganizerDoorbellTakeRule) {
		t.Fatalf("常设授权片段应逐字含门铃取信句 OrganizerDoorbellTakeRule，得:\n%s", block)
	}
	// B435 X（O2 Minor-5）：拉起器与卡级来信退役后，片段不得再把 [handoff] 消息称作「正式请求」，
	// 也不得保留拉起器代开与卡级来信两句。
	for _, gone := range []string{"正式请求", "请替我开一场协调者会话", "卡上的来信"} {
		if strings.Contains(block, gone) {
			t.Fatalf("常设授权片段不得再含 %q，得:\n%s", gone, block)
		}
	}
}

// B435 X（契约 §7、plan b435.5 段 6）：SKILL 全文不得再教 B434 退役面。现行 `session sit … --desktop-session`
// 不在名单内（不得用裸 --desktop-session 断言）。
func TestB435SkillHasNoB434Remnants(t *testing.T) {
	text := readHandoffSkill(t)
	for _, gone := range []string{
		"card coordinate <id> --desktop", "handoff launcher", "seat desktop set", "--via page",
		"用 Claude Code 坐下", "拉起器",
	} {
		if strings.Contains(text, gone) {
			t.Errorf("SKILL.md 不得再含 B434 退役面 %q", gone)
		}
	}
}
