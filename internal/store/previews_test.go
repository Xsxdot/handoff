package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func newPreviewStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func previewRecord(id string, now time.Time) PreviewRecord {
	return PreviewRecord{
		Session: proto.PreviewSession{
			ID: id, EntryURL: "http://localhost:5173/app", Via: []string{"10.0.0.0/8"}, CWD: "/workspace",
			OriginURL: "https://example.test/repo", Branch: "feature/demo", CreatedAt: now.Add(-time.Minute), TTLSeconds: 7200,
		},
		Source:       PreviewSource{Kind: "port", Port: 5173, WorkspaceRoot: "/workspace", RelativePath: ""},
		LastActiveAt: now,
	}
}

func TestPreviewStoreRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)
	st := newPreviewStore(t)
	want := previewRecord("preview-1", now)
	if err := st.InsertPreview(want); err != nil {
		t.Fatalf("insert preview: %v", err)
	}
	got, err := st.GetPreview(want.Session.ID)
	if err != nil {
		t.Fatalf("get preview: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip mismatch: got=%+v want=%+v", got, want)
	}
}

func TestPreviewStoreActiveCloseTouchAndExpire(t *testing.T) {
	now := time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)
	st := newPreviewStore(t)
	active := previewRecord("active", now.Add(-time.Hour))
	closing := previewRecord("closing", now.Add(-time.Hour))
	old := previewRecord("old", now.Add(-3*time.Hour))
	for _, row := range []PreviewRecord{active, closing, old} {
		if err := st.InsertPreview(row); err != nil {
			t.Fatalf("insert %s: %v", row.Session.ID, err)
		}
	}
	if _, ok, err := st.ClosePreview("closing", now); err != nil || !ok {
		t.Fatalf("first close: ok=%v err=%v", ok, err)
	}
	if _, ok, err := st.ClosePreview("closing", now.Add(time.Second)); err != nil || ok {
		t.Fatalf("second close: ok=%v err=%v", ok, err)
	}
	if ok, err := st.TouchPreview("closing", now.Add(time.Second)); err != nil || ok {
		t.Fatalf("touch closed: ok=%v err=%v", ok, err)
	}
	rows, err := st.ListActivePreviews(now)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(rows) != 1 || rows[0].Session.ID != "active" {
		t.Fatalf("active rows=%v", rows)
	}
	expired, err := st.ExpirePreviews(now)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if len(expired) != 1 || expired[0].Session.ID != "old" {
		t.Fatalf("expired=%v", expired)
	}
	expired, err = st.ExpirePreviews(now)
	if err != nil {
		t.Fatalf("expire second: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("second expire=%v", expired)
	}
}

func TestPreviewStoreUpdateEntry(t *testing.T) {
	now := time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)
	st := newPreviewStore(t)
	row := previewRecord("preview-entry", now)
	if err := st.InsertPreview(row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.UpdatePreviewEntry(row.Session.ID, "http://localhost:4021/index.html"); err != nil {
		t.Fatalf("update entry: %v", err)
	}
	got, err := st.GetPreview(row.Session.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Session.EntryURL != "http://localhost:4021/index.html" {
		t.Fatalf("entry=%q", got.Session.EntryURL)
	}
}
