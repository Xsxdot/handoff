// agentd 换版接口测试（package agentd：复用 watchdog_test.go 的 newTestStore）。
//
// 覆盖 B59 Task 3 的 7 个用例：两道闸（非托管硬拒绝且 force 不越过 / 活跃任务
// 默认拒绝可 force 越过）、waiting_review 不计入活跃、body 为空只重启、缺 tag/sha256
// 拒绝、校验/自检失败不许走到 Activate。全部经注入的 UpdateDeps 桩，不碰真实
// 二进制、真实进程管理器、真实 GitHub。
package agentd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// seedTask 以指定状态落一条任务。CreateTask 原样入库不经状态机，
// 直接给最终状态即可（watchdog_test 的 seedRunningTask 等是「创建后再迁移」
// 的另一条路，这里不需要中间态）。
func seedTask(t *testing.T, st *store.Store, state proto.TaskState) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: uuid.NewString(), Target: "local", State: state, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

// newUpdateServer 起一个只用于换版接口的 Server：注入全部外部依赖，
// 一次也不碰真实二进制、真实进程管理器、真实 GitHub。
func newUpdateServer(t *testing.T, st *store.Store, managed bool) (*Server, *[]string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&config.Config{Token: "tk", DataDir: t.TempDir()}, st, log)
	acts := &[]string{}
	target := filepath.Join(t.TempDir(), "handoff")
	os.WriteFile(target, []byte("old"), 0o755)
	srv.SetUpdateDeps(UpdateDeps{
		Getenv: func(k string) string {
			if managed && k == "INVOCATION_ID" {
				return "test"
			}
			return ""
		},
		Executable: func() (string, error) { return target, nil },
		Install: func(_ []byte, _, tag, destDir string) (string, error) {
			p := filepath.Join(destDir, ".handoff.new-"+tag)
			os.WriteFile(p, []byte("new"), 0o755)
			return p, nil
		},
		Activate: func(newPath, tgt string) (string, error) {
			*acts = append(*acts, newPath+"->"+tgt)
			return tgt + ".prev", nil
		},
	})
	srv.SetRestart(func(reason string) bool { *acts = append(*acts, "restart:"+reason); return true })
	return srv, acts
}

// post 发一次换版请求，返回状态码与解出的错误体（200 时 UpdateError 为零值）。
func post(t *testing.T, srv *Server, query string, body []byte) (int, proto.UpdateError, proto.UpdateResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/update?"+query, bytes.NewReader(body))
	// httptest.NewRequest 的默认 Host 是 example.com，会被 hostGuard 在鉴权前
	// 403 掉（W3 的 Host 白名单）。本组用例测的是换版的两道闸，不是白名单，
	// 因此显式给一个回环 Host 让请求走到 handler。
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Authorization", "Bearer tk")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var e proto.UpdateError
	var ok proto.UpdateResp
	json.Unmarshal(w.Body.Bytes(), &e)
	json.Unmarshal(w.Body.Bytes(), &ok)
	return w.Code, e, ok
}

// TestUpdateRejectsUnmanagedEvenWithForce 是闸二的核心断言，也是整个接口
// 最不能出错的一条：换完 exit(0) 之后没人拉起，这台机器上就此没有 agentd
// 在跑，且没有任何信号告诉任何人。force 不是逃生口。
func TestUpdateRejectsUnmanagedEvenWithForce(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, false /*managed*/)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64)+"&force=1", []byte("tgz"))
	if code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409", code)
	}
	if e.Reason != proto.UpdateReasonUnmanaged {
		t.Fatalf("reason = %q，期望 %q", e.Reason, proto.UpdateReasonUnmanaged)
	}
	if len(*acts) != 0 {
		t.Fatalf("被拒后不该有任何换版或重启动作，实得 %v", *acts)
	}
}

// TestUpdateRejectsBusyWithoutForce / TestUpdateForceCrossesBusy 是闸一的
// 两半：默认保护，--force 越过。activeTaskCount 的口径是 running +
// waiting_answer，waiting_review 不计入（它可能挂几天）。
func TestUpdateRejectsBusyWithoutForce(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateRunning)
	srv, acts := newUpdateServer(t, st, true)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusConflict || e.Reason != proto.UpdateReasonBusy {
		t.Fatalf("期望 409/busy，实得 %d/%q", code, e.Reason)
	}
	if len(*acts) != 0 {
		t.Fatalf("被拒后不该有动作，实得 %v", *acts)
	}
}

func TestUpdateForceCrossesBusy(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateRunning)
	srv, acts := newUpdateServer(t, st, true)
	code, _, ok := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64)+"&force=1", []byte("tgz"))
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if !ok.OK || ok.Version != "v9.9.9" || ok.Prev == "" {
		t.Fatalf("响应体不完整: %+v", ok)
	}
	if len(*acts) != 2 || !strings.HasPrefix((*acts)[1], "restart:") {
		t.Fatalf("必须先 Activate 再 restart，实得 %v", *acts)
	}
}

// TestUpdateWaitingReviewDoesNotBlock 锁住活跃口径：waiting_review 不计入。
func TestUpdateWaitingReviewDoesNotBlock(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateWaitingReview)
	srv, _ := newUpdateServer(t, st, true)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusOK {
		t.Fatalf("waiting_review 不该拦下换版，实得 %d/%q", code, e.Error)
	}
}

// TestUpdateEmptyBodyRestartsOnly 是 D8：不带 body = 只重启不换版。
// 本机的二进制由 CLI 直接换（文件就在本地），但仍需要 agentd 重启才生效。
func TestUpdateEmptyBodyRestartsOnly(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	code, _, ok := post(t, srv, "", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if ok.Version != "" {
		t.Fatalf("纯重启不该报换上的版本，实得 %q", ok.Version)
	}
	if len(*acts) != 1 || !strings.HasPrefix((*acts)[0], "restart:") {
		t.Fatalf("纯重启只该有 restart 一个动作，实得 %v", *acts)
	}
}

// TestUpdateRequiresTagAndSum：带 body 就必须给 tag 与 sha256，
// 缺任一个都不能放行——少了它们，agentd 侧的完整性校验与自检都无从比对。
func TestUpdateRequiresTagAndSum(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	for _, q := range []string{"", "tag=v9.9.9", "sha256=" + strings.Repeat("a", 64)} {
		code, e, _ := post(t, srv, q, []byte("tgz"))
		if code != http.StatusBadRequest {
			t.Fatalf("query %q 应 400，实得 %d", q, code)
		}
		if e.Reason != "" {
			t.Fatalf("参数错不属于两道闸，reason 必须为空，实得 %q", e.Reason)
		}
	}
	if len(*acts) != 0 {
		t.Fatalf("参数错不该有动作，实得 %v", *acts)
	}
}

// TestUpdateInstallFailureDoesNotActivate：校验/解包/自检任一失败，
// 都不许走到 Activate——换上一个跑不起来的二进制，agentd 就再也起不来了。
func TestUpdateInstallFailureDoesNotActivate(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	srv.SetUpdateDeps(UpdateDeps{
		Getenv: func(k string) string {
			if k == "INVOCATION_ID" {
				return "test"
			}
			return ""
		},
		Executable: func() (string, error) { return filepath.Join(t.TempDir(), "handoff"), nil },
		Install:    func([]byte, string, string, string) (string, error) { return "", errors.New("自检失败") },
		Activate:   func(string, string) (string, error) { t.Fatal("不该走到 Activate"); return "", nil },
	})
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", code)
	}
	if !strings.Contains(e.Error, "自检失败") {
		t.Fatalf("错误原文必须带出根因，实得 %q", e.Error)
	}
	if len(*acts) != 0 {
		t.Fatalf("失败后不该有动作，实得 %v", *acts)
	}
}
