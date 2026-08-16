// listenclass.go —— listen 地址的三档归类与 loopback 变体推导（B85）。
//
// 职责：
//   - 把 listen 的 host 归为 loopback / 通配 / 单点三档
//   - 对通配与单点给出 "127.0.0.1:<同端口>" 的 loopback 变体地址
//
// 边界：
//   - 纯函数，不做网络请求、不校验地址可绑性
//   - CLI（cmd/root.go 拨号改写）与 agentd（cmd/agentd.go 辅助监听）共用同一
//     口径——两处一旦发散就会出现「CLI 改写了、agentd 没绑」的连接拒绝，判定
//     必须唯一，这正是本文件存在的理由
//   - 与 cmd/init.go 的 listenKind 语义不同（那是 init 交互的预选口径，端口也
//     参与归类），刻意不合并（spec §3.1）
package config

import "net"

// ListenClass 是 listen 地址的三档归类。
type ListenClass int

const (
	// ListenLoopback：host 已是回环（127.x/::1/localhost），或 listen 解析失败——
	// 错的 listen 让 net.Listen 自己去报，归类函数不抢这个错误。
	ListenLoopback ListenClass = iota
	// ListenWildcard：通配（0.0.0.0/::/空 host），监听面已含 loopback。
	ListenWildcard
	// ListenSingle：单网卡 IP 或主机名——需要辅助监听的档位。
	ListenSingle
)

// ClassifyListen 把 listen 的 host 归为三档，并推导 loopback 变体地址。
//
// 参数：
//   - listen: 形如 "host:port" 的监听地址
//
// 返回：
//   - cls: 三档归类；解析失败归 ListenLoopback（即调用方什么都不做）
//   - loopback: 通配/单点档为 "127.0.0.1:<同端口>"；loopback 档（含解析失败）
//     原样返回 listen，调用方可无条件使用返回值
func ClassifyListen(listen string) (cls ListenClass, loopback string) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ListenLoopback, listen
	}
	if host == "" {
		return ListenWildcard, net.JoinHostPort("127.0.0.1", port)
	}
	// localhost 不是 IP 字面量，ParseIP 会失败落进单点档，必须先特判
	if host == "localhost" {
		return ListenLoopback, listen
	}
	ip := net.ParseIP(host)
	switch {
	case ip == nil:
		// 主机名：解析结果不可预知，按单点对待（辅助监听兜底本机可用性）
		return ListenSingle, net.JoinHostPort("127.0.0.1", port)
	case ip.IsLoopback():
		return ListenLoopback, listen
	case ip.IsUnspecified():
		return ListenWildcard, net.JoinHostPort("127.0.0.1", port)
	default:
		return ListenSingle, net.JoinHostPort("127.0.0.1", port)
	}
}
