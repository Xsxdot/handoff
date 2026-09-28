// default_slice_test.go 是 B392 §8.3 的**默认生产竖切**（唯一承重守卫）：
// **不调用 swapCore/swapSessions**，直接用包级默认 liveCore（DefaultDial）
// 走 Pair → SwitchMachine → SessionCookie → Close，用真实 Core + 本地 HTTP
// 对端夹具，断言真实 cookie 值与归属。
//
// 为什么要子进程：liveCore 是包级单例，Close 不可逆、配对会登记机器并起回环
// 反代——若在主测试进程里跑会污染同包其它测试、并使结果依赖测试次序。故真正的
// 竖切放进**子进程**（-test.run 只跑本测试，liveCore 天然干净）；父进程只起真实
// HTTP 对端夹具、把夹具地址经环境变量交给子进程、校验子进程退出。
//
// 禁止以 swap 版竖切或纯身份断言替代本文件（review P0）。独立 swap 真实 Core
// 竖切（realcore_test.go）只作行为补充，不顶替本文件。
package bind

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

const (
	envDefaultSliceChild = "B392_DEFAULT_SLICE_CHILD"
	envAgentdAURL        = "B392_AGENTD_A_URL"
	envAgentdBURL        = "B392_AGENTD_B_URL"

	childTimeout     = 60 * time.Second // 子进程整体时限
	httpProbeTimeout = 5 * time.Second  // 夹具回读/探测的 HTTP 时限
)

// childEnv 过滤父环境里的 B392_* 键后再注入子进程需要的键值：避免父进程或上一次
// 运行残留的子进程标记/夹具地址导致子进程递归或串到旧夹具。
func childEnv(extra map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "B392_") {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// TestDefaultProductionVerticalSlice 是默认生产竖切入口：父进程起夹具并 fork
// 子进程；子进程用默认 liveCore 跑 runDefaultProductionVerticalSlice。子进程带
// 超时（exec.CommandContext）；卡死即失败，不留悬挂进程。
func TestDefaultProductionVerticalSlice(t *testing.T) {
	if os.Getenv(envDefaultSliceChild) == "1" {
		runDefaultProductionVerticalSlice(t)
		return
	}
	fa := newFakeAgentd("A")
	defer fa.close()
	fb := newFakeAgentd("B")
	defer fb.close()

	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDefaultProductionVerticalSlice$", "-test.v")
	cmd.Env = childEnv(map[string]string{
		envDefaultSliceChild: "1",
		envAgentdAURL:        fa.server.URL,
		envAgentdBURL:        fb.server.URL,
	})
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("默认生产竖转子进程超时(%s)：\n%s", childTimeout, out)
	}
	if err != nil {
		t.Fatalf("默认生产竖转子进程失败: %v\n%s", err, out)
	}
	// 必须亲眼看到本测试在子进程里 RUN 且 PASS：拒绝 SKIP / no tests / 只剩汇总行。
	outText := string(out)
	if strings.Contains(outText, "SKIP") ||
		!strings.Contains(outText, "=== RUN   TestDefaultProductionVerticalSlice") ||
		!strings.Contains(outText, "--- PASS: TestDefaultProductionVerticalSlice") {
		t.Fatalf("子进程未明确执行并通过默认生产竖切（可能 no tests/skip）:\n%s", out)
	}
}

// runDefaultProductionVerticalSlice 在子进程内用默认 liveCore 走完整链
// Pair → SwitchMachine → SessionCookie（再 Close 收尾）。前置：两个夹具地址
// 经环境变量传入。全程不调用 swapCore/swapSessions。
func runDefaultProductionVerticalSlice(t *testing.T) {
	urlA, urlB := os.Getenv(envAgentdAURL), os.Getenv(envAgentdBURL)
	if urlA == "" || urlB == "" {
		t.Fatalf("缺夹具地址: A=%q B=%q", urlA, urlB)
	}
	defer func() { _ = Close() }() // 失败路径也收尾（Close 幂等）；成功路径下方显式断言 nil

	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version: proto.PairVersion,
		Machines: []proto.PairMachine{
			{Name: "A", Token: "tok-A", Addr: urlA},
			{Name: "B", Token: "tok-B", Addr: urlB},
		},
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("编码配对 bundle: %v", err)
	}
	// 关键：不调用 swapCore/swapSessions——走生产默认装配（liveCore）。
	if err := Pair(payload); err != nil {
		t.Fatalf("配对: %v", err)
	}

	originA, err := SwitchMachine("A")
	if err != nil {
		t.Fatalf("切机 A: %v", err)
	}
	if !strings.HasPrefix(originA, "http://127.0.0.1:") {
		t.Fatalf("切机 A 应返回 loopback origin，实得 %q", originA)
	}
	gotA, err := SessionCookie("A")
	if err != nil {
		t.Fatalf("取 A cookie: %v", err)
	}
	wantA := fetchLastCookie(t, urlA)
	if gotA == "" || gotA != wantA {
		t.Fatalf("SessionCookie(A) 必须逐字等于夹具 Set-Cookie: got=%q want=%q", gotA, wantA)
	}

	originB, err := SwitchMachine("B")
	if err != nil {
		t.Fatalf("切机 B: %v", err)
	}
	if originB == originA {
		t.Fatalf("切到 B 后 origin 应改变: %q", originB)
	}
	if v, err := SessionCookie("A"); err == nil || v != "" {
		t.Fatalf("切到 B 后取 A 必须失败: value=%q err=%v", v, err)
	}
	gotB, err := SessionCookie("B")
	if err != nil {
		t.Fatalf("取 B cookie: %v", err)
	}
	wantB := fetchLastCookie(t, urlB)
	if gotB == "" || gotB != wantB || gotB == gotA {
		t.Fatalf("SessionCookie(B) 归属错误: gotB=%q wantB=%q gotA=%q", gotB, wantB, gotA)
	}

	// 收尾：Close 必须成功返回 nil（不允许吞掉返回值）。
	if err := Close(); err != nil {
		t.Fatalf("Close 必须返回 nil: %v", err)
	}
}

// fetchLastCookie 向夹具回读它最后签发的 handoff_session 值——证明绑定面返回的
// 值确实来自真实 Set-Cookie 响应，不是测试预置的假值。HTTP 客户端带超时；非 200
// 状态码视为失败，不静默把错误页当成 cookie 值。
func fetchLastCookie(t *testing.T, base string) string {
	t.Helper()
	client := &http.Client{Timeout: httpProbeTimeout}
	resp, err := client.Get(base + "/last-cookie")
	if err != nil {
		t.Fatalf("读夹具 last-cookie: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("读夹具 last-cookie 状态码: got=%d want=%d", resp.StatusCode, http.StatusOK)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读夹具 last-cookie 正文: %v", err)
	}
	return strings.TrimSpace(string(b))
}
