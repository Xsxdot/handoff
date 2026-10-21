// B462 S2：handoff upgrade --from <本机构建目录> 的竖切。
// 假 peer + 真实目录来源（真实 tar.gz 与 sha256sum 格式 checksums.txt），断言推送的字节、
// sha、tag 都逐字来自目录，且对会自拉的对端也强制推送。
package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/release"
)

// writeFromDir 按 build-deploy.sh --pack 的形态造一个打包目录：每个平台一个包 + checksums.txt。
// 返回目录与 资产名 → 包字节。
func writeFromDir(t *testing.T, tag string, platforms ...[2]string) (string, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{}
	var sums strings.Builder
	for _, p := range platforms {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		body := []byte("#!/bin/sh\necho " + tag + "\n")
		if err := tw.WriteHeader(&tar.Header{Name: "handoff", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(body)
		_ = tw.Close()
		_ = gz.Close()
		name := release.AssetName(tag, p[0], p[1])
		files[name] = buf.Bytes()
		if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(buf.Bytes())
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	if err := os.WriteFile(filepath.Join(dir, release.ChecksumsName), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, files
}

// forbidReleaseChecker 让 GitHub 来源一被调用就判测试失败：--from 下不得查 GitHub。
func forbidReleaseChecker(t *testing.T) {
	t.Helper()
	old := newReleaseChecker
	newReleaseChecker = func() releaseChecker {
		t.Error("--from 下不得调用 GitHub release 来源")
		return checkerFunc(func(context.Context) (release.Release, error) {
			return release.Release{Tag: "v0.1.1"}, nil
		})
	}
	t.Cleanup(func() { newReleaseChecker = old })
}

func TestUpgradeFromPushesDirectoryArchiveEvenToPullCapablePeer(t *testing.T) {
	const tag = "abcdef123456"
	fetcher := &fakeFetcher{sum: "from-github"}
	peer := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"}
	withStubs(t, fetcher, peer)
	forbidReleaseChecker(t)
	dir, files := writeFromDir(t, tag, [2]string{"linux", "amd64"})

	out, err := runUpgrade(t, "--now", "--from", dir)
	if err != nil {
		t.Fatalf("upgrade --now --from: %v\n%s", err, out)
	}
	if peer.pushCalls != 1 || peer.pullCalls != 0 {
		t.Fatalf("--from 必须强制推送（对端无法自拉本地目录）：push=%d pull=%d", peer.pushCalls, peer.pullCalls)
	}
	want := files[release.AssetName(tag, "linux", "amd64")]
	sum := sha256.Sum256(want)
	if !bytes.Equal(peer.lastBody, want) || peer.lastSum != hex.EncodeToString(sum[:]) || peer.lastTag != tag {
		t.Fatalf("推送的字节/sha/tag 必须来自目录：tag=%q sum=%q bytes=%d", peer.lastTag, peer.lastSum, len(peer.lastBody))
	}
	if fetcher.archiveCalls != 0 || fetcher.checksumCalls != 0 {
		t.Fatalf("--from 下不得经 GitHub 下载器：archive=%d checksum=%d", fetcher.archiveCalls, fetcher.checksumCalls)
	}
	if !strings.Contains(out, "成功") {
		t.Fatalf("应报成功，得 %q", out)
	}
}

func TestUpgradeFromMissingPlatformFailsOnlyThatMachine(t *testing.T) {
	const tag = "abcdef123456"
	amd := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"}
	arm := &fakePeer{pull: boolPtr(true), platform: "linux/arm64", version: "v0.9.0"}
	withStubsMulti(t, &fakeFetcher{}, []*fakePeer{amd, arm})
	forbidReleaseChecker(t)
	dir, _ := writeFromDir(t, tag, [2]string{"linux", "amd64"})

	out, err := runUpgrade(t, "--now", "--from", dir)
	if err == nil || !strings.Contains(err.Error(), "有 1 台机器升级失败") {
		t.Fatalf("缺平台的机器应单独失败、命令退出非零，得 err=%v", err)
	}
	missing := release.AssetName(tag, "linux", "arm64")
	if !strings.Contains(out, "devbox1  失败") || !strings.Contains(out, missing) {
		t.Fatalf("arm64 那台应失败并点名缺的资产 %s，得:\n%s", missing, out)
	}
	if !strings.Contains(out, "devbox0  成功") || amd.pushCalls != 1 || arm.pushCalls != 0 {
		t.Fatalf("amd64 那台应照常成功（push amd=%d arm=%d），得:\n%s", amd.pushCalls, arm.pushCalls, out)
	}
}

func TestUpgradeFromCheckShowsLocalBuildTagAndDir(t *testing.T) {
	const tag = "abcdef123456"
	peer := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"}
	withStubs(t, &fakeFetcher{}, peer)
	forbidReleaseChecker(t)
	dir, _ := writeFromDir(t, tag, [2]string{"linux", "amd64"})

	out, err := runUpgrade(t, "--from", dir)
	if err != nil {
		t.Fatalf("upgrade --from（巡检）: %v", err)
	}
	first := strings.SplitN(out, "\n", 2)[0]
	if first != "最新     "+tag+"（本机构建 "+dir+"）" {
		t.Fatalf("巡检首行应为本机构建 tag 与绝对目录，得 %q", first)
	}
	if peer.pushCalls != 0 || peer.pullCalls != 0 {
		t.Fatal("巡检不得换版")
	}
}

func TestUpgradeFromRejectsRollback(t *testing.T) {
	withStubs(t, &fakeFetcher{}, &fakePeer{platform: "linux/amd64"})
	dir, _ := writeFromDir(t, "abcdef123456", [2]string{"linux", "amd64"})
	_, err := runUpgrade(t, "--rollback", "--from", dir)
	if err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("--from 与 --rollback 应互斥报错，得 %v", err)
	}
}
