// 下载/校验/替换的测试。资产由 httptest 现造，替换全在 t.TempDir 里做。
package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
