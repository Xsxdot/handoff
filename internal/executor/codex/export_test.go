// export_test.go —— 包内实现细节的测试缝。
//
// 职责：把 unexported 的构造/替换点暴露给同包外的 _test 包，避免为测试改可见性。
// 边界：仅测试构建时编译（_test.go 后缀），不进生产二进制。
package codex

// WriteServeInfoForTest 暴露 writeServeInfo，供 serve.json 回环测试。
func WriteServeInfoForTest(p *Proc) error { return writeServeInfo(p) }

// SwapTmuxKillForTest 替换 tmux kill 测试缝，返回还原函数。
func SwapTmuxKillForTest(fn func(session string) error) func() {
	old := tmuxKill
	oldHas := tmuxHasSession
	tmuxKill = fn
	tmuxHasSession = func(string) bool { return false } // 配套：让「会话不存在」成立
	return func() { tmuxKill = old; tmuxHasSession = oldHas }
}
