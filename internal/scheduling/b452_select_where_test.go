package scheduling_test

// b452_select_where_test.go —— B452 S7 亲和过滤的核心方法：SelectWhere 的谓词是过滤不是排序。
//
// 夹具 newFrozenFixture：小队 S = [A@machine-A, B@machine-B]，各容量 1。
import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/scheduling"
)

func onMachine(m string) func(scheduling.Carrier) bool {
	return func(c scheduling.Carrier) bool { return c.Machine == m }
}

func TestB452SelectWherePredicateFilters(t *testing.T) {
	svc, facade := newFrozenFixture(t)
	req := scheduling.IgnitionRequest{Squad: "S", Actor: "test"}
	b, err := svc.SelectWhere(req, onMachine("machine-B"))
	if err != nil || b.Carrier != "B" || b.Target != "machine-B" {
		t.Fatalf("谓词只放行 machine-B 时应选 B（即便 A 有空位），得 %+v %v", b, err)
	}
	if _, err := svc.SelectWhere(req, onMachine("machine-Z")); !errors.Is(err, scheduling.ErrNoHealthy) {
		t.Fatalf("谓词全拒应 ErrNoHealthy，得 %v", err)
	}
	// 放行的成员全满 → ErrNoSlot（不换到被拒的 A）
	if _, err := svc.AdmitFrozen(b); err != nil {
		t.Fatalf("占满 B: %v", err)
	}
	if _, err := svc.SelectWhere(req, onMachine("machine-B")); !errors.Is(err, scheduling.ErrNoSlot) {
		t.Fatalf("放行成员全满应 ErrNoSlot，得 %v", err)
	}
	// nil 谓词与 Select 同义
	viaNil, err1 := svc.SelectWhere(req, nil)
	viaSelect, err2 := svc.Select(req)
	if err1 != nil || err2 != nil || viaNil != viaSelect || viaNil.Carrier != "A" {
		t.Fatalf("SelectWhere(nil) 应与 Select 一致：%+v/%v vs %+v/%v", viaNil, err1, viaSelect, err2)
	}
	if got := runningCount(t, facade, "carrier/A"); got != 0 {
		t.Fatalf("SelectWhere 只读，不得占 A：%d", got)
	}
}
