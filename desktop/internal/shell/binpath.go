// 本文件负责把 agentd 二进制解析成一个 launchd/systemd 能吃下的绝对路径。
//
// 背景：launchd/systemd 都要求 ProgramArguments 里是绝对路径。BinPath 给相对名
// 等于装了一个永远起不来的 service，而且症状只在用户机器上出现——所以这里
// 在任何路径上都不得回退成相对路径。
//
// 边界：只做路径解析与存在性校验，**不启动进程、不写配置**。
package shell

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

// ResolveBinPath 把用户显式指定的路径或默认候选解析成绝对路径。
//
// 参数：
//   - explicit: 调用方显式指定的路径。非空时用它，空时依次尝试
//     ~/.local/bin/handoff 和 PATH 上的 handoff
//
// 返回：
//   - 解过符号链接的绝对路径
//   - error：目标不存在、或为空时所有候选都找不到。**错误必定带上被查找的路径**，
//     空 explicit 时还要列出全部尝试过的候选，方便用户决定装到哪或修 PATH
//
// 注意：
//   - **绝不返回相对路径**。唯一例外是空 explicit 且所有候选都失败时返回错误，
//     让调用方向上报错，而不是悄悄退回一个 launchd 解析不了的名字
func ResolveBinPath(explicit string) (string, error) {
	candidates := make([]string, 0, 3)
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		if home, err := os.UserHomeDir(); err != nil {
			slog.Warn("取不到用户主目录，跳过 ~/.local/bin/handoff 候选", "cause", err)
		} else {
			candidates = append(candidates, filepath.Join(home, ".local", "bin", "handoff"))
		}
		// LookPath 的返回值可能是相对路径（PATH 里的相对项），需要留给下面的
		// 绝对路径化去收口
		if p, err := exec.LookPath("handoff"); err == nil {
			candidates = append(candidates, p)
		} else {
			slog.Debug("PATH 上找不到 handoff", "cause", err)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("找不到 handoff 二进制，尝试过的路径：无")
	}
	var tried []string
	for _, c := range candidates {
		tried = append(tried, c)
		abs, err := resolveOne(c)
		if err != nil {
			// 继续试下一个候选；所有候选都失败时在下面统一报错
			continue
		}
		slog.Debug("已解析 agentd 二进制绝对路径", "bin", abs)
		return abs, nil
	}
	return "", fmt.Errorf("找不到可用的 handoff 二进制，尝试过：%s", tried)
}

// resolveOne 校验单个候选存在且是常规文件（或指向常规文件的软链），
// 解开符号链接后返回绝对路径。
func resolveOne(candidate string) (string, error) {
	// Stat 跟随符号链接：软链指向常规文件时 mode 也是常规文件。
	// 目录（例如用户把路径写成了 ~/.local/bin）必须在第一步就挡掉
	st, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("目标不存在 %s", candidate)
		}
		return "", fmt.Errorf("检查 %s: %w", candidate, err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("不是常规文件 %s", candidate)
	}
	// 必须解软链：~/.local/bin/handoff 常是指向别处的软链，把软链原样写进
	// plist 后用户一改链接 agentd 就起不来
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("解析符号链接 %s: %w", candidate, err)
	}
	return filepath.Abs(real)
}
