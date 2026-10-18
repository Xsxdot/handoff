// B439 WU2：机器实例 id、自登记与 S8 本地 targets 导入三态。
package agentd

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
)

func warnLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// TestB439MachineIDStable 实例 id 首次生成、之后稳定（重启即同一台）。
func TestB439MachineIDStable(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreateMachineID(dir, slog.Default())
	if err != nil {
		t.Fatalf("首次生成: %v", err)
	}
	if a == "" {
		t.Fatal("实例 id 不能为空")
	}
	b, err := LoadOrCreateMachineID(dir, slog.Default())
	if err != nil {
		t.Fatalf("二次读取: %v", err)
	}
	if a != b {
		t.Fatalf("实例 id 必须稳定: %s != %s", a, b)
	}
}

// TestB439SelfRegisterDirect 直连形态自登记写入设备表（含凭据）。
func TestB439SelfRegisterDirect(t *testing.T) {
	st := newDevicesTestStore(t)
	cfg := &config.Config{Name: "linux-01", Advertise: "10.0.0.1:7777", Token: "tok", DataDir: t.TempDir()}
	if err := SelfRegister(st, cfg, slog.Default()); err != nil {
		t.Fatalf("SelfRegister: %v", err)
	}
	got, err := st.DeviceTarget("linux-01")
	if err != nil {
		t.Fatalf("DeviceTarget: %v", err)
	}
	if got.Addr == nil || *got.Addr != "10.0.0.1:7777" || got.Token == nil || *got.Token != "tok" {
		t.Fatalf("自登记连接块错误: %+v", got)
	}
}

// TestB439SelfRegisterConflict 异实例撞名 → 冲突错误点名名字。
func TestB439SelfRegisterConflict(t *testing.T) {
	st := newDevicesTestStore(t)
	if err := st.RegisterDevice("linux-01", "other-instance", ledger.DeviceTarget{Addr: strp("10.0.0.9:7777"), Token: strp("x")}, nil); err != nil {
		t.Fatalf("预置: %v", err)
	}
	cfg := &config.Config{Name: "linux-01", Advertise: "10.0.0.1:7777", Token: "tok", DataDir: t.TempDir()}
	err := SelfRegister(st, cfg, slog.Default())
	if !errors.Is(err, ledger.ErrDeviceConflict) {
		t.Fatalf("撞名应包装 ErrDeviceConflict: %v", err)
	}
	if !strings.Contains(err.Error(), "linux-01") {
		t.Fatalf("撞名错误应点名名字: %v", err)
	}
}

// TestB439ImportLocalTargets 导入三态：新名导入 / 同名同连接跳过 / 同名异连接 WARN 不覆盖；
// 保留字不导入；导入后本地 targets 被忽略（WARN 提示删除）。
func TestB439ImportLocalTargets(t *testing.T) {
	st := newDevicesTestStore(t)
	// 已存在且同名同连接：mac-02。
	if err := st.RegisterDevice("mac-02", "inst", configToDeviceTarget(config.Target{Relay: "wss://r", Credential: "c", Node: "mac-02", Token: "t"}), nil); err != nil {
		t.Fatalf("预置 mac-02: %v", err)
	}
	// 已存在但同名异连接：mac-03。
	if err := st.RegisterDevice("mac-03", "inst", ledger.DeviceTarget{Addr: strp("10.0.0.3:7777"), Token: strp("old")}, nil); err != nil {
		t.Fatalf("预置 mac-03: %v", err)
	}
	cfg := &config.Config{
		Name: "linux-01", Advertise: "10.0.0.1:7777", Token: "self-tok", DataDir: t.TempDir(),
		Targets: map[string]config.Target{
			"mac-02":   {Relay: "wss://r", Credential: "c", Node: "mac-02", Token: "t"}, // 同名同连接 → 跳过
			"mac-03":   {Addr: "10.0.0.4:7777", Token: "new"},                           // 同名异连接 → 不覆盖
			"mac-04":   {Addr: "10.0.0.4:7777", Token: "t4"},                            // 新名 → 导入
			"本机":       {Addr: "10.0.0.5:7777", Token: "t5"},                            // 保留字 → 不导入
			"linux-01": {Addr: "10.0.0.1:7777", Token: "self-tok"},                      // 自名 → 跳过
		},
	}
	var buf bytes.Buffer
	if err := ImportLocalTargets(st, cfg, warnLogger(&buf)); err != nil {
		t.Fatalf("ImportLocalTargets: %v", err)
	}
	logs := buf.String()

	// 新名导入。
	if _, err := st.DeviceTarget("mac-04"); err != nil {
		t.Fatalf("mac-04 应被导入: %v", err)
	}
	// 同名异连接不覆盖：仍是旧地址。
	got, err := st.DeviceTarget("mac-03")
	if err != nil {
		t.Fatal(err)
	}
	if got.Addr == nil || *got.Addr != "10.0.0.3:7777" {
		t.Fatalf("mac-03 不该被覆盖: %+v", got)
	}
	if !strings.Contains(logs, "mac-03") || !strings.Contains(logs, "diff_fields") {
		t.Fatalf("异连接必须 WARN 列差异: %s", logs)
	}
	// 保留字不导入。
	if _, err := st.DeviceTarget("本机"); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("保留字条目不该导入: %v", err)
	}
	if !strings.Contains(logs, "保留字") {
		t.Fatalf("保留字条目必须 WARN: %s", logs)
	}
	// 导入后提示删除本地 targets。
	if !strings.Contains(logs, "已被设备表取代") {
		t.Fatalf("必须 WARN 提示本地 targets 被忽略: %s", logs)
	}
}

// TestB439SelfTargetForms relay 与直连两形态的连接块投影。
func TestB439SelfTargetForms(t *testing.T) {
	relay, err := SelfTarget(&config.Config{Name: "mac-02", Token: "t", Relay: &config.RelayConfig{URL: "wss://r", Credential: "c", Node: "mac-02"}})
	if err != nil {
		t.Fatalf("relay SelfTarget: %v", err)
	}
	if !relay.IsRelay() || relay.Node != "mac-02" || relay.Credential != "c" {
		t.Fatalf("relay 连接块错误: %+v", relay)
	}
	if _, err := SelfTarget(&config.Config{Name: "x", Token: "t"}); err == nil {
		t.Fatal("无 advertise 无 relay 应报错")
	}
}
