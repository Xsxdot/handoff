// 节点注入点的生产实现：节点派发走 dispatch 通道，裁决节点 wait 终态并取
// 报文；task 生命周期在此收口（裁决落账后 done 归档，不留孤儿）。
package ledgerstep

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// waitForTurnEnd 反复 wait 直到出现回合终态事件（completed/turn_failed/
// failed），中途的权限门与工单一律跳过继续等。
//
// why 要循环：WaitEvent 返回的是「首个可动作事件」而非终态。审阅虽只跑
// 只读命令，但同样要过权限门，也可能发工单——2026-08-19 真机实测，环节
// 几乎必然醒在 permission_request/question 上，随即去取最终报文，报
// 「事件流中没有 completed/failed 最终报文」，一轮审阅白跑。函数头写的
// 「wait 终态」是意图，WaitEvent 的语义不是，这里补上差额。
//
// 阻塞行为：executor 发了工单又不收尾时本函数会一直等，等价于任何一条
// handoff task 挂在 waiting_answer——审核者从 wait --card 上看得见。
func waitForTurnEnd(ctx context.Context, wait func(context.Context) (*proto.Event, error)) error {
	for {
		event, err := wait(ctx)
		if err != nil {
			return err
		}
		if event == nil {
			continue
		}
		switch event.Type {
		case proto.EventTypeCompleted, proto.EventTypeTurnFailed, proto.EventTypeFailed:
			return nil
		}
	}
}

// clientFinalMessage 从 attach 快照取最后一条 completed/turn_failed/failed
// 事件，按真实协议字段返回最终报文；缺失即报错，不拿 progress 凑数。
func clientFinalMessage(ctx context.Context, cl *client.Client, taskID string) (string, error) {
	info, err := cl.Attach(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("取审阅最终报文: %w", err)
	}
	message, err := finalMessageFromEvents(info.RecentEvents)
	if err != nil {
		return "", fmt.Errorf("取审阅最终报文: %w", err)
	}
	return message, nil
}

// finalMessageFromEvents 是协议字段解析的纯函数，供 wire 层单测固定
// completed.summary 与 turn_failed/failed.fail_reason 的真实线格式。
func finalMessageFromEvents(events []proto.Event) (string, error) {
	// completed 优先于失败类事件，即使失败排在后面：codex 收尾时常在
	// completed 之后再补一条 turn_failed（app-server 的 WebSocket 断开），
	// 那是传输层的假警报，不是回合失败——报告已经在 completed 里了。
	// 环节执行器每轮审阅都派一条新 task 并等它的首个终态，所以「本次
	// 生命周期内出现过 completed」就意味着报文存在，取它不会串到上一轮。
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != proto.EventTypeCompleted {
			continue
		}
		var payload struct {
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
			return "", fmt.Errorf("completed payload 解析失败: %w", err)
		}
		if payload.Summary == "" {
			return "", fmt.Errorf("completed payload 缺 summary")
		}
		return payload.Summary, nil
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		switch event.Type {
		case proto.EventTypeCompleted:
			var payload struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return "", fmt.Errorf("completed payload 解析失败: %w", err)
			}
			if payload.Summary == "" {
				return "", fmt.Errorf("completed payload 缺 summary")
			}
			return payload.Summary, nil
		case proto.EventTypeTurnFailed, proto.EventTypeFailed:
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
	}
	return "", fmt.Errorf("事件流中没有 completed/failed 最终报文")
}
