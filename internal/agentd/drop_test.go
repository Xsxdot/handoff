package agentd

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/dropdir"
	"github.com/Xsxdot/handoff/internal/proto"
)

func useDropHome(t *testing.T, home string) {
	t.Helper()
	prev := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = prev })
}

func dropPost(t *testing.T, url, token, name string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/api/drop?name="+name, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDropPutWritesHomeInbox(t *testing.T) {
	home := t.TempDir()
	useDropHome(t, home)
	env := newTestAgentdEnv(t)
	resp := dropPost(t, env.ts.URL, env.token, "photo.png", []byte("png"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var got proto.DropPutResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".handoff", "drop", "photo.png")
	if got.Path != want {
		t.Fatalf("path=%q want %q", got.Path, want)
	}
	if got.Bytes != 3 {
		t.Fatalf("bytes=%d", got.Bytes)
	}
	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "png" {
		t.Fatalf("content=%q", raw)
	}
}

func TestDropPutRejectsTooLarge(t *testing.T) {
	home := t.TempDir()
	useDropHome(t, home)
	env := newTestAgentdEnv(t)
	body := bytes.Repeat([]byte("x"), dropdir.MaxBytes+1)
	resp := dropPost(t, env.ts.URL, env.token, "big.bin", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413", resp.StatusCode)
	}
	dir := dropdir.Dir(home)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "big.bin" {
			t.Fatal("超限留下了成品名")
		}
	}
}

func TestDropPutRejectsBadName(t *testing.T) {
	useDropHome(t, t.TempDir())
	env := newTestAgentdEnv(t)
	resp := dropPost(t, env.ts.URL, env.token, ".", []byte("x"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", resp.StatusCode)
	}
}

func TestDropForwardDeliversBytesToRemote(t *testing.T) {
	remoteHome := t.TempDir()
	useDropHome(t, remoteHome)
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	payload := bytes.Repeat([]byte("z"), 64)
	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/drop?name=shot.png&machine=devbox",
		bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var got proto.DropPutResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, payload) {
		t.Fatalf("远端内容与上传不一致 path=%s n=%d", got.Path, len(raw))
	}
}

func TestDropFromPathForwardsLocalFile(t *testing.T) {
	remoteHome := t.TempDir()
	useDropHome(t, remoteHome)
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "截图.png")
	payload := []byte("png-bytes")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/drop/local?path="+url.QueryEscape(src)+"&machine=devbox", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var got proto.DropPutResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, payload) {
		t.Fatalf("远端内容 = %q，想要 %q", raw, payload)
	}
	if filepath.Base(got.Path) != "截图.png" {
		t.Fatalf("落盘名 = %s", got.Path)
	}
}

func TestDropFromPathRejectsDirectory(t *testing.T) {
	useDropHome(t, t.TempDir())
	env := newTestAgentdEnv(t)
	dir := t.TempDir()
	req, err := http.NewRequest(http.MethodPost,
		env.ts.URL+"/api/drop/local?path="+url.QueryEscape(dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}

func TestDropForwardRejectsOverLimitWithoutTruncating(t *testing.T) {
	remoteHome := t.TempDir()
	useDropHome(t, remoteHome)
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := bytes.Repeat([]byte("x"), dropdir.MaxBytes+1)
	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/drop?name=huge.bin&machine=devbox",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413", resp.StatusCode)
	}
	want := filepath.Join(remoteHome, ".handoff", "drop", "huge.bin")
	if _, err := os.Stat(want); err == nil {
		t.Fatal("超限转发不得在远端留下截断文件")
	}
}
