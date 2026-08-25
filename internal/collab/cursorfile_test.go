// 游标文件介质测试（B156.2 C5，移交区 A.1 岔口四方案甲）：tmp+rename 原子
// 写、并发压测无交错损坏、跨实例持久化、串行单调只进不退。写入口经
// Service.MarkRead（接缝 #1），断言经 Service.Unread 或直接读游标水位；
// 文件介质的注入经 SetCursorStore（组装点同形接线）。
package collab

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/cursor"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestCursorFilePersistsAcrossInstances 文件介质跨实例持久化：MarkRead 后新
// Service + 新 Store 同路径重开，水位仍在（重启/换实例不丢已读）。
func TestCursorFilePersistsAcrossInstances(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	seq, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cursors.json")
	svc.SetCursorStore(cursor.New(path))
	if err := svc.MarkRead("user:sy", card.ID, seq); err != nil {
		t.Fatal(err)
	}
	svc2 := New(ledgerapi.New(st))
	svc2.SetCursorStore(cursor.New(path))
	if n, err := svc2.Unread("user:sy", card.ID); err != nil || n != 0 {
		t.Fatalf("持久化游标应生效: %v %d", err, n)
	}
}

// TestCursorConcurrentMarkRead 并发 MarkRead 无交错损坏 + 单调水位：50 个
// goroutine 各写一个消息 seq，最终水位取最大（只进不退）、文件仍可被新实例
// 完整解析（tmp+rename 原子性证明）。
func TestCursorConcurrentMarkRead(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	seqs := make([]int64, 0, 50)
	for i := 0; i < 50; i++ {
		seq, err := svc.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "user:sy")
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	path := filepath.Join(t.TempDir(), "cursors.json")
	svc.SetCursorStore(cursor.New(path))

	var wg sync.WaitGroup
	for _, s := range seqs {
		wg.Add(1)
		go func(seq int64) {
			defer wg.Done()
			if err := svc.MarkRead("user:sy", card.ID, seq); err != nil {
				t.Errorf("MarkRead(%d): %v", seq, err)
			}
		}(s)
	}
	wg.Wait()

	if n, err := svc.Unread("user:sy", card.ID); err != nil || n != 0 {
		t.Fatalf("单调水位应取最大 seq，未读应 0: %v %d", err, n)
	}
	svc2 := New(ledgerapi.New(st))
	svc2.SetCursorStore(cursor.New(path))
	if n, err := svc2.Unread("user:sy", card.ID); err != nil || n != 0 {
		t.Fatalf("并发写后文件应完好且水位=最大 seq: %v %d", err, n)
	}
}

// TestCursorMarkReadMonotonicSerial 串行确定性锁定 cursor.Store.MarkRead 的
// 「单调只进不退」：先 MarkRead(10) 再 MarkRead(5)，水位必须仍为 10——落后
// 请求不得把已读水位拉回。并发形态（TestCursorConcurrentMarkRead）靠调度凑
// 巧，去掉单调 max 后约 2% 的跑法假绿；本测试把该规则变成确定性。
func TestCursorMarkReadMonotonicSerial(t *testing.T) {
	svc, st := newFixture(t)
	card := mustAnyCard(t, svc, st)
	path := filepath.Join(t.TempDir(), "cursors.json")
	svc.SetCursorStore(cursor.New(path))

	if err := svc.MarkRead("user:sy", card.ID, 10); err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkRead("user:sy", card.ID, 5); err != nil {
		t.Fatal(err)
	}
	got, err := svc.cursor.Cursor("user:sy", card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Fatalf("单调只进不退被破坏：got %d want 10", got)
	}
}

// TestConsumedEventTypeLiteralMatchesLedger 钉住 room.ConsumedEventType 与
// 账本 EvMessageConsumed 的等式（内部锁，理由见 §7）。
func TestConsumedEventTypeLiteralMatchesLedger(t *testing.T) {
	if room.ConsumedEventType != ledger.EvMessageConsumed {
		t.Fatalf("消费事件类型字面量漂移: %q != %q", room.ConsumedEventType, ledger.EvMessageConsumed)
	}
}
