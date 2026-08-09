package permgate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestJudgeFailClosedTable 把 spec §7 的 fail-closed 表逐行钉死。
//
// 表里没有任何一行导向 AutoAllow——这是整个设计的支点，一旦有人加了新的
// 提前返回并误落到 AutoAllow，本用例必须红。
func TestJudgeFailClosedTable(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: t.TempDir()}
	cases := []struct {
		name string
		req  Request
	}{
		{"描述含截断标记", Request{Tool: "bash", Text: "Bash: x", Command: "x", Truncated: true}},
		{"写文件但无路径", Request{Tool: "write", Text: "Write: ?"}},
		{"路径越界", Request{Tool: "write", Text: "Write: /etc/hosts", Paths: []string{"/etc/hosts"}}},
		{"多路径任一越界", Request{Tool: "edit", Text: "Edit: x",
			Paths: []string{filepath.Join(work, "a.go"), "/etc/hosts"}}},
		{"写文件描述命中黑名单", Request{Tool: "write", Text: "Write: /x/sudoers-sudo",
			Paths: []string{filepath.Join(work, "a.go")}}},
		{"剥离后仍命中", Request{Tool: "bash", Text: "Bash: rm -rf /", Command: "rm -rf /"}},
		{"含执行包装器", Request{Tool: "bash", Text: `Bash: sh -c "rm -rf /"`, Command: `sh -c "rm -rf /"`}},
	}
	for _, c := range cases {
		if v := g.Judge(c.req, sc); v.Action != Escalate {
			t.Errorf("%s：必须 Escalate，实得 %s（%s）", c.name, v.Action, v.Reason)
		}
	}
}

// TestJudgeAutoAllowOnlyForInScopeWrites AutoAllow 的唯一合法来源。
func TestJudgeAutoAllowOnlyForInScopeWrites(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	task := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: task}
	ok := []Request{
		{Tool: "write", Text: "Write: main.go", Paths: []string{"main.go"}},
		{Tool: "edit", Text: "Edit: " + filepath.Join(work, "a.go"),
			Paths: []string{filepath.Join(work, "a.go")}},
		{Tool: "write", Text: "Write: notes.md", Paths: []string{filepath.Join(task, "notes.md")}},
	}
	for _, r := range ok {
		if v := g.Judge(r, sc); v.Action != AutoAllow {
			t.Errorf("范围内写入应自动放行：%v，实得 %s（%s）", r.Paths, v.Action, v.Reason)
		}
	}
}

// TestJudgeNeverAutoAllowsNonFileTools 非写文件工具永远拿不到 AutoAllow。
//
// 本次改动不放宽任何现有工具的裁决——bash 再干净也只到 Consult。
func TestJudgeNeverAutoAllowsNonFileTools(t *testing.T) {
	g := newTestGate(t)
	sc := Scope{Workdir: t.TempDir(), TaskDir: t.TempDir()}
	reqs := []Request{
		{Tool: "bash", Text: "Bash: go build ./...", Command: "go build ./..."},
		{Tool: "webfetch", Text: "WebFetch: https://example.com"},
		{Tool: "other", Text: "SomeTool: whatever"},
	}
	for _, r := range reqs {
		if v := g.Judge(r, sc); v.Action == AutoAllow {
			t.Errorf("非写文件工具不得自动放行：%s", r.Text)
		}
	}
}

// TestJudgeNormalizationFailureEscalates 路径归一化失败走 fail-closed。
//
// 用一个 NUL 字节构造必然失败的路径——EvalSymlinks/Abs 都无法处理。
func TestJudgeNormalizationFailureEscalates(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	// 目标路径落在一个「父目录是普通文件」的位置：EvalSymlinks 解不动，
	// resolveExistingPrefix 会退回原路径，最终仍应判越界或归一化失败——
	// 两者都是 Escalate。
	f := filepath.Join(work, "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	v := g.Judge(Request{Tool: "write", Text: "Write: x",
		Paths: []string{filepath.Join(f, "..", "..", "outside.go")}}, Scope{Workdir: work})
	if v.Action == AutoAllow {
		t.Fatalf("越出 Workdir 的路径不得自动放行，实得 %s（%s）", v.Action, v.Reason)
	}
}
