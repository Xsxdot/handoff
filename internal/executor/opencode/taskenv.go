// taskenv.go —— opencode 任务环境物料生成。
//
// 职责：
//   - WriteTaskEnv：在任务目录生成 opencode.json（权限收敛配置）与 prompt.md
//     （回合制纪律 prompt，经 turn.RenderPrompt 渲染），供 serve 进程经
//     OPENCODE_CONFIG 注入（proc.go）与任务首回合 prompt 使用
//
// 边界：
//   - 不启动进程、不发请求：serve 进程生命周期在 proc.go，HTTP 会话在 api.go
//   - 回合协议（prompt 模板渲染 / trailer 解析 / git 取证 / 文本截断）在
//     internal/executor/turn 共享包，本文件不再持有
//
// 为什么 permission 是「静态分级」而非全 ask（2026-08-08 dogfooding 修正）：
// 一期曾把 edit/bash 全部设为 ask，真实派发时协调者被 ls/grep/编辑测试文件
// 这类初级请求连环唤醒，审批噪音让审核流形同虚设——这恰是用户交互式用
// opencode 时不存在的问题（用户在场且全局配置宽松）。修正后的分层：
//   - edit: allow —— 在任务分支上改代码是派发的目的本身，diff 审核兜底；
//     edit 保持 allow（2026-08-09 真机探针复核）：越界写入由
//     external_directory: "ask" 拦截并升级人工，范围内写入本就该直接放行。
//     翻成 ask 等于给每次正常编辑加一道判完还是放行的空门（B27 复核结论，
//     见 docs/superpowers/plans/2026-08-09-permission-payload-probe.md §3.1）。
//   - bash: 模式表 —— 危险模式（rm -rf/sudo/git push/reset --hard/--force/
//     curl/wget 等，见 bashPermissionRules）ask，其余 allow；
//   - webfetch/external_directory: ask —— 外访与越出工作区仍逐次确认。
//
// 这是三级审批链的第 0 层（静态规则）；第 1 层（廉价模型审批者）见二期 spec，
// 第 2 层是协调者/用户本人。
package opencode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/turn"
)

// 文件名常量：任务目录内生成的物料文件名，路径由 WriteTaskEnv 拼接。
const (
	configFileName = "opencode.json"
	promptFileName = "prompt.md"
)

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
	// 重定向到绝对路径或家目录：opencode 自己不检出重定向落点（2026-08-18 真机
	// 探针，spec §2.2.1——同一个路径，作为参数出现要授权、作为重定向落点不要），
	// 不在这里把它们捞进来，permgate 的落点判据根本没机会跑。
	// 四条而不是一条 "*>*"：后者会命中 2>&1，`go test ./... 2>&1 | tail` 是高频
	// 写法，每条都送 Consult，在没配审批者的部署上等于升级人工。
	"*>/*":  "ask", // >/abs、>>/abs 都含子串 ">/"
	"*> /*": "ask", // > /abs、>> /abs 都含子串 "> /"
	"*>~*":  "ask", // >~/x
	"*> ~*": "ask", // > ~/x
	"*":     "allow",
}

// opencodeConfig 是 opencode.json 的完整结构，经结构体 marshal 生成
// （而非字符串拼接），避免引号/转义错误。
//
// Model 可选：经 OPENCODE_CONFIG 注入时会覆盖用户全局配置，因此默认不写死模型。
// 三级优先级（见 WriteTaskEnv）：任务级 model > 环境变量 HANDOFF_OPENCODE_MODEL
// > 不写（executor 自身默认，omitempty）。
type opencodeConfig struct {
	Model      string           `json:"model,omitempty"`
	Permission permissionConfig `json:"permission"`
}

// WriteTaskEnv 在 taskDir 生成 opencode 配置与任务 prompt，返回二者路径。
//
// 参数：
//   - taskDir: 任务工作目录（须已存在，由调用方保证）
//   - taskID: 任务 ID，写入 prompt 标题行
//   - model: 任务级模型覆盖（dispatch --model 折算而来）；空则回退环境变量
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
func WriteTaskEnv(taskDir, taskID, model, planContent string) (configPath, promptPath string, err error) {
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

	// 任务级 OPENCODE_CONFIG 会替换全局配置，不写 model 时只能依赖 opencode
	// 默认。model 三级优先级：任务级 > HANDOFF_OPENCODE_MODEL 环境变量 > 不写。
	// 为什么任务级优先：dispatch --model 在派发期已折算进 task.Model（含配置
	// executor.model 兜底），是「这个任务明确要用什么模型」的唯一权威；env 只是
	// 机器级兜底（远程免费模型 e2e 等场景），任务有显式模型时不应被覆盖。
	model = strings.TrimSpace(model)
	source := "task"
	if model == "" {
		model = strings.TrimSpace(os.Getenv("HANDOFF_OPENCODE_MODEL"))
		if model != "" {
			source = "env"
		}
	}
	cfg := opencodeConfig{
		Model: model,
		Permission: permissionConfig{
			Edit:              "allow",
			Bash:              bashPermissionRules,
			Webfetch:          "ask",
			ExternalDirectory: "ask",
		},
	}
	if cfg.Model != "" {
		logger.Info("任务环境注入模型", "model", cfg.Model, "source", source)
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		// MarshalIndent 对本结构体不可能失败，保留错误返回以防未来字段变更引入
		return configPath, promptPath, fmt.Errorf("序列化 opencode 配置: %w", err)
	}

	promptContent, err := turn.RenderPrompt(taskID, planContent)
	if err != nil {
		return configPath, promptPath, err
	}

	if err := os.WriteFile(configPath, cfgJSON, 0o644); err != nil {
		return configPath, promptPath, fmt.Errorf("写 %s: %w", configPath, err)
	}
	if err := os.WriteFile(promptPath, []byte(promptContent), 0o644); err != nil {
		return configPath, promptPath, fmt.Errorf("写 %s: %w", promptPath, err)
	}
	return configPath, promptPath, nil
}
