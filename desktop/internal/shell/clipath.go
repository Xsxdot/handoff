// 本文件回答一个问题：这台机器上，handoff CLI 该装在哪、又该去哪找。
//
// 职责：
//   - DefaultCLIPath 给出本平台约定的 CLI 绝对路径，供「释出落点」与
//     「查找候选」两处共用同一个答案。
//
// 边界：
//   - **只算路径，不碰盘**。不判断存在性、不创建目录、不写文件——存在性校验
//     在 binpath.go 的 resolveOne，落盘在 release.go 的 ReleaseBinary。
//   - 不做「找一个能用的 handoff」这件事，那是 ResolveBinPath 的职责；本文件
//     只提供它的第一个候选。
//
// 为什么单独成文件：这个落点是**跨平台契约**，必须与安装脚本逐字对齐——
// Unix 侧 install.sh 装到 ~/.local/bin/handoff，Windows 侧 install.ps1 的
// Get-HandoffInstallDir 装到 %LOCALAPPDATA%\Programs\handoff。两边对不上的
// 后果不是报错，是「桌面端用的 handoff 和命令行敲的是两个版本」，属最难
// 排查的一类错配。契约集中在一处，改的时候才有唯一的地方可改。
package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultCLIPath 返回本平台约定的 handoff CLI 绝对路径。
//
// 返回：
//   - 绝对路径。Windows 为 %LOCALAPPDATA%\Programs\handoff\handoff.exe，
//     其余平台为 ~/.local/bin/handoff
//   - error：路径的两个来源（用户主目录、LOCALAPPDATA）都取不到时报错。
//     **绝不返回半截路径或相对路径**——调用方会拿它当释出落点直接写盘，
//     相对路径会把 CLI 写进进程的当前工作目录。
//
// 注意：
//   - **不保证该路径存在**，也不创建它的父目录。调用方要么把它交给
//     ReleaseBinary（对已存在的目标一律报错，绝不覆盖），要么当候选交给
//     resolveOne 做存在性校验。
func DefaultCLIPath() (string, error) {
	// UserHomeDir 失败在 Windows 上不是终局：LOCALAPPDATA 还在的话照样算得出。
	// 所以这里不提前返回，把「两个来源都没有」的判断统一交给 cliPathFor。
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Warn("取不到用户主目录，改判 LOCALAPPDATA", "cause", err)
	}
	path, err := cliPathFor(runtime.GOOS, home, os.Getenv("LOCALAPPDATA"))
	if err != nil {
		logger.Error("算不出 handoff CLI 的约定落点", "goos", runtime.GOOS, "cause", err)
		return "", err
	}
	logger.Debug("handoff CLI 约定落点", "goos", runtime.GOOS, "path", path)
	return path, nil
}

// cliPathFor 是 DefaultCLIPath 的纯函数内核。
//
// 参数：
//   - goos: 目标平台，取值同 runtime.GOOS
//   - home: 用户主目录，取不到时传空串
//   - localAppData: Windows 的 %LOCALAPPDATA%，非 Windows 或未设置时传空串
//
// 返回：
//   - 拼好的绝对路径
//   - error：该平台所需的来源全部为空
//
// 注意：
//   - 拆成纯函数是为了让三平台的落点能在**任意宿主**上穷举测试。让它读
//     runtime.GOOS 与环境变量的话，Windows 分支就只能在 Windows 上验，
//     而那正是这个仓库反复栽跟头的地方。
func cliPathFor(goos, home, localAppData string) (string, error) {
	if goos == "windows" {
		base := localAppData
		if base == "" {
			if home == "" {
				return "", errors.New("Windows 上算不出 CLI 落点：LOCALAPPDATA 未设置且取不到用户主目录")
			}
			// %LOCALAPPDATA% 缺失时按 Windows 的固定布局从 home 推。
			// 精简过的服务账户环境变量表里确实会缺这一项，直接失败等于
			// 让薄壳在那种机器上永远释出不了 CLI。
			base = filepath.Join(home, "AppData", "Local")
		}
		// Programs\handoff 与 install.ps1 的 Get-HandoffInstallDir 逐字一致，
		// 文件名带 .exe：Windows 上没有扩展名的文件既不能双击也不能被
		// CreateProcess 拉起，而这个错误要到 agentd 托管失败时才显形。
		return filepath.Join(base, "Programs", "handoff", "handoff.exe"), nil
	}
	if home == "" {
		return "", fmt.Errorf("%s 上算不出 CLI 落点：取不到用户主目录", goos)
	}
	// 与 install.sh 同一路径。localAppData 在这里被**刻意忽略**：某些环境
	// （Wine、部分 CI 镜像）会在 Unix 上也设这个变量，跟着它走会把 CLI
	// 释出到一个谁都找不到的地方。
	return filepath.Join(home, ".local", "bin", "handoff"), nil
}
