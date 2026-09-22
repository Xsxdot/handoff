// b393_wakeround_event_test.go —— B393 唤醒回合事件 payload 的序列化边界锁。
//
// 职责：锁 WakeRoundEvent 的 encode∘decode 恒等，且「字段缺失」与「值为零」可区分
// （可空指针）——该 payload 经 json.Marshal 写进 ledger.EnsureComment 的 body。
// 缝：内部锁（未导出类型 WakeRoundEvent 的序列化边界）；见 plan T2.3 的序列化边界条目。
// 边界：纯编解码，不落账本、不碰运行态。
package agentd

import (
	"encoding/json"
	"testing"
)

// TestB393WakeRoundEventRoundTrip 锁序列化边界：encode∘decode 恒等，且
// 「字段缺失」与「值为零」可区分（可空指针）。
func TestB393WakeRoundEventRoundTrip(t *testing.T) {
	zero := ""
	var zeroMs int64 = 0
	cases := []WakeRoundEvent{
		{Phase: "start"},                                // 全缺省
		{Phase: "end", Session: &zero, DurationMs: &zeroMs}, // 显式零值
		{Phase: "fail", Class: &zero, Err: &zero},       // class 显式零值
	}
	for _, want := range cases {
		raw, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		var got WakeRoundEvent
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Phase != want.Phase ||
			(got.Class == nil) != (want.Class == nil) ||
			(got.Session == nil) != (want.Session == nil) ||
			(got.Err == nil) != (want.Err == nil) ||
			(got.DurationMs == nil) != (want.DurationMs == nil) {
			t.Fatalf("round-trip 不等：\n got=%+v\nwant=%+v", got, want)
		}
		if want.Class != nil && *got.Class != *want.Class {
			t.Fatalf("class 零值字段丢失：got=%q", *got.Class)
		}
		if want.Session != nil && *got.Session != *want.Session {
			t.Fatalf("零值字段丢失：got=%q", *got.Session)
		}
	}
}
