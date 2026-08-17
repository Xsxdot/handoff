// Package embedbin 持有内嵌的 handoff 二进制，并按构建标签在两种形态间切换。
//
// 职责：
//   - 对外提供唯一入口 Available()/Open()，报告并读出内嵌的 handoff 产物。
//   - 通过 Available() 让调用方（如 shell.DecideRelease）判断能否释出内嵌产物。
//
// 边界：
//   - 不负责释出（解压落盘）、不判断版本号高低、不产生二进制——产物由构建链
//     拷进本包目录，本包只负责嵌入与读出。
//   - 两个入口（Available / Open）分别在 embed.go（//go:build embedbin）
//     与 stub.go（//go:build !embedbin）中声明，各自实现自己的形态。
//
// 为什么要两份实现：go:embed 指向不存在的文件是**编译期错误**。若无条件
// embed，任何没有预先构建产物的机器上 `go build ./...` 与 `go test ./...`
// 都会整片失败。而把 18MB 的二进制提交进仓库既荒唐，也会让构建后工作区变脏，
// 与 handoff 自己「dispatch 要求工作区干净」的硬约束冲突。故用构建标签
// embedbin 分开：默认不嵌，release 才嵌。
package embedbin

// Version 是内嵌二进制的版本号，由构建链经 ldflags 注入：
//
//	-X github.com/Xsxdot/handoff/desktop/internal/embedbin.Version=${TAG}
//
// 与 handoff 本体既有的注入路径（internal/buildinfo.releaseVersion，
// 见 release.yml:85）是同一条契约的两端：同一次 release 用同一个 TAG 注入两边。
//
// **默认为空是刻意的**：开发构建下没有注入，此时版本「判不出」，
// 释出决策必须走保守分支（用用户已有的，不覆盖）——见 shell.DecideRelease。
// 注入这一步属 W5b-3，本 plan 只保证空值时行为正确。
var Version string
