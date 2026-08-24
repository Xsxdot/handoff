// discipline 命令族测试：全部经 runLedgerCLI 穿真实 SQLite 临时库与真实
// openLedger 回退路径；校验类断言的文案锚点取自 internal/ledger 库层错误——
// CLI 只透传不复制规则。
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempBody(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "body.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDisciplinePutGetRoundtrip(t *testing.T) {
	dir := t.TempDir()
	bodyV1 := "角色层第一行甲\n角色层第二行乙\n"
	p := writeTempBody(t, dir, bodyV1)

	out, _, err := runLedgerCLI(t, dir, "discipline", "put", "charter-implement", p)
	if err != nil || !strings.Contains(out, `"version":1`) {
		t.Fatalf("put v1: %v %q", err, out)
	}
	out, _, err = runLedgerCLI(t, dir, "discipline", "get", "charter-implement")
	if err != nil || !strings.Contains(out, "v1") ||
		!strings.Contains(out, "角色层第一行甲") || !strings.Contains(out, "角色层第二行乙") {
		t.Fatalf("get 应打印版本号与正文: %v %q", err, out)
	}

	bodyV2 := "改一句话就是新版本\n"
	p = writeTempBody(t, dir, bodyV2)
	out, _, err = runLedgerCLI(t, dir, "discipline", "put", "charter-implement", p)
	if err != nil || !strings.Contains(out, `"version":2`) {
		t.Fatalf("put v2: %v %q", err, out)
	}
	out, _, err = runLedgerCLI(t, dir, "discipline", "get", "charter-implement")
	if err != nil || !strings.Contains(out, "v2") || !strings.Contains(out, "改一句话就是新版本") {
		t.Fatalf("缺省 get 应取最新版: %v %q", err, out)
	}
	out, _, err = runLedgerCLI(t, dir, "discipline", "get", "charter-implement", "--version", "1")
	if err != nil || !strings.Contains(out, "v1") || !strings.Contains(out, "角色层第一行甲") {
		t.Fatalf("get --version 应取历史版: %v %q", err, out)
	}

	// 未配 ledger.dsn：库必须落在本机 DataDir 回退路径上。
	if _, statErr := os.Stat(filepath.Join(dir, "ledger.db")); statErr != nil {
		t.Fatalf("本机回退 SQLite 未落 DataDir: %v", statErr)
	}
}

func TestDisciplineListAscendingDedup(t *testing.T) {
	dir := t.TempDir()
	put := func(name string, content string) {
		t.Helper()
		p := writeTempBody(t, dir, content)
		out, _, err := runLedgerCLI(t, dir, "discipline", "put", name, p)
		if err != nil {
			t.Fatalf("put %s: %v %q", name, err, out)
		}
	}
	put("ccc-third", "三\n")
	put("aaa-first", "一\n")
	put("bbb-second", "二之一\n")
	put("bbb-second", "二之二\n")

	out, _, err := runLedgerCLI(t, dir, "discipline", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"aaa-first", "bbb-second", "ccc-third"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list 缺 %s: %q", want, out)
		}
	}
	if n := strings.Count(out, "bbb-second"); n != 1 {
		t.Fatalf("同名多版本应去重为一行，实际出现 %d 次: %q", n, out)
	}
	iAaa := strings.Index(out, "aaa-first")
	iBbb := strings.Index(out, "bbb-second")
	iCcc := strings.Index(out, "ccc-third")
	if !(iAaa < iBbb && iBbb < iCcc) {
		t.Fatalf("list 应升序: %q", out)
	}
}

func TestDisciplinePutRejectsBadInput(t *testing.T) {
	dir := t.TempDir()

	// 文案锚点取自 internal/ledger/disciplines.go——错误必须出自真实校验层。
	p := writeTempBody(t, dir, "  \n\t\n")
	_, _, err := runLedgerCLI(t, dir, "discipline", "put", "empty-body", p)
	if err == nil || !strings.Contains(err.Error(), "正文不能为空") {
		t.Fatalf("空正文应被库层拒绝: %v", err)
	}

	p = writeTempBody(t, dir, strings.Repeat("x", 64<<10+1))
	_, _, err = runLedgerCLI(t, dir, "discipline", "put", "too-big", p)
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超 64KiB 应被库层拒绝: %v", err)
	}

	p = writeTempBody(t, dir, "正常正文\n")
	_, _, err = runLedgerCLI(t, dir, "discipline", "put", "a/b", p)
	if err == nil || !strings.Contains(err.Error(), "路径分隔符") {
		t.Fatalf("含路径分隔符名字应被库层拒绝: %v", err)
	}

	out, _, err := runLedgerCLI(t, dir, "discipline", "list")
	if err != nil || strings.Contains(out, "empty-body") || strings.Contains(out, "too-big") || strings.Contains(out, "a/b") {
		t.Fatalf("被拒输入不得留下任何行: %v %q", err, out)
	}
}
