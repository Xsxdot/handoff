// controlplane ProjectService：durable 项目创建与 Location 语义编排。
//
// 职责：
//   - Create：持久化 pending Operation → 校验表单 → 发布 running → 逐目标
//     Inspect/Clone → 归并 detached Workspace → 原子创建资源并提交最终 Operation
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
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProjectService 编排项目创建的长操作。
type ProjectService struct {
	repo Repository
	cmd  MachineCommander
	log  *slog.Logger
	// 同一 operation_id 的并发请求串行化；多个桌面实例重试时只能有一个
	// goroutine 执行目录副作用。
	operationLocks [64]sync.Mutex
	onControlEvent func(ControlEvent)
}

// SetControlEventPublisher 注入事务提交后的实时广播回调。
// durable control_events 才是事实源；回调只降低已连接桌面的可见延迟。
func (s *ProjectService) SetControlEventPublisher(publish func(ControlEvent)) {
	s.onControlEvent = publish
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
//  1. 持久化 pending Operation
//  2. 校验表单不变量（无 Location/双 local/双 remote/role-machine 不一致/
//     远端非绝对路径/clone 缺 URL/identity 不一致）
//  3. 持久化 running Operation
//  4. 逐目标执行 InspectPath（existing_path）或 Clone（git_clone）
//  5. 每个目标成功后解析 main Workspace（detached adoption 或新建）
//  6. 至少一个目标成功后原子提交 Project/Location/Workspace/最终 Operation
//  7. Operation 状态：全部成功=succeeded；部分=partial；全失败=failed
//
// 幂等：同 OperationID 已 succeeded 时直接返回；partial/failed 重试时保留
// 已成功目标，只补失败目标。并发同 ID 请求由 operation lock 串行化。
//
// 为什么校验失败也落 Operation 而非直接报错：客户端以 operation_id 跟踪，
// failed 状态是可查询的权威结果，重试可修正后重投。
func (s *ProjectService) Create(ctx context.Context, cmd CreateProjectCommand) (Operation, error) {
	// 没有 operation_id 就没有 durable identity，无法安全创建 pending 记录；
	// 这是唯一必须在 Operation 之前拒绝的结构性错误。
	if strings.TrimSpace(cmd.OperationID) == "" {
		return Operation{}, &errForm{"operation_id 不能为空"}
	}
	lock := &s.operationLocks[fnv32a(cmd.OperationID)%uint32(len(s.operationLocks))]
	lock.Lock()
	defer lock.Unlock()
	start := time.Now()
	defer func() {
		s.log.Info("项目创建操作完成", "operation_id", cmd.OperationID,
			"elapsed_ms", time.Since(start).Milliseconds())
	}()

	existing, getErr := s.repo.GetOperation(ctx, cmd.OperationID)
	if getErr == nil && existing.State == OperationStateSucceeded {
		s.log.Info("operation 已成功，返回权威结果", "operation_id", cmd.OperationID)
		return existing, nil
	}
	if getErr != nil && !isNotFound(getErr) {
		return Operation{}, getErr
	}
	op := existing
	if isNotFound(getErr) {
		op = Operation{
			OperationID: cmd.OperationID, Kind: OperationKindCreateProject,
			State: OperationStatePending, Targets: []OperationTargetResult{},
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		createdEvent, err := s.repo.CreateOperation(ctx, op)
		if err != nil {
			return Operation{}, fmt.Errorf("持久化 pending operation: %w", err)
		}
		s.publish(createdEvent)
	} else if op.Kind != OperationKindCreateProject {
		return Operation{}, fmt.Errorf("operation_id %s 已用于 %s", cmd.OperationID, op.Kind)
	}
	// 重试也重新校验当前命令；operation_id 只保证副作用幂等，不允许借旧 ID
	// 绕过 role/机器/path 约束。业务校验失败是 accepted command 的权威 failed
	// 结果；底层读取故障则保留 pending，等待同 ID 重试。
	if err := s.validateForm(ctx, cmd); err != nil {
		var formErr *errForm
		if !errors.As(err, &formErr) {
			return op, err
		}
		op.State = OperationStateFailed
		op.Progress = safeMessage(err)
		op.UpdatedAt = time.Now().UTC()
		event, updateErr := s.repo.UpdateOperation(ctx, op)
		if updateErr != nil {
			return Operation{}, updateErr
		}
		s.publish(event)
		s.log.Warn("项目创建被表单校验拒绝", "operation_id", cmd.OperationID, "cause", err)
		return op, nil
	}

	// pending/partial/failed 的执行或重试统一先进入 running。该 durable 事件让
	// 其他桌面实例能看到 operation 已开始，而不是从 pending 直接跳到终态。
	op.State = OperationStateRunning
	op.Progress = fmt.Sprintf("0/%d", len(cmd.Locations))
	op.UpdatedAt = time.Now().UTC()
	runningEvent, err := s.repo.UpdateOperation(ctx, op)
	if err != nil {
		return Operation{}, fmt.Errorf("持久化 running operation: %w", err)
	}
	s.publish(runningEvent)

	// 逐目标执行。
	var (
		succeededLocs []ProjectLocation
		succeededWS   []Workspace
		targetResults []OperationTargetResult
		succeeded     int
		failed        int
		// projectIdentity 在 partial 重试时从已落库 Project 读取，确保后来
		// 恢复的远端目录仍必须与先前成功目录属于同一仓库。
		projectIdentity  string
		identityConflict bool
	)
	now := time.Now().UTC()
	hadProject := op.ProjectID != ""
	projectCreatedAt := now
	if hadProject {
		project, err := s.repo.GetProject(ctx, op.ProjectID)
		if err != nil {
			return Operation{}, fmt.Errorf("读取 partial operation 项目 %s: %w", op.ProjectID, err)
		}
		projectIdentity = project.GitIdentity
		projectCreatedAt = project.CreatedAt
	}
	priorResults := make(map[string]OperationTargetResult, len(op.Targets))
	for _, result := range op.Targets {
		priorResults[result.TargetID] = result
	}
	for i, locCmd := range cmd.Locations {
		targetID := locCmd.TargetID
		if targetID == "" {
			targetID = fmt.Sprintf("target-%d", i)
		}
		if prior, ok := priorResults[targetID]; ok && prior.State == OperationStateSucceeded {
			if prior.MachineID != locCmd.MachineID {
				return Operation{}, fmt.Errorf("operation target %s 的 machine_id 已从 %s 变为 %s",
					targetID, prior.MachineID, locCmd.MachineID)
			}
			targetResults = append(targetResults, prior)
			succeeded++
			continue
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
			if projectIdentity == "" {
				projectIdentity = inspection.RepoIdentity
			} else if projectIdentity != inspection.RepoIdentity {
				s.log.Warn("本机与远端 repo identity 不一致，拒绝组成同一 Project",
					"operation_id", cmd.OperationID, "target_id", targetID,
					"expected", projectIdentity, "actual", inspection.RepoIdentity)
				identityConflict = true
				targetResults = append(targetResults, OperationTargetResult{
					TargetID: targetID, MachineID: locCmd.MachineID,
					State: OperationStateFailed,
					Error: &OperationError{Code: "IDENTITY_MISMATCH",
						Message: "本机与远端目录的 Git 仓库 identity 不一致，不能组成同一项目"},
				})
				failed++
				continue
			}
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

		locationID := "loc-" + shortHash(cmd.OperationID+":"+targetID)
		ws.Kind = WorkspaceKindMain
		ws.LocationID = &locationID
		loc := ProjectLocation{
			ID: locationID, ProjectID: cmd.projectID(), MachineID: locCmd.MachineID,
			MachineKind: mKind,
			Role:        locCmd.Role, MainWorkspaceID: ws.ID,
			Source: locCmd.Source, GitURL: safeGitURLForStorage(locCmd.GitURL),
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

	// 初次创建即 identity 冲突时不能保留半个 Project。把此前临时成功目标也
	// 标成失败，确保修正输入后同 ID 重试会重新检查，而不是跳过未落库结果。
	if identityConflict && !hadProject {
		for index := range targetResults {
			if targetResults[index].State == OperationStateSucceeded {
				targetResults[index].State = OperationStateFailed
				targetResults[index].Result = nil
				targetResults[index].Error = &OperationError{Code: "IDENTITY_MISMATCH",
					Message: "本机与远端目录的 Git 仓库 identity 不一致，不能组成同一项目"}
				failed++
				succeeded--
			}
		}
		succeededLocs = nil
		succeededWS = nil
	}
	// 全失败：不创建空 Project。
	if succeeded == 0 {
		op.State = OperationStateFailed
		op.ProjectID = ""
		op.Targets = targetResults
		op.Progress = fmt.Sprintf("0/%d", len(cmd.Locations))
		op.UpdatedAt = time.Now().UTC()
		event, err := s.repo.UpdateOperation(ctx, op)
		if err != nil {
			return Operation{}, err
		}
		s.publish(event)
		s.log.Info("项目创建失败（无成功目标或 identity 不一致）",
			"operation_id", cmd.OperationID, "failed", failed, "identity_conflict", identityConflict)
		return op, nil
	}

	// 至少一个成功：创建或补齐 Project + Locations + Workspaces（同事务）。
	project := Project{
		ID: cmd.projectID(), Name: cmd.Name, GitIdentity: projectIdentity,
		CreatedAt: projectCreatedAt, UpdatedAt: now,
	}
	state := OperationStateSucceeded
	if failed > 0 {
		state = OperationStatePartial
	}
	op.State = state
	op.ProjectID = project.ID
	op.Targets = targetResults
	op.Progress = fmt.Sprintf("%d/%d", succeeded, len(cmd.Locations))
	op.UpdatedAt = time.Now().UTC()
	if len(succeededLocs) > 0 || !hadProject {
		// 最终 Operation 与 Project/Location/Workspace 及全部 control events
		// 同事务提交；否则两事务间崩溃会留下“目录已创建但 operation 仍 pending”。
		events, err := s.repo.CreateProject(ctx, project, succeededLocs, succeededWS, &op)
		if err != nil {
			return Operation{}, fmt.Errorf("创建项目: %w", err)
		}
		for _, event := range events {
			s.publish(event)
		}
	} else {
		// 当前没有新 Location（例如重复恢复到相同 partial 结果），只需更新
		// Operation；项目聚合未变化，不制造重复资源事件。
		event, err := s.repo.UpdateOperation(ctx, op)
		if err != nil {
			return Operation{}, err
		}
		s.publish(event)
	}
	s.log.Info("项目创建完成", "operation_id", cmd.OperationID,
		"project_id", project.ID, "state", state, "succeeded", succeeded, "failed", failed)
	return op, nil
}

func (s *ProjectService) publish(event ControlEvent) {
	if event.ControlRevision > 0 && s.onControlEvent != nil {
		s.onControlEvent(event)
	}
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
	if strings.TrimSpace(cmd.Name) == "" {
		return &errForm{"项目名不能为空"}
	}
	locs := make([]ProjectLocation, 0, len(cmd.Locations))
	targetIDs := make(map[string]struct{}, len(cmd.Locations))
	for index, l := range cmd.Locations {
		targetID := l.TargetID
		if targetID == "" {
			targetID = fmt.Sprintf("target-%d", index)
		}
		if _, duplicate := targetIDs[targetID]; duplicate {
			return &errForm{fmt.Sprintf("target_id %s 重复", targetID)}
		}
		targetIDs[targetID] = struct{}{}
		kind, err := s.machineKind(ctx, l.MachineID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return &errForm{err.Error()}
			}
			return err
		}
		locs = append(locs, ProjectLocation{
			Role: l.Role, MachineKind: kind, MachineID: l.MachineID,
		})
		switch l.Source {
		case LocationSourceExistingPath:
			if !filepath.IsAbs(l.Path) {
				return &errForm{"已有目录必须是绝对路径"}
			}
		case LocationSourceGitClone:
			if strings.TrimSpace(l.GitURL) == "" {
				return &errForm{"clone 必须提供 Git URL"}
			}
			if l.ClonePath != "" && !ownerAbsolutePath(l.ClonePath) {
				return &errForm{"clone 目录必须是绝对路径或 ~/ 下的路径"}
			}
		default:
			return &errForm{fmt.Sprintf("未知 Location source %q", l.Source)}
		}
	}
	if err := ValidateProjectLocations(locs); err != nil {
		return &errForm{err.Error()}
	}
	return nil
}

func ownerAbsolutePath(value string) bool {
	return filepath.IsAbs(value) || value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`)
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
	trimmed := strings.TrimSpace(url)
	if cut := strings.IndexAny(trimmed, "?#"); cut >= 0 {
		trimmed = trimmed[:cut]
	}
	trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "/"), ".git")
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

// safeGitURLForStorage 移除 URL userinfo/query/fragment，避免 clone 凭证进入
// SQLite、bootstrap 或 control stream。SCP-like 地址也不保留 `user@` 前缀；
// 该字段只用于展示，不作为后续 clone 的凭证来源。
func safeGitURLForStorage(raw string) string {
	if raw == "" {
		return raw
	}
	if !strings.Contains(raw, "://") {
		cleaned := raw
		if cut := strings.IndexAny(cleaned, "?#"); cut >= 0 {
			cleaned = cleaned[:cut]
		}
		if at := strings.LastIndex(cleaned, "@"); at >= 0 && strings.Contains(cleaned[at+1:], ":") {
			return cleaned[at+1:]
		}
		return cleaned
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
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
