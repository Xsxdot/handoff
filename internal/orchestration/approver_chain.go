// approver_chain.go —— 共享审批链的类型、校验与单环裁决实现（B437）。
//
// 职责：
//   - 定义跨机共享审批链的形状：有序环（机器名、执行器、可选模型）+ 链级单环时限
//   - 提供链校验的唯一实现（CLI 写入与注册表 body 解码共用）
//   - 提供链文件（YAML，键名 candidates）与注册表 body（JSON，键名 rings）的编解码
//   - 提供单环本机 OneShot 裁决的导出面（发起方本机环与对端「代他机裁决」端点共用），
//     提示词模板 / nonce 生成与校验 / 裁决解析只有这一份实现
//
// 边界：
//   - 不读账本、不碰设备表：机器名是否在设备表由调用方以 knownMachines 注入
//   - 不做网络：远程裁决口与链读取口都是注入端口（见 Approver 的 setter）
//   - 不写事件：审计归发起方，本文件只返回结构化裁决结果
package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"gopkg.in/yaml.v3"
)

// ApproverChainKind / ApproverChainID 是共享审批链在账本注册表里的键（P3 拍板）。
//
// 纯内部命名，但**跨二进制必须一致**（冻结 F5）：全队任意版本的 agentd 读写同一行。
const (
	ApproverChainKind = "approver_chain"
	ApproverChainID   = "default"
)

// DefaultApproverChainTimeout 是链文件省略 timeout 时的单环裁决时限。
const DefaultApproverChainTimeout = 60 * time.Second

// ApproverChainReadTimeout 是每次读共享审批链的独立短时限（本次补充 P2 约束）。
//
// 为什么必须独立于裁决时限与连接时限：共享账本在 Supabase，实测出现过 DNS 解析
// 挂起 i/o timeout；读若不封顶，每个权限请求都会被一次账本读拖住。2s 到点即用
// last-good（从未读到则回落本地链），与裁决时限（60s）、连接时限（dialTimeout 10s）
// 三者互不共用。包级常量，不进链文件。
const ApproverChainReadTimeout = 2 * time.Second

// ApproverRing 是共享审批链上的一个环：在哪台机器、用哪个执行器、可选模型。
type ApproverRing struct {
	Machine  string `json:"machine" yaml:"machine"`
	Executor string `json:"executor" yaml:"executor"`
	// Model 空 = 用该机器上该执行器的默认模型。
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
}

// ApproverChain 是共享审批链：链级单环时限 + 有序环集合。
//
// JSON 键名（timeout / rings[{machine,executor,model}]）是冻结 F5 的跨二进制约定，
// 只增不改名不删不改性。Timeout 走 time.Duration 的默认 JSON 编码（纳秒整数），
// 编解码互为逆运算，SQLite/PG 双后端往返恒等。
type ApproverChain struct {
	Timeout time.Duration  `json:"timeout" yaml:"timeout"`
	Rings   []ApproverRing `json:"rings" yaml:"rings"`
}

// ErrApproverChainAbsent 表示共享审批链**确定不存在**（注册表无此行）。
//
// 与「读失败」严格区分：clear 之后读到 ErrApproverChainAbsent 是权威的数据结论，
// 必须丢弃 last-good 回落本地链；读失败（含解码失败）是故障，持 last-good 继续。
// 不区分这两者会让 clear 之后的机器永远跑在已删除的链上。
var ErrApproverChainAbsent = errors.New("共享审批链未设置")

// approverChainFile 是 CLI 链文件的形态：键名 candidates（用户可见面，spec 实现决定）。
type approverChainFile struct {
	Timeout    *time.Duration `yaml:"timeout"`
	Candidates []ApproverRing `yaml:"candidates"`
}

// DecodeApproverChainFile 解析 CLI 链文件（YAML）。
//
// 省略 timeout → DefaultApproverChainTimeout；显式 timeout ≤0 → 拒绝（点名取值）。
// 只做形状与时限，不校验机器名（调用方拿设备表另行 ValidateApproverChain）。
func DecodeApproverChainFile(data []byte) (ApproverChain, error) {
	var f approverChainFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return ApproverChain{}, fmt.Errorf("解析审批链文件: %w", err)
	}
	timeout := DefaultApproverChainTimeout
	if f.Timeout != nil {
		if *f.Timeout <= 0 {
			return ApproverChain{}, fmt.Errorf("审批链 timeout 必须 > 0（省略取 %s），实得 %s", DefaultApproverChainTimeout, *f.Timeout)
		}
		timeout = *f.Timeout
	}
	return ApproverChain{Timeout: timeout, Rings: f.Candidates}, nil
}

// EncodeApproverChain 把链编码成注册表 body（冻结 F5 的 JSON 形状）。
func EncodeApproverChain(chain ApproverChain) ([]byte, error) {
	b, err := json.Marshal(chain)
	if err != nil {
		return nil, fmt.Errorf("编码审批链: %w", err)
	}
	return b, nil
}

// DecodeApproverChain 解码注册表 body 并做形状校验（不校验机器名）。
//
// 解码失败 / 形状非法都返回普通错误（发起方按「读失败」处理，持 last-good）。
func DecodeApproverChain(body []byte) (ApproverChain, error) {
	var chain ApproverChain
	if err := json.Unmarshal(body, &chain); err != nil {
		return ApproverChain{}, fmt.Errorf("解码审批链 body: %w", err)
	}
	if chain.Timeout <= 0 {
		chain.Timeout = DefaultApproverChainTimeout
	}
	if err := ValidateApproverChain(chain, nil); err != nil {
		return ApproverChain{}, fmt.Errorf("审批链 body 校验: %w", err)
	}
	return chain, nil
}

// ValidateApproverChain 校验审批链形状（CLI 写入与 body 解码共用的唯一实现）。
//
// 规则：
//   - 环集合非空
//   - 每环 machine / executor 非空
//   - (machine, executor) 不重复（P1 甲：同机同执行器出现两次即拒，model 不参与判重）
//   - executor ∈ executor.SupportedHarnesses()（代码级事实，与装机无关）
//   - timeout 省略（0）取默认；负值拒绝（显式 ≤0 由调用方先行拦下）
//   - knownMachines 非 nil 时每环 machine 必须是其中之一（未知机器拒绝并列出可用名）
//
// 参数：
//   - chain: 待校验链（Timeout 为 0 视为「用默认」，本函数不改写它）
//   - knownMachines: 可用机器名集合；nil = 跳过机器名成员校验（body 解码/单测用）
//
// 返回：nil = 合法；否则返回点名「第几环、哪一环」的可行动错误。
func ValidateApproverChain(chain ApproverChain, knownMachines []string) error {
	if len(chain.Rings) == 0 {
		return errors.New("审批链不能为空：至少一个环（candidates）")
	}
	if chain.Timeout < 0 {
		return fmt.Errorf("审批链 timeout 不能为负: %s", chain.Timeout)
	}
	supported := map[string]bool{}
	for _, name := range executor.SupportedHarnesses() {
		supported[name] = true
	}
	known := map[string]bool{}
	for _, name := range knownMachines {
		known[name] = true
	}
	seen := map[string]int{}
	for i, ring := range chain.Rings {
		pos := i + 1
		if strings.TrimSpace(ring.Machine) == "" {
			return fmt.Errorf("第 %d 环的 machine 不能为空", pos)
		}
		if strings.TrimSpace(ring.Executor) == "" {
			return fmt.Errorf("第 %d 环（机器 %s）的 executor 不能为空", pos, ring.Machine)
		}
		if !supported[ring.Executor] {
			return fmt.Errorf("第 %d 环（机器 %s）的 executor %q 不是受支持的执行器（支持: %s）",
				pos, ring.Machine, ring.Executor, strings.Join(executor.SupportedHarnesses(), ", "))
		}
		if knownMachines != nil && !known[ring.Machine] {
			return fmt.Errorf("第 %d 环的机器 %q 不在设备表；可用机器名: %s",
				pos, ring.Machine, strings.Join(sortedStrings(knownMachines), ", "))
		}
		key := ring.Machine + "\x00" + ring.Executor
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("第 %d 环与第 %d 环重复：同一台机器 %q 上的同一执行器 %q 只能出现一次",
				pos, prev, ring.Machine, ring.Executor)
		}
		seen[key] = pos
	}
	return nil
}

// EffectiveApproverChainTimeout 返回链的实际单环裁决时限：省略（≤0）取默认。
func EffectiveApproverChainTimeout(chain ApproverChain) time.Duration {
	if chain.Timeout > 0 {
		return chain.Timeout
	}
	return DefaultApproverChainTimeout
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// ---- 失败分类（§4.6 五分类 + internal 兜底）----

// 环失败类别常量。前五条是 §4.6 的确定性判据（写进 attempt.Reason）；
// ApproverFailInternal 兜住 5xx、error_kind=internal、未知 error_kind（向前兼容）。
const (
	ApproverFailUnreachable   = "unreachable"    // 连接时限内连不上（传输错误且整体截止未到）
	ApproverFailUnsupported   = "unsupported"    // 对端不支持该端点（404）
	ApproverFailNoExecutor    = "no_executor"    // 对端没有请求点名的执行器
	ApproverFailTimeout       = "timeout"        // 连上后裁决超时（整体截止已到）
	ApproverFailInvalidOutput = "invalid_output" // 对端回了无法解析/非预期的裁决
	ApproverFailInternal      = "internal"       // 其余（5xx / error_kind=internal / 未知）
)

// ApproverFailReason 把失败类别翻成人读的审计理由。
func ApproverFailReason(kind string) string {
	switch kind {
	case ApproverFailUnreachable:
		return "机器不可达"
	case ApproverFailUnsupported:
		return "对端不支持"
	case ApproverFailNoExecutor:
		return "对端无此执行器"
	case ApproverFailTimeout:
		return "超时"
	case ApproverFailInvalidOutput:
		return "裁决无效"
	default:
		return "对端内部错误"
	}
}

// NormalizeApproverErrorKind 把端点回报的 error_kind 归一到三个冻结取值之一。
//
// 冻结 F2：error_kind ∈ {no_executor, invalid_output, internal}；未知值按 internal
// 处理（向前兼容——新版对端新增的类别不该让旧发起方判成无效裁决）。
func NormalizeApproverErrorKind(kind string) string {
	switch kind {
	case ApproverFailNoExecutor, ApproverFailInvalidOutput, ApproverFailInternal:
		return kind
	default:
		return ApproverFailInternal
	}
}

// ---- 链读取口 / 远程裁决口（注入端口，第四条契约面唯一）----

// ChainReader 读当前共享审批链。nil = 无共享链（S5 本地链路径）。
//
// 返回 ErrApproverChainAbsent 表示链确定不存在（clear），其余错误表示读失败。
// 实现应尊重传入 ctx 的取消（Approver 另以 ApproverChainReadTimeout 封顶）。
type ChainReader interface {
	Current(ctx context.Context) (ApproverChain, error)
}

// RemoteOutcome 是一次远程环裁决的结果。
//
//   - Decision: approve / escalate / error
//   - Reason: 裁决理由（approve/escalate）或失败明细（error）
//   - Model: 对端回报的模型标注；空 = 未指定
//   - ElapsedMS: 对端侧该环耗时
//   - ErrorKind: decision=error 时的失败类别（ApproverFail* 常量之一）
type RemoteOutcome struct {
	Decision  string
	Reason    string
	Model     string
	ElapsedMS int64
	ErrorKind string
}

// RemoteDecider 请一台他机的 agentd 代跑一个环。nil = 他机环一律失败。
//
// 实现负责连接选路与失败归类（§4.6 五分类）；timeout 是发起方施加的裁决时限。
type RemoteDecider interface {
	DecideRemote(ctx context.Context, ring ApproverRing, permission, taskSummary string, timeout time.Duration) RemoteOutcome
}

// ReadChainWithTimeout 带独立短时限（ApproverChainReadTimeout）执行一次链读取。
//
// 为什么另起 goroutine：账本驱动（Supabase/PG）的读若不尊重 ctx，仅靠
// context.WithTimeout 到点也不会返回；把读放进 goroutine 并 select 到点，才能
// 保证 2s 内一定返回。读 goroutine 用缓冲通道回传，到点后不会阻塞、也不泄漏判定。
func ReadChainWithTimeout(ctx context.Context, reader ChainReader) (ApproverChain, error) {
	type result struct {
		chain ApproverChain
		err   error
	}
	rctx, cancel := context.WithTimeout(ctx, ApproverChainReadTimeout)
	defer cancel()
	ch := make(chan result, 1)
	go func() {
		c, err := reader.Current(rctx)
		ch <- result{chain: c, err: err}
	}()
	select {
	case r := <-ch:
		return r.chain, r.err
	case <-rctx.Done():
		return ApproverChain{}, fmt.Errorf("读共享审批链超时（%s）: %w", ApproverChainReadTimeout, rctx.Err())
	}
}

// ---- 单环本机裁决（导出面，端点复用）----

// RingOutcome 是单环本机 OneShot 裁决的结构化结果。
//
//   - Decision: approve / escalate / error
//   - Reason: 裁决理由或失败明细
//   - Model: 实际使用的模型标注（未指定 = ApproverModelDefaultLabel）
//   - ElapsedMS: 本环耗时
//   - ErrorKind: decision=error 时的失败类别
type RingOutcome struct {
	Decision  string
	Reason    string
	Model     string
	ElapsedMS int64
	ErrorKind string
}

// RunLocalRing 用与审批链完全同一份实现执行单环本机 OneShot 裁决。
//
// 这是「提示词构造 + nonce 生成与校验 + 裁决解析 + grok EffortLow 档位」的唯一
// 实现：发起方本机环与对端「代他机裁决」端点都调它，两端裁决语义不会漂移。
//
// 参数：
//   - ctx: 上层上下文；timeout > 0 时在其上叠加裁决截止
//   - shot: 已解析的一次性调用能力（nil → error/no_executor）
//   - executorName: 执行器名（决定 grok 档位与 env 解析键）
//   - model: 模型名；空 = 执行器默认
//   - permission/taskSummary: 权限原文与任务背景
//   - timeout: 单环裁决时限；≤0 = 不额外封顶（仅受 ctx 约束）
//   - env: env 文件解析器（nil = 不注入）
//   - log: 日志入口（nil → slog.Default）
//
// 返回：fail-closed——任何无法干净裁决的输入都返回 Decision=error + ErrorKind。
func RunLocalRing(ctx context.Context, shot executor.OneShot, executorName, model, permission, taskSummary string,
	timeout time.Duration, env *envfile.Resolver, log *slog.Logger) RingOutcome {
	start := time.Now()
	if log == nil {
		log = slog.Default()
	}
	modelLabel := model
	if modelLabel == "" {
		modelLabel = ApproverModelDefaultLabel
	}
	if shot == nil {
		err := fmt.Errorf("执行器 %q 未绑定 OneShot", executorName)
		log.Error("单环裁决缺少 OneShot", "executor", executorName)
		return RingOutcome{Decision: "error", Reason: err.Error(), Model: modelLabel,
			ElapsedMS: time.Since(start).Milliseconds(), ErrorKind: ApproverFailNoExecutor}
	}
	nonce := randNonce()
	if nonce == "" {
		log.Warn("生成裁决 nonce 失败，本次裁决不做防伪校验", "executor", executorName)
	}
	prompt := fmt.Sprintf(approverPromptTemplate, taskSummary, permission, nonce, nonce)
	var envList []string
	if env != nil {
		extra, err := env.For(executorName)
		if err != nil {
			log.Error("单环裁决 env 文件解析失败", "executor", executorName, "cause", err)
			return RingOutcome{Decision: "error", Reason: fmt.Sprintf("解析审批者 env 文件: %v", err),
				Model: modelLabel, ElapsedMS: time.Since(start).Milliseconds(), ErrorKind: ApproverFailInternal}
		}
		if len(extra) > 0 {
			envList = append(os.Environ(), extra...)
		}
	}
	req := executor.OneShotReq{Prompt: prompt, Model: model, Env: envList, Limits: executor.OneShotLimits{}}
	if executorName == executor.HarnessGrok {
		req.Limits.Effort = executor.EffortLow
	}
	cctx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	log.Info("单环裁决开始", "executor", executorName, "model", modelLabel)
	reply, err := shot.Invoke(cctx, req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		log.Error("单环裁决失败", "executor", executorName,
			"permission", turn.TruncateRunes(permission, 80), "cause", err, "elapsed_ms", elapsed)
		return RingOutcome{Decision: "error", Reason: err.Error(), Model: modelLabel,
			ElapsedMS: elapsed, ErrorKind: ApproverFailInternal}
	}
	d := parseDecision(reply.Text, nonce, elapsed)
	if d.Err != nil {
		log.Error("单环裁决解析失败，按无效裁决处理", "executor", executorName, "cause", d.Err)
		return RingOutcome{Decision: "error", Reason: d.Err.Error(), Model: modelLabel,
			ElapsedMS: elapsed, ErrorKind: ApproverFailInvalidOutput}
	}
	decision := "escalate"
	if d.Approve {
		decision = "approve"
	}
	log.Info("单环裁决完成", "executor", executorName, "decision", decision,
		"reason", turn.TruncateRunes(d.Reason, 80), "elapsed_ms", elapsed)
	return RingOutcome{Decision: decision, Reason: d.Reason, Model: modelLabel, ElapsedMS: elapsed}
}
