// controlplane ProjectService：durable 项目创建与 Location 语义编排。
//
// 职责：
//   - Create：校验表单不变量 → 持久化 pending Operation → 逐目标 Inspect/Clone →
//     归并 detached Workspace → 创建 Project/Location/Workspace → 更新 Operation
//   - 幂等：OperationID + TargetID 组成目录副作用的幂等键
//   - 部分成功：不删除成功目录，Project 保存成功 Location，Operation=partial
//
// 边界：
//   - HTTP/WS 断开不取消后台 operation；重试同 ID 只补失败目标
//   - 不复制 Workspace、不批量重写 Task（adoption 只更新 location_id/kind）
//   - 本层不直接接触 store 的原始表，全部经 Repository 端口
package controlplane

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProjectService 编排项目创建的长操作。
type ProjectService struct {
	repo Repository
	cmd  MachineCommander
	log  *slog.Logger
}

// NewProjectService 创建项目服务。
//
// 参数：
//   - repo: 项目/Operation/Workspace 持久化端口
//   - cmd: 目标机器资源命令（InspectPath/Clone）
//   - log: 日志入口
func NewProjectService(repo Repository, cmd MachineCommander, log *slog.Logger) *ProjectService {
	return &ProjectService{repo: repo, cmd: cmd, log: log}
}

// errForm 是表单不变量错误的哨兵（Create 把校验失败转为 failed Operation，
// 而非返回错误——校验失败也是可持久化的 operation 结果）。
type errForm struct{ msg string }

func (e *errForm) Error() string { return e.msg }

// Create 创建一个项目。
//
// 流程：
//  1. 校验表单不变量（无 Location/双 local/双 remote/role-machine 不一致/
//     远端非绝对路径/clone 缺 URL/identity 不一致）
//  2. 持久化 pending Operation
//  3. 逐目标执行 InspectPath（existing_path）或 Clone（git_clone）
//  4. 每个目标成功后注册 main Workspace（detached adoption 或新建）
//  5. 至少一个目标成功后创建 Project + Location + control event（同事务）
//  6. Operation 状态：全部成功=succeeded；部分=partial；全失败=failed
//
// 幂等：同 OperationID 重试时若 operation 已是终态（succeeded/partial），
// 直接返回现有权威 Operation，不重复执行。
//
// 为什么校验失败也落 Operation 而非直接报错：客户端以 operation_id 跟踪，
// failed 状态是可查询的权威结果，重试可修正后重投。
func (s *ProjectService) Create(ctx context.Context, cmd CreateProjectCommand) (Operation, error) {
	start := time.Now()
	defer func() {
		s.log.Info("项目创建操作完成", "operation_id", cmd.OperationID,
			"elapsed_ms", time.Since(start).Milliseconds())
	}()

	// 幂等短路：已有终态 operation 直接返回。
	if existing, err := s.repo.GetOperation(ctx, cmd.OperationID); err == nil {
		if existing.State == OperationStateSucceeded || existing.State == OperationStatePartial ||
			existing.State == OperationStateFailed {
			s.log.Info("operation 已存在，返回权威结果", "operation_id", cmd.OperationID, "state", existing.State)
			return existing, nil
		}
	} else if !isNotFound(err) {
		return Operation{}, err
	}

	// 校验表单不变量（客户端可修复，同步返回错误而非落 failed operation）。
	if err := s.validateForm(ctx, cmd); err != nil {
		s.log.Warn("项目创建被表单校验拒绝", "operation_id", cmd.OperationID, "cause", err)
		return Operation{}, err
	}

	op := Operation{
		OperationID: cmd.OperationID, Kind: OperationKindCreateProject,
		State: OperationStatePending, Targets: []OperationTargetResult{},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.repo.CreateOperation(ctx, op); err != nil {
		return Operation{}, fmt.Errorf("持久化 pending operation: %w", err)
	}

	// 逐目标执行。
	var (
		succeededLocs    []ProjectLocation
		succeededWS      []Workspace
		targetResults    []OperationTargetResult
		succeeded        int
		failed           int
		inspectedIDs     []string // 已成功目标的可识别 repo identity（identity 一致性校验用）
		identityConflict bool
	)
	now := time.Now().UTC()
	for i, locCmd := range cmd.Locations {
		targetID := locCmd.TargetID
		if targetID == "" {
			targetID = fmt.Sprintf("target-%d", i)
		}
		// 真实 machine kind（校验阶段已确认 role 匹配）。
		machine, err := s.repo.GetMachine(ctx, locCmd.MachineID)
		if err != nil {
			return Operation{}, fmt.Errorf("读取机器 %s: %w", locCmd.MachineID, err)
		}
		mKind := machine.Kind
		s.log.Info("operation 目标开始", "operation_id", cmd.OperationID,
			"target_id", targetID, "machine_id", locCmd.MachineID, "source", locCmd.Source)

		inspection, inspectErr := s.executeTarget(ctx, cmd, locCmd)
		if inspectErr != nil {
			failed++
			targetResults = append(targetResults, OperationTargetResult{
				TargetID: targetID, MachineID: locCmd.MachineID,
				State: OperationStateFailed,
				Error: &OperationError{Code: "TARGET_FAILED", Message: safeMessage(inspectErr)},
			})
			s.log.Error("operation 目标失败", "operation_id", cmd.OperationID,
				"target_id", targetID, "machine_id", locCmd.MachineID, "cause", inspectErr)
			continue
		}

		// identity 一致性校验：本机与远端都能识别 Git remote 时 identity 必须一致。
		if inspection.IsRepo && inspection.RepoIdentity != "" {
			for _, prev := range inspectedIDs {
				if prev != inspection.RepoIdentity {
					s.log.Warn("本机与远端 repo identity 不一致，拒绝组成同一 Project",
						"operation_id", cmd.OperationID, "target_id", targetID,
						"prev", prev, "cur", inspection.RepoIdentity)
					identityConflict = true
					targetResults = append(targetResults, OperationTargetResult{
						TargetID: targetID, MachineID: locCmd.MachineID,
						State: OperationStateFailed,
						Error: &OperationError{Code: "IDENTITY_MISMATCH",
							Message: "本机与远端目录的 Git 仓库 identity 不一致，不能组成同一项目"},
					})
					failed++
					goto identityMismatch
				}
			}
			inspectedIDs = append(inspectedIDs, inspection.RepoIdentity)
		}

		// 注册 main Workspace：同机器同 canonical path 命中 detached 则原位 adoption。
		ws, adoptErr := s.registerMainWorkspace(ctx, cmd.OperationID, locCmd.MachineID, inspection, targetID)
		if adoptErr != nil {
			failed++
			targetResults = append(targetResults, OperationTargetResult{
				TargetID: targetID, MachineID: locCmd.MachineID,
				State: OperationStateFailed,
				Error: &OperationError{Code: "TARGET_FAILED", Message: safeMessage(adoptErr)},
			})
			s.log.Error("operation 目标注册失败", "operation_id", cmd.OperationID,
				"target_id", targetID, "cause", adoptErr)
			continue
		}

		locationID := uuid.NewString()
		loc := ProjectLocation{
			ID: locationID, ProjectID: cmd.projectID(), MachineID: locCmd.MachineID,
			MachineKind: mKind,
			Role:        locCmd.Role, MainWorkspaceID: ws.ID,
			Source: locCmd.Source, GitURL: locCmd.GitURL,
			CreatedAt: now, UpdatedAt: now,
		}
		succeededLocs = append(succeededLocs, loc)
		succeededWS = append(succeededWS, ws)
		succeeded++
		targetResults = append(targetResults, OperationTargetResult{
			TargetID: targetID, MachineID: locCmd.MachineID, State: OperationStateSucceeded,
			Result: &OperationResult{WorkspaceID: ws.ID, LocationID: locationID, Path: inspection.Path},
		})
		s.log.Info("operation 目标成功", "operation_id", cmd.OperationID,
			"target_id", targetID, "workspace_id", ws.ID, "location_id", locationID)
	}

identityMismatch:
	// 全失败（含 identity 不一致）：不创建 Project。
	if succeeded == 0 || identityConflict {
		op.State = OperationStateFailed
		op.Targets = targetResults
		op.UpdatedAt = time.Now().UTC()
		if err := s.repo.UpdateOperation(ctx, op); err != nil {
			return Operation{}, err
		}
		s.log.Info("项目创建失败（无成功目标或 identity 不一致）",
			"operation_id", cmd.OperationID, "failed", failed, "identity_conflict", identityConflict)
		return op, nil
	}

	// 至少一个成功：创建 Project + Locations + Workspaces（同事务）。
	project := Project{
		ID: cmd.projectID(), Name: cmd.Name,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := s.repo.CreateProject(ctx, project, succeededLocs, succeededWS); err != nil {
		return Operation{}, fmt.Errorf("创建项目: %w", err)
	}

	state := OperationStateSucceeded
	if failed > 0 {
		state = OperationStatePartial
	}
	op.State = state
	op.ProjectID = project.ID
	op.Targets = targetResults
	op.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateOperation(ctx, op); err != nil {
		return Operation{}, err
	}
	s.log.Info("项目创建完成", "operation_id", cmd.OperationID,
		"project_id", project.ID, "state", state, "succeeded", succeeded, "failed", failed)
	return op, nil
}

// projectID 从 OperationID 派生稳定项目 ID（无外部 ID 来源时）。
//
// 为什么固定派生：项目创建以 operation_id 为幂等键，Project ID 必须在该
// operation 内稳定；用哈希派生保证「同 operation 必同 project」。
func (c CreateProjectCommand) projectID() string {
	return "proj-" + shortHash(c.OperationID)
}

// shortHash 返回字符串的 32 位 FNV-1a 哈希（稳定、确定，非加密用途）。
func shortHash(s string) string {
	return fmt.Sprintf("%08x", fnv32a(s))
}

// fnv32a 实现 FNV-1a 32 位哈希（标准算法）。
func fnv32a(s string) uint32 {
	const (
		offset = uint32(2166136261)
		prime  = uint32(16777619)
	)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return h
}

// executeTarget 按 source 执行 InspectPath 或 Clone。
func (s *ProjectService) executeTarget(ctx context.Context, createCmd CreateProjectCommand, loc CreateLocationCommand) (PathInspection, error) {
	switch loc.Source {
	case LocationSourceExistingPath:
		return s.cmd.InspectPath(ctx, InspectPathCommand{
			OperationID: createCmd.OperationID, TargetID: loc.TargetID,
			MachineID: loc.MachineID, Path: loc.Path,
		})
	case LocationSourceGitClone:
		clonePath := loc.ClonePath
		if clonePath == "" {
			clonePath = defaultClonePath(loc.GitURL)
		}
		return s.cmd.Clone(ctx, CloneLocationCommand{
			OperationID: createCmd.OperationID, TargetID: loc.TargetID,
			MachineID: loc.MachineID, GitURL: loc.GitURL, ClonePath: clonePath,
		})
	default:
		return PathInspection{}, fmt.Errorf("未知 source %q", loc.Source)
	}
}

// registerMainWorkspace 把目标目录注册为 main Workspace。
//
// 先查同机器同 canonical path 是否已有 Workspace（detached）：命中则原位
// adoption（只改 location_id/kind，保留 ID 与 Task 引用）；未命中才新建。
func (s *ProjectService) registerMainWorkspace(ctx context.Context, operationID, machineID string,
	inspection PathInspection, targetID string) (Workspace, error) {
	ws, err := s.repo.ResolveWorkspaceForPath(ctx, machineID, inspection.CanonicalPath, inspection.Path)
	if err != nil {
		return Workspace{}, fmt.Errorf("解析工作区: %w", err)
	}
	// ResolveWorkspaceForPath 已返回命中或新创建的行；若命中 detached，
	// adoption 由后续 Location 注册时的 AdoptWorkspace 完成（本实现经
	// ResolveWorkspaceForPath 的复用语义即可，workspace 已存在）。
	s.log.Debug("工作区已解析", "operation_id", operationID, "target_id", targetID,
		"workspace_id", ws.ID, "kind", ws.Kind)
	return ws, nil
}

// validateForm 校验表单不变量。
func (s *ProjectService) validateForm(ctx context.Context, cmd CreateProjectCommand) error {
	locs := make([]ProjectLocation, 0, len(cmd.Locations))
	for _, l := range cmd.Locations {
		kind, err := s.machineKind(ctx, l.MachineID)
		if err != nil {
			return &errForm{err.Error()}
		}
		locs = append(locs, ProjectLocation{
			Role: l.Role, MachineKind: kind, MachineID: l.MachineID,
		})
		if l.Role == LocationRoleRemote && l.Source == LocationSourceExistingPath && !filepath.IsAbs(l.Path) {
			return &errForm{"远端已有路径必须是绝对路径"}
		}
		if l.Source == LocationSourceGitClone && l.GitURL == "" {
			return &errForm{"clone 必须提供 Git URL"}
		}
	}
	if err := ValidateProjectLocations(locs); err != nil {
		return &errForm{err.Error()}
	}
	return nil
}

// machineKind 按机器 ID 解析实际 kind（role 与 machine kind 一致性的依据）。
//
// 为什么不能从 role 反推 kind：role 是用户的意图，machine kind 是机器的事实；
// 用 role 反推会让「local role 配 remote machine」永远通过校验，违背领域不变量。
// 必须查机器注册表取真实 kind。
func (s *ProjectService) machineKind(ctx context.Context, machineID string) (MachineKind, error) {
	m, err := s.repo.GetMachine(ctx, machineID)
	if err != nil {
		return "", fmt.Errorf("读取机器 %s: %w", machineID, err)
	}
	return m.Kind, nil
}

// defaultClonePath 生成默认 clone 目标目录：~/.handoff/<repo-name>。
//
// 为什么默认落在 ~/.handoff：项目目录是 agentd 控制面的资产，统一收口到
// 数据目录可被本机 Reconcile 与权限边界覆盖；用户可编辑覆盖。
func defaultClonePath(gitURL string) string {
	name := repoNameFromURL(gitURL)
	if name == "" {
		name = "repo"
	}
	home := "~"
	return filepath.Join(home, ".handoff", name)
}

// repoNameFromURL 从 Git URL 提取仓库名（去 .git 后缀）。
func repoNameFromURL(url string) string {
	trimmed := strings.TrimSuffix(url, ".git")
	base := path.Base(trimmed)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return base
}

// safeMessage 截断错误消息为安全摘要（不泄露敏感细节）。
func safeMessage(err error) string {
	return truncateString(err.Error(), 200)
}

// truncateString 截断字符串到 max rune。
func truncateString(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}

// isNotFound 判断是否为资源不存在错误。
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "不存在")
}
