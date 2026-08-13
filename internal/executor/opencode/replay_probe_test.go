// replay_probe_test.go —— 2026-08-12「回合终结信号探针」样本的重放测试。
//
// 样本从哪来：2026-08-12 22:09–22:40 在专用沙箱仓库上按场景 S1/S2/S3/S4 各派发
// 一次 opencode 任务，由 internal/executor/rawtap 的原始字节旁路抓下 GET /event
// 的逐行原始字节（探针记录见 docs/superpowers/probes/2026-08-12-turn-end/README.md）。
// 入库前只做了一次反转义（rawtap 写盘时把 `\` `\n` `\r` 转义过），未做任何裁剪。
//
// 职责：
//   - 把 testdata/probe-s{1..4}-opencode.jsonl 原样回放给 adapter，断言回合分类
//   - 断言探针的核心结论：截断场景 S3 的两个判别信号 —— step-finish 的
//     reason:"unknown" 与 tool part 被翻成 "invalid" —— 在 S3 出现，
//     且在 S1/S2/S4 三份基线里**一次都不出现**
//   - 断言 S4 的提问确实走了 opencode 原生 question 通道（que_ 前缀的原生 id）
//
// 边界：
//   - 只断言「这份样本里有什么 / 当前代码对它判成什么」，不断言 opencode 应该发什么
//   - 不 mock 解析：样本按原始字节喂进 streamOnce，走生产解析路径；样本级扫描
//     也复用生产的 sseEvent 分帧壳
//   - 不断言探针当时的线上判定（S1 当时判 result OK）：那次判定取决于沙箱仓库
//     有没有新提交，而回放用的是非 git 目录，兜底分类恒定判「无新提交」
//
// 为什么必须入库（既有规矩，见同目录 replay_spike_test.go 的头注释）：spec
// §3.5「opencode 加证据层、grok/codex 不加」这条处置，全部依据就是这四份样本。
// 样本留在本机等于结论无法从任何一个 clone 复核 —— B74 的原始现场正是这样永久
// 丢失的。
//
// 这个测试在防什么退化：
//  1. 防「判别信号消失而无人知晓」。证据层将来要读的就是 reason/tool 这两个字段；
//     上游一旦改掉取值（比如 unknown 改成别的、invalid 改成 error），本测试变红，
//     而不是等证据层在线上默默失效。
//  2. 防「把不判别的信号当判别用」。反向断言（S1/S2/S4 三份基线里恒为 0）才是
//     「可判别」的证明 —— 只证明 S3 里有这个值，证明不了它能区分 S3 与 S1。
package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
)

// probeSample 描述一份探针样本及其可核对的事实。
//
// 字段取值全部来自探针结果表（README 的 15 行表），不是期望值：与表对不上的
// 断言比没有断言更糟。
type probeSample struct {
	scenario string // 场景代号（S1/S2/S3/S4）
	file     string // testdata 下的文件名
	session  string // 样本里的 opencode 会话 id（会话隔离过滤要匹配它）
	// truncSignals 是本样本里两个截断判别信号的出现次数。
	// S3 之外的三份必须全为 0 —— 这就是判别性本身。
	wantUnknownStepFinish int
	wantInvalidToolPart   int
	// wantNativeQuestion 为真表示本场景走了 opencode 原生 question 通道
	// （question.asked 事件 + que_ 前缀的原生请求 id）
	wantNativeQuestion bool
}

var probeSamples = []probeSample{
	{
		scenario: "S1", file: "probe-s1-opencode.jsonl",
		session:               "ses_009b13876ffea3ePfR7RnhYl3s",
		wantUnknownStepFinish: 0, wantInvalidToolPart: 0,
	},
	{
		scenario: "S2", file: "probe-s2-opencode.jsonl",
		session:               "ses_009ae0247ffePgNX6z20Ex8nD1",
		wantUnknownStepFinish: 0, wantInvalidToolPart: 0,
	},
	{
		scenario: "S3", file: "probe-s3-opencode.jsonl",
		session: "ses_009abfbdeffecetlZpIxomROw4",
		// 实测：截断的那一步多出一条 reason:"unknown" 的 step-finish（tokens 全 0），
		// 且那个 write 工具 part 的 tool 字段被翻成 "invalid"（两帧：pending 与其后的快照）
		wantUnknownStepFinish: 1, wantInvalidToolPart: 2,
	},
	{
		scenario: "S4", file: "probe-s4-opencode.jsonl",
		session:               "ses_00996b4a1ffe0KsDSS9EahleNo",
		wantUnknownStepFinish: 0, wantInvalidToolPart: 0,
		wantNativeQuestion: true,
	},
}

// probePartFields 是样本级扫描要取的两个字段。
//
// 为什么单独定义而不复用生产结构：生产的 mapPartUpdated 今天只解
// id/messageID/type/text —— reason 与 tool 正是它**尚未**读取的两个字段，
// 而 spec §3.5 的证据层要做的就是把它们读起来。分帧壳仍用生产的 sseEvent，
// 不另写一套 SSE 解析。
type probePartFields struct {
	Part struct {
		Type   string `json:"type"`
		Reason string `json:"reason"` // type=step-finish 时的终结原因
		Tool   string `json:"tool"`   // type=tool 时的工具名
	} `json:"part"`
}

// scanProbeSignals 逐帧扫描一份样本，数出两个判别信号的出现次数。
//
// 参数：
//   - file: testdata 下的样本文件名
//
// 返回：
//   - reason:"unknown" 的 step-finish part 帧数
//   - tool:"invalid" 的 tool part 帧数
func scanProbeSignals(t *testing.T, file string) (unknownSteps, invalidTools int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatalf("读取探针样本 %s: %v", file, err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev sseEvent
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev) != nil {
			continue
		}
		if ev.Type != "message.part.updated" {
			continue
		}
		var pf probePartFields
		if json.Unmarshal(ev.Properties, &pf) != nil {
			continue
		}
		switch {
		case pf.Part.Type == "step-finish" && pf.Part.Reason == "unknown":
			unknownSteps++
		case pf.Part.Tool == "invalid":
			invalidTools++
		}
	}
	return unknownSteps, invalidTools
}

// TestProbeTruncationSignalIsDiscriminative 是本文件的主断言，两个方向缺一不可：
//
//   - 正向：S3（截断）样本里 reason:"unknown" 与 tool:"invalid" 确实出现
//   - 反向：S1/S2/S4 三份基线样本里两者出现 0 次
//
// 反向那半才是「可判别于 S1」的证明。少了它，本测试只能说明「S3 里有这个值」，
// 而 spec §3.5 要的是「这个值能把 S3 和 S1 分开」。
func TestProbeTruncationSignalIsDiscriminative(t *testing.T) {
	for _, s := range probeSamples {
		t.Run(s.scenario, func(t *testing.T) {
			unknownSteps, invalidTools := scanProbeSignals(t, s.file)
			if unknownSteps != s.wantUnknownStepFinish {
				t.Errorf("%s: step-finish reason=\"unknown\" 出现 %d 次, want %d",
					s.scenario, unknownSteps, s.wantUnknownStepFinish)
			}
			if invalidTools != s.wantInvalidToolPart {
				t.Errorf("%s: tool part tool=\"invalid\" 出现 %d 次, want %d",
					s.scenario, invalidTools, s.wantInvalidToolPart)
			}
		})
	}
}

// TestProbeReplayClassification 把四份样本原样回放给 adapter，断言生产解析路径
// 对每份样本的回合分类。
//
// 回放用的 RepoPath 是空临时目录（非 git），兜底分类因此恒定判「无新提交」→
// 转 question。这是刻意的：探针当时 S1 被判 result OK 靠的是沙箱仓库里有新提交，
// 那是仓库副作用而非事件层信息 —— 探针的第二条结论正是「trailer 缺失时事件层
// 给不出任何帮助」，这里把它固定成断言。
func TestProbeReplayClassification(t *testing.T) {
	for _, s := range probeSamples {
		t.Run(s.scenario, func(t *testing.T) {
			fx := spikeFixture{file: s.file, session: s.session}
			got := collectReplay(t, startReplay(t, fx), time.Second)

			var last *executor.AdapterEvent
			for i, ev := range got {
				if ev.Type == "question" || ev.Type == "result" {
					last = &got[i]
				}
			}
			if last == nil {
				t.Fatalf("%s 回放未产出任何终结事件，实际事件 %v", s.scenario, typesOf(got))
			}
			if last.Type != "question" {
				t.Fatalf("%s 回放终结事件 = %s, want question（非 git 仓库下兜底恒转提问）",
					s.scenario, last.Type)
			}
			// S4 走原生通道：QuestionID 是 opencode 的原生 que_ 请求 id；
			// 其余三份是回合末合成的 ask，没有原生 id（manager 退回 uuid）
			if s.wantNativeQuestion {
				if !strings.HasPrefix(last.QuestionID, "que_") {
					t.Errorf("%s 应走原生 question 通道，QuestionID = %q, want que_ 前缀",
						s.scenario, last.QuestionID)
				}
			} else if last.QuestionID != "" {
				t.Errorf("%s 不应带原生提问 id，实际 %q", s.scenario, last.QuestionID)
			}
		})
	}
}

// TestProbeFixturesAreRealCaptures 守住样本本身：必须是 SSE 原始字节
// （data: 前缀 + 空行分隔）且能在流里找到该场景的会话 id。
// 样本被误替换成手写 JSON 时，上面两条断言就不再是「真实形态」的证据。
func TestProbeFixturesAreRealCaptures(t *testing.T) {
	for _, s := range probeSamples {
		raw, err := os.ReadFile(filepath.Join("testdata", s.file))
		if err != nil {
			t.Fatalf("读取探针样本 %s: %v", s.file, err)
		}
		if !strings.HasPrefix(string(raw), "data: ") {
			t.Errorf("%s 不是 SSE 原始抓包（应以 \"data: \" 开头）", s.file)
		}
		var sawSession bool
		for line := range strings.SplitSeq(string(raw), "\n") {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var ev sseEvent
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev) != nil {
				continue
			}
			var prop struct {
				SessionID string `json:"sessionID"`
			}
			_ = json.Unmarshal(ev.Properties, &prop)
			if prop.SessionID == s.session {
				sawSession = true
				break
			}
		}
		if !sawSession {
			t.Errorf("%s 中找不到会话 %s，样本与测试的会话 id 已失配", s.file, s.session)
		}
	}
}
