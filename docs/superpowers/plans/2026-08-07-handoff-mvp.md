# Handoff MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 handoff——审核者（交互式 Claude Code）把 plan 分发给 executor（本机/远程 opencode）执行，通过阻塞 CLI 完成唤醒、审批、提问、审核、回发修改的完整闭环。

**Architecture:** Go 单二进制 `handoff`，两端复用。executor 所在机跑 `handoff agentd`（HTTP API + WS 事件流 + SQLite 持久化）；本机 CLI 拨号消费。executor = opencode server 模式：agentd 每任务在 tmux 内独立拉起 `opencode serve`，经 HTTP API 建会话发 prompt，经 SSE 消费权限请求与执行事件；权限用 opencode 原生 respond 端点应答，提问走回合制 trailer JSON 协议，等待全部发生在 opencode 会话内部（无 hook 超时、无 Bash 超时问题）。

**Tech Stack:** Go 1.22+（stdlib `net/http` 路由模式）、`github.com/spf13/cobra`、`github.com/coder/websocket`、`modernc.org/sqlite`（纯 Go 无 cgo，跨平台交叉编译分发到远程机）、`gopkg.in/yaml.v3`、`github.com/google/uuid`、`log/slog`（结构化日志）。

**Spec:** `docs/superpowers/specs/2026-08-07-handoff-design.md`

## Global Constraints

- Go ≥ 1.22；单二进制；SQLite 用 `modernc.org/sqlite`（禁止 cgo 依赖）。
- 零 MCP、零 hooks：executor 挂载只走 opencode server HTTP API + SSE（spec §6）。
- 日志一律 `log/slog`（`internal/logx` 统一初始化），**禁止 `fmt.Printf` 作日志**；CLI 面向用户的正常输出（JSON 结果等）走 `os.Stdout` 的 `fmt.Fprintln` 是允许的——那是程序输出不是日志。
- 每个新文件顶部必须有中文「职责 + 边界」头注释；导出函数必须有 doc 注释（用户全局 CLAUDE.md §2）。
- 事件不丢不重：events 表自增 seq + 客户端 cursor；tickets 幂等（INSERT OR IGNORE by id；权限 ticket id = `<taskID>:<permissionID>`——实现时按 P1-6 改为命名空间化，裸 permissionID 会跨任务碰撞）。
- 任务状态机只有：`pending / running / waiting_answer / waiting_review / completed / failed`。
- v1 单 target 串行执行任务，不做并发调度（spec §11）。
- module path：`github.com/xushixin/handoff`。
- agentd 数据目录 `~/.handoff/`：`agentd.db`、`agentd.log`、`config.yaml`、`tasks/<id>/`。
- 危险操作升级、审批分级是**审核者（Claude）的行为策略**，不写死在代码里；代码只负责忠实转发。
- opencode 事件流字段随版本演进：SSE 解析必须宽容（未知事件 Debug 后跳过，绝不 panic/报错中断）。

## File Structure

```
handoff/
├── main.go                        # 入口，调 cmd.Execute()
├── cmd/                           # cobra 子命令（薄壳，逻辑在 internal）
│   ├── root.go                    # 根命令 + --agentd/--target/--config 全局 flag + logx 初始化
│   ├── agentd.go                  # handoff agentd（--executor=opencode|fake）
│   ├── dispatch.go  wait.go  reply.go  tasks.go  attach.go
│   └── continue.go  done.go  diff.go  fetch.go  run.go
├── internal/
│   ├── logx/logx.go               # slog 统一初始化（文件 + stderr）
│   ├── config/config.go           # ~/.handoff/config.yaml（listen/token/targets）
│   ├── proto/proto.go             # Task/Event/Ticket 类型与状态机常量
│   ├── store/store.go             # SQLite 持久化
│   ├── agentd/
│   │   ├── hub.go                 # 事件订阅广播 + ticket 应答路由
│   │   ├── server.go              # HTTP API + WS 事件流
│   │   ├── manager.go             # 任务生命周期 + adapter 事件中介（ticket 化）
│   │   ├── workspace.go           # git 分支准备 / diff / 文件读取 / 命令执行
│   │   └── watchdog.go            # stalled 看门狗 + 启动恢复探测
│   ├── executor/
│   │   ├── executor.go            # Adapter 接口 + AdapterEvent
│   │   ├── fake/fake.go           # 脚本化 fake adapter（集成测试用）
│   │   └── opencode/
│   │       ├── api.go             # opencode server HTTP 客户端 + SSE 订阅
│   │       ├── proc.go            # opencode serve 进程管理（tmux 内）+ 探活
│   │       ├── taskenv.go         # 任务级 opencode 配置 + prompt 生成 + trailer 解析
│   │       └── adapter.go         # 组装为 executor.Adapter 实现
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
- Produces（HTTP API，Bearer token 鉴权）:

```
GET  /api/tasks                     → []proto.Task
GET  /api/tasks/{id}                → {task, pending_tickets, recent_events}  // attach 数据源
POST /api/tasks/{id}/reply          → body {ticket_id, answer}  // answer: "allow" / "deny[:原因]" / 自由文本
GET  /ws/events?task={id}&from_seq={n}  → WS，先补发 store 中 seq>n 的事件，再接实时流；客户端无需 ack（cursor 客户端自存）
```

```go
func NewServer(cfg *config.Config, st *store.Store, log *slog.Logger) *Server
func (s *Server) Handler() http.Handler   // 便于 httptest
func (s *Server) Hub() *Hub
```

reply 处理流程（唤醒闭环的回程）：store.AnswerTicket → hub.NotifyAnswer → 若 task 处于 waiting_answer 且无其余 pending ticket，UpdateTaskState(running)。ticket 的**创建**方在 manager（Task 8）——adapter 事件由 manager 中介转成 ticket，server 不直接创建。

- [ ] **Step 1: 写失败测试**（`httptest.NewServer(srv.Handler())`）：

```go
func TestAuthRequired(t *testing.T)        // 无 token 401；错 token 401；对 token 200
func TestReplyAnswersTicketAndNotifies(t *testing.T) {
	// 预置: task(waiting_answer) + 未答 ticket；goroutine 用 hub.WaitAnswer 阻塞等该 ticket
	// POST /api/tasks/{id}/reply {ticket_id, answer:"用 pgx 不用 gorm"}
	// 断言: WaitAnswer 解除并拿到原文；task.state 回到 running；attach 的 pending_tickets 清空
}
func TestReplyUnknownTicket404(t *testing.T)
func TestWSReplayThenLive(t *testing.T)       // 先 Append 两条事件，WS from_seq=0 收到补发两条；再 Publish 一条实时收到
```

- [ ] **Step 2: 确认失败** → **Step 3: 实现**（stdlib `http.NewServeMux` 方法路由；WS 用 `websocket.Accept`，写循环 select store 补发 + hub 订阅 chan）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：每个 API 入口 Info（method、path、task_id）；鉴权失败 Warn（remote_addr）；WS 连接建立/断开 Info（task、from_seq、补发条数）；reply Info（ticket、answer 截断 80 字符）；所有错误分支 Error 带 task/ticket 上下文与 cause。
- [ ] **Step 6: 加注释**：文件头（职责=对外唯一网络入口；边界=不创建 ticket、不启动 executor——那是 manager 的职责）；导出方法 doc 注释。
- [ ] **Step 7: Commit** `git commit -m "feat: agentd HTTP/WS 服务与 reply 回程"`

---

### Task 6: opencode API 客户端 + serve 进程管理

**Files:**
- Create: `internal/executor/opencode/api.go`, `internal/executor/opencode/proc.go`
- Test: `internal/executor/opencode/api_test.go`（httptest 起 fake opencode server 驱动，不依赖真实 opencode）

**Interfaces:**
- Produces:

```go
// api.go —— opencode server 的最小 HTTP 客户端
func NewAPI(baseURL, password string) *API   // basic auth: 用户名 opencode
func (a *API) CreateSession(ctx context.Context) (sessionID string, err error)        // POST /session
func (a *API) PromptAsync(ctx context.Context, sessionID, text string) error          // POST /session/{id}/prompt_async
func (a *API) RespondPermission(ctx context.Context, sessionID, permID, response string) error // POST /session/{id}/permissions/{permID}，response: "once"|"reject"
// SubscribeEvents 连 GET /event（SSE），每条事件回调 onEvent(raw json.RawMessage)；
// 断流指数退避 1s→2s→…→30s 自动重连，ctx 取消才返回。未知/解析失败的行 Debug 跳过。
func (a *API) SubscribeEvents(ctx context.Context, onEvent func(json.RawMessage)) error

// proc.go —— opencode serve 进程管理
type Proc struct{ Port int; Password string; TmuxSession string }
// StartServe 在 tmux 内拉起 `opencode serve --port <随机空闲端口> --hostname 127.0.0.1`，
// cwd=repoPath，env 注入 OPENCODE_SERVER_PASSWORD 与 OPENCODE_CONFIG（Task 10 生成的配置路径）；
// 轮询 GET / 直至就绪（10s 超时）。tmux session 命名 handoff-<id8>。
func StartServe(ctx context.Context, repoPath, taskID, configPath string, log *slog.Logger) (*Proc, error)
func (p *Proc) Alive() bool        // tmux has-session && HTTP 探活
func (p *Proc) Kill() error        // tmux kill-session
```

- [ ] **Step 1: 写失败测试**：`TestCreateSessionAndPrompt`（fake server 校验路径/方法/basic auth 头/请求体，返回固定 session id）；`TestRespondPermissionBody`（断言 body 含 `{"response":"once"}`）；`TestSubscribeReconnect`（fake SSE 发两条后断开连接，客户端重连后收到第三条——onEvent 共被调 3 次）；`TestSubscribeTolerantGarbage`（夹杂非 JSON 行不中断）。
- [ ] **Step 2: 确认失败** → **Step 3: 实现**（SSE 手写解析：按行读 `data: ` 前缀聚合，`bufio.Scanner` 加大 buffer 到 1MB——事件里可能带大段文本）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：每个 API 调用前后 Info（session、path、耗时），失败 Error 带响应体截断 200 字符；SSE 连接建立/断流/重连尝试 Info（第 n 次、退避秒数）——断流重连是核心链路必须可观测；StartServe Info（port、tmux session、就绪耗时）、就绪超时 Error 带 stderr 尾部。
- [ ] **Step 6: 加注释**：两文件头（api 职责=opencode HTTP/SSE 唯一出口，边界=不理解事件语义——语义归 adapter；proc 职责=进程生命周期，边界=不碰会话）；「为什么进程放 tmux 而不是 agentd 子进程」（agentd 重启不杀任务 + 用户可 attach 旁观）、「为什么 SSE 手写解析不引依赖」两处 why 注释。
- [ ] **Step 7: Commit** `git commit -m "feat: opencode HTTP/SSE 客户端与 serve 进程管理"`

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

### Task 8: Adapter 接口 + fake adapter + manager 中介 + 核心闭环集成测试

**Files:**
- Create: `internal/executor/executor.go`, `internal/executor/fake/fake.go`, `internal/agentd/manager.go`
- Modify: `internal/agentd/server.go`（挂 dispatch/continue/done 三条路由到 Manager）, `internal/client/client.go`（加 Dispatch/Continue/Done）, 新增 `cmd/dispatch.go`, `cmd/continue.go`, `cmd/done.go`
- Test: `internal/agentd/integration_test.go`

**Interfaces:**
- Produces:

```go
// internal/executor/executor.go
type StartReq struct{ Task proto.Task; PlanContent string; TaskDir string }
type Result struct{ Branch, CommitHash, SessionID, Summary string; OK bool; FailReason string }
type AdapterEvent struct {
	Type string          // "permission" | "question" | "progress" | "result"
	PermissionID string  // Type=permission 时有效（同时用作 ticket id，天然幂等）
	Text string          // permission 描述 / question 原文 / progress 文本
	Result *Result       // Type=result 时有效
}
type Adapter interface {
	Start(ctx context.Context, req StartReq) error                 // 异步启动，立即返回
	Events(taskID string) <-chan AdapterEvent                      // 该任务的事件流（Start 后可用）
	Send(ctx context.Context, taskID, text string) error           // 回答提问 / 回发修改指令（同一会话续接）
	RespondPermission(ctx context.Context, taskID, permID, decision string) error // decision: "once"|"reject"
	Stop(taskID string) error
}

// internal/agentd/manager.go —— 状态机中枢 + adapter 事件中介
func NewManager(st *store.Store, hub *Hub, ad executor.Adapter, cfg *config.Config, log *slog.Logger) *Manager
func (m *Manager) Dispatch(ctx, DispatchReq) (*proto.Task, error) // 建 task(pending)→建 taskDir 写 plan→Adapter.Start→state=running→goroutine 消费 Events
func (m *Manager) Continue(ctx, taskID, instructions string) error // waiting_review→running→Adapter.Send
func (m *Manager) Done(ctx, taskID string) error                   // waiting_review→completed→Adapter.Stop
```

manager 的事件中介循环（每任务一个 goroutine，这是整个系统的心脏）：

```go
// permission: CreateTicket(id=ev.PermissionID, kind="gate") → AppendEvent(permission_request)+Publish
//             → state=waiting_answer → go { ans := hub.WaitAnswer(ticket)
//               → ans=="allow" ? RespondPermission("once") : RespondPermission("reject") → state=running }
// question:   CreateTicket(id=uuid, kind="ask") → AppendEvent(question)+Publish → state=waiting_answer
//             → go { ans := hub.WaitAnswer(ticket) → Send(ans) → state=running }
// progress:   AppendEvent(progress)+Publish（不改状态）
// result:     OK → AppendEvent(completed,{branch,commit,summary}) ；!OK → AppendEvent(failed,{fail_reason})
//             两者都 → state=waiting_review（失败也交审核者裁决，不自动重试烧 token）
```

fake adapter：`fake.New(script []fake.Step)`，Step：`{Permission: "Bash: go test ./..."}`（发 permission 事件后阻塞，直到 RespondPermission 被调，记录 decision）、`{Question: "..."}`（阻塞到 Send）、`{Finish: Result{...}}`。Send/RespondPermission 的实参全部记录供断言。

- [ ] **Step 1: 写核心闭环集成测试（本计划最重要的测试）**：

```go
func TestFullLoop(t *testing.T) {
	// fake script: Permission("Bash: go test ./...") → Question("表结构用单数还是复数?") → Finish(OK, branch=handoff/T1)
	// 1. client.Dispatch(plan) → task running
	// 2. client.WaitEvent → permission_request；client.Reply(ticket,"allow")
	//    → 断言 fake 收到 RespondPermission("once")
	// 3. client.WaitEvent → question；client.Reply(ticket,"复数")
	//    → 断言 fake 收到 Send("复数")（回答原文无损透传）
	// 4. client.WaitEvent → completed(payload 含 branch/commit)；task.state==waiting_review
	// 5. client.Continue("把 users 表加索引") → 断言 fake 收到 Send(指令)；再 Finish → 再收 completed
	// 6. client.Done → task.state==completed 且 fake 收到 Stop
}
func TestFullLoopDeny(t *testing.T)   // Reply "deny:太危险" → fake 收到 RespondPermission("reject")
func TestRecoverMidTask(t *testing.T) {
	// fake 停在 Question 阻塞 → 新建 client（模拟全新审核者会话）→ ListTasks 看到 waiting_answer
	// → Attach 拿到 pending_tickets[0] 就是该问题 → Reply 后流程继续走完
	// 这是 spec §7「会话恢复」的验收测试
}
```

- [ ] **Step 2: 确认失败** → **Step 3: 实现 fake + manager + 三条新路由/命令**（dispatch 读本地 plan 文件 base64 上传：body {repo, plan_b64, plan_name, target}）→ **Step 4: 确认通过**（`go test -race ./...`）
- [ ] **Step 5: 加日志**：manager 是状态机中枢，每次状态迁移 Info（task、from→to、触发原因）；中介循环每类事件入口 Info（task、type、ticket/perm id）；WaitAnswer 解除 Info（ticket、等待时长、answer 截断 80 字符）；Dispatch/Continue/Done 出入口 Info；Adapter.Start 失败 Error 并 state=failed；Result 到达 Info（task、ok、branch、commit）。fake 里 Debug 级步骤日志。
- [ ] **Step 6: 加注释**：executor.go 文件头（Adapter 契约=五动作，实现方不得直接碰 store——所有状态由 manager 写，这条边界防止 adapter 与 manager 双写打架；PermissionID 复用为 ticket id 的幂等含义）；manager.go 文件头 + 「失败也进 waiting_review」的 why 注释。
- [ ] **Step 7: Commit** `git commit -m "feat: adapter 接口、fake 实现、manager 中介与核心闭环集成测试"`

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

### Task 10: opencode 任务环境——配置、prompt 与 trailer 解析

**Files:**
- Create: `internal/executor/opencode/taskenv.go`
- Test: `internal/executor/opencode/taskenv_test.go`

**Interfaces:**
- Produces:

```go
// WriteTaskEnv 在 taskDir 生成 opencode 配置与任务 prompt，返回二者路径。
func WriteTaskEnv(taskDir, taskID, planContent string) (configPath, promptPath string, err error)
// ParseTrailer 从回合末消息文本提取协议 JSON（取最后一个以 { 开头的行）。
// 返回 kind: "ask"（附 Question）| "finish"（附 Branch/Commit/Summary）| "none"
func ParseTrailer(text string) (kind string, t Trailer)
type Trailer struct{ Question, Branch, Commit, Summary string }
```

配置文件 `opencode.json`（经 `OPENCODE_CONFIG` 注入，spec 风险 #2 的 spike 在 Task 12 清单验证；fallback=写 repo 内 + gitignore）：

```json
{
  "permission": {
    "edit": "ask",
    "bash": "ask",
    "webfetch": "ask",
    "external_directory": "ask"
  }
}
```

prompt.md 模板（回合制纪律，spec §6 的落地，`text/template` 渲染）：

```markdown
你是 handoff 任务 {{.TaskID}} 的执行者，按下方实现计划执行。铁律：
1. 提问纪律：任何需要人决策的问题，输出单行 JSON `{"ask":"<问题>"}`
   然后结束本回合。审核者的回答会作为下一条消息发给你。
   禁止自行假设，禁止用其它格式提问。
2. 收尾纪律：全部完成后必须 git add 并 commit（不要 push），
   然后输出单行 JSON：{"branch":"<分支>","commit":"<hash>","summary":"<50字内摘要>"}
   作为本回合最后一行。
3. 只在当前分支工作，不切分支、不改 git 配置。

--- 实现计划 ---
{{.PlanContent}}
```

- [ ] **Step 1: 写失败测试**：`TestWriteTaskEnv`（生成后 opencode.json 可 Unmarshal 且 permission.bash=="ask"；prompt.md 含 plan 原文与 `{"ask":`；重复调用幂等覆盖）；`TestParseTrailer` 表驱动（末行 `{"ask":"用哪个库?"}` → kind=ask；末行 finish JSON → kind=finish 各字段正确；正文中间出现 JSON 但末行是普通文本 → 取最后一个 `{` 开头行；全文无 JSON → none；JSON 损坏 → none 不 panic）。
- [ ] **Step 2: 确认失败** → **Step 3: 实现**（配置用 Go 结构体 marshal 而非字符串拼接——防转义错误）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：生成成功 Info（taskDir、两个路径）；写失败 Error 带 path。ParseTrailer 是纯函数不打日志（调用方打）。
- [ ] **Step 6: 加注释**：文件头（职责=executor 环境物料与回合协议解析；边界=不启动进程、不发请求）；「为什么 permission 只设四类为 ask」（read/grep/glob 等只读操作放行，审批噪音会淹没审核者——权限收敛策略的代码侧底线，细粒度判断在审核者侧）的 why 注释。
- [ ] **Step 7: Commit** `git commit -m "feat: opencode 任务配置、回合制 prompt 与 trailer 解析"`

---

### Task 11: opencode adapter 组装

**Files:**
- Create: `internal/executor/opencode/adapter.go`
- Modify: `cmd/agentd.go`（flag `--executor=opencode|fake`，默认 opencode）
- Test: `internal/executor/opencode/adapter_test.go`（复用 Task 6 的 fake opencode server，全程不依赖真实 opencode）

**Interfaces:**
- Consumes: Task 6 的 `API`/`Proc`、Task 8 的 `executor.Adapter` 契约、Task 10 的 `WriteTaskEnv`/`ParseTrailer`
- Produces: `opencode.New(log *slog.Logger) *Adapter`（实现 executor.Adapter）

Start 流程：`WriteTaskEnv` → `StartServe`（tmux，cwd=repo，OPENCODE_CONFIG 注入）→ `CreateSession`（session id 经 manager 写入 task.ExecutorSession）→ `PromptAsync(prompt)` → 起 goroutine `SubscribeEvents` 做事件映射。可见性：tmux 第二窗口 `tail -f <taskDir>/render.log`，adapter 把 SSE 里的消息文本追加渲染进 render.log。

SSE → AdapterEvent 映射（事件名以 spike 抓到的真实样本为准，解析必须宽容）：

```go
// permission 类事件（如 permission.updated，含 permissionID 与描述）
//   → AdapterEvent{Type:"permission", PermissionID, Text: 工具与参数描述}
// 消息文本增量事件 → 累积当前回合文本；追加 render.log；节流后发 progress（每 30s 至多 1 条）
// 回合结束事件（session idle 类）→ ParseTrailer(当前回合全文):
//   kind=ask    → AdapterEvent{Type:"question", Text: t.Question}
//   kind=finish → AdapterEvent{Type:"result", Result:{OK:true, Branch,Commit,Summary, SessionID}}
//   kind=none   → 兜底：git -C repo log 对比任务起点有新 commit？
//                 有 → result OK（branch/commit 用 git 实况，summary=回合末 200 字符，Warn 记录不守纪律）
//                 无 → AdapterEvent{Type:"question", Text: 回合全文}（交审核者裁决，流程不卡死）
// serve 进程死亡（Alive()==false 且 SSE 断流重连 3 次失败）
//   → AdapterEvent{Type:"result", Result:{OK:false, FailReason:"opencode serve 已退出", ...stderr 尾部}}
```

Send = `PromptAsync`（同一 session，原生续接）；RespondPermission = `API.RespondPermission`；Stop = `Proc.Kill`。

- [ ] **Step 1: 写失败测试**（fake opencode server 按脚本推 SSE 事件）：`TestStartToPermissionFlow`（推 permission 事件 → Events 收到 Type=permission；RespondPermission 转发到 fake 收到 once）；`TestIdleClassifyAsk`（推文本 + idle → question 事件文本正确）；`TestIdleClassifyFinish`（trailer finish → result OK 字段正确）；`TestIdleFallbackNoTrailer`（无 trailer、测试 repo 无新 commit → question 兜底）；`TestServeDeathEmitsFailed`（杀 fake server 且探活失败 → result !OK）。
- [ ] **Step 2: 确认失败** → **Step 3: 实现** → **Step 4: 确认通过**（`go test -race ./internal/executor/...`）
- [ ] **Step 5: 加日志**：Start 各阶段 Info（taskDir、port、session id）；每个 AdapterEvent 产出 Info（task、type、关键字段截断）；trailer 走兜底 **Warn**（task、回合末 120 字符）——「executor 不守纪律」的观测点必须可见；serve 死亡 Error 带 stderr 尾部；Send/RespondPermission/Stop 出入口 Info。
- [ ] **Step 6: 加注释**：文件头（职责=opencode 语义到 Adapter 契约的翻译层；边界=不写 store、不做审批判断）；「回合文本累积与节流 progress」「兜底分类规则」两处 why 注释。
- [ ] **Step 7: Commit** `git commit -m "feat: opencode adapter 组装与事件映射"`

---

### Task 12: 看门狗 + 启动恢复 + wait --notify + e2e 手动验证清单

**Files:**
- Create: `internal/agentd/watchdog.go`, `docs/superpowers/e2e-checklist.md`
- Modify: `cmd/agentd.go`（启动 watchdog goroutine + RecoverOnStartup）, `cmd/wait.go`（--notify）
- Test: `internal/agentd/watchdog_test.go`

**Interfaces:**
- Produces: `RunWatchdog(ctx, st *store.Store, hub *Hub, stallTimeout time.Duration, log *slog.Logger)`——每分钟扫 running/waiting_answer 任务，最新 event 时间早于 stallTimeout → AppendEvent(stalled,{last_seq,idle}) + Publish（只发一次：已有 stalled 且其后无新事件则不重发）。
- Produces: `RecoverOnStartup(st *store.Store, hub *Hub, probe func(taskID string) bool, log *slog.Logger) error`——agentd 启动时对 running/waiting_answer 任务逐个探测执行器存活（opencode adapter 的 probe = `Proc.Alive()`：tmux has-session + HTTP 探活），不存活 → AppendEvent(failed,{reason:"agentd 重启后执行器已不在"}) + state=waiting_review；存活 → 重建 SSE 订阅继续消费（spec §8）。cmd/agentd.go 在起 HTTP 服务前调用。

- [ ] **Step 1: 写失败测试**：`TestWatchdogFiresOnceOnStall`（造一个 last event 3h 前的 running 任务，tick 两轮只产生一条 stalled）；`TestWatchdogIgnoresFreshAndTerminal`（新鲜任务与 completed 任务不触发）；`TestRecoverOnStartup`（probe 恒 false 的 running 任务 → failed 事件 + waiting_review；probe 恒 true 的不动）。tick 间隔做成参数便于测试注入 10ms。
- [ ] **Step 2: 确认失败** → **Step 3: 实现 + wait.go 加 --notify**（事件到达时 `exec.Command("osascript","-e", "display notification ... with title \"handoff\"")`，仅 darwin，失败仅 Warn 不影响主流程）→ **Step 4: 确认通过**
- [ ] **Step 5: 加日志**：watchdog 扫描轮 Debug（扫了几个）、触发 stalled Warn（task、idle 时长）；RecoverOnStartup 每任务结论 Info（task、alive、动作）；notify 失败 Warn。
- [ ] **Step 6: 加注释**：文件头 + 「只发一次」防事件风暴的 why。
- [ ] **Step 7: 写 e2e 手动验证清单** `docs/superpowers/e2e-checklist.md`（真实 opencode 环境逐项打勾）：

```markdown
# E2E 手动验证清单（真实 opencode）
前置：executor 机装 opencode 并配好模型凭证；`handoff agentd --executor=opencode` 已起。
- [ ] SPIKE-1（spec 风险#1）：手动 `opencode serve` + curl 建会话发 prompt，抓 /event SSE 原始样本：
      确认 permission 事件类型名/字段、回合结束（idle）事件类型名 —— 对照调整 adapter 映射
- [ ] SPIKE-2（spec 风险#2）：验证 OPENCODE_CONFIG 环境变量注入配置生效（permission.bash=ask 真的会问）；
      不生效则切 fallback：写 repo 内 opencode.json + gitignore
- [ ] dispatch → wait 被 permission_request 唤醒 → approve → 执行继续 → completed → diff 有内容
- [ ] deny 链路：reply --deny 后 executor 收到 reject 并调整做法
- [ ] 提问链路：executor 输出 {"ask":...} → wait 唤醒 → reply --answer → 下一回合收到原文
- [ ] 审批挂起过夜：permission 不答搁置 8h 后 approve —— opencode 侧等待不超时、流程继续（替代原 hook 长阻塞 spike）
- [ ] continue：回发修改指令 → 同一 session 续接 → 二轮 completed diff 含新改动
- [ ] 断网演练：wait 期间关 Wi-Fi 3min 恢复 → 自动重连并收到期间积压事件
- [ ] 恢复演练：杀掉审核者会话 → 新会话 tasks+attach 重建现场 → 处理 pending 后流程走通
- [ ] agentd 重启：任务执行中重启 agentd → RecoverOnStartup 重连 SSE，流程不中断
- [ ] tmux attach 能看到 render.log 实况滚动
- [ ] 远程演练（可选功能）：devbox 上起 agentd，本机 --target devbox 跑通上述主链路
```

- [ ] **Step 8: Commit** `git commit -m "feat: 看门狗、启动恢复、通知兜底与 e2e 验证清单"`

---

### Task 13: README + 收尾自检

**Files:**
- Create: `README.md`
- Modify: 按自检结果修补

- [ ] **Step 1: 写 README**：一段话定位、架构图（从 spec 复制）、快速开始（agentd 启动 / 配对 token / dispatch / 审核者侧典型循环）、命令速查表、审核者会话恢复两条命令、troubleshooting（去哪看日志：`~/.handoff/agentd.log`、taskDir 下 render.log / stderr.log；tmux session 命名规则）。
- [ ] **Step 2: 全量回归** `go vet ./... && go test -race ./...`
- [ ] **Step 3: instrumenting-code 终检**（逐项过，任一不过回去修）：每个错误分支带上下文日志；外部调用（git/tmux/HTTP/SSE/WS）前后有日志；成功路径不静默；无 fmt.Printf 充当日志；新文件全有头注释；导出函数全有 doc 注释。
- [ ] **Step 4: 用户全局 CLAUDE.md §5 终审清单**逐项确认（完成目标/架构一致/注释/日志/无硬编码等）。
- [ ] **Step 5: Commit** `git commit -m "docs: README 与收尾自检"`
