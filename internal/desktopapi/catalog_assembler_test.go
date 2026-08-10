// desktopapi CatalogAssembler 纯转换测试。
//
// 职责：
//   - 覆盖 ToBootstrap：nil/空集合、可选字段、枚举
//   - 覆盖 ToControlEvent：合法事件转换与非法 kind 报错
//   - 覆盖 ToOperation：Operation → OperationDTO 字段映射
//
// 边界：
//   - assembler 是纯函数式转换，无业务校验、无 DB/I/O；测试不依赖 store
package desktopapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
)

// TestToBootstrapEmptyCollections 断言空快照转出的字段是空数组而非 null。
//
// 为什么：renderer 按数组契约解码迭代，null 会破坏 TypeScript 侧的类型安全，
// 空数组是「没有数据」与「数据缺失」的明确区分。
func TestToBootstrapEmptyCollections(t *testing.T) {
	a := &CatalogAssembler{}
	b := a.ToBootstrap(controlplane.Snapshot{})
	if b.Machines == nil || len(b.Machines) != 0 {
		t.Errorf("machines = %+v, want 空数组", b.Machines)
	}
	if b.Projects == nil || b.Locations == nil || b.Workspaces == nil ||
		b.GitRefs == nil || b.ActiveTaskSummaries == nil || b.Operations == nil {
		t.Errorf("空集合字段应为空数组而非 null: %+v", b)
	}
	if b.ControlRevision != 0 {
		t.Errorf("control_revision = %d, want 0", b.ControlRevision)
	}
}

// TestToBootstrapFullSnapshot 断言完整快照的字段映射与枚举转换。
func TestToBootstrapFullSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	snap := controlplane.Snapshot{
		ControlRevision: 5,
		Machines: []controlplane.Machine{{
			ID: "m1", DisplayName: "本机", Kind: controlplane.MachineKindLocal,
			Endpoint: "http://127.0.0.1:7777", ProtocolVersion: 1,
			Capabilities: map[string]int{"catalog": 1},
			Status:       controlplane.MachineStatusConnected,
			LastSeenAt:   &now,
		}},
		Projects: []controlplane.Project{{ID: "p1", Name: "handoff"}},
		Locations: []controlplane.ProjectLocation{{
			ID: "loc1", ProjectID: "p1", MachineID: "m1", Role: controlplane.LocationRoleLocal,
			MainWorkspaceID: "ws1", Source: controlplane.LocationSourceExistingPath,
			CreatedAt: now, UpdatedAt: now,
		}},
		Workspaces: []controlplane.Workspace{{
			ID: "ws1", MachineID: "m1", Kind: controlplane.WorkspaceKindMain,
			Path: "/r", CanonicalPath: "/r", Branch: "main",
			Availability: controlplane.AvailabilityAvailable,
		}},
		GitRefs: []controlplane.GitRef{{LocationID: "loc1", Name: "main", HeadOID: "abc"}},
		ActiveTaskSummaries: []controlplane.TaskSummary{{
			TaskID: "t1", MachineID: "m1", WorkspaceID: "ws1",
			Name: "任务", State: controlplane.TaskSummaryStateRunning, Attention: 2,
		}},
	}
	b := (&CatalogAssembler{}).ToBootstrap(snap)
	if b.Machines[0].Status != "connected" || b.Machines[0].Kind != "local" {
		t.Errorf("machine 枚举转换错误: %+v", b.Machines[0])
	}
	if b.Workspaces[0].Kind != "main" || b.Workspaces[0].Availability != "available" {
		t.Errorf("workspace 枚举转换错误: %+v", b.Workspaces[0])
	}
	if b.Locations[0].Role != "local" || b.Locations[0].Source != "existing_path" {
		t.Errorf("location 枚举转换错误: %+v", b.Locations[0])
	}
	if b.ActiveTaskSummaries[0].State != "running" || b.ActiveTaskSummaries[0].Attention != 2 {
		t.Errorf("task summary 转换错误: %+v", b.ActiveTaskSummaries[0])
	}
	if b.ControlRevision != 5 {
		t.Errorf("control_revision = %d, want 5", b.ControlRevision)
	}
	// LastSeenAt 非 nil 时 DTO 应含时间
	if b.Machines[0].LastSeenAt == nil || !b.Machines[0].LastSeenAt.Equal(now) {
		t.Errorf("last_seen_at 未映射: %+v", b.Machines[0].LastSeenAt)
	}
}

// TestToBootstrapOptionalFields 断言可选字段缺省时的 DTO 形态。
func TestToBootstrapOptionalFields(t *testing.T) {
	snap := controlplane.Snapshot{
		Machines: []controlplane.Machine{{
			ID: "m1", Kind: controlplane.MachineKindRemote,
			Status: controlplane.MachineStatusUnavailable, // LastSeenAt nil
		}},
		Workspaces: []controlplane.Workspace{{
			ID: "ws1", MachineID: "m1", Kind: controlplane.WorkspaceKindDetached,
		}},
	}
	b := (&CatalogAssembler{}).ToBootstrap(snap)
	if b.Machines[0].LastSeenAt != nil {
		t.Errorf("LastSeenAt 应保持 nil: %+v", b.Machines[0].LastSeenAt)
	}
	if b.Workspaces[0].LocationID != nil {
		t.Errorf("detached workspace location_id 应为 nil: %+v", b.Workspaces[0].LocationID)
	}
}

// TestToControlEvent 断言 ControlEvent 到信封的转换与非法 kind 报错。
func TestToControlEvent(t *testing.T) {
	now := time.Now().UTC()
	domainPayload, _ := json.Marshal(controlplane.Workspace{
		ID: "ws1", MachineID: "m1", Kind: controlplane.WorkspaceKindMain,
		Path: "/repo", CanonicalPath: "/repo", Availability: controlplane.AvailabilityAvailable,
		LastScannedAt: now,
	})
	ev := controlplane.ControlEvent{
		ControlRevision: 7, Kind: controlplane.ControlEventKindWorkspaceUpsert,
		ResourceID: "ws1", Payload: domainPayload, CreatedAt: now,
	}
	env, err := (&CatalogAssembler{}).ToControlEvent(ev)
	if err != nil {
		t.Fatalf("ToControlEvent: %v", err)
	}
	if env.Revision != 7 || env.Kind != "workspace.upsert" || env.ResourceID != "ws1" {
		t.Errorf("envelope = %+v", env)
	}
	var payload map[string]any
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != "ws1" || payload["machine_id"] != "m1" || payload["ID"] != nil {
		t.Errorf("payload 未转换为 desktop snake_case DTO: %s", env.Payload)
	}
	if !env.CreatedAt.Equal(now) {
		t.Errorf("created_at 不一致: %v", env.CreatedAt)
	}

	// 非法 kind：转换必须报错，不能静默编码出未知字符串
	bad := controlplane.ControlEvent{Kind: "nonsense"}
	if _, err := (&CatalogAssembler{}).ToControlEvent(bad); err == nil {
		t.Errorf("非法 kind 应报错")
	}
}

// TestToOperation 断言 Operation → OperationDTO 的字段映射。
func TestToOperation(t *testing.T) {
	now := time.Now().UTC()
	op := controlplane.Operation{
		OperationID: "op1", Kind: controlplane.OperationKindCreateProject,
		State: controlplane.OperationStateSucceeded, ProjectID: "p1",
		Targets: []controlplane.OperationTargetResult{{
			TargetID: "tg1", MachineID: "m1", State: controlplane.OperationStateSucceeded,
		}},
		CreatedAt: now, UpdatedAt: now,
	}
	dto := (&CatalogAssembler{}).ToOperation(op)
	if dto.OperationID != "op1" || dto.Kind != "create_project" || dto.State != "succeeded" {
		t.Errorf("operation dto = %+v", dto)
	}
	if dto.Targets[0].State != "succeeded" {
		t.Errorf("target dto = %+v", dto.Targets[0])
	}
	re := mustMarshal(t, dto)
	if !strings.Contains(string(re), `"operation_id"`) {
		t.Errorf("operation dto JSON 缺少 operation_id: %s", re)
	}
}

// TestToCreateProjectCommand 锁定 CreateProjectRequest → CreateProjectCommand
// 的字段/枚举转换（table test 覆盖 local-only、remote-only、双 Location、clone）。
func TestToCreateProjectCommand(t *testing.T) {
	cases := []struct {
		name string
		req  CreateProjectRequest
		want []controlplane.CreateLocationCommand
	}{
		{"local-only existing",
			CreateProjectRequest{OperationID: "op1", Name: "p", Locations: []CreateProjectLocationReq{
				{MachineID: "m-local", Role: "local", Source: "existing_path", Path: "/repo"},
			}},
			[]controlplane.CreateLocationCommand{{TargetID: "m-local-local", MachineID: "m-local",
				Role: controlplane.LocationRoleLocal, Source: controlplane.LocationSourceExistingPath, Path: "/repo"}}},
		{"remote-only git_clone with default clone path",
			CreateProjectRequest{OperationID: "op2", Name: "p", Locations: []CreateProjectLocationReq{
				{MachineID: "m-remote", Role: "remote", Source: "git_clone", GitURL: "git@github.com:o/r.git", ClonePath: ""},
			}},
			[]controlplane.CreateLocationCommand{{TargetID: "m-remote-remote", MachineID: "m-remote",
				Role: controlplane.LocationRoleRemote, Source: controlplane.LocationSourceGitClone,
				GitURL: "git@github.com:o/r.git", ClonePath: ""}}},
		{"local+remote dual",
			CreateProjectRequest{OperationID: "op3", Name: "p", Locations: []CreateProjectLocationReq{
				{MachineID: "m-local", Role: "local", Source: "existing_path", Path: "/a"},
				{MachineID: "m-remote", Role: "remote", Source: "existing_path", Path: "/b"},
			}},
			[]controlplane.CreateLocationCommand{
				{TargetID: "m-local-local", MachineID: "m-local", Role: controlplane.LocationRoleLocal,
					Source: controlplane.LocationSourceExistingPath, Path: "/a"},
				{TargetID: "m-remote-remote", MachineID: "m-remote", Role: controlplane.LocationRoleRemote,
					Source: controlplane.LocationSourceExistingPath, Path: "/b"},
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := (&CatalogAssembler{}).ToCreateProjectCommand(c.req)
			if err != nil {
				t.Fatalf("ToCreateProjectCommand: %v", err)
			}
			if got.OperationID != c.req.OperationID {
				t.Fatalf("operation_id = %q", got.OperationID)
			}
			if len(got.Locations) != len(c.want) {
				t.Fatalf("locations = %d, want %d", len(got.Locations), len(c.want))
			}
			for i := range c.want {
				if got.Locations[i] != c.want[i] {
					t.Errorf("locations[%d] = %+v, want %+v", i, got.Locations[i], c.want[i])
				}
			}
		})
	}
}

// TestToCreateProjectCommandErrors 覆盖非法枚举与缺 operation_id 报错。
func TestToCreateProjectCommandErrors(t *testing.T) {
	a := &CatalogAssembler{}
	if _, err := a.ToCreateProjectCommand(CreateProjectRequest{Name: "p"}); err == nil {
		t.Fatal("缺 operation_id 应报错")
	}
	if _, err := a.ToCreateProjectCommand(CreateProjectRequest{OperationID: "op1", Name: "p",
		Locations: []CreateProjectLocationReq{{MachineID: "m1", Role: "bogus", Source: "existing_path"}}}); err == nil {
		t.Fatal("非法 role 应报错")
	}
	if _, err := a.ToCreateProjectCommand(CreateProjectRequest{OperationID: "op1", Name: "p",
		Locations: []CreateProjectLocationReq{{MachineID: "m1", Role: "local", Source: "bogus"}}}); err == nil {
		t.Fatal("非法 source 应报错")
	}
}
