package sessdir

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
)

func sampleMeta(id string) Meta {
	return Meta{
		ID: id, BasePath: "/repo/a", BaseKind: "workspace", Cwd: "/repo/a",
		Shell: "/bin/zsh", CreatedAt: time.Unix(1755648000, 0).UTC(),
		PID: 4242, ProtoVersion: 1,
	}
}

// TestMetaRoundTrip 元数据写读往返。
func TestMetaRoundTrip(t *testing.T) {
	root := t.TempDir()
	m := sampleMeta("s1")
	if err := Create(root, m.ID); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := WriteMeta(root, m); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	got, err := ReadMeta(root, m.ID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if got != m {
		t.Fatalf("读回 = %+v，期望 %+v", got, m)
	}
	di, err := os.Stat(Dir(root, m.ID))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("目录权限 = %o，期望 0700", di.Mode().Perm())
	}
	fi, err := os.Stat(MetaPath(root, m.ID))
	if err != nil {
		t.Fatalf("stat meta: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("meta 权限 = %o，期望 0600", fi.Mode().Perm())
	}
}

// TestScanThreeStates 三态各来一个：活着、已死、meta 坏但锁还占着。
func TestScanThreeStates(t *testing.T) {
	if !prochost.LockSupported() {
		t.Skip("本平台不支持文件锁，判活语义不成立")
	}
	root := t.TempDir()

	live := sampleMeta("live")
	if err := Create(root, live.ID); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(root, live); err != nil {
		t.Fatal(err)
	}
	lk, err := prochost.AcquireLock(LockPath(root, live.ID))
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = lk.Release() })

	dead := sampleMeta("dead")
	if err := Create(root, dead.ID); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(root, dead); err != nil {
		t.Fatal(err)
	}

	if err := Create(root, "broken"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MetaPath(root, "broken"), []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	lk2, err := prochost.AcquireLock(LockPath(root, "broken"))
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = lk2.Release() })

	entries, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	byID := map[string]Entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	if len(byID) != 3 {
		t.Fatalf("扫到 %d 条，期望 3：%+v", len(byID), entries)
	}
	if byID["live"].State != StateLive || byID["live"].Meta != live {
		t.Fatalf("live = %+v", byID["live"])
	}
	if byID["dead"].State != StateDead {
		t.Fatalf("dead = %+v", byID["dead"])
	}
	if byID["broken"].State != StateBroken || byID["broken"].Err == nil {
		t.Fatalf("broken = %+v", byID["broken"])
	}
}

// TestScanDoesNotDelete Scan 不删任何东西——删是调用方的事。
func TestScanDoesNotDelete(t *testing.T) {
	root := t.TempDir()
	m := sampleMeta("dead")
	if err := Create(root, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(root); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if _, err := os.Stat(Dir(root, m.ID)); err != nil {
		t.Fatalf("Scan 不该删目录: %v", err)
	}
}

// TestScanMissingRoot 根目录不存在时给空结果，不报错——首次启动就是这样。
func TestScanMissingRoot(t *testing.T) {
	entries, err := Scan(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v，期望空", entries)
	}
}

// TestScanIgnoresStrayFiles 根目录下的散文件不是会话，跳过。
func TestScanIgnoresStrayFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v，期望空", entries)
	}
}

// TestRemoveIsIdempotent 删不存在的会话不报错。
func TestRemoveIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := Remove(root, "never-existed"); err != nil {
		t.Fatalf("Remove 应幂等: %v", err)
	}
}

// TestCheckSockPath 路径过长要在 bind 之前给出可读错误。
func TestCheckSockPath(t *testing.T) {
	if err := CheckSockPath("/Users/dev/.handoff/ptys", "7ec762e7-3bd2-412c-a39c-e4cf8b4057ad"); err != nil {
		t.Fatalf("正常路径不该被拒: %v", err)
	}
	long := filepath.Join("/tmp", string(make([]byte, 200)))
	err := CheckSockPath(long, "s1")
	if err == nil {
		t.Fatal("超长路径应被拒")
	}
}
