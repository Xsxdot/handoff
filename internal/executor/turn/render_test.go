package turn_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/turn"
)

func TestAppendRenderCreatesAndAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), "render.log")
	if err := turn.AppendRender(p, "第一段"); err != nil {
		t.Fatalf("首次追加出错: %v", err)
	}
	if err := turn.AppendRender(p, "第二段"); err != nil {
		t.Fatalf("二次追加出错: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if string(b) != "第一段第二段" {
		t.Errorf("内容 = %q，期望 第一段第二段", string(b))
	}
}
