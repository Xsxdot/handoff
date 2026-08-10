// desktopapi workspace resource golden 契约测试。
//
// 职责：
//   - round-trip 文件、搜索、Git、PTY 与 Preview JSON fixture
//   - 锁定跨 Go/TypeScript 的稳定 identity/version/incarnation/seq 字段
//
// 边界：
//   - 不执行资源 I/O；fixture 同时由 desktop Zod 测试消费
package desktopapi

import (
	"encoding/json"
	"testing"
)

func TestResourceGoldenRoundTrips(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		newValue func() any
		check    func(t *testing.T, value any)
	}{
		{"file entry", "file-entry.json", func() any { return &FileEntryDTO{} }, func(t *testing.T, value any) {
			if value.(*FileEntryDTO).WorkspaceID != "ws-main" {
				t.Fatalf("workspace_id 丢失: %+v", value)
			}
		}},
		{"file document", "file-document.json", func() any { return &FileDocumentDTO{} }, func(t *testing.T, value any) {
			if value.(*FileDocumentDTO).Version != "sha256:2cf24dba" {
				t.Fatalf("version 丢失: %+v", value)
			}
		}},
		{"file search", "file-search-result.json", func() any { return &FileSearchResultDTO{} }, func(t *testing.T, value any) {
			if len(value.(*FileSearchResultDTO).Matches) != 1 {
				t.Fatalf("matches 丢失: %+v", value)
			}
		}},
		{"git status", "git-status.json", func() any { return &GitStatusSnapshotDTO{} }, func(t *testing.T, value any) {
			if value.(*GitStatusSnapshotDTO).Branch != "main" {
				t.Fatalf("branch 丢失: %+v", value)
			}
		}},
		{"pty session", "pty-session.json", func() any { return &PtySessionDTO{} }, func(t *testing.T, value any) {
			if value.(*PtySessionDTO).Incarnation != "inc-1" {
				t.Fatalf("incarnation 丢失: %+v", value)
			}
		}},
		{"pty frame", "pty-frame.json", func() any { return &PtyServerFrameDTO{} }, func(t *testing.T, value any) {
			if value.(*PtyServerFrameDTO).Seq != 7 {
				t.Fatalf("seq 丢失: %+v", value)
			}
		}},
		{"preview", "preview-session.json", func() any { return &PreviewSessionDTO{} }, func(t *testing.T, value any) {
			if value.(*PreviewSessionDTO).PreviewSessionID != "preview-1" {
				t.Fatalf("preview id 丢失: %+v", value)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := readTestdata(t, tc.file)
			value := tc.newValue()
			if err := json.Unmarshal(raw, value); err != nil {
				t.Fatalf("Unmarshal %s: %v", tc.file, err)
			}
			tc.check(t, value)
			if _, err := json.Marshal(value); err != nil {
				t.Fatalf("Marshal %s: %v", tc.file, err)
			}
		})
	}
}
