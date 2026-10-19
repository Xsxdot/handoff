// B441：控制台按 ?machine= 路由的入口与池同源解析机器，且自名指本机。
//
// 断言（生产形态：有设备表源、本地 targets: 为空——与 B439 告警「targets 已被设备表取代」
// 后清掉配置的机器一致）：
//   - 设备表里登记的远端机，HTTP 转发 / 终端 WS 转发 / 任务路由 / 看板汇总都能找到它
//     （不再报「未在本机配置的 targets 中定义」、不再被汇总当作已删机器跳过）
//   - ?machine=<自名> 一律本机处理（通用转发、终端 WS、drop），不当远端转发、不报 400
//   - 设备表与配置中均无此名时仍 400 点名（未知机器语义不变）
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

// registerB441Remote 把一台测试远端 agentd 以直连形态登记进设备表。
func registerB441Remote(t *testing.T, st *ledger.Store, name, url string) {
	t.Helper()
	if err := st.RegisterDevice(name, "inst-"+name, ledger.DeviceTarget{
		Addr: strp(url), Token: strp(testToken),
	}, nil); err != nil {
		t.Fatalf("RegisterDevice(%s): %v", name, err)
	}
}

func b441Do(t *testing.T, method, url string, body []byte) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestB441ForwardResolvesDeviceTableMachine：设备表里的远端机，?machine= 转发原样透传远端应答。
func TestB441ForwardResolvesDeviceTableMachine(t *testing.T) {
	var buf bytes.Buffer
	env, st := newB439MachineEnv(t, &buf)
	remote := newTestAgentdEnv(t) // 远端 manager 未注入，登记必 503「manager 未就绪」
	registerB441Remote(t, st, "devbox", remote.ts.URL)

	code, body := b441Do(t, http.MethodPost, env.ts.URL+"/api/projects?machine=devbox",
		[]byte(`{"origin_url":"git@github.com:x/h.git"}`))
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "manager 未就绪") {
		t.Fatalf("设备表机器应被转发并原样透传远端 503，实得 %d %s", code, body)
	}
	// 本机未注入 manager 时也会答 503，单看应答分不清「转发了」与「误在本机处理」（review I2）
	if !strings.Contains(buf.String(), "msg=转发请求 machine=devbox") {
		t.Fatalf("应确实转发到 devbox，日志：%s", buf.String())
	}
}

// TestB441SelfNameServedLocally：?machine=<自名> 本机处理，与省略 machine 同一应答。
func TestB441SelfNameServedLocally(t *testing.T) {
	var buf bytes.Buffer
	env, _ := newB439MachineEnv(t, &buf) // 自名 linux-01

	wantCode, wantBody := b441Do(t, http.MethodGet, env.ts.URL+"/api/discipline", nil)
	code, body := b441Do(t, http.MethodGet, env.ts.URL+"/api/discipline?machine=linux-01", nil)
	if code != wantCode || body != wantBody {
		t.Fatalf("?machine=自名 应与本机同一应答：本机 %d %s；自名 %d %s", wantCode, wantBody, code, body)
	}
	if code == http.StatusBadRequest {
		t.Fatalf("?machine=自名 不应 400：%s", body)
	}
}

// TestB441UnknownMachineStillRejected：设备表与配置中均无此名 → 400 点名。
func TestB441UnknownMachineStillRejected(t *testing.T) {
	var buf bytes.Buffer
	env, _ := newB439MachineEnv(t, &buf)
	code, body := b441Do(t, http.MethodGet, env.ts.URL+"/api/discipline?machine=ghost", nil)
	if code != http.StatusBadRequest || !strings.Contains(body, "ghost") {
		t.Fatalf("未知机器应 400 点名，实得 %d %s", code, body)
	}
}

// TestB441PtyWSResolvesDeviceTableMachine：终端 WS 转发找得到设备表机器（拨到远端，
// 远端无此会话而关闭），不再在握手前被 400「未在 targets 中定义」挡下。
func TestB441PtyWSResolvesDeviceTableMachine(t *testing.T) {
	var buf bytes.Buffer
	env, st := newB439MachineEnv(t, &buf)
	remote := newTestAgentdEnv(t)
	registerB441Remote(t, st, "devbox", remote.ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/ws/pty?session=nope&machine=devbox"
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + testToken}},
	})
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "")
	}
	if err != nil && resp != nil && resp.StatusCode == http.StatusBadRequest {
		t.Fatalf("设备表机器的终端 WS 不应在握手前被 400 拒绝：%v", err)
	}
	if strings.Contains(buf.String(), "WS 转发被拒") {
		t.Fatalf("不应走「机器名未定义」拒绝分支：%s", buf.String())
	}
}

// TestB441TasksAllListsDeviceTableMachines（review I1）：看板 scope=all 汇总按池同源判
// 「有哪些机器」——设备表机器的镜像任务与机器行都在，本机自名不当远端机器行。
func TestB441TasksAllListsDeviceTableMachines(t *testing.T) {
	var buf bytes.Buffer
	env, st := newB439MachineEnv(t, &buf)
	ensureTestManager(t, env)
	registerB441Remote(t, st, "devbox", "http://127.0.0.1:1")
	now := time.Now().UTC()
	if err := env.st.UpsertMirrorTask("devbox", proto.Task{
		ID: "b441-remote-task", Name: "远端任务", State: proto.TaskStateCompleted,
		RepoPath: "/remote/x", CreatedAt: now, UpdatedAt: now,
	}, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}
	code, body := b441Do(t, http.MethodGet, env.ts.URL+"/api/tasks?scope=all", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/tasks?scope=all: %d %s", code, body)
	}
	var resp proto.TasksResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解码: %v", err)
	}
	found := false
	for _, v := range resp.Tasks {
		if v.ID == "b441-remote-task" && v.Machine == "devbox" {
			found = true
		}
	}
	if !found {
		t.Fatalf("设备表机器的镜像任务应在汇总里：%s", body)
	}
	rows := map[string]bool{}
	for _, m := range resp.Machines {
		rows[m.Name] = true
	}
	if !rows["devbox"] {
		t.Fatalf("设备表机器应有机器行：%+v", resp.Machines)
	}
	if rows["linux-01"] {
		t.Fatalf("本机自名不应作为远端机器行出现：%+v", resp.Machines)
	}
}

// TestB441TaskRouteResolvesDeviceTableMachine：镜像任务的单任务请求按设备表转发到远端。
func TestB441TaskRouteResolvesDeviceTableMachine(t *testing.T) {
	var buf bytes.Buffer
	env, st := newB439MachineEnv(t, &buf)
	ensureTestManager(t, env)
	remote := newTestAgentdEnv(t)
	registerB441Remote(t, st, "devbox", remote.ts.URL)
	now := time.Now().UTC()
	if err := env.st.UpsertMirrorTask("devbox", proto.Task{
		ID: "b441-route-task", Name: "远端任务", State: proto.TaskStateCompleted,
		RepoPath: "/remote/x", CreatedAt: now, UpdatedAt: now,
	}, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}
	code, body := b441Do(t, http.MethodGet, env.ts.URL+"/api/tasks/b441-route-task", nil)
	if strings.Contains(body, "未在本机配置的 targets") || strings.Contains(body, "未登记") {
		t.Fatalf("设备表机器的镜像任务不应被判未登记：%d %s", code, body)
	}
	if !strings.Contains(buf.String(), "任务路由：转发到远端") || !strings.Contains(buf.String(), "machine=devbox") {
		t.Fatalf("应按设备表转发到 devbox，日志：%s", buf.String())
	}
}

// TestB441SelfNameLocalForPtyAndDrop：终端 WS 与 drop 两入口的 ?machine=自名 也留本机。
func TestB441SelfNameLocalForPtyAndDrop(t *testing.T) {
	var buf bytes.Buffer
	env, _ := newB439MachineEnv(t, &buf) // 自名 linux-01，设备表里没有它

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/ws/pty?session=nope&machine=linux-01"
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + testToken}},
	})
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "")
	}
	if err != nil && resp != nil && resp.StatusCode == http.StatusBadRequest {
		t.Fatalf("?machine=自名 的终端 WS 不应被当远端拒绝：%v", err)
	}
	if strings.Contains(buf.String(), "WS 转发被拒") {
		t.Fatalf("自名不应走 WS 远端转发：%s", buf.String())
	}

	code, body := b441Do(t, http.MethodPost, env.ts.URL+"/api/drop?name=a.txt&machine=linux-01", []byte("hi"))
	if strings.Contains(body, "未登记") || strings.Contains(buf.String(), "drop 转发被拒") {
		t.Fatalf("?machine=自名 的 drop 不应被当远端拒绝：%d %s", code, body)
	}
	if !strings.Contains(buf.String(), "drop 目标是本机") {
		t.Fatalf("drop 自名应走本机分支，日志：%s", buf.String())
	}
}
