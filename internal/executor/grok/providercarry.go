// providercarry.go —— 从用户权威 grok 配置里搬运自定义 provider 定义。
//
// 职责：
//   - 从 ~/.grok/config.toml 的文本里抽出 [model.*] 段与 [models] 的 default
//   - 只做文本切段，不解析 TOML、不改写任何字段值
//
// 边界：
//   - 不写文件：结果交 WriteTaskEnv 织进任务级 config.toml
//   - 不判断字段含义、不识别密钥：段内字节原样搬运
//   - 不搬 [ui] / [permission] / [cli]：那三段永远以 handoff 为准，搬过来
//     等于让用户的个人配置覆盖任务级权限隔离
//
// 为什么不引入 TOML 库：本仓零 TOML 依赖，且 WriteTaskEnv 本身就是手写字符串
// 拼接。「解析 + 再序列化」会重排键、丢掉用户注释，生成的 config 不再一眼可读；
// 原样搬字节连注释都保得住。代价是自己认段边界，已知边界见 extractProviderConfig。
//
// 日志纪律：只打段名与条数，任何情况下不打段内容、不打字段值——[model.*] 段里
// 有 api_key。与 authsync.go 文件头「不打 token 值」同源。
package grok

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// carryResult 是从权威配置里抽出的可搬运部分。
type carryResult struct {
	// ModelSections 是 [model.*] 各段的原文（原样字节，含注释与段间空行）。
	// 无自定义 provider 时为空串。
	ModelSections string
	// SectionNames 是各段段名（如 "model.deepseek-v4-pro"），**仅供日志**。
	// 段名不是密钥，可以打；段内容不行。
	SectionNames []string
	// DefaultModel 是 [models] 段里 default 的值；权威配置没写时为空串。
	DefaultModel string
}

// extractProviderConfig 从 config.toml 的内容里抽出可搬运部分。
//
// 参数：
//   - content: 权威 config.toml 的全文
//
// 返回：抽取结果；没有任何 [model.*] 段与 default 时返回零值（非错误）。
//
// 注意：段边界靠「行首（允许前导空白）以 [ 开头」判定，不解析 TOML。
// **已知边界**：若某字段值是跨行数组、且续行顶格以 [ 开头，会被误判成段边界，
// 导致该 provider 段被截断。真实 provider 段的字段都是单行标量或内联表
// （extra_headers = { … }），不触发这条；用测试固化这个形态，不做更复杂的解析。
func extractProviderConfig(content string) carryResult {
	var (
		res      carryResult
		buf      strings.Builder
		inModel  bool
		inModels bool
	)
	for _, line := range strings.Split(content, "\n") {
		if name, ok := sectionHeader(line); ok {
			inModel = strings.HasPrefix(name, "model.")
			inModels = name == "models"
			if inModel {
				res.SectionNames = append(res.SectionNames, name)
			}
		}
		switch {
		case inModel:
			buf.WriteString(line)
			buf.WriteString("\n")
		case inModels:
			if v, ok := defaultValue(line); ok {
				res.DefaultModel = v
			}
		}
	}
	res.ModelSections = buf.String()
	return res
}

// sectionHeader 判断一行是不是段头，是则返回段名。
//
// 段名取 '[' 之后到第一个 ']' 之前的原文。数组表 [[x]] 会得到 "[x"——它不以
// "model." 开头，因此被当成普通段，**正确地终结**上一个 model 段而不被误收。
func sectionHeader(line string) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	end := strings.Index(t, "]")
	if end < 1 { // 只有 "[" 没有 "]"，或形如 "[]"，都不是有效段头
		return "", false
	}
	return t[1:end], true
}

// defaultValue 从 [models] 段的一行里取 default 的值。
//
// 返回：值与是否命中。带引号的值取到闭引号——这样 `default = "x"  # 注释`
// 也能正确解析；不带引号的截到第一个 '#'。
//
// 为什么要检查 '=' 紧跟在 "default" 之后：grok 的 [models] 段里还有
// default_reasoning_effort，只按前缀匹配会把它的值错当成默认模型名。
func defaultValue(line string) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, "default") {
		return "", false
	}
	rest := strings.TrimSpace(t[len("default"):])
	if !strings.HasPrefix(rest, "=") {
		return "", false
	}
	rest = strings.TrimSpace(rest[1:])
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : 1+end], true
		}
		return "", false // 引号没闭合，当没写
	}
	if i := strings.Index(rest, "#"); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}

// authorityConfigPath 返回权威配置路径 ~/.grok/config.toml。
//
// 单开一个函数而不是在调用点拼路径：与 authsync.go 的 authorityAuthPath 同样
// 的理由——两处各拼一遍，将来改动时漏掉一处就会读错文件。
func authorityConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录: %w", err)
	}
	return filepath.Join(home, ".grok", configFileName), nil
}

// loadAuthorityProviderConfig 读权威 config.toml 并抽出可搬运部分。
//
// 参数：
//   - log: 日志入口；nil 时退回 slog.Default()
//
// 返回：抽取结果。
//
// **本函数不返回 error**，这是刻意的：搬运是增强不是必需。权威配置不存在
// （用内建 provider 的人就是这样）、读不动、或压根没有自定义 provider，三种
// 情况都按「无操作」继续，失败原因经日志留痕。让一个可选文件拖垮派发是错的。
//
// 日志纪律：只打路径、段名与条数，绝不打段内容——[model.*] 段里有 api_key。
func loadAuthorityProviderConfig(log *slog.Logger) carryResult {
	if log == nil {
		log = slog.Default()
	}
	path, err := authorityConfigPath()
	if err != nil {
		log.Warn("解析权威 grok 配置路径失败，任务 home 不带自定义 provider", "cause", err)
		return carryResult{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debug("未发现权威 grok 配置，任务 home 不带自定义 provider", "path", path)
		} else {
			log.Warn("读权威 grok 配置失败，任务 home 不带自定义 provider", "path", path, "cause", err)
		}
		return carryResult{}
	}
	res := extractProviderConfig(string(b))
	if len(res.SectionNames) == 0 && res.DefaultModel == "" {
		log.Debug("权威 grok 配置无自定义 provider 与 default", "path", path)
	}
	return res
}
