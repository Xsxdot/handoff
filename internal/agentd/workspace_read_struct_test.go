// workspace_read_struct_test.go —— 保真读取契约的钉测（B81 写入前置）。
//
// 职责：钉住 ReadFile 返回 FileRead 结构体后的契约——完整文本带哈希、截断不带
// 提示与哈希、二进制不带哈希、二进制判据边界。
//
// 边界：不触真实工作区，全部在 t.TempDir() 里造文件。
package agentd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileTextGivesSHA 验证普通文本文件返回完整内容 + 哈希 + 真实大小。
func TestReadFileTextGivesSHA(t *testing.T) {
	dir := t.TempDir()
	body := "module handoff\n\ngo 1.26.1\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir, "go.mod")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got.Content != body {
		t.Errorf("Content=%q, want %q", got.Content, body)
	}
	if got.Size != int64(len(body)) {
		t.Errorf("Size=%d, want %d", got.Size, len(body))
	}
	if got.Truncated || got.Binary {
		t.Errorf("Truncated=%v Binary=%v, want false false", got.Truncated, got.Binary)
	}
	sum := sha256.Sum256([]byte(body))
	if want := hex.EncodeToString(sum[:]); got.SHA256 != want {
		t.Errorf("SHA256=%q, want %q", got.SHA256, want)
	}
}

// TestReadFileTruncatedHasNoNotice 是本期最要紧的一条：截断的正文里**不再**
// 含那行中文提示，且不给哈希（不完整的内容当基线等于允许把文件截断后存回去）。
func TestReadFileTruncatedHasNoNotice(t *testing.T) {
	dir := t.TempDir()
	raw := bytes.Repeat([]byte("x"), maxRunOutput+4096)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir, "big.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !got.Truncated {
		t.Error("Truncated=false, want true")
	}
	if len(got.Content) != maxRunOutput {
		t.Errorf("len(Content)=%d, want %d", len(got.Content), maxRunOutput)
	}
	if strings.Contains(got.Content, "内容已截断") {
		t.Error("正文仍含截断提示——提示必须留在 handleTaskFile 里，不能进 ReadFile 的返回")
	}
	if got.Size != int64(len(raw)) {
		t.Errorf("Size=%d, want 磁盘真实大小 %d", got.Size, len(raw))
	}
	if got.SHA256 != "" {
		t.Errorf("SHA256=%q, want 空（截断内容不可当写入基线）", got.SHA256)
	}
}

// TestReadFileBinary 验证前 8 KiB 出现 NUL 即判为二进制，且不给哈希。
func TestReadFileBinary(t *testing.T) {
	dir := t.TempDir()
	raw := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir, "logo.png")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !got.Binary {
		t.Error("Binary=false, want true")
	}
	if got.SHA256 != "" {
		t.Errorf("SHA256=%q, want 空", got.SHA256)
	}
	if got.Size != int64(len(raw)) {
		t.Errorf("Size=%d, want %d", got.Size, len(raw))
	}
}

// TestIsBinaryPrefix 钉住判据边界：NUL 落在 8 KiB 之内算，之外不算。
func TestIsBinaryPrefix(t *testing.T) {
	inside := append(bytes.Repeat([]byte("a"), binaryProbeBytes-1), 0x00)
	if !isBinaryPrefix(inside) {
		t.Error("第 8192 字节的 NUL 应判为二进制")
	}
	outside := append(bytes.Repeat([]byte("a"), binaryProbeBytes), 0x00)
	if isBinaryPrefix(outside) {
		t.Error("第 8193 字节的 NUL 不在探测窗口内，不该判为二进制")
	}
}
