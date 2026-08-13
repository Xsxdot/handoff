// 本文件枚举本机可广告的单播地址，供 init 配对片段的 addr 使用。
//
// 职责：
//   - 从网卡地址里筛出可给对端抄的 IPv4（排除 loopback / link-local）
//   - 把 listen 上的通配地址换成排序后的第一条，拼进配对 addr
//
// 边界：
//   - **不写 listen、不改配置**：探到的 IP 只出现在配对片段。绑到某一张
//     网卡会让 127.0.0.1 连不上，DHCP / Tailscale 一变 agentd 也起不来。
//   - 本期只要 IPv4；IPv6 留给后续（本仓库远程场景是 Tailscale CGNAT）
package cmd

import (
	"log/slog"
	"net"
)

// interfaceAddrs 是 net.InterfaceAddrs 的可替换缝，测试注入假地址表。
var interfaceAddrs = net.InterfaceAddrs

var (
	// IPv4 link-local：DHCP 没拿到地址时的自分配段，对端抄了也连不上。
	linkLocalV4 = net.IPNet{IP: net.IPv4(169, 254, 0, 0), Mask: net.CIDRMask(16, 32)}
	// Tailscale 用的 CGNAT 段。远程配对几乎都走这条，所以排在其它 IPv4 前面。
	tailscaleCGNAT = net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
)

// listAdvertiseAddrs 返回本机可写入配对 addr 的 IPv4，Tailscale 在前。
//
// 返回：
//   - 过滤后的 IPv4 列表；枚举失败或筛空则返回 nil
//
// 注意：
//   - 排除 loopback、169.254.0.0/16、fe80::/10（后者本就是 IPv6，IPv4-only
//     时自然掉出；显式跳过是为了以后加 IPv6 时别把链路本地当可达地址）
//   - 同组内保持网卡枚举顺序，不按数值排序
func listAdvertiseAddrs() []net.IP {
	addrs, err := interfaceAddrs()
	if err != nil {
		slog.Warn("枚举网卡地址失败", "cause", err)
		return nil
	}
	var ts, others []net.IP
	for _, a := range addrs {
		ip := ipFromAddr(a)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || linkLocalV4.Contains(ip) {
			continue
		}
		ip4 := ip.To4()
		if ip4 == nil {
			continue
		}
		// To4 可能返回底层缓冲区的切片，拷走避免后续调用方改到网卡表。
		ip4 = append(net.IP(nil), ip4...)
		if tailscaleCGNAT.Contains(ip4) {
			ts = append(ts, ip4)
		} else {
			others = append(others, ip4)
		}
	}
	return append(ts, others...)
}

// advertiseAddr 把 listen 换成配对片段里该抄的 host:port。
//
// 参数：
//   - listen: 配置里的绑定地址（形如 0.0.0.0:7777 / 127.0.0.1:7777）
//
// 返回：
//   - 通配（0.0.0.0 / :: / 空 host）→ 第一条可广告 IP + listen 的端口
//   - 一条都探不到 → `<本机IP>:<port>`
//   - 具体 host（含 127.0.0.1）原样保留，只规范化 host:port
//
// 注意：
//   - 解析不出端口时默认 7777，与出厂 listen 一致
//   - 决策打到 slog，不要写进 init 的 stdout（那是给人抄的 yaml）
func advertiseAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		port = "7777"
		host = listen
	}
	switch host {
	case "0.0.0.0", "::", "":
		ips := listAdvertiseAddrs()
		if len(ips) == 0 {
			fallback := "<本机IP>:" + port
			slog.Warn("没有可广告的网卡地址，配对 addr 退回占位符", "listen", listen, "addr", fallback)
			return fallback
		}
		addr := net.JoinHostPort(ips[0].String(), port)
		slog.Info("配对 addr 选用探到的可达 IP", "listen", listen, "addr", addr, "candidates", len(ips))
		return addr
	default:
		return net.JoinHostPort(host, port)
	}
}

// ipFromAddr 从 net.InterfaceAddrs 常见的两种具体类型里取出 IP。
func ipFromAddr(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}
