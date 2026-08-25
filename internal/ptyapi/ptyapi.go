// Package ptyapi 是 d_sessions（终端 PTY 域）的入站薄门面（B156.3 §7.0）：
// attach 入口所需的会话存在性与快照信息。打开/写入/订阅终端仍走既有
// gateway 直连通道，本包不重复其语义。
//
// 命名警示（spec §7.0）：d_sessions 是终端 PTY 域；B156.2 的会话子系统是协作
// 房间域；keystone 管的是协调者会话的拉起与唤醒。三者同名不同物。
package ptyapi

import "github.com/Xsxdot/handoff/internal/ptyhost"

// SessionInfo 是 attach 定位需要的会话快照。Dir 是会话的基准目录
// （ptyhost.Session.BasePath 的门面投影）。
type SessionInfo struct {
	ID  string `json:"id"`
	Dir string `json:"dir,omitempty"`
}

// Host 包装既有 *ptyhost.Host，收窄成薄门面。
type Host struct {
	h *ptyhost.Host
}

// New 包装一个已构造的 PTY 宿主。
func New(h *ptyhost.Host) *Host { return &Host{h: h} }

// Has 报告会话是否存活可附着（直通镜像：ptyhost.Host.Get 的存在性半边）。
func (p *Host) Has(id string) bool {
	_, ok := p.h.Get(id)
	return ok
}

// Snapshot 读会话快照（直通镜像：ptyhost.Host.Get）。不存在返回 ok=false。
func (p *Host) Snapshot(id string) (SessionInfo, bool) {
	sess, ok := p.h.Get(id)
	if !ok {
		return SessionInfo{}, false
	}
	return SessionInfo{ID: sess.ID, Dir: sess.BasePath}, true
}
