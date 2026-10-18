// approver.go：权限请求的前置裁决——廉价模型 CLI 裁决。
//
// 职责：
//   - 在权限请求升级人工协调者之前，先做一级廉价分流：调用配置的廉价模型
//     执行者（opencode/claude 的 one-shot 模式）对权限请求做一次裁决，
//     approve 则自动放行
//   - fail-closed：裁决命令失败 / 输出解析失败 / 超时 / decision 取值非法
//     一律按 escalate（升级人工协调者），绝不静默放行
//
// 边界：
//   - 无 deny 权——裁决出口只有 approve（自动放行）与 escalate（升级人工），
//     拒绝权限不是审批者的职权，只有协调者（人）能拒绝
//   - 不写 store、不碰 adapter、不做状态迁移——纯裁决计算；落库与回传
//     （工单/应答/事件）由 manager 完成
//   - 裁决输出的 nonce 防伪：权限原文来自被监管的 executor，不可信
//
// 2026-08-09（B23/B27）黑名单与截断判定已迁往 internal/permgate，本文件
// 不再持有规则表；Approver 退化为只做「调模型 + 解析裁决」。
package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
)

// ApproverAttempt 是一次候选尝试的审计记录（B376）。
//
//   - Executor: 本候选执行者名
//   - Decision: approve / escalate / error（方案与 ApproverDecision 同义）
//   - Reason: 裁决理由或失败原因
//   - Err: 非 nil 表示该候选失败（命令失败/解析失败/未绑定/超时）
//   - ElapsedMS: 本候选耗时
//   - Model: 本候选实际使用的模型名；未指定时 = ApproverModelDefaultLabel
type ApproverAttempt struct {
	Executor  string
	Decision  string
	Reason    string
	Err       error
	ElapsedMS int64
	Model     string
}

// ApproverDecision 是审批者一次裁决的结果。
//
//   - Approve: true=自动放行（executor 收 once）；false=升级人工协调者
//   - Reason: 裁决理由（approve/escalate 时可能非空）
//   - ElapsedMS: 本次裁决耗时（含 CLI 调用）
//   - Err: 非 nil 表示裁决本身失败（命令失败/解析失败/超时/取值非法）——
//     区别于干净的 escalate（Err=nil）。上层据此做连续失败计数：只有 Err 非 nil
//     才累计，干净的 escalate 不算失败（那是审批者正常行使职权）
//   - Executor: 最终生效的候选名（failover 后停下的那个）
//   - Model: 最终生效候选实际使用的模型名；未指定时 = ApproverModelDefaultLabel
//   - Attempts: 本次请求内各候选的尝试记录，按尝试顺序
type ApproverDecision struct {
	Approve   bool
	Reason    string
	ElapsedMS int64
	Err       error
	Executor  string
	Model     string
	Attempts  []ApproverAttempt
}

// Approver 是审批链的廉价模型裁决器。
type Approver struct {
	log *slog.Logger
	// names 是有序候选执行者名（B376）。单候选时 names[0] 即历史 executorName；
	// 第一个是「默认名」，单测/日志兼容旧语义。
	names        []string
	executorName string // = names[0]，保留给日志与单候选兼容
	// model 是链级模型；空=执行者自身默认。候选无单独 models 条目（或条目值
	// 为空串）时的回落（B405）。
	model string
	// models 是候选名→模型名（B405）；空=无候选级覆盖。键由 config 校验保证
	// 必是 names 成员。
	models  map[string]string
	timeout time.Duration
	// env 是本审批者所用 agent 的 env 文件解析器；nil=不注入（未配置或测试场景）。
	// 审批者与任务执行者是同一个 agent 的两次启动，必须共用同一份 env——代理只配
	// 半边会让审批者连不出去后静默 fail-closed 升级，是最难查的那类故障。
	// failover 时每个候选各取自己的 env.For(名字)。
	env *envfile.Resolver
	// shots 是候选名 → 一次性调用能力（B376）。单候选路径绑定到 names[0]，
	// 兼容既有 BindOneShot 调用方。
	shots map[string]executor.OneShot
}

// ApproverModelDefaultLabel 是候选未解析出模型名时审计记录里的显式占位。
//
// 「未指定」与「指定了名为『默认』的模型」在审计记录里不做机器区分：模型名
// 今天无判据可校验（B203 OOS），占位符只服务人读的可观测性（B405 故事 4）。
const ApproverModelDefaultLabel = "默认"

// modelFor 解析某候选实际应使用的模型名（B405 取值优先级）：
// 候选 models 条目（非空）> 链级 model > 执行者自身默认（空串）。
//
// 注意「值空串与缺条目同义」：先回落链级 model，链级也空才用执行者默认，
// 不是跳过链级直接用默认（2026-09-23 批准意见）。
func (a *Approver) modelFor(name string) string {
	if m, ok := a.models[name]; ok && m != "" {
		return m
	}
	return a.model
}

// BindOneShot 绑定一次性调用能力实现（绑到首要候选 names[0]）。
func (a *Approver) BindOneShot(shot executor.OneShot) {
	if a.shots == nil {
		a.shots = map[string]executor.OneShot{}
	}
	if len(a.names) == 0 {
		a.shots[a.executorName] = shot
		return
	}
	a.shots[a.names[0]] = shot
}

// BindOneShotFor 把一次性调用能力绑到指定候选名（B376 failover）。
func (a *Approver) BindOneShotFor(name string, shot executor.OneShot) {
	if a.shots == nil {
		a.shots = map[string]executor.OneShot{}
	}
	a.shots[name] = shot
}

// NewApprover 构造审批者。
//
// 参数：
//   - cfg: 审批者配置；cfg.Executor 为空表示不启用审批链，返回 (nil, nil)。
//     非空时按序候选，第一个不可用/失败才试下一个
//   - env: 本 agent 的 env 文件解析器（B19）；nil=不注入
//   - log: 包日志入口
//
// 返回：
//   - 未启用时返回 (nil, nil)；启用时返回可用的审批者
func NewApprover(cfg config.ApproverConfig, env *envfile.Resolver, log *slog.Logger) (*Approver, error) {
	if cfg.Executor.Empty() {
		return nil, nil
	}
	names := append([]string(nil), cfg.Executor...)
	// 拷贝而不是直接引用 cfg.Models：NewApprover 的入参是值，但 map 是引用
	// 类型，调用方之后改自己的 map 不该影响已构造的 Approver。
	var models map[string]string
	if len(cfg.Models) > 0 {
		models = make(map[string]string, len(cfg.Models))
		for k, v := range cfg.Models {
			models[k] = v
		}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Approver{
		log:          log,
		names:        names,
		executorName: names[0],
		model:        cfg.Model,
		models:       models,
		timeout:      cfg.Timeout,
		env:          env,
	}, nil
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
	// 一个 nonce 服务整条请求：所有候选拿到同一个 prompt，防伪语义不变
	prompt := fmt.Sprintf(approverPromptTemplate, taskSummary, permission, nonce, nonce)
	a.log.Info("审批者开始裁决", "permission", turn.TruncateRunes(permission, 80),
		"executors", strings.Join(a.names, ","), "model", a.model, "models", a.models, "nonce", nonce)

	var attempts []ApproverAttempt
	var lastErr error
	for _, name := range a.names {
		attempt := a.decideWithCandidate(ctx, name, prompt, nonce, permission, taskSummary)
		attempts = append(attempts, attempt)
		if attempt.Err == nil {
			// 干净裁决（approve 或 escalate）：停，不再试下一个
			return ApproverDecision{
				Approve:   attempt.Decision == "approve",
				Reason:    attempt.Reason,
				ElapsedMS: time.Since(start).Milliseconds(),
				Executor:  name,
				Model:     attempt.Model,
				Attempts:  attempts,
			}
		}
		lastErr = attempt.Err
		a.log.Warn("审批者候选失败，尝试下一个", "executor", name,
			"cause", attempt.Err, "tried", len(attempts))
	}
	// 全部候选都失败：返回一个总 Err（请求级失败计 1，不按候选数累加）
	if lastErr == nil {
		lastErr = fmt.Errorf("审批者未绑定 OneShot")
	}
	final := ApproverDecision{
		Approve:   false,
		ElapsedMS: time.Since(start).Milliseconds(),
		Err:       fmt.Errorf("全部审批候选均失败（%s）: %w", strings.Join(a.names, ","), lastErr),
		Attempts:  attempts,
	}
	if len(attempts) > 0 {
		final.Executor = attempts[len(attempts)-1].Executor
		final.Model = attempts[len(attempts)-1].Model
	}
	a.log.Error("审批者全部候选失败", "executors", strings.Join(a.names, ","),
		"tried", len(attempts), "cause", final.Err)
	return final
}

// decideWithCandidate 对单个候选执行一次裁决尝试。
//
// 未绑定 OneShot / env 解析失败都算该候选失败（Decision=error, Err 非 nil），
// 由 Decide 决定是否继续 failover。每个候选各取自己的 env.For(name) 与
// modelFor(name)（B405）。
func (a *Approver) decideWithCandidate(ctx context.Context, name, prompt, nonce, permission, taskSummary string) ApproverAttempt {
	candStart := time.Now()
	// 候选级模型在本候选一开始就定下来：失败尝试（未绑定/env 失败/invoke 失败）
	// 也记同一个模型名，审计记录才回答得了「哪个候选带哪个模型跑的」。
	model := a.modelFor(name)
	modelLabel := model
	if modelLabel == "" {
		modelLabel = ApproverModelDefaultLabel
	}
	shot := a.shots[name]
	if shot == nil {
		err := fmt.Errorf("审批者候选 %q 未绑定 OneShot", name)
		a.log.Error("审批者候选缺少 OneShot", "executor", name)
		return ApproverAttempt{Executor: name, Decision: "error", Reason: err.Error(),
			Err: err, ElapsedMS: time.Since(candStart).Milliseconds(), Model: modelLabel}
	}
	var env []string
	if a.env != nil {
		extra, err := a.env.For(name)
		if err != nil {
			a.log.Error("审批者 env 文件解析失败，该候选按失败处理",
				"executor", name, "cause", err)
			return ApproverAttempt{Executor: name, Decision: "error", Reason: err.Error(),
				Err: fmt.Errorf("解析审批者 env 文件: %w", err), ElapsedMS: time.Since(candStart).Milliseconds(),
				Model: modelLabel}
		}
		if len(extra) > 0 {
			env = append(os.Environ(), extra...)
		}
	}
	req := executor.OneShotReq{Prompt: prompt, Model: model, Env: env, Limits: executor.OneShotLimits{}}
	if name == executor.HarnessGrok {
		req.Limits.Effort = executor.EffortLow
	}
	cctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	a.log.Info("审批者候选开始", "executor", name, "model", modelLabel)
	reply, err := shot.Invoke(cctx, req)
	elapsed := time.Since(candStart).Milliseconds()
	if err != nil {
		a.log.Error("审批者候选裁决失败", "executor", name,
			"permission", turn.TruncateRunes(permission, 80),
			"cause", err, "output", turn.TruncateRunes(reply.Text, 200), "elapsed_ms", elapsed)
		return ApproverAttempt{Executor: name, Decision: "error", Reason: err.Error(),
			Err: err, ElapsedMS: elapsed, Model: modelLabel}
	}
	d := parseDecision(reply.Text, nonce, elapsed)
	if d.Err != nil {
		if strings.Contains(d.Err.Error(), "nonce 不匹配") {
			a.log.Error("审批者裁决 nonce 校验失败，按候选失败处理", "executor", name,
				"task_summary", turn.TruncateRunes(taskSummary, 60),
				"permission", turn.TruncateRunes(permission, 80), "cause", d.Err)
		}
		return ApproverAttempt{Executor: name, Decision: "error", Reason: d.Err.Error(),
			Err: d.Err, ElapsedMS: elapsed, Model: modelLabel}
	}
	decision := "escalate"
	if d.Approve {
		decision = "approve"
	}
	a.log.Info("审批者候选裁决完成", "executor", name, "decision", decision,
		"reason", turn.TruncateRunes(d.Reason, 80), "elapsed_ms", elapsed)
	return ApproverAttempt{Executor: name, Decision: decision, Reason: d.Reason, ElapsedMS: elapsed, Model: modelLabel}
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
				Err: fmt.Errorf("审批者裁决 nonce 不匹配（期望 %s，实得 %q），疑似伪造裁决", nonce, turn.TruncateRunes(m.Nonce, 40))}
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
//
// 2026-08-18 增补自指令一行（B115）：这**不是**主判据——自指令由 permgate
// 的 selfcmd.go 判成 Escalate，根本走不到审批者这里。加它是兜底：万一判据
// 漏了某种形态落到 Consult，模型还有一次机会拦下。
const approverPromptTemplate = `你是代码任务的权限审批者。任务背景：%s
权限请求：%s
本次裁决编号 nonce=%s，你必须在输出的 JSON 里原样回显它，否则裁决作废。
仅当该操作明显安全（任务仓库内读写、跑测试/构建、装项目依赖、常规 git 提交）时才批准。
任何不确定、可能破坏数据、影响范围超出任务仓库的操作，必须升级给上级协调者。
涉及生产环境、部署动作、运维目标机（如 kubectl -n prod、ssh 到生产主机、terraform apply）的操作，一律升级给上级协调者。
涉及调用 handoff 自身 CLI 变更任务状态的操作（如 dispatch/continue/done/reply/run），一律升级给上级协调者。
只输出一行 JSON，不要输出其他内容：{"decision":"approve","nonce":"%s"} 或 {"decision":"escalate","reason":"简要原因","nonce":"<同一 nonce>"}`
