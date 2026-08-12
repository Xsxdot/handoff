package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusDetectsStaleSite 是一致性检查的核心断言：改坏某一处落点后
// 能准确报出**是哪一处**旧了。
//
// why：因为安装是我们自己做的、落点已知，所以这个判断是准确的而不是猜的。
// 一条会说谎的诊断命令比没有更糟。
func TestStatusDetectsStaleSite(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(home, ".grok"), 0o755)
	Install("新内容", home)
	// 把 .grok 那处换成一个内容不同的实体文件
	p := filepath.Join(home, ".grok", "skills", "handoff")
	os.RemoveAll(p)
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("旧内容"), 0o644)

	sites, err := Status("新内容", home)
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for _, s := range sites {
		if s.State == StateStale {
			stale = append(stale, s.Path)
		}
	}
	if len(stale) != 1 || !strings.Contains(stale[0], ".grok") {
		t.Fatalf("应准确报出 .grok 一处旧了，实得 %v", stale)
	}
}

// TestStatusNeverClaimsNotInstalled：落点不存在时只报 missing，
// 绝不断言「你没装」——我们只知道自己写过哪几处，不知道 agent 从别处读没读到。
func TestStatusNeverClaimsNotInstalled(t *testing.T) {
	sites, err := Status("x", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.State != StateMissing {
			t.Fatalf("空 HOME 下每处都该是 missing，实得 %+v", s)
		}
	}
}
