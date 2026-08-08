// 本文件定义 CLI 的退出码语义：把「失败的类别」编码进进程退出码。
//
// 职责：
//   - 提供 exitCodeError 包装，让特定失败带上专属退出码
//   - 提供 ExitCode，供 main 把 Execute 返回的错误换算成退出码
//
// 边界：
//   - 不打印任何东西（错误文本由 cobra 打到 stderr）
//   - 不决定「什么算失败」，只决定「这次失败对外表达成几号」
package cmd

import "errors"

// 退出码约定。
//
// 为什么不全用 1：wait 的无人值守场景（cron/脚本挂在后台等唤醒）拿不到 stderr，
// 只能看退出码。全是 1 的话，「等满了时限」与「token 没同步导致鉴权失败」这两件
// 处置完全不同的事，脚本无从区分——前者该继续等，后者该立刻报警。
// 124 沿用 timeout(1) 的惯例，也与 handoff run 里被杀命令的退出码一致。
const (
	ExitFailure = 1
	ExitTimeout = 124
)

// exitCodeError 是带专属退出码的错误包装。
//
// 错误文本与 Unwrap 链都原样透传，errors.Is/As 与 cobra 的 stderr 打印不受影响。
type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string { return e.err.Error() }
func (e *exitCodeError) Unwrap() error { return e.err }

// ExitCode 把 Execute 返回的错误换算成进程退出码。
//
// 参数：
//   - err: Execute 的返回值（nil 表示成功）
//
// 返回：
//   - 0（成功）、错误自带的专属退出码，或通用失败码 ExitFailure
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return ec.code
	}
	return ExitFailure
}
