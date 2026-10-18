// Package p2p 是 handoff 的 NAT 穿透子系统（B438）。
//
// 职责：给定调用方传入的 ICE 配置与角色，收集本端候选、与对端描述连通，产出一条
// 经直连路径的可靠有序字节流（ICE → sctp 单流 → 字节流适配），或在有界时间内
// 失败；另拥有打洞信令子协议的编解码（signal.go）。
// 边界：不认识 relay 控制帧、E2E、yamux、HTTP；不读配置文件（STUN 列表由调用方
// 从 config 缺省值或用户配置传入，本包不写死任何 STUN 地址）；不做 TURN——兜底就是
// relay。用户可见日志由调用方（relay）打，本包只打 Debug，且永不记录候选串与
// ICE 凭据（候选串内含 ufrag）。
package p2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/sctp"
	"github.com/pion/stun/v4"
	"github.com/pion/transport/v5"
)

// Net 是 ICE 的网络抽象，生产调用方不需要 import pion。nil = 真网络。
type Net = transport.Net

// 时间参数（spec S2/S3 判据倒推，见 plan §4.3）。
const (
	stunGatherTimeout   = 3 * time.Second
	disconnectedTimeout = 5 * time.Second
	failedTimeout       = 5 * time.Second
	keepaliveInterval   = 2 * time.Second
	// maxWriteChunk 小于 sctp 缺省 MaxMessageSize（65536），单条写超出即切片。
	maxWriteChunk = 32 * 1024
	readBufSize   = 64 * 1024
)

// Role 是本端在一次打洞里的角色。发起方（coordinator）= ICE controlling = sctp
// client = OpenStream；应答方（listener）= controlled = sctp server = AcceptStream。
type Role int

const (
	Controlling Role = iota
	Controlled
)

func (r Role) String() string {
	if r == Controlling {
		return "controlling"
	}
	return "controlled"
}

// ErrBadCandidate 表示对端描述里有无法解析的候选。
var ErrBadCandidate = errors.New("p2p: 对端候选非法")

// ErrICEFailed 表示 ICE 连通检查以 Failed 收尾（典型：一端对称 NAT 或 UDP 被封）。
var ErrICEFailed = errors.New("p2p: ICE 连通失败")

// Config 是一次打洞的配置。
type Config struct {
	// STUN 是 stun:host:port 形态的 URL 列表；空 = 只收集 host 候选、零 STUN 包。
	STUN []string
	// Net 是测试缝：nil = 真网络；测试注入 pion vnet。
	Net Net
	// Logger 可带调用方上下文 attrs（node 等）；nil = slog.Default()。
	Logger *slog.Logger
}

// Agent 是一次打洞尝试的 ICE 代理（收集完成后的状态）。Connect 成功后其资源归
// 返回的 Conn 所有；Connect 前或失败后由调用方 Close。
type Agent struct {
	agent *ice.Agent
	role  Role
	log   *slog.Logger
	local Description

	failedOnce sync.Once
	failed     chan struct{} // ICE Failed/Closed 时关闭
	stateMu    sync.Mutex
	onState    func(ice.ConnectionState)

	closeOnce sync.Once
}

// Gather 创建 ICE 代理并收集本端候选（不 trickle：等收集完成）。
//
// 参数：ctx 限定收集时长（STUN 收集另有 3 s 内部上限）；cfg.STUN 中任一项解析失败
// 即返回错误（config 侧已做词法校验，这里是第二道）。
// 返回：*Agent，其 Local() 可直接写入信令。
func Gather(ctx context.Context, cfg Config, role Role) (*Agent, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	urls := make([]*stun.URI, 0, len(cfg.STUN))
	for _, raw := range cfg.STUN {
		u, err := stun.ParseURI(raw)
		if err != nil {
			log.Warn("p2p STUN 地址无法解析，放弃本次打洞", "index", len(urls), "cause", err)
			return nil, fmt.Errorf("p2p 解析 STUN 地址 #%d: %w", len(urls), err)
		}
		urls = append(urls, u)
	}
	opts := []ice.AgentOption{
		ice.WithUrls(urls),
		ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}),
		ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost, ice.CandidateTypeServerReflexive}),
		ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled),
		ice.WithDisconnectedTimeout(disconnectedTimeout),
		ice.WithFailedTimeout(failedTimeout),
		ice.WithKeepaliveInterval(keepaliveInterval),
		ice.WithSTUNGatherTimeout(stunGatherTimeout),
		ice.WithLoggerFactory(discardLoggerFactory{}),
	}
	if cfg.Net != nil {
		opts = append(opts, ice.WithNet(cfg.Net))
	}
	ia, err := ice.NewAgentWithOptions(opts...)
	if err != nil {
		log.Warn("p2p 创建 ICE 代理失败", "ice_role", role.String(), "cause", err)
		return nil, fmt.Errorf("p2p 创建 ICE 代理: %w", err)
	}
	a := &Agent{agent: ia, role: role, log: log, failed: make(chan struct{})}
	if err := ia.OnConnectionStateChange(a.handleState); err != nil {
		_ = ia.GracefulClose()
		return nil, fmt.Errorf("p2p 注册状态回调: %w", err)
	}
	done := make(chan struct{})
	var mu sync.Mutex
	var cands []string
	if err := ia.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			close(done)
			return
		}
		mu.Lock()
		cands = append(cands, c.Marshal())
		mu.Unlock()
	}); err != nil {
		_ = ia.GracefulClose()
		return nil, fmt.Errorf("p2p 注册候选回调: %w", err)
	}
	start := time.Now()
	if err := ia.GatherCandidates(); err != nil {
		_ = ia.GracefulClose()
		log.Warn("p2p 收集候选失败", "ice_role", role.String(), "cause", err)
		return nil, fmt.Errorf("p2p 收集候选: %w", err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		_ = ia.GracefulClose()
		return nil, fmt.Errorf("p2p 收集候选: %w", ctx.Err())
	}
	ufrag, pwd, err := ia.GetLocalUserCredentials()
	if err != nil {
		_ = ia.GracefulClose()
		return nil, fmt.Errorf("p2p 读本端凭据: %w", err)
	}
	mu.Lock()
	if len(cands) > MaxCandidates {
		cands = cands[:MaxCandidates]
	}
	a.local = Description{Ufrag: ufrag, Pwd: pwd, Candidates: cands}
	mu.Unlock()
	log.Debug("p2p 候选收集完成", "ice_role", role.String(), "count", len(a.local.Candidates),
		"stun_servers", len(urls), "elapsed", time.Since(start).Round(time.Millisecond))
	return a, nil
}

// Local 返回本端描述（写进信令）。
func (a *Agent) Local() Description { return a.local }

func (a *Agent) handleState(s ice.ConnectionState) {
	a.log.Debug("p2p ICE 状态变化", "ice_role", a.role.String(), "state", s.String())
	if s == ice.ConnectionStateFailed || s == ice.ConnectionStateClosed {
		a.failedOnce.Do(func() { close(a.failed) })
	}
	a.stateMu.Lock()
	f := a.onState
	a.stateMu.Unlock()
	if f != nil {
		f(s)
	}
}

func (a *Agent) setOnState(f func(ice.ConnectionState)) {
	a.stateMu.Lock()
	a.onState = f
	a.stateMu.Unlock()
}

// Connect 与对端描述连通并建立可靠字节流。
//
// ctx 是整次连通（ICE 检查 + sctp 握手 + 应答方等待首个 DATA）的预算；ICE 进入
// Failed 即早退返回 ErrICEFailed（pion 的 Dial/Accept 自身不因 Failed 返回）。
// 失败时 Agent 仍需调用方 Close；成功时资源归返回的 Conn。
func (a *Agent) Connect(ctx context.Context, remote Description) (*Conn, error) {
	for i, raw := range remote.Candidates {
		// 错误文本刻意不带 pion 的 cause：它可能回显候选串（内含 ufrag），而
		// 这里的错误会进调用方的 INFO 日志。
		c, err := ice.UnmarshalCandidate(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: 对端候选 #%d 无法解析", ErrBadCandidate, i)
		}
		if err := a.agent.AddRemoteCandidate(c); err != nil {
			return nil, fmt.Errorf("%w: 对端候选 #%d 无法添加", ErrBadCandidate, i)
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-a.failed:
			cancel()
		case <-cctx.Done():
		}
	}()
	start := time.Now()
	var iceConn *ice.Conn
	var err error
	if a.role == Controlling {
		iceConn, err = a.agent.Dial(cctx, remote.Ufrag, remote.Pwd)
	} else {
		iceConn, err = a.agent.Accept(cctx, remote.Ufrag, remote.Pwd)
	}
	if err != nil {
		return nil, a.classify(ctx, "ICE 连通", err)
	}
	a.log.Debug("p2p ICE 已连通", "ice_role", a.role.String(), "elapsed", time.Since(start).Round(time.Millisecond))
	// sctp 的 Server 无 ctx：预算到点或 ICE 失败时关掉 ICE 连接让它返回。
	stop := make(chan struct{})
	go func() {
		select {
		case <-cctx.Done():
			_ = iceConn.Close()
		case <-stop:
		}
	}()
	stream, assoc, err := a.openStream(cctx, iceConn)
	close(stop)
	if err != nil {
		return nil, a.classify(ctx, "sctp 建流", err)
	}
	conn := newConn(a, iceConn, assoc, stream)
	a.log.Debug("p2p 直连流已建立", "ice_role", a.role.String(), "elapsed", time.Since(start).Round(time.Millisecond))
	return conn, nil
}

func (a *Agent) classify(ctx context.Context, stage string, err error) error {
	select {
	case <-a.failed:
		return fmt.Errorf("%w（阶段：%s）", ErrICEFailed, stage)
	default:
	}
	if ctx.Err() != nil {
		return fmt.Errorf("p2p %s: %w", stage, ctx.Err())
	}
	return fmt.Errorf("p2p %s: %w", stage, err)
}

func (a *Agent) openStream(ctx context.Context, iceConn *ice.Conn) (*sctp.Stream, *sctp.Association, error) {
	if a.role == Controlling {
		assoc, err := sctp.ClientContext(ctx, sctp.WithNetConn(iceConn),
			sctp.WithLoggerFactory(discardLoggerFactory{}), sctp.WithBlockWrite(true))
		if err != nil {
			return nil, nil, err
		}
		stream, err := assoc.OpenStream(0, sctp.PayloadTypeWebRTCBinary)
		if err != nil {
			_ = assoc.Close()
			return nil, nil, err
		}
		return stream, assoc, nil
	}
	assoc, err := sctp.ServerWithOptions(sctp.WithNetConn(iceConn),
		sctp.WithLoggerFactory(discardLoggerFactory{}), sctp.WithBlockWrite(true))
	if err != nil {
		return nil, nil, err
	}
	// AcceptStream 等对端首个 DATA：发起方的 E2E SecureClient 先写 salt。
	stream, err := assoc.AcceptStream()
	if err != nil {
		_ = assoc.Close()
		return nil, nil, err
	}
	return stream, assoc, nil
}

// Close 释放 ICE 代理（等待其全部 goroutine 退出）。幂等；不得在 pion 回调内调用。
func (a *Agent) Close() error {
	var err error
	a.closeOnce.Do(func() { err = a.agent.GracefulClose() })
	return err
}

// Path 描述选中的候选对，只含类型与地址族——可安全进日志。
type Path struct {
	LocalType  string
	RemoteType string
	Network    string
}

func (a *Agent) selectedPath() Path {
	p, err := a.agent.GetSelectedCandidatePair()
	if err != nil || p == nil {
		return Path{LocalType: "unknown", RemoteType: "unknown", Network: "unknown"}
	}
	network := "udp4"
	if ip := net.ParseIP(p.Local.Address()); ip != nil && ip.To4() == nil {
		network = "udp6"
	}
	return Path{
		LocalType:  strings.ToLower(p.Local.Type().String()),
		RemoteType: strings.ToLower(p.Remote.Type().String()),
		Network:    network,
	}
}
