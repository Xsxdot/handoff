// B435 O1：Facade 项目房间写读面（真 Store → Facade；新建/命中/哨兵映射；会话 → 项目读）。
package api

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB435FacadeEnsureProjectRoom(t *testing.T) {
	st, err := ledger.Open(filepath.Join(t.TempDir(), "project-room.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := New(st)
	room, created, err := f.EnsureProjectRoom("handoff", proto.SystemNotifierIdentity, "cli:u@h")
	if err != nil || !created {
		t.Fatalf("Facade 首次 ensure: created=%v err=%v", created, err)
	}
	if room.Owner != proto.SystemNotifierIdentity || len(room.Members) != 1 || len(room.Cards) != 0 {
		t.Fatalf("wire 会话应群主 = 系统身份、恰一名显式成员、零卡: %+v", room)
	}
	again, created, err := f.EnsureProjectRoom("handoff", proto.SystemNotifierIdentity, "cli:u@h")
	if err != nil || created || again.ID != room.ID {
		t.Fatalf("Facade 再次 ensure 应命中: %+v created=%v err=%v", again, created, err)
	}
	if p, err := f.SessionProject(room.ID); err != nil || p != "handoff" {
		t.Fatalf("Facade SessionProject: %q err=%v", p, err)
	}
	plain, err := f.CreateSession("普通", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := f.SessionProject(plain.ID); err != nil || p != "" {
		t.Fatalf("普通会话应无项目: %q err=%v", p, err)
	}
	if _, _, err := f.EnsureProjectRoom("", proto.SystemNotifierIdentity, "t"); !errors.Is(err, client.ErrBadState) {
		t.Fatalf("空项目名应映射 client.ErrBadState，得 %v", err)
	}
}
