package grok

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSymlinkCapabilityOnUnix 钉住 unix 上恒可用——那里建符号链接不需要特权。
func TestSymlinkCapabilityOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("本用例只描述 unix 上的恒真行为")
	}
	ok, reason := SymlinkCapability(t.TempDir())
	if !ok {
		t.Fatalf("unix 上竟然报不支持: %s", reason)
	}
	if reason != "" {
		t.Fatalf("支持时 reason 应为空，实得 %q", reason)
	}
}

// TestSymlinkCapabilityLeavesNothingBehind 钉住探测不留垃圾：它跑在 DataDir 下，
// 每次 agentd 启动都会执行，留一个就是每次启动留一个。
func TestSymlinkCapabilityLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if ok, reason := SymlinkCapability(dir); !ok && runtime.GOOS != "windows" {
		t.Fatalf("探测失败: %s", reason)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读目录失败: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("探测后目录不干净，残留 %v", names)
	}
}

// TestSymlinkCapabilityUnwritableDir 钉住探测目录不可用时给出的是理由而不是 panic。
func TestSymlinkCapabilityUnwritableDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ok, reason := SymlinkCapability(missing)
	if ok {
		t.Fatalf("目录不存在时竟然报支持")
	}
	if reason == "" {
		t.Fatalf("不支持时必须给出理由")
	}
}
