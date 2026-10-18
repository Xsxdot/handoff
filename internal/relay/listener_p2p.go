// 职责：executor 侧打洞应答（B438）——在一条 relay 信令会话（已过 E2E）上读请求、
// 收集本端候选、回应答、关信令；随后在 Listener 生命周期 goroutine 里连通 ICE，
// 在直连流上重跑 E2E（responder），交给与 relay 会话相同的 app 服务路径。
// 边界：NAT 穿透归 internal/p2p；直连会话只承载 app-yamux（不做 raw/信令判别，
// 防止直连成为绕过 preview-raw 网络白名单的旁路）。
package relay

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/Xsxdot/handoff/internal/p2p"
)

// 上限（plan §4.3）：一台 executor 的协调者是个位数，每个 Dialer 至多一条直连；
// 待连每个占一个 ICE 代理的 UDP 套接字至多 upgradeBudget。
const (
	listenerP2PMaxPending     = 4
	listenerP2PMaxEstablished = 16
	listenerP2PRequestTimeout = 10 * time.Second
)

// serveSignalSession 应答一次打洞请求。返回时信令会话已可关闭（调用方 defer 关）；
// 连通与直连服务在 Listener 级 goroutine 里继续。
func (l *Listener) serveSignalSession(ctx context.Context, secure net.Conn, reader *bufio.Reader) {
	log := l.log.With("node", l.node, "role", "responder")
	// W15：E2E 完成起限时收齐请求，防半开信令会话占名额。
	reqCtx, cancel := context.WithTimeout(ctx, l.p2pRequestTimeout)
	stop := closeOnDone(reqCtx, secure)
	desc, err := p2p.ReadRequest(reader)
	stop()
	cancel()
	if err != nil {
		var re *p2p.RequestError
		code := p2p.RejectBadRequest
		if errors.As(err, &re) {
			code = re.Code
		}
		log.Info("p2p 打洞请求非法，已拒绝", "reject", code, "cause", err.Error())
		_ = p2p.WriteReject(secure, code)
		return
	}
	if !l.p2p.Enabled {
		log.Debug("p2p 本机已关闭打洞，回 disabled")
		_ = p2p.WriteReject(secure, p2p.RejectDisabled)
		return
	}
	if !l.reservePending() {
		log.Info("p2p 打洞应答已达上限，回 busy",
			"max_pending", l.p2pMaxPending, "max_established", l.p2pMaxEstablished)
		_ = p2p.WriteReject(secure, p2p.RejectBusy)
		return
	}
	// 从这里起名额已占：任何出口都要 releasePending（或转成 established）。
	connectCtx, connectCancel := context.WithTimeout(ctx, l.p2pConnectBudget)
	agent, err := p2p.Gather(connectCtx, p2p.Config{STUN: l.p2p.STUN, Net: l.p2p.Net, Logger: log}, p2p.Controlled)
	if err != nil {
		connectCancel()
		l.releasePending()
		log.Info("p2p 应答方收集候选失败", "cause", err.Error())
		_ = p2p.WriteReject(secure, p2p.RejectBusy)
		return
	}
	if err := p2p.WriteAccept(secure, agent.Local()); err != nil {
		connectCancel()
		_ = agent.Close()
		l.releasePending()
		log.Info("p2p 应答写出失败", "cause", err.Error())
		return
	}
	account := l.account
	l.p2pWG.Add(1)
	go func() {
		defer l.p2pWG.Done()
		defer connectCancel()
		l.connectAndServe(ctx, connectCtx, agent, desc, account, log)
	}()
}

func (l *Listener) connectAndServe(lifeCtx, connectCtx context.Context, agent *p2p.Agent, remote p2p.Description, account string, log *slog.Logger) {
	conn, err := agent.Connect(connectCtx, remote)
	if err != nil {
		_ = agent.Close()
		l.releasePending()
		log.Info("p2p 应答方连通失败", "cause", err.Error())
		return
	}
	secure, err := SecureServer(connectCtx, conn, l.token, account, l.node)
	if err != nil {
		_ = conn.Close()
		l.releasePending()
		log.Warn("p2p 直连 E2E 握手失败，已关闭直连", "cause", err.Error())
		return
	}
	l.promotePending()
	pi := conn.Path()
	log.Info("p2p 直连已建立", "local_type", pi.LocalType, "remote_type", pi.RemoteType, "network", pi.Network)
	// ICE 死亡时关掉直连，让 http.Serve 退出。
	go func() {
		select {
		case <-conn.Dead():
		case <-lifeCtx.Done():
		}
		_ = conn.Close()
	}()
	l.serveApp(lifeCtx, secure, "p2p")
	_ = conn.Close()
	l.releaseEstablished()
	log.Info("p2p 直连会话已关闭")
}

func (l *Listener) reservePending() bool {
	l.p2pMu.Lock()
	defer l.p2pMu.Unlock()
	if l.p2pPending >= l.p2pMaxPending || l.p2pPending+l.p2pEstablished >= l.p2pMaxEstablished {
		return false
	}
	l.p2pPending++
	return true
}

func (l *Listener) releasePending() {
	l.p2pMu.Lock()
	l.p2pPending--
	l.p2pMu.Unlock()
}

func (l *Listener) promotePending() {
	l.p2pMu.Lock()
	l.p2pPending--
	l.p2pEstablished++
	l.p2pMu.Unlock()
}

func (l *Listener) releaseEstablished() {
	l.p2pMu.Lock()
	l.p2pEstablished--
	l.p2pMu.Unlock()
}
