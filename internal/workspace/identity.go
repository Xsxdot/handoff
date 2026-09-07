// identity.go —— 工作区身份编码（B233.4）。
//
// 职责：冻结任务短 id、自动分支名、40 位提交号与结果引用的唯一编码。
//
// 边界：纯函数，无 I/O。与 agentd.id8 / taskBranch / baseCommitRe 同形；
// 实现节点把生产路径收成调用本包，禁止第二套截断规则。
package workspace

import (
	"fmt"
	"reflect"
	"regexp"
)

// TaskBranchPrefix 是自动任务分支的前缀。改这个字面值会让既有任务分支对不上。
const TaskBranchPrefix = "handoff/"

// commitSHARe 限定结果/基线 commit 只能是 40 位小写十六进制
// （git rev-parse HEAD 的输出形态）。与 agentd.baseCommitRe 同形。
var commitSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ID8 截取任务 ID 前 8 个字节，用于分支名与 managed worktree 目录名。
// 与 cmd/wait.go 的通知短 id、agentd.id8 共用同一截断规则。
func ID8(taskID string) string {
	if len(taskID) > 8 {
		return taskID[:8]
	}
	return taskID
}

// TaskBranch 由任务 ID 派生自动分支名 handoff/<id8>。
func TaskBranch(taskID string) string {
	return TaskBranchPrefix + ID8(taskID)
}

// IsCommitSHA 判断 s 是否为 40 位小写十六进制提交号。
func IsCommitSHA(s string) bool {
	return commitSHARe.MatchString(s)
}

// NewResultRef 构造结果引用。commit 非空时必须通过 IsCommitSHA。
func NewResultRef(repo, branch, commit, path string) (ResultRef, error) {
	if commit != "" && !IsCommitSHA(commit) {
		return ResultRef{}, fmt.Errorf("结果 commit 必须是 40 位小写十六进制，实得 %q", commit)
	}
	return ResultRef{Repo: repo, Branch: branch, Commit: commit, Path: path}, nil
}

// HasLedgerAttachField 报告结构体是否声明了挂卡字段（CardIDs 或 CardResults）。
// Ticket 0 用它锁住 PrepareReq / ManualReq / ManualTree 不得携带挂卡参数。
func HasLedgerAttachField(v any) bool {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	if _, ok := t.FieldByName("CardIDs"); ok {
		return true
	}
	_, ok := t.FieldByName("CardResults")
	return ok
}
