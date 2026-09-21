// logx 包测试：验证 JSON 文件输出、级别过滤与空 logPath 降级行为。
package logx_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/logx"
)

func TestSetupWritesJSONToFile(t *testing.T) {
	// 本测试验 JSON 落盘与属性，不验默认级别（P-5）：默认已改 warn，须显式 info。
	t.Setenv("HANDOFF_LOG_LEVEL", "info")
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

// TestSetupDefaultLevelWarnBehavior 锁 F19：默认级别 warn——INFO 不落盘、
// WARN 落盘。
func TestSetupDefaultLevelWarnBehavior(t *testing.T) {
	t.Setenv("HANDOFF_LOG_LEVEL", "")
	p := filepath.Join(t.TempDir(), "handoff.log")
	log := logx.Setup("test", p)
	log.Info("info-msg")
	log.Warn("warn-msg")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "info-msg") {
		t.Fatalf("默认级别下 INFO 不得落盘: %s", s)
	}
	if !strings.Contains(s, "warn-msg") {
		t.Fatalf("默认级别下 WARN 必须落盘: %s", s)
	}
}

// TestSetupSingleWriteWithLogPath 锁 F20 两半边：带 logPath 时同一记录落文件
// 恰一次且不落 stderr；不带 logPath 时仅 stderr 文本、不创建文件。
func TestSetupSingleWriteWithLogPath(t *testing.T) {
	t.Setenv("HANDOFF_LOG_LEVEL", "info")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = writer
	p := filepath.Join(t.TempDir(), "handoff.log")
	log := logx.Setup("test", p)
	log.Info("once-only", "k", "v")
	os.Stderr = old
	_ = writer.Close()
	captured, _ := io.ReadAll(reader)
	_ = reader.Close()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(b), `"msg":"once-only"`); got != 1 {
		t.Fatalf("带 logPath 时同一记录必须落文件恰一次，实得 %d: %s", got, b)
	}
	if strings.Contains(string(captured), "once-only") {
		t.Fatalf("带 logPath 时不得再落 stderr: %s", captured)
	}
	// 另一半：不带 logPath 时仅 stderr 文本，不创建文件。
	dir := t.TempDir()
	path := filepath.Join(dir, "should-not-exist.log")
	reader2, writer2, _ := os.Pipe()
	os.Stderr = writer2
	log2 := logx.Setup("test", "")
	log2.Warn("stderr-only-line")
	os.Stderr = old
	_ = writer2.Close()
	captured2, _ := io.ReadAll(reader2)
	_ = reader2.Close()
	if !strings.Contains(string(captured2), "stderr-only-line") {
		t.Fatalf("不带 logPath 必须写 stderr: %s", captured2)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("不带 logPath 不应创建文件: %s", path)
	}
}
