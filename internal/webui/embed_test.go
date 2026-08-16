//go:build embedweb

package webui

import (
	"io/fs"
	"testing"
)

// 只在 release 构建路径（-tags embedweb）跑：确认产物真的进来了。
// 这道门存在的理由：go:embed 一个只有 index.html 的空壳目录也能编译通过，
// 光「编译过了」不代表前端资源在里面。
func TestEmbeddedFSHasRealAssets(t *testing.T) {
	if !Embedded() {
		t.Fatal("带 embedweb 标签时 Embedded() 必须为 true")
	}
	if _, err := fs.ReadFile(FS(), "index.html"); err != nil {
		t.Fatalf("嵌入产物缺 index.html：%v", err)
	}
	n := 0
	if err := fs.WalkDir(FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("遍历嵌入产物失败：%v", err)
	}
	// vite 产物至少是 index.html + 一个 JS + 一个 CSS
	if n < 3 {
		t.Errorf("嵌入产物只有 %d 个文件，疑似嵌进了空壳目录", n)
	}
}
