// skill_text_b435_organizer_test.go —— B435 O2：skills/handoff/SKILL.md「组织者」节的关键句在位（spec S5
// 后半、S6、S8；D7/D9/D11；§9 路径；breakdown P8 甲、R-K1 (a)/(a′)；O2 plan 段 5、N1/N4/N6、Q4）。
//
// 职责：按节切 SKILL.md（skill_text_b434_test.go#skillSection），断言组织者节逐字含下表关键句；
// 承重两句（授权规则、门铃取信句）只从 desktop_rule.go 常量取，D11 口吻句从 keystone 源码读
// briefingRoomTone 字面比对——三者都不在测试里另抄一份。
// 边界：只锁文本在位；模型是否照做归真机 O2-U2/U4/U5/U6。
package skill

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// organizerKeySentences 是组织者节必须逐字出现的关键句（按来源分组；改字须同步 SKILL.md）。
var organizerKeySentences = []struct{ why, text string }{
	// spec §9 路径（逐字）。
	{"规则文件路径（spec §9）", "`.handoff/organizer.md`"},
	{"记忆目录路径（spec §9）", "`~/.handoff/organizer/<project>/`"},
	// 坐下与前提（R 的 sit 写面、N4、N6、C-414）。
	{"坐下命令（R 写面）", "handoff session sit <项目房间> --as agent:<名字> --desktop-session <自己的 local_… id>"},
	{"坐下须在桌面端会话里执行", "必须在这场桌面端会话里执行"},
	{"本机须跑 agentd（N4）", "坐下的这台机器必须跑着 agentd"},
	{"建议 agent 身份（N6）", "建议以 `agent:<名字>` 身份坐下"},
	{"一项目一名（C-414）", "一个项目房间至多一名组织者"},
	// 门铃处置（N1）。
	{"门铃不含原文", "门铃信不含消息原文"},
	// P8 甲。
	{"from_seq=0 视作回放（P8 甲）", "`from_seq=0` 视作回放"},
	{"回放逐条先查", "逐条先查人类是否已答"},
	// D9。
	{"组织类范围（D9）", "组织类（先后、等待、基线、机器、卡间冲突）"},
	{"组织类答回卡所在会话（D9）", "答回卡所在会话，署名组织者"},
	{"内容类范围（D9）", "内容类（设计、验收、范围）"},
	{"内容类问人后转发（D9）", "在本桌面端会话里问人，人答后转发"},
	{"答前先查（D9）", "答前先查该问题有无人类回复，有则不答"},
	{"取证命令（H5）", "handoff room read <会话>"},
	// R-K1 (a)/(a′)。
	{"协调者须显式 @（R-K1）", "协调者向你提问须到项目房间发言并显式 @ 你的身份"},
	{"无 @ 叫不醒（R-K1）", "无 @ 的发言叫不醒你"},
	// R-K1 (a″)：组织者是同项目卡会话的书写者，答话须显式寻址。
	{"组织者可直接答回（R-K1 (a″)）", "项目房间驻场者是同项目卡会话的书写者"},
	{"答话须显式 @卡号（R-K1 (a″)）", "须显式 `@<卡号>`（或 `--reply-to` 协调者那条）"},
	{"不带 @ 叫不醒协调者（R-K1 (a″)）", "不带 @ 的答话叫不醒协调者"},
	// S8 恢复（R Q8 甲）。
	{"恢复：先 stand（S8）", "handoff session stand <项目房间> --as <身份>"},
	{"恢复：新会话再 sit（S8）", "--desktop-session <新的 local_… id>"},
	{"恢复：身份不变游标续用（S8）", "身份不变则从游标续读"},
}

func TestB435SkillOrganizerSectionKeySentences(t *testing.T) {
	sec := skillSection(t, readHandoffSkill(t), "### 组织者")
	for _, k := range organizerKeySentences {
		if !strings.Contains(sec, k.text) {
			t.Errorf("「组织者」节缺关键句（%s）：%q", k.why, k.text)
		}
	}
	for name, c := range map[string]string{
		"DesktopLetterAuthorizationRule": DesktopLetterAuthorizationRule,
		"OrganizerDoorbellTakeRule":      OrganizerDoorbellTakeRule,
	} {
		if !strings.Contains(sec, c) {
			t.Errorf("「组织者」节须逐字含 %s", name)
		}
	}
	// R-K1 (a″) 前的旧句（被拒时告诉人）已失效，不得残留。
	if stale := "发言被拒（你不是该会话的书写者）"; strings.Contains(sec, stale) {
		t.Errorf("「组织者」节残留 R-K1 (a″) 前的旧句：%q", stale)
	}
}

// R-K1 (a″)：「发言自由，身份执法」条的成员公式含项目房间驻场者这一半（与 collab.Service.sessionWriters 同口径）。
func TestB435SkillWriterFormulaIncludesProjectRoomResident(t *testing.T) {
	sec := skillSection(t, readHandoffSkill(t), "### 会话即工作单元")
	if want := "；含某项目卡的会话另加该项目的项目房间驻场者"; !strings.Contains(sec, want) {
		t.Errorf("「会话即工作单元」节成员公式缺 R-K1 (a″) 半边：%q", want)
	}
}

// D11：组织者节的房间口吻句与 push 简报（keystone.briefingRoomTone）同文。跨包未导出常量读不到，
// 读 keystone 源码取字面比对——keystone 侧改字时本测试同红，两处不漂。
func TestB435SkillOrganizerRoomToneMatchesKeystone(t *testing.T) {
	raw, err := os.ReadFile("../keystone/keystone.go")
	if err != nil {
		t.Fatalf("读 keystone.go: %v", err)
	}
	m := regexp.MustCompile(`(?m)^const briefingRoomTone = "([^"]+)"`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("keystone.go 找不到 const briefingRoomTone 字面（K0 简报口吻句）")
	}
	sec := skillSection(t, readHandoffSkill(t), "### 组织者")
	if !strings.Contains(sec, string(m[1])) {
		t.Fatalf("「组织者」节须逐字含 push 简报口吻句 %q（D11）", m[1])
	}
}
