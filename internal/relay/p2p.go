// 职责：relay 形态的「直连优先、打洞次之、relay 兜底」编排（B438）。coordinator
// 侧：relay 隧道建好后后台经同一 relay 开信令会话、交换 ICE 描述、打洞，成功后新
// app 流优先走直连，直连死亡回落 relay 并退避重试。executor 侧（listener_p2p.go）
// 应答信令并在直连流上提供与 relay 会话相同的服务。
// 边界：NAT 穿透本身归 internal/p2p；本文件只做状态机与组装。relay 服务端零改动：
// 信令走的是一条普通 CONNECT 会话里的 E2E 密文。
package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/Xsxdot/handoff/internal/p2p"
)

// P2POptions 是 relay 形态 Dialer/Listener 的打洞选项（B438 plan §6 F3 冻结）。
// 零值 = 不打洞，行为与 B438 之前逐点一致。
type P2POptions struct {
	// Enabled 为 false 时 Dialer 不起任何升级、Listener 对信令回 disabled。
	Enabled bool
	// STUN 是 stun:host:port 列表；由组装点从 config 传入（config.P2PSTUN），
	// 空 = 只 host 候选、零 STUN。
	STUN []string
	// Net 是测试缝：nil = 真网络。
	Net p2p.Net
}

// DialerOption 配置 Dialer。
type DialerOption func(*Dialer)

// WithP2P 开启 Dialer 的打洞升级（F3 冻结）。
func WithP2P(o P2POptions) DialerOption { return func(d *Dialer) { d.p2p = o } }

// WithP2PStartDelay 让升级在 relay 隧道建好后延迟 delay 才起跳（plan P2：CLI 一次性
// 进程在此之前退出就完全不碰 STUN/信令，不白占对端 listener 的待连名额）。
func WithP2PStartDelay(delay time.Duration) DialerOption {
	return func(d *Dialer) { d.p2pStartDelay = delay }
}

// 时间参数（plan §4.3）。测试经 Dialer 私有字段缩短。
const (
	upgradeBudget            = 12 * time.Second
	upgradeBackoffBase       = 30 * time.Second
	upgradeBackoffMax        = 10 * time.Minute
	upgradeUnsupportedPeriod = 10 * time.Minute
	directDeadRetry          = 30 * time.Second
)

// 失败分类（INFO attrs 的 reason）。
const (
	reasonRelayConnect = "relay_connect"
	reasonGather       = "gather"
	reasonSignalIO     = "signal_io"
	reasonBadAnswer    = "bad_answer"
	reasonBusy         = "busy"
	reasonRejected     = "rejected"
	reasonTimeout      = "timeout"
	reasonICEFailed    = "ice_failed"
	reasonE2EFailed    = "e2e_failed"
)

// directPath 是一条在役的直连会话：p2p 字节流上的 E2E + app-yamux。
type directPath struct {
	conn    *p2p.Conn
	session *yamux.Session
}

func (p *directPath) close() {
	_ = p.session.Close()
	_ = p.conn.Close()
}

type upgradeError struct {
	reason string
	err    error
}

func (e *upgradeError) Error() string { return e.reason + ": " + e.err.Error() }
func (e *upgradeError) Unwrap() error { return e.err }

// maybeStartUpgradeLocked 在 relay 隧道可用时判定是否起一次后台升级。调用方持 d.mu。
// O(1)、不阻塞 DialContext。
func (d *Dialer) maybeStartUpgradeLocked() {
	if !d.p2p.Enabled || d.closed || d.direct != nil || d.attempting || d.account == "" {
		return
	}
	if d.now().Before(d.nextAttempt) {
		return
	}
	d.attempting = true
	d.wg.Add(1)
	go d.runUpgrade(d.account)
}

func (d *Dialer) runUpgrade(account string) {
	defer d.wg.Done()
	if d.p2pStartDelay > 0 {
		timer := time.NewTimer(d.p2pStartDelay)
		select {
		case <-timer.C:
		case <-d.ctx.Done():
			timer.Stop()
			d.finishAttempt(nil, nil)
			return
		}
	}
	ctx, cancel := context.WithTimeout(d.ctx, d.upgradeBudget)
	defer cancel()
	start := d.now()
	d.log.Debug("p2p 升级开始", "node", d.node)
	path, err := d.upgrade(ctx, account)
	if err != nil && d.ctx.Err() == nil && ctx.Err() != nil {
		var ue *upgradeError
		if errors.As(err, &ue) && ue.reason != reasonICEFailed {
			ue.reason = reasonTimeout
		}
	}
	d.finishAttempt(path, err)
	if err == nil {
		d.log.Debug("p2p 升级完成", "node", d.node, "elapsed", d.now().Sub(start).Round(time.Millisecond))
	}
}

// finishAttempt 落一次升级的结果：成功则装上直连并起监视；失败按分类退避并打日志。
func (d *Dialer) finishAttempt(path *directPath, err error) {
	d.mu.Lock()
	d.attempting = false
	if d.closed {
		d.mu.Unlock()
		if path != nil {
			path.close()
		}
		d.log.Debug("p2p 升级因 Dialer 关闭而中止", "node", d.node)
		return
	}
	if path == nil && err == nil { // 延迟期内被关（上面已判 closed，这里防御）
		d.mu.Unlock()
		return
	}
	if err == nil {
		d.direct = path
		d.backoff = 0
		d.wg.Add(1)
		d.mu.Unlock()
		pi := path.conn.Path()
		d.log.Info("p2p 直连已建立", "node", d.node, "role", "initiator",
			"local_type", pi.LocalType, "remote_type", pi.RemoteType, "network", pi.Network)
		go d.watchDirect(path)
		return
	}
	if errors.Is(err, p2p.ErrPeerUnsupported) {
		d.nextAttempt = d.now().Add(d.unsupportedPeriod)
		first := !d.unsupportedLogged
		d.unsupportedLogged = true
		d.mu.Unlock()
		if first {
			d.log.Info("p2p 对端不支持打洞，继续走 relay", "node", d.node, "cause", err.Error())
		} else {
			d.log.Debug("p2p 对端仍不支持打洞", "node", d.node, "cause", err.Error())
		}
		return
	}
	d.backoff = nextBackoff(d.backoff, d.backoffBase, d.backoffMax)
	retry := d.backoff
	d.nextAttempt = d.now().Add(retry)
	d.mu.Unlock()
	reason := "unknown"
	cause := err
	var ue *upgradeError
	if errors.As(err, &ue) {
		reason, cause = ue.reason, ue.err // reason 已单列，cause 不再重复它
	}
	if reason == reasonE2EFailed {
		d.log.Warn("p2p 直连 E2E 握手失败，继续走 relay", "node", d.node, "reason", reason, "cause", cause.Error(), "retry_in", retry.String())
		return
	}
	d.log.Info("p2p 打洞失败，继续走 relay", "node", d.node, "reason", reason, "cause", cause.Error(), "retry_in", retry.String())
}

// nextBackoff 计算退避序列（单测锁住：30s,60s,…,600s,600s）。
func nextBackoff(cur, base, max time.Duration) time.Duration {
	if cur == 0 {
		return base
	}
	if cur >= max {
		return max
	}
	if cur*2 > max {
		return max
	}
	return cur * 2
}

// watchDirect 监视在役直连：ICE 死亡或 app-yamux 关闭即摘下并回落 relay。
func (d *Dialer) watchDirect(path *directPath) {
	defer d.wg.Done()
	select {
	case <-path.conn.Dead():
	case <-path.session.CloseChan():
	case <-d.ctx.Done():
		return // Close 负责关闭 direct
	}
	if d.dropDirect(path) {
		d.log.Info("p2p 直连已断开，回落 relay", "node", d.node)
	}
}

// dropDirect 摘下并关闭 path（若仍是当前直连）。返回是否由本次摘下。
func (d *Dialer) dropDirect(path *directPath) bool {
	d.mu.Lock()
	if d.direct != path {
		d.mu.Unlock()
		return false
	}
	d.direct = nil
	d.nextAttempt = d.now().Add(d.deadRetry)
	d.backoff = 0
	d.mu.Unlock()
	path.close()
	return true
}

// upgrade 执行一次完整升级：信令会话交换描述 → ICE/sctp → 直连 E2E → app-yamux。
func (d *Dialer) upgrade(ctx context.Context, account string) (*directPath, error) {
	signal, err := d.openSecureSession(ctx)
	if err != nil {
		return nil, &upgradeError{reasonRelayConnect, err}
	}
	stopSignal := closeOnDone(ctx, signal)
	defer stopSignal()
	defer signal.Close()

	agent, err := p2p.Gather(ctx, p2p.Config{STUN: d.p2p.STUN, Net: d.p2p.Net, Logger: d.log.With("node", d.node)}, p2p.Controlling)
	if err != nil {
		return nil, &upgradeError{reasonGather, err}
	}
	keepAgent := false
	defer func() {
		if !keepAgent {
			_ = agent.Close()
		}
	}()
	if err := p2p.WriteRequest(signal, agent.Local()); err != nil {
		return nil, &upgradeError{reasonSignalIO, err}
	}
	answer, err := p2p.ReadAnswer(signal)
	if err != nil {
		var rej *p2p.RejectedError
		switch {
		case errors.Is(err, p2p.ErrPeerUnsupported):
			return nil, err
		case errors.As(err, &rej) && rej.Code == p2p.RejectBusy:
			return nil, &upgradeError{reasonBusy, err}
		case errors.As(err, &rej):
			return nil, &upgradeError{reasonRejected, err}
		default:
			return nil, &upgradeError{reasonBadAnswer, err}
		}
	}
	// 一问一答即关信令会话（F1 W11）。
	stopSignal()
	_ = signal.Close()

	conn, err := agent.Connect(ctx, answer)
	if err != nil {
		if errors.Is(err, p2p.ErrICEFailed) {
			return nil, &upgradeError{reasonICEFailed, err}
		}
		return nil, &upgradeError{reasonTimeout, err}
	}
	keepAgent = true // 资源已归 conn
	secure, err := SecureClient(ctx, conn, d.token, account, d.node)
	if err != nil {
		_ = conn.Close()
		return nil, &upgradeError{reasonE2EFailed, err}
	}
	session, err := yamux.Client(secure, relayYamuxConfig())
	if err != nil {
		_ = conn.Close()
		return nil, &upgradeError{reasonE2EFailed, fmt.Errorf("直连 app yamux: %w", err)}
	}
	return &directPath{conn: conn, session: session}, nil
}

// closeOnDone 在 ctx 结束时关闭 c；返回的 stop 幂等。
func closeOnDone(ctx context.Context, c net.Conn) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-stop:
		}
	}()
	stopped := false
	return func() {
		if !stopped {
			stopped = true
			close(stop)
			<-done
		}
	}
}

// dialDirect 返回在役且可选的直连上的一条新 app 流；ok=false 表示应走 relay。
func (d *Dialer) dialDirect() (net.Conn, bool) {
	d.mu.Lock()
	path := d.direct
	d.mu.Unlock()
	if path == nil || !path.conn.Selectable() {
		return nil, false
	}
	stream, err := path.session.Open()
	if err != nil {
		d.log.Debug("p2p 直连开流失败，本次走 relay", "node", d.node, "cause", err)
		if d.dropDirect(path) {
			d.log.Info("p2p 直连已断开，回落 relay", "node", d.node)
		}
		return nil, false
	}
	return stream, true
}
