// Package proto 是 handoff 协议类型的唯一定义处。
//
// 职责：
//   - 定义任务状态（TaskState）、事件类型（EventType）及任务/事件/工单（Ticket）数据结构
//   - 提供任务状态机迁移合法性校验（CanTransit）
//
// 边界：
//   - 纯类型包：无 I/O、无业务逻辑、无外部依赖
//   - 不负责持久化、事件派发、状态变更执行等行为
package proto

import (
	"encoding/json"
	"time"
)

// TaskState 表示任务所处状态。
type TaskState string

const (
	TaskStatePending       TaskState = "pending"
	TaskStateRunning       TaskState = "running"
	TaskStateWaitingAnswer TaskState = "waiting_answer"
	TaskStateWaitingReview TaskState = "waiting_review"
	TaskStateCompleted     TaskState = "completed"
	TaskStateFailed        TaskState = "failed"
)

// EventType 表示任务产生的事件类型。
type EventType string

const (
	EventTypePermissionRequest EventType = "permission_request"
	EventTypeQuestion          EventType = "question"
	EventTypeProgress          EventType = "progress"
	EventTypeCompleted         EventType = "completed"
	EventTypeFailed            EventType = "failed"
	EventTypeStalled           EventType = "stalled"
	// EventTypeDeliveryFailed 表示审核者的应答已落库但没能送达 executor。
	//
	// 为什么必须是一类事件而不只是日志：应答未送达时 executor 仍原地阻塞，
	// 而工单已被消耗、不再出现在挂起项里——若只写日志，审核者这边完全无感，
	// 任务会一直挂到看门狗超时。作为事件产出才能唤醒审核者去执行 handoff resume。
	EventTypeDeliveryFailed EventType = "delivery_failed"
)

// Task 表示一个 handoff 任务。
//
// JSON 线格式契约（CLI wait/tasks/attach 输出与 server WS/REST 共用此结构，
// key 必须小写——上层脚本按 {"id":..,"state":..,"created_at":..} 解析）。
type Task struct {
	ID              string    `json:"id"`
	Target          string    `json:"target"`
	RepoPath        string    `json:"repo_path"`
	Branch          string    `json:"branch"`
	PlanPath        string    `json:"plan_path"`
	PlanSummary     string    `json:"plan_summary"`
	ExecutorSession string    `json:"executor_session"`
	State           TaskState `json:"state"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Event 表示任务生命周期中产生的一条事件记录。
//
// JSON 线格式契约（wait 命令输出与 WS 推送共用此结构）：{"seq":..,"task_id":..,
// "type":..,"payload":{..},"created_at":..}，key 必须小写（上层脚本按此解析）。
type Event struct {
	Seq       int64           `json:"seq"`
	TaskID    string          `json:"task_id"`
	Type      EventType       `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// Ticket 表示一次需人工介入的请求，Kind 取 "gate"（许可门）或 "ask"（提问）。
//
// JSON 线格式契约（attach 输出 pending_tickets 与 REST 响应共用此结构，
// key 必须小写）：{"id":..,"task_id":..,"kind":..,"request":{..},"answer":..,..}。
type Ticket struct {
	ID         string          `json:"id"`
	TaskID     string          `json:"task_id"`
	Kind       string          `json:"kind"`
	Request    json.RawMessage `json:"request"`
	Answer     *string         `json:"answer"`
	CreatedAt  time.Time       `json:"created_at"`
	AnsweredAt *time.Time      `json:"answered_at"`
	// DeliveredAt 是应答送达 executor 的时刻；非 nil 才代表 executor 真的收到了。
	// 与 AnsweredAt 分开记录：「审核者已裁决」与「裁决已送达」是两件事实，
	// 合并会让中继失败后无从判断该不该重投（见 Manager.RecoverStuck）。
	DeliveredAt *time.Time `json:"delivered_at"`
}

// transitTable 是任务状态机迁移表，key 为来源状态，value 为允许迁移到的状态集合。
var transitTable = map[TaskState][]TaskState{
	TaskStatePending: {TaskStateRunning, TaskStateFailed},
	TaskStateRunning: {TaskStateWaitingAnswer, TaskStateWaitingReview, TaskStateCompleted, TaskStateFailed},
	// failed 允许回到 running：任务失败后可人工重试，状态机应支持重新开始而非死锁。
	TaskStateWaitingAnswer: {TaskStateRunning, TaskStateFailed},
	TaskStateWaitingReview: {TaskStateRunning, TaskStateCompleted, TaskStateFailed},
	TaskStateCompleted:     {},
	TaskStateFailed:        {TaskStateRunning},
}

// CanTransit 校验状态迁移是否合法（如 completed 不可回 running）。
//
// 参数：
//   - from: 来源状态
//   - to: 目标状态
//
// 返回：
//   - true 表示迁移合法，false 表示不允许该迁移
//
// 注意：
//   - 未在迁移表中登记的状态一律视为不可迁移
//   - 本函数仅做静态校验，不实际变更状态
func CanTransit(from, to TaskState) bool {
	for _, allowed := range transitTable[from] {
		if allowed == to {
			return true
		}
	}
	return false
}
