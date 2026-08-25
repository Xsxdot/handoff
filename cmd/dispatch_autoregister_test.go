package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TestIsProjectNotRegistered 验证 CLI 能从 agentd 的 400 报文里认出
// 「项目未登记」，从而触发自动补登记后重发（spec §6.2）。
//
// 为什么按文本判：错误跨进程传递，errors.Is 在这一侧失效；agentd 的
// ErrProjectNotRegistered 报文以「项目未登记」四字开头，两边靠这个约定对齐。
func TestIsProjectNotRegistered(t *testing.T) {
	yes := []string{
		"dispatch 失败: HTTP 400: 项目未登记: project_id=9f2a1c7d5e3b0a84；本机已登记的项目：（本机尚无任何项目）",
		"项目未登记: \"nova\"；本机已登记的项目：handoff → /w/handoff",
	}
	for _, s := range yes {
		if !isProjectNotRegistered(errStr(s)) {
			t.Errorf("应识别为未登记: %q", s)
		}
	}
	no := []string{
		"dispatch 失败: HTTP 409: 工作区不干净",
		"dispatch 失败: HTTP 400: 请求未指明项目（project_id 与 project_name 至少其一）",
		"",
	}
	for _, s := range no {
		if isProjectNotRegistered(errStr(s)) {
			t.Errorf("不应识别为未登记: %q", s)
		}
	}
	if isProjectNotRegistered(nil) {
		t.Error("nil 不应识别为未登记")
	}
}

// errStr 把字符串包成 error，供表驱动用例使用。
type errStr string

func (e errStr) Error() string { return string(e) }

// TestDispatchWithAutoRegisterRetriesOnce 验证编排的正常路径：
// 首次派发被拒 → 触发一次登记 → 重发成功。
func TestDispatchWithAutoRegisterRetriesOnce(t *testing.T) {
	dispatches, registers := 0, 0
	task, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			if dispatches == 1 {
				return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
			}
			return &proto.Task{ID: "t1"}, nil
		},
		func() error { registers++; return nil },
	)
	if err != nil {
		t.Fatalf("重发应成功: %v", err)
	}
	if task == nil || task.ID != "t1" {
		t.Fatalf("应返回重发得到的任务, got %+v", task)
	}
	if dispatches != 2 || registers != 1 {
		t.Fatalf("应派发 2 次、登记 1 次，got dispatch=%d register=%d", dispatches, registers)
	}
}

// TestDispatchWithAutoRegisterGivesUpAfterOneRetry 验证登记成功后仍被拒时**不再重试**：
// 那说明另有原因（如刚被别人 project rm 掉），无限重试会把可诊断的失败变成死循环。
func TestDispatchWithAutoRegisterGivesUpAfterOneRetry(t *testing.T) {
	dispatches, registers := 0, 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
		},
		func() error { registers++; return nil },
	)
	if err == nil {
		t.Fatal("持续被拒时应返回错误")
	}
	if dispatches != 2 || registers != 1 {
		t.Fatalf("最多派发 2 次、登记 1 次，got dispatch=%d register=%d", dispatches, registers)
	}
}

// TestDispatchWithAutoRegisterSurfacesRegisterFailure 验证登记失败时透出原文、
// **不重发**：clone 失败或落点被占都需要人去那台机器上处置，替它猜只会掩盖真因。
func TestDispatchWithAutoRegisterSurfacesRegisterFailure(t *testing.T) {
	dispatches := 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
		},
		func() error { return errStr("落点 /root/work/handoff 已存在") },
	)
	if err == nil {
		t.Fatal("登记失败时 dispatch 应整体失败")
	}
	if !strings.Contains(err.Error(), "落点 /root/work/handoff 已存在") {
		t.Errorf("应透出登记失败原文，got %q", err.Error())
	}
	if dispatches != 1 {
		t.Fatalf("登记失败后不应重发，got dispatch=%d", dispatches)
	}
}

// TestDispatchWithAutoRegisterPassesThroughOtherErrors 验证非「未登记」的错误
// 原样透出，绝不触发登记——工作区不干净之类的失败自动登记帮不上任何忙。
func TestDispatchWithAutoRegisterPassesThroughOtherErrors(t *testing.T) {
	sentinel := errStr("HTTP 409: 工作区不干净")
	registers := 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) { return nil, sentinel },
		func() error { registers++; return nil },
	)
	if !errors.Is(err, error(sentinel)) {
		t.Fatalf("应原样透出原错误，got %v", err)
	}
	if registers != 0 {
		t.Fatalf("不该触发登记，got register=%d", registers)
	}
}

// cleanRepoWithOrigin 在临时目录造一个「有 origin、工作区干净、有一个提交」
// 的仓库并 chdir 进去。dispatch 需要这三样：origin 派生项目身份、
// 干净工作区过 checkLocalWorktree、HEAD 算基线。
func cleanRepoWithOrigin(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	git("remote", "add", "origin", "git@example.com:x/handoff.git")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	t.Chdir(repo)
	return repo
}

// TestDispatchAutoRegisterSurvivesMissingLocalAgentd 是纯协调者机首次派发的
// 端到端回归：目标机不认识这个项目 → CLI 自动登记 → 本机那一跳够不着被降级
// → 目标机登记成功 → 重发派发成功。
//
// 修复前的症状：整条命令停在「登记到本机: … connection refused」，
// 目标机那一跳一个字节都没发出去。
func TestDispatchAutoRegisterSurvivesMissingLocalAgentd(t *testing.T) {
	cleanRepoWithOrigin(t)

	var taskHits, projectHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			// B229 起派发前必探能力位（§3.1 拒发闸）；本测试钉的是自动登记编排。
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"disciplines_supported":true}`)
		case "/api/projects":
			projectHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, projectHopJSON)
		case "/api/tasks":
			// 第一次派发：目标机不认识这个项目，报文以「项目未登记」开头
			// （CLI 靠这四个字触发自动登记，见 isProjectNotRegistered）
			if taskHits.Add(1) == 1 {
				http.Error(w, "项目未登记: project_id=pid1；本机已登记的项目：（本机尚无任何项目）",
					http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, dispatchTestTaskJSON)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	addr := strings.TrimPrefix(ts.URL, "http://")
	cfg := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+addr+"\"\n    token: \"remote-tok\"\n")
	resetFlags(t)
	configPath = cfg
	targetName = "devbox"
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false

	rootCmd.SetArgs([]string{"dispatch", "--target", "devbox", "--prompt", "x", "--no-terminal"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	if err := Execute(); err != nil {
		t.Fatalf("本机没有 agentd 时首次派发应当成功: %v（stderr=%q）", err, errBuf.String())
	}
	if got := projectHits.Load(); got != 1 {
		t.Errorf("目标机应收到 1 次登记，实得 %d", got)
	}
	if got := taskHits.Load(); got != 2 {
		t.Errorf("派发应发生 2 次（首拒 + 重发），实得 %d", got)
	}
	if !strings.Contains(errBuf.String(), "跳过本机登记") {
		t.Errorf("降级必须说出来，stderr=%q", errBuf.String())
	}
	// stdout 契约：第一行必须是任务 JSON，降级提示一个字都不许漏进来
	first := strings.SplitN(strings.TrimSpace(out.String()), "\n", 2)[0]
	if !strings.HasPrefix(first, "{") {
		t.Errorf("stdout 第一行必须是任务 JSON，实得 %q", first)
	}
}
