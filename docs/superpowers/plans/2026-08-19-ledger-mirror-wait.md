# 事件镜像 + 多路 wait（Plan B / B156.1-镜像）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把挂账 task 的事件从各执行机镜像进账本单流（lease 仲裁单镜像者、幂等写入、健康心跳），并交付 `handoff wait --card <id> [--subtree]` 账本单流多路 wait——「全程一个 wait」的承重件。

**Architecture:** 新包 `internal/ledgermirror` 作为 agentd 的第二个子系统（蓝图 §3.8 扩展点①），形态照抄 web-console 已有的 `internal/agentd/mirror.go`（per-task 订阅、断线退避、watermark 从库派生）——差别只有三点：写入目标是**账本库** `card_events`（带来源三元组幂等键）、订阅集来自 `card_tasks`（挂账即订阅，spec §3 修订版）、多协调机由**账本库 lease** 仲裁单实例。wait --card 在 CLI 侧直连账本库消费单流：PG 走 LISTEN + 按 seq 补查（推送只是叫醒，真相永远查表），SQLite 回退 2s 轮询。

**Tech Stack:** `internal/ledger`（Plan A）、`internal/client`（`StreamEventsOnce`，web-console 已有）、`internal/proto`（事件类型常量）、`github.com/jackc/pgx/v5`（LISTEN 用裸连接）。

**前置条件：** Plan A 已合入；main 已含 web-console 合并（`internal/agentd/mirror.go` 与 `client.StreamEventsOnce` 存在——开工实查，没有即 BLOCKED）。基线 `go build ./... && go test ./...` 全绿。

**House rules：** 同 Plan A。镜像是后台子系统，日志要求比库层高：启动/退出/lease 得失/订阅增减/断线重连必打 Info（带 target/task 上下文），事件写入循环内降 Debug。`internal/proto` 的事件类型常量名以实际代码为准（本 plan 按摸底写 `EventTypeProgress`/`EventTypeCompleted`/`EventTypeFailed` 等，有出入改本 plan 侧）。

---

## File Structure

```
internal/ledger/
  mirror.go          // 账本侧镜像原语：watermark/幂等写入/lease/健康表/MaxSeq
  mirror_test.go
  follow.go          // Follow：PG LISTEN + SQLite 轮询的单流消费
  follow_test.go
  store.go           // 改动：Store 记住 dsn（Follow 的 LISTEN 需要）
internal/ledgermirror/
  mirror.go          // 子系统：lease 循环 + 订阅对账 + per-task 镜像
  mirror_test.go     // fake 事件源测试（不碰网络）
cmd/
  agentd.go          // 改动：打开账本库 + 挂载镜像子系统（构造→go Run→Stop 次序）
  wait.go            // 改动：--card/--subtree 多路 wait
  wait_card_test.go
```

---

### Task 1: internal/ledger/mirror.go——镜像原语

**Files:**
- Create: `internal/ledger/mirror.go`
- Modify: `internal/ledger/store.go`（Store 增加 `dsn string` 字段，`Open` 里保存原始 dsn）
- Test: `internal/ledger/mirror_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import (
	"testing"
	"time"
)

func TestAppendMirroredEventIdempotent(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "挂账卡")
	if err := s.LinkTask(c.ID, "mac-02", "T1", "implement", "test"); err != nil {
		t.Fatalf("link: %v", err)
	}
	ev := MirroredEvent{Target: "mac-02", Task: "T1", SourceSeq: 7,
		Type: "completed", Payload: []byte(`{"x":1}`), CreatedAt: time.Now()}
	wrote, err := s.AppendMirroredEvent(c.ID, ev)
	if err != nil || !wrote {
		t.Fatalf("首写: %v %v", err, wrote)
	}
	// 同 (target, task, seq) 重放：不重不错（幂等键）
	wrote, err = s.AppendMirroredEvent(c.ID, ev)
	if err != nil || wrote {
		t.Fatalf("重放应静默跳过: %v %v", err, wrote)
	}
	evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
	mirrored := 0
	for _, e := range evs {
		if e.Type == EvTaskMirrored {
			mirrored++
			if e.SourceTarget != "mac-02" || e.SourceSeq != 7 {
				t.Fatalf("来源三元组: %+v", e)
			}
		}
	}
	if mirrored != 1 {
		t.Fatalf("镜像事件应恰一条: %d", mirrored)
	}
	// watermark 从库派生
	wm, err := s.MirrorWatermark("mac-02", "T1")
	if err != nil || wm != 7 {
		t.Fatalf("watermark: %v %d", err, wm)
	}
	if wm, _ := s.MirrorWatermark("mac-02", "没镜像过"); wm != 0 {
		t.Fatalf("空 watermark 应为 0: %d", wm)
	}
}

func TestMirrorLease(t *testing.T) {
	s := seedStore(t)
	ttl := 200 * time.Millisecond
	got, err := s.AcquireMirrorLease("coordA", ttl)
	if err != nil || !got {
		t.Fatalf("A 首取: %v %v", err, got)
	}
	// 未过期 B 抢不到；A 续约成功
	if got, _ := s.AcquireMirrorLease("coordB", ttl); got {
		t.Fatal("B 不应抢到")
	}
	if got, _ := s.AcquireMirrorLease("coordA", ttl); !got {
		t.Fatal("A 续约应成功")
	}
	// 过期后 B 接任（判据⑩的 lease 切换单测形）
	time.Sleep(ttl + 50*time.Millisecond)
	if got, _ := s.AcquireMirrorLease("coordB", ttl); !got {
		t.Fatal("过期后 B 应接任")
	}
	if got, _ := s.AcquireMirrorLease("coordA", ttl); got {
		t.Fatal("A 丢 lease 后不应立刻拿回")
	}
}

func TestMirrorHealth(t *testing.T) {
	s := seedStore(t)
	if err := s.TouchMirrorHealth("mac-02", 42); err != nil {
		t.Fatalf("touch: %v", err)
	}
	// 空 touch（静默期心跳）：seq 不回退
	if err := s.TouchMirrorHealth("mac-02", 0); err != nil {
		t.Fatalf("touch2: %v", err)
	}
	rows, err := s.MirrorHealth()
	if err != nil || len(rows) != 1 || rows[0].LastSeq != 42 || rows[0].UpdatedAt.IsZero() {
		t.Fatalf("health: %v %+v", err, rows)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run 'TestAppendMirrored|TestMirrorLease|TestMirrorHealth' -v`
Expected: FAIL（类型/方法未定义）

- [ ] **Step 3: store.go 记住 dsn**

`Open` 开头 `s := &Store{}` 改为 `s := &Store{dsn: dsn}`，Store 结构体加：

```go
	dsn string // 原始 DSN（Follow 的 PG LISTEN 需要开第二条裸连接）
```

- [ ] **Step 4: 实现 mirror.go**

```go
// 账本侧镜像原语：镜像者（internal/ledgermirror）与看板共用。
// 设计要点：watermark 不设独立游标，从 card_events 的 MAX(source_seq)
// 派生——游标与数据同源，不可能漂移（spec §3 修订版）；幂等写入在
// mutate 全局写锁内先查后插，唯一索引作最后防线。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MirroredEvent 一条待镜像的 task 事件（来源三元组 + 原始负载）。
type MirroredEvent struct {
	Target, Task string
	SourceSeq    int64
	Type         string // 原 task 事件类型，存入 payload；账本事件类型恒为 task_mirrored
	Payload      []byte
	CreatedAt    time.Time
}

// MirrorHealthRow per-target 镜像健康行（滞后判定数据源）。
type MirrorHealthRow struct {
	Target    string
	LastSeq   int64
	UpdatedAt time.Time
}

// AppendMirroredEvent 幂等写入镜像事件。返回是否真的写入（false=重放跳过）。
// 账本事件 type=task_mirrored，payload 包原类型与原始负载；来源三元组落
// source_* 列。写入即 NOTIFY（单流消费者不区分事件出身）。
func (s *Store) AppendMirroredEvent(cardID string, ev MirroredEvent) (bool, error) {
	wrote := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var one int
		err := tx.QueryRow(s.q(`SELECT 1 FROM card_events
			WHERE source_target = ? AND source_task = ? AND source_seq = ?`),
			ev.Target, ev.Task, ev.SourceSeq).Scan(&one)
		if err == nil {
			return nil // 已镜像过：幂等跳过
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("查幂等键: %w", err)
		}
		payload := fmt.Sprintf(`{"task_type":%q,"payload":%s}`, ev.Type, string(ev.Payload))
		var seq int64
		if s.dialect == dialectPG {
			err = tx.QueryRow(s.q(`INSERT INTO card_events
				(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
				VALUES (?,?,?,?,?,?,?,?) RETURNING seq`),
				cardID, EvTaskMirrored, "mirror", payload,
				ev.Target, ev.Task, ev.SourceSeq, s.tval(ev.CreatedAt)).Scan(&seq)
			if err == nil {
				_, err = tx.Exec(`SELECT pg_notify('card_events', $1)`, fmt.Sprint(seq))
			}
		} else {
			var res sql.Result
			res, err = tx.Exec(s.q(`INSERT INTO card_events
				(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
				VALUES (?,?,?,?,?,?,?,?)`),
				cardID, EvTaskMirrored, "mirror", payload,
				ev.Target, ev.Task, ev.SourceSeq, s.tval(ev.CreatedAt))
			if err == nil {
				seq, err = res.LastInsertId()
			}
		}
		if err != nil {
			return fmt.Errorf("写镜像事件 %s/%s#%d: %w", ev.Target, ev.Task, ev.SourceSeq, err)
		}
		sink.seqs = append(sink.seqs, seq)
		wrote = true
		return nil
	})
	return wrote, err
}

// MirrorWatermark per (target, task) 的续拉起点 = 已镜像的最大原 seq。
func (s *Store) MirrorWatermark(target, task string) (int64, error) {
	var wm sql.NullInt64
	err := s.db.QueryRow(s.q(`SELECT MAX(source_seq) FROM card_events
		WHERE source_target = ? AND source_task = ?`), target, task).Scan(&wm)
	if err != nil {
		return 0, fmt.Errorf("查 watermark: %w", err)
	}
	return wm.Int64, nil
}

// AcquireMirrorLease 取/续镜像 lease（单行表 CAS）。规则：无持有者、
// 持有者是自己、或已过期 → 得到并续期；否则 false。得失变更打 Info。
func (s *Store) AcquireMirrorLease(holder string, ttl time.Duration) (bool, error) {
	got := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		var cur string
		var until any
		err := tx.QueryRow(s.q(`SELECT holder, lease_until FROM mirror_lease WHERE id = 1`)).Scan(&cur, &until)
		now := time.Now()
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(s.q(`INSERT INTO mirror_lease (id, holder, lease_until) VALUES (1, ?, ?)`),
				holder, s.tval(now.Add(ttl))); err != nil {
				return fmt.Errorf("首建 lease: %w", err)
			}
			log().Info("镜像 lease 首取", "holder", holder)
			got = true
			return nil
		case err != nil:
			return fmt.Errorf("读 lease: %w", err)
		}
		if cur != holder && toTime(until).After(now) {
			return nil // 别人持有且未过期
		}
		if cur != holder {
			log().Info("镜像 lease 接任", "from", cur, "to", holder)
		}
		if _, err := tx.Exec(s.q(`UPDATE mirror_lease SET holder = ?, lease_until = ? WHERE id = 1`),
			holder, s.tval(now.Add(ttl))); err != nil {
			return fmt.Errorf("写 lease: %w", err)
		}
		got = true
		return nil
	})
	return got, err
}

// TouchMirrorHealth per-target 心跳：updated_at 恒更新（静默期空 touch
// 防误报滞后），last_seq 只升不降。
func (s *Store) TouchMirrorHealth(target string, lastSeq int64) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		_, err := tx.Exec(s.q(`INSERT INTO mirror_cursors (target, last_seq, updated_at)
			VALUES (?,?,?)
			ON CONFLICT (target) DO UPDATE SET
				last_seq = CASE WHEN excluded.last_seq > mirror_cursors.last_seq
					THEN excluded.last_seq ELSE mirror_cursors.last_seq END,
				updated_at = excluded.updated_at`),
			target, lastSeq, s.tval(time.Now()))
		if err != nil {
			return fmt.Errorf("touch 镜像健康 %s: %w", target, err)
		}
		return nil
	})
}

// MirrorHealth 全部 target 的健康行（看板滞后判定：now-UpdatedAt>60s 亮灯）。
func (s *Store) MirrorHealth() ([]MirrorHealthRow, error) {
	rows, err := s.db.Query(`SELECT target, last_seq, updated_at FROM mirror_cursors ORDER BY target`)
	if err != nil {
		return nil, fmt.Errorf("读镜像健康: %w", err)
	}
	defer rows.Close()
	var out []MirrorHealthRow
	for rows.Next() {
		var r MirrorHealthRow
		var ut any
		if err := rows.Scan(&r.Target, &r.LastSeq, &ut); err != nil {
			return nil, err
		}
		r.UpdatedAt = toTime(ut)
		out = append(out, r)
	}
	return out, rows.Err()
}

// MaxSeq 账本单流当前最大 seq（wait 的起点）。
func (s *Store) MaxSeq() (int64, error) {
	var m sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM card_events`).Scan(&m); err != nil {
		return 0, fmt.Errorf("查最大 seq: %w", err)
	}
	return m.Int64, nil
}
```

- [ ] **Step 5: 跑测试确认通过 + Commit**

Run: `go test ./internal/ledger/ -v` → 全 PASS

```bash
git add internal/ledger/
git commit -m "feat(ledger): 镜像原语——幂等写入/派生 watermark/lease CAS/健康心跳/MaxSeq"
```

---

### Task 2: internal/ledger/follow.go——单流消费（PG LISTEN / SQLite 轮询）

**Files:**
- Create: `internal/ledger/follow.go`
- Test: `internal/ledger/follow_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import (
	"context"
	"testing"
	"time"
)

func TestFollowSQLitePoll(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "被跟的卡")
	start, _ := s.MaxSeq()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan Event, 16)
	done := make(chan error, 1)
	go func() {
		done <- s.Follow(ctx, func() ([]string, error) { return []string{c.ID}, nil },
			start, 100*time.Millisecond, func(e Event) error {
				got <- e
				return nil
			})
	}()
	// Follow 挂起期间新写的事件要被送达（判据⑤的单测形）
	time.Sleep(300 * time.Millisecond)
	if _, err := s.AddComment(c.ID, "新评论", "普通", "test"); err != nil {
		t.Fatalf("comment: %v", err)
	}
	select {
	case e := <-got:
		if e.Type != EvComment {
			t.Fatalf("送达类型: %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("事件未送达")
	}
	cancel()
	if err := <-done; err != nil && ctx.Err() == nil {
		t.Fatalf("follow 退出: %v", err)
	}
}

func TestFollowDynamicMembership(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "根")
	start, _ := s.MaxSeq()
	// 成员集是函数：Follow 挂起期间新拆的子卡自然进流
	members := func() ([]string, error) { return s.Subtree(c.ID) }

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan Event, 16)
	go s.Follow(ctx, members, start, 100*time.Millisecond, func(e Event) error {
		got <- e
		return nil
	})
	time.Sleep(300 * time.Millisecond)
	child, err := s.SplitCard(c.ID, "新子卡", "test")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	_, _ = s.AddComment(child.ID, "子卡上的事件", "普通", "test")
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-got:
			if e.CardID == child.ID && e.Type == EvComment {
				return // 新成员的事件进流了
			}
		case <-deadline:
			t.Fatal("新成员事件未进流")
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledger/ -run TestFollow -v` → FAIL（Follow 未定义）

- [ ] **Step 3: 实现 follow.go**

```go
// 账本单流消费：多路 wait 的读侧。推送只是叫醒，真相永远按 seq 查表——
// PG 用 LISTEN card_events 叫醒 + 兜底轮询（30s），SQLite 纯轮询
// （CLI 与写入者不同进程，进程内回调够不着）。
package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Follow 从 fromSeq（排他）起持续消费 members() 集合内卡的事件（含
// card_id 为空的项目级事件不在内——多路 wait 只关心子树）。members
// 每轮重新求值：wait 挂起期间新拆/新派发的卡天然进流。onEvent 返回
// 错误即终止。pollInterval 是 SQLite 的轮询间隔与 PG 的兜底间隔；
// 生产用 2*time.Second，测试注短。阻塞直到 ctx 取消或 onEvent 报错。
func (s *Store) Follow(ctx context.Context, members func() ([]string, error),
	fromSeq int64, pollInterval time.Duration, onEvent func(Event) error) error {
	wake := make(chan struct{}, 1)
	if s.dialect == dialectPG {
		// LISTEN 用独立裸连接：database/sql 连接池拿不到稳定的会话级 LISTEN
		conn, err := pgx.Connect(ctx, s.dsn)
		if err != nil {
			return fmt.Errorf("LISTEN 连接: %w", err)
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(ctx, "LISTEN card_events"); err != nil {
			return fmt.Errorf("LISTEN: %w", err)
		}
		go func() {
			defer close(wake)
			for {
				// 通知只当叫醒铃用，内容不解析——查询以 seq 为准
				if _, err := conn.WaitForNotification(ctx); err != nil {
					return // ctx 取消或连接断：主循环靠兜底轮询继续
				}
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}()
	}
	cursor := fromSeq
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		ids, err := members()
		if err != nil {
			return fmt.Errorf("解析成员集: %w", err)
		}
		evs, err := s.EventsFromAsc(ids, cursor, 500)
		if err != nil {
			return err
		}
		for _, e := range evs {
			if err := onEvent(e); err != nil {
				return err
			}
			cursor = e.Seq
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		case <-wake:
		}
	}
}
```

- [ ] **Step 4: 跑测试确认通过 + Commit**

Run: `go test ./internal/ledger/ -run TestFollow -v` → PASS

```bash
git add internal/ledger/
git commit -m "feat(ledger): Follow 单流消费——PG LISTEN 叫醒 + 查表为准，SQLite 轮询，动态成员集"
```

---

### Task 3: internal/ledgermirror——镜像子系统

**Files:**
- Create: `internal/ledgermirror/mirror.go`
- Test: `internal/ledgermirror/mirror_test.go`

- [ ] **Step 1: 写失败测试（fake 事件源，不碰网络）**

```go
// 镜像子系统测试：事件源用 fake 注入，验证 lease 独占、幂等重放、
// 挂账对账、终态退订。真网络路径归真机判据（⑤⑦⑩），不在单测里装。
package ledgermirror

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func testLedger(t *testing.T) *ledger.Store {
	t.Helper()
	s, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMirrorFlowsLinkedTaskEvents(t *testing.T) {
	s := testLedger(t)
	c, _ := s.CreateCard(ledger.NewCard{Title: "卡", Project: "p", Workflow: "bug", Actor: "t"})
	_ = s.LinkTask(c.ID, "mac-02", "T1", "implement", "t")

	var calls atomic.Int64
	fake := func(ctx context.Context, addr, token, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		calls.Add(1)
		// 每次订阅都从 fromSeq 重放固定三条：progress 应被过滤，重连不重写
		for _, e := range []proto.Event{
			{Seq: 1, TaskID: taskID, Type: proto.EventTypeProgress, Payload: []byte(`{}`)},
			{Seq: 2, TaskID: taskID, Type: "message", Payload: []byte(`{"text":"hi"}`)},
			{Seq: 3, TaskID: taskID, Type: proto.EventTypeCompleted, Payload: []byte(`{}`)},
		} {
			if e.Seq <= fromSeq {
				continue
			}
			if err := onEvent(e); err != nil {
				return err
			}
		}
		<-ctx.Done() // 挂住模拟长连接
		return ctx.Err()
	}
	m := New(s, func() map[string]config.Target {
		return map[string]config.Target{"mac-02": {Addr: "127.0.0.1:1", Token: "t"}}
	}, Options{Holder: "test-coord", Tick: 50 * time.Millisecond,
		LeaseTTL: time.Second, Source: fake})
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); m.Stop() })

	// 等镜像落账
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
		var mirrored int
		for _, e := range evs {
			if e.Type == ledger.EvTaskMirrored {
				mirrored++
			}
		}
		if mirrored == 2 { // message + completed；progress 被过滤
			// 健康心跳也在
			rows, _ := s.MirrorHealth()
			if len(rows) == 1 && rows[0].Target == "mac-02" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("镜像未按期落账（应恰 2 条：progress 过滤、幂等不重）")
}

func TestMirrorLeaseExclusive(t *testing.T) {
	s := testLedger(t)
	blockSrc := func(ctx context.Context, _, _, _ string, _ int64, _ func(proto.Event) error) error {
		<-ctx.Done()
		return ctx.Err()
	}
	targets := func() map[string]config.Target { return nil }
	a := New(s, targets, Options{Holder: "A", Tick: 50 * time.Millisecond, LeaseTTL: time.Second, Source: blockSrc})
	b := New(s, targets, Options{Holder: "B", Tick: 50 * time.Millisecond, LeaseTTL: time.Second, Source: blockSrc})
	ctx, cancel := context.WithCancel(context.Background())
	go a.Run(ctx)
	go b.Run(ctx)
	t.Cleanup(func() { cancel(); a.Stop(); b.Stop() })
	time.Sleep(300 * time.Millisecond)
	// 恰一个持有（A 先起大概率是 A，但断言只看排他性）
	if a.Holding() == b.Holding() {
		t.Fatalf("lease 应恰一人持有: A=%v B=%v", a.Holding(), b.Holding())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/ledgermirror/ -v` → FAIL（包不存在）

- [ ] **Step 3: 实现 mirror.go**

```go
// Package ledgermirror 是 agentd 的账本镜像子系统（蓝图 §3.8 扩展点①
// 的第二个用户）：把挂账 task 的事件从各执行机镜像进账本单流。
//
// 形态照抄 internal/agentd/mirror.go（per-task 订阅、断线退避、watermark
// 库派生），三点不同：写账本库（幂等键 = 来源三元组）、订阅集来自
// card_tasks（挂账即订阅）、多协调机由账本库 lease 仲裁单实例。
// 边界：本包不 import internal/agentd；事件源经 Source 注入（生产为
// client.StreamEventsOnce 包装），测试不碰网络。
package ledgermirror

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// Source 一条 per-task 事件订阅：从 fromSeq（排他）起回放 + 跟流，
// 阻塞直到 ctx 取消或连接终结。生产实现见 DefaultSource。
type Source func(ctx context.Context, addr, token, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error

// Options 子系统参数。零值取生产默认。
type Options struct {
	Holder   string        // lease 持有者标识（机器名）
	Tick     time.Duration // lease 续约 + 订阅对账周期，默认 10s
	LeaseTTL time.Duration // 默认 30s
	Source   Source        // 默认 DefaultSource
}

// DefaultSource 生产事件源：client.StreamEventsOnce 的薄包装。
func DefaultSource(ctx context.Context, addr, token, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	return client.New(addr, token).StreamEventsOnce(ctx, taskID, fromSeq, onEvent)
}

// mirrorSkip 不入账本单流的事件类型。与 internal/client 的 isDeliverable
// 保持同一份语义（progress 高频噪音；三个审计类型 live 流本就不发，镜像
// 若写入会让重放比实况多）。改这里前先看 client.go 的对应注释。
var mirrorSkip = map[proto.EventType]bool{
	proto.EventTypeProgress:         true,
	proto.EventTypeApproverDecision: true,
	proto.EventTypeApproverDisabled: true,
	proto.EventTypeTicketsVoided:    true,
}

// Mirror 镜像子系统实例。
type Mirror struct {
	st      *ledger.Store
	targets func() map[string]config.Target
	opt     Options
	log     *slog.Logger

	holding atomic.Bool
	mu      sync.Mutex
	subs    map[string]context.CancelFunc // key: target + "/" + task
	wg      sync.WaitGroup
}

// New 构造。targets 用函数注入（config 可被 /api/machines 热改）。
func New(st *ledger.Store, targets func() map[string]config.Target, opt Options) *Mirror {
	if opt.Tick == 0 {
		opt.Tick = 10 * time.Second
	}
	if opt.LeaseTTL == 0 {
		opt.LeaseTTL = 30 * time.Second
	}
	if opt.Source == nil {
		opt.Source = DefaultSource
	}
	return &Mirror{st: st, targets: targets, opt: opt,
		log: slog.Default().With("subsystem", "ledgermirror"), subs: map[string]context.CancelFunc{}}
}

// Holding 当前是否持有镜像 lease（测试与状态面用）。
func (m *Mirror) Holding() bool { return m.holding.Load() }

// Run 主循环：每 Tick 取/续 lease；持有则对账订阅集，失去则停掉全部
// 订阅（续约失败立即停写，绝不双写）。阻塞直到 ctx 取消。
func (m *Mirror) Run(ctx context.Context) {
	m.log.Info("账本镜像子系统启动", "holder", m.opt.Holder, "tick", m.opt.Tick, "lease_ttl", m.opt.LeaseTTL)
	tick := time.NewTicker(m.opt.Tick)
	defer tick.Stop()
	for {
		got, err := m.st.AcquireMirrorLease(m.opt.Holder, m.opt.LeaseTTL)
		if err != nil {
			m.log.Warn("lease 操作失败", "err", err)
		}
		if got != m.holding.Load() {
			m.log.Info("镜像 lease 状态变化", "holding", got)
		}
		m.holding.Store(got)
		if got {
			m.reconcile(ctx)
		} else {
			m.stopAllSubs("失去 lease")
		}
		select {
		case <-ctx.Done():
			m.log.Info("账本镜像子系统退出", "cause", ctx.Err())
			m.stopAllSubs("关停")
			return
		case <-tick.C:
		}
	}
}

// Stop 等全部订阅 goroutine 退出。必须在账本库 Close 之前调用——
// 订阅回调在写库。
func (m *Mirror) Stop() {
	m.stopAllSubs("Stop")
	m.wg.Wait()
}

// reconcile 订阅集对账：card_tasks 里、target 已登记、且订阅未在跑的
// (target, task) 起订；已不在挂账表（不会发生，弱引用不删）或 target
// 已注销的停订。
func (m *Mirror) reconcile(ctx context.Context) {
	targets := m.targets()
	links, err := m.st.AllTaskLinks()
	if err != nil {
		m.log.Warn("读挂账表失败", "err", err)
		return
	}
	want := map[string]ledger.TaskLink{}
	for _, l := range links {
		if _, ok := targets[l.Target]; !ok {
			continue // target 未登记：无从拨号，留给健康面报
		}
		want[l.Target+"/"+l.TaskID] = l
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, cancel := range m.subs {
		if _, ok := want[key]; !ok {
			cancel()
			delete(m.subs, key)
			m.log.Info("退订", "sub", key)
		}
	}
	for key, l := range want {
		if _, ok := m.subs[key]; ok {
			continue
		}
		subCtx, cancel := context.WithCancel(ctx)
		m.subs[key] = cancel
		tgt := targets[l.Target]
		m.wg.Add(1)
		go m.subscribe(subCtx, l, tgt)
		m.log.Info("起订", "sub", key)
	}
	// 静默期空 touch：健康心跳不因没事件而误报滞后
	for name := range targets {
		if err := m.st.TouchMirrorHealth(name, 0); err != nil {
			m.log.Warn("touch 健康失败", "target", name, "err", err)
		}
	}
}

// subscribe 单 task 常驻订阅：watermark 起拉、断线退避重连、事件过滤后
// 幂等落账。回源正常终结（task 归档）即退出。
func (m *Mirror) subscribe(ctx context.Context, l ledger.TaskLink, tgt config.Target) {
	defer m.wg.Done()
	backoff := 300 * time.Millisecond
	const maxBackoff = 10 * time.Second
	for ctx.Err() == nil {
		wm, err := m.st.MirrorWatermark(l.Target, l.TaskID)
		if err != nil {
			m.log.Warn("读 watermark 失败", "task", l.TaskID, "err", err)
			return
		}
		err = m.opt.Source(ctx, tgt.Addr, tgt.Token, l.TaskID, wm, func(e proto.Event) error {
			if mirrorSkip[e.Type] {
				return nil
			}
			wrote, err := m.st.AppendMirroredEvent(l.CardID, ledger.MirroredEvent{
				Target: l.Target, Task: l.TaskID, SourceSeq: e.Seq,
				Type: string(e.Type), Payload: e.Payload, CreatedAt: e.CreatedAt,
			})
			if err != nil {
				return err
			}
			if wrote {
				m.log.Debug("镜像事件", "task", l.TaskID, "seq", e.Seq, "type", e.Type)
				if err := m.st.TouchMirrorHealth(l.Target, e.Seq); err != nil {
					m.log.Warn("touch 健康失败", "target", l.Target, "err", err)
				}
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			m.log.Info("订阅正常终结（task 归档）", "task", l.TaskID)
			return
		}
		m.log.Warn("订阅断开，退避重连", "task", l.TaskID, "backoff", backoff, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (m *Mirror) stopAllSubs(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.subs) > 0 {
		m.log.Info("停全部订阅", "n", len(m.subs), "reason", reason)
	}
	for key, cancel := range m.subs {
		cancel()
		delete(m.subs, key)
	}
}
```

配套在 `internal/ledger/tasks.go` 追加 `AllTaskLinks`（+ tasks_test.go 一条断言）：

```go
// AllTaskLinks 全部挂账行（镜像对账用）。
func (s *Store) AllTaskLinks() ([]TaskLink, error) {
	rows, err := s.db.Query(`SELECT card_id, target, task_id, purpose, created_at FROM card_tasks`)
	if err != nil {
		return nil, fmt.Errorf("读全部挂账: %w", err)
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
```

- [ ] **Step 4: 跑测试确认通过 + Commit**

Run: `go test ./internal/ledgermirror/ ./internal/ledger/ -v` → 全 PASS

```bash
git add internal/ledgermirror/ internal/ledger/
git commit -m "feat(ledgermirror): 账本镜像子系统——lease 独占/挂账对账/per-task 订阅/幂等落账/健康心跳"
```

---

### Task 4: cmd/agentd.go 挂载子系统

**Files:**
- Modify: `cmd/agentd.go`

- [ ] **Step 1: 在 Mirror（web-console 既有）挂载点之后追加**

对照现有 `if len(cfg.Targets) > 0 { mirror := agentd.NewMirror(...) ... }` 段（约 `cmd/agentd.go:210-220`），紧随其后加：

```go
	// 账本镜像子系统：有已登记 target 才有镜像对象；账本库按配置解析
	// （dsn 空 = DataDir/ledger.db 单机回退）。构造→go Run→（退出时）
	// Stop→Close 的次序是硬约束：订阅回调在写库，Stop 必须先于 Close。
	if len(cfg.Targets) > 0 {
		ldsn := cfg.Ledger.DSN
		if ldsn == "" {
			ldsn = filepath.Join(cfg.DataDir, "ledger.db")
		}
		lst, err := ledger.Open(ldsn)
		if err != nil {
			return fmt.Errorf("打开账本库: %w", err)
		}
		defer lst.Close()
		if err := lst.EnsureDefaultWorkflows(); err != nil {
			return fmt.Errorf("seed 默认工作流: %w", err)
		}
		host, _ := os.Hostname()
		lm := ledgermirror.New(lst, func() map[string]config.Target { return cfg.Targets },
			ledgermirror.Options{Holder: host})
		go lm.Run(wdCtx)
		defer lm.Stop() // LIFO：Stop 先于上面的 lst.Close 执行
		logger.Info("账本镜像子系统已挂载", "holder", host)
	} else {
		logger.Info("账本镜像未启动：无已登记 target")
	}
```

（import 补 `"github.com/Xsxdot/handoff/internal/ledger"`、`"github.com/Xsxdot/handoff/internal/ledgermirror"`；`filepath`/`os` 已有则不重复。`cfg.Targets` 若在 web-console 分支上是经 `Server.conf()` 热读的，targets 函数改为从热读入口取——以实际代码为准，保持「热改 /api/machines 后 tick 内生效」。）

- [ ] **Step 2: 构建 + 既有测试不回归**

Run: `go build ./... && go test ./cmd/ 2>&1 | tail -5`
Expected: 编译过、既有 agentd 测试不红。挂载路径的行为验证归真机判据（⑤⑦⑩），不在单测里起真 agentd。

- [ ] **Step 3: Commit**

```bash
git add cmd/
git commit -m "feat(agentd): 挂载账本镜像子系统——Stop 先于账本库 Close 的关停次序落死在 defer 序"
```

---

### Task 5: wait --card 多路 wait

**Files:**
- Modify: `cmd/wait.go`
- Test: `cmd/wait_card_test.go`

- [ ] **Step 1: 写失败测试**

```go
package cmd

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestWaitCardSubtreeExitsWhenAllDone(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "根卡", "--project", "demo", "--workflow", "bug")
	var root struct{ ID string `json:"id"` }
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &root)
	out, _, _ = runLedgerCLI(t, dir, "card", "split", root.ID, "子卡")
	var child struct{ ID string `json:"id"` }
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &child)

	// 后台把两张卡推到终态；wait 应随之退出且事件都打出来了
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond)
		st, err := ledger.Open(dir + "/ledger.db")
		if err != nil {
			t.Error(err)
			return
		}
		defer st.Close()
		for _, id := range []string{child.ID, root.ID} {
			_ = st.MoveCard(id, "进行中", "", "test")
			_ = st.MoveCard(id, "待审阅", "", "test")
			_ = st.MoveCard(id, "已完成", "", "test")
		}
	}()
	waitOut, _, err := runLedgerCLI(t, dir, "wait", "--card", root.ID, "--subtree", "--timeout", "15s")
	wg.Wait()
	if err != nil {
		t.Fatalf("wait 应正常退出: %v", err)
	}
	// 子树两张卡的转移事件都在输出流里（每行一个 JSON 事件）
	if !strings.Contains(waitOut, child.ID) || !strings.Contains(waitOut, root.ID) {
		t.Fatalf("事件缺失: %q", waitOut)
	}
}

func TestWaitCardConflictsWithTaskArg(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "wait", "T123", "--card", "B1"); err == nil {
		t.Fatal("task 参数与 --card 互斥应报错")
	}
}
```

- [ ] **Step 2: 实现（cmd/wait.go 改动）**

`waitCmd` 的 `Args` 从 `cobra.ExactArgs(1)` 改为 `cobra.MaximumNArgs(1)`；加 flag 与分流：

```go
var (
	waitCardID   string
	waitSubtree  bool
)
// init() 追加：
//   waitCmd.Flags().StringVar(&waitCardID, "card", "", "账本多路 wait：跟一张卡（配 --subtree 跟整棵树）")
//   waitCmd.Flags().BoolVar(&waitSubtree, "subtree", false, "扩展到子树（后代 + 并入成员，动态）")
```

`RunE` 开头分流（在现有 task 路径之前）：

```go
		if waitCardID != "" {
			if len(args) > 0 {
				return fmt.Errorf("--card 与 task 参数互斥：账本 wait 不指向单 task")
			}
			return runCardWait(cmd, waitCardID, waitSubtree, waitTimeout)
		}
		if len(args) != 1 {
			return fmt.Errorf("需要 task id 参数（或 --card 走账本多路 wait）")
		}
```

同文件追加：

```go
// runCardWait 账本单流多路 wait：从当前 seq 起跟子树事件（每行一个
// JSON 事件到 stdout），全部成员达骨架终态（已完成/终止）即退出 0。
// 成员集每轮重算——wait 挂起期间新拆/新并入的卡天然进流。timeout 是
// 总时长（0=不限），超时退出码 124 与单 task wait 一致。
func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error {
	st, err := openLedger()
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.GetCard(cardID); err != nil {
		return err
	}
	members := func() ([]string, error) {
		if subtree {
			return st.Subtree(cardID)
		}
		return []string{cardID}, nil
	}
	start, err := st.MaxSeq()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	slog.SetDefault(logx.Setup("cli", "")) // 长命命令：stderr 日志是诊断通道
	enc := json.NewEncoder(cmd.OutOrStdout())
	allDone := errors.New("all-done") // 内部哨兵：借 onEvent 的错误通道正常收口
	checkDone := func() (bool, error) {
		ids, err := members()
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			c, err := st.GetCard(id)
			if err != nil {
				return false, err
			}
			if c.Status != ledger.StatusDone && c.Status != ledger.StatusClosed {
				return false, nil
			}
		}
		return true, nil
	}
	// 起跑即查一次：wait 挂上时子树可能已经全完成
	if done, err := checkDone(); err != nil {
		return err
	} else if done {
		fmt.Fprintln(cmd.ErrOrStderr(), "子树已全部完成")
		return nil
	}
	err = st.Follow(ctx, members, start, 2*time.Second, func(e ledger.Event) error {
		if err := enc.Encode(e); err != nil {
			return err
		}
		if e.Type != ledger.EvStatusMoved {
			return nil
		}
		if done, err := checkDone(); err != nil {
			return err
		} else if done {
			return allDone
		}
		return nil
	})
	switch {
	case errors.Is(err, allDone):
		fmt.Fprintln(cmd.ErrOrStderr(), "子树全部完成，wait 退出")
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 超时")}
	default:
		return err
	}
}
```

（import 补 `"context"`、`"errors"`、`"log/slog"`、ledger 包；`exitCodeError`/`ExitTimeout`/`logx` 均既有。）

- [ ] **Step 3: 跑测试确认通过 + Commit**

Run: `go test ./cmd/ -run TestWaitCard -v` → PASS

```bash
git add cmd/
git commit -m "feat(cli): wait --card [--subtree]——账本单流多路 wait，动态成员集，全终态退出/超时 124"
```

---

### Task 6: 整包终审

- [ ] **Step 1: 终审命令**

```bash
gofmt -l internal/ledger/ internal/ledgermirror/ cmd/   # 无输出
go vet ./internal/ledger/ ./internal/ledgermirror/ ./cmd/
go test ./... 2>&1 | tail -20                           # 全仓全绿
```

- [ ] **Step 2: 对照 spec §3 修订版逐条自检**

挂账即订阅（reconcile 从 card_tasks 对账）✓；watermark 库派生 ✓；lease 30s/续约 10s、CAS 抢占、续约失败立即停写 ✓；幂等键（先查后插 + 唯一索引兜底）✓；per-target 健康心跳 + 静默期空 touch ✓；「无 lease 持有者也亮滞后」——健康行 updated_at 只有持有者在 touch，无持有者自然全员变陈，看板侧 60s 判定即覆盖（Plan D 落 UI）✓；工单随镜像入流（permission_request/question 不在 mirrorSkip）✓。

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "test(mirror): 镜像 + 多路 wait 整包终审"
```

---

## Self-Review 记录

1. **Spec 覆盖**：§3 修订版全部落位（Step 2 清单）；「看板亮滞后」的 UI 归 Plan D，数据源（MirrorHealth）本 plan 交付。
2. **类型一致性**：`MirroredEvent`/`TaskLink`/`Source`/`Options` 定义与使用处已互核；`AllTaskLinks` 补充在 tasks.go 并有调用方（reconcile）。
3. **已知妥协**：镜像 payload 用 fmt.Sprintf 拼 JSON（task_type + 原始负载原样嵌入——负载本身是合法 JSON，来源是我们自己的 events 表；若真机发现非 JSON 负载，改 json.Marshal 包一层）；Follow 的 PG LISTEN 断连后靠兜底轮询降级（不重建 LISTEN 连接，wait 是短命进程，降级可接受，注释写明）；`checkDone` 只在 status_moved 事件后触发 + 起跑一次（其余事件不可能改变终态判定）。
4. **真机判据归属（审核者本地，不派发）**：⑤ 多路 wait 不漏（真派发 + wait 挂起期间 split/dispatch）；⑦ 断链恢复（停 target agentd ≥60s）；⑩ 双协调机 lease 切换（本机 + mac-02 指同一 PG）。单测覆盖的是它们的库层/子系统形（幂等重放、lease 排他、动态成员）。

## 与 Plan A/A2 的接缝

- 依赖 Plan A 的：mutate/appendEvent/EventsFromAsc/Subtree/LinkTask/card_tasks 表。
- 依赖 Plan A2 的：`openLedger`/`runLedgerCLI`（wait --card 的 CLI 底座与测试基座）。
- 给 Plan C 的：`AllTaskLinks`、镜像事件里的 review verdict 原文（节点执行器解析 handoff-verdict 用）。
- 给 Plan D 的：`MirrorHealth`、`Holding`、单流查询。
