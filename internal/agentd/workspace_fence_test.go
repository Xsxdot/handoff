package agentd

import (
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/prochost"
)

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
