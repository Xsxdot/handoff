// taskenv.go —— grok 任务环境物料生成：任务级 GROK_HOME 与权限配置。
//
// 职责：
//   - WriteTaskEnv：建 <taskDir>/grokhome 并写 config.toml（钉死 permission_mode
//     与第 0 层分级规则、注入任务级模型）
//   - EnsureAuthLink：普通派发幂等地把 grokhome/auth.json 指向真实 ~/.grok/auth.json；
//     StartServe 的载体派发路径可把权威副本切到载体 HOME 下的 .grok/auth.json
//     （serve 启动脚本与 secret 注入已随 tmux 拆除，改由 proc.go 的 Spec.Env 承担）
//
// 边界：
//   - 不起进程、不连网络：进程在 proc.go，协议在 acp.go
//   - 不读用户的真实 grok 配置（除 auth.json 软链外一律纯净）
//
// 为什么任务级 GROK_HOME 是必需而非可选：用户真实 ~/.grok/config.toml 常见
// permission_mode = "always-approve"，直接沿用等于所有工具调用自动放行、
// permission 事件永不产生——审批门全废。任务级 home 把它钉死为 "default"。
//
// 为什么权限规则表比 opencode 短：grok 内建按 && / || / ; / 管道分段识别只读
// 命令并自动放行（ls/cat/git status/grep/rg 等），且 `ls && rm -rf /` 会拆开、
// rm 段仍然拦。opencode 那张以 "*": "allow" 收尾的表是手工补的等价物，这里
// 只需补 ask 危险模式与 allow 编辑放行。
//
// 已知泄漏（关不掉）：grok 无视 GROK_HOME，仍从真实 HOME 读 ~/.claude/settings*.json
// 与 ~/.claude/skills。缓解是 grok 的求值为 deny > ask > allow 跨源生效——本文件
// 写的 ask 压得过用户个人 allowlist 的 allow，第 0 层分级仍成立。
package grok

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	homeDirName    = "grokhome"
	configFileName = "config.toml"
	serveLogName   = "serve.log"
	renderLogName  = "render.log"
	serveInfoName  = "serve.json" // 保留旧文件名常量以兼容既有测试读法；实际落盘已改 proc.json
)

// askRules 是第 0 层静态分级的危险模式表。
//
// 每次修改本表必须同步 taskenv_test 的逐条断言——少一条就是静默放行。
var askRules = []string{
	"Write(*)",                 // 写文件：路径是否越出任务范围由 handoff 的 permgate 判（B27）
	"Edit(*)",                  // 同上
	"Bash(rm *)",               // 任何直接 rm（误拒成本低、误放成本高）
	"Bash(*sudo*)",             // 提权
	"Bash(*git push*)",         // 外推：收尾纪律要求不 push，出现即异常
	"Bash(*git reset --hard*)", // 丢弃提交
	"Bash(*--force*)",          // 各类强制开关
	"Bash(curl *)",             // 外访直调
	"Bash(wget *)",             // 外访直调
	"WebFetch(*)",              // 外访
}

// allowRules 2026-08-09 起为空：Edit/Write 已移入 askRules，由 handoff 判
// 目标路径是否落在任务范围内，范围内的写入在 manager 侧自动放行、不建工单。
// 留在 allow 里等于「写 ~/.ssh/authorized_keys 连事件都不留」（B27）。
//
// 探针实测依据（Task 1 结论文档 §2）：allowRules 置空期间 grok 任务正常跑完
// 并逐次产出权限事件，未出现「默认全 ask」的连环唤醒——grok 内建对只读命令
// （ls/cat/git status/grep/rg 等）自动放行，无需手工补白名单。
var allowRules = []string{}

// WriteTaskEnv 建任务级 GROK_HOME 并写入权限配置，返回该 home 目录路径。
//
// 参数：
//   - taskDir: 任务工作目录（须已存在，由调用方保证）
//   - model: 任务级模型；空则退回权威配置的 default，两者都空时不写 default
//     （此时 [models] 段仍可能因搬来的辅助旋钮而存在）
//
// 返回：grokhome 目录路径；建目录或写文件失败时返回错误
//
// 注意：重复调用幂等覆盖，调用方可安全重试
func WriteTaskEnv(taskDir, model string) (homeDir string, err error) {
	log := slog.Default()
	homeDir = filepath.Join(taskDir, homeDirName)
	log.Info("grok 生成任务环境", "task_dir", taskDir, "home", homeDir)
	// 搬运结果与 default 来源在 defer 的日志里要用，先声明再赋值
	var (
		carried     carryResult
		defaultFrom = "none"
	)
	defer func() {
		if err != nil {
			log.Error("grok 生成任务环境失败", "home", homeDir, "cause", err)
		} else {
			log.Info("grok 任务环境已生成", "home", homeDir, "model", model,
				"provider_sections", len(carried.SectionNames),
				"provider_names", carried.SectionNames,
				"default_from", defaultFrom,
				"models_extra_keys", carried.ModelsExtraKeys)
		}
	}()

	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return homeDir, fmt.Errorf("建 grok home %s: %w", homeDir, err)
	}

	// 权威配置里的自定义 provider 定义（B135）。不搬的话，配了自定义 provider
	// 的机器上 grok 不认识任务级 default 里的模型名，会回落内建 x.ai provider
	// 并报 Authentication required——报文指向「没登录」，根因其实是 provider 缺失。
	carried = loadAuthorityProviderConfig(log)

	// default 的取值优先级：--model 传入值 > 权威 config 的 default > 不写。
	// 三选一而不是各写一段：TOML 不允许同名表定义两次，[models] 只能出现一次。
	defaultModel := strings.TrimSpace(model)
	switch {
	case defaultModel != "":
		defaultFrom = "flag"
	case carried.DefaultModel != "":
		defaultModel, defaultFrom = carried.DefaultModel, "authority"
	}

	var b strings.Builder
	b.WriteString("# 由 handoff agentd 生成的任务级 grok 配置，勿手工编辑。\n\n")
	b.WriteString("[ui]\n")
	b.WriteString("permission_mode = \"default\"\n\n")
	// [models] 段：default 由上面的优先级决定，其余旋钮（web_search /
	// session_summary / image_description 等）从权威配置原样搬来。
	// 两者合成一段而不是各写一段——TOML 不允许同名表定义两次。
	if defaultModel != "" || carried.ModelsExtra != "" {
		b.WriteString("[models]\n")
		if defaultModel != "" {
			fmt.Fprintf(&b, "default = %q\n", defaultModel)
		}
		b.WriteString(carried.ModelsExtra) // 已归一化：每行带 \n、无尾随空行
		b.WriteString("\n")
	}
	b.WriteString("[permission]\n")
	b.WriteString("ask = [\n")
	for _, r := range askRules {
		fmt.Fprintf(&b, "  %q,\n", r)
	}
	b.WriteString("]\n")
	b.WriteString("allow = [\n")
	for _, r := range allowRules {
		fmt.Fprintf(&b, "  %q,\n", r)
	}
	b.WriteString("]\n")
	// [cli] auto_update = false 是硬要求，缺它 grok 会补默认值并可能无限期挂起。
	//
	// 三层原因：
	//  1. 这个键不是我们写不写的问题：grok 首次启动会把 config.toml 整个重写、
	//     补进全套默认值（含 [cli] auto_update = true），连我们的注释都抹掉。
	//     不写这一段，就等于接受默认 true。
	//  2. 默认 true 在**全新任务级 GROK_HOME** 下会让 `grok agent serve` 启动时
	//     联网查更新并无限期阻塞——表现为 serve.log 只有版本横幅、进程健在、
	//     端口不监听、15s 探活超时（2026-08-09 真机实测，见
	//     docs/superpowers/specs/2026-08-08-handoff-grok-adapter-design.md）。
	//     真实 ~/.grok 有 version.json 等缓存所以从不触发。
	//  3. 即便不挂起也不该开：任务执行到一半把 CLI 自更新掉，等于在任务中途
	//     换掉执行器版本——后台静默变基，协调者的结论可能因此失准。
	//
	// 实测：任务级 home 里我们自己写 [cli] auto_update = false 后，grok 不再重写
	// config（注释保住），serve 正常监听。
	b.WriteString("\n[cli]\n")
	b.WriteString("auto_update = false\n")

	// provider 定义追加在末尾：handoff 自己写的四段保持连续，便于逐字节比对与
	// 人工核对。TOML 段顺序无语义，放末尾不影响 grok 读取。
	if carried.ModelSections != "" {
		b.WriteString("\n# 以下 provider 定义由 handoff 从 ~/.grok/config.toml 原样透传，勿手工编辑。\n\n")
		b.WriteString(carried.ModelSections)
	}

	cfgPath := filepath.Join(homeDir, configFileName)
	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		return homeDir, fmt.Errorf("写 %s: %w", cfgPath, err)
	}
	return homeDir, nil
}

// EnsureAuthLink 幂等地把 <homeDir>/auth.json 指向真实 ~/.grok/auth.json。
//
// 为什么必须可修复而非一次性建立：spike 实测任务级 home 的软链会在 token 刷新
// 前后消失，随后 session/new 直接返回 Authentication required。Start 与 Resume
// 都调本函数，成本为零。
//
// 为什么用软链而非拷贝：拷贝会让每个任务 home 各自持有凭据并独立刷新，而刷新
// 令牌轮换可能反噬用户本人的登录态——凭据只应有一个权威副本。
func EnsureAuthLink(homeDir string) error {
	return ensureAuthLinkAt(homeDir, "")
}
