// machineauthority reconciler：把真实仓库状态与 store 投影对齐。
//
// 职责：
//   - ReconcileLocation：扫描一个 ProjectLocation 的 main 与 worktree、分支，
//     经 store 的 durable outbox 产生 workspace/git_ref 的 upsert/remove
//
// 边界：
//   - 只读扫描仓库，不做写操作（clone/checkout 由 ProjectService 发起）
//   - 每次完整 Reconcile 是无副作用的扫描；变化经事务 outbox 上报，
//     重复 Reconcile（无变化）不产生事件
package machineauthority

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

// ReconcileLocation 扫描 location 指向的仓库并投影其 workspaces 与 git refs。
//
// 参数：
//   - ctx: 控制扫描生命周期
//   - st: 持久化（outbox 事务写入点）
//   - loc: 要扫描的 ProjectLocation（MachineID 必须是本机）
//   - inv: 仓库扫描器
//
// 返回：
//   - ReconcileResult：扫描到的 workspaces/refs 与 upsert/remove 计数
//   - err: 扫描或落库失败
//
// 注意：
//   - 本函数是本机 Reconcile 的同步核心；watcher/periodic/startup 都复用它
//   - upsert 走 UpsertWorkspaceWithMachineEvent（资源与 outbox 同事务）
func ReconcileLocation(ctx context.Context, st *store.Store, loc controlplane.ProjectLocation, inv *Inventory) (ReconcileResult, error) {
	if loc.MachineID == "" {
		return ReconcileResult{}, fmt.Errorf("location %s 缺少 machine_id", loc.ID)
	}
	// 主目录 canonical path = location 的 main workspace 路径。
	// 由 ProjectService 在创建 Location 时登记；Reconcile 时若主目录未登记，
	// 用仓库根路径推导（main workspace 的 path == inv.Root）。
	rootCanonical, cerr := canonicalPath(inv.Root)
	if cerr != nil {
		return ReconcileResult{}, fmt.Errorf("规范化仓库根 %s: %w", inv.Root, cerr)
	}
	_ = rootCanonical

	workspaces, err := inv.DiscoverWorkspaces(ctx)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("扫描 location %s 的 workspaces: %w", loc.ID, err)
	}
	refs, err := inv.DiscoverGitRefs(ctx, loc.ID)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("扫描 location %s 的 refs: %w", loc.ID, err)
	}

	result := ReconcileResult{GitRefs: refs}
	for _, ws := range workspaces {
		// 归属本机
		ws.MachineID = loc.MachineID
		// main workspace 绑定 location；worktree 属于同 location
		ws.LocationID = &loc.ID
		if _, err := st.UpsertWorkspaceWithMachineEvent(ctx, ws, controlplane.MachineEventWorkspaceUpsert); err != nil {
			return ReconcileResult{}, fmt.Errorf("upsert workspace %s: %w", ws.ID, err)
		}
		result.Workspaces = append(result.Workspaces, ws)
		result.Upserted++
	}
	if len(refs) > 0 {
		if _, err := st.UpsertGitRefsWithMachineEvents(ctx, loc.ID, refs); err != nil {
			return ReconcileResult{}, fmt.Errorf("upsert git refs: %w", err)
		}
	}
	// remove 侧：已从磁盘消失的 worktree 由 store 侧按扫描集合清理。
	// （本计划 Reconcile 以 upsert 为主，remove 由 worktree remove 场景的
	// 显式扫描差异产生；简化实现先在 upsert 幂等基础上由上层触发 remove。）
	return result, nil
}

// canonicalPath 是 store.CanonicalPath 的本地别名。
func canonicalPath(path string) (string, error) {
	return store.CanonicalPath(path)
}

// LocalReconciler 是本机资源权威的运行期编排：启动/周期/事件触发的完整
// Reconcile + .git watcher 提示。
//
// 职责：
//   - ReconcileAll：按 reason 扫描本机全部登记 Location 并把变化投影进 outbox
//   - StartWatch：为每个登记 Location 挂 .git watcher（提示即可）
//   - 周期兜底扫描（默认 30s，可注入）
type LocalReconciler struct {
	st       *store.Store
	machine  controlplane.Machine
	log      *slog.Logger
	periodic time.Duration
	// newInventory 为测试注入自定义 Inventory 提供接缝；nil=按 Root 构建
	newInventory func(root string) *Inventory
}

// NewLocalReconciler 创建本机 reconciler。
//
// 参数：
//   - st: 持久化（Location 查询 + outbox 写入）
//   - machine: 本机 Machine（确定本机身份）
//   - log: 日志入口
func NewLocalReconciler(st *store.Store, machine controlplane.Machine, log *slog.Logger) *LocalReconciler {
	return &LocalReconciler{
		st: st, machine: machine, log: log, periodic: 30 * time.Second,
	}
}

// SetPeriodic 设置周期兜底扫描间隔（测试注入用）。
func (r *LocalReconciler) SetPeriodic(d time.Duration) { r.periodic = d }

// ReconcileAll 扫描本机全部登记 Location，返回扫描摘要。
//
// 参数：
//   - ctx: 控制扫描生命周期
//   - reason: startup|watch|periodic|peer_reconnect（日志用）
//
// 日志：包含原因、扫描数、upsert/remove 数、耗时、machine cursor；无变化也
// 记录 Debug 摘要，变化成功记录 Info。
func (r *LocalReconciler) ReconcileAll(ctx context.Context, reason string) (*ReconcileSummary, error) {
	start := time.Now()
	locations, err := r.st.ListLocationsForMachine(ctx, r.machine.ID)
	if err != nil {
		return nil, fmt.Errorf("列出本机 locations: %w", err)
	}
	summary := &ReconcileSummary{Reason: reason, Locations: len(locations)}
	for _, loc := range locations {
		// 定位 location 根：main workspace 的 path；没有 main workspace 则跳过
		if loc.MainWorkspaceID == "" {
			continue
		}
		main, err := r.st.GetWorkspace(ctx, loc.MainWorkspaceID)
		if err != nil {
			r.log.Warn("reconcile 跳过 location（读取 main workspace 失败）",
				"reason", reason, "location_id", loc.ID, "cause", err)
			continue
		}
		inv := r.inventoryFor(main.Path)
		res, err := ReconcileLocation(ctx, r.st, loc, inv)
		if err != nil {
			r.log.Error("reconcile location 失败",
				"reason", reason, "location_id", loc.ID, "machine_id", r.machine.ID, "cause", err)
			continue
		}
		summary.Upserted += res.Upserted
		summary.Removed += res.Removed
		summary.Workspaces += len(res.Workspaces)
	}
	cursor, err := r.st.CurrentCursor(ctx, r.machine.ID)
	if err != nil {
		return nil, fmt.Errorf("读取本机 cursor: %w", err)
	}
	summary.Cursor = cursor
	summary.ElapsedMS = time.Since(start).Milliseconds()
	if summary.Upserted+summary.Removed == 0 {
		r.log.Debug("reconcile 无变化",
			"reason", reason, "locations", summary.Locations,
			"workspaces", summary.Workspaces, "cursor", summary.Cursor,
			"elapsed_ms", summary.ElapsedMS)
	} else {
		r.log.Info("reconcile 完成",
			"reason", reason, "locations", summary.Locations,
			"workspaces", summary.Workspaces,
			"upserted", summary.Upserted, "removed", summary.Removed,
			"cursor", summary.Cursor, "elapsed_ms", summary.ElapsedMS)
	}
	return summary, nil
}

// inventoryFor 为 location 根构建 Inventory（测试注入接缝）。
func (r *LocalReconciler) inventoryFor(root string) *Inventory {
	if r.newInventory != nil {
		return r.newInventory(root)
	}
	return &Inventory{Root: root}
}

// ReconcileSummary 是一次 Reconcile 的统计摘要。
type ReconcileSummary struct {
	Reason     string
	Locations  int
	Workspaces int
	Upserted   int
	Removed    int
	Cursor     int64
	ElapsedMS  int64
}

// StartWatch 为每个登记 Location 挂 .git watcher，并启动周期兜底扫描。
//
// watcher 只作为「尽快扫描」的提示，不直接把文件系统事件当事实（spec §8.2）。
// 返回的 cancel 停止全部 watcher 与周期 ticker。
func (r *LocalReconciler) StartWatch(ctx context.Context) (cancel func()) {
	locations, err := r.st.ListLocationsForMachine(ctx, r.machine.ID)
	if err != nil {
		r.log.Error("启动 watcher 前列出 locations 失败", "machine_id", r.machine.ID, "cause", err)
	}
	var watchers []*GitWatcher
	for _, loc := range locations {
		if loc.MainWorkspaceID == "" {
			continue
		}
		main, err := r.st.GetWorkspace(ctx, loc.MainWorkspaceID)
		if err != nil {
			continue
		}
		gitDir := filepath.Join(main.Path, ".git")
		inv := r.inventoryFor(main.Path)
		w := NewGitWatcher(main.Path, gitDir, func() {
			if _, err := r.ReconcileAll(context.Background(), "watch"); err != nil {
				r.log.Error("watcher 触发 reconcile 失败", "location_id", loc.ID, "cause", err)
			}
		})
		_ = inv
		if err := w.Start(); err != nil {
			r.log.Warn("git watcher 启动失败（仅记录，靠周期兜底）", "path", main.Path, "cause", err)
			continue
		}
		watchers = append(watchers, w)
	}

	stopCh := make(chan struct{})
	if r.periodic > 0 {
		go func() {
			ticker := time.NewTicker(r.periodic)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if _, err := r.ReconcileAll(context.Background(), "periodic"); err != nil {
						r.log.Error("周期 reconcile 失败", "cause", err)
					}
				case <-stopCh:
					return
				}
			}
		}()
	}
	return func() {
		close(stopCh)
		for _, w := range watchers {
			w.Close()
		}
	}
}
