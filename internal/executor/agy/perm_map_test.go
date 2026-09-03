package agy

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestPermTextAndRequestWriteFile(t *testing.T) {
	text, req := permTextAndRequest("write_file", json.RawMessage(`{"TargetFile":"/work/main.go"}`))
	if req == nil || req.Tool != executor.PermToolWrite || len(req.Paths) != 1 || req.Paths[0] != "/work/main.go" {
		t.Fatalf("write_file 必须进写路径判据，text=%q req=%#v", text, req)
	}
}

func TestPermTextAndRequestViewFileInScopePath(t *testing.T) {
	_, req := permTextAndRequest("view_file", json.RawMessage(`{"AbsolutePath":"/work/readme.md"}`))
	if req == nil || req.Tool != executor.PermToolEdit || len(req.Paths) != 1 || req.Paths[0] != "/work/readme.md" {
		t.Fatalf("view_file 必须带路径走范围内自动放行，req=%#v", req)
	}
}

func TestPermTextAndRequestListDirPath(t *testing.T) {
	_, req := permTextAndRequest("list_dir", json.RawMessage(`{"DirectoryPath":"/work"}`))
	if req == nil || req.Tool != executor.PermToolEdit || len(req.Paths) != 1 || req.Paths[0] != "/work" {
		t.Fatalf("list_dir 必须带 DirectoryPath 走范围内自动放行，req=%#v", req)
	}
}

func TestPermTextAndRequestUnknownIsOther(t *testing.T) {
	_, req := permTextAndRequest("manage_task", json.RawMessage(`{"name":"x"}`))
	if req == nil || req.Tool != executor.PermToolOther {
		t.Fatalf("未识别工具必须是 Other（Consult）而不是 nil 升级，req=%#v", req)
	}
}
