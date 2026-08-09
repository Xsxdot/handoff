// controlplane 包领域模型测试：锁死枚举 JSON 线格式与关键业务约束。
//
// 职责：
//   - 断言 Machine/Workspace/Operation/Location 各枚举的 JSON 值恒定
//   - 覆盖 Project Location 约束：1–2 个、本机至多一个、远端至多一个
//   - 覆盖 TaskState 新增 stalled 的合法/非法迁移（与 proto 包联动）
//
// 边界：
//   - 不覆盖持久化（由 store 包测试负责）
//   - 不覆盖 Repository 实现（领域层只定义端口，实现另行测试）
package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEnumJSONValues 锁定全部枚举的 JSON 线格式值。
//
// 为什么断言序列化结果：桌面 DTO 与 DB 列都直接沿用这些字符串值，
// 一旦有人改常量名/值就会造成跨层契约漂移，此测试锁死序列化产物。
func TestEnumJSONValues(t *testing.T) {
	// Machine 状态五值（spec §12.1）
	machineStatusCases := []struct {
		s    MachineStatus
		want string
	}{
		{MachineStatusConnecting, "connecting"},
		{MachineStatusReconciling, "reconciling"},
		{MachineStatusConnected, "connected"},
		{MachineStatusUnavailable, "unavailable"},
		{MachineStatusIncompatible, "incompatible"},
	}
	for _, c := range machineStatusCases {
		if got := jsonString(c.s); got != c.want {
			t.Errorf("MachineStatus %v JSON = %q, want %q", c.s, got, c.want)
		}
	}

	// Workspace kind 三值
	workspaceKindCases := []struct {
		k    WorkspaceKind
		want string
	}{
		{WorkspaceKindMain, "main"},
		{WorkspaceKindWorktree, "worktree"},
		{WorkspaceKindDetached, "detached"},
	}
	for _, c := range workspaceKindCases {
		if got := jsonString(c.k); got != c.want {
			t.Errorf("WorkspaceKind %v JSON = %q, want %q", c.k, got, c.want)
		}
	}

	// Operation 五态
	operationStateCases := []struct {
		s    OperationState
		want string
	}{
		{OperationStatePending, "pending"},
		{OperationStateRunning, "running"},
		{OperationStatePartial, "partial"},
		{OperationStateSucceeded, "succeeded"},
		{OperationStateFailed, "failed"},
	}
	for _, c := range operationStateCases {
		if got := jsonString(c.s); got != c.want {
			t.Errorf("OperationState %v JSON = %q, want %q", c.s, got, c.want)
		}
	}

	// Location role/source
	if got := jsonString(LocationRoleLocal); got != "local" {
		t.Errorf("LocationRoleLocal JSON = %q, want local", got)
	}
	if got := jsonString(LocationRoleRemote); got != "remote" {
		t.Errorf("LocationRoleRemote JSON = %q, want remote", got)
	}
	if got := jsonString(LocationSourceExistingPath); got != "existing_path" {
		t.Errorf("LocationSourceExistingPath JSON = %q, want existing_path", got)
	}
	if got := jsonString(LocationSourceGitClone); got != "git_clone" {
		t.Errorf("LocationSourceGitClone JSON = %q, want git_clone", got)
	}
}

// jsonString 把实现 json.Marshaler 的枚举值序列化为裸字符串。
func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return strings.Trim(string(b), `"`)
}

// TestValidateProjectLocations 覆盖项目 Location 约束：
//   - 无 Location 拒绝
//   - 本机最多一个、远端最多一个
//   - 合计 1–2 个（>=1）
//   - role 与 machine kind 一致
func TestValidateProjectLocations(t *testing.T) {
	local := ProjectLocation{Role: LocationRoleLocal, MachineID: "m-local", MachineKind: MachineKindLocal}
	remote := ProjectLocation{Role: LocationRoleRemote, MachineID: "m-remote", MachineKind: MachineKindRemote}

	cases := []struct {
		name string
		locs []ProjectLocation
		ok   bool
	}{
		{"无 Location 拒绝", nil, false},
		{"空切片拒绝", []ProjectLocation{}, false},
		{"仅本机合法", []ProjectLocation{local}, true},
		{"仅远端合法", []ProjectLocation{remote}, true},
		{"本机+远端合法", []ProjectLocation{local, remote}, true},
		{"远端+本机合法（顺序无关）", []ProjectLocation{remote, local}, true},
		{"两个本机拒绝", []ProjectLocation{local, local}, false},
		{"两个远端拒绝", []ProjectLocation{remote, remote}, false},
		{"三个 Location 拒绝", []ProjectLocation{local, remote, local}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateProjectLocations(c.locs); (err == nil) != c.ok {
				t.Errorf("ValidateProjectLocations(%d 个) err = %v, want ok=%v", len(c.locs), err, c.ok)
			}
		})
	}
}

// TestValidateProjectLocationsRoleMachine 覆盖 role 与 machine kind 一致性校验。
//
// 为什么独立测试：role 与 machine kind 的一致性校验必须由控制面校验，
// assembler/handler 层只做字段转换，不承载业务规则（spec §5.2）。
func TestValidateProjectLocationsRoleMachine(t *testing.T) {
	cases := []struct {
		name string
		locs []ProjectLocation
		ok   bool
	}{
		{"local role 但 remote machine 拒绝",
			[]ProjectLocation{{Role: LocationRoleLocal, MachineID: "m1", MachineKind: MachineKindRemote}}, false},
		{"remote role 但 local machine 拒绝",
			[]ProjectLocation{{Role: LocationRoleRemote, MachineID: "m1", MachineKind: MachineKindLocal}}, false},
		{"local role + local machine 合法",
			[]ProjectLocation{{Role: LocationRoleLocal, MachineID: "m1", MachineKind: MachineKindLocal}}, true},
		{"remote role + remote machine 合法",
			[]ProjectLocation{{Role: LocationRoleRemote, MachineID: "m1", MachineKind: MachineKindRemote}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateProjectLocations(c.locs); (err == nil) != c.ok {
				t.Errorf("err = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

// TestProjectLocationRoleCompatibility 断言 role 与 MachineKind 的兼容矩阵本身正确：
// local role 只允许 local machine，remote role 只允许 remote machine。
func TestProjectLocationRoleCompatibility(t *testing.T) {
	if !LocationRoleLocal.Compatible(MachineKindLocal) {
		t.Error("local role 应兼容 local machine")
	}
	if LocationRoleLocal.Compatible(MachineKindRemote) {
		t.Error("local role 不得兼容 remote machine")
	}
	if !LocationRoleRemote.Compatible(MachineKindRemote) {
		t.Error("remote role 应兼容 remote machine")
	}
	if LocationRoleRemote.Compatible(MachineKindLocal) {
		t.Error("remote role 不得兼容 local machine")
	}
}
