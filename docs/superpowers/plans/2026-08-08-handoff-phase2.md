# Handoff 二期实现计划（审批链 / executor 选择 / dispatch 扩展 / 可观测性）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 spec `docs/superpowers/specs/2026-08-08-handoff-approver-dispatch-observability-design.md` 的四个功能：agentd 侧廉价模型审批链、dispatch 指定 executor+model 机制、dispatch 分支/worktree/prompt/name 参数、attach 终端实况与默认弹终端。

**Architecture:** 在现有 Manager 中枢上做四处扩展：(1) handlePermission 前置审批链（黑名单→审批者 CLI→escalate 兜底）；(2) Manager 的单 adapter 字段改为注册表 map + 缺省名路由；(3) PrepareBranch 泛化为 PrepareWorkspace（分支×worktree 正交矩阵）；(4) CLI 侧 attach 语义翻转 + osascript 弹终端。所有新配置进 `~/.handoff/config.yaml`（严格解析，需同步已知键清单）。

**Tech Stack:** Go 1.x、cobra、SQLite（database/sql）、yaml.v3、tmux/ssh/osascript（exec.Command）。

## Global Constraints

- 全部日志走 `slog`（manager/store 模式：`m.log` / 包级 `log()`），**禁止 fmt.Printf 做日志**；CLI 面向用户的输出用 `fmt.Fprintln(cmd.OutOrStdout(), ...)`。
- 每个新文件顶部写职责+边界注释；每个导出函数写参数/返回/注意事项注释；复杂逻辑写中文「为什么」注释（见既有文件风格，如 workspace.go）。
- git 全部经 `gitRun`（exec 参数列表，不拼 shell）；以 `-` 开头的 rev/branch 参数一律拒绝（参数注入面，参照 ErrBadBaseBranch）。
- 状态迁移只经 store CAS（transit/transitBestEffort），不新增状态写入者。
- adapter 不碰 store（executor 包边界注释）；审批者逻辑放 agentd 侧，不放 adapter。
- 审批者 fail-closed：超时/解析失败/非零退出/命令不存在一律按 escalate。
- 测试命令统一 `go test ./... `；提交信息用中文、feat/fix/docs 前缀（见 git log 惯例）。
- 每个任务完成前按 instrumenting-code 清单自检：错误分支带上下文、成功路径不静默、无 print 式日志、新文件头注释、导出方法注释。

---

### Task 1: proto + store —— Task 新字段与事件类型

**Files:**
- Modify: `internal/proto/proto.go`（Task 结构、EventType 常量）
- Modify: `internal/store/store.go`（DDL、迁移、CreateTask、scan、白名单）
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: 现有 `proto.Task` / `store.CreateTask` / `store.GetTask`。
- Produces（后续任务全部依赖）：
  - `proto.Task` 新增字段：`Name string \`json:"name"\``、`Executor string \`json:"executor"\``、`Model string \`json:"model"\``、`WorkDir string \`json:"work_dir"\``（空=原地模式）、`WorktreeManaged bool \`json:"worktree_managed"\``；
  - 方法 `func (t *Task) Workdir() string`：`WorkDir` 非空返回它，否则返回 `RepoPath`（executor cwd 与审阅命令的统一取值点）；
  - 事件类型常量：`EventTypeApproverDecision EventType = "approver_decision"`、`EventTypeApproverDisabled EventType = "approver_disabled"`。

- [ ] **Step 1: 写失败测试**（store_test.go 追加）

```go
func TestCreateTaskPersistsPhase2Fields(t *testing.T) {
	st := openTestStore(t) // 若无此 helper，参照本文件既有测试的 store.Open(t.TempDir()+"/db") 写法
	task := &proto.Task{
		ID: "t1", RepoPath: "/repo", State: proto.TaskStatePending,
		Name: "重构支付", Executor: "opencode", Model: "gpt-5-mini",
		WorkDir: "/wt/t1", WorktreeManaged: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "重构支付" || got.Executor != "opencode" || got.Model != "gpt-5-mini" ||
		got.WorkDir != "/wt/t1" || !got.WorktreeManaged {
		t.Fatalf("二期字段未持久化: %+v", got)
	}
	if got.Workdir() != "/wt/t1" {
		t.Fatalf("Workdir() 应返回 WorkDir，得到 %s", got.Workdir())
	}
}

func TestWorkdirFallsBackToRepoPath(t *testing.T) {
	tk := proto.Task{RepoPath: "/repo"}
	if tk.Workdir() != "/repo" {
		t.Fatalf("WorkDir 为空时 Workdir() 应回退 RepoPath")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run TestCreateTaskPersistsPhase2Fields -v`
Expected: 编译失败（Task 无 Name 字段）。

- [ ] **Step 3: 实现**

proto.go：Task 增加五字段与 `Workdir()` 方法（doc 注释说明「WorkDir 空=原地模式，审阅命令与 executor cwd 统一走本方法」）；EventType 增加两常量（注释：approver_decision 只入库不唤醒、approver_disabled 表示本任务审批者连续失败已停用）。

store.go：
- tasks DDL 追加列：`name TEXT NOT NULL DEFAULT ''`、`executor TEXT NOT NULL DEFAULT ''`、`model TEXT NOT NULL DEFAULT ''`、`work_dir TEXT NOT NULL DEFAULT ''`、`worktree_managed INTEGER NOT NULL DEFAULT 0`；
- 旧库迁移：参照 `delivered_at` 的 ALTER TABLE + duplicate column 容忍写法，5 列各一条；
- CreateTask 的 INSERT 与 GetTask/ListTasks 的 scan 同步补列（worktree_managed 用 int 转 bool）；
- `allowedTaskFields` 不加新键——五个新字段全部创建时已知，随 CreateTask 写入（维持「创建期只写创建时已知的字段」约定；branch 维持现状不动）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store/ ./internal/proto/ -v`
Expected: PASS（含既有测试不回归）。

- [ ] **Step 5: 加关键节点日志**

store.Open 的迁移失败分支已有错误包装即可；CreateTask 无新日志需求（既有模式无每行日志）。确认新增代码错误路径都带 task id 上下文。

- [ ] **Step 6: 加注释**

五个新列在 DDL 旁注释用途；迁移块注释「为什么逐列 ALTER + 容忍 duplicate column」（沿用 delivered_at 的说明句式）。

- [ ] **Step 7: Commit**

```bash
git add internal/proto/proto.go internal/store/store.go internal/store/store_test.go
git commit -m "feat: Task 二期字段（name/executor/model/work_dir/worktree_managed）与审批事件类型"
```

---

### Task 2: config —— approver / executor / terminal 三节配置

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces：
  - `Config` 新增字段：`Approver ApproverConfig`、`Executor ExecutorConfig`、`Terminal TerminalConfig`；
  - `type ApproverConfig struct { Executor string; Model string; Timeout time.Duration; Blacklist []string }`（yaml 键：approver.executor/model/timeout/blacklist；Executor 空=不启用审批链）；
  - `type ExecutorConfig struct { Default string; Model string }`（yaml 键：executor.default/model）；
  - `type TerminalConfig struct { Auto bool }`（yaml 键：terminal.auto）；
  - 默认值（Load 初始 cfg 字面量预置，yaml 覆盖式解码）：`Approver.Timeout=60s`、`Executor.Default="opencode"`、`Terminal.Auto=true`。

- [ ] **Step 1: 写失败测试**

```go
func TestLoadPhase2Sections(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte(`
token: abc
approver:
  executor: claude
  model: haiku
  blacklist:
    - "kubectl .*delete"
executor:
  default: opencode
  model: cheap/model
terminal:
  auto: false
`), 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Approver.Executor != "claude" || cfg.Approver.Model != "haiku" {
		t.Fatalf("approver 解析错误: %+v", cfg.Approver)
	}
	if cfg.Approver.Timeout != 60*time.Second {
		t.Fatalf("approver.timeout 缺省应为 60s，得到 %s", cfg.Approver.Timeout)
	}
	if len(cfg.Approver.Blacklist) != 1 {
		t.Fatalf("blacklist 解析错误")
	}
	if cfg.Executor.Default != "opencode" || cfg.Executor.Model != "cheap/model" {
		t.Fatalf("executor 解析错误: %+v", cfg.Executor)
	}
	if cfg.Terminal.Auto {
		t.Fatalf("terminal.auto=false 未生效")
	}
}

func TestLoadPhase2Defaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(p) // 首次运行生成默认配置
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Approver.Executor != "" || cfg.Approver.Timeout != 60*time.Second {
		t.Fatalf("approver 默认值错误: %+v", cfg.Approver)
	}
	if cfg.Executor.Default != "opencode" || !cfg.Terminal.Auto {
		t.Fatalf("executor/terminal 默认值错误")
	}
}

func TestValidateRejectsBadApproverTimeout(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: a\napprover:\n  executor: claude\n  timeout: -1s\n"), 0o600)
	if _, err := config.Load(p); err == nil {
		t.Fatalf("approver.timeout 非正值应被拒绝")
	}
}

func TestValidateRejectsBadBlacklistRegex(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: a\napprover:\n  executor: claude\n  blacklist:\n    - \"([\"\n"), 0o600)
	if _, err := config.Load(p); err == nil {
		t.Fatalf("非法正则应在启动期被拒绝，而不是运行期 panic")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestLoadPhase2 -v`
Expected: 编译失败（无 Approver 字段）。

- [ ] **Step 3: 实现**

- 三个结构体 + Config 字段；Load 初始字面量补默认值（覆盖式解码保默认）；
- validate 追加：`Approver.Timeout <= 0` 报错（仅在 Approver.Executor 非空时校验超时与黑名单）；黑名单每条 `regexp.Compile` 预检，报错带出错的规则原文；
- decodeStrict 的错误文案更新已知键清单：`listen/token/datadir/stalltimeout/targets{addr,token}/approver{executor,model,timeout,blacklist}/executor{default,model}/terminal{auto}`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -v`
Expected: PASS。

- [ ] **Step 5: 加关键节点日志**

validate 失败分支沿用既有 `log().Error("配置校验失败", ...)` 出口（Load 已统一打）；黑名单编译失败错误文本含规则原文与序号。

- [ ] **Step 6: 加注释**

ApproverConfig doc 注释写清「Executor 空=不启用审批链（现行为）」「Model 空=用执行者自身默认模型」；TerminalConfig 注释「Auto 默认 true，仅 darwin 生效，其余平台降级打印命令」。

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: 配置新增 approver/executor/terminal 三节（含默认值与启动期校验）"
```

---

### Task 3: executor one-shot 调用映射

**Files:**
- Create: `internal/executor/oneshot.go`
- Test: `internal/executor/oneshot_test.go`

**Interfaces:**
- Produces：`func OneShotArgs(executorName, model, prompt string) ([]string, error)` —— 返回一次性调用 argv（prompt 作末位参数）；未知执行者返回错误并列出支持项。映射：`opencode` → `["opencode","run","-m",model,prompt]`（model 空则省 `-m`）；`claude` → `["claude","-p","--model",model,prompt]`（model 空则省 `--model`）。

- [ ] **Step 1: 写失败测试**

```go
package executor_test

import (
	"reflect"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

func TestOneShotArgs(t *testing.T) {
	cases := []struct {
		name, exec, model, prompt string
		want                      []string
		wantErr                   bool
	}{
		{"opencode 带模型", "opencode", "m1", "p", []string{"opencode", "run", "-m", "m1", "p"}, false},
		{"opencode 无模型", "opencode", "", "p", []string{"opencode", "run", "p"}, false},
		{"claude 带模型", "claude", "haiku", "p", []string{"claude", "-p", "--model", "haiku", "p"}, false},
		{"claude 无模型", "claude", "", "p", []string{"claude", "-p", "p"}, false},
		{"未知执行者", "grok", "", "p", nil, true},
	}
	for _, c := range cases {
		got, err := executor.OneShotArgs(c.exec, c.model, c.prompt)
		if c.wantErr != (err != nil) {
			t.Fatalf("%s: err=%v", c.name, err)
		}
		if !c.wantErr && !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/executor/ -run TestOneShotArgs -v`，Expected: 编译失败。

- [ ] **Step 3: 实现** — switch 三分支；default 错误文本 `未知执行者 %q（one-shot 支持 opencode/claude）`。文件头注释：职责=「执行者名字 → 一次性 CLI argv 的唯一映射点，审批者与未来的降级调用共用」；边界=「不执行命令、不读配置；grok 等新执行者在此登记」。

- [ ] **Step 4: 跑测试确认通过** — Run: `go test ./internal/executor/ -v`，Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/executor/oneshot.go internal/executor/oneshot_test.go
git commit -m "feat: executor one-shot 调用映射（opencode/claude）"
```

（纯映射函数无 I/O，无日志需求；注释步已并入 Step 3。）

---

### Task 4: 审批者核心（黑名单 + CLI 裁决 + fail-closed）

**Files:**
- Create: `internal/agentd/approver.go`
- Test: `internal/agentd/approver_test.go`

**Interfaces:**
- Consumes: `config.ApproverConfig`（Task 2）、`executor.OneShotArgs`（Task 3）。
- Produces（Task 8 依赖）：
  - `type ApproverDecision struct { Approve bool; Reason string; ElapsedMS int64; Err error }`（Err 非 nil 表示裁决本身失败——区别于干净的 escalate，供连续失败计数）；
  - `type Approver struct { ... }`，构造：`func NewApprover(cfg config.ApproverConfig, log *slog.Logger) (*Approver, error)` —— cfg.Executor 为空返回 `(nil, nil)`；黑名单（内置+自定义）在此编译；
  - `func (a *Approver) Blacklisted(permission string) (hit bool, rule string)`；
  - `func (a *Approver) Decide(ctx context.Context, permission, taskSummary string) ApproverDecision`；
  - 测试缝：`Approver.runCmd func(ctx context.Context, argv []string) (string, error)` 非导出字段，NewApprover 默认填真实现（exec.CommandContext + CombinedOutput），测试直接改字段注入。

- [ ] **Step 1: 写失败测试**

```go
package agentd

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
)

func newTestApprover(t *testing.T, out string, err error) *Approver {
	a, aerr := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, slog.Default())
	if aerr != nil {
		t.Fatal(aerr)
	}
	a.runCmd = func(ctx context.Context, argv []string) (string, error) { return out, err }
	return a
}

func TestApproverNilWhenUnconfigured(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{}, slog.Default())
	if err != nil || a != nil {
		t.Fatalf("未配置应返回 (nil,nil)，得到 %v %v", a, err)
	}
}

func TestBlacklistBuiltinAndCustom(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{
		Executor: "opencode", Timeout: time.Second,
		Blacklist: []string{`kubectl .*delete`},
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"Bash: rm -rf node_modules", "Bash: git push --force origin main",
		"Bash: sudo systemctl restart nginx", "Bash: git reset --hard HEAD~3",
		"Bash: psql -c 'DROP TABLE users'", "Bash: deploy to production",
		"Bash: kubectl pods delete --all",
	} {
		if hit, _ := a.Blacklisted(s); !hit {
			t.Fatalf("应命中黑名单: %s", s)
		}
	}
	if hit, _ := a.Blacklisted("Bash: go test ./..."); hit {
		t.Fatalf("go test 不应命中黑名单")
	}
}

func TestDecideApprove(t *testing.T) {
	a := newTestApprover(t, "思考过程...\n{\"decision\":\"approve\",\"reason\":\"项目内读写\"}\n", nil)
	d := a.Decide(context.Background(), "Edit: main.go", "修 bug")
	if !d.Approve || d.Err != nil {
		t.Fatalf("应 approve: %+v", d)
	}
}

func TestDecideEscalate(t *testing.T) {
	a := newTestApprover(t, `{"decision":"escalate","reason":"拿不准"}`, nil)
	d := a.Decide(context.Background(), "Bash: curl ...", "")
	if d.Approve || d.Err != nil || d.Reason != "拿不准" {
		t.Fatalf("应干净 escalate: %+v", d)
	}
}

// fail-closed 三连：命令失败 / 输出无 JSON / decision 取值非法，全部 escalate 且 Err 非 nil
func TestDecideFailClosed(t *testing.T) {
	for name, a := range map[string]*Approver{
		"命令失败":  newTestApprover(t, "", errors.New("exit 1")),
		"无JSON":  newTestApprover(t, "我觉得可以批", nil),
		"取值非法": newTestApprover(t, `{"decision":"deny"}`, nil),
	} {
		if d := a.Decide(context.Background(), "x", ""); d.Approve || d.Err == nil {
			t.Fatalf("%s: 应 fail-closed escalate: %+v", name, d)
		}
	}
}

func TestDecidePromptContainsContext(t *testing.T) {
	var got []string
	a := newTestApprover(t, `{"decision":"approve"}`, nil)
	a.runCmd = func(ctx context.Context, argv []string) (string, error) { got = argv; return `{"decision":"approve"}`, nil }
	a.Decide(context.Background(), "PERM-TEXT", "TASK-SUMMARY")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "PERM-TEXT") || !strings.Contains(joined, "TASK-SUMMARY") {
		t.Fatalf("裁决 prompt 应含权限原文与任务摘要: %v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/agentd/ -run 'TestApprover|TestBlacklist|TestDecide' -v`，Expected: 编译失败。

- [ ] **Step 3: 实现**

内置黑名单（编译为 `(?i)` 不区分大小写）：

```go
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
```

Decide 流程：组装裁决 prompt（固定模板，见下）→ `executor.OneShotArgs(a.executorName, a.model, prompt)` → `runCmd`（ctx 带 `a.timeout` 截止）→ 从输出**由后向前**逐行 `json.Unmarshal` 到 `struct{ Decision, Reason string }`，首个合法行生效；decision 仅接受 `approve`/`escalate`，其余按 Err。裁决 prompt 模板：

```
你是代码任务的权限审批者。任务背景：<taskSummary>
权限请求：<permission>
仅当该操作明显安全（任务仓库内读写、跑测试/构建、装项目依赖、常规 git 提交）时才批准。
任何不确定、可能破坏数据、影响范围超出任务仓库的操作，必须升级给上级审核者。
只输出一行 JSON，不要输出其他内容：{"decision":"approve"} 或 {"decision":"escalate","reason":"简要原因"}
```

真实 runCmd：`exec.CommandContext(ctx, argv[0], argv[1:]...)` + `CombinedOutput`，输出上限 64KiB 截断（防失控输出驻留内存）。

- [ ] **Step 4: 跑测试确认通过** — Run: `go test ./internal/agentd/ -run 'TestApprover|TestBlacklist|TestDecide' -v`，Expected: PASS。

- [ ] **Step 5: 加关键节点日志**

- Decide 进入：Info（permission 截断 80、executor、model）；
- 裁决完成：Info（decision、reason 截断 80、elapsed_ms）；
- 失败分支：Error（cause、输出尾部截断 200）——这是「审批者为什么没批」的第一现场；
- Blacklisted 命中：Info（rule、permission 截断 80）。

- [ ] **Step 6: 加注释**

文件头：职责=「权限请求的前置裁决：黑名单硬规则 + 廉价模型 CLI 裁决」；边界=「无 deny 权——出口只有 approve/escalate；不写 store、不碰 adapter，纯裁决计算，落库与回传由 manager 完成」。内置黑名单每条注释拦截意图；「由后向前找 JSON 行」注释 why（模型常在 JSON 前输出思考文本）。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/approver.go internal/agentd/approver_test.go
git commit -m "feat: 审批者核心——内置黑名单、one-shot CLI 裁决、fail-closed"
```

---

### Task 5: Manager adapter 注册表

**Files:**
- Modify: `internal/agentd/manager.go`（Manager 结构、NewManager、全部 `m.ad.` 调用点：约 9 处，见 grep `m\.ad\.`）
- Modify: `cmd/agentd.go`（接线）
- Test: `internal/agentd/manager_test.go`（既有测试改构造调用 + 新路由测试）

**Interfaces:**
- Consumes: `config.ExecutorConfig`（Task 2）、`proto.Task.Executor`（Task 1）。
- Produces（Task 7/8 依赖）：
  - `NewManager(st *store.Store, hub *Hub, ads map[string]executor.Adapter, cfg *config.Config, log *slog.Logger) *Manager` —— 第三参从单 Adapter 改为注册表；
  - `func (m *Manager) adapterFor(taskID string) (executor.Adapter, error)` —— GetTask 读 task.Executor（空回退 `m.cfg.Executor.Default`）查注册表；
  - `func (m *Manager) resolveExecutor(name string) (string, executor.Adapter, error)` —— dispatch 期用：name 空回退缺省，未注册返回 `errBadDispatchRequest` 包装错误并列出已注册名。

- [ ] **Step 1: 写失败测试**

```go
// manager_test.go 追加。既有测试的 NewManager(st, hub, ad, cfg, log) 调用
// 统一改为 NewManager(st, hub, map[string]executor.Adapter{"fake": ad}, cfg, log)，
// 且测试用 cfg.Executor.Default = "fake"（一处 helper 改完全体生效的话优先改 helper）。

func TestAdapterForRoutesByTaskExecutor(t *testing.T) {
	adA, adB := fake.New(nil), fake.New(nil)
	m, st := newTestManagerWithAds(t, map[string]executor.Adapter{"a": adA, "b": adB}, "a")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "b", State: proto.TaskStateRunning})
	mustCreateTask(t, st, &proto.Task{ID: "t2", RepoPath: "/r", Executor: "", State: proto.TaskStateRunning})
	if got, _ := m.adapterFor("t1"); got != adB {
		t.Fatalf("t1 应路由到 b")
	}
	if got, _ := m.adapterFor("t2"); got != adA {
		t.Fatalf("executor 为空应回退缺省 a")
	}
}

func TestResolveExecutorRejectsUnknown(t *testing.T) {
	m, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"a": fake.New(nil)}, "a")
	if _, _, err := m.resolveExecutor("nope"); err == nil || !strings.Contains(err.Error(), "a") {
		t.Fatalf("未注册执行者应报错并列出可用项: %v", err)
	}
}
```

（`newTestManagerWithAds` / `mustCreateTask` 若无现成等价物则新建 helper；照抄本文件既有 manager 测试的 store/hub 搭建方式。）

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/agentd/ -run 'TestAdapterFor|TestResolveExecutor' -v`，Expected: 编译失败。

- [ ] **Step 3: 实现**

- Manager：`ad executor.Adapter` → `ads map[string]executor.Adapter`；
- 9 处 `m.ad.X(...)` 调用点：有 task 快照在手的（Dispatch）直接用 resolveExecutor 返回的 adapter；只有 taskID 的（Continue/Done/mediate/waitPermission/waitQuestion/RelayAnswer/ResumeTask）经 `adapterFor(taskID)`，取失败时 Error 日志 + 按「executor 已不在」语义处置（与现 `ErrTaskNotRunning` 分支同路）；
- `m.ad.(restorer)` 断言（manager.go:1038 ResumeTask）改为对 `adapterFor` 结果断言；
- cmd/agentd.go：`ads := map[string]executor.Adapter{"opencode": opencode.New(logger), "fake": fake.New(nil)}` 两个都注册；`--executor` flag 语义改为「覆盖 cfg.Executor.Default」（flag 缺省空=用配置值），flag 帮助文案同步。

- [ ] **Step 4: 跑全量测试** — Run: `go test ./...`，Expected: PASS（既有测试构造已批量更新）。

- [ ] **Step 5: 加关键节点日志** — adapterFor 失败分支 Error（task、executor 名、已注册名列表）；agentd 启动日志追加 `"default_executor"` 字段。

- [ ] **Step 6: 加注释** — Manager 结构注释更新（ads 只读 map，构造后不变，并发安全依旧）；adapterFor doc 注释写明「task.Executor 空=缺省执行者（老任务兼容）」。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go cmd/agentd.go
git commit -m "feat: Manager adapter 注册表与按任务路由（executor 选择机制）"
```

---

### Task 6: workspace —— PrepareWorkspace 与 worktree 管理

**Files:**
- Modify: `internal/agentd/workspace.go`（新增 PrepareWorkspace/RemoveManagedWorktree；PrepareBranch 降为内部路径之一）
- Test: `internal/agentd/workspace_test.go`

**Interfaces:**
- Consumes: 既有 `gitRun` / `taskBranch` / `ErrDirtyWorktree` / `ErrRepoUnusable`。
- Produces（Task 7/9 依赖）：

```go
// WorkspaceReq 描述 dispatch 的工作区诉求（分支 × worktree 两个正交维度）。
type WorkspaceReq struct {
	Repo         string // 主仓库路径
	TaskID       string
	Branch       string // 已存在分支（与 NewBranch 互斥）
	NewBranch    string // 新建分支名（空且 Branch 空 = 自动 handoff/<id8>）
	Base         string // 新分支起点，仅与 NewBranch/自动分支连用；空 = HEAD
	Worktree     string // 已存在 worktree 路径（与 NewWorktree 互斥）
	NewWorktree  bool
	WorktreesDir string // agentd 管理的 worktree 根目录（DataDir/worktrees）
}

// Workspace 是准备完成的工作区结果。
type Workspace struct {
	Branch  string
	WorkDir string // executor cwd 与审阅命令目录；原地模式 = Repo
	Managed bool   // WorkDir 是 agentd 创建的 worktree（done 时代删）
}

func PrepareWorkspace(req WorkspaceReq) (Workspace, error)
func RemoveManagedWorktree(repo, workdir string) error // git -C repo worktree remove workdir
var ErrBadWorkspaceReq = errors.New("工作区参数非法")   // 互斥冲突/分支不存在/路径不是 worktree/rev 以 - 开头
```

- [ ] **Step 1: 写失败测试**（用 t.TempDir 建真实 git 仓库，参照本文件既有测试的 init/commit helper；无则新建 `initTestRepo(t) string`：git init + 一次空提交，配置 user.name/email）

```go
func TestPrepareWorkspaceDefaultKeepsCurrentBehavior(t *testing.T) {
	repo := initTestRepo(t)
	ws, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "abcdefgh-rest"})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Branch != "handoff/abcdefgh" || ws.WorkDir != repo || ws.Managed {
		t.Fatalf("缺省行为应与一期一致: %+v", ws)
	}
}

func TestPrepareWorkspaceExistingBranch(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "feat-x")
	ws, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "t1", Branch: "feat-x"})
	if err != nil || ws.Branch != "feat-x" {
		t.Fatalf("应切到已存在分支: %+v %v", ws, err)
	}
	if cur := gitOut(t, repo, "branch", "--show-current"); cur != "feat-x" {
		t.Fatalf("HEAD 应在 feat-x，得到 %s", cur)
	}
}

func TestPrepareWorkspaceBranchNotExist(t *testing.T) {
	repo := initTestRepo(t)
	if _, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "t1", Branch: "ghost"}); !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("不存在的分支应拒发: %v", err)
	}
}

func TestPrepareWorkspaceNewBranchWithBase(t *testing.T) {
	repo := initTestRepo(t)
	base := gitOut(t, repo, "rev-parse", "HEAD")
	writeAndCommit(t, repo, "f.txt", "x") // HEAD 前进一格
	ws, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "t1", NewBranch: "feat-y", Base: base})
	if err != nil || ws.Branch != "feat-y" {
		t.Fatal(err)
	}
	if head := gitOut(t, repo, "rev-parse", "HEAD"); head != base {
		t.Fatalf("新分支应从 base 起点: head=%s base=%s", head, base)
	}
}

func TestPrepareWorkspaceNewWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wtDir := filepath.Join(t.TempDir(), "worktrees")
	ws, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "abcdefgh-x", NewWorktree: true, WorktreesDir: wtDir})
	if err != nil {
		t.Fatal(err)
	}
	if !ws.Managed || ws.WorkDir != filepath.Join(wtDir, "abcdefgh") {
		t.Fatalf("managed worktree 路径错误: %+v", ws)
	}
	if cur := gitOut(t, ws.WorkDir, "branch", "--show-current"); cur != "handoff/abcdefgh" {
		t.Fatalf("worktree 内应在任务分支: %s", cur)
	}
	// 同 repo 第二个任务并行派发不冲突（一期原地模式做不到）
	if _, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "second-t", NewWorktree: true, WorktreesDir: wtDir}); err != nil {
		t.Fatalf("同 repo 并行派发应成功: %v", err)
	}
}

func TestPrepareWorkspaceNewWorktreeAllowsDirtyMainRepo(t *testing.T) {
	repo := initTestRepo(t)
	os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x"), 0o644) // 主仓脏
	if _, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "t1", NewWorktree: true,
		WorktreesDir: filepath.Join(t.TempDir(), "w")}); err != nil {
		t.Fatalf("new-worktree 不应受主仓脏工作区限制: %v", err)
	}
}

func TestPrepareWorkspaceExistingWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt1")
	gitT(t, repo, "worktree", "add", "-b", "pre-branch", wt)
	ws, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "abcdefgh-x", Worktree: wt})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Managed || ws.WorkDir != wt || ws.Branch != "handoff/abcdefgh" {
		t.Fatalf("用户自带 worktree: Managed 应为 false 且在其中开任务分支: %+v", ws)
	}
	// 非 worktree 路径拒发
	if _, err := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "t2", Worktree: t.TempDir()}); !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("非本 repo worktree 应拒发: %v", err)
	}
}

func TestPrepareWorkspaceMutualExclusionAndInjection(t *testing.T) {
	repo := initTestRepo(t)
	for name, req := range map[string]WorkspaceReq{
		"branch×new-branch":     {Repo: repo, TaskID: "t", Branch: "a", NewBranch: "b"},
		"worktree×new-worktree": {Repo: repo, TaskID: "t", Worktree: "/x", NewWorktree: true},
		"base 无 new-branch":     {Repo: repo, TaskID: "t", Base: "HEAD~1"},
		"分支名 - 开头":            {Repo: repo, TaskID: "t", Branch: "-evil"},
		"base - 开头":             {Repo: repo, TaskID: "t", NewBranch: "b", Base: "--evil"},
	} {
		if _, err := PrepareWorkspace(req); !errors.Is(err, ErrBadWorkspaceReq) {
			t.Fatalf("%s 应拒发: %v", name, err)
		}
	}
}

func TestRemoveManagedWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wtDir := filepath.Join(t.TempDir(), "w")
	ws, _ := PrepareWorkspace(WorkspaceReq{Repo: repo, TaskID: "abcdefgh-x", NewWorktree: true, WorktreesDir: wtDir})
	if err := RemoveManagedWorktree(repo, ws.WorkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("worktree 目录应已删除")
	}
	// 分支保留（spec：只删工作树不删分支）
	if out := gitOut(t, repo, "branch", "--list", "handoff/abcdefgh"); out == "" {
		t.Fatalf("任务分支不应被删除")
	}
}
```

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/agentd/ -run TestPrepareWorkspace -v`，Expected: 编译失败。

- [ ] **Step 3: 实现**

PrepareWorkspace 分层：
1. **参数校验**（纯内存）：互斥、Base 依赖、`-` 开头拒绝（Branch/NewBranch/Base/Worktree 四项都查）——全部包 `ErrBadWorkspaceReq`；
2. **分支名决议**：`branch = req.Branch | req.NewBranch | taskBranch(req.TaskID)`；`Branch` 模式先 `git rev-parse --verify --quiet refs/heads/<name>` 验存在；
3. **按 worktree 维度分派**：
   - `NewWorktree`：path=`filepath.Join(WorktreesDir, id8(TaskID))`（id8 复用 taskBranch 的截断逻辑，提为小函数）；`MkdirAll(WorktreesDir, 0o700)`；已存在分支 → `git -C repo worktree add <path> <branch>`；新建/自动 → `git -C repo worktree add -b <branch> <path> <base|HEAD>`；无脏检查（新树天然净）；
   - `Worktree`：归属校验——`git -C <wt> rev-parse --path-format=absolute --git-common-dir` 与 repo 侧同命令结果比对（EvalSymlinks 归一后相等才放行），不等包 ErrBadWorkspaceReq；对该 worktree 做脏检查（status --porcelain）；在其中 checkout（已存在分支）或 checkout -b（新建/自动，带 base）；
   - 原地：脏检查 repo（沿用 PrepareBranch 现逻辑）→ checkout / checkout -b；
4. 现 `PrepareBranch` 函数体收编为原地+自动分支的内部路径，导出符号保留一个过渡版本或直接删除并更新 Dispatch 调用（Task 7 会改调用点；本任务内先保留 PrepareBranch 不破坏编译）。

RemoveManagedWorktree：`gitRun(ctx, repo, "worktree", "remove", workdir)`；错误原样带 stderr 返回（是否降级由调用方定）。

- [ ] **Step 4: 跑测试确认通过** — Run: `go test ./internal/agentd/ -run 'TestPrepareWorkspace|TestRemoveManaged' -v`，Expected: PASS。

- [ ] **Step 5: 加关键节点日志** — PrepareWorkspace 进入（req 全字段）、每个 git 动作前后已有 gitRun 统一日志、完成 Info（branch、workdir、managed）；校验拒绝分支 Warn（哪条规则、哪个值）。RemoveManagedWorktree 进入/完成/失败三条。

- [ ] **Step 6: 加注释** — 文件头「职责」段补 PrepareWorkspace；正交矩阵（3 分支模式 × 3 工作树模式）在函数 doc 注释里列全 9 种组合的行为表；「为什么 NewWorktree 免脏检查」「为什么归属校验用 git-common-dir 比对」各一条 why 注释。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_test.go
git commit -m "feat: PrepareWorkspace——分支×worktree 正交工作区准备与 managed worktree 清理"
```

---

### Task 7: Dispatch 全链路扩展（manager → server → client → CLI）

**Files:**
- Modify: `internal/agentd/manager.go`（DispatchReq、Dispatch）
- Modify: `internal/agentd/server.go`（handleDispatch 请求体）
- Modify: `internal/client/client.go`（Dispatch 签名）
- Modify: `cmd/dispatch.go`（flags）
- Modify: `internal/agentd/server.go` 的 `taskRepoOrErr`（改用 `task.Workdir()`）
- Modify: `internal/executor/opencode/adapter.go`（cwd 取 `req.Task.Workdir()`；grep 现用 `RepoPath` 的点一并换）
- Test: `internal/agentd/manager_test.go`、`internal/agentd/integration_test.go`、`cmd/` 既有测试

**Interfaces:**
- Consumes: Task 1 字段、Task 5 `resolveExecutor`、Task 6 `PrepareWorkspace`。
- Produces：
  - `DispatchReq` 新增：`Prompt, Name, Executor, Model, Branch, NewBranch, Base, Worktree string; NewWorktree bool`；`PlanB64` 允许为空（与 Prompt 至少其一）；
  - HTTP body 新键（snake_case）：`prompt/name/executor/model/branch/new_branch/base/worktree/new_worktree`；
  - `client.DispatchOpts struct`（字段与 body 一一对应）+ `func (c *Client) Dispatch(ctx context.Context, opts DispatchOpts) (*proto.Task, error)`；
  - CLI：`handoff dispatch [plan.md] --repo ... [--prompt] [--name] [--executor] [--model] [--branch|--new-branch [--base]] [--worktree|--new-worktree] [--no-terminal]`（`--no-terminal` 本任务只注册 flag，行为在 Task 12）。

- [ ] **Step 1: 写失败测试**（manager 层为主，fake adapter）

```go
func TestDispatchPromptOnly(t *testing.T) {
	m, st := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "把 README 安装命令改成 brew", Target: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.PlanSummary == "" || !strings.Contains(task.PlanSummary, "README") {
		t.Fatalf("prompt-only 派发应以 prompt 生成摘要: %q", task.PlanSummary)
	}
	if task.Name != "把 README 安装命令改成 brew"[:len("把 README 安装命令改成 brew")] && task.Name == "" {
		t.Fatalf("name 应从 prompt 派生: %q", task.Name)
	}
}

func TestDispatchRequiresPlanOrPrompt(t *testing.T) {
	m, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	if _, err := m.Dispatch(context.Background(), DispatchReq{Repo: "/r"}); !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("plan 与 prompt 都缺应 400: %v", err)
	}
}

func TestDispatchPromptAppendedToPlan(t *testing.T) {
	m, st := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	plan := base64.StdEncoding.EncodeToString([]byte("# 计划标题\n正文"))
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, PlanB64: plan, PlanName: "p.md", Prompt: "只改 X 模块",
	})
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(task.PlanPath)
	if !strings.Contains(string(content), "附加指令") || !strings.Contains(string(content), "只改 X 模块") {
		t.Fatalf("prompt 应拼接在 plan 之后: %s", content)
	}
}

func TestDispatchUnknownExecutorRejected(t *testing.T) {
	m, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	_, err := m.Dispatch(context.Background(), DispatchReq{Repo: "/r", Prompt: "x", Executor: "nope"})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("未注册执行者应 400: %v", err)
	}
}

func TestDispatchPersistsExecutorModelAndWorkspace(t *testing.T) {
	m, st := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", Model: "m1",
		Name: "自定义名", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Executor != "fake" || task.Model != "m1" || task.Name != "自定义名" {
		t.Fatalf("executor/model/name 未落库: %+v", task)
	}
	if task.WorkDir == "" || !task.WorktreeManaged {
		t.Fatalf("new-worktree 元数据未落库: %+v", task)
	}
}

func TestDeriveName(t *testing.T) {
	for _, c := range []struct{ name, planName, prompt, want string }{
		{"显式优先", "p.md", "x", "显式优先"},
		{"", "2026-08-08-fix-login.md", "", "fix-login"},
		{"", "", "把 README 里的安装命令改成 brew 并验证一遍效果", "把 README 里的安装命令改成 brew "},
	} {
		if got := deriveName(c.name, c.planName, c.prompt); got != c.want {
			t.Fatalf("deriveName(%q,%q,%q)=%q want %q", c.name, c.planName, c.prompt, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/agentd/ -run 'TestDispatch|TestDeriveName' -v`。

- [ ] **Step 3: 实现（manager 层）**

Dispatch 重构点（保持既有日志/回滚模式）：
1. 校验：`Repo == "" || (PlanB64 == "" && Prompt == "")` → errBadDispatchRequest；互斥参数校验交给 PrepareWorkspace（其 ErrBadWorkspaceReq 在 server 层映射 400，见 Step 5）；
2. `execName, ad, err := m.resolveExecutor(req.Executor)`；`model := req.Model`（空取 `m.cfg.Executor.Model`）；
3. 内容合成：`content = plan`；prompt 非空且 plan 非空 → `content += "\n\n## 附加指令（派发时提供）\n\n" + req.Prompt`；plan 空 → `content = req.Prompt`，PlanName 空则置 `"prompt.md"`；
4. `deriveName(name, planName, prompt)`：显式名直接用；planName 去 `^\d{4}-\d{2}-\d{2}-` 前缀与 `.md` 后缀；都无则 prompt 前 20 rune；
5. `PrepareWorkspace(WorkspaceReq{Repo, TaskID, req.Branch, req.NewBranch, req.Base, req.Worktree, req.NewWorktree, filepath.Join(m.cfg.DataDir, "worktrees")})` 替代 PrepareBranch（此时删掉旧 PrepareBranch 导出或改为薄包装，二选一，全仓 grep 清理）；
6. CreateTask 携带全部新字段（Name/Executor=execName/Model=model/WorkDir=ws 非原地时的 WorkDir/WorktreeManaged）；**WorkDir 原地模式存空串**（Workdir() 回退语义）；branch 仍走 SetTaskField 老路径不动；
7. `ad.Start(...)` 用 resolveExecutor 返回的 adapter（不再 m.ad）。

server.go：handleDispatch 的请求体结构补 9 个新键；`writeDispatchError` 增加 `errors.Is(err, ErrBadWorkspaceReq)` → 400；`taskRepoOrErr` 返回 `task.Workdir()`（diff/fetch/run 在 worktree 上执行——分支 HEAD 在那里）。

client.go：`Dispatch(ctx, opts DispatchOpts)`；调用点（cmd/dispatch.go）同步。

cmd/dispatch.go：`Args: cobra.MaximumNArgs(1)`；9 个新 flag + `--no-terminal`（bool，本任务仅注册）；plan 参数缺失且 --prompt 也空时本地先报错（省一次网络往返）；互斥预检 `--branch`×`--new-branch`、`--worktree`×`--new-worktree`（cobra `MarkFlagsMutuallyExclusive`）。

opencode/adapter.go：grep `RepoPath`，cwd 相关取值全部改 `req.Task.Workdir()`（tmux -c、OPENCODE_CONFIG cwd 等）。

- [ ] **Step 4: 跑全量测试** — Run: `go test ./...`，Expected: PASS。

- [ ] **Step 5: 加关键节点日志** — Dispatch 进入日志追加 executor/model/name/branch/worktree 字段；工作区准备完成 Info（workdir、managed）；resolveExecutor 拒绝 Warn。

- [ ] **Step 6: 加注释** — DispatchReq 每个新字段一行语义注释（互斥关系写明）；「附加指令拼接」与「prompt-only 的 PlanName 兜底」各一条 why；taskRepoOrErr 改动处注释「为什么 diff/fetch/run 必须在 Workdir 而非主仓」。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/ internal/client/client.go cmd/dispatch.go internal/executor/opencode/
git commit -m "feat: dispatch 扩展——prompt/name/executor/model/分支/worktree 参数全链路"
```

---

### Task 8: 审批链接入 handlePermission

**Files:**
- Modify: `internal/agentd/manager.go`（Manager 增 approver 字段与 in-flight/失败计数；handlePermission 前置分流；新增 tryApproverApprove/escalatePermission）
- Modify: `cmd/agentd.go`（NewApprover 接线，构造失败即启动失败）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: Task 4 `Approver`/`ApproverDecision`、Task 1 事件类型、既有 ticket/事件机制。
- Produces：
  - `NewManager(..., approver *Approver, ...)` —— 在 Task 5 签名上再加一参（nil=不启用，两处测试 helper 同步）；
  - 事件 payload：`approverDecisionPayload{TicketID, Permission, Decision, Reason string, ElapsedMS int64}`（Decision 取 approve/escalate/error）；`approverDisabledPayload{Reason string}`；
  - 行为契约：approve 路径 = CreateTicket(gate) → AnswerTicket(ticketID, `"allow（审批者批准：<reason>）"`) → RespondPermission(once) → markDelivered → AppendEvent(approver_decision) **不 Publish**；escalate 路径 = AppendEvent(approver_decision) 后走既有中介流程原样（工单/waiting_answer/waiter/Publish）；同任务 `Decide` 连续 Err 3 次 → 本任务后续 permission 不再调审批者 + AppendEvent(approver_disabled) 一次。

- [ ] **Step 1: 写失败测试**（fake adapter 脚本产出 permission 事件；审批者用 runCmd 注入）

```go
// helper：构造带审批者的 manager，approverOut/approverErr 注入裁决结果
func newTestManagerWithApprover(t *testing.T, out string, cmdErr error) (*Manager, *store.Store, *fake.Fake) { ... }

func TestApproverApprovesPermissionWithoutWaking(t *testing.T) {
	// fake 脚本：permission("run tests") → 等 RespondPermission → result(OK)
	m, st, fk := newTestManagerWithApprover(t, `{"decision":"approve","reason":"跑测试"}`, nil)
	task := mustDispatch(t, m, ...)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingReview) // 直通完成，未经 waiting_answer 停留
	// 断言 1：工单已建且已答已送达（审计闭环）
	tk := mustGetTicket(t, st, task.ID+":"+fk.PermID())
	if tk.Answer == nil || !strings.Contains(*tk.Answer, "审批者批准") || tk.DeliveredAt == nil {
		t.Fatalf("approve 应自动应答并标记送达: %+v", tk)
	}
	// 断言 2：approver_decision 事件在，permission_request 事件不在（不唤醒审核者）
	evs := mustEvents(t, st, task.ID)
	if !hasEvent(evs, proto.EventTypeApproverDecision) || hasEvent(evs, proto.EventTypePermissionRequest) {
		t.Fatalf("approve 只留审计事件，不发 permission_request: %v", evs)
	}
	// 断言 3：executor 真收到 once
	if fk.LastDecision() != "once" {
		t.Fatalf("executor 应收到 once")
	}
}

func TestApproverEscalateFallsThroughToReviewer(t *testing.T) {
	m, st, _ := newTestManagerWithApprover(t, `{"decision":"escalate","reason":"拿不准"}`, nil)
	task := mustDispatch(t, m, ...)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingAnswer)
	evs := mustEvents(t, st, task.ID)
	if !hasEvent(evs, proto.EventTypeApproverDecision) || !hasEvent(evs, proto.EventTypePermissionRequest) {
		t.Fatalf("escalate 应留审计事件并走既有唤醒流程")
	}
}

func TestApproverBlacklistSkipsApprover(t *testing.T) {
	// fake 脚本 permission 文本 "Bash: sudo rm -rf /"；审批者 runCmd 若被调用则 t.Fatal
	m, st, _ := newTestManagerWithApproverFunc(t, func(ctx context.Context, argv []string) (string, error) {
		t.Fatal("黑名单命中不应调用审批者")
		return "", nil
	})
	task := mustDispatch(t, m, ...)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingAnswer) // 直接升级审核者
}

func TestApproverFailClosedCountsAndDisables(t *testing.T) {
	// runCmd 恒错；fake 脚本连发 4 个 permission
	callCount := 0
	m, st, _ := newTestManagerWithApproverFunc(t, func(ctx context.Context, argv []string) (string, error) {
		callCount++
		return "", errors.New("boom")
	})
	task := mustDispatch(t, m, ...)
	// 4 个 permission 全部升级审核者（fail-closed），但审批者只被调 3 次即禁用
	waitCondition(t, func() bool { return callCount == 3 })
	evs := mustEvents(t, st, task.ID)
	if !hasEvent(evs, proto.EventTypeApproverDisabled) {
		t.Fatalf("连续失败 3 次应记 approver_disabled")
	}
	if countEvents(evs, proto.EventTypePermissionRequest) != 4 {
		t.Fatalf("4 个权限请求都应升级审核者")
	}
}

func TestNilApproverKeepsCurrentBehavior(t *testing.T) {
	// approver=nil：现行为回归——permission 直接产生 permission_request 事件
}
```

（helper 细节按既有 manager_test.go 的 fake 脚本/waitTaskState 模式实现；`fake.Fake` 若缺 `PermID()/LastDecision()` 探查方法则在 fake 包补——fake 包本就是测试载具。）

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/agentd/ -run TestApprover -v`。

- [ ] **Step 3: 实现**

Manager 增字段：`approver *Approver`、`apMu sync.Mutex`、`apInflight map[string]bool`（key=ticketID）、`apFails map[string]int`（key=taskID）、`apDisabled map[string]bool`。

handlePermission 改造（保持既有顺序契约注释原样，新逻辑前插）：

```go
ticketID := taskID + ":" + ev.PermissionID
if m.isPermissionReplay(taskID, ev.PermissionID, ticketID) {
	return
}
if m.shouldConsultApprover(taskID, ev.Text) { // approver!=nil && !apDisabled[task] && !黑名单命中
	if m.markApproverInflight(ticketID) {      // 已在裁决中的重放：直接吞掉
		go m.consultApprover(ctx, taskID, ev, ticketID)
	}
	return
}
m.escalatePermission(ctx, taskID, ev, ticketID) // 既有函数体原样搬移
```

consultApprover（goroutine，不阻塞 mediate 循环——审批期间同任务的 progress 事件仍能入库）：
1. `summary` 取 task.PlanSummary（GetTask 失败用空串，不因此失败）；
2. `d := m.approver.Decide(ctx, ev.Text, summary)`；defer 清 inflight；
3. AppendEvent(approver_decision, payload{Decision: approve/escalate/error 按 d}) —— **不 Publish**（只入库不唤醒，审计经 show 可见）；
4. `d.Err != nil`：apFails[taskID]++，达 3 → apDisabled[taskID]=true + AppendEvent(approver_disabled) 一次；然后走 escalatePermission；
5. `d.Approve`：apFails 清零；CreateTicket(gate)（幂等）→ `m.st.AnswerTicket(ticketID, "allow（审批者批准："+d.Reason+"）")` → RespondPermission(unaryCtx, once) → 成功 markDelivered / 失败 NoteDeliveryFailed（沿用 waitPermission 的失败语义）；**全程不动任务状态**（任务保持 running，executor 收 once 后继续跑）；
6. 干净 escalate：apFails 清零，走 escalatePermission。

（注意：escalatePermission 从 goroutine 调用时，其内部 `go m.waitPermission(ctx, ...)` 用的 ctx 仍是 mediate 的任务级 ctx——保持传入不变。）

cmd/agentd.go：`ap, err := agentd.NewApprover(cfg.Approver, logger)`，err 非 nil 直接启动失败（黑名单正则错误属配置错误）；NewManager 传入。

- [ ] **Step 4: 跑全量测试** — Run: `go test ./...`，Expected: PASS。

- [ ] **Step 5: 加关键节点日志** — consultApprover 进入/裁决结果/自动批准回传成功/失败各一条（task、ticket、decision、elapsed_ms）；禁用时 Warn（task、连续失败次数）。**成功路径不静默**：自动批准完成必有 Info。

- [ ] **Step 6: 加注释** — handlePermission 顺序契约注释补一段「审批链前置的时序说明：approve 全程不动状态机；escalate 完整走原契约」；apInflight 注释 why（SSE 重放会在裁决窗口内送达同一 permission，无 inflight 去重会双呼审批者）；「失败计数 3 次禁用」注释 why（防对已损坏审批者命令的重试风暴，spec §7）。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/ cmd/agentd.go internal/executor/fake/
git commit -m "feat: 分级审批链——黑名单前置、审批者自动批准、fail-closed 与连续失败禁用"
```

---

### Task 9: Done 时清理 managed worktree

**Files:**
- Modify: `internal/agentd/manager.go`（Done）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: Task 6 `RemoveManagedWorktree`、Task 1 `WorktreeManaged/WorkDir` 字段。
- Produces：行为契约——Done 在 Stop 之后：`task.WorktreeManaged && task.WorkDir != ""` → RemoveManagedWorktree(task.RepoPath, task.WorkDir)；失败 AppendEvent(progress, "worktree 清理失败：<原因>，请手动 git worktree remove") + Error 日志，**不影响归档结果**；用户自带 worktree（Managed=false）不删。

- [ ] **Step 1: 写失败测试**

```go
func TestDoneRemovesManagedWorktree(t *testing.T) {
	// dispatch NewWorktree=true（fake adapter，脚本直接 result OK）→ waiting_review → Done
	// 断言 worktree 目录已删、分支保留、任务 completed
}

func TestDoneKeepsUserWorktree(t *testing.T) {
	// dispatch Worktree=<用户预建worktree> → Done 后目录仍在
}

func TestDoneWorktreeRemoveFailureDoesNotBlockArchive(t *testing.T) {
	// Done 前往 worktree 里塞未提交文件（git worktree remove 会拒绝脏树）
	// 断言：任务仍 completed；events 里有含「worktree 清理失败」的 progress 事件
}
```

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/agentd/ -run TestDone -v`。

- [ ] **Step 3: 实现** — Done 尾部追加清理块（Stop 之后，err 已定型不覆盖）。

- [ ] **Step 4: 跑测试确认通过** — Run: `go test ./internal/agentd/ -run TestDone -v`，Expected: PASS。

- [ ] **Step 5: 加关键节点日志** — 清理进入/成功 Info（task、workdir）、失败 Error（stderr 原文）。

- [ ] **Step 6: 加注释** — 「为什么失败只降级不阻塞归档」（任务已审核通过，残树是运维问题不是任务问题）+「为什么只删 managed」。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat: done 归档时自动清理 agentd 管理的 worktree（失败降级为警告）"
```

---

### Task 10: opencode adapter 消费 task.Model

**Files:**
- Modify: `internal/executor/opencode/taskenv.go`
- Test: `internal/executor/opencode/taskenv_test.go`

**Interfaces:**
- Consumes: Task 1 `proto.Task.Model`（经 StartReq.Task 到达）。
- Produces：行为契约——任务级 OPENCODE_CONFIG 的 model 取值优先级：`task.Model` > `HANDOFF_OPENCODE_MODEL` 环境变量 > 不写（executor 自身默认）。taskenv 的构建函数签名增加 model 入参（现从 `os.Getenv` 单源取，见 taskenv.go:124；改为 `model` 参数优先、env 兜底），adapter.go 调用点传 `req.Task.Model`。

- [ ] **Step 1: 写失败测试**（照 taskenv_test.go 既有模式）

```go
func TestTaskModelOverridesEnv(t *testing.T) {
	t.Setenv("HANDOFF_OPENCODE_MODEL", "env-model")
	// buildTaskEnv/WriteTaskConfig（按实际函数名）传 model="task-model"
	// 断言写出的 opencode.json 里 model == "task-model"
}

func TestTaskModelFallsBackToEnvThenEmpty(t *testing.T) {
	t.Setenv("HANDOFF_OPENCODE_MODEL", "env-model")
	// model="" → 断言 json 里 model == "env-model"
	// unset env + model="" → 断言 json 无 model 键（omitempty）
}
```

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./internal/executor/opencode/ -run TestTaskModel -v`。

- [ ] **Step 3: 实现** — 签名加参 + 优先级逻辑；taskenv.go:121-124 的注释同步更新（写清三级优先级与 why：flag/config 在 dispatch 期已折算进 task.Model，env 仅作机器级兜底）。

- [ ] **Step 4: 跑测试确认通过** — Run: `go test ./internal/executor/opencode/ -v`，Expected: PASS。

- [ ] **Step 5: 加关键节点日志** — 既有「任务环境注入模型」Info 补 `"source"` 字段（task/env）。

- [ ] **Step 6: Commit**

```bash
git add internal/executor/opencode/
git commit -m "feat: opencode 任务级模型注入改为 task.Model 优先、env 兜底"
```

---

### Task 11: CLI 可观测性——show 改名与 attach 终端实况

**Files:**
- Create: `cmd/show.go`（现 attach.go 内容迁移，Use 改 "show <task>"）
- Rewrite: `cmd/attach.go`（终端 attach + 交互列表）
- Modify: `README.md`（命令表：attach 语义、show 新增；会话恢复流程改 `tasks` + `show`）
- Modify: `docs/superpowers/specs/2026-08-07-handoff-design.md`（§7 加一行注记：「二期起快照命令更名 handoff show，attach 改为终端实况；见二期 spec」）
- Test: `cmd/root_test.go`（或新建 `cmd/attach_test.go`）

**Interfaces:**
- Consumes: `client.ListTasks`、`client.Attach`（HTTP API 不改名，仅 CLI 层改）、config `Targets`（ssh host 推导）、opencode 的 tmux 会话命名约定 `handoff-<id8>`。
- Produces：
  - `handoff show <task>`：输出与一期 attach 完全相同的快照 JSON；
  - `handoff attach [task]`：有 task → 组装并执行 attach 命令（本机 `tmux attach -t handoff-<id8>`；远程 `ssh -t <host> tmux attach -t handoff-<id8>`，host = `Targets[target].Addr` 冒号前段）；无 task → 任务选择列表；
  - 内部函数（供 Task 12 复用）：`func attachCommandFor(taskID, target string, cfg *config.Config) (argv []string, err error)`；
  - 非 TTY（stdin 非字符设备）：打印列表与建议命令后退出 0，不进交互。

- [ ] **Step 1: 写失败测试**

```go
func TestAttachCommandForLocal(t *testing.T) {
	cfg := &config.Config{}
	argv, err := attachCommandFor("abcdefgh-1234", "", cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tmux", "attach", "-t", "handoff-abcdefgh"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("got %v want %v", argv, want)
	}
}

func TestAttachCommandForRemote(t *testing.T) {
	cfg := &config.Config{Targets: map[string]config.Target{"dev": {Addr: "devbox:7777"}}}
	argv, err := attachCommandFor("abcdefgh-1234", "dev", cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ssh", "-t", "devbox", "tmux", "attach", "-t", "handoff-abcdefgh"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("got %v want %v", argv, want)
	}
}

func TestAttachCommandForUnknownTarget(t *testing.T) {
	if _, err := attachCommandFor("t", "ghost", &config.Config{}); err == nil {
		t.Fatalf("未配对 target 应报错")
	}
}

func TestShowCommandRegistered(t *testing.T) {
	// rootCmd 下存在 "show" 且 attach 的 Short 已是终端语义（防改名回归）
}
```

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./cmd/ -run TestAttachCommand -v`。

- [ ] **Step 3: 实现**

- show.go：迁移现 attach.go 全部内容，命令名/注释改 show；文件头注释保留「审核者会话恢复关键数据源」说明；
- attach.go 重写：
  - `id8` 截断逻辑与 opencode 侧一致（>8 取前 8）；本文件内小函数 + 注释「与 opencode adapter 的 tmux 会话命名约定耦合，改一处必改两处」；
  - 有 task：`attachCommandFor` → `syscall.Exec`（查 `exec.LookPath(argv[0])`；Exec 替换进程让 tmux 拿到真 TTY）；
  - 无 task：ListTasks → 排序（running/waiting_answer/waiting_review 在前，组内 created_at 降序）→ 表格输出 `序号 name executor 状态 更新时间`（name 空回退 id 前 8）→ `bufio.Scanner` 读序号 → 组装执行；
  - 非 TTY 判定：`os.Stdin.Stat()` 的 `ModeCharDevice`；非 TTY 打印每行 `handoff attach <id> [--target x]` 建议命令；
- README：命令表两行更新 + 会话恢复段命令替换；一期 spec §7 尾部加一行注记（不改历史结论，只指向二期 spec）。

- [ ] **Step 4: 跑测试确认通过** — Run: `go test ./cmd/ -v`，Expected: PASS。

- [ ] **Step 5: 加关键节点日志** — CLI 无 slog 管道，面向用户输出即可观测面：exec 前打印将执行的完整命令（用户可复制重试，spec §7 错误处理项）；LookPath 失败给出「tmux 未安装」可读报错。

- [ ] **Step 6: 加注释** — attach.go 文件头：职责=「终端实况入口（人类命令）」、边界=「快照恢复走 show；不改任务状态；fake executor 无 tmux 会话，attach 失败属预期」。

- [ ] **Step 7: 手动验证清单**（记入 commit message body 或 README 开发段）

```bash
go build -o /tmp/handoff . 
# 1) handoff attach（无参）在真终端出列表可选择进入
# 2) handoff attach <运行中任务> 进 tmux 实况，Ctrl-b d 可退出
# 3) echo | handoff attach 走非 TTY 降级打印
```

- [ ] **Step 8: Commit**

```bash
git add cmd/show.go cmd/attach.go README.md docs/superpowers/specs/2026-08-07-handoff-design.md
git commit -m "feat: attach 语义翻转为 tmux 终端实况（列表选择），快照改名 show"
```

---

### Task 12: dispatch 默认弹终端

**Files:**
- Modify: `cmd/dispatch.go`
- Test: `cmd/dispatch_test.go`（新建）

**Interfaces:**
- Consumes: Task 11 `attachCommandFor`、Task 2 `Terminal.Auto`、Task 7 已注册的 `--no-terminal` flag。
- Produces：行为契约——dispatch 成功输出任务 JSON 后：`--no-terminal` 或 `cfg.Terminal.Auto==false` 或 `runtime.GOOS != "darwin"` → 打印一行提示 `实况: handoff attach <id>`（远程含 --target）；否则 `osascript -e 'tell application "Terminal" to do script "<attach 命令>"' -e 'tell application "Terminal" to activate'` 弹窗；osascript 失败降级为同款提示行 + stderr 警告，**不影响退出码**。
- 测试缝：`var openTerminal = func(attachArgv []string) error {...}` 包级变量，测试替换记录调用。

- [ ] **Step 1: 写失败测试**

```go
func TestDispatchOpensTerminalByDefault(t *testing.T) {
	// httptest 假 agentd 返回任务 JSON；替换 openTerminal 记录调用
	// darwin 上跑：断言 openTerminal 被调且 argv 含 "attach" 与任务 id
	// 非 darwin：t.Skip
}

func TestDispatchNoTerminalFlagSuppresses(t *testing.T) {
	// --no-terminal → openTerminal 不被调，stdout 含 "handoff attach" 提示行
}

func TestDispatchTerminalFailureDoesNotFailCommand(t *testing.T) {
	// openTerminal 返回错误 → 命令退出码 0，任务 JSON 正常输出
}
```

- [ ] **Step 2: 跑测试确认失败** — Run: `go test ./cmd/ -run TestDispatch -v`。

- [ ] **Step 3: 实现** — dispatch RunE 尾部弹终端块；osascript 的 do script 参数把 attach argv 以 shellQuote 逐个拼接（cmd 包内补一个与 opencode 同实现的 shellQuote 小函数或提公共包——**优先提到 `internal/logx` 之外的新家 `internal/shellq`，两处引用**，避免复制漂移）。

- [ ] **Step 4: 跑测试确认通过** — Run: `go test ./cmd/ -v`，Expected: PASS。

- [ ] **Step 5: 加关键节点日志** — 弹窗失败的警告写 stderr（`fmt.Fprintln(cmd.ErrOrStderr(), ...)`），带 osascript 错误原文与建议命令。

- [ ] **Step 6: 加注释** — 「为什么弹窗失败不影响退出码」（派发已成功，弹窗是增强可见性）；shellq 包文件头注释。

- [ ] **Step 7: 手动验证** — 真机 dispatch 一个 fake 任务确认 Terminal.app 弹窗进实况；`terminal.auto: false` 配置确认不弹。

- [ ] **Step 8: Commit**

```bash
git add cmd/ internal/shellq/ internal/executor/opencode/
git commit -m "feat: dispatch 成功后默认弹终端实况（osascript，--no-terminal/配置可关）"
```

---

### Task 13: 文档收口与全量回归

**Files:**
- Modify: `README.md`（配置样例补 approver/executor/terminal 三节；dispatch 新参数示例；审批链一节）
- Modify: `docs/superpowers/specs/2026-08-08-handoff-approver-dispatch-observability-design.md`（如实现偏差则回填修订）
- Test: 全量

- [ ] **Step 1: README 更新** — 配置样例、命令表（dispatch 全 flag、attach/show）、审批链流程图（spec §3.1 复用）、e2e 手动清单补二期项（审批链真实 opencode 裁决一次、worktree 并行派发、attach 列表）。
- [ ] **Step 2: 全量测试** — Run: `go test ./...`，Expected: PASS。
- [ ] **Step 3: instrumenting-code 终检** — 逐项过：新增错误分支带上下文、成功路径不静默、无 print 式日志、新文件（oneshot.go/approver.go/show.go/attach.go/shellq）头注释、导出方法注释齐全。
- [ ] **Step 4: gofmt** — Run: `gofmt -l .`，Expected: 无输出。
- [ ] **Step 5: Commit**

```bash
git add README.md docs/
git commit -m "docs: 二期功能文档收口（审批链/executor 选择/dispatch 参数/attach）"
```

---

## Self-Review 记录

- **Spec 覆盖**：§3 审批链→Task 2/3/4/8；§4 executor 机制→Task 1/2/5/7/10；§5 dispatch 参数→Task 1/6/7/9；§6 可观测性→Task 11/12；§7 错误处理→分散在 4（fail-closed）/6（拒发）/8（禁用）/9（降级）/11（ssh 失败提示）/12（osascript 降级）；§8 测试策略→各任务 TDD + 11/12 手动清单。
- **类型一致性**：`ApproverDecision`（4→8）、`WorkspaceReq/Workspace/RemoveManagedWorktree`（6→7/9）、`resolveExecutor/adapterFor`（5→7/8）、`attachCommandFor`（11→12）、`Task.Workdir()`（1→7）、`OneShotArgs`（3→4）已交叉核对。
- **占位符扫描**：无 TBD/TODO；Task 8 helper 与 Task 9 测试骨架标明「按既有模式实现」并指出了参照文件，属实现指引而非占位。
