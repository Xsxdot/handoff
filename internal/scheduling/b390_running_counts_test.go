package scheduling_test

// b390_running_counts_test.go —— B390 名额键残留观察面（读半边）的缝级测试。
//
// 职责：经真实 ledger 适配器种子 sched_running 行，断言 RunningCounts 按行 id
// 返回 {id: count}，缺失键不出现（区分「缺失」与「零」）。
// 边界：只读——本测试不清理任何共享键（R3 的 TTL/自愈是架构级议题，B390 不做）。

import (
	"strconv"
	"testing"
)

// TestRunningCountsReportsSeededKeys 锁：种子两个不同占用键，RunningCounts 精确
// 返回它们；未种子的键不出现（缺失 ≠ 零）。
func TestRunningCountsReportsSeededKeys(t *testing.T) {
	svc, facade := newRowsFixture(t)
	for key, count := range map[string]int{"squad/pro/cmd": 2, "carrier/cmd": 1} {
		body := []byte(`{"count":` + strconv.Itoa(count) + `}`)
		if _, err := facade.Put("sched_running", key, 0, body, "test"); err != nil {
			t.Fatalf("种子运行计数 %s: %v", key, err)
		}
	}
	counts, err := svc.RunningCounts()
	if err != nil {
		t.Fatalf("读运行计数: %v", err)
	}
	if len(counts) != 2 {
		t.Fatalf("运行计数行数 %d ≠ 2：%+v", len(counts), counts)
	}
	if counts["squad/pro/cmd"] != 2 || counts["carrier/cmd"] != 1 {
		t.Fatalf("运行计数不符: %+v", counts)
	}
	if _, present := counts["squad/absent/cmd"]; present {
		t.Fatalf("未种子的键不得出现（缺失≠零）: %+v", counts)
	}
}

// TestRunningCountsEmptyIsEmptyMap 锁：无任何占用行时返回空（非 nil）且无错误。
func TestRunningCountsEmptyIsEmptyMap(t *testing.T) {
	svc, _ := newRowsFixture(t)
	counts, err := svc.RunningCounts()
	if err != nil {
		t.Fatalf("读空运行计数: %v", err)
	}
	if counts == nil || len(counts) != 0 {
		t.Fatalf("空库应返回空非 nil map，得 %+v", counts)
	}
}
