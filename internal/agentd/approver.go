// approver.go：权限请求的前置裁决——廉价模型 CLI 裁决。
//
// 职责：
//   - 在权限请求升级人工审核者之前，先做一级廉价分流：调用配置的廉价模型
//     执行者（opencode/claude 的 one-shot 模式）对权限请求做一次裁决，
//     approve 则自动放行
//   - fail-closed：裁决命令失败 / 输出解析失败 / 超时 / decision 取值非法
//     一律按 escalate（升级人工审核者），绝不静默放行
//
// 边界：
//   - 无 deny 权——裁决出口只有 approve（自动放行）与 escalate（升级人工），
//     拒绝权限不是审批者的职权，只有审核者（人）能拒绝
//   - 不写 store、不碰 adapter、不做状态迁移——纯裁决计算；落库与回传
//     （工单/应答/事件）由 manager 完成
//   - 裁决输出的 nonce 防伪：权限原文来自被监管的 executor，不可信
//
// 2026-08-09（B23/B27）黑名单与截断判定已迁往 internal/permgate，本文件
// 不再持有规则表；Approver 退化为只做「调模型 + 解析裁决」。
package agentd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/envfile"
	"github.com/xushixin/handoff/internal/executor"
)

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
	// env 是本审批者所用 agent 的 env 文件解析器；nil=不注入（未配置或测试场景）。
	// 审批者与任务执行者是同一个 agent 的两次启动，必须共用同一份 env——代理只配
	// 半边会让审批者连不出去后静默 fail-closed 升级，是最难查的那类故障
	env *envfile.Resolver
	// runCmd 是执行 one-shot 裁决命令的测试缝（非导出字段，测试直接改注入）。
	// 默认实现是 exec.CommandContext + CombinedOutput（输出上限 maxDecideOutput）。
	runCmd func(ctx context.Context, argv []string) (string, error)
}

// NewApprover 构造审批者。
//
// 参数：
//   - cfg: 审批者配置；cfg.Executor 为空表示不启用审批链，返回 (nil, nil)
//   - env: 本 agent 的 env 文件解析器（B19）；nil=不注入
//   - log: 包日志入口
//
// 返回：
//   - 未启用时返回 (nil, nil)；启用时返回可用的审批者
func NewApprover(cfg config.ApproverConfig, env *envfile.Resolver, log *slog.Logger) (*Approver, error) {
	if cfg.Executor == "" {
		return nil, nil
	}
	a := &Approver{
		log:          log,
		executorName: cfg.Executor,
		model:        cfg.Model,
		timeout:      cfg.Timeout,
		env:          env,
	}
	// runCmd 绑到方法而不是包级函数：默认实现需要读 a.env 才能注入环境变量，
	// 同时保持 runCmd 字段签名不变——既有 15 处测试注入点一行都不用改
	a.runCmd = a.defaultRunCmd
	return a, nil
}

// defaultRunCmd 是 runCmd 的默认实现：exec.CommandContext + CombinedOutput，
// 输出上限 maxDecideOutput（截断而非报错——解析只关心输出开头的 JSON 行）。
//
// 环境（B19）：继承 agentd 环境并**追加**本 agent 的 env 文件变量。
//   - 为什么是追加而不是替换：替换会让审批者连 PATH 都没有，executor 根本起不来
//   - 为什么解析失败直接返回错误而不是无环境硬跑：Decide 的既有失败分支会把它
//     变成 escalate（升级人工审核者）。让它带病裁决更危险——没有代理时模型请求
//     必然失败，而失败会被当成「审批者判不了」，与真正的判不了混为一谈
func (a *Approver) defaultRunCmd(ctx context.Context, argv []string) (string, error) {
	env := os.Environ()
	if a.env != nil {
		extra, err := a.env.For(a.executorName)
		if err != nil {
			a.log.Error("审批者 env 文件解析失败，本次裁决升级人工审核者",
				"executor", a.executorName, "cause", err)
			return "", fmt.Errorf("解析审批者 env 文件: %w", err)
		}
		env = append(env, extra...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
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

// randNonce 生成一次性裁决随机数（8 字节 → 16 位十六进制）。
//
// 为什么是随机而不是计数器/时间戳：nonce 的唯一作用是「executor 无法预知」——
// 可预测的值可以被提前构造进权限描述里，防伪就失效了。
// 随机源失败时返回空串，由调用方按「本次不带 nonce 校验」降级：
// 拿不到随机数不该让整条审批链瘫痪，而缺 nonce 的裁决仍受 §fail-closed 保护。
func randNonce() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
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
	nonce := randNonce()
	if nonce == "" {
		a.log.Warn("生成裁决 nonce 失败，本次裁决不做防伪校验", "executor", a.executorName)
	}
	a.log.Info("审批者开始裁决", "permission", truncateRunes(permission, 80),
		"executor", a.executorName, "model", a.model, "nonce", nonce)
	prompt := fmt.Sprintf(approverPromptTemplate, taskSummary, permission, nonce, nonce)
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
	d := parseDecision(out, nonce, elapsed)
	if d.Err != nil && strings.Contains(d.Err.Error(), "nonce 不匹配") {
		a.log.Error("审批者裁决 nonce 校验失败，按升级处理", "task_summary", truncateRunes(taskSummary, 60),
			"permission", truncateRunes(permission, 80), "cause", d.Err)
	}
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
//   - nonce 非空时裁决 JSON 必须回显同一 nonce，否则 Err 非 nil（防伪校验）
//   - 合法 escalate → 干净 escalate（Err=nil，Reason 取模型给的理由）
//   - 合法 approve → Approve=true
//
// 参数：
//   - out: 裁决命令输出
//   - nonce: 本次 prompt 的 nonce；空=不校验（随机源失败时的降级）
//   - elapsedMS: 本次裁决耗时
func parseDecision(out, nonce string, elapsedMS int64) ApproverDecision {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var m struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
			Nonce    string `json:"nonce"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue // 非 JSON 行（思考文本），继续向前找
		}
		// nonce 防伪：只有真正读到本次 prompt 的模型才回显得出这个值。
		// 不匹配即判无效 → fail-closed 升级人工，绝不当成一次干净的 escalate
		//（那会掩盖「有人在伪造裁决」这件事本身）
		if nonce != "" && m.Nonce != nonce {
			return ApproverDecision{Approve: false, ElapsedMS: elapsedMS,
				Err: fmt.Errorf("审批者裁决 nonce 不匹配（期望 %s，实得 %q），疑似伪造裁决", nonce, truncateRunes(m.Nonce, 40))}
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
// 改语义只改这里；四个 %s 分别填充任务摘要、权限原文、nonce、nonce（值出现
// 两次：一处是「回显它」的指令，一处是 approve 输出里的占位）。
//
// 2026-08-09 增补生产环境一行：内置黑名单原有 `\bproduction\b|\bprod\b` 一条，
// 实测 9 条误命中里 4 条出自它（`go test ./internal/prod/...` 这类），已删除；
// 该语义改由模型承担——正则分不出 `go test ./internal/prod/...` 与
// `kubectl -n prod delete deploy/api`，模型分得出。
const approverPromptTemplate = `你是代码任务的权限审批者。任务背景：%s
权限请求：%s
本次裁决编号 nonce=%s，你必须在输出的 JSON 里原样回显它，否则裁决作废。
仅当该操作明显安全（任务仓库内读写、跑测试/构建、装项目依赖、常规 git 提交）时才批准。
任何不确定、可能破坏数据、影响范围超出任务仓库的操作，必须升级给上级审核者。
涉及生产环境、部署动作、运维目标机（如 kubectl -n prod、ssh 到生产主机、terraform apply）的操作，一律升级给上级审核者。
只输出一行 JSON，不要输出其他内容：{"decision":"approve","nonce":"%s"} 或 {"decision":"escalate","reason":"简要原因","nonce":"<同一 nonce>"}`
