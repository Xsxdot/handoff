// 消费 WaitEvent 终态并从 Attach 快照选择回合正文；不负责生产事件或裁决路由。
// 节点派发走 dispatch 通道，裁决落账后的 task 生命周期由上层收口。
package ledgerstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

var turnEndGrace = time.Second

// errTurnArchived 表示节点等待的 task 已被归档（done 等外部动作），此后不会再
// 产出 completed/failed 裁决报文。它是一类明确的终止信号，不是普通断线——必须
// 让等待收口，否则运行锁与卡槽位永不释放（B396 陈旧运行锁不自愈）。
var errTurnArchived = errors.New("等待的 task 已归档，回合不会再产出裁决报文")

// finalMessageClient 是最终报文取用的最小面：只读 attach 快照。接口定义在使用方
// 并嵌入提供方内部冻结的 client.ExecutionClient（B233.16 契约冻结）。
type finalMessageClient interface {
	client.ExecutionClient
	Attach(ctx context.Context, taskID string) (*client.AttachInfo, error)
}

type completedPayload struct {
	Summary   string  `json:"summary"`
	FinalText *string `json:"final_text"`
}

// TurnEnd 是等待层观察到的一次回合终态事实：事件类型、事件序号与失败分类。
//
// 为什么不能只返回 error：下游要按结构化字段判断「这是不是一次明确的零文本
// 故障」，把它压成裸字符串就等于逼消费方回去解析 fail_reason 散文（B100 禁止）。
// FailureClass 只从 turn_failed payload 的精确字段取，空/未知一律零值。
type TurnEnd struct {
	EventType    proto.EventType
	Seq          int64
	FailureClass proto.FailureClass
}

// failedPayload 是 turn_failed/failed 事件 payload 的解析子集。
type failedPayload struct {
	FailReason   string             `json:"fail_reason"`
	FailureClass proto.FailureClass `json:"failure_class"`
}

// failureClassOf 只从 turn_failed 事件 payload 里取封闭分类；failed 事件与
// 缺字段、未知值一律返回零值（不可自动重试）。
func failureClassOf(event *proto.Event) proto.FailureClass {
	if event == nil || event.Type != proto.EventTypeTurnFailed {
		return ""
	}
	var payload failedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return ""
	}
	return payload.FailureClass
}

func decodeCompletedPayload(event *proto.Event) (completedPayload, error) {
	if event == nil {
		return completedPayload{}, errors.New("completed event 为空")
	}
	var payload completedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return completedPayload{}, fmt.Errorf("解析 completed payload 失败: %w", err)
	}
	return payload, nil
}

// waitForTurnEnd 反复 wait 直到收到带正文的 completed、真实失败终态，
// 或 task 归档（archived，B396），中途的权限门与工单一律跳过继续等。
//
// why 要循环：WaitEvent 返回的是「首个可动作事件」而非终态。审阅虽只跑
// 只读命令，但同样要过权限门，也可能发工单——2026-08-19 真机实测，环节
// 几乎必然醒在 permission_request/question 上，随即去取最终报文，报
// 「事件流中没有 completed/failed 最终报文」，一轮审阅白跑。函数头写的
// 「wait 终态」是意图，WaitEvent 的语义不是，这里补上差额。
//
// 收到缺正文的 completed 后，后续 wait 使用秒级 child deadline 给迟到正文机会；
// deadline 到期且 parent 仍存活时返回成功，由 Attach 快照回落摘要。
func waitForTurnEnd(ctx context.Context, wait func(context.Context) (*proto.Event, error)) (TurnEnd, error) {
	for {
		event, err := wait(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "等待回合终态失败", "saw_completed", false, "error", err)
			return TurnEnd{}, err
		}
		if event == nil {
			slog.WarnContext(ctx, "等待回合终态收到空事件")
			continue
		}
		switch event.Type {
		case proto.EventTypeArchived:
			// B396：task 已被归档（外部 done/resume --force 后再 done），此后不会
			// 再产出 completed/failed 裁决报文。继续等只会把运行锁与卡节点槽位
			// 钉死，使同卡同节点永远 409。收口为可行动错误，由上游落等人标记。
			slog.WarnContext(ctx, "等待的 task 已归档，回合不会再有终态报文",
				"seq", event.Seq, "task", event.TaskID, "cause", errTurnArchived)
			return TurnEnd{}, errTurnArchived
		case proto.EventTypeCompleted:
			payload, decodeErr := decodeCompletedPayload(event)
			if decodeErr != nil {
				slog.WarnContext(ctx, "completed payload 不可解析，进入宽限", "error", decodeErr)
			} else if payload.FinalText != nil && *payload.FinalText != "" {
				slog.InfoContext(ctx, "收到带 final_text 的 completed")
				return TurnEnd{EventType: proto.EventTypeCompleted, Seq: event.Seq}, nil
			} else {
				finalTextPresent := payload.FinalText != nil
				slog.InfoContext(ctx, "收到残缺 completed，进入宽限", "final_text_present", finalTextPresent)
			}
			return waitForTurnEndGrace(ctx, wait, TurnEnd{EventType: proto.EventTypeCompleted, Seq: event.Seq})
		case proto.EventTypeTurnFailed, proto.EventTypeFailed:
			class := failureClassOf(event)
			slog.InfoContext(ctx, "未见 completed，失败事件收口",
				"event_type", event.Type, "failure_class", class)
			return TurnEnd{
				EventType:    event.Type,
				Seq:          event.Seq,
				FailureClass: class,
			}, nil
		}
	}
}

// waitForTurnEndGrace 在收到缺正文的 completed 后，用秒级 child deadline 给迟到
// 正文一次机会；到期且 parent 仍存活时返回已观察到的 completed 终态（seq 来自
// 首个 completed），由 Attach 快照回落摘要。
func waitForTurnEndGrace(ctx context.Context, wait func(context.Context) (*proto.Event, error), completed TurnEnd) (TurnEnd, error) {
	waitCtx, cancel := context.WithTimeout(ctx, turnEndGrace)
	defer cancel()
	for {
		event, err := wait(waitCtx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				slog.InfoContext(ctx, "completed 宽限到期，继续收取 Attach 摘要")
				return completed, nil
			}
			slog.ErrorContext(ctx, "等待回合终态失败", "saw_completed", true, "error", err)
			return TurnEnd{}, err
		}
		if event == nil {
			slog.WarnContext(ctx, "等待回合终态收到空事件")
			continue
		}
		if event.Type == proto.EventTypeCompleted {
			payload, decodeErr := decodeCompletedPayload(event)
			if decodeErr != nil {
				slog.WarnContext(ctx, "宽限内 completed payload 不可解析，继续等待", "error", decodeErr)
				continue
			}
			if payload.FinalText != nil && *payload.FinalText != "" {
				slog.InfoContext(ctx, "宽限内收到带 final_text 的 completed")
				return TurnEnd{EventType: proto.EventTypeCompleted, Seq: event.Seq}, nil
			}
			slog.InfoContext(ctx, "宽限内收到残缺 completed，继续等待", "final_text_present", payload.FinalText != nil)
			continue
		}
		if event.Type == proto.EventTypeArchived {
			slog.WarnContext(ctx, "宽限期内 task 已归档，回合不会再有终态报文",
				"seq", event.Seq, "task", event.TaskID, "cause", errTurnArchived)
			return TurnEnd{}, errTurnArchived
		}
		if event.Type == proto.EventTypeTurnFailed || event.Type == proto.EventTypeFailed {
			slog.InfoContext(ctx, "宽限内忽略失败事件，继续等待 completed", "event_type", event.Type)
		}
	}
}

// clientFinalMessage 从 attach 快照取最后一条 completed/turn_failed/failed
// 事件，按真实协议字段返回最终报文；缺失即报错，不拿 progress 凑数。
func clientFinalMessage(ctx context.Context, cl finalMessageClient, taskID string) (string, error) {
	info, err := cl.Attach(ctx, taskID)
	if err != nil {
		slog.ErrorContext(ctx, "取审阅 Attach 快照失败", "task", taskID, "error", err)
		return "", fmt.Errorf("取审阅最终报文: %w", err)
	}
	slog.InfoContext(ctx, "取到审阅 Attach 快照", "task", taskID, "events", len(info.RecentEvents))
	message, err := finalMessageFromEvents(info.RecentEvents)
	if err != nil {
		slog.ErrorContext(ctx, "从 Attach 快照取审阅最终报文失败", "task", taskID, "error", err)
		return "", fmt.Errorf("取审阅最终报文: %w", err)
	}
	slog.InfoContext(ctx, "取审阅最终报文完成", "task", taskID, "bytes", len(message))
	return message, nil
}

// finalMessageFromEvents 是协议字段解析的纯函数，供 wire 层单测固定
// completed.summary 与 turn_failed/failed.fail_reason 的真实线格式。
func finalMessageFromEvents(events []proto.Event) (string, error) {
	// completed 优先于失败类事件，即使失败排在后面：codex 收尾时常在
	// completed 之后再补一条 turn_failed（app-server 的 WebSocket 断开），
	// 那是传输层的假警报，不是回合失败——报告已经在 completed 里了。
	// 多条 completed 时继续扫描，非空 final_text 优先于摘要与显式空值。
	var summary string
	summaryAvailable := false
	explicitEmpty := false
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Type != proto.EventTypeCompleted {
			continue
		}
		payload, err := decodeCompletedPayload(&event)
		if err != nil {
			slog.Warn("跳过不可解析的 completed payload", "index", i, "error", err)
			continue
		}
		if payload.FinalText != nil {
			if *payload.FinalText != "" {
				return *payload.FinalText, nil
			}
			explicitEmpty = true
			continue
		}
		if payload.Summary == "" {
			slog.Warn("completed payload 缺 summary", "index", i)
			continue
		}
		if !summaryAvailable {
			summary = payload.Summary
			summaryAvailable = true
		}
	}
	if summaryAvailable {
		return summary, nil
	}
	if explicitEmpty {
		return "", fmt.Errorf("completed payload final_text 为空")
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Type != proto.EventTypeTurnFailed && event.Type != proto.EventTypeFailed {
			continue
		}
		var payload struct {
			FailReason string `json:"fail_reason"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return "", fmt.Errorf("failed payload 解析失败: %w", err)
		}
		if payload.FailReason == "" {
			return "", fmt.Errorf("failed payload 缺 fail_reason")
		}
		return payload.FailReason, nil
	}
	return "", fmt.Errorf("事件流中没有 completed/failed 最终报文")
}
