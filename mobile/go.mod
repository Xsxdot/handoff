// 移动端 App 的连接核绑定模块（B369）。独立嵌套 Go module，经 replace 指回主模块；
// 与 desktop/ 同构：内部协议逻辑零重实现，只把 internal/mobilecore 经 gomobile
// 编成 Android AAR / iOS XCFramework。反向依赖禁止（根模块不得 import 本模块）。
module github.com/Xsxdot/handoff/mobile

go 1.26.1

require github.com/Xsxdot/handoff v0.0.0-00010101000000-000000000000

require (
	github.com/coder/websocket v1.8.15 // indirect
	github.com/flynn/noise v1.1.0 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/Xsxdot/handoff => ../
