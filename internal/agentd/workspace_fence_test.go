package agentd

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// errFakeEAGAIN 模拟一次真实的 fork 失败：错误链里挂着 syscall.EAGAIN，
// 外层文案与 exec 包实际产出的一致。
var errFakeEAGAIN = fmt.Errorf("fork/exec /bin/sh: %w", syscall.EAGAIN)

// 归因只改文案、不改错误语义：非配额类失败必须原样上抛，调用方的
// errors.Is / 退出码判断一个都不能被影响。
func TestRunCmdNonQuotaErrorUnchanged(t *testing.T) {
	dir := t.TempDir()
	// 一条正常失败的命令：退出码非零，但不是 fork 失败
	out, code, err := RunCmd(context.Background(), dir, "exit 3")
	if err != nil {
		t.Fatalf("命令非零退出不应返回错误，得到 %v", err)
	}
	if code != 3 {
		t.Fatalf("退出码应为 3，得到 %d（输出 %q）", code, out)
	}
}

// 归因文案必须能出现在返回的 error 里——审核者看到的是这个字符串，
// 只写日志等于没归因（日志在执行机上，审核者手边没有）。
func TestForkFailureNoteReachesCaller(t *testing.T) {
	note := quotaNote(errFakeEAGAIN)
	if note == "" {
		t.Fatalf("EAGAIN 应产出归因文案")
	}
	if !strings.Contains(note, "配额") && !strings.Contains(note, "未知") {
		t.Fatalf("归因文案应给出结论或明确说未知，得到 %q", note)
	}
}
