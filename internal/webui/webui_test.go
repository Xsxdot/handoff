package webui

import (
	"io/fs"
	"strings"
	"testing"
)

// 默认构建（不带 embedweb）下必须是 stub，且 stub 必须诚实——
// 不能是空白页，也不能假装成正常控制台。
func TestStubFSHasHonestIndex(t *testing.T) {
	if Embedded() {
		t.Fatal("默认构建不应报告已嵌入前端；本测试只在无 embedweb 标签时有意义")
	}
	b, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("stub 必须提供 index.html：%v", err)
	}
	body := string(b)
	for _, want := range []string{"未嵌入", "npm run dev"} {
		if !strings.Contains(body, want) {
			t.Errorf("stub 说明页缺少关键信息 %q，实际内容：%s", want, body)
		}
	}
}

// FS() 必须永远可用——调用方不该需要判空。
func TestFSNeverNil(t *testing.T) {
	if FS() == nil {
		t.Fatal("FS() 返回 nil，调用方将无法区分「没有产物」与「包坏了」")
	}
}
