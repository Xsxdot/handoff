// B439 WU2：设备表接进机器列表/控制台增删（S3/S4/F8）。
//
// 断言：GET /api/machines 与日志零凭据；控制台增删写设备表且保留重名/不存在语义；
// 两个组装点共享一本账本时 B 无本地 targets 也能按名取 A 的客户端（S3 互见）。
package agentd

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

// newB439MachineEnv 造一台有设备表源的 agentd（自名 linux-01）。
func newB439MachineEnv(t *testing.T, logBuf *bytes.Buffer) (*testAgentdEnv, *ledger.Store) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := &config.Config{
		Token:     testToken,
		Name:      "linux-01",
		Advertise: "127.0.0.1:7777",
		Listen:    "127.0.0.1:7777",
	}
	env := newTestAgentdEnvWithCfg(t, cfg, logger)
	st := newDevicesTestStore(t)
	env.srv.SetLedger(st)
	env.srv.SetDeviceSource(NewDeviceTableSource(st, logger))
	return env, st
}

// TestB439MachinesNoCredentialLeak F8：GET /api/machines 响应体与日志零凭据。
func TestB439MachinesNoCredentialLeak(t *testing.T) {
	const secretToken = "SUPER-SECRET-TOKEN-abc123"
	const secretCred = "SUPER-SECRET-CRED-xyz789"
	var buf bytes.Buffer
	env, st := newB439MachineEnv(t, &buf)

	if err := st.RegisterDevice("mac-02", "inst-mac", ledger.DeviceTarget{
		Relay: strp("wss://relay.example"), Credential: strp(secretCred),
		Node: strp("mac-02"), Token: strp(secretToken),
	}, nil); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	// 直接取响应体原文，做 grep 级断言。
	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/machines", nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/machines: %v", err)
	}
	raw := new(bytes.Buffer)
	if _, err := io.Copy(raw, resp.Body); err != nil {
		t.Fatalf("读响应: %v", err)
	}
	resp.Body.Close()
	body := raw.String()
	if strings.Contains(body, secretToken) || strings.Contains(body, secretCred) {
		t.Fatalf("GET /api/machines 泄漏凭据: %s", body)
	}
	if !strings.Contains(body, "mac-02") {
		t.Fatalf("机器列表应含设备表条目: %s", body)
	}
	if strings.Contains(buf.String(), secretToken) || strings.Contains(buf.String(), secretCred) {
		t.Fatalf("探活日志泄漏凭据: %s", buf.String())
	}
}

// TestB439ConsoleAddRemoveWritesDevices S4：控制台增删写设备表，重名/不存在语义保留。
func TestB439ConsoleAddRemoveWritesDevices(t *testing.T) {
	var buf bytes.Buffer
	env, st := newB439MachineEnv(t, &buf)

	if err := env.srv.addMachine(proto.AddMachineReq{Name: "mac-02", Addr: "10.0.0.2:7777", Token: "tok"}); err != nil {
		t.Fatalf("addMachine: %v", err)
	}
	got, err := st.DeviceTarget("mac-02")
	if err != nil {
		t.Fatalf("设备表应有 mac-02: %v", err)
	}
	if got.Addr == nil || *got.Addr != "10.0.0.2:7777" {
		t.Fatalf("设备表连接方式错误: %+v", got)
	}
	// 重名 → ErrMachineExists（409 语义）。
	if err := env.srv.addMachine(proto.AddMachineReq{Name: "mac-02", Addr: "10.0.0.9:7777", Token: "t2"}); !errors.Is(err, ErrMachineExists) {
		t.Fatalf("重名应 ErrMachineExists: %v", err)
	}
	// 删除。
	if err := env.srv.removeMachine("mac-02"); err != nil {
		t.Fatalf("removeMachine: %v", err)
	}
	if _, err := st.DeviceTarget("mac-02"); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("删除后设备表不该还有: %v", err)
	}
	if err := env.srv.removeMachine("mac-02"); !errors.Is(err, ErrMachineNotFound) {
		t.Fatalf("删不存在应 ErrMachineNotFound: %v", err)
	}
}

// TestB439SelfRowMarkedInMachinesList 有自名时本机行标 Self 且用自名。
func TestB439SelfRowMarkedInMachinesList(t *testing.T) {
	var buf bytes.Buffer
	env, st := newB439MachineEnv(t, &buf)
	if err := st.RegisterDevice("linux-01", "inst-self", ledger.DeviceTarget{Addr: strp("127.0.0.1:7777"), Token: strp("t")}, nil); err != nil {
		t.Fatalf("RegisterDevice self: %v", err)
	}
	var resp proto.MachinesResp
	if code := env.getJSON(t, "/api/machines", &resp); code != http.StatusOK {
		t.Fatalf("状态码 %d", code)
	}
	var selfSeen, dup int
	for _, m := range resp.Machines {
		if m.Self {
			selfSeen++
			if m.Name != "linux-01" {
				t.Fatalf("本机行应用自名: %+v", m)
			}
		}
		if m.Name == "linux-01" {
			dup++
		}
	}
	if selfSeen != 1 {
		t.Fatalf("本机行应恰一条标 Self: %+v", resp.Machines)
	}
	if dup != 1 {
		t.Fatalf("自名行不该重复出现: %+v", resp.Machines)
	}
}

// TestB439MutualVisibility S3：两个组装点共享一本账本，B 无本地 targets 也能 For(A 的名)。
func TestB439MutualVisibility(t *testing.T) {
	st := newDevicesTestStore(t)
	// A 自登记。
	cfgA := &config.Config{Name: "linux-01", Advertise: "10.0.0.1:7777", Token: "tA", DataDir: t.TempDir()}
	if err := SelfRegister(st, cfgA, slog.Default()); err != nil {
		t.Fatalf("A 自登记: %v", err)
	}
	// B：活配置空 targets，池源指向同一本账本。
	pB := targetclient.NewPool(func() *config.Config { return &config.Config{Targets: map[string]config.Target{}} }, slog.Default())
	t.Cleanup(func() { pB.Close() })
	pB.SetSource(NewDeviceTableSource(st, slog.Default()))
	if _, err := pB.For("linux-01"); err != nil {
		t.Fatalf("B 无本地 targets 应能按名取 A 的客户端: %v", err)
	}
	if _, err := pB.For("mac-02"); err == nil {
		t.Fatal("未登记名必须被拒")
	}
}
