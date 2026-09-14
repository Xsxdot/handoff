// Package bind 是移动端 App 的 gomobile 绑定面（B369）。
//
// 职责：把 internal/mobilecore 的连接核能力收敛成 gomobile 可导出的最小面
// （Relay 形态/直连形态的拨号 + 回环反代 + 配对载体解析），编为 Android AAR /
// iOS XCFramework。壳（Kotlin/Swift）只经这里调用，协议零重实现。
//
// 边界：本包不含任何协议逻辑；一切拨号/选路/存取复用主模块 internal/mobilecore
// （其内复用 internal/relay 与 internal/client）。导出面最小 = 绑定面积最小。
//
// 构建（工具链 gate 见 b369 contract：go 1.26.1 + gomobile@2026-09-08 +
// Xcode 26.6 + NDK 30，Android 须显式 -androidapi 21）：
//
//	gomobile bind -target=android -androidapi 21 -o mobile.aar ./bind
//	gomobile bind -target=ios -o mobile.xcframework ./bind
package bind

import (
	"context"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/mobilecore"
)

var core = mobilecore.New(nil, slog.Default())

// Pair 解析一份配对 bundle 载荷并登记其中的机器，返回各机器的 webview 源。
// 离线机器标记 online=false（部分 bundle，不整单失败）。
func Pair(bundleJSON string) (mobilecore.PairResult, error) {
	return core.Pair(context.Background(), bundleJSON)
}

// Origin 返回某台机器 webview 应加载的 loopback 源（形如 http://127.0.0.1:<port>/）。
func Origin(machine string) (string, error) {
	return core.Origin(machine)
}

// MachineNames 返回已登记机器名（设置页配对清单）。
func MachineNames() []string {
	return core.MachineNames()
}

// Close 收掉全部回环反代与客户端。
func Close() error {
	return core.Close()
}
