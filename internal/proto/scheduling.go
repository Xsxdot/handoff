// 编制域 wire 面（B156.3 K3）：squads/queue 端点与协调者拉起响应的请求/响应
// DTO。字段名与 web/src/api/scheduling.ts 镜像一字不差；线格式由
// TestContractFixtures 逐字节钉住（改字段先看红，再 -update 显式刷新）。
//
// 边界：proto 不 import 编制域——View 类型是独立投影，scheduling.Carrier/Squad
// → View 的手写投影在 agentd（schedapi.go），该边界由其 wire 往返测试锁。
package proto

// CarrierView 是载体登记行（GET /api/squads 返回元素）。Version 是 registry
// 行版本，CAS 编辑回路（GET 取版 → PUT ?expect=）的唯一权威来源。
type CarrierView struct {
	Name           string `json:"name"`
	Machine        string `json:"machine"`
	CLI            string `json:"cli"`
	HomeDir        string `json:"home_dir"`
	Model          string `json:"model,omitempty"`
	Credential     string `json:"credential"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	Healthy        bool   `json:"healthy"`
	Version        int    `json:"version"`
}

// SquadView 是小队登记行。MaxConcurrency=0（不限）以键缺席表达。
type SquadView struct {
	Name           string   `json:"name"`
	Role           string   `json:"role"`
	Members        []string `json:"members"`
	MaxConcurrency int      `json:"max_concurrency,omitempty"`
	Version        int      `json:"version"`
}

// SquadsResp 是 GET /api/squads 的响应体。空库时两个字段是空数组而非 null
// ——「什么都没配」是合法态，前端据此渲染引导文案而不是报错。
type SquadsResp struct {
	Carriers []CarrierView `json:"carriers"`
	Squads   []SquadView   `json:"squads"`
}

// CarrierInput 是 PUT 载体的请求体。刻意不含 healthy：本期无探测手段，
// 服务端会把 false 静默翻真（防饿死），收了就是恒假承诺。
type CarrierInput struct {
	Name           string `json:"name,omitempty"`
	Machine        string `json:"machine"`
	CLI            string `json:"cli"`
	HomeDir        string `json:"home_dir"`
	Model          string `json:"model,omitempty"`
	Credential     string `json:"credential"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

// SquadInput 是 PUT 小队的请求体。Members 为空合法（岔口四：先建空队再补成员）。
type SquadInput struct {
	Name           string   `json:"name,omitempty"`
	Role           string   `json:"role"`
	Members        []string `json:"members"`
	MaxConcurrency int      `json:"max_concurrency,omitempty"`
}

// SquadPutResp 是 PUT 成功响应：version 是本次写入产生的版本（expect+1）；
// 他人的并发写会再推进，编辑回路每次 GET 重取。
type SquadPutResp struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// QueueEntry 是 GET /api/queue 的元素。Kind/ID/Seq/Position 来自队列元数据，
// 其余九个字段逐一来自入队时刻的 IgnitionRequest 快照（拍板记录②：出队前
// 不重读卡）。Position 是清队顺序下的全局 1 基位次。
type QueueEntry struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Card     string `json:"card"`
	Node     string `json:"node,omitempty"`
	Squad    string `json:"squad"`
	Target   string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model    string `json:"model,omitempty"`
	Priority string `json:"priority,omitempty"`
	Ready    bool   `json:"ready"`
	Actor    string `json:"actor"`
	Seq      int64  `json:"seq"`
	Position int    `json:"position"`
}

// QueueResp 是 GET /api/queue 的响应体；空队列 = "queue":[]。
type QueueResp struct {
	Queue []QueueEntry `json:"queue"`
}

// CoordinatorLaunchResp 是 POST /api/cards/{id}/coordinator/launch 的成功响应
// （keystone.RoundResult 的 wire 投影，K4 的 handler 产出、K3 的 CLI 消费——
// 形状以此处为准，K4 不得另行发明字段）。
type CoordinatorLaunchResp struct {
	Woke      bool   `json:"woke"`
	SessionID string `json:"session_id,omitempty"`
	Rebuilt   bool   `json:"rebuilt"`
	Escalated bool   `json:"escalated"`
	Output    string `json:"output,omitempty"`
}

// CoordinatorAttachInfo 是协调者会话的定位三元组；Machine 允许为空串表示本机。
// Dir 与 Command 均由服务端定位器产生，客户端不得自行拼接或改写。
type CoordinatorAttachInfo struct {
	Machine string `json:"machine"`
	Dir     string `json:"dir"`
	Command string `json:"command"`
}

// CoordinatorStatus 是 GET coordinator 的状态；Attach=nil 序列化为 null 表示未绑定。
// attach_active 是进程内的人工接管态，与 Attach 三元组是否存在是两件事。
type CoordinatorStatus struct {
	Bound        bool                   `json:"bound"`
	AttachActive bool                   `json:"attach_active"`
	Attach       *CoordinatorAttachInfo `json:"attach"`
}

// CoordinatorAttachReleaseResp 是 active=false 交回无头后的成功回执。
type CoordinatorAttachReleaseResp struct {
	OK bool `json:"ok"`
}
