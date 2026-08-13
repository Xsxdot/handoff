package codex_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/codex"
)

// 裁决映射：只有 once 放行，其余一律 decline（fail-closed）
func TestDecisionForIsFailClosed(t *testing.T) {
	if got := codex.DecisionForTest("once"); got != "accept" {
		t.Fatalf(`once → %q，应为 "accept"`, got)
	}
	for _, d := range []string{"reject", "", "always", "ONCE", "accept", "cancel"} {
		if got := codex.DecisionForTest(d); got != "decline" {
			t.Fatalf(`%q → %q，应为 "decline"`, d, got)
		}
	}
}

// cancel 会掐掉整个回合，handoff 的 reject 语义是「拒这一次、回合继续」，绝不能用它
func TestDecisionNeverEmitsCancel(t *testing.T) {
	for _, d := range []string{"once", "reject", "cancel", "acceptForSession"} {
		if got := codex.DecisionForTest(d); got == "cancel" {
			t.Fatalf("裁决 %q 映射出了 cancel，会掐掉整个回合", d)
		}
	}
}

func TestPermRequestFromCommandKeepsFullCommand(t *testing.T) {
	long := "echo " + strings.Repeat("x", 5000) + " && rm -rf build"
	raw := []byte(`{"itemId":"exec-1","threadId":"t","turnId":"u",
		"command":` + mustJSON(long) + `,"cwd":"/w",
		"commandActions":[{"type":"read","path":"/w/a.go"},{"type":"unknown","command":"rm -rf build"}]}`)
	a, ok := codex.ParseCommandApprovalForTest(raw)
	if !ok {
		t.Fatal("应解析成功")
	}
	p := codex.PermRequestFromCommandForTest(a)
	if p == nil {
		t.Fatal("命令类权限必须给出结构化 PermRequest")
	}
	if p.Tool != executor.PermToolBash {
		t.Fatalf("Tool = %s", p.Tool)
	}
	if p.Command != long {
		t.Fatalf("命令被改写或截断了（安全判据必须拿到全文），len=%d", len(p.Command))
	}
	if len(p.Paths) != 1 || p.Paths[0] != "/w/a.go" {
		t.Fatalf("Paths = %v，应只收 commandActions 里非空的 path", p.Paths)
	}
}

func TestPermRequestFromFileChangeClassifiesTool(t *testing.T) {
	allUpdate := codex.ThreadItemForTest("patch-1", "fileChange",
		[][2]string{{"/w/a.go", "update"}, {"/w/b.go", "update"}})
	p := codex.PermRequestFromFileChangeForTest(allUpdate)
	if p == nil || p.Tool != executor.PermToolEdit {
		t.Fatalf("全 update 应判 edit，实得 %+v", p)
	}
	if len(p.Paths) != 2 || p.Paths[1] != "/w/b.go" {
		t.Fatalf("Paths = %v", p.Paths)
	}

	mixed := codex.ThreadItemForTest("patch-2", "fileChange",
		[][2]string{{"/w/a.go", "update"}, {"/w/new.go", "add"}})
	p2 := codex.PermRequestFromFileChangeForTest(mixed)
	if p2 == nil || p2.Tool != executor.PermToolWrite {
		t.Fatalf("含非 update 应判 write（爆炸半径更大，往大了判），实得 %+v", p2)
	}
}

// 索引查不到时必须返回 nil，让 manager fail-closed 升级人工——绝不伪造空结构
func TestPermRequestFromFileChangeNilWhenUnknown(t *testing.T) {
	if p := codex.PermRequestFromFileChangeForTest(nil); p != nil {
		t.Fatalf("索引未命中必须返回 nil，实得 %+v", p)
	}
	empty := codex.ThreadItemForTest("patch-3", "fileChange", nil)
	if p := codex.PermRequestFromFileChangeForTest(empty); p != nil {
		t.Fatalf("没有任何 change 时必须返回 nil，实得 %+v", p)
	}
}

// B6：权限文本不为安全而截断，只有超 64KB 硬上限才截
func TestPermTextNotTruncatedForSecurity(t *testing.T) {
	long := strings.Repeat("y", 20000)
	a, _ := codex.ParseCommandApprovalForTest([]byte(
		`{"itemId":"exec-2","command":` + mustJSON(long) + `,"cwd":"/w"}`))
	text := codex.CommandPermTextForTest(a)
	if !strings.Contains(text, long) {
		t.Fatal("20KB 的命令不该被截断——安全门要看全文")
	}
	if strings.Contains(text, executor.TruncationMarker) {
		t.Fatal("未超硬上限不应出现截断标记")
	}

	huge := strings.Repeat("z", 70000)
	a2, _ := codex.ParseCommandApprovalForTest([]byte(
		`{"itemId":"exec-3","command":` + mustJSON(huge) + `,"cwd":"/w"}`))
	text2 := codex.CommandPermTextForTest(a2)
	if !strings.Contains(text2, executor.TruncationMarker) {
		t.Fatal("超 64KB 硬上限必须截断并留标记（防失控）")
	}
}

// 挂起表：取走即移除，作废返回数量
func TestPermTableTakeAndVoid(t *testing.T) {
	tb := codex.NewPermTableForTest()
	tb.NoteForTest("exec-1", []byte("1"), "运行 rm -rf build")
	if _, ok := tb.TakeForTest("exec-1"); !ok {
		t.Fatal("应能取到")
	}
	if _, ok := tb.TakeForTest("exec-1"); ok {
		t.Fatal("取走后不应还在")
	}
	tb.NoteForTest("exec-2", []byte("2"), "d2")
	tb.NoteForTest("exec-3", []byte("3"), "d3")
	if n := tb.VoidAllForTest(); n != 2 {
		t.Fatalf("作废数量 = %d，应为 2", n)
	}
}

// 被拒清单交给协调者的是描述而不是不透明 id
func TestRejectedTurnQuestionShowsDescription(t *testing.T) {
	tb := codex.NewPermTableForTest()
	tb.NoteRejectedForTest("运行 rm -rf /etc")
	got := tb.TakeRejectedForTest()
	if len(got) != 1 {
		t.Fatalf("被拒清单 = %v", got)
	}
	q := codex.RejectedTurnQuestionForTest(got)
	if !strings.Contains(q, "rm -rf /etc") {
		t.Fatalf("问题正文必须含权限描述，实得: %s", q)
	}
	if len(tb.TakeRejectedForTest()) != 0 {
		t.Fatal("取走后应清空，否则下回合会重复上报")
	}
}

func mustJSON(s string) string {
	b, _ := jsonMarshal(s)
	return b
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
