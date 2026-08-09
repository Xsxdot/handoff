// proto 包测试：验证任务状态机迁移表（CanTransit）与 JSON 线格式契约。
//
// 职责：
//   - 锁定任务状态迁移的合法性（pending → running → … → completed/failed）
//   - 断言 wait/tasks/attach 命令与 server 共用结构体的 JSON key 全部小写，
//     锁死 wait 输出与 HTTP/WS 线格式契约（上层脚本按小写 key 解析）
//
// 边界：
//   - 不覆盖持久化（由 store 包测试负责）、不覆盖状态机的并发迁移
//     （CanTransit 只回答「单步迁移是否合法」）
package proto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCanTransit(t *testing.T) {
	cases := []struct {
		name string
		from TaskState
		to   TaskState
		want bool
	}{
		{"pending_to_running", TaskStatePending, TaskStateRunning, true},
		{"running_to_waiting_answer", TaskStateRunning, TaskStateWaitingAnswer, true},
		{"waiting_answer_to_running", TaskStateWaitingAnswer, TaskStateRunning, true},
		{"running_to_waiting_review", TaskStateRunning, TaskStateWaitingReview, true},
		{"waiting_review_to_running_continue", TaskStateWaitingReview, TaskStateRunning, true},
		{"completed_to_running", TaskStateCompleted, TaskStateRunning, false},
		{"failed_to_running_retry", TaskStateFailed, TaskStateRunning, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanTransit(c.from, c.to); got != c.want {
				t.Errorf("CanTransit(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
			}
		})
	}
}

// TestJSONWireFormat 断言 CLI 输出与 WS/REST 共用结构体的 JSON 线格式 key 全部小写。
//
// 为什么断言序列化产物而非 Go 结构体字段：wait/tasks/attach 命令与 server
// 直接 json.Marshal 这些结构体，上层脚本按小写 key（{"seq":..,"type":..}）解析；
// 若字段未带 JSON tag，Go 会输出大写 key，脚本静默失效。此测试锁死序列化结果，
// 防止未来有人给结构体改名字时顺手把契约改坏。
func TestJSONWireFormat(t *testing.T) {
	now := time.Now()
	answer := "allow"
	event := Event{
		Seq:       7,
		TaskID:    "t1",
		Type:      EventTypeQuestion,
		Payload:   json.RawMessage(`{"text":"继续吗"}`),
		CreatedAt: now,
	}
	task := Task{
		ID:          "t1",
		Target:      "opencode",
		RepoPath:    "/repo",
		Branch:      "feat/x",
		PlanPath:    "plan.md",
		PlanSummary: "s",
		State:       TaskStateWaitingAnswer,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	ticket := Ticket{
		ID:         "tk1",
		TaskID:     "t1",
		Kind:       "gate",
		Request:    json.RawMessage(`{"cmd":"rm -rf /tmp/x"}`),
		Answer:     &answer,
		CreatedAt:  now,
		AnsweredAt: &now,
	}

	evJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	taskJSON, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	ticketJSON, err := json.Marshal(ticket)
	if err != nil {
		t.Fatalf("marshal ticket: %v", err)
	}

	// 契约 key 必须小写（wait 输出 {"seq":..,"type":"question","payload":{..}}）。
	eventWant := []string{`"seq":7`, `"task_id":"t1"`, `"type":"question"`, `"payload":{"text":"继续吗"}`}
	for _, want := range eventWant {
		if !strings.Contains(string(evJSON), want) {
			t.Errorf("event JSON %s 缺少契约 key %s", evJSON, want)
		}
	}
	taskWant := []string{`"id":"t1"`, `"repo_path":"/repo"`, `"state":"waiting_answer"`, `"created_at"`}
	for _, want := range taskWant {
		if !strings.Contains(string(taskJSON), want) {
			t.Errorf("task JSON %s 缺少契约 key %s", taskJSON, want)
		}
	}
	ticketWant := []string{`"id":"tk1"`, `"kind":"gate"`, `"request":{"cmd":"rm -rf /tmp/x"}`, `"answer":"allow"`}
	for _, want := range ticketWant {
		if !strings.Contains(string(ticketJSON), want) {
			t.Errorf("ticket JSON %s 缺少契约 key %s", ticketJSON, want)
		}
	}

	// 反向断言：不得出现大写 key（Go 默认输出即大写，一旦 JSON tag 丢失立即暴露）。
	for name, b := range map[string][]byte{"event": evJSON, "task": taskJSON, "ticket": ticketJSON} {
		if strings.Contains(string(b), `"Seq"`) || strings.Contains(string(b), `"ID"`) ||
			strings.Contains(string(b), `"TaskID"`) || strings.Contains(string(b), `"CreatedAt"`) {
			t.Errorf("%s JSON 包含大写 key，违反小写契约: %s", name, b)
		}
	}
}

// TestTaskJSONDesktopIDs 断言 Task JSON 线格式包含 machine_id 与 workspace_id，
// 且旧 JSON（缺这两个键）仍可解码为空值——桌面归属字段对旧 CLI/历史库兼容。
func TestTaskJSONDesktopIDs(t *testing.T) {
	task := Task{
		ID:          "t1",
		MachineID:   "m-local",
		WorkspaceID: "ws-1",
	}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	for _, want := range []string{`"machine_id":"m-local"`, `"workspace_id":"ws-1"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("task JSON %s 缺少契约 key %s", b, want)
		}
	}

	// 旧 JSON 缺字段仍可解码（默认空串），不破坏历史库/旧 CLI 输出解析
	old := []byte(`{"id":"t1","repo_path":"/repo","state":"pending"}`)
	var decoded Task
	if err := json.Unmarshal(old, &decoded); err != nil {
		t.Fatalf("unmarshal 旧 JSON: %v", err)
	}
	if decoded.MachineID != "" || decoded.WorkspaceID != "" {
		t.Errorf("旧 JSON 解码后 machine_id=%q workspace_id=%q, want 空串", decoded.MachineID, decoded.WorkspaceID)
	}
}

// TestTaskStateStalledTransitions 覆盖 TaskStateStalled 的合法/非法迁移。
func TestTaskStateStalledTransitions(t *testing.T) {
	valid := [][2]TaskState{
		{TaskStateRunning, TaskStateStalled},
		{TaskStateWaitingAnswer, TaskStateStalled},
		{TaskStateWaitingReview, TaskStateStalled},
		{TaskStateStalled, TaskStateRunning},
		{TaskStateStalled, TaskStateFailed},
	}
	for _, c := range valid {
		if !CanTransit(c[0], c[1]) {
			t.Errorf("CanTransit(%q, %q) = false, want true", c[0], c[1])
		}
	}
	invalid := [][2]TaskState{
		{TaskStatePending, TaskStateStalled},
		{TaskStateCompleted, TaskStateStalled},
		{TaskStateFailed, TaskStateStalled},
		{TaskStateStalled, TaskStatePending},
		{TaskStateStalled, TaskStateCompleted},
		{TaskStateStalled, TaskStateWaitingAnswer},
		{TaskStateStalled, TaskStateWaitingReview},
	}
	for _, c := range invalid {
		if CanTransit(c[0], c[1]) {
			t.Errorf("CanTransit(%q, %q) = true, want false", c[0], c[1])
		}
	}
}
