// 会话（群）域账本存储的行为锁（B358 契约 §4.1 条 6/9/13）。只测 Store 公开
// 门面；facade 翻译与门面路径断言在 internal/collab/sessions_test.go。
//
// 本文件由 B358.1 协调者拍板 ① 显式扩进有界文件集：Store.ArchiveSession 的
// 已归档短路（幂等：重复归档不落第二条 EvSessionArchived）是契约条 6 冻结
// 语义，必须在 Store 层有能变红的测试——facade 层短路会造成 collab 测试绿而
// 账本契约继续破的假绿。
package ledger

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestArchiveSessionIdempotentLandsExactlyOneEvent 契约 §4.1 条 6：首次归档
// 落恰一条 EvSessionArchived；重复归档幂等 nil 且不再落事件、archived 位保持
// 真。不存在会话归档 → ErrNotFound 原样（不伪装幂等成功）。
func TestArchiveSessionIdempotentLandsExactlyOneEvent(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	session, err := st.CreateSession("幂等归档会话", "user:sy", "user:sy")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	if err := st.ArchiveSession(session.ID, "user:sy"); err != nil {
		t.Fatalf("首次归档: %v", err)
	}
	if err := st.ArchiveSession(session.ID, "user:sy"); err != nil {
		t.Fatalf("重复归档应幂等 nil: %v", err)
	}
	if err := st.ArchiveSession("session:999", "user:sy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在会话归档应 ErrNotFound: %v", err)
	}

	got, err := st.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Archived {
		t.Fatal("归档后 archived 位应为真")
	}
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range events {
		if ev.Type == EvSessionArchived {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("全生命周期应恰一条 EvSessionArchived（重复归档不叠加），got %d", n)
	}
}
