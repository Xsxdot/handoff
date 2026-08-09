// desktopapi 无状态转换器：领域模型 ↔ wire DTO。
//
// 职责：
//   - ToBootstrap：controlplane.Snapshot → BootstrapResponse
//   - ToControlEvent：controlplane.ControlEvent → ControlEventEnvelope
//   - ToOperation：controlplane.Operation → OperationDTO
//
// 边界：
//   - 纯转换，无业务校验、无 DB/I/O、无状态
//   - 业务规则（Location 数量、role/Machine 匹配、Git identity）由 ProjectService 校验
//   - 枚举映射失败时返回错误，不静默编码未知字符串
package desktopapi

import (
	"fmt"

	"github.com/xushixin/handoff/internal/controlplane"
)

// CatalogAssembler 是无状态纯转换器，可安全地并发使用。
type CatalogAssembler struct{}

// ToBootstrap 把控制面快照转换为 bootstrap 响应。
//
// 注意：空集合统一转成空数组（非 nil），保证 renderer 侧数组解码安全。
func (a *CatalogAssembler) ToBootstrap(s controlplane.Snapshot) BootstrapResponse {
	return BootstrapResponse{
		Machines:            a.toMachines(s.Machines),
		Projects:            a.toProjects(s.Projects),
		Locations:           a.toLocations(s.Locations),
		Workspaces:          a.toWorkspaces(s.Workspaces),
		GitRefs:             a.toGitRefs(s.GitRefs),
		ActiveTaskSummaries: a.toTaskSummaries(s.ActiveTaskSummaries),
		Operations:          a.toOperations(s.Operations),
		ControlRevision:     s.ControlRevision,
	}
}

func (a *CatalogAssembler) toMachines(ms []controlplane.Machine) []MachineDTO {
	out := make([]MachineDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, MachineDTO{
			ID:              m.ID,
			DisplayName:     m.DisplayName,
			Kind:            string(m.Kind),
			Endpoint:        m.Endpoint,
			ProtocolVersion: m.ProtocolVersion,
			Capabilities:    m.Capabilities,
			Status:          string(m.Status),
			LastSeenAt:      m.LastSeenAt,
		})
	}
	return out
}

func (a *CatalogAssembler) toProjects(ps []controlplane.Project) []ProjectDTO {
	out := make([]ProjectDTO, 0, len(ps))
	for _, p := range ps {
		out = append(out, ProjectDTO{
			ID: p.ID, Name: p.Name, GitIdentity: p.GitIdentity,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		})
	}
	return out
}

func (a *CatalogAssembler) toLocations(ls []controlplane.ProjectLocation) []ProjectLocationDTO {
	out := make([]ProjectLocationDTO, 0, len(ls))
	for _, l := range ls {
		out = append(out, ProjectLocationDTO{
			ID: l.ID, ProjectID: l.ProjectID, MachineID: l.MachineID,
			Role: string(l.Role), MainWorkspaceID: l.MainWorkspaceID,
			Source: string(l.Source), GitURL: l.GitURL,
			CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
		})
	}
	return out
}

func (a *CatalogAssembler) toWorkspaces(ws []controlplane.Workspace) []WorkspaceDTO {
	out := make([]WorkspaceDTO, 0, len(ws))
	for _, w := range ws {
		out = append(out, WorkspaceDTO{
			ID: w.ID, MachineID: w.MachineID, LocationID: w.LocationID,
			Kind: string(w.Kind), Path: w.Path, CanonicalPath: w.CanonicalPath,
			RepoIdentity: w.RepoIdentity, GitCommonDir: w.GitCommonDir,
			Branch: w.Branch, HeadOID: w.HeadOID,
			Availability: string(w.Availability), LastScannedAt: w.LastScannedAt,
		})
	}
	return out
}

func (a *CatalogAssembler) toGitRefs(rs []controlplane.GitRef) []GitRefDTO {
	out := make([]GitRefDTO, 0, len(rs))
	for _, r := range rs {
		out = append(out, GitRefDTO{
			LocationID: r.LocationID, Name: r.Name, HeadOID: r.HeadOID,
			CheckedOutWorkspaceIDs: r.CheckedOutWorkspaceIDs,
		})
	}
	return out
}

func (a *CatalogAssembler) toTaskSummaries(ts []controlplane.TaskSummary) []TaskSummaryDTO {
	out := make([]TaskSummaryDTO, 0, len(ts))
	for _, t := range ts {
		out = append(out, TaskSummaryDTO{
			TaskID: t.TaskID, MachineID: t.MachineID, WorkspaceID: t.WorkspaceID,
			Name: t.Name, Executor: t.Executor, State: string(t.State),
			Attention: t.Attention, UpdatedAt: t.UpdatedAt,
		})
	}
	return out
}

func (a *CatalogAssembler) toOperations(ops []controlplane.Operation) []OperationDTO {
	out := make([]OperationDTO, 0, len(ops))
	for _, op := range ops {
		out = append(out, a.ToOperation(op))
	}
	return out
}

// ToOperation 把领域 Operation 转换为 wire DTO。
func (a *CatalogAssembler) ToOperation(op controlplane.Operation) OperationDTO {
	targets := make([]OperationTargetDTO, 0, len(op.Targets))
	for _, tg := range op.Targets {
		dto := OperationTargetDTO{TargetID: tg.TargetID, MachineID: tg.MachineID, State: string(tg.State)}
		if tg.Result != nil {
			dto.Result = &OperationResultDTO{
				WorkspaceID: tg.Result.WorkspaceID,
				LocationID:  tg.Result.LocationID,
				Path:        tg.Result.Path,
			}
		}
		if tg.Error != nil {
			dto.Error = &OperationErrorDTO{Code: tg.Error.Code, Message: tg.Error.Message}
		}
		targets = append(targets, dto)
	}
	return OperationDTO{
		OperationID: op.OperationID, Kind: string(op.Kind), State: string(op.State),
		ProjectID: op.ProjectID, Targets: targets, Progress: op.Progress,
		CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt,
	}
}

// ToControlEvent 把领域 ControlEvent 转换为 wire 信封。
//
// 为什么非法 kind 报错：assembler 是契约边界，未知枚举应显式失败而非静默
// 编码出桌面端无法识别的字符串。
func (a *CatalogAssembler) ToControlEvent(ev controlplane.ControlEvent) (ControlEventEnvelope, error) {
	kind, err := controlEventKindString(ev.Kind)
	if err != nil {
		return ControlEventEnvelope{}, err
	}
	return ControlEventEnvelope{
		Revision: ev.ControlRevision, Kind: kind, ResourceID: ev.ResourceID,
		Payload: ev.Payload, CreatedAt: ev.CreatedAt,
	}, nil
}

// controlEventKindString 把 ControlEventKind 映射为线格式字符串。
func controlEventKindString(k controlplane.ControlEventKind) (string, error) {
	switch k {
	case controlplane.ControlEventKindMachineUpsert:
		return "machine.upsert", nil
	case controlplane.ControlEventKindProjectUpsert:
		return "project.upsert", nil
	case controlplane.ControlEventKindLocationUpsert:
		return "location.upsert", nil
	case controlplane.ControlEventKindWorkspaceUpsert:
		return "workspace.upsert", nil
	case controlplane.ControlEventKindWorkspaceRemove:
		return "workspace.remove", nil
	case controlplane.ControlEventKindGitRefUpsert:
		return "git_ref.upsert", nil
	case controlplane.ControlEventKindGitRefRemove:
		return "git_ref.remove", nil
	case controlplane.ControlEventKindTaskSummaryUpsert:
		return "task_summary.upsert", nil
	case controlplane.ControlEventKindTaskSummaryRemove:
		return "task_summary.remove", nil
	case controlplane.ControlEventKindOperationUpsert:
		return "operation.upsert", nil
	}
	return "", fmt.Errorf("未知 ControlEventKind %q", k)
}

// ToCreateProjectCommand 把 CreateProjectRequest 转换为领域 CreateProjectCommand。
//
// 只做字段/枚举转换；Location 数量、role/Machine 匹配、Git identity 等业务
// 规则仍由 ProjectService 校验（spec §5.2：assembler 不承载业务规则）。
func (a *CatalogAssembler) ToCreateProjectCommand(req CreateProjectRequest) (controlplane.CreateProjectCommand, error) {
	cmd := controlplane.CreateProjectCommand{OperationID: req.OperationID, Name: req.Name}
	if cmd.OperationID == "" {
		return cmd, fmt.Errorf("operation_id 不能为空")
	}
	for _, l := range req.Locations {
		role, err := parseLocationRole(l.Role)
		if err != nil {
			return cmd, err
		}
		source, err := parseLocationSource(l.Source)
		if err != nil {
			return cmd, err
		}
		cmd.Locations = append(cmd.Locations, controlplane.CreateLocationCommand{
			TargetID:  targetIDFrom(l),
			MachineID: l.MachineID,
			Role:      role,
			Source:    source,
			Path:      l.Path,
			GitURL:    l.GitURL,
			ClonePath: l.ClonePath,
		})
	}
	return cmd, nil
}

// targetIDFrom 为 Location 派生稳定目标 ID（wire 无显式 target_id 时用 machine+role 派生）。
func targetIDFrom(l CreateProjectLocationReq) string {
	if l.MachineID != "" {
		return l.MachineID + "-" + l.Role
	}
	return l.Role
}

// parseLocationRole 解析 wire role 字符串。
func parseLocationRole(s string) (controlplane.LocationRole, error) {
	switch s {
	case "local":
		return controlplane.LocationRoleLocal, nil
	case "remote":
		return controlplane.LocationRoleRemote, nil
	}
	return "", fmt.Errorf("未知 Location role %q", s)
}

// parseLocationSource 解析 wire source 字符串。
func parseLocationSource(s string) (controlplane.LocationSource, error) {
	switch s {
	case "existing_path":
		return controlplane.LocationSourceExistingPath, nil
	case "git_clone":
		return controlplane.LocationSourceGitClone, nil
	}
	return "", fmt.Errorf("未知 Location source %q", s)
}
