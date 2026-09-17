// 会话身份的统一记法（B358.9）：人 user:<name>、主 agent agent:<name>。
//
// 身份语义（spec 冻结决定 1/3/4）：权力（写权限、成员名单、@ 路由）只认固定
// 人名；端戳只做落款。解析失败 fail-closed（决定 8），禁止旧脸回落。
//
// 本文件是统一记法的唯一定义处；web:<host> / cli:<user>@<host> 是旧传输层临时
// 脸，不属本词表。协调者席位 cli:<cli>#<session_id> 由 seat.go 定义——席位是
// 「卡当前席位」的协调者身份，与人与主 agent 的成员身份平行，不入本记法。
package proto

import (
	"fmt"
	"strings"
	"unicode"
)

// 成员身份种类词表（统一记法前缀）。
const (
	IdentityKindUser  = "user"
	IdentityKindAgent = "agent"
)

// identitySep 是 kinds 与名字之间的唯一分隔符。
const identitySep = ":"

// IdentityResp 是控制台身份读缝（GET /api/identity）的响应。
//
// 三个键恒出（无 omitempty）：前端要能区分「值为空」与「键不存在」——
// member 为空可能是 console_user 未配置（configured=false，可行动提示的
// 数据源），也可能后端版本过旧根本没发这个字段。缺键与空串是两回事。
type IdentityResp struct {
	// Member 本请求的人名统一记法 user:<name>；console_user 未配置时为空串。
	Member string `json:"member"`
	// Device 端戳：本次登录会话登记的设备名；主令牌身份（无登录会话）为空串。
	// 端戳缺失不拦门（少的是落款，不是权力——spec 决定 3）。
	Device string `json:"device"`
	// Configured 本机 console_user 是否已配置。false 时控制台应给可行动提示。
	Configured bool `json:"configured"`
}

// MemberIdentity 用统一记法拼一个成员身份：user:<name> / agent:<name>。
//
// kind 只认 IdentityKindUser / IdentityKindAgent；name 非空、无首尾空白、
// 不含分隔符 : 与空白字符。错误信息可行动（写清缺什么）。
func MemberIdentity(kind, name string) (string, error) {
	if kind != IdentityKindUser && kind != IdentityKindAgent {
		return "", fmt.Errorf("成员身份种类非法 %q：只认 user 或 agent", kind)
	}
	if err := validateMemberName(name); err != nil {
		return "", err
	}
	return kind + identitySep + name, nil
}

// ParseMemberIdentity 解出统一记法的 (kind, name)。空串、非统一记法前缀、
// 残缺名字都返回错误——fail-closed，绝不把残缺身份悄悄降级成别的东西。
func ParseMemberIdentity(raw string) (kind, name string, err error) {
	if raw == "" {
		return "", "", fmt.Errorf("成员身份为空")
	}
	kind, name, ok := strings.Cut(raw, identitySep)
	if !ok {
		return "", "", fmt.Errorf("成员身份格式非法 %q：须形如 user:<名字> 或 agent:<名字>", raw)
	}
	if kind != IdentityKindUser && kind != IdentityKindAgent {
		return "", "", fmt.Errorf("成员身份前缀非法 %q：只认 user: 或 agent:", raw)
	}
	if err := validateMemberName(name); err != nil {
		return "", "", err
	}
	return kind, name, nil
}

// ValidateMemberIdentity 判断 raw 是否是合法统一记法；供「快速失败」预检。
func ValidateMemberIdentity(raw string) bool {
	_, _, err := ParseMemberIdentity(raw)
	return err == nil
}

// validateMemberName 校验名字本体：非空、无首尾空白、不含冒号与空白字符。
// 名字是全系统唯一的「人是谁」锚，宽松会造出两个看起来一样的人。
func validateMemberName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("成员名字不能为空或带首尾空白")
	}
	if strings.ContainsRune(name, ':') {
		return fmt.Errorf("成员名字不能含 %q", identitySep)
	}
	if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return fmt.Errorf("成员名字不能含空白字符")
	}
	return nil
}
