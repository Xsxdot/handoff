// 本文件是事件镜像两表（mirror_events / mirror_tasks）的持久化实现。
//
// 职责：
//   - 幂等追加远端事件副本（INSERT OR IGNORE，(task_id, seq) 复合主键即幂等键）
//   - 水位（远端 seq 已复制到的最大值）与开区间续拉
//   - 任务快照 upsert 与「任务 → 所属机器」的路由索引
//
// 边界：
//   - **镜像是 replication 不是真相**：权威始终在任务所在机器，本表只是副本，
//     可随时整表删除、按 from_seq=0 从远端重建——不存在本机独有状态
//   - 与 store.go 一致的叶子层纪律：方法错误 return 前不打日志；仅两个高价值
//     点例外（ListMirrorTasks 的坏行、DeleteMirrorTask 的破坏性操作）
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// MirrorTask 是 mirror_tasks 的一行：远端任务的快照副本 + 路由信息。
type MirrorTask struct {
	Task      proto.Task
	Target    string // §5.1 透明路由的索引：这条任务该转发给谁
	FetchedAt time.Time
}

// AppendMirrorEvent 幂等追加一条远端事件副本。
//
// 参数：
//   - taskID: 所属任务 ID
//   - ev: 事件（Seq 为**远端库**的自增 seq，原值保留）
//
// 返回：
//   - inserted: true=本次真的插入了新行；false=该 (task_id, seq) 已存在
//     （重连补拉重复到达），本次为 no-op
//   - err: 数据库错误
//
// 注意：
//   - INSERT OR IGNORE 的幂等语义就是「重连补拉天然去重」：远端是权威，本机
//     不重编号，重连凭 seq 续拉即可
func (s *Store) AppendMirrorEvent(taskID string, ev proto.Event) (inserted bool, err error) {
	res, err := s.db.ExecContext(context.Background(), `
INSERT OR IGNORE INTO mirror_events (task_id, seq, type, payload, created_at)
VALUES (?, ?, ?, ?, ?)`,
		taskID, ev.Seq, string(ev.Type), string(ev.Payload), fmtTime(ev.CreatedAt))
	if err != nil {
		return false, fmt.Errorf("追加镜像事件 %s/%d: %w", taskID, ev.Seq, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取镜像事件影响行数: %w", err)
	}
	return n == 1, nil
}

// MirrorWatermark 返回某任务镜像事件的当前水位（远端 seq 已复制的最大值）。
//
// 返回：任务无任何镜像事件时为 0——「首次订阅从头拉」与「没镜像过」同义。
func (s *Store) MirrorWatermark(taskID string) (int64, error) {
	var wm int64
	err := s.db.QueryRowContext(context.Background(),
		"SELECT COALESCE(MAX(seq),0) FROM mirror_events WHERE task_id = ?", taskID).Scan(&wm)
	if err != nil {
		return 0, fmt.Errorf("查询镜像事件水位 %s: %w", taskID, err)
	}
	return wm, nil
}

// MirrorEventsFrom 返回任务 taskID 的镜像事件中 seq 大于 fromSeq 的条目，
// 按 seq 升序，最多 limit 条。
//
// 注意：fromSeq 是**开区间**（seq > fromSeq），与本机 EventsFromAsc 的语义一致；
// 远端 seq 原值保留，重连凭它续拉。
func (s *Store) MirrorEventsFrom(taskID string, fromSeq int64, limit int) ([]proto.Event, error) {
	rows, err := s.db.QueryContext(context.Background(), `
SELECT seq, task_id, type, payload, created_at FROM mirror_events
WHERE task_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?`, taskID, fromSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("查询镜像事件: %w", err)
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
			return nil, fmt.Errorf("读取镜像事件行: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		e.CreatedAt = parseTime(createdAt)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历镜像事件: %w", err)
	}
	return events, nil
}

// UpsertMirrorTask 覆盖式写入（或更新）一条任务快照。
//
// 参数：
//   - target: 任务所在机器的名字（本机 cfg.Targets 的键）
//   - task: 任务体 JSON，整体序列化进 snapshot 列
//   - fetchedAt: 本次快照的时刻（UI 据此显示数据新旧）
//
// 注意：
//   - upsert 语义：同 task_id 重复调用用新快照覆盖旧快照，不报错
func (s *Store) UpsertMirrorTask(target string, task proto.Task, fetchedAt time.Time) error {
	snap, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("序列化镜像任务 %s: %w", task.ID, err)
	}
	_, err = s.db.ExecContext(context.Background(), `
INSERT INTO mirror_tasks (task_id, target, snapshot, fetched_at) VALUES (?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
  target = excluded.target, snapshot = excluded.snapshot, fetched_at = excluded.fetched_at`,
		task.ID, target, string(snap), fmtTime(fetchedAt))
	if err != nil {
		return fmt.Errorf("写入镜像任务 %s: %w", task.ID, err)
	}
	return nil
}

// ListMirrorTasks 返回全部镜像任务快照，按快照时刻降序。
//
// 注意：
//   - 单行快照解析失败只 Warn 跳过（副本脏了不该让看板挂掉），不使整个列表失败
//   - 返回的切片可能为空，恒不为 nil
func (s *Store) ListMirrorTasks() ([]MirrorTask, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT task_id, target, snapshot, fetched_at FROM mirror_tasks ORDER BY fetched_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询镜像任务列表: %w", err)
	}
	defer rows.Close()
	var out []MirrorTask
	for rows.Next() {
		var (
			taskID    string
			target    string
			snapshot  string
			fetchedAt string
		)
		if err := rows.Scan(&taskID, &target, &snapshot, &fetchedAt); err != nil {
			return nil, fmt.Errorf("读取镜像任务行: %w", err)
		}
		var task proto.Task
		if err := json.Unmarshal([]byte(snapshot), &task); err != nil {
			// 副本脏了必须留痕：单行坏掉跳过，其余照常返回
			log().Warn("镜像任务快照解析失败，跳过该行", "task_id", taskID, "cause", err)
			continue
		}
		out = append(out, MirrorTask{Task: task, Target: target, FetchedAt: parseTime(fetchedAt)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历镜像任务: %w", err)
	}
	if out == nil {
		out = []MirrorTask{}
	}
	return out, nil
}

// MirrorTaskTarget 返回任务所属机器（mirror_tasks 的路由索引查询）。
//
// 返回：
//   - ok=true: target 有效（该任务有镜像记录）
//   - ok=false: 任务没有镜像记录（从没发现过它），target 为空串
//   - err: 数据库错误
func (s *Store) MirrorTaskTarget(taskID string) (string, bool, error) {
	var target string
	err := s.db.QueryRowContext(context.Background(),
		"SELECT target FROM mirror_tasks WHERE task_id = ?", taskID).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("查询镜像任务路由 %s: %w", taskID, err)
	}
	return target, true, nil
}

// DeleteMirrorTask 删除一条镜像任务及其全部镜像事件。
//
// 返回：
//   - 被删除的事件条数（任务快照本身不存在时不报错，返回 0）
//
// 注意：
//   - 这是本包唯一的破坏性镜像操作，成功打 Info（task_id + 事件条数）
//   - 两个删放在一个事务里：镜像任务是复制品，删除必须原子，不能留
//     「快照没了、事件还在」的半截
func (s *Store) DeleteMirrorTask(taskID string) (int, error) {
	ctx := context.Background()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("开启删除镜像任务事务: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, "DELETE FROM mirror_events WHERE task_id = ?", taskID)
	if err != nil {
		return 0, fmt.Errorf("删除镜像事件 %s: %w", taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取镜像事件删除行数: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mirror_tasks WHERE task_id = ?", taskID); err != nil {
		return 0, fmt.Errorf("删除镜像任务 %s: %w", taskID, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交删除镜像任务事务: %w", err)
	}
	log().Info("镜像任务已删除", "task_id", taskID, "events_removed", n)
	return int(n), nil
}
