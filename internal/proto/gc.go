// gc.go —— handoff gc 的传输契约类型。
//
// 职责：
//   - 定义机器级 gc 的预览/执行请求，以及缓存叶子与残留工作树的报告行
//   - 固定「将释放字节」可缺席与明确为零的 JSON 区分
//
// 边界：
//   - 只描述线上格式，不计算目录、工作树或任务状态
//   - 不改变任务状态、不删除任务目录、分支或用户自建工作树
package proto

// GCRequest 是 POST /api/gc 的请求体。
//
// GET /api/gc 使用同名 force 查询参数；execute 由 HTTP 方法表达，不能由
// 客户端请求体伪造成执行动作。
type GCRequest struct {
	Force bool `json:"force"`
}

// GCItemStatus 是 gc 报告行的处置状态。
type GCItemStatus string

const (
	// GCItemPlanned 表示预览中将被删除，尚未执行。
	GCItemPlanned GCItemStatus = "planned"
	// GCItemDeleted 表示执行中已经删除。
	GCItemDeleted GCItemStatus = "deleted"
	// GCItemSkipped 表示按保护规则保留。
	GCItemSkipped GCItemStatus = "skipped"
	// GCItemFailed 表示本应删除但删除失败。
	GCItemFailed GCItemStatus = "failed"
)

// GCCacheRow 是一处 task-private cache leaf 的报告行。
type GCCacheRow struct {
	TaskID string       `json:"task_id"`
	Path   string       `json:"path"`
	Bytes  int64        `json:"bytes"`
	Status GCItemStatus `json:"status"`
	Error  string       `json:"error,omitempty"`
}

// GCWorktreeRow 是 gc 内部复用 reclaim 判定后的残留 managed worktree 报告行。
//
// 字段显式展开而非嵌入 ReclaimRow，避免新增 wire 的 JSON 形状依赖 Go 匿名字段
// 的序列化规则；实现节点仍可从既有 reclaim 判定得到这些值。
type GCWorktreeRow struct {
	TaskID     string        `json:"task_id"`
	Name       string        `json:"name"`
	State      string        `json:"state"`
	Branch     string        `json:"branch"`
	WorkDir    string        `json:"work_dir"`
	Worktree   WorktreeState `json:"worktree"`
	DirtyCount int           `json:"dirty_count"`
	Note       string        `json:"note,omitempty"`
	Status     GCItemStatus  `json:"status"`
	Error      string        `json:"error,omitempty"`
}

// GCResp 是 GET /api/gc 预览或 POST /api/gc 执行的统一报告。
//
// ReleasableBytes 使用指针：nil 表示尚未取得可计算结果，非 nil 且为 0 表示
// 已计算并确认没有可释放字节，防止 JSON 中「缺席」与「零」被混为一谈。
type GCResp struct {
	Preview         bool            `json:"preview"`
	Force           bool            `json:"force"`
	ReleasableBytes *int64          `json:"releasable_bytes,omitempty"`
	CacheRows       []GCCacheRow    `json:"cache_rows"`
	WorktreeRows    []GCWorktreeRow `json:"worktree_rows"`
	Scanned         int             `json:"scanned"`
	Failures        int             `json:"failures"`
}
