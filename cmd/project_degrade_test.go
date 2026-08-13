// project_degrade_test.go —— 两跳登记在本机 agentd 缺席时的降级行为。
//
// 边界：只覆盖 registerProjectBothHops 这一个编排函数。dispatch 自动登记
// 走同一个函数，它的端到端回归在 dispatch_autoregister_test.go。
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// projectHopJSON 是一条位置记录的响应体，字段名对齐 proto.ProjectLocation。
const projectHopJSON = `{"project_id":"pid1","name":"handoff",` +
	`"path":"/srv/repos/handoff","origin_url":"git@example.com:x/handoff.git"}`

// newProjectHopServer 起一个只认 POST /api/projects 的假 agentd。
//
// 参数：
//   - status: 期望返回的状态码；200 返回一条位置记录，其余返回错误体
//   - hits:   收到的登记请求计数（用来证明「目标机那一跳真的发出去了」）
//
// 返回：不含 scheme 的 host:port，直接填进测试配置的 addr。
func newProjectHopServer(t *testing.T, status int, hits *atomic.Int32) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		if status != http.StatusOK {
			http.Error(w, "路径已被另一个项目占用", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, projectHopJSON)
	}))
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "http://")
}

// newHopCmd 造一个只用来承载 ctx 与输出流的裸命令。
//
// 为什么必须 SetContext：cobra v1.10 的 Command.Context() 在未经 Execute
// 时返回 nil，而 http.NewRequestWithContext 收到 nil ctx 会 panic。
func newHopCmd() (*cobra.Command, *bytes.Buffer) {
	c := &cobra.Command{}
	c.SetContext(context.Background())
	var errBuf bytes.Buffer
	c.SetOut(io.Discard)
	c.SetErr(&errBuf)
	return c, &errBuf
}

// TestRegisterDegradesWhenLocalAgentdMissing：本机够不着 + 有 target
// → 目标机照常登记，整体成功，降级说人话。
//
// 这是纯协调者机（含 Windows）首次派发的主路径。
func TestRegisterDegradesWhenLocalAgentdMissing(t *testing.T) {
	var remoteHits atomic.Int32
	remoteAddr := newProjectHopServer(t, http.StatusOK, &remoteHits)
	// listen 指 127.0.0.1:1：必定 refused，且不受开发机上真跑着的 agentd 影响
	cfg := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+remoteAddr+"\"\n    token: \"remote-tok\"\n")
	resetFlags(t)
	configPath = cfg
	targetName = "devbox"

	c, errBuf := newHopCmd()
	err := registerProjectBothHops(c, "git@example.com:x/handoff.git", "", "/home/me/handoff", "")
	if err != nil {
		t.Fatalf("本机够不着不该让整次登记失败: %v", err)
	}
	if got := remoteHits.Load(); got != 1 {
		t.Fatalf("目标机应收到 1 次登记，实得 %d", got)
	}
	s := errBuf.String()
	if !strings.Contains(s, "跳过本机登记") {
		t.Errorf("降级必须说出来，stderr=%q", s)
	}
	if !strings.Contains(s, "handoff project add") {
		t.Errorf("降级必须给补救办法，stderr=%q", s)
	}
}

// TestRegisterFailsOnLocalConflict：本机返回 409 → 整体失败。
//
// 拿到了响应就是真冲突，降级不许吞它——吞了就是脏登记。
func TestRegisterFailsOnLocalConflict(t *testing.T) {
	var localHits, remoteHits atomic.Int32
	localAddr := newProjectHopServer(t, http.StatusConflict, &localHits)
	remoteAddr := newProjectHopServer(t, http.StatusOK, &remoteHits)
	cfg := writeTestConfig(t, "listen: \""+localAddr+"\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+remoteAddr+"\"\n    token: \"remote-tok\"\n")
	resetFlags(t)
	configPath = cfg
	targetName = "devbox"

	c, _ := newHopCmd()
	err := registerProjectBothHops(c, "git@example.com:x/handoff.git", "", "/home/me/handoff", "")
	if err == nil {
		t.Fatal("本机 409 必须整体失败")
	}
	if !strings.Contains(err.Error(), "登记到本机") {
		t.Errorf("报文应指明是哪一跳失败的，实得 %q", err.Error())
	}
	if got := remoteHits.Load(); got != 0 {
		t.Errorf("本机跳失败后不应再打目标机，实得 %d 次", got)
	}
}

// TestRegisterFailsWhenNoLocalAndNoTarget：本机够不着 + 无 target → 报错。
//
// 两跳都没发生时报成功是撒谎。报文必须给出两条出路。
func TestRegisterFailsWhenNoLocalAndNoTarget(t *testing.T) {
	cfg := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n")
	resetFlags(t)
	configPath = cfg
	targetName = ""

	c, _ := newHopCmd()
	err := registerProjectBothHops(c, "git@example.com:x/handoff.git", "", "/home/me/handoff", "")
	if err == nil {
		t.Fatal("两跳都无处登记时必须报错")
	}
	for _, want := range []string{"没有 agentd", "--target"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文缺少 %q：%q", want, err.Error())
		}
	}
}
