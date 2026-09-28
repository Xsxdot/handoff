// unlinked_summary_test.go verifies that /api/cards stays on the local ledger
// path while remote target summaries refresh independently in the background.
package agentd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

func TestCardsListDoesNotWaitForSlowTargetSummary(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			_ = json.NewEncoder(w).Encode([]proto.TaskView{{Task: proto.Task{
				ID: "remote-task", Name: "慢目标中的未挂账任务", State: proto.TaskStateRunning,
			}}})
		case <-r.Context().Done():
		}
	}))
	addr := strings.TrimPrefix(remote.URL, "http://")
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{
		"slow": {Addr: addr, Token: testToken},
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = env.srv.CloseTargets()
	})

	startedAt := time.Now()
	code, body := getCardsWithTimeout(t, env.testAgentdEnv, 300*time.Millisecond)
	if elapsed := time.Since(startedAt); elapsed >= 250*time.Millisecond {
		t.Fatalf("GET /api/cards waited %s for remote target; want <250ms: %s", elapsed, body)
	}
	if code != http.StatusOK {
		t.Fatalf("GET /api/cards status=%d body=%s", code, body)
	}
	var first struct {
		Cards    []map[string]any `json:"cards"`
		Unlinked struct {
			Status        string   `json:"status"`
			ObservedAt    *string  `json:"observed_at"`
			Count         int      `json:"count"`
			Tasks         []any    `json:"tasks"`
			UnknownTarget []string `json:"unknown_targets"`
		} `json:"unlinked"`
	}
	if err := json.Unmarshal([]byte(body), &first); err != nil {
		t.Fatalf("decode first cards response: %v; body=%s", err, body)
	}
	if len(first.Cards) != 0 {
		t.Fatalf("local card result changed: %+v", first.Cards)
	}
	if first.Unlinked.Status != "unavailable" || first.Unlinked.ObservedAt != nil ||
		first.Unlinked.Count != 0 || first.Unlinked.Tasks == nil || len(first.Unlinked.Tasks) != 0 ||
		len(first.Unlinked.UnknownTarget) != 1 || first.Unlinked.UnknownTarget[0] != "slow" {
		t.Fatalf("first response must distinguish not-yet-observed from a valid zero: %+v", first.Unlinked)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background target refresh did not start")
	}
	startedAt = time.Now()
	if code, body = getCardsWithTimeout(t, env.testAgentdEnv, 300*time.Millisecond); code != http.StatusOK {
		t.Fatalf("second local cards read should remain available during refresh: %d %s", code, body)
	}
	if elapsed := time.Since(startedAt); elapsed >= 250*time.Millisecond {
		t.Fatalf("second GET /api/cards waited %s behind the in-flight refresh", elapsed)
	}
	releaseOnce.Do(func() { close(release) })
	waitForUnlinkedStatus(t, env, "latest")
}

func TestUnlinkedRefreshIsSingleFlightAndIndependentOfRequestLifetime(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var calls atomic.Int32
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			_ = json.NewEncoder(w).Encode([]proto.TaskView{{Task: proto.Task{
				ID: "detached-task", Name: "独立刷新", State: proto.TaskStateRunning,
			}}})
		case <-r.Context().Done():
			canceled <- struct{}{}
		}
	}))
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{
		"slow": {Addr: strings.TrimPrefix(remote.URL, "http://"), Token: testToken},
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = env.srv.CloseTargets()
	})

	const initialReaders = 12
	startRequests := make(chan struct{})
	initialResults := make(chan error, initialReaders)
	for range initialReaders {
		go func() {
			<-startRequests
			code, body, err := doCardsWithTimeout(env.testAgentdEnv, 300*time.Millisecond)
			if err != nil {
				initialResults <- err
				return
			}
			if code != http.StatusOK {
				initialResults <- fmt.Errorf("initial cards response: %d %s", code, body)
				return
			}
			initialResults <- nil
		}()
	}
	close(startRequests)
	for range initialReaders {
		if err := <-initialResults; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	// The HTTP response has completed; close/cancel its client side while the
	// remote request is still blocked. The worker must remain server-scoped.
	for i := 0; i < 12; i++ {
		if code, body := getCardsWithTimeout(t, env.testAgentdEnv, 300*time.Millisecond); code != http.StatusOK {
			t.Fatalf("concurrent cards request %d: %d %s", i, code, body)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent readers started %d target requests, want one", got)
	}
	select {
	case <-canceled:
		t.Fatal("request-lifetime cancellation reached the detached worker")
	case <-time.After(25 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	waitForUnlinkedStatus(t, env, "latest")
}

func TestCloseTargetsCancelsAndJoinsUnlinkedRefresh(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{})
	var calls atomic.Int32
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
		close(canceled)
	}))
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{
		"blocked": {Addr: strings.TrimPrefix(remote.URL, "http://"), Token: testToken},
	})
	code, body := getCardsWithTimeout(t, env.testAgentdEnv, 300*time.Millisecond)
	if code != http.StatusOK {
		t.Fatalf("initial cards response: %d %s", code, body)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- env.srv.CloseTargets() }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("CloseTargets did not cancel the in-flight target request")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("CloseTargets: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseTargets returned before the refresh worker joined")
	}
	if err := env.srv.CloseTargets(); err != nil {
		t.Fatalf("second CloseTargets: %v", err)
	}
	_ = env.srv.unlinkedSummary()
	time.Sleep(25 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh restarted after close: target calls=%d", got)
	}
}

func TestUnlinkedRefreshDeadlinePublishesSuccessfulSubset(t *testing.T) {
	fast := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]proto.TaskView{{Task: proto.Task{
			ID: "fast-task", Name: "快目标", State: proto.TaskStateRunning,
		}}})
	}))
	slowStarted := make(chan struct{}, 1)
	slow := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		slowStarted <- struct{}{}
		<-r.Context().Done()
	}))
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{
		"fast": {Addr: strings.TrimPrefix(fast.URL, "http://"), Token: testToken},
		"slow": {Addr: strings.TrimPrefix(slow.URL, "http://"), Token: testToken},
	})
	defer env.srv.CloseTargets()
	startedAt := time.Now()
	code, body := getCardsWithTimeout(t, env.testAgentdEnv, 300*time.Millisecond)
	if code != http.StatusOK {
		t.Fatalf("initial cards response: %d %s", code, body)
	}
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow target refresh did not start alongside fast target")
	}
	waitForUnlinkedStatusWithin(t, env, "partial", 3*time.Second)
	if elapsed := time.Since(startedAt); elapsed > 3*time.Second {
		t.Fatalf("bounded fanout took %s, want the 2s total deadline plus scheduling tolerance", elapsed)
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("GET /api/cards status=%d body=%s", code, body)
	}
	var response struct {
		Unlinked struct {
			Status string `json:"status"`
			Count  int    `json:"count"`
			Tasks  []struct {
				TaskID string `json:"task_id"`
			} `json:"tasks"`
			UnknownTarget []string `json:"unknown_targets"`
		} `json:"unlinked"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode /api/cards: %v; body=%s", err, body)
	}
	if response.Unlinked.Status != "partial" || response.Unlinked.Count != 1 || len(response.Unlinked.Tasks) != 1 ||
		response.Unlinked.Tasks[0].TaskID != "fast-task" || len(response.Unlinked.UnknownTarget) != 1 ||
		response.Unlinked.UnknownTarget[0] != "slow" {
		t.Fatalf("deadline must preserve the successful subset and identify the slow target: %+v", response.Unlinked)
	}
}

func TestUnlinkedSummaryKeepsLongExpiredSnapshotVisible(t *testing.T) {
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{
		"mac-02": {Addr: "127.0.0.1:1", Token: testToken},
	})
	defer env.srv.CloseTargets()
	snapshotTime := time.Now().Add(-10 * time.Minute).UTC()
	env.srv.unlinkedCache = &unlinkedSummarySnapshot{
		Tasks:            []map[string]any{{"target": "mac-02", "task_id": "old-task", "title": "old", "state": proto.TaskStateRunning}},
		TargetConfigs:    map[string]config.Target{"mac-02": {Addr: "127.0.0.1:1", Token: testToken}},
		SuccessfulTarget: map[string]struct{}{"mac-02": {}}, ObservedAt: snapshotTime,
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("GET /api/cards status=%d body=%s", code, body)
	}
	var response struct {
		Unlinked struct {
			Status     string `json:"status"`
			ObservedAt string `json:"observed_at"`
			Count      int    `json:"count"`
			Tasks      []struct {
				TaskID string `json:"task_id"`
			} `json:"tasks"`
			Unknown []string `json:"unknown_targets"`
		} `json:"unlinked"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode stale /api/cards response: %v; body=%s", err, body)
	}
	if response.Unlinked.Status != "stale" || response.Unlinked.Count != 1 || response.Unlinked.ObservedAt != snapshotTime.Format(time.RFC3339Nano) ||
		len(response.Unlinked.Tasks) != 1 || response.Unlinked.Tasks[0].TaskID != "old-task" || len(response.Unlinked.Unknown) != 0 {
		t.Fatalf("ten-minute old data should remain visible and labeled stale: %+v", response.Unlinked)
	}
}

func TestUnlinkedSummaryInvalidatesSameNameTargetAfterConfigChange(t *testing.T) {
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]proto.TaskView{{Task: proto.Task{
			ID: "old-endpoint-task", Name: "旧 target 配置", State: proto.TaskStateRunning,
		}}})
	}))
	oldTarget := config.Target{Addr: strings.TrimPrefix(remote.URL, "http://"), Token: testToken}
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{"remote": oldTarget})
	defer env.srv.CloseTargets()
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards"); code != http.StatusOK {
		t.Fatalf("首个 /api/cards: status=%d body=%s", code, body)
	}
	waitForUnlinkedStatus(t, env, "latest")
	_, oldBody := ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if !strings.Contains(oldBody, "old-endpoint-task") {
		t.Fatalf("旧 target 应先产生可识别的历史快照: %s", oldBody)
	}

	updated := *env.srv.conf()
	updated.Targets = map[string]config.Target{"remote": {Addr: "127.0.0.1:1", Token: "new-token"}}
	env.srv.cfg.Store(&updated)
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("target 配置更新后的 /api/cards: status=%d body=%s", code, body)
	}
	var response struct {
		Unlinked struct {
			Status     string   `json:"status"`
			ObservedAt *string  `json:"observed_at"`
			Count      int      `json:"count"`
			Tasks      []any    `json:"tasks"`
			Unknown    []string `json:"unknown_targets"`
		} `json:"unlinked"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode changed-target /api/cards: %v; body=%s", err, body)
	}
	if response.Unlinked.Status != "unavailable" || response.Unlinked.ObservedAt != nil || response.Unlinked.Count != 0 ||
		len(response.Unlinked.Tasks) != 0 || len(response.Unlinked.Unknown) != 1 || response.Unlinked.Unknown[0] != "remote" ||
		strings.Contains(body, "old-endpoint-task") {
		t.Fatalf("同名 target 配置变更不得将旧观测标成当前: %+v body=%s", response.Unlinked, body)
	}
}

func TestProjectUnlinkedSnapshotRequiresExactCurrentTargetObservation(t *testing.T) {
	now := time.Now().UTC()
	oldTarget := config.Target{Addr: "127.0.0.1:7777", Token: "old-token"}
	snapshot := &unlinkedSummarySnapshot{
		Tasks:            []map[string]any{{"target": "remote", "task_id": "old-task"}},
		TargetConfigs:    map[string]config.Target{"remote": oldTarget},
		SuccessfulTarget: map[string]struct{}{"remote": {}},
		ObservedAt:       now,
	}

	for name, current := range map[string]config.Target{
		"renamed target":             {Addr: "127.0.0.1:7778", Token: "new-token"},
		"same name changed endpoint": {Addr: "127.0.0.1:7778", Token: "old-token"},
		"same name changed token":    {Addr: "127.0.0.1:7777", Token: "new-token"},
	} {
		t.Run(name, func(t *testing.T) {
			status, observedAt, rows, unknown := projectUnlinkedSnapshot(snapshot, map[string]config.Target{"remote": current}, now)
			if status != "unavailable" || !observedAt.IsZero() || len(rows) != 0 || len(unknown) != 1 || unknown[0] != "remote" {
				t.Fatalf("old configuration must not qualify current target observation: status=%q observed=%v rows=%v unknown=%v", status, observedAt, rows, unknown)
			}
		})
	}
	failed := *snapshot
	failed.SuccessfulTarget = map[string]struct{}{}
	status, observedAt, rows, unknown := projectUnlinkedSnapshot(&failed, map[string]config.Target{"remote": oldTarget}, now)
	if status != "unavailable" || !observedAt.IsZero() || len(rows) != 0 || len(unknown) != 1 {
		t.Fatalf("没有任何成功 target 观测时必须 unavailable: status=%q observed=%v rows=%v unknown=%v", status, observedAt, rows, unknown)
	}
}

func TestProjectUnlinkedSnapshotAllowsObservedSubsetAfterTargetRemoval(t *testing.T) {
	now := time.Now().UTC()
	kept := config.Target{Addr: "127.0.0.1:7777", Token: "kept-token"}
	removed := config.Target{Addr: "127.0.0.1:7778", Token: "removed-token"}
	snapshot := &unlinkedSummarySnapshot{
		Tasks: []map[string]any{
			{"target": "kept", "task_id": "kept-task"},
			{"target": "removed", "task_id": "removed-task"},
		},
		TargetConfigs:    map[string]config.Target{"kept": kept, "removed": removed},
		SuccessfulTarget: map[string]struct{}{"kept": {}, "removed": {}},
		ObservedAt:       now,
	}
	status, observedAt, rows, unknown := projectUnlinkedSnapshot(snapshot, map[string]config.Target{"kept": kept}, now)
	if status != "latest" || !observedAt.Equal(now) || len(rows) != 1 || rows[0]["task_id"] != "kept-task" || len(unknown) != 0 {
		t.Fatalf("removed targets must not contaminate the still-complete current subset: status=%q observed=%v rows=%v unknown=%v", status, observedAt, rows, unknown)
	}
}

func TestUnlinkedSummaryNoTargetsReturnsImmediateLatestEmpty(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	defer env.srv.CloseTargets()
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("initial GET /api/cards status=%d body=%s", code, body)
	}
	var response struct {
		Unlinked struct {
			Status     string   `json:"status"`
			ObservedAt *string  `json:"observed_at"`
			Count      int      `json:"count"`
			Tasks      []any    `json:"tasks"`
			Unknown    []string `json:"unknown_targets"`
		} `json:"unlinked"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode latest empty summary: %v; body=%s", err, body)
	}
	if response.Unlinked.Status != "latest" || response.Unlinked.ObservedAt == nil || response.Unlinked.Count != 0 ||
		response.Unlinked.Tasks == nil || len(response.Unlinked.Tasks) != 0 || response.Unlinked.Unknown == nil || len(response.Unlinked.Unknown) != 0 {
		t.Fatalf("no configured targets must be an immediate valid empty result: %+v", response.Unlinked)
	}
}

func TestCloseTargetsCancelsKeyReadBlockedByPostgresLock(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 agentd + PostgreSQL 关停收敛测试")
	}
	probe, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("连接隔离 PostgreSQL 测试库: %v", err)
	}
	defer probe.Close()
	var databaseName string
	if err := probe.QueryRowContext(t.Context(), `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatalf("确认 PostgreSQL 数据库名: %v", err)
	}
	if databaseName != "handoff_b409_test" {
		t.Fatalf("拒绝对非隔离 PostgreSQL 库执行锁测试: database=%q", databaseName)
	}

	pgLedger, err := ledger.Open(dsn)
	if err != nil {
		t.Fatalf("打开隔离 PostgreSQL ledger: %v", err)
	}
	t.Cleanup(func() { _ = pgLedger.Close() })
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{
		"blocked": {Addr: "127.0.0.1:1", Token: testToken},
	})
	defer env.srv.CloseTargets()
	env.srv.SetLedger(pgLedger)

	lockConn, err := probe.Conn(t.Context())
	if err != nil {
		t.Fatalf("获取 PostgreSQL 锁连接: %v", err)
	}
	defer lockConn.Close()
	lockTx, err := lockConn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("开启 PostgreSQL 锁事务: %v", err)
	}
	defer lockTx.Rollback()
	if _, err := lockTx.ExecContext(t.Context(), `LOCK TABLE card_tasks IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("锁住未挂账摘要读取表: %v", err)
	}

	started := time.Now()
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("锁等待期间 /api/cards 仍须先返回本地列表: status=%d body=%s", code, body)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("本地卡列表被远端摘要的 PostgreSQL 锁等待阻塞: %s", elapsed)
	}

	blockedDeadline := time.Now().Add(2 * time.Second)
	blocked := false
	for time.Now().Before(blockedDeadline) {
		var waiting int
		if err := probe.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database() AND state = 'active' AND wait_event_type = 'Lock'
			AND query LIKE 'SELECT target, task_id FROM card_tasks%'`).Scan(&waiting); err != nil {
			t.Fatalf("确认 agentd key-only 读取进入 PostgreSQL 锁等待: %v", err)
		}
		if waiting > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("agentd 未在 PostgreSQL card_tasks 锁上等待，无法验证关停取消")
	}

	closed := make(chan error, 1)
	closeStarted := time.Now()
	go func() { closed <- env.srv.CloseTargets() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("CloseTargets 关闭池失败: %v", err)
		}
		if elapsed := time.Since(closeStarted); elapsed > 2*time.Second {
			t.Fatalf("PostgreSQL 锁等待中的刷新未在关停期限内收敛: %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CloseTargets 未能取消并 join 被 PostgreSQL 锁阻塞的摘要 worker")
	}
	env.srv.unlinkedMu.Lock()
	refreshing := env.srv.unlinkedRefreshing
	env.srv.unlinkedMu.Unlock()
	if refreshing {
		t.Fatal("CloseTargets 返回后摘要 worker 仍标记为运行中")
	}
}

func TestUnlinkedSnapshotStatusThirtySecondBoundary(t *testing.T) {
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	if got := unlinkedSnapshotStatus(now.Add(-unlinkedSummaryTTL), now, false); got != "latest" {
		t.Fatalf("exact 30s observation should remain latest, got %q", got)
	}
	if got := unlinkedSnapshotStatus(now.Add(-unlinkedSummaryTTL-time.Nanosecond), now, false); got != "stale" {
		t.Fatalf("older than 30s observation should be stale, got %q", got)
	}
	if got := unlinkedSnapshotStatus(now, now, true); got != "partial" {
		t.Fatalf("fresh observation with unknown targets should be partial, got %q", got)
	}
}

func TestUnlinkedSummaryAllTargetsFailedRemainsUnavailable(t *testing.T) {
	var calls atomic.Int32
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{
		"offline": {Addr: strings.TrimPrefix(remote.URL, "http://"), Token: testToken},
	})
	defer env.srv.CloseTargets()
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards"); code != http.StatusOK {
		t.Fatalf("initial GET /api/cards status=%d body=%s", code, body)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		env.srv.unlinkedMu.Lock()
		refreshing := env.srv.unlinkedRefreshing
		env.srv.unlinkedMu.Unlock()
		if !refreshing && calls.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("GET /api/cards status=%d body=%s", code, body)
	}
	var response struct {
		Unlinked struct {
			Status     string   `json:"status"`
			ObservedAt *string  `json:"observed_at"`
			Count      int      `json:"count"`
			Tasks      []any    `json:"tasks"`
			Unknown    []string `json:"unknown_targets"`
		} `json:"unlinked"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode unavailable summary: %v; body=%s", err, body)
	}
	if response.Unlinked.Status != "unavailable" || response.Unlinked.ObservedAt != nil || response.Unlinked.Count != 0 ||
		response.Unlinked.Tasks == nil || len(response.Unlinked.Tasks) != 0 || len(response.Unlinked.Unknown) != 1 || response.Unlinked.Unknown[0] != "offline" {
		t.Fatalf("all target failures must remain unavailable rather than become zero: %+v", response.Unlinked)
	}
	if calls.Load() != 1 {
		t.Fatalf("unavailable retry throttling allowed %d target calls in one cache window", calls.Load())
	}
}

func getCardsWithTimeout(t *testing.T, env *testAgentdEnv, timeout time.Duration) (int, string) {
	t.Helper()
	code, body, err := doCardsWithTimeout(env, timeout)
	if err != nil {
		t.Fatalf("GET /api/cards: %v", err)
	}
	return code, body
}

func doCardsWithTimeout(env *testAgentdEnv, timeout time.Duration) (int, string, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+"/api/cards", nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, string(data), nil
}

func waitForUnlinkedStatus(t *testing.T, env *ledgerEnv, want string) {
	t.Helper()
	waitForUnlinkedStatusWithin(t, env, want, time.Second)
}

func waitForUnlinkedStatusWithin(t *testing.T, env *ledgerEnv, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards")
		if code != http.StatusOK {
			t.Fatalf("GET /api/cards status=%d body=%s", code, body)
		}
		var response struct {
			Unlinked struct {
				Status string `json:"status"`
			} `json:"unlinked"`
		}
		if err := json.Unmarshal([]byte(body), &response); err != nil {
			t.Fatalf("decode unlinked status: %v; body=%s", err, body)
		}
		if response.Unlinked.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("/api/cards did not reach unlinked status %q", want)
}
