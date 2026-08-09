# 任务运行态对账与恢复出口 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让「executor 已经不在了」这个事实无论从哪个口子到达都被对账成事件与可操作状态，并给 `waiting_review` 的任务一条带上下文的原地续接出口。

**Architecture:** 三处改动咬合成一条链。(1) 把「executor 没了怎么收尾」的两份拷贝收敛成 `reconcileExecutorGone` 一个包级函数，并接上第二个到达口——`mediate` 事件通道关闭；(2) 把 adapter 的 `Resume` 从「热重连布尔判据」升级成四级恢复阶梯（内存态 → 热重连 → 冷恢复 → 新会话），由 `Continue` 按需触发；(3) 给 adapter 加 `Reap` 可选能力，无内存运行态时按确定性 tmux 会话名兜底回收，失败留事件。状态机零改动，事件类型零新增。

**Tech Stack:** Go 1.x，`log/slog`，tmux，SQLite（`internal/store`），三个真实 adapter（opencode HTTP+SSE / grok ACP over WebSocket / Claude Code tmux+fifo+stream-json）。

**Spec:** [2026-08-09-handoff-runtime-reconciliation-design.md](../specs/2026-08-09-handoff-runtime-reconciliation-design.md)

---

## 对 spec 的一处补充（实现期发现，必须落实）

Spec §3.1 的 `ResumeReq` **字段不够**，照写会让冷恢复起出一个跑不动的 executor：

| 缺的字段 | 为什么必须有 |
|---|---|
| `Env []string` | 冷恢复要重新拉起进程，走的是 `StartServe`/`StartProc` 同一条路径，而这两个都吃 `env []string`（B19 的 env 注入）。不传，重起的 executor 就丢掉用户配的代理与密钥，表现为「进程起来了但一调模型就失败」——而且是静默的 |
| `Model string` | `grok.StartServe(ctx, repoPath, taskID, taskDir, model, env, log)` 与 `claudecode.StartProcReq.Model` 都要它；serve.json / claude.json 都没存模型名，只有 `task.Model` 有 |

两个字段都由 manager 侧填（`m.env.For(execName)` 与 `task.Model`，与 `Dispatch` 同源）。Task 2 落实。

**凭据纪律（本项目既有约定，全程适用）：** `Env` 的**值**绝不进日志、事件、render.log；要打只打 key 名（照抄 `claudecode/proc.go:119-130` 的写法）。

---

## Global Constraints

以下是全项目约束，每个 task 的要求都隐含包含本节：

- **不新增事件类型**：`proto` 的 `EventType` 一行不改，只复用 `failed` / `progress`。
- **不改状态机**：6 状态、`internal/proto/proto.go:126` 的 `transitTable` 一行不改。
- **不加周期性存活探活**：`RunWatchdog` 的 `StallTimeout` 语义保持原样（它兜的是「executor 活着但长时间无产出」）。理由见 spec §2.2。
- **adapter 不写 store**：状态机只有一个写入者（manager）。adapter 只产事件、返回值。
- **日志一律 `slog`**，禁止 `fmt.Printf` / `print`。凭据值不进日志（只打 key 名）。
- **注释一律中文**：新文件写文件头（职责 + 边界），导出方法写 doc（参数/返回/注意），非显然分支写「为什么」。
- **`ErrTaskNotRunning` 是唯一判据**：区分「executor 已经不在」与「调用失败但 executor 还在」，禁止靠错误文本判别（`internal/executor/executor.go:29-36`）。
- **每个 task 结束时 `go build ./... && go test ./...` 全绿**才能提交。

---

## 文件结构

| 文件 | 状态 | 职责 |
|---|---|---|
| `internal/executor/resume.go` | 新建 | `ResumeReq` / `ResumeOutcome` / `ResumeMode*` 常量。纯数据契约，三个 adapter 与 manager 共用 |
| `internal/agentd/reconcile.go` | 新建 | `reconcileExecutorGone` 包级函数 + `Manager` 的 `stopping` 取走式标记（`noteStopping`/`takeStopping`）+ `Manager.stopExecutor` 辅助 |
| `internal/agentd/reconcile_test.go` | 新建 | 对账函数表驱动 + mediate 退出对账 + stopping 标记语义 |
| `internal/agentd/manager.go` | 修改 | `restorer`/`reaper` 接口；`mediate` 尾部对账；`Continue` 恢复阶梯；`Done`/`Stop` 接 `stopExecutor`；`ResumeTask` 构造 `ResumeReq` |
| `internal/agentd/watchdog.go` | 修改 | `RecoverOnStartup` 的内联收尾块换成 `reconcileExecutorGone` |
| `internal/executor/opencode/resume.go` | 新建 | 从 `adapter.go` 迁出的 `Resume` + 冷恢复（adapter.go 已 1400 行，恢复逻辑本次要显著变长，独立成文件） |
| `internal/executor/opencode/reap.go` | 新建 | `Reap` 兜底回收 |
| `internal/executor/claudecode/resume.go` | 新建 | 同上（从 `adapter.go:400-476` 迁出 + 冷恢复） |
| `internal/executor/claudecode/reap.go` | 新建 | `Reap` |
| `internal/executor/grok/resume.go` | 修改 | 已是独立文件，就地加冷恢复 |
| `internal/executor/grok/reap.go` | 新建 | `Reap` |
| `internal/executor/opencode/adapter.go` | 修改 | 删掉迁出的 `Resume`；`mapIdle` 空回合转 `result{OK:false}` |
| `internal/executor/grok/adapter.go` | 修改 | `finishTurn` 兜底分支加空文本守卫 |
| `internal/executor/claudecode/adapter.go` | 修改 | 删掉迁出的 `Resume`；`fallbackClassify` 加空文本守卫 |

---

## Task 1: Spike —— 三个 executor 的冷恢复真机验证（全部实现的前置门）

**这是一道门，不是热身。** claude 的 `--resume` + `--print --output-format stream-json` + fifo 持续喂 stdin 这个组合能不能成立，帮助文本回答不了，只有真机能回答。Task 10 的形态由本 task 的结论决定。

**环境：** devbox `sycm@100.73.238.21`（免密），项目 `/Users/sycm/workspace/handoff`。三个 executor 的二进制：`/Users/sycm/.local/bin/claude`、`/Users/sycm/.grok/bin/grok`、`~/.opencode/bin/opencode`。SSH 里 PATH 不全，用 `zsh -l -i -c "..."` 包一层。

**Files:**
- Modify: `docs/superpowers/specs/2026-08-09-handoff-runtime-reconciliation-design.md`（回填 §11 实测附录）

**Interfaces:**
- Produces: §11 的三行结论表，Task 8/9/10 各自读自己那一行决定实现形态。

- [ ] **Step 1: grok —— 起一个带口令的真会话**

在 devbox 上，用一个临时目录当 cwd（不必经 agentd，手工起 serve 即可）：

```bash
ssh sycm@100.73.238.21 'zsh -l -i -c "mkdir -p /tmp/spike-grok && cd /tmp/spike-grok && git init -q . 2>/dev/null; grok --help | head -40"'
```

先确认 serve 子命令与端口参数形态，再照 `internal/executor/grok/proc.go:132` 的 `StartServe` 拼出同款启动命令（`GROK_HOME=<taskDir>/grokhome`）。会话建好后发第一条 prompt：

```
记住这个口令：ALPACA-7731。之后我会问你它是什么。
```

记下 `session/new` 返回的 sessionId。

- [ ] **Step 2: grok —— 杀掉进程，冷恢复，问口令**

```bash
ssh sycm@100.73.238.21 'zsh -l -i -c "pkill -f grok-serve-spike; ls /tmp/spike-grok/grokhome/sessions/"'
```

确认会话目录仍在磁盘上。重起 serve（新端口，`GROK_HOME` 不变），ACP `initialize` → `session/load`（sessionId 用 Step 1 的）→ 发：

```
我刚才让你记的口令是什么？只回答口令本身。
```

**通过判据：模型回出 `ALPACA-7731`。** 日志里写了 `session/load` 成功不算数。

- [ ] **Step 3: opencode —— 同样三步**

会话在全局 sqlite（`~/.local/share/opencode/opencode.db`），所以「杀进程」之后重起 serve、`GET /session` 里应仍能看到那个 session id，然后 `POST /session/<id>/prompt_async` 问口令。用不同口令（`OPOSSUM-4419`）避免与 grok 的实验串味。

- [ ] **Step 4: claude —— 风险最高的一条，按真实形态搭**

必须复刻真实形态，否则结论不可用：**tmux 会话 + fifo 喂 stdin + `--print --output-format stream-json`**。照 `internal/executor/claudecode/proc.go:187-228` 的 `writeRunScript` 手写一个脚本，唯一差别是把 `--session-id <uuid>` 换成 `--resume <uuid>`：

```sh
#!/bin/sh
exec 2>> /tmp/spike-claude/stderr.log
exec 3<> /tmp/spike-claude/in.fifo
claude -p --input-format stream-json --output-format stream-json --verbose \
  --include-partial-messages --resume <SESSION-ID> \
  --setting-sources user,project <&3 | tee -a /tmp/spike-claude/out.jsonl
printf '{"type":"handoff_exit","code":%d}\n' "$?" >> /tmp/spike-claude/out.jsonl
```

口令用 `CARIBOU-2856`。先跑一遍 `--session-id` 建会话记口令，`tmux kill-session` 杀掉，再用上面的脚本 `--resume` 起来问口令。

要盯三件事，任一不成立即判不通过：
1. `--resume` 与 `--print`/`--input-format stream-json` 能否共存（可能直接报参数冲突）
2. resume 起来后是否照常吐 `system/init`（`Start` 的就绪判据依赖它，`adapter.go:249`）
3. 模型能否复述 `CARIBOU-2856`

- [ ] **Step 5: 回填 spec §11**

三行结论表，每行：executor / 是否通过 / 实际命令 / 模型复述证据（原样贴模型回答）/ 踩到的坑。不通过的写清楚卡在哪一步。

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/specs/2026-08-09-handoff-runtime-reconciliation-design.md && git commit -m "docs(spec): 回填冷恢复三 executor 真机实测附录"
```

- [ ] **Step 7: 分流**

- 三个都通过 → Task 8/9/10 按计划实现冷恢复。
- claude 不通过 → **Task 10 改为只实现第 4 级**（`Cold=true` 时不 `--resume`，直接新开会话 + `Mode=fresh`），其余一律照旧；Task 8/9 完全不受影响。把这个结论也写进 §11。
- grok 或 opencode 不通过 → 停下来找我，这两个的实证证据（会话目录/sqlite 就在盘上）与失败结论矛盾，说明现场理解有误，不要硬改设计。

---

## Task 2: 恢复契约的数据类型与签名迁移（行为不变）

纯签名迁移：三个 adapter 的 `Resume` 换成新入参/返回值，语义**一行不变**（全部返回 `Mode=reattach`）。把它独立成一个 task，是为了让后面每个冷恢复 task 的 diff 里只有真正的新逻辑。

**Files:**
- Create: `internal/executor/resume.go`
- Modify: `internal/agentd/manager.go`（`restorer` 接口、`ResumeTask`）
- Modify: `internal/executor/grok/resume.go`、`internal/executor/opencode/adapter.go:396`、`internal/executor/claudecode/adapter.go:423`
- Test: `internal/agentd/resume_test.go`

**Interfaces:**
- Produces（后续全部 task 依赖）：
  - `executor.ResumeReq{TaskID, TaskDir, RepoPath, SessionID string; Env []string; Model string; Cold bool}`
  - `executor.ResumeOutcome{Alive bool; Mode, SessionID, Note string}`
  - `executor.ResumeModeReattach = "reattach"` / `ResumeModeCold = "cold"` / `ResumeModeFresh = "fresh"`
  - `agentd.restorer interface{ Resume(executor.ResumeReq) (executor.ResumeOutcome, error) }`（私有，留在 `manager.go`）

- [ ] **Step 1: 写共享数据类型**

创建 `internal/executor/resume.go`：

```go
// resume.go —— 执行恢复的共享数据契约。
//
// 职责：
//   - 定义 ResumeReq / ResumeOutcome / ResumeMode 常量，供 manager 与三个 adapter 共用
//
// 边界：
//   - 只有数据，没有接口：恢复能力的接口由消费方（manager）定义并做类型断言，
//     这样「不支持恢复的 adapter 一律按不存活走 failed 恢复路径」仍是自然语义，
//     executor.Adapter 的五动作核心契约也不被污染
//   - 无 I/O、无实现（与本包其余部分同规格）
package executor

// ResumeReq 是一次恢复请求。
//
// 字段说明：
//   - TaskDir: DataDir/tasks/<id>，恢复凭据（serve.json / claude.json）与会话
//     数据（grok 的 grokhome）都在里面
//   - RepoPath: 任务工作区（worktree 任务为 Workdir）。冷恢复时它是新进程的 cwd，
//     claude 的会话文件路径按 cwd 编码，传错等于找不到会话
//   - SessionID: 落库的 task.ExecutorSession；空则无法载入原会话
//   - Env: 冷恢复重起进程时要注入的环境变量（KEY=VALUE，已解析已展开）。
//     **不传就是静默故障**：进程能起来，但用户配的代理/密钥全没了，一调模型才失败。
//     值绝不进日志，要打只打 key 名
//   - Model: 冷恢复重起进程时的模型名（serve.json/claude.json 都没存它，
//     只有 task.Model 有）
//   - Cold: true=允许冷恢复（进程已死时重起进程 + 载入原会话）；
//     false=只热重连，进程不在即判不可恢复
type ResumeReq struct {
	TaskID    string
	TaskDir   string
	RepoPath  string
	SessionID string
	Env       []string
	Model     string
	Cold      bool
}

// 恢复实际走到的级别（ResumeOutcome.Mode 的取值）。
//
// 四级阶梯的第 1 级「运行态还在 adapter 内存里」不在此列——那种情况根本不会
// 调到 Resume。
const (
	ResumeModeReattach = "reattach" // 第 2 级：进程还活着，重连
	ResumeModeCold     = "cold"     // 第 3 级：重起进程 + 载入原会话，上下文完整
	ResumeModeFresh    = "fresh"    // 第 4 级：原会话载不进，已新开会话，上下文从下一条指令开始
)

// ResumeOutcome 是恢复结果。
//
// 字段说明：
//   - Alive: 是否拿到了可用的运行态。false 时其余字段除 Note 外均为空
//   - Mode: ResumeMode* 之一；Alive=false 时为空串
//   - SessionID: 恢复后实际生效的会话 id。fresh 时是新 id，manager 据此落库
//   - Note: 一句话结论，manager 转成事件文本或错误信息给审核者看
type ResumeOutcome struct {
	Alive     bool
	Mode      string
	SessionID string
	Note      string
}
```

- [ ] **Step 2: 改 manager 的 restorer 接口与 ResumeTask**

`internal/agentd/manager.go:1693` 的接口体换成新签名（doc 注释保留原有的「为什么用类型断言」段落，追加一句说明数据类型已挪到 `internal/executor`）：

```go
type restorer interface {
	Resume(executor.ResumeReq) (executor.ResumeOutcome, error)
}
```

`ResumeTask`（`manager.go:1754` 起）的尾部换成：

```go
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	execName := task.Executor
	if execName == "" {
		execName = m.cfg.Executor.Default
	}
	// env 与 Dispatch 同源：冷恢复重起进程要原样注入（B19），解析失败不阻断
	// 恢复——热重连根本用不上它，冷恢复用不上时由 adapter 侧自行报错
	envKVs, eerr := m.env.For(execName)
	if eerr != nil {
		m.log.Warn("恢复解析 env 失败，按空 env 继续", "task", taskID, "executor", execName, "cause", eerr)
	}
	// 启动恢复一律 Cold=false：agentd 重启时若有 10 个任务的 executor 已死，
	// 急着冷恢复等于凭空拉起 10 个没人跟它说话的 executor（spec §4）
	out, err := r.Resume(executor.ResumeReq{
		TaskID: taskID, TaskDir: taskDir, RepoPath: task.Workdir(),
		SessionID: task.ExecutorSession, Env: envKVs, Model: task.Model, Cold: false,
	})
	if err != nil {
		m.log.Error("重建任务执行失败", "task", taskID, "cause", err)
		return false
	}
	if out.Alive {
		m.log.Info("任务执行已重建，重启中介循环", "task", taskID, "mode", out.Mode)
		go m.mediate(taskID)
	}
	return out.Alive
```

- [ ] **Step 3: 三个 adapter 换签名（语义不变）**

每个都是同一套机械改写：入参改成 `req executor.ResumeReq`，函数体里 `taskID`→`req.TaskID`、`taskDir`→`req.TaskDir`、`repoPath`→`req.RepoPath`、`sessionID`→`req.SessionID`；`return false, nil` → `return executor.ResumeOutcome{}, nil`；`return false, err` → `return executor.ResumeOutcome{}, err`；最后的 `return true, nil` →

```go
	return executor.ResumeOutcome{
		Alive: true, Mode: executor.ResumeModeReattach, SessionID: req.SessionID,
		Note: "executor 仍存活，已重连事件流",
	}, nil
```

opencode（`adapter.go:396`）与 claude（`adapter.go:423`）同时**迁到新文件**：`internal/executor/opencode/resume.go`、`internal/executor/claudecode/resume.go`。两个新文件都写文件头注释：

```go
// resume.go —— 执行恢复：热重连与冷恢复。
//
// 职责：
//   - Resume：按 ResumeReq 走恢复阶梯，返回实际走到的级别
//
// 边界：
//   - 不判断「该不该恢复」的业务前提（如是否有未决权限工单）——那需要工单知识，
//     属 manager（见 manager.go 的 volatilePermitter）
//   - 不改任务状态：adapter 不写 store（见 executor 包级边界）
package opencode
```

grok 的 `resume.go` 已经是独立文件，只改签名。

- [ ] **Step 4: 跑全量测试确认零回归**

Run: `go build ./... && go test ./...`
Expected: PASS。这一步只做签名迁移，任何行为变化都是 bug。

- [ ] **Step 5: 写恢复请求装配的钉子测试**

`Env` 漏传是「编译照过、静默失效」的典型（`claudecode/proc.go:72-74` 已为 B19 留过同款钉子），必须钉住。追加到 `internal/agentd/resume_test.go`：

```go
// recordingRestorer 记录 Resume 收到的实参，供断言 manager 侧的请求装配。
type recordingRestorer struct {
	chanAdapter
	mu  sync.Mutex
	got executor.ResumeReq
}

func (r *recordingRestorer) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = req
	return executor.ResumeOutcome{}, nil // 不存活：本用例只验请求装配
}

// TestResumeTaskAssemblesRequest 钉住 ResumeTask 的请求装配：
// 启动恢复必须 Cold=false（不冷恢复，why 见 spec §4），且 Env/Model 必须传下去
// ——漏传 Env 会让冷恢复起出一个没有用户密钥的进程，编译照过、只在真机静默失败。
func TestResumeTaskAssemblesRequest(t *testing.T) {
	ad := &recordingRestorer{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	// env 文件：<DataDir>/env/dev.env，配置里把 fake 指向它
	envDir := filepath.Join(m.cfg.DataDir, "env")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "dev.env"), []byte("FOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.cfg.Env = map[string]string{"fake": "dev.env"}
	m.env = envfile.NewResolver(envfile.Dir(m.cfg.DataDir), m.cfg.Env, slog.New(slog.NewTextHandler(io.Discard, nil)))

	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		Model: "sonnet", State: proto.TaskStateRunning, ExecutorSession: "sess-1"})
	if alive := m.ResumeTask("t1"); alive {
		t.Fatalf("Resume 返回不存活时 ResumeTask 应为 false")
	}
	ad.mu.Lock()
	got := ad.got
	ad.mu.Unlock()
	if got.Cold {
		t.Fatalf("启动恢复必须 Cold=false，实际 true")
	}
	if got.SessionID != "sess-1" || got.Model != "sonnet" {
		t.Fatalf("会话/模型未透传: %+v", got)
	}
	if len(got.Env) != 1 || got.Env[0] != "FOO=bar" {
		t.Fatalf("env 未透传（漏传会让冷恢复丢掉用户密钥）: %v", got.Env)
	}
}
```

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/agentd/ -run TestResumeTaskAssemblesRequest -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/executor/resume.go internal/executor/*/resume.go internal/executor/*/adapter.go internal/agentd/manager.go internal/agentd/resume_test.go && git commit -m "refactor(resume): 恢复契约改为 ResumeReq/ResumeOutcome，补 Env/Model 字段"
```

---

## Task 3: `reconcileExecutorGone` —— 对账的唯一实现

「executor 没了怎么收尾」现在有两份拷贝，措辞与顺序都不一致（`watchdog.go:222-243` 与 `manager.go:1478`）。Task 4 会再加第三个调用点，所以先收敛。

**Files:**
- Create: `internal/agentd/reconcile.go`
- Create: `internal/agentd/reconcile_test.go`
- Modify: `internal/agentd/watchdog.go:220-243`
- Modify: `internal/agentd/manager.go:1478-1500`（`abandonToReview`）

**Interfaces:**
- Consumes: `recoverTransit(st, taskID, cur)`（`watchdog.go:256`，已存在，含 `waiting_answer` 两跳）
- Produces: `reconcileExecutorGone(st *store.Store, hub *Hub, taskID, reason string, log *slog.Logger) proto.TaskState`

- [ ] **Step 1: 先写失败的表驱动测试**

创建 `internal/agentd/reconcile_test.go`：

```go
// 运行态对账的白盒测试：executor 已不在这一事实的唯一收尾实现。
package agentd

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// newTestStore 开一个临时库（对账函数不需要完整 Manager）。
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestReconcileExecutorGone 表驱动：活跃态收尾并落 waiting_review，终态/待审核态是空操作。
func TestReconcileExecutorGone(t *testing.T) {
	cases := []struct {
		name      string
		from      proto.TaskState
		wantState proto.TaskState
		wantEvent bool
	}{
		{"running 收尾", proto.TaskStateRunning, proto.TaskStateWaitingReview, true},
		{"waiting_answer 两跳收尾", proto.TaskStateWaitingAnswer, proto.TaskStateWaitingReview, true},
		{"waiting_review 空操作", proto.TaskStateWaitingReview, proto.TaskStateWaitingReview, false},
		{"completed 空操作", proto.TaskStateCompleted, proto.TaskStateCompleted, false},
		{"failed 空操作", proto.TaskStateFailed, proto.TaskStateFailed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: c.from})
			got := reconcileExecutorGone(st, NewHub(), "t1", "测试来源", quietLog())
			if got != c.wantState {
				t.Fatalf("返回状态 = %s，期望 %s", got, c.wantState)
			}
			cur, err := st.GetTask("t1")
			if err != nil {
				t.Fatal(err)
			}
			if cur.State != c.wantState {
				t.Fatalf("落库状态 = %s，期望 %s", cur.State, c.wantState)
			}
			evs, err := st.EventsFromAsc("t1", 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			hasFailed := false
			for _, e := range evs {
				if e.Type == proto.EventTypeFailed {
					hasFailed = true
				}
			}
			if hasFailed != c.wantEvent {
				t.Fatalf("failed 事件 = %v，期望 %v", hasFailed, c.wantEvent)
			}
		})
	}
}

// TestReconcileExecutorGoneIdempotent 幂等：三个到达口可能对同一任务先后触发，
// 第二次必须是空操作，不产重复事件。
func TestReconcileExecutorGoneIdempotent(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: proto.TaskStateRunning})
	reconcileExecutorGone(st, NewHub(), "t1", "第一次", quietLog())
	reconcileExecutorGone(st, NewHub(), "t1", "第二次", quietLog())
	evs, err := st.EventsFromAsc("t1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Type == proto.EventTypeFailed {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("failed 事件应只有 1 条（幂等），实际 %d", n)
	}
}

// TestReconcileExecutorGoneVoidsPendingTickets 验证挂起工单被作废：
// executor 已不在，attach 继续展示可操作的挂起项就是假象（P1-16 同因）。
func TestReconcileExecutorGoneVoidsPendingTickets(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: proto.TaskStateRunning})
	if _, err := st.CreateTicket(&proto.Ticket{ID: "t1:p1", TaskID: "t1", Kind: "permission", Text: "Bash: ls"}); err != nil {
		t.Fatal(err)
	}
	reconcileExecutorGone(st, NewHub(), "t1", "测试来源", quietLog())
	pend, err := st.PendingTickets("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 0 {
		t.Fatalf("挂起工单应被作废，实际剩 %d", len(pend))
	}
}
```

（`mustCreateTask` 已在 `manager_test.go:128` 定义，同包可直接用。`proto.Ticket` 的字段名若与此处不符，以 `internal/proto` 实际定义为准，测试意图不变。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestReconcileExecutorGone -v`
Expected: FAIL，`undefined: reconcileExecutorGone`

- [ ] **Step 3: 实现对账函数**

创建 `internal/agentd/reconcile.go`：

```go
// reconcile.go —— 任务运行态与 executor 实际存活性的对账。
//
// 职责：
//   - reconcileExecutorGone：「executor 已不在」这一事实的唯一收尾实现，
//     三个到达口（启动探活 / 事件通道关闭 / 审核者动作撞上失配）共用
//
// 边界：
//   - 不探活：本文件只负责「已经知道 executor 没了之后怎么办」，
//     「怎么知道的」属各到达口自己（spec §2.2 明确不加周期性探活）
//   - 不碰 adapter：收尾只动 store 与 hub
package agentd

import (
	"log/slog"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// reconcileExecutorGone 收尾一个 executor 已不在的任务：
// 作废挂起工单 → 追加 failed 事件 → 迁 waiting_review → 广播。
//
// 参数：
//   - st/hub: 存储与实时路由
//   - taskID: 待收尾任务
//   - reason: 失配来源的人话说明，直接进 failed 事件的 fail_reason。审核者据此
//     区分「agentd 重启后 executor 已不在」「executor 事件流终结」「恢复操作发现
//     executor 已不在」三种现场——三者的后续处置不同，混成一句话等于丢信息
//   - log: 日志入口
//
// 返回：
//   - 收尾后的任务状态（调用方要回给 CLI 时用）；读任务失败时返回空串
//
// 注意：
//   - 对 waiting_review / completed / failed 是**空操作**：前者本就是待审核终态
//     （追加事件只是噪音），后两者已终结。三个到达口可能对同一任务先后触发，
//     幂等由这条保证
//   - 作废工单排在事件之前，且作废失败只记日志不中断：事件是审核者的主要信息
//     来源，必须落
//   - 追加事件失败则不迁移状态：迁了却没事件 = 审核者看到状态变化却不知原因
func reconcileExecutorGone(st *store.Store, hub *Hub, taskID, reason string, log *slog.Logger) proto.TaskState {
	cur, err := st.GetTask(taskID)
	if err != nil {
		log.Error("对账读取任务失败", "task", taskID, "reason", reason, "cause", err)
		return ""
	}
	log.Info("executor 已不在，开始对账", "task", taskID, "state", cur.State, "reason", reason)
	if cur.State != proto.TaskStateRunning && cur.State != proto.TaskStateWaitingAnswer {
		// 空操作：待审核终态与已终结态都不需要收尾（why 见 doc 注意）
		log.Info("任务无需对账，跳过", "task", taskID, "state", cur.State)
		return cur.State
	}

	if voided, verr := st.VoidPendingTickets(taskID); verr != nil {
		log.Error("对账作废挂起工单失败，继续追加事件", "task", taskID, "cause", verr)
	} else if voided > 0 {
		log.Warn("对账作废挂起工单", "task", taskID, "voided", voided)
	}
	evt, err := st.AppendEvent(taskID, proto.EventTypeFailed, failedPayload{FailReason: reason})
	if err != nil {
		log.Error("对账追加 failed 事件失败，不迁移状态", "task", taskID, "cause", err)
		return cur.State
	}
	if err := recoverTransit(st, taskID, cur.State); err != nil {
		log.Error("对账迁移 waiting_review 失败", "task", taskID, "cause", err)
		return cur.State
	}
	hub.Publish(evt)
	log.Info("对账完成", "task", taskID, "from", cur.State, "to", proto.TaskStateWaitingReview)
	return proto.TaskStateWaitingReview
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestReconcileExecutorGone -v`
Expected: PASS（三个用例全绿）

- [ ] **Step 5: 把 RecoverOnStartup 的内联块换掉**

`watchdog.go` 中 `failed++` 之后的整段（原 220-243 行的事件/迁移/作废/广播）换成一行调用，`kept`/`recovered` 计数逻辑不动：

```go
		failed++
		log.Info("执行器已不在，任务转 waiting_review 交审核者", "task", t.ID, "alive", false, "state", t.State)
		reconcileExecutorGone(st, hub, t.ID, "agentd 重启后执行器已不在", log)
```

`fail_reason` 文本保持「agentd 重启后执行器已不在」一字不变——它是既有事件里已有的文本，改了会让历史事件与新事件对不上。

- [ ] **Step 6: 把 abandonToReview 换掉**

`manager.go:1478` 的函数体换成：

```go
func (m *Manager) abandonToReview(taskID, ticketID string, cause error) proto.TaskState {
	return reconcileExecutorGone(m.st, m.hub, taskID,
		fmt.Sprintf("恢复操作发现 executor 已不在，应答 %s 无法送达: %v", ticketID, cause), m.log)
}
```

doc 注释保留并补一句：收尾实现已统一到 `reconcileExecutorGone`，本函数只负责拼这一句 reason。

- [ ] **Step 7: 跑全量测试**

Run: `go test ./...`
Expected: PASS。`RecoverStuck` 与 `RecoverOnStartup` 的既有用例都必须绿——它们是这次收敛的回归网。

若 `RecoverStuck` 的用例红了：大概率是 `abandonToReview` 旧实现用 `m.transitToReview`（带重试）而新实现用 `recoverTransit`（两跳）。两者对 running/waiting_answer 等价，若某个用例从别的状态进来，说明**旧实现在那个状态下会产出噪音事件**——按新语义修用例，不要改回去。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/reconcile.go internal/agentd/reconcile_test.go internal/agentd/watchdog.go internal/agentd/manager.go && git commit -m "refactor(agentd): 抽出 reconcileExecutorGone，收敛两份 executor 收尾拷贝"
```

---

## Task 4: 到达口② —— `mediate` 退出时对账 + `stopping` 取走式标记

**这是本 spec 修的最主要的洞。** 三个 adapter 在自己的进程/连接死亡时都会 `closeEvents()`，`mediate` 随之退出——然后什么都不做（`manager.go:897` 只打一条「中介循环结束」）。B21 现场的任务就是这样静止 1 小时无任何信号。

**Files:**
- Modify: `internal/agentd/manager.go`（`Manager` 结构体加 `stopping`；`mediate` 尾部；`Done`/`Stop` 里 `ad.Stop` 之前加 `noteStopping`）
- Modify: `internal/agentd/reconcile.go`（加标记方法与 `Manager` 薄包装）
- Test: `internal/agentd/reconcile_test.go`

**Interfaces:**
- Consumes: `reconcileExecutorGone`（Task 3）
- Produces: `(*Manager).noteStopping(taskID string)` / `(*Manager).takeStopping(taskID string) bool` / `(*Manager).reconcileExecutorGone(taskID, reason string) proto.TaskState`

- [ ] **Step 1: 先写失败的测试**

追加到 `internal/agentd/reconcile_test.go`：

```go
// TestMediateReconcilesOnEventsClosed 到达口②：adapter 关闭事件通道 = executor 终结，
// mediate 退出后必须对账——否则任务停在 running 直到 2h 看门狗（B21 实测静止 1 小时）。
func TestMediateReconcilesOnEventsClosed(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateRunning})
	done := make(chan struct{})
	go func() { m.mediate("t1"); close(done) }()
	close(ad.evCh) // executor 终结
	<-done

	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("事件通道关闭后应对账落 waiting_review，实际 %s", cur.State)
	}
	evs, err := st.EventsFromAsc("t1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("应产出 failed 事件说明 executor 终结")
	}
}

// TestStoppingMarkerSuppressesReconcile 主动停止不该被当成异常终结：
// Manager.Stop 先调 ad.Stop() 再落 failed，中间的窗口里对账会看到 running，
// 补一条噪音 failed 事件并造成 running→waiting_review→failed 的状态抖动。
func TestStoppingMarkerSuppressesReconcile(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateRunning})
	m.noteStopping("t1")
	done := make(chan struct{})
	go func() { m.mediate("t1"); close(done) }()
	close(ad.evCh)
	<-done

	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("主动停止期间不应对账，状态应留在 running，实际 %s", cur.State)
	}
	evs, err := st.EventsFromAsc("t1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("主动停止期间不应产出对账事件，实际 %d 条", len(evs))
	}
}

// TestStoppingMarkerIsTakeStyle 取走式：标记的生命周期就是一次主动停止。
// 若标记长期驻留，下一次 executor 猝死会被上一次的主动停止误抑制，就再没人对账了。
func TestStoppingMarkerIsTakeStyle(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	m.noteStopping("t1")
	if !m.takeStopping("t1") {
		t.Fatalf("首次取走应为 true")
	}
	if m.takeStopping("t1") {
		t.Fatalf("标记必须取走即失效，第二次应为 false")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestMediateReconciles|TestStoppingMarker' -v`
Expected: FAIL（`undefined: noteStopping`；`TestMediateReconciles` 状态停在 running）

- [ ] **Step 3: 加 stopping 标记**

`manager.go` 的 `Manager` 结构体末尾追加字段：

```go
	// stopping 是「接下来这次事件通道关闭是我们自己发起的」的意图标记
	// （apMu 之外单独用 mu 保护）。why 见 reconcile.go 的 noteStopping。
	mu       sync.Mutex
	stopping map[string]struct{}
```

`NewManager` 里初始化 `stopping: map[string]struct{}{}`。

在 `reconcile.go` 追加：

```go
// noteStopping 标记「接下来这次事件通道关闭是我们自己发起的」。
//
// 必须在 ad.Stop() **之前**调用。
//
// why（为什么需要这个标记）：Manager.Stop 先调 ad.Stop() 再落 failed，两步之间
// adapter 已经关掉了事件通道，mediate 随之退出并对账——此时任务状态还是 running，
// 于是补一条它不该有的 failed 事件，并造成 running→waiting_review→failed 的
// 状态抖动（末跳合法，所以不硬失败，但事件是噪音）。
//
// why（为什么不改 Stop 的顺序）：先落 failed 再 ad.Stop() 会让 executor 在状态
// 已定型后仍可能产出事件，各 handler 的「已终结则丢弃」判断要散在更多路径上，
// 风险大于收益。显式标记说的正是「这次关闭是我们自己关的」，诚实且局部。
func (m *Manager) noteStopping(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopping[taskID] = struct{}{}
}

// takeStopping 取走并清空标记，返回本次关闭是否为主动停止。
//
// why（取走式而非常驻）：标记的生命周期就是一次主动停止。若它长期驻留，下一次
// executor 猝死会被上一次的主动停止误抑制——真出事时反而没人对账。与 grok
// adapter 的 takeAskedViaTool、opencode 的 takeTurnRejected 同源。
func (m *Manager) takeStopping(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.stopping[taskID]
	delete(m.stopping, taskID)
	return ok
}

// reconcileExecutorGone 是包级同名函数的方法薄包装（省去调用点重复传 st/hub/log）。
func (m *Manager) reconcileExecutorGone(taskID, reason string) proto.TaskState {
	return reconcileExecutorGone(m.st, m.hub, taskID, reason, m.log)
}
```

- [ ] **Step 4: 接上 mediate 尾部**

`manager.go:894-898` 换成：

```go
	for ev := range events {
		m.handleEvent(taskCtx, taskID, ev)
	}
	// 事件通道关闭 = executor 终结。这是「executor 已不在」最常见的到达口——
	// 三个 adapter 在进程/连接死亡时都会 closeEvents()。不在这里对账，任务会
	// 一直停在 running 直到 2h 看门狗（B21 实测：静止 1 小时无任何信号）
	if m.takeStopping(taskID) {
		m.log.Info("中介循环结束（主动停止，跳过对账）", "task", taskID)
		return
	}
	m.log.Info("中介循环结束，开始对账", "task", taskID)
	m.reconcileExecutorGone(taskID, "executor 事件流已终结（进程退出或连接断开）")
```

- [ ] **Step 5: Done / Stop 里在 ad.Stop 之前打标记**

`manager.go:655-660`（`Done`）：

```go
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("解析任务执行者失败", "task", taskID, "cause", err)
	} else {
		m.noteStopping(taskID) // 必须在 Stop 之前：Stop 会关掉事件通道
		if err := ad.Stop(taskID); err != nil {
			m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
		}
	}
```

`manager.go:728-733`（`Stop`）同款改写（`m.log.Warn("停止 executor 失败，继续落 failed", ...)` 保留）。

`Done` 其实本来就安全（此时状态已是 completed，对账是空操作），但用同一个标记保持一致——避免以后调整顺序时重新踩进来。

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/agentd/ -run 'TestMediateReconciles|TestStoppingMarker' -v && go test ./...`
Expected: 全 PASS

集成测试若出现「多了一条 failed 事件」的红：确认那条路径是不是走了 `ad.Stop()` 却没 `noteStopping`。补标记，不要放宽断言。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/reconcile.go internal/agentd/reconcile_test.go && git commit -m "fix(agentd): 事件通道关闭即对账，主动停止用取走式标记抑制"
```

---

## Task 5: 空回合不静默（三个 adapter）

B21 的直接信号。opencode 的 `mapIdle` 在零文本时只打一条 WARN 就 return，任务停在 running；grok/claude 的兜底分支在文本为空时会产出一张**空工单**——审核者收到一个没有内容的问题。

**Files:**
- Modify: `internal/executor/opencode/adapter.go:1253-1272`（`mapIdle`）
- Modify: `internal/executor/grok/adapter.go:450-469`（`finishTurn` 兜底分支）
- Modify: `internal/executor/claudecode/adapter.go:733-749`（`fallbackClassify`）
- Test: `internal/executor/opencode/adapter_test.go`、`internal/executor/grok/adapter_test.go`、`internal/executor/claudecode/adapter_test.go`

**Interfaces:**
- Consumes: `executor.Result{OK: false, FailReason: string}`；manager 侧 `handleResult` 对 `OK=false` 的既有处置（作废工单 → failed 事件 → `transitToReview`，`manager.go:1591-1606`）正是要的，无需改动
- Produces: 无新导出符号

- [ ] **Step 1: 先写 opencode 的失败测试**

追加到 `internal/executor/opencode/adapter_test.go`（同包白盒，参考文件内已有的 `mapIdle` 用例造 `runState` 的方式）：

```go
// TestMapIdleEmptyTurnEmitsFailedResult 空回合必须产出失败结果而不是静默。
//
// 现场（B21）：opencode 连做 7 个回合工具调用后，最后一步 step-finish 的
// reason=unknown、tokens 全 0（供应商流中断），会话随即 idle、零文本。旧实现
// 只打一条 WARN 就 return，任务停在 running 静止 1 小时，审核者要等 2h 看门狗。
func TestMapIdleEmptyTurnEmitsFailedResult(t *testing.T) {
	a := New(quietLogger())
	r := a.newRun("t1", t.TempDir(), t.TempDir())
	a.mapIdle(r, json.RawMessage(`{"type":"session.idle"}`))

	select {
	case ev := <-r.evCh:
		if ev.Type != "result" {
			t.Fatalf("空回合应产出 result，实际 %s", ev.Type)
		}
		if ev.Result == nil || ev.Result.OK {
			t.Fatalf("空回合应是失败结果: %+v", ev.Result)
		}
		if ev.Result.FailReason == "" {
			t.Fatalf("FailReason 必须写清现场，否则审核者不知道发生了什么")
		}
	default:
		t.Fatalf("空回合静默（无事件产出）——这正是 B21 的缺陷")
	}
}

// TestMapIdleRejectedTurnStillAsks 回归：被拒终止的空回合仍走 question，不受本改动影响。
// 那个现场有内容可问（「我拒了这些权限，接下来怎么办」），零文本回合没有。
func TestMapIdleRejectedTurnStillAsks(t *testing.T) {
	a := New(quietLogger())
	r := a.newRun("t1", t.TempDir(), t.TempDir())
	r.noteRejected("perm-1")
	a.mapIdle(r, json.RawMessage(`{"type":"session.idle"}`))

	select {
	case ev := <-r.evCh:
		if ev.Type != "question" {
			t.Fatalf("被拒终止的空回合应仍走 question，实际 %s", ev.Type)
		}
	default:
		t.Fatalf("被拒终止的空回合应产出事件")
	}
}
```

（`quietLogger()` 若该包内尚无同名 helper，按包内既有测试的 logger 构造方式来。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestMapIdle -v`
Expected: `TestMapIdleEmptyTurnEmitsFailedResult` FAIL（"空回合静默"）

- [ ] **Step 3: 改 opencode 的 mapIdle**

`adapter.go:1269-1271` 换成：

```go
		// 零文本回合转失败结果交审核者（B21）：旧实现在此静默 return，任务停在
		// running 直到 2h 看门狗。
		//
		// 为什么是 result{OK:false} 而不是 question：上面「被拒终止」那条走
		// question，因为那个现场有内容可问；零文本回合没有任何东西可问，它是一份
		// 故障报告——result{OK:false} 的语义才对得上，且 FailReason 能把现场
		// 写清楚。manager 的 handleResult 对 OK=false 的既有处置（作废挂起工单 →
		// failed 事件 → 落 waiting_review）正是我们要的，continue 立刻可用
		a.log.Warn("idle 但回合无文本，转失败结果交审核者", "task", r.taskID,
			"event", turn.TailRunes(string(raw), 120))
		a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.session, Result: &executor.Result{
			OK: false,
			FailReason: "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，" +
				"可 continue 续接重试",
		}})
		r.clearTurn()
		r.captureStartCommit(a)
		return
```

同时更新 `mapIdle` 的 doc 注释：把「空回合跳过分类并 Warn」改成「空回合转失败结果交审核者」。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run TestMapIdle -v`
Expected: PASS（两个用例）

- [ ] **Step 5: 写 grok 的空文本守卫测试**

追加到 `internal/executor/grok/adapter_test.go`：

```go
// TestFinishTurnEmptyTextEmitsFailedResult 兜底分支的空文本守卫。
//
// 旧实现在无新提交时 emit question 携带回合文本，文本为空时产出的是一张**空工单**
// ——审核者收到一个没有内容的问题，除了瞎猜什么也做不了。零文本是故障，按故障报。
func TestFinishTurnEmptyTextEmitsFailedResult(t *testing.T) {
	a := New(quietLogger())
	r := newTestRun(t, a) // 按包内既有 helper 构造；repoPath 用非 git 目录即可（hasNew=false）
	a.finishTurn(r, ACPResult{Result: json.RawMessage(`{"stopReason":"end_turn"}`)})

	select {
	case ev := <-r.evCh:
		if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
			t.Fatalf("零文本且无新提交应产出失败结果，实际 %s %+v", ev.Type, ev.Result)
		}
	default:
		t.Fatalf("零文本回合应产出事件")
	}
}
```

- [ ] **Step 6: 改 grok 的 finishTurn**

`adapter.go:468`（`default` 分支末尾的 `a.emit(... question ...)`）之前插入守卫：

```go
			// 空文本守卫：文本为空时 question 产出的是一张空工单，审核者收到一个
			// 没有内容的问题。零文本是故障报告，不是问题（与 opencode mapIdle
			// 的空回合处置对称）
			if strings.TrimSpace(text) == "" {
				a.log.Warn("回合零文本且无新提交，转失败结果交审核者", "task", r.taskID)
				a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
					Result: &executor.Result{OK: false, SessionID: r.sessionID,
						FailReason: "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试"}})
				return
			}
			a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
```

注意插在 `askedViaTool` 判断**之后**：已经通过原生提问工具问过的回合本就该闭嘴，那条判断优先（`adapter.go:463-467`）。

- [ ] **Step 7: 改 claude 的 fallbackClassify**

`adapter.go:741-743` 的 question 分支同款加守卫（文本为空 → `result{OK:false}`，`SessionID: r.session`）。FailReason 用同一句，三个 adapter 保持对称。

- [ ] **Step 8: 写 claude 的对应测试并跑全量**

参照 Step 5 在 `internal/executor/claudecode/adapter_test.go` 写一个同构用例。

Run: `go test ./internal/executor/... && go test ./...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/executor/opencode/adapter.go internal/executor/grok/adapter.go internal/executor/claudecode/adapter.go internal/executor/*/adapter_test.go && git commit -m "fix(executor): 空回合转失败结果，三个 adapter 不再静默/不再产空工单"
```

---

## Task 6: `Reap` 兜底回收与信号对称（B20）

B20 现场：`done` 归档时 `Stop` 返回 `ErrTaskNotRunning`（agentd 曾重启、内存运行态已丢），代码只打一条 ERROR 继续归档，结果 tmux 会话 `handoff-46e84025` 与其 `opencode serve` 孤儿存活 11.5 小时。**两个可改点**：确定性兜底没用上（会话名恒为 `handoff-<id8>`），信号不对称（worktree 清理失败会发事件，executor 停不掉却完全静默）。

**Files:**
- Create: `internal/executor/opencode/reap.go`、`internal/executor/grok/reap.go`、`internal/executor/claudecode/reap.go`
- Modify: `internal/agentd/manager.go`（`reaper` 接口 + `stopExecutor` 辅助 + `Done`/`Stop` 接线）
- Modify: `internal/agentd/reconcile.go`（`stopExecutor` 放这里，与 `noteStopping` 同处）
- Test: `internal/agentd/reconcile_test.go`、各 adapter 包的 `reap_test.go`

**Interfaces:**
- Consumes: 各包已有的 `id8(s string) string`（`opencode/proc.go:421`、`grok/proc.go:277`、`claudecode/proc.go:491`）与 tmux kill 能力（`Proc.Kill()`）
- Produces:
  - 各 adapter：`func (a *Adapter) Reap(taskID, taskDir string) error`
  - manager：`reaper interface{ Reap(taskID, taskDir string) error }`、`(*Manager) stopExecutor(taskID string, ad executor.Adapter)`

- [ ] **Step 1: 先写 manager 侧的失败测试**

追加到 `internal/agentd/reconcile_test.go`：

```go
// reapAdapter 是实现 reaper 的测试 adapter：Stop 一律返 ErrTaskNotRunning
// （模拟 agentd 重启后内存运行态已丢），Reap 的结果可注入。
type reapAdapter struct {
	chanAdapter
	mu       sync.Mutex
	reapErr  error
	reapHits int
}

func (a *reapAdapter) Stop(taskID string) error {
	return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
}

func (a *reapAdapter) Reap(taskID, taskDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reapHits++
	return a.reapErr
}

// TestStopExecutorFallsBackToReap Stop 报 ErrTaskNotRunning 时必须走确定性兜底回收。
// B20 现场：不兜底，孤儿 tmux 会话 + serve 存活了 11.5 小时。
func TestStopExecutorFallsBackToReap(t *testing.T) {
	ad := &reapAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview})
	m.stopExecutor("t1", ad)

	ad.mu.Lock()
	hits := ad.reapHits
	ad.mu.Unlock()
	if hits != 1 {
		t.Fatalf("Reap 应被调用 1 次，实际 %d", hits)
	}
}

// TestStopExecutorEmitsEventWhenReapFails 信号对称：回收不掉必须留事件。
// worktree 清理失败会发 progress 提示人工，executor 停不掉却完全静默——
// 审核者根本无从知道有残留（B20 的第二个可改点）。
func TestStopExecutorEmitsEventWhenReapFails(t *testing.T) {
	ad := &reapAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		reapErr: errors.New("tmux 不可用")}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "abcdef12-3456-7890-abcd-ef1234567890",
		RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingReview})
	m.stopExecutor("abcdef12-3456-7890-abcd-ef1234567890", ad)

	evs, err := st.EventsFromAsc("abcdef12-3456-7890-abcd-ef1234567890", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress && strings.Contains(string(e.Payload), "handoff-abcdef12") {
			found = true
		}
	}
	if !found {
		t.Fatalf("回收失败应产出带会话名的 progress 事件，实际事件: %v", evs)
	}
}
```

（`e.Payload` 的字段名以 `proto.Event` 实际定义为准；意图是断言事件文本里带得出会话名，审核者能直接照着敲 `tmux kill-session`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestStopExecutor -v`
Expected: FAIL，`m.stopExecutor undefined`

- [ ] **Step 3: 实现 manager 侧的 reaper 与 stopExecutor**

`manager.go` 的 `restorer` 旁边加：

```go
// reaper 是「无内存运行态时按确定性命名兜底回收」的可选 adapter 能力
// （三个真实 adapter 均实现，fake 不实现）。
//
// 为什么单开一个方法而不是让 Stop 自己兜底：Stop 只拿得到 taskID，拿不到 taskDir
// （proc 信息文件在里面）；给 Stop 加参数会改动五动作核心契约、波及 fake 等全部实现。
type reaper interface {
	Reap(taskID, taskDir string) error
}
```

`reconcile.go` 加：

```go
// stopExecutor 停 executor，并在「没有内存运行态」时按确定性命名兜底回收。
//
// 参数：
//   - taskID: 目标任务
//   - ad: 已解析的 adapter（调用方已做 adapterFor）
//
// 注意：
//   - 调用前必须已 noteStopping（本函数会关掉事件通道，mediate 随之退出）
//   - 任何失败都不中断调用方（归档/中止本身已经达成）；回收不掉时留 progress
//     事件提示人工——与 worktree 清理失败的信号对称。B20 现场的孤儿存活 11.5
//     小时，正是因为完全静默、没人知道它在
func (m *Manager) stopExecutor(taskID string, ad executor.Adapter) {
	m.noteStopping(taskID)
	err := ad.Stop(taskID)
	if err == nil {
		return
	}
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		// executor 还在，只是这次没停掉：保持既有语义（只记日志），
		// 兜底回收对它无意义——真去 kill 会话反而可能杀掉正在收尾的进程
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
		return
	}
	rp, ok := ad.(reaper)
	if !ok {
		m.log.Warn("executor 无内存运行态且 adapter 不支持兜底回收", "task", taskID, "cause", err)
		return
	}
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	session := "handoff-" + shortID(taskID)
	m.log.Info("executor 无内存运行态，按确定性命名兜底回收", "task", taskID, "tmux", session)
	if rerr := rp.Reap(taskID, taskDir); rerr != nil {
		m.log.Error("兜底回收失败，留事件提示人工", "task", taskID, "tmux", session, "cause", rerr)
		evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{
			Text: fmt.Sprintf("executor 资源可能残留：tmux 会话 %s，请手动 tmux kill-session -t %s（原因：%v）",
				session, session, rerr),
		})
		if aerr != nil {
			m.log.Error("追加兜底回收失败事件失败", "task", taskID, "cause", aerr)
			return
		}
		m.hub.Publish(evt)
		return
	}
	m.log.Info("按确定性命名兜底回收成功", "task", taskID, "tmux", session)
}

// shortID 取 id 前 8 字符，与三个 adapter 的 tmux 会话命名规则
// （"handoff-" + id8(taskID)）一致。manager 只用它拼给人看的提示文本，
// 真正的回收由 adapter 自己按同一规则完成。
func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
```

- [ ] **Step 4: Done / Stop 接线**

Task 4 里加的 `m.noteStopping(taskID)` + `ad.Stop(taskID)` 两行，在 `Done` 与 `Stop` 里都换成 `m.stopExecutor(taskID, ad)`（`noteStopping` 已被它包住）。

- [ ] **Step 5: 跑 manager 侧测试**

Run: `go test ./internal/agentd/ -run TestStopExecutor -v`
Expected: PASS

- [ ] **Step 6: 实现三个 adapter 的 Reap**

opencode（`internal/executor/opencode/reap.go`）：

```go
// reap.go —— 无内存运行态时的确定性兜底回收。
//
// 职责：
//   - Reap：按 serve.json 或确定性命名找到 tmux 会话并杀掉
//
// 边界：
//   - 不碰任务状态（adapter 不写 store）；回收不掉只返回错误，留不留事件是 manager 的事
package opencode

import (
	"log/slog"
	"path/filepath"
)

// Reap 在没有内存运行态时按确定性命名兜底回收 executor 侧资源。
//
// 回收顺序：
//  1. 读 taskDir 下的 serve.json 拿 tmux 会话名（最准，端口/密码也在里面）
//  2. 文件缺失/损坏时退到确定性命名 "handoff-" + id8(taskID)（与 StartServe 同规则）
//  3. kill 会话
//
// 返回：
//   - 会话本就不存在时返回 nil——目标是「确保它没了」，不是「确保我杀了它」
func (a *Adapter) Reap(taskID, taskDir string) error {
	session := "handoff-" + id8(taskID)
	source := "确定性命名"
	if si, err := readServeInfo(taskDir); err == nil && si.TmuxSession != "" {
		session, source = si.TmuxSession, "serve.json"
	} else if err != nil {
		a.log.Warn("读 serve.json 失败，退到确定性命名回收", "task", taskID, "cause", err)
	}
	a.log.Info("兜底回收 executor 资源", "task", taskID, "tmux", session, "source", source)
	p := &Proc{TmuxSession: session, ServeLogPath: filepath.Join(taskDir, serveLogFileName)}
	if err := p.Kill(); err != nil {
		// 会话已经不在时 tmux kill-session 也会报错——先确认它是不是真没了
		if !p.Alive() {
			a.log.Info("兜底回收：会话已不存在，视为成功", "task", taskID, "tmux", session)
			return nil
		}
		return err
	}
	return nil
}
```

grok 与 claude 同构：grok 读 `ReadServeInfo(taskDir)` 拿 `Session`；claude 读 `readProcInfo(taskDir)` 拿 `TmuxSession`，并按 `claudecode/proc.go:393` 的 `tmuxKill` 回收。

**claude 的一处特别之处**：`tmuxHasSession` 对它不是存活判据（窗口 1 的 `tail -f render.log` 会一直吊着会话），但对 `Reap` **正合适**——`Reap` 要确认的就是「tmux 会话没了」，不是「claude 进程没了」。所以 claude 的 `Reap` 用 `tmuxHasSession` 做「已经不在」的判定。

- [ ] **Step 7: 每个 adapter 写一个 Reap 单测**

三个包各建 `reap_test.go`。以 opencode 为例（`Kill` 走 tmux，测试里把包内的 tmux 执行点替成假实现；若该包没有可替换点，就只测「serve.json 缺失时退到确定性命名」这条可断言的分支）：

```go
// TestReapFallsBackToDeterministicName serve.json 缺失时按确定性命名回收——
// B20 现场正是「运行态丢了但会话名恒为 handoff-<id8>」，兜底完全可用。
func TestReapFallsBackToDeterministicName(t *testing.T) {
	var killed string
	restore := swapTmuxKill(func(session string) error { killed = session; return nil })
	defer restore()

	a := New(quietLogger())
	if err := a.Reap("abcdef12-3456", t.TempDir()); err != nil { // 空 taskDir，无 serve.json
		t.Fatal(err)
	}
	if killed != "handoff-abcdef12" {
		t.Fatalf("应按确定性命名回收，实际杀了 %q", killed)
	}
}
```

- [ ] **Step 8: 跑全量测试**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/executor/*/reap.go internal/executor/*/reap_test.go internal/agentd/manager.go internal/agentd/reconcile.go internal/agentd/reconcile_test.go && git commit -m "feat(executor): 加 Reap 确定性兜底回收，回收失败留 progress 事件"
```

---

## Task 7: `Continue` 接入恢复阶梯

B24 的续接出口。`Continue` 现在 `Send` 一失败就回迁 `waiting_review` 报 409「运行态已丢失，请重新派发」——而会话数据其实就在磁盘上。

**Files:**
- Modify: `internal/agentd/manager.go:583-620`（`Continue`）
- Test: `internal/agentd/reconcile_test.go`

**Interfaces:**
- Consumes: `restorer`（Task 2）、`executor.ErrTaskNotRunning`
- Produces: 无新导出符号；对 CLI 的可观测变化是两条 progress 事件文案

- [ ] **Step 1: 先写失败的测试**

追加到 `internal/agentd/reconcile_test.go`：

```go
// ladderAdapter 是恢复阶梯的测试 adapter：首次 Send 返 ErrTaskNotRunning，
// Resume 之后的 Send 成功。Resume 的返回值可注入。
type ladderAdapter struct {
	chanAdapter
	mu       sync.Mutex
	resumed  bool
	gotReq   executor.ResumeReq
	outcome  executor.ResumeOutcome
	sendHits int
}

func (a *ladderAdapter) Send(ctx context.Context, taskID, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sendHits++
	if !a.resumed {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	return nil
}

func (a *ladderAdapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gotReq = req
	if a.outcome.Alive {
		a.resumed = true
	}
	return a.outcome, nil
}

// TestContinueColdResumesAndRetriesSend Send 撞 ErrTaskNotRunning 时走恢复阶梯：
// Cold=true 冷恢复 → 重试 Send 一次 → 任务留在 running。
// 这是 B24「waiting_review 任务成孤儿、只能新开任务」的出口。
func TestContinueColdResumesAndRetriesSend(t *testing.T) {
	ad := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeCold,
			SessionID: "sess-1", Note: "已从磁盘载入原会话"}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1"})

	if err := m.Continue(context.Background(), "t1", "继续干"); err != nil {
		t.Fatalf("冷恢复成功后 continue 应成功: %v", err)
	}
	ad.mu.Lock()
	req, hits := ad.gotReq, ad.sendHits
	ad.mu.Unlock()
	if !req.Cold {
		t.Fatalf("continue 触发的恢复必须 Cold=true（按需冷恢复，spec §4）")
	}
	if hits != 2 {
		t.Fatalf("Send 应被重试恰好一次（共 2 次），实际 %d", hits)
	}
	cur, _ := st.GetTask("t1")
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("续接成功后应留在 running，实际 %s", cur.State)
	}
}

// TestContinueColdResumeEmitsProgressEvent 冷恢复/降级必须产出事件而不只是日志。
// fresh 尤其重要：上下文断了是审核者需要知道的事实——它直接决定下一条指令
// 要不要重述背景。只写日志等于让审核者在不知情的前提下继续对话。
func TestContinueColdResumeEmitsProgressEvent(t *testing.T) {
	ad := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeFresh,
			SessionID: "sess-new"}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview, ExecutorSession: "sess-old"})

	if err := m.Continue(context.Background(), "t1", "继续干"); err != nil {
		t.Fatal(err)
	}
	cur, _ := st.GetTask("t1")
	if cur.ExecutorSession != "sess-new" {
		t.Fatalf("fresh 的新会话 id 必须落库，实际 %q", cur.ExecutorSession)
	}
	evs, _ := st.EventsFromAsc("t1", 0, 100)
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress && strings.Contains(string(e.Payload), "sess-new") {
			found = true
		}
	}
	if !found {
		t.Fatalf("降级新会话必须产出带新会话 id 的 progress 事件，实际: %v", evs)
	}
}

// TestContinueUnrecoverableFallsBackToReview 阶梯全走完仍不可恢复：
// 回迁 waiting_review（不让任务死在 running），错误里带 Note 说明原因。
func TestContinueUnrecoverableFallsBackToReview(t *testing.T) {
	ad := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: false, Note: "会话数据已不在磁盘上"}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1"})

	err := m.Continue(context.Background(), "t1", "继续干")
	if err == nil {
		t.Fatalf("不可恢复应返回错误")
	}
	if !strings.Contains(err.Error(), "会话数据已不在磁盘上") {
		t.Fatalf("错误应带 Outcome.Note 让审核者知道为什么: %v", err)
	}
	cur, _ := st.GetTask("t1")
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("不可恢复应回迁 waiting_review，实际 %s", cur.State)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestContinue -v`
Expected: 三个新用例 FAIL（现状是 Send 一失败就回迁并报错）

- [ ] **Step 3: 实现阶梯**

`manager.go:613-619` 换成：

```go
	if err := ad.Send(ctx, taskID, instructions); err != nil {
		if !errors.Is(err, executor.ErrTaskNotRunning) {
			// executor 还在，只是这次没打通：保持原语义，回迁让审核者可重试
			m.log.Error("续发指令失败", "task", taskID, "cause", err)
			m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 发送失败回迁")
			return fmt.Errorf("续发指令: %w", err)
		}
		m.log.Warn("续发指令时 executor 已不在，进入恢复阶梯", "task", taskID, "cause", err)
		if rerr := m.resumeForContinue(ctx, taskID, ad); rerr != nil {
			m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 恢复失败回迁")
			return rerr
		}
		// 重试只做一次：重试的前提是「刚刚成功建立了运行态」，这个前提一次就够
		// 验证。循环重试只会在 executor 反复启动失败时放大伤害
		if err := ad.Send(ctx, taskID, instructions); err != nil {
			m.log.Error("恢复后重试续发仍失败", "task", taskID, "cause", err)
			m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 恢复后发送失败回迁")
			return fmt.Errorf("恢复后续发指令: %w", err)
		}
		m.log.Info("恢复后续发指令成功", "task", taskID)
	}
	return nil
}

// resumeForContinue 是 continue 撞上「executor 已不在」时的恢复阶梯（spec §5.4）。
//
// 与启动恢复的关键差别是 Cold=true：审核者手里正好有一条指令要送，把会话拉起来
// 立刻有用；而 agentd 启动时冷恢复等于凭空拉起一堆没人跟它说话的 executor。
//
// 返回：
//   - nil: 已拿到可用运行态，调用方可以重试 Send
//   - 非 nil: 不可恢复，错误里带 Outcome.Note（server 映射 409 时回显给审核者）
//
// 注意：
//   - Mode != reattach 时必须产出 progress 事件：冷恢复换了进程、fresh 断了
//     上下文，都是审核者需要知道的事实（fresh 直接决定下一条指令要不要重述背景）
func (m *Manager) resumeForContinue(ctx context.Context, taskID string, ad executor.Adapter) error {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	r, ok := ad.(restorer)
	if !ok {
		return fmt.Errorf("任务 %s 的执行者不支持恢复，请重新派发: %w", taskID, executor.ErrTaskNotRunning)
	}
	execName := task.Executor
	if execName == "" {
		execName = m.cfg.Executor.Default
	}
	envKVs, eerr := m.env.For(execName)
	if eerr != nil {
		m.log.Warn("恢复解析 env 失败，按空 env 继续", "task", taskID, "cause", eerr)
	}
	m.log.Info("进入冷恢复", "task", taskID, "executor", execName, "session", task.ExecutorSession)
	out, err := r.Resume(executor.ResumeReq{
		TaskID: taskID, TaskDir: filepath.Join(m.cfg.DataDir, "tasks", taskID),
		RepoPath: task.Workdir(), SessionID: task.ExecutorSession,
		Env: envKVs, Model: task.Model, Cold: true,
	})
	if err != nil {
		m.log.Error("恢复失败", "task", taskID, "cause", err)
		return fmt.Errorf("恢复任务 %s 执行: %w", taskID, err)
	}
	m.log.Info("恢复结果", "task", taskID, "alive", out.Alive, "mode", out.Mode,
		"session", out.SessionID, "note", out.Note)
	if !out.Alive {
		note := out.Note
		if note == "" {
			note = "executor 运行态已丢失且无法重建"
		}
		return fmt.Errorf("任务 %s 无法恢复：%s", taskID, note)
	}
	if out.SessionID != "" && out.SessionID != task.ExecutorSession {
		if serr := m.st.SetTaskField(taskID, "executor_session", out.SessionID); serr != nil {
			m.log.Warn("落库新 executor_session 失败", "task", taskID,
				"session", out.SessionID, "cause", serr)
		}
	}
	// 重连（executor 一直活着）对审核者是无感事件，不打扰；换了进程或断了上下文才播报
	if out.Mode == executor.ResumeModeReattach {
		return nil
	}
	text := fmt.Sprintf("executor 进程已不在，已重启并从磁盘载入原会话 %s，上下文完整", out.SessionID)
	if out.Mode == executor.ResumeModeFresh {
		text = fmt.Sprintf("原会话 %s 已不可载入，已新开会话 %s；上下文从本条指令开始，必要时请在指令中重述背景",
			task.ExecutorSession, out.SessionID)
	}
	evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: text})
	if aerr != nil {
		m.log.Error("追加恢复播报事件失败", "task", taskID, "cause", aerr)
		return nil // 事件没落住不影响续接本身
	}
	m.hub.Publish(evt)
	return nil
}
```

同时更新 `Continue` 的 doc 注释，把恢复阶梯写进「注意」。

- [ ] **Step 4: 恢复成功后要重启中介循环**

`resumeForContinue` 成功后 adapter 已建起新的事件通道，但**没有 mediate 在消费它**（原来的那个已经随通道关闭退出了）。在 `Continue` 的 `ad.Send` 重试成功之后补一行：

```go
		m.log.Info("恢复后续发指令成功，重启中介循环", "task", taskID)
		go m.mediate(taskID)
```

漏了这一行，症状是「continue 报成功但审核者再也收不到任何事件」——比原来的 409 更糟。

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/agentd/ -run TestContinue -v && go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/reconcile_test.go && git commit -m "feat(agentd): continue 接入四级恢复阶梯，冷恢复后重试一次并重启中介循环"
```

---

## Task 8: grok 冷恢复（改动最小的一个）

现有 `Resume` 是 `ReadServeInfo → proc.Alive() → EnsureAuthLink → DialACP → initialize → session/load`。**改动只在第二步**——`session/load` 那段代码一行不动。

**Files:**
- Modify: `internal/executor/grok/resume.go`
- Test: `internal/executor/grok/resume_test.go`

**Interfaces:**
- Consumes: `StartServe(ctx, repoPath, taskID, taskDir, model, env, log) (*Proc, error)`（`grok/proc.go:132`）、`EnsureAuthLink`、`DialACP`
- Produces: `Resume` 在 `Cold=true` 且进程已死时返回 `Mode=cold`（或 `session/load` 失败时 `Mode=fresh`）

- [ ] **Step 1: 先写失败的测试**

追加到 `internal/executor/grok/resume_test.go`：

```go
// TestResumeColdDisallowedStaysDead Cold=false 时进程已死即判不可恢复（启动恢复语义不变）。
func TestResumeColdDisallowedStaysDead(t *testing.T) {
	// 造一个 serve.json 指向一个必然探不活的端口
	dir := t.TempDir()
	writeDeadServeInfo(t, dir) // 包内 helper：写一个端口不通的 serve.json
	a := New(quietLogger())
	out, err := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: dir,
		RepoPath: t.TempDir(), SessionID: "sess-1", Cold: false})
	if err != nil {
		t.Fatal(err)
	}
	if out.Alive {
		t.Fatalf("Cold=false 且进程已死应判不可恢复")
	}
}

// TestResumeColdRestartFailureIsNotAnError 冷恢复起不来是可预期现场
// （配额耗尽、凭据过期），按不可恢复处理而非程序错误——返回 error 会让
// manager 侧的日志把它当故障刷 Error，掩盖真正的程序缺陷。
func TestResumeColdRestartFailureIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	restore := swapStartServe(func(...) (*Proc, error) { return nil, errors.New("配额耗尽") })
	defer restore()

	a := New(quietLogger())
	out, err := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: dir,
		RepoPath: t.TempDir(), SessionID: "sess-1", Cold: true})
	if err != nil {
		t.Fatalf("起不来不应返回 error，应判不可恢复: %v", err)
	}
	if out.Alive {
		t.Fatalf("起不来应 Alive=false")
	}
	if out.Note == "" {
		t.Fatalf("Note 必须写清为什么恢复不了，审核者要看到这句")
	}
}
```

（`swapStartServe` 需要把 `StartServe` 的调用点提成包级 `var startServe = StartServe` 才能替换——这是为可测性做的最小改动，在 Step 2 一并完成。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/grok/ -run TestResumeCold -v`
Expected: FAIL

- [ ] **Step 3: 实现冷恢复**

`resume.go:55-58` 的 `if !proc.Alive() { ... return }` 换成：

```go
	mode := executor.ResumeModeReattach
	if !proc.Alive() {
		if !req.Cold {
			a.log.Info("serve 已不在且不允许冷恢复，判不可恢复",
				"task", req.TaskID, "port", proc.Port)
			return executor.ResumeOutcome{Alive: false,
				Note: "serve 进程已不在（本次只允许热重连）"}, nil
		}
		// 冷恢复：会话数据在 <taskDir>/grokhome/sessions/<urlencode(cwd)>/<session-id>/，
		// 只要 taskDir 在它就在。重起一个 serve（新端口，GROK_HOME 不变）后，
		// 下面的 session/load 原样可用——这是三个 adapter 里改动最小的一个
		a.log.Info("serve 已不在，进入冷恢复", "task", req.TaskID,
			"old_port", proc.Port, "session", req.SessionID)
		newProc, err := startServe(context.Background(), req.RepoPath, req.TaskID,
			req.TaskDir, req.Model, req.Env, a.log)
		if err != nil {
			// 起不来是可预期现场（配额/凭据过期），按不可恢复处理而非错误
			a.log.Warn("冷恢复重起 serve 失败，判不可恢复", "task", req.TaskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("重起 grok serve 失败：%v", err)}, nil
		}
		proc = newProc
		mode = executor.ResumeModeCold
		a.log.Info("冷恢复新 serve 就绪", "task", req.TaskID, "new_port", proc.Port)
	}
```

`session/load` 失败分支（`resume.go:92-98`）在 `Cold=true` 时降级第 4 级：

```go
	sessionID := req.SessionID
	if _, err := cli.Call(ctx, "session/load", map[string]any{
		"sessionId": req.SessionID, "cwd": req.RepoPath, "mcpServers": []any{},
	}); err != nil {
		if !req.Cold {
			_ = cli.Close()
			a.log.Warn("session/load 失败，判不可恢复", "task", req.TaskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("载入原会话失败：%v", err)}, nil
		}
		// 第 4 级：原会话载不进，新开一个。上下文断了，manager 会据 Mode=fresh
		// 播报给审核者——这一条必须让人知道，它决定下一条指令要不要重述背景
		a.log.Warn("session/load 失败，降级新开会话", "task", req.TaskID, "cause", err)
		newID, nerr := a.newSessionOnConn(ctx, cli, req.RepoPath)
		if nerr != nil {
			_ = cli.Close()
			a.log.Warn("降级新开会话也失败，判不可恢复", "task", req.TaskID, "cause", nerr)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("原会话载不进且新建会话失败：%v", nerr)}, nil
		}
		sessionID, mode = newID, executor.ResumeModeFresh
	}
	r.sessionID = sessionID
```

`newSessionOnConn` 从现有 `openSession`（`grok/adapter.go:200`）里抽出「在已有连接上 `session/new`」那一段复用，不要复制一份。

`EnsureAuthLink`（`resume.go:60`）在冷恢复路径同样要调——token 刷新期间软链可能已被干掉（B26 实测）。现有代码在 `proc.Alive()` 判断之后、`DialACP` 之前，位置正确，不用挪。

函数末尾返回：

```go
	return executor.ResumeOutcome{Alive: true, Mode: mode, SessionID: sessionID,
		Note: resumeNote(mode, sessionID)}, nil
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/executor/grok/ -v`
Expected: PASS

- [ ] **Step 5: 加冷恢复互斥（spec §6 约束 1）**

两条并发 `continue` 会各自撞 `ErrTaskNotRunning`、各自调 `Resume(Cold:true)`——无互斥就会为同一任务起两个 serve 抢同一个会话。在 `Resume` 开头持 `a.mu` 做检查+占位：

```go
	// 冷恢复互斥（spec §6）：先在 runs 上占位再拉进程，后到者直接返回
	// 「恢复进行中」。两个 serve 抢同一个会话是数据损坏级别的后果
	a.mu.Lock()
	if _, busy := a.runs[req.TaskID]; busy {
		a.mu.Unlock()
		a.log.Info("该任务已有运行态或恢复进行中，跳过本次恢复", "task", req.TaskID)
		return executor.ResumeOutcome{Alive: false, Note: "该任务的恢复正在进行中"}, nil
	}
	a.runs[req.TaskID] = &runState{taskID: req.TaskID} // 占位
	a.mu.Unlock()
	defer func() {
		// 失败路径清掉占位，否则这个任务永远恢复不了
		a.mu.Lock()
		if cur, ok := a.runs[req.TaskID]; ok && cur.evCh == nil {
			delete(a.runs, req.TaskID)
		}
		a.mu.Unlock()
	}()
```

占位项以 `evCh == nil` 与真实运行态区分（真实的在 `Resume` 末尾用完整 `r` 覆盖写入）。

- [ ] **Step 6: 写互斥测试**

```go
// TestResumeColdMutualExclusion 并发两次冷恢复只允许一次真的去拉进程——
// 两个 serve 抢同一个会话是数据损坏级别的后果。
func TestResumeColdMutualExclusion(t *testing.T) {
	var starts int32
	restore := swapStartServe(func(...) (*Proc, error) {
		atomic.AddInt32(&starts, 1)
		time.Sleep(50 * time.Millisecond) // 拉长窗口，让第二个必然撞进来
		return nil, errors.New("测试不真起进程")
	})
	defer restore()

	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	a := New(quietLogger())
	req := executor.ResumeReq{TaskID: "t1", TaskDir: dir, RepoPath: t.TempDir(),
		SessionID: "sess-1", Cold: true}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.Resume(req) }()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&starts); n != 1 {
		t.Fatalf("并发冷恢复应只拉起一次进程，实际 %d 次", n)
	}
}
```

- [ ] **Step 7: 跑全量并提交**

Run: `go test ./...`

```bash
git add internal/executor/grok/ && git commit -m "feat(grok): Resume 支持冷恢复，session/load 失败降级新会话"
```

---

## Task 9: opencode 冷恢复（风险最低）

会话在全局 sqlite（`~/.local/share/opencode/opencode.db`），`serve` 只是它前面的一层 HTTP——进程死了会话完全不受影响。

**Files:**
- Modify: `internal/executor/opencode/resume.go`（Task 2 迁出的文件）
- Test: `internal/executor/opencode/resume_test.go`（新建，或并入 `adapter_test.go`）

**Interfaces:**
- Consumes: `StartServe(...)`（`opencode/proc.go:102`）、`WriteTaskEnv(taskDir, taskID, model, planContent)`（`taskenv.go:103`）、`API.CreateSession`
- Produces: `Resume` 的 cold/fresh 分支

- [ ] **Step 1: 先写失败的测试**

```go
// TestResumeColdVerifiesSessionStillExists 冷恢复必须确认原会话仍在 serve 的
// 会话列表里才算 cold；不在就得降级 fresh 并如实播报，不能默认它还在。
func TestResumeColdVerifiesSessionStillExists(t *testing.T) {
	// httptest server：GET /session 返回不含目标 id 的列表，POST /session 返回新 id
	// （包内既有测试已有 httptest + 假探活的用法，照搬）
	...
	out, err := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: dir,
		RepoPath: repo, SessionID: "gone-session", Cold: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != executor.ResumeModeFresh {
		t.Fatalf("原会话已不在应降级 fresh，实际 %s", out.Mode)
	}
	if out.SessionID == "gone-session" || out.SessionID == "" {
		t.Fatalf("fresh 必须返回新会话 id 供 manager 落库，实际 %q", out.SessionID)
	}
}

// TestResumeColdKeepsSessionWhenPresent 原会话仍在 → Mode=cold，会话 id 不变。
func TestResumeColdKeepsSessionWhenPresent(t *testing.T) { ... }
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestResumeCold -v`
Expected: FAIL

- [ ] **Step 3: 实现冷恢复**

`resume.go` 里 `if !proc.Alive()` 那一段（原 `adapter.go:417-429`，含回收残留会话的 `proc.Kill()`）改成：

```go
	mode := executor.ResumeModeReattach
	if !proc.Alive() {
		// 回收残留会话：Alive() 为假只说明 serve 进程没了，tmux 会话本身可能
		// 还被第二窗口的 tail -f render.log 吊着。冷恢复要新建同名会话，
		// 不先回收会直接撞名（这条在原实现里就有，冷恢复路径更需要它）
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死执行器的 tmux 会话失败，可能需人工清理",
				"task", req.TaskID, "tmux", proc.TmuxSession, "cause", kerr)
		}
		if !req.Cold {
			a.log.Info("serve 已不在且不允许冷恢复，判不可恢复",
				"task", req.TaskID, "tmux", proc.TmuxSession)
			return executor.ResumeOutcome{Alive: false,
				Note: "serve 进程已不在（本次只允许热重连）"}, nil
		}
		a.log.Info("serve 已不在，进入冷恢复", "task", req.TaskID, "session", req.SessionID)
		// 任务物料在 taskDir 里是持久的，路径确定性推导；重写一次保证内容与
		// 当前 model 一致（PlanContent 只在首轮 prompt 用得上，冷恢复不需要）
		configPath := filepath.Join(req.TaskDir, configFileName)
		newProc, err := startServe(context.Background(), req.RepoPath, req.TaskID,
			req.TaskDir, configPath, req.Env, a.log)
		if err != nil {
			a.log.Warn("冷恢复重起 serve 失败，判不可恢复", "task", req.TaskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("重起 opencode serve 失败：%v", err)}, nil
		}
		if werr := writeServeInfo(req.TaskDir, newProc); werr != nil {
			a.log.Warn("冷恢复写 serve.json 失败，下次重启恢复将不可用",
				"task", req.TaskID, "cause", werr)
		}
		proc = newProc
		mode = executor.ResumeModeCold
		a.log.Info("冷恢复新 serve 就绪", "task", req.TaskID, "port", proc.Port)
	}
```

建好 `api` 之后、起订阅之前，加会话在场校验：

```go
	sessionID := req.SessionID
	if mode == executor.ResumeModeCold {
		// 会话在全局 sqlite 里，进程重起不影响它——但要确认它真的还在，
		// 不能默认。不在就降级新会话并如实播报（上下文断了是审核者需要知道的）
		has, err := api.HasSession(ctx, sessionID)
		if err != nil {
			a.log.Warn("查询会话列表失败，保守降级新会话", "task", req.TaskID, "cause", err)
			has = false
		}
		if !has {
			newID, nerr := api.CreateSession(ctx)
			if nerr != nil {
				a.log.Warn("降级新建会话失败，判不可恢复", "task", req.TaskID, "cause", nerr)
				return executor.ResumeOutcome{Alive: false,
					Note: fmt.Sprintf("原会话已不在且新建失败：%v", nerr)}, nil
			}
			a.log.Warn("原会话已不在，已新开会话", "task", req.TaskID,
				"old", sessionID, "new", newID)
			sessionID, mode = newID, executor.ResumeModeFresh
		}
	}
```

`API.HasSession(ctx, id) (bool, error)` 在 `internal/executor/opencode/api.go` 新增：`GET /session` 后按 id 匹配。写 doc 注释说明它只用于冷恢复的在场校验。

- [ ] **Step 4: 跑测试并提交**

Run: `go test ./internal/executor/opencode/ -v && go test ./...`

```bash
git add internal/executor/opencode/ && git commit -m "feat(opencode): Resume 支持冷恢复，会话不在时降级新会话"
```

---

## Task 10: claude 冷恢复（形态由 Task 1 的 spike 决定）

**先读 spec §11 的 claude 那一行。** spike 通过 → 实现 `--resume` 冷恢复；不通过 → 只实现第 4 级（新会话），本 task 的 Step 3/4/5 跳过，直接做 Step 6。

**Files:**
- Modify: `internal/executor/claudecode/resume.go`（Task 2 迁出的文件）
- Modify: `internal/executor/claudecode/proc.go:195`（`--session-id` / `--resume` 二选一）
- Test: `internal/executor/claudecode/resume_test.go`

**Interfaces:**
- Consumes: `StartProc(ctx, StartProcReq, log)`、`readProcInfo`、`procExited`、`tmuxHasSession`、`newPermServer`
- Produces: `StartProcReq.Resume bool` 新字段；`Resume` 的 cold/fresh 分支

- [ ] **Step 1: 先写失败的测试**

```go
// TestWriteRunScriptUsesResumeFlag 冷恢复的启动脚本必须用 --resume 而不是
// --session-id：后者是「建一个这个 id 的新会话」，语义完全相反，写错的表现是
// 「日志说恢复成功、模型却什么都不记得」——最难查的一类 bug。
func TestWriteRunScriptUsesResumeFlag(t *testing.T) {
	dir := t.TempDir()
	path, err := writeRunScript(dir, StartProcReq{TaskID: "t1", TaskDir: dir,
		SessionID: "sess-1", SettingsPath: "/s.json", MCPPath: "/m.json", Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "--resume sess-1") {
		t.Fatalf("冷恢复脚本应含 --resume，实际:\n%s", b)
	}
	if strings.Contains(string(b), "--session-id") {
		t.Fatalf("冷恢复脚本不应含 --session-id（语义相反）")
	}
}

// TestResumeColdRotatesOutJSONL 冷恢复后是全新的输出流，旧 offset 无意义——
// 不轮转的话 tailer 从旧 offset 续读新文件，会把新会话的开头当成旧内容跳过。
func TestResumeColdRotatesOutJSONL(t *testing.T) { ... }
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run 'TestWriteRunScriptUsesResume|TestResumeColdRotates' -v`
Expected: FAIL

- [ ] **Step 3: 加 `Resume` 字段与启动参数分支**

`proc.go:75` 的 `StartProcReq` 加字段：

```go
	// Resume=true 时启动命令用 --resume（载入既有会话）而非 --session-id
	// （建一个这个 id 的新会话）。两者语义相反，写错的表现是「日志说恢复成功、
	// 模型却什么都不记得」
	Resume bool
```

`proc.go:195` 改成：

```go
	if req.Resume {
		args.WriteString(" --resume " + req.SessionID)
	} else {
		args.WriteString(" --session-id " + req.SessionID)
	}
```

- [ ] **Step 4: 实现冷恢复三处配套**

`resume.go` 里两条存活判据（哨兵 / `tmuxHasSession`）为假的分支，在 `Cold=true` 时不再 return，而是走冷恢复：

```go
	mode := executor.ResumeModeReattach
	if dead {
		if kerr := proc.Kill(); kerr != nil { // 先回收旧会话，否则冷恢复撞名
			a.log.Warn("回收已死执行器的 tmux 会话失败", "task", req.TaskID, "cause", kerr)
		}
		if !req.Cold {
			return executor.ResumeOutcome{Alive: false,
				Note: "claude 进程已不在（本次只允许热重连）"}, nil
		}
		// out.jsonl 轮转：冷恢复后是全新的输出流，旧 offset 无意义。旧文件留着
		// （诊断价值），新开一个，offset 归零
		if rerr := rotateOutJSONL(req.TaskDir); rerr != nil {
			a.log.Warn("轮转 out.jsonl 失败，仍尝试冷恢复", "task", req.TaskID, "cause", rerr)
		}
		a.log.Info("claude 已不在，进入冷恢复", "task", req.TaskID, "session", req.SessionID)
		newProc, err := startProc(context.Background(), StartProcReq{
			// cwd 必须是原工作区：会话文件路径按 cwd 编码
			// （~/.claude/projects/<slug(cwd)>/），传错就找不到会话
			RepoPath: req.RepoPath, TaskID: req.TaskID, TaskDir: req.TaskDir,
			SessionID: req.SessionID, Model: req.Model,
			SettingsPath: filepath.Join(req.TaskDir, settingsFileName),
			MCPPath:      filepath.Join(req.TaskDir, mcpFileName),
			Env:          req.Env, Resume: true,
		}, a.log)
		if err != nil {
			a.log.Warn("冷恢复重起 claude 失败，判不可恢复", "task", req.TaskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("重起 claude 失败：%v", err)}, nil
		}
		proc = newProc
		mode = executor.ResumeModeCold
		startOffset = 0
	}
```

`rotateOutJSONL(taskDir)` 新写：把 `out.jsonl` 改名成 `out.<n>.jsonl`（n 从 1 递增到第一个不存在的），再让 tailer 从 0 开始。同时把 `claude.json` 的 `offset` 归零（`writeProcInfo`）。

- [ ] **Step 5: 冷恢复后重开 perm.sock 与看门狗**

现有 `Resume` 的存活分支已经在做这两件事（`adapter.go:461-475`），冷恢复走同一段代码即可——**不要复制一份**，把 `if dead` 块放在它之前，让两条路汇流。

- [ ] **Step 6: spike 不通过时的形态（只在这种情况下做）**

`Cold=true` 且进程已死时不 `--resume`，直接用**新 uuid** `--session-id` 起一个新会话，返回 `Mode=fresh` + 新 session id。manager 侧会据此播报「上下文从本条指令开始，必要时请在指令中重述背景」，审核者知情。

在 `Resume` 的 doc 注释里写清楚：为什么 claude 只到第 4 级、spike 卡在哪一步、以后 claude CLI 改了之后从哪里接着试。**这条注释是给未来的人省一次 spike 的**，不能省。

- [ ] **Step 7: 跑全量并提交**

Run: `go test ./...`

```bash
git add internal/executor/claudecode/ && git commit -m "feat(claude): Resume 支持冷恢复（--resume + out.jsonl 轮转）"
```

---

## Task 11: 真机验收（devbox，三个 executor 各一遍）

单测证明不了「模型真的记得上一回合」，只有真机能。这是 backlog B20/B21/B24 转 `✅ done(已验)` 的证据来源。

**环境：** devbox `sycm@100.73.238.21`，项目 `/Users/sycm/workspace/handoff`，agentd 在 tmux 会话 `agentd` 里跑，端口 7777。本地 CLI 用 `--target devbox`。

**Files:**
- Modify: `docs/superpowers/backlog.md`（B20/B21/B24 转 `✅ done(已验)` + 验收证据）

- [ ] **Step 1: 部署并重启 agentd**

推分支到 devbox，`go build`，重启 tmux 会话 `agentd` 里的进程。确认 `handoff tasks --target devbox` 能通。

- [ ] **Step 2: 派发一个任务跑到 waiting_review**

记下任务 id 与 `executor_session`（`handoff show <task> --target devbox`）。

- [ ] **Step 3: B21/B24 的信号 —— 杀掉 executor，看是否立刻有事件**

```bash
ssh sycm@100.73.238.21 'tmux kill-session -t handoff-<id8>'
```

**期望：** 事件流里**立刻**出现 failed 事件（`fail_reason` = "executor 事件流已终结（进程退出或连接断开）"），**不等 2h**。`handoff show` 显示 `waiting_review`。

若任务本来就在 `waiting_review`（对账是空操作、无事件），这一步换成在 `running` 期间杀——那才是 B21 的现场。

- [ ] **Step 4: B24 的续接 —— 冷恢复且上下文完整**

```bash
handoff continue <task> --instructions "复述你上一回合具体做了什么，不要重新读代码" --target devbox
```

**期望（三条缺一不可）：**
1. agentd 日志出现 `进入冷恢复` → `冷恢复新 serve 就绪` → `恢复结果 mode=cold`
2. 事件流出现 progress："executor 进程已不在，已重启并从磁盘载入原会话 ...，上下文完整"
3. **模型答出上一回合的真实内容**——这一条才是判据，前两条只是它的伴随现象

claude 若 spike 未通过，期望改为 `mode=fresh` + 降级播报事件，模型答不出属预期。

- [ ] **Step 5: B20 的兜底 —— 重启 agentd 后归档**

重启 agentd（丢掉内存运行态）→ `handoff done <task> --target devbox`。

**期望：** 日志「按确定性命名兜底回收成功」；`ssh sycm@100.73.238.21 'tmux ls'` 里**没有** `handoff-<id8>` 残留。

- [ ] **Step 6: B20 的信号 —— 令回收必然失败**

把 devbox 上 agentd 的 PATH 里的 tmux 临时挪走（或用一个 `Reap` 必失败的任务），再 `done`。

**期望：** 事件流出现带会话名的 progress："executor 资源可能残留：tmux 会话 handoff-<id8>，请手动 tmux kill-session -t handoff-<id8>"。

做完把 tmux 放回去。

- [ ] **Step 7: 三个 executor 各跑一遍 Step 2–4**

opencode / grok / claude。每个记下任务 id 与模型复述的原文。

- [ ] **Step 8: 收口 backlog**

`docs/superpowers/backlog.md` 的 B20/B21/B24 三行：
- 状态 → `✅ done(已验)`
- Spec 列 → 指向本 spec
- 验收列 → `go test ./... (ok)；devbox 三 executor 冷恢复实测通过（模型复述原会话内容）；原型/流程图为 — ，自动免除对照 08-09`
- B24 备注里补一句更正：「done 也走不通」不成立，实际缺口只有 continue（见 spec §1.2）

**只改 `backlog.md` 这一个文件。** 本次改动前先 `git status` 确认它没被并行会话占用——写 spec 期间它一直是脏的。

- [ ] **Step 9: Commit**

```bash
git add docs/superpowers/backlog.md && git commit -m "docs(backlog): B20/B21/B24 收口为 done（已验）"
```

---

## Self-Review

**Spec 覆盖对照：**

| Spec 章节 | 落在哪个 task |
|---|---|
| §3.1 共享数据类型 | Task 2（+ 补 `Env`/`Model`，见开头「对 spec 的补充」） |
| §3.2 接口保持私有 | Task 2 Step 2 / Task 6 Step 3 |
| §3.3 `ResumeTask` 迁移 | Task 2 Step 2 |
| §4 按需不预热 | Task 2（`Cold:false`）+ Task 7（`Cold:true`），两处都有钉子测试 |
| §5.1 `reconcileExecutorGone` | Task 3 |
| §5.2 `mediate` 退出对账 | Task 4 |
| §5.3 `stopping` 标记 | Task 4 |
| §5.4 `Continue` 阶梯 | Task 7 |
| §5.5.1/2/3 三个 adapter 冷恢复 | Task 8 / 9 / 10 |
| §5.6 空回合不静默 | Task 5（三个 adapter 全覆盖） |
| §5.7 `Reap` 与信号对称 | Task 6 |
| §6 并发与幂等 5 条 | 1→Task 8 Step 5（互斥，grok 落地后 9/10 同款）；2→Task 3 幂等测试；3→Task 4 取走式测试；4→Task 6 `Reap` 幂等；5→**见下方缺口** |
| §7 失败语义表 7 行 | 分散在 Task 7/8/9/10 的 `Alive=false` + `Note` 分支 |
| §8.1 事件 | Task 4（failed）/ Task 5（failed）/ Task 7（progress）/ Task 6（progress） |
| §8.2 关键节点日志 | 每个 task 的代码块里都带了 |
| §9.1 spike | Task 1 |
| §9.2 单元测试 9 条 | 全部落在 Task 3–10 的测试步骤里 |
| §9.3 真机验收 6 步 | Task 11 |

**发现并已修补的两处：**

1. **`ResumeReq` 缺 `Env`/`Model`**（spec §3.1）——照 spec 写会让冷恢复起出一个丢了用户密钥、也不知道该用哪个模型的进程。已在计划开头显式记录，Task 2 落实，并配了钉子测试。

2. **§6 约束 5（冷恢复不重建 worktree）在 spec 里有、原本没落到任何 task 上。** 补进 Task 8/9/10 的实现：`Resume` 在冷恢复分支拉进程**之前**先确认 `req.TaskDir` 与 `req.RepoPath` 都存在，任一不在直接返回 `Alive=false` + Note「任务工作区已不存在（可能已归档清理），无法恢复」。**重建工作区是 `Dispatch` 的职责，越界重建会让归档过的任务诈尸**——这条约束比它看起来重要，实现时不要跳过。

**类型一致性：** `ResumeReq`/`ResumeOutcome` 的字段名在 Task 2 定义后，Task 7/8/9/10 引用的是同一套（`Alive`/`Mode`/`SessionID`/`Note`、`Cold`/`Env`/`Model`）；`reconcileExecutorGone` 的签名在 Task 3 定义、Task 4 经方法包装引用；`Reap(taskID, taskDir string) error` 在 Task 6 三个 adapter 与 manager 接口两侧一致。

**已知的计划外部依赖：** Task 10 的形态取决于 Task 1 的 spike 结论，两条分支都已写明；Task 8 的互斥实现（Step 5）是 9/10 的模板，实现 9/10 时照抄同款，不要各写一份。
