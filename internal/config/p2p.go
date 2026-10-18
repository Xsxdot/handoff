// 职责：顶层 p2p 配置块（B438：relay 形态 target 的 NAT 打洞）——开关、STUN
// 列表的三态语义与缺省值、取值校验、代理绕行提示判据。
// 边界：缺省 STUN 列表只在本文件定义（p2p/relay/targetclient/mobilecore 代码零
// STUN 字面量）；只做词法校验，不 import pion（否则 pion 进 desktop 依赖图）。
package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// P2PConfig 是 relay 形态 target 的打洞配置。直连形态（addr）不受影响。
//
// 两个字段都是指针，区分「没写」与「显式值」（PlatformInvariants 同款）：
//   - Enabled nil → 开启；显式 false → 只走 relay，listener 拒绝应答打洞
//   - STUN nil → 内置缺省列表 DefaultP2PSTUN()；显式 [] → 零 STUN、只 host 候选
//
// **不要**在 Load 里填缺省值（EnvForward 教训）：填了 Save 就会落盘 p2p 键，
// 而旧版 agentd 以 KnownFields 严格解析，读到它即启动失败。普通 []string 加
// omitempty 会让 Save 吞掉显式 []，变回缺省列表——那是隐私退化，故用 *[]string。
type P2PConfig struct {
	Enabled *bool     `yaml:"enabled,omitempty"`
	STUN    *[]string `yaml:"stun,omitempty"`
}

// defaultP2PSTUN 是缺省 STUN 列表：国内外各有可达者（国内网络对 Google 不可靠）。
// 使用公网 STUN 会向其运营方暴露本机出口 IP，见 PRIVACY.md。
var defaultP2PSTUN = []string{
	"stun:stun.miwifi.com:3478",
	"stun:stun.cloudflare.com:3478",
	"stun:stun.l.google.com:19302",
}

// DefaultP2PSTUN 返回缺省 STUN 列表的副本。没有配置文件的消费者（移动端）也取它。
func DefaultP2PSTUN() []string { return append([]string(nil), defaultP2PSTUN...) }

// P2PProxyNoticeMessage 是「配了 proxy 且 p2p 开着」时进程启动打一次的 INFO 文案
// （spec S8）。agentd 启动与 CLI 构造 relay 形态 Dialer 两处共用。
const P2PProxyNoticeMessage = "p2p 打洞流量不经代理，直接从本机出口发出；需全程走代理请设 p2p.enabled: false"

// P2PEnabled 报告是否尝试打洞。nil Config 或未配置时为 true。
func (c *Config) P2PEnabled() bool {
	if c == nil || c.P2P.Enabled == nil {
		return true
	}
	return *c.P2P.Enabled
}

// P2PSTUN 返回生效的 STUN 列表：未配置 → 缺省列表副本；显式配置 → 原样（可为空切片）。
func (c *Config) P2PSTUN() []string {
	if c == nil || c.P2P.STUN == nil {
		return DefaultP2PSTUN()
	}
	return append([]string{}, (*c.P2P.STUN)...)
}

// P2PProxyNotice 报告是否应打代理绕行提示：proxy 已配且打洞开着。
// 打洞是 UDP，proxy 设置与 HTTPS_PROXY 都不承载它（spec 代理语义）。
func (c *Config) P2PProxyNotice() bool {
	return c != nil && c.Proxy != "" && c.P2PEnabled()
}

// validateP2P 做 stun 列表的词法校验：每项须为 stun:host:port，端口为 1–65535。
// 拒 turn:/stuns:（本期不做 TURN 与 STUN over TLS）与空串。
func (c *Config) validateP2P() error {
	if c.P2P.STUN == nil {
		return nil
	}
	for i, raw := range *c.P2P.STUN {
		if err := validateSTUNURL(raw); err != nil {
			return fmt.Errorf("p2p.stun[%d] %q 非法: %w（形如 stun:stun.example.com:3478）", i, raw, err)
		}
	}
	return nil
}

func validateSTUNURL(raw string) error {
	hostport, ok := strings.CutPrefix(raw, "stun:")
	if !ok {
		return fmt.Errorf("须以 stun: 开头")
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return fmt.Errorf("缺 host:port: %w", err)
	}
	if host == "" {
		return fmt.Errorf("host 为空")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("端口非法 %q", port)
	}
	return nil
}
