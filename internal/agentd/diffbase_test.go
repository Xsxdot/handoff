// diffbase_test.go —— diff 缺省基准的取值规则（B65）：任务有 BaseCommit 就用它，
// 没有才退回按仓库推导。
//
// 边界：本文件不测 Diff() 本身的输出格式（既有行为未变），只测「缺省基准取谁」
// 以及两个端点是否一致地采用了同一个取值。
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// TestDiffBaseForPrefersTaskBaseCommit 钉住优先级：BaseCommit 非空即用它。
func TestDiffBaseForPrefersTaskBaseCommit(t *testing.T) {
	repo := initTestRepo(t)
	task := &proto.Task{ID: "t1", BaseCommit: "0123456789abcdef0123456789abcdef01234567"}
	if got := diffBaseFor(task, repo); got != task.BaseCommit {
		t.Errorf("应优先用任务基线：got=%q want=%q", got, task.BaseCommit)
	}
}

// TestDiffBaseForFallsBackWhenNoBaseCommit 钉住退回：BaseCommit 为空（切已存在
// 分支或老任务）时按仓库推导，退回是正常分支不是兜底。
func TestDiffBaseForFallsBackWhenNoBaseCommit(t *testing.T) {
	repo := initTestRepo(t) // initTestRepo 建的是 main 分支
	task := &proto.Task{ID: "t1"}
	if got := diffBaseFor(task, repo); got != "main" {
		t.Errorf("应退回推导链：got=%q want=%q", got, "main")
	}
}

// TestBranchesEndpointReportsTaskBase 钉住端点一致性：branches 必须把 diff 实际
// 会用的任务基线报出来，否则前端「自动推导（…）」会显示与实际不符的值。
func TestBranchesEndpointReportsTaskBase(t *testing.T) {
	const token = "diffbase-token"
	const sha = "0123456789abcdef0123456789abcdef01234567"
	repo := initTestRepo(t)

	st, err := store.Open(t.TempDir() + "/diffbase.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{
		ID: "t1", Target: "local", State: proto.TaskStatePending,
		WorkDir: repo, BaseCommit: sha, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	srv := NewServer(&config.Config{Token: token, DataDir: t.TempDir()}, st, discardLogger())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks/t1/branches", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 branches: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	var body struct {
		Branches []string `json:"branches"`
		Default  string   `json:"default"`
		TaskBase string   `json:"task_base"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if body.TaskBase != sha {
		t.Errorf("task_base 不对：got=%q want=%q", body.TaskBase, sha)
	}
	if !strings.Contains(strings.Join(body.Branches, ","), "main") {
		t.Errorf("分支列表应含 main：%v", body.Branches)
	}
}
