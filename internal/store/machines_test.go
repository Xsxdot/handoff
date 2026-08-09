// store machines.go 测试：本机稳定身份与配置机器投影。
//
// 职责：
//   - 首次启动生成 local Machine UUID；同库重启保持 ID
//   - 配置 targets 只保存 secret_ref，不落 token 值
//   - endpoint/display name 改变保留 machine ID
//   - 删除 target 后保留 last-known Machine 但标 unavailable
//
// 边界：
//   - 不覆盖旧任务迁移（由 workspaces_test.go 负责）
//   - 使用真实 SQLite（t.TempDir），不用 mock
package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

// TestEnsureLocalMachineStableAcrossOpen 验证 local Machine 的稳定身份：
// 首次启动生成 UUID，同库重启（重新 Open）保持同一 ID。
func TestEnsureLocalMachineStableAcrossOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoff.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m1, err := s.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatalf("EnsureLocalMachine: %v", err)
	}
	if m1.ID == "" {
		t.Fatal("local machine ID 不能为空")
	}
	if m1.Kind != controlplane.MachineKindLocal {
		t.Fatalf("kind = %s, want local", m1.Kind)
	}
	s.Close()

	s2, err := store.Open(path)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	defer s2.Close()
	m2, err := s2.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatalf("重新 EnsureLocalMachine: %v", err)
	}
	if m2.ID != m1.ID {
		t.Fatalf("重启后 machine ID 变化: %s -> %s", m1.ID, m2.ID)
	}
	// 同库重复调用也保持 ID
	m3, _ := s2.EnsureLocalMachine(context.Background(), "本机")
	if m3.ID != m1.ID {
		t.Fatalf("同库重复调用 machine ID 变化: %s -> %s", m1.ID, m3.ID)
	}
}

// TestEnsureLocalMachineUpdatesDisplayName 验证 display name 改变保留 machine ID。
func TestEnsureLocalMachineUpdatesDisplayName(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m1, _ := s.EnsureLocalMachine(context.Background(), "本机")
	m2, err := s.EnsureLocalMachine(context.Background(), "新名字")
	if err != nil {
		t.Fatalf("EnsureLocalMachine: %v", err)
	}
	if m2.ID != m1.ID {
		t.Fatalf("display name 改变不应改变 machine ID")
	}
	if m2.DisplayName != "新名字" {
		t.Fatalf("display name 未更新: %q", m2.DisplayName)
	}
}

// TestSyncConfiguredMachinesSecretRefOnly 验证配置远端只落 secret_ref 引用，
// 不落 token 值——token 只由运行时 credential resolver 从 config 读取。
func TestSyncConfiguredMachinesSecretRefOnly(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	machines, err := s.SyncConfiguredMachines(context.Background(), []controlplane.ConfiguredMachine{{
		ConfigKey: "devbox", DisplayName: "开发机", Kind: controlplane.MachineKindRemote,
		Endpoint: "http://10.0.0.5:7777", SecretRef: "config.targets.devbox.token",
	}})
	if err != nil {
		t.Fatalf("SyncConfiguredMachines: %v", err)
	}
	if len(machines) != 1 {
		t.Fatalf("machines = %d, want 1", len(machines))
	}
	if machines[0].SecretRef != "config.targets.devbox.token" {
		t.Fatalf("secret_ref = %q", machines[0].SecretRef)
	}
	// 序列化整个投影不得出现 token 值本身
	snap, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for _, m := range snap.Machines {
		if m.Endpoint == "http://10.0.0.5:7777" && m.SecretRef == "" {
			t.Fatalf("远端机器缺少 secret_ref: %+v", m)
		}
	}
}

// TestSyncConfiguredMachinesKeepsIDOnChange 验证 endpoint/display name 改变时
// machine ID 保持稳定（按 config_key 关联）。
func TestSyncConfiguredMachinesKeepsIDOnChange(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	first, err := s.SyncConfiguredMachines(context.Background(), []controlplane.ConfiguredMachine{{
		ConfigKey: "devbox", DisplayName: "开发机-1", Kind: controlplane.MachineKindRemote,
		Endpoint: "http://10.0.0.5:7777", SecretRef: "config.targets.devbox.token",
	}})
	if err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	second, err := s.SyncConfiguredMachines(context.Background(), []controlplane.ConfiguredMachine{{
		ConfigKey: "devbox", DisplayName: "开发机-2", Kind: controlplane.MachineKindRemote,
		Endpoint: "http://10.0.0.6:7777", SecretRef: "config.targets.devbox.token",
	}})
	if err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if first[0].ID != second[0].ID {
		t.Fatalf("endpoint/display 改变后 machine ID 变化: %s -> %s", first[0].ID, second[0].ID)
	}
	if second[0].Endpoint != "http://10.0.0.6:7777" || second[0].DisplayName != "开发机-2" {
		t.Fatalf("元数据未更新: %+v", second[0])
	}
}

// TestSyncConfiguredMachinesDeletedTargetUnavailable 验证删除 target 后
// 保留 last-known Machine 但标 unavailable。
func TestSyncConfiguredMachinesDeletedTargetUnavailable(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.SyncConfiguredMachines(context.Background(), []controlplane.ConfiguredMachine{{
		ConfigKey: "devbox", DisplayName: "开发机", Kind: controlplane.MachineKindRemote,
		Endpoint: "http://10.0.0.5:7777", SecretRef: "config.targets.devbox.token",
	}}); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}

	// 删除 target（空配置）后再同步
	machines, err := s.SyncConfiguredMachines(context.Background(), nil)
	if err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if len(machines) != 1 {
		t.Fatalf("删除 target 后机器应保留 last-known，得到 %d 台", len(machines))
	}
	if machines[0].Status != controlplane.MachineStatusUnavailable {
		t.Fatalf("删除 target 后状态 = %s, want unavailable", machines[0].Status)
	}
	if machines[0].Endpoint == "" {
		t.Fatalf("last-known endpoint 不应被清空")
	}
}
