package ledger

import (
	"testing"
	"time"
)

func TestAppendMirroredEventIdempotent(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "挂账卡")
	if err := s.LinkTask(c.ID, "mac-02", "T1", "implement", "test"); err != nil {
		t.Fatalf("link: %v", err)
	}
	ev := MirroredEvent{Target: "mac-02", Task: "T1", SourceSeq: 7,
		Type: "completed", Payload: []byte(`{"x":1}`), CreatedAt: time.Now()}
	wrote, err := s.AppendMirroredEvent(c.ID, ev)
	if err != nil || !wrote {
		t.Fatalf("首写: %v %v", err, wrote)
	}
	wrote, err = s.AppendMirroredEvent(c.ID, ev)
	if err != nil || wrote {
		t.Fatalf("重放应静默跳过: %v %v", err, wrote)
	}
	evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
	mirrored := 0
	for _, e := range evs {
		if e.Type == EvTaskMirrored {
			mirrored++
			if e.SourceTarget != "mac-02" || e.SourceSeq != 7 {
				t.Fatalf("来源三元组: %+v", e)
			}
		}
	}
	if mirrored != 1 {
		t.Fatalf("镜像事件应恰一条: %d", mirrored)
	}
	wm, err := s.MirrorWatermark("mac-02", "T1")
	if err != nil || wm != 7 {
		t.Fatalf("watermark: %v %d", err, wm)
	}
	if wm, _ := s.MirrorWatermark("mac-02", "没镜像过"); wm != 0 {
		t.Fatalf("空 watermark 应为 0: %d", wm)
	}
}

func TestMirrorLease(t *testing.T) {
	s := seedStore(t)
	ttl := 200 * time.Millisecond
	got, err := s.AcquireMirrorLease("coordA", ttl)
	if err != nil || !got {
		t.Fatalf("A 首取: %v %v", err, got)
	}
	if got, _ := s.AcquireMirrorLease("coordB", ttl); got {
		t.Fatal("B 不应抢到")
	}
	if got, _ := s.AcquireMirrorLease("coordA", ttl); !got {
		t.Fatal("A 续约应成功")
	}
	time.Sleep(ttl + 50*time.Millisecond)
	if got, _ := s.AcquireMirrorLease("coordB", ttl); !got {
		t.Fatal("过期后 B 应接任")
	}
	if got, _ := s.AcquireMirrorLease("coordA", ttl); got {
		t.Fatal("A 丢 lease 后不应立刻拿回")
	}
}

func TestMirrorHealth(t *testing.T) {
	s := seedStore(t)
	if err := s.TouchMirrorHealth("mac-02", 42); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := s.TouchMirrorHealth("mac-02", 0); err != nil {
		t.Fatalf("touch2: %v", err)
	}
	rows, err := s.MirrorHealth()
	if err != nil || len(rows) != 1 || rows[0].LastSeq != 42 || rows[0].UpdatedAt.IsZero() {
		t.Fatalf("health: %v %+v", err, rows)
	}
}
