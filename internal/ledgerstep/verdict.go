// Package ledgerstep 工作流节点执行器：主会话（经 CLI）与看板按钮（经 API）
// 共用同一份节点决策与派发装配。
// 边界：无自有状态——回合计数从事件流推导，全部写入经 internal/ledger。
package ledgerstep

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Finding 审阅发现项。
type Finding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	File     string `json:"file,omitempty"`
}

// Verdict 解析后的裁决。
type Verdict struct {
	Pass     bool      `json:"pass"`
	Findings []Finding `json:"findings"`
	Notes    string    `json:"notes,omitempty"`
	Raw      string    `json:"-"`
}

var verdictBlockPat = regexp.MustCompile("(?s)```handoff-verdict\\s*\\n(.*?)\\n?```")

// ParseVerdict 从审阅报文提取最后一个 handoff-verdict block 并解析。
// 解析失败不猜（调用方转「等人」，原文落 timeline）——spec §5 契约。
func ParseVerdict(message string) (Verdict, error) {
	blocks := verdictBlockPat.FindAllStringSubmatch(message, -1)
	if len(blocks) == 0 {
		return Verdict{}, fmt.Errorf("报文中没有 handoff-verdict block")
	}
	raw := strings.TrimSpace(blocks[len(blocks)-1][1])
	var wire struct {
		Verdict  string    `json:"verdict"`
		Findings []Finding `json:"findings"`
		Notes    string    `json:"notes"`
	}
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return Verdict{}, fmt.Errorf("裁决 JSON 解析失败: %w（原文: %.200s）", err, raw)
	}
	switch wire.Verdict {
	case "pass":
		return Verdict{Pass: true, Findings: wire.Findings, Notes: wire.Notes, Raw: raw}, nil
	case "fail":
		return Verdict{Pass: false, Findings: wire.Findings, Notes: wire.Notes, Raw: raw}, nil
	default:
		return Verdict{}, fmt.Errorf("verdict 值 %q 不在 {pass,fail}", wire.Verdict)
	}
}
