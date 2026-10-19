// B435 O1 项目房间绑定（D6、C-513；plan Q1/Q2/Q11/Q12 甲）：每项目恰一场、每场至多一个项目，
// 库层兑现（project 主键 + session 唯一）；惰性创建在单事务内「有绑定即返回，无则建会话 + 写绑定」。
// 夹具：真 SQLite 账本。PG 孪生见 b435_project_room_pg_test.go。
package ledger

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const b435RoomOwner = "agent:handoff" // 群主由调用方给（collab 传 proto.SystemNotifierIdentity）；本包不引 proto 常量

func b435Count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(s.q(query), args...).Scan(&n); err != nil {
		t.Fatalf("计数 %q: %v", query, err)
	}
	return n
}

func TestB435EnsureProjectRoomCreatesOnce(t *testing.T) {
	s := newTestStore(t)
	first, created, err := s.EnsureProjectRoom("handoff", b435RoomOwner, "cli:u@h")
	if err != nil {
		t.Fatalf("首次 ensure: %v", err)
	}
	if !created {
		t.Fatal("首次 ensure 应 created=true")
	}
	if first.Owner != b435RoomOwner || len(first.Members) != 1 || first.Members[0] != b435RoomOwner {
		t.Fatalf("群主与成员应恰为入参 owner: %+v", first)
	}
	if len(first.Cards) != 0 {
		t.Fatalf("项目房间创建时零卡（Q12 甲），得 %v", first.Cards)
	}
	again, created, err := s.EnsureProjectRoom("handoff", "agent:other", "cli:u@h")
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("再次 ensure 应命中同一会话且 created=false: id=%s created=%v err=%v", again.ID, created, err)
	}
	if again.Owner != b435RoomOwner {
		t.Fatalf("命中既有绑定不得改群主: %+v", again)
	}
	id, ok, err := s.ProjectRoom("handoff")
	if err != nil || !ok || id != first.ID {
		t.Fatalf("ProjectRoom 读回: id=%s ok=%v err=%v", id, ok, err)
	}
	project, err := s.SessionProject(first.ID)
	if err != nil || project != "handoff" {
		t.Fatalf("SessionProject 读回: %q err=%v", project, err)
	}
	if _, ok, err := s.ProjectRoom("nobody"); err != nil || ok {
		t.Fatalf("无绑定项目应 ok=false、err=nil: ok=%v err=%v", ok, err)
	}
	plain, err := s.CreateSession("普通会话", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	if project, err := s.SessionProject(plain.ID); err != nil || project != "" {
		t.Fatalf("普通会话应无项目: %q err=%v", project, err)
	}
	if n := b435Count(t, s, `SELECT COUNT(*) FROM card_events WHERE type = ?`, EvSessionCreated); n != 2 {
		t.Fatalf("应只落两条 session_created（项目房间一次 + 普通会话一次），得 %d", n)
	}
}

// H1：两个 Store 句柄（两条独立连接，模拟两个 CLI 进程）上 N 个 goroutine 并发 ensure 同一项目 →
// 全部返回同一会话、恰一个 created=true、绑定恰一行、会话只多一行。
func TestB435EnsureProjectRoomConcurrentExactlyOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	const n = 8
	ids := make([]string, n)
	createds := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := a
			if i%2 == 1 {
				st = b
			}
			<-start
			var session Session
			session, createds[i], errs[i] = st.EnsureProjectRoom("handoff", b435RoomOwner, "cli:u@h")
			ids[i] = session.ID
		}(i)
	}
	close(start)
	wg.Wait()
	created := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d 应成功，得 %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("全部应返回同一会话: %v", ids)
		}
		if createds[i] {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("恰一个 created=true，得 %d", created)
	}
	if rows := b435Count(t, a, `SELECT COUNT(*) FROM project_rooms`); rows != 1 {
		t.Fatalf("绑定恰一行，得 %d", rows)
	}
	if rows := b435Count(t, a, `SELECT COUNT(*) FROM sessions`); rows != 1 {
		t.Fatalf("会话只多一行，得 %d", rows)
	}
}

// C-513：两项目两场；反面——直写第二行把同一会话绑给另一项目，被唯一约束拒绝。
func TestB435ProjectRoomDistinctProjects(t *testing.T) {
	s := newTestStore(t)
	x, _, err := s.EnsureProjectRoom("px", b435RoomOwner, "t")
	if err != nil {
		t.Fatal(err)
	}
	y, _, err := s.EnsureProjectRoom("py", b435RoomOwner, "t")
	if err != nil {
		t.Fatal(err)
	}
	if x.ID == y.ID {
		t.Fatalf("两项目不得共享一场: %s", x.ID)
	}
	_, err = s.db.Exec(s.q(`INSERT INTO project_rooms (project, session, created_at) VALUES (?, ?, ?)`),
		"pz", x.ID, s.tval(time.Now()))
	if err == nil {
		t.Fatal("同一会话绑第二个项目应被唯一约束拒绝")
	}
	if p, err := s.SessionProject(x.ID); err != nil || p != "px" {
		t.Fatalf("被拒后绑定不变: %q err=%v", p, err)
	}
}

// Q11 甲：项目房间归档后绑定不变，ensure 不新建。
func TestB435ProjectRoomArchivedStaysBound(t *testing.T) {
	s := newTestStore(t)
	room, _, err := s.EnsureProjectRoom("handoff", b435RoomOwner, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveSession(room.ID, "t"); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := s.ProjectRoom("handoff"); err != nil || !ok || id != room.ID {
		t.Fatalf("归档后 ProjectRoom 仍返回该场: id=%s ok=%v err=%v", id, ok, err)
	}
	again, created, err := s.EnsureProjectRoom("handoff", b435RoomOwner, "t")
	if err != nil || created || again.ID != room.ID || !again.Archived {
		t.Fatalf("归档后 ensure 应命中归档场: %+v created=%v err=%v", again, created, err)
	}
}

// 绑定写失败（库层注入）→ 整个事务回滚：不留孤儿会话行、不落 session_created、会话号水位不动。
func TestB435ProjectRoomFailureRollsBack(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER b435_inject BEFORE INSERT ON project_rooms
		BEGIN SELECT RAISE(ABORT, 'b435 注入绑定写失败'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnsureProjectRoom("handoff", b435RoomOwner, "t"); err == nil {
		t.Fatal("绑定写失败应返回错误")
	}
	if n := b435Count(t, s, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Fatalf("不得留下孤儿会话行，得 %d", n)
	}
	if n := b435Count(t, s, `SELECT COUNT(*) FROM card_events WHERE type = ?`, EvSessionCreated); n != 0 {
		t.Fatalf("不得落 session_created，得 %d", n)
	}
	if _, err := s.db.Exec(`DROP TRIGGER b435_inject`); err != nil {
		t.Fatal(err)
	}
	room, created, err := s.EnsureProjectRoom("handoff", b435RoomOwner, "t")
	if err != nil || !created || room.ID != "session:1" {
		t.Fatalf("恢复后应建出 session:1（水位未被失败事务推进）: %+v created=%v err=%v", room, created, err)
	}
}

func TestB435EnsureProjectRoomRejectsEmptyArgs(t *testing.T) {
	s := newTestStore(t)
	for _, tc := range [][2]string{{"", b435RoomOwner}, {"handoff", ""}} {
		if _, _, err := s.EnsureProjectRoom(tc[0], tc[1], "t"); !errors.Is(err, ErrBadState) {
			t.Fatalf("入参 %q 应 ErrBadState，得 %v", tc, err)
		}
	}
	if n := b435Count(t, s, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Fatalf("被拒不得写会话，得 %d", n)
	}
}
