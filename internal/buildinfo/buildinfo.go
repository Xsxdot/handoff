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
	"regexp"
	"runtime"
	"runtime/debug"

	"github.com/Xsxdot/handoff/internal/proto"
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
//	-ldflags "-X github.com/Xsxdot/handoff/internal/buildinfo.releaseVersion=v0.1.0"
//
// why（注入而不是运行时读 tag）：二进制跑起来时身边没有 git 仓库可读；
// vcs.revision 只有 commit 没有版本，而「哪个版本更新」是自动更新唯一
// 能回答的问题。本地 go build 不注入，值为空——此时退回模块版本（见
// moduleReleaseVersion），两者都拿不到才判定为非 release 构建。
var releaseVersion string

// releaseTagRe 匹配 release tag 形态的版本号（vX.Y.Z，三段皆为数字）。
//
// why（必须卡这么死）：这是「模块版本能不能当 release 版本号用」的唯一判据，
// 放宽任何一档都会造成真实回归——见 moduleReleaseVersion 的注释。
var releaseTagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// moduleReleaseVersion 从构建信息里取模块版本，仅当它是 release tag 形态时返回，
// 否则返回空串。
//
// why（为什么需要这条回落）：`go install github.com/Xsxdot/handoff@v0.2.3` 是
// 一条对外承诺的安装路径，而它永远不经过 release.yml 的 -X 注入。没有回落的话，
// 这样装的二进制版本恒为 unknown，升级巡检会永远劝它「需要升级」。
//
// why（为什么只认 vX.Y.Z）：Main.Version 还有另外两种取值，认了就会出错——
//   - 仓库内 go build 得到 "(devel)"：认了它，status 会拿 "(devel)" 顶掉
//     revision 展示，而本地构建恰恰只有 revision 有排障价值
//   - `@main` 之类得到 v0.0.0-<时间>-<sha> 伪版本：它在语义上恒小于任何真实
//     tag，认了就等于把开发版说成「比最新 release 旧的 release」，
//     巡检会据此劝人升级到一个他本来就领先的版本
func moduleReleaseVersion(bi *debug.BuildInfo) string {
	if bi == nil || !releaseTagRe.MatchString(bi.Main.Version) {
		return ""
	}
	return bi.Main.Version
}

// Read 返回当前二进制的构建标识。
//
// 返回：
//   - ok=false：读不到构建信息（极少见，如用非 go 工具链链接的二进制）
//   - Revision 为空：不是 go build 产物（go run / 测试二进制），调用方应显示
//     「版本未知」而不是空字符串
func Read() (proto.BuildInfo, bool) {
	// 平台是编译期确定的，与能否读到 debug.BuildInfo 无关——两条返回路径
	// 都必须带上它，漏一条就会让「非 go build 产物」的 agentd 报空平台，
	// 而空平台在远程升级里的语义是「对端过旧，拒绝」，等于一个填漏变成假拒绝
	platform := runtime.GOOS + "/" + runtime.GOARCH
	bi, ok := readBuildInfo()
	if !ok {
		// 即使读不到构建信息，注入的版本号仍然有效——它是编译期常量，
		// 与 debug.ReadBuildInfo 能否读到无关
		return proto.BuildInfo{Version: releaseVersion, Platform: platform}, false
	}
	// 注入值优先：两者同时存在时（打了 tag 的 release 在模块内构建），注入的
	// 才是这次发布的真身
	version := releaseVersion
	if version == "" {
		version = moduleReleaseVersion(bi)
	}
	out := proto.BuildInfo{Go: bi.GoVersion, Version: version, Platform: platform}
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
