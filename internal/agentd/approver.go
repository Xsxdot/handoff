// approver.go：权限请求的前置裁决——黑名单硬规则 + 廉价模型 CLI 裁决。
//
// 职责：
//   - 在权限请求升级人工审核者之前，先做两级廉价分流：
//     1. 内置 + 自定义黑名单硬规则：命中即直接升级（无需浪费一次模型调用）
//     2. 黑名单未命中时，调用配置的廉价模型执行者（opencode/claude 的
//     one-shot 模式）对权限请求做一次裁决，approve 则自动放行
//   - fail-closed：裁决命令失败 / 输出解析失败 / 超时 / decision 取值非法
//     一律按 escalate（升级人工审核者），绝不静默放行
//
// 边界：
//   - 无 deny 权——裁决出口只有 approve（自动放行）与 escalate（升级人工），
//     拒绝权限不是审批者的职权，只有审核者（人）能拒绝
//   - 不写 store、不碰 adapter、不做状态迁移——纯裁决计算；落库与回传
//     （工单/应答/事件）由 manager 完成
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
)

// builtinBlacklist 是内置黑名单（(?i) 不区分大小写编译），全部命中即升级人工审核者。
//
// 拦截意图（逐条）：
//   - rm -rf 类（连写/分写）：不可逆的递归删除，误删任务外目录代价极高
//   - git push --force / -f / --force-with-lease：强推会重写远端历史，
//     影响范围远超任务仓库，审核者应知道是谁在推什么
//   - sudo：提权命令影响整个主机，绝不能由廉价模型自动放行
//   - git reset --hard：丢弃未提交改动，不可逆
//   - drop table/database：删库删表不可恢复
//   - production/prod：生产环境操作，默认升级
var builtinBlacklist = []string{
	`rm\s+-[a-z]*[rf][a-z]*[rf]`,      // rm -rf / -fr / -r -f 的常见连写
	`rm\s+-[rf]\b.*\s-[rf]\b`,         // rm -r ... -f 分写
	`git\s+push\s+.*(--force\b|-f\b)`, // force push
	`\bsudo\b`,
	`git\s+reset\s+--hard`,
	`drop\s+(table|database)`,
	`\bproduction\b|\bprod\b`,
	`git\s+push\s+.*--force-with-lease`, // lease 变体同样升级
}

// maxDecideOutput 是裁决命令输出保留上限：模型在 JSON 后可能输出废话，
// 只保留开头 64KiB 足够解析，防止失控输出驻留内存。
const maxDecideOutput = 64 << 10

// ApproverDecision 是审批者一次裁决的结果。
//
//   - Approve: true=自动放行（executor 收 once）；false=升级人工审核者
//   - Reason: 裁决理由（approve/escalate 时可能非空）
//   - ElapsedMS: 本次裁决耗时（含 CLI 调用）
//   - Err: 非 nil 表示裁决本身失败（命令失败/解析失败/超时/取值非法）——
//     区别于干净的 escalate（Err=nil）。上层据此做连续失败计数：只有 Err 非 nil
//     才累计，干净的 escalate 不算失败（那是审批者正常行使职权）
type ApproverDecision struct {
	Approve   bool
	Reason    string
	ElapsedMS int64
	Err       error
}

// Approver 是审批链的廉价模型裁决器。
type Approver struct {
	log          *slog.Logger
	executorName string // one-shot 执行者名（如 opencode/claude）
	model        string // 审批者模型；空=执行者自身默认
	timeout      time.Duration
	blacklist    []*regexp.Regexp // 内置 + 自定义，编译后
	// runCmd 是执行 one-shot 裁决命令的测试缝（非导出字段，测试直接改注入）。
	// 默认实现是 exec.CommandContext + CombinedOutput（输出上限 maxDecideOutput）。
	runCmd func(ctx context.Context, argv []string) (string, error)
}

// NewApprover 构造审批者。
//
// 参数：
//   - cfg: 审批者配置；cfg.Executor 为空表示不启用审批链，返回 (nil, nil)
//   - log: 包日志入口
//
// 返回：
//   - 未启用时返回 (nil, nil)；启用时返回可用的审批者
//   - 黑名单正则编译失败返回错误（配置错误，启动期即应暴露）
func NewApprover(cfg config.ApproverConfig, log *slog.Logger) (*Approver, error) {
	if cfg.Executor == "" {
		return nil, nil
	}
	patterns := append([]string(nil), builtinBlacklist...)
	patterns = append(patterns, cfg.Blacklist...)
	rx := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		r, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, fmt.Errorf("编译黑名单正则 %q: %w", p, err)
		}
		rx = append(rx, r)
	}
	return &Approver{
		log:          log,
		executorName: cfg.Executor,
		model:        cfg.Model,
		timeout:      cfg.Timeout,
		blacklist:    rx,
		runCmd:       defaultRunCmd,
	}, nil
}

// defaultRunCmd 是 runCmd 的默认实现：exec.CommandContext + CombinedOutput，
// 输出上限 maxDecideOutput（截断而非报错——解析只关心输出开头的 JSON 行）。
func defaultRunCmd(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	b := out.Bytes()
	if len(b) > maxDecideOutput {
		b = b[:maxDecideOutput]
	}
	return string(b), err
}

// Blacklisted 判断权限描述是否命中黑名单（内置 + 自定义）。
//
// 返回：
//   - hit: 是否命中
//   - rule: 命中的规则原文（仅 hit=true 时有效，用于日志/审计）
func (a *Approver) Blacklisted(permission string) (hit bool, rule string) {
	for _, r := range a.blacklist {
		if r.MatchString(permission) {
			a.log.Info("权限命中黑名单，跳过审批者直接升级",
				"rule", r.String(), "permission", truncateRunes(permission, 80))
			return true, r.String()
		}
	}
	return false, ""
}

// Decide 对一次权限请求做裁决：组装 prompt → 调用 one-shot 执行者 → 解析 JSON。
//
// 参数：
//   - ctx: 上层上下文；本方法在其上叠加 a.timeout 作为裁决截止
//   - permission: 权限请求原文（如 "Bash: rm -rf node_modules"）
//   - taskSummary: 任务摘要（写入 prompt 给模型上下文）
//
// 返回：
//   - ApproverDecision，见该类型注释（fail-closed：一切无法干净裁决的输入
//     都返回 Approve=false + Err 非 nil）
func (a *Approver) Decide(ctx context.Context, permission, taskSummary string) ApproverDecision {
	start := time.Now()
	a.log.Info("审批者开始裁决", "permission", truncateRunes(permission, 80),
		"executor", a.executorName, "model", a.model)
	prompt := fmt.Sprintf(approverPromptTemplate, taskSummary, permission)
	argv, err := executor.OneShotArgs(a.executorName, a.model, prompt)
	if err != nil {
		d := ApproverDecision{Approve: false, ElapsedMS: time.Since(start).Milliseconds(), Err: err}
		a.log.Error("审批者裁决命令构造失败", "permission", truncateRunes(permission, 80), "cause", err)
		return d
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	out, err := a.runCmd(ctx, argv)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		// fail-closed：命令失败/超时/不存在一律按 escalate，且 Err 非 nil
		// 供上层计数禁用——这既是「审批者为什么没批」的第一现场，也是
		// 「连续失败多少次就该停用」的判据
		d := ApproverDecision{Approve: false, ElapsedMS: elapsed, Err: err}
		a.log.Error("审批者裁决失败", "permission", truncateRunes(permission, 80),
			"cause", err, "output", truncateRunes(out, 200), "elapsed_ms", elapsed)
		return d
	}
	d := parseDecision(out, elapsed)
	a.log.Info("审批者裁决完成", "decision", decisionLabel(d), "reason", truncateRunes(d.Reason, 80),
		"elapsed_ms", elapsed)
	return d
}

// decisionLabel 把裁决结果压成一个日志可读的短标签。
func decisionLabel(d ApproverDecision) string {
	switch {
	case d.Approve:
		return "approve"
	case d.Err != nil:
		return "error"
	default:
		return "escalate"
	}
}

// parseDecision 从裁决命令输出里解析 decision。
//
// 为什么由后向前逐行找 JSON：廉价模型常在 JSON 输出**前**输出思考文本
// （reasoning/解释），真正的 JSON 是输出的最后几行；由后向前找、逐行尝试
// 反序列化，首个合法 JSON 行生效——若从前往后找，第一行思考文本里的花括号
// 片段极易被误解析。
//
// 解析规则（fail-closed）：
//   - 找不到合法 JSON / decision 不是 approve|escalate → Err 非 nil
//   - 合法 escalate → 干净 escalate（Err=nil，Reason 取模型给的理由）
//   - 合法 approve → Approve=true
func parseDecision(out string, elapsedMS int64) ApproverDecision {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var m struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue // 非 JSON 行（思考文本），继续向前找
		}
		switch m.Decision {
		case "approve":
			return ApproverDecision{Approve: true, Reason: m.Reason, ElapsedMS: elapsedMS}
		case "escalate":
			return ApproverDecision{Approve: false, Reason: m.Reason, ElapsedMS: elapsedMS}
		default:
			return ApproverDecision{Approve: false, ElapsedMS: elapsedMS,
				Err: fmt.Errorf("审批者 decision 取值非法 %q（仅接受 approve/escalate）", m.Decision)}
		}
	}
	return ApproverDecision{Approve: false, ElapsedMS: elapsedMS, Err: errors.New("裁决输出不含可解析的 JSON decision")}
}

// approverPromptTemplate 是裁决 prompt 的固定模板。
//
// 为什么写成模板字符串：裁决语义（什么能批、什么必须升级）集中在唯一一处，
// 改语义只改这里；两个 %s 分别填充任务摘要与权限原文。
const approverPromptTemplate = `你是代码任务的权限审批者。任务背景：%s
权限请求：%s
仅当该操作明显安全（任务仓库内读写、跑测试/构建、装项目依赖、常规 git 提交）时才批准。
任何不确定、可能破坏数据、影响范围超出任务仓库的操作，必须升级给上级审核者。
只输出一行 JSON，不要输出其他内容：{"decision":"approve"} 或 {"decision":"escalate","reason":"简要原因"}`
