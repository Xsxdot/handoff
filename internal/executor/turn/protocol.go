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
// 的审核者会看到不一样的东西。
package turn

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// promptTemplate 是任务 prompt 的回合制纪律模板，逐字来自一期 spec §6 的落地。
//
// 注意：模板内嵌的 {"ask":...}/{"branch":...} 是给模型看的协议样例，
// 与 text/template 语法不冲突（不含 {{ ），可直接放在字面文本中。
const promptTemplate = `你是 handoff 任务 {{.TaskID}} 的执行者，按下方实现计划执行。铁律：
1. 提问纪律：任何需要人决策的问题，输出单行 JSON {"ask":"<问题>"}
   然后结束本回合。审核者的回答会作为下一条消息发给你。
   禁止自行假设，禁止用其它格式提问。
2. 收尾纪律：全部完成后必须 git add 并 commit（不要 push），
   然后输出单行 JSON：{"branch":"<分支>","commit":"<hash>","summary":"<50字内摘要>"}
   作为本回合最后一行。
3. 只在当前分支工作，不切分支、不改 git 配置。

--- 实现计划 ---
{{.PlanContent}}
`

// promptTmpl 是 promptTemplate 的编译结果。Must 保证拼写错误的模板在包加载时
// 立刻暴露（编程错误），而不是在任务运行时才崩——模板不依赖运行时状态。
var promptTmpl = template.Must(template.New("prompt").Parse(promptTemplate))

type promptData struct {
	TaskID      string
	PlanContent string
}

// RenderPrompt 渲染带回合纪律的启动 prompt。
//
// 参数：
//   - taskID: 任务 ID，写入 prompt 标题行
//   - planContent: 实现计划全文（dispatch 侧已把 --prompt 附加指令拼在其后），
//     原样嵌入「实现计划」段，本函数不再二次拼接
//
// 返回：渲染后的 prompt 全文；模板执行失败时返回错误
func RenderPrompt(taskID, planContent string) (string, error) {
	var buf bytes.Buffer
	if err := promptTmpl.Execute(&buf, promptData{TaskID: taskID, PlanContent: planContent}); err != nil {
		return "", fmt.Errorf("渲染 prompt 模板: %w", err)
	}
	return buf.String(), nil
}

// ParseTrailer 从回合末消息文本提取协议 JSON（取最后一个以 { 开头的行）。
//
// 返回：
//   - kind: "ask"（附 Question）| "finish"（附 Branch/Commit/Summary）| "none"
//   - t: 解析出的协议数据；kind 为 "none" 时为零值
//
// 注意：
//   - 宽容语义：末行是普通文本时回退到更早的 { 开头行；找不到或 JSON 损坏时
//     返回 "none"，绝不 panic（模型输出不可信，防御在边界上做）
//   - 纯函数：不打日志，由调用方记录提取结果
func ParseTrailer(text string) (kind string, t Trailer) {
	// 取最后一个以 { 开头的行：模型可能在正文中间输出过协议 JSON 后又追加
	// 说明文字，只有最后一个才有「本回合结论」的语义
	var last string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			last = line
		}
	}
	if last == "" {
		return "none", t
	}

	// 宽容解码：不设 DisallowUnknownFields，模型多带字段时仍能提取已知协议字段
	var payload struct {
		Question string `json:"ask"`
		Branch   string `json:"branch"`
		Commit   string `json:"commit"`
		Summary  string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(last)), &payload); err != nil {
		return "none", t
	}
	t = Trailer{Question: payload.Question, Branch: payload.Branch,
		Commit: payload.Commit, Summary: payload.Summary}

	// ask 与 finish 协议互斥（模型按纪律一次只输出一种），问号优先判定
	switch {
	case t.Question != "":
		return "ask", t
	case t.Branch != "" || t.Commit != "" || t.Summary != "":
		return "finish", t
	default:
		return "none", t
	}
}
