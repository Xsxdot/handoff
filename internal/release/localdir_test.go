// B462 S2：本机构建打包目录（build-deploy.sh --pack 产物）作为 release 来源。
// 目录里的包与 checksums.txt 由测试现造（真实 tar.gz / zip），解析与下载都走真实代码路径。
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writePackDir 把 files 写进一个新目录，并按 sums 列出的资产名生成 sha256sum 两空格格式的 checksums.txt。
func writePackDir(t *testing.T, files map[string][]byte, sums []string) string {
	t.Helper()
	dir := t.TempDir()
	var lines strings.Builder
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range sums {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(&lines, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	if sums != nil {
		if err := os.WriteFile(filepath.Join(dir, ChecksumsName), []byte(lines.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadLocalDirParsesPackOutput(t *testing.T) {
	const tag = "d905c3ab1234"
	linux := AssetName(tag, "linux", "amd64")
	darwin := AssetName(tag, "darwin", "arm64")
	win := AssetName(tag, "windows", "amd64")
	dir := writePackDir(t, map[string][]byte{
		linux:  tgzWith(t, "x"),
		darwin: tgzWith(t, "y"),
		win:    makeZip(t, "handoff.exe", []byte("z")),
		// 干扰文件：薄壳资产、说明文件、备份——都不是本平台 CLI 资产
		"handoff-desktop_" + tag + "_linux_amd64.AppImage": []byte("d"),
		"README":                 []byte("r"),
		linux + ".bak":           []byte("b"),
		"handoff_garbage.tar.gz": []byte("g"),
	}, []string{linux, darwin, win})

	rel, err := LoadLocalDir(dir)
	if err != nil {
		t.Fatalf("LoadLocalDir: %v", err)
	}
	if rel.Tag != tag {
		t.Fatalf("Tag=%q，期望 %q（tag 来自包名，不来自目录名）", rel.Tag, tag)
	}
	for _, p := range [][2]string{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}} {
		a, ok := rel.AssetFor(p[0], p[1])
		if !ok || a.Name != AssetName(tag, p[0], p[1]) {
			t.Fatalf("%s/%s 资产缺失或命名不一致：%+v ok=%v", p[0], p[1], a, ok)
		}
	}
	if _, ok := rel.Checksums(); !ok {
		t.Fatal("应带 checksums.txt 资产")
	}
	if len(rel.Assets) != 4 {
		t.Fatalf("资产应恰为 3 个包 + checksums.txt，得 %+v", rel.Assets)
	}
}

func TestLoadLocalDirRejectsMixedTags(t *testing.T) {
	a := AssetName("aaaaaaaaaaaa", "linux", "amd64")
	b := AssetName("bbbbbbbbbbbb", "darwin", "arm64")
	dir := writePackDir(t, map[string][]byte{a: tgzWith(t, "x"), b: tgzWith(t, "y")}, []string{a, b})
	_, err := LoadLocalDir(dir)
	if err == nil || !strings.Contains(err.Error(), "aaaaaaaaaaaa") || !strings.Contains(err.Error(), "bbbbbbbbbbbb") {
		t.Fatalf("多个 tag 应拒绝并列出全部 tag，得 %v", err)
	}
}

func TestLoadLocalDirRejectsMissingChecksumsOrAssets(t *testing.T) {
	a := AssetName("cccccccccccc", "linux", "amd64")
	noSums := writePackDir(t, map[string][]byte{a: tgzWith(t, "x")}, nil)
	if _, err := LoadLocalDir(noSums); err == nil || !strings.Contains(err.Error(), ChecksumsName) {
		t.Fatalf("缺 checksums.txt 应拒绝，得 %v", err)
	}
	empty := writePackDir(t, map[string][]byte{"README": []byte("r")}, []string{})
	if _, err := LoadLocalDir(empty); err == nil {
		t.Fatal("零资产应拒绝")
	}
	if _, err := LoadLocalDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("目录不存在应报错")
	}
}

// 序列化边界：目录 Installer 穿过真实的 sha 校验、解包与自检（version 首行 == tag）。
func TestLocalDirInstallerFetchInstallsRealArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("自检脚本用 /bin/sh")
	}
	const tag = "d905c3ab1234"
	goos, goarch := CurrentPlatform()
	name := AssetName(tag, goos, goarch)
	dir := writePackDir(t, map[string][]byte{name: tgzWith(t, "#!/bin/sh\necho "+tag+"\n")}, []string{name})
	rel, err := LoadLocalDir(dir)
	if err != nil {
		t.Fatalf("LoadLocalDir: %v", err)
	}
	dest := t.TempDir()
	p, err := NewLocalDirInstaller(quietLog(), dir).Fetch(context.Background(), rel, dest)
	if err != nil {
		t.Fatalf("目录来源 Fetch: %v", err)
	}
	if filepath.Dir(p) != dest || filepath.Base(p) != TempName(tag) {
		t.Fatalf("临时二进制落点不对：%s", p)
	}
}

// Windows 资产走 FetchArchive + extractBinary（非 Windows 上不执行，只断言解包）。
func TestLocalDirInstallerFetchesZipAsset(t *testing.T) {
	const tag = "d905c3ab1234"
	name := AssetName(tag, "windows", "amd64")
	body := makeZip(t, "handoff.exe", []byte("MZ"))
	dir := writePackDir(t, map[string][]byte{name: body}, []string{name})
	rel, err := LoadLocalDir(dir)
	if err != nil {
		t.Fatalf("LoadLocalDir: %v", err)
	}
	got, sum, err := NewLocalDirInstaller(quietLog(), dir).FetchArchive(context.Background(), rel, "windows", "amd64")
	if err != nil {
		t.Fatalf("FetchArchive: %v", err)
	}
	want := sha256.Sum256(body)
	if string(got) != string(body) || sum != hex.EncodeToString(want[:]) {
		t.Fatal("推送字节与 sha 必须逐字来自目录与 checksums.txt")
	}
	if format, err := extractBinary(got, filepath.Join(t.TempDir(), "out")); err != nil || format != "zip" {
		t.Fatalf("zip 解包：format=%q err=%v", format, err)
	}
}

func TestLocalDirInstallerRejectsBadOrMissingChecksum(t *testing.T) {
	const tag = "d905c3ab1234"
	linux := AssetName(tag, "linux", "amd64")
	darwin := AssetName(tag, "darwin", "arm64")
	dir := writePackDir(t, map[string][]byte{linux: tgzWith(t, "x"), darwin: tgzWith(t, "y")}, []string{linux})
	// 篡改 linux 行的一位
	sums, _ := os.ReadFile(filepath.Join(dir, ChecksumsName))
	bad := []byte(string(sums))
	if bad[0] == '0' {
		bad[0] = '1'
	} else {
		bad[0] = '0'
	}
	if err := os.WriteFile(filepath.Join(dir, ChecksumsName), bad, 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := LoadLocalDir(dir)
	if err != nil {
		t.Fatalf("checksums 缺行不应在解析期拒绝（留给按机器的 FetchArchive）：%v", err)
	}
	inst := NewLocalDirInstaller(quietLog(), dir)
	if _, _, err := inst.FetchArchive(context.Background(), rel, "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "sha256 校验不通过") {
		t.Fatalf("sha 被改应拒绝，得 %v", err)
	}
	if _, _, err := inst.FetchArchive(context.Background(), rel, "darwin", "arm64"); err == nil || !strings.Contains(err.Error(), "没有 "+darwin+" 的校验和") {
		t.Fatalf("checksums 缺行应拒绝并点名资产，得 %v", err)
	}
	if _, _, err := inst.FetchArchive(context.Background(), rel, "linux", "arm64"); err == nil || !strings.Contains(err.Error(), AssetName(tag, "linux", "arm64")) {
		t.Fatalf("缺平台应点名资产全名，得 %v", err)
	}
}
