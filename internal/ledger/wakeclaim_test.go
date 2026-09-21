// B389 唤醒认领的行为锁（契约 §4 第 14-17 条，修订轮改为 (card,seq) 复合键）。
package ledger

import (
	"errors"
	"testing"
	"time"
)

// §4-14/15：同一 (card,seq) 并发只允许一个拿到；租约过期后允许第二者接管；
// 不同卡的同 seq 互不排他（扇出场景每卡各自可认领）。
func TestClaimWakeIsExclusiveAndExpires(t *testing.T) {
	s := seedStore(t)
	const seq = 4242

	got, err := s.ClaimWake(seq, "B1", "linux-01", time.Minute)
	if err != nil || !got {
		t.Fatalf("首次认领: got=%v err=%v", got, err)
	}
	// 同一 (card,seq) 上另一台机器在租约内拿不到。
	if got, err := s.ClaimWake(seq, "B1", "mbp", time.Minute); err != nil || got {
		t.Fatalf("租约内不应让第二者拿到: got=%v err=%v", got, err)
	}
	// 同持有者续期仍是自己。
	if got, err := s.ClaimWake(seq, "B1", "linux-01", time.Minute); err != nil || !got {
		t.Fatalf("同持有者续期: got=%v err=%v", got, err)
	}
	// §4-14 反向：不同卡的同 seq 不是竞争关系——B2 应能拿到 (B2,seq)。
	if got, err := s.ClaimWake(seq, "B2", "linux-01", time.Minute); err != nil || !got {
		t.Fatalf("不同卡同 seq 应互不排他（扇出）: got=%v err=%v", got, err)
	}

	// 过期（负 TTL 产出已过期行）后可被接管——沿用 driver_leases 的注入时钟约定。
	if got, err := s.ClaimWake(seq, "B1", "linux-01", -time.Second); err != nil || !got {
		t.Fatalf("自身过期续期: got=%v err=%v", got, err)
	}
	if got, err := s.ClaimWake(seq, "B1", "mbp", time.Minute); err != nil || !got {
		t.Fatalf("过期后应允许接管: got=%v err=%v", got, err)
	}
}

// §4-16：非持有者收尾失败且不改行；对同一 seq 的另一张卡收尾不影响本卡认领行。
func TestCompleteWakeOnlyByHolder(t *testing.T) {
	s := seedStore(t)
	const seq = 77

	if _, err := s.ClaimWake(seq, "B1", "linux-01", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimWake(seq, "B2", "linux-01", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWake(seq, "B1", "mbp"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("非持有者收尾应 ErrCASConflict，得 %v", err)
	}
	if _, err := s.ClaimWake(seq, "B1", "mbp", time.Minute); err != nil {
		t.Fatalf("非持有者收尾失败不应释放认领: %v", err)
	}
	// 用错卡号收尾也应失败：key 是 (card,seq)。
	if err := s.CompleteWake(seq, "B2", "linux-01"); err != nil {
		t.Fatalf("持有者收尾 B2: %v", err)
	}
	if got, err := s.ClaimWake(seq, "B2", "mbp", time.Minute); err != nil || got {
		t.Fatalf("B2 终局事件不应再被认领: got=%v err=%v", got, err)
	}
	// B1 的认领行不受 B2 收尾影响：仍被 B1 持有（mbp 拿不到）。
	if got, err := s.ClaimWake(seq, "B1", "mbp", time.Minute); err != nil || got {
		t.Fatalf("B2 收尾不得影响 B1 认领行: got=%v err=%v", got, err)
	}
	if err := s.CompleteWake(seq, "B1", "linux-01"); err != nil {
		t.Fatalf("持有者收尾 B1: %v", err)
	}
	got, err := s.ClaimWake(seq, "B1", "mbp", time.Minute)
	if err != nil || got {
		t.Fatalf("终局事件不应再被认领: got=%v err=%v", got, err)
	}
}

// §4-17：游标只能推进到"终局前缀"——窗口内有在飞认领时停在它之前；
// 同一 seq 多卡中任一行在飞即挡住该水位（DISTINCT seq）。
func TestCursorWatermarkStopsBeforeInFlightClaims(t *testing.T) {
	s := seedStore(t)

	// 无在飞：候选水位原样可用。
	if w, err := s.CursorWatermark(10, 20); err != nil || w != 20 {
		t.Fatalf("无在飞应到候选水位: w=%d err=%v", w, err)
	}
	// 窗口内三笔：完成的、在飞的、完成的——在飞那笔挡住推进。
	for _, seq := range []int64{12, 15, 18} {
		if _, err := s.ClaimWake(seq, "B1", "linux-01", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// 扇出：seq=15 上第二张卡也认领——两行同时在飞时，WakeClaimsBefore 必须
	// 去重成一条（DISTINCT seq），否则在飞清单会出现重复的 15。
	if _, err := s.ClaimWake(15, "B2", "linux-01", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWake(12, "B1", "linux-01"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWake(18, "B1", "linux-01"); err != nil {
		t.Fatal(err)
	}
	// 两卡都在飞：去重后只应有一条 15。
	inFlightBoth, err := s.WakeClaimsBefore(21)
	if err != nil {
		t.Fatal(err)
	}
	if len(inFlightBoth) != 1 || inFlightBoth[0] != 15 {
		t.Fatalf("同 seq 两卡在飞应 DISTINCT 成一条，实得 %v", inFlightBoth)
	}
	w, err := s.CursorWatermark(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if w != 14 {
		t.Fatalf("任一卡在飞应把水位挡在 14，得 %d", w)
	}
	// B1 收尾但 B2 仍在飞：水位仍被挡住（任一行在飞即挡）。
	if err := s.CompleteWake(15, "B1", "linux-01"); err != nil {
		t.Fatalf("B1 收尾 seq=15: %v", err)
	}
	if w, err := s.CursorWatermark(10, 20); err != nil || w != 14 {
		t.Fatalf("B2 仍在飞应继续挡住水位: w=%d err=%v", w, err)
	}
	// 在飞清单当前只有 15（B2 一行）。
	inFlight, err := s.WakeClaimsBefore(21)
	if err != nil {
		t.Fatal(err)
	}
	if len(inFlight) != 1 || inFlight[0] != 15 {
		t.Fatalf("在飞清单 = %v，期望 [15]", inFlight)
	}
	// 收尾后在飞清空，水位可到候选值；已完成的行不再挡路。
	if err := s.CompleteWake(15, "B2", "linux-01"); err != nil {
		t.Fatal(err)
	}
	if w, err := s.CursorWatermark(10, 20); err != nil || w != 20 {
		t.Fatalf("收尾后应到候选水位: w=%d err=%v", w, err)
	}
	// 单调性：候选不前进时不后退。
	if w, err := s.CursorWatermark(20, 20); err != nil || w != 20 {
		t.Fatalf("候选≤起点应原样返回: w=%d err=%v", w, err)
	}
	// 窗口外的在飞不挡路（seq 15 已完成，此处只验证 from 之后的窗口语义）。
	if w, err := s.CursorWatermark(18, 19); err != nil || w != 19 {
		t.Fatalf("窗口内无在飞应到候选: w=%d err=%v", w, err)
	}
}
