// approver_test.go —— B437 handoff approver 命令族对真实账本库的测试。
//
// 覆盖：set→show 同链同版本（两把 store 句柄模拟两机视角）、CAS 冲突、未知名
// 拒绝并列设备表名、无 DSN 时 set/clear 拒绝与 show 报「未设共享链」。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/spf13/cobra"
)

// newApproverStore 打开一个真实 SQLite 账本库并登记两台机器。
func newApproverStore(t *testing.T, path string, machines ...string) *ledger.Store {
	t.Helper()
	st, err := ledger.Open(path)
	if err != nil {
		t.Fatalf("打开账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, m := range machines {
		if err := st.PutDevice(m, ledger.DeviceTarget{}, nil); err != nil {
			t.Fatalf("登记设备 %s: %v", m, err)
		}
	}
	return st
}

func runApproverCore(t *testing.T, fn func(cmd *cobra.Command, out *bytes.Buffer) error) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := fn(cmd, &out)
	return out.String(), err
}

func writeChainFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chain.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApproverSetShowTwoMachines(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	stA := newApproverStore(t, dbPath, "linux-01", "mac-mini")
	// 第二把句柄 = 另一台机器视角（同一共享库）
	stB := newApproverStore(t, dbPath)

	file := writeChainFile(t, "timeout: 90s\ncandidates:\n  - machine: linux-01\n    executor: codex\n  - machine: mac-mini\n    executor: claude\n    model: m2\n")
	out, err := runApproverCore(t, func(cmd *cobra.Command, _ *bytes.Buffer) error {
		return approverSetWithStore(cmd, stA, file, "cli:a")
	})
	if err != nil {
		t.Fatalf("set 失败: %v (%s)", err, out)
	}
	if !strings.Contains(out, "版本 1") {
		t.Fatalf("set 应报版本 1: %s", out)
	}
	// B 机视角 show 到同链同版本
	outB, err := runApproverCore(t, func(cmd *cobra.Command, _ *bytes.Buffer) error {
		return approverShowWithStore(cmd, stB, config.ApproverConfig{})
	})
	if err != nil {
		t.Fatalf("show 失败: %v", err)
	}
	if !strings.Contains(outB, "版本 1") || !strings.Contains(outB, "linux-01") || !strings.Contains(outB, "m2") {
		t.Fatalf("另一机应看到同链同版本: %s", outB)
	}
}

func TestApproverSetUnknownMachineRejected(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	st := newApproverStore(t, dbPath, "linux-01")
	file := writeChainFile(t, "candidates:\n  - machine: ghost\n    executor: codex\n")
	_, err := runApproverCore(t, func(cmd *cobra.Command, _ *bytes.Buffer) error {
		return approverSetWithStore(cmd, st, file, "cli:a")
	})
	if err == nil || !strings.Contains(err.Error(), "不在设备表") || !strings.Contains(err.Error(), "linux-01") {
		t.Fatalf("未知名应拒绝并列出可用名: %v", err)
	}
}

func TestApproverSetCASConflict(t *testing.T) {
	// 账本层：行已存在时 expect=0 必冲突
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	st := newApproverStore(t, dbPath, "linux-01")
	if _, err := st.RegistryPut(orchestration.ApproverChainKind, orchestration.ApproverChainID, 0,
		[]byte(`{"timeout":1000000000,"rings":[{"machine":"linux-01","executor":"codex"}]}`), "cli:a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RegistryPut(orchestration.ApproverChainKind, orchestration.ApproverChainID, 0,
		[]byte(`{"timeout":1000000000,"rings":[{"machine":"linux-01","executor":"codex"}]}`), "cli:b"); !errors.Is(err, ledger.ErrCASConflict) {
		t.Fatalf("过期版本写应 CAS 冲突: %v", err)
	}
	// CLI 层：CAS 冲突文案点名版本并提示重读
	msg := mapApproverWriteError(2, ledger.ErrCASConflict)
	if !strings.Contains(msg.Error(), "版本冲突") || !strings.Contains(msg.Error(), "重新读取") {
		t.Fatalf("CAS 冲突应提示重读: %v", msg)
	}
}

func TestApproverClear(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	st := newApproverStore(t, dbPath, "linux-01")
	file := writeChainFile(t, "candidates:\n  - machine: linux-01\n    executor: codex\n")
	if _, err := runApproverCore(t, func(cmd *cobra.Command, _ *bytes.Buffer) error {
		return approverSetWithStore(cmd, st, file, "cli:a")
	}); err != nil {
		t.Fatal(err)
	}
	out, err := runApproverCore(t, func(cmd *cobra.Command, _ *bytes.Buffer) error {
		return approverClearWithStore(cmd, st, "cli:a")
	})
	if err != nil {
		t.Fatalf("clear 失败: %v", err)
	}
	if !strings.Contains(out, "已清除") {
		t.Fatalf("clear 应报已清除: %s", out)
	}
	// 再 clear 幂等
	if _, err := runApproverCore(t, func(cmd *cobra.Command, _ *bytes.Buffer) error {
		return approverClearWithStore(cmd, st, "cli:a")
	}); err != nil {
		t.Fatalf("重复 clear 应幂等: %v", err)
	}
}

// ledgerChainReader 对真实 SQLite 账本：读到链；无链 → ErrApproverChainAbsent。
func TestLedgerChainReader(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	st := newApproverStore(t, dbPath, "linux-01")
	reader := newLedgerChainReader(ledgerapi.New(st))
	if _, err := reader.Current(context.Background()); !errors.Is(err, orchestration.ErrApproverChainAbsent) {
		t.Fatalf("无链应 ErrApproverChainAbsent，得到: %v", err)
	}
	body, _ := orchestration.EncodeApproverChain(orchestration.ApproverChain{
		Timeout: 60 * time.Second, Rings: []orchestration.ApproverRing{{Machine: "linux-01", Executor: "codex"}},
	})
	if _, err := st.RegistryPut(orchestration.ApproverChainKind, orchestration.ApproverChainID, 0, body, "cli:a"); err != nil {
		t.Fatal(err)
	}
	chain, err := reader.Current(context.Background())
	if err != nil || len(chain.Rings) != 1 || chain.Rings[0].Machine != "linux-01" {
		t.Fatalf("应读到链: %+v %v", chain, err)
	}
}

// S6 启动 WARN：共享链已设且本地键非空 → 打一次「已被取代」WARN。
func TestWarnLocalApproverSuperseded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	st := newApproverStore(t, dbPath, "linux-01")
	body, _ := orchestration.EncodeApproverChain(orchestration.ApproverChain{
		Rings: []orchestration.ApproverRing{{Machine: "linux-01", Executor: "codex"}},
	})
	if _, err := st.RegistryPut(orchestration.ApproverChainKind, orchestration.ApproverChainID, 0, body, "cli:a"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	warnLocalApproverSuperseded(newLedgerChainReader(ledgerapi.New(st)),
		config.ApproverConfig{Executor: config.ExecutorList{"opencode"}}, log)
	if !strings.Contains(buf.String(), "已被共享链取代") {
		t.Fatalf("应打取代 WARN: %s", buf.String())
	}
	// 本地键为空 → 不打
	buf.Reset()
	warnLocalApproverSuperseded(newLedgerChainReader(ledgerapi.New(st)), config.ApproverConfig{}, log)
	if buf.Len() != 0 {
		t.Fatalf("本地键为空不应打 WARN: %s", buf.String())
	}
}

// 无 DSN：set/clear 拒绝，show 报「未设共享链」。
func TestApproverCLINoDSN(t *testing.T) {
	dir := t.TempDir()
	file := writeChainFile(t, "candidates:\n  - machine: linux-01\n    executor: codex\n")
	if _, _, err := runLedgerCLI(t, dir, "approver", "set", "-f", file); err == nil || !strings.Contains(err.Error(), "ledger.dsn") {
		t.Fatalf("无 DSN set 应拒绝并点名 ledger.dsn: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "approver", "clear"); err == nil || !strings.Contains(err.Error(), "ledger.dsn") {
		t.Fatalf("无 DSN clear 应拒绝: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "approver", "show")
	if err != nil {
		t.Fatalf("无 DSN show 不应报错: %v", err)
	}
	if !strings.Contains(out, "未设共享链") {
		t.Fatalf("无 DSN show 应报未设共享链: %s", out)
	}
}
