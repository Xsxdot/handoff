// gc_test.go —— handoff gc CLI 的旧 agentd 降级行为测试。
//
// 职责：
//   - 锁定预览模式收到 404 时输出可行动的版本过旧提示
//   - 证明 CLI 将 gc 预览请求交给 client，而不在本地猜测清理结果
//
// 边界：
//   - 不验证 agentd 的实际清理逻辑，那属于 internal/agentd 的测试范围
//   - 不复用 rootCmd 执行，避免测试全局 flag 与配置解析污染本用例
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// TestRunGCDegradesOnOldAgentd 锁定 gc 预览遇到双端点旧版本判定后的成功提示。
func TestRunGCDegradesOnOldAgentd(t *testing.T) {
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	oldForce, oldYes, oldJSON := gcForce, gcYes, gcJSON
	gcForce, gcYes, gcJSON = true, false, false
	t.Cleanup(func() { gcForce, gcYes, gcJSON = oldForce, oldYes, oldJSON })

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	if err := runGC(cmd, client.New(ts.URL, "test-token"), ts.URL); err != nil {
		t.Fatalf("旧 agentd 预览应成功降级，得到错误: %v", err)
	}
	if !strings.Contains(out.String(), "过旧") {
		t.Fatalf("旧 agentd 提示应包含过旧，实得：%s", out.String())
	}
	if len(paths) != 1 || paths[0] != "GET /api/gc?force=true" {
		t.Fatalf("预览请求应只发 GET /api/gc?force=true，实得：%v", paths)
	}
}

// TestRenderGCDistinguishesUnknownBytes 锁定人读输出对字节量缺席与零值的区分。
func TestRenderGCDistinguishesUnknownBytes(t *testing.T) {
	zero := int64(0)
	var computed bytes.Buffer
	renderGC(&computed, &proto.GCResp{ReleasableBytes: &zero})
	if got := computed.String(); !strings.Contains(got, "将释放字节：0") {
		t.Fatalf("已计算为零的报告应显示 0，实得：%s", got)
	}

	var unknown bytes.Buffer
	renderGC(&unknown, &proto.GCResp{})
	if got := unknown.String(); !strings.Contains(got, "将释放字节：未计算") {
		t.Fatalf("未计算字节的报告应显示未计算，实得：%s", got)
	}
}

func withGCFlags(t *testing.T, force, yes, jsonOut bool) {
	t.Helper()
	oldF, oldY, oldJ := gcForce, gcYes, gcJSON
	gcForce, gcYes, gcJSON = force, yes, jsonOut
	t.Cleanup(func() { gcForce, gcYes, gcJSON = oldF, oldY, oldJ })
}

func newRunGCCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	return cmd, &out
}

func TestRunGCPreviewUsesGETAndDoesNotPost(t *testing.T) {
	withGCFlags(t, false, false, false)
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		zero := int64(0)
		_ = json.NewEncoder(w).Encode(&proto.GCResp{Preview: true, ReleasableBytes: &zero, CacheRows: []proto.GCCacheRow{}, WorktreeRows: []proto.GCWorktreeRow{}, Scanned: 3})
	}))
	t.Cleanup(ts.Close)
	cmd, out := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatalf("预览应退出 0: %v", err)
	}
	if len(paths) != 1 || paths[0] != "GET /api/gc" {
		t.Fatalf("无 --yes 只发 GET /api/gc，实得 %v", paths)
	}
	if !strings.Contains(out.String(), "将释放字节：0") {
		t.Fatalf("预览必须打字节量：%s", out.String())
	}
	if !strings.Contains(out.String(), "共扫") || !strings.Contains(out.String(), "3") {
		t.Fatalf("预览应打共扫终态任务数：%s", out.String())
	}
}

func TestRunGCForceWithoutYesStillGET(t *testing.T) {
	withGCFlags(t, true, false, false)
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"preview":true,"force":true,"releasable_bytes":0,"cache_rows":[],"worktree_rows":[],"scanned":0,"failures":0}`))
	}))
	t.Cleanup(ts.Close)
	cmd, _ := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "GET /api/gc?force=true" {
		t.Fatalf("仅 --force 应 GET ?force=true，实得 %v", paths)
	}
}

func TestRunGCYesPostsForceBody(t *testing.T) {
	withGCFlags(t, true, true, true)
	var paths []string
	var body proto.GCRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&proto.GCResp{Preview: false, Force: true, CacheRows: []proto.GCCacheRow{}, WorktreeRows: []proto.GCWorktreeRow{}})
	}))
	t.Cleanup(ts.Close)
	cmd, _ := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "POST /api/gc" {
		t.Fatalf(" --yes 应只 POST /api/gc，实得 %v", paths)
	}
	if !body.Force {
		t.Fatal(" --yes --force 必须把 force=true 放进 JSON body")
	}
}

func TestRunGCJSONDistinguishesAbsentAndZero(t *testing.T) {
	withGCFlags(t, false, false, true)
	cases := []struct {
		name string
		body string
		has  bool
		zero bool
	}{
		{"zero", `{"preview":true,"force":false,"releasable_bytes":0,"cache_rows":[],"worktree_rows":[],"scanned":0,"failures":0}`, true, true},
		{"absent", `{"preview":true,"force":false,"cache_rows":[],"worktree_rows":[],"scanned":0,"failures":0}`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(ts.Close)
			cmd, out := newRunGCCmd(t)
			if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
				t.Fatalf("cli json: %v %s", err, out.String())
			}
			raw, ok := fields["releasable_bytes"]
			if ok != tc.has {
				t.Fatalf("present=%v want %v (%s)", ok, tc.has, out.String())
			}
			if tc.zero && string(raw) != "0" {
				t.Fatalf("want 0 got %s", raw)
			}
		})
	}
}

func TestRunGCExecuteFailuresNonZero(t *testing.T) {
	withGCFlags(t, false, true, false)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&proto.GCResp{
			Preview:  false,
			Failures: 1,
			CacheRows: []proto.GCCacheRow{{
				TaskID: "t1", Path: "/tmp/x", Status: proto.GCItemFailed, Error: "e",
			}},
			WorktreeRows: []proto.GCWorktreeRow{},
		})
	}))
	t.Cleanup(ts.Close)
	cmd, out := newRunGCCmd(t)
	err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL)
	if err == nil {
		t.Fatal("Failures>0 的 execute 必须非零")
	}
	if !strings.Contains(out.String(), "失败") && !strings.Contains(err.Error(), "失败") {
		t.Fatalf("必须能看见失败：stdout=%s err=%v", out.String(), err)
	}
}

func TestRunGCExecuteSkipIsZero(t *testing.T) {
	withGCFlags(t, false, true, false)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		zero := int64(12)
		_ = json.NewEncoder(w).Encode(&proto.GCResp{
			Preview: false, Failures: 0, ReleasableBytes: &zero,
			CacheRows:    []proto.GCCacheRow{{TaskID: "t", Path: "/p", Status: proto.GCItemDeleted, Bytes: 12}},
			WorktreeRows: []proto.GCWorktreeRow{{TaskID: "t2", Status: proto.GCItemSkipped, Note: "脏"}},
			Scanned:      2,
		})
	}))
	t.Cleanup(ts.Close)
	cmd, out := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatalf("仅 skip 应退出 0: %v %s", err, out.String())
	}
}

func TestGCCmdRejectsPositionalArgs(t *testing.T) {
	if err := gcCmd.Args(gcCmd, []string{"task-id"}); err == nil {
		t.Fatal("handoff gc 不得接受位置参数")
	}
}

func TestGCCmdReusesRootTargetFlag(t *testing.T) {
	if gcCmd.Flags().Lookup("target") != nil {
		t.Fatal("gc 不得自建 --target，必须复用 root persistent / newTargetClient")
	}
	if rootCmd.PersistentFlags().Lookup("target") == nil {
		t.Fatal("root 必须已有 --target")
	}
}

func TestRenderGCShowsFourStatuses(t *testing.T) {
	var buf bytes.Buffer
	zero := int64(1)
	renderGC(&buf, &proto.GCResp{
		Preview: true, ReleasableBytes: &zero, Scanned: 4,
		CacheRows: []proto.GCCacheRow{
			{TaskID: "a", Path: "/a", Status: proto.GCItemPlanned, Bytes: 1},
			{TaskID: "b", Path: "/b", Status: proto.GCItemDeleted},
			{TaskID: "c", Path: "/c", Status: proto.GCItemSkipped, Error: "占用"},
			{TaskID: "d", Path: "/d", Status: proto.GCItemFailed, Error: "e"},
		},
		WorktreeRows: []proto.GCWorktreeRow{
			{TaskID: "e", Status: proto.GCItemSkipped, Worktree: proto.WorktreeDirty, DirtyCount: 1},
		},
	})
	got := buf.String()
	for _, want := range []string{"将删", "已删", "跳过", "失败"} {
		if !strings.Contains(got, want) {
			t.Fatalf("渲染缺少 %q：%s", want, got)
		}
	}
}
