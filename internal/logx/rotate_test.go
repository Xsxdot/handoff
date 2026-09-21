// rotate_test.go 是轮转 handler 的内部锁：轮转阈值 100MB，经 Setup 缝构造需
// 真实写 100MB（构造不出），故直接调 newRotatingHandler 用 200B 上限验证份数
// 与「第 6 份挤掉最旧」。经 Setup 缝的 F19/F20 断言另有 logx_test.go，不靠本文件顶替。
package logx

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingHandlerKeepsConfiguredBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentd.log")
	h, err := newRotatingHandler(path, 200, 4, &slog.HandlerOptions{Level: slog.LevelInfo})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(h)
	for i := 0; i < 60; i++ { // 每条约 100B，足够触发多次轮转
		log.Info("rotation-line", "i", i, "pad", "0123456789012345678901234567890123456789")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if e.Name() == "agentd.log" ||
			len(e.Name()) > len("agentd.log.") && e.Name()[:len("agentd.log.")] == "agentd.log." {
			count++
		}
	}
	if count > 5 {
		t.Fatalf("轮转最多保留 5 份（当前+4 备份），实得 %d", count)
	}
	if _, err := os.Stat(path + ".4"); err != nil {
		t.Fatalf("应保留到 .4: %v", err)
	}
	if _, err := os.Stat(path + ".5"); err == nil {
		t.Fatal("不得保留 .5（第 6 份挤掉最旧）")
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("当前文件必须存在: %v", err)
	} else if info.Size() > 200 {
		t.Fatalf("当前文件超过单文件上限: %d", info.Size())
	}
}

func TestRotationConstants(t *testing.T) {
	if logRotationMaxBytes != 100*1024*1024 {
		t.Fatalf("单文件上限必须 100MB: %d", logRotationMaxBytes)
	}
	if logRotationMaxBackups+1 != 5 {
		t.Fatalf("总份数必须 5: %d", logRotationMaxBackups+1)
	}
}
