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
