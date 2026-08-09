package envfile

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestResolver 造一个带 env 目录的 resolver，返回 resolver 与该目录。
func newTestResolver(t *testing.T, m map[string]string) (*Resolver, string) {
	t.Helper()
	dir := Dir(t.TempDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return NewResolver(dir, m, quietLogger()), dir
}

func TestDirIsUnderDataDir(t *testing.T) {
	if got, want := Dir("/data"), filepath.Join("/data", "env"); got != want {
		t.Fatalf("Dir: got %q, want %q", got, want)
	}
}

func TestForReturnsNilWhenAgentNotConfigured(t *testing.T) {
	r, _ := newTestResolver(t, map[string]string{})
	got, err := r.For("opencode")
	if err != nil {
		t.Fatalf("未配置的 agent 不应报错: %v", err)
	}
	if got != nil {
		t.Fatalf("未配置的 agent 应返回 nil，实际 %v", got)
	}
}

func TestForLoadsFile(t *testing.T) {
	r, dir := newTestResolver(t, map[string]string{"opencode": "dev.env"})
	if err := os.WriteFile(filepath.Join(dir, "dev.env"),
		[]byte("HTTPS_PROXY=http://127.0.0.1:7890\nGOPROXY=https://goproxy.cn,direct\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := r.For("opencode")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	want := []string{"HTTPS_PROXY=http://127.0.0.1:7890", "GOPROXY=https://goproxy.cn,direct"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("第 %d 条: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestForRejectsNameWithPathSeparator(t *testing.T) {
	for _, name := range []string{"../secrets.env", "sub/dev.env", ".", ".."} {
		r, _ := newTestResolver(t, map[string]string{"opencode": name})
		if _, err := r.For("opencode"); err == nil {
			t.Errorf("文件名 %q 应被拒绝", name)
		}
	}
}

func TestForMissingFileErrorCarriesPath(t *testing.T) {
	r, dir := newTestResolver(t, map[string]string{"opencode": "nope.env"})
	_, err := r.For("opencode")
	if err == nil {
		t.Fatal("文件缺失应报错")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "nope.env")) {
		t.Errorf("错误应含完整路径，实际 %q", err.Error())
	}
}

// TestForRereadsFileEachCall 钉住 spec §5.3 的热更新承诺：改文件后无需重启，
// 下一次 For 就拿到新值。
func TestForRereadsFileEachCall(t *testing.T) {
	r, dir := newTestResolver(t, map[string]string{"opencode": "dev.env"})
	path := filepath.Join(dir, "dev.env")
	if err := os.WriteFile(path, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	first, err := r.For("opencode")
	if err != nil || len(first) != 1 || first[0] != "A=1" {
		t.Fatalf("首次读取异常: %v, %v", first, err)
	}
	if err := os.WriteFile(path, []byte("A=2\n"), 0o600); err != nil {
		t.Fatalf("改写: %v", err)
	}
	second, err := r.For("opencode")
	if err != nil || len(second) != 1 || second[0] != "A=2" {
		t.Fatalf("改文件后应拿到新值，实际 %v, %v", second, err)
	}
}

// TestPreflightDoesNotPanicOnBrokenFile 确认预检不阻断、不 panic（只 WARN）。
func TestPreflightDoesNotPanicOnBrokenFile(t *testing.T) {
	r, dir := newTestResolver(t, map[string]string{"opencode": "bad.env", "claude": "nope.env"})
	if err := os.WriteFile(filepath.Join(dir, "bad.env"), []byte("NOT_A_PAIR\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r.Preflight() // 不返回错误，问题只进日志
}
