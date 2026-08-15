// frames.go —— 结构化回合帧的线格式。
//
// 职责：
//   - 定义 Frame 与 FrameType：frames.jsonl 每一行、以及
//     GET /api/tasks/{id}/frames 每一行的形状
//
// 边界：
//   - 纯类型定义：不写文件、不做 I/O、不认识任何具体 executor
//   - 不是事件：控制面事件是 Event（events 表），帧只用 RefSeq 指向它
//
// 为什么帧的 Seq 与 Event.Seq 是两套编号：帧 Seq 是**任务内**从 1 开始的行号，
// 由 FrameWriter 维护；Event.Seq 是 SQLite 的**库级**自增主键。混用会让
// 「第 5 帧」和「第 5 号事件」互相冒充。
package proto

import "time"

// FrameType 是帧的类型。
type FrameType string

const (
	// FrameText 是模型正文增量（按 Part 拼接）。
	FrameText FrameType = "text"
	// FrameReasoning 是思维链增量（按 Part 拼接）。绝不进回合正文。
	FrameReasoning FrameType = "reasoning"
	// FrameToolCall 是一次工具调用，一次性完整帧。
	FrameToolCall FrameType = "tool_call"
	// FrameToolResult 是一次工具结果，与 tool_call 靠同一个 Part 配对。
	FrameToolResult FrameType = "tool_result"
	// FrameEvent 是控制面事件的引用（只存指针与类型名，不复制 payload）。
	FrameEvent FrameType = "event"
	// FrameTurnStart 是回合边界。
	FrameTurnStart FrameType = "turn_start"
)

// Frame 是一条结构化回合帧，对应 frames.jsonl 的一行。
//
// 字段按 Type 取用，无关字段一律 omitempty 缺席：
//   - text / reasoning:   Part + Delta
//   - tool_call:          Part + Tool + Input（可能 Truncated，Bytes 为原始长度）
//   - tool_result:        Part + Status + Output（同上）
//   - event:              RefSeq + Event
//   - turn_start:         Reason（"dispatch" 或 "send"）
type Frame struct {
	// Seq 是任务内单调递增的帧号，从 1 开始。与 Event.Seq 无关（见文件头）。
	Seq int64 `json:"seq"`
	// TS 是帧产生时刻。
	TS time.Time `json:"ts"`
	// Turn 是回合序号，从 1 开始。
	Turn int `json:"turn"`
	// Type 决定下面哪些字段有意义。
	Type FrameType `json:"type"`

	// Part 标识帧所属的片段：text/reasoning 靠它拼接，tool_call/tool_result
	// 靠它配对。只需在**同一回合内**唯一，跨回合可以重复。
	Part string `json:"part,omitempty"`

	// Delta 是 text / reasoning 的文本增量（不是快照）。
	Delta string `json:"delta,omitempty"`

	// Tool 是 tool_call 的工具名。
	Tool string `json:"tool,omitempty"`
	// Input 是 tool_call 的入参，可能被头尾截断。
	Input string `json:"input,omitempty"`
	// Output 是 tool_result 的输出，可能被头尾截断。
	Output string `json:"output,omitempty"`
	// Status 是 tool_result 的结局（ok / error / 上游原文）。
	Status string `json:"status,omitempty"`

	// Truncated 报告 Input/Output 是否被截断。
	Truncated bool `json:"truncated,omitempty"`
	// Bytes 是截断前的原始字节数（未截断时为 0）。
	Bytes int64 `json:"bytes,omitempty"`

	// RefSeq 是 event 帧指向的 events 表 seq。
	RefSeq int64 `json:"ref_seq,omitempty"`
	// Event 是 event 帧的事件类型名。刻意的小冗余：让前端不查 events 表
	// 也知道该画什么形状的卡片，类型名是稳定的，不会漂移。
	Event string `json:"event,omitempty"`

	// Reason 是 turn_start 的起因："dispatch"（Adapter.Start）或
	// "send"（Adapter.Send）。不细分"续接"与"回答提问"——Send 是单一方法，
	// adapter 分不出来，编出来的区分是假的。
	Reason string `json:"reason,omitempty"`
}
