// taskenv.go —— opencode 任务环境物料生成与回合协议 trailer 解析。
//
// 职责：
//   - WriteTaskEnv：在任务目录生成 opencode.json（权限收敛配置）与 prompt.md
//     （回合制纪律 prompt，text/template 渲染），供 serve 进程经
//     OPENCODE_CONFIG 注入（proc.go）与任务首回合 prompt 使用
//   - ParseTrailer：从模型回合末消息文本中宽容提取协议 JSON（ask/finish），
//     供 adapter 判定「该停下来提问还是该收尾」
//
// 边界：
//   - 不启动进程、不发请求：serve 进程生命周期在 proc.go，HTTP 会话在 api.go
//   - 不校验模型是否遵守纪律：只做「取最后一个 { 开头行解析」的提取，
//     解析失败一律按 none 处理，是否重试由调用方决定
//
// 为什么 permission 是「静态分级」而非全 ask（2026-08-08 dogfooding 修正）：
// 一期曾把 edit/bash 全部设为 ask，真实派发时审核者被 ls/grep/编辑测试文件
// 这类初级请求连环唤醒，审批噪音让审核流形同虚设——这恰是用户交互式用
// opencode 时不存在的问题（用户在场且全局配置宽松）。修正后的分层：
//   - edit: allow —— 在任务分支上改代码是派发的目的本身，diff 审核兜底；
//   - bash: 模式表 —— 危险模式（rm -rf/sudo/git push/reset --hard/--force/
//     curl/wget 等，见 bashPermissionRules）ask，其余 allow；
//   - webfetch/external_directory: ask —— 外访与越出工作区仍逐次确认。
// 这是三级审批链的第 0 层（静态规则）；第 1 层（廉价模型审批者）见二期 spec，
// 第 2 层是审核者/用户本人。
package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// 文件名常量：任务目录内生成的物料文件名，路径由 WriteTaskEnv 拼接。
const (
	configFileName = "opencode.json"
	promptFileName = "prompt.md"
)

// promptTemplate 是任务 prompt 的回合制纪律模板，逐字来自 spec §6 的落地。
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

// promptTmpl 是 promptTemplate 的编译结果。Must 保证拼写错误的模板在包加载
// 时立刻暴露（编程错误），而不是在任务运行时才崩——模板不依赖运行时状态。
var promptTmpl = template.Must(template.New("prompt").Parse(promptTemplate))

// permissionConfig 是 opencode.json 的 permission 段。
//
// 用结构体而非裸 map 是为了让字段顺序稳定且输出确定性，便于测试与人工核对；
// Bash 是 opencode 支持的「命令模式 → allow/ask」对象形态（encoding/json 对
// map 按键排序 marshal，输出同样确定）。分级取值的 why 见文件头注释。
type permissionConfig struct {
	Edit              string            `json:"edit"`
	Bash              map[string]string `json:"bash"`
	Webfetch          string            `json:"webfetch"`
	ExternalDirectory string            `json:"external_directory"`
}

// bashPermissionRules 是 bash 命令的静态分级模式表（第 0 层审批链）。
//
// 模式语义：opencode 按 glob 对整条命令串匹配。前缀模式（"rm *"）拦直接调用，
// 包含模式（"*rm -rf*"）拦复合命令里的嵌入（如 `go test && rm -rf x`）。
// 取舍：curl/wget 只拦前缀直调（包含模式会误伤 `grep curl` 这类无害检索）；
// "*--force*" 覆盖 push --force / --force-with-lease 等强制类变体。
// 每次修改本表必须同步 taskenv_test 的逐条断言——少一条就是静默放行。
var bashPermissionRules = map[string]string{
	"*rm -rf*":           "ask", // 递归强删（含复合命令嵌入）
	"*rm -fr*":           "ask", // 同上的换序写法
	"rm *":               "ask", // 任何直接 rm（误拒成本低、误放成本高）
	"*sudo*":             "ask", // 提权
	"*git push*":         "ask", // 外推：收尾纪律要求不 push，出现即异常
	"*git reset --hard*": "ask", // 丢弃提交
	"*--force*":          "ask", // 各类强制开关（push --force / --force-with-lease 等）
	"curl *":             "ask", // 外访直调
	"wget *":             "ask", // 外访直调
	"*":                  "allow",
}

// opencodeConfig 是 opencode.json 的完整结构，经结构体 marshal 生成
// （而非字符串拼接），避免引号/转义错误。
//
// Model 可选：经 OPENCODE_CONFIG 注入时会覆盖用户全局配置，因此默认不写死模型，
// 仅当环境变量 HANDOFF_OPENCODE_MODEL 非空时写入（远程免费模型 e2e 等场景用）。
type opencodeConfig struct {
	Model      string           `json:"model,omitempty"`
	Permission permissionConfig `json:"permission"`
}

// promptData 是 prompt 模板的渲染数据。
type promptData struct {
	TaskID      string
	PlanContent string
}

// WriteTaskEnv 在 taskDir 生成 opencode 配置与任务 prompt，返回二者路径。
//
// 参数：
//   - taskDir: 任务工作目录（须已存在，由调用方保证）
//   - taskID: 任务 ID，写入 prompt 标题行
//   - planContent: 实现计划全文，原样嵌入 prompt 的「实现计划」段
//
// 返回：
//   - configPath: 生成的 opencode.json 路径
//   - promptPath: 生成的 prompt.md 路径
//   - err: 渲染或写文件失败
//
// 注意：
//   - 重复调用幂等覆盖：同名文件会被新内容覆盖，调用方可安全重试
//   - 配置经结构体 marshal、prompt 经 text/template 渲染，均非字符串拼接
func WriteTaskEnv(taskDir, taskID, planContent string) (configPath, promptPath string, err error) {
	start := time.Now()
	configPath = filepath.Join(taskDir, configFileName)
	promptPath = filepath.Join(taskDir, promptFileName)
	logger := slog.Default()
	logger.Info("opencode 生成任务环境", "task_dir", taskDir, "task_id", taskID,
		"config", configPath, "prompt", promptPath)
	defer func() {
		if err != nil {
			logger.Error("opencode 生成任务环境失败", "task_dir", taskDir,
				"config", configPath, "prompt", promptPath, "cause", err)
		} else {
			logger.Info("opencode 任务环境已生成", "task_dir", taskDir,
				"config", configPath, "prompt", promptPath,
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	// HANDOFF_OPENCODE_MODEL：任务级 OPENCODE_CONFIG 会替换全局配置，不写 model
	// 时只能依赖 opencode 默认；远程无凭证场景显式注入免费模型更稳。
	cfg := opencodeConfig{
		Model: strings.TrimSpace(os.Getenv("HANDOFF_OPENCODE_MODEL")),
		Permission: permissionConfig{
			Edit:              "allow",
			Bash:              bashPermissionRules,
			Webfetch:          "ask",
			ExternalDirectory: "ask",
		},
	}
	if cfg.Model != "" {
		logger.Info("任务环境注入模型", "model", cfg.Model)
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		// MarshalIndent 对本结构体不可能失败，保留错误返回以防未来字段变更引入
		return configPath, promptPath, fmt.Errorf("序列化 opencode 配置: %w", err)
	}

	var buf bytes.Buffer
	if err := promptTmpl.Execute(&buf, promptData{TaskID: taskID, PlanContent: planContent}); err != nil {
		return configPath, promptPath, fmt.Errorf("渲染 prompt 模板: %w", err)
	}

	if err := os.WriteFile(configPath, cfgJSON, 0o644); err != nil {
		return configPath, promptPath, fmt.Errorf("写 %s: %w", configPath, err)
	}
	if err := os.WriteFile(promptPath, buf.Bytes(), 0o644); err != nil {
		return configPath, promptPath, fmt.Errorf("写 %s: %w", promptPath, err)
	}
	return configPath, promptPath, nil
}

// Trailer 是从回合末消息文本提取出的协议数据。
type Trailer struct {
	Question string // ask 类型：需要人决策的问题
	Branch   string // finish 类型：提交所在分支
	Commit   string // finish 类型：提交 hash
	Summary  string // finish 类型：50 字内摘要
}

// ParseTrailer 从回合末消息文本提取协议 JSON（取最后一个以 { 开头的行）。
//
// 返回：
//   - kind: "ask"（附 Question）| "finish"（附 Branch/Commit/Summary）| "none"
//   - t: 解析出的协议数据；kind 为 "none" 时为零值
//
// 注意：
//   - 宽容语义：末行是普通文本时回退到更早的 { 开头行；找不到或 JSON 损坏
//     时返回 "none"，绝不 panic（模型输出不可信，防御在边界上做）
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

	// 宽容解码：不设 DisallowUnknownFields，模型多带字段时仍能提取已知协议
	// 字段；损坏 JSON 一律按 none 处理
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

	// ask 与 finish 协议互斥（模型按纪律一次只输出一种），问号优先判定：
	// 有 ask 即提问回合；否则任一收尾字段非空即收尾回合；都没有则非协议输出
	switch {
	case t.Question != "":
		return "ask", t
	case t.Branch != "" || t.Commit != "" || t.Summary != "":
		return "finish", t
	default:
		return "none", t
	}
}
