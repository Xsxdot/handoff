// 移动端 App 的连接核绑定模块（B369）。独立嵌套 Go module，经 replace 指回主模块；
// 与 desktop/ 同构：内部协议逻辑零重实现，只把 internal/mobilecore 经 gomobile
// 编成 Android AAR / iOS XCFramework。反向依赖禁止（根模块不得 import 本模块）。
module github.com/Xsxdot/handoff/mobile

go 1.26.1

require github.com/Xsxdot/handoff v0.0.0-00010101000000-000000000000

require (
	github.com/coder/websocket v1.8.15 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/flynn/noise v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/mobile v0.0.0-20260908204917-8b95e45f8d3e // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.56.0 // indirect
)

replace github.com/Xsxdot/handoff => ../

tool golang.org/x/mobile/cmd/gobind
