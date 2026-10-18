// B435 O1 项目房间绑定的 PG 孪生（D6、C-513）。默认 skip；设 LEDGER_TEST_PG_DSN 后启用（真机 O1-5）。
// 项目名带纳秒后缀避免与共享库既有数据相撞；结束时删掉自己的绑定行（会话行留存，同 R 孪生口径）。
package ledger

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestB435ProjectRoomPG(t *testing.T) {
	s := newPGStore(t)
	project := fmt.Sprintf("b435-pg-%d", time.Now().UnixNano())
	other := project + "-other"
	t.Cleanup(func() {
		_, _ = s.db.Exec(s.q(`DELETE FROM project_rooms WHERE project = ? OR project = ?`), project, other)
	})

	// 创建 + 命中。
	first, created, err := s.EnsureProjectRoom(project, b435RoomOwner, "t")
	if err != nil || !created {
		t.Fatalf("PG 首次 ensure: created=%v err=%v", created, err)
	}
	if again, created, err := s.EnsureProjectRoom(project, b435RoomOwner, "t"); err != nil || created || again.ID != first.ID {
		t.Fatalf("PG 再次 ensure 应命中: %+v created=%v err=%v", again, created, err)
	}
	if p, err := s.SessionProject(first.ID); err != nil || p != project {
		t.Fatalf("PG SessionProject: %q err=%v", p, err)
	}
	// 唯一：同一会话不得再绑另一项目。
	if _, err := s.db.Exec(s.q(`INSERT INTO project_rooms (project, session, created_at) VALUES (?, ?, ?)`),
		other, first.ID, s.tval(time.Now())); err == nil {
		t.Fatal("PG 同一会话绑第二个项目应被唯一约束拒绝")
	}
	// 并发：另一项目 N 路并发 ensure → 同一会话、恰一个 created。
	const n = 6
	ids := make([]string, n)
	createds := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var session Session
			session, createds[i], errs[i] = s.EnsureProjectRoom(other, b435RoomOwner, "t")
			ids[i] = session.ID
		}(i)
	}
	wg.Wait()
	created = false
	count := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("PG goroutine %d: id=%s err=%v（全部 %v）", i, ids[i], errs[i], ids)
		}
		if createds[i] {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("PG 并发恰一个 created，得 %d", count)
	}
}
