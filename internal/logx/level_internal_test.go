// level_internal_test.go 锁 F19 级别解析：默认 warn，白名单外一律回退 warn。
// 放在包内是因为 parseLevel 未导出。
package logx

import (
	"log/slog"
	"testing"
)

func TestParseLevelDefaultsToWarn(t *testing.T) {
	if got := parseLevel(""); got != slog.LevelWarn {
		t.Fatalf("空值默认级别必须 warn: %v", got)
	}
	for in, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn,
		"error": slog.LevelError, "bogus": slog.LevelWarn,
	} {
		if got := parseLevel(in); got != want {
			t.Fatalf("parseLevel(%q)=%v want %v", in, got, want)
		}
	}
}
