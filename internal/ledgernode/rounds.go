// 回合计数：卡 × 节点粒度，从事件流推导，不存内存（spec §5——恢复
// 现场以账本为准，不信记忆）。规则：数该节点的 review_verdict 事件；
// 遇到带 human_reset_node=<节点> 的 comment 事件即清零（人工介入是
// 新基线，落事件注明）。
package ledgernode

import (
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// MaxRounds 默认封顶回合数，超限转「等人」。
const MaxRounds = 3

// CountRounds 数 node 自最近一次人工重置以来的裁决回合数。
// evs 必须按 seq 升序（EventsFromAsc 的自然输出）。
func CountRounds(evs []ledger.Event, node string) int {
	n := 0
	for _, event := range evs {
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		switch event.Type {
		case ledger.EvReviewVerdict:
			if payload["node"] == node {
				n++
			}
		case ledger.EvComment:
			if payload["human_reset_node"] == node {
				n = 0
			}
		}
	}
	return n
}
