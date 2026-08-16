// 广告地址枚举的单元测试：经 interfaceAddrs 缝注入假网卡表，
// 不依赖本机真实接口。
package cmd

import (
	"net"
	"testing"
)

func TestListAdvertiseAddrsFiltersAndOrders(t *testing.T) {
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
			&net.IPNet{IP: net.ParseIP("10.0.0.8"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("100.73.238.21"), Mask: net.CIDRMask(32, 32)},
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}
	t.Cleanup(func() { interfaceAddrs = old })

	got := listAdvertiseAddrs()
	if len(got) != 2 || !got[0].Equal(net.ParseIP("100.73.238.21")) || !got[1].Equal(net.ParseIP("10.0.0.8")) {
		t.Fatalf("应先 Tailscale 再其它 IPv4，得到 %v", got)
	}
}

func TestAdvertiseAddrAllInterfacesUsesFirst(t *testing.T) {
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("100.73.1.2"), Mask: net.CIDRMask(32, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrs = old })
	if got := advertiseAddr("0.0.0.0:7777"); got != "100.73.1.2:7777" {
		t.Fatalf("got %q", got)
	}
}

func TestAdvertiseAddrLoopbackStaysLocal(t *testing.T) {
	if got := advertiseAddr("127.0.0.1:7777"); got != "127.0.0.1:7777" {
		t.Fatalf("got %q", got)
	}
}

func TestAdvertiseAddrSpecificIPKept(t *testing.T) {
	if got := advertiseAddr("192.168.1.9:7788"); got != "192.168.1.9:7788" {
		t.Fatalf("got %q", got)
	}
}

func TestAdvertiseAddrNoAddrsFallsBack(t *testing.T) {
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) { return nil, nil }
	t.Cleanup(func() { interfaceAddrs = old })
	if got := advertiseAddr("0.0.0.0:7777"); got != "<本机IP>:7777" {
		t.Fatalf("got %q", got)
	}
}
