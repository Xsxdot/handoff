package dropdir

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPutNewFile(t *testing.T) {
	home := t.TempDir()
	path, n, err := Put(home, "photo.png", bytes.NewReader([]byte("png")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := filepath.Join(home, ".handoff", "drop", "photo.png")
	if path != want {
		t.Fatalf("path=%q want %q", path, want)
	}
	if n != 3 {
		t.Fatalf("bytes=%d want 3", n)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "png" {
		t.Fatalf("content=%q", got)
	}
}

func TestPutSameNameGetsSuffix(t *testing.T) {
	home := t.TempDir()
	if _, _, err := Put(home, "photo.png", bytes.NewReader([]byte("a"))); err != nil {
		t.Fatal(err)
	}
	path, _, err := Put(home, "photo.png", bytes.NewReader([]byte("b")))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "photo-2.png" {
		t.Fatalf("got %q", path)
	}
	if _, _, err := Put(home, "photo.png", bytes.NewReader([]byte("c"))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(home), "photo.png")); err != nil {
		t.Fatalf("原文件应还在: %v", err)
	}
}

func TestPutPhoto2CollisionKeepsStem(t *testing.T) {
	home := t.TempDir()
	if _, _, err := Put(home, "photo-2.png", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	path, _, err := Put(home, "photo-2.png", bytes.NewReader([]byte("y")))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "photo-2-2.png" {
		t.Fatalf("got %q want photo-2-2.png", path)
	}
}

func TestPutDotEnvStem(t *testing.T) {
	home := t.TempDir()
	if _, _, err := Put(home, ".env", bytes.NewReader([]byte("A=1"))); err != nil {
		t.Fatal(err)
	}
	path, _, err := Put(home, ".env", bytes.NewReader([]byte("A=2")))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != ".env-2" {
		t.Fatalf("got %q want .env-2", path)
	}
}

func TestPutTakesBasenameOnly(t *testing.T) {
	home := t.TempDir()
	path, _, err := Put(home, "/tmp/secret/../photo.png", bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "photo.png" {
		t.Fatalf("got %q", path)
	}
	if strings.Contains(path, "secret") {
		t.Fatalf("路径逃出了 basename: %s", path)
	}
}

func TestPutRejectsEmptyAndDotNames(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"", ".", ".."} {
		_, _, err := Put(home, name, bytes.NewReader([]byte("x")))
		if !errors.Is(err, ErrBadName) {
			t.Errorf("name %q err=%v want ErrBadName", name, err)
		}
	}
}

func TestPutRejectsEmptyHome(t *testing.T) {
	_, _, err := Put("", "a.txt", bytes.NewReader([]byte("x")))
	if !errors.Is(err, ErrNoHome) {
		t.Fatalf("err=%v want ErrNoHome", err)
	}
}

func TestPutTooLargeLeavesNoProduct(t *testing.T) {
	home := t.TempDir()
	big := bytes.Repeat([]byte("x"), MaxBytes+1)
	_, _, err := Put(home, "big.bin", bytes.NewReader(big))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v want ErrTooLarge", err)
	}
	entries, err := os.ReadDir(Dir(home))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("超限留下了成品名 %s", e.Name())
		}
	}
}

func TestPutExactlyMaxBytesOK(t *testing.T) {
	home := t.TempDir()
	body := bytes.Repeat([]byte("x"), MaxBytes)
	path, n, err := Put(home, "max.bin", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if n != MaxBytes {
		t.Fatalf("n=%d", n)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != MaxBytes {
		t.Fatalf("size=%d", fi.Size())
	}
}

func TestPutConcurrentSameNameNoOverwrite(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	paths := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, _, err := Put(home, "race.txt", bytes.NewReader([]byte{byte(i)}))
			if err != nil {
				errCh <- err
				return
			}
			paths <- p
		}(i)
	}
	wg.Wait()
	close(errCh)
	close(paths)
	for err := range errCh {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for p := range paths {
		if seen[p] {
			t.Fatalf("重复路径 %s（覆盖）", p)
		}
		seen[p] = true
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 8 {
		t.Fatalf("写成 %d 个文件，want 8", len(seen))
	}
}

func TestPutTmpLeftoverDoesNotOccupyName(t *testing.T) {
	home := t.TempDir()
	dir := Dir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".drop-stale.tmp"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, _, err := Put(home, "photo.png", bytes.NewReader([]byte("new")))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "photo.png" {
		t.Fatalf("残留 tmp 不该占用 photo.png，got %s", path)
	}
}
