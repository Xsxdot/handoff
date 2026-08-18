package agentd

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newBundleEnv 构造一个带真 git 仓库的测试环境，并入库一个指向它的任务。
//
// 返回：测试环境、任务 ID、仓库路径、base 提交 sha。
//
// 为什么直接 CreateTask 而不是建完再改：store.SetTaskField 的白名单只有
// branch/executor_session/plan_summary/done_note 四项，repo_path 改不了。
func newBundleEnv(t *testing.T, branch string) (env *testAgentdEnv, taskID, repo, baseSHA string) {
	t.Helper()
	env = newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken, DataDir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	repo, baseSHA = newBundleRepo(t)
	taskID = "t-bundle"
	if err := env.st.CreateTask(&proto.Task{
		ID: taskID, RepoPath: repo, Branch: branch, State: proto.TaskStateWaitingReview,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return env, taskID, repo, baseSHA
}

// getBundle 打一次 bundle 请求，返回响应与已读完的包体。
func getBundle(t *testing.T, env *testAgentdEnv, taskID, have string) (*http.Response, []byte) {
	t.Helper()
	url := env.ts.URL + "/api/tasks/" + taskID + "/bundle"
	if have != "" {
		url += "?have=" + have
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET bundle: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	return resp, body
}

// 正常薄包：200 + octet-stream + Content-Length 与实际字节数一致。
func TestHandleTaskBundleOK(t *testing.T) {
	env, taskID, _, base := newBundleEnv(t, "feat/x")

	resp, body := getBundle(t, env, taskID, base)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码应为 200，实得 %d，体 %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type 应为 application/octet-stream，实得 %q", ct)
	}
	if len(body) == 0 {
		t.Fatal("包体不该为空")
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %q 与实际字节数 %d 不符", got, len(body))
	}
}

// 无 have：全量包也必须成功，且比薄包大。
func TestHandleTaskBundleFull(t *testing.T) {
	env, taskID, _, base := newBundleEnv(t, "feat/x")

	respThin, thin := getBundle(t, env, taskID, base)
	if respThin.StatusCode != http.StatusOK {
		t.Fatalf("薄包应为 200，实得 %d", respThin.StatusCode)
	}
	respFull, full := getBundle(t, env, taskID, "")
	if respFull.StatusCode != http.StatusOK {
		t.Fatalf("全量包应为 200，实得 %d", respFull.StatusCode)
	}
	if len(full) <= len(thin) {
		t.Errorf("全量包应大于薄包，实得 full=%d thin=%d", len(full), len(thin))
	}
}

// 承重：区间为空时端点**不回 204**，而是返回一个照样带 ref 的包。
//
// 判据必须落到「包里真的有那个 ref」——只断言 200 的话，一个不含 ref 的包也能
// 蒙混过去，而客户端的本地分支引用正是靠包里这个 ref 建出来的（B143 真机验收）。
func TestHandleTaskBundleEmptyRangeStillCarriesRef(t *testing.T) {
	env, taskID, repo, _ := newBundleEnv(t, "feat/x")
	head := headSHAForTest(t, repo, "feat/x")

	resp, body := getBundle(t, env, taskID, head)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("空区间应放宽后回 200，实得 %d，体 %s", resp.StatusCode, body)
	}
	tmp := filepath.Join(t.TempDir(), "widened.bundle")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		t.Fatalf("落盘: %v", err)
	}
	if heads := bundleHeads(t, tmp); heads["refs/heads/feat/x"] != head {
		t.Fatalf("包里应含 refs/heads/feat/x 且指向 %s，实得 %v", head, heads)
	}
}

// have 在任务仓库里不存在：400，报文带上那个 sha。
func TestHandleTaskBundleHaveMissing(t *testing.T) {
	env, taskID, _, _ := newBundleEnv(t, "feat/x")

	absent := "0123456789abcdef0123456789abcdef01234567"
	resp, body := getBundle(t, env, taskID, absent)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("have 不存在应为 400，实得 %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), absent) {
		t.Errorf("400 报文应带上该 sha 才能排障，实得 %s", body)
	}
}

// 任务尚无分支：400，而不是 500。
func TestHandleTaskBundleNoBranch(t *testing.T) {
	env, taskID, _, _ := newBundleEnv(t, "")

	resp, _ := getBundle(t, env, taskID, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("无分支应为 400，实得 %d", resp.StatusCode)
	}
}

// 任务不存在：404（byTask 已有的行为，这里锁住它不被新端点绕开）。
func TestHandleTaskBundleTaskNotFound(t *testing.T) {
	env, _, _, _ := newBundleEnv(t, "feat/x")

	resp, _ := getBundle(t, env, "no-such-task", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("任务不存在应为 404，实得 %d", resp.StatusCode)
	}
}
