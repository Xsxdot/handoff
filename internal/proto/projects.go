// projects.go —— 项目树、机器投影与跨机汇总的线格式类型（W3a）。
//
// 职责：
//   - 定义 GET /api/projects/tree 的三层嵌套响应（project → location → workspace）
//   - 定义 GET /api/machines 的机器投影
//   - 定义 §5.3 的跨机汇总信封（machines 一栏让「谁没答上来」必须可见）
//
// 边界：
//   - 纯类型：不含探测逻辑、不含转发逻辑，实现都在 internal/agentd
//   - 这三层里只有 location 有持久化真相（B62 的 project_locations 表）；
//     project 是 origin_url 的纯函数，workspace 是现场探测的产物，都不落库
//   - 本文件是前后端契约的 Go 侧真相：改动必须同步
//     web/src/api/types.ts 与 web/src/api/testdata/*.json（后者由
//     TestContractFixtures -update 生成，不手写）
package proto

import "time"

// Workspace 是一个 git 工作树（含主工作树自身）。
//
// 来源：`git -C <location.path> worktree list --porcelain` 现场探测，不落库——
// worktree 会在 agentd 背后被 add/remove，落表必然产生说谎的行。
type Workspace struct {
	Path string `json:"path"`
	// Branch 是该工作树当前所在分支；detached HEAD 时为空串，此时看 Head。
	Branch string `json:"branch"`
	// Head 是短 sha。
	Head   string `json:"head"`
	IsMain bool   `json:"is_main"`
	// Managed 表示该工作树是 agentd 自建的任务工作树（路径落在 agentd 的
	// worktree 根下）。UI 据此区分「任务工作树」与「人手开的工作树」。
	Managed bool `json:"managed"`
}

// ProjectLocationNode 是一个项目在**一台**机器上的位置（项目树的中间层）。
//
// 不变式（ADR-0008 / W3a §1.1）：单机响应里每个项目的 locations 恒为 0 或 1 条；
// 长度 >1 只可能出现在 ?scope=all 的汇总结果里，此时每条的 Machine 互不相同。
type ProjectLocationNode struct {
	// Machine 是该位置所在机器：""=本机；否则为本机 cfg.Targets 的键。
	Machine string `json:"machine"`
	// Name 是登记名（project_locations.name），每台机器内唯一，不参与身份判定。
	Name string `json:"name"`
	Path string `json:"path"`
	// Workspaces 是该位置下的全部工作树；探测失败时为空数组而非 null。
	Workspaces []Workspace `json:"workspaces"`
	// ProbeError 是探测失败的人话说明，空串=正常。
	//
	// 为什么失败不返回错误码：项目树必须能展示「登记还在、目录已失效」这种
	// 真实状态，整棵树 500 会让用户连哪个项目坏了都看不见。
	ProbeError string `json:"probe_error"`
}

// ProjectNode 是项目树的顶层：一个跨机器同一的项目。
type ProjectNode struct {
	// ProjectID 由 projectid.FromOrigin(origin_url) 派生，跨机天然相等。
	ProjectID string `json:"project_id"`
	OriginURL string `json:"origin_url"`
	// Name 取该项目下首条登记的 name（各机登记名可能不同，展示取其一）。
	Name      string                `json:"name"`
	Locations []ProjectLocationNode `json:"locations"`
}

// MachineStatus 是跨机汇总信封里每台机器的应答情况。
//
// 硬约束（W3a §5.3）：任何一台机器没答上来，都必须出现在汇总响应的 machines
// 里且 Ok=false 带原因——静默少几行是本设计的头号失败模式。
type MachineStatus struct {
	Name string `json:"name"`
	Ok   bool   `json:"ok"`
	// FetchedAt 是本机拿到该机数据的时刻（快照读则为快照时刻），UI 据此显示数据新旧。
	FetchedAt time.Time `json:"fetched_at"`
	// Error 是不可达原因原文，Ok=true 时为空串。
	Error string `json:"error"`
}

// ProjectTreeResp 是 GET /api/projects/tree 的响应。
//
// Machines 仅在 ?scope=all 时出现（单机请求不带这一栏，omitempty）。
type ProjectTreeResp struct {
	Projects []ProjectNode `json:"projects"`
	// Unowned 是算不出 project_id 的脏行（列出登记名），诚实列出不吞。
	Unowned  []string        `json:"unowned"`
	Machines []MachineStatus `json:"machines,omitempty"`
}

// Machine 是 GET /api/machines 的单台投影。
//
// 列表 = 本机 + cfg.Targets 全部条目；运行数据现场探活（并发、共 3s 预算）。
// **不可达是数据不是错误**：单台超时/拒连不影响整个响应 200。
type Machine struct {
	// Name 为 ""=本机（与 tasks.target 的空串语义一致；UI 显示「本机」）。
	Name string `json:"name"`
	Addr string `json:"addr"`
	// Reachable=false 时 Error 必非空。
	Reachable bool   `json:"reachable"`
	Version   string `json:"version"`
	// Executors / DefaultExecutor 取自探活时读到的 GET /api/status，只读投影，
	// 不构成「机器配置面」——执行者开关等写操作不在 W3a 范围内。
	Executors       []string `json:"executors"`
	DefaultExecutor string   `json:"default_executor"`
	// ProbeMs 是本次探活实测往返毫秒；本机恒 0（进程内直查，不自拨 HTTP）。
	ProbeMs     int64  `json:"probe_ms"`
	ActiveTasks int    `json:"active_tasks"`
	Error       string `json:"error"`

	// PtySupported 是这台机器的 PTY 能力位，探活时从它的 StatusResp 投影而来。
	//
	// 三态，与 StatusResp.PtySupported 同一纪律：
	//   nil   = 没上报（对端版本过旧，或这台机器压根没探到）
	//   false = 平台明确不支持
	//   true  = 支持
	// 消费方（控制台）据此决定终端入口画什么。**nil 不许当 false 用**：
	// 那会让老版本 agentd 上的终端入口凭空消失，而它其实可能是能用的。
	PtySupported *bool `json:"pty_supported,omitempty"`
}

// MachinesResp 是 GET /api/machines 的响应信封。
type MachinesResp struct {
	Machines []Machine `json:"machines"`
}

// TasksResp 是 GET /api/tasks?scope=all 的跨机汇总响应（W3a §5.3）。
//
// 注意：不带 scope=all 的 GET /api/tasks 仍返回**裸数组** []TaskView，
// 与 W2 契约逐字节不变——汇总是另一种形状，不能改写既有端点的响应形态。
//
// Tasks 里的远端条目取自 mirror_tasks 快照（不现场扇出），其 Machine 字段
// 由本机 agentd 盖章；本机条目 Machine 为空串。
type TasksResp struct {
	Machines []MachineStatus `json:"machines"`
	Tasks    []TaskView      `json:"tasks"`
}

// DirEntry 是工作树目录列举里的一项（GET /api/workspaces/dir）。
//
// 只有三个字段是刻意的：文件浏览需要的是「这一层有什么、哪些能展开、多大」，
// 而 mtime / mode / owner 都会诱导前端做它不该做的判断（比如按 mtime 猜改动，
// 那是 diff 的活）。Size 只对普通文件有意义，目录恒 0 并被 omitempty 省略。
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

// DirListResult 是 GET /api/workspaces/dir 的响应体。
//
// Entries 永不为 nil：空目录返回 []，前端 `.map` 不需要判空。
type DirListResult struct {
	Entries []DirEntry `json:"entries"`
}

// SearchHit 是搜索命中的一行（GET /api/workspaces/search）。
type SearchHit struct {
	Rel  string `json:"rel"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchResult 是搜索的完整结论。
type SearchResult struct {
	Hits      []SearchHit `json:"hits"`
	Truncated bool        `json:"truncated"`
}

// FileRead 是一次文件读取的完整结论（GET /api/workspaces/file 的响应体）。
//
// 为什么是结构体而不是继续返回一个 content 字符串：写回需要知道「这份内容完不
// 完整、是不是文本、基线哈希是多少」，而这三件事只有读的那一刻知道。让调用方
// 二次判断（比如按扩展名猜二进制）必然与服务端分叉成「前端说能编辑、后端说不能」。
//
// SHA256 只在**完整且是文本**时才有值。它唯一的用途是当写入前置条件，而
// Binary / Truncated 两种情况本来就不许写——**空值即「这文件不可编辑」**，
// 前端不必再判一次，后端也不必为一个注定被拒的写入算哈希。
//
// Size 是磁盘真实大小，不是 len(Content)：截断时两者不同，而用户要看到的是真实大小。
type FileRead struct {
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated,omitempty"` // 超过 1 MiB，只返回开头
	Binary    bool   `json:"binary,omitempty"`    // 前 8 KiB 出现 NUL 字节
	SHA256    string `json:"sha256,omitempty"`
}

// FileWriteReq 是 PUT /api/workspaces/file 的请求体。
//
// BaseSHA256 必填：它是调用方**读到那一版**的哈希，服务端拿它与磁盘现状比对，
// 不一致就 409。空串一律判为不匹配——没读过就想写，正是覆盖别人改动的场景。
type FileWriteReq struct {
	Content    string `json:"content"`
	BaseSHA256 string `json:"base_sha256"`
}

// FileWriteResp 是写入成功后的响应。
//
// SHA256 是**新内容**的哈希，调用方直接拿它当下一次写入的 base_sha256，
// 不需要为了拿新基线再读一次。
type FileWriteResp struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// FileConflictResp 是 409 的响应体。
//
// 带上 Current（磁盘现状的完整读取结论）是为了让冲突界面一次成型：用户要在
// 「放弃我的改动」和「用我的内容覆盖」之间选，两个动作都需要磁盘现状——前者
// 要它的正文，后者要它的哈希当新基线。分两次请求会在两次之间再开一个窗口。
type FileConflictResp struct {
	Error   string   `json:"error"`
	Current FileRead `json:"current"`
}
