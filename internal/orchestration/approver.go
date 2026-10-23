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
	"sync"
	"sync/atomic"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
)

type approverRequestIDKey struct{}

var approverRequestSequence atomic.Uint64

// ApproverAttempt 是一次候选尝试的审计记录（B376）。
//
//   - Executor: 本候选执行者名
//   - Decision: approve / escalate / error（方案与 ApproverDecision 同义）
//   - Reason: 裁决理由或失败原因
//   - Err: 非 nil 表示该候选失败（命令失败/解析失败/未绑定/超时）
//   - ElapsedMS: 本候选耗时
//   - Model: 本候选实际使用的模型名；未指定时 = ApproverModelDefaultLabel
//   - Machine: 本尝试所属环的机器名（B437 共享链）；空 = 本地配置链路径
type ApproverAttempt struct {
	Executor  string
	Decision  string
	Reason    string
	Err       error
	ErrorKind string
	ElapsedMS int64
	Model     string
	Machine   string
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

	// ---- B437 共享审批链（全部经 setter 在装配期注入，零值时 S5 路径逐字不变）----
	//
	// chainMu 保护 chainReader/remoteDecider/lastGood 的读写：装配期注入一次、
	// 运行期只读；lastGood 由裁决 goroutine 与 Available() 并发更新。
	chainMu       sync.Mutex
	chainReader   ChainReader
	remoteDecider RemoteDecider
	// localShot 是共享链本机环的 OneShot 解析缝（B437 §4.9）：shots 未命中时用它。
	localShot func(name string) (executor.OneShot, error)
	// selfMachine 判断环的机器名是否本机（B439 唯一自名判据，组装点注入）。
	selfMachine func(name string) bool
	// lastGood 是最近一次成功读到的共享链；读失败时沿用，ErrApproverChainAbsent 清空。
	lastGood *ApproverChain
	// warnMu/warnAt 实现「同类告警每分钟至多一条」的限频（本次补充 P2 约束）。
	warnMu sync.Mutex
	warnAt map[string]time.Time
}

// SetChainReader 注入共享链读取口（nil = 无共享链，恒走本地配置链）。
func (a *Approver) SetChainReader(r ChainReader) {
	a.chainMu.Lock()
	defer a.chainMu.Unlock()
	a.chainReader = r
}

// SetRemoteDecider 注入他机环的远程裁决口（nil = 他机环一律失败）。
func (a *Approver) SetRemoteDecider(d RemoteDecider) {
	a.chainMu.Lock()
	defer a.chainMu.Unlock()
	a.remoteDecider = d
}

// SetLocalShot 注入共享链本机环的 OneShot 解析缝（shots 未命中时用它）。
func (a *Approver) SetLocalShot(fn func(name string) (executor.OneShot, error)) {
	a.chainMu.Lock()
	defer a.chainMu.Unlock()
	a.localShot = fn
}

// SetSelfMachine 注入「环的机器名是否本机」判据（B439 唯一自名判据）。
func (a *Approver) SetSelfMachine(fn func(name string) bool) {
	a.chainMu.Lock()
	defer a.chainMu.Unlock()
	a.selfMachine = fn
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
	return newApprover(cfg, env, log), nil
}

// NewApproverForShared 为「可能收到共享审批链」的机器构造审批者：即使本地
// approver.executor 为空也返回非 nil 的 Approver（B437 §4.2 启用门缺口）。
//
// 为什么需要：DSN+身份的机器可能一条本地链都没配，全靠共享链生效。若沿用
// NewApprover 的「空配置返 nil」，门 shouldConsultApprover 会在 m.approver==nil
// 时恒 false，S1「配一次、其它机器什么都不配」直接落空。空配置返 nil 的既有
// 语义保留（S5 回归钉），共享机构造走本函数。链读取口等由组装点 setter 后补注入。
func NewApproverForShared(cfg config.ApproverConfig, env *envfile.Resolver, log *slog.Logger) (*Approver, error) {
	return newApprover(cfg, env, log), nil
}

// newApprover 是 NewApprover / NewApproverForShared 的共同构造体。
func newApprover(cfg config.ApproverConfig, env *envfile.Resolver, log *slog.Logger) *Approver {
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
	executorName := ""
	if len(names) > 0 {
		executorName = names[0]
	}
	return &Approver{
		log:          log,
		names:        names,
		executorName: executorName,
		model:        cfg.Model,
		models:       models,
		timeout:      cfg.Timeout,
		env:          env,
	}
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
//   - tc: 任务上下文（B460 S7：形态与三处任务目录）；零值 = 无上下文，提示词同基线
//
// 返回：
//   - ApproverDecision，见该类型注释（fail-closed：一切无法干净裁决的输入
//     都返回 Approve=false + Err 非 nil）
//
// B437：先尝试读共享审批链；读到（或读到过 last-good）则按链逐环裁决，否则
// 走构造期本地配置链（S5 路径逐字不变）。
func (a *Approver) Decide(ctx context.Context, permission, taskSummary string, tc ApproverTaskContext) ApproverDecision {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = withApproverRequestID(ctx)
	if chain, ok := a.effectiveChain(ctx); ok {
		return a.decideSharedChain(ctx, chain, permission, taskSummary, tc)
	}
	return a.decideLocalChain(ctx, permission, taskSummary, tc)
}

func withApproverRequestID(ctx context.Context) context.Context {
	if id, _ := ctx.Value(approverRequestIDKey{}).(string); id != "" {
		return ctx
	}
	id := fmt.Sprintf("ap-%d-%d", time.Now().UnixNano(), approverRequestSequence.Add(1))
	return context.WithValue(ctx, approverRequestIDKey{}, id)
}

func approverRequestID(ctx context.Context) string {
	id, _ := ctx.Value(approverRequestIDKey{}).(string)
	return id
}

// Available 报告审批者此刻是否有可用的链（B437 §4.2 启用门判据）。
//
// 本地配置链非空（今天语义）→ true，不读账本；否则若有共享链读取口，读一次
// （带 ApproverChainReadTimeout 短时限）判断共享链是否存在。DSN 机器无任何链
// → false，与今天「直接升级人工」逐字一致（零新事件、零计数）。
func (a *Approver) Available() bool {
	if a == nil {
		return false
	}
	if len(a.names) > 0 {
		return true
	}
	a.chainMu.Lock()
	hasReader := a.chainReader != nil
	a.chainMu.Unlock()
	if !hasReader {
		return false
	}
	_, ok := a.effectiveChain(context.Background())
	return ok
}

// effectiveChain 解析本次裁决实际应使用的共享链。
//
// 读优先级（P2 + 本次补充）：
//   - 读取口 nil → (zero,false)，走本地链（S5）
//   - 读成功 → 更新 last-good，返回链
//   - ErrApproverChainAbsent（clear 的数据结论）→ 清 last-good，返回 false 回落本地链
//   - 其余错误（含超时/解码失败）→ 有 last-good 用它，否则回落本地链；各打一次限频 WARN
//
// 读封顶 ApproverChainReadTimeout：账本读在独立 goroutine 里跑，主协程 select
// 到点即返回——底层驱动若不尊重 ctx，也不会把裁决拖到裁决时限。
func (a *Approver) effectiveChain(ctx context.Context) (ApproverChain, bool) {
	a.chainMu.Lock()
	reader := a.chainReader
	a.chainMu.Unlock()
	if reader == nil {
		return ApproverChain{}, false
	}
	chain, err := a.readChain(ctx, reader)
	switch {
	case err == nil:
		if len(chain.Rings) == 0 {
			return ApproverChain{}, false
		}
		cp := chain
		a.chainMu.Lock()
		a.lastGood = &cp
		a.chainMu.Unlock()
		return chain, true
	case errors.Is(err, ErrApproverChainAbsent):
		a.chainMu.Lock()
		a.lastGood = nil
		a.chainMu.Unlock()
		a.warnLimited("chain_absent", "共享审批链已被清除，回落本地审批链")
		return ApproverChain{}, false
	default:
		a.chainMu.Lock()
		lg := a.lastGood
		a.chainMu.Unlock()
		if lg != nil {
			a.warnLimited("chain_read_fail", "读共享审批链失败，沿用 last-good 链", "cause", err)
			return *lg, true
		}
		a.warnLimited("chain_read_fail", "读共享审批链失败且无 last-good，回落本地审批链", "cause", err)
		return ApproverChain{}, false
	}
}

// readChain 带独立短时限执行一次链读取。
func (a *Approver) readChain(ctx context.Context, reader ChainReader) (ApproverChain, error) {
	return ReadChainWithTimeout(ctx, reader)
}

// warnLimited 按 key 限频打 WARN：同类告警每分钟至多一条（本次补充 P2 约束）。
//
// 为什么限频：共享账本读失败在热路径上会每个权限请求各来一次，逐请求打 WARN
// 会把真正需要注意的失败淹没在噪音里。
func (a *Approver) warnLimited(key, msg string, args ...any) {
	a.warnMu.Lock()
	if a.warnAt == nil {
		a.warnAt = map[string]time.Time{}
	}
	now := time.Now()
	if t, ok := a.warnAt[key]; ok && now.Sub(t) < time.Minute {
		a.warnMu.Unlock()
		return
	}
	a.warnAt[key] = now
	a.warnMu.Unlock()
	a.log.Warn(msg, args...)
}

// decideLocalChain 是既有本地配置链路径（B376/B405 语义，S5 一字不改）。
func (a *Approver) decideLocalChain(ctx context.Context, permission, taskSummary string, tc ApproverTaskContext) ApproverDecision {
	start := time.Now()
	nonce := randNonce()
	if nonce == "" {
		a.log.Warn("生成裁决 nonce 失败，本次裁决不做防伪校验", "executor", a.executorName)
	}
	// 一个 nonce 服务整条请求：所有候选拿到同一个 prompt，防伪语义不变
	prompt := approverPrompt(taskSummary, permission, nonce, tc)
	a.log.Info("审批者开始裁决", "permission", turn.TruncateRunes(permission, 80),
		"executors", strings.Join(a.names, ","), "model", a.model, "models", a.models, "nonce", nonce,
		"task_mode", tc.Mode, "has_task_context", !tc.IsZero())

	var attempts []ApproverAttempt
	var lastErr error
	for i, name := range a.names {
		model := modelLabelOr(a.modelFor(name))
		a.log.Warn("审批者候选开始", "candidate_index", i+1, "candidate_count", len(a.names),
			"request_id", approverRequestID(ctx), "executor", name, "model", model,
			"permission_class", permissionLogClass(permission))
		attempt := a.decideWithCandidate(ctx, name, prompt, nonce, permission, taskSummary)
		attempts = append(attempts, attempt)
		a.log.Warn("审批者候选结果", "request_id", approverRequestID(ctx), "candidate_index", i+1, "executor", name,
			"model", attempt.Model, "decision", attempt.Decision, "failed", attempt.Err != nil,
			"elapsed_ms", attempt.ElapsedMS)
		if attempt.Err == nil {
			for _, skipped := range a.names[i+1:] {
				a.log.Warn("审批者候选跳过", "executor", skipped,
					"request_id", approverRequestID(ctx), "reason", "前序候选已给出裁决", "prior_executor", name)
			}
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

// decideSharedChain 按共享审批链逐环裁决（B437）。
//
// 每个环各记一条 attempt（带 machine）；某环给出干净裁决即停；全部环失败返回
// 一个总 Err（请求级失败计 1，与本地链路径同款），由上层做连续失败计数。
func (a *Approver) decideSharedChain(ctx context.Context, chain ApproverChain, permission, taskSummary string, tc ApproverTaskContext) ApproverDecision {
	start := time.Now()
	timeout := EffectiveApproverChainTimeout(chain)
	a.log.Info("审批者开始裁决（共享链）", "request_id", approverRequestID(ctx),
		"permission_class", permissionLogClass(permission), "rings", len(chain.Rings), "timeout", timeout)

	var attempts []ApproverAttempt
	var lastErr error
	for i, ring := range chain.Rings {
		model := ring.Model
		if model == "" {
			model = modelLabelOr(a.modelFor(ring.Executor))
		}
		a.log.Warn("审批者环开始", "ring_index", i+1, "ring_count", len(chain.Rings),
			"request_id", approverRequestID(ctx), "machine", ring.Machine, "executor", ring.Executor, "model", model,
			"permission_class", permissionLogClass(permission))
		attempt := a.decideRing(ctx, ring, timeout, permission, taskSummary, tc)
		attempts = append(attempts, attempt)
		a.log.Warn("审批者环结果", "request_id", approverRequestID(ctx), "ring_index", i+1, "machine", ring.Machine,
			"executor", ring.Executor, "model", attempt.Model, "decision", attempt.Decision,
			"failed", attempt.Err != nil, "elapsed_ms", attempt.ElapsedMS)
		if attempt.Err == nil {
			for _, skipped := range chain.Rings[i+1:] {
				a.log.Warn("审批者环跳过", "machine", skipped.Machine, "executor", skipped.Executor,
					"request_id", approverRequestID(ctx), "reason", "前序环已给出裁决", "prior_machine", ring.Machine,
					"prior_executor", ring.Executor)
			}
			return ApproverDecision{
				Approve:   attempt.Decision == "approve",
				Reason:    attempt.Reason,
				ElapsedMS: time.Since(start).Milliseconds(),
				Executor:  ring.Executor,
				Model:     attempt.Model,
				Attempts:  attempts,
			}
		}
		lastErr = attempt.Err
		a.log.Warn("审批者环失败，尝试下一环", "machine", ring.Machine,
			"request_id", approverRequestID(ctx), "executor", ring.Executor,
			"model", attempt.Model, "error_kind", attempt.ErrorKind, "tried", len(attempts),
			"elapsed_ms", attempt.ElapsedMS)
	}
	if lastErr == nil {
		lastErr = errors.New("共享审批链无可用环")
	}
	final := ApproverDecision{
		Approve:   false,
		ElapsedMS: time.Since(start).Milliseconds(),
		Err:       fmt.Errorf("全部审批环均失败（%d 环）: %w", len(chain.Rings), lastErr),
		Attempts:  attempts,
	}
	if len(attempts) > 0 {
		final.Executor = attempts[len(attempts)-1].Executor
		final.Model = attempts[len(attempts)-1].Model
	}
	a.log.Error("审批者全部环失败", "request_id", approverRequestID(ctx), "rings", len(chain.Rings),
		"tried", len(attempts), "error_kind", finalFailureKind(attempts),
		"elapsed_ms", final.ElapsedMS)
	return final
}

// permissionLogClass gives operators a useful correlation label without logging
// command text, environment contents, credentials, or other request payload.
func permissionLogClass(permission string) string {
	trimmed := strings.TrimSpace(permission)
	if i := strings.IndexByte(trimmed, ':'); i > 0 {
		return strings.ToLower(strings.TrimSpace(trimmed[:i]))
	}
	return "unknown"
}

// decideRing 执行共享链上的一个环：本机环就地跑 OneShot，他机环经远程裁决口。
func (a *Approver) decideRing(ctx context.Context, ring ApproverRing, timeout time.Duration, permission, taskSummary string, tc ApproverTaskContext) ApproverAttempt {
	candStart := time.Now()
	if a.isSelfRing(ring.Machine) {
		shot, err := a.resolveLocalShot(ring.Executor)
		if err != nil {
			elapsed := time.Since(candStart).Milliseconds()
			a.log.Error("审批者本机环缺少 OneShot", "request_id", approverRequestID(ctx),
				"machine", ring.Machine, "executor", ring.Executor,
				"model", modelLabelOr(ring.Model), "error_kind", ApproverFailInternal, "elapsed_ms", elapsed)
			return ApproverAttempt{Executor: ring.Executor, Machine: ring.Machine, Decision: "error",
				Reason: err.Error(), Err: err, ErrorKind: ApproverFailInternal, ElapsedMS: elapsed,
				Model: modelLabelOr(ring.Model)}
		}
		out := RunLocalRing(ctx, shot, ring.Executor, ring.Model, permission, taskSummary, tc, timeout, a.env, a.log)
		return ringOutcomeAttempt(ring, out)
	}
	a.chainMu.Lock()
	rd := a.remoteDecider
	a.chainMu.Unlock()
	if rd == nil {
		err := fmt.Errorf("远程裁决口未装配，环 %s 不可用", ring.Machine)
		elapsed := time.Since(candStart).Milliseconds()
		a.log.Error("审批者远程裁决口缺失", "request_id", approverRequestID(ctx),
			"machine", ring.Machine, "executor", ring.Executor,
			"model", modelLabelOr(ring.Model), "error_kind", ApproverFailInternal, "elapsed_ms", elapsed)
		return ApproverAttempt{Executor: ring.Executor, Machine: ring.Machine, Decision: "error",
			Reason: err.Error(), Err: err, ErrorKind: ApproverFailInternal, ElapsedMS: elapsed,
			Model: modelLabelOr(ring.Model)}
	}
	out := rd.DecideRemote(ctx, ring, permission, taskSummary, tc, timeout)
	return remoteOutcomeAttempt(ring, out)
}

// isSelfRing 判断环的机器名是否本机（无注入判据时按「恒非本机」处理）。
func (a *Approver) isSelfRing(machine string) bool {
	a.chainMu.Lock()
	fn := a.selfMachine
	a.chainMu.Unlock()
	return fn != nil && fn(machine)
}

// resolveLocalShot 解析本机环的 OneShot：本地配置链的既有绑定优先，未命中走
// localShot 缝（B437 §4.9）。两条路径在 decideRing 内汇合成同一执行逻辑。
func (a *Approver) resolveLocalShot(name string) (executor.OneShot, error) {
	a.chainMu.Lock()
	shot := a.shots[name]
	fn := a.localShot
	a.chainMu.Unlock()
	if shot != nil {
		return shot, nil
	}
	if fn == nil {
		return nil, fmt.Errorf("本机执行器 %q 无可用 OneShot（本地绑定与解析缝均未命中）", name)
	}
	return fn(name)
}

// ringOutcomeAttempt 把单环本机裁决结果投影成审计 attempt。
func ringOutcomeAttempt(ring ApproverRing, out RingOutcome) ApproverAttempt {
	at := ApproverAttempt{Executor: ring.Executor, Machine: ring.Machine,
		Decision: out.Decision, Reason: out.Reason, ErrorKind: safeApproverErrorKind(out.ErrorKind), ElapsedMS: out.ElapsedMS, Model: out.Model}
	if at.Model == "" {
		at.Model = modelLabelOr(ring.Model)
	}
	if out.Decision == "error" {
		if at.Reason == "" {
			at.Reason = ApproverFailReason(out.ErrorKind)
		}
		at.ErrorKind = safeApproverErrorKind(at.ErrorKind)
		at.Err = errors.New(at.Reason)
	}
	return at
}

// remoteOutcomeAttempt 把远程环裁决结果投影成审计 attempt，并把失败类别翻成人读理由。
func remoteOutcomeAttempt(ring ApproverRing, out RemoteOutcome) ApproverAttempt {
	at := ApproverAttempt{Executor: ring.Executor, Machine: ring.Machine,
		Decision: out.Decision, Reason: out.Reason, ErrorKind: safeApproverErrorKind(out.ErrorKind), ElapsedMS: out.ElapsedMS, Model: out.Model}
	if at.Model == "" {
		at.Model = modelLabelOr(ring.Model)
	}
	if out.Decision == "error" {
		kind := safeApproverErrorKind(out.ErrorKind)
		at.ErrorKind = kind
		if kind == ApproverFailInternal && out.Reason != "" {
			at.Reason = out.Reason
		} else {
			at.Reason = ApproverFailReason(kind)
		}
		at.Err = errors.New(at.Reason)
	}
	return at
}

func safeApproverErrorKind(kind string) string {
	switch kind {
	case ApproverFailUnreachable, ApproverFailUnsupported, ApproverFailNoExecutor,
		ApproverFailTimeout, ApproverFailInvalidOutput, ApproverFailInternal:
		return kind
	default:
		return ApproverFailInternal
	}
}

func finalFailureKind(attempts []ApproverAttempt) string {
	if len(attempts) == 0 || attempts[len(attempts)-1].ErrorKind == "" {
		return ApproverFailInternal
	}
	return safeApproverErrorKind(attempts[len(attempts)-1].ErrorKind)
}

func modelLabelOr(model string) string {
	if model == "" {
		return ApproverModelDefaultLabel
	}
	return model
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
//
// 2026-10-21 增补内联代码一行（B451）：人工清单形态（内联 Python/heredoc、
// psql -c、rg --pre、git checkout/restore --）不再硬升级、改由本审批者裁决，
// 而判据看不见 heredoc 正文——这一行要求模型读完正文、只批任务仓库内读写与
// 跑测试。文字由用户随 spec 逐字批准，改动须回 spec；不得含 %（会打乱上面
// 四个 %s 的 Sprintf 填充）。
const approverPromptTemplate = `你是代码任务的权限审批者。任务背景：%s
权限请求：%s
本次裁决编号 nonce=%s，你必须在输出的 JSON 里原样回显它，否则裁决作废。
仅当该操作明显安全（任务仓库内读写、跑测试/构建、装项目依赖、常规 git 提交）时才批准。
任何不确定、可能破坏数据、影响范围超出任务仓库的操作，必须升级给上级协调者。
涉及生产环境、部署动作、运维目标机（如 kubectl -n prod、ssh 到生产主机、terraform apply）的操作，一律升级给上级协调者。
权限请求含内联代码或脚本（python -c / heredoc、psql -c、rg --pre）或 git checkout/restore -- 时，读完正文再裁决：只在任务仓库内读写文件、跑测试时批准；写库、联网、改仓库外路径、读密钥的，一律升级。
涉及调用 handoff 自身 CLI 变更任务状态的操作（如 dispatch/continue/done/reply/run），一律升级给上级协调者。
只输出一行 JSON，不要输出其他内容：{"decision":"approve","nonce":"%s"} 或 {"decision":"escalate","reason":"简要原因","nonce":"<同一 nonce>"}`
