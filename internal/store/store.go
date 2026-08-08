// Package store 是 handoff 的唯一持久化入口，基于 SQLite（modernc.org/sqlite，纯 Go 无 cgo）。
//
// 职责：
//   - 提供任务（tasks）、事件（events）、工单（tickets）三张表的建表与增删改查
//   - 通过 database/sql 连接池支撑单进程多 goroutine 并发访问（WAL + busy_timeout 防 SQLITE_BUSY）
//   - CreateTicket 用 INSERT OR IGNORE 实现按 id 幂等创建
//
// 边界：
//   - 不含业务规则，仅保留 UpdateTaskState 对 proto.CanTransit 的一处防护性校验
//   - 事件派发、状态变更决策、工单审批逻辑均不在此层
//   - 叶子层：方法错误 return 前不打日志（避免双份），由调用方带上下文记录；
//     仅 Open 成功打 Info、UpdateTaskState 非法迁移打 Warn 两个高价值点例外
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"

	"github.com/xushixin/handoff/internal/proto"
)

// log 是本包日志入口，运行时取 slog.Default()（与 config 包一致）。
// agentd 在 bootstrap 时 logx.Setup + slog.SetDefault(...)，本包日志即跟随统一格式与级别。
// 为什么不用包级 var 捕获：包级 var 在 package init 时求值，晚于其执行的 slog.SetDefault
// 不会生效；运行时求值才能保证「agentd 先 Setup 后 SetDefault」的接线真正接管本包日志。
func log() *slog.Logger { return slog.Default() }

// ErrNotFound 表示按 id 查询的记录不存在。
var ErrNotFound = errors.New("记录不存在")

// ErrBadTransit 表示任务状态迁移违反状态机（见 proto.CanTransit）。
var ErrBadTransit = errors.New("非法状态迁移")

// Store 持有 SQLite 数据库连接池，是 store 包对外唯一入口。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）path 处的 SQLite 数据库并建表。
//
// 参数：
//   - path: 数据库文件路径；父目录需已存在
//
// 返回：
//   - 可用的 Store；打开或建表失败时返回错误
//
// 注意：
//   - driver 为 modernc.org/sqlite（纯 Go 实现，无需 cgo）
//   - 每次调用幂等：建表均带 IF NOT EXISTS
func Open(path string) (*Store, error) {
	// 为什么追加 pragma：agentd 是单进程多 goroutine 并发写，
	// 若不加 WAL 与 busy_timeout，并发写会直接返回 SQLITE_BUSY；
	// WAL 允许读写并行，busy_timeout(5000) 让冲突写等待 5s 再失败。
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 数据库 %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 SQLite 数据库 %s: %w", path, err)
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY, target TEXT NOT NULL DEFAULT '', repo_path TEXT NOT NULL,
  branch TEXT NOT NULL DEFAULT '', plan_path TEXT NOT NULL DEFAULT '',
  plan_summary TEXT NOT NULL DEFAULT '', executor_session TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, type TEXT NOT NULL,
  payload TEXT NOT NULL, created_at TIMESTAMP NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_events_task ON events(task_id, seq)`,
		`CREATE TABLE IF NOT EXISTS tickets (
  id TEXT PRIMARY KEY, task_id TEXT NOT NULL, kind TEXT NOT NULL, request TEXT NOT NULL,
  answer TEXT, created_at TIMESTAMP NOT NULL, answered_at TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(context.Background(), ddl); err != nil {
			db.Close()
			return nil, fmt.Errorf("建表失败: %w", err)
		}
	}
	log().Info("SQLite 存储已打开", "path", path)
	return &Store{db: db}, nil
}

// Close 关闭数据库连接池。
//
// 注意：
//   - 关闭后 Store 不可再使用；sql.DB.Close 幂等，重复调用返回 nil
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateTask 写入一个新任务。
//
// 参数：
//   - t: 任务数据；ID 必须非空且唯一，重复 ID 返回底层主键冲突错误
//
// 注意：
//   - 状态迁移合法性由 UpdateTaskState 校验，此处仅原样入库，不含业务规则
func (s *Store) CreateTask(t *proto.Task) error {
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO tasks (id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Target, t.RepoPath, t.Branch, t.PlanPath, t.PlanSummary,
		t.ExecutorSession, t.State, fmtTime(t.CreatedAt), fmtTime(t.UpdatedAt))
	if err != nil {
		return fmt.Errorf("写入任务 %s: %w", t.ID, err)
	}
	return nil
}

// GetTask 按 id 读取任务；不存在返回 ErrNotFound。
//
// 参数：
//   - id: 任务 ID
//
// 返回：
//   - 任务数据；不存在时返回 ErrNotFound
func (s *Store) GetTask(id string) (*proto.Task, error) {
	var (
		task      proto.Task
		createdAt string
		updatedAt string
	)
	err := s.db.QueryRowContext(context.Background(), `
SELECT id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at
FROM tasks WHERE id = ?`, id).
		Scan(&task.ID, &task.Target, &task.RepoPath, &task.Branch, &task.PlanPath,
			&task.PlanSummary, &task.ExecutorSession, &task.State, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取任务 %s: %w", id, err)
	}
	task.CreatedAt = parseTime(createdAt)
	task.UpdatedAt = parseTime(updatedAt)
	return &task, nil
}

// ListTasks 返回全部任务，按 created_at 降序（最新在前）。
//
// 注意：
//   - created_at 统一为 UTC RFC3339Nano 文本，字典序即时间序，可直接排序
func (s *Store) ListTasks() ([]proto.Task, error) {
	rows, err := s.db.QueryContext(context.Background(), `
SELECT id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at
FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	defer rows.Close()
	var tasks []proto.Task
	for rows.Next() {
		var (
			task      proto.Task
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(&task.ID, &task.Target, &task.RepoPath, &task.Branch, &task.PlanPath,
			&task.PlanSummary, &task.ExecutorSession, &task.State, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("读取任务行: %w", err)
		}
		task.CreatedAt = parseTime(createdAt)
		task.UpdatedAt = parseTime(updatedAt)
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务列表: %w", err)
	}
	return tasks, nil
}

// UpdateTaskState 将任务状态迁移到 st，迁移合法性由 proto.CanTransit 校验。
//
// 参数：
//   - id: 任务 ID；不存在返回 ErrNotFound
//   - st: 目标状态
//
// 返回：
//   - 非法迁移返回 ErrBadTransit；任务不存在返回 ErrNotFound
//
// 注意：
//   - 非法迁移是本包唯一打 Warn 日志的点：排障时可直接定位谁想从哪迁到哪被拒
//   - 写回采用 CAS（WHERE state = 读到的旧状态）：若并发写者先变更了状态，本方法返回
//     ErrBadTransit，调用方应用最新快照重试意图；不会静默覆盖并发迁移
func (s *Store) UpdateTaskState(id string, st proto.TaskState) error {
	cur, err := s.GetTask(id)
	if err != nil {
		return err
	}
	if !proto.CanTransit(cur.State, st) {
		log().Warn("非法状态迁移被拒绝", "task", id, "from", cur.State, "to", st)
		return ErrBadTransit
	}
	// CAS 守卫：把读到的 cur.State 作为 WHERE 条件参与写回，使"读-校验-写"成为原子比较。
	// 若并发写者已先行变更状态，本语句影响 0 行——说明本次迁移基于过期快照，
	// 直接返回 ErrBadTransit 让调用方用最新快照重试意图，避免 last-writer-wins 静默丢失合法迁移。
	res, err := s.db.ExecContext(context.Background(),
		"UPDATE tasks SET state = ?, updated_at = ? WHERE id = ? AND state = ?",
		st, fmtTime(time.Now()), id, cur.State)
	if err != nil {
		return fmt.Errorf("更新任务 %s 状态: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取更新任务 %s 状态影响行数: %w", id, err)
	}
	if affected == 0 {
		log().Warn("状态迁移被并发变更拒绝", "task", id, "from", cur.State, "to", st)
		return ErrBadTransit
	}
	return nil
}

// allowedTaskFields 是 SetTaskField 可写字段白名单。
// 白名单约束保证字段名永远来自受控集合，杜绝 SQL 列注入与越权写关键列（如 id/state）。
var allowedTaskFields = map[string]bool{
	"branch":           true,
	"executor_session": true,
	"plan_summary":     true,
}

// SetTaskField 更新任务白名单内的单个字段（branch/executor_session/plan_summary）。
//
// 参数：
//   - id: 任务 ID
//   - field: 字段名，仅允许白名单三项
//   - value: 新值
//
// 注意：
//   - 白名单之外（如 id/state）返回错误；任务不存在时不报错（不影响其他行即返回 nil）
func (s *Store) SetTaskField(id, field, value string) error {
	if !allowedTaskFields[field] {
		return fmt.Errorf("字段 %s 不在可更新白名单", field)
	}
	if _, err := s.db.ExecContext(context.Background(),
		"UPDATE tasks SET "+field+" = ?, updated_at = ? WHERE id = ?",
		value, fmtTime(time.Now()), id); err != nil {
		return fmt.Errorf("更新任务 %s 字段 %s: %w", id, field, err)
	}
	return nil
}

// AppendEvent 为任务追加一条事件，返回落库后带自增 seq 的完整事件。
//
// 参数：
//   - taskID: 所属任务 ID（与任务表无外键约束，任务不存在时也可追加）
//   - typ: 事件类型
//   - payload: 任意可 JSON 序列化的值，序列化后存入 payload 列
//
// 返回：
//   - 带全局自增 seq 的事件记录
//
// 注意：
//   - seq 由 AUTOINCREMENT 全局分配，跨任务单调递增，可作为全局时间轴排序依据
func (s *Store) AppendEvent(taskID string, typ proto.EventType, payload any) (proto.Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return proto.Event{}, fmt.Errorf("序列化事件 payload: %w", err)
	}
	now := fmtTime(time.Now())
	res, err := s.db.ExecContext(context.Background(),
		"INSERT INTO events (task_id, type, payload, created_at) VALUES (?, ?, ?, ?)",
		taskID, typ, string(b), now)
	if err != nil {
		return proto.Event{}, fmt.Errorf("写入事件: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return proto.Event{}, fmt.Errorf("获取事件 seq: %w", err)
	}
	return proto.Event{Seq: seq, TaskID: taskID, Type: typ, Payload: json.RawMessage(b), CreatedAt: parseTime(now)}, nil
}

// EventsFrom 返回任务 taskID 在 seq 之后的事件，按 seq 升序，最多 limit 条。
//
// 语义（重要）：事件数超过 limit 时返回**最新**的 limit 条（截断掉最旧的），
// 保证读到的始终是「离 now 最近」的窗口——attach 的 recent_events 依赖
// 「最新窗口」，若返回最旧的 limit 条，>limit 的积压会让新事件（如 completed）
// 永远读不到，违反「不丢新事件」。
//
// 注意：**WS 重放不得使用本方法**——截断最旧会让客户端 cursor 越过缺口，缺口
// 永不补齐；WS 重放请用 EventsFromAsc（截断尾部、可续拉）。
//
// 参数：
//   - taskID: 任务 ID
//   - fromSeq: 起始 seq（不含），传 0 表示从头
//   - limit: 返回条数上限
//
// 注意：
//   - 借助 idx_events_task(task_id, seq) 索引加速按任务的时间线扫描
func (s *Store) EventsFrom(taskID string, fromSeq int64, limit int) ([]proto.Event, error) {
	// DESC + 逆序翻转：先取最新 limit 条（截断最旧），再翻回升序交付调用方
	rows, err := s.db.QueryContext(context.Background(), `
SELECT seq, task_id, type, payload, created_at FROM events
WHERE task_id = ? AND seq > ? ORDER BY seq DESC LIMIT ?`, taskID, fromSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("查询事件: %w", err)
	}
	defer rows.Close()
	var events []proto.Event
	for rows.Next() {
		var (
			e         proto.Event
			payload   string
			createdAt string
		)
		if err := rows.Scan(&e.Seq, &e.TaskID, &e.Type, &payload, &createdAt); err != nil {
			return nil, fmt.Errorf("读取事件行: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		e.CreatedAt = parseTime(createdAt)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历事件: %w", err)
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}

// EventsFromAsc 返回任务 taskID 在 fromSeq 之后的事件，按 seq 升序，最多 limit 条。
//
// 与 EventsFrom（最新窗口）语义相反（重要）：事件数超过 limit 时截断**窗口尾部**、
// 保留最旧的 limit 条——客户端 cursor 只前进到「确实收到」的最后一条，被截掉的
// 尾部缺口可凭更大 from_seq 重连续拉，缺口永远可补齐；EventsFrom 截最旧会让
// cursor 越过缺口，缺口永久丢失（见该方法的注意）。
//
// 用途：WS 重放（server.handleEvents）必须用本方法从头补起。
//
// 参数：
//   - taskID: 任务 ID
//   - fromSeq: 起始 seq（不含），传 0 表示从头
//   - limit: 返回条数上限
//
// 注意：
//   - 借助 idx_events_task(task_id, seq) 索引加速按任务的时间线扫描
func (s *Store) EventsFromAsc(taskID string, fromSeq int64, limit int) ([]proto.Event, error) {
	rows, err := s.db.QueryContext(context.Background(), `
SELECT seq, task_id, type, payload, created_at FROM events
WHERE task_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?`, taskID, fromSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("查询事件: %w", err)
	}
	defer rows.Close()
	var events []proto.Event
	for rows.Next() {
		var (
			e         proto.Event
			payload   string
			createdAt string
		)
		if err := rows.Scan(&e.Seq, &e.TaskID, &e.Type, &payload, &createdAt); err != nil {
			return nil, fmt.Errorf("读取事件行: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		e.CreatedAt = parseTime(createdAt)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历事件: %w", err)
	}
	return events, nil
}

// LatestEvent 返回任务最新一条事件（seq 最大）。
//
// 返回：
//   - 任务无任何事件时返回 ErrNotFound
//
// 注意：
//   - 看门狗（stalled 判定）与启动恢复用「最新事件时刻」判断任务是否卡住，
//     单条取最新即可，不必拉全量事件
func (s *Store) LatestEvent(taskID string) (*proto.Event, error) {
	var (
		e         proto.Event
		payload   string
		createdAt string
	)
	err := s.db.QueryRowContext(context.Background(), `
SELECT seq, task_id, type, payload, created_at FROM events
WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, taskID).
		Scan(&e.Seq, &e.TaskID, &e.Type, &payload, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询任务 %s 最新事件: %w", taskID, err)
	}
	e.Payload = json.RawMessage(payload)
	e.CreatedAt = parseTime(createdAt)
	return &e, nil
}

// CreateTicket 幂等创建工单：同 id 重复调用不报错，第二次起 created 为 false。
//
// 参数：
//   - tk: 工单数据；ID 必须非空
//
// 返回：
//   - created: true 表示本次为首次创建（id 此前不存在）
//   - err: 数据库错误
//
// 注意：
//   - 通过 INSERT OR IGNORE 实现幂等，与 upsert 不同：已存在时保留旧数据，不覆盖
//   - answer/answered_at 一律由 AnswerTicket 写入，入参中的值被忽略
func (s *Store) CreateTicket(tk *proto.Ticket) (bool, error) {
	res, err := s.db.ExecContext(context.Background(), `
INSERT OR IGNORE INTO tickets (id, task_id, kind, request, answer, created_at, answered_at)
VALUES (?, ?, ?, ?, NULL, ?, NULL)`,
		tk.ID, tk.TaskID, tk.Kind, string(tk.Request), fmtTime(tk.CreatedAt))
	if err != nil {
		return false, fmt.Errorf("写入工单 %s: %w", tk.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取工单影响行数: %w", err)
	}
	return n == 1, nil
}

// GetTicket 按 id 读取工单；不存在返回 ErrNotFound。
//
// 参数：
//   - id: 工单 ID
//
// 返回：
//   - 工单数据；不存在时返回 ErrNotFound
func (s *Store) GetTicket(id string) (*proto.Ticket, error) {
	var (
		tk         proto.Ticket
		request    string
		answer     sql.NullString
		answeredAt sql.NullString
		createdAt  string
	)
	err := s.db.QueryRowContext(context.Background(), `
SELECT id, task_id, kind, request, answer, created_at, answered_at FROM tickets WHERE id = ?`, id).
		Scan(&tk.ID, &tk.TaskID, &tk.Kind, &request, &answer, &createdAt, &answeredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取工单 %s: %w", id, err)
	}
	// request 先扫进 string 再转换：database/sql 不认 json.RawMessage（命名 []byte 类型）的直接 Scan
	tk.Request = json.RawMessage(request)
	tk.CreatedAt = parseTime(createdAt)
	if answer.Valid {
		tk.Answer = &answer.String
	}
	if answeredAt.Valid {
		t := parseTime(answeredAt.String)
		tk.AnsweredAt = &t
	}
	return &tk, nil
}

// AnswerTicket 填写工单答案，工单随即从待办中移出。
//
// 参数：
//   - id: 工单 ID
//   - answer: 人工答复内容
//
// 注意：
//   - 以 answer IS NULL 为更新条件：工单不存在或已回答（不可重复回答）均返回 ErrNotFound
func (s *Store) AnswerTicket(id, answer string) error {
	res, err := s.db.ExecContext(context.Background(),
		"UPDATE tickets SET answer = ?, answered_at = ? WHERE id = ? AND answer IS NULL",
		answer, fmtTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("回答工单 %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取工单影响行数: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingTickets 返回任务下所有未回答工单（answer IS NULL），按 created_at 升序。
//
// 参数：
//   - taskID: 任务 ID
//
// 注意：
//   - 用于 agent 阻塞等待人工答复的场景；回答过的工单不会出现在结果中
func (s *Store) PendingTickets(taskID string) ([]proto.Ticket, error) {
	rows, err := s.db.QueryContext(context.Background(), `
SELECT id, task_id, kind, request, answer, created_at, answered_at FROM tickets
WHERE task_id = ? AND answer IS NULL ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("查询待办工单: %w", err)
	}
	defer rows.Close()
	var tickets []proto.Ticket
	for rows.Next() {
		var (
			tk         proto.Ticket
			request    string
			answer     sql.NullString
			answeredAt sql.NullString
			createdAt  string
		)
		if err := rows.Scan(&tk.ID, &tk.TaskID, &tk.Kind, &request, &answer, &createdAt, &answeredAt); err != nil {
			return nil, fmt.Errorf("读取工单行: %w", err)
		}
		tk.Request = json.RawMessage(request)
		tk.CreatedAt = parseTime(createdAt)
		if answer.Valid {
			tk.Answer = &answer.String
		}
		if answeredAt.Valid {
			t := parseTime(answeredAt.String)
			tk.AnsweredAt = &t
		}
		tickets = append(tickets, tk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历待办工单: %w", err)
	}
	return tickets, nil
}

// fmtTime 将时间归一化为 UTC RFC3339Nano 文本存库。
// 统一 UTC 与固定格式，保证任意时区写入后回读一致，且文本字典序即时间序。
func fmtTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTime 解析库中存储的时间文本；格式由 fmtTime 保证，解析失败时返回零值。
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
