package webui

import (
	"testing"
)

// FS() 必须永远可用——调用方不该需要判空。
func TestFSNeverNil(t *testing.T) {
	if FS() == nil {
		t.Fatal("FS() 返回 nil，调用方将无法区分「没有产物」与「包坏了」")
	}
}
