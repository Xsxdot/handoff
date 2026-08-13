package turn

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestNoTrailerResultIsNotOK(t *testing.T) {
	r := NoTrailerResult("sess-1", "handoff/T1", "abc1234def", "干完了，已提交。")
	if r.OK {
		t.Fatal("无 trailer 的回合绝不能报 OK——这正是 B74 的假完成")
	}
}

func TestNoTrailerResultKeepsGitTruthStructured(t *testing.T) {
	r := NoTrailerResult("sess-1", "handoff/T1", "abc1234def", "干完了")
	if r.Branch != "handoff/T1" {
		t.Fatalf("branch 必须留在结构化字段里，got %q", r.Branch)
	}
	if r.CommitHash != "abc1234def" {
		t.Fatalf("commit 必须留在结构化字段里，got %q", r.CommitHash)
	}
	if r.SessionID != "sess-1" {
		t.Fatalf("sessionID 丢失，got %q", r.SessionID)
	}
}

func TestNoTrailerResultCarriesTurnDisciplineVoidReason(t *testing.T) {
	r := NoTrailerResult("s", "b", "c", "t")
	if r.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由必须说真话（executor 还活着），got %q", r.VoidReason)
	}
	if strings.Contains(r.VoidReason, "已终结") {
		t.Fatal("executor 并未终结，理由不得说它终结了")
	}
}

func TestNoTrailerFailReasonNamesAllThreeThings(t *testing.T) {
	reason := NoTrailerFailReason("handoff/T1", "abc1234def567890", "……最后这段是正文尾巴")
	// (a) 判定依据
	if !strings.Contains(reason, "未输出协议 trailer") {
		t.Errorf("缺判定依据: %s", reason)
	}
	// (b) git 实况：分支@commit
	if !strings.Contains(reason, "handoff/T1@") || !strings.Contains(reason, "abc1234") {
		t.Errorf("缺 git 实况: %s", reason)
	}
	// (c) 正文尾部片段
	if !strings.Contains(reason, "最后这段是正文尾巴") {
		t.Errorf("缺正文尾部: %s", reason)
	}
}

func TestNoTrailerFailReasonClampsLongBody(t *testing.T) {
	long := strings.Repeat("长", 5000)
	reason := NoTrailerFailReason("b", "c", long)
	if len([]rune(reason)) > 400 {
		t.Fatalf("失败原因未截断，长度 %d 符文——它会进事件 payload 与协调者视野",
			len([]rune(reason)))
	}
}
