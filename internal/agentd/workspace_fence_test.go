package agentd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/xushixin/handoff/internal/prochost"
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

// 归因文案必须能出现在返回的 error 里——协调者看到的是这个字符串，
// 只写日志等于没归因（日志在执行机上，协调者手边没有）。
func TestForkFailureNoteReachesCaller(t *testing.T) {
	note := quotaNote(errFakeEAGAIN)
	if note == "" {
		t.Fatalf("EAGAIN 应产出归因文案")
	}
	if !strings.Contains(note, "配额") && !strings.Contains(note, "未知") {
		t.Fatalf("归因文案应给出结论或明确说未知，得到 %q", note)
	}
}

// 满额时拒发，且错误里必须带数字——「余量不足」四个字对排障毫无价值，
// 「2450/2400」才有。
func TestAdmissionRejectsWhenFull(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{Used: 2450, Limit: 2400, Known: true})
	defer restore()
	err := checkProcHeadroom("dispatch")
	if err == nil {
		t.Fatalf("满额应拒发")
	}
	if !errors.Is(err, ErrNoProcHeadroom) {
		t.Fatalf("应为 ErrNoProcHeadroom，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "2450") || !strings.Contains(err.Error(), "2400") {
		t.Fatalf("拒发文案必须带 used/limit，得到 %q", err.Error())
	}
}

// 高水位但没满：放行，不拦。拦在这里等于把「快满了」当成「满了」，
// 会把还能正常完成的任务无谓地挡掉。
func TestAdmissionPassesAtHighWatermark(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{Used: 2300, Limit: 2400, Known: true})
	defer restore()
	if err := checkProcHeadroom("dispatch"); err != nil {
		t.Fatalf("高水位不该拒发，得到 %v", err)
	}
}

// 读不出数：放行（fail-open）。为「量不出来」而拒绝派发，会让 handoff 在
// 不支持的平台上彻底不能用。
func TestAdmissionFailsOpenWhenUnknown(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{})
	defer restore()
	if err := checkProcHeadroom("dispatch"); err != nil {
		t.Fatalf("读数未知时必须放行，得到 %v", err)
	}
}

// fakeAdmission 替换准入判读缝，返回恢复函数。
func fakeAdmission(a prochost.Admission) func() {
	old := admissionFn
	admissionFn = func() prochost.Admission { return a }
	return func() { admissionFn = old }
}
