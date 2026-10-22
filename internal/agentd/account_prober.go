// account_prober.go —— 所在机账号额度探测器（B453 S2，plan Q3）。
//
// 职责：只探测「所在机 = 本机」的账号——周期扫描（到期 = 上次尝试 + 账号间隔）与
// 立即探测（ProbeNow）共用同一实例与同一条写路径；同一账号单飞（不并发探测），
// 立即探测写入的 last_attempt_at 自然顺延下一次周期探测。读本机凭据、调额度接口
// 由 internal/quota 完成；结果映射成编制域值类型后经 AccountsClient.RecordProbe
// 读-合并-CAS 写进共享账本的读数行。
//
// 边界：
//   - 不代探他机账号（凭据只在所在机）；不碰账号定义行；
//   - 调度状态全在账本读数行（last_attempt_at），进程内只有在飞集合——重启后按
//     读数行重建节拍，不突发全量重探；
//   - 失败按 30s×2ⁿ 退避、封顶为账号间隔（probeDue）；
//   - 生命周期跟 cmd/agentd.go 既有周期任务同形：ticker + ctx，Stop() 取消并等
//     扫描循环与在飞探测全部退出。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

const (
	// accountProbeScan 是扫描节拍：发现新登记/改间隔的账号的最大延迟（plan §6 ≤ 60s）。
	accountProbeScan = 30 * time.Second
	// accountProbeConcurrency 是同时在飞的探测上限（plan §6 ≥ 2）。
	accountProbeConcurrency = 2
	// accountProbeBackoffBase 是失败退避的起点（30s×2ⁿ，封顶为账号间隔）。
	accountProbeBackoffBase = 30 * time.Second
	// accountProbeBudget 是单次探测的总时限上界；适配器自身 HTTP 超时更短（20s），agy 40s。
	accountProbeBudget = 60 * time.Second
)

// ErrAccountNotLocal 表示要求本机探测的账号不在本机。
var ErrAccountNotLocal = errors.New("账号不在本机")

// ErrAccountProberStopped 表示探测器未启动或已停止。
var ErrAccountProberStopped = errors.New("账号探测器未运行")

// QuotaProber 是探测器对额度适配子系统的使用方接口（实现：*quota.Registry）。
type QuotaProber interface {
	Probe(ctx context.Context, req quota.Request) (quota.Snapshot, error)
}

// AccountProberConfig 是探测器的装配参数，全部由组装点（cmd/agentd.go）注入。
type AccountProberConfig struct {
	Accounts   AccountsClient               // 读账号、写读数（共享账本）
	Quota      QuotaProber                  // 按 kind 分派的额度适配
	IsSelf     func(machine string) bool    // 账号所在机是否本机（Server.IsSelfMachine）
	Writer     string                       // 写者标识：本机自名
	ExpandHome func(string) (string, error) // ~ 展开（hostapi.ExpandHomePath）
	Proxy      func() string                // 账号未填代理时的回落：活配置 proxy:（plan Q5）
	Logger     *slog.Logger
}

// AccountProber 是所在机探测器。零值不可用，经 NewAccountProber 构造。
type AccountProber struct {
	cfg       AccountProberConfig
	log       *slog.Logger
	scanEvery time.Duration
	now       func() time.Time

	mu       sync.Mutex
	inflight map[string]*probeFlight // 账号名 → 在飞探测（单飞）
	sem      chan struct{}
	ctx      context.Context // Start 后的生命周期 ctx；nil = 未运行
	cancel   context.CancelFunc
	stopped  bool
	wg       sync.WaitGroup
}

// NewAccountProber 构造探测器（不启动）。Start 之前 ProbeNow 返回 ErrAccountProberStopped。
func NewAccountProber(cfg AccountProberConfig) *AccountProber {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AccountProber{
		cfg: cfg, log: logger.With("component", "account_prober"),
		scanEvery: accountProbeScan, now: time.Now,
		inflight: map[string]*probeFlight{}, sem: make(chan struct{}, accountProbeConcurrency),
	}
}

// Start 启动周期扫描（启动即扫一次）。ctx 取消或 Stop 后退出。重复调用只生效一次。
func (p *AccountProber) Start(parent context.Context) {
	p.mu.Lock()
	if p.ctx != nil || p.stopped {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	p.ctx, p.cancel = ctx, cancel
	p.wg.Add(1)
	p.mu.Unlock()
	p.log.Info("账号探测器已启动", "writer", p.cfg.Writer, "scan", p.scanEvery,
		"concurrency", cap(p.sem))
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.scanEvery)
		defer ticker.Stop()
		for {
			p.scan(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop 取消生命周期并等扫描循环与在飞探测退出。未启动时无操作；可重复调用。
func (p *AccountProber) Stop() {
	p.mu.Lock()
	cancel := p.cancel
	already := p.stopped
	p.stopped = true
	p.mu.Unlock()
	if cancel == nil || already {
		return
	}
	cancel()
	p.wg.Wait()
	p.log.Info("账号探测器已停止", "writer", p.cfg.Writer)
}

// scan 扫一遍账号：本机、到期、不在飞、有空位的发起探测；其余等下一轮。
func (p *AccountProber) scan(ctx context.Context) {
	views, err := p.cfg.Accounts.List()
	if err != nil {
		p.log.Warn("账号探测扫描：列账号失败", "cause", err)
		return
	}
	now := p.now()
	due := 0
	for _, v := range views {
		if ctx.Err() != nil {
			return
		}
		if !p.cfg.IsSelf(v.Account.Machine) || !probeDue(v, now) {
			continue
		}
		due++
		select {
		case p.sem <- struct{}{}:
		default:
			p.log.Debug("账号探测并发已满，留待下一轮", "account", v.Account.Name)
			continue
		}
		flight, started := p.claim(v.Account.Name)
		if !started {
			<-p.sem // 已在飞（立即探测正在跑），本轮不重复
			continue
		}
		p.wg.Add(1)
		go func(a accounts.Account, streak int) {
			defer p.wg.Done()
			defer func() { <-p.sem }()
			p.release(a.Name, flight, p.probeOnce(ctx, a, streak, "periodic"))
		}(v.Account, v.Record.FailureStreak)
	}
	if due > 0 {
		p.log.Debug("账号探测扫描完成", "accounts", len(views), "due", due)
	}
}

// probeDue：从未尝试 → 到期；否则上次尝试 + retryWait 已过。节拍全由读数行推出，
// 重启后自然续上，不突发全量重探。
func probeDue(v accounts.View, now time.Time) bool {
	last := v.Record.LastAttemptAt
	if last == nil {
		return true
	}
	return !now.Before(last.Add(retryWait(v.Account, v.Record)))
}

// retryWait：最近一次成功 → 账号间隔；最近一次失败 → 30s×2^(连续失败数−1)，封顶为
// 间隔（spec S2）。
func retryWait(a accounts.Account, rec accounts.ReadingRecord) time.Duration {
	interval := time.Duration(a.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = accounts.DefaultIntervalMinutes * time.Minute
	}
	if rec.LastError == nil || rec.FailureStreak <= 0 {
		return interval
	}
	wait := accountProbeBackoffBase
	for i := 1; i < rec.FailureStreak && wait < interval; i++ {
		wait *= 2
	}
	return min(wait, interval)
}

// probeFlight 是一次在飞探测：done 关闭后 err 可读（nil = 结果已写回读数行）。
type probeFlight struct {
	done chan struct{}
	err  error
}

// claim 登记在飞：返回在飞记录与是否由本调用者负责执行（false = 已有人在探）。
func (p *AccountProber) claim(name string) (*probeFlight, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f, ok := p.inflight[name]; ok {
		return f, false
	}
	f := &probeFlight{done: make(chan struct{})}
	p.inflight[name] = f
	return f, true
}

// release 结束在飞：先写 err 再关 done（关闭建立 happens-before，等待者读 err 无竞争）。
func (p *AccountProber) release(name string, f *probeFlight, err error) {
	p.mu.Lock()
	delete(p.inflight, name)
	p.mu.Unlock()
	f.err = err
	close(f.done)
}

// ProbeNow 立即探测一个本机账号并返回探测后的读面。同一账号已在飞时等它结束，
// 不并发再探（单飞）。探测在探测器生命周期 ctx 下执行——调用方断开不会把半途
// 取消记成一次「探测失败」；ctx 只约束调用方等待多久。
//
// 返回：探测后的 View（失败也是正常返回，失败写在读数行里）；账号不存在包
// accounts.ErrNotFound；不在本机 ErrAccountNotLocal；未运行 ErrAccountProberStopped。
func (p *AccountProber) ProbeNow(ctx context.Context, name string) (accounts.View, error) {
	if !p.running() {
		return accounts.View{}, ErrAccountProberStopped
	}
	v, err := p.cfg.Accounts.Get(name)
	if err != nil {
		return accounts.View{}, err
	}
	if !p.cfg.IsSelf(v.Account.Machine) {
		return accounts.View{}, fmt.Errorf("%w：账号 %s 的所在机是 %s", ErrAccountNotLocal, name, v.Account.Machine)
	}
	// 在飞登记与 wg.Add 同在锁内、且只在未停止时发生：Stop 置 stopped 后才 Wait，
	// 保证没有 Add 晚于 Wait（WaitGroup 的使用约束）。
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return accounts.View{}, ErrAccountProberStopped
	}
	life := p.ctx
	flight, busy := p.inflight[name]
	if !busy {
		flight = &probeFlight{done: make(chan struct{})}
		p.inflight[name] = flight
		p.wg.Add(1)
	}
	p.mu.Unlock()
	if busy {
		p.log.Info("立即探测：账号已在探测中，等待其结果", "account", name)
	} else {
		go func() {
			defer p.wg.Done()
			p.release(name, flight, p.probeOnce(life, v.Account, v.Record.FailureStreak, "now"))
		}()
	}
	select {
	case <-flight.done:
	case <-ctx.Done():
		return accounts.View{}, ctx.Err()
	}
	if flight.err != nil {
		// 结果没写进账本：如实报错，不把旧读数当「探测成功」交回。
		return accounts.View{}, flight.err
	}
	return p.cfg.Accounts.Get(name)
}

func (p *AccountProber) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ctx != nil && !p.stopped
}

// probeOnce 执行一次探测并写回读数行。凭据值只在 quota 包内存在；这里只见结果。
//
// 返回：nil = 结论（成功或失败）已写回；停机取消返回 ErrAccountProberStopped；
// 写回失败返回该错误——两者都意味着读数行没有记下这次探测。
func (p *AccountProber) probeOnce(ctx context.Context, a accounts.Account, prevStreak int, trigger string) error {
	started := p.now()
	ctx, cancel := context.WithTimeout(ctx, accountProbeBudget)
	defer cancel()
	proxy := a.Proxy
	if proxy == "" && p.cfg.Proxy != nil {
		proxy = p.cfg.Proxy()
	}
	p.log.Info("账号探测开始", "account", a.Name, "kind", a.Kind, "machine", a.Machine,
		"trigger", trigger, "proxy", proxycfg.Mask(proxy), "account_proxy", a.Proxy != "")
	outcome := accounts.ProbeOutcome{StartedAt: started, Writer: p.cfg.Writer}
	home, err := p.cfg.ExpandHome(a.HomeDir)
	var snap quota.Snapshot
	if err != nil {
		err = &quota.Failure{Category: quota.CategoryCredentialMissing, Message: fmt.Sprintf("凭据 HOME %q 无法展开：%v", a.HomeDir, err)}
	} else {
		snap, err = p.cfg.Quota.Probe(ctx, quota.Request{Kind: a.Kind, Home: home, Entry: a.Entry, Proxy: proxy})
	}
	outcome.FinishedAt = p.now()
	elapsed := outcome.FinishedAt.Sub(started)
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		// 停机途中被取消：不是账号的问题，不落一次失败。
		p.log.Info("账号探测随停机取消，不写结果", "account", a.Name, "elapsed", elapsed)
		return ErrAccountProberStopped
	}
	if err != nil {
		outcome.Failure = problemOf(err)
	} else {
		outcome.Reading = readingOf(snap, outcome.FinishedAt)
	}
	if werr := p.cfg.Accounts.RecordProbe(a.Name, outcome); werr != nil {
		p.log.Error("账号探测结果写回失败", "account", a.Name, "kind", a.Kind, "cause", werr)
		return fmt.Errorf("账号 %s 的探测结果写回账本失败: %w", a.Name, werr)
	}
	if outcome.Failure != nil {
		next := outcome.FinishedAt.Add(retryWait(a, accounts.ReadingRecord{
			LastError: outcome.Failure, FailureStreak: prevStreak + 1}))
		p.log.Warn("账号探测失败", "account", a.Name, "kind", a.Kind, "machine", a.Machine,
			"category", outcome.Failure.Category, "cause", outcome.Failure.Message,
			"failure_streak", prevStreak+1, "next", next, "elapsed", elapsed)
		return nil
	}
	next := outcome.FinishedAt.Add(time.Duration(a.IntervalMinutes) * time.Minute)
	p.log.Info("账号探测成功", "account", a.Name, "kind", a.Kind, "machine", a.Machine,
		"windows", len(outcome.Reading.Windows), "blocked", outcome.Reading.Blocked != nil,
		"elapsed", elapsed, "next", next)
	return nil
}

// problemOf 把适配器错误映射成编制域的失败原因（*quota.Failure 之外的归 internal）。
func problemOf(err error) *accounts.Problem {
	var f *quota.Failure
	if errors.As(err, &f) {
		return &accounts.Problem{Category: string(f.Category), Message: f.Message}
	}
	return &accounts.Problem{Category: "internal", Message: err.Error()}
}

// readingOf 把额度适配读数映射成编制域读数（两侧值类型各自定义，映射只在这里）。
func readingOf(s quota.Snapshot, probedAt time.Time) *accounts.Reading {
	r := &accounts.Reading{Windows: make([]accounts.Window, 0, len(s.Windows)),
		PlanType: s.PlanType, Fingerprint: s.Fingerprint, ProbedAt: probedAt.UTC()}
	if e := s.Extra; e != nil {
		r.ExtraUsage = &accounts.ExtraUsage{Unit: e.Unit, Balance: e.Balance, Used: e.Used,
			Limit: e.Limit, Unlimited: e.Unlimited}
	}
	for _, w := range s.Windows {
		r.Windows = append(r.Windows, accounts.Window{Key: w.Key, Group: w.Group,
			UsedPercent: w.UsedPercent, ResetsAt: w.ResetsAt})
	}
	if s.Blocked != nil {
		r.Blocked = &accounts.Problem{Category: string(s.Blocked.Category), Message: s.Blocked.Message}
	}
	return r
}
