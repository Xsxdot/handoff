// 下载/校验/替换的测试。资产由 httptest 现造，替换全在 t.TempDir 里做。
package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// makeTarGz 造一个含单个 handoff 可执行文件的 tar.gz，内容是给定脚本。
func makeTarGz(t *testing.T, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(script)
	if err := tw.WriteHeader(&tar.Header{Name: "handoff", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serveRelease 起一个假的资产服务器，返回 Release 与 tar.gz 的真实 sha。
// badSum=true 时 checksums.txt 里写一个错的哈希。
func serveRelease(t *testing.T, tag, script string, badSum bool) Release {
	t.Helper()
	goos, goarch := CurrentPlatform()
	tgz := makeTarGz(t, script)
	sum := sha256.Sum256(tgz)
	hexSum := hex.EncodeToString(sum[:])
	if badSum {
		hexSum = strings.Repeat("0", 64)
	}
	name := AssetName(tag, goos, goarch)
	checks := fmt.Sprintf("%s  %s\n", hexSum, name)

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz) })
	mux.HandleFunc("/checks", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(checks)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return Release{Tag: tag, Assets: []Asset{
		{Name: name, URL: srv.URL + "/asset"},
		{Name: ChecksumsName, URL: srv.URL + "/checks"},
	}}
}

// newFakeRelease 起一个假的资产服务器，返回 (Release, 关闭函数)。
//
// 为什么要自带一个：跨平台断言必须能同时提供两个平台的资产，
// 而「下错平台」的失败模式恰恰是两个资产都在时才暴露得出来。
func newFakeRelease(t *testing.T, tag string, files map[string][]byte) (Release, func()) {
	t.Helper()
	mux := http.NewServeMux()
	for name, body := range files {
		b := body
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) { w.Write(b) })
	}
	srv := httptest.NewServer(mux)
	rel := Release{Tag: tag}
	for name := range files {
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: srv.URL + "/" + name})
	}
	return rel, srv.Close
}

// tgzWith 造一个内含名为 handoff 的可执行文件的 tar.gz。
func tgzWith(t *testing.T, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "handoff", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(script))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// TestFetchArchiveHonorsRequestedPlatform 是这次拆分的核心断言：
// 请求 linux/amd64 时必须拿到 linux 那份，而不是本机平台那份。
// 拆分前的 Fetch 写死 CurrentPlatform，这条在任何机器上都会翻红。
func TestFetchArchiveHonorsRequestedPlatform(t *testing.T) {
	linux := tgzWith(t, "#!/bin/sh\necho v9.9.9\n")
	darwin := tgzWith(t, "#!/bin/sh\necho WRONG\n")
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	checks := fmt.Sprintf("%s  %s\n%s  %s\n",
		sum(linux), AssetName("v9.9.9", "linux", "amd64"),
		sum(darwin), AssetName("v9.9.9", "darwin", "arm64"))

	rel, closeFn := newFakeRelease(t, "v9.9.9", map[string][]byte{
		AssetName("v9.9.9", "linux", "amd64"):  linux,
		AssetName("v9.9.9", "darwin", "arm64"): darwin,
		ChecksumsName:                          []byte(checks),
	})
	defer closeFn()

	i := NewInstaller(slog.New(slog.NewTextHandler(io.Discard, nil)))
	got, gotSum, err := i.FetchArchive(context.Background(), rel, "linux", "amd64")
	if err != nil {
		t.Fatalf("FetchArchive: %v", err)
	}
	if !bytes.Equal(got, linux) {
		t.Fatalf("下到了别的平台的资产")
	}
	if gotSum != sum(linux) {
		t.Fatalf("返回的 sha256 不是 checksums.txt 里声明的那个：得 %s 期望 %s", gotSum, sum(linux))
	}
}

// TestFetchArchiveDoesNotSelfCheck 锁住「本机不执行远端平台的二进制」这条边界。
// 包内放一个根本不可执行的文件，FetchArchive 仍必须成功——自检归 InstallArchive。
func TestFetchArchiveDoesNotSelfCheck(t *testing.T) {
	body := tgzWith(t, "这不是一个可执行文件")
	s := sha256.Sum256(body)
	checks := fmt.Sprintf("%s  %s\n", hex.EncodeToString(s[:]), AssetName("v9.9.9", "linux", "amd64"))
	rel, closeFn := newFakeRelease(t, "v9.9.9", map[string][]byte{
		AssetName("v9.9.9", "linux", "amd64"): body,
		ChecksumsName:                         []byte(checks),
	})
	defer closeFn()

	i := NewInstaller(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, _, err := i.FetchArchive(context.Background(), rel, "linux", "amd64"); err != nil {
		t.Fatalf("FetchArchive 不该自检，却失败了: %v", err)
	}
}

// TestInstallArchiveRejectsBadSum 锁住 agentd 侧那道「传输完整性」校验。
func TestInstallArchiveRejectsBadSum(t *testing.T) {
	dir := t.TempDir()
	i := NewInstaller(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := i.InstallArchive(tgzWith(t, "#!/bin/sh\necho v1\n"), strings.Repeat("0", 64), "v1", dir)
	if err == nil {
		t.Fatal("sha256 不符必须拒绝")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("拒绝后不该留残件，实得 %v", ents)
	}
}

// TestInstallArchiveSelfChecks 锁住「自检失败即删临时文件」。
func TestInstallArchiveSelfChecks(t *testing.T) {
	dir := t.TempDir()
	body := tgzWith(t, "#!/bin/sh\necho v-WRONG\n")
	s := sha256.Sum256(body)
	i := NewInstaller(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := i.InstallArchive(body, hex.EncodeToString(s[:]), "v9.9.9", dir)
	if err == nil {
		t.Fatal("version 首行对不上目标 tag 必须拒绝")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("自检失败后不该留残件，实得 %v", ents)
	}
}

// 正常路径：下载 → 校验 → 解包 → 自检通过 → 返回临时文件路径。
func TestFetchHappyPath(t *testing.T) {
	tag := "v9.9.9"
	// 自检跑的是 `<新二进制> version`，首行必须等于 tag
	rel := serveRelease(t, tag, "#!/bin/sh\necho "+tag+"\n", false)
	dir := t.TempDir()
	p, err := NewInstaller(quietLog()).Fetch(context.Background(), rel, dir)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if filepath.Dir(p) != dir {
		t.Fatalf("临时文件必须落在 destDir 内（rename 的原子性只在同一文件系统内成立），得到 %s", p)
	}
	if base := filepath.Base(p); base != TempName(tag) {
		t.Fatalf("临时文件名=%q，期望 %q", base, TempName(tag))
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("权限=%v，应为 0755（不可执行就没法自检也没法被拉起）", fi.Mode().Perm())
	}
}

// checksum 不匹配：必须失败，且**把临时文件删掉**。
//
// why 要删：留着一份校验失败的二进制在二进制目录里，下一轮可能被误当成
// 已就绪的 pending，而它的内容是坏的。
func TestFetchRejectsBadChecksum(t *testing.T) {
	tag := "v9.9.9"
	rel := serveRelease(t, tag, "#!/bin/sh\necho "+tag+"\n", true)
	dir := t.TempDir()
	_, err := NewInstaller(quietLog()).Fetch(context.Background(), rel, dir)
	if err == nil {
		t.Fatal("checksum 不匹配应报错")
	}
	if !strings.Contains(err.Error(), "校验") && !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("报错应点明是校验失败，得到: %v", err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("校验失败后应清干净，还剩: %v", ents)
	}
}

// 自检失败（新二进制打印的版本对不上）：必须失败并清理。
//
// why 这道闸重要：它挡的是「下载完整但架构拿错 / 动态库缺失 / 资产本身构建错」
// ——这些情况下 sha256 是对的，但二进制根本跑不起来。没有这道闸，
// 换版会把一个跑不了的东西 rename 到位，然后 agentd 再也起不来。
func TestFetchRejectsFailedSelfCheck(t *testing.T) {
	tag := "v9.9.9"
	rel := serveRelease(t, tag, "#!/bin/sh\necho v0.0.1-wrong\n", false)
	dir := t.TempDir()
	_, err := NewInstaller(quietLog()).Fetch(context.Background(), rel, dir)
	if err == nil {
		t.Fatal("自检版本对不上应报错")
	}
	if !strings.Contains(err.Error(), "自检") {
		t.Fatalf("报错应点明是自检失败，得到: %v", err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("自检失败后应清干净，还剩: %v", ents)
	}
}

// 本平台没有资产：在下载之前就报错。
func TestFetchMissingPlatformAsset(t *testing.T) {
	rel := Release{Tag: "v9.9.9", Assets: []Asset{{Name: ChecksumsName, URL: "http://x/c"}}}
	_, err := NewInstaller(quietLog()).Fetch(context.Background(), rel, t.TempDir())
	if err == nil {
		t.Fatal("缺本平台资产应报错")
	}
}

// Activate：新文件就位、旧文件留成 .prev。
func TestActivateKeepsPrev(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "handoff")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	newp := filepath.Join(dir, TempName("v1.0.0"))
	if err := os.WriteFile(newp, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev, err := Activate(newp, target)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if prev != PrevPath(target) {
		t.Fatalf("prev 路径=%q，期望 %q", prev, PrevPath(target))
	}
	if b, _ := os.ReadFile(target); string(b) != "NEW" {
		t.Fatalf("目标内容=%q，应为 NEW", b)
	}
	if b, _ := os.ReadFile(prev); string(b) != "OLD" {
		t.Fatalf("prev 内容=%q，应为 OLD（回滚全靠它）", b)
	}
	if _, err := os.Stat(newp); !os.IsNotExist(err) {
		t.Error("临时文件 rename 之后不该还在")
	}
}

// Rollback：把 .prev 换回去。
func TestRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "handoff")
	if err := os.WriteFile(target, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PrevPath(target), []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(target); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "OLD" {
		t.Fatalf("回滚后内容=%q，应为 OLD", b)
	}
}

// 没有 .prev 时回滚要给一句人话，而不是甩一个 ENOENT。
func TestRollbackWithoutPrev(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "handoff")
	if err := os.WriteFile(target, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Rollback(target)
	if err == nil {
		t.Fatal("没有 prev 应报错")
	}
	if !strings.Contains(err.Error(), "没有可回滚") {
		t.Fatalf("报错应说人话，得到: %v", err)
	}
}

// 目标目录不可写：报错要点明真因与解法，不能只甩 permission denied。
//
// why（B45）：装在 /usr/local/bin 是常见情况，用户看到扁平的
// "permission denied" 不知道该怎么办。
func TestActivateUnwritableDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "handoff")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	newp := filepath.Join(dir, TempName("v1.0.0"))
	if err := os.WriteFile(newp, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("无法置只读目录，跳过")
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root 无视目录权限，跳过")
	}
	_, err := Activate(newp, target)
	if err == nil {
		t.Fatal("只读目录下 Activate 应失败")
	}
	if !strings.Contains(err.Error(), "写权限") {
		t.Fatalf("报错应点明是目录写权限问题，得到: %v", err)
	}
}

// makeZip 造一个含单个指定文件名的 zip。
//
// 参数：
//   - name: 包内文件名（用来覆盖 handoff.exe 与「包里没有目标文件」两种情形）
//   - content: 文件内容
func makeZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("建 zip 条目失败: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("写 zip 条目失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	return buf.Bytes()
}

// Windows 资产是 zip，包内是 handoff.exe，两者都要认。
func TestExtractBinaryReadsZip(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	format, err := extractBinary(makeZip(t, "handoff.exe", []byte("BINARY")), dest)
	if err != nil {
		t.Fatalf("解 zip 失败: %v", err)
	}
	if format != "zip" {
		t.Fatalf("format = %q，期望 zip", format)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("读解出的文件失败: %v", err)
	}
	if string(b) != "BINARY" {
		t.Fatalf("解出的内容 = %q，期望 BINARY", b)
	}
}

// darwin/linux 资产仍是 tar.gz，包内是 handoff。
func TestExtractBinaryReadsTarGz(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	format, err := extractBinary(makeTarGz(t, "#!/bin/sh\necho hi\n"), dest)
	if err != nil {
		t.Fatalf("解 tar.gz 失败: %v", err)
	}
	if format != "tar.gz" {
		t.Fatalf("format = %q，期望 tar.gz", format)
	}
}

// 格式按魔数判定，不按调用方传的平台——所以「两者都不是」必须有明确报文，
// 否则症状会是一句无从下手的 gzip 解析错误。
func TestExtractBinaryRejectsUnknownFormat(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	_, err := extractBinary([]byte("not an archive at all"), dest)
	if err == nil {
		t.Fatal("无法识别的格式应当报错")
	}
	if !strings.Contains(err.Error(), "既不是 gzip 也不是 zip") {
		t.Fatalf("报文应说清两种格式都不匹配，实得 %v", err)
	}
}

// tgzNamed 造一个含单个指定文件名的 tar.gz。
//
// 与既有的 tgzWith 的区别只在于文件名可指定：tgzWith 把名字写死成 handoff，
// 测不了「包里没有目标文件」这一支。
func tgzNamed(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 包里没有可执行文件时两种格式都要报错，不能留一个空文件在目标路径上。
func TestExtractBinaryRejectsArchiveWithoutBinary(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := extractBinary(makeZip(t, "README.txt", []byte("x")), dest); err == nil {
		t.Fatal("zip 里没有 handoff.exe 时应当报错")
	}
	if _, err := extractBinary(tgzNamed(t, "README.txt", "x"), dest); err == nil {
		t.Fatal("tar.gz 里没有 handoff 时应当报错")
	}
}

// Windows 上临时文件必须以 .exe 结尾：selfCheck 要 exec 它跑 version，
// 没有该后缀的文件在 Windows 上起不来——症状是「自检失败」，真因是文件名。
func TestTempNameHasExeSuffixOnWindows(t *testing.T) {
	if got := tempName("v1.2.3", "windows"); got != ".handoff.new-v1.2.3.exe" {
		t.Fatalf("windows: tempName = %q，期望 .handoff.new-v1.2.3.exe", got)
	}
	for _, goos := range []string{"darwin", "linux"} {
		if got := tempName("v1.2.3", goos); got != ".handoff.new-v1.2.3" {
			t.Fatalf("%s: tempName = %q，期望 .handoff.new-v1.2.3", goos, got)
		}
	}
}

// 前两次 500、第三次 200：get 必须重试并最终成功。
// 重试会真睡退避，所以先把 downloadRetryBase 压到毫秒级，测完恢复。
func TestGetRetriesOnServerError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("hello retry"))
	}))
	defer srv.Close()

	old := downloadRetryBase
	downloadRetryBase = time.Millisecond
	t.Cleanup(func() { downloadRetryBase = old })

	i := NewInstaller(quietLog())
	got, err := i.get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("get 应当在前两次 500 后重试成功，却报错: %v", err)
	}
	if !bytes.Equal(got, []byte("hello retry")) {
		t.Fatalf("正文 = %q，期望 hello retry", got)
	}
	if attempts != 3 {
		t.Fatalf("请求次数 = %d，期望 3（首次 + 2 次重试）", attempts)
	}
}

// 404 是一次性错误，不该重试。
// 这是约束测试：当前实现无重试，红期也会过，但锁住「4xx 不可重试」。
func TestGetDoesNotRetryOnClientError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	old := downloadRetryBase
	downloadRetryBase = time.Millisecond
	t.Cleanup(func() { downloadRetryBase = old })

	i := NewInstaller(quietLog())
	if _, err := i.get(context.Background(), srv.URL); err == nil {
		t.Fatal("404 应当报错")
	}
	if attempts != 1 {
		t.Fatalf("请求次数 = %d，期望 1（4xx 不可重试）", attempts)
	}
}

// 三次全 500：重试耗尽后必须失败，且报错点明尝试次数。
func TestGetFailsAfterRetriesExhausted(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	old := downloadRetryBase
	downloadRetryBase = time.Millisecond
	t.Cleanup(func() { downloadRetryBase = old })

	i := NewInstaller(quietLog())
	_, err := i.get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("三次全 500 应当报错")
	}
	if !strings.Contains(err.Error(), "尝试 3 次") {
		t.Fatalf("报错应点明尝试次数，实得: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("请求次数 = %d，期望 3", attempts)
	}
}

// ctx 取消必须立刻中断退避，不能真等 2s→4s 的整段退避。
// 故意把退避设成 2s 起步：若实现不尊重 ctx，本测试会真睡 6 秒——这正是它锁的行为。
func TestGetCancelStopsBackoff(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	old := downloadRetryBase
	downloadRetryBase = 2 * time.Second
	t.Cleanup(func() { downloadRetryBase = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	i := NewInstaller(quietLog())
	_, err := i.get(ctx, srv.URL)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后应返回 context.Canceled，实得: %v", err)
	}
	if elapsed >= 1*time.Second {
		t.Fatalf("取消后用了 %v 才返回，说明退避没被 ctx 打断", elapsed)
	}
}
