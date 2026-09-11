// approval_client.go —— 编排侧 ApprovalClient 组装与政策版本哈希（B233.8）。
//
// 职责：Manager 是唯一生产组装点，把自身能力绑定到 internal/approval.Client。
// 边界：审批行为、工单/事件写入和 Ack 在 approval 包；本文件不再承载命名的
// ApprovalClient 实现，HashPolicyVersion 继续留在 agentd 作为现有政策版本落点。
package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/Xsxdot/handoff/internal/approval"
	"github.com/Xsxdot/handoff/internal/executor"
)

// bindApproval is the sole production assembly point for a task-scoped
// ApprovalClient. Concrete orchestration capabilities are passed as Hooks;
// approval.Client never reaches back into agentd.
func (m *Manager) bindApproval(taskID string, snap executor.PolicySnapshot) executor.ApprovalClient {
	if snap.TaskID == "" {
		snap.TaskID = taskID
	}
	m.log.Info("组装 ApprovalClient", "task", taskID, "version", snap.Version)
	return approval.NewClient(taskID, snap, approval.Hooks{
		Log:             m.log,
		Store:           m.st,
		Hub:             m.hub,
		JudgePermission: m.judgePermission,
		ShouldConsult:   m.shouldConsultApprover,
		Decide: func(ctx context.Context, permission, summary string) approval.ConsultDecision {
			if m.approver == nil {
				return approval.ConsultDecision{Err: errors.New("审批者不可用")}
			}
			d := m.approver.Decide(ctx, permission, summary)
			return approval.ConsultDecision{
				Approve: d.Approve, Reason: d.Reason,
				ElapsedMS: d.ElapsedMS, Err: d.Err,
			}
		},
		CountConsultFailure: m.countApproverFail,
		ResetConsultFailure: func(id string) {
			m.apMu.Lock()
			defer m.apMu.Unlock()
			delete(m.apFails, id)
		},
		// AutoAllow 只审计、不回传：OpenCode 的原生投递归 adapter（冻结 #3/#16）。
		// 非 OpenCode 的回传在 handlePermission 的 autoAllowPermission 路径。
		AutoAllow:          m.auditAutoAllowOnly,
		TransitBestEffort:  m.transitBestEffort,
		NoteDeliveryFailed: m.NoteDeliveryFailed,
	})
}

// HashPolicyVersion computes a stable version for policy content. The field
// set and sorted inputs are retained at this assembly-side location so policy
// snapshots keep their existing physical hash contract.
func HashPolicyVersion(blacklist []string, approverOn bool, safeIDs []string, scope executor.ApprovalScope) string {
	bl := append([]string(nil), blacklist...)
	sort.Strings(bl)
	ids := append([]string(nil), safeIDs...)
	sort.Strings(ids)
	type payload struct {
		Blacklist  []string `json:"blacklist"`
		ApproverOn bool     `json:"approver_on"`
		Safe       []string `json:"safe"`
		Workdir    string   `json:"workdir"`
		TaskDir    string   `json:"task_dir"`
		TaskTmp    string   `json:"task_tmp"`
	}
	b, _ := json.Marshal(payload{Blacklist: bl, ApproverOn: approverOn, Safe: ids,
		Workdir: scope.Workdir, TaskDir: scope.TaskDir, TaskTmp: scope.TaskTmpDir})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
