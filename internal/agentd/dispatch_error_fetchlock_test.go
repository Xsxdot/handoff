// dispatch_error_fetchlock_test.go —— dispatch 错误映射中远端 ref 锁竞争/分叉/基线缺失的区分。
//
// 职责：钉死锁竞争是可行动的 400，而不是误导成「基线不存在」或「请先 push」。
// 边界：只测 HTTP 状态码与文案映射，不改工作区行为。
package agentd

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/workspace"
)

// TestWriteDispatchErrorKeepsFetchLockContentionDistinct 验证远端 ref 锁竞争是
// 可行动的 400，而不是误导成「基线不存在」或「请先 push」。
func TestWriteDispatchErrorKeepsFetchLockContentionDistinct(t *testing.T) {
	srv := &Server{log: slog.Default()}
	lockErr := fmt.Errorf("%w: fatal: cannot lock ref refs/remotes/origin/main", workspace.ErrFetchRefLockContention)

	lockResp := httptest.NewRecorder()
	srv.writeDispatchError(lockResp, "project-lock", lockErr)
	lockBody := lockResp.Body.String()
	if lockResp.Code != http.StatusBadRequest {
		t.Fatalf("锁竞争状态码=%d，期望 400；body=%s", lockResp.Code, lockBody)
	}
	for _, want := range []string{"基线补拉失败（远端 ref 锁竞争）", "cannot lock ref"} {
		if !strings.Contains(lockBody, want) {
			t.Errorf("锁竞争 body 缺少 %q: %s", want, lockBody)
		}
	}
	for _, forbidden := range []string{"基线提交在任务仓库中不存在", "请先在本地 git push"} {
		if strings.Contains(lockBody, forbidden) {
			t.Errorf("锁竞争 body 不应含 %q: %s", forbidden, lockBody)
		}
	}

	divergedResp := httptest.NewRecorder()
	divergedErr := fmt.Errorf("%w：本地=%s，origin=%s", workspace.ErrLocalBaseBranchDiverged,
		strings.Repeat("2", 40), strings.Repeat("3", 40))
	srv.writeDispatchError(divergedResp, "project-diverged", divergedErr)
	if divergedResp.Code != http.StatusBadRequest {
		t.Fatalf("分叉状态码=%d，期望 400；body=%s", divergedResp.Code, divergedResp.Body.String())
	}
	for _, want := range []string{strings.Repeat("2", 40), strings.Repeat("3", 40), "先合并再派"} {
		if !strings.Contains(divergedResp.Body.String(), want) {
			t.Errorf("分叉 body 缺少 %q: %s", want, divergedResp.Body.String())
		}
	}

	missingResp := httptest.NewRecorder()
	missingErr := fmt.Errorf("%w: abc；请先在本地 git push", workspace.ErrBaseCommitMissing)
	srv.writeDispatchError(missingResp, "project-missing", missingErr)
	if missingResp.Code != http.StatusBadRequest {
		t.Fatalf("真缺失状态码=%d，期望 400；body=%s", missingResp.Code, missingResp.Body.String())
	}
	for _, want := range []string{"基线提交在任务仓库中不存在", "请先在本地 git push"} {
		if !strings.Contains(missingResp.Body.String(), want) {
			t.Errorf("真缺失 body 缺少 %q: %s", want, missingResp.Body.String())
		}
	}
}
