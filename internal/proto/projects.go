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
	// Managed 表示该工作树落在 agentd 的数据区（<DataDir>/worktrees）下——
	// 既包括任务自建树（worktrees/<id8>），也包括手工新建树（worktrees/manual/<名>）。
	// 判据只看路径前缀，不区分二者：本字段没有任何行为消费者（回收只认终态任务
	// 的记录、从不扫目录），为它加特例只会留下一个要读三处代码才懂的例外。
	Managed bool `json:"managed"`
	// CreatedAt 是这个工作树被建出来的时间；零值 = 取不到。
	//
	// 取法：stat <git 公共目录>/worktrees/<名>/gitdir。那个文件由
	// git worktree add 写一次之后就不再动，是唯一稳定的创建时间证据。
	// 刻意不 stat 工作树目录本身——它的 mtime 会随着往里写代码变化，
	// 排出来的是「最近动过」而不是「什么时候建的」。
	//
	// **主工作树恒为零值**：它没有 worktrees/<名>/gitdir，而 .git 目录的 mtime
	// 是「最后一次在里面增删条目」不是创建时间（实测一个 08-07 建的仓库报出
	// 08-18）。准确的答案要文件系统 birthtime，Go 标准库不给。消费方（控制台
	// 排序）把主工作树钉在第一位、不参与比较，这个值没有消费者——如实留零值，
	// 好过报一个自信的错值。
	//
	// 为什么取不到时留零值而不是报错：整棵项目树不该因为一个 stat 失败就 500。
	// 消费方把零值当「最旧」处理。
	CreatedAt time.Time `json:"created_at"`
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

	// RevealSupported 是这台机器的「在访达中显示」能力位，探活时从它的
	// StatusResp 投影而来。三态与 PtySupported 同一纪律。
	RevealSupported *bool `json:"reveal_supported,omitempty"`

	// Upgrade 是这台机器最近一次升级的状态（本机恒缺席：本机版本走薄壳同步路）。
	// 缺席=本 agentd 进程内没发起过升级；读法见 MachineUpgrade 的三态说明。
	Upgrade *MachineUpgrade `json:"upgrade,omitempty"`

	// ScratchRoot 是这台机器的草稿区路径，探活时从它的 StatusResp 投影而来。
	// 空串（omitempty 后为缺席）= 这台机器不支持临时文件，前端不渲染入口。
	ScratchRoot string `json:"scratch_root,omitempty"`
}

// MachineUpgrade 是一台执行机最近一次升级的状态，随 GET /api/machines 一起返回。
//
// 为什么需要它：升级**没有进度流**，这是刻意的——完成的判据就是这台机器的
// version 变成了最新。但那只覆盖成功路径：失败时版本压根不会变，控制台按钮上的
// 「升级中」就永远清不掉，而后端其实早已放弃（真机实测：agentd 三分钟前就记下
// 「下载 checksums.txt 超时」，界面还在转）。这一段把**终态**交回控制台，
// 补的是出口，不是进度流。
//
// 三态读法：
//
//	nil            = 这台机器本进程内从未发起过升级（agentd 重启即回到 nil）
//	Running=true   = 正在升级；其余字段是上一轮的结果，可能全空
//	Running=false  = 已结束，Status 为终态
//
// **nil 不许当「没失败」用**：它只说明这个 agentd 不知道，不说明没发生过。
type MachineUpgrade struct {
	Running bool `json:"running"`
	// Status 是终态：ok / skip / fail；从未跑完时为空。
	Status  string `json:"status,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	// Reason / Remedy 原样透传 internal/upgrade 的结论，不在这里重新措辞。
	Reason string `json:"reason,omitempty"`
	Remedy string `json:"remedy,omitempty"`
	// From / To 仅在 Status==ok 时有值。
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
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
// 字段是刻意克制的：文件浏览需要的是「这一层有什么、哪些能展开、多大、哪些
// 不归 git 管」，而 mtime / mode / owner 都会诱导前端做它不该做的判断（比如按
// mtime 猜改动，那是 diff 的活）。Size 只对普通文件有意义，目录恒 0 并被
// omitempty 省略。
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
	// Ignored 表示该条目被 .gitignore 排除（判据是 git check-ignore，不是前端
	// 猜后缀）。false 会被 omitempty 省略——**缺键 = 未被忽略**，不代表「没查过」：
	// 查不出来时（git 不可用、目录不是仓库）服务端一律按未忽略返回并打日志，
	// 宁可少标一个，也不把源码标成垃圾。
	Ignored bool `json:"ignored,omitempty"`
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

// TaskPlan 是 GET /api/tasks/{id}/plan 的响应：**派发当刻交给 executor 的指令原文**。
//
// 它就是 agentd 归档在任务目录里的那份 plan/prompt（dispatch 的 plan 文件，
// prompt-only 派发时是 prompt.md；两者都有时是拼好的那一份）。控制台把它当
// 「第一条审核者消息」展示——在此之前，界面上唯一能看到的只有一个截断的
// plan_summary，「这个任务当初到底被要求做什么」无处可查。
//
// Size 是磁盘真实大小，不是 len(Content)：截断时两者不同，用户要看到的是真实大小。
type TaskPlan struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated,omitempty"`
}

// CreateWorkspaceEntryReq 是 POST /api/workspaces/entry 的请求体。
type CreateWorkspaceEntryReq struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "file" 或 "dir"
}

// RenameWorkspaceEntryReq 是 PATCH /api/workspaces/entry 的请求体。
type RenameWorkspaceEntryReq struct {
	NewName string `json:"new_name"`
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

// AddMachineReq 是 POST /api/machines 的请求体。
//
// **Token 只进不出**：本结构仅用于反序列化请求。任何响应体、任何日志
// 都不得包含它——proto.Machine 从设计之初就没有 Token 字段，这条性质
// 必须保持。
//
// Force=true 跳过可达性探测直接落库，用于「对端临时离线但确认地址无误」
// 的场景；默认 false，让粘错的地址或令牌当场暴露。
type AddMachineReq struct {
	Name  string `json:"name"`
	Addr  string `json:"addr"`
	Token string `json:"token"`
	User  string `json:"user"`
	Force bool   `json:"force"`
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

// ProjectBranch 是一个本地分支，带「是否已被工作树占用」。
//
// Worktree 为已检出该分支的工作树路径；空串 = 没有任何工作树占用它。
// git 不允许同一分支被两个工作树同时检出，所以占用者就是「这个分支现在
// 不能再开树」的全部原因——界面据此把选项置灰并说清是谁占着。
type ProjectBranch struct {
	Name     string `json:"name"`
	Worktree string `json:"worktree"`
}

// ProjectBranchesResp 是 GET /api/projects/{name}/branches 的响应。
//
// 顶层形状（branches + default）与 /api/tasks/{id}/branches 一致，但 branches
// 是对象数组而非字符串数组——多了占用信息，两者刻意不共用类型。
type ProjectBranchesResp struct {
	// Branches 永不为 nil（空仓库返回空数组）。
	Branches []ProjectBranch `json:"branches"`
	// Default 是推导出的基准分支；推导不出为空串。
	Default string `json:"default"`
	// WorktreeRoot 是手工新建工作树的落点根目录，供界面如实回显「会建在哪」。
	// 界面只回显这个根，不自己拼完整路径——目录名的生成规则只有服务端一份。
	WorktreeRoot string `json:"worktree_root"`
}

// CreateWorktreeReq 是 POST /api/projects/{name}/worktrees 的请求体。
type CreateWorktreeReq struct {
	// Mode 二选一："new_branch"（建新分支并开树）/ "existing_branch"（把已有分支开成一棵树）。
	Mode string `json:"mode"`
	// Branch 是要新建或要检出的分支名，必填。
	Branch string `json:"branch"`
	// Base 是新分支的起点，仅 new_branch 模式有意义；空串时由服务端推导。
	Base string `json:"base"`
}
