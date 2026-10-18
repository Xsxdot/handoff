// B439 WU2 F5：设备表快照源——投影往返与读失败回 last-good。
package agentd

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func newDevicesTestStore(t *testing.T) *ledger.Store {
	t.Helper()
	st, err := ledger.OpenShared(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("OpenShared: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestB439DeviceTableSourceProjectsCreds：源把设备表连接块（含凭据）投影给池。
func TestB439DeviceTableSourceProjectsCreds(t *testing.T) {
	st := newDevicesTestStore(t)
	if err := st.RegisterDevice("linux-01", "inst-a", ledger.DeviceTarget{
		Addr: strp("10.0.0.1:7777"), Token: strp("tok"),
	}, nil); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	src := NewDeviceTableSource(st, slog.Default())
	names := src.Names()
	if len(names) != 1 || names[0] != "linux-01" {
		t.Fatalf("Names = %v", names)
	}
	got, ok := src.Target("linux-01")
	if !ok || got.Addr != "10.0.0.1:7777" || got.Token != "tok" {
		t.Fatalf("Target 投影错误: %+v ok=%v", got, ok)
	}
	if _, ok := src.Target("ghost"); ok {
		t.Fatal("未登记名不该命中")
	}
}

// TestB439DeviceTableSourceKeepsLastGood：读表失败回上一份快照 + WARN，不撕订阅。
func TestB439DeviceTableSourceKeepsLastGood(t *testing.T) {
	st := newDevicesTestStore(t)
	if err := st.RegisterDevice("linux-01", "inst-a", ledger.DeviceTarget{
		Addr: strp("10.0.0.1:7777"), Token: strp("tok"),
	}, nil); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	src := NewDeviceTableSource(st, logger)
	// 先成功读一次建立 last-good。
	if _, ok := src.Target("linux-01"); !ok {
		t.Fatal("首读应命中")
	}
	// 关库制造读失败。
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, ok := src.Target("linux-01")
	if !ok || got.Addr != "10.0.0.1:7777" {
		t.Fatalf("读失败应回 last-good: %+v ok=%v", got, ok)
	}
	if names := src.Names(); len(names) != 1 || names[0] != "linux-01" {
		t.Fatalf("读失败 Names 应回 last-good: %v", names)
	}
	if !strings.Contains(buf.String(), "设备表读失败") {
		t.Fatalf("读失败必须打 WARN，日志=%s", buf.String())
	}
}
