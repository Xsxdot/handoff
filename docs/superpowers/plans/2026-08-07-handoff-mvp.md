# Handoff MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 handoff——审核者（交互式 Claude Code）把 plan 分发给 executor（本机/远程 Claude Code headless）执行，通过阻塞 CLI 完成唤醒、审批、提问、审核、回发修改的完整闭环。

**Architecture:** Go 单二进制 `handoff`，两端复用。executor 所在机跑 `handoff agentd`（HTTP API + WS 事件流 + SQLite 持久化）；本机 CLI 拨号消费。executor 挂载全走 CLI：PreToolUse hook → `handoff gate`（阻塞），提问 → Bash 调 `handoff ask`（阻塞，ticket 幂等）。executor 进程在 tmux 内跑 `claude -p --output-format stream-json`，adapter tail 解析。

**Tech Stack:** Go 1.22+（stdlib `net/http` 路由模式）、`github.com/spf13/cobra`、`github.com/coder/websocket`、`modernc.org/sqlite`（纯 Go 无 cgo，跨平台交叉编译分发到远程机）、`gopkg.in/yaml.v3`、`github.com/google/uuid`、`log/slog`（结构化日志）。

**Spec:** `docs/superpowers/specs/2026-08-07-handoff-design.md`

## Global Constraints

- Go ≥ 1.22；单二进制；SQLite 用 `modernc.org/sqlite`（禁止 cgo 依赖）。
- 零 MCP：executor 挂载只允许 hooks + CLI（spec §6）。
- 日志一律 `log/slog`（`internal/logx` 统一初始化），**禁止 `fmt.Printf` 作日志**；CLI 面向用户的正常输出（JSON 结果等）走 `os.Stdout` 的 `fmt.Fprintln` 是允许的——那是程序输出不是日志。
- 每个新文件顶部必须有中文「职责 + 边界」头注释；导出函数必须有 doc 注释（用户全局 CLAUDE.md §2）。
- 事件不丢不重：events 表自增 seq + 客户端 cursor；tickets 幂等（INSERT OR IGNORE by id）。
- 任务状态机只有：`pending / running / waiting_answer / waiting_review / completed / failed`。
- v1 单 target 串行执行任务，不做并发调度（spec §11）。
- module path：`github.com/xushixin/handoff`。
- agentd 数据目录 `~/.handoff/`：`agentd.db`、`agentd.log`、`config.yaml`、`tasks/<id>/`。
- 危险操作升级、审批分级是**审核者（Claude）的行为策略**，不写死在代码里；代码只负责忠实转发。

## File Structure

```
handoff/
├── main.go                        # 入口，调 cmd.Execute()
├── cmd/                           # cobra 子命令（薄壳，逻辑在 internal）
│   ├── root.go                    # 根命令 + --agentd/--token 全局 flag + logx 初始化
│   ├── agentd.go                  # handoff agentd
│   ├── dispatch.go  wait.go  reply.go  tasks.go  attach.go
│   ├── continue.go  done.go  diff.go  fetch.go  run.go
│   └── gate.go  ask.go            # executor 侧挂载命令
├── internal/
│   ├── logx/logx.go               # slog 统一初始化（文件 + stderr）
│   ├── config/config.go           # ~/.handoff/config.yaml（listen/token/targets）
│   ├── proto/proto.go             # Task/Event/Ticket 类型与状态机常量
│   ├── store/store.go             # SQLite 持久化
│   ├── agentd/
│   │   ├── hub.go                 # 事件订阅广播 + ticket 应答路由
│   │   ├── server.go              # HTTP API + WS + 本地阻塞端点
│   │   └── manager.go             # 任务生命周期：创建/启动 adapter/看门狗
│   ├── executor/
│   │   ├── executor.go            # Adapter 接口
│   │   ├── fake/fake.go           # 脚本化 fake adapter（集成测试用）
│   │   └── claude/
│   │       ├── claude.go          # Claude Code adapter：启动/续接/完成检测
│   │       ├── settings.go        # hooks settings.json + prompt 生成
│   │       └── stream.go          # stream-json tail 解析
│   └── client/client.go           # 本机 CLI 侧 HTTP/WS 客户端（含重连）
└── docs/superpowers/{specs,plans}/
```

---

### Task 1: 项目骨架 + 日志基座 + 配置

**Files:**
- Create: `go.mod`, `main.go`, `cmd/root.go`
- Create: `internal/logx/logx.go`, `internal/config/config.go`
- Test: `internal/config/config_test.go`, `internal/logx/logx_test.go`

**Interfaces:**
- Produces: `logx.Setup(component, logPath string) *slog.Logger`（JSON 写文件 + text 写 stderr，level 由 `HANDOFF_LOG_LEVEL` 控制，默认 info）
- Produces: `config.Load(path string) (*Config, error)`；`Config{Listen string; Token string; DataDir string; StallTimeout time.Duration; Targets map[string]Target}`；`Target{Addr, Token string}`；`config.DefaultPath() string`（`~/.handoff/config.yaml`）；文件不存在时返回带默认值的 Config（Listen=`127.0.0.1:7777`，DataDir=`~/.handoff`，StallTimeout=2h）并自动生成随机 Token 写盘。

- [ ] **Step 1: 初始化模块与依赖**

```bash
cd /Users/xushixin/workspace/handoff
go mod init github.com/xushixin/handoff
go get github.com/spf13/cobra@latest github.com/coder/websocket@latest modernc.org/sqlite@latest gopkg.in/yaml.v3 github.com/google/uuid@latest
```

- [ ] **Step 2: 写 config 失败测试**

```go
// internal/config/config_test.go
func TestLoadGeneratesDefaultsAndToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(p)
	if err != nil { t.Fatal(err) }
	if cfg.Listen != "127.0.0.1:7777" { t.Fatalf("listen=%s", cfg.Listen) }
	if len(cfg.Token) < 16 { t.Fatalf("token 未生成: %q", cfg.Token) }
	// 二次加载读回同一 token（说明已落盘）
	cfg2, err := config.Load(p)
	if err != nil || cfg2.Token != cfg.Token { t.Fatalf("token 未持久化") }
}

func TestLoadParsesTargets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: abc123abc123abc1\ntargets:\n  devbox:\n    addr: \"100.1.2.3:7777\"\n    token: \"tk\"\n"), 0o600)
	cfg, _ := config.Load(p)
	if cfg.Targets["devbox"].Addr != "100.1.2.3:7777" { t.Fatalf("targets 解析失败") }
}
```

- [ ] **Step 3: 跑测试确认失败**（`go test ./internal/config/` → 编译错误，包不存在）

- [ ] **Step 4: 实现 config + logx + root 命令**

```go
// internal/config/config.go —— 头注释：职责=配置加载与默认值/token 生成；边界=不做网络、不校验 target 可达性
func Load(path string) (*Config, error) {
	cfg := &Config{Listen: "127.0.0.1:7777", DataDir: defaultDataDir(), StallTimeout: 2 * time.Hour}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg.Token = randToken() // 首次运行：生成 token 并写盘，配对时人工同步到本机 targets
		if werr := save(path, cfg); werr != nil { return nil, fmt.Errorf("写默认配置 %s: %w", path, werr) }
	case err != nil:
		return nil, fmt.Errorf("读配置 %s: %w", path, err)
	default:
		if uerr := yaml.Unmarshal(b, cfg); uerr != nil { return nil, fmt.Errorf("解析配置 %s: %w", path, uerr) }
	}
	return cfg, nil
}
```

```go
// internal/logx/logx.go —— 职责=统一 slog 初始化；边界=不管理日志轮转（交给 logrotate/newsyslog）
// Setup 返回同时写 JSON 文件与 stderr 文本的 logger；logPath 为空则只写 stderr。
func Setup(component, logPath string) *slog.Logger {
	lvl := parseLevel(os.Getenv("HANDOFF_LOG_LEVEL"))
	hs := []slog.Handler{slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})}
	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			hs = append(hs, slog.NewJSONHandler(f, &slog.HandlerOptions{Level: lvl}))
		}
	}
	return slog.New(multiHandler(hs)).With("component", component)
}
```

`cmd/root.go`：cobra 根命令，全局 flag `--agentd`（默认 `http://127.0.0.1:7777`）、`--target`（查 config.Targets 换算 addr/token）、`--config`。`main.go` 只调 `cmd.Execute()`。

- [ ] **Step 5: 跑测试确认通过**（`go test ./...`）
- [ ] **Step 6: 加关键节点日志**：config.Load 首次生成 token 时 `log.Info("首次运行，已生成配置", "path", path)`；解析失败 Error 带 path 与 cause。logx 自身写文件失败时降级 stderr 并 Warn。
- [ ] **Step 7: 加注释**：两个新文件头注释（职责+边界）、`Setup`/`Load`/`DefaultPath` doc 注释、randToken 的「为什么 16 字节够」内联注释。
- [ ] **Step 8: Commit** `git add -A && git commit -m "feat: 项目骨架、slog 日志基座与配置加载"`

---

### Task 2: proto 包——类型与状态机

**Files:**
- Create: `internal/proto/proto.go`
- Test: `internal/proto/proto_test.go`

**Interfaces:**
- Produces（后续所有任务依赖，签名固定）:

```go
type TaskState string // pending/running/waiting_answer/waiting_review/completed/failed
type EventType string // permission_request/question/progress/completed/failed/stalled
type Task struct {
	ID, Target, RepoPath, Branch, PlanPath, PlanSummary, ExecutorSession string
	State TaskState
	CreatedAt, UpdatedAt time.Time
}
type Event struct {
	Seq int64; TaskID string; Type EventType
	Payload json.RawMessage; CreatedAt time.Time
}
type Ticket struct {
	ID, TaskID, Kind string // Kind: "gate" | "ask"
	Request json.RawMessage
	Answer *string; CreatedAt time.Time; AnsweredAt *time.Time
}
// CanTransit 校验状态迁移合法性（如 completed 不可回 running）
func CanTransit(from, to TaskState) bool
```

- [ ] **Step 1: 写失败测试**：`TestCanTransit` 表驱动——`pending→running` true、`running→waiting_answer` true、`waiting_answer→running` true、`running→waiting_review` true、`waiting_review→running` true（continue 回发）、`completed→running` false、`failed→running` true（重试）。
- [ ] **Step 2: 确认失败** → **Step 3: 实现**（迁移表用 `map[TaskState][]TaskState`）→ **Step 4: 确认通过**
- [ ] **Step 5: 注释**：文件头（职责=协议类型唯一定义处；边界=无 I/O 无业务逻辑）、CanTransit doc 注释、迁移表内联「为什么 failed 允许回 running」。纯类型包无 I/O，无日志要求。
- [ ] **Step 6: Commit** `git commit -m "feat: proto 协议类型与任务状态机"`

---

### Task 3: store 包——SQLite 持久化

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `proto.*`
- Produces:

```go
func Open(path string) (*Store, error) // 建表（tasks/events/tickets，DDL 见下）
func (s *Store) Close() error
func (s *Store) CreateTask(t *proto.Task) error
func (s *Store) GetTask(id string) (*proto.Task, error)         // 不存在返回 ErrNotFound
func (s *Store) ListTasks() ([]proto.Task, error)               // 按 created_at 降序
func (s *Store) UpdateTaskState(id string, st proto.TaskState) error // 内部校验 CanTransit，非法返回 ErrBadTransit
func (s *Store) SetTaskField(id, field, value string) error     // 白名单字段: branch/executor_session/plan_summary
func (s *Store) AppendEvent(taskID string, typ proto.EventType, payload any) (proto.Event, error)
func (s *Store) EventsFrom(taskID string, fromSeq int64, limit int) ([]proto.Event, error)
func (s *Store) CreateTicket(tk *proto.Ticket) (created bool, err error) // INSERT OR IGNORE 幂等
func (s *Store) GetTicket(id string) (*proto.Ticket, error)
func (s *Store) AnswerTicket(id, answer string) error
func (s *Store) PendingTickets(taskID string) ([]proto.Ticket, error) // answer IS NULL
var ErrNotFound, ErrBadTransit error
```

DDL（写在 `Open` 内的 `CREATE TABLE IF NOT EXISTS`）：

```sql
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY, target TEXT NOT NULL DEFAULT '', repo_path TEXT NOT NULL,
  branch TEXT NOT NULL DEFAULT '', plan_path TEXT NOT NULL DEFAULT '',
  plan_summary TEXT NOT NULL DEFAULT '', executor_session TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL);
CREATE TABLE IF NOT EXISTS events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, type TEXT NOT NULL,
  payload TEXT NOT NULL, created_at TIMESTAMP NOT NULL);
CREATE INDEX IF NOT EXISTS idx_events_task ON events(task_id, seq);
CREATE TABLE IF NOT EXISTS tickets (
  id TEXT PRIMARY KEY, task_id TEXT NOT NULL, kind TEXT NOT NULL, request TEXT NOT NULL,
  answer TEXT, created_at TIMESTAMP NOT NULL, answered_at TIMESTAMP);
```

- [ ] **Step 1: 写失败测试**（`t.TempDir()` 下建库）：

```go
func TestTaskLifecycle(t *testing.T)   // Create→Get 回读一致；UpdateTaskState 走合法链；completed→running 返回 ErrBadTransit
func TestEventSeqMonotonic(t *testing.T) // 两个 task 交错 Append，seq 全局单调；EventsFrom(task, seq) 只返回该 task 大于 seq 的
func TestTicketIdempotent(t *testing.T)  // 同 id CreateTicket 两次：第一次 created=true 第二次 false；AnswerTicket 后 PendingTickets 不含它；GetTicket 能读到 answer
```

- [ ] **Step 2: 确认失败** → **Step 3: 实现**（driver name `"sqlite"`，DSN 追加 `?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)`——agentd 单进程多 goroutine 并发写，WAL+busy_timeout 防 SQLITE_BUSY）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：Open 成功 Info（path）；每个方法的错误 return 前不打日志（store 是叶子层，日志由调用方带上下文打，避免双份）——但 `UpdateTaskState` 的非法迁移打 Warn（from/to/task），这是排障高价值点。
- [ ] **Step 6: 加注释**：文件头（职责=唯一持久化入口；边界=不含业务规则，仅 CanTransit 一处防护性校验）、全部导出方法 doc 注释、WAL pragma 的「为什么」内联注释。
- [ ] **Step 7: Commit** `git commit -m "feat: store SQLite 持久化层"`

---

### Task 4: hub——事件广播与应答路由

**Files:**
- Create: `internal/agentd/hub.go`
- Test: `internal/agentd/hub_test.go`

**Interfaces:**
- Produces:

```go
func NewHub() *Hub
func (h *Hub) Publish(ev proto.Event)                       // 广播给该 task 的所有订阅者，无订阅者则丢弃（持久化在 store，不靠 hub）
func (h *Hub) Subscribe(taskID string) (ch <-chan proto.Event, cancel func()) // 仅实时流；历史回放由 server 层用 store.EventsFrom 拼接
func (h *Hub) NotifyAnswer(ticketID, answer string)
func (h *Hub) WaitAnswer(ctx context.Context, ticketID string) (string, error) // ctx 取消返回 ctx.Err()
```

- [ ] **Step 1: 写失败测试**：`TestPublishFanout`（两个订阅者都收到；cancel 后不再收，且 Publish 不阻塞）；`TestWaitAnswerBeforeAndAfter`（先 Wait 后 Notify 能收到；ctx 超时返回 err；Notify 无人等待不 panic）。
- [ ] **Step 2: 确认失败** → **Step 3: 实现**（`sync.Mutex` + `map[string][]chan proto.Event` + `map[string][]chan string`；Publish 用带 buffer 的 chan + select-default 丢弃慢订阅者，防止一个断连的 WS 卡死全局）→ **Step 4: 确认通过**（额外跑 `go test -race ./internal/agentd/`）
- [ ] **Step 5: 加日志**：Subscribe/cancel Debug（taskID、订阅者数）；Publish 时慢订阅者被丢弃打 Warn（taskID、seq）——这是「事件为什么没到」的第一排查点；NotifyAnswer Info（ticketID）。
- [ ] **Step 6: 加注释**：文件头（职责=进程内实时路由；边界=不持久化、不保证送达，可靠性由 store+cursor 承担——这句边界必须写，它解释了整个可靠性设计）。
- [ ] **Step 7: Commit** `git commit -m "feat: hub 事件广播与 ticket 应答路由"`

---

### Task 5: agentd server——HTTP API + WS 事件流

**Files:**
- Create: `internal/agentd/server.go`
- Modify: `cmd/agentd.go`（起服务：logx.Setup("agentd", dataDir+"/agentd.log")、config.Load、store.Open、NewServer、ListenAndServe）
- Test: `internal/agentd/server_test.go`

**Interfaces:**
- Consumes: store、hub、proto
- Produces（HTTP API，Bearer token 鉴权，`/local/` 前缀路由仅接受回环地址连接）:

```
GET  /api/tasks                     → []proto.Task
GET  /api/tasks/{id}                → {task, pending_tickets, recent_events}  // attach 数据源
POST /api/tasks/{id}/reply          → body {ticket_id, answer}  // answer 为 JSON 字符串："allow"/"deny"/自由文本
GET  /ws/events?task={id}&from_seq={n}  → WS，先补发 store 中 seq>n 的事件，再接实时流；客户端无需 ack（cursor 客户端自存）
POST /local/tickets                 → body {task_id, ticket_id, kind, request}；阻塞至 answer，返回 {answer}
GET  /local/tickets/{id}            → 已答直接返回 {answer}，未答阻塞等待（gate/ask 断后重等入口）
```

```go
func NewServer(cfg *config.Config, st *store.Store, log *slog.Logger) *Server
func (s *Server) Handler() http.Handler   // 便于 httptest
func (s *Server) Hub() *Hub
```

reply 处理流程（这是唤醒闭环的回程）：store.AnswerTicket → hub.NotifyAnswer → 若 task 处于 waiting_answer 且无其余 pending ticket，UpdateTaskState(running)。
/local/tickets 处理流程（去程）：CreateTicket（幂等，已答的直接返回存量 answer）→ AppendEvent（kind=gate→permission_request，kind=ask→question，payload=request）→ hub.Publish → UpdateTaskState(waiting_answer)（忽略 ErrBadTransit——completed 任务的迟到 hook 调用不应改状态）→ hub.WaitAnswer 阻塞。

- [ ] **Step 1: 写失败测试**（`httptest.NewServer(srv.Handler())`）：

```go
func TestAuthRequired(t *testing.T)        // 无 token 401；错 token 401；对 token 200
func TestTicketBlockUntilReply(t *testing.T) {
	// goroutine A: POST /local/tickets {kind:"ask"} 阻塞
	// 主线程: 轮询 GET /api/tasks/{id} 直到 pending_tickets 出现且 task.state==waiting_answer
	//         POST /api/tasks/{id}/reply {ticket_id, answer:"用 pgx 不用 gorm"}
	// 断言: A 解除阻塞拿到 answer；task.state 回到 running；events 含一条 question
}
func TestTicketIdempotentReplay(t *testing.T) // 已答 ticket 再 POST /local/tickets → 立即返回存量 answer，不重复计入 events
func TestWSReplayThenLive(t *testing.T)       // 先 Append 两条事件，WS from_seq=0 收到补发两条；再 Publish 一条实时收到
```

- [ ] **Step 2: 确认失败** → **Step 3: 实现**（stdlib `http.NewServeMux` 方法路由；WS 用 `websocket.Accept`，写循环 select store 补发 + hub 订阅 chan；`/local/` 路由用中间件校验 `RemoteAddr` 属于 127.0.0.1/::1）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**（本任务是日志密度最高处，逐点覆盖）：每个 API 入口 Info（method、path、task_id）；鉴权失败 Warn（remote_addr）；ticket 创建 Info（task、ticket、kind）；WaitAnswer 解除 Info（ticket、等待时长）；WS 连接建立/断开 Info（task、from_seq、补发条数）；reply Info（ticket、answer 截断 80 字符）；所有错误分支 Error 带 task/ticket 上下文与 cause。
- [ ] **Step 6: 加注释**：文件头（职责=对外唯一网络入口；边界=不启动 executor——那是 manager 的职责；/local 仅回环）；导出方法 doc 注释；「迟到 hook 调用忽略 ErrBadTransit」的 why 注释。
- [ ] **Step 7: Commit** `git commit -m "feat: agentd HTTP/WS 服务与阻塞 ticket 端点"`

---

### Task 6: gate / ask——executor 侧挂载命令

**Files:**
- Create: `cmd/gate.go`, `cmd/ask.go`
- Test: `internal/agentd/mount_test.go`（gate/ask 的核心逻辑抽到可测函数，cmd 只做壳）

**Interfaces:**
- Consumes: `/local/tickets` 端点
- Produces:
  - `handoff gate --task <id>`：从 stdin 读 PreToolUse hook JSON（含 `session_id`,`tool_name`,`tool_input`），ticket_id = `sha256(session_id|tool_name|tool_input)` 前 16 hex（同一工具调用重试幂等），POST /local/tickets 阻塞；answer 为 `"allow"`/`"deny"`/`"deny:<原因>"`，stdout 输出 hook 决策 JSON 后退出 0：

```json
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"handoff 审核者批准"}}
```

  - `handoff ask --task <id> "<问题>"`：ticket_id 新生成 uuid 并**先写入** `~/.handoff/tasks/<id>/last_ask`（进程若被超时杀死，重试入口存在），阻塞，answer 原文打印 stdout；`handoff ask --task <id> --wait <ticket>` 走 `GET /local/tickets/{id}` 重等。
  - agentd 侧不可达时：gate 输出 `permissionDecision:"deny"` + reason=agentd 不可达（**fail-closed**，绝不放行）；ask 打印错误到 stderr 退出 1。

- [ ] **Step 1: 写失败测试**：`TestGateTicketID`（相同 stdin 两次算出相同 id；不同 tool_input 不同 id）；`TestGateOutputMapping`（answer "allow"→JSON permissionDecision allow；"deny:太危险"→deny 且 reason 含"太危险"）；`TestGateFailClosed`（指向不存在端口 → 输出 deny JSON、退出码 0——hook 要求 0 才能解析 stdout）。
- [ ] **Step 2: 确认失败** → **Step 3: 实现**（核心函数 `RunGate(in io.Reader, out io.Writer, agentdURL, taskID string) error` 放 internal/agentd/mount.go，cmd/gate.go 调它）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：gate/ask 的日志写 stderr（stdout 是协议通道，绝不能污染）：请求发出 Info（task、ticket、kind）、拿到 answer Info（等待时长）、fail-closed 触发 Error（cause）。
- [ ] **Step 6: 加注释**：文件头；「为什么 fail-closed」「为什么 ticket 先落盘再阻塞」两处 why 注释。
- [ ] **Step 7: Commit** `git commit -m "feat: gate/ask executor 挂载命令（fail-closed + ticket 幂等）"`

---

### Task 7: client 包 + wait / reply / tasks / attach 命令

**Files:**
- Create: `internal/client/client.go`, `cmd/wait.go`, `cmd/reply.go`, `cmd/tasks.go`, `cmd/attach.go`
- Test: `internal/client/client_test.go`（对着 httptest 起的真 agentd Server 测）

**Interfaces:**
- Produces:

```go
func New(addr, token string) *Client
func (c *Client) ListTasks(ctx) ([]proto.Task, error)
func (c *Client) Attach(ctx, taskID) (*AttachInfo, error) // {Task, PendingTickets, RecentEvents}
func (c *Client) Reply(ctx, taskID, ticketID, answer string) error
// WaitEvent: 连 WS（cursor 从 ~/.handoff/cursor-<task> 读），跳过 progress（除非 all=true），
// 拿到首个可动作事件即返回并把 cursor 写盘；断线指数退避 1s→2s→…→60s 无限重连，ctx 取消才退出。
func (c *Client) WaitEvent(ctx, taskID string, all bool) (*proto.Event, error)
```

CLI 行为（审核者的使用界面，输出全是单行 JSON 便于我解析）：
- `handoff wait <task> [--notify]` → 阻塞，事件到达打印 `{"seq":..,"type":"question","payload":{...}}` 退出 0；`--notify` 同时 `osascript -e 'display notification ...'`（非 darwin 跳过）。
- `handoff reply <task> --ticket <id> (--approve | --deny [--reason r] | --answer "text")`
- `handoff tasks` → 每行一个 task JSON；`handoff attach <task>` → 完整 AttachInfo JSON（含 pending_tickets——恢复现场的关键）。

- [ ] **Step 1: 写失败测试**：`TestWaitEventSkipsProgress`（Append progress + question，WaitEvent 返回 question 且 cursor 落盘为其 seq）；`TestWaitEventReconnect`（先起 server 收一条后关闭，再在同地址重启并 Publish，WaitEvent 在重连后拿到——验证退避重连）；`TestReplyRoundTrip`（reply 后 attach 的 pending_tickets 清空）。
- [ ] **Step 2: 确认失败** → **Step 3: 实现** → **Step 4: 确认通过**
- [ ] **Step 5: 加日志**（stderr）：连接建立/断开/重连尝试 Info（addr、第 n 次、下次退避秒数）——断网重连是本项目核心场景，这里的日志是用户判断「为什么没唤醒」的唯一线索；cursor 读写 Debug；事件返回 Info（seq、type）。
- [ ] **Step 6: 加注释**：文件头（client 职责=唯一拨号方；边界=无业务判断，审批策略在审核者脑中）；WaitEvent doc 注释写明 cursor 语义与「progress 不唤醒」的 why。
- [ ] **Step 7: Commit** `git commit -m "feat: client 拨号层与 wait/reply/tasks/attach 命令"`

---

### Task 8: executor adapter 接口 + fake adapter + manager + 核心闭环集成测试

**Files:**
- Create: `internal/executor/executor.go`, `internal/executor/fake/fake.go`, `internal/agentd/manager.go`
- Test: `internal/agentd/integration_test.go`

**Interfaces:**
- Produces:

```go
// internal/executor/executor.go
type StartReq struct{ Task proto.Task; PlanContent string; AgentdLocalURL string; TaskDir string }
type Result struct{ Branch, CommitHash, SessionID, Summary string; ExitCode int }
type Adapter interface {
	Start(ctx context.Context, req StartReq) error            // 异步启动，立即返回
	Continue(ctx context.Context, task proto.Task, instructions string) error
	Wait(taskID string) <-chan Result                          // 本轮执行结束（completed/failed 判定材料）
	Stop(taskID string) error
}
// internal/agentd/manager.go
func NewManager(st *store.Store, hub *Hub, ad executor.Adapter, cfg *config.Config, log *slog.Logger) *Manager
func (m *Manager) Dispatch(ctx, DispatchReq) (*proto.Task, error) // 建 task(pending)→建 taskDir 写 plan→Adapter.Start→state=running→goroutine 等 Result
func (m *Manager) Continue(ctx, taskID, instructions string) error // waiting_review→running→Adapter.Continue→等 Result
func (m *Manager) Done(ctx, taskID string) error                   // waiting_review→completed
// Result 到达: ExitCode==0 → AppendEvent(completed,{branch,commit,summary}) + state=waiting_review
//             ExitCode!=0 → AppendEvent(failed,{summary,exit_code}) + state=waiting_review（失败也交审核者裁决）
```

fake adapter：`fake.New(script []fake.Step)`，Step 类型：`{Gate: "工具描述"}`（模拟 hook：POST /local/tickets kind=gate，期望拿到 answer）、`{Ask: "问题"}`、`{Finish: Result{...}}`。每步顺序执行，供集成测试脚本化驱动。

manager 同时把 Task 5 的 server 补全：`POST /api/tasks`（dispatch，body: repo/plan_b64/plan_name/target）、`POST /api/tasks/{id}/continue`、`POST /api/tasks/{id}/done` 三个路由接到 Manager；client 包加 `Dispatch/Continue/Done` 三个方法与 `cmd/dispatch.go`、`cmd/continue.go`、`cmd/done.go`（薄壳，dispatch 读本地 plan 文件 base64 上传）。

- [ ] **Step 1: 写核心闭环集成测试（本计划最重要的测试）**：

```go
func TestFullLoop(t *testing.T) {
	// fake script: Gate("Bash: go test ./...") → Ask("表结构用单数还是复数?") → Finish(ok, branch=handoff/T1)
	// 1. client.Dispatch(plan) → task running
	// 2. client.WaitEvent → permission_request；client.Reply(ticket,"allow")
	// 3. client.WaitEvent → question；client.Reply(ticket,"复数")
	// 4. client.WaitEvent → completed(payload 含 branch/commit)；task.state==waiting_review
	// 5. client.Continue("把 users 表加索引") → fake 收到 instructions；再 Finish → 再收 completed
	// 6. client.Done → task.state==completed
	// 全程断言 fake 拿到的 answer 与 reply 一致（审批/回答内容无损透传）
}
func TestRecoverMidTask(t *testing.T) {
	// fake 停在 Ask 阻塞 → 新建 client（模拟全新审核者会话）→ ListTasks 看到 waiting_answer
	// → Attach 拿到 pending_tickets[0] 就是该问题 → Reply 后流程继续走完
	// 这是 spec §7「会话恢复」的验收测试
}
```

- [ ] **Step 2: 确认失败** → **Step 3: 实现 fake + manager + 三条新路由/命令** → **Step 4: 确认通过**（`go test -race ./...`）
- [ ] **Step 5: 加日志**：manager 是状态机中枢，每次状态迁移 Info（task、from→to、触发原因）；Dispatch/Continue/Done 入口出口 Info；Result 到达 Info（task、exit_code、branch、commit）；Adapter.Start 失败 Error 并 state=failed。fake 里 Debug 级步骤日志。
- [ ] **Step 6: 加注释**：executor.go 文件头（Adapter 契约=五动作，实现方不得直接碰 store——所有状态由 manager 写，这条边界防止 adapter 与 manager 双写打架）；manager.go 文件头 + 状态迁移处 why 注释（失败也进 waiting_review 的原因：让审核者看到失败现场决定重试话术，而不是自动重试烧 token）。
- [ ] **Step 7: Commit** `git commit -m "feat: adapter 接口、fake 实现、manager 生命周期与核心闭环集成测试"`

---

### Task 9: dispatch 的 git 工作区准备 + diff / fetch / run 命令

**Files:**
- Create: `internal/agentd/workspace.go`, `cmd/diff.go`, `cmd/fetch.go`, `cmd/run.go`
- Modify: `internal/agentd/manager.go`（Dispatch 中调用 workspace 准备）, `internal/agentd/server.go`（三条新路由）, `internal/client/client.go`
- Test: `internal/agentd/workspace_test.go`

**Interfaces:**
- Produces:

```go
// workspace.go —— 全部通过 exec.Command("git","-C",repo,...) 实现
func PrepareBranch(repo, taskID string) (branch string, err error) // git checkout -b handoff/<id8>；工作区脏（status --porcelain 非空）→ 返回 ErrDirtyWorktree 拒绝 dispatch
func Diff(repo, baseBranch string) (string, error)                 // git diff <base>...HEAD + git log --oneline <base>..HEAD
func ReadFile(repo, rel string) (string, error)                    // 拒绝路径逃逸（filepath.Clean 后禁止 ".." 前缀与绝对路径）
func RunCmd(ctx, repo, cmdline string) (stdout string, exitCode int, err error) // sh -c，10min 超时，stdout+stderr 合并截断 1MB
```

HTTP：`GET /api/tasks/{id}/diff`、`GET /api/tasks/{id}/file?path=`、`POST /api/tasks/{id}/run` {cmd}。CLI 三个薄壳命令。`run` 是审核者主动发起的审阅动作（跑测试/lint），不走审批门。

- [ ] **Step 1: 写失败测试**（`t.TempDir()` 里 `git init` + 造提交）：`TestPrepareBranchCleanAndDirty`；`TestDiffShowsCommits`（在分支上加提交后 Diff 含该文件名）；`TestReadFileEscapeRejected`（`../etc/passwd` 与 `/etc/passwd` 都报错）；`TestRunCmdTimeoutAndTruncate`。
- [ ] **Step 2: 确认失败** → **Step 3: 实现** → **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：每个 git 调用前后 Info（repo、args、耗时）、失败 Error 带 stderr 输出——git 报错原文是排障必需品；RunCmd 执行 Info（cmd 截断、exit_code、耗时）；路径逃逸拒绝 Warn（task、请求 path）。
- [ ] **Step 6: 加注释**：文件头（职责=agentd 侧唯一 git/文件/命令出口；边界=只读审阅 + 分支准备，绝不代 executor 写代码）；ErrDirtyWorktree 的 why（脏工作区上开分支会把无关改动带进任务 diff，审核会被污染）。
- [ ] **Step 7: Commit** `git commit -m "feat: git 工作区准备与 diff/fetch/run 审阅命令"`

---

### Task 10: Claude adapter——settings/hooks/prompt 生成

**Files:**
- Create: `internal/executor/claude/settings.go`
- Test: `internal/executor/claude/settings_test.go`

**Interfaces:**
- Produces:

```go
// WriteTaskFiles 在 taskDir 生成 settings.json 与 prompt.md，返回二者路径。
func WriteTaskFiles(taskDir, taskID, agentdLocalURL, planContent, selfBin string) (settingsPath, promptPath string, err error)
```

settings.json 内容（selfBin 为 handoff 自身绝对路径 `os.Executable()`）：

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "*",
      "hooks": [{
        "type": "command",
        "command": "<selfBin> gate --task <taskID> --agentd <agentdLocalURL>",
        "timeout": 86400
      }]
    }]
  },
  "env": {
    "BASH_DEFAULT_TIMEOUT_MS": "600000",
    "BASH_MAX_TIMEOUT_MS": "86400000"
  }
}
```

prompt.md 模板（executor 纪律，spec §6/§7 的落地）：

```markdown
你是 handoff 任务 {{TaskID}} 的执行者，按下方实现计划执行。铁律：
1. 提问纪律：任何需要人决策的问题，必须执行 Bash 命令
   `{{SelfBin}} ask --task {{TaskID}} "<问题>"`，其 stdout 即回答。
   禁止自行假设，禁止以其它方式提问。命令被超时中断时，
   读 ~/.handoff/tasks/{{TaskID}}/last_ask 拿 ticket，
   用 `{{SelfBin}} ask --task {{TaskID}} --wait <ticket>` 重新等待。
2. 收尾纪律：全部完成后必须 git add 并 commit（不要 push），
   最后一条输出为单行 JSON：{"branch":"<分支>","commit":"<hash>","summary":"<50字内摘要>"}。
3. 只在当前分支工作，不切分支、不改 git 配置、不动 .handoff 目录。

--- 实现计划 ---
{{PlanContent}}
```

- [ ] **Step 1: 写失败测试**：`TestWriteTaskFiles`——生成后 settings.json 能被 `json.Unmarshal` 且 hook command 含 `gate --task T1`、timeout==86400；prompt.md 含 plan 原文与 `ask --task T1`；重复调用幂等覆盖。
- [ ] **Step 2: 确认失败** → **Step 3: 实现**（settings 用 Go 结构体 marshal 而非字符串拼接——防转义错误；prompt 用 `text/template`）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：生成成功 Info（taskDir、两个路径）；写失败 Error 带 path。
- [ ] **Step 6: 加注释**：文件头（职责=executor 环境物料生成；边界=不启动进程）；「为什么 matcher 用 * 而不是只拦 Bash」的 why 注释（写文件/编辑也要过审批门，权限策略在审核者侧收敛，代码不预设立场）。
- [ ] **Step 7: Commit** `git commit -m "feat: claude adapter 的 hooks settings 与 prompt 生成"`

---### Task 11: Claude adapter——tmux 启动、stream-json 解析、完成检测

**Files:**
- Create: `internal/executor/claude/claude.go`, `internal/executor/claude/stream.go`
- Modify: `cmd/agentd.go`（根据 flag `--executor=claude|fake` 选 adapter，默认 claude）
- Test: `internal/executor/claude/stream_test.go`, `internal/executor/claude/claude_test.go`

**Interfaces:**
- Consumes: Task 8 的 `executor.Adapter` 接口、Task 10 的 `WriteTaskFiles`
- Produces: `claude.New(log *slog.Logger) *Adapter`（实现 executor.Adapter）

启动命令（Start 内组装，写成 taskDir/run.sh 再交给 tmux，便于人工复跑排障）：

```bash
#!/bin/sh
cd <repoPath>
claude -p "$(cat <taskDir>/prompt.md)" \
  --settings <taskDir>/settings.json \
  --output-format stream-json --verbose \
  > <taskDir>/stream.jsonl 2> <taskDir>/stderr.log
echo $? > <taskDir>/exit_code
```

```bash
tmux new-session -d -s "handoff-<id8>" "sh <taskDir>/run.sh"
# 可见性：再开一个窗口跑渲染视图（v1 就是 tail，人能看到 executor 在动）
tmux new-window -t "handoff-<id8>" "tail -f <taskDir>/stream.jsonl"
```

stream.go——tail 解析器（500ms 轮询读增量行，不引 fsnotify 依赖）：

```go
type StreamInfo struct{ SessionID string; LastText string } // LastText: 最后一条 assistant 文本，作 progress/summary 素材
// TailUntilExit 阻塞解析直至 exit_code 文件出现；每条 assistant 文本回调 onProgress（manager 转 progress 事件）。
func TailUntilExit(ctx context.Context, taskDir string, onProgress func(text string)) (StreamInfo, int, error)
// 关注的行: {"type":"system","subtype":"init","session_id":...} 与 {"type":"assistant","message":{"content":[{"type":"text",...}]}}
// 与 {"type":"result",...}；未知行 Debug 后跳过（stream-json 字段随版本演进，解析必须宽容）
```

完成检测（Wait 返回 Result 前）：读 exit_code；解析 LastText 里的收尾 JSON（`{"branch":...,"commit":...,"summary":...}`，用正则提取最后一个 `{...}` 行再 Unmarshal）；解析不到则回退用 `git -C repo rev-parse --abbrev-ref HEAD` + `rev-parse HEAD` 兜底取 branch/commit，summary 取 LastText 截断——**executor 不守纪律不导致流程卡死，只降低摘要质量**。Continue 用 `--resume <SessionID>`，其余同 Start。Stop = `tmux kill-session`。

- [ ] **Step 1: 写失败测试**（不依赖真实 claude，用预制 stream.jsonl 文件驱动）：`TestTailParsesSessionAndResult`（造 init/assistant/result 三行 + exit_code 文件，断言 SessionID、LastText、onProgress 调用次数）；`TestTailTolerantToUnknownLines`（夹杂垃圾行不 panic 不报错）；`TestResultFallbackWhenNoTrailerJSON`（LastText 无收尾 JSON 时走 git 兜底——git 部分用 Task 9 的测试仓库）。
- [ ] **Step 2: 确认失败** → **Step 3: 实现** → **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：Start Info（task、tmux session、run.sh 路径）；tmux 命令失败 Error 带 stderr；解析到 session_id Info；exit_code 出现 Info（code、耗时）；收尾 JSON 解析失败走兜底时 **Warn**（task、LastText 尾部 120 字符）——这是「executor 不守纪律」的观测点，必须可见；Continue/Stop 入口 Info。
- [ ] **Step 6: 加注释**：两文件头；「为什么进程放 tmux 而不是 agentd 直接子进程」（agentd 重启不杀任务 + 用户可 attach 旁观）、「为什么解析必须宽容」两处 why 注释。
- [ ] **Step 7: Commit** `git commit -m "feat: claude adapter 启动/流解析/完成检测"`

---

### Task 12: 看门狗 + wait --notify + 端到端手动验证清单

**Files:**
- Create: `internal/agentd/watchdog.go`, `docs/superpowers/e2e-checklist.md`
- Modify: `cmd/agentd.go`（启动 watchdog goroutine）, `cmd/wait.go`（--notify）
- Test: `internal/agentd/watchdog_test.go`

**Interfaces:**
- Produces: `RunWatchdog(ctx, st *store.Store, hub *Hub, stallTimeout time.Duration, log *slog.Logger)`——每分钟扫 running/waiting_answer 任务，最新 event 时间早于 stallTimeout → AppendEvent(stalled,{last_seq,idle}) + Publish（只发一次：已有 stalled 且其后无新事件则不重发）。
- Produces: `RecoverOnStartup(st *store.Store, hub *Hub, probe func(taskID string) bool, log *slog.Logger) error`——agentd 启动时对 running/waiting_answer 任务逐个探测执行器存活（claude adapter 的 probe = `tmux has-session -t handoff-<id8>` 退出码），不存活 → AppendEvent(failed,{reason:"agentd 重启后执行器已不在"}) + state=waiting_review（spec §8）。cmd/agentd.go 在起 HTTP 服务前调用。

- [ ] **Step 1: 写失败测试**：`TestWatchdogFiresOnceOnStall`（造一个 last event 3h 前的 running 任务，tick 两轮只产生一条 stalled）；`TestWatchdogIgnoresFreshAndTerminal`（新鲜任务与 completed 任务不触发）；`TestRecoverOnStartup`（probe 恒 false 的 running 任务 → failed 事件 + waiting_review；probe 恒 true 的不动）。tick 间隔做成参数便于测试注入 10ms。
- [ ] **Step 2: 确认失败** → **Step 3: 实现 + wait.go 加 --notify**（事件到达时 `exec.Command("osascript","-e", "display notification ... with title \"handoff\"")`，仅 darwin，失败仅 Warn 不影响主流程）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：watchdog 扫描轮 Debug（扫了几个）、触发 stalled Warn（task、idle 时长）；notify 失败 Warn。
- [ ] **Step 6: 加注释**：文件头 + 「只发一次」防事件风暴的 why。
- [ ] **Step 7: 写 e2e 手动验证清单** `docs/superpowers/e2e-checklist.md`（真实 claude 环境逐项打勾）：

```markdown
# E2E 手动验证清单（真实 Claude Code）
前置：本机装 claude CLI 并已登录；`handoff agentd` 已起（--executor=claude）。
- [ ] SPIKE-1（spec 风险#1）：dispatch 一个「跑 `sleep 1 && echo hi`」的迷你 plan，
      不 reply 搁置 30min 后再 approve —— hook 长阻塞未被掐断，executor 继续执行
- [ ] SPIKE-2（spec 风险#2）：plan 中埋一个需要决策的问题，观察 executor 是否用 ask 提问而非自行假设
- [ ] dispatch → wait 被 permission_request 唤醒 → approve → completed → diff 有内容
- [ ] ask 全链路：executor 提问 → wait 唤醒 → reply --answer → executor 拿到原文
- [ ] continue：回发修改指令 → --resume 续会话 → 二轮 completed diff 含新改动
- [ ] 断网演练：wait 期间关 Wi-Fi 3min 恢复 → 自动重连并收到期间积压事件
- [ ] 恢复演练：杀掉审核者会话 → 新会话 tasks+attach 重建现场 → 处理 pending 后流程走通
- [ ] tmux attach 能看到 executor 实况；agentd 重启后 running 任务仍在（tmux 存活探测）
- [ ] 远程演练（可选功能）：devbox 上起 agentd，本机 --target devbox 跑通上述主链路
```

- [ ] **Step 8: Commit** `git commit -m "feat: 看门狗、通知兜底与 e2e 验证清单"`

---

### Task 13: README + 收尾自检

**Files:**
- Create: `README.md`
- Modify: 按自检结果修补

- [ ] **Step 1: 写 README**：一段话定位、架构图（从 spec 复制）、快速开始（agentd 启动 / 配对 token / dispatch / 审核者侧典型循环）、命令速查表、审核者会话恢复两条命令、troubleshooting（去哪看日志：`~/.handoff/agentd.log`、taskDir 下 stream.jsonl / stderr.log）。
- [ ] **Step 2: 全量回归** `go vet ./... && go test -race ./...`
- [ ] **Step 3: instrumenting-code 终检**（逐项过，任一不过回去修）：每个错误分支带上下文日志；外部调用（git/tmux/HTTP/WS）前后有日志；成功路径不静默；无 fmt.Printf 充当日志；新文件全有头注释；导出函数全有 doc 注释。
- [ ] **Step 4: 用户全局 CLAUDE.md §5 终审清单**逐项确认（完成目标/架构一致/注释/日志/无硬编码等）。
- [ ] **Step 5: Commit** `git commit -m "docs: README 与收尾自检"`
