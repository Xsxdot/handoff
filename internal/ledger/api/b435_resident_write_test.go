// B435 R：Facade 驻场者写面（真 Store → Facade → 会话侧哨兵映射；proto.SessionResident 两种键集往返）。
package api

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func b435ResidentKeys(t *testing.T, r proto.SessionResident) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func TestB435FacadeSitStandResident(t *testing.T) {
	st, err := ledger.Open(filepath.Join(t.TempDir(), "resident.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lc := New(st)
	full, err := st.CreateSession("全量", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	bare, err := st.CreateSession("无桌面端", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}

	got, err := lc.SitResident(proto.SessionResident{Session: full.ID, Identity: "user:boss",
		DesktopSession: "local_abc", CLISession: "cli-1", Machine: "mac-pro"})
	if err != nil {
		t.Fatalf("Facade sit: %v", err)
	}
	if got.Session != full.ID || got.Identity != "user:boss" || got.DesktopSession != "local_abc" ||
		got.CLISession != "cli-1" || got.Machine != "mac-pro" || got.SatAt.IsZero() {
		t.Fatalf("sit 返回不符: %+v", got)
	}
	if keys := b435ResidentKeys(t, got); keys != "cli_session,desktop_session,identity,machine,sat_at,session" {
		t.Fatalf("全量键集不符（C-406）: %s", keys)
	}
	read, ok, err := lc.SessionResident(full.ID)
	if err != nil || !ok || !read.SatAt.Equal(got.SatAt) || read.Identity != got.Identity || read.CLISession != got.CLISession {
		t.Fatalf("读回不符: %+v ok=%v err=%v", read, ok, err)
	}
	gotBare, err := lc.SitResident(proto.SessionResident{Session: bare.ID, Identity: "agent:org"})
	if err != nil {
		t.Fatal(err)
	}
	if keys := b435ResidentKeys(t, gotBare); keys != "identity,sat_at,session" {
		t.Fatalf("无桌面端键集不符（C-406）: %s", keys)
	}

	if _, err := lc.SitResident(proto.SessionResident{Session: full.ID, Identity: "agent:x"}); !errors.Is(err, client.ErrResidentOccupied) {
		t.Fatalf("第二次 sit 应映射 client.ErrResidentOccupied，得 %v", err)
	}
	if _, err := lc.SitResident(proto.SessionResident{Session: "session:999", Identity: "user:r"}); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("会话不存在应映射 client.ErrNotFound，得 %v", err)
	}
	if err := st.ArchiveSession(bare.ID, "user:o"); err != nil {
		t.Fatal(err)
	}
	if err := lc.StandResident(bare.ID, "agent:org"); err != nil {
		t.Fatalf("归档会话仍可请下驻场者: %v", err)
	}
	if _, err := lc.SitResident(proto.SessionResident{Session: bare.ID, Identity: "user:r"}); !errors.Is(err, client.ErrBadState) {
		t.Fatalf("已归档应映射 client.ErrBadState，得 %v", err)
	}
	if err := lc.StandResident(full.ID, "agent:x"); !errors.Is(err, client.ErrResidentNotSeated) {
		t.Fatalf("身份不符 stand 应映射 client.ErrResidentNotSeated，得 %v", err)
	}
	if err := lc.StandResident(full.ID, "user:boss"); err != nil {
		t.Fatalf("stand: %v", err)
	}
	if _, ok, err := lc.SessionResident(full.ID); ok || err != nil {
		t.Fatalf("stand 后应 ok=false: %v %v", ok, err)
	}
}

// B444 P2 返工三 裁决 2：驻场写面撞卡席位会话时，Facade 把账本 ErrResidentSeatConflict 翻成
// client.ErrResidentSeatConflict（SitResident 与 SetResidentPushSession 两条），原文案（点名卡）保留。
func TestB444FacadeResidentSeatConflictTranslated(t *testing.T) {
	st, lc := newFacadeStore(t)
	mustBugWorkflow(t, st)
	card, err := st.CreateCard(ledger.NewCard{Title: "席位卡", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeatMode(card.ID, "cli:opencode#ses_seat", proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession("驻场", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	push := proto.SessionResident{Session: session.ID, Identity: "agent:coord", DriverMode: proto.ResidentDriverPush,
		PushCLI: "opencode", PushSession: "ses_seat", Carrier: "c1", CarrierMachine: "linux-01"}
	if _, err := lc.SitResident(push); !errors.Is(err, client.ErrResidentSeatConflict) || !strings.Contains(err.Error(), card.ID) {
		t.Fatalf("SitResident 撞卡席位应映射 client.ErrResidentSeatConflict 且点名卡 %s，得 %v", card.ID, err)
	}
	push.PushSession = ""
	if _, err := lc.SitResident(push); err != nil {
		t.Fatalf("首轮在途行应能坐下: %v", err)
	}
	if err := lc.SetResidentPushSession(session.ID, "agent:coord", "ses_seat"); !errors.Is(err, client.ErrResidentSeatConflict) {
		t.Fatalf("SetResidentPushSession 撞卡席位应映射 client.ErrResidentSeatConflict，得 %v", err)
	}
}
