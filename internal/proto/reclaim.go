// reclaim.go —— handoff reclaim 的传输契约类型。
//
// 职责：
//   - 定义 GET /api/reclaim 的列表响应与 POST /api/tasks/{id}/reclaim 的动作响应
//   - 定义四种 409 拒绝理由的机器码，供 CLI 分派渲染
//
// 边界：
//   - 只描述线上格式，不含任何判定逻辑（判定在 internal/agentd/reclaim.go）
//   - 不描述进程残留（那是 FootprintRow 的事，两者互不覆盖）
package proto

// WorktreeState 是一个终态任务的 managed worktree 当前所处的态。
//
// 注意：Unknown 与 Absent 必须分开。「仓库不可达所以判不出」与「确实没有残留」
// 是两回事，把前者渲染成后者等于用一个假结论把该看的东西藏起来（同 B70 的
// 「不猜 0」纪律）。
type WorktreeState string

const (
	// WorktreeClean 在册且 git status 为空，可直接回收。
	WorktreeClean WorktreeState = "clean"
	// WorktreeDirty 在册但有未提交改动或未跟踪文件，默认拒绝回收。
	WorktreeDirty WorktreeState = "dirty"
	// WorktreePrunable 在册但目录已不存在。它不占磁盘，占的是分支——
	// 照样能让 git push --delete 被拒，因此必须能被看见与回收。
	WorktreePrunable WorktreeState = "prunable"
	// WorktreeAbsent 不在册，无残留。
	WorktreeAbsent WorktreeState = "absent"
	// WorktreeUnknown 仓库不可达或不是 git 仓库，判不出。
	WorktreeUnknown WorktreeState = "unknown"
)

// DirtyFile 是脏工作树里的一个条目，来自 git status --porcelain。
type DirtyFile struct {
	// Status 是 porcelain 的两字符状态码，如 "M " / "??" / "R "。
	Status string `json:"status"`
	Path   string `json:"path"`
}

// ReclaimRow 是 GET /api/reclaim 列表里的一行。
type ReclaimRow struct {
	TaskID string `json:"task_id"`
	Name   string `json:"name"`
	// State 是任务状态（completed / failed），不是工作树状态。
	State   string `json:"state"`
	Branch  string `json:"branch"`
	WorkDir string `json:"work_dir"`
	// Worktree 是工作树状态，取值见 WorktreeState。
	Worktree WorktreeState `json:"worktree"`
	// DirtyCount 仅在 Worktree=dirty 时有意义。列表只给条数不给清单——
	// 清单可能很长，要看细节走单任务回收的 409 响应。
	DirtyCount int `json:"dirty_count"`
	// Note 是 Worktree=unknown / prunable 时的真因，供人读。
	Note string `json:"note,omitempty"`
}

// ReclaimListResp 是 GET /api/reclaim 的响应。
type ReclaimListResp struct {
	// Rows 只含「仍有残留或判不出」的行；干净收场的任务不入表。
	Rows []ReclaimRow `json:"rows"`
	// Scanned 是本次体检过的终态任务总数，供 CLI 打「共体检 N 个」。
	Scanned int `json:"scanned"`
}

// ReclaimAction 是一次回收实际做了什么。
type ReclaimAction string

const (
	// ReclaimRemoved 走 git worktree remove 删掉了。
	ReclaimRemoved ReclaimAction = "removed"
	// ReclaimPruned 走 git worktree prune 清掉了在册条目（remove 失败后的兜底）。
	ReclaimPruned ReclaimAction = "pruned"
	// ReclaimAlreadyAbsent 本来就不在册，无动作。幂等成功走这条。
	ReclaimAlreadyAbsent ReclaimAction = "already_absent"
)

// ReclaimResp 是 POST /api/tasks/{id}/reclaim 成功时的响应。
type ReclaimResp struct {
	Removed bool          `json:"removed"`
	Action  ReclaimAction `json:"action"`
	WorkDir string        `json:"work_dir"`
	Branch  string        `json:"branch"`
	// Discarded 是 force 强删时被丢弃的条目。留痕用：强删不能悄悄发生。
	Discarded []DirtyFile `json:"discarded,omitempty"`
}

// ReclaimReason 是 409 拒绝的机器码。
//
// 为什么必须有：四种拒绝共用 409 一个状态码，CLI 要分派渲染就只能靠它。
// 靠解析中文文案是不行的——文案是给人看的、会改，机器码是契约、不改。
type ReclaimReason string

const (
	ReasonNotTerminal     ReclaimReason = "not_terminal"
	ReasonDirty           ReclaimReason = "dirty"
	ReasonRepoUnreachable ReclaimReason = "repo_unreachable"
	ReasonNotManaged      ReclaimReason = "not_managed"
)

// ReclaimError 是 409 的响应体。
type ReclaimError struct {
	Error  string        `json:"error"`
	Reason ReclaimReason `json:"reason"`
	// Dirty 仅在 Reason=dirty 时非空，是结构化清单而非预渲染文本——
	// 渲染是 CLI 的事。
	Dirty []DirtyFile `json:"dirty,omitempty"`
}
