// capability.go —— 工作区能力契约面（B233.4）。
//
// 职责：
//   - 定义任务/卡应用持有的 Capability：基线、隔离工作区、diff/读文件、managed 树回收
//   - 定义请求/结果/结果引用类型与字面值
//
// 边界：
//   - 本文件无 I/O、无 git 调用、不写账本、不授权 push
//   - 请求类型不含 card_ids；挂卡由应用在调用本能力之后写
//   - 跨机传输复用既有 HTTP/233.3，本包不发明第二套 git 协议
package workspace

import "context"

// ManualNewBranch / ManualExistingBranch 是手工建树 mode 的唯二合法字面值。
// 与 proto.CreateWorktreeReq.Mode 同形，本包不 import proto（d_workspace→d_protocol 预算已满）。
const (
	ManualNewBranch      = "new_branch"
	ManualExistingBranch = "existing_branch"
)

// Trigger 是一次回收决策的触发源。Stop 与 Done 必须分开：失败态是终态，
// 但取消留存现场；归档才允许按保留约定回收 managed 树。
type Trigger string

const (
	TriggerCompensate Trigger = "compensate" // 派发在 executor 接管前失败，回滚刚建的树
	TriggerStop       Trigger = "stop"       // 取消/中止：现场必须留存
	TriggerDone       Trigger = "done"       // 从 waiting_review 归档：可回收 managed
	TriggerExplicit   Trigger = "explicit"   // reclaim / gc：仅终态 managed
)

// Decision 是 MayRecycle 的结果。
type Decision string

const (
	RetainKeep    Decision = "keep"
	RetainRecycle Decision = "recycle"
)

// PrepareReq 描述一次任务工作区准备。字段与 agentd.WorkspaceReq 同形。
// 不含 card_ids。
type PrepareReq struct {
	Repo         string
	TaskID       string
	Branch       string
	NewBranch    string
	Base         string
	Worktree     string
	NewWorktree  bool
	WorktreesDir string
}

// Prepared 是 Prepare 的结果。字段与 agentd.Workspace 同形。
type Prepared struct {
	Branch         string
	WorkDir        string
	Managed        bool
	NewBranchTip   string
	PrevRef        string
	RepoDirtyCount int
	RepoDirtyFiles string
}

// Baseline 是一次基线决议：校验结论与新分支起点出自同一次计算。
type Baseline struct {
	Start   string
	Ahead   int
	Fetched bool
}

// ResultRef 是代码结果的定位引用：仓库、分支、准确 commit，或仍可读的路径。
//
// Commit 非空时必须是 40 位小写十六进制。Path 在 managed 树已回收时允许为空。
type ResultRef struct {
	Repo   string
	Branch string
	Commit string
	Path   string
}

// ManualReq 是手工建树请求。没有 CardIDs 字段。
type ManualReq struct {
	Mode   string
	Branch string
	Base   string
}

// ManualTree 是手工建树结果。没有 CardResults 字段。
type ManualTree struct {
	Path    string
	Branch  string
	Head    string
	Managed bool
}

// FileContent 是一次仓库内文件读取。与 proto.FileRead 同形，本包自持以免涨协议预算。
type FileContent struct {
	Content   string
	Size      int64
	Truncated bool
	Binary    bool
	SHA256    string
}

// RecycleInput 是 MayRecycle 的纯数据输入。不读 git、不读磁盘。
type RecycleInput struct {
	Managed  bool
	Manual   bool
	Terminal bool
	State    string
	Trigger  Trigger
}

// Capability 是工作区能力的进程内契约面。
//
// 具体实现由组装点注入；本节点不接线生产路径。nil 不得解释成「跳过基线/脏检查」。
// 方法集不含 Push。任何方法都不得写 card 附件。
type Capability interface {
	EnsureRepoUsable(ctx context.Context, repo string) error
	ResolveBaseline(ctx context.Context, repo, sha string) (Baseline, error)
	Prepare(ctx context.Context, req PrepareReq) (Prepared, error)
	CreateManual(ctx context.Context, repo, worktreesDir string, req ManualReq) (ManualTree, error)
	DiffRange(ctx context.Context, repo, base, head string) (string, error)
	ReadFile(ctx context.Context, repo, rel string) (FileContent, error)
	RecycleManaged(ctx context.Context, repo, workdir string) error
}
