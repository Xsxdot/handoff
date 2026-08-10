// machineauthority 文件资源测试。
//
// 职责：
//   - 锁定目录浏览、内容 SHA-256 version 与目录冒充文件拒绝
//   - 锁定 if_match 冲突、create_only 与同目录原子替换语义
//   - 验证 rename 前失败会清理临时文件且不破坏旧内容
//
// 边界：
//   - 使用真实磁盘文件；不覆盖 HTTP/peer 路由
package machineauthority

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestDirectoryAndFileReadUseRelativePathsAndContentVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws-1", MachineID: "m-1", RootPath: dir}

	entries, err := authority.ListDirectory(context.Background(), ws, "")
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}
	if len(entries) != 2 || entries[0].WorkspaceID != "ws-1" {
		t.Fatalf("entries = %+v", entries)
	}
	for _, entry := range entries {
		if filepath.IsAbs(entry.Path) || strings.Contains(entry.Path, `\\`) {
			t.Fatalf("公开 path 必须是 slash relative: %+v", entry)
		}
	}

	doc, err := authority.ReadFile(context.Background(), ws, "README.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if doc.Version != "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("version = %q", doc.Version)
	}
	decoded, _ := base64.StdEncoding.DecodeString(doc.ContentBase64)
	if string(decoded) != "hello" || doc.Path != "README.md" {
		t.Fatalf("doc = %+v content=%q", doc, decoded)
	}

	_, err = authority.ReadFile(context.Background(), ws, "docs")
	assertResourceCode(t, err, workspaceapi.ErrorCommandConflict)
}

func TestVersionConflictAndCreateOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir}
	old, err := authority.ReadFile(context.Background(), ws, "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = authority.WriteFile(context.Background(), ws, workspaceapi.WriteFileCommand{
		CommandID: "cmd-conflict", Path: "note.txt", IfMatch: old.Version,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("mine")),
	})
	assertResourceCode(t, err, workspaceapi.ErrorVersionConflict)
	got, _ := os.ReadFile(path)
	if string(got) != "external" {
		t.Fatalf("冲突写破坏了外部内容: %q", got)
	}
	fresh, err := authority.ReadFile(context.Background(), ws, "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := authority.WriteFile(context.Background(), ws, workspaceapi.WriteFileCommand{
		CommandID: "cmd-save", Path: "note.txt", IfMatch: fresh.Version,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("saved")),
	})
	if err != nil || saved.Version == fresh.Version {
		t.Fatalf("normal save = %+v, %v", saved, err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "saved" {
		t.Fatalf("normal save content = %q", got)
	}

	_, err = authority.WriteFile(context.Background(), ws, workspaceapi.WriteFileCommand{
		CommandID: "cmd-no-version", Path: "note.txt",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("mine")),
	})
	assertResourceCode(t, err, workspaceapi.ErrorCommandConflict)

	created, err := authority.WriteFile(context.Background(), ws, workspaceapi.WriteFileCommand{
		CommandID: "cmd-create", Path: "new.txt", CreateOnly: true,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("new")),
	})
	if err != nil || created.Version == "" {
		t.Fatalf("create only = %+v, %v", created, err)
	}
	_, err = authority.WriteFile(context.Background(), ws, workspaceapi.WriteFileCommand{
		CommandID: "cmd-create-again", Path: "new.txt", CreateOnly: true,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("again")),
	})
	assertResourceCode(t, err, workspaceapi.ErrorCommandConflict)
}

func TestAtomicWriteCleansTemporaryFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(io.Discard, nil)))
	authority.beforeRename = func() error { return errors.New("injected rename boundary failure") }
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir}
	doc, err := authority.ReadFile(context.Background(), ws, "note.txt")
	if err != nil {
		t.Fatal(err)
	}

	_, err = authority.WriteFile(context.Background(), ws, workspaceapi.WriteFileCommand{
		CommandID: "cmd-fail", Path: "note.txt", IfMatch: doc.Version,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("after")),
	})
	if err == nil {
		t.Fatal("rename 前注入失败应返回错误")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "before" {
		t.Fatalf("失败写破坏旧文件: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".handoff-save-") {
			t.Fatalf("失败后残留临时文件: %s", entry.Name())
		}
	}
}
