// prober.go —— 账号额度探测器（B453 S2，plan Q3；B464 段 1 迁入 accountwork 子包，plan Q21）。
//
// 职责：只探测「可探登录在本机」的账号——每个账号取一条可探登录（plan Q10：正常 + 所在机
// 发现新鲜 + id 最小，accounts.ProbeLogin），只有那条登录的所在机探它；不支持额度探测的 kind
// （na）在调 quota 之前跳过；探测得到 credential_expired/unauthorized 时给所用登录记失效证据
// （plan Q5，登录读作已过期 → 可探登录自然换下一条，换了登录立即再探、不等失败退避）；周期扫描（到期 = 上次
// 尝试 + 账号间隔）与立即探测（ProbeNow）共用同一实例与同一条写路径；同一账号单飞，立即
// 探测写入的 last_attempt_at 自然顺延下一次周期探测。读本机凭据、调额度接口由
// internal/quota 完成；结果映射成编制域值类型后经 RecordProbe 读-合并-CAS 写进共享账本的
// 读数行（带本次所用登录 id，plan L8）。
//
// 边界：
//   - 不代探他机登录（凭据只在所在机）；不碰账号定义行；登录行只写失效证据（MarkLoginAuthFailed）；
//   - 调度状态全在账本读数行（last_attempt_at），进程内只有在飞集合——重启后按
//     读数行重建节拍，不突发全量重探；
//   - 失败按 30s×2ⁿ 退避、封顶为账号间隔（probeDue）；
//   - 生命周期跟 cmd/agentd.go 既有周期任务同形：ticker + ctx，Stop() 取消并等
//     扫描循环与在飞探测全部退出。
package accountwork

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

// ErrNotLocal 表示要求本机探测的账号，其可探登录不在本机。
var ErrNotLocal = errors.New("账号的可探登录不在本机")

// ErrNoProbeLogin 表示账号没有可探登录（名下没有正常登录）。
var ErrNoProbeLogin = errors.New("账号没有可用登录")

// ErrProbeUnsupported 表示账号 kind 不支持额度探测（claude、第三方 key，plan Q11）。
var ErrProbeUnsupported = errors.New("账号类型不支持额度探测")

// ErrIgnored 表示账号已被忽略（B467）：忽略即不探测，恢复后下一轮扫描才重新探。
var ErrIgnored = errors.New("账号已忽略")

// ErrStopped 表示探测器未启动或已停止。
var ErrStopped = errors.New("账号探测器未运行")

// QuotaProber 是探测器对额度适配子系统的使用方接口（实现：*quota.Registry）。
type QuotaProber interface {
	Probe(ctx context.Context, req quota.Request) (quota.Snapshot, error)
}

// ProberAccounts 是探测器对账号域的使用方接口（实现：*accounts.Service）。方法集 = 生产调用
// 闭包：List（周期扫描）、Get（立即探测读账号与可探登录）、RecordProbe（写回读数行）、
// MarkLoginAuthFailed（凭据失效证据，plan Q5）。
type ProberAccounts interface {
	List() ([]accounts.View, error)
	Get(ref string) (accounts.View, error)
	RecordProbe(id string, o accounts.ProbeOutcome) error
	MarkLoginAuthFailed(id string, at time.Time, writer, category string) (bool, error)
}

// ProberConfig 是探测器的装配参数，全部由组装点（cmd/agentd.go）注入。
type ProberConfig struct {
	Accounts   ProberAccounts               // 读账号与登录、写读数（共享账本）
	Quota      QuotaProber                  // 按 kind 分派的额度适配
	IsSelf     func(machine string) bool    // 登录所在机是否本机（Server.IsSelfMachine）
	Writer     string                       // 写者标识：本机自名
	ExpandHome func(string) (string, error) // ~ 展开（hostapi.ExpandHomePath）
	Proxy      func() string                // 登录未填代理时的回落：活配置 proxy:（plan Q5）
	Logger     *slog.Logger
}

// Prober 是所在机探测器。零值不可用，经 NewProber 构造。
type Prober struct {
	cfg       ProberConfig
	log       *slog.Logger
	scanEvery time.Duration
	now       func() time.Time

	mu       sync.Mutex
	inflight map[string]*probeFlight // 账号 id → 在飞探测（单飞）
	sem      chan struct{}
	ctx      context.Context // Start 后的生命周期 ctx；nil = 未运行
	cancel   context.CancelFunc
	stopped  bool
	wg       sync.WaitGroup
}

// NewProber 构造探测器（不启动）。Start 之前 ProbeNow 返回 ErrStopped。
func NewProber(cfg ProberConfig) *Prober {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Prober{
		cfg: cfg, log: logger.With("component", "account_prober"),
		scanEvery: accountProbeScan, now: time.Now,
		inflight: map[string]*probeFlight{}, sem: make(chan struct{}, accountProbeConcurrency),
	}
}

// Start 启动周期扫描（启动即扫一次）。ctx 取消或 Stop 后退出。重复调用只生效一次。
func (p *Prober) Start(parent context.Context) {
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
func (p *Prober) Stop() {
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

// scan 扫一遍账号：可探登录在本机、到期、不在飞、有空位的发起探测；其余等下一轮。
func (p *Prober) scan(ctx context.Context) {
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
		if !accounts.SupportsProbe(v.Account.Kind) {
			continue // na：不调 quota（未注册 kind 在 quota 里是错误，na 不是）
		}
		if v.Ignored != nil {
			continue // 已忽略：读面已无可探登录；显式跳过让「不探测」不依赖读面细节
		}
		login, ok := probeLoginOf(v)
		if !ok || !p.cfg.IsSelf(login.Machine) || !probeDue(v, now) {
			continue
		}
		due++
		select {
		case p.sem <- struct{}{}:
		default:
			p.log.Debug("账号探测并发已满，留待下一轮", "account", v.Account.ID)
			continue
		}
		flight, started := p.claim(v.Account.ID)
		if !started {
			<-p.sem // 已在飞（立即探测正在跑），本轮不重复
			continue
		}
		p.wg.Add(1)
		go func(a accounts.Account, l accounts.Login, streak int) {
			defer p.wg.Done()
			defer func() { <-p.sem }()
			p.release(a.ID, flight, p.probeOnce(ctx, a, l, streak, "periodic"))
		}(v.Account, login, v.Record.FailureStreak)
	}
	if due > 0 {
		p.log.Debug("账号探测扫描完成", "accounts", len(views), "due", due)
	}
}

// probeLoginOf 取读面上的可探登录（accounts 域已按 Q10 选好 id）。
func probeLoginOf(v accounts.View) (accounts.Login, bool) {
	if v.ProbeLogin == "" {
		return accounts.Login{}, false
	}
	for _, lv := range v.Logins {
		if lv.Login.ID == v.ProbeLogin {
			return lv.Login, true
		}
	}
	return accounts.Login{}, false
}

// probeDue：从未尝试 → 到期；上次失败用的是另一条登录（可探登录已换）→ 立即到期；否则上次
// 尝试 + retryWait 已过。节拍全由读数行推出，重启后自然续上，不突发全量重探。
func probeDue(v accounts.View, now time.Time) bool {
	last := v.Record.LastAttemptAt
	if last == nil {
		return true
	}
	if v.Record.LastError != nil && v.Record.Login != "" && v.Record.Login != v.ProbeLogin {
		return true // 失败退避属于失败的那条登录；换了登录不该替它等
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
func (p *Prober) claim(name string) (*probeFlight, bool) {
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
func (p *Prober) release(name string, f *probeFlight, err error) {
	p.mu.Lock()
	delete(p.inflight, name)
	p.mu.Unlock()
	f.err = err
	close(f.done)
}

// ProbeNow 立即探测一个账号（可探登录须在本机）并返回探测后的读面。同一账号已在飞时等它
// 结束，不并发再探（单飞）。探测在探测器生命周期 ctx 下执行——调用方断开不会把半途
// 取消记成一次「探测失败」；ctx 只约束调用方等待多久。ref 是账号 id 或名字。
//
// 返回：探测后的 View（失败也是正常返回，失败写在读数行里）；账号不存在包
// accounts.ErrNotFound；没有可探登录 ErrNoProbeLogin；可探登录不在本机 ErrNotLocal；
// 未运行 ErrStopped。
func (p *Prober) ProbeNow(ctx context.Context, ref string) (accounts.View, error) {
	if !p.running() {
		return accounts.View{}, ErrStopped
	}
	v, err := p.cfg.Accounts.Get(ref)
	if err != nil {
		return accounts.View{}, err
	}
	if v.Ignored != nil {
		p.log.Info("立即探测被拒：账号已忽略", "account", v.Account.ID, "name", v.Account.Name)
		return accounts.View{}, fmt.Errorf("%w：账号 %s 已忽略，先在账号页恢复", ErrIgnored, v.Account.Name)
	}
	if !accounts.SupportsProbe(v.Account.Kind) {
		return accounts.View{}, fmt.Errorf("%w：账号 %s 是 %s", ErrProbeUnsupported, v.Account.Name, v.Account.Kind)
	}
	login, ok := probeLoginOf(v)
	if !ok {
		return accounts.View{}, fmt.Errorf("%w：账号 %s 名下没有状态正常的登录", ErrNoProbeLogin, v.Account.Name)
	}
	if !p.cfg.IsSelf(login.Machine) {
		return accounts.View{}, fmt.Errorf("%w：账号 %s 的可探登录在 %s", ErrNotLocal, v.Account.Name, login.Machine)
	}
	id := v.Account.ID
	// 在飞登记与 wg.Add 同在锁内、且只在未停止时发生：Stop 置 stopped 后才 Wait，
	// 保证没有 Add 晚于 Wait（WaitGroup 的使用约束）。
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return accounts.View{}, ErrStopped
	}
	life := p.ctx
	flight, busy := p.inflight[id]
	if !busy {
		flight = &probeFlight{done: make(chan struct{})}
		p.inflight[id] = flight
		p.wg.Add(1)
	}
	p.mu.Unlock()
	if busy {
		p.log.Info("立即探测：账号已在探测中，等待其结果", "account", id)
	} else {
		go func() {
			defer p.wg.Done()
			p.release(id, flight, p.probeOnce(life, v.Account, login, v.Record.FailureStreak, "now"))
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
	return p.cfg.Accounts.Get(id)
}

func (p *Prober) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ctx != nil && !p.stopped
}

// probeOnce 经登录 l 执行一次探测并写回读数行。凭据值只在 quota 包内存在；这里只见结果。
//
// 返回：nil = 结论（成功或失败）已写回；停机取消返回 ErrStopped；
// 写回失败返回该错误——两者都意味着读数行没有记下这次探测。
func (p *Prober) probeOnce(ctx context.Context, a accounts.Account, l accounts.Login, prevStreak int, trigger string) error {
	started := p.now()
	ctx, cancel := context.WithTimeout(ctx, accountProbeBudget)
	defer cancel()
	proxy := l.Proxy
	if proxy == "" && p.cfg.Proxy != nil {
		proxy = p.cfg.Proxy()
	}
	p.log.Info("账号探测开始", "account", a.ID, "name", a.Name, "kind", a.Kind, "login", l.ID,
		"machine", l.Machine, "home", l.Home, "source", l.Source, "trigger", trigger,
		"proxy", proxycfg.Mask(proxy), "login_proxy", l.Proxy != "")
	outcome := accounts.ProbeOutcome{StartedAt: started, Writer: p.cfg.Writer, Login: l.ID}
	home, err := p.cfg.ExpandHome(l.Home)
	var snap quota.Snapshot
	if err != nil {
		err = &quota.Failure{Category: quota.CategoryCredentialMissing, Message: fmt.Sprintf("登录 HOME %q 无法展开：%v", l.Home, err)}
	} else {
		snap, err = p.cfg.Quota.Probe(ctx, quota.Request{Kind: a.Kind, Home: home, Entry: SourceEntry(l.Kind, l.Source), Proxy: proxy})
	}
	outcome.FinishedAt = p.now()
	elapsed := outcome.FinishedAt.Sub(started)
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		// 停机途中被取消：不是账号的问题，不落一次失败。
		p.log.Info("账号探测随停机取消，不写结果", "account", a.ID, "elapsed", elapsed)
		return ErrStopped
	}
	if err != nil {
		outcome.Failure = problemOf(err)
	} else {
		outcome.Reading = readingOf(snap, outcome.FinishedAt)
	}
	if werr := p.cfg.Accounts.RecordProbe(a.ID, outcome); werr != nil {
		p.log.Error("账号探测结果写回失败", "account", a.ID, "kind", a.Kind, "cause", werr)
		return fmt.Errorf("账号 %s 的探测结果写回账本失败: %w", a.Name, werr)
	}
	if f := outcome.Failure; f != nil && credentialRejected(f.Category) {
		// 证据写失败只告警：读数行已记下这次失败，下一轮探测会再得到同一证据。
		if _, merr := p.cfg.Accounts.MarkLoginAuthFailed(l.ID, started, p.cfg.Writer, f.Category); merr != nil {
			p.log.Warn("登录失效证据写入失败", "account", a.ID, "login", l.ID, "category", f.Category, "cause", merr)
		}
	}
	if outcome.Failure != nil {
		next := outcome.FinishedAt.Add(retryWait(a, accounts.ReadingRecord{
			LastError: outcome.Failure, FailureStreak: prevStreak + 1}))
		p.log.Warn("账号探测失败", "account", a.ID, "name", a.Name, "kind", a.Kind, "login", l.ID,
			"category", outcome.Failure.Category, "cause", outcome.Failure.Message,
			"proxy", proxycfg.Mask(proxy), "login_proxy", l.Proxy != "",
			"failure_streak", prevStreak+1, "next", next, "elapsed", elapsed)
		return nil
	}
	next := outcome.FinishedAt.Add(time.Duration(a.IntervalMinutes) * time.Minute)
	p.log.Info("账号探测成功", "account", a.ID, "name", a.Name, "kind", a.Kind, "login", l.ID,
		"windows", len(outcome.Reading.Windows), "blocked", outcome.Reading.Blocked != nil,
		"proxy", proxycfg.Mask(proxy), "login_proxy", l.Proxy != "",
		"elapsed", elapsed, "next", next)
	return nil
}

// credentialRejected 报告失败类别是不是「凭据失效」证据（plan Q5）：凭据过期或被上游拒绝。
// 网络、代理、限速、上游 5xx 等与凭据无关，不记证据。
func credentialRejected(category string) bool {
	return category == string(quota.CategoryCredentialExpired) || category == string(quota.CategoryUnauthorized)
}

// SourceEntry 把登录的凭据来源名映射成额度适配的条目（plan V8：探测按登录 source 取凭据）。
// opencode-go：auth.json → `opencode-go` 条目（API key），opencode.db → 登录账号；codex：
// auth.json → `tokens`；grok：auth.json → 首选 issuer（前缀匹配 `issuer::<uuid>`）；agy 无条目。
// 未知组合按适配器缺省条目。
func SourceEntry(kind, source string) string {
	switch {
	case kind == accounts.KindOpencodeGo && source == "auth.json":
		return "opencode-go"
	case kind == accounts.KindOpencodeGo && source == "opencode.db":
		return "opencode.db"
	case kind == accounts.KindCodex && source == "auth.json":
		return "tokens"
	case kind == accounts.KindGrok && source == "auth.json":
		return "https://auth.x.ai"
	}
	return ""
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
