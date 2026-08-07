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
)

// Task 表示一个 handoff 任务。
type Task struct {
	ID              string
	Target          string
	RepoPath        string
	Branch          string
	PlanPath        string
	PlanSummary     string
	ExecutorSession string
	State           TaskState
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Event 表示任务生命周期中产生的一条事件记录。
type Event struct {
	Seq       int64
	TaskID    string
	Type      EventType
	Payload   json.RawMessage
	CreatedAt time.Time
}

// Ticket 表示一次需人工介入的请求，Kind 取 "gate"（许可门）或 "ask"（提问）。
type Ticket struct {
	ID         string
	TaskID     string
	Kind       string
	Request    json.RawMessage
	Answer     *string
	CreatedAt  time.Time
	AnsweredAt *time.Time
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
