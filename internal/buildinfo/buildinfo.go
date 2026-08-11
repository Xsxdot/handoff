// Package buildinfo 读取当前二进制的构建标识（VCS revision、构建时刻、是否带
// 未提交改动）。
//
// 职责：
//   - Read：把 runtime/debug.ReadBuildInfo 的结果与 ldflags 注入的 release
//     版本号归一成 proto.BuildInfo
//
// 边界：
//   - 不做版本比较，也不做展示——那是 cmd/status.go 的事
//   - 不打日志：本包无 I/O、无外部调用，在这种纯取值函数里打日志只会制造噪音；
//     版本读取结果由调用方（status 聚合与 CLI 渲染）记录
//   - 单开一个包而不是塞进 cmd 或 agentd：CLI 与 agentd 都要读自己的构建标识，
//     放任何一边都会造成反向依赖
package buildinfo

import (
	"runtime/debug"

	"github.com/xushixin/handoff/internal/proto"
)

// readBuildInfo 是 debug.ReadBuildInfo 的测试缝（与各 adapter 的进程启动缝
// 同手法）。
//
// why（必须可注入）：go test 编出的测试二进制**不带 vcs 戳**——Settings 里只有
// -buildmode / GOARCH / CGO_* 之类，没有 vcs.revision。真实调用在测试里恒返回
// 空 revision，断言无从写起。
var readBuildInfo = debug.ReadBuildInfo

// releaseVersion 是构建时由 ldflags 注入的 release 版本号（形如 v0.1.0）。
//
// 注入方式（见 .github/workflows/release.yml，路径必须逐字一致）：
//
//	-ldflags "-X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=v0.1.0"
//
// why（注入而不是运行时读 tag）：二进制跑起来时身边没有 git 仓库可读；
// vcs.revision 只有 commit 没有版本，而「哪个版本更新」是自动更新唯一
// 能回答的问题。本地 go build 不注入，值为空——调用方据此判定非 release 构建。
var releaseVersion string

// Read 返回当前二进制的构建标识。
//
// 返回：
//   - ok=false：读不到构建信息（极少见，如用非 go 工具链链接的二进制）
//   - Revision 为空：不是 go build 产物（go run / 测试二进制），调用方应显示
//     「版本未知」而不是空字符串
func Read() (proto.BuildInfo, bool) {
	bi, ok := readBuildInfo()
	if !ok {
		// 即使读不到构建信息，注入的版本号仍然有效——它是编译期常量，
		// 与 debug.ReadBuildInfo 能否读到无关
		return proto.BuildInfo{Version: releaseVersion}, false
	}
	out := proto.BuildInfo{Go: bi.GoVersion, Version: releaseVersion}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			out.Revision = s.Value
		case "vcs.time":
			out.Time = s.Value
		case "vcs.modified":
			// go 把它序列化成字符串 "true"/"false"，不是布尔
			out.Modified = s.Value == "true"
		}
	}
	return out, true
}
