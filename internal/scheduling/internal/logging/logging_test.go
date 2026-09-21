// logging_test.go —— 编制域 logger 入口的包内白盒测试（内部锁，不顶替缝级断言）。
package logging

import (
	"os"
	"strings"
	"testing"
)

// TestStatusLogNonNilAndUsable 断言入口非 nil 且可正常写日志（不 panic）。
func TestStatusLogNonNilAndUsable(t *testing.T) {
	l := StatusLog()
	if l == nil {
		t.Fatal("StatusLog() 不得为 nil")
	}
	l.Info("probe")
}

// TestStatusLogSourceHasModAttr 用源码级断言锁住 mod=scheduling 属性——属性经
// slog.Default() 包装，无法从一个独立 logger 反查，故直接核对实现文本。
func TestStatusLogSourceHasModAttr(t *testing.T) {
	raw, err := os.ReadFile("logging.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"mod", "scheduling"`) {
		t.Fatal("StatusLog 必须带 mod=scheduling 属性")
	}
}
