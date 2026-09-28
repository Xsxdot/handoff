// Package ledgerstep 工作流节点执行器：主会话（经 CLI）与看板按钮（经 API）
// 共用同一份节点决策与派发装配。
// 边界：无自有状态——回合计数从事件流推导，全部写入经 internal/ledger。
// 本文件解析最后 handoff-verdict 围栏；抢救只针对围栏正文，不扫描整回合文本。
// 缺收尾围栏时退化为「最后一处开盘后的第一段 JSON 对象」（B416），仍只认围栏正文。
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

// verdictFenceOpen 是裁决围栏的开盘标记，与 verdictBlockPat 的字面量保持一致。
const verdictFenceOpen = "```handoff-verdict"

// unterminatedVerdictBody 取最后一处开盘围栏之后的第一段完整 JSON 对象，供漏写
// 收尾围栏的报文使用（B416：opencode 部分模型实测会写出只有开盘的围栏）。
//
// 为什么只取紧跟开盘标记的第一个 JSON 对象、而不是「开盘到报文末尾」：模型漏闭合
// 时，围栏正文之后往往直接跟回合 trailer JSON 或后续散文；把整段都当正文会把误读面
// 扩大到 trailer/散文里的任何字面量。只认第一个 JSON 对象，既不丢裁决对象本身，也
// 不吞后文。正文的严格解析与逐字段抢救完全复用正式路径；找不到开盘标记、或其后没有
// 可解码的 JSON 对象时返回 ok=false，由调用方继续 fail-closed。
func unterminatedVerdictBody(message string) (string, bool) {
	i := strings.LastIndex(message, verdictFenceOpen)
	if i < 0 {
		return "", false
	}
	rest := message[i+len(verdictFenceOpen):]
	j := strings.IndexByte(rest, '{')
	if j < 0 {
		return "", false
	}
	var body json.RawMessage
	if err := json.NewDecoder(strings.NewReader(rest[j:])).Decode(&body); err != nil {
		return "", false
	}
	return string(body), true
}

// ParseVerdict 从审阅报文提取最后一个 handoff-verdict block 并解析。
// 严格 JSON 失败时，只从该围栏正文逐字段抢救；无法确认 verdict 时仍报错。
// 模型漏写收尾围栏时（B416），退化为「从最后一处开盘围栏起到报文末尾」，正文仍
// 走同一套严格 JSON + 抢救判定。
func ParseVerdict(message string) (Verdict, error) {
	blocks := verdictBlockPat.FindAllStringSubmatch(message, -1)
	var raw string
	if len(blocks) > 0 {
		raw = strings.TrimSpace(blocks[len(blocks)-1][1])
	} else if body, ok := unterminatedVerdictBody(message); ok {
		raw = body
	} else {
		return Verdict{}, fmt.Errorf("报文中没有 handoff-verdict block")
	}
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
