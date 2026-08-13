// 给本包的测试提供「假装 claude 已安装」的公共替身。
//
// 为什么需要它：StartProc 第一步就 LookPath("claude")，查不到直接返回。开发机上
// 通常装着 Claude Code CLI，于是用例永远走得下去；**CI runner 上没有**，用例会在
// 这一步提前返回，而它们断言的是更靠后的行为（proc.json 写前置时序、超时后回收
// shim），结果表现成与真实缺陷一模一样的假失败。
//
// 边界：只替换路径解析，不替换进程拉起（那是 startProcHost 的活）。
package claudecode

import "testing"

// stubClaudeLookup 把 claude 的路径解析钉成一个固定值，并在用例结束时还原。
//
// 参数：t 用于登记 Cleanup
//
// 注意：返回的路径不需要真实存在——所有会真的去执行它的用例都同时替换了
// startProcHost，进程根本不会被拉起来。
func stubClaudeLookup(t *testing.T) {
	t.Helper()
	orig := lookClaudePath
	lookClaudePath = func() (string, error) { return "/usr/bin/false", nil }
	t.Cleanup(func() { lookClaudePath = orig })
}
