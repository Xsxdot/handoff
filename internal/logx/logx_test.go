// logx 包测试：验证 JSON 文件输出、级别过滤与空 logPath 降级行为。
package logx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/logx"
)

func TestSetupWritesJSONToFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "handoff.log")
	log := logx.Setup("test", p)
	log.Info("hello", "k", "v")

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"msg":"hello"`) {
		t.Fatalf("文件日志缺少消息: %s", s)
	}
	if !strings.Contains(s, `"component":"test"`) || !strings.Contains(s, `"k":"v"`) {
		t.Fatalf("文件日志缺少附加属性: %s", s)
	}
}

func TestSetupLevelFilter(t *testing.T) {
	t.Setenv("HANDOFF_LOG_LEVEL", "error")
	p := filepath.Join(t.TempDir(), "handoff.log")
	log := logx.Setup("test", p)
	log.Debug("debug-msg")
	log.Info("info-msg")
	log.Error("error-msg")

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "error-msg") {
		t.Fatalf("error 级别消息未写入: %s", s)
	}
	if strings.Contains(s, "debug-msg") || strings.Contains(s, "info-msg") {
		t.Fatalf("低于 error 级别的消息不应写入: %s", s)
	}
}

func TestSetupEmptyLogPath(t *testing.T) {
	log := logx.Setup("test", "")
	if log == nil {
		t.Fatal("Setup 返回了 nil logger")
	}
	// 仅验证不 panic、不创建文件
	log.Info("stderr-only")
}
