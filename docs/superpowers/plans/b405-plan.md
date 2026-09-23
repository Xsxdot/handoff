# B405 实现计划：审批链每个候选可单独指定模型

读者：零上下文执行者。级别 L2（单子系统）。spec：`docs/superpowers/specs/b405.md`（已批准 2026-09-23）。
基线：`cards/B233.1-charter-7` @ `0dfa9e74`。本计划在该基线的代码事实上复核（原始读数见 `docs/superpowers/ledgers/2026-09-23-b405-plan-ledger.md`）。

本节点只产出计划，不写实现代码、不建脚手架。以下所有代码块是给 implement 节点执行者的目标形态，不是本节点已落盘的改动。

## 0. 工作树护卫（动手前先看）

工作树 HEAD 必须含 `docs/superpowers/specs/b405.md` 且 `internal/orchestration/approver.go` 已有 B376 的
`names []string` / `shots map[string]executor.OneShot`。缺一即为错基线：

```
git fetch origin cards/B233.1-charter-7
git reset --hard origin/cards/B233.1-charter-7
```

禁止基于 `origin/main` 改 `approver.go` 冒充审批链——main 上 `Decide` 是单 `shot` 旧形态。

## 1. 基线事实（动手前在基线上复核过；原始输出见台账）

- `go build ./...` → exit 0（2026-09-23 本工作树实测）。
- `go test ./internal/config/ ./internal/approval/ -count=1` → `ok` 两包。
- `go test ./internal/orchestration/ -count=1 -run 'TestDecide|TestApprover|TestConsultApprover|TestNilApprover'` → `ok`。
- `internal/config/config.go:201`：`type ApproverConfig struct { Executor ExecutorList; Model string; Timeout time.Duration; Blacklist []string }`，无 yaml tag（go-yaml 按小写字段名映射键）。
- `internal/config/config.go:580`：approver 取值域校验只在 `!c.Approver.Executor.Empty()` 时生效。
- `internal/config/config.go:630`：未知键报错文案里的支持键清单 `approver{executor,model,timeout,blacklist}`。
- `internal/orchestration/approver.go:238`：`req := executor.OneShotReq{Prompt: prompt, Model: a.model, ...}`——链级 model 原样塞给每个候选，不区分候选。
- `internal/orchestration/approver.go:239-241`：`if name == executor.HarnessGrok { req.Limits.Effort = executor.EffortLow }`（本卡不动）。
- 两条观测路径各有独立 payload 结构：`internal/orchestration/contracts.go:171` 的 `ApproverDecisionPayload`（Manager 路径 `manager.go:2379/2392` 写）与 `internal/approval/client.go:539` 的 `approverDecisionPayload`（ApprovalClient 路径 `client.go:351/375` 写）；跨包投影在 `internal/orchestration/approval_client.go:72 toApprovalAttempts`。
- `approval.ApproverAttempt`（`client.go:37`）是 approval 包对 orchestration 同构类型的**手写镜像**（刻意不 import orchestration）。
- `executor.OneShotReq.Model` 为空串 = 执行者自身默认（既有适配器语义，本卡不改适配器）。

## 2. 图覆盖债（codegraph 未命中 / 陈旧，按纪律记录）

- `codegraph sym decideWithCandidate` → `符号 "decideWithCandidate" 不在图中`；近似候选 `[]`。该符号只在本计划查证，未被图覆盖。
- 图 `view: baseline` 的签名陈旧：`codegraph sym ApproverConfig` 返回 `Executor string`、`codegraph sym Approver` 返回 `shot executor.OneShot`，均早于本分支上已合并的 B376。本计划的现状签名以工作树源码（上方基线事实）为准，不采信图。
- `codegraph flow n_orchestration_Approver_Decide` → `degraded=true, steps=0`，`missing: 基线没有 flows 段`；已按纪律回落到读源码（`approver.go:164-210`）。
- 未命中符号记入本节为覆盖债，未用 grep 冒充查图结论。

## 3. 任务 DAG 与测试范围

| task | 内容 | depends | 触及包 / 测试命令 |
|---|---|---|---|
| Task 1（承重·最薄路径） | `approver.models` 配置解析+校验；`Approver` 候选级选值 | — | `internal/config`、`internal/orchestration`：`go test ./internal/config/ -count=1` 与 `go test ./internal/orchestration/ -count=1 -run 'TestDecide|TestApprover|TestConsultApprover|TestNilApprover'` |
| Task 2 | 候选实际模型经 payload 落库（两路观测） | Task 1 | `internal/orchestration`：`go test ./internal/orchestration/ -count=1 -run 'TestConsultApprover|TestApprovalClientPerCandidateModel'` |

每个 task 收尾都跑 `go build ./...`。全量 `go test ./...` 不在任何单 task 内（归 acceptance）。

**最薄路径条**：本卡锁的候选级取值行为今天从声明缝 `Approver.Decide` 调用得不到预期结果（会红），Task 1 即点亮它的最薄可跑路径——同一 task 内同时落配置字段与 `Decide` 选值，不留一个「只解析不用」的中间 task。config 解析本身也是 spec §测试接缝对齐 的缝 1，不是纯脚手架。

## 4. 接口契约（执行者只看本 task，故此处列全）

### Consumes（既有签名，一字不改）

```go
// internal/config/config.go:201
type ApproverConfig struct {
    Executor  ExecutorList
    Model     string
    Timeout   time.Duration
    Blacklist []string
}
type ExecutorList []string            // config.go:213
func (l ExecutorList) Empty() bool    // config.go:244

// internal/orchestration/approver.go
func NewApprover(cfg config.ApproverConfig, env *envfile.Resolver, log *slog.Logger) (*Approver, error) // :122
func (a *Approver) BindOneShot(shot executor.OneShot)                        // :93
func (a *Approver) BindOneShotFor(name string, shot executor.OneShot)        // :105
func (a *Approver) Decide(ctx context.Context, permission, taskSummary string) ApproverDecision // :164
type ApproverAttempt struct { Executor, Decision, Reason string; Err error; ElapsedMS int64 }   // :46
type ApproverDecision struct { Approve bool; Reason string; ElapsedMS int64; Err error; Executor string; Attempts []ApproverAttempt } // :64

// internal/orchestration/contracts.go:171
type ApproverDecisionPayload struct { TicketID, Permission, Decision, Reason string; ElapsedMS int64; Executor string }

// internal/approval/client.go
type ApproverAttempt struct { Executor, Decision, Reason string; Err error; ElapsedMS int64 } // :37
type ConsultDecision struct { Approve bool; Reason string; ElapsedMS int64; Err error; Executor string; Attempts []ApproverAttempt } // :49

// internal/orchestration/approval_client.go
func (m *Manager) bindApproval(taskID string, snap executor.PolicySnapshot) executor.ApprovalClient // :29
func toApprovalAttempts(in []ApproverAttempt) []approval.ApproverAttempt                              // :72

// internal/executor
type OneShotReq struct { Prompt string; Model string; Env []string; Limits OneShotLimits /* ... */ }
```

### Produces（本卡新增，跨 task 逐字对齐）

```go
// Task 1 — internal/config
Models map[string]string `yaml:"models,omitempty"` // ApproverConfig 新字段

// Task 1 — internal/orchestration/approver.go
const ApproverModelDefaultLabel = "默认"
func (a *Approver) modelFor(name string) string
type ApproverAttempt struct { /* ...既有... */ Model string }   // 新字段
type ApproverDecision struct { /* ...既有... */ Model string }  // 新字段

// Task 2 — internal/orchestration/contracts.go
ApproverDecisionPayload struct { /* ...既有... */ Model string `json:"model,omitempty"` }

// Task 2 — internal/approval/client.go
ApproverAttempt struct { /* ...既有... */ Model string }
ConsultDecision struct { /* ...既有... */ Model string }
approverDecisionPayload struct { /* ...既有... */ Model string `json:"model,omitempty"` }
```

## 5. Task 1：`approver.models` 配置 + 候选级选值

文件：`internal/config/config.go`、`internal/config/config_test.go`、`internal/orchestration/approver.go`、`internal/orchestration/approver_test.go`

### 1a. 配置结构体（config.go:201）

把 `ApproverConfig` 与文档注释改成：

```go
// ApproverConfig 描述审批链的廉价模型审批者。
//
// 参数语义：
//   - Executor：审批者执行者有序候选（如 opencode/claude/grok/agy/codex）；
//     空=不启用审批链。YAML 标量与序列都合法（B376）
//   - Model：链级模型名，作为候选未单独指定时的回落；空=用执行者自身默认模型
//   - Models：候选执行者名 → 该候选模型名（B405）。候选无同名条目或值空串→
//     回落 Model；Model 也空→用该执行者自身默认。键必须是 Executor 候选成员
//     （approver 启用时启动期校验）
//   - Timeout：单次裁决超时，超时按 escalate 处理（fail-closed）
//   - Blacklist：自定义黑名单正则；命中即跳过审批者直接升级人工协调者
type ApproverConfig struct {
	Executor  ExecutorList
	Model     string
	Models    map[string]string `yaml:"models,omitempty"`
	Timeout   time.Duration
	Blacklist []string
}
```

`omitempty` 是硬要求：nil/空 map 不落盘，存量配置 Save 输出逐字节不变（go-yaml 字段顺序即结构体顺序）。

### 1b. 校验（config.go:559 `validate()` 内，`if !c.Approver.Executor.Empty() {` 块里，紧接 `seen` 循环之后）

```go
		// B405：approver.models 的键必须是候选成员。错字键（codex 写成 codexx）
		// 会被静默忽略——用户以为给某候选配了模型，实际它悄悄回落到链级/默认，
		// 只能靠行为差异发现。与 blacklist 正则在启动期硬拒同一条纪律。
		// 先收集再排序：map 迭代无序，多个错字时错误文本必须稳定可复现。
		if len(c.Approver.Models) > 0 {
			bad := make([]string, 0, len(c.Approver.Models))
			for key := range c.Approver.Models {
				if !seen[key] {
					bad = append(bad, key)
				}
			}
			if len(bad) > 0 {
				sort.Strings(bad)
				return fmt.Errorf("approver.models 含非候选键 %s（候选: %s）",
					strings.Join(bad, ","), strings.Join(c.Approver.Executor, ","))
			}
		}
```

`seen` 是上方 `for i, name := range c.Approver.Executor` 循环里 `seen := make(map[string]bool, ...)` 的同一个变量，仍在本 `if` 块作用域内。值不做校验（spec 允许空串）。approver 未启用时整块不进——`models` 出现与否都不校验（spec §实现决定）。

### 1c. 未知键报错文案（config.go:630）

把 `approver{executor,model,timeout,blacklist}` 改为 `approver{executor,model,models,timeout,blacklist}`。

### 1d. import（config.go:14-30）

`"sort"` 与 `"strings"` 均未在当前 import 中，追加（字母序插入）。

### 1e. `Approver` 结构（approver.go:74）

```go
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
	models map[string]string
	timeout time.Duration
	env   *envfile.Resolver
	shots map[string]executor.OneShot
}
```

（`timeout`/`env`/`shots` 及注释保持原样，仅在上方插入 `model` 注释更新与 `models` 字段。）

### 1f. 标签常量与选值函数（approver.go，放在 `Approver` 结构之后、`BindOneShot` 之前）

```go
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
```

### 1g. `NewApprover`（approver.go:122）

在 `names := ...` 之后、`return` 之前插入拷贝，并把 `models: models` 加进结构体字面量：

```go
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
```

### 1h. 审计结构（approver.go:46 / :64）

```go
type ApproverAttempt struct {
	Executor  string
	Decision  string
	Reason    string
	Err       error
	ElapsedMS int64
	// Model 是本候选实际使用的模型名；未指定时 = ApproverModelDefaultLabel。
	Model string
}

type ApproverDecision struct {
	Approve   bool
	Reason    string
	ElapsedMS int64
	Err       error
	Executor  string
	// Model 是最终生效候选实际使用的模型名；未指定时 = ApproverModelDefaultLabel。
	Model    string
	Attempts []ApproverAttempt
}
```

### 1i. `Decide`（approver.go:164）

- 开始日志加 `"models", a.models`（保留 `"model", a.model` 记链级）：

```go
	a.log.Info("审批者开始裁决", "permission", turn.TruncateRunes(permission, 80),
		"executors", strings.Join(a.names, ","), "model", a.model, "models", a.models, "nonce", nonce)
```

- 干净裁决返回处补 `Model: attempt.Model`：

```go
			return ApproverDecision{
				Approve:   attempt.Decision == "approve",
				Reason:    attempt.Reason,
				ElapsedMS: time.Since(start).Milliseconds(),
				Executor:  name,
				Model:     attempt.Model,
				Attempts:  attempts,
			}
```

- 全失败收尾处补最终候选模型：

```go
	if len(attempts) > 0 {
		final.Executor = attempts[len(attempts)-1].Executor
		final.Model = attempts[len(attempts)-1].Model
	}
```

### 1j. `decideWithCandidate`（approver.go:216）整函数替换为

```go
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
```

日志纪律：入口带 `name`/`modelLabel`；外部调用 `shot.Invoke` 前后各一条（开始/完成、开始/失败）；每条错误分支带 `executor` 与 `cause`；成功路径不静默。config 面的拒绝日志由 `Load` 在 `config.go:450` 的 `log().Error("配置校验失败", ..., "cause", verr)` 承担，validate 内不重复打。

### 1k. 注释纪律

- `modelFor` / `ApproverModelDefaultLabel` 的「为什么」注释见 1f（值空串=缺条目、占位符只服务人读）。
- `Models` 字段的 yaml/回落语义见 1a。
- `decideWithCandidate` 的函数注释见 1j。

### 1l. 步骤（红绿只套在缝断言上）

1. **配置缝①（解析）**：加 1a 字段（scaffolding，无行为）→ `go build ./...`。此步后解析自动生效（go-yaml 字段映射）。
2. **写 config 缝测试（红/绿混合）**，追加到 `internal/config/config_test.go`（`strings`/`os`/`filepath`/`time` 已在 import 中）：

```go
// TestLoadApproverModels 钉住 B405：approver.models 是候选名→模型名映射；
// 值可空串（与缺条目同义，回落逻辑在 orchestration 面）。
func TestLoadApproverModels(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	raw := "token: a\napprover:\n  executor: [agy, codex]\n  model: chain-m\n  models:\n    codex: gpt-6-luna\n    agy: \"\"\n  timeout: 30s\n"
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Approver.Model != "chain-m" {
		t.Fatalf("链级 model = %q，期望 chain-m", cfg.Approver.Model)
	}
	if got := cfg.Approver.Models["codex"]; got != "gpt-6-luna" {
		t.Fatalf("models[codex] = %q，期望 gpt-6-luna", got)
	}
	if v, ok := cfg.Approver.Models["agy"]; !ok || v != "" {
		t.Fatalf("models[agy] 显式空串应解析为空串条目，得到 %q ok=%v", v, ok)
	}
}

// TestLoadApproverModelsRejectsNonCandidate 钉住 B405 故事 3：错字键在审批链
// 启用时于启动期硬拒，报错点名错字键。
func TestLoadApproverModelsRejectsNonCandidate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	raw := "token: a\napprover:\n  executor: [agy, codex]\n  models:\n    codexx: gpt-6-luna\n  timeout: 30s\n"
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("approver.models 含非候选键应在启动期被拒绝")
	}
	if !strings.Contains(err.Error(), "codexx") {
		t.Fatalf("报错必须点名错字键 codexx，得到: %v", err)
	}
}

// TestLoadApproverModelsIgnoredWhenDisabled 反锁：审批链未启用时 models 出现
// 与否都不校验（与现有 approver 取值域校验只在启用时生效同款）。
func TestLoadApproverModelsIgnoredWhenDisabled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	raw := "token: a\napprover:\n  models:\n    any-key: any-model\n"
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err != nil {
		t.Fatalf("未启用审批链时 models 不应被校验: %v", err)
	}
}

// TestApproverModelsRoundTrip 锁序列化边界：models 经 Save/Load 往返，值不丢。
func TestApproverModelsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Defaults()
	cfg.Approver.Executor = config.ExecutorList{"agy", "codex"}
	cfg.Approver.Models = map[string]string{"codex": "gpt-6-luna"}
	cfg.Approver.Timeout = 30 * time.Second
	if err := config.Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Approver.Models["codex"] != "gpt-6-luna" {
		t.Fatalf("roundtrip models = %#v", loaded.Approver.Models)
	}
}
```

3. **跑红**：`go test ./internal/config/ -count=1 -run 'TestLoadApprover|TestApproverModels'`。预期 `TestLoadApproverModelsRejectsNonCandidate` 红（当前无校验，Load 成功），其余绿。若全绿说明第 4 步已提前实现，核对顺序。
4. **实现校验**：加 1b、1c、1d。
5. **跑绿**：同 3 的命令。
6. **Decide 缝②（scaffolding）**：加 1e、1h（结构字段）。`go build ./...`（`Decide` 尚未填 Model，编译通过、断言会红）。
7. **写 Decide 缝测试**，追加到 `internal/orchestration/approver_test.go`（`context`/`errors`/`config`/`executor`/`slog`/`time` 已在 import）：

```go
// TestDecidePerCandidateModel 钉住 B405 故事 1（目标场景）：executor=[agy,codex]
// + models={codex: gpt-6-luna}，链级 model 空。agy 候选不带模型名（走 agy
// 默认），codex 候选带 gpt-6-luna；agy 失败 failover 到 codex 后各自取值。
func TestDecidePerCandidateModel(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{
		Executor: config.ExecutorList{"agy", "codex"},
		Models:   map[string]string{"codex": "gpt-6-luna"},
		Timeout:  time.Second,
	}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	agyShot := &stubShot{fn: func(context.Context, executor.OneShotReq) (executor.OneShotReply, error) {
		return executor.OneShotReply{Status: executor.OneShotFailed}, errors.New("agy 挂了")
	}}
	codexShot := &stubShot{out: `{"decision":"approve","reason":"ok"}`}
	a.BindOneShotFor("agy", agyShot)
	a.BindOneShotFor("codex", codexShot)

	d := a.Decide(context.Background(), "Bash: ls", "摘要")
	if !d.Approve || d.Err != nil {
		t.Fatalf("codex 干净放行应 approve: %+v", d)
	}
	if agyShot.last.Model != "" {
		t.Fatalf("agy 未指定模型，OneShotReq.Model 应为空（走默认），得到 %q", agyShot.last.Model)
	}
	if codexShot.last.Model != "gpt-6-luna" {
		t.Fatalf("codex OneShotReq.Model = %q，期望 gpt-6-luna", codexShot.last.Model)
	}
	if len(d.Attempts) != 2 {
		t.Fatalf("Attempts = %+v，期望两条", d.Attempts)
	}
	if d.Attempts[0].Model != ApproverModelDefaultLabel {
		t.Fatalf("agy attempt.Model = %q，未指定应显式记 %q", d.Attempts[0].Model, ApproverModelDefaultLabel)
	}
	if d.Attempts[1].Model != "gpt-6-luna" {
		t.Fatalf("codex attempt.Model = %q，期望 gpt-6-luna", d.Attempts[1].Model)
	}
	if d.Model != "gpt-6-luna" {
		t.Fatalf("最终生效 Model = %q，期望 gpt-6-luna", d.Model)
	}
}

// TestDecideCandidateEmptyEntryFallsBackToChain 钉住批准意见：候选条目值空串
// 与缺条目同义，先回落链级 model，不是直接跳到执行者默认。
func TestDecideCandidateEmptyEntryFallsBackToChain(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{
		Executor: config.ExecutorList{"agy", "codex"},
		Model:    "chain-m",
		Models:   map[string]string{"codex": ""},
		Timeout:  time.Second,
	}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	agyShot := &stubShot{fn: func(context.Context, executor.OneShotReq) (executor.OneShotReply, error) {
		return executor.OneShotReply{Status: executor.OneShotFailed}, errors.New("agy 挂了")
	}}
	codexShot := &stubShot{out: `{"decision":"approve","reason":"ok"}`}
	a.BindOneShotFor("agy", agyShot)
	a.BindOneShotFor("codex", codexShot)

	d := a.Decide(context.Background(), "Bash: ls", "摘要")
	if agyShot.last.Model != "chain-m" {
		t.Fatalf("agy 缺条目应回落链级 chain-m，得到 %q", agyShot.last.Model)
	}
	if codexShot.last.Model != "chain-m" {
		t.Fatalf("codex 空串条目应回落链级 chain-m，得到 %q", codexShot.last.Model)
	}
	if d.Attempts[0].Model != "chain-m" || d.Attempts[1].Model != "chain-m" {
		t.Fatalf("attempt 模型 = %q/%q，期望均为 chain-m", d.Attempts[0].Model, d.Attempts[1].Model)
	}
}

// TestDecideNoModelAnywhereUsesDefaultLabel 钉住故事 1/4 的边界：链级与候选级
// 都空→请求不带模型名（执行者默认），审计记录显式记「默认」。
func TestDecideNoModelAnywhereUsesDefaultLabel(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{
		Executor: config.ExecutorList{"agy"}, Timeout: time.Second,
	}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	shot := &stubShot{out: `{"decision":"approve","reason":"ok"}`}
	a.BindOneShot(shot)
	d := a.Decide(context.Background(), "Bash: ls", "摘要")
	if shot.last.Model != "" {
		t.Fatalf("无任何模型配置时 OneShotReq.Model 应为空，得到 %q", shot.last.Model)
	}
	if len(d.Attempts) != 1 || d.Attempts[0].Model != ApproverModelDefaultLabel {
		t.Fatalf("attempt.Model 应显式记 %q，得到 %+v", ApproverModelDefaultLabel, d.Attempts)
	}
}

// TestDecideChainModelRegression 钉住故事 2：只配链级 model（无 models）时
// 每候选仍收到同一模型名，与 B376 前一致。
func TestDecideChainModelRegression(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{
		Executor: config.ExecutorList{"agy", "codex"},
		Model:    "chain-m",
		Timeout:  time.Second,
	}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	agyShot := &stubShot{fn: func(context.Context, executor.OneShotReq) (executor.OneShotReply, error) {
		return executor.OneShotReply{Status: executor.OneShotFailed}, errors.New("agy 挂了")
	}}
	codexShot := &stubShot{out: `{"decision":"approve","reason":"ok"}`}
	a.BindOneShotFor("agy", agyShot)
	a.BindOneShotFor("codex", codexShot)
	if d := a.Decide(context.Background(), "Bash: ls", "摘要"); d.Err != nil {
		t.Fatalf("Decide: %+v", d)
	}
	if agyShot.last.Model != "chain-m" || codexShot.last.Model != "chain-m" {
		t.Fatalf("存量链级 model 应逐候选同值，得到 agy=%q codex=%q", agyShot.last.Model, codexShot.last.Model)
	}
}
```

8. **跑红**：`go test ./internal/orchestration/ -count=1 -run 'TestDecide'`。预期 `TestDecidePerCandidateModel`（codex 期望 gpt-6-luna 实得空）、`TestDecideCandidateEmptyEntryFallsBackToChain`、`TestDecideNoModelAnywhereUsesDefaultLabel` 红；`TestDecideChainModelRegression` 与既有 failover 用例绿。确认红因是「选值未实现」而非编译错误。
9. **实现**：加 1f、1g、1i、1j。
10. **跑绿**：`go test ./internal/config/ -count=1` 与 `go test ./internal/orchestration/ -count=1 -run 'TestDecide|TestApprover|TestConsultApprover|TestNilApprover'`。
11. `go build ./...`。

## 6. Task 2：候选实际模型经 payload 落库（两路观测）

文件：`internal/orchestration/contracts.go`、`internal/orchestration/manager.go`、`internal/orchestration/approval_client.go`、`internal/approval/client.go`、`internal/orchestration/approver_test.go`（扩既有 manager 路径测试）、`internal/orchestration/approval_client_test.go`（新测试）。

### 2a. orchestration payload（contracts.go:171）

```go
// B405：Model 记录该候选实际使用的模型名；未指定时 = "默认"（ApproverModelDefaultLabel）。
// addit 字段，omitempty 保证旧事件形态不变。
type ApproverDecisionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	Executor   string `json:"executor,omitempty"`
	Model      string `json:"model,omitempty"`
}
```

### 2b. manager.go 写事件（:2377、:2379-2382、:2392-2395）

per-attempt 循环日志加 `"model", at.Model`，事件加 `Model: at.Model`：

```go
			m.log.Info("审批者候选尝试", "task", taskID, "ticket", ticketID,
				"executor", at.Executor, "decision", at.Decision, "model", at.Model)
			if _, err := m.st.AppendEvent(taskID, proto.EventTypeApproverDecision, ApproverDecisionPayload{
				TicketID: ticketID, Permission: permEventText(ev.Text), Decision: at.Decision,
				Reason: atReason, ElapsedMS: at.ElapsedMS, Executor: at.Executor, Model: at.Model,
			}); err != nil {
```

else 分支（Attempts 为空）加 `Model: d.Model`：

```go
		if _, err := m.st.AppendEvent(taskID, proto.EventTypeApproverDecision, ApproverDecisionPayload{
			TicketID: ticketID, Permission: permEventText(ev.Text), Decision: decision,
			Reason: reason, ElapsedMS: d.ElapsedMS, Executor: d.Executor, Model: d.Model,
		}); err != nil {
```

### 2c. orchestration 投影（approval_client.go:46-50、:78-81）

Decide 钩子补 `Model: d.Model`：

```go
			return approval.ConsultDecision{
				Approve: d.Approve, Reason: d.Reason,
				ElapsedMS: d.ElapsedMS, Err: d.Err,
				Executor: d.Executor, Model: d.Model,
				Attempts: toApprovalAttempts(d.Attempts),
			}
```

`toApprovalAttempts` 补 `Model: a.Model`：

```go
		out = append(out, approval.ApproverAttempt{
			Executor: a.Executor, Decision: a.Decision, Reason: a.Reason,
			Err: a.Err, ElapsedMS: a.ElapsedMS, Model: a.Model,
		})
```

### 2d. approval 镜像与 payload（client.go）

镜像结构加字段（:37、:49）：

```go
type ApproverAttempt struct {
	Executor  string
	Decision  string
	Reason    string
	Err       error
	ElapsedMS int64
	// Model 是本候选实际使用的模型名；未指定时 = "默认"（B405）。
	Model string
}

type ConsultDecision struct {
	Approve   bool
	Reason    string
	ElapsedMS int64
	Err       error
	Executor  string
	// Model 是最终生效候选实际使用的模型名；未指定时 = "默认"（B405）。
	Model    string
	Attempts []ApproverAttempt
}
```

payload（:539）：

```go
type approverDecisionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	Executor   string `json:"executor,omitempty"`
	Model      string `json:"model,omitempty"`
}
```

`consult` per-attempt（:349 日志、:351-358 事件）加 `"model", at.Model` 与 `Model: at.Model`；else 分支（:375-382）加 `Model: dec.Model`：

```go
			c.logger().Info("审批者候选尝试", "task", c.taskID, "ticket", ticketID,
				"executor", at.Executor, "decision", at.Decision, "model", at.Model)
			if _, err := c.hooks.Store.AppendEvent(c.taskID, proto.EventTypeApproverDecision, approverDecisionPayload{
				TicketID:   ticketID,
				Permission: permText,
				Decision:   at.Decision,
				Reason:     reason,
				ElapsedMS:  at.ElapsedMS,
				Executor:   at.Executor,
				Model:      at.Model,
			}); err != nil {
```

```go
		if _, err := c.hooks.Store.AppendEvent(c.taskID, proto.EventTypeApproverDecision, approverDecisionPayload{
			TicketID:   ticketID,
			Permission: permText,
			Decision:   decision,
			Reason:     reason,
			ElapsedMS:  dec.ElapsedMS,
			Executor:   dec.Executor,
			Model:      dec.Model,
		}); err != nil {
```

日志纪律：两处 per-attempt 日志都加 `model`（外部写入 `AppendEvent` 前已有一条）；成功路径不静默。

### 2e. 步骤

1. **先加字段（scaffolding，不赋值）**：只加 4 个 `Model` 字段——`orchestration.ApproverDecisionPayload.Model`（2a 的结构体部分）、`approval.ApproverAttempt.Model` + `approval.ConsultDecision.Model` + `approval.approverDecisionPayload.Model`（2d 的结构体部分）。**不加任何赋值**。此步 `go build ./...` 通过。
2. **写 manager 路径缝测试**：扩 `TestConsultApproverWritesPerAttemptEventsManagerPath`（`approver_test.go:390`）。在 `NewApprover` 的 `config.ApproverConfig{...}` 里加 `Models: map[string]string{"grok": "grok-model"}`，并在既有 `decisions[1]` 断言之后追加：

```go
	if decisions[0].Model != ApproverModelDefaultLabel {
		t.Fatalf("codex 未指定模型，payload.Model 应显式记 %q，得到 %q",
			ApproverModelDefaultLabel, decisions[0].Model)
	}
	if decisions[1].Model != "grok-model" {
		t.Fatalf("grok payload.Model = %q，期望 grok-model", decisions[1].Model)
	}
```

入口 = `Manager.handlePermission`，调用链穿过 `Approver.Decide`（spec 缝 2）；断言从 `Store.AppendEvent` 的真实 JSON 反序列化 `ApproverDecisionPayload` 读出 `Model`。
3. **写 client 路径缝测试**：在 `internal/orchestration/approval_client_test.go` 追加（该文件需补 import `"encoding/json"`；`fmt`/`strings`/`context`/`errors`/`config`/`executor`/`proto`/`store` 已在 import）：

```go
// TestApprovalClientPerCandidateModelReachesEventPayload 钉住 B405 故事 4 的
// 序列化边界：候选级模型从 config → Decide → OneShotReq.Model → attempt →
// toApprovalAttempts 投影 → approval 包 approver_decision payload（真实 JSON），
// 一路不丢。入口 c.Request 经 bindApproval → Approver.Decide（spec 缝 2）。
func TestApprovalClientPerCandidateModelReachesEventPayload(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewApprover(config.ApproverConfig{
		Executor: config.ExecutorList{"opencode"},
		Models:   map[string]string{"opencode": "gpt-6-luna"},
		Timeout:  time.Second,
	}, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	var capturedReq executor.OneShotReq
	app.BindOneShot(&stubShot{
		fn: func(_ context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
			capturedReq = req
			return executor.OneShotReply{
				Text:   fmt.Sprintf(`{"decision":"approve","reason":"safe test","nonce":%q}`, extractNonceForTest(req.Prompt)),
				Status: executor.OneShotOK,
			}, nil
		},
	})
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub()
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, hub, map[string]executor.Adapter{"fake": &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}, cfg, nil, app, newTestGate(t), logger)
	task := &proto.Task{ID: "T-model", RepoPath: t.TempDir(), WorkDir: t.TempDir(),
		State: proto.TaskStateRunning, Executor: "fake"}
	mustCreateTask(t, st, task)
	scope := executor.ApprovalScope{
		Workdir:    task.Workdir(),
		TaskDir:    taskDirOf(m, "T-model"),
		TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, "T-model"),
	}
	c := m.bindApproval("T-model", executor.PolicySnapshot{Version: "v1", TaskID: "T-model", Scope: scope})

	res, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "perm-model",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("approver approve 应为 allow, got %q", res.Decision.Status)
	}
	if capturedReq.Model != "gpt-6-luna" {
		t.Fatalf("OneShotReq.Model = %q，期望 gpt-6-luna", capturedReq.Model)
	}
	events, err := st.EventsFromAsc("T-model", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	for _, e := range events {
		if e.Type != proto.EventTypeApproverDecision {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("解析 approver_decision: %v", err)
		}
		got = append(got, p)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 条 approver_decision，得到 %d: %+v", len(got), got)
	}
	if got[0]["model"] != "gpt-6-luna" {
		t.Fatalf("approver_decision payload model = %v，期望 gpt-6-luna", got[0]["model"])
	}
}
```

4. **跑红**：
   `go test ./internal/orchestration/ -count=1 -run 'TestConsultApproverWritesPerAttemptEventsManagerPath|TestApprovalClientPerCandidateModelReachesEventPayload'`。
   预期两条都红且是**断言红不是编译红**：`decisions[0].Model == ""`（期望 `ApproverModelDefaultLabel`）、`got[0]["model"] == nil`（键缺失）。若出现编译错，说明第 1 步字段没加全。
5. **实现赋值**：加 2b、2c、2d 的事件/投影赋值部分（2a/2d 的结构体字段已在第 1 步就位）。
6. **跑绿**：同 4 命令；再 `go test ./internal/orchestration/ -count=1 -run 'TestConsultApprover|TestApprovalClient'`；`go test ./internal/approval/ -count=1`（既有 client 测试不回归）；`go build ./...`。

### 2f. 注释纪律

- 两个 payload 的 `Model` 注释见 2a/2d（写清「未指定时=默认」与 omitempty 理由）。
- 镜像结构 `Model` 注释见 2d（写清跨包镜像、不 import 的边界）。

## 7. 缺陷族对抗审查（五项检查 #1）

- **生命周期/取消**：本卡不改 `Decide` 的 ctx/超时链路；`modelFor` 是纯函数。候选失败仍逐候选 failover，全失败计 1 次请求级失败（现状不变）。无新孤儿进程。
- **静默失败**：错字 `models` 键从「静默回落」变为启动期硬拒（故事 3）；候选未指定模型时请求 `Model` 为空是**执行者默认**的正常语义，但审计记录显式记「默认」，不会看不出「到底带没带模型」。`OneShot` 失败/en文解析失败的 attempt 也带 Model，不留空。
- **跨平台**：改动是 Go 字符串/映射，不依赖 shell；YAML map 在 go-yaml v3 各平台一致。无 OS 分支。
- **假红/假绿**：Decide 缝测试断言 `stubShot.last.Model`（真实 `OneShotReq`）与 `Attempt.Model`，入口 `Approver.Decide`；failover 用「第一候选真返 error」推进，不靠 mock 顺序假设。config 错字测试断言报错**含错字键**，不是仅断言「有 error」（否则 timeout 校验也会假绿）。payload 测试从 Store 真读 JSON，不读内存结构。
- **门禁绕过**：本卡不碰 permgate/审批门禁；`models` 只影响送进 OneShot 的模型名，不改变 approve/escalate 判定路径。`approver.models` 未启用时不校验（与既有 approver 取值域纪律同款，不是新洞）。

## 8. 序列化边界设问（五项检查 #2）

新增字段 `Model` 从产生到消费的手写投影：

1. `orchestration.ApproverAttempt.Model`（产生：`decideWithCandidate`）→ `toApprovalAttempts`（手写 DTO 转换，approval_client.go:72）→ `approval.ApproverAttempt.Model`。
2. `orchestration.ApproverDecisionPayload.Model`（manager.go 手拼结构体）→ `AppendEvent` JSON。
3. `approval.approverDecisionPayload.Model`（client.go 手拼结构体）→ `AppendEvent` JSON。
4. `approval.ConsultDecision.Model`（bindApproval 钩子赋值）。

断言：Task 2 的 manager 路径测试覆盖 2；`TestApprovalClientPerCandidateModelReachesEventPayload` 覆盖 1 与 3（真实 `bindApproval` + 真实 `Approver` + Store JSON）。两者都用 `omitempty`，用「字段缺失」vs「值为空」的差异：无模型时 attempt 记「默认」（非空），确保序列化后能读出。

## 9. 上下文预算检查（五项检查 #3）

- Task 1 有界：4 个文件，全部在 `internal/config` 与 `internal/orchestration` 的 approver 局部。
- Task 2 有界：4 个生产文件 + 2 个测试文件，均为 payload 直连投影。圈得出。

## 10. 类型标注 / 边界型子系统（五项检查 #4）

spec 明示「L2 无跨子系统契约段，接缝对面无外部 wire 格式」。本卡不是边界型子系统：`approver.models` 是机器本地 config.yaml 键，裁决是进程内方法调用，OneShot 结构与 CLI 通道均不变，故不写真机清单；真机验收（linux-01 重配置造真实审批）归 acceptance 节点、由协调者执行（见 §14）。唯一近似边界是事件 JSON 的 additive `model` 字段，其回归由 §8 的两条真实 JSON 测试承担。

## 11. 接缝覆盖（双向，五项检查 #5）

spec §测试接缝对齐 的缝清单（两条）：

| spec 缝 | 锁它的测试 | 入口符号 | 方向 |
|---|---|---|---|
| 缝 1：`config.Load` 对 `approver.models` 的解析与校验 | Task 1 `TestLoadApproverModels` / `...RejectsNonCandidate` / `...IgnoredWhenDisabled` / `TestApproverModelsRoundTrip` | `config.Load` | 测试 → 缝：入口即缝符号 |
| 缝 2：`Approver.Decide` 候选级选值（落到 `OneShotReq.Model`） | Task 1 `TestDecidePerCandidateModel` / `...EmptyEntryFallsBackToChain` / `...NoModelAnywhereUsesDefaultLabel` / `...ChainModelRegression` | `Approver.Decide` | 测试 → 缝：入口即缝符号 |

缝 → 测试：两条缝各有缝级断言锁住（上表）。无「锁不住的缝」。

**附加锁（内部锁声明）**：Task 2 的 `TestConsultApproverWritesPerAttemptEventsManagerPath`（入口 `Manager.handlePermission`，调用链穿过 `Approver.Decide` 缝）与 `TestApprovalClientPerCandidateModelReachesEventPayload`（入口 `approval.Client.Request` → `bindApproval` 钩子 → 真实 `Approver.Decide`，调用链穿过缝 2）。两条都**不能顶替**上述缝级锁，只附加。合法理由：

- manager 路径测试：它锁的是 spec 缝清单之外的序列化边界（`ApproverDecisionPayload` JSON），**从 `Decide` 缝构造不出该断言**——payload 由 `Manager.consultApprover` 在 `Decide` 之外手拼写入。它的入口调用链穿过缝 2，故也不必降格为内部锁。
- client 路径测试：锁 `approval.approverDecisionPayload` JSON，**从 `Decide` 缝构造不出**——该 JSON 由 approval 包在注入的 `Decide` 钩子之外写入。入口调用链经 `bindApproval` 到真实 `Approver.Decide`，穿过缝 2。

无「若意外先绿就改成直喂 X」的退路。

## 12. 占位符扫描

- 无 TBD / 「适当处理」/ 「同 Task N」。
- 测试全部给出完整可编译代码块；复用既有夹具 `stubShot`、`newTestManagerWithApprover`、`newTestGate`、`chanAdapter`、`taskDirOf`、`looseTempDir`、`extractNonceForTest`，无新建 harness。
- 内部锁声明见 §11（两条附加锁及理由）。

## 13. spec 故事归属

1. linux-01 目标场景（agy 默认 + codex gpt-6-luna，failover 各自取值）→ Task 1 `TestDecidePerCandidateModel`（含 `Attempts[0].Model=默认`）。
2. 存量零漂移（只配链级 model / 只配 executor）→ Task 1 `TestDecideChainModelRegression` + `TestDecideNoModelAnywhereUsesDefaultLabel`。
3. 错字保护（models 键不在候选列表 → 启动期报错点名）→ Task 1 `TestLoadApproverModelsRejectsNonCandidate`。
4. 可观测（每次裁决日志与候选尝试记录带实际模型，未指定显式记「默认」）→ Task 1 `decideWithCandidate` 日志与 `Attempt.Model`；Task 2 两条 payload 缝测试。

## 14. 派发前自审

本计划无需要驱动派发系统自身的验收步骤。真机验收（linux-01 按新能力重配、重启 agentd、造真实审批请求、从裁决日志核每候选实际模型；spec §测试决定）**归协调者执行，不派发**，是 acceptance 节点的事，不是任何 implement task 的验收命令。本 plan 的 implement 验收只有 `go build ./...` 与局部 `go test`。

## 15. 自审三查

1. **spec 覆盖**：四条故事均能指到 task（§13）；spec §实现决定逐条落位——`Models` 键与白名单（Task 1，1a/1c）、启用时校验错字键（1b）、空串=缺条目先回落链级（1f/1g）、链级 Model 退为回落（1f）、签名与 OneShot 结构不变（1j 仅换 `Model` 取值来源）、审计增记模型（1h/2a/2d）、env/timeout/blacklist/failover 不动（未触碰）。
2. **占位符扫描**：见 §12，无占位符。
3. **跨 task 类型/签名一致性**：Task 1 Produces 的 `ApproverAttempt.Model` / `ApproverDecision.Model` 与 Task 2 消费处逐字对齐（`at.Model`、`d.Model`、`a.Model`、`dec.Model`）；两个 payload 的 `json:"model,omitempty"` 一致；`approval.ApproverAttempt` 与 `orchestration.ApproverAttempt` 字段名/类型一致（均 `Model string`）。

## 16. 收口项（非本 plan task）

- `docs/roadmap.md:32` 「每候选单独 `approver.model`」残余行由本卡 finish 节点回写划掉（spec §Out of Scope 收口项）。
- spec 记录的 linux-01 临时配置（备份 `config.yaml.bak-approver-20260923`）本卡不动；是否按新能力重配属真机验收，由人决定。
- `HashPolicyVersion`（approval_client.go:89）不含 approver 模型（今天链级 model 也不含），本卡不改——模型名不入策略哈希是既有不变式，不在本卡范围。
