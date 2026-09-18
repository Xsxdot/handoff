package bind

import "errors"

// 本文件是 P4=A 要求的核侧会话入口适配层：绑定面导出的 SessionCookie /
// SwitchMachine 是**稳定的壳契约**；核侧实际方法名以 S2（B369.2）落地为准，
// S2 到位后只改本文件的 sessions 装配，不动导出面。
//
// 为什么用 interface 而非直接调 *mobilecore.Core：S2 尚未落地，S4 不发明 S2 的
// 冻结签名；接口让绑定层可编译、可测，且把「名字对齐」收敛到一处。

// sessionAPI 是绑定面对核侧会话入口的窄消费面（duck typing）。
type sessionAPI interface {
	// SessionCookie 返回某台机器已兑换的会话 cookie 值（核侧持有，壳注入 webview）。
	SessionCookie(machine string) (string, error)
	// SwitchMachine 清掉上一台机器的会话并为目标机器重兑换，返回目标 loopback 源。
	SwitchMachine(machine string) (string, error)
}

// errSessionsNotWired 是 S2 未落地时的 fail-closed 结果：绝不静默成功。
var errSessionsNotWired = errors.New("核侧会话入口未接线（S2 未落地）")

// notWiredSessions 在 S2 落地前占住 sessionAPI 的生产接线点。
type notWiredSessions struct{}

func (notWiredSessions) SessionCookie(string) (string, error) { return "", nil }
func (notWiredSessions) SwitchMachine(string) (string, error) { return "", errSessionsNotWired }

// sessions 是核侧会话入口的装配点；S2 落地后在此接 *mobilecore.Core 的适配器。
var sessions sessionAPI = notWiredSessions{}

// SessionCookie 返回某台机器当前会话 cookie 的值，供壳注入 webview cookie jar。
// 空串 + error 表示未配对或兑换失败；**绝不返回 token**。
func SessionCookie(machine string) (string, error) {
	value, err := sessions.SessionCookie(machine)
	if err != nil {
		log.Error("绑定面取会话 cookie 失败", "machine", machine, "cause", err)
		return "", err
	}
	// 凭据卫生：只记长度，不记值。
	log.Debug("绑定面取会话 cookie 完成", "machine", machine, "value_len", len(value))
	return value, nil
}

// SwitchMachine 切机：核侧清掉上一台机器的会话并为目标机器重兑换，返回目标
// loopback 源。壳拿到返回后须清空 webview cookie jar 再注入 SessionCookie 的值。
func SwitchMachine(machine string) (string, error) {
	log.Info("绑定面切机", "machine", machine)
	origin, err := sessions.SwitchMachine(machine)
	if err != nil {
		log.Error("绑定面切机失败", "machine", machine, "cause", err)
		return "", err
	}
	log.Info("绑定面切机完成", "machine", machine, "origin", origin)
	return origin, nil
}
