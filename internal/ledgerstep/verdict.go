// Package ledgerstep 工作流节点执行器：主会话（经 CLI）与看板按钮（经 API）
// 共用同一份节点决策与派发装配。
// 边界：无自有状态——回合计数从事件流推导，全部写入经 internal/ledger。
// 本文件解析最后 handoff-verdict 围栏；抢救只针对围栏正文，不扫描整回合文本。
package ledgerstep

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
	Pass            bool      `json:"pass"`
	Findings        []Finding `json:"findings"`
	Notes           string    `json:"notes,omitempty"`
	Raw             string    `json:"-"`
	salvaged        bool
	notesDropped    bool
	findingsDropped bool
}

var verdictBlockPat = regexp.MustCompile("(?s)```handoff-verdict\\s*\\n(.*?)\\n?```")

// ParseVerdict 从审阅报文提取最后一个 handoff-verdict block 并解析。
// 严格 JSON 失败时，只从该围栏正文逐字段抢救；无法确认 verdict 时仍报错。
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
		verdictValue, ok := firstVerdictValue(raw)
		if !ok {
			return Verdict{}, fmt.Errorf("裁决 JSON 解析失败，且无法从围栏正文抢救 verdict")
		}
		var findings []Finding
		findingsPresent := strings.Contains(raw, `"findings"`)
		findingsOK := !findingsPresent || decodeVerdictField(raw, "findings", &findings)
		if !findingsOK {
			findings = nil
		}
		var notes string
		notesPresent := strings.Contains(raw, `"notes"`)
		notesOK := !notesPresent || decodeVerdictField(raw, "notes", &notes)
		if !notesOK {
			notes = ""
		}
		result := Verdict{
			Pass: verdictValue == "pass", Findings: findings, Notes: notes, Raw: raw,
			salvaged: true, notesDropped: notesPresent && !notesOK,
			findingsDropped: findingsPresent && !findingsOK,
		}
		slog.Warn("裁决围栏已抢救", "verdict", verdictValue,
			"notes_dropped", result.notesDropped, "findings_dropped", result.findingsDropped)
		return result, nil
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

func decodeVerdictField(raw, key string, dst any) bool {
	pat := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*`)
	loc := pat.FindStringIndex(raw)
	if loc == nil {
		return false
	}
	dec := json.NewDecoder(strings.NewReader(raw[loc[1]:]))
	if err := dec.Decode(dst); err != nil {
		return false
	}
	tail := strings.TrimSpace(raw[loc[1]+int(dec.InputOffset()):])
	return tail == "" || tail[0] == ',' || tail[0] == '}'
}

func firstVerdictValue(raw string) (string, bool) {
	pat := regexp.MustCompile(`(?s)"verdict"\s*:\s*"(pass|fail)"`)
	match := pat.FindStringSubmatch(raw)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}
