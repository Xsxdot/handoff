package agentd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

func ptyPost(t *testing.T, env *testAgentdEnv, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, env.ts.URL+"/api/pty/sessions", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// base_path 不是本机已探测到的工作树 → 400 且文案说清是参数问题。
//
// 400 而不是 403：会话在能力上等价于主令牌（spec §1），白名单是参数校验
// 不是安全边界，不能再借安全的名义（spec §5.2）。
func TestCreatePtySessionRejectsUnknownBasePath(t *testing.T) {
	env := newTestAgentdEnv(t)
	resp, body := ptyPost(t, env, `{"base_path":"/nowhere/at/all","base_kind":"workspace","cols":80,"rows":24}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400；体=%s", resp.StatusCode, body)
	}
	if bytes.Contains(body, []byte("拒绝访问")) || bytes.Contains(body, []byte("权限")) {
		t.Errorf("文案不得再用安全口径，实得 %s", body)
	}
}

// home 基准：忽略传入的 base_path，直接用服务端 $HOME（spec §5.2）。
func TestCreatePtySessionHomeBase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持，另有降级用例")
	}
	env := newTestAgentdEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	resp, body := ptyPost(t, env, `{"base_path":"/攻击者传的路径","base_kind":"home","cols":100,"rows":30}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200；体=%s", resp.StatusCode, body)
	}
	var s proto.PtySession
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("解析响应: %v；体=%s", err, body)
	}
	if s.BasePath != home {
		t.Errorf("home 基准必须落在 $HOME=%s，实得 %s", home, s.BasePath)
	}
	if s.ExitCode != nil {
		t.Errorf("新会话的 exit_code 必须缺席，实得 %d", *s.ExitCode)
	}
	t.Cleanup(func() { env.srv.pty.Close(s.ID) })
}

// 列表 → 删除 → 再删 404。
func TestPtySessionListAndDelete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持，另有降级用例")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	_, body := ptyPost(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	var s proto.PtySession
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("解析响应: %v；体=%s", err, body)
	}

	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/pty/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	lb, _ := io.ReadAll(resp.Body)
	var list proto.PtySessionsResp
	if err := json.Unmarshal(lb, &list); err != nil {
		t.Fatalf("解析列表: %v；体=%s", err, lb)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != s.ID {
		t.Fatalf("列表 = %s，期望恰好含刚建的会话 %s", lb, s.ID)
	}
	if list.Machines != nil {
		t.Errorf("不带 scope=all 时不该有 machines 信封，实得 %s", lb)
	}

	del := func() int {
		r, _ := http.NewRequest(http.MethodDelete, env.ts.URL+"/api/pty/sessions/"+s.ID, nil)
		r.Header.Set("Authorization", "Bearer "+testToken)
		rr, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer rr.Body.Close()
		return rr.StatusCode
	}
	if code := del(); code != http.StatusOK {
		t.Fatalf("首次 DELETE = %d，期望 200", code)
	}
	if code := del(); code != http.StatusNotFound {
		t.Fatalf("重复 DELETE = %d，期望 404", code)
	}
}

// /api/status 必须上报能力位，且**不是 nil**——nil 是留给老版本的。
func TestStatusReportsPtySupported(t *testing.T) {
	env := newTestAgentdEnv(t)
	// newTestAgentdEnv 不注入 manager，/api/status 会 503；白盒包里有现成的
	// newTestManager（manager_test.go），注入后 handleStatus 才能走到填能力位
	m, _, _, _ := newTestManager(t)
	env.srv.SetManager(m)
	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var st proto.StatusResp
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatalf("解析 status: %v", err)
	}
	if st.PtySupported == nil {
		t.Fatal("本版 agentd 必须上报 pty_supported，nil 只能表示对端版本过旧")
	}
	if *st.PtySupported != (runtime.GOOS != "windows") {
		t.Errorf("pty_supported = %v，与平台不符（GOOS=%s）", *st.PtySupported, runtime.GOOS)
	}
}
