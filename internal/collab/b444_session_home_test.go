// b444_session_home_test.go —— B444 P2 会话门面的所属项目投影与改名入口（plan §5 A2.1/A2.5/A2.6 collab 腿；
// 缺陷族 R7/R10）。
//
// 观察点：真账本 + 真 collab 门面（newSessionFixture）。① 项目内会话的所属项目进列表与详情摘要，旧会话
// 为空；项目名非法零写入；② 改名只许群主或现驻场者：群主、push 驻场者成功，第三方 ErrNotWriter 且标题
// 不变；expect_title 不符 ErrTitleConflict；空标题 ErrSessionTitle；归档 ErrReadOnly；不存在 ErrNoRoom；
// 改名进详情 timeline（kind=renamed，Detail 带 from/title）。
package collab

import (
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB444SessionHomeProjectProjection(t *testing.T) {
	svc, _, _ := newSessionFixture(t)
	homed, err := svc.CreateSessionInProject("项目内", "user:o", "handoff", "user:o")
	if err != nil {
		t.Fatalf("建项目内会话: %v", err)
	}
	legacy, err := svc.CreateSession("旧会话", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := svc.ListSessions("")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]proto.SessionSummary{}
	for _, s := range summaries {
		got[s.ID] = s
	}
	if got[homed.ID].HomeProject != "handoff" || got[legacy.ID].HomeProject != "" {
		t.Fatalf("列表摘要所属项目不符: homed=%q legacy=%q", got[homed.ID].HomeProject, got[legacy.ID].HomeProject)
	}
	// 所属项目不是项目房间标记：project 键仍为空。
	if got[homed.ID].Project != "" {
		t.Fatalf("所属项目不得写进项目房间标记 project，得 %q", got[homed.ID].Project)
	}
	detail, err := svc.SessionDetail(homed.ID)
	if err != nil || detail.Summary.HomeProject != "handoff" {
		t.Fatalf("详情摘要所属项目: %q err=%v", detail.Summary.HomeProject, err)
	}

	before, _ := svc.ListSessions("")
	for _, bad := range []string{" ", "has space", "tab\tname"} {
		if _, err := svc.CreateSessionInProject("x", "user:o", bad, "user:o"); !errors.Is(err, ErrProjectName) {
			t.Fatalf("项目名 %q 应 ErrProjectName，得 %v", bad, err)
		}
	}
	after, _ := svc.ListSessions("")
	if len(after) != len(before) {
		t.Fatalf("项目名非法不得建会话：%d → %d", len(before), len(after))
	}
}

func TestB444RenameSessionPermissions(t *testing.T) {
	svc, _, _ := newSessionFixture(t)
	sess, err := svc.CreateSessionInProject(proto.SessionUntitledTitle, "user:o", "handoff", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{
		Session: sess.ID, Identity: "agent:coord", DriverMode: proto.ResidentDriverPush,
		PushCLI: "opencode", PushSession: "ses_1", Carrier: "coord-carrier", CarrierMachine: "linux-01",
	}); err != nil {
		t.Fatalf("坐 push 驻场者: %v", err)
	}
	title := func() string {
		t.Helper()
		got, err := svc.SessionDetail(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Summary.Title
	}

	// 第三方：拒且标题不变。
	if _, err := svc.RenameSession(sess.ID, "路人起名", "", "user:mallory"); !errors.Is(err, ErrNotWriter) {
		t.Fatalf("第三方改名应 ErrNotWriter，得 %v", err)
	}
	if got := title(); got != proto.SessionUntitledTitle {
		t.Fatalf("第三方改名被拒后标题应不变，得 %q", got)
	}
	// 驻场者按占位名 CAS 改名：成功。
	if _, err := svc.RenameSession(sess.ID, "排查登录", proto.SessionUntitledTitle, "agent:coord"); err != nil {
		t.Fatalf("驻场者 CAS 改名应成功: %v", err)
	}
	// 驻场者再按占位名 CAS：已不是占位名 → 冲突，标题保持。
	if _, err := svc.RenameSession(sess.ID, "又一个名", proto.SessionUntitledTitle, "agent:coord"); !errors.Is(err, ErrTitleConflict) {
		t.Fatalf("expect 不符应 ErrTitleConflict，得 %v", err)
	}
	// 群主改名：成功。
	if _, err := svc.RenameSession(sess.ID, "  群主定名  ", "", "user:o"); err != nil {
		t.Fatalf("群主改名应成功: %v", err)
	}
	if got := title(); got != "群主定名" {
		t.Fatalf("群主改名后标题应裁剪为「群主定名」，得 %q", got)
	}
	if _, err := svc.RenameSession(sess.ID, "   ", "", "user:o"); !errors.Is(err, ErrSessionTitle) {
		t.Fatalf("空标题应 ErrSessionTitle，得 %v", err)
	}
	if _, err := svc.RenameSession("session:999", "x", "", "user:o"); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("不存在会话应 ErrNoRoom，得 %v", err)
	}

	detail, err := svc.SessionDetail(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var renamed []proto.SessionTimelineEvent
	for _, ev := range detail.Timeline {
		if ev.Kind == proto.SessionEventRenamed {
			renamed = append(renamed, ev)
		}
	}
	if len(renamed) != 2 {
		t.Fatalf("timeline 应恰两条 renamed（驻场者、群主各一），得 %+v", renamed)
	}
	if renamed[0].Actor != "agent:coord" || !strings.Contains(renamed[0].Detail, `"from":"新会话"`) ||
		!strings.Contains(renamed[0].Detail, `"title":"排查登录"`) {
		t.Fatalf("首条 renamed 行形状不符: %+v", renamed[0])
	}

	if err := svc.ArchiveSession(sess.ID, "user:o"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RenameSession(sess.ID, "归档后", "", "user:o"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("归档会话改名应 ErrReadOnly，得 %v", err)
	}
}
