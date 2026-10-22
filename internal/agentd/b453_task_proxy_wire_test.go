package agentd

// b453_task_proxy_wire_test.go —— 审查第 1 轮 I3a：任务行落了账号代理快照（可能带 userinfo
// 密码）时，GET /api/tasks/{id} 的响应体不得带出代理串。穿真实路由与 JSON 编码。
import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB453TaskGetNeverLeaksAccountProxy(t *testing.T) {
	env := ensureTestManager(t, newTestAgentdEnv(t))
	const pass = "b453-proxy-pass"
	now := time.Now().UTC()
	mustCreateTask(t, env.st, &proto.Task{ID: "b453-proxy-task", Name: "带账号代理的任务",
		State: proto.TaskStateRunning, RepoPath: "/r", CreatedAt: now, UpdatedAt: now,
		Account: "cx", AccountKind: "codex", AccountProxy: "socks5://u:" + pass + "@127.0.0.1:1080"})
	if got, err := env.st.GetTask("b453-proxy-task"); err != nil || got.AccountProxy == "" {
		t.Fatalf("前提：本机任务库应存了代理快照，得 %+v %v", got, err)
	}
	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/tasks/b453-proxy-task", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "b453-proxy-task") {
		t.Fatalf("GET /api/tasks/{id}: %d %s", resp.StatusCode, body)
	}
	for _, leak := range []string{pass, "socks5://", "127.0.0.1:1080", "account_proxy"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("任务详情响应体不得含账号代理（%q）: %s", leak, body)
		}
	}
}
