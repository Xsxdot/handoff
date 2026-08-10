// machineauthority 有界 literal 文件搜索测试。
//
// 职责：
//   - 锁定 .git、二进制与超大文件跳过语义
//   - 锁定 max_results、单文件/总扫描上限与 truncated
//   - 证明日志不包含 query 或 preview 内容
//
// 边界：
//   - 搜索真实临时目录，不测试 renderer 展示
package machineauthority

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestFileSearchIsLiteralBoundedAndSkipsUnsafeInputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSearchFile(t, dir, "visible.txt", "needle one\nneedle two\n")
	writeSearchFile(t, dir, ".git/hidden", "needle hidden")
	writeSearchFile(t, dir, "binary.bin", "before\x00needle")
	writeSearchFile(t, dir, "large.txt", strings.Repeat("x", 80)+"needle")

	var logs bytes.Buffer
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(&logs, nil)))
	authority.searchLimits = searchLimits{maxResults: 200, perFileBytes: 64, totalBytes: 1024}
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir}
	result, err := authority.SearchFiles(context.Background(), ws, workspaceapi.SearchFilesCommand{
		Query: "needle", MaxResults: 500,
	})
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	if len(result.Matches) != 2 || result.Matches[0].Path != "visible.txt" || result.Matches[0].Line != 1 || result.Matches[0].Column != 1 {
		t.Fatalf("matches = %+v", result.Matches)
	}
	if strings.Contains(logs.String(), "needle") || strings.Contains(logs.String(), "needle one") {
		t.Fatalf("搜索日志泄漏 query/preview: %s", logs.String())
	}
}

func TestFileSearchCapsResultsAndTotalBytes(t *testing.T) {
	dir := t.TempDir()
	writeSearchFile(t, dir, "many.txt", strings.Repeat("hit\n", 240))
	authority := NewResourceAuthority(slog.Default())
	authority.searchLimits = searchLimits{maxResults: 200, perFileBytes: 2048, totalBytes: 4096}
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir}
	result, err := authority.SearchFiles(context.Background(), ws, workspaceapi.SearchFilesCommand{Query: "hit", MaxResults: 999})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 200 || !result.Truncated {
		t.Fatalf("result cap = %d truncated=%v", len(result.Matches), result.Truncated)
	}

	writeSearchFile(t, dir, "z-second.txt", strings.Repeat("y", 32))
	authority.searchLimits = searchLimits{maxResults: 200, perFileBytes: 2048, totalBytes: 16}
	result, err = authority.SearchFiles(context.Background(), ws, workspaceapi.SearchFilesCommand{Query: "absent", MaxResults: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.ScannedBytes > 16 {
		t.Fatalf("total scan cap = %+v", result)
	}
}

func TestListAndSearchAllowInternalSymlinkButRejectEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeSearchFile(t, dir, "docs/inside.txt", "find-me")
	writeSearchFile(t, outside, "secret.txt", "find-me")
	if err := os.Symlink("docs", filepath.Join(dir, "docs-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outside-link")); err != nil {
		t.Fatal(err)
	}
	authority := NewResourceAuthority(slog.Default())
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir}
	entries, err := authority.ListDirectory(context.Background(), ws, "docs-link")
	if err != nil || len(entries) != 1 || entries[0].Path != "docs-link/inside.txt" {
		t.Fatalf("internal symlink list = %+v, %v", entries, err)
	}
	result, err := authority.SearchFiles(context.Background(), ws, workspaceapi.SearchFilesCommand{
		Path: "docs-link", Query: "find-me", MaxResults: 10,
	})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Path != "docs-link/inside.txt" {
		t.Fatalf("internal symlink search = %+v, %v", result, err)
	}
	_, err = authority.ListDirectory(context.Background(), ws, "outside-link")
	assertResourceCode(t, err, workspaceapi.ErrorPathOutsideWorkspace)
	_, err = authority.SearchFiles(context.Background(), ws, workspaceapi.SearchFilesCommand{
		Path: "outside-link", Query: "find-me", MaxResults: 10,
	})
	assertResourceCode(t, err, workspaceapi.ErrorPathOutsideWorkspace)
}

func writeSearchFile(t *testing.T, root, name, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
