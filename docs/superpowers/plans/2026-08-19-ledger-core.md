# 账本域核心库（Plan A / B156.1-核心）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地 `internal/ledger` 包——任务卡（Card）账本的持久化与全部领域操作（B 号分配、CAS 状态转移 + workflow gate、类型化关系边 + 环检测、合并/拆回/拆分、基线分支继承、账本事件单流 + 推送、裁决项、派生标记），PG 为主、SQLite 回退，纯库 + 单测，不含 HTTP/CLI（那是 Plan A2）。

**Architecture:** 单包 `internal/ledger` 即账本域边界（蓝图 §3.8：不 import agentd/executor 的 internal，反向也只许经此包公共 API）。所有变更操作走 `mutate` 事务包裹（PG 用一把粗粒度 advisory lock 串行化写入——写 QPS 极小，正确性优先；SQLite 靠单写者天然串行）。事件在写操作同事务内 append，PG 事务内 `pg_notify` 推送、SQLite 提交后进程内回调。方言差异（占位符、DDL、时间、通知）收在 store 层四个小函数里，不外溢。

**Tech Stack:** Go 1.26（模块 `github.com/Xsxdot/handoff`）、`modernc.org/sqlite`（既有）、`github.com/jackc/pgx/v5/stdlib`（新增，database/sql 驱动）、标准库 `database/sql`。测试 `go test`，SQLite 全跑，PG 冒烟由 `LEDGER_TEST_PG_DSN` 环境变量门控（未设即 skip）。

**上游 spec:** `docs/superpowers/specs/2026-08-18-workbench-phase1-design.md`（§2 数据模型 + §2.1 DDL 是本 plan 的法源；DDL 落码以 spec 为准，本 plan 内联的 DDL 与其一致）。

**前置条件（开工前执行者必查）：**
1. 本分支基于 main 且 main 已含 web-console 合并（判法：`git log --oneline -5 main | grep -q "web-console"` 或 `ls internal/agentd/mirror.go` 存在——若不存在，STOP 上报 BLOCKED，说明 web-console 尚未合回 main）。
2. `go build ./... && go test ./...` 在基线上全绿（判据先在基线上重跑，防基线自身就是红的）。

**House rules（全程适用）：**
- 日志走 `log()` 返回 `slog.Default()` 的既有模式（照抄 `internal/store/store.go` 的做法），**禁止 `fmt.Printf`**。账本库是叶子层：错误一律 wrap 上下文返回、不在叶子层打错误日志（调用方打）；仅三类例外主动打日志——`Open` 成功（Info，含方言与目标）、CAS/gate/环检测/合并校验的**拒绝**（Warn，含卡 id 与拒因）、默认工作流 seed（Info）。这与 `internal/store` 的既有纪律一致。
- 每个新文件顶部写「职责 + 边界」package/file 注释；每个导出符号写 doc 注释（参数、返回、注意事项）；复杂判定（环检测、基线继承、CAS）写中文「为什么」注释。
- SQL 一律写 `?` 占位符，经 `s.q()` 重写为 PG 的 `$N`；时间经 `s.tval()`/`toTime()` 出入。
- 每个 Task 完成即 commit，提交信息用 Task 内给定原文。

---

## File Structure

```
internal/ledger/
  ledger.go        // package 注释：职责+边界；错误哨兵；日志 helper
  types.go         // Card/Attachment/Relation/Event/Workflow/Decision 等类型与常量
  store.go         // Open、方言、schema DDL、mutate 事务包裹、eventSink
  cards.go         // CreateCard/GetCard/ListCards/UpdateCard/CloseCard/ReviveCard、B 号分配
  move.go          // MoveCard（CAS + gate）
  relations.go     // AddBlocks/AddRelation/RemoveRelation、环检测、EffectiveBaseBranch
  merge.go         // MergeCards/UnmergeCard/SplitCard
  events.go        // appendEvent、EventsFromAsc、Subtree、AddComment、RecordAcceptance、MarkNeedsHuman
  workflows.go     // Workflow 聚合：Put/Get/EnsureDefaults/MigrateCard
  decisions.go     // OpenDecision/AnswerDecision/ListDecisions
  derived.go       // CardView 派生标记（blocked/跟随/needs）
  *_test.go        // 与实现文件同名配对；store_pg_test.go 为 PG 冒烟（env 门控）
internal/config/config.go   // 追加 LedgerConfig（omitempty，硬约束见 Task 1）
```

派发模板（dispatch_templates）表的 DDL 在本 plan 建（schema 完整落一次），但其领域操作归 Plan C（节点执行器）——本 plan 只建表不写操作，YAGNI。

---

### Task 1: pgx 依赖 + LedgerConfig 配置节

**Files:**
- Modify: `go.mod`（go get 自动）
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`（已存在则追加）

- [ ] **Step 1: 引入 pgx**

```bash
go get github.com/jackc/pgx/v5/stdlib@latest
go mod tidy
```

- [ ] **Step 2: 写失败测试——LedgerConfig round-trip**

在 `internal/config/config_test.go` 追加：

```go
// TestLedgerConfigRoundTrip 保证新增 ledger 节可保存可回读，且旧配置文件
// （无 ledger 键）加载不受影响——KnownFields(true) 下新键必须 omitempty，
// 否则新版写的配置会让旧版 agentd 起不来（config.go 顶部注释的既有约束）。
func TestLedgerConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	c := &Config{Listen: "127.0.0.1:0", Token: "t", DataDir: dir}
	c.Ledger.DSN = "postgres://u:p@localhost:5432/handoff"
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Ledger.DSN != c.Ledger.DSN {
		t.Fatalf("dsn 丢失: %q", got.Ledger.DSN)
	}
	// 空 ledger 节不得写进文件（omitempty 生效的直接证据）
	c2 := &Config{Listen: "127.0.0.1:0", Token: "t", DataDir: dir}
	if err := Save(p, c2); err != nil {
		t.Fatalf("save2: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "ledger") {
		t.Fatalf("空 ledger 节不应落盘: %s", raw)
	}
}
```

（若 `Save` 的签名与现存不符——现存是 `config.Save(path, cfg)` 还是方法，以 `internal/config/config.go` 实际为准改测试调用，不改生产签名。）

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestLedgerConfigRoundTrip -v`
Expected: FAIL（`c.Ledger` 未定义）

- [ ] **Step 4: 实现 LedgerConfig**

在 `internal/config/config.go` 的 `Config` 结构体内追加字段（放在 `Targets` 之后）：

```go
	// Ledger 中心账本库连接。DSN 空 = 单机回退模式（账本落
	// DataDir/ledger.db 的 SQLite）。omitempty 是硬约束不是风格：
	// 解码是 KnownFields(true)，新键不 omitempty 会让旧版 agentd
	// 读到新版写的配置直接启动失败。
	Ledger LedgerConfig `yaml:"ledger,omitempty"`
```

同文件追加类型（与其他子结构体放一起）：

```go
// LedgerConfig 账本域（任务卡）中心库配置。只描述本机如何连库，
// 不描述库里有什么——schema 归 internal/ledger 管。
type LedgerConfig struct {
	// DSN 形如 postgres://user:pass@host:5432/db。空 = SQLite 回退。
	DSN string `yaml:"dsn,omitempty"`
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/config/ -v`
Expected: 全部 PASS（含既有用例——严格解码不被破坏）

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config/
git commit -m "feat(ledger): pgx 依赖 + config 新增 ledger.dsn 节（omitempty 硬约束）"
```

---

### Task 2: 包骨架——types.go + ledger.go（类型、常量、错误哨兵）

**Files:**
- Create: `internal/ledger/ledger.go`
- Create: `internal/ledger/types.go`

纯声明，无行为，不写测试（后续每个 Task 的测试都会碾过这些类型）。

- [ ] **Step 1: 写 ledger.go**

```go
// Package ledger 是账本域的唯一入口：任务卡（Card）、类型化关系边、
// 账本事件单流、工作流聚合与裁决项的持久化与领域操作。
//
// 边界（蓝图 §3.8 模块化单体约束）：
//   - 本包不 import internal/agentd、internal/executor 的任何东西；
//   - 执行域与本包的全部联系 = card_tasks 弱引用表 +（对侧的）opaque
//     card_id 标签，无跨库外键；
//   - 账本库凭据只存在于协调机（config.ledger.dsn），executor 拿不到。
//
// 并发模型：所有写操作经 mutate() 串行化（PG advisory lock / SQLite
// 单写者），换取环检测、B 号分配、CAS 的读-判-写原子性；写 QPS 极小，
// 正确性优先于吞吐。
package ledger

import (
	"errors"
	"log/slog"
)

// 错误哨兵。调用方（CLI/agentd）按哨兵翻译为退出码或 HTTP 状态。
var (
	ErrNotFound    = errors.New("ledger: 记录不存在")
	ErrCASConflict = errors.New("ledger: 状态已被并发修改")   // Move 前值不符
	ErrGateBlocked = errors.New("ledger: workflow gate 拒绝") // 缺附件/缺判据
	ErrCycle       = errors.New("ledger: 阻塞边成环")
	ErrBadMerge    = errors.New("ledger: 合并校验失败")       // 跨基线/链式/重复
	ErrBadState    = errors.New("ledger: 当前状态不允许该操作")
)

// log 返回全局 slog——函数而非包变量，令 main 侧后设的
// slog.SetDefault 也能生效（与 internal/store 同一模式）。
func log() *slog.Logger { return slog.Default() }
```

- [ ] **Step 2: 写 types.go**

```go
// 账本域的数据类型与受控词表。字段与 spec §2.1 DDL 一一对应；
// 状态骨架锚点、关系类型、事件类型的字符串字面量以本文件为唯一定义点。
package ledger

import (
	"encoding/json"
	"time"
)

// 状态骨架锚点（workflow 自定义状态插在锚点之间；终止不在 States 序列里，
// 它经 CloseCard 从任意非终态进入，带 reason）。
const (
	StatusTodo   = "待办"
	StatusDoing  = "进行中"
	StatusReview = "待审阅"
	StatusDone   = "已完成"
	StatusClosed = "终止"
)

// 终止 reason 受控词表。
const (
	CloseCancelled = "取消"
	CloseAbandoned = "废弃"
	CloseShelved   = "搁置" // 唯一可复活的终止
)

// 关系类型。merged_into 不许经 AddRelation 直建，必须走 MergeCards
// （因为要做基线/链式校验）。
const (
	RelBlocks         = "blocks"
	RelMergedInto     = "merged_into"
	RelDiscoveredFrom = "discovered_from"
	RelSplitFrom      = "split_from"
	RelRelates        = "relates"
)

// 事件类型（card_events.type）。task_mirrored 由 Plan B 镜像子系统写入，
// 这里先占词表位。
const (
	EvCardCreated        = "card_created"
	EvStatusMoved        = "status_moved"
	EvDispatched         = "dispatched"
	EvReviewVerdict      = "review_verdict"
	EvMerged             = "merged"
	EvUnmerged           = "unmerged"
	EvSplit              = "split"
	EvAcceptanceRecorded = "acceptance_recorded"
	EvComment            = "comment"
	EvNeedsHuman         = "needs_human"
	EvNeedsCleared       = "needs_cleared"
	EvDecisionOpened     = "decision_opened"
	EvDecisionAnswered   = "decision_answered"
	EvTaskMirrored       = "task_mirrored"
)

// Attachment 卡的附件引用。Path 是相对 docs/superpowers/ 的规范形 git 路径。
type Attachment struct {
	Kind string `json:"kind"` // spec|plan|doc
	Path string `json:"path"`
}

// Card 任务卡。字段语义见 spec §2；零值时间用 IsZero 判空。
type Card struct {
	ID                 string
	Title              string
	Status             string
	TerminateReason    string // 仅 Status==终止 时非空
	Priority           string // 高|中|低
	Project            string
	ParentID           string
	WorkflowName       string
	WorkflowVersion    int
	Attachments        []Attachment
	AcceptanceCriteria string
	BaseBranch         string // 空 = 继承祖先/项目主线（EffectiveBaseBranch 解析）
	DriverSession      string
	DriverHeartbeatAt  time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Relation 类型化关系边。
type Relation struct {
	From, To, Type string
	CreatedAt      time.Time
}

// Event 账本单流事件。镜像事件三个 Source 字段非空，其余事件为空。
type Event struct {
	Seq          int64
	CardID       string // 空 = 项目级事件（如项目级裁决）
	Type         string
	Actor        string
	Payload      json.RawMessage
	SourceTarget string
	SourceTask   string
	SourceSeq    int64
	CreatedAt    time.Time
}

// Gate workflow 转移进入某状态前的门条件。
type Gate struct {
	RequireAttachment string `json:"require_attachment,omitempty"` // 附件 kind 非空集
	RequireAcceptance bool   `json:"require_acceptance,omitempty"` // 验收判据非空
}

// WorkflowDef 状态机形状。States 是含插入状态的全序列（不含「终止」）；
// 一期不限制转移方向（人工回退是真实需求），只校验目标在 States 内 + gate。
type WorkflowDef struct {
	States []string        `json:"states"`
	Gates  map[string]Gate `json:"gates,omitempty"` // key = 目标状态
}

// Workflow 不可变版本化聚合：同 name 只增版本，不改旧行。
type Workflow struct {
	Name      string
	Version   int
	Def       WorkflowDef
	CreatedAt time.Time
}

// Decision 裁决项。CardID 空 = 项目级请示。
type Decision struct {
	ID         int64
	CardID     string
	Body       string
	Options    []string
	Status     string // open|answered
	CreatedBy  string
	Answer     string
	AnsweredBy string
	CreatedAt  time.Time
	AnsweredAt time.Time
}

// CardView = Card + 查询期计算的派生标记（不落列，spec §2）。
type CardView struct {
	Card
	Blocked       bool
	BlockedBy     []string // 未完成的 blocker
	Following     string   // 非空 = merged_into 的承载卡 id（跟随态）
	NeedsReason   string   // 非空 = 等人，值为 reason
	OpenDecisions int
}

// CardFilter ListCards 的过滤条件；零值 = 不过滤该维度。
type CardFilter struct {
	Project         string
	Status          string
	BaseBranch      string
	Blocked         bool // true = 只要 blocked
	Needs           bool // true = 只要 等人/有 open 裁决
	IncludeTerminal bool // false = 排除 已完成/终止
}
```

- [ ] **Step 3: 编译检查 + Commit**

Run: `go build ./internal/ledger/`
Expected: 编译通过（unused 警告不存在——Go 对未使用的包级声明不报错）

```bash
git add internal/ledger/
git commit -m "feat(ledger): 包骨架——类型、受控词表、错误哨兵"
```

---

### Task 3: store.go——Open、方言、schema、mutate 事务

**Files:**
- Create: `internal/ledger/store.go`
- Test: `internal/ledger/store_test.go`
- Test: `internal/ledger/store_pg_test.go`

- [ ] **Step 1: 写失败测试**

`internal/ledger/store_test.go`：

```go
// store 层测试基座：所有领域操作测试共用 newTestStore。
// SQLite 全量跑；PG 冒烟在 store_pg_test.go 由环境变量门控。
package ledger

import (
	"path/filepath"
	"testing"
)

// newTestStore 返回临时 SQLite 账本库。后续所有 *_test.go 复用。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := newTestStore(t)
	// 全部表都建出来了：逐表 SELECT 不报错即证明 DDL 幂等执行成功
	for _, tbl := range []string{"cards", "card_relations", "card_tasks",
		"card_events", "workflows", "dispatch_templates", "decisions",
		"mirror_lease", "mirror_cursors", "ledger_meta"} {
		if _, err := s.db.Exec("SELECT * FROM " + tbl + " LIMIT 0"); err != nil {
			t.Fatalf("表 %s 不存在: %v", tbl, err)
		}
	}
	// 幂等：重开不报错
	s2, err := Open(filepath.Join(filepath.Dir(s.path), "ledger.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s2.Close()
}

func TestQRebind(t *testing.T) {
	s := &Store{dialect: dialectPG}
	got := s.q("SELECT ? , ?")
	if got != "SELECT $1 , $2" {
		t.Fatalf("rebind: %q", got)
	}
	s2 := &Store{dialect: dialectSQLite}
	if s2.q("SELECT ?") != "SELECT ?" {
		t.Fatal("sqlite 不应重写")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run 'TestOpen|TestQRebind' -v`
Expected: FAIL（Store/Open 未定义）

- [ ] **Step 3: 实现 store.go**

```go
// 账本库的打开、schema、方言吸收与事务包裹。方言差异只允许出现在
// 本文件的四个点：driver 选择、DDL 变体、q() 占位符重写、tval()/toTime()
// 时间编解码、notify()。其余文件写方言无关的 SQL。
package ledger

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PG driver: "pgx"
	_ "modernc.org/sqlite"             // SQLite driver: "sqlite"
)

type dialect int

const (
	dialectSQLite dialect = iota
	dialectPG
)

// Store 账本库句柄。并发安全；Close 后不可用。
type Store struct {
	db      *sql.DB
	dialect dialect
	path    string // 仅 SQLite：文件路径（测试用）

	mu        sync.Mutex
	listeners []func(seq int64) // SQLite 回退模式的进程内事件推送
}

// Open 打开账本库并幂等建 schema。dsn 以 postgres:// 或 postgresql://
// 开头走 PG，否则视为 SQLite 文件路径（单机回退模式）。
func Open(dsn string) (*Store, error) {
	s := &Store{}
	var err error
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		s.dialect = dialectPG
		s.db, err = sql.Open("pgx", dsn)
	} else {
		s.dialect = dialectSQLite
		s.path = dsn
		s.db, err = sql.Open("sqlite", dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	}
	if err != nil {
		return nil, fmt.Errorf("打开账本库: %w", err)
	}
	if err := s.db.Ping(); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("连接账本库: %w", err)
	}
	if err := s.ensureSchema(); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("建账本 schema: %w", err)
	}
	log().Info("账本库已打开", "dialect", map[dialect]string{dialectSQLite: "sqlite", dialectPG: "postgres"}[s.dialect])
	return s, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// q 把 ? 占位符重写为 PG 的 $N。仅做占位符转换，不做任何转义——
// SQL 里不许出现字面 '?'（本包 SQL 全部自控，无用户拼接）。
func (s *Store) q(query string) string {
	if s.dialect != dialectPG {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tval 时间入库编码：PG 直接传 time.Time（timestamptz），SQLite 存
// RFC3339Nano 文本。
func (s *Store) tval(t time.Time) any {
	if s.dialect == dialectPG {
		return t.UTC()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// toTime 时间出库解码，兼容两方言的扫描产物。
func toTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		ts, _ := time.Parse(time.RFC3339Nano, t)
		return ts
	case []byte:
		ts, _ := time.Parse(time.RFC3339Nano, string(t))
		return ts
	}
	return time.Time{}
}

// eventSink 收集本事务内 append 的事件 seq，供提交后做 SQLite 进程内
// 推送（PG 的推送在事务内 pg_notify，由 LISTEN 端收）。
type eventSink struct{ seqs []int64 }

// mutate 写事务包裹：PG 先拿全局 advisory lock 串行化全部账本写
// （B 号分配的 max+1、环检测的读全图、合并校验都要求读-判-写原子；
// 写 QPS 极小，一把粗锁换正确性，蓝图 §3.1 记过这笔账）；SQLite 由
// 单写者天然串行。fn 内经 sink append 的事件在提交后触发本地 listeners。
func (s *Store) mutate(fn func(tx *sql.Tx, sink *eventSink) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开账本事务: %w", err)
	}
	defer tx.Rollback() // 提交后是 no-op
	if s.dialect == dialectPG {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(915601)`); err != nil {
			return fmt.Errorf("取账本写锁: %w", err)
		}
	}
	sink := &eventSink{}
	if err := fn(tx, sink); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交账本事务: %w", err)
	}
	if s.dialect == dialectSQLite && len(sink.seqs) > 0 {
		s.mu.Lock()
		ls := append([]func(int64){}, s.listeners...)
		s.mu.Unlock()
		for _, fn := range ls {
			for _, seq := range sink.seqs {
				fn(seq)
			}
		}
	}
	return nil
}

// OnEvent 注册进程内事件回调（仅 SQLite 回退模式有意义；PG 模式下
// 消费者应走 LISTEN card_events）。回调在提交后同步触发，勿做慢活。
func (s *Store) OnEvent(fn func(seq int64)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, fn)
}
```

- [ ] **Step 4: 实现 ensureSchema（同文件追加）**

DDL 与 spec §2.1 一致；SQLite 按 spec 的回退映射。

```go
// ensureSchema 幂等建表。与 internal/store 相同的策略：CREATE IF NOT
// EXISTS 列表顺序执行，无版本号——幂等即契约；后续加列走「ALTER +
// 容忍 duplicate column」（现在还没有第二版，留到真需要时加）。
func (s *Store) ensureSchema() error {
	var ddl []string
	if s.dialect == dialectPG {
		ddl = []string{
			`CREATE TABLE IF NOT EXISTS cards (
				id TEXT PRIMARY KEY, title TEXT NOT NULL, status TEXT NOT NULL,
				terminate_reason TEXT, priority TEXT NOT NULL DEFAULT '中',
				project TEXT NOT NULL, parent_id TEXT REFERENCES cards(id),
				workflow_name TEXT NOT NULL, workflow_version INT NOT NULL,
				attachments JSONB NOT NULL DEFAULT '[]', acceptance_criteria TEXT,
				base_branch TEXT, driver_session TEXT, driver_heartbeat_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_cards_board ON cards(project, status)`,
			`CREATE INDEX IF NOT EXISTS idx_cards_parent ON cards(parent_id)`,
			`CREATE TABLE IF NOT EXISTS card_relations (
				from_id TEXT NOT NULL REFERENCES cards(id),
				to_id TEXT NOT NULL REFERENCES cards(id),
				type TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL,
				PRIMARY KEY (from_id, to_id, type))`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uq_rel_merged_into
				ON card_relations(from_id) WHERE type = 'merged_into'`,
			`CREATE INDEX IF NOT EXISTS idx_rel_to ON card_relations(to_id, type)`,
			`CREATE TABLE IF NOT EXISTS card_tasks (
				card_id TEXT NOT NULL REFERENCES cards(id),
				target TEXT NOT NULL, task_id TEXT NOT NULL, purpose TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (target, task_id))`,
			`CREATE INDEX IF NOT EXISTS idx_card_tasks_card ON card_tasks(card_id)`,
			`CREATE TABLE IF NOT EXISTS card_events (
				seq BIGSERIAL PRIMARY KEY, card_id TEXT REFERENCES cards(id),
				type TEXT NOT NULL, actor TEXT NOT NULL, payload JSONB NOT NULL,
				source_target TEXT, source_task TEXT, source_seq BIGINT,
				created_at TIMESTAMPTZ NOT NULL)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uq_events_mirror
				ON card_events(source_target, source_task, source_seq)
				WHERE source_target IS NOT NULL`,
			`CREATE INDEX IF NOT EXISTS idx_events_card ON card_events(card_id, seq)`,
			`CREATE TABLE IF NOT EXISTS workflows (
				name TEXT NOT NULL, version INT NOT NULL, definition JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (name, version))`,
			`CREATE TABLE IF NOT EXISTS dispatch_templates (
				name TEXT NOT NULL, version INT NOT NULL, definition JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (name, version))`,
			`CREATE TABLE IF NOT EXISTS decisions (
				id BIGSERIAL PRIMARY KEY, card_id TEXT REFERENCES cards(id),
				body TEXT NOT NULL, options JSONB,
				status TEXT NOT NULL DEFAULT 'open', created_by TEXT NOT NULL,
				answer TEXT, answered_by TEXT,
				created_at TIMESTAMPTZ NOT NULL, answered_at TIMESTAMPTZ)`,
			`CREATE INDEX IF NOT EXISTS idx_decisions_open ON decisions(status) WHERE status = 'open'`,
			`CREATE TABLE IF NOT EXISTS mirror_lease (
				id INT PRIMARY KEY CHECK (id = 1),
				holder TEXT NOT NULL, lease_until TIMESTAMPTZ NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS mirror_cursors (
				target TEXT PRIMARY KEY, last_seq BIGINT NOT NULL,
				updated_at TIMESTAMPTZ NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS ledger_meta (
				key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		}
	} else {
		// SQLite 回退映射（spec §2.1 文末注）：BIGSERIAL→AUTOINCREMENT、
		// JSONB→TEXT、TIMESTAMPTZ→TEXT(RFC3339)、partial index→应用层校验
		// （merged_into 至多一条与镜像幂等在写路径校验，见 merge.go/events.go）。
		ddl = []string{
			`CREATE TABLE IF NOT EXISTS cards (
				id TEXT PRIMARY KEY, title TEXT NOT NULL, status TEXT NOT NULL,
				terminate_reason TEXT, priority TEXT NOT NULL DEFAULT '中',
				project TEXT NOT NULL, parent_id TEXT REFERENCES cards(id),
				workflow_name TEXT NOT NULL, workflow_version INTEGER NOT NULL,
				attachments TEXT NOT NULL DEFAULT '[]', acceptance_criteria TEXT,
				base_branch TEXT, driver_session TEXT, driver_heartbeat_at TEXT,
				created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_cards_board ON cards(project, status)`,
			`CREATE INDEX IF NOT EXISTS idx_cards_parent ON cards(parent_id)`,
			`CREATE TABLE IF NOT EXISTS card_relations (
				from_id TEXT NOT NULL REFERENCES cards(id),
				to_id TEXT NOT NULL REFERENCES cards(id),
				type TEXT NOT NULL, created_at TEXT NOT NULL,
				PRIMARY KEY (from_id, to_id, type))`,
			`CREATE INDEX IF NOT EXISTS idx_rel_to ON card_relations(to_id, type)`,
			`CREATE TABLE IF NOT EXISTS card_tasks (
				card_id TEXT NOT NULL REFERENCES cards(id),
				target TEXT NOT NULL, task_id TEXT NOT NULL, purpose TEXT NOT NULL,
				created_at TEXT NOT NULL, PRIMARY KEY (target, task_id))`,
			`CREATE INDEX IF NOT EXISTS idx_card_tasks_card ON card_tasks(card_id)`,
			`CREATE TABLE IF NOT EXISTS card_events (
				seq INTEGER PRIMARY KEY AUTOINCREMENT, card_id TEXT REFERENCES cards(id),
				type TEXT NOT NULL, actor TEXT NOT NULL, payload TEXT NOT NULL,
				source_target TEXT, source_task TEXT, source_seq INTEGER,
				created_at TEXT NOT NULL)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uq_events_mirror
				ON card_events(source_target, source_task, source_seq)`,
			`CREATE INDEX IF NOT EXISTS idx_events_card ON card_events(card_id, seq)`,
			`CREATE TABLE IF NOT EXISTS workflows (
				name TEXT NOT NULL, version INTEGER NOT NULL, definition TEXT NOT NULL,
				created_at TEXT NOT NULL, PRIMARY KEY (name, version))`,
			`CREATE TABLE IF NOT EXISTS dispatch_templates (
				name TEXT NOT NULL, version INTEGER NOT NULL, definition TEXT NOT NULL,
				created_at TEXT NOT NULL, PRIMARY KEY (name, version))`,
			`CREATE TABLE IF NOT EXISTS decisions (
				id INTEGER PRIMARY KEY AUTOINCREMENT, card_id TEXT REFERENCES cards(id),
				body TEXT NOT NULL, options TEXT,
				status TEXT NOT NULL DEFAULT 'open', created_by TEXT NOT NULL,
				answer TEXT, answered_by TEXT,
				created_at TEXT NOT NULL, answered_at TEXT)`,
			`CREATE INDEX IF NOT EXISTS idx_decisions_open ON decisions(status)`,
			`CREATE TABLE IF NOT EXISTS mirror_lease (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				holder TEXT NOT NULL, lease_until TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS mirror_cursors (
				target TEXT PRIMARY KEY, last_seq INTEGER NOT NULL,
				updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS ledger_meta (
				key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		}
	}
	for _, stmt := range ddl {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("执行 DDL %q: %w", stmt[:40], err)
		}
	}
	return nil
}
```

注意 SQLite 的 `uq_events_mirror` 没有 partial WHERE：SQLite 的 UNIQUE 索引对多行 NULL 不冲突（NULL != NULL），语义恰好等价，写一行「为什么可以不用 partial」的注释。

- [ ] **Step 5: 写 PG 冒烟测试 store_pg_test.go**

```go
// PG 冒烟：真 PG 上跑 schema + 基本读写。默认 skip，设
// LEDGER_TEST_PG_DSN 后启用（判据⑩落地前审核者本地跑一次）。
package ledger

import (
	"os"
	"testing"
)

func newPGStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 PG 冒烟")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPGSchema(t *testing.T) {
	s := newPGStore(t)
	if _, err := s.db.Exec("SELECT * FROM cards LIMIT 0"); err != nil {
		t.Fatalf("cards 表: %v", err)
	}
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/ledger/ -v`
Expected: TestOpenCreatesSchema / TestQRebind PASS，TestPGSchema SKIP

- [ ] **Step 7: Commit**

```bash
git add internal/ledger/
git commit -m "feat(ledger): store——双方言 Open/schema/mutate 事务/事件推送底座"
```

---

### Task 4: workflows.go——工作流聚合与出厂默认

（先于 cards：CreateCard 要钉工作流版本。）

**Files:**
- Create: `internal/ledger/workflows.go`
- Test: `internal/ledger/workflows_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import "testing"

func TestEnsureDefaultWorkflows(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	wf, err := s.GetWorkflow("feature", 0) // 0 = 最新版
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if wf.Version != 1 || wf.Def.States[1] != "已出spec" {
		t.Fatalf("feature 流不符: %+v", wf)
	}
	if g := wf.Def.Gates["已出spec"]; g.RequireAttachment != "spec" {
		t.Fatalf("gate 缺失: %+v", wf.Def.Gates)
	}
	// 幂等：重复 seed 不产生新版本
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if wf2, _ := s.GetWorkflow("feature", 0); wf2.Version != 1 {
		t.Fatalf("seed 不幂等，版本涨到 %d", wf2.Version)
	}
}

func TestPutWorkflowVersioning(t *testing.T) {
	s := newTestStore(t)
	def := WorkflowDef{States: []string{"待办", "进行中", "已完成"}}
	v, err := s.PutWorkflow("bugx", def)
	if err != nil || v != 1 {
		t.Fatalf("v1: %d %v", v, err)
	}
	def.States = []string{"待办", "进行中", "待审阅", "已完成"}
	v, err = s.PutWorkflow("bugx", def)
	if err != nil || v != 2 {
		t.Fatalf("v2: %d %v", v, err)
	}
	// 旧版本仍可读（不可变版本化：钉在 v1 的卡随时能取回自己的形状）
	old, err := s.GetWorkflow("bugx", 1)
	if err != nil || len(old.Def.States) != 3 {
		t.Fatalf("v1 被改动: %+v %v", old, err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run TestEnsureDefault -v`
Expected: FAIL（方法未定义）

- [ ] **Step 3: 实现 workflows.go**

```go
// Workflow 聚合：不可变版本化的状态机形状。只插新版本、永不 UPDATE
// 旧行——钉版本的卡随时能取回当时的形状，这是审计链的前提。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PutWorkflow 写入 name 的下一个版本并返回版本号。def 原样存 JSON。
func (s *Store) PutWorkflow(name string, def WorkflowDef) (int, error) {
	raw, err := json.Marshal(def)
	if err != nil {
		return 0, fmt.Errorf("编码工作流定义: %w", err)
	}
	var ver int
	err = s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		row := tx.QueryRow(s.q(`SELECT COALESCE(MAX(version),0) FROM workflows WHERE name = ?`), name)
		if err := row.Scan(&ver); err != nil {
			return fmt.Errorf("查最大版本: %w", err)
		}
		ver++
		_, err := tx.Exec(s.q(`INSERT INTO workflows (name, version, definition, created_at) VALUES (?,?,?,?)`),
			name, ver, string(raw), s.tval(time.Now()))
		if err != nil {
			return fmt.Errorf("写工作流 %s v%d: %w", name, ver, err)
		}
		return nil
	})
	return ver, err
}

// GetWorkflow 取指定版本；version==0 取最新版。找不到返回 ErrNotFound。
func (s *Store) GetWorkflow(name string, version int) (Workflow, error) {
	q := `SELECT name, version, definition, created_at FROM workflows WHERE name = ?`
	args := []any{name}
	if version > 0 {
		q += ` AND version = ?`
		args = append(args, version)
	}
	q += ` ORDER BY version DESC LIMIT 1`
	row := s.db.QueryRow(s.q(q), args...)
	var w Workflow
	var raw string
	var ct any
	if err := row.Scan(&w.Name, &w.Version, &raw, &ct); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Workflow{}, fmt.Errorf("工作流 %s v%d: %w", name, version, ErrNotFound)
		}
		return Workflow{}, fmt.Errorf("读工作流: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &w.Def); err != nil {
		return Workflow{}, fmt.Errorf("解码工作流定义: %w", err)
	}
	w.CreatedAt = toTime(ct)
	return w, nil
}

// EnsureDefaultWorkflows 幂等 seed 出厂工作流：feature 流（带「已出spec」
// 插入状态与两道 gate，对齐 💡→📋→🔨→✅ 生命周期）与 bug 流（无门直流）。
// 已存在同名工作流则不动（不覆盖用户改过的版本）。
func (s *Store) EnsureDefaultWorkflows() error {
	defaults := map[string]WorkflowDef{
		"feature": {
			States: []string{StatusTodo, "已出spec", StatusDoing, StatusReview, "待合并", StatusDone},
			Gates: map[string]Gate{
				"已出spec": {RequireAttachment: "spec"},
				"待合并":  {RequireAcceptance: true},
			},
		},
		"bug": {
			States: []string{StatusTodo, StatusDoing, StatusReview, StatusDone},
		},
	}
	for name, def := range defaults {
		if _, err := s.GetWorkflow(name, 0); err == nil {
			continue // 已存在，不覆盖
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.PutWorkflow(name, def); err != nil {
			return err
		}
		log().Info("seed 默认工作流", "name", name)
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledger/ -run 'TestEnsureDefault|TestPutWorkflow' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ledger/
git commit -m "feat(ledger): workflow 聚合——不可变版本化 + feature/bug 出厂默认与 gate 定义"
```

---

### Task 5: cards.go + events.go 底座——建卡、B 号分配、读卡、事件 append

**Files:**
- Create: `internal/ledger/cards.go`
- Create: `internal/ledger/events.go`（本 Task 只写 appendEvent + EventsFromAsc，其余操作在 Task 8）
- Test: `internal/ledger/cards_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import (
	"strings"
	"testing"
)

// seedStore 建好默认工作流的测试库——建卡类测试共用。
func seedStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s
}

func TestCreateCardAllocatesBNumbers(t *testing.T) {
	s := seedStore(t)
	// 迁移前的新库要先垫号，防与 markdown 总账撞号（spec §2.1 B 号分配）
	if err := s.EnsureMinB(156); err != nil {
		t.Fatalf("EnsureMinB: %v", err)
	}
	c1, err := s.CreateCard(NewCard{Title: "第一张", Project: "handoff", Workflow: "feature", Actor: "test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c1.ID != "B157" {
		t.Fatalf("垫号后首卡应为 B157，得 %s", c1.ID)
	}
	c2, _ := s.CreateCard(NewCard{Title: "第二张", Project: "handoff", Workflow: "bug", Actor: "test"})
	if c2.ID != "B158" {
		t.Fatalf("连续编号: %s", c2.ID)
	}
	// 子卡走点号位
	ch1, err := s.CreateCard(NewCard{Title: "子一", Project: "handoff", Workflow: "feature", Parent: c1.ID, Actor: "test"})
	if err != nil || ch1.ID != "B157.1" {
		t.Fatalf("子卡: %v %s", err, ch1.ID)
	}
	ch2, _ := s.CreateCard(NewCard{Title: "子二", Project: "handoff", Workflow: "feature", Parent: c1.ID, Actor: "test"})
	if ch2.ID != "B157.2" {
		t.Fatalf("子卡连续: %s", ch2.ID)
	}
	// 初始态 = 工作流首态；钉最新版本
	if c1.Status != StatusTodo || c1.WorkflowVersion != 1 {
		t.Fatalf("初始态/版本: %+v", c1)
	}
	// 出生事件落流
	evs, err := s.EventsFromAsc([]string{c1.ID}, 0, 10)
	if err != nil || len(evs) == 0 || evs[0].Type != EvCardCreated {
		t.Fatalf("出生事件: %v %+v", err, evs)
	}
}

func TestCreateCardValidation(t *testing.T) {
	s := seedStore(t)
	if _, err := s.CreateCard(NewCard{Project: "p", Workflow: "feature"}); err == nil {
		t.Fatal("空标题应拒绝")
	}
	if _, err := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "不存在的流"}); err == nil {
		t.Fatal("未知工作流应拒绝")
	}
	if _, err := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Parent: "B999"}); err == nil {
		t.Fatal("父卡不存在应拒绝")
	}
}

func TestUpdateCardAttachAccept(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Actor: "test"})
	if err := s.AttachFile(c.ID, "spec", "specs/x.md", "test"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.SetAcceptance(c.ID, "跑通判据一", "test"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if len(got.Attachments) != 1 || got.Attachments[0].Path != "specs/x.md" {
		t.Fatalf("附件: %+v", got.Attachments)
	}
	if got.AcceptanceCriteria != "跑通判据一" {
		t.Fatalf("判据: %q", got.AcceptanceCriteria)
	}
	// 同 path 重复 attach 幂等（不追加重复项）
	_ = s.AttachFile(c.ID, "spec", "specs/x.md", "test")
	got, _ = s.GetCard(c.ID)
	if len(got.Attachments) != 1 {
		t.Fatalf("attach 不幂等: %+v", got.Attachments)
	}
	if err := s.DetachFile(c.ID, "specs/x.md", "test"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, _ = s.GetCard(c.ID)
	if len(got.Attachments) != 0 {
		t.Fatalf("detach 未生效: %+v", got.Attachments)
	}
}

func TestCloseAndRevive(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "bug", Actor: "test"})
	if err := s.CloseCard(c.ID, "无效理由", "test"); err == nil {
		t.Fatal("非受控 reason 应拒绝")
	}
	if err := s.CloseCard(c.ID, CloseShelved, "test"); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Status != StatusClosed || got.TerminateReason != CloseShelved {
		t.Fatalf("终止态: %+v", got)
	}
	if err := s.ReviveCard(c.ID, "test"); err != nil {
		t.Fatalf("revive: %v", err)
	}
	got, _ = s.GetCard(c.ID)
	if got.Status != StatusTodo || got.TerminateReason != "" {
		t.Fatalf("复活态: %+v", got)
	}
	// 取消/废弃不可复活
	_ = s.CloseCard(c.ID, CloseCancelled, "test")
	if err := s.ReviveCard(c.ID, "test"); err == nil || !strings.Contains(err.Error(), "搁置") {
		t.Fatalf("取消卡不应可复活: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run 'TestCreateCard|TestUpdateCard|TestCloseAnd' -v`
Expected: FAIL（NewCard/CreateCard 等未定义）

- [ ] **Step 3: 实现 events.go 底座**

```go
// 账本事件单流：同事务 append、PG 事务内 pg_notify 推送、游标升序读。
// 追加语义与 internal/store 的 events 表同源：append-only、seq 全局
// 自增（单卡内稀疏）、升序读截断尾部保证游标只越过真正收到的事件。
package ledger

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// appendEvent 在事务内落一条账本事件并（PG）推送通知。cardID 可空 =
// 项目级事件。返回 seq。所有领域操作共用此入口，禁止绕过它裸 INSERT。
func (s *Store) appendEvent(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("编码事件 payload: %w", err)
	}
	var cid any
	if cardID != "" {
		cid = cardID
	}
	var seq int64
	if s.dialect == dialectPG {
		// PG 用 RETURNING 拿 seq，并在同事务内 NOTIFY——提交即送达 LISTEN 端
		err = tx.QueryRow(s.q(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
			VALUES (?,?,?,?,?) RETURNING seq`),
			cid, typ, actor, string(raw), s.tval(time.Now())).Scan(&seq)
		if err == nil {
			_, err = tx.Exec(`SELECT pg_notify('card_events', $1)`, fmt.Sprint(seq))
		}
	} else {
		var res sql.Result
		res, err = tx.Exec(s.q(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
			VALUES (?,?,?,?,?)`),
			cid, typ, actor, string(raw), s.tval(time.Now()))
		if err == nil {
			seq, err = res.LastInsertId()
		}
	}
	if err != nil {
		return 0, fmt.Errorf("落账本事件 %s: %w", typ, err)
	}
	sink.seqs = append(sink.seqs, seq)
	return seq, nil
}

// EventsFromAsc 按 seq 升序读事件。cardIDs 空 = 全流（含项目级）；fromSeq
// 排他；limit<=0 取 1000。升序 + LIMIT 截尾，游标语义与 store.EventsFromAsc
// 一致——绝不能改成降序截头，那会让游标永久跨过缺口。
func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE seq > ?`
	args := []any{fromSeq}
	if len(cardIDs) > 0 {
		q += ` AND card_id IN (?` + strings.Repeat(",?", len(cardIDs)-1) + `)`
		for _, id := range cardIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY seq ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(s.q(q), args...)
	if err != nil {
		return nil, fmt.Errorf("读账本事件流: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var cid, st, stask sql.NullString
		var sseq sql.NullInt64
		var raw string
		var ct any
		if err := rows.Scan(&e.Seq, &cid, &e.Type, &e.Actor, &raw, &st, &stask, &sseq, &ct); err != nil {
			return nil, fmt.Errorf("扫描事件行: %w", err)
		}
		e.CardID, e.SourceTarget, e.SourceTask, e.SourceSeq = cid.String, st.String, stask.String, sseq.Int64
		e.Payload = json.RawMessage(raw)
		e.CreatedAt = toTime(ct)
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: 实现 cards.go**

```go
// 任务卡的建/读/改/终止/复活与 B 号分配。状态转移在 move.go，
// 关系与合并在 relations.go / merge.go——本文件不碰关系表。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NewCard 建卡请求。Workflow 必填（调用方负责缺省解析，如 CLI 默认
// feature）；Parent 非空则建子卡（点号 id，继承基线由查询期解析）。
type NewCard struct {
	Title, Project, Priority, Parent, Workflow, BaseBranch, Actor string
}

var topIDPat = regexp.MustCompile(`^B(\d+)$`)

// EnsureMinB 垫高 B 号水位（迁移前防与 markdown 总账撞号；只升不降）。
func (s *Store) EnsureMinB(n int) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		cur := 0
		var v string
		err := tx.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = 'min_b'`)).Scan(&v)
		if err == nil {
			cur, _ = strconv.Atoi(v)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("读 min_b: %w", err)
		}
		if n <= cur {
			return nil
		}
		// upsert：两方言都支持 INSERT ... ON CONFLICT
		_, err = tx.Exec(s.q(`INSERT INTO ledger_meta (key, value) VALUES ('min_b', ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`), strconv.Itoa(n))
		if err != nil {
			return fmt.Errorf("写 min_b: %w", err)
		}
		return nil
	})
}

// nextTopID 事务内分配下一个顶层 B 号：max(现存顶层号, min_b) + 1。
// 号永不复用（终止/归档的卡仍占号）。在 mutate 的全局写锁内调用，
// 无并发分配竞态。
func (s *Store) nextTopID(tx *sql.Tx) (string, error) {
	maxN := 0
	rows, err := tx.Query(`SELECT id FROM cards WHERE parent_id IS NULL`)
	if err != nil {
		return "", fmt.Errorf("扫现存 B 号: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		if m := topIDPat.FindStringSubmatch(id); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > maxN {
				maxN = n
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	var v string
	if err := tx.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = 'min_b'`)).Scan(&v); err == nil {
		if n, _ := strconv.Atoi(v); n > maxN {
			maxN = n
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return fmt.Sprintf("B%d", maxN+1), nil
}

// nextChildID 分配 parent 下一个点号子位（B157 → B157.1、B157.2…）。
func (s *Store) nextChildID(tx *sql.Tx, parent string) (string, error) {
	rows, err := tx.Query(s.q(`SELECT id FROM cards WHERE parent_id = ?`), parent)
	if err != nil {
		return "", fmt.Errorf("扫子卡号: %w", err)
	}
	defer rows.Close()
	maxN := 0
	prefix := parent + "."
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		if rest, ok := strings.CutPrefix(id, prefix); ok {
			if n, err := strconv.Atoi(rest); err == nil && n > maxN {
				maxN = n
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%d", parent, maxN+1), nil
}

// CreateCard 建卡：分配 B 号、钉工作流最新版本、初始态 = 工作流首态、
// 落 card_created 事件。
func (s *Store) CreateCard(nc NewCard) (Card, error) {
	if strings.TrimSpace(nc.Title) == "" {
		return Card{}, fmt.Errorf("建卡: 标题不能为空")
	}
	if nc.Project == "" {
		return Card{}, fmt.Errorf("建卡: project 不能为空")
	}
	if nc.Priority == "" {
		nc.Priority = "中"
	}
	wf, err := s.GetWorkflow(nc.Workflow, 0)
	if err != nil {
		return Card{}, fmt.Errorf("建卡取工作流 %q: %w", nc.Workflow, err)
	}
	var c Card
	err = s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var id string
		var perr error
		if nc.Parent != "" {
			if _, gerr := getCardTx(s, tx, nc.Parent); gerr != nil {
				return fmt.Errorf("父卡 %s: %w", nc.Parent, gerr)
			}
			id, perr = s.nextChildID(tx, nc.Parent)
		} else {
			id, perr = s.nextTopID(tx)
		}
		if perr != nil {
			return perr
		}
		now := time.Now()
		var parent any
		if nc.Parent != "" {
			parent = nc.Parent
		}
		var base any
		if nc.BaseBranch != "" {
			base = nc.BaseBranch
		}
		_, err := tx.Exec(s.q(`INSERT INTO cards
			(id, title, status, priority, project, parent_id, workflow_name, workflow_version,
			 attachments, acceptance_criteria, base_branch, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,'[]','',?,?,?)`),
			id, nc.Title, wf.Def.States[0], nc.Priority, nc.Project, parent,
			wf.Name, wf.Version, base, s.tval(now), s.tval(now))
		if err != nil {
			return fmt.Errorf("插入卡 %s: %w", id, err)
		}
		if _, err := s.appendEvent(tx, sink, id, EvCardCreated, nc.Actor,
			map[string]any{"title": nc.Title, "workflow": wf.Name, "workflow_version": wf.Version}); err != nil {
			return err
		}
		c = Card{ID: id, Title: nc.Title, Status: wf.Def.States[0], Priority: nc.Priority,
			Project: nc.Project, ParentID: nc.Parent, WorkflowName: wf.Name,
			WorkflowVersion: wf.Version, BaseBranch: nc.BaseBranch, CreatedAt: now, UpdatedAt: now}
		return nil
	})
	return c, err
}

// cardColumns 与 scanCard 配对——加列只改这两处（照抄 store 的 taskColumns 模式）。
const cardColumns = `id, title, status, terminate_reason, priority, project, parent_id,
	workflow_name, workflow_version, attachments, acceptance_criteria, base_branch,
	driver_session, driver_heartbeat_at, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanCard(r rowScanner) (Card, error) {
	var c Card
	var tr, parent, ac, base, drv sql.NullString
	var att string
	var hb, ct, ut any
	if err := r.Scan(&c.ID, &c.Title, &c.Status, &tr, &c.Priority, &c.Project, &parent,
		&c.WorkflowName, &c.WorkflowVersion, &att, &ac, &base, &drv, &hb, &ct, &ut); err != nil {
		return Card{}, err
	}
	c.TerminateReason, c.ParentID, c.AcceptanceCriteria = tr.String, parent.String, ac.String
	c.BaseBranch, c.DriverSession = base.String, drv.String
	if err := json.Unmarshal([]byte(att), &c.Attachments); err != nil {
		return Card{}, fmt.Errorf("解码附件: %w", err)
	}
	c.DriverHeartbeatAt, c.CreatedAt, c.UpdatedAt = toTime(hb), toTime(ct), toTime(ut)
	return c, nil
}

func getCardTx(s *Store, tx *sql.Tx, id string) (Card, error) {
	c, err := scanCard(tx.QueryRow(s.q(`SELECT `+cardColumns+` FROM cards WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	return c, err
}

// GetCard 读单卡。不存在返回 ErrNotFound。
func (s *Store) GetCard(id string) (Card, error) {
	c, err := scanCard(s.db.QueryRow(s.q(`SELECT `+cardColumns+` FROM cards WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, fmt.Errorf("卡 %s: %w", id, ErrNotFound)
	}
	return c, err
}

// AttachFile 挂附件（同 path 幂等）；落 comment 事件记录动作，附件本体
// 是卡字段不是事件。
func (s *Store) AttachFile(id, kind, path, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		c, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("挂附件: 卡 %s: %w", id, err)
		}
		for _, a := range c.Attachments {
			if a.Path == path {
				return nil // 幂等
			}
		}
		c.Attachments = append(c.Attachments, Attachment{Kind: kind, Path: path})
		raw, _ := json.Marshal(c.Attachments)
		if _, err := tx.Exec(s.q(`UPDATE cards SET attachments = ?, updated_at = ? WHERE id = ?`),
			string(raw), s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写附件: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("挂附件 %s:%s", kind, path)})
		return err
	})
}

// DetachFile 摘附件（按 path 匹配）。
func (s *Store) DetachFile(id, path, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		c, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("摘附件: 卡 %s: %w", id, err)
		}
		kept := c.Attachments[:0]
		for _, a := range c.Attachments {
			if a.Path != path {
				kept = append(kept, a)
			}
		}
		raw, _ := json.Marshal(kept)
		if _, err := tx.Exec(s.q(`UPDATE cards SET attachments = ?, updated_at = ? WHERE id = ?`),
			string(raw), s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写附件: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": "摘附件 " + path})
		return err
	})
}

// SetAcceptance 写验收判据文本（判据是卡字段；验收**结果**走
// RecordAcceptance 落事件，Task 8）。
func (s *Store) SetAcceptance(id, criteria, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		res, err := tx.Exec(s.q(`UPDATE cards SET acceptance_criteria = ?, updated_at = ? WHERE id = ?`),
			criteria, s.tval(time.Now()), id)
		if err != nil {
			return fmt.Errorf("写判据: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("卡 %s: %w", id, ErrNotFound)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": "更新验收判据"})
		return err
	})
}

// CloseCard 终止（从任意非终态；reason 受控词表）。终止不是删除：
// 号仍占用、事件仍在流里、搁置可复活。
func (s *Store) CloseCard(id, reason, actor string) error {
	if reason != CloseCancelled && reason != CloseAbandoned && reason != CloseShelved {
		return fmt.Errorf("终止 reason %q 不在受控词表 {取消,废弃,搁置}", reason)
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		c, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("终止: 卡 %s: %w", id, err)
		}
		if c.Status == StatusClosed || c.Status == StatusDone {
			log().Warn("终止被拒", "card", id, "status", c.Status)
			return fmt.Errorf("卡 %s 已处于 %s: %w", id, c.Status, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET status = ?, terminate_reason = ?, updated_at = ? WHERE id = ?`),
			StatusClosed, reason, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写终止: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvStatusMoved, actor,
			map[string]any{"from": c.Status, "to": StatusClosed, "reason": reason})
		return err
	})
}

// ReviveCard 复活：仅 终止(搁置) → 待办。
func (s *Store) ReviveCard(id, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		c, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("复活: 卡 %s: %w", id, err)
		}
		if c.Status != StatusClosed || c.TerminateReason != CloseShelved {
			log().Warn("复活被拒", "card", id, "status", c.Status, "reason", c.TerminateReason)
			return fmt.Errorf("卡 %s 非 终止(搁置)，不可复活: %w", id, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET status = ?, terminate_reason = NULL, updated_at = ? WHERE id = ?`),
			StatusTodo, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写复活: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvStatusMoved, actor,
			map[string]any{"from": StatusClosed, "to": StatusTodo, "reason": "复活"})
		return err
	})
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/ledger/ -v`
Expected: 本 Task 四个测试 PASS，既有测试不回归

- [ ] **Step 6: 自检日志与注释**（instrumenting-code 清单）

- 拒绝路径（终止被拒/复活被拒）已带 Warn + 卡 id 上下文；错误全部 wrap；
- cards.go / events.go 文件头注释、导出符号 doc 注释齐全；
- B 号分配与 min_b 的「为什么」注释在位。缺则补齐再进下一步。

- [ ] **Step 7: Commit**

```bash
git add internal/ledger/
git commit -m "feat(ledger): 建卡/读卡/附件/判据/终止复活 + B 号分配 + 事件 append 与升序游标读"
```

---

### Task 6: move.go——CAS 状态转移 + workflow gate

**Files:**
- Create: `internal/ledger/move.go`
- Test: `internal/ledger/move_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import (
	"errors"
	"testing"
)

func TestMoveCASAndGate(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Actor: "test"})

	// gate：无 spec 附件进「已出spec」被拒（判据⑬的单测形）
	err := s.MoveCard(c.ID, "已出spec", "", "test")
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("gate 应拒绝: %v", err)
	}
	_ = s.AttachFile(c.ID, "spec", "specs/x.md", "test")
	if err := s.MoveCard(c.ID, "已出spec", "", "test"); err != nil {
		t.Fatalf("挂附件后应放行: %v", err)
	}

	// CAS：expect 与实际不符干净失败
	err = s.MoveCard(c.ID, StatusDoing, StatusTodo, "test")
	if !errors.Is(err, ErrCASConflict) {
		t.Fatalf("CAS 应冲突: %v", err)
	}
	if err := s.MoveCard(c.ID, StatusDoing, "已出spec", "test"); err != nil {
		t.Fatalf("正确前值应成功: %v", err)
	}

	// 未知状态拒绝
	if err := s.MoveCard(c.ID, "不存在的状态", "", "test"); err == nil {
		t.Fatal("未知状态应拒绝")
	}
	// 终态卡不可 move
	_ = s.MoveCard(c.ID, StatusReview, "", "test")
	_ = s.SetAcceptance(c.ID, "判据", "test")
	_ = s.MoveCard(c.ID, "待合并", "", "test")
	_ = s.MoveCard(c.ID, StatusDone, "", "test")
	if err := s.MoveCard(c.ID, StatusTodo, "", "test"); !errors.Is(err, ErrBadState) {
		t.Fatalf("已完成卡 move 应拒: %v", err)
	}

	// 事件审计：status_moved 序列完整
	evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
	moves := 0
	for _, e := range evs {
		if e.Type == EvStatusMoved {
			moves++
		}
	}
	if moves != 5 { // 成功的五次：→已出spec →进行中 →待审阅 →待合并 →已完成
		t.Fatalf("status_moved 事件数 %d != 5", moves)
	}
}

func TestMoveGateAcceptance(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Actor: "test"})
	_ = s.AttachFile(c.ID, "spec", "s.md", "test")
	_ = s.MoveCard(c.ID, "已出spec", "", "test")
	_ = s.MoveCard(c.ID, StatusDoing, "", "test")
	_ = s.MoveCard(c.ID, StatusReview, "", "test")
	// 判据为空进「待合并」被拒（feature 流第二道门）
	if err := s.MoveCard(c.ID, "待合并", "", "test"); !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("缺判据应拒: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run TestMove -v`
Expected: FAIL（MoveCard 未定义）

- [ ] **Step 3: 实现 move.go**

```go
// CAS 状态转移 + workflow gate。一期不限制转移方向（人工回退是真实
// 需求），只做四重校验：目标状态在钉住版本的 States 内、当前非终态、
// CAS 前值、gate 条件。
package ledger

import (
	"database/sql"
	"fmt"
	"time"
)

// MoveCard 状态转移。expect 空 = 以事务内读到的当前值为前值（交互场景）；
// 非空 = 显式 CAS（脚本场景钉死前值）。冲突返回 ErrCASConflict，
// gate 不过返回 ErrGateBlocked（错误文案指明缺什么）。
func (s *Store) MoveCard(id, to, expect, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		c, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("转移: 卡 %s: %w", id, err)
		}
		if c.Status == StatusDone || c.Status == StatusClosed {
			log().Warn("转移被拒：终态卡", "card", id, "status", c.Status)
			return fmt.Errorf("卡 %s 已处于终态 %s: %w", id, c.Status, ErrBadState)
		}
		if expect != "" && c.Status != expect {
			log().Warn("转移被拒：CAS 冲突", "card", id, "expect", expect, "actual", c.Status)
			return fmt.Errorf("卡 %s 当前是 %q 非 %q: %w", id, c.Status, expect, ErrCASConflict)
		}
		wf, err := s.getWorkflowTx(tx, c.WorkflowName, c.WorkflowVersion)
		if err != nil {
			return fmt.Errorf("转移取工作流: %w", err)
		}
		found := false
		for _, st := range wf.Def.States {
			if st == to {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("状态 %q 不在工作流 %s v%d 中", to, wf.Name, wf.Version)
		}
		if g, ok := wf.Def.Gates[to]; ok {
			if g.RequireAttachment != "" {
				has := false
				for _, a := range c.Attachments {
					if a.Kind == g.RequireAttachment {
						has = true
						break
					}
				}
				if !has {
					log().Warn("转移被拒：gate 缺附件", "card", id, "to", to, "need", g.RequireAttachment)
					return fmt.Errorf("进 %q 需要 %s 附件（当前没有）: %w", to, g.RequireAttachment, ErrGateBlocked)
				}
			}
			if g.RequireAcceptance && c.AcceptanceCriteria == "" {
				log().Warn("转移被拒：gate 缺判据", "card", id, "to", to)
				return fmt.Errorf("进 %q 需要验收判据非空: %w", to, ErrGateBlocked)
			}
		}
		// CAS 写：前值进 WHERE，被并发抢先则 0 行（照抄 store.UpdateTaskState 模式）
		res, err := tx.Exec(s.q(`UPDATE cards SET status = ?, updated_at = ? WHERE id = ? AND status = ?`),
			to, s.tval(time.Now()), id, c.Status)
		if err != nil {
			return fmt.Errorf("写转移: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("卡 %s 状态已被并发修改: %w", id, ErrCASConflict)
		}
		_, err = s.appendEvent(tx, sink, id, EvStatusMoved, actor,
			map[string]any{"from": c.Status, "to": to})
		return err
	})
}

// getWorkflowTx 事务内取指定版本工作流（Move 的 gate 判定必须与写同事务）。
func (s *Store) getWorkflowTx(tx *sql.Tx, name string, version int) (Workflow, error) {
	row := tx.QueryRow(s.q(`SELECT name, version, definition, created_at FROM workflows
		WHERE name = ? AND version = ?`), name, version)
	var w Workflow
	var raw string
	var ct any
	if err := row.Scan(&w.Name, &w.Version, &raw, &ct); err != nil {
		return Workflow{}, fmt.Errorf("工作流 %s v%d: %w", name, version, ErrNotFound)
	}
	if err := jsonUnmarshal(raw, &w.Def); err != nil {
		return Workflow{}, err
	}
	w.CreatedAt = toTime(ct)
	return w, nil
}
```

在 `workflows.go` 追加小 helper（GetWorkflow 复用之）：

```go
// jsonUnmarshal 统一 JSON 解码错误措辞。
func jsonUnmarshal(raw string, v any) error {
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("解码 JSON 定义: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledger/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ledger/
git commit -m "feat(ledger): MoveCard——CAS 前值 + workflow gate 双校验，拒因落 Warn 与错误文案"
```

---

### Task 7: relations.go——类型化关系边、环检测、基线继承

**Files:**
- Create: `internal/ledger/relations.go`
- Test: `internal/ledger/relations_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import (
	"errors"
	"testing"
)

func mk(t *testing.T, s *Store, title string) Card {
	t.Helper()
	c, err := s.CreateCard(NewCard{Title: title, Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("mk: %v", err)
	}
	return c
}

func TestBlocksCycleDetection(t *testing.T) {
	s := seedStore(t)
	a, b, c := mk(t, s, "a"), mk(t, s, "b"), mk(t, s, "c")
	// a blocks b, b blocks c 合法
	if err := s.AddBlocks(a.ID, b.ID, "test"); err != nil {
		t.Fatalf("ab: %v", err)
	}
	if err := s.AddBlocks(b.ID, c.ID, "test"); err != nil {
		t.Fatalf("bc: %v", err)
	}
	// c blocks a 成环，拒绝
	if err := s.AddBlocks(c.ID, a.ID, "test"); !errors.Is(err, ErrCycle) {
		t.Fatalf("应报环: %v", err)
	}
	// 自阻塞拒绝
	if err := s.AddBlocks(a.ID, a.ID, "test"); err == nil {
		t.Fatal("自阻塞应拒")
	}
	// 阻塞自己的祖先/后代拒绝（parent 树与 blocks 混合成环的具体解释）
	child, _ := s.CreateCard(NewCard{Title: "child", Project: "p", Workflow: "bug", Parent: a.ID, Actor: "test"})
	if err := s.AddBlocks(child.ID, a.ID, "test"); !errors.Is(err, ErrCycle) {
		t.Fatalf("子阻塞父应拒: %v", err)
	}
	// 重复加边幂等报错（主键冲突转干净错误）
	if err := s.AddBlocks(a.ID, b.ID, "test"); err == nil {
		t.Fatal("重复边应报错")
	}
	// 解除
	if err := s.RemoveRelation(a.ID, b.ID, RelBlocks); err != nil {
		t.Fatalf("unlink: %v", err)
	}
}

func TestAddRelationTypes(t *testing.T) {
	s := seedStore(t)
	a, b := mk(t, s, "a"), mk(t, s, "b")
	if err := s.AddRelation(a.ID, b.ID, RelDiscoveredFrom, "test"); err != nil {
		t.Fatalf("discovered_from: %v", err)
	}
	// merged_into 禁止直建（必须走 MergeCards 的校验）
	if err := s.AddRelation(a.ID, b.ID, RelMergedInto, "test"); err == nil {
		t.Fatal("merged_into 直建应拒")
	}
	// 未知类型拒绝
	if err := s.AddRelation(a.ID, b.ID, "xx", "test"); err == nil {
		t.Fatal("未知类型应拒")
	}
	rels, err := s.RelationsOf(a.ID)
	if err != nil || len(rels) != 1 || rels[0].Type != RelDiscoveredFrom {
		t.Fatalf("RelationsOf: %v %+v", err, rels)
	}
}

func TestEffectiveBaseBranch(t *testing.T) {
	s := seedStore(t)
	epic, _ := s.CreateCard(NewCard{Title: "epic", Project: "p", Workflow: "feature",
		BaseBranch: "desktop-shell", Actor: "test"})
	child, _ := s.CreateCard(NewCard{Title: "c", Project: "p", Workflow: "feature",
		Parent: epic.ID, Actor: "test"})
	grand, _ := s.CreateCard(NewCard{Title: "g", Project: "p", Workflow: "feature",
		Parent: child.ID, Actor: "test"})
	top, _ := s.CreateCard(NewCard{Title: "hotfix", Project: "p", Workflow: "bug", Actor: "test"})

	for _, tc := range []struct{ id, want string }{
		{epic.ID, "desktop-shell"},
		{child.ID, "desktop-shell"}, // 一级继承
		{grand.ID, "desktop-shell"}, // 跨级继承
		{top.ID, ""},                // 顶层无设置 = 空（主线）
	} {
		got, err := s.EffectiveBaseBranch(tc.id)
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %q want %q err %v", tc.id, got, tc.want, err)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run 'TestBlocks|TestAddRelation|TestEffective' -v`
Expected: FAIL

- [ ] **Step 3: 实现 relations.go**

```go
// 类型化关系边。方向语义钉死在此文件：
//   blocks:          from 阻塞 to（to 被 from 阻塞）
//   merged_into:     from 并入 to（to 是承载卡）——只许经 merge.go 写
//   discovered_from: from 发现自 to
//   split_from:      from 拆分自 to
//   relates:         无向关联（存单向行，查询双向）
// 环检测只对 blocks 生效（spec §2）；「parent 树与 blocks 混合成环」的
// 具体解释 = 禁止阻塞自己的祖先或后代 + blocks 图内禁有向环。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var validRelTypes = map[string]bool{
	RelBlocks: true, RelDiscoveredFrom: true, RelSplitFrom: true, RelRelates: true,
	// RelMergedInto 故意不在此表——必须走 MergeCards
}

// AddBlocks 加阻塞边：blocker 阻塞 blocked。事务内做环检测。
func (s *Store) AddBlocks(blocker, blocked, actor string) error {
	if blocker == blocked {
		return fmt.Errorf("不能自阻塞: %w", ErrCycle)
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		for _, id := range []string{blocker, blocked} {
			if _, err := getCardTx(s, tx, id); err != nil {
				return fmt.Errorf("阻塞边: 卡 %s: %w", id, err)
			}
		}
		// 祖先/后代互斥：blocked 的祖先链含 blocker，或 blocker 的祖先链含
		// blocked，都视为 parent 树参与的环
		for _, pair := range [][2]string{{blocker, blocked}, {blocked, blocker}} {
			anc, err := s.ancestorsTx(tx, pair[0])
			if err != nil {
				return err
			}
			if anc[pair[1]] {
				log().Warn("阻塞边被拒：跨父子", "blocker", blocker, "blocked", blocked)
				return fmt.Errorf("%s 与 %s 是祖先/后代关系: %w", blocker, blocked, ErrCycle)
			}
		}
		// blocks 图有向环：加边后从 blocked 出发（沿「X 阻塞 Y」的 X→Y 方向）
		// 能回到 blocker 即成环。图读写同事务 + 全局写锁 = 判定与写入原子。
		edges, err := s.blocksEdgesTx(tx)
		if err != nil {
			return err
		}
		edges[blocker] = append(edges[blocker], blocked)
		seen := map[string]bool{}
		var dfs func(string) bool
		dfs = func(n string) bool {
			if n == blocker {
				return true
			}
			if seen[n] {
				return false
			}
			seen[n] = true
			for _, next := range edges[n] {
				if dfs(next) {
					return true
				}
			}
			return false
		}
		if dfs(blocked) {
			log().Warn("阻塞边被拒：成环", "blocker", blocker, "blocked", blocked)
			return fmt.Errorf("%s→%s 使阻塞图成环: %w", blocker, blocked, ErrCycle)
		}
		if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
			VALUES (?,?,?,?)`), blocker, blocked, RelBlocks, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写阻塞边（可能已存在）: %w", err)
		}
		_, err = s.appendEvent(tx, sink, blocked, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("被 %s 阻塞", blocker), "refs": []string{blocker}})
		return err
	})
}

// AddRelation 加非阻塞、非合并的关系边（discovered_from/split_from/relates）。
func (s *Store) AddRelation(from, to, typ, actor string) error {
	if typ == RelMergedInto {
		return fmt.Errorf("merged_into 必须经 MergeCards 建立（要做基线与链式校验）")
	}
	if typ == RelBlocks {
		return s.AddBlocks(from, to, actor)
	}
	if !validRelTypes[typ] {
		return fmt.Errorf("未知关系类型 %q", typ)
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		for _, id := range []string{from, to} {
			if _, err := getCardTx(s, tx, id); err != nil {
				return fmt.Errorf("关系边: 卡 %s: %w", id, err)
			}
		}
		if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
			VALUES (?,?,?,?)`), from, to, typ, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写关系边: %w", err)
		}
		_, err := s.appendEvent(tx, sink, from, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("关系 %s → %s", typ, to), "refs": []string{to}})
		return err
	})
}

// RemoveRelation 删关系边。merged_into 请走 UnmergeCard。
func (s *Store) RemoveRelation(from, to, typ string) error {
	if typ == RelMergedInto {
		return fmt.Errorf("解除并入请走 unmerge")
	}
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`DELETE FROM card_relations WHERE from_id = ? AND to_id = ? AND type = ?`),
			from, to, typ)
		if err != nil {
			return fmt.Errorf("删关系边: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("关系 %s-%s-%s: %w", from, typ, to, ErrNotFound)
		}
		return nil
	})
}

// RelationsOf 双向取一张卡的全部关系边（from 或 to 命中皆返回）。
func (s *Store) RelationsOf(id string) ([]Relation, error) {
	rows, err := s.db.Query(s.q(`SELECT from_id, to_id, type, created_at FROM card_relations
		WHERE from_id = ? OR to_id = ? ORDER BY created_at`), id, id)
	if err != nil {
		return nil, fmt.Errorf("读关系边: %w", err)
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		var r Relation
		var ct any
		if err := rows.Scan(&r.From, &r.To, &r.Type, &ct); err != nil {
			return nil, err
		}
		r.CreatedAt = toTime(ct)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ancestorsTx 取卡的祖先集合（parent 链，含防御性深度上限）。
func (s *Store) ancestorsTx(tx *sql.Tx, id string) (map[string]bool, error) {
	anc := map[string]bool{}
	cur := id
	for i := 0; i < 64; i++ { // B 号树实际深度 ≤2，64 是防坏数据死循环
		var p sql.NullString
		err := tx.QueryRow(s.q(`SELECT parent_id FROM cards WHERE id = ?`), cur).Scan(&p)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return anc, nil
			}
			return nil, fmt.Errorf("读父链: %w", err)
		}
		if !p.Valid || p.String == "" {
			return anc, nil
		}
		anc[p.String] = true
		cur = p.String
	}
	return anc, fmt.Errorf("父链深度超限（数据疑似成环）: %s", id)
}

// blocksEdgesTx 事务内读全部阻塞边成邻接表（from 阻塞 → to 列表）。
func (s *Store) blocksEdgesTx(tx *sql.Tx) (map[string][]string, error) {
	rows, err := tx.Query(s.q(`SELECT from_id, to_id FROM card_relations WHERE type = ?`), RelBlocks)
	if err != nil {
		return nil, fmt.Errorf("读阻塞图: %w", err)
	}
	defer rows.Close()
	edges := map[string][]string{}
	for rows.Next() {
		var f, t string
		if err := rows.Scan(&f, &t); err != nil {
			return nil, err
		}
		edges[f] = append(edges[f], t)
	}
	return edges, rows.Err()
}

// EffectiveBaseBranch 解析卡的有效基线分支：自身非空即自身；否则沿
// parent 链向上取最近的显式设置；全空返回 ""（= 项目默认主线，由
// 调用方在派发时解析为具体分支名——库不猜 main/master）。
func (s *Store) EffectiveBaseBranch(id string) (string, error) {
	var out string
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		var e error
		out, e = s.effectiveBaseTx(tx, id)
		return e
	})
	return out, err
}

func (s *Store) effectiveBaseTx(tx *sql.Tx, id string) (string, error) {
	cur := id
	for i := 0; i < 64; i++ {
		var base, parent sql.NullString
		err := tx.QueryRow(s.q(`SELECT base_branch, parent_id FROM cards WHERE id = ?`), cur).Scan(&base, &parent)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("卡 %s: %w", cur, ErrNotFound)
			}
			return "", fmt.Errorf("读基线: %w", err)
		}
		if base.Valid && strings.TrimSpace(base.String) != "" {
			return base.String, nil
		}
		if !parent.Valid || parent.String == "" {
			return "", nil
		}
		cur = parent.String
	}
	return "", fmt.Errorf("父链深度超限: %s", id)
}
```

- [ ] **Step 4: 跑测试确认通过 + Commit**

Run: `go test ./internal/ledger/ -v` → 全 PASS

```bash
git add internal/ledger/
git commit -m "feat(ledger): 关系边——blocks 环检测（含父子互斥）、类型词表、基线分支继承解析"
```

---

### Task 8: merge.go + events.go 补全——合并/拆回/拆分、评论、验收、等人

**Files:**
- Create: `internal/ledger/merge.go`
- Modify: `internal/ledger/events.go`（追加 AddComment/RecordAcceptance/MarkNeedsHuman/ClearNeedsHuman/Subtree）
- Test: `internal/ledger/merge_test.go`
- Test: `internal/ledger/events_test.go`

- [ ] **Step 1: 写失败测试 merge_test.go**

```go
package ledger

import (
	"errors"
	"testing"
)

func TestMergeUnmergeSplit(t *testing.T) {
	s := seedStore(t)
	carrier := mk(t, s, "承载卡")
	m1, m2, m3 := mk(t, s, "m1"), mk(t, s, "m2"), mk(t, s, "m3")
	_ = s.SetAcceptance(m1.ID, "m1 判据", "test")

	if err := s.MergeCards([]string{m1.ID, m2.ID, m3.ID}, carrier.ID, "test"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// 跟随派生：Following 指向承载卡
	views, _ := s.ListCards(CardFilter{Project: "p", IncludeTerminal: true})
	byID := map[string]CardView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	if byID[m1.ID].Following != carrier.ID {
		t.Fatalf("m1 应跟随 %s: %+v", carrier.ID, byID[m1.ID])
	}
	// 被并卡验收判据无损
	got, _ := s.GetCard(m1.ID)
	if got.AcceptanceCriteria != "m1 判据" {
		t.Fatalf("判据被吞: %q", got.AcceptanceCriteria)
	}
	// 链式拒绝：承载卡再并入别人 / 已并卡再当承载卡
	x := mk(t, s, "x")
	if err := s.MergeCards([]string{carrier.ID}, x.ID, "test"); !errors.Is(err, ErrBadMerge) {
		t.Fatalf("承载卡被并应拒: %v", err)
	}
	if err := s.MergeCards([]string{x.ID}, m1.ID, "test"); !errors.Is(err, ErrBadMerge) {
		t.Fatalf("被并卡承载应拒: %v", err)
	}
	// 重复并入拒绝
	if err := s.MergeCards([]string{m1.ID}, carrier.ID, "test"); !errors.Is(err, ErrBadMerge) {
		t.Fatalf("重复并入应拒: %v", err)
	}
	// 拆回：恢复自主 + 判据仍在（判据⑫的单测形）
	if err := s.UnmergeCard(m1.ID, "test"); err != nil {
		t.Fatalf("unmerge: %v", err)
	}
	views, _ = s.ListCards(CardFilter{Project: "p", IncludeTerminal: true})
	for _, v := range views {
		if v.ID == m1.ID && v.Following != "" {
			t.Fatalf("拆回后仍跟随: %+v", v)
		}
	}
	got, _ = s.GetCard(m1.ID)
	if got.AcceptanceCriteria != "m1 判据" {
		t.Fatalf("拆回后判据丢失")
	}
}

func TestMergeCrossBaseRejected(t *testing.T) {
	s := seedStore(t)
	a, _ := s.CreateCard(NewCard{Title: "a", Project: "p", Workflow: "bug",
		BaseBranch: "desktop-shell", Actor: "test"})
	b := mk(t, s, "b") // 基线 = 主线
	err := s.MergeCards([]string{a.ID}, b.ID, "test")
	if !errors.Is(err, ErrBadMerge) {
		t.Fatalf("跨基线应拒: %v", err)
	}
}

func TestSplitCard(t *testing.T) {
	s := seedStore(t)
	parent := mk(t, s, "大卡")
	child, err := s.SplitCard(parent.ID, "拆出的子项", "test")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if child.ParentID != parent.ID || child.ID != parent.ID+".1" {
		t.Fatalf("子卡形态: %+v", child)
	}
	// split_from 边自动挂
	rels, _ := s.RelationsOf(child.ID)
	found := false
	for _, r := range rels {
		if r.Type == RelSplitFrom && r.From == child.ID && r.To == parent.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 split_from 边: %+v", rels)
	}
}
```

- [ ] **Step 2: 写失败测试 events_test.go**

```go
package ledger

import (
	"encoding/json"
	"testing"
)

func TestCommentRefsAutoRelate(t *testing.T) {
	s := seedStore(t)
	a, b := mk(t, s, "a"), mk(t, s, "b")
	ev, err := s.AddComment(a.ID, "这个问题与 #"+b.ID+" 同源", "普通", "test")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	var p struct {
		Body string   `json:"body"`
		Refs []string `json:"refs"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if len(p.Refs) != 1 || p.Refs[0] != b.ID {
		t.Fatalf("refs 解析: %+v", p)
	}
	// 引用自动落 relates 边
	rels, _ := s.RelationsOf(a.ID)
	found := false
	for _, r := range rels {
		if r.Type == RelRelates && r.To == b.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 relates 边: %+v", rels)
	}
	// 引用不存在的卡：评论照发、边不建、不报错（评论是记录不是校验）
	if _, err := s.AddComment(a.ID, "见 #B9999", "普通", "test"); err != nil {
		t.Fatalf("幽灵引用不应报错: %v", err)
	}
	// 更正类评论
	ev2, _ := s.AddComment(a.ID, "上一条口径不对", "更正", "test")
	_ = json.Unmarshal(ev2.Payload, &p)
	if p.Body == "" {
		t.Fatal("更正评论 body 丢失")
	}
}

func TestAcceptanceAndNeeds(t *testing.T) {
	s := seedStore(t)
	a := mk(t, s, "a")
	if err := s.RecordAcceptance(a.ID, true, "真机跑通 go test", "test"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := s.MarkNeedsHuman(a.ID, "审阅超轮", "test"); err != nil {
		t.Fatalf("needs: %v", err)
	}
	views, _ := s.ListCards(CardFilter{Project: "p"})
	var av CardView
	for _, v := range views {
		if v.ID == a.ID {
			av = v
		}
	}
	if av.NeedsReason != "审阅超轮" {
		t.Fatalf("等人派生: %+v", av)
	}
	if err := s.ClearNeedsHuman(a.ID, "test"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	views, _ = s.ListCards(CardFilter{Project: "p"})
	for _, v := range views {
		if v.ID == a.ID && v.NeedsReason != "" {
			t.Fatalf("等人未清除: %+v", v)
		}
	}
}

func TestSubtree(t *testing.T) {
	s := seedStore(t)
	root := mk(t, s, "root")
	c1, _ := s.SplitCard(root.ID, "c1", "test")
	_, _ = s.SplitCard(c1.ID, "cc", "test")
	m := mk(t, s, "m")
	_ = s.MergeCards([]string{m.ID}, root.ID, "test")
	ids, err := s.Subtree(root.ID)
	if err != nil {
		t.Fatalf("subtree: %v", err)
	}
	// 根 + 两级后代 + 并入成员 = 4
	if len(ids) != 4 {
		t.Fatalf("子树成员 %v", ids)
	}
}
```

（注意 `ListCards` 到 Task 9 才实现——本 Task 先让 merge/events 测试中不依赖 ListCards 的断言通过；两个测试文件里依赖 `ListCards` 的用例在 Task 9 结束时必须全绿。执行顺序上允许 Task 8 结束时 `TestMergeUnmergeSplit`/`TestAcceptanceAndNeeds` 因 ListCards 未定义而编译失败——**为避免这种半红状态，Task 8 与 Task 9 合并为一次 commit 序列，见 Task 9 Step 5。**）

- [ ] **Step 3: 实现 merge.go**

```go
// 合并/拆回/拆分：账本域一等操作。合并 = 关系不是销毁——被并卡的
// 判据、事件、B 号全部保留，拆回只删一条边。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MergeCards 把 ids 并入承载卡 into。校验（任一不过全拒，ErrBadMerge）：
// 卡都存在且非终态；不含 into 自身；无重复并入；无链式（into 自身未被并
// 入、ids 里没有正在承载别人的卡）；全体有效基线一致。
func (s *Store) MergeCards(ids []string, into, actor string) error {
	if len(ids) == 0 {
		return fmt.Errorf("合并: 成员为空")
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, into); err != nil {
			return fmt.Errorf("合并: 承载卡 %s: %w", into, err)
		}
		if n, err := s.mergedIntoTx(tx, into); err != nil {
			return err
		} else if n != "" {
			log().Warn("合并被拒：链式", "into", into, "its_carrier", n)
			return fmt.Errorf("承载卡 %s 自身已并入 %s，不许链式: %w", into, n, ErrBadMerge)
		}
		intoBase, err := s.effectiveBaseTx(tx, into)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id == into {
				return fmt.Errorf("卡 %s 不能并入自己: %w", id, ErrBadMerge)
			}
			if _, err := getCardTx(s, tx, id); err != nil {
				return fmt.Errorf("合并: 成员 %s: %w", id, err)
			}
			if cur, err := s.mergedIntoTx(tx, id); err != nil {
				return err
			} else if cur != "" {
				log().Warn("合并被拒：重复并入", "card", id, "already", cur)
				return fmt.Errorf("卡 %s 已并入 %s: %w", id, cur, ErrBadMerge)
			}
			var cnt int
			if err := tx.QueryRow(s.q(`SELECT COUNT(*) FROM card_relations WHERE to_id = ? AND type = ?`),
				id, RelMergedInto).Scan(&cnt); err != nil {
				return fmt.Errorf("查承载: %w", err)
			}
			if cnt > 0 {
				log().Warn("合并被拒：成员在承载别人", "card", id)
				return fmt.Errorf("卡 %s 正承载着别的卡，不许链式: %w", id, ErrBadMerge)
			}
			base, err := s.effectiveBaseTx(tx, id)
			if err != nil {
				return err
			}
			if base != intoBase {
				log().Warn("合并被拒：跨基线", "card", id, "base", base, "into_base", intoBase)
				return fmt.Errorf("卡 %s 基线 %q ≠ 承载卡基线 %q: %w", id, base, intoBase, ErrBadMerge)
			}
		}
		for _, id := range ids {
			if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
				VALUES (?,?,?,?)`), id, into, RelMergedInto, s.tval(time.Now())); err != nil {
				return fmt.Errorf("写并入边 %s: %w", id, err)
			}
		}
		_, err = s.appendEvent(tx, sink, into, EvMerged, actor, map[string]any{"members": ids})
		return err
	})
}

// UnmergeCard 拆回：删 merged_into 边，恢复自主流转。判据/事件无损
// （它们从未被动过）。
func (s *Store) UnmergeCard(id, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		carrier, err := s.mergedIntoTx(tx, id)
		if err != nil {
			return err
		}
		if carrier == "" {
			return fmt.Errorf("卡 %s 未并入任何卡: %w", id, ErrNotFound)
		}
		if _, err := tx.Exec(s.q(`DELETE FROM card_relations WHERE from_id = ? AND type = ?`),
			id, RelMergedInto); err != nil {
			return fmt.Errorf("删并入边: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvUnmerged, actor, map[string]any{"from_carrier": carrier})
		return err
	})
}

// SplitCard 拆子卡：建子卡（点号 id、同工作流、同 project）+ split_from 边。
func (s *Store) SplitCard(parent, title, actor string) (Card, error) {
	p, err := s.GetCard(parent)
	if err != nil {
		return Card{}, fmt.Errorf("拆分: 父卡 %s: %w", parent, err)
	}
	child, err := s.CreateCard(NewCard{Title: title, Project: p.Project,
		Workflow: p.WorkflowName, Parent: parent, Actor: actor})
	if err != nil {
		return Card{}, err
	}
	err = s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
			VALUES (?,?,?,?)`), child.ID, parent, RelSplitFrom, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写 split_from: %w", err)
		}
		_, err := s.appendEvent(tx, sink, parent, EvSplit, actor,
			map[string]any{"child": child.ID, "title": title})
		return err
	})
	return child, err
}

// mergedIntoTx 查卡当前并入的承载卡（"" = 未并入）。也承担 SQLite 侧
// 「一卡至多一条 merged_into」的应用层校验职责（PG 有 partial unique
// index 兜底，SQLite 靠 MergeCards 的先查后插 + 全局写锁保证）。
func (s *Store) mergedIntoTx(tx *sql.Tx, id string) (string, error) {
	var to string
	err := tx.QueryRow(s.q(`SELECT to_id FROM card_relations WHERE from_id = ? AND type = ?`),
		id, RelMergedInto).Scan(&to)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("查并入: %w", err)
	}
	return to, nil
}
```

- [ ] **Step 4: 在 events.go 追加领域操作**

```go
// ---- 以下为建立在事件流上的领域操作 ----

var cardRefPat = regexp.MustCompile(`#(B\d+(?:\.\d+)*)`)

// AddComment 发评论。body 里的 #B 号引用解析出来：存在的卡自动建
// relates 边（幂等），不存在的只留在 refs 里（评论是记录不是校验）。
// kind ∈ {普通, 更正}——「更正」承接 markdown 总账的变更痕迹文化。
func (s *Store) AddComment(cardID, body, kind, actor string) (Event, error) {
	if kind != "普通" && kind != "更正" {
		return Event{}, fmt.Errorf("评论 kind %q 不在 {普通,更正}", kind)
	}
	var out Event
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("评论: 卡 %s: %w", cardID, err)
		}
		var refs []string
		for _, m := range cardRefPat.FindAllStringSubmatch(body, -1) {
			ref := m[1]
			if ref == cardID {
				continue
			}
			refs = append(refs, ref)
			if _, err := getCardTx(s, tx, ref); err != nil {
				continue // 幽灵引用：不建边不报错
			}
			// 幂等建边：已存在同主键则忽略（两方言都支持 ON CONFLICT DO NOTHING）
			if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
				VALUES (?,?,?,?) ON CONFLICT (from_id, to_id, type) DO NOTHING`),
				cardID, ref, RelRelates, s.tval(time.Now())); err != nil {
				return fmt.Errorf("评论引用建边 %s: %w", ref, err)
			}
		}
		seq, err := s.appendEvent(tx, sink, cardID, EvComment, actor,
			map[string]any{"kind": kind, "body": body, "refs": refs})
		if err != nil {
			return err
		}
		out = Event{Seq: seq, CardID: cardID, Type: EvComment, Actor: actor}
		raw, _ := json.Marshal(map[string]any{"kind": kind, "body": body, "refs": refs})
		out.Payload = raw
		return nil
	})
	return out, err
}

// RecordAcceptance 落验收结果事件（verified=true 表示真机已验）。
// 判据文本在卡字段；结果是事件——「已验/待真机验」从最后一条
// acceptance_recorded 推导。
func (s *Store) RecordAcceptance(cardID string, verified bool, evidence, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("验收: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvAcceptanceRecorded, actor,
			map[string]any{"verified_on_real_machine": verified, "evidence": evidence})
		return err
	})
}

// MarkNeedsHuman 打等人标记（reason 必填）；ClearNeedsHuman 清除。
// 等人不落列，从最后一条 needs_human/needs_cleared 事件推导（spec §2）。
func (s *Store) MarkNeedsHuman(cardID, reason, actor string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("等人标记必须带 reason")
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("等人: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvNeedsHuman, actor, map[string]any{"reason": reason})
		return err
	})
}

// ClearNeedsHuman 清除等人标记。
func (s *Store) ClearNeedsHuman(cardID, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("清等人: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvNeedsCleared, actor, map[string]any{})
		return err
	})
}

// Subtree 返回卡树成员 id 集：root + 全部后代（parent 链）+ 并入成员
// （merged_into 指向集内任一成员的卡）。多路 wait 与看板 rollup 共用。
func (s *Store) Subtree(rootID string) ([]string, error) {
	if _, err := s.GetCard(rootID); err != nil {
		return nil, err
	}
	set := map[string]bool{rootID: true}
	frontier := []string{rootID}
	for len(frontier) > 0 {
		q := `SELECT id FROM cards WHERE parent_id IN (?` + strings.Repeat(",?", len(frontier)-1) + `)`
		args := make([]any, len(frontier))
		for i, f := range frontier {
			args[i] = f
		}
		rows, err := s.db.Query(s.q(q), args...)
		if err != nil {
			return nil, fmt.Errorf("读子树: %w", err)
		}
		var next []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if !set[id] {
				set[id] = true
				next = append(next, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		frontier = next
	}
	// 并入成员：merged_into 指向集内任何成员的卡也算子树
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	q := `SELECT from_id FROM card_relations WHERE type = 'merged_into' AND to_id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(s.q(q), args...)
	if err != nil {
		return nil, fmt.Errorf("读并入成员: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, rows.Err()
}
```

（events.go 的 import 块需补 `"regexp"`、`"sort"`、`"strings"`、`"encoding/json"`——以编译器为准补齐。）

- [ ] **Step 5: 编译 + 不依赖 ListCards 的测试先行**

Run: `go build ./internal/ledger/`
Expected: 编译通过（merge_test/events_test 里引用 ListCards 的用例此刻编译不过——**立即进入 Task 9**，两个 Task 一起转绿后统一 commit；这是计划内的唯一一处跨 Task 编译依赖）

---

### Task 9: derived.go——ListCards 派生标记 + 全量转绿

**Files:**
- Create: `internal/ledger/derived.go`
- Test: `internal/ledger/derived_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import "testing"

func TestListCardsDerivedBlocked(t *testing.T) {
	s := seedStore(t)
	blocker, blocked := mk(t, s, "blocker"), mk(t, s, "blocked")
	_ = s.AddBlocks(blocker.ID, blocked.ID, "test")

	views, err := s.ListCards(CardFilter{Project: "p"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	m := map[string]CardView{}
	for _, v := range views {
		m[v.ID] = v
	}
	if !m[blocked.ID].Blocked || len(m[blocked.ID].BlockedBy) != 1 {
		t.Fatalf("blocked 派生: %+v", m[blocked.ID])
	}
	// blocker 完成 → 解除
	_ = s.MoveCard(blocker.ID, StatusDoing, "", "test")
	_ = s.MoveCard(blocker.ID, StatusReview, "", "test")
	_ = s.MoveCard(blocker.ID, StatusDone, "", "test")
	views, _ = s.ListCards(CardFilter{Project: "p"})
	for _, v := range views {
		if v.ID == blocked.ID && v.Blocked {
			t.Fatalf("blocker 完成后仍 blocked: %+v", v)
		}
	}
}

func TestBlockerTerminatedNeedsHuman(t *testing.T) {
	s := seedStore(t)
	blocker, blocked := mk(t, s, "blocker"), mk(t, s, "blocked")
	_ = s.AddBlocks(blocker.ID, blocked.ID, "test")
	_ = s.CloseCard(blocker.ID, CloseCancelled, "test")
	// 判据③的单测形：blocker 终止不解锁，下游得等人
	views, _ := s.ListCards(CardFilter{Project: "p"})
	for _, v := range views {
		if v.ID == blocked.ID {
			if !v.Blocked {
				t.Fatalf("blocker 终止不应解锁: %+v", v)
			}
			if v.NeedsReason == "" {
				t.Fatalf("blocker 终止应派生等人: %+v", v)
			}
		}
	}
}

func TestListCardsFilters(t *testing.T) {
	s := seedStore(t)
	a := mk(t, s, "a")
	b, _ := s.CreateCard(NewCard{Title: "b", Project: "p", Workflow: "bug",
		BaseBranch: "desktop-shell", Actor: "test"})
	_ = s.CloseCard(a.ID, CloseAbandoned, "test")

	// 默认排除终态
	views, _ := s.ListCards(CardFilter{Project: "p"})
	for _, v := range views {
		if v.ID == a.ID {
			t.Fatal("默认应排除终止卡")
		}
	}
	// IncludeTerminal 包含
	views, _ = s.ListCards(CardFilter{Project: "p", IncludeTerminal: true})
	found := false
	for _, v := range views {
		if v.ID == a.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("IncludeTerminal 应包含终止卡")
	}
	// 基线过滤
	views, _ = s.ListCards(CardFilter{Project: "p", BaseBranch: "desktop-shell"})
	if len(views) != 1 || views[0].ID != b.ID {
		t.Fatalf("基线过滤: %+v", views)
	}
	// Needs 过滤（open 裁决也算——裁决在 Task 10，先用 MarkNeedsHuman）
	_ = s.MarkNeedsHuman(b.ID, "试一下", "test")
	views, _ = s.ListCards(CardFilter{Project: "p", Needs: true})
	if len(views) != 1 || views[0].ID != b.ID {
		t.Fatalf("needs 过滤: %+v", views)
	}
}
```

- [ ] **Step 2: 实现 derived.go**

```go
// 查询期派生标记：blocked / 跟随 / 等人。不落列（spec §2）——账面永远
// 从边表 + 事件流现算，不存在「派生列忘更新」这类说谎方式。实现取三次
// 全量小表 + 内存组装：卡量级在数百张，正确性与可读性优先，慢了再谈索引。
package ledger

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// ListCards 过滤 + 派生。排序：待人处理的在前（needs/blocked），其余按
// id 升序——CLI 领活与看板共用此序。
func (s *Store) ListCards(f CardFilter) ([]CardView, error) {
	q := `SELECT ` + cardColumns + ` FROM cards WHERE 1=1`
	var args []any
	if f.Project != "" {
		q += ` AND project = ?`
		args = append(args, f.Project)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, f.Status)
	}
	if !f.IncludeTerminal {
		q += ` AND status NOT IN (?, ?)`
		args = append(args, StatusDone, StatusClosed)
	}
	rows, err := s.db.Query(s.q(q), args...)
	if err != nil {
		return nil, fmt.Errorf("列卡: %w", err)
	}
	defer rows.Close()
	var cards []Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, fmt.Errorf("扫卡行: %w", err)
		}
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rels, err := s.allRelations()
	if err != nil {
		return nil, err
	}
	statusOf, err := s.allStatuses()
	if err != nil {
		return nil, err
	}
	needs, err := s.needsMap()
	if err != nil {
		return nil, err
	}
	openDec, err := s.openDecisionCount()
	if err != nil {
		return nil, err
	}

	var out []CardView
	for _, c := range cards {
		v := CardView{Card: c, OpenDecisions: openDec[c.ID], NeedsReason: needs[c.ID]}
		for _, r := range rels {
			switch {
			case r.Type == RelMergedInto && r.From == c.ID:
				v.Following = r.To
			case r.Type == RelBlocks && r.To == c.ID:
				// blocker 到「已完成」才解除；终止不解除且派生等人（判据③）
				st := statusOf[r.From]
				if st != StatusDone {
					v.Blocked = true
					v.BlockedBy = append(v.BlockedBy, r.From)
					if st == StatusClosed && v.NeedsReason == "" {
						v.NeedsReason = "前置 " + r.From + " 已终止"
					}
				}
			}
		}
		if f.BaseBranch != "" {
			eff, err := s.EffectiveBaseBranch(c.ID)
			if err != nil {
				return nil, err
			}
			if eff != f.BaseBranch {
				continue
			}
		}
		if f.Blocked && !v.Blocked {
			continue
		}
		if f.Needs && v.NeedsReason == "" && v.OpenDecisions == 0 {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		pi := out[i].NeedsReason != "" || out[i].Blocked
		pj := out[j].NeedsReason != "" || out[j].Blocked
		if pi != pj {
			return pi
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *Store) allRelations() ([]Relation, error) {
	rows, err := s.db.Query(`SELECT from_id, to_id, type FROM card_relations`)
	if err != nil {
		return nil, fmt.Errorf("读边表: %w", err)
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		var r Relation
		if err := rows.Scan(&r.From, &r.To, &r.Type); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) allStatuses() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id, status FROM cards`)
	if err != nil {
		return nil, fmt.Errorf("读状态表: %w", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, err
		}
		m[id] = st
	}
	return m, rows.Err()
}

// needsMap 每卡最后一条 needs_human/needs_cleared 决定当前等人态。
// 单卡最多几十条事件、卡数百张，直接扫两类事件按 seq 归并即可。
func (s *Store) needsMap() (map[string]string, error) {
	rows, err := s.db.Query(s.q(`SELECT card_id, type, payload FROM card_events
		WHERE type IN (?, ?) AND card_id IS NOT NULL ORDER BY seq ASC`), EvNeedsHuman, EvNeedsCleared)
	if err != nil {
		return nil, fmt.Errorf("读等人事件: %w", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var cid, typ, payload string
		if err := rows.Scan(&cid, &typ, &payload); err != nil {
			return nil, err
		}
		if typ == EvNeedsCleared {
			delete(m, cid)
			continue
		}
		var p struct {
			Reason string `json:"reason"`
		}
		_ = jsonUnmarshal(payload, &p)
		m[cid] = p.Reason
	}
	return m, rows.Err()
}

// openDecisionCount 每卡 open 裁决数（decisions 表在 Task 10 前恒空，
// 查询天然返回空 map，不需要桩）。
func (s *Store) openDecisionCount() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT card_id, COUNT(*) FROM decisions
		WHERE status = 'open' AND card_id IS NOT NULL GROUP BY card_id`)
	if err != nil {
		return nil, fmt.Errorf("读裁决计数: %w", err)
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var cid sql.NullString
		var n int
		if err := rows.Scan(&cid, &n); err != nil {
			return nil, err
		}
		if cid.Valid {
			m[cid.String] = n
		}
	}
	return m, rows.Err()
}

var _ = strings.TrimSpace // 占位防未用 import（若最终未用 strings 则删除本行与 import）
```

（最后一行是给执行者的提示：以编译器为准清理 import，别留占位。）

- [ ] **Step 3: 全量跑测试**

Run: `go test ./internal/ledger/ -v`
Expected: Task 8 + Task 9 全部测试 PASS（含 TestMergeUnmergeSplit / TestAcceptanceAndNeeds 中依赖 ListCards 的断言）

- [ ] **Step 4: 自检日志与注释**

合并四处拒绝路径都有 Warn + 上下文；derived.go 文件头说明「为什么不落列」「为什么全量扫可接受」；缺则补。

- [ ] **Step 5: Commit（Task 8+9 一起）**

```bash
git add internal/ledger/
git commit -m "feat(ledger): 合并/拆回/拆分 + 评论引用建边 + 验收/等人事件 + ListCards 派生标记（blocked/跟随/等人）"
```

---

### Task 10: decisions.go——裁决项

**Files:**
- Create: `internal/ledger/decisions.go`
- Test: `internal/ledger/decisions_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import (
	"errors"
	"testing"
)

func TestDecisionLifecycle(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "有请示的卡")

	d, err := s.OpenDecision(c.ID, "合并顺序按 done 还是按依赖？", []string{"done 时序", "依赖序"}, "main-session")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if d.ID == 0 || d.Status != "open" {
		t.Fatalf("open 返回: %+v", d)
	}
	// 项目级裁决（card_id 空）
	pd, err := s.OpenDecision("", "推不推汇流线？", nil, "main-session")
	if err != nil {
		t.Fatalf("open project-level: %v", err)
	}

	// open 裁决使卡进「需要你」面（派生联动）
	views, _ := s.ListCards(CardFilter{Project: "p", Needs: true})
	if len(views) != 1 || views[0].ID != c.ID || views[0].OpenDecisions != 1 {
		t.Fatalf("needs 联动: %+v", views)
	}

	// 答复
	if err := s.AnswerDecision(d.ID, "done 时序", "user"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	// 重复答复拒绝
	if err := s.AnswerDecision(d.ID, "再答", "user"); !errors.Is(err, ErrBadState) {
		t.Fatalf("重复答复应拒: %v", err)
	}
	// 不存在的裁决
	if err := s.AnswerDecision(99999, "x", "user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("幽灵裁决: %v", err)
	}

	// list --open 只剩项目级那条
	open, err := s.ListDecisions(true)
	if err != nil || len(open) != 1 || open[0].ID != pd.ID {
		t.Fatalf("open 列表: %v %+v", err, open)
	}
	// 全量列表两条，且答复字段完整
	all, _ := s.ListDecisions(false)
	if len(all) != 2 {
		t.Fatalf("全量: %+v", all)
	}
	for _, x := range all {
		if x.ID == d.ID && (x.Answer != "done 时序" || x.AnsweredBy != "user" || x.AnsweredAt.IsZero()) {
			t.Fatalf("答复字段: %+v", x)
		}
	}

	// 事件流：decision_opened ×2 + decision_answered ×1（项目级 card_id 空也在流里）
	evs, _ := s.EventsFromAsc(nil, 0, 100)
	opened, answered := 0, 0
	for _, e := range evs {
		switch e.Type {
		case EvDecisionOpened:
			opened++
		case EvDecisionAnswered:
			answered++
		}
	}
	if opened != 2 || answered != 1 {
		t.Fatalf("裁决事件: opened=%d answered=%d", opened, answered)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run TestDecision -v`
Expected: FAIL（OpenDecision 未定义）

- [ ] **Step 3: 实现 decisions.go**

```go
// 裁决项（Decision）：主会话回合末落的结构化请示。开/答均落事件流；
// open 裁决是「需要你」面的一等数据源（derived.go 联动）。一期答复消费 =
// 会话唤醒后 ListDecisions 查账；自动唤醒留三期。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// OpenDecision 开一条裁决。cardID 空 = 项目级请示；options 可空 = 开放问答。
func (s *Store) OpenDecision(cardID, body string, options []string, createdBy string) (Decision, error) {
	if body == "" {
		return Decision{}, fmt.Errorf("裁决 body 不能为空")
	}
	var d Decision
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if cardID != "" {
			if _, err := getCardTx(s, tx, cardID); err != nil {
				return fmt.Errorf("裁决: 卡 %s: %w", cardID, err)
			}
		}
		var opts any
		if len(options) > 0 {
			raw, _ := json.Marshal(options)
			opts = string(raw)
		}
		var cid any
		if cardID != "" {
			cid = cardID
		}
		now := time.Now()
		var id int64
		if s.dialect == dialectPG {
			err := tx.QueryRow(s.q(`INSERT INTO decisions (card_id, body, options, status, created_by, created_at)
				VALUES (?,?,?,'open',?,?) RETURNING id`), cid, body, opts, createdBy, s.tval(now)).Scan(&id)
			if err != nil {
				return fmt.Errorf("写裁决: %w", err)
			}
		} else {
			res, err := tx.Exec(s.q(`INSERT INTO decisions (card_id, body, options, status, created_by, created_at)
				VALUES (?,?,?,'open',?,?)`), cid, body, opts, createdBy, s.tval(now))
			if err != nil {
				return fmt.Errorf("写裁决: %w", err)
			}
			id, _ = res.LastInsertId()
		}
		if _, err := s.appendEvent(tx, sink, cardID, EvDecisionOpened, createdBy,
			map[string]any{"decision_id": id, "body": body, "options": options}); err != nil {
			return err
		}
		d = Decision{ID: id, CardID: cardID, Body: body, Options: options,
			Status: "open", CreatedBy: createdBy, CreatedAt: now}
		return nil
	})
	return d, err
}

// AnswerDecision 答复。已答复的拒绝（ErrBadState）——答案是账，不许改写；
// 要改口径开新裁决。
func (s *Store) AnswerDecision(id int64, answer, answeredBy string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var status string
		var cid sql.NullString
		err := tx.QueryRow(s.q(`SELECT status, card_id FROM decisions WHERE id = ?`), id).Scan(&status, &cid)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("裁决 %d: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("读裁决: %w", err)
		}
		if status != "open" {
			log().Warn("答复被拒：已答复", "decision", id)
			return fmt.Errorf("裁决 %d 已答复: %w", id, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`UPDATE decisions SET status = 'answered', answer = ?, answered_by = ?, answered_at = ?
			WHERE id = ?`), answer, answeredBy, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写答复: %w", err)
		}
		_, err = s.appendEvent(tx, sink, cid.String, EvDecisionAnswered, answeredBy,
			map[string]any{"decision_id": id, "answer": answer})
		return err
	})
}

// ListDecisions openOnly=true 只列未答复（全局裁决收件箱）；false 全量按
// 创建时间升序。
func (s *Store) ListDecisions(openOnly bool) ([]Decision, error) {
	q := `SELECT id, card_id, body, options, status, created_by, answer, answered_by, created_at, answered_at
		FROM decisions`
	if openOnly {
		q += ` WHERE status = 'open'`
	}
	q += ` ORDER BY id ASC`
	rows, err := s.db.Query(s.q(q))
	if err != nil {
		return nil, fmt.Errorf("列裁决: %w", err)
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var d Decision
		var cid, opts, ans, ansBy sql.NullString
		var ct, at any
		if err := rows.Scan(&d.ID, &cid, &d.Body, &opts, &d.Status, &d.CreatedBy, &ans, &ansBy, &ct, &at); err != nil {
			return nil, fmt.Errorf("扫裁决行: %w", err)
		}
		d.CardID, d.Answer, d.AnsweredBy = cid.String, ans.String, ansBy.String
		if opts.Valid && opts.String != "" {
			if err := jsonUnmarshal(opts.String, &d.Options); err != nil {
				return nil, err
			}
		}
		d.CreatedAt, d.AnsweredAt = toTime(ct), toTime(at)
		out = append(out, d)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: 跑测试确认通过 + Commit**

Run: `go test ./internal/ledger/ -v` → 全 PASS

```bash
git add internal/ledger/
git commit -m "feat(ledger): 裁决项——open/answer/list，开闭落事件流，open 裁决联动需要你派生"
```

---

### Task 11: tasks.go——card_tasks 弱引用 + driver lease

**Files:**
- Create: `internal/ledger/tasks.go`
- Test: `internal/ledger/tasks_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import (
	"errors"
	"testing"
	"time"
)

func TestLinkTask(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "卡")
	if err := s.LinkTask(c.ID, "mac-02", "T1234", "implement", "test"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// 同 (target, task) 重复挂账拒绝——一个 task 至多挂一张卡（主键约束转干净错误）
	c2 := mk(t, s, "另一张")
	if err := s.LinkTask(c2.ID, "mac-02", "T1234", "review", "test"); err == nil {
		t.Fatal("重复挂账应拒")
	}
	links, err := s.TasksOf(c.ID)
	if err != nil || len(links) != 1 || links[0].TaskID != "T1234" || links[0].Purpose != "implement" {
		t.Fatalf("TasksOf: %v %+v", err, links)
	}
	// 反查：task → 卡
	cardID, err := s.CardOfTask("mac-02", "T1234")
	if err != nil || cardID != c.ID {
		t.Fatalf("CardOfTask: %v %q", err, cardID)
	}
	if _, err := s.CardOfTask("mac-02", "无此任务"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("幽灵 task: %v", err)
	}
}

func TestDriverLease(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "卡")
	if err := s.ClaimDriver(c.ID, "session-A"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// 心跳未过期时他人抢占失败
	if err := s.ClaimDriver(c.ID, "session-B"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("未过期应拒抢: %v", err)
	}
	// 同会话续心跳
	if err := s.HeartbeatDriver(c.ID, "session-A"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	// 过期后可抢（把心跳改老模拟过期，driverLeaseTTL 是包常量）
	old := time.Now().Add(-2 * driverLeaseTTL)
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ?`),
		s.tval(old), c.ID); err != nil {
		t.Fatalf("做旧: %v", err)
	}
	if err := s.ClaimDriver(c.ID, "session-B"); err != nil {
		t.Fatalf("过期后抢占: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.DriverSession != "session-B" {
		t.Fatalf("驱动会话: %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run 'TestLinkTask|TestDriverLease' -v`
Expected: FAIL

- [ ] **Step 3: 实现 tasks.go**

```go
// card_tasks 弱引用（账本 → 执行域的唯一通道）与卡的 driver lease。
// 弱引用无外键校验 task 真实存在——执行域在别的机器的 SQLite 里，
// 账本只记指针；指针悬空由镜像/看板 join 时显性化，不在写入时拦。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// driverLeaseTTL 驱动会话心跳有效期。超过即视为无主，可被抢占；
// 看板「无驱动会话」告警也以此为准。
const driverLeaseTTL = 5 * time.Minute

// TaskLink card_tasks 一行。
type TaskLink struct {
	CardID, Target, TaskID, Purpose string
	CreatedAt                       time.Time
}

// LinkTask 把 (target, task) 挂到卡上。purpose ∈ {implement, review, merge}
// 语义由调用方保证（词表不在库层锁死——三期可能扩）。落 comment 事件。
func (s *Store) LinkTask(cardID, target, taskID, purpose, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("挂账: 卡 %s: %w", cardID, err)
		}
		if _, err := tx.Exec(s.q(`INSERT INTO card_tasks (card_id, target, task_id, purpose, created_at)
			VALUES (?,?,?,?,?)`), cardID, target, taskID, purpose, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写挂账（task 可能已挂在别的卡）: %w", err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("挂账 task %s@%s（%s）", taskID, target, purpose)})
		return err
	})
}

// TasksOf 列一张卡挂的全部 task。
func (s *Store) TasksOf(cardID string) ([]TaskLink, error) {
	rows, err := s.db.Query(s.q(`SELECT card_id, target, task_id, purpose, created_at
		FROM card_tasks WHERE card_id = ? ORDER BY created_at`), cardID)
	if err != nil {
		return nil, fmt.Errorf("读挂账: %w", err)
	}
	defer rows.Close()
	var out []TaskLink
	for rows.Next() {
		var l TaskLink
		var ct any
		if err := rows.Scan(&l.CardID, &l.Target, &l.TaskID, &l.Purpose, &ct); err != nil {
			return nil, err
		}
		l.CreatedAt = toTime(ct)
		out = append(out, l)
	}
	return out, rows.Err()
}

// CardOfTask 反查 task 挂在哪张卡（镜像写入路径的热查询）。
func (s *Store) CardOfTask(target, taskID string) (string, error) {
	var cid string
	err := s.db.QueryRow(s.q(`SELECT card_id FROM card_tasks WHERE target = ? AND task_id = ?`),
		target, taskID).Scan(&cid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("task %s@%s 未挂账: %w", taskID, target, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("反查挂账: %w", err)
	}
	return cid, nil
}

// ClaimDriver 认领驱动权：现驱动为空、为己、或心跳过期才可得；
// 否则 ErrCASConflict（提示对方会话标识）。
func (s *Store) ClaimDriver(cardID, session string) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		c, err := getCardTx(s, tx, cardID)
		if err != nil {
			return fmt.Errorf("认领驱动: 卡 %s: %w", cardID, err)
		}
		expired := c.DriverHeartbeatAt.IsZero() || time.Since(c.DriverHeartbeatAt) > driverLeaseTTL
		if c.DriverSession != "" && c.DriverSession != session && !expired {
			log().Warn("驱动认领被拒", "card", cardID, "holder", c.DriverSession, "claimer", session)
			return fmt.Errorf("卡 %s 正由 %s 驱动: %w", cardID, c.DriverSession, ErrCASConflict)
		}
		_, err = tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_heartbeat_at = ? WHERE id = ?`),
			session, s.tval(time.Now()), cardID)
		if err != nil {
			return fmt.Errorf("写驱动: %w", err)
		}
		return nil
	})
}

// HeartbeatDriver 续心跳（仅现持有者可续）。
func (s *Store) HeartbeatDriver(cardID, session string) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ? AND driver_session = ?`),
			s.tval(time.Now()), cardID, session)
		if err != nil {
			return fmt.Errorf("续心跳: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("卡 %s 的驱动不是 %s: %w", cardID, session, ErrCASConflict)
		}
		return nil
	})
}
```

- [ ] **Step 4: 跑测试确认通过 + Commit**

Run: `go test ./internal/ledger/ -v` → 全 PASS

```bash
git add internal/ledger/
git commit -m "feat(ledger): card_tasks 弱引用（挂账/反查）+ driver lease（认领/心跳/过期抢占）"
```

---

### Task 12: PG 冒烟扩展 + 整包终审

**Files:**
- Modify: `internal/ledger/store_pg_test.go`

- [ ] **Step 1: 扩展 PG 冒烟为迷你端到端**

在 `store_pg_test.go` 追加：

```go
// TestPGSmokeEndToEnd 在真 PG 上过一遍核心链路：seed→建卡→gate→CAS→
// 合并→裁决→事件序完整。SQLite 全量测试覆盖逻辑，这里只验方言差异
// （$N 占位、RETURNING、JSONB、partial index、pg_notify 不报错）。
func TestPGSmokeEndToEnd(t *testing.T) {
	s := newPGStore(t)
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.EnsureMinB(9000); err != nil { // 高位垫号，避免与库内已有数据撞
		t.Fatalf("minb: %v", err)
	}
	c, err := s.CreateCard(NewCard{Title: "pg 冒烟", Project: "smoke", Workflow: "feature", Actor: "pgtest"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MoveCard(c.ID, "已出spec", "", "pgtest"); err == nil {
		t.Fatal("gate 在 PG 上应同样拒绝")
	}
	_ = s.AttachFile(c.ID, "spec", "s.md", "pgtest")
	if err := s.MoveCard(c.ID, "已出spec", "", "pgtest"); err != nil {
		t.Fatalf("gate 放行: %v", err)
	}
	m, _ := s.CreateCard(NewCard{Title: "成员", Project: "smoke", Workflow: "bug", Actor: "pgtest"})
	if err := s.MergeCards([]string{m.ID}, c.ID, "pgtest"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	d, err := s.OpenDecision(c.ID, "冒烟请示", []string{"a", "b"}, "pgtest")
	if err != nil {
		t.Fatalf("decision: %v", err)
	}
	if err := s.AnswerDecision(d.ID, "a", "pgtest"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	evs, err := s.EventsFromAsc([]string{c.ID, m.ID}, 0, 100)
	if err != nil || len(evs) < 5 {
		t.Fatalf("事件序: %v n=%d", err, len(evs))
	}
	// 清理冒烟数据，PG 库可能是长命共享库
	for _, tbl := range []string{"card_events", "card_relations", "decisions", "cards"} {
		if _, err := s.db.Exec(`DELETE FROM ` + tbl + ` WHERE TRUE`); err != nil {
			t.Logf("清理 %s: %v", tbl, err)
		}
	}
}
```

（清理段假设冒烟用**专用测试库**；若 DSN 指向共享库，执行者不得跑本测试——在测试文件头注释写明这一前提。）

- [ ] **Step 2: 整包终审**

```bash
gofmt -l internal/ledger/ internal/config/     # 无输出
go vet ./internal/ledger/ ./internal/config/   # 无输出
go test ./... 2>&1 | tail -20                  # 全绿（全仓不回归）
```

Expected: gofmt 无输出（executor 的 ledger 会漏 gofmt——这一步必跑，是审核纪律里的实测坑）；vet 无输出；全仓测试绿。

- [ ] **Step 3: 对照 spec §2/§2.1 逐条自检**

核对以下每条都有实现 + 测试，缺任何一条回去补：
B 号连续编含子卡点号位（Task 5）；无类型身份只有阶段与附件（Task 5）；
5 种关系类型 + blocks 环检测 + merged_into 单跳（Task 7/8）；合并/拆回判据
无损 + 跨基线拒绝（Task 8）；验收结构化事件（Task 8）；终止 reason 词表 +
搁置复活（Task 5）；card_tasks 弱引用无 FK（Task 11）；card_events 单流 +
镜像幂等索引建表（Task 3，写入路径归 Plan B）；workflows/dispatch_templates
不可变版本化建表 + workflow 操作（Task 4；模板操作归 Plan C）；decisions
开答列（Task 10）；正交标记不落列查询推导（Task 9）；PG/SQLite 双方言 +
NOTIFY/进程内推送（Task 3）。

- [ ] **Step 4: Commit**

```bash
git add internal/ledger/
git commit -m "test(ledger): PG 冒烟端到端 + 整包 gofmt/vet 终审"
```

---

## Self-Review 记录（写 plan 时已做）

1. **Spec 覆盖**：§2 的全部结构性决策映射到 Task 3–11（见 Task 12 Step 3 清单）。**不在本 plan 的**：镜像写入/lease 仲裁（Plan B）、dispatch_templates 领域操作与 dispatch/回合计数（Plan C）、CLI/HTTP 面（Plan A2）、看板（Plan D）、迁移（Plan E）——表已建齐，操作按依赖归属后续 plan。
2. **类型一致性**：`eventSink`/`appendEvent`/`getCardTx`/`effectiveBaseTx`/`jsonUnmarshal` 的定义 Task 与全部使用 Task 已互核；`rowScanner` 只在 cards.go 定义一次。
3. **已知妥协（执行者不要"修复"它们）**：mutate 全局粗锁（正确性优先，写 QPS 极小）；ListCards 全量小表扫（卡数百张量级）；SQLite 的 merged_into 唯一性靠应用层（写路径全在 MergeCards + 全局锁内）；`EffectiveBaseBranch` 返回空串表主线（库不猜 main/master，派发方解析）。
4. **测试环境**：全部测试 SQLite 即可跑；PG 冒烟需 `LEDGER_TEST_PG_DSN` 指向**专用测试库**，默认 skip——远端执行机上没有 PG 也不会红。

## 验收归属提醒（审核者读）

一期 spec §9 的真机判据 ①–⑭ 绝大部分依赖后续 plan（镜像/CLI/看板）；本 plan 交付后可在单测层面确认的是 ③（blocker 终止）⑫（合并拆回）⑬（gate）⑭（跨基线拒绝）的库层形。PG 双协调机判据（⑩）待 Plan B。派发本 plan 时按纪律块 B/A 版执行；**PG 冒烟测试不派发**（远端无库），审核者本地跑。

