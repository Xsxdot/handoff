// Package turn 提供 executor 无关的「回合协议」：教模型协议的 prompt 模板、
// 解析模型输出的 trailer、回合取证与文本工具。
//
// 职责：
//   - RenderPrompt：把实现计划渲染成带回合纪律（提问/收尾/不切分支）的启动 prompt
//   - ParseTrailer：从回合末文本宽容提取协议 JSON（ask/finish）
//   - GitTurnStatus：trailer 缺失时以「是否有新提交」作事实裁决
//   - 文本截断与 render.log 追加等两 adapter 共用的小工具
//
// 边界：
//   - 不认识任何具体 executor（opencode/grok/claude），不发请求、不起进程
//   - 不做状态机迁移、不写 store：只做纯变换与两个受限 I/O（git 只读、日志追加）
//
// 为什么 prompt 模板与 ParseTrailer 必须同包：教模型协议的 prompt 与解析协议的
// 代码是同一契约的两半，分居两处必然出现「改纪律只改一半」的漂移——两个 executor
// 的协调者会看到不一样的东西。
package turn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// Trailer 是从回合末消息文本提取出的协议数据。
type Trailer struct {
	Question string // ask 类型：需要人决策的问题
	Branch   string // finish 类型：提交所在分支
	Commit   string // finish 类型：提交 hash
	Summary  string // finish 类型：50 字内摘要
}

// ProtocolRules 是回合制协议铁律的原文，供启动 prompt 与 codex 的常驻指令复用。
//
// 只保留一份文本是为了避免首回合消息与常驻指令漂移；收尾纪律是
// turn.ParseTrailer 的前提，漂移会让完成判定失去协议依据。
const ProtocolRules = `1. 提问纪律：任何需要人决策的问题，输出单行 JSON {"ask":"<问题>"}
   然后结束本回合。协调者的回答会作为下一条消息发给你。
   禁止自行假设，禁止用其它格式提问。
2. 收尾纪律：全部完成后必须 git add 并 commit（不要 push），
   然后输出单行 JSON：{"branch":"<分支>","commit":"<hash>","summary":"<50字内摘要>"}
   作为本回合最后一行。
3. 只在当前分支工作，不切分支、不改 git 配置。`

// promptTemplate 是任务 prompt 的骨架：标题行 + 协议铁律 + 可选纪律块 + 实现计划。
// 纪律块用 {{if}} 包住，显式关闭注入时不会留下小标题或空段落。
const promptTemplate = `你是 handoff 任务 {{.TaskID}} 的执行者，按下方实现计划执行。铁律：
{{.ProtocolRules}}

{{if .Discipline}}--- 执行纪律（先读这段，再读计划）---
{{.Discipline}}

{{end}}--- 实现计划 ---
{{.PlanContent}}
`

// promptTmpl 是 promptTemplate 的编译结果。Must 保证拼写错误的模板在包加载时
// 立刻暴露（编程错误），而不是在任务运行时才崩——模板不依赖运行时状态。
var promptTmpl = template.Must(template.New("prompt").Parse(promptTemplate))

type promptData struct {
	TaskID        string
	ProtocolRules string
	Discipline    string
	PlanContent   string
}

// RenderPrompt 渲染带回合纪律的启动 prompt。
//
// 参数：
//   - taskID: 任务 ID，写入 prompt 标题行
//   - planContent: 实现计划全文（dispatch 侧已把 --prompt 附加指令拼在其后），
//     原样嵌入「实现计划」段，本函数不再二次拼接
//   - disciplineBlock: 按执行者裁出的执行纪律块；空串表示不注入，产物不含纪律块标记
//
// 返回：渲染后的 prompt 全文；模板执行失败时返回错误
func RenderPrompt(taskID, planContent, disciplineBlock string) (string, error) {
	var buf bytes.Buffer
	if err := promptTmpl.Execute(&buf, promptData{
		TaskID: taskID, ProtocolRules: ProtocolRules,
		Discipline: strings.TrimSpace(disciplineBlock), PlanContent: planContent,
	}); err != nil {
		return "", fmt.Errorf("渲染 prompt 模板: %w", err)
	}
	return buf.String(), nil
}

// ParseTrailer 从回合末文本宽容提取协议 JSON（ask/finish）。
//
// 提取分两级：
//   - 主路径：最后一个非空行，从该行第一个 { 起解码一个 JSON 值（容忍前缀
//     与后缀正文）
//   - 回退：主路径无果时，取最后一个「以 { 开头」的行按整行解码（旧规则）
//
// 返回：
//   - kind: "ask"（附 Question）| "finish"（附 Branch/Commit/Summary）| "none"
//   - t: 解析出的协议数据；kind 为 "none" 时为零值
//
// 注意：
//   - 放宽只作用于最后一个非空行：正文中间复述协议 JSON 不会被误当成结论
//   - 找不到或 JSON 损坏时返回 "none"，绝不 panic（模型输出不可信，防御在边界上做）
//   - 纯函数：不打日志，由调用方记录提取结果
func ParseTrailer(text string) (kind string, t Trailer) {
	// 裁决块是正文的一部分，不是回合协议 trailer。模型可能把它写在
	// trailer 后（codex 真机形态），若直接按最后一行/最后一个 JSON 行扫描，
	// 裁决 JSON 会遮住真正的 branch/commit/summary。先移除完整裁决块，保留
	// trailer 的既有「末行优先、旧 JSON 行回退」语义；不完整的块不移除，
	// 继续 fail-closed 地判 none。
	text = verdictBlockPat.ReplaceAllString(text, "")
	lines := strings.Split(text, "\n")

	// 主路径：只在最后一个非空行上宽容提取（B48）。模型会把正文和协议 JSON
	// 写在同一行（真机现场 `g.{"branch":...}`），旧的「整行以 { 开头」判据
	// 认不出，于是判 none 走 git 兜底、用 git 实况顶掉模型自己报的结论。
	//
	// 为什么只放宽最后一行：放宽必然扩大误吞面——正文里复述协议格式的 JSON
	// 会被当成本回合结论，这是 grok adapter 已经踩过的坑。限制在末行与收尾
	// 纪律「作为本回合最后一行」对齐，正文中间写什么都不受影响。
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue // 末尾空行不算「最后一行」
		}
		if k, tr, ok := decodeProtocolJSON(line); ok {
			return k, tr
		}
		break // 只试最后一个非空行，不向前扫
	}

	// 回退：现有规则原样保留——取最后一个「以 { 开头」的行。模型写完 trailer
	// 又追加了一整行正文时，末行没有 {，靠这条兜住。
	var last string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			last = line
		}
	}
	if last == "" {
		return "none", t
	}
	if k, tr, ok := decodeProtocolJSON(strings.TrimSpace(last)); ok {
		return k, tr
	}
	return "none", t
}

var verdictBlockPat = regexp.MustCompile("(?s)```handoff-verdict\\s*\\n.*?\\n?```")

// decodeProtocolJSON 从 line 中第一个 { 起解码一个 JSON 值，并按协议字段分类。
//
// 参数：line 为已 TrimSpace 的单行文本
//
// 返回：
//   - kind: "ask" | "finish"
//   - t: 解析出的协议数据
//   - ok: 是否解出了带协议字段的 JSON；false 时前两个返回值无意义
//
// 注意：
//   - 用 json.Decoder 而非 Unmarshal：Decode 读满第一个完整 JSON 值即停，
//     因此该值之后的正文（`{"ask":"q"} 好的`）不会让解析失败
//   - 宽容解码：不设 DisallowUnknownFields，模型多带字段时仍能提取已知字段
//   - 解出的 JSON 不含任何协议字段时返回 ok=false，由调用方继续往下判
func decodeProtocolJSON(line string) (kind string, t Trailer, ok bool) {
	i := strings.Index(line, "{")
	if i < 0 {
		return "", t, false
	}
	var payload struct {
		Question string `json:"ask"`
		Branch   string `json:"branch"`
		Commit   string `json:"commit"`
		Summary  string `json:"summary"`
	}
	if err := json.NewDecoder(strings.NewReader(line[i:])).Decode(&payload); err != nil {
		return "", t, false
	}
	t = Trailer{Question: payload.Question, Branch: payload.Branch,
		Commit: payload.Commit, Summary: payload.Summary}
	// ask 与 finish 协议互斥（模型按纪律一次只输出一种），问号优先判定
	switch {
	case t.Question != "":
		return "ask", t, true
	case t.Branch != "" || t.Commit != "" || t.Summary != "":
		return "finish", t, true
	default:
		return "", Trailer{}, false
	}
}
