package cmd

// b452_template_test.go —— B452 S1（V1）CLI 侧：template put 改用 TemplateDef.Validate。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeB452Def(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestB452TemplatePutSkilledRules(t *testing.T) {
	dir := t.TempDir()
	t.Run("只读形态 discipline 空可写且 show 原样回显两新键", func(t *testing.T) {
		p := writeB452Def(t, dir, "ro.json", `{"receiver":"mimo","mode":"readonly","target":"here","prompt":"查"}`)
		out, _, err := runLedgerCLI(t, dir, "template", "put", "code-investigate", "--file", p)
		if err != nil || !strings.Contains(out, `"version":1`) {
			t.Fatalf("put: %v %q", err, out)
		}
		out, _, err = runLedgerCLI(t, dir, "template", "show", "code-investigate")
		if err != nil {
			t.Fatalf("show: %v", err)
		}
		for _, want := range []string{`"receiver":"mimo"`, `"mode":"readonly"`, `"target":"here"`} {
			if !strings.Contains(out, want) {
				t.Fatalf("show 缺 %s: %s", want, out)
			}
		}
	})
	t.Run("mode 非法值被拒且点名合法值", func(t *testing.T) {
		p := writeB452Def(t, dir, "bad.json", `{"executor":"codex","prompt":"p","discipline":"implement","mode":"ro"}`)
		_, _, err := runLedgerCLI(t, dir, "template", "put", "bad", "--file", p)
		if err == nil || !strings.Contains(err.Error(), `"readonly"`) || !strings.Contains(err.Error(), `"branch"`) {
			t.Fatalf("应拒 mode=ro 并点名合法值，得 %v", err)
		}
	})
	t.Run("分支形态 discipline 空仍被拒", func(t *testing.T) {
		p := writeB452Def(t, dir, "br.json", `{"executor":"codex","prompt":"p","mode":"branch"}`)
		_, _, err := runLedgerCLI(t, dir, "template", "put", "br", "--file", p)
		if err == nil || !strings.Contains(err.Error(), "discipline") {
			t.Fatalf("应拒 discipline 空，得 %v", err)
		}
	})
	t.Run("旧定义 show 不出现新键", func(t *testing.T) {
		p := writeB452Def(t, dir, "old.json", `{"executor":"codex","purpose":"implement","branch_prefix":"cards","prompt":"x","discipline":"implement"}`)
		if _, _, err := runLedgerCLI(t, dir, "template", "put", "old", "--file", p); err != nil {
			t.Fatalf("put 旧定义: %v", err)
		}
		out, _, err := runLedgerCLI(t, dir, "template", "show", "old")
		if err != nil {
			t.Fatal(err)
		}
		want := `"Def":{"executor":"codex","target":"","purpose":"implement","branch_prefix":"cards","prompt":"x","discipline":"implement"}`
		if !strings.Contains(out, want) {
			t.Fatalf("旧定义 show 的 Def 字节应与基线一致:\n got %s\nwant 含 %s", out, want)
		}
	})
}
