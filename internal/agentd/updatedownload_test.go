// 本文件覆盖桌面端安装包下载的完整性、并发、唤起与平台边界。
//
// 职责：锁住 POST/GET /api/update/desktop/download 的外部行为。
// 边界：使用 Server 缝替换网络下载与文件管理器，不触碰 GitHub 或真实桌面。
package agentd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/release"
	"github.com/Xsxdot/handoff/internal/selfupdate"
)

func downloadSum(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func newDesktopUpdateEnv(t *testing.T) *testAgentdEnv {
	t.Helper()
	return newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken, DataDir: t.TempDir()}, discardLogger())
}

func postDesktopDownload(t *testing.T, env *testAgentdEnv, tag string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		env.ts.URL+"/api/update/desktop/download?tag="+tag, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST 下载: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// 校验不过必须删文件：留一份坏安装包在下载目录里，用户下次会装上它。
func TestDownloadDeletesFileOnChecksumMismatch(t *testing.T) {
	env := newDesktopUpdateEnv(t)
	env.srv.downloadFetch = func(context.Context, string, string) ([]byte, string, error) {
		return []byte("bad package"), downloadSum([]byte("good package")), nil
	}
	env.srv.downloadOpen = func(string) error { return nil }

	if code := postDesktopDownload(t, env, "v0.3.1"); code != http.StatusBadGateway {
		t.Fatalf("校验不符得到 %d，想要 502", code)
	}
	name, ok := release.DesktopAssetName("v0.3.1", "linux", "amd64")
	if !ok {
		t.Fatal("测试平台应有薄壳资产")
	}
	if _, err := os.Stat(filepath.Join(env.srv.conf().DataDir, "downloads", name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("校验失败后文件仍存在，stat err=%v", err)
	}
}

// 同时只允许一个下载：重复 POST 返回 409。
func TestDownloadRejectsConcurrent(t *testing.T) {
	env := newDesktopUpdateEnv(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	env.srv.downloadFetch = func(context.Context, string, string) ([]byte, string, error) {
		close(started)
		<-finish
		return []byte("package"), downloadSum([]byte("package")), nil
	}
	env.srv.downloadOpen = func(string) error { return nil }

	first := make(chan int, 1)
	go func() { first <- postDesktopDownload(t, env, "v0.3.1") }()
	<-started
	if code := postDesktopDownload(t, env, "v0.3.1"); code != http.StatusConflict {
		t.Fatalf("并发下载得到 %d，想要 409", code)
	}
	close(finish)
	if code := <-first; code != http.StatusOK {
		t.Fatalf("首个下载得到 %d，想要 200", code)
	}
}

// 唤起文件管理器失败不影响下载成功：仍 200，opened=false 且带绝对路径。
func TestDownloadSucceedsWhenOpenerFails(t *testing.T) {
	env := newDesktopUpdateEnv(t)
	body := []byte("package")
	env.srv.downloadFetch = func(context.Context, string, string) ([]byte, string, error) {
		return body, downloadSum(body), nil
	}
	env.srv.downloadOpen = func(string) error { return errors.New("找不到文件管理器") }

	if code := postDesktopDownload(t, env, "v0.3.1"); code != http.StatusOK {
		t.Fatalf("唤起失败时下载得到 %d，想要 200", code)
	}
	var got proto.DownloadState
	if code := env.getJSON(t, "/api/update/desktop/download", &got); code != http.StatusOK {
		t.Fatalf("读取下载状态得到 %d，想要 200", code)
	}
	if got.Stage != "done" || got.Opened || got.Path == "" || !filepath.IsAbs(got.Path) {
		t.Fatalf("下载状态不完整: %+v", got)
	}
}

func TestDownloadSkipsMatchingExistingFile(t *testing.T) {
	env := newDesktopUpdateEnv(t)
	body := []byte("already downloaded")
	name, ok := release.DesktopAssetName("v0.3.1", "linux", "amd64")
	if !ok {
		t.Fatal("测试平台应有薄壳资产")
	}
	dir := filepath.Join(env.srv.conf().DataDir, "downloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("创建下载目录: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("写已有安装包: %v", err)
	}
	env.srv.downloadChecksum = func(context.Context, string, string) (string, error) {
		return downloadSum(body), nil
	}
	called := false
	env.srv.downloadFetch = func(context.Context, string, string) ([]byte, string, error) {
		called = true
		return nil, "", errors.New("不应下载")
	}
	env.srv.downloadOpen = func(string) error { return nil }
	if code := postDesktopDownload(t, env, "v0.3.1"); code != http.StatusOK {
		t.Fatalf("已有安装包得到 %d，想要 200", code)
	}
	if called {
		t.Fatal("已有且校验通过的安装包不应再次下载")
	}
}

// 平台没有薄壳发布物时明确拒绝，而不是去下一个不存在的文件名。
func TestDownloadRefusesUnsupportedPlatform(t *testing.T) {
	env := newDesktopUpdateEnv(t)
	env.srv.downloadPlatform = func() (string, string) { return "freebsd", "amd64" }
	called := false
	env.srv.downloadFetch = func(context.Context, string, string) ([]byte, string, error) {
		called = true
		return nil, "", nil
	}
	if code := postDesktopDownload(t, env, "v0.3.1"); code != http.StatusBadRequest {
		t.Fatalf("不支持平台得到 %d，想要 400", code)
	}
	if called {
		t.Fatal("不支持平台不应调用下载")
	}
	if !strings.Contains(env.srv.downloadState.Error, "freebsd/amd64") {
		t.Fatalf("错误应包含平台，得到 %q", env.srv.downloadState.Error)
	}
}

func TestUpdateLatestUsesFreshSharedCache(t *testing.T) {
	env := newDesktopUpdateEnv(t)
	checkedAt := time.Now().UTC()
	if err := selfupdate.SaveCLICheck(env.srv.conf().DataDir,
		&selfupdate.CLICheck{CheckedAt: checkedAt, Latest: "v9.9.9"}); err != nil {
		t.Fatalf("保存 fixture: %v", err)
	}
	called := false
	env.srv.latestFetch = func(context.Context) (release.Release, error) {
		called = true
		return release.Release{Tag: "v0.0.1"}, nil
	}
	var got proto.LatestResp
	if code := env.getJSON(t, "/api/update/latest", &got); code != http.StatusOK {
		t.Fatalf("读取最新版得到 %d，想要 200", code)
	}
	if called || got.Tag != "v9.9.9" || got.CheckedAt == "" {
		t.Fatalf("应读新鲜共用缓存，called=%v resp=%+v", called, got)
	}
}

func TestUpdateLatestRefreshesAndSavesCache(t *testing.T) {
	env := newDesktopUpdateEnv(t)
	env.srv.latestFetch = func(context.Context) (release.Release, error) {
		return release.Release{Tag: "v0.4.0"}, nil
	}
	var got proto.LatestResp
	if code := env.getJSON(t, "/api/update/latest?refresh=1", &got); code != http.StatusOK {
		t.Fatalf("强制检查得到 %d，想要 200", code)
	}
	if got.Tag != "v0.4.0" || got.CheckedAt == "" {
		t.Fatalf("刷新响应不完整: %+v", got)
	}
	cached := selfupdate.LoadCLICheck(env.srv.conf().DataDir)
	if cached == nil || cached.Latest != "v0.4.0" {
		t.Fatalf("刷新结果未写入共用缓存: %+v", cached)
	}
}

func TestUpdateLatestFailureIsEmptySuccess(t *testing.T) {
	env := newDesktopUpdateEnv(t)
	env.srv.latestFetch = func(context.Context) (release.Release, error) {
		return release.Release{}, errors.New("GitHub 限流")
	}
	var got proto.LatestResp
	if code := env.getJSON(t, "/api/update/latest?refresh=1", &got); code != http.StatusOK {
		t.Fatalf("查询失败得到 %d，想要 200", code)
	}
	if got.Tag != "" {
		t.Fatalf("失败时 Tag 必须为空，得到 %q", got.Tag)
	}
}
