// Package p2ptest 是 p2p 的测试辅助：在 pion vnet 上搭「WAN + 两个 NAT 后的 LAN +
// WAN 上一台纯 STUN 服务器」的虚拟世界，供 p2p、relay（以及 B438.2 的 mobilecore）
// 的测试共用。边界：只给测试用，生产代码不得 import。
package p2ptest

import (
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"github.com/pion/logging"
	"github.com/pion/transport/v5/vnet"
	"github.com/pion/turn/v5"
)

// STUNIP 是虚拟 WAN 上 STUN 服务器的地址。
const STUNIP = "1.2.3.4"

// NAT 行为预设。
var (
	// PortRestrictedCone ≈ Linux MASQUERADE：端点无关映射 + 地址端口相关过滤。
	PortRestrictedCone = &vnet.NATType{
		MappingBehavior:   vnet.EndpointIndependent,
		FilteringBehavior: vnet.EndpointAddrPortDependent,
	}
	// Symmetric：映射随目的端点变化，打洞必失败。
	Symmetric = &vnet.NATType{
		MappingBehavior:   vnet.EndpointAddrPortDependent,
		FilteringBehavior: vnet.EndpointAddrPortDependent,
	}
)

// World 是一个虚拟网络世界。A、B 分别位于两个 NAT 之后。
type World struct {
	A, B *vnet.Net

	wan      *vnet.Router
	srv      *turn.Server
	stunPkts atomic.Int64
	wanPkts  atomic.Int64
	cut      atomic.Bool
}

// New 搭世界并启动；t.Cleanup 负责拆除。natA/natB 为 nil 表示无 NAT（直接公网）。
func New(t testing.TB, natA, natB *vnet.NATType) *World {
	t.Helper()
	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelDisabled
	w := &World{}
	var err error
	if w.wan, err = vnet.NewRouter(&vnet.RouterConfig{CIDR: "0.0.0.0/0", LoggerFactory: lf}); err != nil {
		t.Fatalf("p2ptest wan router: %v", err)
	}
	wanNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{STUNIP}})
	if err != nil {
		t.Fatalf("p2ptest wan net: %v", err)
	}
	if err := w.wan.AddNet(wanNet); err != nil {
		t.Fatalf("p2ptest add wan net: %v", err)
	}
	mk := func(pub, cidr, local string, nt *vnet.NATType) *vnet.Net {
		r, err := vnet.NewRouter(&vnet.RouterConfig{StaticIPs: []string{pub}, CIDR: cidr, NATType: nt, LoggerFactory: lf})
		if err != nil {
			t.Fatalf("p2ptest lan router: %v", err)
		}
		n, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{local}})
		if err != nil {
			t.Fatalf("p2ptest lan net: %v", err)
		}
		if err := r.AddNet(n); err != nil {
			t.Fatalf("p2ptest add lan net: %v", err)
		}
		if err := w.wan.AddRouter(r); err != nil {
			t.Fatalf("p2ptest add lan router: %v", err)
		}
		return n
	}
	w.A = mk("27.1.1.1", "192.168.0.0/24", "192.168.0.1", natA)
	w.B = mk("28.1.1.1", "10.2.0.0/24", "10.2.0.1", natB)
	w.wan.AddChunkFilter(func(c vnet.Chunk) bool {
		if c.Network() == "udp" {
			w.wanPkts.Add(1)
			if ua, ok := c.DestinationAddr().(*net.UDPAddr); ok && ua.IP.String() == STUNIP {
				w.stunPkts.Add(1)
			}
		}
		return !w.cut.Load()
	})
	if err := w.wan.Start(); err != nil {
		t.Fatalf("p2ptest wan start: %v", err)
	}
	pc, err := wanNet.ListenPacket("udp", STUNIP+":3478")
	if err != nil {
		t.Fatalf("p2ptest stun listen: %v", err)
	}
	if w.srv, err = turn.NewServer(turn.ServerConfig{
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: pc}}, LoggerFactory: lf,
	}); err != nil {
		t.Fatalf("p2ptest stun server: %v", err)
	}
	t.Cleanup(func() {
		_ = w.srv.Close()
		_ = w.wan.Stop()
	})
	return w
}

// STUNURL 返回虚拟 STUN 服务器的 URL。
func (w *World) STUNURL() string { return fmt.Sprintf("stun:%s:3478", STUNIP) }

// STUNPackets 返回经 WAN 发往 STUN 服务器的 UDP 包数。
func (w *World) STUNPackets() int64 { return w.stunPkts.Load() }

// WANPackets 返回经 WAN 的 UDP 包总数。
func (w *World) WANPackets() int64 { return w.wanPkts.Load() }

// Cut 为 true 时 WAN 丢弃一切包（模拟直连路径中途断开）。
func (w *World) Cut(on bool) { w.cut.Store(on) }
