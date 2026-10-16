// skill_text_b434_test.go —— B434.2：skills/handoff/SKILL.md 的桌面端坐席、拉起器与来信授权
// 规则文本（契约条 262、277、278；拆解稿 P6=甲）。
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

// 条 277：「占座」节含授权规则逐字。
func TestB434SkillSeatHasAuthorizationRule(t *testing.T) {
	sec := skillSection(t, readHandoffSkill(t), "### 占座")
	if !strings.Contains(sec, DesktopLetterAuthorizationRule) {
		t.Fatal("「占座」节必须逐字含 DesktopLetterAuthorizationRule")
	}
}

// 条 278：「主 agent 回路」的 CLAUDE.md 常设授权片段（围栏块）含授权规则逐字与 [handoff] 标记。
func TestB434SkillClaudeMDSnippetHasAuthorizationRule(t *testing.T) {
	sec := skillSection(t, readHandoffSkill(t), "### 主 agent 回路")
	block := fencedBlockAfter(t, sec, "CLAUDE.md 常设授权片段")
	// 围栏错配（如闭栏被吞）时块会越过后续标题、吞进「占座」节的那份规则而假绿：块内不得出现标题行。
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
}

// 条 262：「主 agent 回路」含 handoff card coordinate <id> --desktop。
func TestB434SkillMainAgentDesktopCoordinate(t *testing.T) {
	sec := skillSection(t, readHandoffSkill(t), "### 主 agent 回路")
	if !strings.Contains(sec, "handoff card coordinate <id> --desktop") {
		t.Fatal("「主 agent 回路」必须含 handoff card coordinate <id> --desktop")
	}
}

// P6=甲：「占座」补「用 Claude Code 坐下」与桌面端 bind / 补记 / 拉起器登记命令；
// 「主 agent 回路」补拉起器职责。命令形状与 cmd 落地的 flag 逐字对齐。
func TestB434SkillSeatDesktopCommands(t *testing.T) {
	text := readHandoffSkill(t)
	seat := skillSection(t, text, "### 占座")
	for _, want := range []string{
		"用 Claude Code 坐下",
		"handoff card bind <id> --desktop-session <local_…> --via page",
		"handoff card seat desktop set <id> --desktop-session <local_…>",
		"handoff launcher register --project <项目> --desktop-session <local_…>",
		// 跨卡审计 §14-4：「浏览器页不能坐下」与「用 Claude Code 坐下」按钮不得自相矛盾。
		"浏览器页不能坐下（页面只负责开会话，坐下仍是会话里跑 `card bind --desktop-session`）",
	} {
		if !strings.Contains(seat, want) {
			t.Fatalf("「占座」节缺少 %q", want)
		}
	}
	main := skillSection(t, text, "### 主 agent 回路")
	for _, want := range []string{"拉起器", "handoff launcher register", "请替我开一场协调者会话"} {
		if !strings.Contains(main, want) {
			t.Fatalf("「主 agent 回路」缺少 %q", want)
		}
	}
}
