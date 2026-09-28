package bind

// 本文件是核侧会话入口的导出面：绑定面导出的 SessionCookie / SwitchMachine 是
// **稳定的壳契约**（B386 后七函数之一）。生产装配由 adapter.go 的 coreSessions
// 接到 bind.go 的唯一真实 Core（liveCore），不再有 notWiredSessions 占位。
//
// sessionAPI 只保留为测试缝（swapSessions）；它不是跨语言契约，也不是新的生产
// 占位入口。

// sessionAPI 是绑定面对核侧会话入口的窄消费面（duck typing）。
type sessionAPI interface {
	// SessionCookie 返回某台机器已兑换的会话 cookie 值（核侧持有，壳注入 webview）。
	SessionCookie(machine string) (string, error)
	// SwitchMachine 清掉上一台机器的会话并为目标机器重兑换，返回目标 loopback 源。
	SwitchMachine(machine string) (string, error)
}

// sessions 是会话入口的装配点：默认接 liveCore（与配对面同一实例），
// 测试用 swapSessions 换替身。生产路径不得再出现占位实现或第二个 Core。
var sessions sessionAPI = newCoreSessions(liveCore)

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
